package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConsumerConfig holds configuration for a durable consumer.
type ConsumerConfig struct {
	// Code is the service code (e.g., "homeos", "finance").
	Code string

	// StreamName is the JetStream stream to consume from (e.g., "HC_HOMEOS").
	StreamName string

	// EventType is the event type to filter (e.g., "finance.transaction.created").
	EventType string

	// FilterSubject is the exact subject to match (e.g., "finance.transaction.created").
	FilterSubject string

	// AckWait is how long to wait for acknowledgment before redelivery.
	// Default: 30s per tech plan §3.4.
	AckWait time.Duration

	// MaxDeliver is the maximum number of delivery attempts.
	// Default: 4 per tech plan §3.4 (allows 3 retries).
	MaxDeliver int

	// BackOff defines the retry backoff intervals.
	// Default: [1s, 10s, 60s] per tech plan §3.4.
	BackOff []time.Duration
}

// DedupeRecord is the row shape of {code}_event_dedupe, field-for-field the column set 0002
// declares: (event_type, business_id, created_at).
//
// It used to carry an `ID int64 gorm:"primaryKey;autoIncrement"`. homeos.homeos_event_dedupe has no
// id column — its only key is the documented global unique index on (event_type, business_id)
// (0002:42-54, §3.4). With an auto-increment primary key in the struct, GORM appends
// `RETURNING "id"` to the INSERT (gorm@v1.31.2 callbacks/create.go:52-64 builds RETURNING from
// FieldsWithDefaultDBValue), and Postgres answers 42703 column "id" does not exist — so §3.4's
// dedupe INSERT failed on every delivery. That is why the key here is the documented composite, not
// a surrogate id.
type DedupeRecord struct {
	EventType  string    `gorm:"column:event_type;not null"`
	BusinessID string    `gorm:"column:business_id;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
}

// DeadLetterRecord is the row shape of {code}_dead_letter, field-for-field the column set 0002
// declares: (id, family_id, consumer_code, event_type, envelope, last_error, created_at,
// resolved_at).
//
// The struct used to write `subject` and `attempts`, and to name the failure column `error`. None of
// those are in the DDL: 0002:71 records the judgement「§3.1「subject 命名即事件名」-> 与 subject
// 同义，不重复存 subject」, there is no attempts column (a dead letter is written once, after the
// delivery budget is spent), and the reason is stored in `last_error` (0002:75-76). Inserting through
// the old shape failed with 42703 and the dead letter was then discarded, i.e. the message vanished
// from both the stream and the replay table.
type DeadLetterRecord struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	FamilyID     *string    `gorm:"column:family_id"`
	ConsumerCode string     `gorm:"column:consumer_code;not null"`
	EventType    string     `gorm:"column:event_type;not null"`
	Envelope     string     `gorm:"type:jsonb;not null"`
	LastError    string     `gorm:"column:last_error;type:text"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	ResolvedAt   *time.Time `gorm:"column:resolved_at"`
}

// DedupeTableName is the single source of the dedupe table name. 0002 names it {code}_event_dedupe
// (homeos_event_dedupe / finance_event_dedupe).
//
// The (DedupeRecord).TableName() method that used to sit here returned the unprefixed
// "event_dedupe" — a name no migration ever created — so any caller that relied on the model instead
// of db.Table(...) addressed a table that does not exist. GORM cannot see the service code from a
// model method, so the caller passes it, exactly as the outbox side does.
func DedupeTableName(code string) string {
	return code + "_event_dedupe"
}

// DeadLetterTableName is the single source of the dead-letter table name ({code}_dead_letter per
// 0002), for the same reason as DedupeTableName.
func DeadLetterTableName(code string) string {
	return code + "_dead_letter"
}

// DurableConsumer handles message consumption with deduplication and dead-letter support.
type DurableConsumer struct {
	db      *gorm.DB
	js      JetStreamWrapper
	cfg     ConsumerConfig
	handler Handler
	stopCh  chan struct{}

	// alertFn is where this consumer reports what it cannot fix by itself — today that is a dedupe
	// claim that survived a failed delivery, which swallows the event until someone deletes the row.
	// NewDurableConsumer always installs defaultAlertFn, so a nil sink only happens for a struct built
	// by hand (the unit tests do that and swap in a recorder to assert on the alert).
	alertFn func(msg string)
}

// defaultAlertFn writes to stderr with a prefix a container log can be grepped for. The consumer has
// no logger field and no constructor parameter for one, and inventing either is outside this card.
func defaultAlertFn(msg string) {
	fmt.Fprintf(os.Stderr, "bus ALERT: %s\n", msg)
}

