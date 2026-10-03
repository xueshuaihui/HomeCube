package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/handler"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Auto migrate all finance tables
	err = db.AutoMigrate(
		&model.FinanceAccount{},
		&model.FinanceCategory{},
		&model.FinanceTransaction{},
		&model.FinanceLedger{},
		&model.FinanceBudget{},
		&model.FinanceBill{},
		&model.FinanceLoan{},
		&model.FinanceRepaymentPlan{},
		&model.FinanceGoal{},
		&model.FinanceSplitSettlement{},
		&model.FinanceParticipant{},
		&model.FinanceCreditCard{},
		&model.FinanceInvoice{},
		&model.FinanceAssetLiabilityReport{},
		&model.FinanceTag{},
		&model.FinanceRecurringRule{},
		&model.FinanceBudgetPeriod{},
		&model.FinanceSettings{},
	)
	require.NoError(t, err)

	return db
}

func TestGetFinanceSettings_NotFound_CreatesDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.GET("/settings", h.GetFinanceSettings)

	familyID := "test-family-001"
	req := httptest.NewRequest(http.MethodGet, "/settings?family_id="+familyID, nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var settings model.FinanceSettings
	err := json.Unmarshal(w.Body.Bytes(), &settings)
	require.NoError(t, err)

	assert.Equal(t, familyID, settings.FamilyID)
	assert.Equal(t, "CNY", settings.CurrencyUnit)
	assert.Equal(t, int32(2), settings.DecimalPlaces)
	assert.InDelta(t, 0.80, settings.BudgetAlertThreshold, 0.01)
	assert.False(t, settings.AutoCategorizeEnabled)
	assert.False(t, settings.ReceiptOCREnabled)
	assert.False(t, settings.VoiceInputEnabled)
}

func TestGetFinanceSettings_Existing_ReturnsSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	// Create initial settings
	ctx := context.Background()
	familyID := "test-family-002"
	settings := &model.FinanceSettings{
		FamilyID:              familyID,
		CurrencyUnit:          "USD",
		DecimalPlaces:         4,
		BudgetAlertThreshold:  0.90,
		AutoCategorizeEnabled: true,
		ReceiptOCREnabled:     true,
		VoiceInputEnabled:     true,
	}
	err := financeRepo.UpsertSettings(ctx, settings)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/settings", h.GetFinanceSettings)

	req := httptest.NewRequest(http.MethodGet, "/settings?family_id="+familyID, nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result model.FinanceSettings
	err = json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, familyID, result.FamilyID)
	assert.Equal(t, "USD", result.CurrencyUnit)
	assert.Equal(t, int32(4), result.DecimalPlaces)
	assert.InDelta(t, 0.90, result.BudgetAlertThreshold, 0.01)
	assert.True(t, result.AutoCategorizeEnabled)
	assert.True(t, result.ReceiptOCREnabled)
	assert.True(t, result.VoiceInputEnabled)
}

func TestUpdateFinanceSettings_PartialUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	// Create initial settings
	ctx := context.Background()
	familyID := "test-family-003"
	settings := &model.FinanceSettings{
		FamilyID:              familyID,
		CurrencyUnit:          "CNY",
		DecimalPlaces:         2,
		BudgetAlertThreshold:  0.80,
		AutoCategorizeEnabled: false,
		ReceiptOCREnabled:     false,
		VoiceInputEnabled:     false,
	}
	err := financeRepo.UpsertSettings(ctx, settings)
	require.NoError(t, err)

	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	// Update only currency and decimal places
	updateReq := map[string]interface{}{
		"family_id":       familyID,
		"currency_unit":   "USD",
		"decimal_places":  4,
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result model.FinanceSettings
	err = json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, familyID, result.FamilyID)
	assert.Equal(t, "USD", result.CurrencyUnit) // Updated
	assert.Equal(t, int32(4), result.DecimalPlaces) // Updated
	assert.InDelta(t, 0.80, result.BudgetAlertThreshold, 0.01) // Unchanged
	assert.False(t, result.AutoCategorizeEnabled) // Unchanged
	assert.False(t, result.ReceiptOCREnabled) // Unchanged
	assert.False(t, result.VoiceInputEnabled) // Unchanged
}

func TestUpdateFinanceSettings_FullUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	familyID := "test-family-004"
	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	// Update with all fields
	updateReq := map[string]interface{}{
		"family_id":                familyID,
		"currency_unit":            "EUR",
		"decimal_places":           3,
		"budget_alert_threshold":   0.75,
		"auto_categorize_enabled":  true,
		"receipt_ocr_enabled":      true,
		"voice_input_enabled":      true,
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result model.FinanceSettings
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, familyID, result.FamilyID)
	assert.Equal(t, "EUR", result.CurrencyUnit)
	assert.Equal(t, int32(3), result.DecimalPlaces)
	assert.InDelta(t, 0.75, result.BudgetAlertThreshold, 0.01)
	assert.True(t, result.AutoCategorizeEnabled)
	assert.True(t, result.ReceiptOCREnabled)
	assert.True(t, result.VoiceInputEnabled)
}

func TestGetFinanceSettings_MissingFamilyID_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.GET("/settings", h.GetFinanceSettings)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateFinanceSettings_InvalidCurrency_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	updateReq := map[string]interface{}{
		"family_id":     "test-family-005",
		"currency_unit": "INVALID",
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateFinanceSettings_InvalidDecimalPlaces_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	updateReq := map[string]interface{}{
		"family_id":      "test-family-006",
		"decimal_places": 5, // Exceeds max of 4
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateFinanceSettings_InvalidThreshold_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	updateReq := map[string]interface{}{
		"family_id":              "test-family-007",
		"budget_alert_threshold": 1.5, // Exceeds max of 1.0
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
