package bus

import (
	"context"
	"testing"
	"time"
)

// TestInterfaceDefinitions verifies that the interface types are properly defined.
func TestInterfaceDefinitions(t *testing.T) {
	// Test Envelope structure.
	env := Envelope{
		EventType:     "finance.transaction.created",
		BusinessID:    "test-id-123",
		FamilyID:      "family-uuid",
		Payload:       map[string]any{"amount": 100},
		Version:       "1.0",
		CausationID:   "cause-123",
		CorrelationID: "corr-456",
		Timestamp:     "2026-10-01T00:00:00Z",
	}

	if env.EventType != "finance.transaction.created" {
		t.Errorf("expected EventType to be 'finance.transaction.created', got %s", env.EventType)
	}

	if env.BusinessID != "test-id-123" {
		t.Errorf("expected BusinessID to be 'test-id-123', got %s", env.BusinessID)
	}

	// Test Message structure.
	msg := Message{
		Subject:  "finance.transaction.created",
		Envelope: env,
		Headers:  map[string]string{"key": "value"},
	}

	if msg.Subject != "finance.transaction.created" {
		t.Errorf("expected Subject to be 'finance.transaction.created', got %s", msg.Subject)
	}

	if len(msg.Headers) != 1 {
		t.Errorf("expected 1 header, got %d", len(msg.Headers))
	}
}

// TestBuildSubject verifies subject construction.
func TestBuildSubject(t *testing.T) {
	tests := []struct {
		code   string
		object string
		action string
		want   string
	}{
		{"homeos", "member", "created", "homeos.member.created"},
		{"finance", "transaction", "created", "finance.transaction.created"},
		{"finance", "budget", "exceeded", "finance.budget.exceeded"},
	}

	for _, tt := range tests {
		got := BuildSubject(tt.code, tt.object, tt.action)
		if got != tt.want {
			t.Errorf("BuildSubject(%q, %q, %q) = %q, want %q", tt.code, tt.object, tt.action, got, tt.want)
		}
	}
}

// TestMarshalUnmarshalEnvelope verifies envelope serialization.
func TestMarshalUnmarshalEnvelope(t *testing.T) {
	env := Envelope{
		EventType:  "finance.transaction.created",
		BusinessID: "tx-123",
		FamilyID:   "fam-456",
		Payload:    map[string]any{"amount_cents": 1000},
		Version:    "1.0",
		Timestamp:  "2026-10-01T00:00:00Z",
	}

	data, err := MarshalEnvelope(env)
	if err != nil {
		t.Fatalf("MarshalEnvelope failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty marshaled data")
	}

	unmarshaled, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope failed: %v", err)
	}

	if unmarshaled.EventType != env.EventType {
		t.Errorf("expected EventType %q, got %q", env.EventType, unmarshaled.EventType)
	}

	if unmarshaled.BusinessID != env.BusinessID {
		t.Errorf("expected BusinessID %q, got %q", env.BusinessID, unmarshaled.BusinessID)
	}
}

// TestOutboxConfigDefaults verifies default configuration values.
func TestOutboxConfigDefaults(t *testing.T) {
	cfg := OutboxConfig{}

	// After NewOutboxDeliverer, defaults should be applied.
	deliverer := NewOutboxDeliverer(nil, nil, cfg, nil)

	if deliverer.cfg.BatchSize != 100 {
		t.Errorf("expected default BatchSize 100, got %d", deliverer.cfg.BatchSize)
	}

	if deliverer.cfg.Interval != 500*time.Millisecond {
		t.Errorf("expected default Interval 500ms, got %v", deliverer.cfg.Interval)
	}

	if deliverer.cfg.MaxAttempts != 10 {
		t.Errorf("expected default MaxAttempts 10, got %d", deliverer.cfg.MaxAttempts)
	}
}

// TestConsumerConfigDefaults verifies consumer configuration defaults.
func TestConsumerConfigDefaults(t *testing.T) {
	cfg := ConsumerConfig{
		Code:       "finance",
		StreamName: "HC_FINANCE",
		EventType:  "finance.transaction.created",
	}

	consumer := NewDurableConsumer(nil, nil, cfg)

	if consumer.cfg.AckWait != 30*time.Second {
		t.Errorf("expected default AckWait 30s, got %v", consumer.cfg.AckWait)
	}

	if consumer.cfg.MaxDeliver != 4 {
		t.Errorf("expected default MaxDeliver 4, got %d", consumer.cfg.MaxDeliver)
	}

	if len(consumer.cfg.BackOff) != 3 {
		t.Errorf("expected 3 backoff intervals, got %d", len(consumer.cfg.BackOff))
	}
}

// TestOutboxStatusConstants verifies outbox status values.
func TestOutboxStatusConstants(t *testing.T) {
	if OutboxStatusPending != "pending" {
		t.Errorf("expected OutboxStatusPending to be 'pending', got %s", OutboxStatusPending)
	}

	if OutboxStatusSent != "sent" {
		t.Errorf("expected OutboxStatusSent to be 'sent', got %s", OutboxStatusSent)
	}

	if OutboxStatusFailed != "failed" {
		t.Errorf("expected OutboxStatusFailed to be 'failed', got %s", OutboxStatusFailed)
	}
}

// TestTableNameMethods verifies table name methods exist.
func TestTableNameMethods(t *testing.T) {
	outbox := OutboxMessage{}
	_ = outbox.TableName() // Should not panic

	dedupe := DedupeRecord{}
	_ = dedupe.TableName() // Should not panic

	deadLetter := DeadLetterRecord{}
	_ = deadLetter.TableName() // Should not panic
}

// TestHandlerType verifies Handler type signature.
func TestHandlerType(t *testing.T) {
	var handler Handler = func(ctx context.Context, msg Message) error {
		return nil
	}

	ctx := context.Background()
	msg := Message{
		Subject: "test.subject",
		Envelope: Envelope{
			EventType: "test.event",
		},
	}

	err := handler(ctx, msg)
	if err != nil {
		t.Errorf("handler returned unexpected error: %v", err)
	}
}
