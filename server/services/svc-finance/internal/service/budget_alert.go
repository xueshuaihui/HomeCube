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

// BudgetAlertService handles budget exceeded detection and event publishing.
type BudgetAlertService struct {
	repo *repo.FinanceRepo
	db   *gorm.DB
}

// NewBudgetAlertService creates a new budget alert service.
func NewBudgetAlertService(repo *repo.FinanceRepo, db *gorm.DB) *BudgetAlertService {
	return &BudgetAlertService{
		repo: repo,
		db:   db,
	}
}

// CheckAndAlert checks if expenses exceed the budget and publishes an event if so.
// This should be called within a transaction after creating/updating transactions.
func (s *BudgetAlertService) CheckAndAlert(ctx context.Context, tx *gorm.DB, familyID, categoryID string, startDate, endDate time.Time, period string) error {
	// Get total spent for this category in the period
	totalSpent, _, err := s.repo.CheckExceeded(ctx, familyID, categoryID, startDate, endDate)
	if err != nil {
		return fmt.Errorf("failed to check exceeded budget: %w", err)
	}

	// Get all active budgets for this category and period
	budgets, err := s.repo.ListBudgetsByFamily(ctx, familyID, &period)
	if err != nil {
		return fmt.Errorf("failed to list budgets: %w", err)
	}

	// Check each budget
	for _, budget := range budgets {
		if budget.CategoryID != categoryID || !budget.IsActive {
			continue
		}

		// Check if spent exceeds budget
		if totalSpent > budget.AmountCents {
			// Publish budget exceeded event
			err := s.publishBudgetExceededEvent(ctx, tx, &budget, totalSpent, period)
			if err != nil {
				return fmt.Errorf("failed to publish budget exceeded event: %w", err)
			}
		}
	}

	return nil
}

// publishBudgetExceededEvent publishes a finance.budget.exceeded event via outbox.
func (s *BudgetAlertService) publishBudgetExceededEvent(ctx context.Context, tx *gorm.DB, budget *model.FinanceBudget, totalSpent int64, period string) error {
	// business_id = {budget_id}:{period} as per PRD 10.4
	businessID := fmt.Sprintf("%s:%s", budget.ID, period)

	envelope := bus.Envelope{
		EventType:  "finance.budget.exceeded",
		BusinessID: businessID,
		FamilyID:   budget.FamilyID,
		Payload: map[string]any{
			"budget_id":    budget.ID,
			"category_id":  budget.CategoryID,
			"amount_cents": budget.AmountCents,
			"spent_cents":  totalSpent,
			"period":       period,
			"start_date":   budget.StartDate.Format("2006-01-02"),
			"end_date":     budget.EndDate.Format("2006-01-02"),
		},
		Version:   "1.0",
		Timestamp: time.Now().Format(time.RFC3339),
	}

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// Insert into outbox table within the same transaction. The budget's own family goes into the row's
	// family_id column (finance_0006's finance_budget.family_id is NOT NULL, §10.3「指标最小集（全部带
	// family_id 与 code）」), and it is the same value the envelope already carries.
	err = bus.InsertOutboxMessageWithFamily(tx, "finance", budget.FamilyID,
		"finance.budget.exceeded", string(envelopeJSON))
	if err != nil {
		return fmt.Errorf("failed to insert outbox message: %w", err)
	}

	return nil
}
