// due_revoked_test.go is REV-1's consumer-side test: one accepted finance.due.revoked delivery becomes
// one deleted_at stamp on homeos_due_registration, and nothing else.
//
// Four things it pins that the shipped code cannot show elsewhere:
//
//  1. The 撤销 is produced by the code under test. wiring_test.go's「撤销后重新排期」case used to write
//     deleted_at by hand, which proves only that the table tolerates the column; here the revocation is
//     DueRevokedHandler's own UPDATE, so the assertion covers the path the B 区 depends on.
//  2. Idempotency at BOTH layers (§3.4「至少一次投递」): a redelivery of the same (event_type,
//     business_id) is stopped by the底座's {code}_event_dedupe and acked, while a second revoked event
//     for the same object under a NEW key reaches the handler and changes nothing -- 0 rows matched,
//     no error, no dead letter. Only the second is this handler's own judgement.
//  3. Refusals are refusals: no payload / no source_system / no source_id / no family_id / an envelope
//     family_id contradicting the payload's / a reason outside
//     contracts/events/finance.yaml:105's enum(completed|deleted|expired) / an unparsable revoked_at / a
//     business_id that is not the contract's 「"{source_id}:{due_at}"」 all return errors, which is what
//     puts the delivery on §3.4's failure path -- Nak'd through the max_deliver=4 ladder and only then
//     parked in the dead letter, instead of quietly leaving a paid bill in 首页 B 区.
//  4. The 撤销锚点 is (source_system, source_id, family_id, due_at) and it lands on exactly one row
//     (RD-1). homeos_0008:52-54 make the unique index partial on kind, so one (source_system, source_id)
//     can hold several live registrations at once -- one per kind, and in principle one per family. The
//     cases below assert that another 期 and another family's row survive, and that every refusal leaves
//     0 soft-deleted rows: 「宁可进死信也不误删」 is only a sentence until a test reads the table back.
//
// Fixtures (newDueTable / newDedupeTable / newDeadLetterTable / fakeJS / testMsg / countRows /
// deadLetters / dedupeCount / the two fixture ids) are wiring_test.go's, same package: the revoked chain
// runs against the same 0008-shaped table as the registration chain, which is the point.
//
// Fixtures (newDueTable / newDedupeTable / newDeadLetterTable / fakeJS / testMsg / countRows /
// deadLetters / dedupeCount / the two fixture ids) are wiring_test.go's, same package: the revoked chain
// runs against the same 0008-shaped table as the registration chain, which is the point.
package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// revokedClock is the business moment the producer puts in revoked_at -- the bill's paid_at, not this
// consumer's now(), so a redelivery cannot move the 撤销时刻.
const revokedClock = "2026-10-10T09:30:00Z"

// The three extra facts the锚点 cases need. secondDueAt is the 期 a rescheduled object registers under
// (wiring_test.go uses the same instant inline); otherFamilyID is ANOTHER household holding a
// registration for the same source_id -- the cross-family row the two-column anchor used to hit;
// otherSourceID is a sibling object of the same family.
const (
	secondDueAt   = "2026-11-10T00:00:00Z"
	otherFamilyID = "8d2c2f7b-3b5f-5e7f-8b21-22a3f4e5d6c7"
	otherSourceID = "bb2b1e6a-2a4f-4d6e-9a10-11f2e3d4c5b6"
)

// revokedEnvelope is bus.Envelope's wire form of contracts/events/finance.yaml:93-105: the payload holds
// exactly that entry's five payload_schema keys, and business_id is its
// business_id: "{source_id}:{due_at}".
func revokedEnvelope(businessID, revokedAt, reason string) bus.Envelope {
	return bus.Envelope{
		EventType:  DueRevokedEventType,
		BusinessID: businessID,
		FamilyID:   fixtureFamilyID,
		Version:    "1.0",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"source_system": "finance",
			"source_id":     fixtureSourceID,
			"family_id":     fixtureFamilyID,
			"revoked_at":    revokedAt,
			"reason":        reason,
		},
	}
}

func revokedMessage(env bus.Envelope) bus.Message {
	return bus.Message{Subject: DueRevokedEventType, Envelope: env}
}

// quietLogger keeps the handler's Info line out of the test log without dropping the code path that
// writes it; SetupBus hands the same slot its obs logger.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// registrationRow is the whole state this file judges: deleted_at is the撤销, deleted_by stays NULL
// (homeos_0008:44-46 reserves it for a 成员 id and the actor here is a service), and title / due_at
// stay because 0008 revokes by软删 rather than by rewriting or removing the row.
type registrationRow struct {
	ID        string
	Title     string
	DueAt     time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	DeletedBy *string
}

func readRegistration(t *testing.T, db *gorm.DB, id string) registrationRow {
	t.Helper()

	var row registrationRow
	require.NoError(t, db.Table("homeos_due_registration").
		Where("id = ?", id).First(&row).Error, "读回注册行：撤销是打标记，不是删行")
	return row
}

