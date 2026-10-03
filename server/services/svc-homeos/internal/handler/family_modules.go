// family_modules.go is the 面配置 pair: GET and PUT /api/homeos/family/modules
// (contracts/openapi/homeos.yaml L277, PRD 3.4.1 面配置 row, 17.8, 15.3).
//
// Both endpoints read the merge in faces.go, so the 开通页's catalog and the 首页 matrix are two
// renderings of ONE composition (registry 面目录 × homeos_family_module rows × 服务出生 × 角色
// scope=module) rather than two ideas that can drift apart -- that identity is 定版 ⑯'s 五处同源 and is
// asserted in faces_test.go.
//
// Neither endpoint takes a family identifier from the request. familyID, role and member id come from
// the session the auth middleware installed (PRD 15.2 「判定以 family_id 为界」), which is what makes
// "read, or switch, somebody else's household's face config" unreachable.
package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// FamilyModuleItem is one /family/modules entry -- exactly the contract's property set
// (code/name/icon/enabled/visible/born/availability).
//
// `enabled` is the FAMILY reading and `visible` the ROLE reading; the contract keeps them apart
// because PRD 17.8's 停用不隐藏 and 15.3's 角色裁剪 are different facts. `born` is what lets the 开通页
// say 即将上线 without the client consulting a phase table (mine/index.vue tests `!mod.born` first), and
// `availability` is nav §3.3's cell state, both decided in faces.go.
//
// The contract gives no per-item `version`, so none is sent: the lock is the family-level `version`
// below. The granularity mismatch between the two is reported rather than solved by adding an
// undeclared key.
type FamilyModuleItem struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Icon         string `json:"icon"`
	Enabled      bool   `json:"enabled"`
	Visible      bool   `json:"visible"`
	Born         bool   `json:"born"`
	Availability string `json:"availability"`
}

// FamilyModulesResponse is GET's body: the whole catalog with its four flags plus the lock version.
type FamilyModulesResponse struct {
	Modules []FamilyModuleItem `json:"modules"`
	Version int64              `json:"version"`
}

// moduleEpoch folds the per-row optimistic locks into the one integer the contract exposes.
//
// Why a sum instead of a max: homeos_family_module.version is per (family, code) row (0007), while
// homeos.yaml declares a single `version` on both the response and the PUT body. A sum is strictly
// increased by every write to any row -- enabling a new face adds 1, toggling an existing face adds 1
// -- so a client holding an old epoch can never be spuriously accepted, and a client holding the epoch
// it just read is never rejected for an unrelated face's change. A max would silently accept a stale
// epoch whenever the row with the greatest version was untouched, which is the opposite of a lock.
// The row-level predicate inside repo.SetFamilyModuleEnabled remains the real serialization point.
func moduleEpoch(entries []FaceEntry) int64 {
	var epoch int64
	for _, e := range entries {
		epoch += e.Version
	}
	return epoch
}

func moduleItems(entries []FaceEntry) []FamilyModuleItem {
	out := make([]FamilyModuleItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, FamilyModuleItem{
			Code:         e.Code,
			Name:         e.Name,
			Icon:         e.Icon,
			Enabled:      e.Enabled,
			Visible:      e.Visible,
			Born:         e.Born,
			Availability: e.Availability,
		})
	}
	return out
}

// GetFamilyModules handles GET /api/homeos/family/modules.
//
// Read gate: authz's homeos:module_config row (PRD 15.3 「面配置」 is R for member/ward/guest and A for
// owner), so every member sees the same catalog and the same flags their role yields -- 17.8 says the
// 面配置 page is 同源 with the 首页 matrix, and a role that cannot see a face gets visible=false here and
// no cell there.
func GetFamilyModules(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSModuleConfig, authz.ActionRead) {
		return
	}
	entries, err := s.composeFaceEntries(c.Request.Context(), sess.Claims, sess.FamilyID)
	if err != nil {
		s.internal(c, "modules_load_failed", err)
		return
	}
	c.JSON(http.StatusOK, FamilyModulesResponse{
		Modules: moduleItems(entries),
		Version: moduleEpoch(entries),
	})
}

// UpdateFamilyModuleRequest is PUT's body. The contract lists all three fields as required; the
// pointers are how "required" is enforced without rejecting the one legal value a struct field would
// swallow: version 0.
//
// 0 is not a filler -- it is repo.SetFamilyModuleEnabled's 「I expect no row yet」 token, i.e. the only
// way a 半成品家庭 (PRD 17.8 强制选面, no rows at all, epoch 0) can mount its first face. With `int` and
// `binding:"required"`, Go's zero-value rule would make 0 indistinguishable from absent and the
// binding would refuse the exact request the 引导态 page sends.
type UpdateFamilyModuleRequest struct {
	Code    *string `json:"code"`
	Enabled *bool   `json:"enabled"`
	Version *int64  `json:"version"`
}

