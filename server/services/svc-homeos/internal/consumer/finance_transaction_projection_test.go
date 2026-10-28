// finance_transaction_projection_test.go is the consumer-side判据 for the money leg: one accepted
// finance.transaction.created becomes one month bucket in homeos_proj_finance, expenses arrive with the
// projection-side sign (magnitude, non-negative), a delete takes the contribution back out, and replaying
// the same message twice changes nothing (task 判据 (b): income/expense/delete + replay-safe).
//
// What each case pins:
//  1. Sign. svc-finance negates expenses (model/finance.go:67), so the event carries amount_cents=-8880;
//     faces.go:236 prints 「本周期支出 」+ formatMoney(ExpenseCents) with no sign flip -- the stored
//     expense_cents must be +8880 or the card would read 「本周期支出 -¥88.80」.
//  2. Recompute, never increment. The aggregate is rebuilt as SUM over homeos_proj_finance_txn, so the
//     same created delivered twice lands on identical totals even when it reaches the handler twice
//     (the去重层 in front of it is pinned separately, in the durable-consumer case at the bottom).
//  3. Delete removes the contribution, and deleting the last transaction of a bucket drops the bucket
//     row entirely -- that is what flips the face back to 「本周期暂无记账」 (HasData/BucketCount in
//     repo/homeos_projection.go), rather than leaving a phantom 0/0 row that would print 「本周期支出 ¥0」.
//  4. budget_remaining_cents stays NULL through every path (0004:24 「NULL = 未设该周期预算」), so a
//     family with transactions but no budget reads headlineSpend, never a fabricated 剩余.
//  5. Transfers hit neither wing (internal movement, not spend).
//
// Fixtures reuse wiring_test.go's same-package kit (fixtureFamilyID, fakeJS, testMsg, newDedupeTable,
// newDeadLetterTable) and add the two projection tables shaped like homeos_0004 + homeos_0012 -- sqlite
// TEXT for uuid/date (the handlers write 'YYYY-MM-01' text, PG casts it to date), with the two conflict
// anchors the recompute needs: UNIQUE (family_id, bucket_month) and the transaction_id PRIMARY KEY.

package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Money fixture ids: one family plus two transactions that land in the same 2026-10 bucket.
const (
	projTxnIncome  = "11111111-2a4f-4d6e-9a10-11f2e3d4c5b6"
	projTxnExpense = "22222222-2a4f-4d6e-9a10-11f2e3d4c5b6"
	projBucketOct  = "2026-10-01"
	projBucketNov  = "2026-11-01"
	projOccurredAt = "2026-10-18T12:00:00Z"
)

func newProjFinanceTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// homeos_0004 的形状（列名/非空性/默认值一致；uuid/date/bigint/timestamptz 在 sqlite 里的对应物）。
	require.NoError(t, db.Exec(`CREATE TABLE homeos_proj_finance (
		family_id TEXT NOT NULL,
		bucket_month TEXT NOT NULL,
		income_cents INTEGER NOT NULL DEFAULT 0,
		expense_cents INTEGER NOT NULL DEFAULT 0,
		budget_remaining_cents INTEGER,
		updated_at DATETIME NOT NULL,
		UNIQUE (family_id, bucket_month)
	)`).Error)
	// homeos_0012 的形状：transaction_id 主键是重放锚，bucket_month 是分组成员。
	require.NoError(t, db.Exec(`CREATE TABLE homeos_proj_finance_txn (
		transaction_id TEXT PRIMARY KEY,
		family_id TEXT NOT NULL,
		bucket_month TEXT NOT NULL,
		income_cents INTEGER NOT NULL DEFAULT 0,
		expense_cents INTEGER NOT NULL DEFAULT 0,
		occurred_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error)
}

// createdEnvelope is the wire shape of contracts/events/finance.yaml:17-36 (FROZEN): business_id is the
// transaction_id, and the payload holds exactly the eight payload_schema keys.
func createdEnvelope(txnID, txnType string, amountCents int64, occurredAt string) bus.Envelope {
	return bus.Envelope{
		EventType:  TransactionCreatedEventType,
		BusinessID: txnID,
		FamilyID:   fixtureFamilyID,
		Version:    "1.0",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": txnID,
			"family_id":      fixtureFamilyID,
			"type":           txnType,
			"amount_cents":   amountCents,
			"category_id":    "33333333-2a4f-4d6e-9a10-11f2e3d4c5b6",
			"account_id":     "44444444-2a4f-4d6e-9a10-11f2e3d4c5b6",
			"occurred_at":    occurredAt,
			"created_by":     "55555555-2a4f-4d6e-9a10-11f2e3d4c5b6",
		},
	}
}

// transactionMessage builds the bus.Message for any of the three subjects; the handler dispatches on
// Envelope.EventType, and Subject equals the event_type per §3.2「subject == event_type」.
func transactionMessage(eventType string, env bus.Envelope) bus.Message {
	return bus.Message{Subject: eventType, Envelope: env}
}

// financeBucketRow reads back one homeos_proj_finance row. BudgetRemainingCents is a pointer so the
// NULL（未设预算）state is assertable as distinct from 0.
type financeBucketRow struct {
	FamilyID             string    `gorm:"column:family_id"`
	BucketMonth          string    `gorm:"column:bucket_month"`
	IncomeCents          int64     `gorm:"column:income_cents"`
	ExpenseCents         int64     `gorm:"column:expense_cents"`
	BudgetRemainingCents *int64    `gorm:"column:budget_remaining_cents"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func readBucket(t *testing.T, db *gorm.DB, bucket string) (financeBucketRow, bool) {
	t.Helper()

	var row financeBucketRow
	err := db.Table("homeos_proj_finance").Where("family_id = ? AND bucket_month = ?", fixtureFamilyID, bucket).
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return row, false
	}
	require.NoError(t, err)
	return row, true
}

func countMirrorRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var n int64
	require.NoError(t, db.Table("homeos_proj_finance_txn").Count(&n).Error)
	return n
}

