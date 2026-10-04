package repo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Every repo write appends a row to the 同步底座 change log (packages/sync.Repo.
	// AppendChangeLog), so the fixture has to hold finance_change_log or the transaction
	// rolls back with "no such table". Column set is exactly the published DDL of
	// migrations/finance/finance_0003_change_log_idempotency.up.sql:12-22 -- the six
	// columns (lsn, family_id, entity, entity_id, op, version) plus the (family_id, lsn)
	// index. No data / created_at column: FS1 removed them, the object body stays in the
	// business table and the delta reader re-fetches it by (entity, entity_id).
	// lsn is a bigserial in Postgres; INTEGER PRIMARY KEY AUTOINCREMENT is the sqlite
	// equivalent that lets the database mint it, which is what the repo relies on.
	require.NoError(t, db.Exec(`
CREATE TABLE finance_change_log (
	lsn       INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id TEXT    NOT NULL,
	entity    TEXT    NOT NULL,
	entity_id TEXT    NOT NULL,
	op        TEXT    NOT NULL,
	version   INTEGER NOT NULL
)`).Error)
	require.NoError(t, db.Exec(`
CREATE INDEX finance_change_log_family_lsn_idx ON finance_change_log (family_id, lsn)`).Error)

	// Auto migrate test tables
	err = db.AutoMigrate(
		&model.FinanceTag{},
		&model.FinanceRecurringRule{},
		&model.FinanceBudgetPeriod{},
	)
	require.NoError(t, err)

	return db
}

func TestCreateTag(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	tag := &model.FinanceTag{
		FamilyID: "test-family-001",
		Name:     "Test Tag",
		Color:    "#FF5733",
	}

	err := repo.CreateTag(ctx, tag)
	assert.NoError(t, err)
	assert.NotEmpty(t, tag.ID)
}

func TestGetTagByID(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create a tag first
	tag := &model.FinanceTag{
		FamilyID: "test-family-001",
		Name:     "Test Tag",
		Color:    "#FF5733",
	}
	err := repo.CreateTag(ctx, tag)
	require.NoError(t, err)

	// Get the tag
	retrieved, err := repo.GetTagByID(ctx, "test-family-001", tag.ID)
	assert.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, tag.Name, retrieved.Name)
	assert.Equal(t, tag.Color, retrieved.Color)
}

func TestListTagsByFamily(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create multiple tags
	for i := 1; i <= 3; i++ {
		tag := &model.FinanceTag{
			FamilyID: "test-family-001",
			Name:     "Tag " + string(rune('0'+i)),
			Color:    "#FF5733",
		}
		err := repo.CreateTag(ctx, tag)
		require.NoError(t, err)
	}

	// List tags
	tags, err := repo.ListTagsByFamily(ctx, "test-family-001")
	assert.NoError(t, err)
	assert.Len(t, tags, 3)
}

func TestUpdateTag(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create a tag
	tag := &model.FinanceTag{
		FamilyID: "test-family-001",
		Name:     "Original Name",
		Color:    "#FF5733",
	}
	err := repo.CreateTag(ctx, tag)
	require.NoError(t, err)

	// Update the tag
	tag.Name = "Updated Name"
	tag.Color = "#00FF00"
	err = repo.UpdateTag(ctx, tag)
	assert.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetTagByID(ctx, "test-family-001", tag.ID)
	assert.NoError(t, err)
	assert.Equal(t, "Updated Name", retrieved.Name)
	assert.Equal(t, "#00FF00", retrieved.Color)
}

func TestDeleteTag(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create a tag
	tag := &model.FinanceTag{
		FamilyID: "test-family-001",
		Name:     "To Delete",
		Color:    "#FF5733",
	}
	err := repo.CreateTag(ctx, tag)
	require.NoError(t, err)

	// Delete the tag
	err = repo.DeleteTag(ctx, "test-family-001", tag.ID)
	assert.NoError(t, err)

	// Verify deletion
	// 软删除后「取不到」用哨兵错误表达（errors.Is + ErrNotFound），不是 (nil, nil)。
	// 旧断言是 `assert.NoError` + `assert.Nil(retrieved)`，那等于把
	// 「repo 用 nil 表达不存在」这条隐患写进了契约：调用方的 `if err != nil` 抓不到，
	// 随后解引用 nil 就 panic（实测 DELETE /transactions/{id} 打不存在的 id 即 500 空体）。
	retrieved, err := repo.GetTagByID(ctx, "test-family-001", tag.ID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, retrieved)
}

func TestCreateRecurringRule(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	now := time.Now()
	rule := &model.FinanceRecurringRule{
		FamilyID:      "test-family-001",
		Name:          "Monthly Rent",
		Type:          "expense",
		AmountCents:   500000,
		AccountID:     "account-001",
		CategoryID:    "category-001",
		Cycle:         "monthly",
		StartDate:     now,
		NextExecuteAt: now.AddDate(0, 1, 0),
		IsActive:      true,
		Description:   "Monthly rent payment",
	}

	err := repo.CreateRecurringRule(ctx, rule)
	assert.NoError(t, err)
	assert.NotEmpty(t, rule.ID)
}

