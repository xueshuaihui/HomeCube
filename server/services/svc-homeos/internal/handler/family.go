// Package handler provides family management handlers for the homeos service.
package handler

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// CreateFamilyRequest is POST /families' body, and its required set is the 定版 one:
// PRD 3.4.1's 接口表 row for `POST /api/homeos/families` reads 「创建家庭（名称、时区、币种、头像）；
// **成功后必须接续强制选面**（`PUT family/modules`，17.8），不允许存在 0 面家庭」, and nav doc §4.1 row ①
// plus §6.11 give page `homeos/auth/family-create` exactly those four fields with
// 「提交后 redirectTo `homeos/family/modules` 的引导态完成强制选面」 as its navigation rule. A required
// `modules` therefore contradicted the frozen request shape and made step ① unpostable -- which is where
// onboarding stopped.
//
// Modules stays ACCEPTED (optional): a client that holds the wizard's two pages in memory may submit
// both at once, and repo.CreateFamily then writes family + owner row + faces in ONE transaction, which
// is the stronger of the two shapes for PRD's 「不允许出现 0 面家庭」. It is not REQUIRED: absent or empty
// means 强制选面 has not happened yet, and 17.8's 「至少启用 1 个面才能完成创建」 is then enforced by the
// two rules that do exist server-side -- 无行即未启用 (a family with no FamilyModule row sees zero faces,
// so there is nothing to skip INTO) and SetFamilyModuleEnabled's refusal to disable the last face (the
// same floor's other end). The 技术方案 S3 row names the remaining server-side backstop for the
// 半成品家庭 window (「鉴权侧按 0 可见面处理、写接口一律拒绝」); that gate belongs to the modules write
// path and the authz side, not to this binding, and its absence is reported rather than papered over by
// a validation rule that breaks the 定版 shape.
type CreateFamilyRequest struct {
	Name     string   `json:"name" binding:"required"`
	Timezone string   `json:"timezone" binding:"required"` // e.g., "Asia/Shanghai"
	Currency string   `json:"currency" binding:"required"` // e.g., "CNY"
	Avatar   string   `json:"avatar,omitempty"`
	Modules  []string `json:"modules,omitempty"` // 选填：17.8 强制选面的两定版入口之一（另一个是 PUT family/modules）
}

// CreateFamilyResponse is 201's body: the new family AND the real session it belongs to.
//
// Returning a token pair is the point of this endpoint in the bootstrap: the caller arrives holding
// only an onboarding token (svcauth.ScopeOnboarding, no fid), and 建家 is what ends that state. Handing
// back the family id alone would force the client to log out and in again -- i.e. it would re-create
// the deadlock this card exists to close, one step later. The pair is signed through the same
// svcauth.Session path login uses, with role and pver read back from the rows just written.
type CreateFamilyResponse struct {
	ID           string `json:"id"`
	FamilyID     string `json:"family_id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	PVer         int64  `json:"pver"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Message      string `json:"message"`
}

