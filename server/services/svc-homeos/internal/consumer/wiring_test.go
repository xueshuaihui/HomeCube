// wiring_test.go pins the two things this card adds: that a finance.due.registered envelope becomes
// one homeos_due_registration row (§3.6's「消费并按 (source_system, source_id, kind) upsert」), and that
// the chain around it behaves the way §3.4 writes it down -- 幂等 on redelivery, a failure that spends
// 「max_deliver=4（对应 10.4 的至多重试 3 次）」before anything is parked (「超限失败 -> publish 到
// dl.{consumerCode}.{event_type} + 写 {code}_dead_letter」), and no start at all when the broker is not
// there.
//
// The fixtures are sqlite, the shape this service's other tests use (handler/auth_test.go:84). Since
// FB1 fixed packages/bus's row shapes and table names, they are the shipped DDL column-for-column:
//
//   - homeos_event_dedupe (0002:42-54) has NO id column -- its key is the documented
//     (event_type, business_id) unique index -- and bus.DedupeRecord now has no primary-key field
//     either, so gorm emits no `RETURNING` for it and the fixture no longer has to invent one. The
//     dedupe 判定 therefore comes back as RowsAffected==0 (ON CONFLICT DO NOTHING), on sqlite as well
//     as on Postgres.
//   - homeos_dead_letter (0002:64-85) IS created here, with its real column set
//     (id, family_id, consumer_code, event_type, envelope, last_error, created_at, resolved_at). It
//     used to be left out because bus.DeadLetterRecord wrote subject / attempts / error, none of
//     which the DDL has -- so the dead-letter row could not land and the test could only assert the
//     Nak. Against the fixed底座 the row itself is assertable, and 判据 4 (「非法 kind 被 CHECK 拒后
//     走死信而不是丢消息」「畸形 JSON 进死信」) is asserted on it.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

const (
	fixtureFamilyID = "7c1b1e6a-2a4f-4d6e-9a10-11f2e3d4c5b6"
	fixtureSourceID = "aa1b1e6a-2a4f-4d6e-9a10-11f2e3d4c5b6"
	fixtureDueAt    = "2026-10-10T00:00:00Z"
)

// financeCode names the source domain the way cmd/svc-homeos/main.go's dueEventSourceCode does:
// registry exports HomeosCode only (registry.go:266), so the other half of §3.1's two P1 rows is
// looked up through registry.ByCode rather than built by hand -- and ByCode's refusal is what proves
// the wiring is not subscribing to a domain the table does not have.
const financeCode = "finance"

// ==================== fixtures ====================

// newDueTable builds 0008's column set (the write target of HandleDueRegistered) in its sqlite form,
// including 0008:52-54's PARTIAL unique index -- the (source_system, source_id, kind) WHERE
// deleted_at IS NULL anchor is what makes 「一个到期对象只有一条活的注册行」 a table fact rather than
// a code habit, so a fixture without it would let a duplicate live row pass unseen.
func newDueTable(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.Exec(`CREATE TABLE homeos_due_registration (
		id TEXT PRIMARY KEY,
		family_id TEXT NOT NULL,
		source_system TEXT NOT NULL,
		source_id TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('bill','budget','goal','repayment')),
		title TEXT NOT NULL,
		due_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME,
		deleted_by TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX homeos_due_registration_source_uidx
		ON homeos_due_registration (source_system, source_id, kind)
		WHERE deleted_at IS NULL`).Error)
}

