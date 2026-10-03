// due_trigger_scanner_test.go verifies the due-trigger scanner's core behavior: scanning for
// upcoming dues, deduplicating via homeos_reminder_sent, and publishing homeos.reminder.fired events.
package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// setupTestDB creates an in-memory SQLite database with the necessary tables for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Create homeos_due_registration table (simplified version of 0008)
	err = db.Exec(`
		CREATE TABLE homeos_due_registration (
			id TEXT PRIMARY KEY,
			family_id TEXT NOT NULL,
			source_system TEXT NOT NULL,
			source_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('bill', 'budget', 'goal', 'repayment')),
			title TEXT NOT NULL,
			due_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP
		)
	`).Error
	require.NoError(t, err)

	// Create homeos_reminder_sent table (0011)
	err = db.Exec(`
		CREATE TABLE homeos_reminder_sent (
			id TEXT PRIMARY KEY,
			registration_id TEXT NOT NULL,
			fire_date DATE NOT NULL,
			fired_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`).Error
	require.NoError(t, err)

	// Create unique index on (registration_id, fire_date)
	err = db.Exec(`
		CREATE UNIQUE INDEX idx_reminder_sent_reg_fire
		ON homeos_reminder_sent (registration_id, fire_date)
	`).Error
	require.NoError(t, err)

	// Create homeos_outbox table (simplified version of 0002)
	err = db.Exec(`
		CREATE TABLE homeos_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			family_id TEXT,
			subject TEXT NOT NULL,
			envelope TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'sent')),
			attempts INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`).Error
	require.NoError(t, err)

	return db
}

// insertTestRegistration inserts a test registration into the database.
func insertTestRegistration(t *testing.T, db *gorm.DB, id, familyID, sourceSystem, sourceID, kind, title string, dueAt time.Time) {
	t.Helper()

	err := db.Table("homeos_due_registration").Create(map[string]any{
		"id":            id,
		"family_id":     familyID,
		"source_system": sourceSystem,
		"source_id":     sourceID,
		"kind":          kind,
		"title":         title,
		"due_at":        dueAt,
		"created_at":    time.Now(),
		"updated_at":    time.Now(),
	}).Error
	require.NoError(t, err)
}

func TestDueTriggerScanner_ScanFindsUpcomingDues(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now().UTC()
	// Insert registrations at various times relative to now
	insertTestRegistration(t, db, "reg-1", "family-1", "finance", "bill-1", "bill", "Electric Bill", now.Add(2*time.Minute))
	insertTestRegistration(t, db, "reg-2", "family-1", "finance", "bill-2", "bill", "Water Bill", now.Add(-3*time.Minute))
	insertTestRegistration(t, db, "reg-3", "family-1", "finance", "bill-3", "bill", "Future Bill", now.Add(1*time.Hour)) // Too far in future
	insertTestRegistration(t, db, "reg-4", "family-1", "finance", "bill-4", "bill", "Past Bill", now.Add(-1*time.Hour))   // Too far in past

	scanner := NewDueTriggerScanner(ScannerConfig{
		DB:               db,
		Code:             "homeos",
		ScanInterval:     1 * time.Minute,
		ReminderLeadTime: 5 * time.Minute,
	})

	ctx := context.Background()

	// Perform one scan
	scanner.scanOnce(ctx)

	// Check that outbox messages were created for reg-1 and reg-2 (within ±5 min window)
	var outboxCount int64
	err := db.Table("homeos_outbox").Count(&outboxCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(2), outboxCount, "should create outbox messages for items within the due window")

	// Verify the outbox messages have correct event type
	var messages []struct {
		Subject  string
		Envelope string
	}
	err = db.Table("homeos_outbox").Select("subject, envelope").Find(&messages).Error
	require.NoError(t, err)

	for _, msg := range messages {
		assert.Equal(t, DueTriggerEventType, msg.Subject)

		var envelope bus.Envelope
		err := json.Unmarshal([]byte(msg.Envelope), &envelope)
		require.NoError(t, err)
		assert.Equal(t, DueTriggerEventType, envelope.EventType)
		assert.Contains(t, []string{"reg-1", "reg-2"}, envelope.Payload["reminder_id"])
	}
}

