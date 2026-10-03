// home_summary_test.go extends 定版 ⑯'s 五处同源 identity to its third consumer and pins the refusals
// this endpoint exists to make.
//
// faces_test.go already asserts that GET /family/modules filtered on enabled ∧ visible equals
// mountedFaces(composeFaceEntries(claims)). That equality was stated against the SLICE home/summary
// would build, because the handler did not exist yet. Now it does, so the same identity is asserted
// across three answers about one household and one session: the 开通页's list, the 首页's faces[], and
// the composition both read. PRD 17.8's wording is 「同一份服务端合成，客户端只拿自己可见的那一份」 and
// its 定版 ⑯ row covers 首页矩阵 + 面目录 + 其余消费位 -- a third consumer that grows its own filter is
// exactly what this triangle catches. A 首页 that filtered on enabled alone (the family-level reading,
// ignoring 15.3) would serve a ward a cell the 开通页 says is invisible to them, i.e. the
// 「不得由客户端自行按角色过滤」 discipline moved server-side and still broken; the ward/guest legs below
// are where that difference is visible.
//
// The other tests cover what the contract's own lines demand and no other file asserts: period's 400
// with no silent fallback (PRD 4.5.3 定版 ⑬, 18.2#9 「非法编码返回 400 不回落」), the onboarding-scoped
// token being refused (svcauth's allowlist, PRD 15.2 「判定以 family_id 为界」), and the four zones
// carrying server-decided numbers (17.1 第 2/3 条 for `unread` vs 待处理, 17.2 for B 区 count/items and
// D 区 前 20 条, repo.HomeSummaryDueToday's Answerable for 今日到期).
//
// A summary fixture needs this service's own applied tables, not just 0004: the handler reads
// homeos_proj_finance (0004) for the cell sentence, homeos_dynamic (0007) for D 区 and
// homeos_notification (0007) for `unread`, and an unread read that fails is a 500 rather than an empty
// red dot (17.2 四区一个请求：either it answers the 首页 or it says it cannot). A fixture that omitted
// one of them would be asserting a 500 the deployment cannot produce, because the migration sequence
// 0001-0009 has all three applied. homeos_due_registration (0008) is the one table the B 区 tests add
// selectively -- see TestHomeSummaryDueTodayOmitsCountWhenProjectionCannotAnswer for what its absence
// means now that 0008 has landed.
//
// BZ-1 adds the two B 区 read-side judgments and, with them, a rule about how the fixtures are built:
// the rows B 区 reads are written by this service's own consumer (registerDue ->
// consumer.DueRegisteredHandler and revokeDue -> consumer.DueRevokedHandler, the two ends of the 到期
// chain), so a red test below says something about the read path rather than about a shape this file
// invented. The one hand-written row is seedBaseDue's 底座-sourced registration, and its doc says why
// (P1 has no producer of one). TestHomeSummaryDueTodayDropsRevokedRegistrations pins the 软删 predicate
// -- which is also 0008:58-60's index predicate -- and
// TestHomeSummaryDueTodayIsFilteredToTheMemberMountedFaces pins PRD 17.8's 「聚合…一律按本家庭的已挂载面
// 集合过滤」 across three legs (挂载 / 角色不可见 / 停用后重新启用), the set being the same mountedFaces
// output C 区 is drawn from.
package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/consumer"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

const summaryPath = "/api/homeos/home/summary"

// ==================== fixtures ====================

// newSummaryRouter mounts the two face-set READ consumers plus the two public auth routes the
// onboarding-refusal test needs to obtain a real family-less token, each in the shape
// cmd/svc-homeos/main.go uses (same prefix, same middleware, same (c, services, mw) call). No PUT:
// the 同源 identity is a read-side property and must not depend on the write path.
func newSummaryRouter(t *testing.T, s *Services) *gin.Engine {
	t.Helper()

	mw, err := s.Middleware()
	require.NoError(t, err)
	d, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group(d.RoutePrefix)
	group.POST("/auth/sms-code", func(c *gin.Context) { SendSMSCode(c, s) })
	group.POST("/auth/login", func(c *gin.Context) { Login(c, s) })

	protected := group.Group("", mw.Handler())
	protected.GET("/home/summary", func(c *gin.Context) { GetHomeSummary(c, s, mw) })
	protected.GET("/family/modules", func(c *gin.Context) { GetFamilyModules(c, s, mw) })
	return r
}

// addFinanceProjectionTable is 0004's column set (homeos.homeos_proj_finance). bucket_month is a
// `date` holding the month's first day and updated_at is the projection's write time, which is what
// faces[].as_of reports (12.3 「截至 HH:MM」).
func addFinanceProjectionTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`CREATE TABLE homeos_proj_finance (
			family_id TEXT NOT NULL, bucket_month DATE NOT NULL,
			income_cents INTEGER NOT NULL DEFAULT 0, expense_cents INTEGER NOT NULL DEFAULT 0,
			budget_remaining_cents INTEGER, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`).Error)
}

// addNotificationTable is 0007's homeos_notification: `unread`'s only caliber is read_at IS NULL
// aggregated by member_id (PRD 17.1 第 2 条), so the test that asserts 「未读是消息数、不是动态数」 needs
// the table the number is counted from.
func addNotificationTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`CREATE TABLE homeos_notification (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, member_id TEXT NOT NULL,
			type TEXT NOT NULL CHECK (type IN ('budget_alert','system','reminder')),
			content TEXT NOT NULL, channel TEXT NOT NULL DEFAULT 'inapp', dedupe_key TEXT,
			read_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`).Error)
}

// addSummaryReadTables is what a real P1 checkout has for this endpoint to read: 0004's monthly finance
// projection plus 0007's notification table. Named as one call because every summary request touches
// both, and a fixture missing either asserts a deployment-time 500 instead of a behaviour.
func addSummaryReadTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	addFinanceProjectionTable(t, db)
	addNotificationTable(t, db)
}

