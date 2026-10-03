package bus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestInterfaceDefinitions verifies that the interface types are properly defined.
func TestInterfaceDefinitions(t *testing.T) {
	// Test Envelope structure.
	env := Envelope{
		EventType:     "finance.transaction.created",
		BusinessID:    "test-id-123",
		FamilyID:      "family-uuid",
		Payload:       map[string]any{"amount": 100},
		Version:       "1.0",
		CausationID:   "cause-123",
		CorrelationID: "corr-456",
		Timestamp:     "2026-10-01T00:00:00Z",
	}

	if env.EventType != "finance.transaction.created" {
		t.Errorf("expected EventType to be 'finance.transaction.created', got %s", env.EventType)
	}

	if env.BusinessID != "test-id-123" {
		t.Errorf("expected BusinessID to be 'test-id-123', got %s", env.BusinessID)
	}

	// Test Message structure.
	msg := Message{
		Subject:  "finance.transaction.created",
		Envelope: env,
		Headers:  map[string]string{"key": "value"},
	}

	if msg.Subject != "finance.transaction.created" {
		t.Errorf("expected Subject to be 'finance.transaction.created', got %s", msg.Subject)
	}

	if len(msg.Headers) != 1 {
		t.Errorf("expected 1 header, got %d", len(msg.Headers))
	}
}

// TestBuildSubject verifies subject construction.
func TestBuildSubject(t *testing.T) {
	tests := []struct {
		code   string
		object string
		action string
		want   string
	}{
		{"homeos", "member", "created", "homeos.member.created"},
		{"finance", "transaction", "created", "finance.transaction.created"},
		{"finance", "budget", "exceeded", "finance.budget.exceeded"},
	}

	for _, tt := range tests {
		got := BuildSubject(tt.code, tt.object, tt.action)
		if got != tt.want {
			t.Errorf("BuildSubject(%q, %q, %q) = %q, want %q", tt.code, tt.object, tt.action, got, tt.want)
		}
	}
}

// TestMarshalUnmarshalEnvelope verifies envelope serialization.
func TestMarshalUnmarshalEnvelope(t *testing.T) {
	env := Envelope{
		EventType:  "finance.transaction.created",
		BusinessID: "tx-123",
		FamilyID:   "fam-456",
		Payload:    map[string]any{"amount_cents": 1000},
		Version:    "1.0",
		Timestamp:  "2026-10-01T00:00:00Z",
	}

	data, err := MarshalEnvelope(env)
	if err != nil {
		t.Fatalf("MarshalEnvelope failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty marshaled data")
	}

	unmarshaled, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope failed: %v", err)
	}

	if unmarshaled.EventType != env.EventType {
		t.Errorf("expected EventType %q, got %q", env.EventType, unmarshaled.EventType)
	}

	if unmarshaled.BusinessID != env.BusinessID {
		t.Errorf("expected BusinessID %q, got %q", env.BusinessID, unmarshaled.BusinessID)
	}
}

// TestOutboxConfigDefaults verifies default configuration values.
func TestOutboxConfigDefaults(t *testing.T) {
	cfg := OutboxConfig{}

	// After NewOutboxDeliverer, defaults should be applied.
	deliverer := NewOutboxDeliverer(nil, nil, cfg, nil)

	if deliverer.cfg.BatchSize != 100 {
		t.Errorf("expected default BatchSize 100, got %d", deliverer.cfg.BatchSize)
	}

	if deliverer.cfg.Interval != 500*time.Millisecond {
		t.Errorf("expected default Interval 500ms, got %v", deliverer.cfg.Interval)
	}

	if deliverer.cfg.MaxAttempts != 10 {
		t.Errorf("expected default MaxAttempts 10, got %d", deliverer.cfg.MaxAttempts)
	}
}

// TestConsumerConfigDefaults verifies consumer configuration defaults.
func TestConsumerConfigDefaults(t *testing.T) {
	cfg := ConsumerConfig{
		Code:       "finance",
		StreamName: "HC_FINANCE",
		EventType:  "finance.transaction.created",
	}

	consumer := NewDurableConsumer(nil, nil, cfg)

	if consumer.cfg.AckWait != 30*time.Second {
		t.Errorf("expected default AckWait 30s, got %v", consumer.cfg.AckWait)
	}

	if consumer.cfg.MaxDeliver != 4 {
		t.Errorf("expected default MaxDeliver 4, got %d", consumer.cfg.MaxDeliver)
	}

	if len(consumer.cfg.BackOff) != 3 {
		t.Errorf("expected 3 backoff intervals, got %d", len(consumer.cfg.BackOff))
	}
}

// TestOutboxStatusConstants pins the two-state machine 0002's CHECK declares. There is no third
// constant to assert on: OutboxStatusFailed is gone, because the DDL's CHECK allows only
// ('pending','sent') and §3.3's failure rule is「失败 attempts+1」+「attempts>10 告警（不丢，只是没
// 送）」— a message that was never delivered is not a message that failed.
func TestOutboxStatusConstants(t *testing.T) {
	if OutboxStatusPending != "pending" {
		t.Errorf("expected OutboxStatusPending to be 'pending', got %s", OutboxStatusPending)
	}

	if OutboxStatusSent != "sent" {
		t.Errorf("expected OutboxStatusSent to be 'sent', got %s", OutboxStatusSent)
	}
}