func TestDueTriggerScanner_Idempotency(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now().UTC()
	insertTestRegistration(t, db, "reg-1", "family-1", "finance", "bill-1", "bill", "Test Bill", now)

	scanner := NewDueTriggerScanner(ScannerConfig{
		DB:               db,
		Code:             "homeos",
		ScanInterval:     1 * time.Minute,
		ReminderLeadTime: 5 * time.Minute,
	})

	ctx := context.Background()

	// First scan
	scanner.scanOnce(ctx)

	var outboxCount1 int64
	err := db.Table("homeos_outbox").Count(&outboxCount1).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), outboxCount1, "first scan should create one outbox message")

	// Second scan (should not create duplicate)
	scanner.scanOnce(ctx)

	var outboxCount2 int64
	err = db.Table("homeos_outbox").Count(&outboxCount2).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), outboxCount2, "second scan should not create duplicate outbox message")

	// Verify dedup record exists
	var dedupCount int64
	err = db.Table("homeos_reminder_sent").Count(&dedupCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), dedupCount, "should have one dedup record")
}

func TestDueTriggerScanner_SoftDeletedIgnored(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now().UTC()
	insertTestRegistration(t, db, "reg-1", "family-1", "finance", "bill-1", "bill", "Active Bill", now)
	insertTestRegistration(t, db, "reg-2", "family-1", "finance", "bill-2", "bill", "Deleted Bill", now)

	// Soft delete reg-2
	err := db.Table("homeos_due_registration").Where("id = ?", "reg-2").Update("deleted_at", time.Now()).Error
	require.NoError(t, err)

	scanner := NewDueTriggerScanner(ScannerConfig{
		DB:               db,
		Code:             "homeos",
		ScanInterval:     1 * time.Minute,
		ReminderLeadTime: 5 * time.Minute,
	})

	ctx := context.Background()
	scanner.scanOnce(ctx)

	var outboxCount int64
	err = db.Table("homeos_outbox").Count(&outboxCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), outboxCount, "should only create outbox message for non-deleted registration")
}

func TestDueTriggerScanner_ReminderTypeClassification(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now().UTC()
	insertTestRegistration(t, db, "reg-budget", "family-1", "finance", "budget-1", "budget", "Monthly Budget", now)
	insertTestRegistration(t, db, "reg-bill", "family-1", "finance", "bill-1", "bill", "Electric Bill", now)
	insertTestRegistration(t, db, "reg-repayment", "family-1", "finance", "loan-1", "repayment", "Loan Payment", now)
	insertTestRegistration(t, db, "reg-goal", "family-1", "finance", "goal-1", "goal", "Savings Goal", now)

	scanner := NewDueTriggerScanner(ScannerConfig{
		DB:               db,
		Code:             "homeos",
		ScanInterval:     1 * time.Minute,
		ReminderLeadTime: 5 * time.Minute,
	})

	ctx := context.Background()
	scanner.scanOnce(ctx)

	var messages []struct {
		Envelope string
	}
	err := db.Table("homeos_outbox").Select("envelope").Find(&messages).Error
	require.NoError(t, err)
	require.Len(t, messages, 4)

	// Check type classification
	typeMap := map[string]string{
		"reg-budget":    "budget_alert",
		"reg-bill":      "reminder",
		"reg-repayment": "reminder",
		"reg-goal":      "reminder",
	}

	for _, msg := range messages {
		var envelope bus.Envelope
		err := json.Unmarshal([]byte(msg.Envelope), &envelope)
		require.NoError(t, err)

		reminderID := envelope.Payload["reminder_id"].(string)
		expectedType := typeMap[reminderID]
		assert.Equal(t, expectedType, envelope.Payload["type"], "reminder type should match kind")
	}
}

func TestDueTriggerScanner_StartStop(t *testing.T) {
	db := setupTestDB(t)

	scanner := NewDueTriggerScanner(ScannerConfig{
		DB:               db,
		Code:             "homeos",
		ScanInterval:     100 * time.Millisecond, // Fast interval for testing
		ReminderLeadTime: 5 * time.Minute,
	})

	ctx := context.Background()

	// Start the scanner
	scanner.Start(ctx)

	// Let it run for a bit
	time.Sleep(250 * time.Millisecond)

	// Stop the scanner
	scanner.Stop()

	// Should not panic or hang
	assert.True(t, true, "scanner should start and stop cleanly")
}

func TestClassifyReminderType(t *testing.T) {
	tests := []struct {
		kind     string
		expected string
	}{
		{"budget", "budget_alert"},
		{"bill", "reminder"},
		{"repayment", "reminder"},
		{"goal", "reminder"},
		{"unknown", "reminder"}, // Default case
		{"", "reminder"},        // Empty string defaults to reminder
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			result := classifyReminderType(tt.kind)
			assert.Equal(t, tt.expected, result)
		})
	}
}
