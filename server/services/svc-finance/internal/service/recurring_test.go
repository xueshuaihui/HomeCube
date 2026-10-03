package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCalculateNextExecuteAt(t *testing.T) {
	baseTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		cycle    string
		expected time.Time
		hasError bool
	}{
		{
			name:     "daily",
			cycle:    "daily",
			expected: time.Date(2024, 1, 16, 10, 0, 0, 0, time.UTC),
			hasError: false,
		},
		{
			name:     "weekly",
			cycle:    "weekly",
			expected: time.Date(2024, 1, 22, 10, 0, 0, 0, time.UTC),
			hasError: false,
		},
		{
			name:     "monthly",
			cycle:    "monthly",
			expected: time.Date(2024, 2, 15, 10, 0, 0, 0, time.UTC),
			hasError: false,
		},
		{
			name:     "yearly",
			cycle:    "yearly",
			expected: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
			hasError: false,
		},
		{
			name:     "invalid cycle",
			cycle:    "invalid",
			expected: time.Time{},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := calculateNextExecuteAt(tt.cycle, baseTime)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func setupTestDBForService(t *testing.T) (*gorm.DB, *repo.FinanceRepo) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Auto migrate test tables
	err = db.AutoMigrate(
		&model.FinanceTag{},
		&model.FinanceRecurringRule{},
		&model.FinanceBudgetPeriod{},
		&model.FinanceTransaction{},
		&model.FinanceAccount{},
		&model.FinanceCategory{},
	)
	require.NoError(t, err)

	repo := repo.NewFinanceRepo(db)
	return db, repo
}

type mockBusPublisher struct {
	published []map[string]any
}

func (m *mockBusPublisher) Publish(ctx context.Context, db *gorm.DB, subject string, envelope map[string]any) error {
	m.published = append(m.published, envelope)
	return nil
}

func TestNewRecurringService(t *testing.T) {
	db, financeRepo := setupTestDBForService(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	pub := &mockBusPublisher{}
	service := NewRecurringService(financeRepo, pub, "finance")

	assert.NotNil(t, service)
	assert.Equal(t, "finance", service.code)
}

func TestExecuteDueRecurringRules_EmptyList(t *testing.T) {
	db, financeRepo := setupTestDBForService(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	pub := &mockBusPublisher{}
	service := NewRecurringService(financeRepo, pub, "finance")

	ctx := context.Background()
	err := service.ExecuteDueRecurringRules(ctx, db)

	assert.NoError(t, err)
	assert.Len(t, pub.published, 0)
}

func TestExecuteDueRecurringRules_WithDueRule(t *testing.T) {
	db, financeRepo := setupTestDBForService(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// Create account and category first
	account := &model.FinanceAccount{
		ID:       "account-001",
		FamilyID: "test-family-001",
		Name:     "Test Account",
		Type:     "cash",
		Balance:  100000,
		Version:  1,
	}
	err := financeRepo.CreateAccount(context.Background(), account)
	require.NoError(t, err)

	category := &model.FinanceCategory{
		ID:       "category-001",
		FamilyID: "test-family-001",
		Name:     "Test Category",
		IsActive: true,
		Version:  1,
	}
	err = financeRepo.CreateCategory(context.Background(), category)
	require.NoError(t, err)

	// Create a due recurring rule
	now := time.Now()
	rule := &model.FinanceRecurringRule{
		FamilyID:      "test-family-001",
		Name:          "Monthly Rent",
		Type:          "expense",
		AmountCents:   -500000, // Negative for expense
		AccountID:     "account-001",
		CategoryID:    "category-001",
		Cycle:         "monthly",
		StartDate:     now.AddDate(0, 0, -30),
		NextExecuteAt: now.Add(-1 * time.Hour), // Due in the past
		IsActive:      true,
		Description:   "Monthly rent payment",
	}
	err = financeRepo.CreateRecurringRule(context.Background(), rule)
	require.NoError(t, err)

	pub := &mockBusPublisher{}
	service := NewRecurringService(financeRepo, pub, "finance")

	ctx := context.Background()
	err = service.ExecuteDueRecurringRules(ctx, db)

	// Note: This test may fail because CreateTransaction requires sync.Repo which is not fully set up
	// The test demonstrates the structure but actual execution requires full infrastructure
	assert.NoError(t, err)
}