// TestBusTableNamesArePrefixedByCode pins the {code}_ naming the migrations ship. The TableName()
// methods that used to live on these models returned the unprefixed "outbox" / "event_dedupe" /
// "dead_letter" — names no migration ever created — and GORM cannot see the service code from a
// model method, so the caller passes it and these three functions are the only definition.
func TestBusTableNamesArePrefixedByCode(t *testing.T) {
	for _, tt := range []struct{ code, want string }{
		{"homeos", "homeos_outbox"},
		{"finance", "finance_outbox"},
	} {
		if got := OutboxTableName(tt.code); got != tt.want {
			t.Errorf("OutboxTableName(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}

	if got := DedupeTableName("homeos"); got != "homeos_event_dedupe" {
		t.Errorf("DedupeTableName(\"homeos\") = %q, want %q", got, "homeos_event_dedupe")
	}

	if got := DeadLetterTableName("finance"); got != "finance_dead_letter" {
		t.Errorf("DeadLetterTableName(\"finance\") = %q, want %q", got, "finance_dead_letter")
	}
}

// TestHandlerType verifies Handler type signature.
func TestHandlerType(t *testing.T) {
	var handler Handler = func(ctx context.Context, msg Message) error {
		return nil
	}

	ctx := context.Background()
	msg := Message{
		Subject: "test.subject",
		Envelope: Envelope{
			EventType: "test.event",
		},
	}

	err := handler(ctx, msg)
	if err != nil {
		t.Errorf("handler returned unexpected error: %v", err)
	}
}

// ====================================================================================
// DDL-exact fixtures.
//
// Everything below runs against tables created the way migrations/homeos/homeos_0002_
// outbox_dedupe_dead_letter.up.sql declares them -- same names ({code}_ prefixed), same column set,
// same CHECK on outbox.status, same unique index on (event_type, business_id). That is the point:
// the defects this card fixes are 「the code writes columns / tables the shipped DDL does not have」,
// and no fixture that is more generous than the DDL can ever notice them. On the real database those
// deviations answered SQLSTATE 42703, the row stayed pending, and the deliverer republished it every
// 500ms.
// ====================================================================================

// ddlFixtureSQL is 0002's three tables in their sqlite form. The column sets are transcribed from
// migrations/homeos/homeos_0002_outbox_dedupe_dead_letter.up.sql:
//
//	homeos_outbox        -- 0002:11-31, seven columns; no updated_at, no sent_at, no error.
//	homeos_event_dedupe  -- 0002:42-51, three columns and NO id, plus the unique index at :54.
//	homeos_dead_letter   -- 0002:64-82, last_error (not error), no subject, no attempts.
//
// Kept comment-free on purpose: TestBusModelColumnsMatchShippedDDL reads the created table back out
// of sqlite_master, and inline SQL comments would show up as fake columns there.
const ddlFixtureSQL = `
CREATE TABLE homeos_outbox (
    id         integer      PRIMARY KEY AUTOINCREMENT,
    family_id  text,
    subject    text         NOT NULL,
    envelope   text         NOT NULL,
    status     text         NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent')),
    attempts   integer      NOT NULL DEFAULT 0,
    created_at datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE homeos_event_dedupe (
    event_type  text        NOT NULL,
    business_id text        NOT NULL,
    created_at  datetime    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX homeos_event_dedupe_key ON homeos_event_dedupe (event_type, business_id);

CREATE TABLE homeos_dead_letter (
    id            integer     PRIMARY KEY AUTOINCREMENT,
    family_id     text,
    consumer_code text        NOT NULL,
    event_type    text        NOT NULL,
    envelope      text        NOT NULL,
    last_error    text,
    created_at    datetime    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at   datetime
);
CREATE INDEX homeos_dead_letter_family_created_idx ON homeos_dead_letter (family_id, created_at);
`

// unprefixedDecoys are the table names the deleted TableName() methods returned ("outbox",
// "event_dedupe", "dead_letter") plus what GORM would derive without a Table() override
// ("outbox_messages"). They are created with the same columns so a write aimed at one would SUCCEED
// -- and then be caught by assertNoDecoyRows. Silence is not evidence; a row in a decoy is.
var unprefixedDecoys = []struct{ decoy, real string }{
	{"outbox", "homeos_outbox"},
	{"outbox_messages", "homeos_outbox"},
	{"event_dedupe", "homeos_event_dedupe"},
	{"dead_letter", "homeos_dead_letter"},
}

func newDDLFixture(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	// One connection, or ":memory:" means one private database per connection and the fixture
	// disappears between statements.
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(ddlFixtureSQL).Error)
	for _, d := range unprefixedDecoys {
		require.NoError(t, db.Exec("CREATE TABLE "+d.decoy+" AS SELECT * FROM "+d.real+" WHERE 0").Error)
	}
	return db
}

func assertNoDecoyRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	for _, d := range unprefixedDecoys {
		var count int64
		require.NoError(t, db.Table(d.decoy).Count(&count).Error, "counting %s", d.decoy)
		assert.Zero(t, count, "底座写进了无前缀的 %s：{code}_ 表名契约被破坏", d.decoy)
	}
}

func outboxRows(t *testing.T, db *gorm.DB) []map[string]any {
	t.Helper()

	var rows []map[string]any
	require.NoError(t, db.Table(OutboxTableName("homeos")).Order("id ASC").Find(&rows).Error)
	return rows
}

func countRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()

	var count int64
	require.NoError(t, db.Table(table).Count(&count).Error)
	return count
}

// ==================== (a) pending -> sent, with only the DDL's columns ====================

func TestDelivererAdvancesPendingToSentOnDDLColumnSet(t *testing.T) {
	db := newDDLFixture(t)

	require.NoError(t, InsertOutboxMessageWithFamily(db, "homeos", "2f9a1c3e-0000-4a7b-9c1d-000000000001",
		"homeos.member.created", `{"event_type":"homeos.member.created","business_id":"m-1:1"}`))

	inserted := outboxRows(t, db)
	require.Len(t, inserted, 1)
	assert.Equal(t, "pending", inserted[0]["status"], "§3.3「INSERT {code}_outbox(subject, envelope, status=pending)」")
	assert.Equal(t, int64(0), inserted[0]["attempts"])
	assert.NotNil(t, inserted[0]["created_at"])

	js := &fakeJetStream{}
	var alerts []string
	d := NewOutboxDeliverer(db, js, OutboxConfig{Code: "homeos"}, capture(&alerts))

	d.deliverBatch(context.Background())

	require.Len(t, js.published, 1, "投递器必须把信封原样 publish 到 subject")
	assert.Equal(t, "homeos.member.created", js.published[0].subject)

	after := outboxRows(t, db)
	require.Len(t, after, 1, "投出去是 UPDATE，不是重插一行")
	assert.Equal(t, "sent", after[0]["status"], "§3.3「成功置 sent」")
	assert.Equal(t, int64(0), after[0]["attempts"])
	assert.Empty(t, alerts, "任何告警都是底座写了 DDL 里没有的列（42703 走告警分支）")
	assertNoDecoyRows(t, db)
}

