package bus

import (
	"context"
)

// Bus defines the interface for the HomeCube message bus abstraction.
// It supports outbox-based publishing and durable consumer subscriptions
// backed by NATS JetStream.
type Bus interface {
	// Publish sends a message to the given subject with the provided envelope.
	// The message is first persisted to the outbox table within the caller's
	// transaction boundary, then asynchronously delivered to JetStream.
	Publish(ctx context.Context, subject string, envelope Envelope) error

	// Subscribe registers a handler for messages consumed by the named durable
	// consumer. The consumer will filter messages by subject and process them
	// with at-least-once delivery semantics, including deduplication and
	// dead-letter handling.
	Subscribe(ctx context.Context, consumerName string, handler Handler) error

	// Close gracefully shuts down the bus, stopping all consumers and flushes
	// pending outbox deliveries.
	Close() error
}

// Message represents a message received from the bus.
type Message struct {
	// Subject is the NATS subject the message was published to.
	// Format: {code}.{object}.{action}
	Subject string

	// Envelope contains the structured event data.
	Envelope Envelope

	// Headers contains optional metadata headers.
	Headers map[string]string
}

// Envelope is the standard event payload structure used across all services.
type Envelope struct {
	// EventType identifies the type of event (e.g., "finance.transaction.created").
	EventType string `json:"event_type"`

	// BusinessID is the business-level identifier for deduplication.
	// See PRD 10.4 for取值约定 per event type.
	BusinessID string `json:"business_id"`

	// FamilyID identifies the family this event belongs to.
	FamilyID string `json:"family_id"`

	// Payload is the event-specific data.
	Payload map[string]any `json:"payload"`

	// Version is the schema version of this event.
	Version string `json:"version"`

	// CausationID links to the immediate cause of this event.
	CausationID string `json:"causation_id,omitempty"`

	// CorrelationID groups related events in a workflow.
	CorrelationID string `json:"correlation_id,omitempty"`

	// Timestamp is when the event occurred (ISO 8601).
	Timestamp string `json:"timestamp"`
}

// Handler is a function that processes an incoming message.
// It should be idempotent and handle errors appropriately.
type Handler func(ctx context.Context, msg Message) error