// UpdateFamilyModuleResponse is PUT 200's body: the new lock epoch and the new permission version.
type UpdateFamilyModuleResponse struct {
	Version int64 `json:"version"`
	PVer    int64 `json:"pver"`
}

// UpdateFamilyModule handles PUT /api/homeos/family/modules (PRD 17.8, 3.4.1, contract L315).
//
// Gate: homeos:module_config + update. In the matrix only owner carries A on that resource, so this is
// the 「仅管理员可调用」 the contract's 403 describes; the refusal goes through svcauth.Require, which
// also writes the 15.5 denied audit row. A non-admin therefore cannot reach the write path at all, and
// the check is on the session's role, never on a role the request claimed.
//
// The write itself is one transaction covering the row, homeos_families.pver, the 15.5 audit row and
// both event envelopes, because 15.6 forbids pver advancing without the change it versions committed
// and 定版 ㉖ requires the module event to publish with it. repo.SetFamilyModuleEnabled opens its own
// transaction; GORM 1.31 nests that through a SAVEPOINT, so passing the handler's tx in keeps one
// commit point rather than two. This is the same shape repo.SetMemberRole uses.
func UpdateFamilyModule(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	var req UpdateFamilyModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "请求体需要 code、enabled、version 三项（契约 required；首次开通某面时 version 传 0）")
		return
	}
	if req.Code == nil || req.Enabled == nil || req.Version == nil {
		badRequest(c, "invalid_request", "请求体需要 code、enabled、version 三项（契约 required；首次开通某面时 version 传 0）")
		return
	}
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSModuleConfig, authz.ActionUpdate) {
		return
	}

	code := strings.TrimSpace(*req.Code)
	ctx := c.Request.Context()

	// The catalog lookup doubles as the code validator: an answer composed from registry cannot be
	// asked about a face registry does not know (PRD 16.1 「命名唯一真源」), so there is no second list
	// of acceptable codes here and no invented face gets a row.
	entries, err := s.composeFaceEntries(ctx, sess.Claims, sess.FamilyID)
	if err != nil {
		s.internal(c, "modules_load_failed", err)
		return
	}
	var target *FaceEntry
	for i := range entries {
		if entries[i].Code == code {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		badRequest(c, "unknown_module", "面 code 未登记在 registry 面目录（PRD 16.1），不可开通或停用")
		return
	}
	// 17.8「可选项只列服务已出生的面」and the same document's 「即将上线」 cell: an unborn face is a
	// READ-ONLY row, so both write directions are refused rather than only enabled=true. 开通 an unborn
	// face would create a row the 首页 can never mount and a 家庭 that pays for a service that does not
	// exist; 停用 one is not a legal act either, because no path can have enabled it -- repo.CreateFamily
	// refuses an unregistered-or-unborn code (identity.go 的 ErrInvalidArgument 分支) and this branch is
	// what keeps PUT from doing it, so a row claiming enabled=true on an unborn face is a state that
	// cannot arise while this handler is the only writer.
	if !target.Born {
		badRequest(c, "face_not_born", fmt.Sprintf("「%s」面服务尚未出生（registry 出生阶段 %s > 当前阶段 %s），面在配置页为只读的「即将上线」项，不可开通也不可停用（PRD 17.8）",
			target.Name, faceBirthPhase(code), registry.CurrentPhase))
		return
	}

	// Lock check, then the row's own version is what the repo's UPDATE predicate uses.
	epoch := moduleEpoch(entries)
	if *req.Version != epoch {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "version_conflict",
			"message": "面配置已被其他成员改动，已取回最新配置，请照着新状态再决定（PRD 17.8 乐观锁）",
			"version": epoch,
		})
		return
	}

	// Switching to the state the face is already in is not a write: no version burned, no pver bump,
	// no event, no audit row -- the same idempotent-replay branch repo.SetMemberRole has, and the
	// answer reports the current numbers so the caller resyncs instead of retrying with a stale epoch.
	if target.Enabled == *req.Enabled {
		var pver int64
		if fam, ferr := repo.GetFamily(ctx, s.DB, sess.FamilyID); ferr == nil {
			pver = fam.PVersion
		} else if !errors.Is(ferr, repo.ErrNoRow) {
			s.internal(c, "family_load_failed", ferr)
			return
		}
		c.JSON(http.StatusOK, UpdateFamilyModuleResponse{Version: epoch, PVer: pver})
		return
	}

	var resp UpdateFamilyModuleResponse
	enabled := *req.Enabled
	txErr := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := repo.SetFamilyModuleEnabled(ctx, tx, sess.FamilyID, code, enabled, target.Version, sess.MemberID)
		if err != nil {
			return err
		}

		pver, err := repo.BumpFamilyPVer(ctx, tx, sess.FamilyID)
		if err != nil {
			return err
		}

		// Re-read inside the transaction so the epoch returned is the one the next GET answers,
		// including the row this write just created.
		after, err := repo.ListFamilyModules(ctx, tx, sess.FamilyID)
		if err != nil {
			return err
		}
		newEpoch := int64(0)
		for _, r := range after {
			newEpoch += r.Version
		}

		state := "停用"
		if enabled {
			state = "开通"
		}
		action := authz.ActionUpdate
		entity := "family_module"
		reason := state + "面 " + code + "（version " + strconv.FormatInt(row.Version, 10) + "，pver " + strconv.FormatInt(pver, 10) + "）"
		audit := s.auditRow(c, sess, model.AuditEventPermissionChange, model.AuditResultAllowed, reason)
		audit.Action = &action
		audit.Entity = &entity
		rowID := row.ID
		audit.EntityID = &rowID
		if err := repo.AppendAudit(ctx, tx, audit); err != nil {
			return err
		}

		// 定版 ㉖: the face's own change event plus the permission-version event. Both envelopes carry
		// business ids the contracts/events/homeos.yaml defines verbatim --
		// 「{family_id}:{code}:{version}」 and 「{family_id}:{pver}」 -- which is what makes the bus's
		// dedup and authz's pver cache invalidation work off them.
		if err := repo.AppendOutboxEnvelope(ctx, tx, sess.FamilyID, "homeos.family.module.updated",
			fmt.Sprintf("%s:%s:%d", sess.FamilyID, code, row.Version), map[string]any{
				"family_id": sess.FamilyID,
				"code":      code,
				"enabled":   enabled,
				"version":   row.Version,
				// enabled_by / enabled_at mirror the row: 停用 keeps both (17.8「停用不删数据」), so the
				// payload says who mounted this face and when, not who just switched it off.
				"enabled_by": row.EnabledBy,
				"enabled_at": row.EnabledAt,
			}); err != nil {
			return err
		}
		if err := repo.AppendOutboxEnvelope(ctx, tx, sess.FamilyID, "homeos.permission.updated",
			fmt.Sprintf("%s:%d", sess.FamilyID, pver), map[string]any{
				"family_id":   sess.FamilyID,
				"pver":        pver,
				"changed_by":  sess.MemberID,
				"change_type": "module",
			}); err != nil {
			return err
		}

		resp = UpdateFamilyModuleResponse{Version: newEpoch, PVer: pver}
		return nil
	})
	if txErr != nil {
		writeModuleError(c, s, txErr)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// writeModuleError maps the repo's sentinels onto the contract's 400 / 409 / 500 answers. The
// distinction matters to the client: 409 means "reload the list, do not resend" (home.ts's
// setModuleEnabled takes exactly that branch), while a 400 is a request the client must fix.
func writeModuleError(c *gin.Context, s *Services, err error) {
	switch {
	case errors.Is(err, repo.ErrLastEnabledModule):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "cannot_disable_last_module",
			"message": "至少保留一个已启用的面：家庭不能处于 0 面状态（PRD 17.8 第 4 条）",
		})
	case errors.Is(err, repo.ErrModuleVersionConflict), errors.Is(err, repo.ErrOptimisticLock):
		c.JSON(http.StatusConflict, gin.H{
			"error":   "version_conflict",
			"message": "面配置已被其他成员改动，已取回最新配置，请照着新状态再决定（PRD 17.8 乐观锁）",
		})
	case errors.Is(err, repo.ErrModuleNotFound):
		// The epoch matched but the row disappeared between the read and the write: another kind of
		// lost race, answered the same way so the client resyncs.
		c.JSON(http.StatusConflict, gin.H{
			"error":   "version_conflict",
			"message": "该面的配置行已变化，请重新载入面配置后再操作",
		})
	case errors.Is(err, repo.ErrInvalidArgument):
		badRequest(c, "invalid_request", err.Error())
	default:
		s.internal(c, "module_update_failed", err)
	}
}

// faceBirthPhase looks up the registry row's 出生阶段 for the 面服务尚未出生 message. The message names
// a phase only ever read from the registry, never from a copy in this file.
func faceBirthPhase(code string) string {
	if d, ok := registry.ByCode(code); ok {
		return d.BirthPhase
	}
	return "未登记"
}