// CreateFamily handles POST /api/homeos/families (PRD 3.4.1 家庭核心动作, 17.8 强制选面, 14.5 #2).
//
// Multi-tenancy: no family identifier is read from the request at all. The new family's id is
// generated inside repo.CreateFamily, the owner member row's user_id is the TOKEN's subject, and the
// <=5 families/account cap is counted for that subject (PRD 3.4.1). A caller therefore cannot create
// a family "into" somebody else's account or ask for a particular family id.
func CreateFamily(c *gin.Context, s *Services) {
	var req CreateFamilyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "创建家庭需要 name、timezone、currency（定版第①步入参；modules 选填，见 PRD 3.4.1 与 17.8）")
		return
	}
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		// Wiring bug in this process, never a client error: answer 401 rather than creating a family
		// with an empty owner.
		s.internal(c, "session_unavailable", err)
		return
	}
	codes := make([]string, 0, len(req.Modules))
	for _, m := range req.Modules {
		if code := strings.TrimSpace(m); code != "" {
			codes = append(codes, code)
		}
	}
	// An absent field, an empty list and a list of blanks all mean the same thing here: 强制选面 has not
	// been answered yet. None of them is a client error -- they are the 定版 step-① body -- and the
	// family that results is PRD's 半成品家庭 (无行即未启用, so it sees 0 faces), whose next and only
	// usable step is the 引导态 of homeos/family/modules.

	ctx := c.Request.Context()
	familyID, memberID, err := repo.CreateFamily(ctx, s.DB, repo.Actor{
		AccountID: sess.AccountID,
		MemberID:  memberIDOf(sess),
		IP:        c.ClientIP(),
		UserAgent: truncatedUA(c),
	}, strings.TrimSpace(req.Name), strings.TrimSpace(req.Timezone), strings.TrimSpace(req.Currency),
		strings.TrimSpace(req.Avatar), codes)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrInvalidArgument):
			// Includes 未出生 faces (PRD 17.8「可选项只列服务已出生的面」) -- the registry is the
			// authority, so the message quotes the repo's own reason rather than a hand-listed set.
			badRequest(c, "invalid_modules", err.Error())
		case errors.Is(err, repo.ErrFamilyLimitReached):
			c.JSON(http.StatusConflict, gin.H{
				"error":   "family_limit_reached",
				"code":    "family_limit_reached",
				"message": "一个账号最多归属 5 个家庭（PRD 3.4.1），请先退出或解散一个家庭",
			})
		default:
			s.internal(c, "family_create_failed", err)
		}
		return
	}

	// Read the session back rather than assembling it: role and pver must come from the rows the
	// transaction wrote (PRD 15.6「pver 取自家庭行」), not from a constant this handler assumed. The
	// 5-family cap and the owner row are both already committed at this point.
	next, err := s.familySession(ctx, sess.AccountID, familyID, memberID)
	if err != nil {
		s.internal(c, "family_session_failed", err)
		return
	}
	tokens, err := s.signTokens(*next)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	if err := repo.SaveRefreshToken(ctx, s.DB, next.AccountID, tokens.refreshHash, tokens.refreshExp); err != nil {
		s.internal(c, "refresh_token_store_failed", err)
		return
	}

	c.JSON(http.StatusCreated, CreateFamilyResponse{
		ID:           familyID,
		FamilyID:     familyID,
		Name:         req.Name,
		Role:         next.Role,
		PVer:         next.PVersion,
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    tokens.expiresIn,
		// The 引导 flow's last word: when this request carried the faces, 17.8's 强制选面 happened here
		// and the client lands on the new family's 首页 (nav doc §4.1 row ②). When it did not, the
		// family is a 半成品家庭 and the honest copy says so -- the next page is the 引导态 of
		// homeos/family/modules, not 首页 (nav §6.11 「至少勾选 1 个面 → 确认 → 家庭创建完成」).
		Message: familyCreatedMessage(codes),
	})
}

// familyCreatedMessage states, in the response, which half of the 建家引导 this request completed. It
// is copy, not enforcement -- the enforcement is 无行即未启用 plus SetFamilyModuleEnabled's floor.
func familyCreatedMessage(codes []string) string {
	if len(codes) == 0 {
		return "家庭已创建。强制选面尚未完成：请至少启用 1 个功能面（PRD 17.8，该步骤不可跳过）"
	}
	return "家庭创建成功，已启用 " + strings.Join(codes, "、") + " 面"
}

// memberIDOf keeps an existing member id when one is known; for a建家 call from an onboarding session
// there is none yet (the family's owner row is created by this call), and repo.CreateFamily leaves
// homeos_families.owner_id pointing at the account, which is what 0006's DDL asks for.
func memberIDOf(sess *svcauth.Session) string {
	if sess == nil {
		return ""
	}
	return sess.MemberID
}

// truncatedUA mirrors the audit sink's guard: user_agent is varchar(500), and an over-long header
// must not turn an audited write into a constraint violation.
func truncatedUA(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	ua := c.Request.UserAgent()
	if len(ua) > 500 {
		ua = ua[:500]
	}
	return ua
}

