package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
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

// DedupeRecord represents a row in the {code}_event_dedupe table.
type DedupeRecord struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	EventType  string    `gorm:"not null"`
	BusinessID string    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null"`
}

// TableName returns the table name for deduplication records.
func (DedupeRecord) TableName() string {
	return "event_dedupe"
}

// DeadLetterRecord represents a row in the {code}_dead_letter table.
type DeadLetterRecord struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	ConsumerCode string    `gorm:"not null"`
	EventType    string    `gorm:"not null"`
	Subject      string    `gorm:"not null"`
	Envelope     string    `gorm:"type:jsonb;not null"`
	Error        string    `gorm:"type:text"`
	Attempts     int       `gorm:"not null;default:0"`
	CreatedAt    time.Time `gorm:"not null"`
}

// TableName returns the table name for dead letter records.
func (DeadLetterRecord) TableName() string {
	return "dead_letter"
}

// DurableConsumer handles message consumption with deduplication and dead-letter support.
type DurableConsumer struct {
	db      *gorm.DB
	js      JetStreamWrapper
	cfg     ConsumerConfig
	handler Handler
	stopCh  chan struct{}
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
		db:     db,
		js:     js,
		cfg:    cfg,
		stopCh: make(chan struct{}),
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
func (c *DurableConsumer) handleMessage(ctx context.Context, msg jetstream.Msg) {
	// Parse the envelope.
	var envelope Envelope
	if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
		// Malformed message: nack and move to dead letter.
		c.writeDeadLetter(ctx, msg.Subject(), string(msg.Data()), "malformed envelope: "+err.Error())
		msg.Nak()
		return
	}

	// First thing: check deduplication.
	deduped, err := c.checkDedupe(ctx, envelope.EventType, envelope.BusinessID)
	if err != nil {
		// Database error: nack for retry.
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
		// Handler failed: write to dead letter and nack.
		c.writeDeadLetter(ctx, msg.Subject(), string(msg.Data()), err.Error())
		msg.Nak()
		return
	}

	// Success: ack the message.
	msg.Ack()
}

// checkDedupe checks if this event has already been processed.
// Returns true if the event was already processed (deduplicated).
func (c *DurableConsumer) checkDedupe(ctx context.Context, eventType, businessID string) (bool, error) {
	tableName := fmt.Sprintf("%s_event_dedupe", c.cfg.Code)

	record := DedupeRecord{
		EventType:  eventType,
		BusinessID: businessID,
		CreatedAt:  time.Now(),
	}

	// INSERT ON CONFLICT DO NOTHING
	err := c.db.Table(tableName).Create(&record).Error
	if err != nil {
		// Check if it's a unique constraint violation (already exists).
		if isUniqueViolation(err) {
			return true, nil
		}
		return false, fmt.Errorf("failed to insert dedupe record: %w", err)
	}

	return false, nil
}

// writeDeadLetter writes a failed message to the dead letter table and publishes to DLQ.
func (c *DurableConsumer) writeDeadLetter(ctx context.Context, subject, envelopeJSON, errMsg string) {
	tableName := fmt.Sprintf("%s_dead_letter", c.cfg.Code)

	record := DeadLetterRecord{
		ConsumerCode: c.cfg.Code,
		EventType:    c.cfg.EventType,
		Subject:      subject,
		Envelope:     envelopeJSON,
		Error:        errMsg,
		Attempts:     1,
		CreatedAt:    time.Now(),
	}

	err := c.db.Table(tableName).Create(&record).Error
	if err != nil && c.cfg.Code != "" {
		// Log but don't fail - we're already in an error path.
		fmt.Printf("failed to write dead letter: %v\n", err)
	}

	// Publish to dead letter queue subject: dl.{consumerCode}.{event_type}
	dlSubject := fmt.Sprintf("dl.%s.%s", c.cfg.Code, c.cfg.EventType)
	_, _ = c.js.Publish(ctx, dlSubject, []byte(envelopeJSON))
}

// isUniqueViolation checks if an error is a unique constraint violation.
// This is a simplified check; in production, you'd check the specific error code.
func isUniqueViolation(err error) bool {
	// GORM will return an error that contains "unique" for constraint violations.
	// In production, check the specific PostgreSQL error code (23505).
	errMsg := err.Error()
	return containsIgnoreCase(errMsg, "unique") || containsIgnoreCase(errMsg, "duplicate")
}

// containsIgnoreCase checks if a string contains a substring (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	sLower := s
	substrLower := substr
	for i := range s {
		if s[i] >= 'A' && s[i] <= 'Z' {
			sLower = s[:i] + string(s[i]+32) + s[i+1:]
		}
	}
	for i := range substr {
		if substr[i] >= 'A' && substr[i] <= 'Z' {
			substrLower = substr[:i] + string(substr[i]+32) + substr[i+1:]
		}
	}
	return len(sLower) >= len(substrLower) && findSubstring(sLower, substrLower)
}

// findSubstring is a simple substring search.
func findSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
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
