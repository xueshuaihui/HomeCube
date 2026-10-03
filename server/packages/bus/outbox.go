package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// OutboxStatus represents the delivery status of an outbox message.
//
// The state machine has exactly two states because that is all the shipped DDL allows:
// migrations/homeos/homeos_0002_outbox_dedupe_dead_letter.up.sql:25 declares
// `status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent'))`, which is the direct
// translation of tech plan §3.3「INSERT {code}_outbox(subject, envelope, status=pending)」+「成功置
// sent」. There is deliberately no failed status: §3.3's failure rule is「失败 attempts+1」+
// 「attempts>10 告警（不丢，只是没送）」and 0002's comment states the consequence explicitly
// （「超限仍留在 pending，不另设失败态」). Writing any other value is a CHECK violation.
type OutboxStatus string

const (
	OutboxStatusPending OutboxStatus = "pending"
	OutboxStatusSent    OutboxStatus = "sent"
)

// OutboxMessage is the row shape of {code}_outbox, field-for-field the column set 0002 declares:
// (id, family_id, subject, envelope, status, attempts, created_at).
//
// This struct used to carry updated_at, sent_at and error. None of those exist in the DDL
// (0002:11-31 lists seven columns; its comment on created_at records that「投递成功时刻（sent_at）
// 文档未给列」), so every INSERT through this struct failed and every deliverer UPDATE failed with
// SQLSTATE 42703 — the row stayed pending forever and the deliverer republished it every 500ms.
// Anything the deliverer needs to know about a delivery is either in JetStream or derivable from
// attempts/created_at; it is not stored in invented columns.
type OutboxMessage struct {
	ID        int64        `gorm:"primaryKey;autoIncrement"`
	FamilyID  *string      `gorm:"column:family_id"`
	Subject   string       `gorm:"not null"`
	Envelope  string       `gorm:"type:jsonb;not null"`
	Status    OutboxStatus `gorm:"not null;default:'pending'"`
	Attempts  int          `gorm:"not null;default:0"`
	CreatedAt time.Time    `gorm:"column:created_at;not null"`
}

// OutboxTableName is the single source of the outbox table name: 0002 names the table
// {code}_outbox (e.g. homeos_outbox), and the prefix is the service code, not a schema qualifier.
//
// OutboxMessage deliberately has no TableName() method: GORM cannot reach the code from a model
// method, and the placeholder it used to return ("outbox") silently resolved to an unprefixed table
// for any caller that forgot db.Table(...). Every read and write in this package passes
// OutboxTableName(cfg.Code) / OutboxTableName(code) instead, so the name has one definition.
func OutboxTableName(code string) string {
	return code + "_outbox"
}

// OutboxConfig holds configuration for the outbox deliverer.
type OutboxConfig struct {
	// BatchSize is the number of messages to process in each batch.
	// Default: 100 per tech plan §3.3.
	BatchSize int

	// Interval is how often to check for pending messages.
	// Default: 500ms per tech plan §3.3.
	Interval time.Duration

	// MaxAttempts is the maximum number of delivery attempts before alerting.
	// Default: 10 per tech plan §3.3.
	MaxAttempts int

	// Code is the service code for table naming (e.g., "homeos", "finance").
	Code string
}

// OutboxDeliverer handles asynchronous delivery of outbox messages to JetStream.
// It runs a background goroutine that periodically batches pending messages and
// publishes them to JetStream with async acknowledgment.
type OutboxDeliverer struct {
	db      *gorm.DB
	js      JetStreamWrapper
	cfg     OutboxConfig
	stopCh  chan struct{}
	wg      sync.WaitGroup
	alertFn func(msg string) // Alert function for failed deliveries
}

// NewOutboxDeliverer creates a new outbox deliverer instance.
func NewOutboxDeliverer(db *gorm.DB, js JetStreamWrapper, cfg OutboxConfig, alertFn func(msg string)) *OutboxDeliverer {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.Interval == 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 10
	}

	return &OutboxDeliverer{
		db:      db,
		js:      js,
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		alertFn: alertFn,
	}
}

// Start begins the background delivery loop.
func (d *OutboxDeliverer) Start(ctx context.Context) {
	d.wg.Add(1)
	go d.run(ctx)

	// On startup, scan for any pending messages from previous crashes.
	d.recoverPending(ctx)
}

// Stop gracefully shuts down the deliverer.
func (d *OutboxDeliverer) Stop() {
	close(d.stopCh)
	d.wg.Wait()
}

// run is the main delivery loop.
func (d *OutboxDeliverer) run(ctx context.Context) {
	defer d.wg.Done()

	ticker := time.NewTicker(d.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.deliverBatch(ctx)
		}
	}
}

// recoverPending scans for pending messages on startup (crash recovery).
func (d *OutboxDeliverer) recoverPending(ctx context.Context) {
	var count int64
	tableName := OutboxTableName(d.cfg.Code)
	err := d.db.Table(tableName).Where("status = ?", OutboxStatusPending).Count(&count).Error
	if err != nil {
		if d.alertFn != nil {
			d.alertFn(fmt.Sprintf("failed to scan pending outbox messages: %v", err))
		}
		return
	}

	if count > 0 {
		if d.alertFn != nil {
			d.alertFn(fmt.Sprintf("recovered %d pending outbox messages on startup", count))
		}
		// Immediately attempt delivery of recovered messages.
		d.deliverBatch(ctx)
	}
}