// familySession rebuilds a session for (account, family) from the rows that exist NOW, so every
// post-write token carries the family's current role and pver instead of the caller's stale ones.
// The member id stays whatever the caller already resolved (repo.CreateFamily returns it for a new
// family), because that is the row the token's role was read from.
//
// repo.ErrNoRow / a missing membership are returned as errors: a token must never be signed for a
// family the account does not occupy (PRD 15.2), so "the read disagrees with the write" is a fault to
// report, not a state to paper over.
func (s *Services) familySession(ctx context.Context, accountID, familyID, memberID string) (*svcauth.Session, error) {
	memberships, err := repo.ListFamiliesForAccount(ctx, s.DB, accountID)
	if err != nil {
		return nil, err
	}
	for _, m := range memberships {
		if m.ID != familyID {
			continue
		}
		if memberID == "" {
			memberID = m.MemberID
		}
		return &svcauth.Session{
			AccountID: accountID,
			FamilyID:  m.ID,
			MemberID:  memberID,
			Role:      m.Role,
			PVersion:  m.PVersion,
		}, nil
	}
	return nil, fmt.Errorf("%w: account %s has no member row in family %s", repo.ErrAccountNotInFamily, accountID, familyID)
}

// AcceptInviteRequest is POST /family/invite/accept's body.
type AcceptInviteRequest struct {
	InviteCode string `json:"invite_code" binding:"required"`
}