// addDueRegistrationTable is 0008's homeos_due_registration -- the table repo.HomeSummaryDueToday
// treats as the switch between 「数过了」 and 「无从数起」. Both of 0008's indexes are reproduced, the
// partial unique upsert anchor (0008:52-54) and the partial 到期索引 (0008:58-60), because a fixture
// that omitted the first would let two live rows for one object pass unseen and the second is the
// predicate the read side is now required to match (deleted_at IS NULL).
func addDueRegistrationTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`CREATE TABLE homeos_due_registration (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, source_system TEXT NOT NULL,
			source_id TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('bill','budget','goal','repayment')),
			title TEXT NOT NULL, due_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME, deleted_by TEXT)`).Error)
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX homeos_due_registration_source_uidx
		 ON homeos_due_registration (source_system, source_id, kind)
		 WHERE deleted_at IS NULL`).Error)
	require.NoError(t, db.Exec(
		`CREATE INDEX homeos_due_registration_family_due_idx
		 ON homeos_due_registration (family_id, due_at ASC)
		 WHERE deleted_at IS NULL`).Error)
}

// registerDue writes one due registration through this service's OWN consumer path --
// consumer.DueRegisteredHandler is production's writer of homeos_due_registration (§3.6「消费
// finance.due.registered 并按 (source_system, source_id, kind) upsert」, bus_runtime.go subscribes it),
// so the rows B 区 reads are rows the shipped write path produced rather than rows this file
// hand-inserted. The payload's field set is contracts/events/finance.yaml's payload_schema, verbatim.
// Returns the registration row id AND the source_id the revoke half of the chain keys on.
func registerDue(t *testing.T, db *gorm.DB, familyID, sourceSystem, title string, dueAt time.Time) (string, string) {
	t.Helper()

	sourceID := uuid.NewString()
	require.NoError(t, consumer.DueRegisteredHandler(db)(t.Context(), bus.Message{
		Subject: consumer.DueRegisteredEventType,
		Envelope: bus.Envelope{
			EventType:  consumer.DueRegisteredEventType,
			BusinessID: sourceID + ":" + dueAt.UTC().Format(time.RFC3339),
			FamilyID:   familyID,
			Version:    "1.0",
			Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
			Payload: map[string]any{
				"source_system": sourceSystem,
				"source_id":     sourceID,
				"family_id":     familyID,
				"due_at":        dueAt.UTC().Format(time.RFC3339),
				"kind":          "bill",
				"title":         title,
			},
		},
	}))

	var id string
	require.NoError(t, db.Raw(`SELECT id FROM homeos_due_registration WHERE source_id = ?`, sourceID).Scan(&id).Error)
	require.NotEmpty(t, id, "消费路径写完就查不回，B 区也就无从读起")
	return id, sourceID
}

// revokeDue revokes that registration through the other half of the same chain:
// consumer.DueRevokedHandler, i.e. the effect 0008:43-45 call 「本表唯一的删除路径」 (finance.due.revoked
// -> 打 deleted_at，不删行). Nothing in TestHomeSummaryDueTodayDropsRevokedRegistrations is a hand-stamped
// column: 注册行来自 registered 消费路径，deleted_at 来自 revoked 消费路径，被测的是读侧那条谓词。
// The payload is contracts/events/finance.yaml:93-105's revoked payload_schema (reason 取
// enum(completed|deleted|expired) 的 completed = 账单结清)。The discard logger only silences that
// handler's own info line; the 撤销效果 itself is asserted on the 首屏 response.
//
// dueAt is the 注册时那一期 the caller passed to registerDue, NOT the撤销时刻: the contract fixes
// business_id at 「"{source_id}:{due_at}"」 (finance.yaml:95), 与注册事件写下的同一段文本，而
// due_revoked_handler.go 的撤销锚点是 (source_system, source_id, family_id, due_at) WHERE deleted_at
// IS NULL —— due_at 就是从这段 business_id 里切出来的。拿 revoked_at 顶替它，锚点指的是一个从未注册过
// 的期，handler 会 0 行返回（契约形态错了也不报，因为「撤销那一期没注册过」本来就是合法结局），B 区于是
// 继续显示那条已结清的账单。revoked_at 本身仍然是「结清时刻」= now()，那是 deleted_at 的值。
func revokeDue(t *testing.T, db *gorm.DB, familyID, sourceSystem, sourceID string, dueAt time.Time) {
	t.Helper()

	revokedAt := time.Now().UTC().Format(time.RFC3339)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.NoError(t, consumer.DueRevokedHandler(db, log)(t.Context(), bus.Message{
		Subject: consumer.DueRevokedEventType,
		Envelope: bus.Envelope{
			EventType:  consumer.DueRevokedEventType,
			BusinessID: sourceID + ":" + dueAt.UTC().Format(time.RFC3339),
			FamilyID:   familyID,
			Version:    "1.0",
			Timestamp:  revokedAt,
			Payload: map[string]any{
				"source_system": sourceSystem,
				"source_id":     sourceID,
				"family_id":     familyID,
				"revoked_at":    revokedAt,
				"reason":        "completed",
			},
		},
	}))
}

// seedBaseDue writes a 底座-sourced registration directly. It is a fixture rather than a code path
// because P1 has no producer of one: the only due event published is finance's (16.4's 注册契约 lists
// the 面 registrations; 底座's own 待办 arrive with TIME-1's homeos_todos, which has no due-registration
// writer yet -- reported). The read side's face filter is what is under test, and it reads the
// source_system column regardless of who wrote it. kind is 'goal' because 0008:36 pins the four values
// finance's frozen enum declares -- a 底座 kind outside that CHECK cannot be expressed in this table
// today, which is the same 上报项.
func seedBaseDue(t *testing.T, db *gorm.DB, familyID, title string, dueAt time.Time) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_due_registration (id, family_id, source_system, source_id, kind, title, due_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'goal', ?, ?, datetime('now'), datetime('now'))`,
		id, familyID, registry.HomeosCode, uuid.NewString(), title, dueAt).Error)
	return id
}

// setFaceEnabled flips homeos_family_module.enabled, the column repo.SetFamilyModuleEnabled writes on
// 停用/启用. The PUT path cannot express 「停用本家庭唯一一个面」 in P1 -- 17.8 第 4 条「不允许出现 0 面
// 家庭」 makes it a 409 (faces_test.go ④ pins that refusal) and P1 has exactly one born face -- so the
// 停用 state this test needs is seeded here while the read under test stays the real one:
// composeFaceEntries reads this very column into FaceEntry.Enabled.
func setFaceEnabled(t *testing.T, db *gorm.DB, familyID, code string, enabled bool) {
	t.Helper()
	require.NoError(t, db.Exec(
		`UPDATE homeos_family_module SET enabled = ? WHERE family_id = ? AND code = ?`, enabled, familyID, code).Error)
}

func insertNotification(t *testing.T, db *gorm.DB, familyID, memberID, typ string, read bool) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_notification (id, family_id, member_id, type, content, channel, created_at)
		 VALUES (?, ?, ?, ?, ?, 'inapp', datetime('now'))`,
		uuid.NewString(), familyID, memberID, typ, typ+" 内容").Error)
	if read {
		require.NoError(t, db.Exec(
			`UPDATE homeos_notification SET read_at = datetime('now')
			 WHERE family_id = ? AND member_id = ? AND type = ?`, familyID, memberID, typ).Error)
	}
}

// insertBucket writes one 0004 monthly bucket. A nil budgetRemaining is 定版 ⑬'s 「未设该周期预算」
// state (0004: NULL, never 0), which is what makes the 季/年 档 answer differ from the 月 档.
func insertBucket(t *testing.T, db *gorm.DB, familyID string, month time.Time, income, expense int64, budgetRemaining *int64) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_proj_finance (family_id, bucket_month, income_cents, expense_cents, budget_remaining_cents, updated_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'))`,
		familyID, month, income, expense, budgetRemaining).Error)
}

