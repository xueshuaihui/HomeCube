// notifications_test.go pins 消息中心's pair against its contract block (homeos.yaml L488 / L549) and
// against the one 未读口径 PRD 17.1 第 2 条 defines.
//
// Four things are asserted that nothing else in this package covers:
//
//   - the wire shape: GET's five item properties and four top-level keys, POST's two integers, and the
//     additive `unread_by_type` (whose three keys must be exactly repo.NotificationTypes -- a fourth
//     type landing on one side only is the drift this catches).
//   - the count caliber: `unread` is the member's UNFILTERED total even when ?type= narrows `items`,
//     because the shipped client writes that number into the shell's single global unread store on
//     every list load. A tab filter that scaled the count would clear a red dot nobody tapped.
//   - per-member isolation: POST marks the caller's rows and nobody else's, which here is not a
//     property of the gate (member/ward/guest all carry M on homeos:time_collab) but of
//     repo.MarkNotificationsRead's `member_id = ?` predicate. The test therefore reads the second
//     member's count from the database, not from a response the same member produced.
//   - no client-supplied counts: POST's `unread` is re-read after the write, so an id list that
//     matched nothing answers marked_count 0 and the true remaining total.
//
// The cursor tests bind their timestamps through Go time.Time values rather than SQLite's
// datetime('now') (which is what home_summary_test.go's insertNotification helper uses where the
// instant is irrelevant): the driver writes a bound time.Time as TEXT *including its offset*, so a
// page whose rows were written by two different renderings would be ordered lexicographically and the
// second page would be an accident rather than a fact. home_summary_test.go's own note documents that
// engine caveat; here it is load-bearing.
package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

const (
	notificationsPath        = "/api/homeos/notifications"
	notificationsReadPath    = "/api/homeos/notifications/read"
	dynamicsPath             = "/api/homeos/dynamics"
	dynamicsReadPath         = "/api/homeos/dynamics/read"
	feedNotificationPageTime = "2026-03-01T00:00:00Z"
)

// ==================== fixtures ====================

// newFeedRouter mounts the four 动态流/消息中心 routes on the same prefix, the same middleware and the
// same (c, services, mw) shape cmd/svc-homeos uses, plus the two public auth routes the onboarding
// refusal test needs in order to obtain a REAL family-less token. GET /home/summary and GET
// /family/modules ride along because the feed's assertions are about 同源: 17.2 requires 「与 GET
// /api/homeos/home/summary 同一个口径」 and 17.8 requires the 筛选选项 to be the same server-derived face
// set, and neither is checkable from one endpoint's own answer. A router built here rather than a
// direct handler call is what makes the 403/401 assertions about the process's route list.
func newFeedRouter(t *testing.T, s *Services) *gin.Engine {
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
	protected.GET("/notifications", func(c *gin.Context) { GetNotifications(c, s, mw) })
	protected.POST("/notifications/read", func(c *gin.Context) { MarkNotificationsRead(c, s, mw) })
	protected.GET("/dynamics", func(c *gin.Context) { GetDynamics(c, s, mw) })
	protected.POST("/dynamics/read", func(c *gin.Context) { MarkDynamicsRead(c, s, mw) })
	protected.GET("/home/summary", func(c *gin.Context) { GetHomeSummary(c, s, mw) })
	protected.GET("/family/modules", func(c *gin.Context) { GetFamilyModules(c, s, mw) })
	return r
}

