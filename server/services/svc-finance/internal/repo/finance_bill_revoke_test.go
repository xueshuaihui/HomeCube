// finance_bill_revoke_test.go is the producer-side contract test of REV-1: paying a bill
// (repo.MarkAsPaid, the write behind handler.PayBill / PUT /api/finance/bills/:id/pay) must put exactly
// one finance.due.revoked row in the outbox, in the same transaction as the bill's own UPDATE.
//
// Why it exists: contracts/events/finance.yaml declares the 到期 chain as two events --
// finance.due.registered (:77, 「到期对象注册到HomeOS日历/提醒中心」) and finance.due.revoked (:93,
// 「到期对象撤销（业务对象删除或完成）」) -- and homeos_0008:43-44 names the second as
// homeos_due_registration 唯一的删除路径. Before this card the first had a producer and the second had
// none, so a paid bill's 到期行 stayed in 首页 B 区 permanently (PRD 卷首第 3 条's反向半边 missing).
//
// The assertions read the row back through the底座's own decoder (bus.UnmarshalEnvelope) rather than by
// digging through a map: that is the same call packages/bus/consumer.go:151 makes on the delivery bytes
// before any handler runs, so a payload written at the TOP level of the envelope -- the flat shape that
// makes svc-homeos's handlers answer ErrNoPayload -- cannot pass here.
package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/gorm"
)

// subjectDueRevoked is transcribed from contracts/events/finance.yaml:93 (the event name == the subject,
// §3.1「subject 命名即事件名：{code}.{object}.{action}」). Declared in the test rather than imported from
// the repo package's unexported const, so a producer that renamed the subject is caught by a name the
// contract itself pins.
const subjectDueRevoked = "finance.due.revoked"

// revokedPayloadKeys is contracts/events/finance.yaml:100-105's payload_schema, verbatim:
//
//	payload_schema:
//	  source_system: finance
//	  source_id: uuid
//	  family_id: uuid
//	  revoked_at: timestamp
//	  reason: enum(completed|deleted|expired)
//
// The set is asserted exactly: one key missing means the consumer cannot locate the registration, and
// one key invented means this service ships a field the frozen contract never agreed to (the kind column
// is registered's, :89 -- revoked does not carry it, which is why the撤销 anchor is source_system +
// source_id and is reported as a契约 difference rather than papered over).
var revokedPayloadKeys = []string{"source_system", "source_id", "family_id", "revoked_at", "reason"}

// outboxRow is one finance_outbox row as the deliverer reads it (finance_0002's seven columns).
type outboxRow struct {
	ID        int64  `gorm:"column:id"`
	FamilyID  string `gorm:"column:family_id"`
	Subject   string `gorm:"column:subject"`
	Envelope  string `gorm:"column:envelope"`
	Status    string `gorm:"column:status"`
	Attempts  int    `gorm:"column:attempts"`
	CreatedAt string `gorm:"column:created_at"`
}

func loadRevokedRows(t *testing.T, db *gorm.DB) []outboxRow {
	t.Helper()

	var rows []outboxRow
	require.NoError(t, db.Table("finance_outbox").Where("subject = ?", subjectDueRevoked).
		Order("id ASC").Find(&rows).Error)
	return rows
}

func paidBill(t *testing.T, ctx context.Context, r *repo.FinanceRepo, dueAt time.Time) *model.FinanceBill {
	t.Helper()

	bill := &model.FinanceBill{
		FamilyID:    "test-family-001",
		PayeeID:     "test-payee-001",
		AmountCents: 100000,
		DueAt:       dueAt,
		Status:      "pending",
		Version:     1,
	}
	require.NoError(t, r.CreateBill(ctx, bill))
	return bill
}

