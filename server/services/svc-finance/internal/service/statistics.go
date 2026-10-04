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
	// Count 是该分类下的流水笔数。客户端报表页渲染「N 笔」用的是这个字段，
	// 此前响应里没有它，页面就显示成「笔」（数字为空）。
	Count int64 `json:"count"`
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
		// 别名 `ft` 不是装饰：下面的 Select/Joins/Group 全用 `ft.` 前缀指主表。
		// 只写 `.Table("finance_transaction")` 而不起别名，GORM 生成的 SQL 里就没有 `ft`，
		// PostgreSQL 直接报 `missing FROM-clause entry for table "ft"`（SQLSTATE 42P01）——
		// 也就是说 `/statistics/category` **每次调用都 500**。
		Table("finance_transaction AS ft").
		Where("ft.family_id = ? AND ft.type = 'expense' AND ft.deleted_at IS NULL", familyID)

	// Apply period filter
	query = applyPeriodFilter(query, period)

	type result struct {
		CategoryID   string `gorm:"column:category_id"`
		CategoryName string `gorm:"column:category_name"`
		TotalAmount  int64  `gorm:"column:total_amount"`
		Count        int64  `gorm:"column:count"`
	}

	var results []result
	// `category_id` 是 uuid 列，**不能** COALESCE 成 ''：PostgreSQL 要求
	// COALESCE 的各分支类型可统一，uuid 与 unknown/text 字面量混用直接报
	// `invalid input syntax for type uuid: ""`（SQLSTATE 22P02）——
	// 即使该行 category_id 本来就非空，COALESCE 也会在规划期对字面量 '' 做类型转换。
	// 「未分类」这个语义由 LEFT JOIN 出来的 `fc.name IS NULL` 承担，见下面的 CASE。
	err := query.Select(`
		ft.category_id,
		CASE WHEN fc.name IS NULL THEN '未分类' ELSE fc.name END as category_name,
		COALESCE(SUM(ft.amount_cents), 0) as total_amount,
		COUNT(*) as count`).
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
			Count:        r.Count,
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
//
// P1 的诚实口径：**成员维度统计在当前 schema 下不成立，返回空列表而不是假数据。**
// 原因有两处，都不是可以顺手补一句 SQL 的笔误：
//  1. `finance_transaction` 里没有记录操作人的列（迁移 finance_0001_base_schema 的
//     17 列里只有 deleted_by，没有 created_by / member_id）。写侧也没有写入点，
//     所以「谁记的」这一事实在库里根本不存在 —— 任何按成员的聚合都会是编造。
//  2. 成员投影表 `finance_proj_homeos` 只有 (family_id, member_id, role, pver,
//     updated_at) 五列，既没有 `id` 也没有 `name`，连 `JOIN ... ON fm.id = ft.created_by`
//     与「成员名」都取不到。
//
// 旧实现假设了 `ft.created_by` 与 `fm.id`/`fm.name`，实测报
// `column ft.created_by does not exist`（SQLSTATE 42703），即该接口**每次调用都 500**。
// PRD 15.4 的成员维度要等 finance 侧补上「记账人」列（建议 member_id uuid）后再实现；
// 在那之前返回空切片并让页面走空态，比给一个恒 500 的接口或编造的归属好。
func (s *StatisticsService) GetMemberStats(ctx context.Context, familyID string, period string) ([]MemberStat, error) {
	if err := ValidatePeriod(period); err != nil {
		return nil, err
	}

	// 校验参数形状（familyID 非空、period 合法）已由上面的 ValidatePeriod 完成；
	// 真正缺的是 schema 里的记账人列，这里显式返回空集合而不是 nil。
	return []MemberStat{}, nil
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
