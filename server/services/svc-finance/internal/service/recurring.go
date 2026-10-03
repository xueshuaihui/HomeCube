// Package service provides business logic for the finance service.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// subjectTransactionCreated is the event name of contracts/events/finance.yaml:17
// finance.transaction.created (§3.1「subject 命名即事件名：{code}.{object}.{action}」). Declared once so
// the outbox row's subject column and the envelope's event_type can never drift apart -- before this
// constant existed the subject was typed as a literal while the envelope had no event_type at all.
const subjectTransactionCreated = "finance.transaction.created"

// BusPublisher is an interface for publishing events via outbox.
type BusPublisher interface {
	Publish(ctx context.Context, db *gorm.DB, subject string, envelope map[string]any) error
}

// RecurringService handles recurring transaction execution.
type RecurringService struct {
	repo *repo.FinanceRepo
	pub  BusPublisher
	code string // service code for outbox table naming
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

		// Publish event via outbox.
		envelopeJSON, err := bus.MarshalEnvelope(transactionCreatedEnvelope(tx, rule))
		if err != nil {
			return fmt.Errorf("failed to marshal envelope: %w", err)
		}

		// The row carries the rule's family: finance_0012's finance_recurring_rules.family_id is NOT NULL,
		// and §10.3「指标最小集（全部带 family_id 与 code）」wants the same attribution on the event, so
		// this is not one of §2.2's family-less 系统预置 objects that would keep family_id NULL.
		if err := bus.InsertOutboxMessageWithFamily(txDB, s.code, rule.FamilyID,
			subjectTransactionCreated, string(envelopeJSON)); err != nil {
			return fmt.Errorf("failed to insert outbox message: %w", err)
		}

		return nil
	})
}

// transactionCreatedEnvelope builds the bus.Envelope of one finance.transaction.created event for a
// recurring-rule execution: the exact value executeRule marshals into the outbox row's envelope column.
// It is a function rather than an inline literal so the contract test can put the production bytes in
// front of a decoder without needing a live database (ENV-1).
//
// Same base envelope type as every other producer in this service (budget_alert.go:68-83 and
// bill.go): the event data goes into bus.Envelope's `payload` object, because
// packages/bus/consumer.go:151-157 unmarshals the delivery into bus.Envelope and a flat map -- what
// this function replaced -- would arrive there with Payload == nil.
//
// business_id = {transaction_id} per contracts/events/finance.yaml:35, an event contracts/README.md:33-37
// classes as 一次性语义: the row this event announces is minted by this very execution and can never
// legitimately repeat, so the object id alone is the dedupe key (unlike finance.due.registered, whose
// {source_id}:{due_at} key exists precisely so a reschedule can re-fire). An empty business_id -- what
// the old literal produced -- trips packages/bus/consumer.go:210-215, which NAKs that delivery WITHOUT
// writing a dead-letter row: the event vanishes silently.
func transactionCreatedEnvelope(tx *model.FinanceTransaction, rule model.FinanceRecurringRule) bus.Envelope {
	return bus.Envelope{
		EventType:  subjectTransactionCreated,
		BusinessID: tx.ID,
		FamilyID:   rule.FamilyID,
		Payload: map[string]any{
			// contracts/events/finance.yaml:25-31 declares transaction_id / family_id / type /
			// amount_cents / category_id / account_id / occurred_at / created_by. The first seven are
			// emitted here; created_by is NOT, because neither FinanceTransaction nor FinanceRecurringRule
			// has a creator column and this path is machine-triggered -- an invented uuid would be worse
			// than an absent one. Reported as contract drift.
			"transaction_id": tx.ID,
			"family_id":      tx.FamilyID,
			"type":           tx.Type,
			"amount_cents":   tx.AmountCents,
			"account_id":     tx.AccountID,
			"category_id":    tx.CategoryID,
			"occurred_at":    tx.OccurredAt.UTC().Format(time.RFC3339),
			// Provenance of the recurring path that the frozen contract has no slot for; kept as it was
			// (reported as contract drift, not invented). source_system/source_id are gone: they are
			// finance.due.registered's vocabulary, and this event's object id is transaction_id.
			"description":       tx.Description,
			"is_recurring":      true,
			"recurring_rule_id": rule.ID,
		},
		Version:   envelopeSchemaVersion,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
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
