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
	retrieved, err := repo.GetTagByID(ctx, "test-family-001", tag.ID)
	assert.NoError(t, err)
	assert.Nil(t, retrieved) // Should be nil after soft delete
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
	err = repo.MarkRecurringRuleExecuted(ctx, rule.ID, nextExecuteAt)
	assert.NoError(t, err)

	// Verify update
	updated, err := repo.GetRecurringRuleByID(ctx, "test-family-001", rule.ID)
	assert.NoError(t, err)
	assert.NotNil(t, updated.LastExecutedAt)
	assert.Equal(t, nextExecuteAt, updated.NextExecuteAt)
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
			StartDate: time.Date(2024, i, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, i, 31, 0, 0, 0, 0, time.UTC),
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
	retrieved, err := repo.GetBudgetPeriodByID(ctx, "test-family-001", period.ID)
	assert.NoError(t, err)
	assert.Nil(t, retrieved) // Should be nil after soft delete
}
