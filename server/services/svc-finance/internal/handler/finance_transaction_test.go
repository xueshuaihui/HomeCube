package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// POST /api/finance/transactions is the only place ErrDuplicateRequest reaches an HTTP client,
// and it reaches it through TWO different repo returns:
//
//   - the idempotency hit: sync.CheckIdempotency finds (family_id, key) with the same request
//     hash, so CreateTransaction returns the bare sentinel (repo/finance.go:233);
//   - the database unique index: the key is already taken but the body differs (a retry whose
//     amount or occurred_at changed), so the hash check declines (packages/sync/repo.go:196
//     returns found=false), the flow keeps going and tx.Create collides with
//     uk_client_request_id (model/finance.go:74, migrations/finance/finance_0001:83). That error
//     reaches the handler wrapped as "failed to create transaction: %w" (repo/finance.go:244).
//
// The wrapped half is what TestFinanceHandler_CreateTransaction_ReusedKeyWithDifferentBodyIsConflict
// pins: before the fix it answered 500, i.e. "the server broke, retry later" for a request whose
// duplicate the server had already processed -- the opposite of what the idempotency contract asks.
//
// The fixture below is the shipped DDL shape, not a looser stand-in: finance_change_log /
// finance_idempotency mirror migrations/finance/finance_0003_change_log_idempotency.up.sql (five
// idempotency columns, no primary key, ONE unique index over (family_id, key)) and
// finance_transaction carries the unique client_request_id index by AutoMigrating the model itself.
const (
	ddlFinanceChangeLog = `
CREATE TABLE finance_change_log (
	lsn       INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id TEXT    NOT NULL,
	entity    TEXT    NOT NULL,
	entity_id TEXT    NOT NULL,
	op        TEXT    NOT NULL,
	version   INTEGER NOT NULL
)`
	ddlFinanceIdempotency = `
CREATE TABLE finance_idempotency (
	key               TEXT    NOT NULL,
	family_id         TEXT    NOT NULL,
	request_hash      TEXT    NOT NULL,
	response_snapshot BLOB,
	created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`
	ddlFinanceIdempotencyUidx = `
CREATE UNIQUE INDEX finance_idempotency_family_key_uidx ON finance_idempotency(family_id, key)`
)

// setupTxHandlerDB returns a handler wired to an in-memory finance DB that can carry an
// idempotent transaction write end to end.
func setupTxHandlerDB(t *testing.T) (*gorm.DB, *FinanceHandler) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// One connection: :memory: is per-connection, and the assertion reads back the rows the
	// handler's own transaction wrote.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// TablePrefix is a package-level var the repo test flips; pin it so a test-order change
	// cannot make these tables arrive under a "finance." schema.
	model.TablePrefix = ""
	// 账户与分类也要建：CreateTransaction 现在会校验引用的存在性
	//（repo.AssertRefsExist —— 只靠 binding 的 `required,uuid` 会让「格式合法但指向
	//不存在账户」的流水落库，G4 数据门禁实测能查出这类孤儿行）。
	require.NoError(t, db.AutoMigrate(
		&model.FinanceTransaction{},
		&model.FinanceAccount{},
		&model.FinanceCategory{},
	))
	require.NoError(t, db.Exec(ddlFinanceChangeLog).Error)
	require.NoError(t, db.Exec(ddlFinanceIdempotency).Error)
	require.NoError(t, db.Exec(ddlFinanceIdempotencyUidx).Error)

	// 请求体里的 account_id 必须在本家庭真实存在：CreateTransaction 现在先走
	// repo.AssertRefsExist（`WHERE id = ? AND family_id = ?`），查不到就 404 拒写。
	// 这里直接把 txBody/无键用例引用的那个账户种进 txClientFamilyUUID。
	require.NoError(t, db.Create(&model.FinanceAccount{
		ID:       txClientAccountUUID,
		FamilyID: txClientFamilyUUID,
		Name:     "引用校验账户",
		Type:     "cash",
	}).Error)

	// balanceService 必须给：记账成功后要同步 account.balance 缓存
	//（service.RecalcAccountBalance）。给 nil 会在写路径上 panic。
	return db, NewFinanceHandler(
		repo.NewFinanceRepo(db),
		service.NewBalanceService(db, repo.NewFinanceRepo(db)),
		nil, nil, nil, nil,
	)
}

// seedAccount 造一个本家庭的账户并回传它的 id，供请求体里的 account_id 使用。
func seedAccount(t *testing.T, db *gorm.DB, familyID string) string {
	t.Helper()
	acc := &model.FinanceAccount{
		ID:       "aaaaaaaa-0000-4000-8000-000000000001",
		FamilyID: familyID,
		Name:     "验收账户",
		Type:     "cash",
	}
	require.NoError(t, db.Create(acc).Error)
	return acc.ID
}

func postTransaction(t *testing.T, h *FinanceHandler, body string) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/finance/transactions", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// 家庭边界改造后 handler 只认 session（scopeFamily fail closed：没有 session 一律 401），
	// 所以测试必须装上中间件同款会话 —— 这正是要验的：body 里的 family_id 只被「校验」，
	// 落库家庭恒为 sess.FamilyID。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "test-account",
		FamilyID:  txClientFamilyUUID,
		MemberID:  "test-member",
		Role:      "owner",
	})

	h.CreateTransaction(c)
	return w
}

const (
	txClientFamilyUUID  = "11111111-1111-4111-8111-111111111111"
	txClientAccountUUID = "22222222-2222-4222-8222-222222222222"
)