// alert reports through alertFn, tolerating a nil one.
func (c *DurableConsumer) alert(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if c.alertFn != nil {
		c.alertFn(msg)
		return
	}
	defaultAlertFn(msg)
}

// NewDurableConsumer creates a new durable consumer instance.
func NewDurableConsumer(db *gorm.DB, js JetStreamWrapper, cfg ConsumerConfig) *DurableConsumer {
	if cfg.AckWait == 0 {
		cfg.AckWait = 30 * time.Second
	}
	if cfg.MaxDeliver == 0 {
		cfg.MaxDeliver = 4
	}
	if len(cfg.BackOff) == 0 {
		cfg.BackOff = []time.Duration{1 * time.Second, 10 * time.Second, 60 * time.Second}
	}

	return &DurableConsumer{
		db:      db,
		js:      js,
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		alertFn: defaultAlertFn,
	}
}

// Start begins consuming messages with the given handler.
func (c *DurableConsumer) Start(ctx context.Context, handler Handler) error {
	c.handler = handler

	// Build consumer name: {consumerCode}-{event_type}
	consumerName := fmt.Sprintf("%s-%s", c.cfg.Code, c.cfg.EventType)

	// Create or update the durable consumer.
	err := c.js.Subscribe(ctx, c.cfg.StreamName, consumerName, c.cfg.FilterSubject, func(msg jetstream.Msg) {
		c.handleMessage(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe consumer %s: %w", consumerName, err)
	}

	return nil
}

// Stop gracefully shuts down the consumer.
func (c *DurableConsumer) Stop() {
	close(c.stopCh)
}

// handleMessage processes an incoming message with deduplication and error handling.
//
// §3.4 puts two requirements in the same subsection, and they only both hold if a failed delivery
// releases its own claim:
//
//   - 「进 handler 第一件事：INSERT ON CONFLICT DO NOTHING INTO {code}_event_dedupe；已存在即 ack
//     返回——重复投递不产生重复业务对象是表约束保证，不是代码纪律」 -> the claim goes BEFORE the
//     handler, and it is the table's unique index that decides, not this code.
//   - 「max_deliver=4（对应 10.4 的至多重试 3 次）」 -> those 3 retries have to actually reach the
//     handler.
//
// Keeping the row after a failed handler makes the two sentences mutually exclusive: every redelivery
// is answered by the index with 「already claimed」, gets ack'd, and the business effect never happens
// — one failure permanently swallows the event, and §3.4's backoff=[1s,10s,60s] ladder plus the dead
// letter's 「重放即原样重新入队，走同一幂等键」 both stop working. The fix is therefore release-on-
// failure, not moving the claim after the handler (that would delete the first sentence).
func (c *DurableConsumer) handleMessage(ctx context.Context, msg jetstream.Msg) {
	// Parse the envelope.
	var envelope Envelope
	if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
		// A deterministic poison message, and this is where it differs from the handler-failure
		// branch below: a handler error can be transient (the database is unreachable, a constraint
		// race, a downstream timeout) so it earns the retry budget, while bytes that will not parse
		// never start parsing on the fourth try — spending backoff=[1s,10s,60s] on them only delays
		// the inevitable. Dead-letter it now and Term the delivery so the stream stops redelivering.
		// There is also nothing to release here: the envelope never parsed, so checkDedupe was never
		// reached and no claim exists.
		c.writeDeadLetter(ctx, c.cfg.EventType, "", string(msg.Data()), "malformed envelope: "+err.Error())
		msg.Term()
		return
	}

	// First thing: check deduplication. This INSERT is the claim.
	deduped, err := c.checkDedupe(ctx, envelope.EventType, envelope.BusinessID)
	if err != nil {
		// Database error: nack for retry. This delivery did not take a claim (a claim it took would
		// have come back as deduped==true, not as an error), so there is nothing to release — the
		// 底座 cannot reach the table, which is retryable.
		msg.Nak()
		return
	}

	if deduped {
		// Already processed: ack immediately.
		msg.Ack()
		return
	}

	// Call the business handler.
	message := Message{
		Subject:  msg.Subject(),
		Envelope: envelope,
		Headers:  extractHeaders(msg.Headers()),
	}

	err = c.handler(ctx, message)
	if err != nil {
		attempt := c.deliveryAttempt(msg)

		// The claim was taken by this delivery and this delivery failed, so it is this delivery's to
		// give back — before both the Nak and the dead-letter paths, otherwise the retry (and, after
		// the budget is spent, the operator's replay of the dead letter straight back into the stream)
		// is swallowed by the row the failure left behind.
		if relErr := c.releaseDedupe(ctx, envelope.EventType, envelope.BusinessID); relErr != nil {
			// Loudly, never silently: this claim now permanently swallows the event — every
			// redelivery answers ack 「already processed」 while the business effect has never
			// happened, and only deleting the row by hand unblocks it.
			c.alert("dedupe claim (%s, %s) was NOT released after delivery %d/%d failed: %v — "+
				"redeliveries of this event will be ack'd as duplicates until that row is deleted by hand; handler error: %v",
				envelope.EventType, envelope.BusinessID, attempt, c.cfg.MaxDeliver, relErr, err)
		}

		if attempt < c.cfg.MaxDeliver {
			// Still inside the retry budget: nack so §3.4's backoff ladder runs, and write NO dead
			// letter — the dead-letter sentence starts with「超限失败 ->」. Writing it on every failure
			// would park up to MaxDeliver rows for one event, and 人工重放 on each of them would
			// re-enqueue the same event that many times.
			msg.Nak()
			return
		}

		// 超限失败 -> publish 到 dl.{consumerCode}.{event_type} + 写 {code}_dead_letter.
		c.writeDeadLetter(ctx, orConfiguredEventType(c.cfg.EventType, envelope.EventType), envelope.FamilyID, string(msg.Data()), err.Error())
		// The budget is spent, so nack'ing would only ask the stream for another delivery it is
		// already done with: term is what ends this delivery for good.
		msg.Term()
		return
	}

	// Success: ack the message.
	msg.Ack()
}

// deliveryAttempt reports which attempt this delivery is, 1-based (first delivery -> 1).
//
// The card words this as msg.Info().RedeliveryCount + 1; on the pinned nats.go v1.54.0 the delivery
// counter is not on Info() (jetstream.Msg has no Info method and there is no MsgInfo type) but on
// Metadata() (*jetstream.MsgMetadata, error).NumDelivered, which is already 1 on the first delivery.
// Same number, one API name newer.
//
// When the counter cannot be read the attempt is unknown, and the only ending that cannot lose the
// event is the over-budget one: a dead letter keeps the raw envelope for 人工重放, while a Nak on a
// delivery whose metadata is unreadable buys nothing. Alert it, because guessing is not free.
func (c *DurableConsumer) deliveryAttempt(msg jetstream.Msg) int {
	md, err := msg.Metadata()
	if err == nil && md == nil {
		err = fmt.Errorf("metadata returned (nil, nil)")
	}
	if err != nil {
		c.alert("cannot read the delivery count of subject %q (%v), treating the failure as over-budget: %s",
			msg.Subject(), err, "dead letter written, delivery terminated")
		return c.cfg.MaxDeliver
	}
	if md.NumDelivered == 0 {
		return 1
	}
	return int(md.NumDelivered)
}

// orConfiguredEventType keeps event_type NOT NULL: prefer the envelope's own event type, fall back
// to what the consumer was configured for.
func orConfiguredEventType(configured, fromEnvelope string) string {
	if fromEnvelope != "" {
		return fromEnvelope
	}
	return configured
}

// checkDedupe claims the (event_type, business_id) key for this delivery.
//
// §3.4 verbatim:「进 handler 第一件事：INSERT ON CONFLICT DO NOTHING INTO {code}_event_dedupe；已存在
// 即 ack 返回」and「重复投递不产生重复业务对象是表约束保证，不是代码纪律」. So the claim is the
// INSERT itself, guarded by the table's unique index — nothing here reads-then-writes, and the
// answer comes back as rows affected (0 = someone already claimed the key) rather than as a
// judgment this package makes about the business.
//
// Returns true when the event was already processed (deduplicated).
func (c *DurableConsumer) checkDedupe(ctx context.Context, eventType, businessID string) (bool, error) {
	if c.cfg.Code == "" {
		return false, fmt.Errorf("bus: dedupe table name needs a service code, got an empty one")
	}
	if eventType == "" || businessID == "" {
		return false, fmt.Errorf("bus: dedupe key (event_type, business_id) needs both parts, got (%q, %q)", eventType, businessID)
	}

	record := DedupeRecord{
		EventType:  eventType,
		BusinessID: businessID,
		CreatedAt:  time.Now(),
	}

	result := c.db.WithContext(ctx).
		Table(DedupeTableName(c.cfg.Code)).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&record)
	if result.Error != nil {
		// A driver or dialect that reports the collision as an error instead of swallowing it still
		// means the same thing: the key is taken, so this delivery is a duplicate.
		if isUniqueViolation(result.Error) {
			return true, nil
		}
		return false, fmt.Errorf("failed to insert dedupe record: %w", result.Error)
	}

	return result.RowsAffected == 0, nil
}

