package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
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

	// The 底座运行表 every repo write appends to inside its transaction (packages/sync). It was missing
	// from this fixture, which is why TestExecuteDueRecurringRules_WithDueRule failed with
	// "no such table: finance_change_log". Same DDL text the bill tests use (see bill_test.go).
	require.NoError(t, db.Exec(ddlFinanceChangeLog).Error)

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
	//
	// ENV-1 上报（本卡不改，另卡跟）：这句 NoError 是空断言。ExecuteDueRecurringRules 把 executeRule
	// 的错误 `continue` 吞掉了（recurring.go:46-49）。实测：直调 executeRule 在本夹具下返回
	// 「failed to create transaction for recurring rule: ... no such table: finance_transaction」，
	// finance_outbox 0 行——因为 s.repo 绑在外层 db 上，CreateTransaction 在连接池的第二条
	// sqlite `:memory:` 连接上开它自己的事务（那条连接看不到夹具建的表），而 outbox 行写的是 txDB
	// 那条。同一个结构问题在真库上也成立：业务写与事件写分属两个事务，与 §3.4.6「发布前落盘 + 同事务」
	// 相悖。真实信封外形因此由 TestTransactionCreatedEnvelope_NestsPayloadAndCarriesTheIdempotencyKey
	// 直接对 executeRule 调用的那个构造函数断言，不依赖这段被吞掉的路径。
	assert.NoError(t, err)
}

// transactionEventView is the contract read model of finance.transaction.created
// (contracts/events/finance.yaml:24-32) used to prove the payload's fields survive the round trip.
// created_by is declared by the contract but is not emitted -- neither FinanceTransaction nor
// FinanceRecurringRule has a creator column -- so it is absent here as well, and reported.
type transactionEventView struct {
	TransactionID string  `json:"transaction_id"`
	FamilyID      string  `json:"family_id"`
	Type          string  `json:"type"`
	AmountCents   int64   `json:"amount_cents"`
	AccountID     string  `json:"account_id"`
	CategoryID    *string `json:"category_id"`
	OccurredAt    string  `json:"occurred_at"`
}