// registerOneLiveRow drives the registration through the shipped handler and returns its row, so every
// case below starts from a real注册 rather than a hand-written one.
func registerOneLiveRow(t *testing.T, db *gorm.DB, dueAt, title string) registrationRow {
	t.Helper()

	require.NoError(t, DueRegisteredHandler(db)(context.Background(), dueMessage(
		dueEnvelope(fixtureSourceID+":"+dueAt, dueAt, title))))

	var ids []string
	require.NoError(t, db.Table("homeos_due_registration").
		Where("deleted_at IS NULL").Pluck("id", &ids).Error)
	require.Len(t, ids, 1, "注册后恰有一条活行")
	return readRegistration(t, db, ids[0])
}

// dueAtInstant parses one fixture due-date text the way both handlers parse it (time.RFC3339, so a
// trailing Z means UTC). sqlite stores and compares the serialised instant, so a hand-built row has to
// carry the same UTC moment the event names -- one built from time.Now() in the machine's own zone would
// match nothing and prove nothing.
func dueAtInstant(t *testing.T, text string) time.Time {
	t.Helper()

	instant, err := time.Parse(time.RFC3339, text)
	require.NoError(t, err, "fixture 的 due_at 文本必须是 RFC3339")
	return instant
}

// registerHandRow writes one live registration directly. The shipped registration handler cannot produce
// these: dueEnvelope pins kind=bill and fixtureFamilyID for fixtureSourceID, while the锚点 cases are about
// the OTHER live rows sharing that (source_system, source_id) -- another kind, another 期, another family.
// 0008:52-54's partial unique index is what makes that legal (one live row per kind), so the rows this
// helper builds are the exact shape the dev库 showed.
func registerHandRow(t *testing.T, db *gorm.DB, id, familyID, sourceID, kind, dueAtText, title string) registrationRow {
	t.Helper()

	dueAt := dueAtInstant(t, dueAtText)
	now := time.Now()
	require.NoError(t, db.Table("homeos_due_registration").Create(map[string]any{
		"id": id, "family_id": familyID, "source_system": "finance", "source_id": sourceID,
		"kind": kind, "title": title, "due_at": dueAt, "created_at": now, "updated_at": now,
	}).Error)
	return readRegistration(t, db, id)
}

// runLoggedRevocation drives one delivery through the shipped handler against an in-memory slog sink.
// rows_revoked is the handler's only in-process evidence distinguishing「撤销生效」from「事件被吞了」
// (and, since RD-1, from「那一期根本没有注册」), so it has to be assertable rather than readable only in a
// developer's terminal.
func runLoggedRevocation(t *testing.T, db *gorm.DB, env bus.Envelope) (string, error) {
	t.Helper()

	var logs bytes.Buffer
	err := DueRevokedHandler(db, slog.New(slog.NewTextHandler(&logs, nil)))(context.Background(), revokedMessage(env))
	return logs.String(), err
}

// ==================== the handler ====================

// TestDueRevokedHandlerStampsDeletedAtOnLiveRegistration is 判据「消费侧把对应行打上 deleted_at」: the
// registration comes from DueRegisteredHandler, the撤销 from DueRevokedHandler, and the row is still
// there afterwards.
func TestDueRevokedHandlerStampsDeletedAtOnLiveRegistration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
	assert.Nil(t, live.DeletedAt, "撤销前是活行")

	require.NoError(t, DueRevokedHandler(db, quietLogger())(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))))

	after := readRegistration(t, db, live.ID)
	assert.Equal(t, live.ID, after.ID, "撤销是对同一条注册打标记")
	require.NotNil(t, after.DeletedAt, "deleted_at 必须被写上，否则账单还留在首页 B 区")
	assert.True(t, after.DeletedAt.UTC().Format(time.RFC3339) == revokedClock,
		"deleted_at 取事件里的 revoked_at（业务时刻），got %s want %s",
		after.DeletedAt.UTC().Format(time.RFC3339), revokedClock)
	assert.Nil(t, after.DeletedBy, "跨服务撤销的动作者不是家庭成员（homeos_0008:44-46）")
	assert.Equal(t, live.Title, after.Title, "撤销不重写注册内容")
	assert.True(t, after.UpdatedAt.After(live.UpdatedAt), "updated_at 留下这次改动的痕迹")

	assert.Equal(t, int64(1), countRows(t, db, "1 = 1"), "软删：行留在注册历史里")
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NULL"), "活行归零，B 区不再读它")
}

