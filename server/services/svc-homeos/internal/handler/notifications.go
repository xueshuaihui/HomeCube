// notifications.go is 消息中心's pair: GET /api/homeos/notifications and
// POST /api/homeos/notifications/read (contracts/openapi/homeos.yaml L488 / L549, PRD 3.6
// Notification row, 17.1 未读口径, 15.3 row 3 「homeos:time_collab」 which is the matrix row that
// names reminder/board/vote/DYNAMIC -- the message page and the feed are the same object class).
//
// The shapes below are the contract's, property for property, and the two files that consume them
// (web/src/pages/homeos/messages/index.vue, web/src/stores/home.ts) are written against the same
// block, so nothing here is a private invention:
//
//   - GET  -> {items:[{id,type,content,read_at,created_at}], unread, unread_by_type, next_cursor}
//   - POST {notification_ids, all} -> {marked_count, unread}
//
// `unread_by_type` is the one addition, and it is additive: the client's three type tabs need the
// per-type counts, repo.UnreadNotificationCounts already returns them, and homeos.yaml's 200 block
// simply did not declare the key (the page's own comment records that as a 定版冲突). A field the
// server already computes and the client already reads is a contract gap, not a licence to invent a
// second endpoint; the yaml now says what the response says.
//
// Two rules bind every line:
//
//   - family_id and member_id come from the session the auth middleware installed, never from the
//     request (PRD 15.2 「判定以 family_id 为界」). There is no family or member parameter to ignore,
//     and repo's readers take the member id as a predicate rather than as a display hint --
//     homeos_notification rows are fanned out per member, so "somebody else's inbox" is not a query
//     this file can build.
//   - every 未读 number is computed server-side (PRD 17.1 第 2 条「未读口径唯一: read_at IS NULL 按
//     member_id 聚合」, 17.1 第 3 条 forbids the client carrying counts). No request body field is
//     ever read as a count, and POST's `unread` is re-read after the write rather than derived from
//     the number of ids the caller sent.
//
// Offset pagination is absent by design: repo.ListNotifications speaks the opaque
// base64url(RFC3339Nano~id) cursor, which is the only paging form this project allows.
package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// maxMarkReadIDs bounds one POST's id list. The largest page either list endpoint can hand back is
// 100 (repo.clampLimit's cap), so 200 ids is already "two pages of picks" and anything past it is a
// request that would build an unbounded IN list -- a self-inflicted 500 on a statement the database
// would rather not plan. Refused as a 400 with the number in the message.
const maxMarkReadIDs = 200

// ==================== response shapes (contract-exact) ====================