// AcceptInviteResponse reports the family joined plus a token pair for it.
//
// The pair is what makes 「加入已有家庭不重新选面」(PRD 17.8) work in one round trip: after accepting,
// the caller's session family IS the joined family, carrying the role the invitation granted and the
// family's pver read back from homeos_families -- not from the invitation row.
type AcceptInviteResponse struct {
	FamilyID     string `json:"family_id"`
	FamilyName   string `json:"family_name"`
	Role         string `json:"role"`
	PVer         int64  `json:"pver"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Message      string `json:"message"`
}

// AcceptInvite handles POST /api/homeos/family/invite/accept (PRD 3.4.1 邀请、18.2#1).
//
// The write goes through repo.AcceptInvitation, which owns the four things the previous inline version
// got wrong or skipped: it re-checks the three-state (pending) AND the expiry inside the transaction,
// enforces the 12-member cap and the <=5-families-per-account cap (PRD 3.4.1, 18.1), flips the row to
// accepted, bumps homeos_families.pver and writes the permission_change audit row -- all atomically.
// The old version inserted a member row, ignored every cap, and marked the invitation accepted with a
// fire-and-forget update whose error was discarded.
func AcceptInvite(c *gin.Context, s *Services) {
	var req AcceptInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 invite_code 字段")
		return
	}
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}

	ctx := c.Request.Context()
	familyID, role, err := repo.AcceptInvitation(ctx, s.DB, strings.TrimSpace(req.InviteCode), sess.AccountID, repo.Actor{
		AccountID: sess.AccountID,
		MemberID:  sess.MemberID,
		IP:        c.ClientIP(),
		UserAgent: truncatedUA(c),
	})
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrNoRow):
			// repo looks the code up under status='pending', so an accepted, revoked or unknown code all
			// answer the same 404 -- which is also the leak-free shape: a caller cannot probe which codes
			// were once live in somebody else's family.
			c.JSON(http.StatusNotFound, gin.H{"error": "invite_not_found", "message": "邀请码无效或已过期"})
		case errors.Is(err, repo.ErrInviteExpired):
			c.JSON(http.StatusConflict, gin.H{"error": "invite_expired", "message": "邀请已过期，请向管理员重新索取"})
		case errors.Is(err, repo.ErrMemberCapReached):
			c.JSON(http.StatusConflict, gin.H{"error": "member_cap_reached", "message": "该家庭已达 12 人上限（PRD 18.1）"})
		case errors.Is(err, repo.ErrFamilyLimitReached):
			c.JSON(http.StatusConflict, gin.H{"error": "family_limit_reached", "message": "一个账号最多归属 5 个家庭（PRD 3.4.1）"})
		default:
			s.internal(c, "invite_accept_failed", err)
		}
		return
	}

	next, err := s.familySession(ctx, sess.AccountID, familyID, "")
	if err != nil {
		s.internal(c, "family_session_failed", err)
		return
	}

	tokens, err := s.signTokens(*next)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	if err := repo.SaveRefreshToken(ctx, s.DB, next.AccountID, tokens.refreshHash, tokens.refreshExp); err != nil {
		s.internal(c, "refresh_token_store_failed", err)
		return
	}

	c.JSON(http.StatusOK, AcceptInviteResponse{
		FamilyID:     familyID,
		FamilyName:   nextFamilyName(ctx, s.DB, familyID),
		Role:         role,
		PVer:         next.PVersion,
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    tokens.expiresIn,
		Message:      "已加入家庭，当前会话家庭已切换",
	})
}

// nextFamilyName reads the joined family's display name for the confirmation copy. An empty string is
// acceptable: the join already committed, and a name lookup must not turn a success into a 500.
func nextFamilyName(ctx context.Context, db *gorm.DB, familyID string) string {
	f, err := repo.GetFamily(ctx, db, familyID)
	if err != nil || f == nil {
		return ""
	}
	return f.Name
}

// ListFamiliesResponse is GET /families' body: the caller's own account-family list, which is what
// web/src/stores/home.ts reads as 「角色快照的唯一来源」.
type ListFamiliesResponse struct {
	Families []FamilyInfo `json:"families"`
}

// ListFamilies handles GET /api/homeos/families.
//
// Account-scoped, not family-scoped: the only key is the token's sub, and no family id is taken from
// the request, which is why this read is on the onboarding allowlist while /families/{id}/... is not.
// A removed membership is excluded (m.deleted_at IS NULL) and the role is this account's role in THAT
// family, so a caller in two families sees its own two different roles.
func ListFamilies(c *gin.Context, s *Services) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	rows, err := repo.ListFamiliesForAccount(c.Request.Context(), s.DB, sess.AccountID)
	if err != nil {
		s.internal(c, "family_list_failed", err)
		return
	}
	c.JSON(http.StatusOK, ListFamiliesResponse{Families: familyInfos(rows)})
}

// SwitchFamilyRequest is POST /family/switch's body.
type SwitchFamilyRequest struct {
	FamilyID string `json:"family_id" binding:"required"`
}

// SwitchFamilyResponse is the rotated pair plus the new session family's snapshot.
type SwitchFamilyResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	FamilyID     string `json:"family_id"`
	Role         string `json:"role"`
	PVer         int64  `json:"pver"`
	Message      string `json:"message"`
}

// SwitchFamily handles POST /api/homeos/family/switch (PRD 14.5 #2: 切换家庭并在一秒内重发令牌).
//
// The caller's identity comes from the auth middleware's session, not from a token the client hands
// over, and the target family is checked against homeos_members for THAT account: 切换 therefore means
// "become the member you already are elsewhere", never "point the token at a family id I choose"
// (PRD 15.2「判定以 family_id 为界」). The target's role and pver are read fresh, so the new token
// snapshots the target family's own permission version (PRD 15.6) instead of the previous family's.
//
// The old refresh token is deliberately NOT revoked: homeos_refresh_tokens (0006) has no family_id
// column, so revoking "this account's sessions" would also log the same person out of every other
// family they belong to, which PRD 3.4.1 explicitly allows (<=5). The old ACCESS token dies with its
// own 15-minute expiry. Reported as the same schema gap repo.RemoveMember's comment names.
func SwitchFamily(c *gin.Context, s *Services) {
	var req SwitchFamilyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 family_id 字段")
		return
	}

	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		// An unauthenticated switch is a wiring bug in this process, never a client error: answer 401
		// and audit the attempt rather than switching families with an empty subject.
		if s.Logger != nil {
			s.Logger.Error("family: 切换家庭时鉴权中间件未运行", "err", err.Error(), "path", c.Request.URL.Path)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "登录状态无效或不属于当前家庭"})
		return
	}
	if sess.IsOnboarding() {
		// Second gate behind svcauth.Middleware's allowlist (which already refuses this route for an
		// onboarding token): 切换家庭 presupposes a current family, and a handler that proceeded with an
		// empty sess.FamilyID would be one wiring bug away from a cross-family write.
		s.auditDenied(c, sess, model.AuditEventCrossFamilyAttempt, "onboarding 会话尝试切换家庭")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "code": "no_family",
			"message": "当前会话尚无家庭，无可切换的家庭"})
		return
	}

	ctx := c.Request.Context()
	next, err := s.familySession(ctx, sess.AccountID, strings.TrimSpace(req.FamilyID), "")
	if err != nil {
		if errors.Is(err, repo.ErrAccountNotInFamily) {
			// PRD 15.2: a family the account does not occupy is not switched into, and 15.5 wants the
			// attempt recorded.
			s.auditDenied(c, sess, model.AuditEventCrossFamilyAttempt, "切换家庭请求了非本人所属的家庭 "+req.FamilyID)
			c.JSON(http.StatusForbidden, gin.H{"error": "not_family_member", "message": "您不是该家庭成员"})
			return
		}
		s.internal(c, "membership_lookup_failed", err)
		return
	}
	if next.FamilyID == sess.FamilyID {
		// Same family: re-issuing a pair would rotate a refresh row for no state change, so the call
		// is answered honestly instead of pretending to switch. No tokens are returned, and the client
		// that sees 未签发 keeps its current pair -- which is also what makes a double tap harmless.
		c.JSON(http.StatusOK, SwitchFamilyResponse{
			FamilyID: next.FamilyID,
			Role:     next.Role,
			PVer:     next.PVersion,
			Message:  "当前已在该家庭，未签发新令牌",
		})
		return
	}

	tokens, err := s.signTokens(*next)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	if err := repo.SaveRefreshToken(ctx, s.DB, next.AccountID, tokens.refreshHash, tokens.refreshExp); err != nil {
		s.internal(c, "refresh_token_store_failed", err)
		return
	}

	c.JSON(http.StatusOK, SwitchFamilyResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    tokens.expiresIn,
		FamilyID:     next.FamilyID,
		Role:         next.Role,
		PVer:         next.PVersion,
		Message:      "切换家庭成功",
	})
}

// CreateInviteRequest is POST /families/{family_id}/invites' body.
type CreateInviteRequest struct {
	Role          string `json:"role" binding:"required,oneof=owner member ward guest"`
	InviteePhone  string `json:"invitee_phone,omitempty"`
	ExpiresInDays int    `json:"expires_in_days"` // default 7 days
}

// CreateInviteResponse answers with the invitation code and metadata.
type CreateInviteResponse struct {
	ID           string    `json:"id"`
	FamilyID     string    `json:"family_id"`
	Code         string    `json:"code"`
	Role         string    `json:"role"`
	InviteePhone string    `json:"invitee_phone,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	InviteLink   string    `json:"invite_link"` // deep link for QR code
}

