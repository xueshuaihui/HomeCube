package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamConfig holds the configuration for NATS JetStream connection.
type JetStreamConfig struct {
	// URL is the NATS server URL (e.g., "nats://localhost:4222").
	URL string

	// Code is the service code (e.g., "homeos", "finance").
	Code string

	// MaxAge is the retention period for messages in the stream.
	// Default: 90 days per tech plan §3.1.
	MaxAge time.Duration

	// Replicas is the number of replicas for the stream.
	// P1 uses R=1 (single node) per tech plan §3.1.
	Replicas int
}

// JetStreamWrapper provides a thin abstraction over NATS JetStream for testing.
// It allows mocking without requiring a real NATS connection.
type JetStreamWrapper interface {
	// CreateStream ensures a stream exists with the given configuration.
	CreateStream(ctx context.Context, name string, subjects []string) error

	// Publish sends a message to the given subject.
	Publish(ctx context.Context, subject string, data []byte) (*jetstream.PubAck, error)

	// Subscribe creates a durable consumer and returns a subscription.
	Subscribe(ctx context.Context, streamName, consumerName, filterSubject string, handler jetstream.MessageHandler) error

	// Close gracefully closes the connection.
	Close()
}

// natsJetStream implements JetStreamWrapper using real NATS JetStream.
type natsJetStream struct {
	conn *nats.Conn
	js   jetstream.JetStream
	cfg  JetStreamConfig
}

// NewJetStreamWrapper creates a new JetStream wrapper instance.
func NewJetStreamWrapper(cfg JetStreamConfig) (JetStreamWrapper, error) {
	conn, err := nats.Connect(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	return &natsJetStream{
		conn: conn,
		js:   js,
		cfg:  cfg,
	}, nil
}

// CreateStream ensures a stream exists with the given configuration.
// Stream naming: HC_{CODE} (uppercase).
// Subject pattern: {code}.>
func (n *natsJetStream) CreateStream(ctx context.Context, name string, subjects []string) error {
	streamCfg := jetstream.StreamConfig{
		Name:     name,
		Subjects: subjects,
		MaxAge:   n.cfg.MaxAge,
		Replicas: n.cfg.Replicas,
	}

	_, err := n.js.CreateOrUpdateStream(ctx, streamCfg)
	if err != nil {
		return fmt.Errorf("failed to create stream %s: %w", name, err)
	}

	return nil
}

// Publish sends a message to the given subject.
func (n *natsJetStream) Publish(ctx context.Context, subject string, data []byte) (*jetstream.PubAck, error) {
	ack, err := n.js.Publish(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("failed to publish message to %s: %w", subject, err)
	}
	return ack, nil
}

// Subscribe creates a durable consumer and registers a message handler.
func (n *natsJetStream) Subscribe(ctx context.Context, streamName, consumerName, filterSubject string, handler jetstream.MessageHandler) error {
	consumerCfg := jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: filterSubject,
		AckWait:       30 * time.Second,
		MaxDeliver:    4,
		BackOff:       []time.Duration{1 * time.Second, 10 * time.Second, 60 * time.Second},
	}

	_, err := n.js.CreateOrUpdateConsumer(ctx, streamName, consumerCfg)
	if err != nil {
		return fmt.Errorf("failed to create consumer %s: %w", consumerName, err)
	}

	// Note: Actual message consumption would be handled by the consumer framework
	// in consumer.go. This method just ensures the consumer exists.
	return nil
}

// Close gracefully closes the NATS connection.
func (n *natsJetStream) Close() {
	if n.conn != nil {
		n.conn.Close()
	}
}

// BuildSubject constructs a subject string from code, object, and action.
// Format: {code}.{object}.{action}
func BuildSubject(code, object, action string) string {
	return fmt.Sprintf("%s.%s.%s", code, object, action)
}

// MarshalEnvelope serializes an Envelope to JSON bytes.
func MarshalEnvelope(envelope Envelope) ([]byte, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal envelope: %w", err)
	}
	return data, nil
}

// UnmarshalEnvelope deserializes JSON bytes to an Envelope.
func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	err := json.Unmarshal(data, &env)
	if err != nil {
		return Envelope{}, fmt.Errorf("failed to unmarshal envelope: %w", err)
	}
	return env, nil
}
