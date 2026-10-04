// identity_events_test.go proves the svc-homeos publisher side of the frozen homeos.* contract: each
// mutation in repo/identity.go appends the exact outbox row contracts/events/homeos.yaml declares --
// right subject, right business_id form, right payload_schema keys. It reads homeos_outbox straight
// back (0002's seven columns) rather than through JetStream, because the outbox row is the deliverable:
// the 投递器 (packages/bus) is already covered by bus_test.go and moves whatever subject/envelope lands
// here verbatim (§3.3「INSERT homeos_outbox(subject, envelope, status=pending)」).
package repo

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
)

// newEventFixture is the applied identity column set (mirrors the sqlite fixtures used across this
// service's tests) plus homeos_outbox, on which every assertion below reads. It seeds one account and
// no family, so CreateFamily runs the real建家 transaction end to end.
func newEventFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	stmts := []string{
		`CREATE TABLE homeos_users (
			id TEXT PRIMARY KEY, phone TEXT UNIQUE NOT NULL, name TEXT, avatar TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE homeos_families (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, owner_id TEXT NOT NULL,
			timezone TEXT NOT NULL, currency TEXT NOT NULL, avatar TEXT,
			pver INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE homeos_members (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, user_id TEXT, role TEXT NOT NULL,
			relation TEXT, name TEXT, avatar TEXT, guardian_id TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			deleted_at DATETIME, deleted_by TEXT, UNIQUE(family_id, user_id))`,
		`CREATE TABLE homeos_invitations (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, code TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL, inviter_id TEXT NOT NULL, invitee_phone TEXT,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','expired')),
			expires_at DATETIME NOT NULL, revoked_at DATETIME, revoked_by TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE homeos_family_module (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, code TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT 0, enabled_at DATETIME, enabled_by TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			deleted_at DATETIME, deleted_by TEXT)`,
		`CREATE TABLE homeos_dynamic (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, code TEXT NOT NULL,
			actor_member_id TEXT, actor_name TEXT NOT NULL, action TEXT NOT NULL,
			summary TEXT NOT NULL, entity TEXT NOT NULL, entity_id TEXT, on_behalf_of TEXT,
			at DATETIME NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			deleted_at DATETIME)`,
		`CREATE TABLE homeos_audit_log (
			id TEXT PRIMARY KEY, family_id TEXT, target_family_id TEXT, code TEXT,
			event TEXT NOT NULL CHECK (event IN ('login','permission_change','cross_family_attempt',
				'export','delete','l3_read','rule_toggle','dead_letter_replay','on_behalf_write')),
			actor_member_id TEXT, actor_user_id TEXT, action TEXT, entity TEXT, entity_id TEXT,
			result TEXT NOT NULL DEFAULT 'allowed' CHECK (result IN ('allowed','denied')),
			reason TEXT, ip TEXT, user_agent TEXT,
			created_at DATETIME NOT NULL, occurred_at DATETIME NOT NULL)`,
		// 0002's homeos_outbox, seven columns, the shape bus.OutboxMessage writes.
		`CREATE TABLE homeos_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT, family_id TEXT, subject TEXT NOT NULL,
			envelope TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending','sent')),
			attempts INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}

	accountID := "11111111-1111-4111-8111-111111111111"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_users (id, phone, name, created_at, updated_at)
		 VALUES (?, '13800000001', '小明', datetime('now'), datetime('now'))`, accountID).Error)
	return db, accountID
}

// outboxEnvelope is one homeos_outbox row decoded to its envelope, so the assertions below speak the
// contract's vocabulary (subject == event_type, business_id, payload keys) instead of raw JSON.
type outboxEnvelope struct {
	Subject   string `gorm:"column:subject"`
	FamilyID  string `gorm:"column:family_id"`
	EventType string `json:"event_type"`
	BizID     string `json:"business_id"`
	Payload   map[string]any
}

// envelopesBySubject reads every outbox row grouped by subject, decoding each envelope. The payload is
// pulled out of the envelope's own "payload" object (bus.Envelope's json tag), not the flat message.
func envelopesBySubject(t *testing.T, db *gorm.DB) map[string][]outboxEnvelope {
	t.Helper()

	var rows []struct {
		Subject  string `gorm:"column:subject"`
		FamilyID string `gorm:"column:family_id"`
		Envelope string `gorm:"column:envelope"`
	}
	require.NoError(t, db.Table("homeos_outbox").
		Select("subject", "family_id", "envelope").Order("id").Find(&rows).Error)

	out := map[string][]outboxEnvelope{}
	for _, r := range rows {
		var env bus.Envelope
		require.NoError(t, json.Unmarshal([]byte(r.Envelope), &env))
		out[r.Subject] = append(out[r.Subject], outboxEnvelope{
			Subject: r.Subject, FamilyID: r.FamilyID,
			EventType: env.EventType, BizID: env.BusinessID, Payload: env.Payload,
		})
	}
	return out
}

