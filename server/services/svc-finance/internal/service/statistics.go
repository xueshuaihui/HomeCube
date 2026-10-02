// Package service provides business logic for the finance service.
package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"gorm.io/gorm"
)

// StatisticsService provides statistics calculation operations.
type StatisticsService struct {
	db *gorm.DB
}

// NewStatisticsService creates a new statistics service instance.
func NewStatisticsService(db *gorm.DB) *StatisticsService {
	return &StatisticsService{
		db: db,
	}
}

// ValidatePeriod validates the period parameter format.
// Valid formats: YYYY-MM, YYYY-Qn (n=1-4), YYYY
// Returns error if invalid, does not silently fallback to current month.
func ValidatePeriod(period string) error {
	if period == "" {
		return nil // Empty period means lifetime, which is valid
	}

	// YYYY-MM format
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}$`, period); matched {
		year := period[:4]
		month := period[5:7]
		y := parseInt(year)
		m := parseInt(month)
		if y < 1900 || y > 2100 || m < 1 || m > 12 {
			return fmt.Errorf("invalid period: %s", period)
		}
		return nil
	}

	// YYYY-Qn format (quarter)
	if matched, _ := regexp.MatchString(`^\d{4}-Q[1-4]$`, period); matched {
		year := period[:4]
		y := parseInt(year)
		if y < 1900 || y > 2100 {
			return fmt.Errorf("invalid period: %s", period)
		}
		return nil
	}

	// YYYY format (year)
	if matched, _ := regexp.MatchString(`^\d{4}$`, period); matched {
		year := period
		y := parseInt(year)
		if y < 1900 || y > 2100 {
			return fmt.Errorf("invalid period: %s", period)
		}
		return nil
	}

	return fmt.Errorf("invalid period format: %s (expected YYYY-MM, YYYY-Qn, or YYYY)", period)
}

// parseInt converts string to int, returns 0 on error.
func parseInt(s string) int {
	var result int
	fmt.Sscanf(s, "%d", &result)
	return result
}

// OverviewStats represents the overview statistics view.
type OverviewStats struct {
	TotalIncome  int64 `json:"total_income"`
	TotalExpense int64 `json:"total_expense"`
	NetBalance   int64 `json:"net_balance"`
	AccountCount int   `json:"account_count"`
}

// GetOverview calculates the overview statistics for a family within a period.
func (s *StatisticsService) GetOverview(ctx context.Context, familyID string, period string) (*OverviewStats, error) {
	if err := ValidatePeriod(period); err != nil {
		return nil, err
	}

	query := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	// Apply period filter
	query = applyPeriodFilter(query, period)

	var totalIncome, totalExpense int64

	// Calculate income
	err := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND deleted_at IS NULL AND type = 'income'", familyID).
		Select("COALESCE(SUM(amount_cents), 0)").
		Scan(&totalIncome).Error
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total income: %w", err)
	}

	// Calculate expense
	err = s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND deleted_at IS NULL AND type = 'expense'", familyID).
		Select("COALESCE(SUM(amount_cents), 0)").
		Scan(&totalExpense).Error
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total expense: %w", err)
	}

	// Count active accounts
	var accountCount int64
	err = s.db.WithContext(ctx).
		Model(&struct{}{}).
		Table("finance_account").
		Where("family_id = ? AND deleted_at IS NULL AND is_archived = false", familyID).
		Count(&accountCount).Error
	if err != nil {
		return nil, fmt.Errorf("failed to count accounts: %w", err)
	}

	return &OverviewStats{
		TotalIncome:  totalIncome,
		TotalExpense: totalExpense,
		NetBalance:   totalIncome + totalExpense, // Expense is negative
		AccountCount: int(accountCount),
	}, nil
}

// TrendPoint represents a single point in the trend view.
type TrendPoint struct {
	Period  string `json:"period"`
	Income  int64  `json:"income"`
	Expense int64  `json:"expense"`
}

// GetTrend calculates the trend statistics aggregated by granularity.
// Granularity: day, week, month
func (s *StatisticsService) GetTrend(ctx context.Context, familyID string, period string, granularity string) ([]TrendPoint, error) {
	if err := ValidatePeriod(period); err != nil {
		return nil, err
	}

	if granularity == "" {
		granularity = "month" // Default to month
	}

	// Validate granularity
	if granularity != "day" && granularity != "week" && granularity != "month" {
		return nil, errors.New("invalid granularity: must be day, week, or month")
	}

	query := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	// Apply period filter
	query = applyPeriodFilter(query, period)

	type result struct {
		PeriodLabel string `gorm:"column:period_label"`
		Income      int64  `gorm:"column:income"`
		Expense     int64  `gorm:"column:expense"`
	}

	var selectExpr string
	switch granularity {
	case "day":
		selectExpr = "TO_CHAR(occurred_at, 'YYYY-MM-DD') as period_label"
	case "week":
		selectExpr = "TO_CHAR(occurred_at, 'IYYY-IW') as period_label"
	case "month":
		selectExpr = "TO_CHAR(occurred_at, 'YYYY-MM') as period_label"
	}

	var results []result
	err := query.Select(selectExpr + `,
		COALESCE(SUM(CASE WHEN type = 'income' THEN amount_cents ELSE 0 END), 0) as income,
		COALESCE(SUM(CASE WHEN type = 'expense' THEN amount_cents ELSE 0 END), 0) as expense`).
		Group("period_label").
		Order("period_label ASC").
		Scan(&results).Error

	if err != nil {
		return nil, fmt.Errorf("failed to calculate trend: %w", err)
	}

	points := make([]TrendPoint, len(results))
	for i, r := range results {
		points[i] = TrendPoint{
			Period:  r.PeriodLabel,
			Income:  r.Income,
			Expense: r.Expense,
		}
	}

	return points, nil
}

// CategoryStat represents a category's spending statistics.
type CategoryStat struct {
	CategoryID   string  `json:"category_id"`
	CategoryName string  `json:"category_name"`
	Amount       int64   `json:"amount"`
	Percentage   float64 `json:"percentage"`
}

// GetCategoryStats calculates spending breakdown by category.
func (s *StatisticsService) GetCategoryStats(ctx context.Context, familyID string, period string) ([]CategoryStat, error) {
	if err := ValidatePeriod(period); err != nil {
		return nil, err
	}

	query := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND type = 'expense' AND deleted_at IS NULL", familyID)

	// Apply period filter
	query = applyPeriodFilter(query, period)

	type result struct {
		CategoryID   string `gorm:"column:category_id"`
		CategoryName string `gorm:"column:category_name"`
		TotalAmount  int64  `gorm:"column:total_amount"`
	}

	var results []result
	err := query.Select(`
		COALESCE(ft.category_id, '') as category_id,
		COALESCE(fc.name, '未分类') as category_name,
		COALESCE(SUM(ft.amount_cents), 0) as total_amount`).
		Joins("LEFT JOIN finance_category fc ON ft.category_id = fc.id").
		Group("ft.category_id, fc.name").
		Order("total_amount DESC").
		Scan(&results).Error

	if err != nil {
		return nil, fmt.Errorf("failed to calculate category stats: %w", err)
	}

	// Calculate total for percentage
	var totalExpense int64
	for _, r := range results {
		totalExpense += r.TotalAmount
	}

	stats := make([]CategoryStat, len(results))
	for i, r := range results {
		percentage := float64(0)
		if totalExpense != 0 {
			percentage = float64(r.TotalAmount) / float64(totalExpense) * 100
		}
		stats[i] = CategoryStat{
			CategoryID:   r.CategoryID,
			CategoryName: r.CategoryName,
			Amount:       r.TotalAmount,
			Percentage:   percentage,
		}
	}

	return stats, nil
}

// MemberStat represents a member's contribution statistics.
type MemberStat struct {
	MemberID   string `json:"member_id"`
	MemberName string `json:"member_name"`
	Income     int64  `json:"income"`
	Expense    int64  `json:"expense"`
	Net        int64  `json:"net"`
}

// GetMemberStats calculates contribution ranking by member.
func (s *StatisticsService) GetMemberStats(ctx context.Context, familyID string, period string) ([]MemberStat, error) {
	if err := ValidatePeriod(period); err != nil {
		return nil, err
	}

	query := s.db.WithContext(ctx).
		Model(&struct {
			AmountCents int64 `gorm:"column:amount_cents"`
		}{}).
		Table("finance_transaction").
		Where("family_id = ? AND deleted_at IS NULL", familyID)

	// Apply period filter
	query = applyPeriodFilter(query, period)

	type result struct {
		MemberID   string `gorm:"column:member_id"`
		MemberName string `gorm:"column:member_name"`
		Income     int64  `gorm:"column:income"`
		Expense    int64  `gorm:"column:expense"`
	}

	var results []result
	err := query.Select(`
		COALESCE(ft.created_by, '') as member_id,
		COALESCE(fm.name, '未知') as member_name,
		COALESCE(SUM(CASE WHEN ft.type = 'income' THEN ft.amount_cents ELSE 0 END), 0) as income,
		COALESCE(SUM(CASE WHEN ft.type = 'expense' THEN ft.amount_cents ELSE 0 END), 0) as expense`).
		Joins("LEFT JOIN finance_proj_homeos fm ON ft.created_by = fm.id").
		Group("ft.created_by, fm.name").
		Order("income + expense DESC").
		Scan(&results).Error

	if err != nil {
		return nil, fmt.Errorf("failed to calculate member stats: %w", err)
	}

	stats := make([]MemberStat, len(results))
	for i, r := range results {
		stats[i] = MemberStat{
			MemberID:   r.MemberID,
			MemberName: r.MemberName,
			Income:     r.Income,
			Expense:    r.Expense,
			Net:        r.Income + r.Expense,
		}
	}

	return stats, nil
}

// applyPeriodFilter applies period filter to a GORM query.
func applyPeriodFilter(query *gorm.DB, period string) *gorm.DB {
	if period == "" {
		return query
	}

	switch {
	case len(period) == 7 && period[4] == '-' && period[5] != 'Q': // YYYY-MM
		return query.Where("TO_CHAR(occurred_at, 'YYYY-MM') = ?", period)
	case len(period) == 7 && period[5] == 'Q': // YYYY-Qn
		year := period[:4]
		quarter := period[6:]
		return query.Where("TO_CHAR(occurred_at, 'YYYY') = ? AND EXTRACT(QUARTER FROM occurred_at) = ?", year, quarter)
	case len(period) == 4: // YYYY
		return query.Where("TO_CHAR(occurred_at, 'YYYY') = ?", period)
	default:
		return query
	}
}