// txBody builds a valid POST /transactions body: clientReqID is the idempotency key, amountCents
// and occurredAt are the two fields a retrying client realistically changes.
func txBody(t *testing.T, clientReqID string, amountCents int64, occurredAt string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"family_id":         txClientFamilyUUID,
		"type":              "expense",
		"amount_cents":      amountCents,
		"account_id":        txClientAccountUUID,
		"occurred_at":       occurredAt,
		"description":       "午餐",
		"client_request_id": clientReqID,
	})
	require.NoError(t, err)
	return string(raw)
}

// countTransactions reads the committed rows straight from SQL -- the handler's response body is
// not the only thing that must not exist after a rejected duplicate.
//
// 按 client_request_id 过滤而不是全表计数：每个用例用各自的幂等键，按总数统计会互相污染
// （原实现把键硬编码成某一个常量，于是「换键复用」那个用例数到的是另一个用例的行数）。
func countTransactions(t *testing.T, db *gorm.DB, clientReqID string) int64 {
	t.Helper()

	var count int64
	require.NoError(t, db.WithContext(context.Background()).
		Table("finance_transaction").
		Where("client_request_id = ?", clientReqID).
		Count(&count).Error)
	return count
}

// gjsonID 从响应体里取 id。幂等重放要断言的是「返回同一个 id」，所以需要能读出来比对。
func gjsonID(t *testing.T, body string) string {
	t.Helper()

	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out), "响应体不是合法 JSON: %s", body)
	require.NotEmpty(t, out.ID, "响应体缺少 id: %s", body)
	return out.ID
}

// The 500 that this card was filed for: the same client_request_id comes back with a different
// body, so the idempotency hash declines to call it a replay and the write lands on
// uk_client_request_id instead. A duplicate submission is a conflict the client can act on
// (409), not a server fault it should retry (500).
func TestFinanceHandler_CreateTransaction_ReusedKeyWithDifferentBodyIsConflict(t *testing.T) {
	db, h := setupTxHandlerDB(t)

	const key = "33333333-3333-4333-8333-333333333333"
	w := postTransaction(t, h, txBody(t, key, 1000, "2026-10-01T09:00:00Z"))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	w2 := postTransaction(t, h, txBody(t, key, 2000, "2026-10-01T09:00:00Z"))
	require.Equal(t, http.StatusConflict, w2.Code, "唯一键冲突必须是 409，不是 500：%s", w2.Body.String())
	assert.Contains(t, w2.Body.String(), repo.ErrDuplicateRequest.Error())

	// And the rejected retry must not have written a second row.
	assert.Equal(t, int64(1), countTransactions(t, db, key), "重复提交不得落下第二笔流水")
}

// The other half of the same sentinel: an **exact** replay (same key, same body) is caught by
// the idempotency check. Per PRD 14.7 / 迁移 0003「重放返回首次响应而非重复执行」it must
// answer 200 with the first response, NOT 409.
//
// 为什么改这个断言：旧断言写的是 409，那是把缺陷当契约。幂等键的全部意义就是让网络超时
// 后的重试安全 —— 客户端重试时如果看到「冲突」，无法判断自己第一次写入到底成功了没有，
// 于是只能让用户手动核对或再试一次，可能真的记两笔账。返回首次响应才是幂等。
// 下面仍然断言「库里只有一行」，那是这个测试真正要守的东西（重放不重复写入）。
func TestFinanceHandler_CreateTransaction_ExactReplayReturnsFirstResponse(t *testing.T) {
	db, h := setupTxHandlerDB(t)

	const key = "33333333-3333-4333-8333-333333333333"
	w := postTransaction(t, h, txBody(t, key, 1000, "2026-10-01T09:00:00Z"))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	firstID := gjsonID(t, w.Body.String())

	w2 := postTransaction(t, h, txBody(t, key, 1000, "2026-10-01T09:00:00Z"))
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())

	// 重放必须给出同一个 id —— 客户端据此知道「我之前那次已经成功了」。
	assert.Equal(t, firstID, gjsonID(t, w2.Body.String()), "重放应返回首次创建的 id")

	assert.Equal(t, int64(1), countTransactions(t, db, key), "重放不得落下第二笔流水")
}

// A transaction with no idempotency key keeps flowing: three of them share nothing and all three
// must commit. Guards the unique-conflict classifier against "any INSERT error is a duplicate".
func TestFinanceHandler_CreateTransaction_WithoutKeyStillCommits(t *testing.T) {
	db, h := setupTxHandlerDB(t)

	for _, amount := range []int64{100, 200, 300} {
		raw, err := json.Marshal(map[string]any{
			"family_id":    txClientFamilyUUID,
			"type":         "expense",
			"amount_cents": amount,
			"account_id":   txClientAccountUUID,
			"occurred_at":  "2026-10-01T09:00:00Z",
			"description":  "公交",
		})
		require.NoError(t, err)

		w := postTransaction(t, h, string(raw))
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	var count int64
	require.NoError(t, db.Table("finance_transaction").
		Where("client_request_id IS NULL").Count(&count).Error)
	assert.Equal(t, int64(3), count)

	var nullCheck sql.NullString
	require.NoError(t, db.Table("finance_transaction").
		Select("client_request_id").Limit(1).Scan(&nullCheck).Error)
	assert.False(t, nullCheck.Valid, "无键写入不得把幂等键补成空串")
}
