package push

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// StubProvider is a P1-M1 stub implementation of the Provider interface.
// It writes logs and returns fake receipts to ensure the push notification
// pipeline is testable without real provider integration.
type StubProvider struct {
	logger *slog.Logger
}

// NewStubProvider creates a new stub push provider.
func NewStubProvider(logger *slog.Logger) *StubProvider {
	return &StubProvider{
		logger: logger,
	}
}

// Send implements the Provider interface for stub testing.
// It logs the push attempt and returns a fabricated receipt.
func (s *StubProvider) Send(ctx context.Context, device Device, msg Msg) (Receipt, error) {
	if s.logger != nil {
		s.logger.Info("stub push notification",
			"device_id", device.DeviceID,
			"platform", device.Platform,
			"family_id", device.FamilyID,
			"title", msg.Title,
			"priority", msg.Priority,
		)
	}

	// Return a fabricated successful receipt
	receipt := Receipt{
		MessageID: fmt.Sprintf("stub-msg-%d", time.Now().UnixNano()),
		Status:    "sent",
		Timestamp: time.Now(),
		Error:     "",
	}

	return receipt, nil
}