// TestDueRevokedHandlerSecondDeliveryChangesNothing is 判据 4's handler-level half: a revoked event for
// the SAME object under a NEW business_id (so the去重层 cannot be what saves it) runs again and must
// neither error nor move the already-revoked row.
func TestDueRevokedHandlerSecondDeliveryChangesNothing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")

	handler := DueRevokedHandler(db, quietLogger())
	require.NoError(t, handler(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))))
	first := readRegistration(t, db, live.ID)
	require.NotNil(t, first.DeletedAt)

	// Same object, different key, later 撤销时刻.
	require.NoError(t, handler(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":2026-11-10T00:00:00Z", "2026-11-10T09:30:00Z", "completed"))))

	second := readRegistration(t, db, live.ID)
	assert.Equal(t, first, second, "重复撤销：一条已撤销状态就是它本来的样子，不二次改动、也不复活")
	assert.Equal(t, int64(1), countRows(t, db, "1 = 1"), "仍是一条行")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NOT NULL"), "仍是一条已撤销行")
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NULL"))
}

// TestDueRevokedHandlerOnUnknownObjectIsNotAFailure: 撤销一个本服务没注册过的对象是幂等终点，不是错误 --
// 报错会让 §3.4 把一条已经达成目的的事件写进死信表。
func TestDueRevokedHandlerOnUnknownObjectIsNotAFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	err = DueRevokedHandler(db, quietLogger())(context.Background(), revokedMessage(
		revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "deleted")))
	require.NoError(t, err, "没有活行可撤销时不能报错")
	assert.Equal(t, int64(0), countRows(t, db, "1 = 1"))
}

// TestDueRevokedHandlerTouchesOnlyItsOwnObject: the anchor names one object, so a sibling registration --
// same family, other object, other source domain -- keeps its live row.
func TestDueRevokedHandlerTouchesOnlyItsOwnObject(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	target := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")

	// A second object of the same family, and a same-id object from ANOTHER source domain.
	require.NoError(t, db.Table("homeos_due_registration").Create(map[string]any{
		"id": "sibling-row", "family_id": fixtureFamilyID, "source_system": "finance",
		"source_id": otherSourceID, "kind": "goal",
		"title": "存钱目标", "due_at": time.Now(), "created_at": time.Now(), "updated_at": time.Now(),
	}).Error)
	require.NoError(t, db.Table("homeos_due_registration").Create(map[string]any{
		"id": "other-domain-row", "family_id": fixtureFamilyID, "source_system": "purchase",
		"source_id": fixtureSourceID, "kind": "bill",
		"title": "采购到期", "due_at": time.Now(), "created_at": time.Now(), "updated_at": time.Now(),
	}).Error)

	require.NoError(t, DueRevokedHandler(db, quietLogger())(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))))

	assert.NotNil(t, readRegistration(t, db, target.ID).DeletedAt, "被撤销的是事件指向的那条")
	var untouched int64
	require.NoError(t, db.Table("homeos_due_registration").
		Where("id IN ? AND deleted_at IS NULL", []string{"sibling-row", "other-domain-row"}).
		Count(&untouched).Error)
	assert.Equal(t, int64(2), untouched, "同一家庭的另一条注册、以及另一来源域的同 id 注册都不许被动到")
}

// TestDueRevokedHandlerAnchorsOnePeriod is RD-1's core case, and the shape the dev库 actually showed: one
// (source_system, source_id) holds TWO live registrations at once -- homeos_0008:52-54's unique index is
// partial on kind, so a bill and a budget can carry the same object id side by side. The two-column anchor
// revoked both with one UPDATE, i.e. it soft-deleted a registration the event never named. The撤销 now
// answers to the 期 business_id carries, and only that one.
func TestDueRevokedHandlerAnchorsOnePeriod(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	target := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
	sibling := registerHandRow(t, db, "other-period-row", fixtureFamilyID, fixtureSourceID,
		"budget", secondDueAt, "11 月预算")

	logs, err := runLoggedRevocation(t, db, revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))
	require.NoError(t, err)
	assert.Contains(t, logs, "rows_revoked=1", "锚点收到的是唯一一行，got %s", logs)

	after := readRegistration(t, db, target.ID)
	require.NotNil(t, after.DeletedAt, "被撤销的是事件指名的那一期")
	assert.True(t, after.DeletedAt.UTC().Format(time.RFC3339) == revokedClock,
		"deleted_at 仍是事件的 revoked_at，got %s", after.DeletedAt)

	require.Nil(t, readRegistration(t, db, sibling.ID).DeletedAt,
		"同一对象的另一期必须仍然活着：撤销不是「这个 source_id 的所有注册」")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NULL"), "活行只剩另一期那一条")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NOT NULL"))
	assert.Equal(t, int64(2), countRows(t, db, "1 = 1"), "两条行都留在注册历史里（软删不删行）")

	// The other direction, so due_at is shown to be a real anchor rather than「先到的那条算数」: 撤销另一期
	// 的事件带走的是另一行，第一条的撤销时刻不动。
	secondClock := "2026-11-10T09:30:00Z"
	logs, err = runLoggedRevocation(t, db, revokedEnvelope(fixtureSourceID+":"+secondDueAt, secondClock, "expired"))
	require.NoError(t, err)
	assert.Contains(t, logs, "rows_revoked=1")
	assert.True(t, readRegistration(t, db, sibling.ID).DeletedAt.UTC().Format(time.RFC3339) == secondClock,
		"第二期打上的是它自己那条事件的撤销时刻")
	assert.True(t, readRegistration(t, db, target.ID).DeletedAt.UTC().Format(time.RFC3339) == revokedClock,
		"另一条的撤销时刻不跟着动")
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NULL"))
}

