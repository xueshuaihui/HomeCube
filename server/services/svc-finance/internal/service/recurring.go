// Package service provides business logic for the finance service.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// BusPublisher is an interface for publishing events via outbox.
type BusPublisher interface {
	Publish(ctx context.Context, db *gorm.DB, subject string, envelope map[string]any) error
}

// RecurringService handles recurring transaction execution.
type RecurringService struct {
	repo    *repo.FinanceRepo
	pub     BusPublisher
	code    string // service code for outbox table naming
}

// NewRecurringService creates a new recurring service instance.
func NewRecurringService(repo *repo.FinanceRepo, pub BusPublisher, code string) *RecurringService {
	return &RecurringService{
		repo: repo,
		pub:  pub,
		code: code,
	}
}

// ExecuteDueRecurringRules executes all recurring rules that are due.
// This should be called periodically (e.g., by a cron job or background worker).
func (s *RecurringService) ExecuteDueRecurringRules(ctx context.Context, db *gorm.DB) error {
	rules, err := s.repo.ListDueRecurringRules(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("failed to list due recurring rules: %w", err)
	}

	for _, rule := range rules {
		if err := s.executeRule(ctx, db, rule); err != nil {
			// Log error but continue with other rules
			continue
		}
	}

	return nil
}

// executeRule executes a single recurring rule.
func (s *RecurringService) executeRule(ctx context.Context, db *gorm.DB, rule model.FinanceRecurringRule) error {
	// Create transaction record
	tx := &model.FinanceTransaction{
		FamilyID:    rule.FamilyID,
		Type:        rule.Type,
		AmountCents: rule.AmountCents,
		AccountID:   rule.AccountID,
		CategoryID:  &rule.CategoryID,
		OccurredAt:  time.Now(),
		Description: fmt.Sprintf("周期记账: %s", rule.Name),
		Version:     1,
	}

	// Calculate next execution time based on cycle
	nextExecuteAt, err := calculateNextExecuteAt(rule.Cycle, rule.NextExecuteAt)
	if err != nil {
		return fmt.Errorf("failed to calculate next execution time: %w", err)
	}

	// Execute in a transaction
	return db.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		// Create transaction
		input := repo.CreateTransactionInput{
			Transaction: tx,
			ClientReqID: "", // No idempotency key for auto-generated transactions
			FamilyID:    rule.FamilyID,
			RequestData: rule,
		}

		if err := s.repo.CreateTransaction(ctx, input); err != nil {
			return fmt.Errorf("failed to create transaction for recurring rule: %w", err)
		}

		// Mark rule as executed and update next execution time
		if err := s.repo.MarkRecurringRuleExecuted(ctx, rule.ID, nextExecuteAt); err != nil {
			return fmt.Errorf("failed to mark rule as executed: %w", err)
		}

		// Publish event via outbox
		envelope := map[string]any{
			"source_system": "finance",
			"source_id":     tx.ID,
			"type":          tx.Type,
			"amount_cents":  tx.AmountCents,
			"account_id":    tx.AccountID,
			"category_id":   tx.CategoryID,
			"occurred_at":   tx.OccurredAt.Format(time.RFC3339),
			"description":   tx.Description,
			"is_recurring":  true,
			"recurring_rule_id": rule.ID,
		}

		envelopeJSON, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("failed to marshal envelope: %w", err)
		}

		if err := bus.InsertOutboxMessage(txDB, s.code, "finance.transaction.created", string(envelopeJSON)); err != nil {
			return fmt.Errorf("failed to insert outbox message: %w", err)
		}

		return nil
	})
}

// calculateNextExecuteAt calculates the next execution time based on the cycle.
func calculateNextExecuteAt(cycle string, lastExecuteAt time.Time) (time.Time, error) {
	switch cycle {
	case "daily":
		return lastExecuteAt.AddDate(0, 0, 1), nil
	case "weekly":
		return lastExecuteAt.AddDate(0, 0, 7), nil
	case "monthly":
		return lastExecuteAt.AddDate(0, 1, 0), nil
	case "yearly":
		return lastExecuteAt.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("invalid cycle: %s", cycle)
	}
}