// seedDynamic writes one feed row through the repo's own writer -- the seam the bus subscriber uses,
// since 17.2 says dynamics are 仅由事件总线写入 and a handler has no business inventing one.
func seedDynamic(t *testing.T, db *gorm.DB, familyID, code, actor, action, summary string, at time.Time) {
	t.Helper()
	require.NoError(t, repo.AppendDynamic(t.Context(), db, &model.HomeosDynamic{
		FamilyID:  familyID,
		Code:      code,
		ActorName: actor,
		Action:    action,
		Summary:   summary,
		Entity:    "transaction",
		At:        at,
	}))
}

// setFamilyTimezoneUTC pins the session family's timezone to UTC in the tests whose windows are
// compared against stored timestamps. The reason is the test engine, not the rule: the SQLite driver
// binds a time.Time as TEXT including its offset, so a +08:00 window compared against a +00:00 column
// value is a lexicographic compare and the month/day boundary arithmetic PRD 4.5.3 asks for stops
// being arithmetic. PostgreSQL (production) compares instants, which is why this is a fixture note
// rather than a handler concern -- and it is reported as a cross-engine caveat in the card report.
func setFamilyTimezoneUTC(t *testing.T, db *gorm.DB, familyID string) {
	t.Helper()
	require.NoError(t, db.Exec(`UPDATE homeos_families SET timezone = 'UTC' WHERE id = ?`, familyID).Error)
}

// getSummary is the one GET this card adds, with an optional raw query string appended. The query is
// spelled by the caller rather than built from a struct because the period tests are about the exact
// wire value ('2026-13', 'bogus'), and a helper that encoded it would hide it.
func getSummary(t *testing.T, r *gin.Engine, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := summaryPath
	if query != "" {
		path += "?" + query
	}
	return doJSON(t, r, http.MethodGet, path, token, nil)
}

// ==================== views for the 同源 identity ====================

// summaryFaceView is the identity one face cell has across all three consumers: code, name and the
// server-decided availability. faces[] adds a 状态句 on top and the contract gives it no icon key,
// so those are not part of the comparison (faceCellView in faces_test.go covers the icon leg).
type summaryFaceView struct {
	Code         string
	Name         string
	Availability string
}

// modulesMounted filters the 开通页's answer the way the contract's own consistency line says
// (nav §3.4 「faces[] 必须与 family/modules 中 enabled ∧ visible 的集合一致」).
func modulesMounted(items []FamilyModuleItem) []summaryFaceView {
	var out []summaryFaceView
	for _, m := range items {
		if m.Enabled && m.Visible {
			out = append(out, summaryFaceView{Code: m.Code, Name: m.Name, Availability: m.Availability})
		}
	}
	return out
}

func entriesMounted(entries []FaceEntry) []summaryFaceView {
	var out []summaryFaceView
	for _, e := range mountedFaces(entries) {
		out = append(out, summaryFaceView{Code: e.Code, Name: e.Name, Availability: e.Availability})
	}
	return out
}

func statusesViewed(faces []FaceStatus) []summaryFaceView {
	var out []summaryFaceView
	for _, f := range faces {
		out = append(out, summaryFaceView{Code: f.Code, Name: f.Name, Availability: f.Availability})
	}
	return out
}

func jsonKeys(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &keys), "响应必须是 JSON 对象：%s", string(raw))
	return keys
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func indexOfCode(codes []string, code string) int {
	for i, c := range codes {
		if c == code {
			return i
		}
	}
	return -1
}

// ==================== 五处同源：第三个消费位 ====================