// NotificationCell is one /notifications item: the contract's five properties and nothing else.
// model.HomeosNotification also carries family_id / member_id / channel / dedupe_key, and none of
// the four is declared at homeos.yaml L526-542 -- dedupe_key in particular is a bus-replay
// implementation detail. The rows therefore go through a projection instead of being serialized
// directly, which is also what keeps a future column from silently becoming API surface.
type NotificationCell struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Content   string     `json:"content"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// UnreadByTypeBlock is the additive `unread_by_type` object: the contract's enum trio
// (homeos.yaml L503) as three integer keys. Every key is always present, because repo's map omits
// types with no unread rows and a missing tab count would make the client guess between "0" and
// "not computed" (its own code does `unreadByType[type] ?? 0`, i.e. it needs all three).
//
// The three names are written once here as json keys and once in repo.NotificationTypes;
// notifications_test.go asserts the two sets are equal, so the enum cannot gain a member on one
// side only.
type UnreadByTypeBlock struct {
	BudgetAlert int64 `json:"budget_alert"`
	System      int64 `json:"system"`
	Reminder    int64 `json:"reminder"`
}

// NotificationsResponse is GET's body.
//
// `unread` is deliberately the caller's UNFILTERED total, even when ?type= narrowed `items`. The
// shipped client writes this number into the shell's single global unread store on every list load
// (web/src/stores/home.ts fetchNotifications), and PRD 17.1 第 4 条 makes that number 顶栏红点 and the
// 首页 D 行 read of one caliber. A tab filter that also scaled the count would let "查看提醒" clear the
// 预算提醒 red dot by accident; the type tabs' own numbers live in unread_by_type, which is where a
// per-type figure belongs.
type NotificationsResponse struct {
	Items        []NotificationCell `json:"items"`
	Unread       int64              `json:"unread"`
	UnreadByType UnreadByTypeBlock  `json:"unread_by_type"`
	NextCursor   *string            `json:"next_cursor"`
}

// ==================== GET /notifications ====================

// GetNotifications handles GET /api/homeos/notifications.
//
// Gate: authz's homeos:time_collab row + read (PRD 15.3 row 3, whose comment enumerates
// calendar/todo/reminder/board/vote/dynamic). Owner carries A, member/ward/guest carry M, and M
// permits read -- so every role in the family can read its own inbox, which is also exactly the
// reading GET /dynamics uses (dynamics.go) and keeps one object class on one gate.
//
// An onboarding (family-less) token never reaches this function: the route is outside svcauth's
// bootstrap allowlist, so the middleware answers 403 insufficient_scope first. There is therefore
// no code path here that has to ask what "no session family" means for an inbox.
func GetNotifications(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSTimeCollab, authz.ActionRead) {
		return
	}
	ctx := c.Request.Context()

	limit, ok := pageLimit(c.Query("limit"))
	if !ok {
		badRequest(c, "invalid_limit", "limit 需为正整数（缺省 20，超过 100 按 100 处理；见 homeos.yaml /notifications limit 与 repo 分页策略）")
		return
	}

	// The type filter is validated by the repository, not by a second list of accepted values here:
	// repo.ListNotifications rejects anything outside NotificationTypes with ErrInvalidArgument, which
	// is the single naming authority for the three types (homeos.yaml L503's enum and the DDL CHECK
	// constraint in homeos_0007 agree with it). An unknown type is therefore a 400, never an empty
	// page -- the difference matters because the client renders three tabs off this filter and an
	// empty list would read as "no reminders" rather than "you asked for a type that does not exist".
	rows, nextCursor, err := repo.ListNotifications(
		ctx, s.DB, sess.FamilyID, sess.MemberID,
		optionalQuery(c, "type"), optionalQuery(c, "cursor"), limit,
	)
	if err != nil {
		writePageError(c, s, "notifications_load_failed", err)
		return
	}

	byType, total, err := repo.UnreadNotificationCounts(ctx, s.DB, sess.FamilyID, sess.MemberID)
	if err != nil {
		s.internal(c, "unread_count_failed", err)
		return
	}

	c.JSON(http.StatusOK, NotificationsResponse{
		Items:        notificationCells(rows),
		Unread:       total,
		UnreadByType: unreadByTypeBlock(byType),
		NextCursor:   nextCursor,
	})
}

// ==================== POST /notifications/read ====================

// MarkNotificationsReadRequest is POST's body: the contract's two keys.
//
// `all` is a plain bool, so an absent key is false; that is the contract's own default
// (homeos.yaml L573 `default: false`). L570's description for notification_ids says 「为空则标记全部
// 已读」, which contradicts it -- and the contradiction is not harmless: a `{}` body would otherwise
// clear a whole inbox nobody asked to clear. This handler requires an explicit intention instead
// (see the 400 below) and reports the conflict rather than picking the destructive reading. The
// shipped client always sends both keys (web/src/stores/home.ts markNotificationsRead posts
// {notification_ids, all}), so no existing call path is affected by the narrowing.
type MarkNotificationsReadRequest struct {
	NotificationIDs []string `json:"notification_ids"`
	All             bool     `json:"all"`
}

// MarkNotificationsReadResponse is POST 200's body: the contract's two integers, both computed from
// the database. marked_count is the UPDATE's RowsAffected, i.e. the rows this member's call actually
// flipped -- already-read and non-belongs-to-me ids drop out of the predicate instead of being
// counted or refused, which is what makes a double-tap on 「全部已读」 answer 0 the second time rather
// than an error.
type MarkNotificationsReadResponse struct {
	MarkedCount int64 `json:"marked_count"`
	Unread      int64 `json:"unread"`
}

// MarkNotificationsRead handles POST /api/homeos/notifications/read.
//
// Gate: homeos:time_collab + update. M permits update, so a member/ward/guest may mark messages
// read -- and the write cannot touch anybody else's state because repo.MarkNotificationsRead's
// predicate is `family_id = ? AND member_id = ? AND read_at IS NULL`: the member id is the session's,
// and no request field names a member. 「Nothing here is a write to another member's state」 is a
// property of that WHERE clause, not of the gate.
//
// `all:true` wins over a non-empty id list rather than intersecting with it: the two keys arriving
// together is a client bug, and the reading the contract's own description suggests (ids empty =>
// all) is the one both keys agree on.
func MarkNotificationsRead(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	var req MarkNotificationsReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "请求体需为 JSON：all:true 标记本人全部未读，或 notification_ids 给出具体的本人消息 id")
		return
	}
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSTimeCollab, authz.ActionUpdate) {
		return
	}

	ids := cleanIDs(req.NotificationIDs)
	if !req.All && len(ids) == 0 {
		badRequest(c, "invalid_request",
			"必须显式指定标记范围：all:true（本人全部未读）或非空的 notification_ids；两者皆空时不做任何写入（契约 /notifications/read 的 notification_ids 描述「为空则标记全部已读」与 all 缺省 false 冲突，已按不破坏收件箱的一边实现并上报）")
		return
	}
	if len(ids) > maxMarkReadIDs {
		badRequest(c, "too_many_ids",
			"notification_ids 最多 "+strconv.Itoa(maxMarkReadIDs)+" 个（单页上限 100，多余请用 all:true）")
		return
	}
	ctx := c.Request.Context()

	if req.All {
		// nil ids is repo's 「本人全部未读」 branch -- and only 本人, because member_id is in the predicate.
		ids = nil
	}
	marked, err := repo.MarkNotificationsRead(ctx, s.DB, sess.FamilyID, sess.MemberID, ids)
	if err != nil {
		if errors.Is(err, repo.ErrInvalidArgument) {
			badRequest(c, "invalid_request", err.Error())
			return
		}
		s.internal(c, "notifications_mark_failed", err)
		return
	}

	// The surviving unread count is read back rather than computed as "before - marked": a count the
	// client could not influence is the point (PRD 17.1 第 3 条), and a concurrent write from the bus
	// would otherwise be silently subtracted from a number nobody counted.
	_, unread, err := repo.UnreadNotificationCounts(ctx, s.DB, sess.FamilyID, sess.MemberID)
	if err != nil {
		s.internal(c, "unread_count_failed", err)
		return
	}
	c.JSON(http.StatusOK, MarkNotificationsReadResponse{MarkedCount: marked, Unread: unread})
}

// cleanIDs trims, drops blanks and de-duplicates while preserving order: a repeated id would inflate
// the IN list (and, for the dynamics pair, insert the same receipt twice against the unique index).
func cleanIDs(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// ==================== shared list plumbing (GET /notifications, GET /dynamics) ====================

// optionalQuery returns nil for an absent or blank-after-trim query parameter, which is how "no
// filter / first page" is expressed to the repository: both List functions treat a nil pointer and an
// empty string as "not given", and trimming here keeps `?type=%20` from becoming a filter value that
// matches nothing.
func optionalQuery(c *gin.Context, name string) *string {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil
	}
	return &raw
}

// pageLimit reads ?limit. Absent means 0, which repo.clampLimit turns into this service's default
// (20, capped at 100); a present value must be a positive integer or the request is refused --
// `limit=0` asking for an empty page is a client bug, and silently answering 20 items would hide it.
// Anything above 100 is accepted and clamped by the repository, since the cap is the repo's policy
// rather than a fact about this request.
func pageLimit(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// writePageError maps the repository's paging sentinels onto statuses the client can branch on.
//
// ErrInvalidCursor is a 400 and, importantly, not a fallback to the first page: repo's own doc states
// 「handlers map it to 400 and must NOT fall back to the first page」, because replaying page 1 after a
// stale cursor would make an infinite-scroll list loop.
func writePageError(c *gin.Context, s *Services, internalCode string, err error) {
	switch {
	case errors.Is(err, repo.ErrInvalidCursor):
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_cursor",
			"message": "cursor 非法或已过期：cursor 是上一页最后一条的不透明令牌，请从首页重新加载，不要自行构造或复用（本项目不使用 offset 分页）",
		})
	case errors.Is(err, repo.ErrInvalidArgument):
		// The message is the repo's own (it names the offending enum value), so this file never keeps
		// a second list of accepted values that could disagree with the query it is describing.
		badRequest(c, "invalid_request", err.Error())
	default:
		s.internal(c, internalCode, err)
	}
}

// ==================== mappers ====================

func notificationCells(rows []model.HomeosNotification) []NotificationCell {
	out := make([]NotificationCell, 0, len(rows))
	for _, r := range rows {
		out = append(out, NotificationCell{
			ID:        r.ID,
			Type:      r.Type,
			Content:   r.Content,
			ReadAt:    r.ReadAt,
			CreatedAt: r.CreatedAt,
		})
	}
	return out
}

// unreadByTypeBlock renders the repo's sparse map as the contract's three keys. The repo documents
// the sparseness as its side of the deal (「Only types with at least one unread entry appear in the
// map; callers render the missing three as 0」), and a zero here is a real zero -- the map was
// produced by the same GROUP BY that produced the total.
func unreadByTypeBlock(byType map[string]int64) UnreadByTypeBlock {
	return UnreadByTypeBlock{
		BudgetAlert: byType["budget_alert"],
		System:      byType["system"],
		Reminder:    byType["reminder"],
	}
}
