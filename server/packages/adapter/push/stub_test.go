package push

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

type mockLogger struct {
	*slog.Logger
}

func newMockLogger() *mockLogger {
	return &mockLogger{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

func TestStubProvider_Send(t *testing.T) {
	logger := newMockLogger()
	provider := NewStubProvider(logger.Logger)

	ctx := context.Background()
	device := Device{
		DeviceID: "test-device-123",
		Platform: "ios",
		Token:    "test-token",
		FamilyID: "family-456",
	}

	msg := Msg{
		Title:    "Test Notification",
		Body:     "This is a test message",
		Data:     map[string]string{"key": "value"},
		Priority: "high",
		TTL:      5 * time.Minute,
	}

	receipt, err := provider.Send(ctx, device, msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if receipt.MessageID == "" {
		t.Error("expected non-empty message ID")
	}

	if receipt.Status != "sent" {
		t.Errorf("expected status 'sent', got %q", receipt.Status)
	}

	if receipt.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	if receipt.Error != "" {
		t.Errorf("expected empty error, got %q", receipt.Error)
	}
}

func TestStubProvider_Interface(t *testing.T) {
	// Verify StubProvider implements Provider interface
	var _ Provider = (*StubProvider)(nil)
}

func TestStubProvider_NilLogger(t *testing.T) {
	// Test that stub works with nil logger
	provider := NewStubProvider(nil)

	ctx := context.Background()
	device := Device{
		DeviceID: "test-device",
		Platform: "android",
		Token:    "token",
		FamilyID: "family",
	}

	msg := Msg{
		Title: "Test",
		Body:  "Body",
	}

	receipt, err := provider.Send(ctx, device, msg)
	if err != nil {
		t.Fatalf("expected no error with nil logger, got %v", err)
	}

	if receipt.Status != "sent" {
		t.Errorf("expected status 'sent', got %q", receipt.Status)
	}
}

func TestDevice_Structure(t *testing.T) {
	device := Device{
		DeviceID: "dev123",
		Platform: "web",
		Token:    "web-token",
		FamilyID: "fam789",
	}

	if device.DeviceID != "dev123" {
		t.Errorf("expected DeviceID dev123, got %q", device.DeviceID)
	}
	if device.Platform != "web" {
		t.Errorf("expected Platform web, got %q", device.Platform)
	}
}

func TestMsg_Structure(t *testing.T) {
	msg := Msg{
		Title:    "Title",
		Body:     "Body",
		Data:     map[string]string{"k": "v"},
		Priority: "normal",
		TTL:      time.Hour,
	}

	if msg.Title != "Title" {
		t.Errorf("expected Title 'Title', got %q", msg.Title)
	}
	if msg.Priority != "normal" {
		t.Errorf("expected Priority 'normal', got %q", msg.Priority)
	}
}

func TestReceipt_Structure(t *testing.T) {
	now := time.Now()
	receipt := Receipt{
		MessageID: "msg-123",
		Status:    "delivered",
		Timestamp: now,
		Error:     "some error",
	}

	if receipt.MessageID != "msg-123" {
		t.Errorf("expected MessageID 'msg-123', got %q", receipt.MessageID)
	}
	if receipt.Status != "delivered" {
		t.Errorf("expected Status 'delivered', got %q", receipt.Status)
	}
}