// TestHomeSummaryFacesEqualMountedFacesAndFamilyModulesForEveryRole is this card's acceptance
// criterion: home/summary's faces[] must equal GET /family/modules filtered on enabled ∧ visible, and
// both must equal mountedFaces over the one composition -- for the owner and for three non-admin roles.
func TestHomeSummaryFacesEqualMountedFacesAndFamilyModulesForEveryRole(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	s := newServices(t, db)

	born := firstBornFace(t)
	unborn := firstUnbornFace(t)
	// One enabled row, so the enabled ∧ visible filter has a term to remove: the born face is mounted by
	// the family (17.8), and whether a role gets a cell is then decided by 15.3 alone.
	mountFace(t, db, familyID, born.Code, 1)

	memberAccount := addAccountWithRole(t, db, familyID, "13800000022", "成员", "member")
	wardAccount := addAccountWithRole(t, db, familyID, "13800000023", "被记录成员", "ward")
	guestAccount := addAccountWithRole(t, db, familyID, "13800000024", "访客", "guest")

	r := newSummaryRouter(t, s)

	for _, tc := range []struct {
		role      string
		accountID string
		// wantMounted is the 15.3 reading of the born face for that role, stated so the equality cannot
		// pass by both sides being silently empty for everybody (the same guard faces_test.go puts up).
		wantMounted bool
	}{
		// owner: A on finance -> visible -> mounted. member: M (read is family-wide) -> mounted.
		// ward/guest: 「-」 on finance -> the 首页 draws no cell while the 开通页 still shows the row.
		{role: "owner", accountID: ownerAccount, wantMounted: true},
		{role: "member", accountID: memberAccount, wantMounted: true},
		{role: "ward", accountID: wardAccount, wantMounted: false},
		{role: "guest", accountID: guestAccount, wantMounted: false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			token := sessionToken(t, s, tc.accountID, familyID, tc.role)

			// 消费位 1：开通页（面目录 + 四个 flag）
			wModules := doJSON(t, r, http.MethodGet, modulesPath, token, nil)
			require.Equal(t, http.StatusOK, wModules.Code, wModules.Body.String())
			var modules FamilyModulesResponse
			require.NoError(t, json.Unmarshal(wModules.Body.Bytes(), &modules))

			// 消费位 2：首页四区聚合（本卡接线的那一个）
			wSummary := getSummary(t, r, token, "")
			require.Equal(t, http.StatusOK, wSummary.Code, wSummary.Body.String())
			var got HomeSummaryResponse
			require.NoError(t, json.Unmarshal(wSummary.Body.Bytes(), &got))

			// 消费位 3：两份渲染共同读的那一次合成
			claims, err := s.Signer.Verify(token)
			require.NoError(t, err)
			entries, err := s.composeFaceEntries(t.Context(), claims, familyID)
			require.NoError(t, err)

			fromModules := modulesMounted(modules.Modules)
			fromEntries := entriesMounted(entries)
			fromSummary := statusesViewed(got.Faces)

			assert.Equal(t, fromEntries, fromSummary,
				"home/summary 的 faces[] 必须与 mountedFaces 逐项一致（role=%s）", tc.role)
			assert.Equal(t, fromEntries, fromModules,
				"home/summary 的 faces[] 必须与 GET /family/modules 按 enabled∧visible 过滤后逐项一致（role=%s，PRD 17.8 定版 ⑯）", tc.role)

			if tc.wantMounted {
				require.Len(t, got.Faces, 1, "role=%s 下该面已启用且可见，首页必须画出这一格", tc.role)
				assert.Equal(t, born.Code, got.Faces[0].Code)
				assert.NotEmpty(t, got.Faces[0].Headline, "每一格都带一句服务端状态句（17.2 C 区）")
			} else {
				// 17.8 第 4 条: the role-cropped set CAN be zero cells, and then C 区 simply is not drawn --
				// which requires the answer to be an empty list, not a placeholder and not an error.
				assert.Empty(t, got.Faces, "role=%s 在 15.3 上对该面是「-」，首页不得有格子", tc.role)
				assert.Empty(t, fromModules)
				assert.Equal(t, "[]", string(jsonKeys(t, wSummary.Body.Bytes())["faces"]),
					"faces 必须是空数组而不是 null/缺字段（客户端按「收到什么画什么」渲染）")
			}

			// 未启用与未出生的面在首页「根本不存在」：不补齐、不占位、不置灰（17.2、nav §3.3「没有第三态」）。
			// Asserted against the serialized body, because a placeholder cell is still a cell.
			body := wSummary.Body.String()
			assert.NotContains(t, body, unborn.Code,
				"未出生的面 %q 不得以任何形态出现在 home/summary 里（17.2「首页不呈现未启用与未出生的面」）", unborn.Code)
			for _, code := range catalogCodes() {
				if code == born.Code {
					continue
				}
				assert.NotContains(t, body, code, "家庭未挂载/角色不可见的面 %q 不得在首页留格子", code)
			}

			// availability is server-decided (nav §3.3), never the client's guess, and is the same value
			// the 开通页 shows for the same registry row; 顺序是 registry 登记序，不重排（17.1/17.2）。
			prev := -1
			for _, f := range got.Faces {
				d, ok := registry.ByCode(f.Code)
				require.True(t, ok, "faces[].code 必须是 registry 登记的域：%s", f.Code)
				assert.Equal(t, d.Title, f.Name, "面名必须取自 registry Title（不硬编码）")
				assert.Equal(t, faceAvailability(d), f.Availability)
				at := indexOfCode(catalogCodes(), f.Code)
				assert.Greater(t, at, prev, "faces[] 顺序必须是 registry 登记序（role=%s）", tc.role)
				prev = at
			}

			// The enabled-but-invisible case is what makes this three-way rather than two-way: the 开通页
			// must still show the row (17.8「停用不隐藏」/面目录全量渲染) while the 首页 draws nothing.
			for _, m := range modules.Modules {
				if m.Code == born.Code && !tc.wantMounted {
					assert.True(t, m.Enabled, "本家庭确实挂载了该面（role=%s）", tc.role)
					assert.False(t, m.Visible, "该角色在 15.3 上对该面不可见，首页才因此 0 格")
				}
			}

			// badge is NOT sent (see summaryFaces' doc): 0004 carries no 待处理 count, and PRD 17.1 第 3 条
			// forbids substituting the unread number for it. Pinned here so a future "just put unread in it"
			// patch has a test to fail.
			if len(got.Faces) > 0 {
				assert.NotContains(t, string(jsonKeys(t, wSummary.Body.Bytes())["faces"]), "badge",
					"faces[].badge 是待处理数，P1 无出生数据可数（0004 DDL 注）：缺字段而不是 0")
			}
		})
	}
}

// ==================== period：400 且不回落 ====================

// TestHomeSummaryPeriodIllegalIsFourHundredAndNeverFallsBack pins PRD 4.5.3 定版 ⑬ / 18.2#9: an
// unparseable period is a refusal, not a hint to show the current month. A silent fallback is the
// defect that makes a 家庭 think they are looking at 本月 while the shell asked for something else, and
// it is invisible in a screenshot -- hence the assertion that the 400 carries no summary keys at all.
func TestHomeSummaryPeriodIllegalIsFourHundredAndNeverFallsBack(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	s := newServices(t, db)
	born := firstBornFace(t)
	mountFace(t, db, familyID, born.Code, 1)

	r := newSummaryRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	t.Run("legal_values_answer_200", func(t *testing.T) {
		for _, q := range []string{"", "period=2026-10", "period=2026-Q4", "period=1999-12", "period=2026-01"} {
			w := getSummary(t, r, token, q)
			require.Equal(t, http.StatusOK, w.Code, "period=%q：%s", q, w.Body.String())
		}
	})

	t.Run("illegal_values_answer_400", func(t *testing.T) {
		for _, q := range []string{
			// The three the card names, then the shapes that make the pattern's insufficiency visible.
			"period=2026-13", // pattern-clean, calendar-impossible: time.Date would normalize it into 2027-01
			"period=bogus",
			"period=2026-Q5",
			"period=2026", // 年档：契约 pattern 不认（见报告的上报项），所以这里必须 400 而不是「当作整年」
			"period=2026-00",
			"period=2026-1",
			"period=26-10",
			"period=2026-10-01",
			"period=2026_Q4",
			"period=202610",
			"period=2026-Q0",
			"period=2026-Q13",
			"period=2026-10%3B", // 一个真带分号的值。写成字面 `2026-10;` 是测不到 handler 的：Go 的
			// URL.Query() 自 1.17 起把 ';' 当作被废弃的分隔符，解析报错后静默丢掉那一段，handler 收到的
			// 就已经是干净的 "2026-10"。%3B 才会带着 ';' 走到 pattern 面前，正是这条拒绝要的输入。
			"period=-10",
		} {
			w := getSummary(t, r, token, q)
			require.Equal(t, http.StatusBadRequest, w.Code, "period=%q 必须 400，不得回落为当前月：%s", q, w.Body.String())

			keys := jsonKeys(t, w.Body.Bytes())
			require.Contains(t, keys, "error", "400 必须带错误码：%s", w.Body.String())
			assert.Contains(t, string(keys["error"]), "invalid_period")
			assert.Contains(t, string(keys["message"]), "period")
			// The no-fallback half: a 200-shaped body wearing a 400 status would still render a 本月 首页.
			for _, zone := range []string{"family", "faces", "dynamics", "due_today", "unread"} {
				assert.NotContains(t, keys, zone, "400 的响应体不得带任何一区的字段（period=%q）", q)
			}
		}
	})

	t.Run("quarter_window_differs_from_month_window_on_the_same_bucket", func(t *testing.T) {
		// The window is not decorative: the same mounted face answers a different 状态句 per 档, which is
		// 定版 ⑬'s 「服务端在月桶上求和」 and the thing 18.3#9 checks across 四处.
		setFamilyTimezoneUTC(t, db, familyID)
		insertBucket(t, db, familyID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 0, 50_000, int64ptr(8_60))
		insertBucket(t, db, familyID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 0, 50_000, nil)

		month := summaryFace(t, r, token, "period=2026-10", born.Code)
		assert.Contains(t, month.Headline, "预算剩余", "月档：该月有预算桶，状态句是预算剩余（17.2 C 区）")

		quarter := summaryFace(t, r, token, "period=2026-Q4", born.Code)
		assert.Contains(t, quarter.Headline, "已设预算 1 个月", "季档：两个月桶里只有一个月带预算 -> 报覆盖月数，不乘 3（定版 ⑬ 红线）")
		assert.NotEqual(t, month.Headline, quarter.Headline, "同一格在月档与季档必须给出不同的句子，否则 period 没有传到位")

		empty := summaryFace(t, r, token, "period=2026-09", born.Code)
		assert.Equal(t, headlineNoData, empty.Headline, "窗口内无桶必须报「暂无记账」，不能报 0 元")
		assert.Nil(t, empty.AsOf, "没有算料就没有时效：as_of 缺省而不是编一个时刻（12.3）")
	})
}