// ==================== created ====================

func TestTxnCreatedIncomeBucketsMonth(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	require.NoError(t, FinanceTransactionProjectionHandler(db)(context.Background(),
		transactionMessage(TransactionCreatedEventType,
			createdEnvelope(projTxnIncome, "income", 1200000, projOccurredAt))))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found, "收入必须进 2026-10 月桶")
	assert.Equal(t, int64(1200000), row.IncomeCents)
	assert.Equal(t, int64(0), row.ExpenseCents)
	assert.Nil(t, row.BudgetRemainingCents, "流水事件不谈预算，budget_remaining_cents 保持 NULL（未设）")
	assert.Equal(t, int64(1), countMirrorRows(t, db))
}

func TestTxnCreatedExpenseStoresPositiveMagnitude(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	// svc-finance 发的是负数（model/finance.go:67），投影存的是幅值：faces.go:236 直接
	// 「本周期支出 」+ formatMoney(ExpenseCents)，不再取负。
	require.NoError(t, FinanceTransactionProjectionHandler(db)(context.Background(),
		transactionMessage(TransactionCreatedEventType,
			createdEnvelope(projTxnExpense, "expense", -8880, projOccurredAt))))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found)
	assert.Equal(t, int64(0), row.IncomeCents)
	assert.Equal(t, int64(8880), row.ExpenseCents, "expense_cents 必须是非负幅值，否则卡片读成「本周期支出 -¥88.80」")
}

func TestTxnCreatedTransferContributesNothing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	require.NoError(t, FinanceTransactionProjectionHandler(db)(context.Background(),
		transactionMessage(TransactionCreatedEventType,
			createdEnvelope(projTxnIncome, "transfer", 500000, projOccurredAt))))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found, "转账仍是流水（桶存在，HasData 成立）")
	assert.Zero(t, row.IncomeCents, "转账是家庭内部挪动，不计入收入")
	assert.Zero(t, row.ExpenseCents, "也不计入支出")
}

func TestTxnCreatedReplayTwiceSameTotals(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	ctx := context.Background()
	handler := FinanceTransactionProjectionHandler(db)
	income := transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnIncome, "income", 100000, projOccurredAt))
	expense := transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnExpense, "expense", -25000, projOccurredAt))

	require.NoError(t, handler(ctx, income))
	require.NoError(t, handler(ctx, expense))
	require.NoError(t, handler(ctx, income)) // 重投：同一个 (event_type, business_id) 再跑一次 handler
	require.NoError(t, handler(ctx, expense))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found)
	assert.Equal(t, int64(100000), row.IncomeCents, "重投不重复计入（PRD 10.4）")
	assert.Equal(t, int64(25000), row.ExpenseCents)
	assert.Equal(t, int64(2), countMirrorRows(t, db), "镜像按 transaction_id 去重，不追加第二行")
}

// ==================== updated ====================

