// dynamics.go is 动态流's pair: GET /api/homeos/dynamics and
// POST /api/homeos/dynamics/read (docs/prd-homecube.md 3.7 动态流 + 17.2 D 区 + 15.3 row 3
// 「homeos:time_collab」, whose comment names dynamic as one of that row's objects;
// 17.1 第 2 条 and 15.2 for the read-state boundary).
//
// Item shape: this endpoint reuses handler.DynamicCell, the very type home/summary's D 区 serializes
// (home_summary.go L141), so the 首页's 20-row strip and the 动态流 page's full feed are two renderings
// of ONE row projection -- {id, code, actor_name, action, summary, at}. That identity is deliberate:
// two mappers over the same table is how a feed starts answering 首页 and 动态流 page differently about
// the same event, which is the same 同源 defect 定版 ⑯ closes for the 面目录. It is asserted across the
// HTTP boundary in dynamics_test.go by comparing the two responses' keys.
//
// It does NOT reuse home_summary.go's summaryDynamics helper: that function truncates to
// summaryDynamicLimit (20) because the contract pins D 区 at maxItems 20, while GET /dynamics may be
// asked for up to repo.clampLimit's 100. Truncating a page here would drop rows the cursor then skips
// forever, so the page mapper below maps all rows and leaves paging to the repository.
//
// Read state is per MEMBER, which is why homeos_dynamic_read exists at all (PRD 17.1 第 2 条, 15.2): a
// boolean on homeos_dynamic would let one member's 「全部已读」 clear the 未读 for the whole family, i.e.
// a write to somebody else's state through a read endpoint. Every unread predicate is therefore
// 「this member has no receipt for this row」:
//
//	repo.unreadDynamicsCondition ->
//	  id NOT IN (SELECT dynamic_id FROM homeos_dynamic_read WHERE member_id = ?)
//
// and the ? is sess.MemberID, taken from the token the middleware verified -- never from a parameter
// or a body field. There is no way to ask about another member's 未读, and no way to write a receipt
// for one.
//
// No offset paging: cursor only, opaque base64url(RFC3339Nano~id), (at DESC, id DESC).
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// ==================== response shapes ====================

