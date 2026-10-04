// Package service provides business logic for the finance service.
package service

import (
	"context"
	"fmt"

	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
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

// RecalcAccountBalance 重新计算并**写回** finance_account.balance。
//
// 为什么需要「写回」：CalculateAccountBalance 是纯读，而 finance_account.balance 这一列
// 被建表时就给了（bigint，注释写着账户余额），于是它成了**没人维护的陈旧缓存** ——
// 记账不写它、删除不写它、只有「查余额」那个 GET 接口会现算现答。
// G4 数据门禁实测：12 个账户里有 6 个有流水却 balance = 0，漂移最大 57200 分（¥572）。
//
// 后果不是「显示不好看」：任何直接读 balance 列的地方（报表、导出、预算可用额度、
// 归档前的余额校验）拿到的都是错的数，且与 API 返回值对不上 ——
// 同一个账户，GET /accounts/{id}/balance 答 -572，GET /accounts 答 0。
//
// 这里用「重算」而不是「增量加减」：增量在撤销/修改/软删除恢复时会漂移，
// 而流水是软删的、可恢复的，重算永远是 SUM(non-deleted)，不需要维护加减逻辑。
func (s *BalanceService) RecalcAccountBalance(ctx context.Context, accountID string, familyID string) (int64, error) {
	total, err := s.CalculateAccountBalance(ctx, accountID, familyID)
	if err != nil {
		return 0, err
	}
	if err := s.db.WithContext(ctx).
		Model(&model.FinanceAccount{}).
		Where("id = ? AND family_id = ?", accountID, familyID).
		Update("balance", total).Error; err != nil {
		return 0, fmt.Errorf("failed to persist account balance: %w", err)
	}
	return total, nil
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