// TestDeliveredRowIsNotRepublished is the live symptom the card reports: because the UPDATE failed,
// every row stayed pending and the deliverer published the same event again on the next 500ms tick.
func TestDeliveredRowIsNotRepublished(t *testing.T) {
	db := newDDLFixture(t)
	require.NoError(t, InsertOutboxMessage(db, "homeos", "homeos.family.module.updated", `{"event_type":"homeos.family.module.updated","business_id":"f-1:homeos:3"}`))

	js := &fakeJetStream{}
	var alerts []string
	d := NewOutboxDeliverer(db, js, OutboxConfig{Code: "homeos"}, capture(&alerts))

	for i := 0; i < 5; i++ {
		d.deliverBatch(context.Background())
	}

	assert.Equal(t, 1, js.count(), "5 个 tick 只应 publish 一次：sent 之后不再被 pending 扫描捞起")
	assert.Equal(t, int64(0), countRows(t, db, "homeos_outbox WHERE status = 'pending'"))
	assert.Empty(t, alerts)
}

// ==================== (d) publish failure: attempts+1, still pending, alert over the budget ========

func TestDelivererFailureIncrementsAttemptsAndStaysPending(t *testing.T) {
	db := newDDLFixture(t)
	require.NoError(t, InsertOutboxMessage(db, "homeos", "homeos.member.updated", `{"event_type":"homeos.member.updated","business_id":"m-2"}`))

	js := &fakeJetStream{failWith: errors.New("nats: connection closed")}
	var alerts []string
	// MaxAttempts 3 so the alert boundary is reachable in three ticks.
	d := NewOutboxDeliverer(db, js, OutboxConfig{Code: "homeos", MaxAttempts: 3}, capture(&alerts))

	for step := 1; step <= 3; step++ {
		d.deliverBatch(context.Background())

		row := outboxRows(t, db)
		require.Len(t, row, 1, "§3.3「不丢」：失败不能删行")
		assert.Equal(t, int64(step), row[0]["attempts"], "§3.3「失败 attempts+1」第 %d 次", step)
		assert.Equal(t, "pending", row[0]["status"], "0002 的 CHECK 没有第三态，失败必须留在 pending")

		if step < 3 {
			assert.Empty(t, alerts, "未到 MaxAttempts 不该告警")
		} else {
			require.Len(t, alerts, 1, "attempts 触到 MaxAttempts 必须告警")
			assert.Contains(t, alerts[0], "exceeded max attempts (3/3)")
		}
	}

	// Fourth tick: still counted, still pending, still there. Nothing was dropped or moved to a
	// status the DDL forbids.
	d.deliverBatch(context.Background())
	row := outboxRows(t, db)
	assert.Equal(t, int64(4), row[0]["attempts"])
	assert.Equal(t, "pending", row[0]["status"])
	assert.Equal(t, 4, js.calls, "每一轮都重新尝试投递（「不丢，只是没送」）")
	assertNoDecoyRows(t, db)
}

// TestOutboxStatusCheckRejectsInventedState is the reverse guard: if code ever writes a third
// status, the table itself says no -- on Postgres and here alike.
func TestOutboxStatusCheckRejectsInventedState(t *testing.T) {
	db := newDDLFixture(t)
	require.NoError(t, InsertOutboxMessage(db, "homeos", "homeos.member.created", `{}`))

	err := db.Table(OutboxTableName("homeos")).Where("subject = ?", "homeos.member.created").
		Updates(map[string]any{"status": "failed"}).Error
	require.Error(t, err, "status='failed' 不在 0002 的 CHECK 里，必须被拒")
}

// ==================== (b) dedupe: the second delivery is swallowed by the table's own index =======

func TestRedeliveryIsSwallowedByTheDedupeIndex(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered"})

	ctx := context.Background()
	first, err := c.checkDedupe(ctx, "finance.due.registered", "bill-7:2026-10-05")
	require.NoError(t, err)
	assert.False(t, first, "第一次投递不是重复")

	second, err := c.checkDedupe(ctx, "finance.due.registered", "bill-7:2026-10-05")
	require.NoError(t, err)
	assert.True(t, second, "§3.4「已存在即 ack 返回」：第二次必须由表约束判成重复")

	assert.Equal(t, int64(1), countRows(t, db, "homeos_event_dedupe"), "去重键不能插出第二行")

	// The other half of §3.4's business_id rule: a different period for the same object is a
	// different key, not a duplicate.
	third, err := c.checkDedupe(ctx, "finance.due.registered", "bill-7:2026-11-05")
	require.NoError(t, err)
	assert.False(t, third, "business_id 带周期后缀时同对象的下一期是合法新事件")
	assert.Equal(t, int64(2), countRows(t, db, "homeos_event_dedupe"))

	assertNoDecoyRows(t, db)
}

func TestHandleMessageRedeliveryAcksWithoutRunningTheHandler(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "homeos.member.created"})

	handled := 0
	c.handler = func(ctx context.Context, msg Message) error {
		handled++
		return nil
	}

	env, err := json.Marshal(Envelope{
		EventType:  "homeos.member.created",
		BusinessID: "m-9:1",
		FamilyID:   "2f9a1c3e-0000-4a7b-9c1d-000000000009",
		Version:    "1.0",
	})
	require.NoError(t, err)

	first := &fakeMsg{subject: "homeos.member.created", data: env}
	c.handleMessage(context.Background(), first)
	assert.Equal(t, 1, handled)
	assert.Equal(t, 1, first.acked)
	assert.Zero(t, first.nacked)

	// The same delivery again (JetStream redelivery, ack lost, whatever): the unique index claims it.
	second := &fakeMsg{subject: "homeos.member.created", data: env}
	c.handleMessage(context.Background(), second)
	assert.Equal(t, 1, handled, "重复投递不得再跑一次 handler")
	assert.Equal(t, 1, second.acked, "§3.4「已存在即 ack 返回」")
	assert.Equal(t, int64(1), countRows(t, db, "homeos_event_dedupe"))
	assertNoDecoyRows(t, db)
}