func TestListDueRecurringRules(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	now := time.Now()

	// Create a due rule
	dueRule := &model.FinanceRecurringRule{
		FamilyID:      "test-family-001",
		Name:          "Due Rule",
		Type:          "expense",
		AmountCents:   10000,
		AccountID:     "account-001",
		CategoryID:    "category-001",
		Cycle:         "monthly",
		StartDate:     now.AddDate(0, 0, -30),
		NextExecuteAt: now.Add(-1 * time.Hour), // Due in the past
		IsActive:      true,
	}
	err := repo.CreateRecurringRule(ctx, dueRule)
	require.NoError(t, err)

	// Create a future rule
	futureRule := &model.FinanceRecurringRule{
		FamilyID:      "test-family-001",
		Name:          "Future Rule",
		Type:          "expense",
		AmountCents:   20000,
		AccountID:     "account-001",
		CategoryID:    "category-001",
		Cycle:         "monthly",
		StartDate:     now,
		NextExecuteAt: now.AddDate(0, 1, 0), // Due in the future
		IsActive:      true,
	}
	err = repo.CreateRecurringRule(ctx, futureRule)
	require.NoError(t, err)

	// List due rules
	rules, err := repo.ListDueRecurringRules(ctx, now)
	assert.NoError(t, err)
	assert.Len(t, rules, 1)
	assert.Equal(t, "Due Rule", rules[0].Name)
}

func TestMarkRecurringRuleExecuted(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	now := time.Now()
	rule := &model.FinanceRecurringRule{
		FamilyID:      "test-family-001",
		Name:          "Test Rule",
		Type:          "expense",
		AmountCents:   10000,
		AccountID:     "account-001",
		CategoryID:    "category-001",
		Cycle:         "monthly",
		StartDate:     now,
		NextExecuteAt: now,
		IsActive:      true,
	}
	err := repo.CreateRecurringRule(ctx, rule)
	require.NoError(t, err)

	// Mark as executed
	nextExecuteAt := now.AddDate(0, 1, 0)
	err = repo.MarkRecurringRuleExecuted(ctx, "test-family-001", rule.ID, nextExecuteAt)
	assert.NoError(t, err)

	// Verify update
	updated, err := repo.GetRecurringRuleByID(ctx, "test-family-001", rule.ID)
	assert.NoError(t, err)
	assert.NotNil(t, updated.LastExecutedAt)
	// The column is written and read back through the driver, which returns the instant
	// with a fixed-offset Location instead of time.Local, so compare instants (same
	// convention as bill_test.go's due_at check). Same point in time is the requirement:
	// a different instant, or the old value still sitting there, still fails here.
	assert.True(t, nextExecuteAt.Equal(updated.NextExecuteAt),
		"next_execute_at 没有被持久化成传入的时刻: want %s, got %s", nextExecuteAt, updated.NextExecuteAt)
}

func TestCreateBudgetPeriod(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	period := &model.FinanceBudgetPeriod{
		FamilyID:  "test-family-001",
		Name:      "2024-01",
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		IsActive:  true,
	}

	err := repo.CreateBudgetPeriod(ctx, period)
	assert.NoError(t, err)
	assert.NotEmpty(t, period.ID)
}

func TestListBudgetPeriodsByFamily(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create multiple periods
	for i := 1; i <= 3; i++ {
		period := &model.FinanceBudgetPeriod{
			FamilyID:  "test-family-001",
			Name:      "2024-" + string(rune('0'+i)),
			StartDate: time.Date(2024, time.Month(i), 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, time.Month(i), 28, 0, 0, 0, 0, time.UTC),
			IsActive:  true,
		}
		err := repo.CreateBudgetPeriod(ctx, period)
		require.NoError(t, err)
	}

	// List periods
	periods, err := repo.ListBudgetPeriodsByFamily(ctx, "test-family-001")
	assert.NoError(t, err)
	assert.Len(t, periods, 3)
}

func TestUpdateBudgetPeriod(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create a period
	period := &model.FinanceBudgetPeriod{
		FamilyID:  "test-family-001",
		Name:      "Original Period",
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		IsActive:  true,
	}
	err := repo.CreateBudgetPeriod(ctx, period)
	require.NoError(t, err)

	// Update the period
	period.Name = "Updated Period"
	period.IsActive = false
	err = repo.UpdateBudgetPeriod(ctx, period)
	assert.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetBudgetPeriodByID(ctx, "test-family-001", period.ID)
	assert.NoError(t, err)
	assert.Equal(t, "Updated Period", retrieved.Name)
	assert.False(t, retrieved.IsActive)
}

func TestDeleteBudgetPeriod(t *testing.T) {
	db := setupTestDB(t)
	repo := NewFinanceRepo(db)
	ctx := context.Background()

	// Create a period
	period := &model.FinanceBudgetPeriod{
		FamilyID:  "test-family-001",
		Name:      "To Delete",
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		IsActive:  true,
	}
	err := repo.CreateBudgetPeriod(ctx, period)
	require.NoError(t, err)

	// Delete the period
	err = repo.DeleteBudgetPeriod(ctx, "test-family-001", period.ID)
	assert.NoError(t, err)

	// Verify deletion
	// 同上：不存在的语义是 ErrNotFound，而不是 (nil, nil)。
	retrieved, err := repo.GetBudgetPeriodByID(ctx, "test-family-001", period.ID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, retrieved)
}