// TestDueRevokedHandlerDoesNotCrossFamilies: family_id is the boundary that carries this case alone --
// the other household's registration is for the SAME source_id at the SAME due_at, so a WHERE without
// family_id revokes a row belonging to a different family. §2.2「索引第一列固定 family_id」/15.2「跨家庭
// 读取一律拒绝」 say a query in this service never spans families; a query that spans families and WRITEs
// is worse than the read this project already refuses.
//
// (The row is registered under another kind because 0008:52-54's unique index is
// (source_system, source_id, kind) WHERE deleted_at IS NULL and does not carry family_id -- two families
// cannot hold a live row of the SAME kind for one source_id at all. That the index omits family_id is
// reported as a separate observation, not fixed here.)
func TestDueRevokedHandlerDoesNotCrossFamilies(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	target := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
	other := registerHandRow(t, db, "other-family-row", otherFamilyID, fixtureSourceID,
		"budget", fixtureDueAt, "别家的同一期")

	require.NoError(t, DueRevokedHandler(db, quietLogger())(ctx, revokedMessage(
		revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))))

	require.NotNil(t, readRegistration(t, db, target.ID).DeletedAt, "本家的这一期撤销了")
	assert.Nil(t, readRegistration(t, db, other.ID).DeletedAt, "另一个家庭同样 due_at 的注册不受影响")
	assert.Equal(t, int64(1), countRows(t, db, "family_id = ? AND deleted_at IS NULL", otherFamilyID),
		"跨家庭一行都没动")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NOT NULL"), "撤销只有一条")
}

// TestRevokeDueRegistrationNeedsEveryAnchorColumn pins the exported function's own boundary with no
// envelope in the way: the four columns plus deleted_at IS NULL are the WHERE, and an anchor that misses
// any one of them answers 0 rows instead of touching a row it no longer names.
func TestRevokeDueRegistrationNeedsEveryAnchorColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
	revokedAt := dueAtInstant(t, revokedClock)
	dueAt := dueAtInstant(t, fixtureDueAt)

	n, err := RevokeDueRegistration(ctx, db, "finance", fixtureSourceID, otherFamilyID, dueAt, revokedAt)
	require.NoError(t, err)
	assert.Zero(t, n, "family_id 不符就是 0 行，不是「差不多就删」")
	assert.Nil(t, readRegistration(t, db, live.ID).DeletedAt)

	n, err = RevokeDueRegistration(ctx, db, "finance", fixtureSourceID, fixtureFamilyID,
		dueAtInstant(t, secondDueAt), revokedAt)
	require.NoError(t, err)
	assert.Zero(t, n, "due_at 不符就是 0 行：撤销不顺手带走同一对象的其他期")
	assert.Nil(t, readRegistration(t, db, live.ID).DeletedAt)

	n, err = RevokeDueRegistration(ctx, db, "finance", fixtureSourceID, fixtureFamilyID, dueAt, revokedAt)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "四列齐才命中，且只命中一条")
	after := readRegistration(t, db, live.ID)
	require.NotNil(t, after.DeletedAt)
	assert.True(t, after.DeletedAt.UTC().Format(time.RFC3339) == revokedClock,
		"deleted_at 取发布方的业务时刻，got %s", after.DeletedAt)
	assert.Nil(t, after.DeletedBy, "跨服务撤销的动作者不是家庭成员（homeos_0008:44-46）")

	n, err = RevokeDueRegistration(ctx, db, "finance", fixtureSourceID, fixtureFamilyID, dueAt, revokedAt)
	require.NoError(t, err, "重复撤销不是错误")
	assert.Zero(t, n, "deleted_at IS NULL 已不成立，重投命中 0 行")
	assert.Equal(t, after, readRegistration(t, db, live.ID), "重投也不二次改动")
}

// TestDueRevokedHandlerRedeliveryOfTheSameEventIsZeroRows: 让重投安全的是这条 WHERE 本身，不是去重层
// （去重层由 TestDurableConsumerPathRevokesAndDeduplicates 管）。同一个 envelope 走同一颗 handler 两次：
// 第二次 0 行、返回 nil、日志留下 rows_revoked=0 -- §3.4「重复投递不报错」，报错就是误写死信。
func TestDueRevokedHandlerRedeliveryOfTheSameEventIsZeroRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
	env := revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed")

	firstLogs, err := runLoggedRevocation(t, db, env)
	require.NoError(t, err)
	assert.Contains(t, firstLogs, "rows_revoked=1")
	assert.Contains(t, firstLogs, "due_at="+fixtureDueAt, "锚点的一期要写进日志：0 行时得知道撤销的是哪一期")

	secondLogs, err := runLoggedRevocation(t, db, env)
	require.NoError(t, err, "同一个事件重投不是失败")
	assert.Contains(t, secondLogs, "rows_revoked=0")

	after := readRegistration(t, db, live.ID)
	require.NotNil(t, after.DeletedAt)
	assert.True(t, after.DeletedAt.UTC().Format(time.RFC3339) == revokedClock, "重投不移动撤销时刻")
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NULL"))
	assert.Equal(t, int64(1), countRows(t, db, "1 = 1"))
}