// releaseDedupe hands back the claim checkDedupe took:
// `DELETE FROM {code}_event_dedupe WHERE event_type = ? AND business_id = ?`.
//
// It exists because §3.4's claim is a claim on 「this (event_type, business_id) is being processed」,
// not on 「it is done」. A delivery that failed has done nothing, and if its row stays the retries the
// same subsection asks for (「至多重试 3 次」) and the dead letter's replay path (「重放即原样重新入队，
// 走同一幂等键」) are both ack'd away by the unique index without ever reaching the handler again.
//
// Only call it on a path where this delivery really did take the claim — checkDedupe returning an
// error, or returning deduped==true, means the row is not ours to delete. And the WHERE is the whole
// key on purpose: a release without (event_type, business_id) would wipe every event's claim and
// trade 「重复投递不产生重复业务对象」 for 「所有事件都重来一遍」.
func (c *DurableConsumer) releaseDedupe(ctx context.Context, eventType, businessID string) error {
	if c.cfg.Code == "" {
		return fmt.Errorf("bus: dedupe table name needs a service code, got an empty one")
	}
	if eventType == "" || businessID == "" {
		return fmt.Errorf("bus: dedupe key (event_type, business_id) needs both parts, got (%q, %q)", eventType, businessID)
	}

	result := c.db.WithContext(ctx).
		Table(DedupeTableName(c.cfg.Code)).
		Where("event_type = ? AND business_id = ?", eventType, businessID).
		Delete(&DedupeRecord{})
	if result.Error != nil {
		return fmt.Errorf("bus: failed to release dedupe claim (%s, %s): %w", eventType, businessID, result.Error)
	}
	return nil
}