func requireOne(t *testing.T, by map[string][]outboxEnvelope, subject string) outboxEnvelope {
	t.Helper()
	rows := by[subject]
	require.Len(t, rows, 1, "subject %q 必须恰好一条 outbox 行", subject)
	assert.Equal(t, subject, rows[0].EventType, "信封 event_type 必须与 subject 同名（§3.1「subject 命名即事件名」）")
	return rows[0]
}

// TestCreateFamilyPublishesFamilyAndMemberCreated: the建家 transaction emits homeos.family.created and a
// homeos.member.created for the owner row, each with the contract's exact payload keys.
func TestCreateFamilyPublishesFamilyAndMemberCreated(t *testing.T) {
	db, accountID := newEventFixture(t)
	ctx := context.Background()

	familyID, memberID, err := CreateFamily(ctx, db, Actor{AccountID: accountID, MemberID: ""},
		"温暖小家", "Asia/Shanghai", "CNY", "", nil)
	require.NoError(t, err)
	require.NotEmpty(t, familyID)

	by := envelopesBySubject(t, db)

	fam := requireOne(t, by, "homeos.family.created")
	assert.Equal(t, familyID, fam.BizID, "family.created 的 business_id 是 {family_id}")
	assert.Equal(t, familyID, fam.FamilyID)
	assertPayloadKeys(t, fam, "family_id", "name", "timezone", "currency")
	assert.Equal(t, "温暖小家", fam.Payload["name"])
	assert.Equal(t, "Asia/Shanghai", fam.Payload["timezone"])
	assert.Equal(t, "CNY", fam.Payload["currency"])

	mem := requireOne(t, by, "homeos.member.created")
	assert.Equal(t, memberID, mem.BizID, "member.created 的 business_id 是 {member_id}")
	assertPayloadKeys(t, mem, "member_id", "family_id", "name", "relation", "role", "on_behalf_of")
	assert.Equal(t, memberID, mem.Payload["member_id"])
	assert.Equal(t, familyID, mem.Payload["family_id"])
	assert.Equal(t, "owner", mem.Payload["role"])
	assert.Nil(t, mem.Payload["on_behalf_of"], "业主行的 on_behalf_of 是 JSON null，不是空串")

	// 同一事务里落的那条审计也自带契约事件（appendAuditTx 是 homeos_audit_log 的唯一写方）。
	audit := requireOne(t, by, "homeos.audit.recorded")
	assertPayloadKeys(t, audit, "audit_id", "family_id", "actor_id", "action", "resource", "outcome", "recorded_at")
	assert.Equal(t, "success", audit.Payload["outcome"])
}

// TestSetMemberRolePublishesMemberUpdated: a role change emits homeos.member.updated (updated_fields
// names the changed column) alongside the permission.updated it already emitted.
func TestSetMemberRolePublishesMemberUpdated(t *testing.T) {
	db, accountID := newEventFixture(t)
	ctx := context.Background()
	familyID, ownerMemberID, err := CreateFamily(ctx, db, Actor{AccountID: accountID}, "家", "", "", "", nil)
	require.NoError(t, err)

	// A second member to promote, created as a 无账号 ward row (user_id NULL avoids the
	// UNIQUE(family_id,user_id) the owner row already occupies).
	other := "99999999-9999-4999-8999-999999999999"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_members (id, family_id, user_id, role, name, created_at, updated_at)
		 VALUES (?, ?, NULL, 'member', '小王', datetime('now'), datetime('now'))`,
		other, familyID).Error)

	_, err = SetMemberRole(ctx, db, familyID, other, "owner", Actor{AccountID: accountID, MemberID: ownerMemberID})
	require.NoError(t, err)

	by := envelopesBySubject(t, db)
	upd := requireOne(t, by, "homeos.member.updated")
	assert.Equal(t, other, upd.BizID, "member.updated 的 business_id 是 {member_id}")
	assertPayloadKeys(t, upd, "member_id", "family_id", "updated_fields")
	fields, ok := upd.Payload["updated_fields"].(map[string]any)
	require.True(t, ok, "updated_fields 必须是一个 object")
	assert.Equal(t, "owner", fields["role"])

	// The permission signal rides alongside; both are emitted from the one transaction.
	requireOne(t, by, "homeos.permission.updated")
}

// TestRemoveMemberPublishesMemberDeleted: soft-removing a member emits homeos.member.deleted with
// member_id / family_id / deleted_by.
func TestRemoveMemberPublishesMemberDeleted(t *testing.T) {
	db, accountID := newEventFixture(t)
	ctx := context.Background()
	familyID, ownerMemberID, err := CreateFamily(ctx, db, Actor{AccountID: accountID}, "家", "", "", "", nil)
	require.NoError(t, err)

	other := "88888888-8888-4888-8888-888888888888"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_members (id, family_id, user_id, role, created_at, updated_at)
		 VALUES (?, ?, ?, 'member', datetime('now'), datetime('now'))`,
		other, familyID, "77777777-7777-4777-8777-777777777777").Error)

	_, err = RemoveMember(ctx, db, familyID, other, Actor{AccountID: accountID, MemberID: ownerMemberID})
	require.NoError(t, err)

	by := envelopesBySubject(t, db)
	del := requireOne(t, by, "homeos.member.deleted")
	assert.Equal(t, other, del.BizID)
	assertPayloadKeys(t, del, "member_id", "family_id", "deleted_by")
	assert.Equal(t, other, del.Payload["member_id"])
	assert.Equal(t, familyID, del.Payload["family_id"])
	assert.Equal(t, ownerMemberID, del.Payload["deleted_by"])
}

