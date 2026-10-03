// Package consumer provides event consumers for the HomeOS service.
package consumer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
)

// DueRegistrationEvent represents a due registration event from other services.
type DueRegistrationEvent struct {
	SourceSystem string `json:"source_system"`
	SourceID     string `json:"source_id"`
	Kind         string `json:"kind"`
	DueAt        string `json:"due_at"`
	Title        string `json:"title"`
	FamilyID     string `json:"family_id,omitempty"`
	AmountCents  int64  `json:"amount_cents,omitempty"`
}

// HandleDueRegistered handles finance.due.registered events and upserts them into homeos_due_registration.
func HandleDueRegistered(ctx context.Context, msg jetstream.Msg, db *gorm.DB) error {
	// Parse message
	var event DueRegistrationEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		return fmt.Errorf("failed to unmarshal due registration event: %w", err)
	}

	// Parse due_at timestamp
	dueAt, err := time.Parse(time.RFC3339, event.DueAt)
	if err != nil {
		return fmt.Errorf("failed to parse due_at timestamp: %w", err)
	}

	// Upsert into homeos_due_registration.
	//
	// The relation is homeos_0008_due_registration's (applied on the dev库), whose column set is
	// documented there as 「列 = 消费方实际写入的列 + 契约声明的列」; this function's Create map and that
	// DDL are the two halves of the same list. What was missing was a caller: bus.NewDurableConsumer
	// had none anywhere in the repository until bus_runtime.go registered finance.due.registered against
	// HC_FINANCE and DueRegisteredHandler wired this function to it.
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Check if record already exists
		var existing struct {
			ID string
		}
		// deleted_at IS NULL is not decoration: homeos_0008_due_registration's upsert anchor is the
		// PARTIAL unique index (source_system, source_id, kind) WHERE deleted_at IS NULL, and the DDL
		// states why (0008:49-54「撤销后同一对象再次注册（账单重新排期）要能进新的一行，否则历史撤销
		// 行会把槽位永久占住」). Matching a revoked row here would instead UPDATE a soft-deleted
		// registration -- the re-registered bill stays invisible to the B 区 and the slot is occupied
		// forever. finance.due.revoked is the only writer of deleted_at (0008:43-44), and REV-1 put that
		// writer here: due_revoked_handler.go, subscribed through bus_runtime.go, so a revoked row stays
		// revoked and a re-registration of the same object takes the new-row path 0008 describes.
		result := tx.Table("homeos_due_registration").
			Select("id").
			Where("source_system = ? AND source_id = ? AND kind = ? AND deleted_at IS NULL",
				event.SourceSystem, event.SourceID, event.Kind).
			First(&existing)

		now := time.Now()

		if result.Error == nil {
			// Update existing record
			return tx.Table("homeos_due_registration").
				Where("id = ?", existing.ID).
				Updates(map[string]any{
					"due_at":     dueAt,
					"title":      event.Title,
					"updated_at": now,
				}).Error
		}

		// Insert new record
		return tx.Table("homeos_due_registration").Create(map[string]any{
			"id":            generateUUID(),
			"source_system": event.SourceSystem,
			"source_id":     event.SourceID,
			"kind":          event.Kind,
			"due_at":        dueAt,
			"title":         event.Title,
			"family_id":     event.FamilyID,
			"created_at":    now,
			"updated_at":    now,
		}).Error
	})

	if err != nil {
		return fmt.Errorf("failed to upsert due registration: %w", err)
	}

	// Acknowledge the message
	if err := msg.Ack(); err != nil {
		return fmt.Errorf("failed to ack message: %w", err)
	}

	return nil
}

// generateUUID generates a UUID v4 using crypto/rand.
func generateUUID() string {
	uuid := make([]byte, 16)
	_, err := rand.Read(uuid)
	if err != nil {
		panic(fmt.Sprintf("failed to generate UUID: %v", err))
	}
	// Set version bits (version 4)
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant bits (RFC 4122)
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}
