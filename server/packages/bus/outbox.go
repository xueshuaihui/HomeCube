package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// OutboxStatus represents the delivery status of an outbox message.
type OutboxStatus string

const (
	OutboxStatusPending OutboxStatus = "pending"
	OutboxStatusSent    OutboxStatus = "sent"
	OutboxStatusFailed  OutboxStatus = "failed"
)

// OutboxMessage represents a row in the {code}_outbox table.
type OutboxMessage struct {
	ID        int64        `gorm:"primaryKey;autoIncrement"`
	Subject   string       `gorm:"not null;index"`
	Envelope  string       `gorm:"type:jsonb;not null"`
	Status    OutboxStatus `gorm:"not null;default:'pending';index"`
	Attempts  int          `gorm:"not null;default:0"`
	CreatedAt time.Time    `gorm:"not null"`
	UpdatedAt time.Time    `gorm:"not null"`
	SentAt    *time.Time   `gorm:"null"`
	Error     string       `gorm:"type:text"`
}

// TableName returns the table name for the outbox messages.
// The actual table name should be prefixed with the service code, e.g., "homeos_outbox".
func (OutboxMessage) TableName() string {
	// This is a placeholder; the actual implementation should use the service code prefix.
	return "outbox"
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
	tableName := fmt.Sprintf("%s_outbox", d.cfg.Code)
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
	tableName := fmt.Sprintf("%s_outbox", d.cfg.Code)

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
func (d *OutboxDeliverer) processMessage(ctx context.Context, tableName string, msg OutboxMessage) {
	// Attempt to publish to JetStream.
	_, err := d.js.Publish(ctx, msg.Subject, []byte(msg.Envelope))

	now := time.Now()

	if err != nil {
		// Delivery failed: increment attempts.
		newAttempts := msg.Attempts + 1

		updateData := map[string]any{
			"attempts":   newAttempts,
			"updated_at": now,
			"error":      err.Error(),
		}

		// Check if we've exceeded max attempts.
		if newAttempts >= d.cfg.MaxAttempts {
			updateData["status"] = OutboxStatusFailed

			// Alert on excessive failures (but don't drop the message).
			if d.alertFn != nil {
				d.alertFn(fmt.Sprintf(
					"outbox message %d exceeded max attempts (%d/%d), subject: %s",
					msg.ID, newAttempts, d.cfg.MaxAttempts, msg.Subject,
				))
			}
		}

		err := d.db.Table(tableName).Where("id = ?", msg.ID).Updates(updateData).Error
		if err != nil && d.alertFn != nil {
			d.alertFn(fmt.Sprintf("failed to update outbox message %d: %v", msg.ID, err))
		}
	} else {
		// Delivery succeeded: mark as sent.
		updateData := map[string]any{
			"status":     OutboxStatusSent,
			"updated_at": now,
			"sent_at":    now,
		}

		err := d.db.Table(tableName).Where("id = ?", msg.ID).Updates(updateData).Error
		if err != nil && d.alertFn != nil {
			d.alertFn(fmt.Sprintf("failed to update outbox message %d after successful delivery: %v", msg.ID, err))
		}
	}
}

// InsertOutboxMessage inserts a new message into the outbox table within a transaction.
// This should be called within the same database transaction as the business operation.
func InsertOutboxMessage(tx *gorm.DB, code, subject string, envelopeJSON string) error {
	tableName := fmt.Sprintf("%s_outbox", code)

	msg := OutboxMessage{
		Subject:   subject,
		Envelope:  envelopeJSON,
		Status:    OutboxStatusPending,
		Attempts:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return tx.Table(tableName).Create(&msg).Error
}
