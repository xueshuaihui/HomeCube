package push

import (
	"context"
	"time"
)

// Device represents a target device for push notifications.
type Device struct {
	// DeviceID is the unique identifier for the device.
	DeviceID string

	// Platform indicates the device platform (ios, android, web).
	Platform string

	// Token is the push notification token provided by the platform.
	Token string

	// FamilyID identifies the family this device belongs to.
	FamilyID string
}

// Msg represents a push notification message.
type Msg struct {
	// Title is the notification title.
	Title string

	// Body is the notification body text.
	Body string

	// Data contains additional payload data.
	Data map[string]string

	// Priority indicates message priority (high, normal).
	Priority string

	// TTL is the time-to-live for the message.
	TTL time.Duration
}

// Receipt represents the delivery receipt from a push provider.
type Receipt struct {
	// MessageID is the provider-assigned message ID.
	MessageID string

	// Status indicates delivery status (sent, delivered, failed).
	Status string

	// Timestamp is when the receipt was generated.
	Timestamp time.Time

	// Error contains error details if delivery failed.
	Error string
}

// Provider defines the interface for push notification providers.
// P1-M1 uses stub implementation; real providers implement this interface.
type Provider interface {
	// Send delivers a push notification to the specified device.
	Send(ctx context.Context, device Device, msg Msg) (Receipt, error)
}