// ==================== (c) dead letter: the failed message lands in {code}_dead_letter =============

// TestHandlerFailureWritesDeadLetterRow drives §3.4's「超限失败 -> publish 到
// dl.{consumerCode}.{event_type} + 写 {code}_dead_letter」at the boundary it names: max_deliver=4, so
// the dead letter is written by the FOURTH failed delivery, not by the first. Every row assertion
// below is the original one (consumer_code / event_type / last_error / envelope 原文 / family_id /
// resolved_at NULL) -- what changed is how many deliveries it takes to get there, plus the claim and
// ack-verb counters that only exist now that a failure releases what it claimed.
func TestHandlerFailureWritesDeadLetterRow(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered"})

	c.handler = func(ctx context.Context, msg Message) error {
		return errors.New("insert into homeos_due_registration: no such table")
	}

	env, err := json.Marshal(Envelope{
		EventType:  "finance.due.registered",
		BusinessID: "bill-1:2026-10-05",
		FamilyID:   "2f9a1c3e-0000-4a7b-9c1d-000000000002",
		Version:    "1.0",
	})
	require.NoError(t, err)

	// The three retries §3.4's「max_deliver=4（对应 10.4 的至多重试 3 次）」buys: Nak, no dead letter,
	// and the claim released so the next attempt actually reaches the handler.
	for attempt := 1; attempt <= 3; attempt++ {
		m := &fakeMsg{subject: "finance.due.registered", data: env, numDelivered: uint64(attempt)}
		c.handleMessage(context.Background(), m)

		assert.Equal(t, 1, m.nacked, "第 %d/4 次失败在重试预算内，必须 Nak 让 backoff=[1s,10s,60s] 生效", attempt)
		assert.Zero(t, m.terminated, "第 %d/4 次还没超限，Term 掉它等于砍掉剩下的重试", attempt)
		assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"),
			"第 %d/4 次失败后 claim 必须已释放：留着它，下一次重投会被表约束 ack 掉，handler 再也跑不到", attempt)
		assert.Empty(t, js.published, "第 %d/4 次没超限，不该 publish 到 dl.*（否则一条事件最多 4 行死信，人工重放即重复入队 4 次）", attempt)
	}

	msg := &fakeMsg{subject: "finance.due.registered", data: env, numDelivered: 4}
	c.handleMessage(context.Background(), msg)

	assert.Zero(t, msg.nacked, "第 4/4 次重试预算已尽，Nak 只是让流继续投一条它已经投完的")
	assert.Equal(t, 1, msg.terminated, "超限的那一次是 Term：死信行已经是这条事件的重放入口")
	assert.Zero(t, msg.acked)
	assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"),
		"§3.4「重放即原样重新入队，走同一幂等键」：claim 没释放的话，管理台重放这条死信会被旧行 ack 掉")

	var rows []map[string]any
	require.NoError(t, db.Table(DeadLetterTableName("homeos")).Find(&rows).Error)
	require.Len(t, rows, 1, "死信必须落库，不能写完失败就丢掉；且一条事件只有一行（只在超限时写）")

	row := rows[0]
	assert.Equal(t, "homeos", row["consumer_code"])
	assert.Equal(t, "finance.due.registered", row["event_type"])
	assert.Equal(t, "insert into homeos_due_registration: no such table", row["last_error"])
	assert.Contains(t, row["envelope"], `"business_id":"bill-1:2026-10-05"`, "§3.4「重放即原样重新入队」-> 存原文")
	assert.Equal(t, "2f9a1c3e-0000-4a7b-9c1d-000000000002", row["family_id"])
	assert.Nil(t, row["resolved_at"], "0002 不发明 status 枚举，resolved_at 由重放/忽略那一步写")

	// §3.4 的 dl subject：dl.{consumerCode}.{event_type} —— 四次投递里只有超限那一次 publish。
	require.Len(t, js.published, 1)
	assert.Equal(t, "dl.homeos.finance.due.registered", js.published[0].subject)
	assertNoDecoyRows(t, db)
}