// CreateInvite handles POST /api/homeos/families/{family_id}/invites (PRD 3.4.1 邀请成员).
//
// Only family owners can create invitations (PRD 15.3 「面配置」+「成员管理」). The invite code is a
// random 8-character string, valid for 7 days by default. The three-state discipline (pending/accepted/expired)
// is enforced by repo.AcceptInvitation at consume time.
func CreateInvite(c *gin.Context, s *Services) {
	familyID := c.Param("family_id")
	if strings.TrimSpace(familyID) == "" {
		badRequest(c, "invalid_request", "需要 family_id 路径参数")
		return
	}

	var req CreateInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 role 字段（owner/member/ward/guest）")
		return
	}

	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}

	// Enforce owner-only for creating invites (PRD 15.3)
	if sess.Role != authz.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "只有家庭管理员可以创建邀请",
		})
		return
	}

	ctx := c.Request.Context()

	// Verify the inviter is actually in this family
	memberAccountID := sess.AccountID
	var membership model.HomeosMember
	if err := s.DB.WithContext(ctx).Where("family_id = ? AND user_id = ?", familyID, memberAccountID).First(&membership).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "not_family_member",
				"message": "您不是该家庭成员",
			})
			return
		}
		s.internal(c, "member_lookup_failed", err)
		return
	}

	// Generate invite code (8-char random string)
	code := generateInviteCode()

	// Calculate expiry
	expiresIn := req.ExpiresInDays
	if expiresIn <= 0 {
		expiresIn = 7
	}
	expiresAt := time.Now().UTC().Add(time.Duration(expiresIn) * 24 * time.Hour)

	// Create invitation row
	now := time.Now().UTC()
	inviteID := uuid.New().String()
	var inviteePhone *string
	if strings.TrimSpace(req.InviteePhone) != "" {
		inviteePhone = &req.InviteePhone
	}
	invite := model.HomeosInvitation{
		ID:             inviteID,
		FamilyID:       familyID,
		Code:           code,
		Role:           req.Role,
		InviterID:      sess.MemberID,
		InviteePhone:   inviteePhone,
		Status:         "pending",
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.DB.WithContext(ctx).Create(&invite).Error; err != nil {
		slog.Error("invite_create_failed", "err", err.Error(), "family_id", familyID, "code", code)
		s.internal(c, "invite_create_failed", err)
		return
	}

	// Build deep link for QR code scanning
	inviteLink := fmt.Sprintf("homecube://invite/%s", code)

	c.JSON(http.StatusCreated, CreateInviteResponse{
		ID:           inviteID,
		FamilyID:     familyID,
		Code:         code,
		Role:         req.Role,
		InviteePhone: req.InviteePhone,
		ExpiresAt:    expiresAt,
		CreatedAt:    now,
		InviteLink:   inviteLink,
	})
}