// TestMarkAsPaidEmitsDueRevokedInSameTransaction is the whole judgement on one call: the bill flips to
// paid, the change log gains its UPDATE row, and the outbox gains exactly one finance.due.revoked row
// carrying the contract's five payload fields.
func TestMarkAsPaidEmitsDueRevokedInSameTransaction(t *testing.T) {
	r, db := setupTestRepo(t)
	ctx := context.Background()

	dueAt := time.Date(2026, 10, 10, 9, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)) // 01:00 UTC
	bill := paidBill(t, ctx, r, dueAt)

	updated, err := r.MarkAsPaid(ctx, bill.FamilyID, bill.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.PaidAt)

	rows := loadRevokedRows(t, db)
	require.Len(t, rows, 1, "结清一次账单恰有一条 finance.due.revoked 待投递行")

	row := rows[0]
	assert.Equal(t, subjectDueRevoked, row.Subject, "§3.3「INSERT finance_outbox(subject, envelope, status=pending)」")
	assert.Equal(t, "pending", row.Status, "同事务写入的行是 pending，由投递器置 sent（§3.3）")
	assert.Equal(t, 0, row.Attempts)
	assert.Equal(t, bill.FamilyID, row.FamilyID, "outbox 行的 family_id 列（§10.3「指标最小集（全部带 family_id 与 code）」）")

	// The底座's own decoder: what the consumer sees before it reaches a handler.
	env := decodeRevokedEnvelope(t, row.Envelope)

	assert.Equal(t, subjectDueRevoked, env.EventType)
	assert.Equal(t, bill.FamilyID, env.FamilyID)
	assert.Equal(t, "1.0", env.Version, "§3.1「版本不进 subject 而进信封 version 字段」")
	_, err = time.Parse(time.RFC3339, env.Timestamp)
	assert.NoError(t, err, "timestamp 必须是 RFC3339")

	// business_id per contracts/events/finance.yaml:95「"{source_id}:{due_at}"」+ README.md:29 的幂等键口径.
	assert.Equal(t, bill.ID+":"+dueAt.UTC().Format(time.RFC3339), env.BusinessID)

	// ---- payload 逐字段（finance.yaml:100-105）----
	require.Len(t, env.Payload, len(revokedPayloadKeys), "payload 键集合必须恰是契约声明的那五个")
	for _, key := range revokedPayloadKeys {
		_, ok := env.Payload[key]
		assert.True(t, ok, "契约声明的字段 %s 必须在 payload 里", key)
	}
	assert.Equal(t, "finance", env.Payload["source_system"], "source_system: finance")
	assert.Equal(t, bill.ID, env.Payload["source_id"], "source_id: uuid —— 被撤销的那条注册的锚点")
	assert.Equal(t, bill.FamilyID, env.Payload["family_id"], "family_id: uuid")
	assert.Equal(t, updated.PaidAt.UTC().Format(time.RFC3339), env.Payload["revoked_at"],
		"revoked_at: timestamp —— 与账单的 paid_at 是同一个瞬间")
	assert.Equal(t, "completed", env.Payload["reason"],
		"reason: enum(completed|deleted|expired) —— 结清是 completed")

	// The same transaction, or nothing: bill row + change log row + outbox row all come from one commit.
	var changeLogs int64
	require.NoError(t, db.Table("finance_change_log").Where("entity = ? AND entity_id = ?", "bill", bill.ID).
		Count(&changeLogs).Error)
	assert.Equal(t, int64(2), changeLogs, "CREATE + UPDATE 两条变更日志与撤销行同批提交")
}