// TestFailedHandlerReleasesClaimAndRunsAgainOnRedelivery is the direct falsification of the blocking
// defect the review measured on live NATS + Postgres: 「首投失败 Nak+死信，重投 -> handler calls=1
// acks=1 naks=0」-- the redelivery was ack'd as already-processed while the business effect had never
// happened once. The claim is still taken before the handler (§3.4 says so in those words); what a
// failed handler now does is give it back, so the next delivery runs the handler for real.
func TestFailedHandlerReleasesClaimAndRunsAgainOnRedelivery(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "homeos.member.created"})

	var calls int
	handlerErr := errors.New("insert into homeos_member: database is locked")
	c.handler = func(ctx context.Context, msg Message) error {
		calls++
		return handlerErr
	}

	env, err := json.Marshal(Envelope{
		EventType:  "homeos.member.created",
		BusinessID: "m-30:1",
		FamilyID:   "2f9a1c3e-0000-4a7b-9c1d-000000000030",
		Version:    "1.0",
	})
	require.NoError(t, err)

	first := &fakeMsg{subject: "homeos.member.created", data: env, numDelivered: 1}
	c.handleMessage(context.Background(), first)

	assert.Equal(t, 1, calls, "首投要真的跑过 handler，否则「释放 claim」无从谈起")
	assert.Equal(t, 1, first.nacked, "§3.4 第 1/4 次失败先 nack，让 backoff/max_deliver 继续生效")
	assert.Zero(t, first.acked, "失败的那一次绝不能 ack")
	assert.Zero(t, first.terminated)
	assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"),
		"缺陷本体：handler 失败后 {code}_event_dedupe 里不能留 claim —— 留着重投就被表约束 ack 掉，一次失败永久吞事件")
	assert.Equal(t, int64(0), countRows(t, db, "homeos_dead_letter"), "还没超限（1/4），不该有死信行")
	assert.Empty(t, js.published, "还没超限（1/4），不该 publish 到 dl.*")

	// The redelivery. The transient failure is gone, so this attempt must be able to do the work.
	handlerErr = nil
	second := &fakeMsg{subject: "homeos.member.created", data: env, numDelivered: 2}
	c.handleMessage(context.Background(), second)

	assert.Equal(t, 2, calls, "重投必须真的再跑一次 handler（评审实测到的是 calls=1，业务效果从未落地）")
	assert.Equal(t, 1, second.acked, "这一次是真的处理完了，才 ack")
	assert.Zero(t, second.nacked)
	assert.Equal(t, int64(1), countRows(t, db, "homeos_event_dedupe"),
		"成功的那一次把 claim 留住：§3.4「重复投递不产生重复业务对象是表约束保证」")
	assert.Equal(t, int64(0), countRows(t, db, "homeos_dead_letter"), "成功收尾，不该有任何死信")
	assert.Empty(t, js.published)

	// And the swallow-on-duplicate rule still holds for a delivery after the success.
	third := &fakeMsg{subject: "homeos.member.created", data: env, numDelivered: 3}
	c.handleMessage(context.Background(), third)
	assert.Equal(t, 2, calls, "成功之后的重复投递仍由表约束拦下，不重跑 handler")
	assert.Equal(t, 1, third.acked)
	assertNoDecoyRows(t, db)
}

// TestDeadLetterWrittenOnlyAtMaxDeliver pins the counting §3.4 asks for: max_deliver=4 buys 3
// retries and exactly ONE dead-letter row for one event. Writing a dead letter on every failure would
// park up to four rows per event, and「管理台按服务查看并人工重放」re-enqueues each of them, i.e. the
// same event replayed four times -- the duplicate the幂等键 was supposed to prevent.
func TestDeadLetterWrittenOnlyAtMaxDeliver(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered", MaxDeliver: 4})

	var calls int
	c.handler = func(ctx context.Context, msg Message) error {
		calls++
		return errors.New("homeos_due_registration: relation does not exist")
	}

	env, err := json.Marshal(Envelope{
		EventType:  "finance.due.registered",
		BusinessID: "bill-31:2026-10-05",
		FamilyID:   "2f9a1c3e-0000-4a7b-9c1d-000000000031",
		Version:    "1.0",
	})
	require.NoError(t, err)

	// One event, four deliveries: NumDelivered 1..4 (the card's RedeliveryCount 0..3).
	var nacked, terminated, acked int
	for delivery := 1; delivery <= 4; delivery++ {
		m := &fakeMsg{subject: "finance.due.registered", data: env, numDelivered: uint64(delivery)}
		c.handleMessage(context.Background(), m)
		assert.Equal(t, delivery, calls, "每一次投递都要真的跑到 handler —— 前一次失败留下的 claim 不能把它拦下")
		nacked += m.nacked
		terminated += m.terminated
		acked += m.acked

		assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"),
			"第 %d/4 次失败后 claim 必须为 0 行，否则后面的投递全被表约束 ack 掉", delivery)

		if delivery < 4 {
			assert.Equal(t, 1, m.nacked, "第 %d/4 次在重试预算内，必须 Nak 一次", delivery)
			assert.Zero(t, m.terminated, "第 %d/4 次不许 Term（Term 直接终止重试）", delivery)
			assert.Equal(t, int64(0), countRows(t, db, "homeos_dead_letter"), "第 %d/4 次没超限，不写死信", delivery)
			assert.Empty(t, js.published, "第 %d/4 次没超限，不 publish dl.*")
		} else {
			assert.Zero(t, m.nacked, "第 4/4 次预算已尽，不再 Nak")
			assert.Equal(t, 1, m.terminated, "第 4/4 次要 Term")
		}
	}

	assert.Equal(t, 4, calls, "§3.4「max_deliver=4（对应 10.4 的至多重试 3 次）」= 首投 + 3 次重试，都跑到 handler")
	assert.Equal(t, 3, nacked, "前 3 次各 Nak 一次")
	assert.Equal(t, 1, terminated, "只有超限那一次 Term")
	assert.Zero(t, acked, "恒失败的 handler 不该有任何 ack")

	assert.Equal(t, int64(1), countRows(t, db, "homeos_dead_letter"), "一条事件恰好一行死信（超限才写）")
	require.Len(t, js.published, 1, "dl.* 只 publish 一次")
	assert.Equal(t, "dl.homeos.finance.due.registered", js.published[0].subject)
	assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"), "超限收尾也要释放 claim，人工重放才走得动同一幂等键")
	assertNoDecoyRows(t, db)
}