// newDedupeTable builds 0002's homeos_event_dedupe: the three documented columns and 0002:54's unique
// index, and no id column -- that is the DDL, and since FB1 removed the phantom autoIncrement field
// from bus.DedupeRecord it is also what the底座 writes.
func newDedupeTable(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.Exec(`CREATE TABLE homeos_event_dedupe (
		event_type TEXT NOT NULL,
		business_id TEXT NOT NULL,
		created_at DATETIME NOT NULL)`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX homeos_event_dedupe_key
		ON homeos_event_dedupe (event_type, business_id)`).Error)
}

// newDeadLetterTable builds 0002's homeos_dead_letter, column-for-column the shape FB1's
// bus.DeadLetterRecord now writes. It is the replay table PRD 3.7 addresses by id, hence the
// autoincrement primary key.
func newDeadLetterTable(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.Exec(`CREATE TABLE homeos_dead_letter (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		family_id TEXT,
		consumer_code TEXT NOT NULL,
		event_type TEXT NOT NULL,
		envelope TEXT NOT NULL,
		last_error TEXT,
		created_at DATETIME NOT NULL,
		resolved_at DATETIME)`).Error)
}

// deadLetters reads back what §3.4's「超限失败 -> 写 {code}_dead_letter」 actually stored.
type deadLetterRow struct {
	ID           int64
	ConsumerCode string `gorm:"column:consumer_code"`
	EventType    string `gorm:"column:event_type"`
	Envelope     string
	LastError    string `gorm:"column:last_error"`
}

func deadLetters(t *testing.T, db *gorm.DB) []deadLetterRow {
	t.Helper()

	var rows []deadLetterRow
	require.NoError(t, db.Table("homeos_dead_letter").
		Select("id", "consumer_code", "event_type", "envelope", "last_error").
		Order("id").Scan(&rows).Error)
	return rows
}

// dueEnvelope is the信封 bus.Envelope's wire form of one finance.due.registered: the payload object
// holds exactly contracts/events/finance.yaml's payload_schema fields, and business_id is the
// registered idempotency key {source_id}:{due_at} (contracts/README.md「{code}.due.registered」row).
func dueEnvelope(businessID, dueAt, title string) bus.Envelope {
	return bus.Envelope{
		EventType:  DueRegisteredEventType,
		BusinessID: businessID,
		FamilyID:   fixtureFamilyID,
		Version:    "1.0",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"source_system": "finance",
			"source_id":     fixtureSourceID,
			"family_id":     fixtureFamilyID,
			"due_at":        dueAt,
			"kind":          "bill",
			"title":         title,
		},
	}
}

func dueMessage(env bus.Envelope) bus.Message {
	return bus.Message{Subject: DueRegisteredEventType, Envelope: env}
}

// fakeJS is the test double of bus.JetStreamWrapper. It exists because the底座's real Subscribe leaves
// the handler uncalled; capturing it here is what lets a test drive bus.DurableConsumer.handleMessage
// -- the code that owns dedupe, ack, nak and dead letter.
type fakeJS struct {
	subscriptions []fakeSubscription
	published     []string
	closed        int
}

type fakeSubscription struct {
	stream, consumer, filter string
	handler                  jetstream.MessageHandler
}

func (f *fakeJS) CreateStream(context.Context, string, []string) error { return nil }

func (f *fakeJS) Publish(_ context.Context, subject string, _ []byte) (*jetstream.PubAck, error) {
	f.published = append(f.published, subject)
	return &jetstream.PubAck{}, nil
}

func (f *fakeJS) Subscribe(_ context.Context, streamName, consumerName, filterSubject string, handler jetstream.MessageHandler) error {
	f.subscriptions = append(f.subscriptions, fakeSubscription{streamName, consumerName, filterSubject, handler})
	return nil
}

func (f *fakeJS) Close() { f.closed++ }

// testMsg is one delivery as the底座 sees it. Data / Subject / Headers / Ack / Nak / Term / Metadata
// are on handleMessage's path: the底座 reads the delivery counter before it decides between Nak and
// the dead letter (consumer.go:227-259 -> deliveryAttempt -> msg.Metadata()), and Term is how it ends
// a delivery whose budget is spent. The embedded nil interface keeps the rest of jetstream.Msg present
// without pretending to work -- every method the底座 can reach is therefore implemented here, because
// a nil-interface call is a SIGSEGV that takes the whole package's test binary with it.
//
// numDelivered is the投递序号 the底座 sees, and it is 1-based exactly the way the pinned nats.go
// v1.54.0 renders it (jetstream.MsgMetadata.NumDelivered is already 1 on the first delivery; there is
// no Info()/MsgInfo on that interface). Its ZERO VALUE means 「this case did not set it」 and is
// reported as the FIRST delivery rather than as an unreadable counter: every pre-existing case in this
// package that builds a `&testMsg{data:, subject:}` is a first delivery, and mapping the zero value to
// the error half below would silently turn all of them into the底座's「读不到元数据」branch -- a
// different judgement, chosen by the fixture instead of by the case. A case that wants that branch
// asks for it on purpose, through metadataErr.
type testMsg struct {
	jetstream.Msg
	data         []byte
	subject      string
	acks         int
	naks         int
	terms        int
	redeliver    bool
	numDelivered uint64
	metadataErr  error
}

func (m *testMsg) Data() []byte         { return m.data }
func (m *testMsg) Subject() string      { return m.subject }
func (m *testMsg) Headers() nats.Header { return nil }
func (m *testMsg) Ack() error           { m.acks++; return nil }
func (m *testMsg) Nak() error           { m.naks++; return nil }
func (m *testMsg) Term() error          { m.terms++; return nil }
func (m *testMsg) Redelivered() bool    { return m.redeliver }

// Metadata hands the底座 the delivery counter, i.e. the one number §3.4's retry ladder is counted in.
func (m *testMsg) Metadata() (*jetstream.MsgMetadata, error) {
	if m.metadataErr != nil {
		return nil, m.metadataErr
	}
	num := m.numDelivered
	if num == 0 {
		num = 1
	}
	return &jetstream.MsgMetadata{NumDelivered: num}, nil
}

// countRows is the one assertion helper this file needs on both tables.
func countRows(t *testing.T, db *gorm.DB, where string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, db.Table("homeos_due_registration").Where(where, args...).Count(&n).Error, "count")
	return n
}

// ==================== the adapter ====================

func TestDueRegisteredHandlerWritesRegistration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	err = DueRegisteredHandler(db)(context.Background(), dueMessage(
		dueEnvelope(fixtureSourceID+":"+fixtureDueAt, fixtureDueAt, "10 月房贷")))
	require.NoError(t, err)

	var row struct {
		SourceSystem string
		SourceID     string
		Kind         string
		Title        string
		FamilyID     string
		DueAt        time.Time
	}
	require.NoError(t, db.Table("homeos_due_registration").First(&row).Error)
	assert.Equal(t, "finance", row.SourceSystem)
	assert.Equal(t, fixtureSourceID, row.SourceID)
	assert.Equal(t, "bill", row.Kind, "kind 的四值枚举是冻结契约的，homeos_due_registration 上有 CHECK")
	assert.Equal(t, "10 月房贷", row.Title)
	assert.Equal(t, fixtureFamilyID, row.FamilyID)
	assert.True(t, row.DueAt.UTC().Format(time.RFC3339) == fixtureDueAt,
		"due_at round-trip: got %s want %s", row.DueAt, fixtureDueAt)
}

// The 改期 leg, first half: the contract's business_id carries due_at precisely so a reschedule is a
// NEW event (contracts/README.md「{source_id}:{due_at}（改期即重发）」) -- i.e. the去重层 does NOT
// swallow it -- while §3.6's upsert anchor is (source_system, source_id, kind), and 0008:52-54 makes
// that anchor a PARTIAL unique index. So a reschedule of a still-live registration rewrites that one
// row (the B 区 must not show the same bill at both the old and the new date), and this is the case
// that judgement 5's「新增一行」phrasing does not cover: with a live row in the slot, a second insert
// would violate the shipped index. The new-row case is the revoked one below.
func TestDueRegisteredHandlerRewritesSameObjectOnReschedule(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	handler := DueRegisteredHandler(db)
	ctx := context.Background()

	require.NoError(t, handler(ctx, dueMessage(
		dueEnvelope(fixtureSourceID+":2026-10-10T00:00:00Z", "2026-10-10T00:00:00Z", "10 月房贷"))))
	first := dueRow(t, db, "deleted_at IS NULL")

	require.NoError(t, handler(ctx, dueMessage(
		dueEnvelope(fixtureSourceID+":2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z", "11 月房贷"))))

	require.Equal(t, int64(1), countRows(t, db, "1 = 1"), "同一 (source_system, source_id, kind) 的活行只有一条")

	row := dueRow(t, db, "deleted_at IS NULL")
	assert.Equal(t, first.ID, row.ID, "改期重写的是同一条注册，不是新增一条")
	assert.Equal(t, "11 月房贷", row.Title)
	assert.Equal(t, "2026-11", row.DueAt.UTC().Format("2006-01"))
	assert.True(t, row.UpdatedAt.After(first.UpdatedAt), "重写要留下 updated_at 的痕迹，否则无法与「事件被吞掉」区分")
}

// The 改期 leg, second half -- the one that DOES add a row, and the reason the index is partial
// (0008:49-54「撤销后同一对象再次注册（账单重新排期）要能进新的一行，否则历史撤销行会把槽位永久占
// 住」). The revoked state is produced by the shipped撤销 path: finance.due.revoked is this table's only
// deletion path per 0008:43-44, and due_revoked_handler.go -- subscribed through bus_runtime.go (REV-1)
// -- is what applies it. Writing deleted_at by hand here would have made this case assert the shape of
// its own fixture instead of the code the 首页 depends on. The registration history therefore keeps the
// old row and the new due date gets its own.
func TestDueRegisteredHandlerRegistersNewRowAfterRevocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	require.NoError(t, DueRegisteredHandler(db)(ctx, dueMessage(
		dueEnvelope(fixtureSourceID+":2026-10-10T00:00:00Z", "2026-10-10T00:00:00Z", "10 月房贷"))))
	revoked := dueRow(t, db, "deleted_at IS NULL")

	// The revocation itself: one real finance.due.revoked delivery through the subscribed handler.
	require.NoError(t, DueRevokedHandler(db, quietLogger())(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":2026-10-10T00:00:00Z", revokedClock, "completed"))))
	require.NotNil(t, readRegistration(t, db, revoked.ID).DeletedAt,
		"deleted_at 是撤销处理器写上的，不是本用例手写的")

	require.NoError(t, DueRegisteredHandler(db)(ctx, dueMessage(
		dueEnvelope(fixtureSourceID+":2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z", "11 月房贷"))))

	require.Equal(t, int64(2), countRows(t, db, "1 = 1"), "撤销行留在历史里")
	require.Equal(t, int64(1), countRows(t, db, "deleted_at IS NULL"), "重新排期进的是新的一行")

	live := dueRow(t, db, "deleted_at IS NULL")
	assert.NotEqual(t, revoked.ID, live.ID, "新注册是新行，不是把撤销行改活（那会让 due_at 之外的历史一起复活）")
	assert.Equal(t, "2026-11", live.DueAt.UTC().Format("2006-01"))
	assert.Equal(t, "11 月房贷", live.Title)
}

func dueRow(t *testing.T, db *gorm.DB, liveOnly string) dueRowSnapshot {
	t.Helper()

	var row dueRowSnapshot
	require.NoError(t, db.Table("homeos_due_registration").Where(liveOnly).First(&row).Error, "读回注册行")
	return row
}

func TestDueRegisteredHandlerRefusesPayloadlessEnvelope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	err = DueRegisteredHandler(db)(context.Background(), bus.Message{
		Subject: DueRegisteredEventType,
		Envelope: bus.Envelope{
			EventType:  DueRegisteredEventType,
			BusinessID: fixtureSourceID + ":" + fixtureDueAt,
		},
	})
	require.ErrorIs(t, err, ErrNoPayload)
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
}

// A payload whose due_at is not the RFC3339 form the contract's `timestamp` type means is a rejected
// delivery, not a row with a zero timestamp -- §18.2's 「到期日当天 09:00 前提醒」 is keyed on that
// column, and a silently-zeroed one would put an item at the epoch.
func TestDueRegisteredHandlerRejectsUnparsableDueAt(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	err = DueRegisteredHandler(db)(context.Background(), dueMessage(
		dueEnvelope(fixtureSourceID+":nope", "not-a-timestamp", "账单")))
	require.Error(t, err)
	assert.ErrorContains(t, err, "due_at")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
}

// A kind outside the contract's enum(bill|budget|goal|repayment) is refused by the table's CHECK, and
// that refusal has to travel back to the durable consumer as an error (the V3 card's fix was exactly
// this: a publisher must not be able to park a row the B 区 then cannot read).
func TestDueRegisteredHandlerRefusesKindOutsideContractEnum(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	env := dueEnvelope(fixtureSourceID+":"+fixtureDueAt, fixtureDueAt, "订阅")
	env.Payload["kind"] = "subscription"

	require.Error(t, DueRegisteredHandler(db)(context.Background(), dueMessage(env)))
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
}

// ==================== the chain around the handler ====================

// countedHandler wraps a real handler and counts how many times the底座 actually let it run. That
// count is what §3.4's「max_deliver=4（对应 10.4 的至多重试 3 次）」is bought with, and it is exactly
// what a claim surviving a failure takes away: every redelivery is then answered by the dedupe index
// with「already claimed」+ ack, so the ladder runs out without a second attempt at the write.
func countedHandler(next bus.Handler, calls *int) bus.Handler {
	return func(ctx context.Context, msg bus.Message) error {
		*calls++
		return next(ctx, msg)
	}
}

// deliverFailing drives one delivery of a deterministically failing envelope through the底座's
// handler, numbered the way JetStream numbers it (`attempt`, 1-based, read back through
// testMsg.Metadata) so a case can state which rung of the ladder it is asserting on. `subject` is the
// delivery's subject, i.e. the event name -- the registered leg and the revoked leg share this helper.
func deliverFailing(handler jetstream.MessageHandler, subject string, raw []byte, attempt uint64) *testMsg {
	msg := &testMsg{
		data:         raw,
		subject:      subject,
		numDelivered: attempt,
		redeliver:    attempt > 1,
	}
	handler(msg)
	return msg
}

// assertInsideRetryBudget pins the half of §3.4's failure branch that runs while the delivery budget
// lasts: the delivery is Naked, so the backoff ladder [1s,10s,60s] runs and the event stays in the
// stream -- and NOTHING is parked yet, because the dead-letter sentence starts with「超限失败 ->」.
//
// The dedupe assertion is this file's regression pin for the blocking defect BUS-4 fixed: the claim is
// taken before the handler (§3.4「进 handler 第一件事 INSERT ON CONFLICT DO NOTHING」), so a failure
// that left the row behind would have every one of these redeliveries ack'd as a duplicate without
// reaching the handler, i.e. 一次失败永久吞掉事件、业务效果从未发生. Releasing it is what makes the
// 至多重试 3 次 real.
func assertInsideRetryBudget(t *testing.T, db *gorm.DB, fake *fakeJS, msg *testMsg, attempt uint64) {
	t.Helper()

	assert.Equal(t, 1, msg.naks, "第 %d 次投递仍在重试预算内，必须 Nak 重投，不能 ack 掉事件", attempt)
	assert.Zero(t, msg.acks, "第 %d 次投递不许 ack", attempt)
	assert.Zero(t, msg.terms, "第 %d 次投递预算未用完，不许 Term（那等于取消 §3.4 的至多重试 3 次）", attempt)
	assert.Empty(t, fake.published, "第 %d 次投递未超限，不该有 dl.* 发布（判据「超限失败 ->」）", attempt)
	assert.Empty(t, deadLetters(t, db), "第 %d 次投递未超限，不该有死信行", attempt)
	assert.Equal(t, int64(0), dedupeCount(t, db), "第 %d 次投递失败后必须释放 (event_type, business_id) claim，否则重投被去重层拦下", attempt)
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"), "失败的处理不许留下 B 区的行")
}

// TestDurableConsumerPathProcessesAndDeduplicates is §3.4's chain as one unit: the durable consumer
// 底座 built, this service's handler attached to it, and two deliveries of the same
// (event_type, business_id) -- one row, and the second delivery acked as a duplicate rather than
// re-run. This is the judgement-4 shape (「同一 {source_id}:{due_at} 连发两次，表中只有一行」)
// without a broker in front of it.
func TestDurableConsumerPathProcessesAndDeduplicates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code:          registry.HomeosCode,
		StreamName:    "HC_FINANCE",
		EventType:     DueRegisteredEventType,
		FilterSubject: DueRegisteredEventType,
	})
	require.NoError(t, consumer.Start(context.Background(), DueRegisteredHandler(db)))
	require.Len(t, fake.subscriptions, 1)
	sub := fake.subscriptions[0]
	assert.Equal(t, "HC_FINANCE", sub.stream)
	assert.Equal(t, DueRegisteredEventType, sub.filter, "filter_subject 精确匹配（§3.4）")
	assert.Equal(t, "homeos-finance.due.registered", sub.consumer,
		"底座 builds {code}-{event_type}; pump.durableName is what makes it JetStream-legal")

	raw, err := json.Marshal(dueEnvelope(fixtureSourceID+":"+fixtureDueAt, fixtureDueAt, "10 月房贷"))
	require.NoError(t, err)

	first := &testMsg{data: raw, subject: DueRegisteredEventType}
	sub.handler(first)

	assert.Equal(t, 1, first.acks, "首次处理成功由 consumer 侧 ack")
	assert.Zero(t, first.naks)
	require.Equal(t, int64(1), countRows(t, db, "1 = 1"))
	require.Equal(t, int64(1), dedupeCount(t, db), "一次业务写入 = 一条去重行")
	assert.Empty(t, fake.published, "没有失败，就不该有 dl.* 发布")

	afterFirst := dueRow(t, db, "1 = 1")

	// The redelivery of the same (event_type, business_id).
	second := &testMsg{data: raw, subject: DueRegisteredEventType, redeliver: true}
	sub.handler(second)

	assert.Equal(t, int64(1), countRows(t, db, "1 = 1"), "重复投递不产生重复业务对象（§3.4）")
	assert.Equal(t, int64(1), dedupeCount(t, db), "重复投递不再插去重行")
	assert.Empty(t, fake.published, "去重命中不是失败，不该走死信")
	assert.Empty(t, deadLetters(t, db), "去重命中不是失败，不该有死信行")
	assert.Equal(t, 1, second.acks, "§3.4「已存在即 ack 返回」：重复投递答复是 ack")
	assert.Zero(t, second.naks, "重复投递不是失败，不许 Nak 成无限重投")

	afterSecond := dueRow(t, db, "1 = 1")
	assert.Equal(t, afterFirst, afterSecond, "去重命中的投递不得重跑 upsert")

	// Why the duplicate answers ack on both engines now, and why it did not before FB1: §3.4's claim is
	// the INSERT itself (ON CONFLICT DO NOTHING), so the answer is RowsAffected==0 rather than a
	// reading of the driver's error TEXT -- checkDedupe's isUniqueViolation branch is only the fallback
	// for drivers that report the collision instead of swallowing it.
}

// dueRowSnapshot is the row-identity this file compares across deliveries: same id, same title, same
// updated_at means the upsert did not run again; a different id means a new registration really is a
// new row.
type dueRowSnapshot struct {
	ID           string
	SourceSystem string
	SourceID     string
	Kind         string
	Title        string
	DueAt        time.Time
	UpdatedAt    time.Time
}

// TestDurableConsumerNaksAndWritesDeadLetterOnBadKind: 判据 4's「非法 kind 被 CHECK 拒后走死信而不是
// 丢消息」as one chain. A payload whose kind is outside the frozen enum is refused by
// homeos_due_registration's CHECK (0008:36), the handler's error reaches the底座, and §3.4's failure
// branch spends its ladder before it parks anything:「max_deliver=4（对应 10.4 的至多重试 3 次）」 makes
// deliveries 1-3 Naked with no dead letter, and「超限失败 -> publish 到 dl.{consumerCode}.{event_type}
// + 写 {code}_dead_letter」 is the fourth delivery's ending -- exactly one row, then Term, because
// asking the stream for a delivery it has already refused is not a retry.
//
// This is also where the file's regression钉 lives, because the order §3.4 fixes has a consequence the
// previous version of this case pinned backwards. The dedupe row IS inserted BEFORE the handler runs
// (consumer.go:204-218), and that is only compatible with the retry ladder because a failed delivery
// gives its claim back (consumer.go:231-242). So the redelivery that follows this Nak is NOT already
// claimed: it reaches the handler and attempts the write again, which is the whole point of retrying.
// Asserting「重投被去重层拦下」would have asserted the defect itself -- 首投失败被 ack、业务效果从未发生.
func TestDurableConsumerNaksAndWritesDeadLetterOnBadKind(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code: registry.HomeosCode, StreamName: "HC_FINANCE",
		EventType: DueRegisteredEventType, FilterSubject: DueRegisteredEventType,
		// MaxDeliver left at the底座's default 4 -- §3.4's own number, so the ladder this case walks
		// is the one the shipped consumer would run against a real stream.
	})
	attempts := 0
	require.NoError(t, consumer.Start(context.Background(),
		countedHandler(DueRegisteredHandler(db), &attempts)))
	require.Len(t, fake.subscriptions, 1)
	handler := fake.subscriptions[0].handler

	env := dueEnvelope(fixtureSourceID+":"+fixtureDueAt, fixtureDueAt, "订阅")
	env.Payload["kind"] = "subscription"
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	// 预算内的三次投递：Nak、不 park、claim 当场交还。
	for attempt := uint64(1); attempt < 4; attempt++ {
		assertInsideRetryBudget(t, db, fake,
			deliverFailing(handler, DueRegisteredEventType, raw, attempt), attempt)
	}

	assert.Equal(t, 3, attempts, "至多重试 3 次 = 前三个预算内的投递每次都真的跑了 handler")

	// 超限的那一次：死信 + Term，各一次。
	fourth := deliverFailing(handler, DueRegisteredEventType, raw, 4)
	assert.Zero(t, fourth.naks, "预算已花完，不该再向 stream 要一次它已经拒过的投递")
	assert.Equal(t, 1, fourth.terms, "超限的收尾是 Term：这一投递到此为止，重放走死信那一行")
	assert.Zero(t, fourth.acks, "超限失败不能当成功 ack")
	assert.Equal(t, 4, attempts, "四次投递 = handler 跑了四次，一次也没被去重层吞掉")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"), "非法 kind 不许落进 B 区的源表")

	assert.Equal(t, []string{"dl.homeos." + DueRegisteredEventType}, fake.published,
		"dl.* 只在超限时 publish 一次（每投一次发一次，人工重放就会把同一事件入队四次）")

	letters := deadLetters(t, db)
	require.Len(t, letters, 1, "超限失败的消息要留在重放表里，而且只留一行")
	assert.Equal(t, "homeos", letters[0].ConsumerCode)
	assert.Equal(t, DueRegisteredEventType, letters[0].EventType)
	assert.Contains(t, strings.ToLower(letters[0].LastError), "check constraint",
		"last_error 要能看出是表约束拒的（sqlite「CHECK constraint failed」/ Postgres「violates check constraint」）")
	assert.Contains(t, letters[0].LastError, "kind", "并且指向的是 kind 这一列，不是别处")
	assert.JSONEq(t, string(raw), letters[0].Envelope, "§3.4「重放即原样重新入队」-> 存原文")

	// 超限收尾也要把 claim 交回去：死信行的「重放即原样重新入队，走同一幂等键」走的是同一个
	// (event_type, business_id)，留着首投失败那一行的话，人工重放也会被去重层当成重复而 ack 掉。
	assert.Equal(t, int64(0), dedupeCount(t, db))
}

// TestDurableConsumerNaksAndAttemptsDeadLetter: a handler failure that is not a schema collision (here
// the contract's `timestamp` type, an unparsable due_at) goes the same way as the one above -- three
// Naks inside the budget, then the dead letter and Term on the fourth delivery. The failure is
// deterministic, but it reaches the底座 through the handler rather than through the envelope parse,
// and only the latter is treated as a poison message on its first delivery (see the malformed case
// below): a handler error can be transient, so it earns the ladder.
func TestDurableConsumerNaksAndAttemptsDeadLetter(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code: registry.HomeosCode, StreamName: "HC_FINANCE",
		EventType: DueRegisteredEventType, FilterSubject: DueRegisteredEventType,
	})
	attempts := 0
	require.NoError(t, consumer.Start(context.Background(),
		countedHandler(DueRegisteredHandler(db), &attempts)))
	require.Len(t, fake.subscriptions, 1)
	handler := fake.subscriptions[0].handler

	env := dueEnvelope(fixtureSourceID+":bad", "not-a-timestamp", "账单")
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	for attempt := uint64(1); attempt < 4; attempt++ {
		assertInsideRetryBudget(t, db, fake,
			deliverFailing(handler, DueRegisteredEventType, raw, attempt), attempt)
	}

	fourth := deliverFailing(handler, DueRegisteredEventType, raw, 4)
	assert.Zero(t, fourth.naks, "超限之后不再 Nak")
	assert.Equal(t, 1, fourth.terms, "超限的收尾是 Term")
	assert.Zero(t, fourth.acks, "处理失败不能当成功 ack")
	assert.Equal(t, 4, attempts, "预算花完 = handler 被给了四次机会，而不是一次")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
	assert.Equal(t, []string{"dl.homeos." + DueRegisteredEventType}, fake.published,
		"dl.* 的发布留在超限那一次")

	letters := deadLetters(t, db)
	require.Len(t, letters, 1, "超限失败才写死信，且只写一行")
	assert.Contains(t, letters[0].LastError, "due_at",
		"last_error 指向的是契约的 timestamp 字段，不是别处")
	assert.JSONEq(t, string(raw), letters[0].Envelope)
	assert.Equal(t, int64(0), dedupeCount(t, db), "超限收尾不留 claim，否则人工重放这条死信还是被拦下")
}

// TestDurableConsumerPathRejectsMalformedEnvelope: bytes that are not an envelope at all must not be
// acked into the void -- §3.4's dead-letter branch runs, and the raw body is what the dead letter
// stores, because there is no parsed event to store. Unlike a handler error, an envelope that will not
// parse never starts parsing on the fourth try, so the底座 spends no ladder on it: the first delivery
// is already the over-budget ending (dead letter + Term, no Nak), which is what keeps the stream from
// being asked for 1s/10s/60s of redeliveries of the same undecidable bytes.
//
// There is also no claim to release on this path: the envelope never parsed, so checkDedupe was never
// reached -- the dedupe table is asserted empty for exactly that reason.
//
// The one thing this fixture cannot show is the shipped engine's answer to that raw body:
// homeos_dead_letter.envelope is `jsonb` (0002:74), and a non-JSON body is an input syntax error for
// jsonb, so on Postgres the dead-letter INSERT itself fails (packages/bus prints
// 「failed to write dead letter」 and continues) -- reported, since the fix belongs to the底座, which
// this card may not touch. The message is not lost either way: the raw body is in the dead-letter row
// this case asserts on sqlite, and that row is the replay entry. sqlite's column is TEXT, so the row
// lands here and the assertion is kept honest by naming the difference instead of hiding it.
func TestDurableConsumerPathRejectsMalformedEnvelope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code: registry.HomeosCode, StreamName: "HC_FINANCE",
		EventType: DueRegisteredEventType, FilterSubject: DueRegisteredEventType,
	})
	attempts := 0
	require.NoError(t, consumer.Start(context.Background(),
		countedHandler(DueRegisteredHandler(db), &attempts)))

	msg := &testMsg{data: []byte("{not json"), subject: DueRegisteredEventType}
	fake.subscriptions[0].handler(msg)

	assert.Equal(t, 1, msg.terms, "解不出信封是确定性毒消息：第一次投递就收尾")
	assert.Zero(t, msg.naks, "Nak 只会让 stream 按 backoff 重投同一堆解不出来的字节")
	assert.Zero(t, msg.acks, "畸形 JSON 不能当成功 ack")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
	assert.Equal(t, int64(0), dedupeCount(t, db), "信封都没解出来，去重 claim 从未被拿走")
	assert.Equal(t, 0, attempts, "解不出信封的消息根本进不了 handler")
	assert.Equal(t, []string{"dl.homeos." + DueRegisteredEventType}, fake.published, "畸形 JSON 也要 publish 到 dl.*")

	letters := deadLetters(t, db)
	require.Len(t, letters, 1, "sqlite 上死信行能落（Postgres 的 jsonb 差异见上）")
	require.Len(t, fake.published, 1, "一次收尾 = 一次 dl.* 发布")
	assert.Equal(t, DueRegisteredEventType, letters[0].EventType,
		"信封解不出来，event_type 只能取消费者配置的那一个")
	assert.Contains(t, letters[0].LastError, "malformed envelope")
	assert.Equal(t, "{not json", letters[0].Envelope, "原文即重放的全部依据")
}

// TestFixtureDeliveryCounterDefaultsToFirstDelivery: the zero value of testMsg.numDelivered has to read
// back as the FIRST delivery, not as 「元数据读不到」. consumer.go:275-289 turns an unreadable counter
// into an immediate over-budget ending (dead letter + Term), so a fixture that reached that branch by
// accident would let every older `&testMsg{data:, subject:}` case in this package decide its own
// semantics -- and the ladder cases above would stop testing what they say they test.
func TestFixtureDeliveryCounterDefaultsToFirstDelivery(t *testing.T) {
	md, err := (&testMsg{subject: DueRegisteredEventType}).Metadata()
	require.NoError(t, err, "零值不是「读不到元数据」")
	require.NotNil(t, md)
	assert.Equal(t, uint64(1), md.NumDelivered, "首投即 1（nats.go v1.54.0 的 NumDelivered 就是 1-based）")

	md, err = (&testMsg{subject: DueRegisteredEventType, numDelivered: 3}).Metadata()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), md.NumDelivered)

	// The「读不到」branch is asked for on purpose, never by coincidence.
	md, err = (&testMsg{
		subject:     DueRegisteredEventType,
		metadataErr: errors.New("not a jetstream message"),
	}).Metadata()
	require.Error(t, err)
	assert.Nil(t, md)
}

// TestDurableConsumerTerminatesWhenDeliveryCountIsUnreadable: the底座 cannot choose between the retry
// ladder and the over-budget ending without a delivery counter, and consumer.go:270-284 names the only
// ending that cannot lose the event -- a dead letter keeps the raw envelope for 人工重放, while a Nak
// on a delivery whose metadata is unreadable buys nothing. So: no Nak, Term, one dead letter, and the
// claim released on the way out (releaseDedupe ran first; §3.4's replay path needs it).
//
// The alert that accompanies this branch goes to the default sink (defaultAlertFn -> stderr):
// DurableConsumer.alertFn is unexported and NewDurableConsumer always installs the default, so this
// fixture can observe what the consumer DID, not read its alert line back.
func TestDurableConsumerTerminatesWhenDeliveryCountIsUnreadable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code: registry.HomeosCode, StreamName: "HC_FINANCE",
		EventType: DueRegisteredEventType, FilterSubject: DueRegisteredEventType,
	})
	require.NoError(t, consumer.Start(context.Background(), DueRegisteredHandler(db)))

	env := dueEnvelope(fixtureSourceID+":"+fixtureDueAt, fixtureDueAt, "订阅")
	env.Payload["kind"] = "subscription"
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	msg := &testMsg{
		data:        raw,
		subject:     DueRegisteredEventType,
		metadataErr: errors.New("fixture: no JetStream metadata on this delivery"),
	}
	fake.subscriptions[0].handler(msg)

	assert.Zero(t, msg.naks, "读不到投递序号就不赌预算，不再 Nak")
	assert.Zero(t, msg.acks)
	assert.Equal(t, 1, msg.terms, "按超限收尾：Term")
	assert.Equal(t, []string{"dl.homeos." + DueRegisteredEventType}, fake.published)
	require.Len(t, deadLetters(t, db), 1, "读不到元数据的那一次投递也要留下一行重放依据")
	assert.Equal(t, int64(0), dedupeCount(t, db), "超限收尾之前先释放 claim，重放才走得进去")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
}

// ==================== naming and fail-fast ====================

// TestDurableNameIsJetStreamLegal: the底座's {code}-{event_type} is not a legal consumer name (the
// client rejects '.', '>', '*', '=', ' ' before the request goes out), while §3.4's own example is the
// dashed form.
func TestDurableNameIsJetStreamLegal(t *testing.T) {
	cases := map[string]string{
		"homeos-finance.due.registered": "homeos-finance-due-registered",
		"homeos-finance.due.revoked":    "homeos-finance-due-revoked",
		"homeos-family.module.updated":  "homeos-family-module-updated",
		"already-legal-name":            "already-legal-name",
	}
	for in, want := range cases {
		got := durableName(in)
		assert.Equal(t, want, got)
		assert.NotContains(t, []byte(got), byte('.'))
		for _, bad := range []string{".", ">", "*", "=", " "} {
			assert.NotContains(t, got, bad, "JetStream 不接受的字符 %q 必须被规范化", bad)
		}
	}

	// The normalised name is the one that reaches the server, and the pump must hand底座's callback the
	// filter subject unchanged.
	homeos, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)
	require.Equal(t, "homeos-finance-due-registered",
		durableName(fmt.Sprintf("%s-%s", homeos.Code, DueRegisteredEventType)))
}

func TestSetupBusFailsFastWithoutBroker(t *testing.T) {
	homeos, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)
	finance, ok := registry.ByCode(financeCode)
	require.True(t, ok)

	ctx := context.Background()

	// Port 1 on loopback refuses immediately: a service must not start with a missing subscription.
	rt, err := SetupBus(ctx, BusConfig{
		Logger:  nil,
		Own:     homeos,
		Source:  finance,
		DSN:     "user=hc_homeos password=x dbname=homecube host=127.0.0.1 port=5432 search_path=homeos",
		NATSURL: "nats://127.0.0.1:1",
	})
	require.Error(t, err)
	assert.Nil(t, rt)
	// The refusal names the layer that failed (packages/bus's own wrapper message), i.e. the process
	// aborts at 「连不上 broker」 rather than at some later step that would have left the deliverer
	// running without a consumer.
	assert.Contains(t, err.Error(), "NATS")

	// Stop on the never-started runtime is the fail-fast path's shape, so it must not panic.
	assert.NotPanics(t, func() { rt.Stop() })
}

func TestSetupBusRefusesConfiguredStreamsThatAreNotTwoDomains(t *testing.T) {
	homeos, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)

	_, err := SetupBus(context.Background(), BusConfig{Own: homeos, Source: homeos, NATSURL: "nats://127.0.0.1:1"})
	require.Error(t, err)

	_, err = SetupBus(context.Background(), BusConfig{})
	require.Error(t, err)
}

// TestOpenBusDBRefusesUnreachableDatabase covers the one fail-fast step the broker-less test above can
// never reach: the bus side's own handle. SetupBus stops at the NATS connect when there is no broker,
// so the database leg's refusal is asked directly -- and it is the leg that would otherwise leave the
// deliverer running against nothing (rows never marked sent, consumer never writing).
func TestOpenBusDBRefusesUnreachableDatabase(t *testing.T) {
	db, err := openBusDB(
		"host=127.0.0.1 port=1 user=hc_homeos password=none dbname=homecube sslmode=disable search_path=homeos",
		slog.Default())
	require.Error(t, err, "连不上库必须回报，不能返回一个写什么都失败的空句柄")
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "本域数据库")
}

// TestBusStreamNamesComeFromRegistry: §3.1's two P1 rows are the names the wiring asks JetStream for;
// nothing in this card coins a stream or subject string.
func TestBusStreamNamesComeFromRegistry(t *testing.T) {
	homeos, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)
	finance, ok := registry.ByCode(financeCode)
	require.True(t, ok)

	assert.Equal(t, "HC_HOMEOS", homeos.StreamName())
	assert.Equal(t, "homeos.>", homeos.StreamSubjectPattern())
	assert.Equal(t, "HC_FINANCE", finance.StreamName())
	assert.Equal(t, "finance.>", finance.StreamSubjectPattern())
}

func dedupeCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var n int64
	require.NoError(t, db.Table("homeos_event_dedupe").Count(&n).Error)
	return n
}
