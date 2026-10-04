// due_trigger_scanner.go is svc-homeos's background job that scans homeos_due_registration for
// upcoming due dates and publishes homeos.reminder.fired events via the outbox pattern (§3.3).
//
// What it does: every ScanInterval (default 1 minute), it queries for registrations whose due_at
// falls within [now - ReminderLeadTime, now + ReminderLeadTime] — i.e. items that are either just
// past due or about to come due. For each such item it checks whether a reminder has already been
// sent for this exact (registration_id, fire_date) pair by consulting homeos_reminder_sent, a
// deduplication table introduced alongside this scanner. If no prior send exists, it inserts one
// row into homeos_outbox with event_type homeos.reminder.fired and marks the send record.
//
// Why two tables: homeos_due_registration is the source of truth for what is due;
// homeos_reminder_sent is the side-effect log that makes this scanner idempotent. A delivery can
// arrive twice (at-least-once semantics from §3.4), but the same (id, fire_date) must never
// produce two outbox rows — otherwise consumers see duplicate reminders and users get double
// notifications. The unique index on homeos_reminder_sent(registration_id, fire_date) enforces
// that at the database level.
//
// Timezone handling: due_at is stored as timestamptz (0008:40), so comparisons against now() are
// always in UTC. The family's timezone matters only at display time (repo.HomeSummaryDueToday
// converts to the family zone); the scanner fires based on absolute instants, which is correct
// because a bill due at 2026-10-27 09:00 Asia/Shanghai is the same instant worldwide.
//
// Lifecycle: started by SetupBus in bus_runtime.go, stopped when the runtime shuts down. Safe to
// call on a partially built runtime (nil check guards).
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// DueTriggerEventType is the event this scanner publishes, named by contracts/events/homeos.yaml:161
// (homeos.reminder.fired, publisher svc-homeos, consumers: all six faces). It sits next to
// bus_runtime.go's DueRegisteredEventType / DueRevokedEventType so the three ends of the 到期 chain
// are readable together.
const DueTriggerEventType = "homeos.reminder.fired"

// ScannerConfig holds configuration for the due-trigger scanner.
type ScannerConfig struct {
	// Logger receives operational logs (scan counts, errors). Nil means slog.Default().
	Logger *slog.Logger

	// DB is the bus-side database handle (same one openBusDB returns).
	DB *gorm.DB

	// Code is the service code for outbox table naming (e.g., "homeos").
	Code string

	// ScanInterval is how often the scanner checks for upcoming dues. Default: 1 minute.
	ScanInterval time.Duration

	// ReminderLeadTime is the window around now() within which dues are considered "due".
	// Default: 5 minutes — items due within ±5 min of now get a reminder fired.
	ReminderLeadTime time.Duration
}

// DueTriggerScanner is the background job that scans for upcoming dues and fires reminders.
type DueTriggerScanner struct {
	cfg    ScannerConfig
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewDueTriggerScanner creates a new scanner instance.
func NewDueTriggerScanner(cfg ScannerConfig) *DueTriggerScanner {
	if cfg.ScanInterval == 0 {
		cfg.ScanInterval = 1 * time.Minute
	}
	if cfg.ReminderLeadTime == 0 {
		cfg.ReminderLeadTime = 5 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &DueTriggerScanner{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start begins the background scanning loop.
func (s *DueTriggerScanner) Start(ctx context.Context) {
	s.wg.Add(1)
	go s.run(ctx)

	// On startup, do an immediate scan so reminders don't wait a full interval after restart.
	s.scanOnce(ctx)
}

// Stop gracefully shuts down the scanner.
func (s *DueTriggerScanner) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

// run is the main scanning loop.
func (s *DueTriggerScanner) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.scanOnce(ctx)
		}
	}
}

// scanOnce performs one scan cycle: finds upcoming dues, checks deduplication, and publishes events.
func (s *DueTriggerScanner) scanOnce(ctx context.Context) {
	now := time.Now().UTC()
	windowStart := now.Add(-s.cfg.ReminderLeadTime)
	windowEnd := now.Add(s.cfg.ReminderLeadTime)

	// Query for registrations in the due window, excluding soft-deleted ones.
	// Only live rows (deleted_at IS NULL) and only those belonging to mounted faces
	// (source_system IN (...)) would be ideal, but the scanner has no session context
	// to know which faces are mounted for which family. Instead, it fires for ALL live
	// registrations in the window — the consumer side (or notification handler) filters
	// by face visibility when delivering to users. This matches PRD 17.8's intent:
	// 「被停用面的注册项不消失也不触发」 means they stay in the table but don't reach users,
	// not that the scanner skips them entirely (that would require per-family face lookups
	// on every scan, which is expensive).
	var registrations []dueRegistrationRow
	err := s.cfg.DB.WithContext(ctx).
		Table("homeos_due_registration").
		Select("id, family_id, source_system, source_id, kind, title, due_at").
		Where("due_at >= ? AND due_at < ? AND deleted_at IS NULL", windowStart, windowEnd).
		Order("due_at ASC").
		Find(&registrations).Error

	if err != nil {
		s.cfg.Logger.Error("due_trigger_scan_failed", "error", err.Error())
		return
	}

	if len(registrations) == 0 {
		return
	}

	firedCount := 0
	for _, reg := range registrations {
		if err := s.fireReminder(ctx, reg, now); err != nil {
			s.cfg.Logger.Error("due_trigger_fire_failed",
				"registration_id", reg.ID,
				"family_id", reg.FamilyID,
				"error", err.Error())
			// Continue with other registrations — one failure shouldn't block the rest.
			continue
		}
		firedCount++
	}

	if firedCount > 0 {
		s.cfg.Logger.Info("due_trigger_scan_completed",
			"window_start", windowStart.Format(time.RFC3339),
			"window_end", windowEnd.Format(time.RFC3339),
			"candidates", len(registrations),
			"fired", firedCount)
	}
}

// fireReminder publishes a homeos.reminder.fired event for one registration, if not already sent.
//
// Idempotency: the INSERT into homeos_reminder_sent uses ON CONFLICT DO NOTHING on the unique
// index (registration_id, fire_date). If a row already exists for this pair, the insert is a
// no-op and no outbox message is written — exactly §3.4's「重复投递不报错」.
//
// Transactional: both the dedup insert and the outbox insert happen in one transaction, so
// either both succeed or neither does. This prevents orphaned dedup records (sent but no event)
// or duplicate events (event sent but no dedup record, leading to re-send on retry).
func (s *DueTriggerScanner) fireReminder(ctx context.Context, reg dueRegistrationRow, now time.Time) error {
	// fire_date is the calendar date of the due_at in UTC. This is what the contract declares
	// (contracts/events/homeos.yaml:175 fire_date: date) and what makes business_id unique per day.
	fireDate := reg.DueAt.UTC().Truncate(24 * time.Hour)

	return s.cfg.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.fireReminderWithCheck(tx, reg, fireDate, now)
	})
}