// summaryFace reads one cell out of a 200 summary, failing the test when that cell is absent -- which
// is itself an assertion, since the face is mounted and visible to the owner in every fixture here.
func summaryFace(t *testing.T, r *gin.Engine, token, query, code string) FaceStatus {
	t.Helper()
	w := getSummary(t, r, token, query)
	require.Equal(t, http.StatusOK, w.Code, "%s：%s", query, w.Body.String())
	var got HomeSummaryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	for _, f := range got.Faces {
		if f.Code == code {
			return f
		}
	}
	t.Fatalf("faces[] 里没有 %s：%s", code, w.Body.String())
	return FaceStatus{}
}

func int64ptr(v int64) *int64 { return &v }

// ==================== onboarding token 必须被拒 ====================

// TestHomeSummaryRefusesOnboardingScopedToken is PRD 15.2's boundary enforced at the route: a session
// that has no family may create one and read its own account, and may not read a family's 首页. The
// token here is the one a real family-less login returns, so the assertion covers the allowlist the
// process actually installs rather than a hand-made claim.
func TestHomeSummaryRefusesOnboardingScopedToken(t *testing.T) {
	const bootstrapPhone = "13900000123"
	db, _, _, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	s := newServices(t, db)
	r := newSummaryRouter(t, s)

	// The code goes in through the real SMS route rather than a hand-written INSERT: the token under
	// test must come from the same login path the app uses, or the refused subject would be the test's.
	w := doJSON(t, r, http.MethodPost, "/api/homeos/auth/sms-code", "", gin.H{"phone": bootstrapPhone})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": bootstrapPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var login LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &login))
	require.Equal(t, svcauth.ScopeOnboarding, login.Scope, "无家庭登录给的就是 onboarding 会话，本测试才有被测对象")
	require.Empty(t, login.FamilyID)

	for _, path := range []string{summaryPath, modulesPath} {
		refused := doJSON(t, r, http.MethodGet, path, login.AccessToken, nil)
		require.Equal(t, http.StatusForbidden, refused.Code, "%s 必须拒绝 onboarding 会话（403，不是 401）：%s", path, refused.Body.String())
		keys := jsonKeys(t, refused.Body.Bytes())
		assert.Contains(t, string(keys["error"]), "insufficient_scope")
		assert.NotContains(t, keys, "family", "被拒的请求不得漏出任何一区数据：%s", path)
	}

	// 403 and not 401: the caller IS authenticated, it just has no family to act inside -- and 15.5 wants
	// the attempt itself in the audit log.
	var denied int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM homeos_audit_log WHERE result = 'denied'`).Scan(&denied).Error)
	assert.GreaterOrEqual(t, denied, int64(2), "越权尝试必须落审计（PRD 15.5），两次拒绝两行")

	// And no token at all is the middleware's 401, unchanged by this route's own gate.
	anonymous := doJSON(t, r, http.MethodGet, summaryPath, "", nil)
	assert.Equal(t, http.StatusUnauthorized, anonymous.Code)
}

// ==================== A 区 / D 区 / 未读 ====================

// TestHomeSummaryFamilyDynamicsAndUnreadAreServerCounts pins the three numbers the client is told not
// to derive: member_count (17.2 定版 ⑮ 「不数列表长度」), the D 区 page (前 20 条, newest first) and
// `unread`, which is the MESSAGE count from homeos_notification and must not be the dynamics count
// (PRD 17.1 第 2 条 未读只有一个口径; 第 3 条 语义不得混用).
func TestHomeSummaryFamilyDynamicsAndUnreadAreServerCounts(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	s := newServices(t, db)

	// 名册：一个有账号的成人成员 + 一个无账号被记录成员（relation 是 ⑭ 称谓的唯一源）。
	memberAccount := addAccountWithRole(t, db, familyID, "13800000032", "成员", "member")
	otherMember := memberIDForAccount(t, db, familyID, memberAccount)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_members (id, family_id, user_id, role, relation, name, created_at, updated_at)
		 VALUES (?, ?, NULL, 'ward', '爸爸', '父亲', datetime('now'), datetime('now'))`,
		uuid.NewString(), familyID).Error)

	// 未读：本人 2 条未读 + 1 条已读；别人的未读不得进本人的数（15.2 以成员为界）。
	insertNotification(t, db, familyID, ownerMember, "system", false)
	insertNotification(t, db, familyID, ownerMember, "reminder", false)
	insertNotification(t, db, familyID, ownerMember, "budget_alert", true)
	insertNotification(t, db, familyID, otherMember, "system", false)
	insertNotification(t, db, familyID, otherMember, "system", false)

	// 动态：25 条，at 依次递减。
	base := time.Now().UTC().Truncate(time.Hour)
	const dynamicRows = 25
	for i := 0; i < dynamicRows; i++ {
		seedDynamic(t, db, familyID, "finance", "妈妈", "记了一笔",
			fmt.Sprintf("餐饮 ¥%d", 10+i), base.Add(-time.Duration(i)*time.Minute))
	}

	r := newSummaryRouter(t, s)
	w := getSummary(t, r, sessionToken(t, s, ownerAccount, familyID, "owner"), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got HomeSummaryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	body := w.Body.Bytes()

	// A 区形状：契约的 family 块只有四个 key。role / timezone / currency 在 homeos_families 里都有，
	// nav §3.4 的样例也写了三个，但 openapi 的 family 块没有声明 -> 不下发，缺字段作为冲突上报。
	familyKeys := jsonKeys(t, jsonKeys(t, body)["family"])
	assert.ElementsMatch(t, []string{"id", "name", "members", "member_count"}, keysOf(familyKeys),
		"family 块必须正好是契约声明的四项")
	assert.Equal(t, familyID, got.Family.ID)
	assert.Equal(t, "真实家庭", got.Family.Name)
	assert.Equal(t, 3, got.Family.MemberCount, "名册含无账号被记录成员（17.2 A 区「含无账号被记录成员」）")
	assert.Len(t, got.Family.Members, got.Family.MemberCount, "member_count 与 members[] 必须同源，客户端不为 +N 另发请求")

	var seenOwner, seenRecorded bool
	for _, m := range got.Family.Members {
		switch {
		case m.MemberID == ownerMember:
			seenOwner = true
		case m.Relation != nil && *m.Relation == "爸爸":
			seenRecorded = true
			assert.Equal(t, "父亲", m.Name, "无账号被记录成员的名字取成员行")
			assert.Nil(t, m.Avatar, "无头像就是 null（契约 avatar nullable: true），不是空串占位")
		}
	}
	assert.True(t, seenOwner, "本人成员行必须在名册里")
	assert.True(t, seenRecorded, "无账号被记录成员必须在名册里（⑭ 称谓的唯一来源）")

	// D 区：前 20 条、新的在前，条目形状正好是契约的六项。
	assert.Len(t, got.Dynamics.Items, 20, "17.2 D 区前 20 条（契约 maxItems: 20），第 21 条在 GET /dynamics 那一页")
	for i, item := range got.Dynamics.Items {
		assert.NotEmpty(t, item.ID)
		assert.Equal(t, "finance", item.Code, "条目的面图标位取 registry code（17.2 不引入第二套缩写）")
		assert.Equal(t, "妈妈", item.ActorName)
		assert.Equal(t, "记了一笔", item.Action)
		assert.NotEmpty(t, item.Summary, "整句由服务端拼好下发，客户端不拼动词（17.2）")
		if i > 0 {
			assert.False(t, item.At.After(got.Dynamics.Items[i-1].At), "D 区必须按 at 倒序")
		}
	}
	// 契约的 dynamics 块只有 items 一项（nav §3.4 的样例还写了 cursor，首页不用：「查看全部」那一页的
	// 分页属于 GET /dynamics），条目则正好是契约声明的六项。断言在原始 JSON 上做，因为多一个 key 只有
	// 在序列化结果里才看得见。
	dynamics := jsonKeys(t, jsonKeys(t, body)["dynamics"])
	assert.Equal(t, []string{"items"}, keysOf(dynamics), "dynamics 块按契约只有 items 一项")
	var itemsRaw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(dynamics["items"], &itemsRaw))
	require.NotEmpty(t, itemsRaw)
	assert.ElementsMatch(t, []string{"id", "code", "actor_name", "action", "summary", "at"},
		keysOf(itemsRaw[0]), "D 区条目必须正好是契约的六项（entity/entity_id 是落地参数，不是渲染要素）")

	// 未读：消息数（2），既不是动态条数（20/25），也不是别人的未读（2+2）。
	assert.EqualValues(t, 2, got.Unread, "unread 是 homeos_notification 的本人未读总数（17.1 第 2 条唯一口径）")
	assert.NotEqual(t, int64(len(got.Dynamics.Items)), got.Unread, "未读与动态条数是两件事，不得互顶")
	assert.Equal(t, "2", string(jsonKeys(t, body)["unread"]),
		"契约的 unread 就是一个整数，不是 {total,by_type} 对象（nav §3.4 样例与 yaml 此处不一致，已上报）")
}