// DynamicsResponse is GET's body: the contract block this card adds to homeos.yaml.
//
// No `unread` key. The 未读 for the feed is answered by POST /dynamics/read (which must return it per
// the contract block this file adds) and by nothing else; a list endpoint that also carried a count
// would give the client two numbers to keep in sync, and PRD 17.1 第 2 条 names exactly one caliber
// per thing counted.
type DynamicsResponse struct {
	Items      []DynamicCell `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

// ==================== GET /dynamics ====================

// GetDynamics handles GET /api/homeos/dynamics.
//
// Gate: homeos:time_collab + read -- the same row and the same action GET /notifications gates on,
// because the feed and the inbox are one object class in PRD 15.3's row 3. Owner carries A, and
// member/ward/guest carry M, which permits read: 17.2 makes the 动态流 a family-shared stream, so
// refusing a 儿童 or 访客 the feed would be a reading the matrix does not support.
//
// ?code= is the 面筛选 (17.8's fifth consumer of the face set). Two facts decide its treatment:
//
//   - the filter OPTION LIST is server-derived and lives in home/summary's faces[] (mountedFaces --
//     registry 面目录 × homeos_family_module × 服务出生 × 角色 scope, all four in faces.go). The page
//     builds its chips from web/src/stores/home.ts faces, so 未出生面 and 未挂载面 never appear as
//     options and there is nothing here to hide.
//   - the filter VALUE is validated against registry, the 「全文唯一命名权威」 (PRD 16.1), so an
//     invented code is refused with 400 rather than answering a page that quietly proves nothing is
//     named that. A registered-but-unborn or 停用 code is NOT refused: 17.8 「停用不删数据」 means a
//     face's historical events must stay reachable after it is switched off, and the only writer this
//     checkout has stamps code="homeos" (repo.CreateFamily), which is not a mountable 面 at all -- so
//     "must be mounted" would delete the one real row from the feed.
func GetDynamics(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSTimeCollab, authz.ActionRead) {
		return
	}

	code := optionalQuery(c, "code")
	if code != nil {
		if _, known := registry.ByCode(*code); !known {
			badRequest(c, "unknown_module", "code 未登记在 registry 面目录（PRD 16.1 命名唯一真源），不可作为动态流筛选项；可选筛选项请取 home/summary 的 faces[].code")
			return
		}
	}

	limit, ok := pageLimit(c.Query("limit"))
	if !ok {
		badRequest(c, "invalid_limit", "limit 需为正整数（缺省 20，超过 100 按 100 处理；见 repo 分页策略）")
		return
	}

	ctx := c.Request.Context()
	// onlyUnread=false: 「只看未读」 is not part of the body/parameter shapes this card was given
	// (GET /dynamics takes code, cursor, limit), so repo's unread subset and MarkAllDynamicsRead's
	// per-face narrowing stay uncalled here -- reported as an unused repo capability rather than
	// widened into the contract unasked.
	rows, nextCursor, err := repo.ListDynamics(
		ctx, s.DB, sess.FamilyID, sess.MemberID, code, false,
		optionalQuery(c, "cursor"), limit,
	)
	if err != nil {
		writePageError(c, s, "dynamics_load_failed", err)
		return
	}
	c.JSON(http.StatusOK, DynamicsResponse{
		Items:      dynamicPageCells(rows),
		NextCursor: nextCursor,
	})
}

// ==================== POST /dynamics/read ====================

// MarkDynamicsReadRequest is POST's body.
//
// Like the notifications pair, `all:false` plus an empty id list is refused instead of being read as
// "mark everything": the two keys are the contract's, and the destructive interpretation of a blank
// one is not worth an ambiguity the client never exercises (web/src/stores/home.ts markDynamicsRead
// always sends both).
type MarkDynamicsReadRequest struct {
	DynamicIDs []string `json:"dynamic_ids"`
	All        bool     `json:"all"`
}

// MarkDynamicsReadResponse is POST 200's body. `unread` is the DYNAMIC unread for this member after
// the write, re-counted by the repository -- never derived from how many ids the caller sent. It is
// not the message-center count PRD 17.1 第 2 条 defines as 未读口径 (that one is
// GET /notifications' `unread`, which home/summary reads from the same function); the client currently
// writes both into one store, which is a 定版冲突 reported rather than papered over here.
type MarkDynamicsReadResponse struct {
	MarkedCount int64 `json:"marked_count"`
	Unread      int64 `json:"unread"`
}

// MarkDynamicsRead handles POST /api/homeos/dynamics/read.
//
// Gate: homeos:time_collab + update (M permits update), i.e. any family member may mark the feed
// read -- and only for themselves. The two repo writes behind this handler touch nothing else:
// MarkDynamicRead inserts one homeos_dynamic_read row whose member_id is the session's (unique index
// (dynamic_id, member_id) + ON CONFLICT DO NOTHING, so re-sending the same ids is a no-op rather than
// a duplicate receipt), and MarkAllDynamicsRead selects exactly the rows this member has no receipt
// for. Neither statement can name another member, because the member id is a session value.
//
// One transaction covers the count-before, the writes, and the count-after, so marked_count and
// unread describe the same instant and a mid-request failure leaves no half-applied receipt set.
// repo's two functions open transactions of their own; GORM 1.31 nests those through a SAVEPOINT when
// handed the handler's tx, which is the same shape UpdateFamilyModule uses.
func MarkDynamicsRead(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	var req MarkDynamicsReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "请求体需为 JSON：all:true 标记本人全部未读，或 dynamic_ids 给出具体的动态 id")
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

	ids := cleanIDs(req.DynamicIDs)
	if !req.All && len(ids) == 0 {
		badRequest(c, "invalid_request", "必须显式指定标记范围：all:true（本人全部未读）或非空的 dynamic_ids；两者皆空时不做任何写入")
		return
	}
	if len(ids) > maxMarkReadIDs {
		badRequest(c, "too_many_ids", "dynamic_ids 最多 "+strconv.Itoa(maxMarkReadIDs)+" 个（单页上限 100，多余请用 all:true）")
		return
	}
	ctx := c.Request.Context()

	var resp MarkDynamicsReadResponse
	txErr := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Counted over the same predicate the writes use (family + this member + no receipt), so the
		// two numbers below are two reads of one caliber rather than a read and a guess.
		before, err := repo.CountUnreadDynamics(ctx, tx, sess.FamilyID, sess.MemberID, nil)
		if err != nil {
			return err
		}

		var marked int64
		if req.All {
			// nil code: the whole stream, which is what this contract body expresses -- there is no
			// per-face 全部已读 in the request shape this card was given.
			marked, err = repo.MarkAllDynamicsRead(ctx, tx, sess.FamilyID, sess.MemberID, nil)
			if err != nil {
				return err
			}
		} else {
			for _, id := range ids {
				// Family-scoped existence check inside: an id from another family, or one that never
				// existed, is ErrDynamicNotFound, which aborts the whole request -- so a typo cannot
				// turn "mark these three" into a silent two-of-three.
				if err := repo.MarkDynamicRead(ctx, tx, sess.FamilyID, sess.MemberID, id); err != nil {
					return err
				}
			}
		}

		after, err := repo.CountUnreadDynamics(ctx, tx, sess.FamilyID, sess.MemberID, nil)
		if err != nil {
			return err
		}
		if !req.All {
			// Receipts already present are the no-op case, so the honest figure is what disappeared
			// from the unread set. A concurrent event could add a row between the two counts; the
			// clamp keeps a negative off the wire rather than letting it read as a huge count.
			marked = before - after
			if marked < 0 {
				marked = 0
			}
		}
		resp = MarkDynamicsReadResponse{MarkedCount: marked, Unread: after}
		return nil
	})
	if txErr != nil {
		switch {
		case errors.Is(txErr, repo.ErrDynamicNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "dynamic_not_found",
				"message": "dynamic_ids 含不存在或不属于当前家庭的动态 id，本次未写入任何已读回执",
			})
		case errors.Is(txErr, repo.ErrInvalidArgument):
			badRequest(c, "invalid_request", txErr.Error())
		default:
			s.internal(c, "dynamics_mark_failed", txErr)
		}
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ==================== mapper ====================

// dynamicPageCells projects feed rows onto the SAME DynamicCell home/summary emits, without truncating
// (see this file's header for why summaryDynamics is not reused). `at` is the event instant the writer
// froze (0007 「人名与整句都在写入时定稿」), which is what 17.2's 「文案由服务端拼好整句下发」 and the
// 深链's entity columns are built around; entity / entity_id stay out of the response for the same
// reason D 区 keeps them out -- they are routing parameters, not render fields.
func dynamicPageCells(rows []model.HomeosDynamic) []DynamicCell {
	out := make([]DynamicCell, 0, len(rows))
	for _, r := range rows {
		out = append(out, DynamicCell{
			ID:        r.ID,
			Code:      r.Code,
			ActorName: r.ActorName,
			Action:    r.Action,
			Summary:   r.Summary,
			At:        r.At,
		})
	}
	return out
}