// deliverBatch processes a batch of pending outbox messages.
func (d *OutboxDeliverer) deliverBatch(ctx context.Context) {
	tableName := OutboxTableName(d.cfg.Code)

	// Fetch pending messages in batch.
	var messages []OutboxMessage
	err := d.db.Table(tableName).
		Where("status = ?", OutboxStatusPending).
		Order("created_at ASC").
		Limit(d.cfg.BatchSize).
		Find(&messages).Error

	if err != nil {
		if d.alertFn != nil {
			d.alertFn(fmt.Sprintf("failed to fetch pending outbox messages: %v", err))
		}
		return
	}

	if len(messages) == 0 {
		return
	}

	// Process each message.
	for _, msg := range messages {
		d.processMessage(ctx, tableName, msg)
	}
}

// processMessage attempts to deliver a single outbox message.
//
// Both branches write only columns 0002 declares. §3.3 in full:「每 500ms 批量 100 行 → JetStream
// Publish（异步 ack）→ 成功置 sent，失败 attempts+1」and「attempts>10 告警（不丢，只是没送）」.
// The failure branch therefore touches `attempts` and nothing else: the row stays pending (the CHECK
// has no third state), it is never deleted, and the alert is the only extra effect.
func (d *OutboxDeliverer) processMessage(ctx context.Context, tableName string, msg OutboxMessage) {
	// Attempt to publish to JetStream.
	_, err := d.js.Publish(ctx, msg.Subject, []byte(msg.Envelope))

	if err != nil {
		// Delivery failed: attempts+1, status stays 'pending' (§3.3「不丢，只是没送」).
		// The increment is written as SQL, not as msg.Attempts+1, because more than one deliverer
		// can be pointed at the same table and a read-modify-write would lose increments.
		newAttempts := msg.Attempts + 1

		updateData := map[string]any{
			"attempts": gorm.Expr("attempts + 1"),
		}

		// Alert once the row has used up its budget. The threshold stays the one the deliverer was
		// built with (MaxAttempts, default 10 per §3.3), compared the same way it always was; note
		// §3.3 literally says「attempts>10」so this alerts one attempt earlier than the floor, never
		// later. Reported rather than silently re-tuned here.
		if newAttempts >= d.cfg.MaxAttempts {
			if d.alertFn != nil {
				d.alertFn(fmt.Sprintf(
					"outbox message %d exceeded max attempts (%d/%d), subject: %s",
					msg.ID, newAttempts, d.cfg.MaxAttempts, msg.Subject,
				))
			}
		}

		if err := d.db.Table(tableName).Where("id = ?", msg.ID).Updates(updateData).Error; err != nil {
			if d.alertFn != nil {
				d.alertFn(fmt.Sprintf("failed to update outbox message %d: %v", msg.ID, err))
			}
		}
		return
	}

	// Delivery succeeded: mark as sent. §3.3 gives this transition one column and no timestamp —
	// 0002 has no sent_at column, so there is nothing to stamp.
	if err := d.db.Table(tableName).Where("id = ?", msg.ID).
		Updates(map[string]any{"status": OutboxStatusSent}).Error; err != nil {
		if d.alertFn != nil {
			d.alertFn(fmt.Sprintf("failed to update outbox message %d after successful delivery: %v", msg.ID, err))
		}
	}
}

// InsertOutboxMessage inserts a new message into the outbox table within a transaction.
// This should be called within the same database transaction as the business operation.
//
// family_id is left NULL — §2.2「系统预置数据用可空 family_id 表达」makes that legal for events that
// belong to no family. Producers that have a family call InsertOutboxMessageWithFamily.
func InsertOutboxMessage(tx *gorm.DB, code, subject string, envelopeJSON string) error {
	return InsertOutboxMessageWithFamily(tx, code, "", subject, envelopeJSON)
}

// InsertOutboxMessageWithFamily is InsertOutboxMessage plus the family_id 0002 declares (§10.3
// 「指标最小集（全部带 family_id 与 code）」). An empty familyID inserts NULL, never the empty string
// (the column is uuid on Postgres, where ” is an input syntax error).
func InsertOutboxMessageWithFamily(tx *gorm.DB, code, familyID, subject string, envelopeJSON string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("bus: outbox table name needs a service code, got an empty one")
	}

	msg := OutboxMessage{
		Subject:   subject,
		Envelope:  envelopeJSON,
		Status:    OutboxStatusPending,
		Attempts:  0,
		CreatedAt: time.Now(),
	}
	if familyID != "" {
		msg.FamilyID = &familyID
	}

	if err := tx.Table(OutboxTableName(code)).Create(&msg).Error; err != nil {
		return fmt.Errorf("bus: failed to insert outbox message %q: %w", subject, err)
	}
	return nil
}