// TestMalformedEnvelopeTerminatesImmediately: bytes that are not an envelope at all are a
// deterministic poison message -- redelivery can never make them parse. Unlike a handler failure they
// do not get the retry budget, and there is no business_id to release because the envelope never
// parsed, so the底座 parks them once and terms the delivery.
func TestMalformedEnvelopeTerminatesImmediately(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "homeos.member.created"})

	handled := 0
	c.handler = func(ctx context.Context, msg Message) error {
		handled++
		return nil
	}

	msg := &fakeMsg{subject: "homeos.member.created", data: []byte("{not json"), numDelivered: 1}
	c.handleMessage(context.Background(), msg)

	assert.Equal(t, 1, msg.terminated, "解不出信封的重投还是解不出，直接 Term 收尾，不烧 backoff 预算")
	assert.Zero(t, msg.nacked, "畸形信封不该 Nak —— 那只是把同一次失败再走 3 遍")
	assert.Zero(t, msg.acked, "不能当成功 ack 掉")
	assert.Zero(t, handled, "解不出信封没有可交给 handler 的消息")

	assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"),
		"信封没解出来就没有 business_id，去重表一行都不该被动过")

	var letters []map[string]any
	require.NoError(t, db.Table(DeadLetterTableName("homeos")).Find(&letters).Error)
	require.Len(t, letters, 1, "畸形信封也要留在重放表里，不能解不出来就丢")
	assert.Contains(t, letters[0]["last_error"], "malformed envelope")
	assert.Equal(t, "homeos.member.created", letters[0]["event_type"], "用 consumer 配置的事件类型，NOT NULL 不能空")
	assert.Nil(t, letters[0]["family_id"], "信封没解出来，family_id 只能是 NULL")
	assert.Contains(t, letters[0]["envelope"], "{not json", "存原文")

	require.Len(t, js.published, 1)
	assert.Equal(t, "dl.homeos.homeos.member.created", js.published[0].subject)
	assertNoDecoyRows(t, db)
}

// TestReleaseDedupeDeletesOnlyItsOwnKey guards the release against being worse than the bug: a
// DELETE whose WHERE is not the full (event_type, business_id) key would wipe every event's claim and
// turn「重复投递不产生重复业务对象」into「所有事件都重来一遍」.
func TestReleaseDedupeDeletesOnlyItsOwnKey(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered"})

	ctx := context.Background()
	claimed, err := c.checkDedupe(ctx, "finance.due.registered", "bill-40:2026-10-05")
	require.NoError(t, err)
	assert.False(t, claimed, "第一次投递不是重复")

	// A second, unrelated event claimed in the same table -- it must survive the release below.
	claimedOther, err := c.checkDedupe(ctx, "finance.due.registered", "bill-41:2026-10-05")
	require.NoError(t, err)
	assert.False(t, claimedOther)
	require.Equal(t, int64(2), countRows(t, db, "homeos_event_dedupe"))

	require.NoError(t, c.releaseDedupe(ctx, "finance.due.registered", "bill-40:2026-10-05"))
	assert.Equal(t, int64(1), countRows(t, db, "homeos_event_dedupe"),
		"释放只删自己那一个键；别的事件的 claim 被删掉就等于让别的事件重复处理")

	again, err := c.checkDedupe(ctx, "finance.due.registered", "bill-40:2026-10-05")
	require.NoError(t, err)
	assert.False(t, again, "释放之后同一个键必须能重新 claim —— 这正是重投与人工重放的入口")

	otherStillClaimed, err := c.checkDedupe(ctx, "finance.due.registered", "bill-41:2026-10-05")
	require.NoError(t, err)
	assert.True(t, otherStillClaimed, "另一条事件的 claim 不受影响")

	// Releasing a key nobody holds is not a failure, and a half-empty key is.
	require.NoError(t, c.releaseDedupe(ctx, "finance.due.registered", "bill-99:2026-10-05"))
	require.Error(t, c.releaseDedupe(ctx, "finance.due.registered", ""), "business_id 为空时绝不能拼出无 WHERE 的 DELETE")
	require.Error(t, c.releaseDedupe(ctx, "", "bill-40:2026-10-05"))
	assertNoDecoyRows(t, db)
}

// TestDedupeReleaseFailureIsAlerted covers「释放本身失败时必须告警，不得静默忽略」: if the claim
// survives, every later delivery of that (event_type, business_id) is ack'd as a duplicate while the
// business effect has never happened -- only a human deleting the row unblocks it, so it has to be said
// out loud.
//
// The fixture forces the failure the way it actually happens in production rather than by patching
// GORM's callback table: the delivery context is cancelled inside the handler (AckWait expiring, or
// the consumer's shutdown), so the claim went in on a live context while the DELETE that would hand
// it back only ever sees a cancelled one.
func TestDedupeReleaseFailureIsAlerted(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered"})

	var alerts []string
	c.alertFn = capture(&alerts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlerErr := errors.New("insert into homeos_due_registration: crash")
	c.handler = func(_ context.Context, msg Message) error {
		cancel()
		return handlerErr
	}

	env, err := json.Marshal(Envelope{
		EventType:  "finance.due.registered",
		BusinessID: "bill-50:2026-10-05",
		FamilyID:   "2f9a1c3e-0000-4a7b-9c1d-000000000050",
		Version:    "1.0",
	})
	require.NoError(t, err)

	msg := &fakeMsg{subject: "finance.due.registered", data: env, numDelivered: 1}
	c.handleMessage(ctx, msg)

	assert.Equal(t, 1, msg.nacked, "释放失败不改 ack 语义：还在预算内就照旧 Nak，能救一次是一次")
	// 用没被取消的上下文读回来：这一行确实还占着，下面那条告警说的就是它。
	assert.Equal(t, int64(1), countRows(t, db, "homeos_event_dedupe"), "夹具逼出的正是「claim 还留着」这个状态")
	require.Len(t, alerts, 1, "删不掉 claim 就是「这条事件被永久吞掉」，静默忽略等于把缺陷换了个形态留下")
	assert.Contains(t, alerts[0], "bill-50:2026-10-05", "告警要指名是哪一把键卡住了：%v", alerts)
	assert.Contains(t, alerts[0], "context canceled", "告警要带上删除失败的原因：%v", alerts)
	assert.Contains(t, alerts[0], "insert into homeos_due_registration: crash", "还要带上业务失败的原因：%v", alerts)
	assert.Equal(t, int64(0), countRows(t, db, "homeos_dead_letter"), "第 1/4 次仍不写死信")
	assertNoDecoyRows(t, db)
}

// TestUnreadableDeliveryMetadataParksTheEvent pins the one guess handleMessage makes: without a
// delivery counter it cannot tell whether the retry budget has room left, and the ending that cannot
// lose the event is the over-budget one (a dead-letter row survives; a Nak nobody can redeliver is
// just silence).
func TestUnreadableDeliveryMetadataParksTheEvent(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "finance.due.registered"})

	var alerts []string
	c.alertFn = capture(&alerts)

	c.handler = func(ctx context.Context, msg Message) error { return errors.New("downstream 503") }

	env, err := json.Marshal(Envelope{
		EventType:  "finance.due.registered",
		BusinessID: "bill-60:2026-10-05",
		Version:    "1.0",
	})
	require.NoError(t, err)

	msg := &fakeMsg{subject: "finance.due.registered", data: env, metadataErr: errors.New("metadata not available")}
	c.handleMessage(context.Background(), msg)

	assert.Zero(t, msg.nacked, "读不到投递序号时 Nak 换不来任何重投")
	assert.Equal(t, 1, msg.terminated)
	assert.Equal(t, int64(1), countRows(t, db, "homeos_dead_letter"), "按超限收尾：至少把原文留在重放表里")
	assert.Equal(t, int64(0), countRows(t, db, "homeos_event_dedupe"), "收尾之前照旧释放 claim")
	require.Len(t, alerts, 1, "读不到投递序号是猜出来的超限，必须告警")
	assert.Contains(t, alerts[0], "finance.due.registered")
	assertNoDecoyRows(t, db)
}