func memberIDForAccount(t *testing.T, db *gorm.DB, familyID, accountID string) string {
	t.Helper()
	var id string
	require.NoError(t, db.Raw(
		`SELECT id FROM homeos_members WHERE family_id = ? AND user_id = ?`, familyID, accountID).Scan(&id).Error)
	require.NotEmpty(t, id, "测试种下的成员行没读回来")
	return id
}

// ==================== B 区：不可回答时不编数 ====================

// TestHomeSummaryDueTodayOmitsCountWhenProjectionCannotAnswer is repo.DueToday's Answerable flag
// reaching the wire. Counting 0 when nothing was counted would tell the family 今日无到期 on a screen
// that never looked; the contract's block has two properties and no required list, so the honest answer
// to 「没数过」 is one property missing plus a real (empty) item list -- and the honest answer to
// 「数过了，是 0 项」 is count: 0, which is what the last subtest pins.
//
// The two branches are not symmetric in reach: migrations/homeos/homeos_0008_due_registration.up.sql IS
// in the applied sequence, so homeos_due_registration exists in every migrated checkout and
// Answerable=false is only the un-migrated/partial-sequence state. The first subtest therefore pins the
// guard itself, and the third the state a real P1 deployment answers (table present, feed empty).
func TestHomeSummaryDueTodayOmitsCountWhenProjectionCannotAnswer(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	s := newServices(t, db)
	r := newSummaryRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	t.Run("no_registration_table_reports_no_number", func(t *testing.T) {
		// No homeos_due_registration in this fixture -> repo's Answerable=false -> no number on the wire.
		w := getSummary(t, r, token, "period=2026-10")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		due := jsonKeys(t, jsonKeys(t, w.Body.Bytes())["due_today"])
		assert.NotContains(t, due, "count",
			"没有到期注册表时不得下发 count：0 会被首页画成「今日 0 项」，而真实答案是「没数过」（repo.DueToday 文档）")
		require.Contains(t, due, "items")
		assert.Equal(t, "[]", string(due["items"]), "items 是本响应真的没有条目这一事实，可以照常给空数组")
		assert.ElementsMatch(t, []string{"items"}, keysOf(due), "不可回答时 due_today 就是只剩 items")
	})

	t.Run("registration_table_gives_full_count_and_three_items", func(t *testing.T) {
		addDueRegistrationTable(t, db)
		setFamilyTimezoneUTC(t, db, familyID)
		// The rows below are 'finance' registrations, so the family has to have 财务 mounted: since
		// BZ-1 the due read is filtered by the member's 已挂载面集合 (PRD 17.8 定版 ⑯「聚合与触发一律按
		// 本家庭的已挂载面集合过滤」), and an unmounted face's registrations must answer 0 rather than
		// 4. TestHomeSummaryDueTodayIsFilteredToTheMemberMountedFaces pins the filtered side.
		mountFace(t, db, familyID, firstBornFace(t).Code, 1)

		today := time.Now().UTC().Truncate(24 * time.Hour)
		for i := 0; i < 4; i++ {
			require.NoError(t, db.Exec(
				`INSERT INTO homeos_due_registration (id, family_id, source_system, source_id, kind, title, due_at, created_at, updated_at)
				 VALUES (?, ?, 'finance', ?, 'bill', ?, ?, datetime('now'), datetime('now'))`,
				uuid.NewString(), familyID, uuid.NewString(), fmt.Sprintf("房贷 %d", i),
				today.Add(time.Duration(9+i)*time.Hour)).Error)
		}
		// 明天的一条不属于「今日」，也不得被计入 count。
		require.NoError(t, db.Exec(
			`INSERT INTO homeos_due_registration (id, family_id, source_system, source_id, kind, title, due_at, created_at, updated_at)
			 VALUES (?, ?, 'finance', ?, 'bill', '明日到期', ?, datetime('now'), datetime('now'))`,
			uuid.NewString(), familyID, uuid.NewString(), today.Add(30*time.Hour)).Error)

		w := getSummary(t, r, token, "period=2026-10")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var got HomeSummaryResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

		require.NotNil(t, got.DueToday.Count, "有注册表就必须给数：这是 count 的正常形态")
		assert.EqualValues(t, 4, *got.DueToday.Count, "count 是今日全量（17.2 标题行「今日 N 项」）")
		assert.Len(t, got.DueToday.Items, 3, "items 恒 ≤3（契约 maxItems: 3），第 4 条在到期中心看")
		for i := 1; i < len(got.DueToday.Items); i++ {
			assert.False(t, got.DueToday.Items[i].DueAt.Before(got.DueToday.Items[i-1].DueAt),
				"items 按 due_at 升序（17.2「服务端按 due_at 升序给前 3 条」）")
		}
		assert.Equal(t, "finance", got.DueToday.Items[0].SourceSystem, "source_system 取注册行的来源域 code（0008 DDL 注）")

		due := jsonKeys(t, jsonKeys(t, w.Body.Bytes())["due_today"])
		assert.ElementsMatch(t, []string{"count", "items"}, keysOf(due), "可回答时 due_today 正好是契约的两项")
		assert.Equal(t, "4", string(due["count"]),
			"count 与 items 长度可以不等（4 vs 3），是设计结果而不是缺字段")
	})

	t.Run("empty_day_still_answers_zero", func(t *testing.T) {
		// The other side of the same rule: once there IS a table to count, 0 is a real answer and must be
		// sent -- the omission above is about not looking, not about not having anything to report.
		// This is also a migrated P1 checkout's steady state: 0008 has created the table, while no
		// publisher emits finance.due.registered yet (internal/consumer/finance_consumer.go is the only
		// reader of that subject), so every family's 首页 currently says 「今日 0 项」. Reported as the
		// B 区 dependency that has no producer, rather than papered over by withholding the number.
		require.NoError(t, db.Exec(`DELETE FROM homeos_due_registration`).Error)
		w := getSummary(t, r, token, "period=2026-10")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		due := jsonKeys(t, jsonKeys(t, w.Body.Bytes())["due_today"])
		require.Contains(t, due, "count", "数过了，就必须报数")
		assert.Equal(t, "0", string(due["count"]))
	})
}