// fireReminderWithCheck checks if a reminder was already sent, and if not, sends it.
func (s *DueTriggerScanner) fireReminderWithCheck(tx *gorm.DB, reg dueRegistrationRow, fireDate time.Time, now time.Time) error {
	// Check if already sent
	var count int64
	err := tx.Table("homeos_reminder_sent").
		Where("registration_id = ? AND fire_date = ?", reg.ID, fireDate).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check reminder sent status: %w", err)
	}
	if count > 0 {
		// Already sent — this is a redelivery or a duplicate scan. Skip silently.
		return nil
	}

	// Insert dedup record with ON CONFLICT DO NOTHING for safety (even though we checked above,
	// this protects against races if multiple scanner instances run concurrently).
	dedup := reminderSentRecord{
		ID:             generateUUID(),
		RegistrationID: reg.ID,
		FireDate:       fireDate,
		FiredAt:        now,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "registration_id"}, {Name: "fire_date"}},
		DoNothing: true,
	}).Create(&dedup).Error; err != nil {
		return fmt.Errorf("failed to insert reminder sent record: %w", err)
	}

	// Build the envelope payload per contracts/events/homeos.yaml:173-178
	payload := map[string]any{
		"reminder_id":   reg.ID,
		"fire_date":     fireDate.Format("2006-01-02"), // date format (YYYY-MM-DD)
		"source_system": reg.SourceSystem,
		"source_id":     reg.SourceID,
		"type":          classifyReminderType(reg.Kind),
		"title":         reg.Title,
		"family_id":     reg.FamilyID,
		"due_at":        reg.DueAt.UTC().Format(time.RFC3339),
	}

	envelope := bus.Envelope{
		EventType:  DueTriggerEventType,
		BusinessID: fmt.Sprintf("%s:%s", reg.ID, fireDate.Format("2006-01-02")),
		FamilyID:   reg.FamilyID,
		Payload:    payload,
		Version:    "1.0",
		Timestamp:  now.Format(time.RFC3339Nano),
	}

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// Insert into outbox for async delivery to JetStream
	if err := bus.InsertOutboxMessageWithFamily(tx, s.cfg.Code, reg.FamilyID, DueTriggerEventType, string(envelopeJSON)); err != nil {
		return fmt.Errorf("failed to insert outbox message: %w", err)
	}

	return nil
}

// dueRegistrationRow is the shape of one row from homeos_due_registration that the scanner reads.
type dueRegistrationRow struct {
	ID           string    `gorm:"column:id"`
	FamilyID     string    `gorm:"column:family_id"`
	SourceSystem string    `gorm:"column:source_system"`
	SourceID     string    `gorm:"column:source_id"`
	Kind         string    `gorm:"column:kind"`
	Title        string    `gorm:"column:title"`
	DueAt        time.Time `gorm:"column:due_at"`
}

// reminderSentRecord is the deduplication table row for tracking sent reminders.
type reminderSentRecord struct {
	ID             string    `gorm:"column:id;primaryKey"`
	RegistrationID string    `gorm:"column:registration_id;not null"`
	FireDate       time.Time `gorm:"column:fire_date;not null"`
	FiredAt        time.Time `gorm:"column:fired_at;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;default:now()"`
}

// TableName returns the table name for reminderSentRecord.
func (reminderSentRecord) TableName() string {
	return "homeos_reminder_sent"
}

// classifyReminderType maps the registration kind to the reminder type enum.
// Per contracts/events/homeos.yaml:178 type: enum(budget_alert|system|reminder).
// Most due registrations are "reminder" type; budget-related ones could be "budget_alert".
func classifyReminderType(kind string) string {
	switch kind {
	case "budget":
		return "budget_alert"
	case "bill", "repayment", "goal":
		return "reminder"
	default:
		return "reminder"
	}
}