func TestMalformedEnvelopeGoesToDeadLetter(t *testing.T) {
	db := newDDLFixture(t)
	js := &fakeJetStream{}
	c := NewDurableConsumer(db, js, ConsumerConfig{Code: "homeos", EventType: "homeos.member.created"})

	c.handleMessage(context.Background(), &fakeMsg{subject: "homeos.member.created", data: []byte("{not json")})

	var rows []map[string]any
	require.NoError(t, db.Table(DeadLetterTableName("homeos")).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Contains(t, rows[0]["last_error"], "malformed envelope")
	assert.Equal(t, "homeos.member.created", rows[0]["event_type"], "信封解析不出来时用 consumer 配置的事件类型，NOT NULL 不能空")
	assert.Equal(t, "dl.homeos.homeos.member.created", js.published[0].subject)
	assertNoDecoyRows(t, db)
}

// ==================== the collision classifier that decides 「已存在」 =============================

// TestRealUniqueViolationStillClassifies covers the fallback branch: a driver that reports the
// collision as an error instead of swallowing it must still be read as a duplicate, not as a
// database failure. The hand-rolled lower-casing it replaces only ever lowered the LAST upper-case
// rune, so sqlite's 「UNIQUE constraint failed」 matched nothing and every duplicate looked like an
// outage (Nak, retry, handler never runs).
func TestRealUniqueViolationStillClassifies(t *testing.T) {
	db := newDDLFixture(t)

	first := DedupeRecord{EventType: "finance.due.registered", BusinessID: "bill-3:2026-10-05", CreatedAt: time.Now()}
	require.NoError(t, db.Table(DedupeTableName("homeos")).Create(&first).Error)

	second := DedupeRecord{EventType: "finance.due.registered", BusinessID: "bill-3:2026-10-05", CreatedAt: time.Now()}
	err := db.Table(DedupeTableName("homeos")).Create(&second).Error
	require.Error(t, err, "不带 ON CONFLICT 的重复插入要照原样报错")
	assert.True(t, isUniqueViolation(err), "sqlite 的冲突文本：%v", err)

	// The texts the two dialects this project runs on actually produce.
	for _, s := range []string{
		`ERROR: duplicate key value violates unique constraint "homeos_event_dedupe_key" (SQLSTATE 23505)`,
		`SQLITE_CONSTRAINT_UNIQUE: UNIQUE constraint failed: homeos_event_dedupe.event_type, homeos_event_dedupe.business_id`,
	} {
		assert.True(t, isUniqueViolation(errors.New(s)), "必须认出：%s", s)
	}

	assert.False(t, isUniqueViolation(errors.New("context deadline exceeded")))
	assert.False(t, isUniqueViolation(nil))
}

// ==================== code <-> shipped DDL, checked against the migration file ====================

// TestBusModelColumnsMatchShippedDDL is the falsification of the whole defect class this card fixes.
// It reads the published 0002 up-file, takes each table's declared column set, and requires the GORM
// schema of the three bus models to be exactly that set. Add a column to a struct that the DDL does
// not have -- updated_at, sent_at, error, subject, attempts-on-dead-letter, an id on the dedupe
// table -- and this goes red before any of it reaches a database.
func TestBusModelColumnsMatchShippedDDL(t *testing.T) {
	db := newDDLFixture(t)

	for _, tt := range []struct {
		table string
		model any
	}{
		{"homeos_outbox", &OutboxMessage{}},
		{"homeos_event_dedupe", &DedupeRecord{}},
		{"homeos_dead_letter", &DeadLetterRecord{}},
	} {
		want := ddlColumns(t, tt.table)
		require.NotEmpty(t, want, "没能从迁移文件里解析出 %s 的列", tt.table)

		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(tt.model))
		assert.ElementsMatch(t, want, stmt.Schema.DBNames,
			"%s 的 GORM 列集必须与 0002 声明的一致（多了就是 42703，少了就是文档里的列没人写）", tt.table)

		// And the fixture the other tests run on is not more generous than the DDL either.
		got := sqliteColumns(t, db, tt.table)
		assert.ElementsMatch(t, want, got, "sqlite 夹具必须按 DDL 原样建表")
	}
}

// TestInsertOutboxMessageRejectsEmptyCode keeps the {code}_ prefix from degrading into "_outbox",
// which is the same bug in a different costume.
func TestInsertOutboxMessageRejectsEmptyCode(t *testing.T) {
	db := newDDLFixture(t)

	err := InsertOutboxMessage(db, "", "homeos.member.created", `{}`)
	require.Error(t, err, "空 code 不能拼出 _outbox 这种表名")
	assert.Contains(t, err.Error(), "service code")
	assert.Equal(t, int64(0), countRows(t, db, "homeos_outbox"))
}

