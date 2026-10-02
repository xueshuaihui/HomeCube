package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	// Auto-migrate tables
	err = db.AutoMigrate(&model.FinanceTransaction{}, &model.FinanceAccount{}, &model.FinanceCategory{})
	assert.NoError(t, err)

	return db
}

func TestValidateExportParams(t *testing.T) {
	tests := []struct {
		name       string
		typeFilter string
		days       int
		format     string
		wantErr    bool
	}{
		{"valid income csv", "income", 30, "csv", false},
		{"valid expense xlsx", "expense", 7, "xlsx", false},
		{"valid transfer csv", "transfer", 14, "csv", false},
		{"valid all xlsx", "all", 365, "xlsx", false},
		{"invalid type", "invalid", 30, "csv", true},
		{"invalid days zero", "income", 0, "csv", true},
		{"invalid days negative", "income", -1, "csv", true},
		{"invalid days too large", "income", 366, "csv", true},
		{"invalid format", "income", 30, "pdf", true},
		{"empty type", "", 30, "csv", true},
		{"empty format", "income", 30, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExportParams(tt.typeFilter, tt.days, tt.format)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExportService_ExportTransactions_CSV(t *testing.T) {
	db := setupTestDB(t)
	service := NewExportService(db)

	// Insert test data
	familyID := "test-family-001"
	now := time.Now()
	categoryID := "cat-001"
	accountID := "acc-001"

	transactions := []model.FinanceTransaction{
		{
			ID:          "txn-001",
			FamilyID:    familyID,
			Type:        "expense",
			AmountCents: -5000, // -50.00 yuan
			CategoryID:  &categoryID,
			AccountID:   accountID,
			OccurredAt:  now.Add(-24 * time.Hour),
			Description: "餐饮支出",
			Version:     1,
		},
		{
			ID:          "txn-002",
			FamilyID:    familyID,
			Type:        "income",
			AmountCents: 10000, // 100.00 yuan
			CategoryID:  &categoryID,
			AccountID:   accountID,
			OccurredAt:  now.Add(-48 * time.Hour),
			Description: "工资收入",
			Version:     1,
		},
	}

	for _, txn := range transactions {
		err := db.Create(&txn).Error
		assert.NoError(t, err)
	}

	// Test CSV export
	ctx := context.Background()
	data, filename, err := service.ExportTransactions(ctx, familyID, "all", 7, "csv")

	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Contains(t, filename, ".csv")
	assert.Contains(t, filename, "all")
	assert.Contains(t, filename, "7days")

	// Verify CSV content contains headers
	csvContent := string(data)
	assert.Contains(t, csvContent, "ID,Type,Amount,Currency,Category,Account,Occurred At,Description")
	assert.Contains(t, csvContent, "txn-001")
	assert.Contains(t, csvContent, "txn-002")
}

func TestExportService_ExportTransactions_XLSX(t *testing.T) {
	db := setupTestDB(t)
	service := NewExportService(db)

	// Insert test data
	familyID := "test-family-002"
	now := time.Now()
	categoryID := "cat-002"
	accountID := "acc-002"

	transactions := []model.FinanceTransaction{
		{
			ID:          "txn-003",
			FamilyID:    familyID,
			Type:        "expense",
			AmountCents: -3000, // -30.00 yuan
			CategoryID:  &categoryID,
			AccountID:   accountID,
			OccurredAt:  now.Add(-24 * time.Hour),
			Description: "交通费用",
			Version:     1,
		},
	}

	for _, txn := range transactions {
		err := db.Create(&txn).Error
		assert.NoError(t, err)
	}

	// Test XLSX export
	ctx := context.Background()
	data, filename, err := service.ExportTransactions(ctx, familyID, "expense", 7, "xlsx")

	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Contains(t, filename, ".xlsx")
	assert.Contains(t, filename, "expense")
	assert.Contains(t, filename, "7days")

	// XLSX files start with PK signature
	assert.Greater(t, len(data), 4)
	assert.Equal(t, uint8('P'), data[0])
	assert.Equal(t, uint8('K'), data[1])
}

func TestExportService_ExportTransactions_TypeFilter(t *testing.T) {
	db := setupTestDB(t)
	service := NewExportService(db)

	// Insert test data with different types
	familyID := "test-family-003"
	now := time.Now()
	accountID := "acc-003"

	incomeTxn := model.FinanceTransaction{
		ID:          "txn-income",
		FamilyID:    familyID,
		Type:        "income",
		AmountCents: 10000,
		AccountID:   accountID,
		OccurredAt:  now.Add(-24 * time.Hour),
		Description: "收入",
		Version:     1,
	}

	expenseTxn := model.FinanceTransaction{
		ID:          "txn-expense",
		FamilyID:    familyID,
		Type:        "expense",
		AmountCents: -5000,
		AccountID:   accountID,
		OccurredAt:  now.Add(-48 * time.Hour),
		Description: "支出",
		Version:     1,
	}

	err := db.Create(&incomeTxn).Error
	assert.NoError(t, err)
	err = db.Create(&expenseTxn).Error
	assert.NoError(t, err)

	ctx := context.Background()

	// Test income filter
	data, _, err := service.ExportTransactions(ctx, familyID, "income", 7, "csv")
	assert.NoError(t, err)
	assert.Contains(t, string(data), "txn-income")
	assert.NotContains(t, string(data), "txn-expense")

	// Test expense filter
	data, _, err = service.ExportTransactions(ctx, familyID, "expense", 7, "csv")
	assert.NoError(t, err)
	assert.Contains(t, string(data), "txn-expense")
	assert.NotContains(t, string(data), "txn-income")

	// Test all filter
	data, _, err = service.ExportTransactions(ctx, familyID, "all", 7, "csv")
	assert.NoError(t, err)
	assert.Contains(t, string(data), "txn-income")
	assert.Contains(t, string(data), "txn-expense")
}

func TestExportService_ExportTransactions_DaysRange(t *testing.T) {
	db := setupTestDB(t)
	service := NewExportService(db)

	// Insert test data with different dates
	familyID := "test-family-004"
	now := time.Now()
	accountID := "acc-004"

	recentTxn := model.FinanceTransaction{
		ID:          "txn-recent",
		FamilyID:    familyID,
		Type:        "expense",
		AmountCents: -1000,
		AccountID:   accountID,
		OccurredAt:  now.Add(-2 * 24 * time.Hour), // 2 days ago
		Description: "最近",
		Version:     1,
	}

	oldTxn := model.FinanceTransaction{
		ID:          "txn-old",
		FamilyID:    familyID,
		Type:        "expense",
		AmountCents: -2000,
		AccountID:   accountID,
		OccurredAt:  now.Add(-10 * 24 * time.Hour), // 10 days ago
		Description: "较早",
		Version:     1,
	}

	err := db.Create(&recentTxn).Error
	assert.NoError(t, err)
	err = db.Create(&oldTxn).Error
	assert.NoError(t, err)

	ctx := context.Background()

	// Test 7-day range (should only include recent transaction)
	data, _, err := service.ExportTransactions(ctx, familyID, "all", 7, "csv")
	assert.NoError(t, err)
	assert.Contains(t, string(data), "txn-recent")
	assert.NotContains(t, string(data), "txn-old")

	// Test 30-day range (should include both)
	data, _, err = service.ExportTransactions(ctx, familyID, "all", 30, "csv")
	assert.NoError(t, err)
	assert.Contains(t, string(data), "txn-recent")
	assert.Contains(t, string(data), "txn-old")
}

func TestExportService_ExportTransactions_InvalidParams(t *testing.T) {
	db := setupTestDB(t)
	service := NewExportService(db)

	ctx := context.Background()

	// Test invalid type
	_, _, err := service.ExportTransactions(ctx, "family-001", "invalid", 7, "csv")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid type filter")

	// Test invalid days
	_, _, err = service.ExportTransactions(ctx, "family-001", "income", 0, "csv")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid days")

	// Test invalid format
	_, _, err = service.ExportTransactions(ctx, "family-001", "income", 7, "pdf")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid format")
}