// TestTransactionCreatedEnvelope_NestsPayloadAndCarriesTheIdempotencyKey is ENV-1's producer half for
// the recurring path. Before this card the envelope was a hand-built map with NO event_type and NO
// business_id, and every business key at the top level: bus.Envelope decoded it to an empty dedupe key
// (rejected by packages/bus/consumer.go:210-215, which NAKs without writing a dead-letter row -- the
// event vanished silently) and an empty Payload.
//
// It runs on transactionCreatedEnvelope, the function executeRule itself calls, and on the base's own
// marshal + decode pair, so what is asserted is the byte form that lands in finance_outbox.envelope.
func TestTransactionCreatedEnvelope_NestsPayloadAndCarriesTheIdempotencyKey(t *testing.T) {
	categoryID := "category-001"
	rule := model.FinanceRecurringRule{
		ID:          "rule-001",
		FamilyID:    "test-family-001",
		Name:        "Monthly Rent",
		Type:        "expense",
		AmountCents: -500000,
		AccountID:   "account-001",
		CategoryID:  categoryID,
	}
	tx := &model.FinanceTransaction{
		ID:          "tx-cross-001",
		FamilyID:    "test-family-001",
		Type:        rule.Type,
		AmountCents: rule.AmountCents,
		AccountID:   rule.AccountID,
		CategoryID:  &categoryID,
		OccurredAt:  time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC),
		Description: "周期记账: Monthly Rent",
	}

	envelopeJSON, err := bus.MarshalEnvelope(transactionCreatedEnvelope(tx, rule))
	require.NoError(t, err)

	// 落库外形：payload 键必须在，业务键不能在顶层。
	var raw map[string]any
	require.NoError(t, json.Unmarshal(envelopeJSON, &raw))
	require.Contains(t, raw, "payload", "recurring 的信封也必须嵌套 payload")
	for _, flattened := range []string{"transaction_id", "type", "amount_cents", "occurred_at"} {
		assert.NotContains(t, raw, flattened, "%s 必须在 payload 里，不在顶层", flattened)
	}

	env, err := bus.UnmarshalEnvelope(envelopeJSON)
	require.NoError(t, err)

	// 元数据齐全，幂等键 = {transaction_id}（contracts/events/finance.yaml:35）
	assert.Equal(t, "finance.transaction.created", env.EventType)
	assert.Equal(t, tx.ID, env.BusinessID, "一次性语义事件的幂等键就是对象 id（contracts/README.md:33-37）")
	assert.Equal(t, rule.FamilyID, env.FamilyID)
	assert.Equal(t, "1.0", env.Version)
	_, err = time.Parse(time.RFC3339, env.Timestamp)
	require.NoError(t, err, "timestamp 必须是 RFC3339")

	// 消费侧同一入口：底座守卫 + payload 守卫都不拒。
	msg := bus.Message{Subject: "finance.transaction.created", Envelope: env}
	require.NoError(t, consumerAccepts(msg), "生产侧真实产出的信封必须过消费侧守卫（got: %v）", consumerAccepts(msg))

	// 契约声明的字段逐项可读。
	data, err := json.Marshal(env.Payload)
	require.NoError(t, err)
	var view transactionEventView
	require.NoError(t, json.Unmarshal(data, &view))
	assert.Equal(t, tx.ID, view.TransactionID)
	assert.Equal(t, rule.FamilyID, view.FamilyID)
	assert.Equal(t, "expense", view.Type)
	assert.Equal(t, tx.AmountCents, view.AmountCents)
	assert.Equal(t, tx.AccountID, view.AccountID)
	require.NotNil(t, view.CategoryID)
	assert.Equal(t, categoryID, *view.CategoryID)
	occurredAt, err := time.Parse(time.RFC3339, view.OccurredAt)
	require.NoError(t, err, "occurred_at 必须是消费方能解析的时间戳")
	assert.True(t, occurredAt.Equal(tx.OccurredAt.Truncate(time.Second)))

	// recurring 溯源三列仍在（冻结契约没给它们位置，已作为契约漂移上报）
	assert.Equal(t, true, env.Payload["is_recurring"])
	assert.Equal(t, rule.ID, env.Payload["recurring_rule_id"])
	assert.Equal(t, tx.Description, env.Payload["description"])
}

// TestTransactionCreatedEnvelope_FlatShapeIsRejected feeds the same entry point the pre-ENV-1 recurring// literal produced, verbatim, and requires the base's dedupe guard to refuse it -- the silent-vanish
// half of the defect (NAK with no dead-letter row, so nothing is ever observable downstream).
func TestTransactionCreatedEnvelope_FlatShapeIsRejected(t *testing.T) {
	const flatBeforeENV1 = `{"source_system":"finance","source_id":"tx-001","type":"expense",` +
		`"amount_cents":-500000,"account_id":"account-001","category_id":"category-001",` +
		`"occurred_at":"2026-10-28T00:00:00Z","description":"周期记账: Monthly Rent",` +
		`"is_recurring":true,"recurring_rule_id":"rule-001"}`

	env, err := bus.UnmarshalEnvelope([]byte(flatBeforeENV1))
	require.NoError(t, err, "旧形状不是坏 JSON：解码成功，只是三处都空")
	assert.Empty(t, env.EventType, "旧形状根本没有 event_type")
	assert.Empty(t, env.BusinessID, "旧形状根本没有 business_id")
	assert.Empty(t, env.Payload, "旧形状的业务键全在顶层，payload 是空的")

	got := consumerAccepts(bus.Message{Subject: "finance.transaction.created", Envelope: env})
	require.Error(t, got, "空去重键必须被拒（consumer.go:210-215：只 Nak、不写死信）")
	assert.EqualError(t, got,
		`bus: dedupe key (event_type, business_id) needs both parts, got ("", "")`)
	t.Logf("recurring 旧扁平形状在消费侧入口被拒：%v", got)
}