func TestInsertOutboxMessageWritesFamilyID(t *testing.T) {
	db := newDDLFixture(t)

	require.NoError(t, InsertOutboxMessage(db, "homeos", "homeos.family.created", `{"event_type":"homeos.family.created"}`))
	require.NoError(t, InsertOutboxMessageWithFamily(db, "homeos", "2f9a1c3e-0000-4a7b-9c1d-000000000003",
		"homeos.member.created", `{}`))

	rows := outboxRows(t, db)
	require.Len(t, rows, 2)

	// §2.2「系统预置数据用可空 family_id 表达」: no family -> NULL, never ''.
	assert.Nil(t, rows[0]["family_id"])
	assert.Equal(t, "2f9a1c3e-0000-4a7b-9c1d-000000000003", rows[1]["family_id"])
	assert.Equal(t, "pending", rows[1]["status"])
	assertNoDecoyRows(t, db)
}

// ==================== test doubles (only ever in this file) ======================================

type publishedMsg struct {
	subject string
	data    []byte
}

// fakeJetStream stands in for the NATS connection: the outbox deliverer and the consumer both reach
// the wire through JetStreamWrapper, so the two failure modes under test (publish ok / publish err)
// are the only things it has to model.
type fakeJetStream struct {
	failWith  error
	published []publishedMsg
	// calls counts every Publish attempt, successful or not, so a test that forces failures can
	// still assert the deliverer keeps trying instead of losing the row.
	calls int
}

func (f *fakeJetStream) CreateStream(ctx context.Context, name string, subjects []string) error {
	return nil
}

func (f *fakeJetStream) Publish(ctx context.Context, subject string, data []byte) (*jetstream.PubAck, error) {
	f.calls++
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.published = append(f.published, publishedMsg{subject: subject, data: data})
	return &jetstream.PubAck{}, nil
}

func (f *fakeJetStream) Subscribe(ctx context.Context, streamName, consumerName, filterSubject string, handler jetstream.MessageHandler) error {
	return nil
}

func (f *fakeJetStream) Close() {}

func (f *fakeJetStream) count() int { return len(f.published) }

// fakeMsg is a JetStream delivery: the embedded nil interface keeps the rest of jetstream.Msg
// present without the server, and handleMessage only ever touches the six methods below.
//
// numDelivered is what the real delivery carries in Metadata().NumDelivered: 1 on the first attempt,
// 2 on the first redelivery. It is what decides whether a handler failure is still inside the retry
// budget (§3.4「max_deliver=4」) or over it, so a test that reaches the failure path has to set it --
// leaving it zero reads as「first delivery」.
type fakeMsg struct {
	jetstream.Msg
	subject      string
	data         []byte
	numDelivered uint64
	metadataErr  error
	acked        int
	nacked       int
	terminated   int
}

func (m *fakeMsg) Data() []byte    { return m.data }
func (m *fakeMsg) Subject() string { return m.subject }
func (m *fakeMsg) Headers() nats.Header {
	return nats.Header{}
}
func (m *fakeMsg) Ack() error {
	m.acked++
	return nil
}
func (m *fakeMsg) Nak() error {
	m.nacked++
	return nil
}

// Term is the「别再投了」answer: the底座 uses it once the retry budget is spent (and on a malformed
// envelope, which no retry can fix), so a test can tell it apart from Nak.
func (m *fakeMsg) Term() error {
	m.terminated++
	return nil
}

// Metadata carries the delivery counter handleMessage reads to know which attempt this is.
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	if m.metadataErr != nil {
		return nil, m.metadataErr
	}
	return &jetstream.MsgMetadata{NumDelivered: m.numDelivered}, nil
}

func capture(sink *[]string) func(string) {
	return func(msg string) { *sink = append(*sink, msg) }
}

// ==================== migration-file readers ===================================================

const ddl0002Path = "../../migrations/homeos/homeos_0002_outbox_dedupe_dead_letter.up.sql"

// ddlColumns parses the declared column set of one table out of 0002, so the assertions above are
// checked against the shipped DDL rather than against a copy of it written here.
func ddlColumns(t *testing.T, table string) []string {
	t.Helper()

	raw, err := os.ReadFile(ddl0002Path)
	require.NoError(t, err, "0002 必须还在原位：%s", ddl0002Path)

	lines := strings.Split(string(raw), "\n")
	marker := "CREATE TABLE IF NOT EXISTS homeos." + table + " ("
	start := -1
	for i, line := range lines {
		if strings.Contains(strings.Join(strings.Fields(line), " "), marker) {
			start = i + 1
			break
		}
	}
	require.True(t, start > 0, "0002 里没有 %s", table)

	var columns []string
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "--"):
			continue
		case strings.HasPrefix(trimmed, ")"):
			return columns
		}
		// Table-level constraints would start with these; 0002 keeps them outside the body, but a
		// future PRIMARY KEY / UNIQUE line must not be mistaken for a column.
		first := strings.Fields(trimmed)[0]
		switch strings.ToUpper(first) {
		case "PRIMARY", "UNIQUE", "CHECK", "FOREIGN", "CONSTRAINT":
			continue
		}
		columns = append(columns, strings.TrimSuffix(first, ","))
	}
	t.Fatalf("0002 的 %s 没有闭合", table)
	return nil
}

// sqliteColumns reads back the columns the fixture actually created.
func sqliteColumns(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()

	var rows []map[string]any
	require.NoError(t, db.Table("sqlite_master").
		Where("type = ? AND name = ?", "table", table).
		Select("sql").Find(&rows).Error)
	require.Len(t, rows, 1, "夹具里没有 %s", table)

	ddl, _ := rows[0]["sql"].(string)
	open := strings.Index(ddl, "(")
	require.True(t, open >= 0, "%s 的建表语句读不出列", table)

	var columns []string
	for _, part := range strings.Split(ddl[open+1:], ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) < 2 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "PRIMARY", "UNIQUE", "CHECK", "FOREIGN", "CONSTRAINT":
			continue
		}
		columns = append(columns, fields[0])
	}
	return columns
}