// TestDueRevokedHandlerRefusals: every refusal is an error (so §3.4 sends it to the dead letter) and
// leaves the live registration exactly as it was -- 「撤销」 never happens on a partial read. The table
// carries the anchor's own refusals too (family_id, business_id), because those are exactly the ones a
// wide fallback could have quietly turned into a wrong UPDATE: with a row for another 期 and a row for
// another family in the same table, the judgement asserted afterwards is 0 soft-deleted rows.
func TestDueRevokedHandlerRefusals(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(bus.Envelope) bus.Envelope
		wantErr string
		wantIs  error
	}{
		{
			name: "缺 reason",
			mutate: func(env bus.Envelope) bus.Envelope {
				delete(env.Payload, "reason")
				return env
			},
			wantErr: "enum(completed|deleted|expired)",
		},
		{
			name: "reason 在契约枚举外",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.Payload["reason"] = "cancelled"
				return env
			},
			wantErr: "enum(completed|deleted|expired)",
		},
		{
			name: "缺 source_system",
			mutate: func(env bus.Envelope) bus.Envelope {
				delete(env.Payload, "source_system")
				return env
			},
			wantErr: "source_system",
		},
		{
			name: "缺 source_id",
			mutate: func(env bus.Envelope) bus.Envelope {
				delete(env.Payload, "source_id")
				return env
			},
			wantErr: "source_id",
		},
		{
			name: "缺 family_id",
			mutate: func(env bus.Envelope) bus.Envelope {
				delete(env.Payload, "family_id")
				return env
			},
			wantErr: "不能少 family_id",
			wantIs:  ErrNoRevokedFamilyID,
		},
		{
			name: "信封 family_id 与 payload 不一致",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.FamilyID = otherFamilyID
				return env
			},
			wantErr: "不一致",
			wantIs:  ErrRevokedFamilyMismatch,
		},
		{
			name: "business_id 前段不是 payload.source_id",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.BusinessID = otherSourceID + ":" + fixtureDueAt
				return env
			},
			wantErr: "不是同一个对象",
			wantIs:  ErrRevokedBusinessIDShape,
		},
		{
			name: "business_id 没有冒号（不带 due_at 段）",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.BusinessID = fixtureSourceID
				return env
			},
			wantErr: `"{source_id}:{due_at}"`,
			wantIs:  ErrRevokedBusinessIDShape,
		},
		{
			name: "business_id 里的 due_at 不是 RFC3339",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.BusinessID = fixtureSourceID + ":not-a-timestamp"
				return env
			},
			wantErr: "due_at",
			wantIs:  ErrRevokedDueAtUnparsable,
		},
		{
			name: "revoked_at 不是 RFC3339",
			mutate: func(env bus.Envelope) bus.Envelope {
				env.Payload["revoked_at"] = "not-a-timestamp"
				return env
			},
			wantErr: "revoked_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			newDueTable(t, db)

			ctx := context.Background()
			live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")
			// 两条「宽锚点会顺手带走」的行：同一家庭的另一期，以及另一个家庭的同样一期。
			otherPeriod := registerHandRow(t, db, "other-period-row", fixtureFamilyID, fixtureSourceID,
				"budget", secondDueAt, "11 月预算")
			otherFamilyRow := registerHandRow(t, db, "other-family-row", otherFamilyID, fixtureSourceID,
				"repayment", fixtureDueAt, "别家的同一期")

			err = DueRevokedHandler(db, quietLogger())(ctx, revokedMessage(
				tc.mutate(revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			if tc.wantIs != nil {
				require.ErrorIs(t, err, tc.wantIs, "新增的拒绝要有可命名的哨兵，测试才断言得到是哪一条规则")
			}

			after := readRegistration(t, db, live.ID)
			assert.Equal(t, live, after, "被拒的投递不得改动注册行：撤销没发生就是没发生")
			assert.Nil(t, readRegistration(t, db, otherPeriod.ID).DeletedAt, "同一对象的另一期同样不许被动")
			assert.Nil(t, readRegistration(t, db, otherFamilyRow.ID).DeletedAt, "另一个家庭的注册同样不许被动")
			assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NOT NULL"),
				"宁可进死信也不误删：被拒的投递在整张表里留下 0 条撤销")
			assert.Equal(t, int64(3), countRows(t, db, "deleted_at IS NULL"), "三条活行都还在")
		})
	}
}