// insertDatedNotification writes one message row with an explicit instant, because the paging tests
// compare cursor timestamps against the column and both sides must be the same rendering (see this
// file's header). `read` stamps read_at; false leaves it NULL, which is the unread caliber.
func insertDatedNotification(t *testing.T, db *gorm.DB, familyID, memberID, typ, content string, at time.Time, read bool) string {
	t.Helper()
	id := uuid.NewString()
	var readAt any
	if read {
		readAt = at.UTC()
	}
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_notification (id, family_id, member_id, type, content, channel, read_at, created_at)
		 VALUES (?, ?, ?, ?, ?, 'inapp', ?, ?)`,
		id, familyID, memberID, typ, content, readAt, at.UTC()).Error)
	return id
}

// unreadFor reads a member's message counts straight from the repository, i.e. the same read the
// handler performs. The isolation assertions go through this instead of through a second response, so
// "member B was untouched" is a statement about the table.
func unreadFor(t *testing.T, db *gorm.DB, familyID, memberID string) (map[string]int64, int64) {
	t.Helper()
	byType, total, err := repo.UnreadNotificationCounts(t.Context(), db, familyID, memberID)
	require.NoError(t, err)
	return byType, total
}

// ==================== GET /notifications: contract shape ====================

func TestNotificationsAnswerIsTheContractShapeAndServerCounts(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addNotificationTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	base, err := time.Parse(time.RFC3339Nano, feedNotificationPageTime)
	require.NoError(t, err)
	// Two unread of different types plus one already-read: the already-read row must still be LISTED
	// (the message page shows history) while contributing to no count.
	insertDatedNotification(t, db, familyID, ownerMember, "budget_alert", "本月餐饮超预算 12%", base.Add(-2*time.Hour), false)
	insertDatedNotification(t, db, familyID, ownerMember, "reminder", "话费账单今天到期", base.Add(-1*time.Hour), false)
	systemID := insertDatedNotification(t, db, familyID, ownerMember, "system", "系统维护完成", base, true)

	w := doJSON(t, r, http.MethodGet, notificationsPath, sessionToken(t, s, ownerAccount, familyID, "owner"), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	keys := jsonKeys(t, w.Body.Bytes())
	assert.ElementsMatch(t, []string{"items", "next_cursor", "unread", "unread_by_type"}, keysOf(keys),
		"GET /notifications 的顶层键必须与契约一致（unread_by_type 为本次新增的附加字段）：%s", w.Body.String())

	var resp NotificationsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(2), resp.Unread, "未读只数 read_at IS NULL 的本人行（PRD 17.1 第 2 条）")
	assert.Equal(t, UnreadByTypeBlock{BudgetAlert: 1, System: 0, Reminder: 1}, resp.UnreadByType,
		"三个键恒在，repo 的稀疏映射里缺失的类型渲染为 0")
	require.Len(t, resp.Items, 3, "已读消息仍在列表里，只是不进计数")
	assert.Nil(t, resp.NextCursor, "一页取完不得给出游标")

	// Item property set: exactly the contract's five, so family_id / member_id / channel / dedupe_key
	// (all real columns on the row) never ride out onto the wire.
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.ElementsMatch(t, []string{"content", "created_at", "id", "read_at", "type"}, keysOf(raw.Items[0]),
		"items[] 字段集即 homeos.yaml L526-542")

	assert.True(t, resp.Items[0].CreatedAt.After(resp.Items[1].CreatedAt) ||
		resp.Items[0].CreatedAt.Equal(resp.Items[1].CreatedAt), "created_at DESC")
	assert.Equal(t, systemID, resp.Items[0].ID, "最新的一条是已读的 system 行")
	assert.NotNil(t, resp.Items[0].ReadAt)
	assert.Nil(t, resp.Items[1].ReadAt, "未读的 read_at 必须是 null（契约 nullable: true）")
}

// TestNotificationsUnreadByTypeKeysMatchRepoEnum keeps the additive block and the repository's type
// list one fact instead of two: UnreadByTypeBlock's json keys are read off the struct by reflection
// and compared with repo.NotificationTypes, which is what homeos.yaml's enum and 0007's CHECK
// constraint also agree with.
func TestNotificationsUnreadByTypeKeysMatchRepoEnum(t *testing.T) {
	var block UnreadByTypeBlock
	fields := make([]string, 0, reflect.TypeOf(block).NumField())
	for i := 0; i < reflect.TypeOf(block).NumField(); i++ {
		tag := reflect.TypeOf(block).Field(i).Tag.Get("json")
		require.NotEmpty(t, tag, "unread_by_type 的每个键都要有 json tag")
		fields = append(fields, tag)
	}
	assert.ElementsMatch(t, repo.NotificationTypes, fields,
		"unread_by_type 的键必须与 repo.NotificationTypes 同集合，否则新增类型只会出现在一边")
}

// TestNotificationsTypeFilterDoesNotMoveUnread is the red-dot caliber test: a tab filter narrows the
// LIST and must not narrow the COUNT.
func TestNotificationsTypeFilterDoesNotMoveUnread(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addNotificationTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	base := time.Now().UTC().Add(-time.Hour)
	insertDatedNotification(t, db, familyID, ownerMember, "reminder", "提醒一", base, false)
	insertDatedNotification(t, db, familyID, ownerMember, "reminder", "提醒二", base.Add(time.Minute), false)
	insertDatedNotification(t, db, familyID, ownerMember, "budget_alert", "预算一", base.Add(2*time.Minute), false)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	all := doJSON(t, r, http.MethodGet, notificationsPath, token, nil)
	require.Equal(t, http.StatusOK, all.Code)
	var allResp NotificationsResponse
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &allResp))
	require.Equal(t, int64(3), allResp.Unread)

	filtered := doJSON(t, r, http.MethodGet, notificationsPath+"?type=reminder", token, nil)
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	var got NotificationsResponse
	require.NoError(t, json.Unmarshal(filtered.Body.Bytes(), &got))
	require.Len(t, got.Items, 2, "?type=reminder 只列提醒")
	for _, item := range got.Items {
		assert.Equal(t, "reminder", item.Type)
	}
	assert.Equal(t, int64(3), got.Unread,
		"未读总数不随页签筛选缩水：客户端把这一个数写进全局红点（PRD 17.1 第 2/4 条）")
	assert.Equal(t, UnreadByTypeBlock{BudgetAlert: 1, System: 0, Reminder: 2}, got.UnreadByType,
		"页签计数始终按类型给出，与列表筛选无关")

	// An unknown type is a 400 (repo.ErrInvalidArgument), not an empty list: three tabs must never read
	// "no such type" as "nothing in this type".
	bad := doJSON(t, r, http.MethodGet, notificationsPath+"?type=letter", token, nil)
	require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
	assert.Contains(t, string(jsonKeys(t, bad.Body.Bytes())["error"]), "invalid_request")
}

// ==================== POST /notifications/read ====================

// TestMarkNotificationsReadIsPerMemberAndNeedsExplicitIntent is the isolation + intent test.
func TestMarkNotificationsReadIsPerMemberAndNeedsExplicitIntent(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addNotificationTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	memberAccount := addAccountWithRole(t, db, familyID, "13800000045", "二妹", "member")
	memberID := memberIDForAccount(t, db, familyID, memberAccount)

	base := time.Now().UTC().Add(-time.Hour)
	ownerPicked := insertDatedNotification(t, db, familyID, ownerMember, "system", "给管理员", base, false)
	insertDatedNotification(t, db, familyID, ownerMember, "reminder", "管理员的提醒", base.Add(time.Minute), false)
	otherID := insertDatedNotification(t, db, familyID, memberID, "budget_alert", "别人的预算提醒", base.Add(2*time.Minute), false)

	ownerToken := sessionToken(t, s, ownerAccount, familyID, "owner")
	memberToken := sessionToken(t, s, memberAccount, familyID, "member")

	// ① No explicit range: 400 and nothing written. homeos.yaml L570's 「为空则标记全部已读」 conflicts with
	//    L573's `all default: false`; the handler takes the non-destructive side and reports it.
	empty := doJSON(t, r, http.MethodPost, notificationsReadPath, ownerToken, gin.H{})
	require.Equal(t, http.StatusBadRequest, empty.Code, empty.Body.String())
	_, stillUnread := unreadFor(t, db, familyID, ownerMember)
	assert.Equal(t, int64(2), stillUnread, "400 之后本人未读一条不少")

	// ② An id that is not the caller's: counted as nothing, and the OTHER member's row stays unread.
	foreign := doJSON(t, r, http.MethodPost, notificationsReadPath, ownerToken,
		gin.H{"notification_ids": []string{otherID}, "all": false})
	require.Equal(t, http.StatusOK, foreign.Code, foreign.Body.String())
	var foreignResp MarkNotificationsReadResponse
	require.NoError(t, json.Unmarshal(foreign.Body.Bytes(), &foreignResp))
	assert.Zero(t, foreignResp.MarkedCount, "member_id 在 WHERE 里，别人的行不在可写集合内")
	byType, total := unreadFor(t, db, familyID, memberID)
	assert.Equal(t, int64(1), total, "另一成员的未读不受影响（PRD 17.1 第 2 条按 member_id 聚合）")
	assert.Equal(t, map[string]int64{"budget_alert": int64(1)}, byType)

	// ③ Own one id by id.
	picked := doJSON(t, r, http.MethodPost, notificationsReadPath, ownerToken,
		gin.H{"notification_ids": []string{ownerPicked}, "all": false})
	require.Equal(t, http.StatusOK, picked.Code, picked.Body.String())
	var pickedResp MarkNotificationsReadResponse
	require.NoError(t, json.Unmarshal(picked.Body.Bytes(), &pickedResp))
	assert.Equal(t, int64(1), pickedResp.MarkedCount)
	assert.Equal(t, int64(1), pickedResp.Unread, "剩余未读服务端重读，不由 ids 数量推出")
	_, afterPicked := unreadFor(t, db, familyID, ownerMember)
	assert.Equal(t, int64(1), afterPicked)

	// ④ Same id again: a no-op that reports 0, not an error and not a double count.
	replay := doJSON(t, r, http.MethodPost, notificationsReadPath, ownerToken,
		gin.H{"notification_ids": []string{ownerPicked}, "all": false})
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	var replayResp MarkNotificationsReadResponse
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayResp))
	assert.Zero(t, replayResp.MarkedCount, "read_at IS NULL 已经把已读行排除在外")
	assert.Equal(t, int64(1), replayResp.Unread)

	// ⑤ all:true clears the caller only.
	all := doJSON(t, r, http.MethodPost, notificationsReadPath, ownerToken, gin.H{"all": true})
	require.Equal(t, http.StatusOK, all.Code, all.Body.String())
	var allResp MarkNotificationsReadResponse
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &allResp))
	assert.Equal(t, int64(1), allResp.MarkedCount)
	assert.Equal(t, int64(0), allResp.Unread)
	_, ownerNow := unreadFor(t, db, familyID, ownerMember)
	assert.Equal(t, int64(0), ownerNow)
	_, memberNow := unreadFor(t, db, familyID, memberID)
	assert.Equal(t, int64(1), memberNow, "「全部已读」是本人的全部（15.2 判定以 family_id+member_id 为界）")

	// ⑥ The other member can still clear their own, and the top-bar number they then read is 0.
	theirAll := doJSON(t, r, http.MethodPost, notificationsReadPath, memberToken, gin.H{"all": true})
	require.Equal(t, http.StatusOK, theirAll.Code, theirAll.Body.String())
	var theirResp MarkNotificationsReadResponse
	require.NoError(t, json.Unmarshal(theirAll.Body.Bytes(), &theirResp))
	assert.Equal(t, int64(1), theirResp.MarkedCount)

	// ⑦ 已读不等于消失: GET /notifications has no unread-only filter in the contract, so the two rows are
	//    still listed with read_at filled, while the counted caliber has moved to 0. The message page's
	//    history and the 顶栏红点 therefore read one table with two questions, not two tables.
	afterAll := doJSON(t, r, http.MethodGet, notificationsPath, ownerToken, nil)
	require.Equal(t, http.StatusOK, afterAll.Code)
	var listed NotificationsResponse
	require.NoError(t, json.Unmarshal(afterAll.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 2, "已读消息仍在列表里")
	for _, item := range listed.Items {
		assert.NotNil(t, item.ReadAt, "本人两条都已读：%s", item.ID)
	}
	assert.Equal(t, int64(0), listed.Unread)
	assert.Equal(t, UnreadByTypeBlock{}, listed.UnreadByType, "三个键都归 0，而不是缺键")

	// A type with nothing in it answers a real empty array (the client's v-for must not meet a missing
	// key), and no cursor is invented for a page that ran out.
	budgetTab := doJSON(t, r, http.MethodGet, notificationsPath+"?type=budget_alert", ownerToken, nil)
	require.Equal(t, http.StatusOK, budgetTab.Code, budgetTab.Body.String())
	var budget NotificationsResponse
	require.NoError(t, json.Unmarshal(budgetTab.Body.Bytes(), &budget))
	assert.Empty(t, budget.Items)
	assert.NotNil(t, budget.Items, "items 必须是真数组，而不是缺键")
	assert.Nil(t, budget.NextCursor)
}

// TestMarkNotificationsReadRefusesOversizedIDList keeps the IN list bounded (maxMarkReadIDs) instead of
// handing the database a statement it cannot plan.
func TestMarkNotificationsReadRefusesOversizedIDList(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addNotificationTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	seeded := insertDatedNotification(t, db, familyID, ownerMember, "system", "不会被标记的一条", time.Now().UTC(), false)
	ids := make([]string, maxMarkReadIDs+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	w := doJSON(t, r, http.MethodPost, notificationsReadPath,
		sessionToken(t, s, ownerAccount, familyID, "owner"),
		gin.H{"notification_ids": ids, "all": false})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, string(jsonKeys(t, w.Body.Bytes())["error"]), "too_many_ids")
	_, unread := unreadFor(t, db, familyID, ownerMember)
	assert.Equal(t, int64(1), unread, "越界的请求体必须在触碰数据库之前被拒：%s", seeded)
}

// ==================== cursor / limit ====================

// TestNotificationsCursorPagesAndRefusesForgedTokens is the paging contract: an opaque token hands
// back the next page, and an illegal one is a 400 that does NOT replay page 1 (repo's own doc line:
// 「handlers map it to 400 and must NOT fall back to the first page」 -- a fallback would make an
// infinite-scroll list loop).
func TestNotificationsCursorPagesAndRefusesForgedTokens(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addNotificationTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	base := time.Now().UTC().Add(-3 * time.Hour)
	for i := 0; i < 3; i++ {
		insertDatedNotification(t, db, familyID, ownerMember, "system",
			fmt.Sprintf("系统消息 %d", i+1), base.Add(time.Duration(i)*time.Minute), false)
	}
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	first := doJSON(t, r, http.MethodGet, notificationsPath+"?limit=2", token, nil)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstResp NotificationsResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResp))
	require.Len(t, firstResp.Items, 2)
	require.NotNil(t, firstResp.NextCursor)

	second := doJSON(t, r, http.MethodGet, notificationsPath+"?limit=2&cursor="+*firstResp.NextCursor, token, nil)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var secondResp NotificationsResponse
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResp))
	require.Len(t, secondResp.Items, 1, "第三页只剩一条，且不是第一页的重播")
	assert.NotEqual(t, secondResp.Items[0].ID, firstResp.Items[0].ID)
	assert.Nil(t, secondResp.NextCursor, "取完即无游标，不给假令牌")

	forged := base64.RawURLEncoding.EncodeToString([]byte("2026-03-01T00:00:00Z"))
	bad := doJSON(t, r, http.MethodGet, notificationsPath+"?cursor="+forged, token, nil)
	require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
	keys := jsonKeys(t, bad.Body.Bytes())
	assert.Contains(t, string(keys["error"]), "invalid_cursor")
	assert.NotContains(t, keys, "items", "非法游标不得回落成首页数据：%s", bad.Body.String())

	for _, raw := range []string{"?limit=0", "?limit=-5", "?limit=abc"} {
		w := doJSON(t, r, http.MethodGet, notificationsPath+raw, token, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s 必须是 400，而不是静默按 20 条回答：%s", raw, w.Body.String())
	}

	// A limit above the repository's cap is accepted and clamped, so the page size stays bounded
	// without turning a client's 5000 into an error.
	clamped := doJSON(t, r, http.MethodGet, notificationsPath+"?limit=5000", token, nil)
	require.Equal(t, http.StatusOK, clamped.Code, clamped.Body.String())
}

// ==================== 会话边界 ====================

// TestNotificationsRefusesOnboardingScopedTokenAndAnonymousCall covers the four new routes' auth
// boundary in one place: an onboarding (family-less) token gets 403 insufficient_scope from svcauth's
// allowlist, and no token at all gets the middleware's 401. Both are asserted for the two POSTs too,
// since a family-less session has no member id to write a receipt for.
func TestNotificationsRefusesOnboardingScopedTokenAndAnonymousCall(t *testing.T) {
	const bootstrapPhone = "13900000456"
	db, _, _, _ := setupIdentityDB(t)
	addNotificationTable(t, db)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	w := doJSON(t, r, http.MethodPost, "/api/homeos/auth/sms-code", "", gin.H{"phone": bootstrapPhone})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": bootstrapPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var login LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &login))
	require.Equal(t, svcauth.ScopeOnboarding, login.Scope, "被测对象必须是真实的 onboarding 令牌")

	type call struct {
		method string
		path   string
		body   any
	}
	calls := []call{
		{http.MethodGet, notificationsPath, nil},
		{http.MethodPost, notificationsReadPath, gin.H{"all": true}},
		{http.MethodGet, dynamicsPath, nil},
		{http.MethodPost, dynamicsReadPath, gin.H{"all": true}},
	}
	for _, c := range calls {
		refused := doJSON(t, r, c.method, c.path, login.AccessToken, c.body)
		require.Equal(t, http.StatusForbidden, refused.Code, "%s %s 必须拒绝 onboarding 会话：%s", c.method, c.path, refused.Body.String())
		keys := jsonKeys(t, refused.Body.Bytes())
		assert.Contains(t, string(keys["error"]), "insufficient_scope")
		assert.NotContains(t, keys, "items", "被拒的请求不得漏出任何数据：%s", c.path)

		anonymous := doJSON(t, r, c.method, c.path, "", c.body)
		assert.Equal(t, http.StatusUnauthorized, anonymous.Code, "%s %s 无令牌必须 401", c.method, c.path)
	}
}
