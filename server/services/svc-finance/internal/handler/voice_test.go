package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/xueshuaihui/HomeCube/server/packages/adapter/asr"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupVoiceTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(&model.FinanceTransaction{}, &model.FinanceAccount{}, &model.FinanceCategory{})
	assert.NoError(t, err)

	return db
}

func TestVoiceHandler_VoiceEntry_ManualFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := asr.NewStubAdapter()
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	// Create test request without audio file (should fallback to manual)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/finance/voice-entry", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	reqBody := map[string]string{
		"family_id":  "test-family-001",
		"account_id": "test-account-001",
	}
	body, _ := json.Marshal(reqBody)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	handler.VoiceEntry(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response VoiceEntryResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "manual", response.Source)
	assert.NotNil(t, response.Draft)
}

func TestVoiceHandler_ParseTranscriptToDraft(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := asr.NewStubAdapter()
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	tests := []struct {
		name           string
		transcript     string
		expectedType   string
		expectedAmount int64
	}{
		{"expense with amount", "餐饮支出 50 元", "expense", -5000},
		{"income with amount", "工资收入 1000 元", "income", 100000},
		{"decimal amount", "购物 29.9 元", "expense", -2990},
		{"no amount", "餐饮支出", "expense", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := VoiceEntryRequest{
				FamilyID:  "test-family",
				AccountID: "test-account",
			}

			draft, err := handler.parseTranscriptToDraft(tt.transcript, req)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedType, draft.Type)
			assert.Equal(t, tt.expectedAmount, draft.AmountCents)
		})
	}
}

func TestVoiceHandler_ExtractCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := asr.NewStubAdapter()
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	tests := []struct {
		transcript string
		category   string
	}{
		{"餐饮支出 50 元", "餐饮"},
		{"交通费用 30 元", "交通"},
		{"购物消费 100 元", "购物"},
		{"娱乐活动 80 元", "娱乐"},
		{"医疗费用 200 元", "医疗"},
		{"教育培训 500 元", "教育"},
		{"住房租金 3000 元", "住房"},
		{"水电费 150 元", "水电"},
		{"其他支出", ""},
	}

	for _, tt := range tests {
		t.Run(tt.transcript, func(t *testing.T) {
			category := handler.extractCategory(tt.transcript)
			assert.Equal(t, tt.category, category)
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		s       string
		substrs []string
		want    bool
	}{
		{"餐饮支出 50 元", []string{"收入", "收款"}, false},
		{"工资收入 1000 元", []string{"收入", "收款"}, true},
		{"转账 500 元", []string{"转账", "转出"}, true},
		{"", []string{"test"}, false},
	}

	for _, tt := range tests {
		result := containsAny(tt.s, tt.substrs)
		assert.Equal(t, tt.want, result)
	}
}
