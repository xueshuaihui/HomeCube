package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Same two 底座运行表 shapes as the service tests: finance_outbox mirrors the shipped
// migrations/finance/finance_0002 column set (no updated_at / sent_at / error), finance_change_log
// mirrors the shipped migrations/finance/finance_0003_change_log_idempotency.up.sql column set --
// the six columns (lsn, family_id, entity, entity_id, op, version), no data / created_at.
const (
	ddlOutbox = `
CREATE TABLE finance_outbox (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id  TEXT,
	subject    TEXT NOT NULL,
	envelope   TEXT NOT NULL,
	status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent')),
	attempts   INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	ddlChangeLog = `
CREATE TABLE finance_change_log (
	lsn       INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id TEXT    NOT NULL,
	entity    TEXT    NOT NULL,
	entity_id TEXT    NOT NULL,
	op        TEXT    NOT NULL,
	version   INTEGER NOT NULL
)`
)

func setupBillHandlerDB(t *testing.T) (*gorm.DB, *FinanceHandler) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.AutoMigrate(&model.FinanceBill{}))
	require.NoError(t, db.Exec(ddlOutbox).Error)
	require.NoError(t, db.Exec(ddlChangeLog).Error)

	financeRepo := repo.NewFinanceRepo(db)
	billService := service.NewBillService(financeRepo, db, "finance")
	// The other services are not on the POST /bills path.
	h := NewFinanceHandler(financeRepo, nil, nil, nil, nil, billService)
	return db, h
}

func postBill(t *testing.T, h *FinanceHandler, body string) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/finance/bills", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.CreateBill(c)
	return w
}

const (
	testFamilyUUID = "11111111-1111-4111-8111-111111111111"
	testPayeeUUID  = "22222222-2222-4222-8222-222222222222"
)

// TestFinanceHandler_CreateBill_RegistersDueDate is the production path: POST /api/finance/bills must
// leave the bill row AND its finance.due.registered outbox row behind, in one transaction.
func TestFinanceHandler_CreateBill_RegistersDueDate(t *testing.T) {
	db, h := setupBillHandlerDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	dueAt := time.Date(2026, 12, 1, 8, 30, 0, 0, time.UTC)
	body := `{"family_id":"` + testFamilyUUID + `","payee_id":"` + testPayeeUUID +
		`","amount_cents":120000,"due_at":"` + dueAt.Format(time.RFC3339) + `"}`

	w := postBill(t, h, body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var created model.FinanceBill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)

	var billCount int64
	require.NoError(t, db.Model(&model.FinanceBill{}).Count(&billCount).Error)
	assert.Equal(t, int64(1), billCount)

	var rows []struct {
		Subject  string `gorm:"column:subject"`
		Envelope string `gorm:"column:envelope"`
		Status   string `gorm:"column:status"`
	}
	require.NoError(t, db.Table("finance_outbox").Find(&rows).Error)
	require.Len(t, rows, 1, "创建账单必须同时产生一条到期注册")
	assert.Equal(t, "finance.due.registered", rows[0].Subject)
	assert.Equal(t, "pending", rows[0].Status)

	// ENV-1: the outbox column now holds bus.Envelope's wire form, so the contract fields live under
	// `payload` and the idempotency key under the metadata's `business_id`. Same fields, same expected
	// values -- only the location moved. (This file sits outside ENV-1's declared scope; the shape change
	// is what the card was created for, so the assertions followed it rather than the producer being
	// left flat. See the ENV-1 report.)
	env, err := bus.UnmarshalEnvelope([]byte(rows[0].Envelope))
	require.NoError(t, err)
	require.NotEmpty(t, env.Payload,
		"空 payload 会被消费侧直接 Nak+死信（svc-homeos internal/consumer/due_registered_handler.go:36）")
	assert.Equal(t, "bill", env.Payload["kind"], "kind 取冻结枚举 bill|budget|goal|repayment 中的 bill")
	assert.Equal(t, created.ID, env.Payload["source_id"])
	assert.Equal(t, testFamilyUUID, env.Payload["family_id"])
	assert.Equal(t, dueAt.Format(time.RFC3339), env.Payload["due_at"])
	assert.Equal(t, "finance.due.registered", env.EventType, "去重键的一半，缺了底座就 Nak 不写死信")
	assert.Equal(t, created.ID+":"+dueAt.Format(time.RFC3339), env.BusinessID)
}

// TestFinanceHandler_CreateBill_FailsWhenRegistrationFails pins 实现要求 3: when the due registration
// cannot be written, the request must not answer 201 -- and nothing may be committed.
func TestFinanceHandler_CreateBill_FailsWhenRegistrationFails(t *testing.T) {
	db, h := setupBillHandlerDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	// Swap in a bill service whose outbox table was never migrated: the event write fails.
	financeRepo := repo.NewFinanceRepo(db)
	broken := service.NewBillService(financeRepo, db, "finance_unmigrated")
	h.billService = broken

	dueAt := time.Date(2026, 12, 1, 8, 30, 0, 0, time.UTC)
	body := `{"family_id":"` + testFamilyUUID + `","payee_id":"` + testPayeeUUID +
		`","amount_cents":120000,"due_at":"` + dueAt.Format(time.RFC3339) + `"}`

	w := postBill(t, h, body)
	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "failed to create bill")

	var billCount int64
	require.NoError(t, db.Model(&model.FinanceBill{}).Count(&billCount).Error)
	assert.Equal(t, int64(0), billCount, "注册失败时账单行必须回滚")

	var changeLogCount int64
	require.NoError(t, db.Table("finance_change_log").Count(&changeLogCount).Error)
	assert.Equal(t, int64(0), changeLogCount)

	var outboxCount int64
	require.NoError(t, db.Table("finance_outbox").Count(&outboxCount).Error)
	assert.Equal(t, int64(0), outboxCount)
}

// TestFinanceHandler_CreateBill_RejectsMissingDueAt keeps the binding on due_at honest: without a due
// date there is nothing to register, so the request is a 400 rather than a silent bill.
func TestFinanceHandler_CreateBill_RejectsMissingDueAt(t *testing.T) {
	db, h := setupBillHandlerDB(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	body := `{"family_id":"` + testFamilyUUID + `","payee_id":"` + testPayeeUUID + `","amount_cents":120000}`
	w := postBill(t, h, body)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	var billCount int64
	require.NoError(t, db.WithContext(context.Background()).Model(&model.FinanceBill{}).Count(&billCount).Error)
	assert.Equal(t, int64(0), billCount)
}