// TestDueRevokedHandlerRefusesPayloadlessEnvelope: the flat-envelope failure mode (fields at the TOP
// level of the envelope, so Envelope.Payload is empty) is refused, not defaulted to 「撤销一切」.
func TestDueRevokedHandlerRefusesPayloadlessEnvelope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")

	err = DueRevokedHandler(db, quietLogger())(ctx, bus.Message{
		Subject: DueRevokedEventType,
		Envelope: bus.Envelope{
			EventType:  DueRevokedEventType,
			BusinessID: fixtureSourceID + ":" + fixtureDueAt,
			FamilyID:   fixtureFamilyID,
		},
	})
	require.ErrorIs(t, err, ErrNoRevokedPayload)
	assert.Nil(t, readRegistration(t, db, live.ID).DeletedAt, "被拒的投递没有撤销任何东西")
}

// TestDueRevokedHandlerReadsNestedPayloadOnly is the跨包 proof that this consumer speaks the底座's
// envelope: bytes marshalled by bus.MarshalEnvelope from an Envelope with a nested Payload decode back
// through bus.UnmarshalEnvelope into exactly the five contract fields the handler consumes. A producer
// that put the fields at the top level would arrive here with an empty Payload (the blocking defect
// ENV-1 is fixing on the registration side).
func TestDueRevokedHandlerReadsNestedPayloadOnly(t *testing.T) {
	env := revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed")
	raw, err := bus.MarshalEnvelope(env)
	require.NoError(t, err)

	decoded, err := bus.UnmarshalEnvelope(raw)
	require.NoError(t, err)
	assert.Equal(t, DueRevokedEventType, decoded.EventType)
	assert.Equal(t, env.BusinessID, decoded.BusinessID)
	require.Len(t, decoded.Payload, 5, "契约的 payload_schema 恰有五个字段（finance.yaml:100-105）")

	// The flat shape, for contrast: same fields, no payload wrapper.
	flat, err := json.Marshal(map[string]any{
		"event_type": DueRevokedEventType, "source_system": "finance", "source_id": fixtureSourceID,
	})
	require.NoError(t, err)
	flatEnv, err := bus.UnmarshalEnvelope(flat)
	require.NoError(t, err)
	assert.Empty(t, flatEnv.Payload, "扁平信封在底座解出来就是空 payload —— 消费侧因此必须拒（ErrNoRevokedPayload）")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	require.ErrorIs(t, DueRevokedHandler(db, quietLogger())(context.Background(),
		bus.Message{Subject: DueRevokedEventType, Envelope: flatEnv}), ErrNoRevokedPayload)
}

// ==================== the chain around the handler ====================

// TestDurableConsumerPathRevokesAndDeduplicates is §3.4's chain for the撤销 leg as one unit: the durable
// consumer底座 built, DueRevokedHandler attached to it, and the same (event_type, business_id) delivered
// twice -- one revocation, the second delivery acked as a duplicate, and no dead-letter row for either.
func TestDurableConsumerPathRevokesAndDeduplicates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code:          registry.HomeosCode,
		StreamName:    "HC_FINANCE",
		EventType:     DueRevokedEventType,
		FilterSubject: DueRevokedEventType,
	})
	require.NoError(t, consumer.Start(ctx, DueRevokedHandler(db, quietLogger())))
	require.Len(t, fake.subscriptions, 1)
	sub := fake.subscriptions[0]
	assert.Equal(t, DueRevokedEventType, sub.filter, "撤销腿有它自己的 filter subject")
	assert.Equal(t, "homeos-finance.due.revoked", sub.consumer,
		"底座 builds {code}-{event_type}; pump.durableName is what makes it JetStream-legal")

	raw, err := json.Marshal(revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "completed"))
	require.NoError(t, err)

	first := &testMsg{data: raw, subject: DueRevokedEventType}
	sub.handler(first)
	assert.Equal(t, 1, first.acks, "撤销成功由 consumer 侧 ack")
	assert.Zero(t, first.naks)
	assert.Equal(t, int64(1), dedupeCount(t, db), "一次撤销 = 一条去重行")

	afterFirst := readRegistration(t, db, live.ID)
	require.NotNil(t, afterFirst.DeletedAt, "撤销是这次真实投递写上的")

	second := &testMsg{data: raw, subject: DueRevokedEventType, redeliver: true}
	sub.handler(second)
	assert.Equal(t, 1, second.acks, "§3.4「已存在即 ack 返回」")
	assert.Zero(t, second.naks)
	assert.Equal(t, int64(1), dedupeCount(t, db), "重复投递不再插去重行")
	assert.Empty(t, deadLetters(t, db), "重复投递不是失败，不该有死信行")
	assert.Empty(t, fake.published, "没有失败，就不该有 dl.* 发布")
	assert.Equal(t, afterFirst, readRegistration(t, db, live.ID), "去重命中的投递不得重跑撤销")

	// The other live registration of the same object: 撤销后重新注册进新的一行（0008:49-54 的部分索引），
	// 而历史撤销行留在表里 -- the end state 首页 B 区 reads.
	require.NoError(t, DueRegisteredHandler(db)(ctx, dueMessage(
		dueEnvelope(fixtureSourceID+":2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z", "11 月房贷"))))
	assert.Equal(t, int64(2), countRows(t, db, "1 = 1"), "撤销行留在历史里")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NULL"), "重新排期进的是新的一行")
}