// generateInviteCode produces an 8-character random code (uppercase + digits).
func generateInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I/O/0/1 to avoid confusion
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// ListInvites handles GET /api/homeos/families/{family_id}/invites.
func ListInvites(c *gin.Context, s *Services) {
	familyID := c.Param("family_id")
	if strings.TrimSpace(familyID) == "" {
		badRequest(c, "invalid_request", "需要 family_id 路径参数")
		return
	}

	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}

	// Only owners can list invites
	if sess.Role != authz.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "只有家庭管理员可以查看邀请列表",
		})
		return
	}

	ctx := c.Request.Context()
	var invites []model.HomeosInvitation
	if err := s.DB.WithContext(ctx).Where("family_id = ?", familyID).Order("created_at DESC").Find(&invites).Error; err != nil {
		s.internal(c, "invite_list_failed", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": invites})
}

// RevokeInvite handles DELETE /api/homeos/families/{family_id}/invites/{invite_id} (PRD 3.4.1 撤销邀请).
func RevokeInvite(c *gin.Context, s *Services) {
	familyID := c.Param("family_id")
	inviteID := c.Param("invite_id")
	if strings.TrimSpace(familyID) == "" || strings.TrimSpace(inviteID) == "" {
		badRequest(c, "invalid_request", "需要 family_id 和 invite_id 路径参数")
		return
	}

	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}

	// Only owners can revoke invites
	if sess.Role != authz.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden",
			"message": "只有家庭管理员可以撤销邀请",
		})
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()

	// Update invitation status to expired
	result := s.DB.WithContext(ctx).Model(&model.HomeosInvitation{}).
		Where("id = ? AND family_id = ? AND status = ?", inviteID, familyID, "pending").
		Updates(map[string]interface{}{
			"status":     "expired",
			"updated_at": now,
		})

	if result.Error != nil {
		s.internal(c, "invite_revoke_failed", result.Error)
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "invite_not_found",
			"message": "邀请不存在或已失效",
		})
		return
	}

	// Audit log
	reason := "撤销邀请 " + inviteID
	s.writeAudit(ctx, s.auditRow(c, sess, model.AuditEventPermissionChange, model.AuditResultAllowed, reason))

	c.JSON(http.StatusOK, gin.H{
		"message": "邀请已撤销",
	})
}