// writeDeadLetter writes a failed message to the dead letter table and publishes to DLQ (§3.4
// 「超限失败 -> publish 到 dl.{consumerCode}.{event_type} + 写 {code}_dead_letter」).
func (c *DurableConsumer) writeDeadLetter(ctx context.Context, eventType, familyID, envelopeJSON, errMsg string) {
	if c.cfg.Code == "" {
		// Without a code there is no table to write to and no dl subject to publish; say so loudly
		// instead of addressing a name no migration created.
		fmt.Printf("bus: dead letter dropped, ConsumerConfig.Code is empty (subject event_type=%q): %s\n", eventType, errMsg)
		return
	}

	record := DeadLetterRecord{
		ConsumerCode: c.cfg.Code,
		EventType:    eventType,
		Envelope:     envelopeJSON,
		LastError:    errMsg,
		CreatedAt:    time.Now(),
	}
	if familyID != "" {
		record.FamilyID = &familyID
	}

	if err := c.db.WithContext(ctx).Table(DeadLetterTableName(c.cfg.Code)).Create(&record).Error; err != nil {
		// Log but don't fail - we're already in an error path.
		fmt.Printf("failed to write dead letter: %v\n", err)
	}

	// Publish to dead letter queue subject: dl.{consumerCode}.{event_type}
	dlSubject := fmt.Sprintf("dl.%s.%s", c.cfg.Code, eventType)
	_, _ = c.js.Publish(ctx, dlSubject, []byte(envelopeJSON))
}

// uniqueViolationMarkers are the collision texts this package can be handed, per driver:
//   - Postgres (SQLSTATE 23505): 「duplicate key value violates unique constraint "..."」
//   - the sqlite driver the unit tests run on: 「UNIQUE constraint failed: table.columns」 and its
//     wrapped 「SQLITE_CONSTRAINT_UNIQUE」 code.
var uniqueViolationMarkers = []string{
	"duplicate key",
	"violates unique constraint",
	"unique constraint failed",
	"sqlite_constraint_unique",
	"sqlstate 23505",
}

// isUniqueViolation reports whether err is a unique-constraint collision.
//
// The previous implementation lower-cased by hand with a loop that rebuilt the string from the
// original each time, so it only ever lowered the LAST upper-case rune: 「UNIQUE constraint failed」
// became 「UNIQe constraint failed」 and matched nothing. Every sqlite collision was therefore
// classified as a database error, checkDedupe NAKed, and a duplicate delivery was never recognised
// as one. strings.ToLower does what that loop was trying to do.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range uniqueViolationMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// extractHeaders extracts headers from NATS message headers.
func extractHeaders(headers map[string][]string) map[string]string {
	result := make(map[string]string)
	for k, v := range headers {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}