// deadLetterStatus is the part of a parked row wiring_test.go's deadLetterRow leaves out: family_id (the
// column 管理台「按服务查看」时 grouping 用的) and resolved_at (人工重放的凭证就是它还是 NULL)。
type deadLetterStatus struct {
	FamilyID   *string
	ResolvedAt *time.Time
}

func readDeadLetterStatus(t *testing.T, db *gorm.DB, id int64) deadLetterStatus {
	t.Helper()

	var st deadLetterStatus
	require.NoError(t, db.Table("homeos_dead_letter").
		Select("family_id", "resolved_at").Where("id = ?", id).First(&st).Error,
		"死信行要能按 id 读回：人工重放走的正是这一条")
	return st
}

// assertRevokedInsideRetryBudget pins one delivery of the撤销腿 that is still inside §3.4's budget: Nak'd,
// nothing parked, claim given back, and the注册表 exactly as it was.
//
// It is this file's sibling of wiring_test.go's assertInsideRetryBudget, and deliberately NOT that helper:
// its last line asserts `countRows(t, db, "1 = 1") == 0`, i.e.「注册表为空」. That is right for the
// registered leg (whose every failure leaves nothing behind) and wrong here, because every撤销腿 case starts
// from registerOneLiveRow's ONE live registration -- reusing it would raise a false alarm on a healthy
// fixture. So the expected注册行数 is a parameter (wantRows), and the Nak count is stated cumulatively
// (naksSoFar -- the total over the deliveries walked so far) because the budget §3.4 spends is the event's,
// not one delivery's: on rung N of the ladder the event has been Nak'd exactly N times.
func assertRevokedInsideRetryBudget(t *testing.T, db *gorm.DB, fake *fakeJS, msg *testMsg, attempt uint64,
	liveID string, wantRows, naksSoFar int) {
	t.Helper()

	assert.Equal(t, 1, msg.naks, "第 %d 次投递仍在重试预算内，必须 Nak 重投，不能 ack 掉事件", attempt)
	assert.Equal(t, int(attempt), naksSoFar,
		"走到第 %d 投，累计 Nak 次数就该是 %d —— 预算是按事件算的，不是按投递算的", attempt, attempt)
	assert.Zero(t, msg.acks, "第 %d 次投递不许 ack", attempt)
	assert.Zero(t, msg.terms, "第 %d 次投递预算未用完，不许 Term（那等于取消 §3.4 的至多重试 3 次）", attempt)
	assert.Empty(t, fake.published, "第 %d 次投递未超限，不该有 dl.* 发布（判据「超限失败 ->」）", attempt)
	assert.Empty(t, deadLetters(t, db), "第 %d 次投递未超限，{code}_dead_letter 就该是 0 行", attempt)
	assert.Equal(t, int64(0), dedupeCount(t, db),
		"第 %d 次投递失败后必须释放 (event_type, business_id) claim，否则重投被去重层拦下、至多重试 3 次是空的", attempt)

	assert.Equal(t, int64(wantRows), countRows(t, db, "1 = 1"), "第 %d 次投递不许新增或删掉注册行", attempt)
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NOT NULL"),
		"第 %d 次投递被契约拒了，整张表里 0 条撤销：宁可进死信也不误删", attempt)
	assert.Nil(t, readRegistration(t, db, liveID).DeletedAt,
		"第 %d 次投递之后那条活注册的 deleted_at 必须仍是 NULL", attempt)
}