func TestTxnUpdatedWithAmountRewritesBucket(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	ctx := context.Background()
	handler := FinanceTransactionProjectionHandler(db)
	require.NoError(t, handler(ctx, transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnExpense, "expense", -30000, projOccurredAt))))

	// finance.yaml:41-45：updated 只带 transaction_id/family_id/updated_fields/version，
	// 金额藏在 updated_fields 里（JSON number 解码为 float64，消费侧要能接住）。
	env := bus.Envelope{
		EventType: TransactionUpdatedEventType, BusinessID: projTxnExpense, FamilyID: fixtureFamilyID,
		Version: "1.0", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": projTxnExpense,
			"family_id":      fixtureFamilyID,
			"updated_fields": map[string]any{"amount_cents": float64(-50000)},
			"version":        float64(2),
		},
	}
	require.NoError(t, handler(ctx, transactionMessage(TransactionUpdatedEventType, env)))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found)
	assert.Equal(t, int64(50000), row.ExpenseCents, "改额走重算：500 而不是 300+200 的增量")
	assert.Equal(t, int64(1), countMirrorRows(t, db))
}

func TestTxnUpdatedMovesMonthReplacesBucket(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	ctx := context.Background()
	handler := FinanceTransactionProjectionHandler(db)
	require.NoError(t, handler(ctx, transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnExpense, "expense", -30000, projOccurredAt))))

	env := bus.Envelope{
		EventType: TransactionUpdatedEventType, BusinessID: projTxnExpense, FamilyID: fixtureFamilyID,
		Version: "1.0", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": projTxnExpense,
			"family_id":      fixtureFamilyID,
			"updated_fields": map[string]any{"occurred_at": "2026-11-02T09:00:00Z"},
			"version":        float64(3),
		},
	}
	require.NoError(t, handler(ctx, transactionMessage(TransactionUpdatedEventType, env)))

	_, old := readBucket(t, db, projBucketOct)
	assert.False(t, old, "改月之后旧桶重算为空，行必须被清掉，否则旧月卡片挂着幽灵支出")
	row, found := readBucket(t, db, projBucketNov)
	require.True(t, found)
	assert.Equal(t, int64(30000), row.ExpenseCents)
}

func TestTxnUpdatedWithoutMoneyKeysLeavesTotals(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	ctx := context.Background()
	handler := FinanceTransactionProjectionHandler(db)
	require.NoError(t, handler(ctx, transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnIncome, "income", 100000, projOccurredAt))))

	env := bus.Envelope{
		EventType: TransactionUpdatedEventType, BusinessID: projTxnIncome, FamilyID: fixtureFamilyID,
		Version: "1.0", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"transaction_id": projTxnIncome,
			"family_id":      fixtureFamilyID,
			"updated_fields": map[string]any{"category_id": "99999999-2a4f-4d6e-9a10-11f2e3d4c5b6"},
			"version":        float64(2),
		},
	}
	require.NoError(t, handler(ctx, transactionMessage(TransactionUpdatedEventType, env)))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found)
	assert.Equal(t, int64(100000), row.IncomeCents, "分类改动不动钱")
}

// ==================== deleted ====================

func TestTxnDeletedRemovesContributionAndEmptyingBucketDropsRow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)

	ctx := context.Background()
	handler := FinanceTransactionProjectionHandler(db)
	require.NoError(t, handler(ctx, transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnIncome, "income", 100000, projOccurredAt))))
	require.NoError(t, handler(ctx, transactionMessage(TransactionCreatedEventType,
		createdEnvelope(projTxnExpense, "expense", -25000, projOccurredAt))))

	deleted := func(txnID string) bus.Message {
		return transactionMessage(TransactionDeletedEventType, bus.Envelope{
			EventType: TransactionDeletedEventType, BusinessID: txnID, FamilyID: fixtureFamilyID,
			Version: "1.0", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Payload: map[string]any{
				"transaction_id": txnID,
				"family_id":      fixtureFamilyID,
				"deleted_at":     "2026-10-20T08:00:00Z",
				"deleted_by":     "55555555-2a4f-4d6e-9a10-11f2e3d4c5b6",
			},
		})
	}

	require.NoError(t, handler(ctx, deleted(projTxnExpense)))
	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found, "另一笔还在，桶不该消失")
	assert.Equal(t, int64(100000), row.IncomeCents, "另一翼不受牵连")
	assert.Equal(t, int64(0), row.ExpenseCents, "删除要撤掉贡献（PRD 10.4），不是留 -25000 也不是留 25000")

	require.NoError(t, handler(ctx, deleted(projTxnIncome)))
	_, found = readBucket(t, db, projBucketOct)
	assert.False(t, found, "全删后月桶行消失 —— 首页那一格才回到「本周期暂无记账」而不是「本周期支出 ¥0」")
	assert.Equal(t, int64(0), countMirrorRows(t, db))

	// 重复删除：镜像里已无此行，幂等终点，不是错误。
	require.NoError(t, handler(ctx, deleted(projTxnIncome)))
	require.NoError(t, handler(ctx, deleted("00000000-0000-0000-0000-000000000000")))
}