// ==================== B 区判据 3：撤销（软删）的注册项不再是事实 ====================

// summaryAt drives the one 首屏 request and hands back the decoded body plus its raw text, so a
// failure message always carries what the endpoint actually said.
func summaryAt(t *testing.T, r *gin.Engine, token, query string) (HomeSummaryResponse, string) {
	t.Helper()
	w := getSummary(t, r, token, query)
	require.Equal(t, http.StatusOK, w.Code, "%s：%s", query, w.Body.String())
	var got HomeSummaryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got, w.Body.String()
}

// dueSources is the B 区 item list reduced to the face each entry came from -- the observable form of
// 「哪一面的注册项进了待办」.
func dueSources(items []repo.DueTodayItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.SourceSystem)
	}
	return out
}

// TestHomeSummaryDueTodayDropsRevokedRegistrations is the read side of 0008's soft delete. Two
// registrations for one family and one day are written by this service's own consumer
// (registerDue -> consumer.DueRegisteredHandler), and one of them is then revoked by the other half of
// the same chain (revokeDue -> consumer.DueRevokedHandler, 0008:43-45「finance.due.revoked 是本表唯一的
// 删除路径」). Nothing in this case is a hand-stamped column: the state the read side must reject is
// produced by the production写路径 that produces it in the field. Before/after on the same two rows is
// asserted so the guard cannot pass because the revoked row never landed.
func TestHomeSummaryDueTodayDropsRevokedRegistrations(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	addDueRegistrationTable(t, db)
	setFamilyTimezoneUTC(t, db, familyID)

	born := firstBornFace(t)
	mountFace(t, db, familyID, born.Code, 1)

	s := newServices(t, db)
	r := newSummaryRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	// revokedDue is named once and given to BOTH legs: registerDue writes it into the payload's due_at
	// and into business_id, revokeDue has to put the SAME 期 into the revoked event's business_id
	// (finance.yaml:95「"{source_id}:{due_at}"」), because that is where撤销锚点 reads due_at from.
	revokedDue := today.Add(10 * time.Hour)
	liveID, _ := registerDue(t, db, familyID, born.Code, "房贷 10 月", today.Add(9*time.Hour))
	revokedID, revokedSourceID := registerDue(t, db, familyID, born.Code, "房贷 9 月已结清", revokedDue)

	before, body := summaryAt(t, r, token, "period=2026-10")
	require.NotNil(t, before.DueToday.Count, "表在且数过 -> count 必须给（判据 4 的 Answerable 取舍不受本改动影响）")
	require.EqualValues(t, 2, *before.DueToday.Count, "撤销前的对照：两条活注册都进了 count：%s", body)
	require.Len(t, before.DueToday.Items, 2, "撤销前的对照：两条活注册都进了 items：%s", body)

	revokeDue(t, db, familyID, born.Code, revokedSourceID, revokedDue)

	after, body := summaryAt(t, r, token, "period=2026-10")
	require.NotNil(t, after.DueToday.Count, "数过了就必须报数，过滤不改变 Answerable 的含义")
	assert.EqualValues(t, 1, *after.DueToday.Count, "count 只数未撤销的注册项")
	require.Len(t, after.DueToday.Items, 1)
	assert.Equal(t, liveID, after.DueToday.Items[0].ID)
	assert.Equal(t, "房贷 10 月", after.DueToday.Items[0].Title)
	assert.NotContains(t, body, "房贷 9 月已结清", "已撤销的注册项不得出现在 B 区（撤销不是「排到第 4 条之后」）")
	assert.NotContains(t, body, revokedID, "撤销行的 id 也不得下发")

	// 软删而不是物理删除：读侧过滤掉了它，表里那一行仍然是历史（0008:49-54 的部分唯一索引正是为它留的槽位）。
	var rows int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM homeos_due_registration`).Scan(&rows).Error)
	assert.EqualValues(t, 2, rows, "撤销行留在表里，只是不再被 B 区读出来")
}

// ==================== B 区判据 4：一律按已挂载面集合过滤 ====================

// TestHomeSummaryDueTodayIsFilteredToTheMemberMountedFaces pins PRD 17.8 定版 ⑯'s 聚合 rule on B 区:
// 「聚合与触发一律按本家庭的已挂载面集合过滤——被停用面的注册项不消失也不触发、不出现在日历与待办里，
// 重新启用后按其自身时间规则照常恢复」. The set is the SAME mountedFaces output C 区 is drawn from
// (faces.go's composition, role 裁剪 already applied per 定版 ⑯), so three legs share one fixture:
//
//   - 挂载 + 底座 -> 两条都在;
//   - 角色不可见（15.3 的 scope=module 为「-」，ward）-> 面那条不进 B 区，底座那条照常 —— this is the
//     leg that distinguishes 「该成员该次请求的已挂载面」 from 「家庭全部 enabled 的 code」，两者对非
//     owner 角色不同；
//   - 家庭停用该面 -> owner 的请求里也只剩底座那条；重新启用 -> 原来那条按自己的 due_at 回来（不是新行、
//     不是复活，表里 deleted_at 一直为空）。
//
// The 底座 code is in the filter set because composeFaceEntries subtracts it from the matrix on purpose
// (PRD 17.1「首页自身不进矩阵」) while it is not a face a family can 停用 —— homeos.go's dueFaceCodes
// states that reasoning; the 底座 leg below is what pins it.
func TestHomeSummaryDueTodayIsFilteredToTheMemberMountedFaces(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	addDueRegistrationTable(t, db)
	setFamilyTimezoneUTC(t, db, familyID)

	born := firstBornFace(t)
	mountFace(t, db, familyID, born.Code, 1)
	wardAccount := addAccountWithRole(t, db, familyID, "13800000044", "被记录成员", "ward")

	s := newServices(t, db)
	r := newSummaryRouter(t, s)
	ownerToken := sessionToken(t, s, ownerAccount, familyID, "owner")
	wardToken := sessionToken(t, s, wardAccount, familyID, "ward")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	financeID, _ := registerDue(t, db, familyID, born.Code, "本月账单到期", today.Add(9*time.Hour))
	baseID := seedBaseDue(t, db, familyID, "底座待办：给爸爸打电话", today.Add(10*time.Hour))

	// ① 面已挂载（且该角色可见）+ 底座：两条都进 B 区，顺序仍是 due_at 升序。
	got, body := summaryAt(t, r, ownerToken, "period=2026-10")
	require.NotNil(t, got.DueToday.Count)
	require.EqualValues(t, 2, *got.DueToday.Count, "挂载面与底座的注册都该数进来：%s", body)
	assert.Equal(t, []string{born.Code, registry.HomeosCode}, dueSources(got.DueToday.Items),
		"items 按 due_at 升序（17.2），且两个来源都因为「在已挂载集合里」而存在")
	assert.Equal(t, baseID, got.DueToday.Items[1].ID)

	// ② 同一家庭、同一次数据，换一个对该面不可见的角色：面来源的注册项消失，底座那条照常。
	ward, body := summaryAt(t, r, wardToken, "period=2026-10")
	require.Empty(t, ward.Faces, "前提：ward 在 C 区就是 0 格（同一份合成），B 区必须给出同一个答案")
	require.NotNil(t, ward.DueToday.Count)
	assert.EqualValues(t, 1, *ward.DueToday.Count, "角色看不到的面，其注册项也不进该成员看到的待办（定版 ⑯ 的裁剪）")
	assert.Equal(t, []string{registry.HomeosCode}, dueSources(ward.DueToday.Items))
	assert.NotContains(t, body, "本月账单到期", "C 区没有那一格，B 区却显示那一面的到期 —— 一个响应里的两个答案")
	assert.NotContains(t, body, financeID)

	// ③ 家庭停用该面（homeos_family_module.enabled=false，即 PUT /family/modules 写的那一列）：
	// owner 的请求也只剩底座那条。
	setFaceEnabled(t, db, familyID, born.Code, false)
	off, body := summaryAt(t, r, ownerToken, "period=2026-10")
	require.NotNil(t, off.DueToday.Count)
	assert.EqualValues(t, 1, *off.DueToday.Count, "停掉财务面之后，财务的账单到期不得还显示在 B 区（17.8）")
	assert.Equal(t, []string{registry.HomeosCode}, dueSources(off.DueToday.Items))
	assert.NotContains(t, body, financeID, "「不消失」指的是表里的行，不是首页还把它发出去")

	// 注册项在停用期间「不消失也不触发」：读侧过滤不动表，行仍是活的。
	var liveRows int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM homeos_due_registration WHERE deleted_at IS NULL`).Scan(&liveRows).Error)
	assert.EqualValues(t, 2, liveRows, "停用不删数据（17.8）：过滤发生在读侧而不是表上")

	// 重新启用后按其自身时间规则照常恢复：同一条行、同一个 due_at，今日窗口内重新出现。
	setFaceEnabled(t, db, familyID, born.Code, true)
	back, _ := summaryAt(t, r, ownerToken, "period=2026-10")
	require.NotNil(t, back.DueToday.Count)
	assert.EqualValues(t, 2, *back.DueToday.Count, "重新启用 -> 两条都在（17.8「重新启用后按其自身时间规则照常恢复」）")
	assert.Equal(t, []string{born.Code, registry.HomeosCode}, dueSources(back.DueToday.Items))
	assert.Equal(t, financeID, back.DueToday.Items[0].ID, "恢复的是同一条注册行，不是重注册出来的新行")
	assert.True(t, back.DueToday.Items[0].DueAt.Equal(financeDueAtOf(t, db, financeID)),
		"恢复按对象自己的时间规则：due_at 一个字段都没被动过")
}

// financeDueAtOf reads a registration's stored due_at back from the table -- the「自身时间规则」half of
// the 重新启用 leg, asserted against the stored value rather than against a value the test remembers.
func financeDueAtOf(t *testing.T, db *gorm.DB, id string) time.Time {
	t.Helper()
	var at time.Time
	require.NoError(t, db.Raw(`SELECT due_at FROM homeos_due_registration WHERE id = ?`, id).Scan(&at).Error)
	require.False(t, at.IsZero(), "注册行读不回 due_at")
	return at
}
