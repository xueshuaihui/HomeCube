package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ddlFinanceOutbox mirrors the shipped production DDL of migrations/finance/finance_0002: the column
// set is exactly (id, family_id, subject, envelope, status, attempts, created_at), status has only the
// two CHECK states, and there is no updated_at / sent_at / error column. The point of keeping it that
// way is that the write path under test goes through the base's own writer
// (bus.InsertOutboxMessageWithFamily, whose OutboxMessage struct is the same seven columns): a fixture
// that added invented columns would let either side drift away from 0002 and still pass here while
// failing on the real database.
const ddlFinanceOutbox = `
CREATE TABLE finance_outbox (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id  TEXT,
	subject    TEXT NOT NULL,
	envelope   TEXT NOT NULL,
	status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent')),
	attempts   INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

// ddlFinanceChangeLog mirrors the published DDL of
// migrations/finance/finance_0003_change_log_idempotency.up.sql:12-19 -- exactly the six columns
// §五 names (lsn bigserial, family_id, entity, entity_id, op, version). It used to carry data /
// created_at as well, which is what packages/sync's old ChangeLog struct declared; FS1 converged
// the struct on the migration, so the two extra columns are gone here too. Keeping the fixture on
// the migration's shape is the point: a fixture that still declared them would let a future struct
// drift back to eight columns and pass here while the real database rejects the insert.
// lsn is a bigserial in Postgres; INTEGER PRIMARY KEY AUTOINCREMENT is the sqlite equivalent that
// lets the database mint it, which is what the base relies on (it never sends lsn). The migration's
// finance_change_log_family_lsn_idx is an access path for the delta read, so it is not created here
// -- nothing in this package queries the log by (family_id, lsn).
const ddlFinanceChangeLog = `
CREATE TABLE finance_change_log (
	lsn       INTEGER PRIMARY KEY AUTOINCREMENT,
	family_id TEXT    NOT NULL,
	entity    TEXT    NOT NULL,
	entity_id TEXT    NOT NULL,
	op        TEXT    NOT NULL,
	version   INTEGER NOT NULL
)`

func setupTestDBForBill(t *testing.T) (*gorm.DB, *BillService) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// One connection: the in-memory database only exists for the connection that created it, and the
	// transaction tests below must run their begin/write/rollback on it.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Auto migrate test tables
	err = db.AutoMigrate(
		&model.FinanceBill{},
	)
	require.NoError(t, err)

	// The two 底座运行表 the bill path writes: the outbox row (this card's deliverable) and the change
	// log row that repo.CreateBill appends inside the same transaction.
	require.NoError(t, db.Exec(ddlFinanceOutbox).Error)
	require.NoError(t, db.Exec(ddlFinanceChangeLog).Error)

	billService := NewBillService(repo.NewFinanceRepo(db), db, "finance")
	return db, billService
}

// dueRow is one finance_outbox row, read back the way the deliverer will read it.
type dueRow struct {
	FamilyID string `gorm:"column:family_id"`
	Subject  string `gorm:"column:subject"`
	Envelope string `gorm:"column:envelope"`
	Status   string `gorm:"column:status"`
}

func loadDueRows(t *testing.T, db *gorm.DB) []dueRow {
	t.Helper()

	var rows []dueRow
	require.NoError(t, db.Table("finance_outbox").Where("subject = ?", "finance.due.registered").
		Order("id ASC").Find(&rows).Error)
	return rows
}

func TestNewBillService(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	assert.NotNil(t, service)
	assert.Equal(t, "finance", service.code)
}

// TestRegisterBillDue is the contract test for finance.due.registered. The field list it asserts is
// transcribed from the FROZEN contracts/events/finance.yaml entry (v1.0.0, 2026-10-03):
//
//	event_type: finance.due.registered
//	business_id: "{source_id}:{due_at}"                       <- contracts/README.md:29 (PRD 10.4)
//	payload_schema: source_system: finance / source_id: uuid / family_id: uuid /
//	                due_at: timestamp / kind: enum(bill|budget|goal|repayment) /
//	                title: string / members: array[uuid]
//
// ENV-1 put those seven fields into the envelope's `payload` object instead of the top level. That is
// the shape bus.Envelope declares (packages/bus/interface.go:41-66) and the only shape
// svc-homeos/internal/consumer/due_registered_handler.go:36 accepts; the pre-ENV-1 flat form decoded
// to Payload == nil, so every real delivery was NAK-ed into the dead letter and never reached
// homeos_due_registration.
//
// amount_cents, which the flat envelope used to carry, is gone: it is not in the FROZEN
// payload_schema, and svc-homeos' DueRegistrationEvent.AmountCents is declared `omitempty` and never
// read by HandleDueRegistered's write path, so dropping it changes nothing on the consumer side.
func TestRegisterBillDue(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	now := time.Now()

	bill := &model.FinanceBill{
		ID:          "bill-001",
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 100000,
		DueAt:       now.AddDate(0, 0, 7),
		Status:      "pending",
		Version:     1,
	}

	// Create the bill first
	err := db.WithContext(ctx).Create(bill).Error
	require.NoError(t, err)

	// Register due date
	err = service.RegisterBillDue(ctx, db, bill)
	require.NoError(t, err)

	// Verify outbox message was created
	rows := loadDueRows(t, db)
	require.Len(t, rows, 1, "一次账单到期注册恰有一条 finance.due.registered 待投递行")

	row := rows[0]
	assert.Equal(t, "finance.due.registered", row.Subject)
	assert.Equal(t, "pending", row.Status)
	assert.Equal(t, bill.FamilyID, row.FamilyID, "outbox 行的 family_id 列（§10.3 指标最小集要求带 family_id）")

	// ---- 信封外形：契约载荷在 payload 子对象里，顶层只剩元数据 ----
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.Envelope), &raw))
	require.Contains(t, raw, "payload", "落库信封必须有 payload 键（bus.Envelope 的 json tag，packages/bus/interface.go:53）")
	for _, flattened := range []string{"source_system", "source_id", "due_at", "kind", "title", "members"} {
		assert.NotContains(t, raw, flattened, "%s 已搬进 payload，顶层不能再有扁平残留", flattened)
	}

	env := decodeDueEnvelope(t, row.Envelope)
	payload := env.Payload

	// ---- 契约 payload_schema 逐字段（不多不少，finance.yaml:84-91）----
	assert.ElementsMatch(t,
		[]string{"source_system", "source_id", "family_id", "due_at", "kind", "title", "members"},
		payloadKeys(payload),
		"payload 字段集合要逐字段等于冻结契约的 payload_schema")
	assert.Equal(t, "finance", payload["source_system"], "source_system: finance")
	assert.Equal(t, bill.ID, payload["source_id"], "source_id: uuid")
	assert.Equal(t, bill.FamilyID, payload["family_id"], "family_id: uuid")
	assert.Equal(t, bill.DueAt.UTC().Format(time.RFC3339), payload["due_at"], "due_at: timestamp")
	// kind 只能是冻结枚举 bill|budget|goal|repayment 之一；账单路径只能是 bill。
	assert.Equal(t, "bill", payload["kind"], "kind: enum(bill|budget|goal|repayment)")
	assert.IsType(t, "", payload["title"], "title: string")
	assert.NotEmpty(t, payload["title"])
	members, ok := payload["members"]
	require.True(t, ok, "members 必须在契约声明的字段集合里")
	memberList, isArray := members.([]any)
	require.True(t, isArray, "members: array[uuid]")
	assert.Empty(t, memberList, "FinanceBill 没有成员归属列，注册成员集为空集（不编造成员）")

	// ---- 信封元数据（§3.1、PRD 10.4）----
	assert.Equal(t, "finance.due.registered", env.EventType,
		"event_type 是消费侧去重键的一半；缺了它 packages/bus/consumer.go:210-215 只 Nak、不写死信，消息静默消失")
	assert.Equal(t, bill.FamilyID, env.FamilyID)
	assert.Equal(t, "1.0", env.Version)
	_, err = time.Parse(time.RFC3339, env.Timestamp)
	assert.NoError(t, err, "timestamp 必须是 RFC3339")

	// business_id = {source_id}:{due_at}（改期即重发的幂等锚点）
	assert.Equal(t, bill.ID+":"+bill.DueAt.UTC().Format(time.RFC3339), env.BusinessID)

	// due_at 必须与账单的到期时刻是同一个瞬间（消费方用 time.RFC3339 解析后写 timestamptz）。
	// RFC3339 只到秒，所以比较基准是截断到秒——用 Round 会在 .5s 以上时与序列化结果差一秒。
	dueAt, err := time.Parse(time.RFC3339, payload["due_at"].(string))
	require.NoError(t, err)
	assert.True(t, dueAt.Equal(bill.DueAt.Truncate(time.Second)),
		"due_at 解析回来的瞬间要等于 bill.DueAt（截断到秒），got %v want %v", dueAt, bill.DueAt.Truncate(time.Second))
}

// decodeDueEnvelope reads one finance_outbox envelope column back through the base's own decoder:
// bus.UnmarshalEnvelope is json.Unmarshal into bus.Envelope, the same call
// packages/bus/consumer.go:151-157 makes on the delivery bytes before any handler runs.
func decodeDueEnvelope(t *testing.T, envelopeJSON string) bus.Envelope {
	t.Helper()

	env, err := bus.UnmarshalEnvelope([]byte(envelopeJSON))
	require.NoError(t, err, "底座解码失败")
	return env
}

// payloadKeys lists a payload object's keys, for set comparisons against the contract.
func payloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}

// TestRegisterBillDue_DueAtIsUTC pins the timezone bug: the previous code formatted with the layout
// "2006-01-02T15:04:05Z", which prints the wall clock of the bill's own location and appends a literal
// Z -- for a +08:00 deployment that shifts the registered due instant by 8 hours.
func TestRegisterBillDue_DueAtIsUTC(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	cst := time.FixedZone("UTC+8", 8*60*60)
	bill := &model.FinanceBill{
		ID:          "bill-tz-001",
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 1,
		DueAt:       time.Date(2026, 10, 10, 9, 0, 0, 0, cst), // 09:00 +08:00 == 01:00 UTC
		Status:      "pending",
		Version:     1,
	}
	require.NoError(t, db.WithContext(ctx).Create(bill).Error)
	require.NoError(t, service.RegisterBillDue(ctx, db, bill))

	rows := loadDueRows(t, db)
	require.Len(t, rows, 1)

	env := decodeDueEnvelope(t, rows[0].Envelope)
	assert.Equal(t, "2026-10-10T01:00:00Z", env.Payload["due_at"])
	assert.Equal(t, "bill-tz-001:2026-10-10T01:00:00Z", env.BusinessID, "幂等键在信封元数据上（bus.Envelope.BusinessID）")
}

// TestRegisterBillDue_RescheduleEmitsANewKey covers 幂等键随 due_at 变化（contracts/README.md:29
// 「{source_id}:{due_at}（改期即重发）」）: the consumer upserts on (source_system, source_id, kind),
// so a rescheduled bill must carry a different business_id or it is swallowed as a duplicate.
func TestRegisterBillDue_RescheduleEmitsANewKey(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	bill := &model.FinanceBill{
		ID:          "bill-resched-001",
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 500,
		DueAt:       due,
		Status:      "pending",
		Version:     1,
	}
	require.NoError(t, db.WithContext(ctx).Create(bill).Error)
	require.NoError(t, service.RegisterBillDue(ctx, db, bill))

	// 改期
	bill.DueAt = due.AddDate(0, 0, 5)
	require.NoError(t, service.RegisterBillDue(ctx, db, bill))

	rows := loadDueRows(t, db)
	require.Len(t, rows, 2)

	first := decodeDueEnvelope(t, rows[0].Envelope)
	second := decodeDueEnvelope(t, rows[1].Envelope)

	assert.Equal(t, "bill-resched-001:2026-11-01T00:00:00Z", first.BusinessID)
	assert.Equal(t, "bill-resched-001:2026-11-06T00:00:00Z", second.BusinessID)
	assert.NotEqual(t, first.BusinessID, second.BusinessID, "改期即重发：幂等键必须随 due_at 变")
	assert.Equal(t, first.Payload["source_id"], second.Payload["source_id"], "同一个账单")
	assert.Equal(t, "bill", second.Payload["kind"])

	// 同一 due_at 再注册一次得到同一个键（去重锚点稳定）
	require.NoError(t, service.RegisterBillDue(ctx, db, bill))
	rows = loadDueRows(t, db)
	require.Len(t, rows, 3)
	third := decodeDueEnvelope(t, rows[2].Envelope)
	assert.Equal(t, second.BusinessID, third.BusinessID)
}

// TestCreateBillWithDueRegistration_CommitsBillAndEventTogether is the outbox premise of §3.4.6:
// the business write and the event write share one transaction.
func TestCreateBillWithDueRegistration_CommitsBillAndEventTogether(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	bill := &model.FinanceBill{
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 88000,
		DueAt:       time.Now().AddDate(0, 0, 3).UTC().Round(time.Second),
		Status:      "pending",
		Version:     1,
	}

	require.NoError(t, service.CreateBillWithDueRegistration(ctx, bill))

	// 账单落库（id 由 repo.CreateBill 生成）
	var bills []model.FinanceBill
	require.NoError(t, db.Find(&bills).Error)
	require.Len(t, bills, 1)
	assert.NotEmpty(t, bills[0].ID)

	// 同事务内的同步底座变更日志也写进来了
	var changeLogCount int64
	require.NoError(t, db.Table("finance_change_log").Count(&changeLogCount).Error)
	assert.Equal(t, int64(1), changeLogCount)

	// 到期注册事件同批落库，且指向刚创建的那笔账单
	rows := loadDueRows(t, db)
	require.Len(t, rows, 1)
	env := decodeDueEnvelope(t, rows[0].Envelope)
	assert.Equal(t, bills[0].ID, env.Payload["source_id"])
	assert.Equal(t, "bill", env.Payload["kind"])
	assert.Equal(t, bills[0].DueAt.UTC().Format(time.RFC3339), env.Payload["due_at"])
	assert.Equal(t, bills[0].ID+":"+bills[0].DueAt.UTC().Format(time.RFC3339), env.BusinessID)
	assert.Equal(t, bills[0].FamilyID, env.Payload["family_id"], "契约 payload 也要带 family_id：消费方写 homeos_due_registration.family_id 读的是它")
}

// TestCreateBillWithDueRegistration_RollbackWhenRegistrationFails is the same-transaction evidence:
// when the outbox write fails, the bill row must NOT be committed and no half-written event may be
// left behind. The failure is injected by pointing the service at a code whose outbox table does not
// exist (the real-world shape of "迁移没到位/事件写不进去"), not by stubbing the code under test.
func TestCreateBillWithDueRegistration_RollbackWhenRegistrationFails(t *testing.T) {
	db, _ := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	broken := NewBillService(repo.NewFinanceRepo(db), db, "finance_unmigrated")

	ctx := context.Background()
	bill := &model.FinanceBill{
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 77000,
		DueAt:       time.Now().AddDate(0, 0, 2).UTC().Round(time.Second),
		Status:      "pending",
		Version:     1,
	}

	err := broken.CreateBillWithDueRegistration(ctx, bill)
	require.Error(t, err, "注册失败必须让整次写失败")

	// 业务行回滚
	var billCount int64
	require.NoError(t, db.Model(&model.FinanceBill{}).Count(&billCount).Error)
	assert.Equal(t, int64(0), billCount, "账单行不能留下（回滚）")

	// 业务行的同步底座副作用也回滚
	var changeLogCount int64
	require.NoError(t, db.Table("finance_change_log").Count(&changeLogCount).Error)
	assert.Equal(t, int64(0), changeLogCount)

	// outbox 里没有残留行
	var outboxCount int64
	require.NoError(t, db.Table("finance_outbox").Count(&outboxCount).Error)
	assert.Equal(t, int64(0), outboxCount, "finance_outbox 不能残留半条事件")
}

func TestRegisterBillDue_InvalidBill(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()

	// Try to register a bill that doesn't exist in database
	bill := &model.FinanceBill{
		ID:          "bill-nonexistent",
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 100000,
		DueAt:       time.Now().AddDate(0, 0, 7),
		Status:      "pending",
		Version:     1,
	}

	// This should fail because the bill is not in the database
	// Note: The current implementation doesn't validate bill existence, it just publishes the event
	// In production, this would be called after the bill is successfully created -- i.e. by
	// CreateBillWithDueRegistration, inside that creation's transaction.
	err := service.RegisterBillDue(ctx, db, bill)
	require.NoError(t, err) // Current implementation doesn't check existence

	// 断言做实：事件确实写进了 outbox，且幂等键就是契约口径的 {source_id}:{due_at}
	rows := loadDueRows(t, db)
	require.Len(t, rows, 1)
	env := decodeDueEnvelope(t, rows[0].Envelope)
	assert.Equal(t, "bill-nonexistent", env.Payload["source_id"])
	assert.Equal(t, "bill", env.Payload["kind"])
	assert.Equal(t, "bill-nonexistent:"+bill.DueAt.UTC().Format(time.RFC3339), env.BusinessID)
}

// TestRegisterBillDue_GuardsNilHandles pins the two new argument guards (a nil bill or a nil
// transaction handle must be an error, never a silently dropped registration).
func TestRegisterBillDue_GuardsNilHandles(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	require.Error(t, service.RegisterBillDue(ctx, db, nil))
	require.Error(t, service.RegisterBillDue(ctx, nil, &model.FinanceBill{ID: "bill-x", DueAt: time.Now()}))

	// 守卫不吞下副作用：两次失败都没有写 outbox
	rows := loadDueRows(t, db)
	assert.Len(t, rows, 0)
}

// ============================== ENV-1 跨包契约测试 ==============================
//
// Before ENV-1 the two halves of 「账单到期 → 首页 B 区」 each had a green test written against its own
// idea of the wire shape: this package asserted the flat form (the old TestRegisterBillDue), while
// svc-homeos built nested envelopes in its own fixture. Nothing ever fed one side's bytes to the other,
// so the chain was dead in production with zero red tests. These two cases close that gap in the only
// direction this card is allowed to reach: producer bytes -> consumer entry point.
//
// errNoPayloadAtConsumer is svc-homeos' refusal, text and all, transcribed from
// services/svc-homeos/internal/consumer/due_registered_handler.go:30 (`ErrNoPayload`).
var errNoPayloadAtConsumer = errors.New("finance.due.registered 信封缺少 payload")

// consumerAccepts is the first statement of svc-homeos' DueRegisteredHandler, transcribed verbatim from
// services/svc-homeos/internal/consumer/due_registered_handler.go:35-38, plus the dedupe-key guard the
// base applies before the handler is ever reached (packages/bus/consumer.go:210-215).
//
// It is a transcription because Go's internal rule makes it un-importable here:
// services/svc-homeos/internal/** may only be imported from within services/svc-homeos/, and ENV-1 is
// forbidden from touching that service. What is NOT a transcription is the input: the bytes under test
// come out of the real producer's real finance_outbox column, and the decode is the base's own
// bus.UnmarshalEnvelope -- the same json.Unmarshal into bus.Envelope the real consumer runs
// (packages/bus/consumer.go:151-157). A mirror that only matched another mirror is what this card is
// fixing, not repeating.
func consumerAccepts(msg bus.Message) error {
	if msg.Envelope.EventType == "" || msg.Envelope.BusinessID == "" {
		// Same wording as packages/bus/consumer.go:215, so the mirrored refusal is comparable to the
		// real one instead of a message this test invented.
		return fmt.Errorf("bus: dedupe key (event_type, business_id) needs both parts, got (%q, %q)",
			msg.Envelope.EventType, msg.Envelope.BusinessID)
	}
	if len(msg.Envelope.Payload) == 0 {
		return errNoPayloadAtConsumer
	}
	return nil
}

// dueEventView is the flat read model HandleDueRegistered unmarshals the payload object into
// (services/svc-homeos/internal/consumer/finance_consumer.go:16-24). Tags transcribed from there.
// The real struct's seventh field, `AmountCents int64 \`json:"amount_cents,omitempty"\“, is left out
// on purpose: it is never read by that function's Create map, and ENV-1 dropped amount_cents from the
// payload because contracts/events/finance.yaml does not declare it. `members` is declared by the
// contract and simply absent from the consumer's struct -- nothing to read, hence nothing to assert
// beyond its presence in the payload map.
type dueEventView struct {
	SourceSystem string `json:"source_system"`
	SourceID     string `json:"source_id"`
	Kind         string `json:"kind"`
	DueAt        string `json:"due_at"`
	Title        string `json:"title"`
	FamilyID     string `json:"family_id,omitempty"`
}

// TestDueRegisteredEnvelope_CrossPackageContract feeds the bytes svc-finance actually wrote to
// finance_outbox through the consumer's entry point and requires them to be accepted, field by field.
func TestDueRegisteredEnvelope_CrossPackageContract(t *testing.T) {
	db, service := setupTestDBForBill(t)
	defer func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	}()

	ctx := context.Background()
	bill := &model.FinanceBill{
		ID:          "bill-cross-001",
		FamilyID:    "test-family-001",
		PayeeID:     "payee-001",
		AmountCents: 4200,
		DueAt:       time.Date(2026, 12, 1, 2, 30, 0, 0, time.UTC),
		Status:      "pending",
		Version:     1,
	}
	require.NoError(t, db.WithContext(ctx).Create(bill).Error)
	require.NoError(t, service.RegisterBillDue(ctx, db, bill))

	rows := loadDueRows(t, db)
	require.Len(t, rows, 1)

	// The outbox envelope column IS the delivery body: packages/bus/outbox.go ships it verbatim to
	// JetStream, so these bytes are what the consumer will be handed.
	delivered := []byte(rows[0].Envelope)

	env, err := bus.UnmarshalEnvelope(delivered)
	require.NoError(t, err, "底座解码失败 = 死信（consumer.go:152-157）")
	msg := bus.Message{Subject: rows[0].Subject, Envelope: env}

	// (1) 不被 ErrNoPayload 拒掉 —— 这就是 ENV-1 之前每一条真实消息的结局。
	require.NoError(t, consumerAccepts(msg),
		"生产侧真实产出的信封必须过消费侧守卫（got: %v）", consumerAccepts(msg))

	// (2) 幂等键两半齐全，否则 consumer.go:210-215 只 Nak、不写死信。
	assert.Equal(t, "finance.due.registered", env.EventType)
	assert.Equal(t, "bill-cross-001:2026-12-01T02:30:00Z", env.BusinessID)

	// (3) payload 逐字段可读：按消费方的方式重新序列化 payload 再解码成它的读模型。
	data, err := json.Marshal(env.Payload)
	require.NoError(t, err)
	var view dueEventView
	require.NoError(t, json.Unmarshal(data, &view))
	assert.Equal(t, "finance", view.SourceSystem)
	assert.Equal(t, "bill-cross-001", view.SourceID)
	assert.Equal(t, "bill", view.Kind)
	assert.Equal(t, "test-family-001", view.FamilyID)
	assert.Equal(t, "账单到期: bill-cross-001", view.Title)

	// (4) due_at 过得了消费方那一行 time.Parse(time.RFC3339, event.DueAt)（finance_consumer.go:35-39）：
	// 解析失败同样是整条事件进死信。
	dueAt, err := time.Parse(time.RFC3339, view.DueAt)
	require.NoError(t, err, "消费侧解析 due_at 失败")
	assert.True(t, dueAt.Equal(bill.DueAt.Truncate(time.Second)))

	// (5) 契约声明的 7 个字段全在 payload 里（members 消费方结构体没建字段，只断言存在）。
	assert.ElementsMatch(t,
		[]string{"source_system", "source_id", "family_id", "due_at", "kind", "title", "members"},
		payloadKeys(env.Payload))
}

// TestDueRegisteredEnvelope_FlatShapeIsRejected is the regression half: the exact flat envelope
// svc-finance used to write (every key at the top level, no payload object) must be refused by the very
// same entry point, so this defect can never again pass by both sides agreeing with themselves.
func TestDueRegisteredEnvelope_FlatShapeIsRejected(t *testing.T) {
	const flatBeforeENV1 = `{"amount_cents":100000,"business_id":"bill-001:2026-12-01T02:30:00Z",` +
		`"due_at":"2026-12-01T02:30:00Z","event_type":"finance.due.registered",` +
		`"family_id":"test-family-001","kind":"bill","members":[],"source_id":"bill-001",` +
		`"source_system":"finance","timestamp":"2026-10-28T00:00:00Z","title":"账单到期: bill-001",` +
		`"version":"1.0"}`

	// The flat form is not malformed JSON: it decodes fine, which is precisely why the old unit tests
	// stayed green. The metadata half lands on the struct; the contract half has nowhere to go.
	env, err := bus.UnmarshalEnvelope([]byte(flatBeforeENV1))
	require.NoError(t, err)
	assert.Empty(t, env.Payload, "顶层扁平键不会落进 bus.Envelope.Payload —— 消费方读到的就是空 payload")
	assert.Equal(t, "finance.due.registered", env.EventType, "元数据在顶层，所以解码认得出它")

	msg := bus.Message{Subject: "finance.due.registered", Envelope: env}
	got := consumerAccepts(msg)
	require.Error(t, got, "扁平形状必须被拒（ENV-1 之前的真实结局：Nak + 死信，B 区永远看不到这笔账单）")
	assert.EqualError(t, got, "finance.due.registered 信封缺少 payload")
	t.Logf("扁平形状在消费侧入口被拒：%v", got)

	// 同一入口的反向另一半（recurring.go 旧形状连 event_type/business_id 都没有）在
	// recurring_test.go 的 TestTransactionCreatedEnvelope_FlatShapeIsRejected。
}