// ==================== refusals ====================

func TestTxnHandlerRefusals(t *testing.T) {
	cases := []struct {
		name    string
		msg     bus.Message
		wantErr error
	}{
		{
			name:    "空 payload 的扁平信封",
			msg:     bus.Message{Subject: TransactionCreatedEventType, Envelope: bus.Envelope{EventType: TransactionCreatedEventType}},
			wantErr: ErrNoTxnPayload,
		},
		{
			name: "缺 transaction_id",
			msg: func() bus.Message {
				env := createdEnvelope("", "expense", -1, projOccurredAt)
				delete(env.Payload, "transaction_id")
				return transactionMessage(TransactionCreatedEventType, env)
			}(),
			wantErr: ErrNoTxnID,
		},
		{
			name: "缺 family_id",
			msg: func() bus.Message {
				env := createdEnvelope(projTxnIncome, "expense", -1, projOccurredAt)
				delete(env.Payload, "family_id")
				env.FamilyID = ""
				return transactionMessage(TransactionCreatedEventType, env)
			}(),
			wantErr: ErrNoTxnFamilyID,
		},
		{
			name: "occurred_at 缺失/不可解析则无法定月桶",
			msg: func() bus.Message {
				env := createdEnvelope(projTxnIncome, "expense", -1, "not-a-date")
				return transactionMessage(TransactionCreatedEventType, env)
			}(),
			wantErr: ErrTxnOccurredAt,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			newProjFinanceTables(t, db)

			err = FinanceTransactionProjectionHandler(db)(context.Background(), tc.msg)
			require.ErrorIs(t, err, tc.wantErr, "被拒的投递要报错，走 §3.4 的重试-死信阶梯而不是静默丢钱")
			assert.Equal(t, int64(0), countMirrorRows(t, db), "被拒的投递在镜像里留下 0 行")
			var n int64
			require.NoError(t, db.Table("homeos_proj_finance").Count(&n).Error)
			assert.Equal(t, int64(0), n, "也不留下任何聚合行")
		})
	}
}

// ==================== the chain around the handler ====================

// TestDurableConsumerPathDeduplicatesTransaction: §3.4's chain for the money leg -- the shipped durable
// consumer with this handler attached, the same finance.transaction.created delivered twice: one bucket
// write, the second delivery acked as a duplicate, nothing parked. The handler-level replay test above
// proves the recompute is safe even without the dedupe layer; this proves the dedupe layer is there.
func TestDurableConsumerPathDeduplicatesTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newProjFinanceTables(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code:          registry.HomeosCode,
		StreamName:    "HC_FINANCE",
		EventType:     TransactionCreatedEventType,
		FilterSubject: TransactionCreatedEventType,
	})
	require.NoError(t, consumer.Start(context.Background(), FinanceTransactionProjectionHandler(db)))
	require.Len(t, fake.subscriptions, 1)

	raw, err := json.Marshal(createdEnvelope(projTxnExpense, "expense", -8880, projOccurredAt))
	require.NoError(t, err)

	first := &testMsg{data: raw, subject: TransactionCreatedEventType}
	fake.subscriptions[0].handler(first)
	assert.Equal(t, 1, first.acks)
	assert.Zero(t, first.naks)

	second := &testMsg{data: raw, subject: TransactionCreatedEventType, redeliver: true}
	fake.subscriptions[0].handler(second)
	assert.Equal(t, 1, second.acks, "重复投递 ack 掉（§3.4「已存在即 ack」）")
	assert.Zero(t, second.naks)
	assert.Equal(t, int64(1), dedupeCount(t, db))
	assert.Empty(t, deadLetters(t, db))

	row, found := readBucket(t, db, projBucketOct)
	require.True(t, found)
	assert.Equal(t, int64(8880), row.ExpenseCents, "两次投递合计只计一次")
	assert.Equal(t, int64(1), countMirrorRows(t, db))
}