// TestAcceptInvitationPublishesMemberCreated: a fresh account joining by invite emits member.created for
// the new member row.
func TestAcceptInvitationPublishesMemberCreated(t *testing.T) {
	db, accountID := newEventFixture(t)
	ctx := context.Background()
	familyID, _, err := CreateFamily(ctx, db, Actor{AccountID: accountID}, "家", "", "", "", nil)
	require.NoError(t, err)

	// A second account, invited as member.
	invitee := "66666666-6666-4666-8666-666666666666"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_users (id, phone, created_at, updated_at)
		 VALUES (?, '13800000002', datetime('now'), datetime('now'))`, invitee).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_invitations (id, family_id, code, role, inviter_id, status, expires_at, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, 'INV-CODE', 'member', ?, 'pending',
		         datetime('now', '+7 day'), datetime('now'), datetime('now'))`,
		familyID, accountID).Error)

	gotFamily, _, err := AcceptInvitation(ctx, db, "INV-CODE", invitee, Actor{})
	require.NoError(t, err)
	assert.Equal(t, familyID, gotFamily)

	by := envelopesBySubject(t, db)
	// Two member.created rows exist for this family: the owner row from建家 and the invitee's. Pick the
	// invitee by role so the assertion targets AcceptInvitation's own emit.
	rows := by["homeos.member.created"]
	require.NotEmpty(t, rows, "接受邀请必须落一条 homeos.member.created")
	mem := rows[len(rows)-1] // AcceptInvitation runs last, so its row is the newest.
	assert.Equal(t, "member", mem.Payload["role"])
	assertPayloadKeys(t, mem, "member_id", "family_id", "name", "relation", "role", "on_behalf_of")
	assert.Equal(t, familyID, mem.Payload["family_id"])
	assert.Nil(t, mem.Payload["on_behalf_of"])
}

// TestPublishersWithoutMutationEndpoint cover the three contracted events whose business mutation is not
// built in svc-homeos this round: the seam functions still write the exact envelope the contract names.
func TestPublishersWithoutMutationEndpoint(t *testing.T) {
	db, _ := newEventFixture(t)
	ctx := context.Background()
	familyID := "22222222-2222-4222-8222-222222222222"
	now := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)

	require.NoError(t, PublishFamilyUpdated(ctx, db, familyID, map[string]any{"name": "新名"}))
	require.NoError(t, PublishFamilyDissolved(ctx, db, familyID, "owner-member", now))
	require.NoError(t, PublishTodoCompleted(ctx, db, familyID, "todo-1", "finance", "src-1", "member-1", now))

	by := envelopesBySubject(t, db)

	fu := requireOne(t, by, "homeos.family.updated")
	assert.Equal(t, familyID, fu.BizID)
	assertPayloadKeys(t, fu, "family_id", "updated_fields")
	assert.Equal(t, "新名", fu.Payload["updated_fields"].(map[string]any)["name"])

	fd := requireOne(t, by, "homeos.family.dissolved")
	assert.Equal(t, familyID, fd.BizID)
	assertPayloadKeys(t, fd, "family_id", "dissolved_at", "dissolved_by")

	tc := requireOne(t, by, "homeos.todo.completed")
	assert.Equal(t, "todo-1", tc.BizID, "todo.completed 的 business_id 是 {todo_id}")
	assertPayloadKeys(t, tc, "todo_id", "source_system", "source_id", "completed_at", "completed_by")
	// The contract declares no family_id in the body, but the outbox ROW carries one (§10.3 指标最小集).
	assert.Equal(t, familyID, tc.FamilyID, "todo.completed 的 outbox 行仍带 family_id 列")
}

// assertPayloadKeys fails when a payload object is missing any declared contract key.
func assertPayloadKeys(t *testing.T, env outboxEnvelope, keys ...string) {
	t.Helper()
	require.NotNil(t, env.Payload, "信封必须有 payload 对象")
	for _, k := range keys {
		_, ok := env.Payload[k]
		assert.True(t, ok, "payload 缺少契约字段 %q（%s）；实际键：%v", k, env.Subject, mapKeys(env.Payload))
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
