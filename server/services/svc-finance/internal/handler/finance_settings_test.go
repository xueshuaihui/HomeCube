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
	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
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

	// 底座运行表族（§2.3 的第 4、5 行 / §五）：packages/sync 的 repo 基类在**每一次业务写入**
	// 里都会向 {code}_change_log 追加一行（业务代码不记得写，见 packages/sync/repo.go:163 的
	// tx.Table(r.tableName).Create）。这两张表没有对应的 GORM model（sync 用 Table() 动态写），
	// 所以 AutoMigrate 不会建它们 —— 缺表的表现是每个写接口都 500：
	//   sync: append to finance_change_log: no such table: finance_change_log
	// 这里显式建出来，列集合照抄迁移 finance_0003_change_log_idempotency.up.sql。
	// bigserial 在 SQLite 下没有等价类型，用自增整型即可（测试只关心「表在、能插入」）。
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS finance_change_log (
			lsn        INTEGER PRIMARY KEY AUTOINCREMENT,
			family_id  TEXT NOT NULL,
			entity     TEXT NOT NULL,
			entity_id  TEXT NOT NULL,
			op         TEXT NOT NULL,
			version    INTEGER NOT NULL
		)`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS finance_idempotency (
			key              TEXT NOT NULL,
			family_id        TEXT NOT NULL,
			request_hash     TEXT NOT NULL,
			response_snapshot TEXT,
			created_at       DATETIME
		)`).Error)
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_finance_idempotency_key_family ON finance_idempotency (key, family_id)`).Error)

	return db
}

// withSession installs the session the auth middleware would have installed.
//
// 家庭边界改造后 handler 的唯一作用域来源是 sess.FamilyID（scopeFamily fail closed：
// 没有 session 一律 401，连查询都不发）。这些测试直接挂 handler 路由、不走真实中间件，
// 所以要自己把 CtxSession 装上 —— 这正是「客户端的 family_id 只被校验、绝不被使用」
// 能在测试里成立的前提。
func withSession(familyID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(svcauth.CtxFamilyID, familyID)
		c.Set(svcauth.CtxSession, &svcauth.Session{
			AccountID: "settings-account",
			FamilyID:  familyID,
			MemberID:  "settings-member",
			Role:      "owner",
		})
		c.Next()
	}
}

func TestGetFinanceSettings_NotFound_CreatesDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	familyID := "11111111-1111-4111-8111-000000000001"
	router := gin.New()
	router.Use(withSession(familyID))
	router.GET("/settings", h.GetFinanceSettings)
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
	familyID := "11111111-1111-4111-8111-000000000002"
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
	router.Use(withSession(familyID))
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
	familyID := "11111111-1111-4111-8111-000000000003"
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
	router.Use(withSession(familyID))
	router.PUT("/settings", h.UpdateFinanceSettings)

	// Update only currency and decimal places
	updateReq := map[string]interface{}{
		"family_id":      familyID,
		"currency_unit":  "USD",
		"decimal_places": 4,
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
	assert.Equal(t, "USD", result.CurrencyUnit)                // Updated
	assert.Equal(t, int32(4), result.DecimalPlaces)            // Updated
	assert.InDelta(t, 0.80, result.BudgetAlertThreshold, 0.01) // Unchanged
	assert.False(t, result.AutoCategorizeEnabled)              // Unchanged
	assert.False(t, result.ReceiptOCREnabled)                  // Unchanged
	assert.False(t, result.VoiceInputEnabled)                  // Unchanged
}

func TestUpdateFinanceSettings_FullUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	familyID := "11111111-1111-4111-8111-000000000004"
	router := gin.New()
	router.Use(withSession(familyID))
	router.PUT("/settings", h.UpdateFinanceSettings)

	// Update with all fields
	updateReq := map[string]interface{}{
		"family_id":               familyID,
		"currency_unit":           "EUR",
		"decimal_places":          3,
		"budget_alert_threshold":  0.75,
		"auto_categorize_enabled": true,
		"receipt_ocr_enabled":     true,
		"voice_input_enabled":     true,
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

// TestGetFinanceSettings_MissingSession_FailsClosed replaces the old
// 「缺 family_id → 400」 expectation: after the family-boundary fix the client's family_id is
// only ever *validated*, never *used* —— scope comes from the session, so a missing query
// param is no longer an error. What must never happen is answering an unscoped query: no
// session (middleware absent / onboarding token) means 401 before any DB read (fail closed).
func TestGetFinanceSettings_MissingSession_FailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.GET("/settings", h.GetFinanceSettings)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no_session_family")

	// fail closed 的落点：没有任何默认设置被凭空创建 —— 家庭都确定不了就不写库。
	var count int64
	require.NoError(t, db.Model(&model.FinanceSettings{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestUpdateFinanceSettings_InvalidCurrency_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	h := handler.NewFinanceHandler(financeRepo, nil, nil, nil, nil, nil)

	router := gin.New()
	router.PUT("/settings", h.UpdateFinanceSettings)

	updateReq := map[string]interface{}{
		"family_id":     "11111111-1111-4111-8111-000000000005",
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
		"family_id":      "11111111-1111-4111-8111-000000000006",
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
		"family_id":              "11111111-1111-4111-8111-000000000007",
		"budget_alert_threshold": 1.5, // Exceeds max of 1.0
	}
	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
