// Package service provides business logic for the finance service.
package service

import (
	"context"
	"fmt"

	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// BalanceService provides balance calculation operations.
type BalanceService struct {
	db   *gorm.DB
	repo *repo.FinanceRepo
}

// NewBalanceService creates a new balance service instance.
func NewBalanceService(db *gorm.DB, repo *repo.FinanceRepo) *BalanceService {
	return &BalanceService{
		db:   db,
		repo: repo,
	}
}

// CalculateAccountBalance calculates the balance for a single account by summing amount_cents.
// This is a derived value computed on-demand, not an incremental counter.
func (s *BalanceService) CalculateAccountBalance(ctx context.Context, accountID string, familyID string) (int64, error) {
	var total int64
	err := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Select("COALESCE(SUM(amount_cents), 0)").
		Where("account_id = ? AND family_id = ? AND deleted_at IS NULL", accountID, familyID).
		Scan(&total).Error

	if err != nil {
		return 0, fmt.Errorf("failed to calculate account balance: %w", err)
	}

	return total, nil
}

// GetAccountsBalance calculates balances for multiple accounts in a family within a specified period.
// Period format: YYYY-MM (month), YYYY-Qn (quarter), YYYY (year).
// If period is empty, calculates lifetime balance.
func (s *BalanceService) GetAccountsBalance(ctx context.Context, familyID string, period string) (map[string]int64, error) {
	type result struct {
		AccountID   string `gorm:"column:account_id"`
		TotalAmount int64  `gorm:"column:total_amount"`
	}

	query := s.db.WithContext(ctx).
		Model(&struct {
			AccountID   string `gorm:"column:account_id"`
			AmountCents int64  `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Select("account_id, COALESCE(SUM(amount_cents), 0) as total_amount").
		Where("family_id = ? AND deleted_at IS NULL", familyID).
		Group("account_id")

	// Apply period filter if provided
	if period != "" {
		switch {
		case len(period) == 7 && period[4] == '-': // YYYY-MM
			query = query.Where("TO_CHAR(occurred_at, 'YYYY-MM') = ?", period)
		case len(period) == 7 && period[4] == '-' && period[5] == 'Q': // YYYY-Qn (invalid format, handled below)
			// This case won't match because period[5] would be 'Q' not a digit
		case len(period) == 4: // YYYY
			query = query.Where("TO_CHAR(occurred_at, 'YYYY') = ?", period)
		case len(period) == 7 && period[5] == 'Q': // YYYY-Qn
			// Extract year and quarter
			year := period[:4]
			quarter := period[6:]
			query = query.Where("TO_CHAR(occurred_at, 'YYYY') = ? AND EXTRACT(QUARTER FROM occurred_at) = ?", year, quarter)
		}
	}

	var results []result
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to calculate accounts balance: %w", err)
	}

	balances := make(map[string]int64)
	for _, r := range results {
		balances[r.AccountID] = r.TotalAmount
	}

	return balances, nil
}