// TestDurableConsumerPathDeadLettersRejectedRevocation: a revoked event the handler refuses (a reason the
// frozen enum does not allow) is retried §3.4's full ladder before anything is parked --
// 「max_deliver=4（对应 10.4 的至多重试 3 次）」 makes deliveries 1-3 Naked with no dead letter, and
// 「超限失败 -> publish 到 dl.{consumerCode}.{event_type} + 写 {code}_dead_letter」 is only the fourth
// delivery's ending. The alternative is a paid bill whose到期行 silently stays in 首页 B 区.
//
// The two branches are mutually exclusive by construction (consumer.go:244-258: the budget branch Nak's and
// returns; the dead letter is written on the way out of the 超限 branch), so this case walks the ladder
// rather than asserting both halves off one delivery -- the same shape as wiring_test.go's
// TestDurableConsumerNaksAndWritesDeadLetterOnBadKind, whose rack this is. And because a failed delivery
// gives its claim back, each of the four really re-runs the handler: the whole point of the retry is that
// the撤销 gets four chances, not one.
func TestDurableConsumerPathDeadLettersRejectedRevocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	newDueTable(t, db)
	newDedupeTable(t, db)
	newDeadLetterTable(t, db)

	ctx := context.Background()
	live := registerOneLiveRow(t, db, fixtureDueAt, "10 月房贷")

	fake := &fakeJS{}
	consumer := bus.NewDurableConsumer(db, fake, bus.ConsumerConfig{
		Code:          registry.HomeosCode,
		StreamName:    "HC_FINANCE",
		EventType:     DueRevokedEventType,
		FilterSubject: DueRevokedEventType,
		// MaxDeliver left at the底座's default 4 == §3.4 的 max_deliver=4，所以这里走的阶梯就是
		// 真消费者对着真 stream 会走的那一条。
	})
	attempts := 0
	require.NoError(t, consumer.Start(ctx,
		countedHandler(DueRevokedHandler(db, quietLogger()), &attempts)))
	require.Len(t, fake.subscriptions, 1)
	handler := fake.subscriptions[0].handler

	env := revokedEnvelope(fixtureSourceID+":"+fixtureDueAt, revokedClock, "refunded")
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	// 预算内的三次投递：每次 Nak 一次、什么都不 park、claim 当场交还、注册行一动不动。
	naks := 0
	for attempt := uint64(1); attempt < 4; attempt++ {
		msg := deliverFailing(handler, DueRevokedEventType, raw, attempt)
		naks += msg.naks
		assertRevokedInsideRetryBudget(t, db, fake, msg, attempt, live.ID, 1, naks)
		assert.Equal(t, int(attempt), attempts,
			"第 %d 次投递要真的再跑一次 handler，否则「至多重试 3 次」只是三次把事件交给去重层", attempt)
	}

	// 超限的那一次：死信 + Term，各一次；不再 Nak。
	fourth := deliverFailing(handler, DueRevokedEventType, raw, 4)
	assert.Zero(t, fourth.naks, "预算已花完，不该再向 stream 要一次它已经拒过的投递")
	assert.Zero(t, fourth.acks, "被拒的撤销不能当成功 ack 掉")
	assert.Equal(t, 1, fourth.terms, "超限的收尾是 Term：这一投递到此为止，重放走死信那一行")
	assert.Equal(t, 3, naks, "累计 Nak 三次 == §3.4 的「至多重试 3 次」")
	assert.Equal(t, 4, attempts, "四次投递 = handler 跑了四次，一次也没被去重层吞掉")

	assert.Equal(t, []string{"dl.homeos." + DueRevokedEventType}, fake.published,
		"dl.* 只在超限时 publish 一次（每投一次发一次，人工重放就会把同一事件入队四次）")

	letters := deadLetters(t, db)
	require.Len(t, letters, 1, "超限失败的消息要留在重放表里，而且只留一行")
	assert.Equal(t, registry.HomeosCode, letters[0].ConsumerCode,
		"§3.4「管理台按服务查看」走的正是 consumer_code")
	assert.Equal(t, DueRevokedEventType, letters[0].EventType)
	assert.Contains(t, letters[0].LastError, "reason",
		"last_error 要指向真实的拒绝原因（契约 enum(completed|deleted|expired)），不是别处")
	assert.Contains(t, letters[0].LastError, "refunded", "还要看得出被拒的是哪个值")
	assert.JSONEq(t, string(raw), letters[0].Envelope, "§3.4「重放即原样重新入队」-> 存原文")

	st := readDeadLetterStatus(t, db, letters[0].ID)
	require.NotNil(t, st.FamilyID, "死信行要带上事件自己的 family_id，管理台才分得出是哪一家的")
	assert.Equal(t, fixtureFamilyID, *st.FamilyID)
	assert.Nil(t, st.ResolvedAt, "人工重放之前 resolved_at 必须是 NULL")

	// 本用例的语义底线：事件进死信等人工重放，而不是被 ack 掉当没事 -- 阶梯走完，那条活注册一行都没被误删。
	after := readRegistration(t, db, live.ID)
	assert.Nil(t, after.DeletedAt, "被拒的撤销从头到尾没有改动注册")
	assert.Equal(t, live, after, "连 deleted_at 之外的列也不该被这次失败动到")
	assert.Equal(t, int64(1), countRows(t, db, "1 = 1"), "注册表仍是夹具那一条")
	assert.Equal(t, int64(1), countRows(t, db, "deleted_at IS NULL"), "它仍然是活的，等人工重放")
	assert.Equal(t, int64(0), countRows(t, db, "deleted_at IS NOT NULL"))

	// 超限收尾同样要把 claim 交回去：死信行的「重放即原样重新入队，走同一幂等键」走的是同一个
	// (event_type, business_id)，留着首投失败那一行的话人工重放也会被去重层当成重复而 ack 掉。
	assert.Equal(t, int64(0), dedupeCount(t, db))
}