// TestMarkAsPaidRevokedFailureRollsBackBillUpdate is the outbox premise of §3.4.6 / PRD 3.4
// 「发布前落盘」 in the other direction: when the event write fails, the bill must not be committed as
// paid. The failure is forced at the database (a BEFORE INSERT trigger on the real finance_outbox table,
// RAISE(ABORT)) rather than by stubbing the code under test, so the code path -- transaction, ordering,
// error propagation -- is the shipped one.
func TestMarkAsPaidRevokedFailureRollsBackBillUpdate(t *testing.T) {
	r, db := setupTestRepo(t)
	ctx := context.Background()

	bill := paidBill(t, ctx, r, time.Now().AddDate(0, 0, 7))

	require.NoError(t, db.Exec(`CREATE TRIGGER force_outbox_failure
		BEFORE INSERT ON finance_outbox
		BEGIN SELECT RAISE(ABORT, 'forced outbox failure'); END`).Error)

	_, err := r.MarkAsPaid(ctx, bill.FamilyID, bill.ID)
	require.Error(t, err, "撤销事件写不进去时，整次结清必须失败")
	assert.Contains(t, err.Error(), "due revocation", "错误要指向撤销那一步，不是别处")

	// 业务行回滚：账单仍是 pending、version 仍是 1、paid_at 没有落下。
	retrieved, err := r.GetBillByID(ctx, bill.FamilyID, bill.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "pending", retrieved.Status, "账单不能留下已付状态（回滚）")
	assert.Equal(t, int64(1), retrieved.Version)
	assert.Nil(t, retrieved.PaidAt)

	// 同步底座的 UPDATE 行也回滚了（只剩 CREATE 那一条）。
	var changeLogs int64
	require.NoError(t, db.Table("finance_change_log").Count(&changeLogs).Error)
	assert.Equal(t, int64(1), changeLogs, "change log 不能留下半批")

	// outbox 里没有残留行。
	assert.Empty(t, loadRevokedRows(t, db), "finance_outbox 不能残留半条事件")

	require.NoError(t, db.Exec(`DROP TRIGGER force_outbox_failure`).Error)

	// 触发器撤掉后同一条路径恢复正常：撤销事件确实由这一步写出，不是被守卫吞掉。
	_, err = r.MarkAsPaid(ctx, bill.FamilyID, bill.ID)
	require.NoError(t, err)
	require.Len(t, loadRevokedRows(t, db), 1)
}

// TestMarkAsPaidRepeatKeepsOneIdempotencyKey pins what a double pay does. MarkAsPaid has no
// already-paid guard (handler.PayBill calls it on whatever id arrives), so the second call emits a
// second row; both carry the SAME {source_id}:{due_at} business_id, which is what
// {code}_event_dedupe's unique index turns into 「重复投递不产生重复业务对象」 (§3.4) on the consumer
// side -- the registration is revoked once, not twice.
func TestMarkAsPaidRepeatKeepsOneIdempotencyKey(t *testing.T) {
	r, db := setupTestRepo(t)
	ctx := context.Background()

	bill := paidBill(t, ctx, r, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))

	_, err := r.MarkAsPaid(ctx, bill.FamilyID, bill.ID)
	require.NoError(t, err)
	_, err = r.MarkAsPaid(ctx, bill.FamilyID, bill.ID)
	require.NoError(t, err)

	rows := loadRevokedRows(t, db)
	require.Len(t, rows, 2, "两次结清各写一条事件：是否重复由消费方的去重层判定，不在这里吞")

	first := decodeRevokedEnvelope(t, rows[0].Envelope)
	second := decodeRevokedEnvelope(t, rows[1].Envelope)
	assert.Equal(t, first.BusinessID, second.BusinessID,
		"幂等键只由 (source_id, due_at) 决定（finance.yaml:95），不随投递时刻变")
	assert.Equal(t, first.Payload["source_id"], second.Payload["source_id"])
}

// TestMarkAsPaidUnknownBillWritesNothing: 「bill not found」 stays a plain failure -- no event row, so a
// wrong id cannot revoke another household's registration.
func TestMarkAsPaidUnknownBillWritesNothing(t *testing.T) {
	r, db := setupTestRepo(t)
	ctx := context.Background()

	_, err := r.MarkAsPaid(ctx, "test-family-001", "00000000-0000-4000-8000-000000000000")
	require.Error(t, err)
	assert.Empty(t, loadRevokedRows(t, db))
	assert.Equal(t, int64(0), countOutboxRows(t, db))
}

func decodeRevokedEnvelope(t *testing.T, envelopeJSON string) bus.Envelope {
	t.Helper()

	env, err := bus.UnmarshalEnvelope([]byte(envelopeJSON))
	require.NoError(t, err, "信封必须能被底座自己的解码器读出来")
	require.NotEmpty(t, env.Payload,
		"payload 必须在信封的嵌套 payload 键下（packages/bus/interface.go:53）：写在顶层的扁平信封到消费侧只会得到「信封缺少 payload」")
	return env
}

func countOutboxRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var n int64
	require.NoError(t, db.Table("finance_outbox").Count(&n).Error)
	return n
}
