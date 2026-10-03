// Package handler provides the identity handlers of the homeos service: the SMS verification-code
// endpoint, the login / refresh / logout trio, and the audit sink the auth middleware writes through.
//
// Shape of this file is decided by three documents, not by taste:
//   - contracts/openapi/homeos.yaml /auth/login (L111-158) and /auth/refresh (L160-194): the request
//     and response field names. The contract wins over what the shipped client currently sends, and
//     the divergence is reported rather than silently patched (see LoginResponse's two extra fields).
//   - PRD 3.4.1 + PRD 15.6: a token carries family_id, a role snapshot and pver, and is signed by
//     svc-homeos alone -- which is svcauth.Signer, so this file contains no jwt code at all.
//   - PRD 21.5: 登录 is one of the nine audited event classes, so the login path writes an audit row
//     (through the sink below) and the middleware's refusals use the same sink.
//
// What used to be here and is now gone: a hardcoded HS256 secret passed to an RS256 header (every
// POST /auth/login answered 500), a fabricated 「示例家庭 / family-001」, and a Logout that returned
// success without touching the database.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// Services is the identity side's dependency bundle: the handle, the only signer in the deployment,
// the SMS channel behind its seam, and the logger the audit sink degrades to.
//
// Why a bundle instead of the previous (c, db) pair: the login path needs three collaborators, and
// passing a *gorm.DB alone is how the previous revision ended up inventing a user, a family and a
// signing key inside the handler. Handlers that only read business rows keep the (c, db) signature.
type Services struct {
	DB     *gorm.DB
	Signer *svcauth.Signer
	SMS    svcauth.SMSProvider
	Logger *slog.Logger

	// RoutePrefix is this process's mount prefix (obs.Config.RoutePrefix, i.e. "/api/homeos" per
	// registry's 路由 rule and cmd's `svc.Engine.Group(d.RoutePrefix)`). It is a field rather than a
	// literal because the onboarding allowlist must be built from the SAME string the router used --
	// a hard-coded prefix in the middleware would silently deny POST /families under any rename, and
	// deny-closed is the safe direction but a broken bootstrap.
	RoutePrefix string
}

// phonePattern is P1's account key: a mainland mobile number, which is what 手机号验证码登录 (PRD
// 3.4.1) authenticates and what the client's login form already enforces (11 digits). The documents
// define no phone format, so this is a card-level choice and is reported as one; homeos_users.phone
// is varchar(20), which is the only stored constraint.
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

// ==================== POST /auth/sms-code ====================

// SMSCodeRequest is POST /auth/sms-code's body. The endpoint has no block in
// contracts/openapi/homeos.yaml (reported); the field names are PRD 3.7's and the client's.
type SMSCodeRequest struct {
	Phone string `json:"phone" binding:"required"`
}

// SMSCodeResponse answers with the window the stored code is valid for and which channel answered.
//
// The previous revision returned the code itself in the body ("For testing only"). With the code now
// behind svcauth.SMSProvider there is no reason to widen that: the P1 fixed code is a documented
// constant (packages/adapter/DEPENDENCIES.md 短信行, and the login page prints it), while a response
// body carrying the code is a shape every future real provider would inherit by accident.
type SMSCodeResponse struct {
	ExpiresIn int    `json:"expires_in"`
	Channel   string `json:"channel"`
	Message   string `json:"message"`
}

// SendSMSCode handles POST /api/homeos/auth/sms-code (PRD 3.7 认证 row: 验证码发送, 按 21.4 限流).
//
// The code comes from the provider seam and is then stored, because a code that is not stored cannot
// expire, cannot be single-use and cannot tell a fresh one from a replayed one -- which is exactly
// what repo.ConsumeSMSCode enforces on the login side.
func SendSMSCode(c *gin.Context, s *Services) {
	var req SMSCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "缺少或非法的 phone 字段")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if !phonePattern.MatchString(phone) {
		badRequest(c, "invalid_phone", "手机号格式不正确（P1 仅支持 11 位大陆手机号）")
		return
	}

	ctx := c.Request.Context()
	code, ttl, err := s.SMS.SendCode(ctx, phone)
	if err != nil {
		s.internal(c, "sms_channel_unavailable", err)
		return
	}
	if err := repo.IssueSMSCode(ctx, s.DB, phone, code, ttl); err != nil {
		s.internal(c, "sms_code_store_failed", err)
		return
	}

	c.JSON(http.StatusOK, SMSCodeResponse{
		ExpiresIn: int(ttl.Seconds()),
		Channel:   s.SMS.Name(),
		Message:   "验证码已发送，有效期 " + fmt.Sprintf("%d", int(ttl.Minutes())) + " 分钟；当前通道 " + s.SMS.Name(),
	})
}

// ==================== POST /auth/login ====================

// LoginRequest is /auth/login's body: contracts/openapi/homeos.yaml L125 declares
// required [phone, code] with an optional family_id.
//
// The field is "code", not the "sms_code" the handler used to bind: 定版冲突已上报 -- the shipped
// login page (web/src/pages/homeos/auth/login.vue) posts phone + sms_code, so the client is the side
// that must change (that edit belongs to the client card, not to this file and not to the yaml).
type LoginRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Code     string `json:"code" binding:"required"`
	FamilyID string `json:"family_id,omitempty"`
}

// LoginResponse is /auth/login's 200 body: the contract's six fields, in the contract's names
// (access_token / refresh_token / expires_in / family_id / role / pver).
//
// User and Families are NOT in that contract block. They are kept -- filled from real rows, never
// assembled -- because web/src/pages/homeos/auth/login.vue branches on res.data.families.length to
// choose 首页 vs 创建家庭 and stores res.data.user. Reported as a contract gap (the endpoint publishes
// no account or family list) instead of editing the yaml or breaking the page that is already wired
// to them; removing them is a two-sided change (contract + client) this card does not own.
//
// Scope is the seventh field this card adds, and unlike User/Families it is not a client convenience:
// it is the response's copy of the token's own scope claim, so the client branches on a stated fact
// ("this session can only create a family") instead of inferring it from family_id == "" -- the
// inference is exactly the presentation-level check that must not be the only gate (PRD 15.2).
// For a family session Scope is "" (absent in JSON), so the change is additive for the current client.
type LoginResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
	FamilyID     string       `json:"family_id"`
	Role         string       `json:"role"`
	PVer         int64        `json:"pver"`
	Scope        string       `json:"scope,omitempty"`
	User         *UserClaims  `json:"user,omitempty"`
	Families     []FamilyInfo `json:"families,omitempty"`
}

// UserClaims is the account half of the login payload (see LoginResponse's note).
type UserClaims struct {
	ID     string `json:"id"`
	Phone  string `json:"phone"`
	Name   string `json:"name,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// FamilyInfo is one entry of the caller's family list. It is the shape both /auth/login's `families`
// and GET /families return, and it is the shape web/src/stores/home.ts reads there (「角色快照的唯一
// 来源：GET /api/homeos/families → {families:[{id,name,role}]}」), which is why the type is shared
// rather than duplicated per handler.
type FamilyInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"` // owner|member|ward|guest, this account's role in THAT family
}

// Login handles POST /api/homeos/auth/login.
//
// The flow is the one PRD 3.4.1 describes, against the tables migration 0006 created:
// spend the stored code -> find the account by phone -> read that account's real memberships ->
// sign (sub, fid, role, pver) with the service's RS256 key -> store the refresh token's hash.
// Nothing here fabricates a user or a family, and no token is issued for a family the account does
// not occupy.
//
// An account with zero families gets a 200 with an ONBOARDING-scoped pair (family_id / role empty,
// pver 0 because no family row exists to read one from), which is what unblocks POST /families:
// docs/p1-page-structure-navigation.md §4.1 row ① makes 「登录后无家庭」 a defined post-login state,
// so 登录成功 must not answer 403. The authority of that token is enforced in svcauth.Middleware, not
// here -- see that package's OnboardingAllowedRoutes comment for why a 403-on-login and an anonymous
// 建家口 were both rejected.
func Login(c *gin.Context, s *Services) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 phone 与 code 两个字段（见 contracts/openapi/homeos.yaml /auth/login）")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if !phonePattern.MatchString(phone) {
		badRequest(c, "invalid_phone", "手机号格式不正确（P1 仅支持 11 位大陆手机号）")
		return
	}
	ctx := c.Request.Context()

	// ① The code is spent through the repository, not compared to a literal: unknown, expired or
	//    already-used codes all answer the same 401 (PRD 3.4.1 验证码单次有效, contract's '401 验证码错误或过期').
	if err := repo.ConsumeSMSCode(ctx, s.DB, phone, strings.TrimSpace(req.Code)); err != nil {
		if errors.Is(err, repo.ErrSMSCodeInvalid) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_code", "message": "验证码错误或已过期"})
			return
		}
		s.internal(c, "sms_code_check_failed", err)
		return
	}

	// ② 账号: the phone IS the identity (PRD 3.4.1 defines 验证码登录 and no password registration,
	//    PRD 3.6's User row has no credential column), so a verified first-time phone gets its row
	//    created here.
	user, isNewAccount, err := repo.FindOrCreateUser(ctx, s.DB, phone)
	if err != nil {
		s.internal(c, "account_lookup_failed", err)
		return
	}

	// ③ Memberships read from homeos_members JOIN homeos_families -- the real family list.
	memberships, err := repo.ListFamiliesForAccount(ctx, s.DB, user.ID)
	if err != nil {
		s.internal(c, "membership_lookup_failed", err)
		return
	}
	if len(memberships) == 0 {
		// 登录后无家庭: the bootstrap session. Audited as a ALLOWED login (the login did succeed --
		// PRD 21.5's 登录 class is about the authentication act, and denying it a token would make the
		// 「无家庭」 state unrepresentable in the audit trail), and the token is the narrow one.
		s.issueOnboardingSession(c, ctx, user, isNewAccount)
		return
	}

	// ④ Session family: an explicit family_id must be one of the account's own families (PRD 15.2
	//    「判定以 family_id 为界」), otherwise the first family by creation order.
	family := memberships[0]
	if req.FamilyID != "" {
		found := false
		for i := range memberships {
			if memberships[i].ID == req.FamilyID {
				family = memberships[i]
				found = true
				break
			}
		}
		if !found {
			s.writeAudit(ctx, s.auditRow(c, &svcauth.Session{
				AccountID: user.ID, FamilyID: req.FamilyID,
			}, model.AuditEventCrossFamilyAttempt, model.AuditResultDenied,
				"登录时请求了不属于该账号的家庭 "+req.FamilyID))
			c.JSON(http.StatusForbidden, gin.H{"error": "not_family_member", "message": "您不是该家庭成员"})
			return
		}
	}

	sess := svcauth.Session{
		AccountID: user.ID,
		FamilyID:  family.ID,
		MemberID:  family.MemberID,
		Role:      family.Role,
		PVersion:  family.PVersion,
	}
	tokens, err := s.signTokens(sess)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	// The refresh token is stored as a hash only (PRD 20章 密钥与凭据不落明文), and it is the row
	// /auth/logout and /auth/refresh revoke -- without it neither could do anything real.
	if err := repo.SaveRefreshToken(ctx, s.DB, sess.AccountID, tokens.refreshHash, tokens.refreshExp); err != nil {
		s.internal(c, "refresh_token_store_failed", err)
		return
	}

	s.writeAudit(ctx, s.auditRow(c, &sess, model.AuditEventLogin, model.AuditResultAllowed,
		"验证码登录成功；首次登录建号="+fmt.Sprintf("%t", isNewAccount)))

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    tokens.expiresIn,
		FamilyID:     sess.FamilyID,
		Role:         sess.Role,
		PVer:         sess.PVersion,
		User:         userClaims(user),
		Families:     familyInfos(memberships),
	})
}

// issueOnboardingSession answers 200 + a family-less session for an account that occupies no family
// yet (PRD 3.4.1「创建家庭」, nav doc §4.1 row ①). It is the only place in this file that signs a
// token without a family_id, and it stores the refresh half exactly like the family path does, so
// /auth/refresh can rotate it and /auth/logout can kill it.
func (s *Services) issueOnboardingSession(c *gin.Context, ctx context.Context, user *model.HomeosUser, isNewAccount bool) {
	access, refresh, refreshHash, refreshExp, err := s.Signer.IssueOnboardingTokens(user.ID)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	if err := repo.SaveRefreshToken(ctx, s.DB, user.ID, refreshHash, refreshExp); err != nil {
		s.internal(c, "refresh_token_store_failed", err)
		return
	}

	sess := svcauth.Session{AccountID: user.ID, Scope: svcauth.ScopeOnboarding}
	s.writeAudit(ctx, s.auditRow(c, &sess, model.AuditEventLogin, model.AuditResultAllowed,
		"验证码登录成功（账号尚无任何家庭，签发 onboarding 令牌）；首次登录建号="+fmt.Sprintf("%t", isNewAccount)))

	c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(svcauth.OnboardingTokenTTL.Seconds()),
		// family_id / role stay EMPTY and pver 0: there is no family row yet, and a placeholder id or a
		// pre-granted "owner" role would be the fabricated data the family path exists to avoid.
		Scope:    svcauth.ScopeOnboarding,
		User:     userClaims(user),
		Families: []FamilyInfo{},
	})
}

// ==================== POST /auth/refresh ====================

// RefreshRequest is /auth/refresh's body (contract L168-177: required [refresh_token]).
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshResponse is /auth/refresh's 200 body: the contract's three fields.
//
// Scope is this card's addition, same reason as LoginResponse's: the client must be able to tell
// "you are still in 引导态" from "you have a session family" without decoding the JWT. It is absent for
// a family session, so the current client is unaffected.
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

// Refresh handles POST /api/homeos/auth/refresh: rotate one session's token pair (PRD 3.4.1).
//
// Four checks, in this order, and each of them is load-bearing:
//  1. the presented token must verify under the service's RS256 key and carry sub/fid/role;
//  2. the member row must still exist in that family -- a removed or switched-out account cannot
//     extend its life through the refresh path (the same rule the middleware enforces on reads);
//  3. the token's hash must be a live row in homeos_refresh_tokens, which is also what stops an
//     ACCESS token from being used here: access hashes are never stored, so only the refresh half of
//     a pair can pass, without the SDK needing a token-type claim this card must not invent;
//  4. session KIND is preserved: an onboarding refresh rotates to an onboarding pair, a family
//     refresh to a family pair. Verify runs first, so a family token can never be downgraded here
//     (authz.Verify would have accepted it and the onboarding branch would be unreachable), and
//     VerifyOnboarding refuses a token that carries a fid or a role.
//
// The role and pver come from the database, not from the old token, so a role change during the
// refresh window is picked up instead of being re-snapshotted for another 30 days. The same read also
// lets an onboarding session UPGRADE: if the account acquired a family in the meantime (created on
// another device, or accepted an invite), the rotated pair is a real family session rather than a
// narrower one that the client would then have to log out of.
func Refresh(c *gin.Context, s *Services) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 refresh_token 字段")
		return
	}
	raw := strings.TrimSpace(req.RefreshToken)
	ctx := c.Request.Context()

	claims, err := s.Signer.Verify(raw)
	if err != nil {
		onboarding, onboardingErr := s.Signer.VerifyOnboarding(raw)
		if onboardingErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "refresh token 无效或已过期"})
			return
		}
		s.refreshOnboarding(c, ctx, s.auditSessionFromOnboarding(onboarding), onboarding.Subject, raw)
		return
	}

	memberID, err := s.resolveMember(ctx, claims.FamilyID, claims.Subject)
	if err != nil {
		if errors.Is(err, svcauth.ErrNoMember) {
			s.auditDenied(c, nil, model.AuditEventCrossFamilyAttempt,
				"refresh 时该账号已不在此家庭: "+claims.Subject)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "会话家庭已失效"})
			return
		}
		s.internal(c, "membership_lookup_failed", err)
		return
	}
	var family repo.FamilyMembership
	found := false
	memberships, err := repo.ListFamiliesForAccount(ctx, s.DB, claims.Subject)
	if err != nil {
		s.internal(c, "membership_lookup_failed", err)
		return
	}
	for _, m := range memberships {
		if m.ID == claims.FamilyID {
			family, found = m, true
			break
		}
	}
	if !found {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "会话家庭已失效"})
		return
	}

	sess := svcauth.Session{
		AccountID: claims.Subject,
		FamilyID:  claims.FamilyID,
		MemberID:  memberID,
		Role:      family.Role,
		PVersion:  family.PVersion,
	}
	tokens, err := s.signTokens(sess)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	// One transaction: the presented row is revoked and its replacement recorded, so a replayed old
	// refresh token stops working the moment the pair is rotated (repo.RotateRefreshToken).
	if err := repo.RotateRefreshToken(ctx, s.DB, svcauth.HashToken(raw),
		tokens.refreshHash, claims.Subject, tokens.refreshExp); err != nil {
		if errors.Is(err, repo.ErrTokenNotActive) {
			s.auditDenied(c, &sess, model.AuditEventCrossFamilyAttempt, "refresh token 已撤销/已轮换/不属于该账号")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "refresh token 无效或已撤销"})
			return
		}
		s.internal(c, "refresh_token_rotate_failed", err)
		return
	}

	c.JSON(http.StatusOK, RefreshResponse{
		AccessToken:  tokens.access,
		RefreshToken: tokens.refresh,
		ExpiresIn:    tokens.expiresIn,
	})
}

// refreshOnboarding rotates a family-less session's pair. Same stored-hash discipline as the family
// path, so a replayed onboarding refresh dies at the same moment.
func (s *Services) refreshOnboarding(c *gin.Context, ctx context.Context, sess *svcauth.Session, accountID, raw string) {
	// The account may have acquired a family since the onboarding pair was issued: 建家 happens on this
	// very session, so a client that refreshed after (re)loading could otherwise be stuck on a token
	// that is refused everywhere except the bootstrap routes.
	memberships, err := repo.ListFamiliesForAccount(ctx, s.DB, accountID)
	if err != nil {
		s.internal(c, "membership_lookup_failed", err)
		return
	}
	if len(memberships) > 0 {
		if _, err := s.resolveMember(ctx, memberships[0].ID, accountID); err != nil {
			if errors.Is(err, svcauth.ErrNoMember) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "会话家庭已失效"})
				return
			}
			s.internal(c, "membership_lookup_failed", err)
			return
		}
		family := memberships[0]
		upgraded := svcauth.Session{
			AccountID: accountID,
			FamilyID:  family.ID,
			MemberID:  family.MemberID,
			Role:      family.Role,
			PVersion:  family.PVersion,
		}
		tokens, err := s.signTokens(upgraded)
		if err != nil {
			s.internal(c, "token_signing_failed", err)
			return
		}
		if err := repo.RotateRefreshToken(ctx, s.DB, svcauth.HashToken(raw),
			tokens.refreshHash, accountID, tokens.refreshExp); err != nil {
			if errors.Is(err, repo.ErrTokenNotActive) {
				s.auditDenied(c, sess, model.AuditEventCrossFamilyAttempt, "onboarding refresh token 已撤销/已轮换/不属于该账号")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "refresh token 无效或已撤销"})
				return
			}
			s.internal(c, "refresh_token_rotate_failed", err)
			return
		}
		c.JSON(http.StatusOK, RefreshResponse{
			AccessToken:  tokens.access,
			RefreshToken: tokens.refresh,
			ExpiresIn:    tokens.expiresIn,
		})
		return
	}

	access, refresh, refreshHash, refreshExp, err := s.Signer.IssueOnboardingTokens(accountID)
	if err != nil {
		s.internal(c, "token_signing_failed", err)
		return
	}
	if err := repo.RotateRefreshToken(ctx, s.DB, svcauth.HashToken(raw),
		refreshHash, accountID, refreshExp); err != nil {
		if errors.Is(err, repo.ErrTokenNotActive) {
			s.auditDenied(c, sess, model.AuditEventCrossFamilyAttempt, "onboarding refresh token 已撤销/已轮换/不属于该账号")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_refresh_token", "message": "refresh token 无效或已撤销"})
			return
		}
		s.internal(c, "refresh_token_rotate_failed", err)
		return
	}
	c.JSON(http.StatusOK, RefreshResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(svcauth.OnboardingTokenTTL.Seconds()),
		Scope:        svcauth.ScopeOnboarding,
	})
}

// auditSessionFromOnboarding builds the session record the audit sink wants for a family-less caller:
// account only, no family, since there is no family to attribute the event to (PRD 15.2 keeps the
// boundary at the token's fid, and this token has none).
func (s *Services) auditSessionFromOnboarding(claims *authz.Claims) *svcauth.Session {
	return &svcauth.Session{AccountID: claims.Subject, Scope: svcauth.ScopeOnboarding, Claims: claims}
}

// ==================== POST /auth/logout ====================

// LogoutRequest carries the credential the endpoint revokes. The client's 「退出登录」 button has the
// refresh token in storage (web/src/utils/request.ts keeps access_token + refresh_token), and an
// access token's hash is not in homeos_refresh_tokens, so the refresh half is the only one that can
// actually be revoked -- which is the point of the row the login path stored.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// LogoutResponse reports how many rows were revoked, so a caller can tell "revoked now" from
// "nothing to revoke" without learning whether a token ever existed (both answer revoked=0).
type LogoutResponse struct {
	Revoked int64  `json:"revoked"`
	Message string `json:"message"`
}

// Logout handles POST /api/homeos/auth/logout: 登出即撤销 refresh (PRD 3.4.1).
func Logout(c *gin.Context, s *Services) {
	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid_request", "需要 refresh_token 字段（登出撤销的是 refresh 行，access token 不落库）")
		return
	}
	ctx := c.Request.Context()

	n, err := repo.RevokeRefreshToken(ctx, s.DB, svcauth.HashToken(strings.TrimSpace(req.RefreshToken)))
	if err != nil {
		s.internal(c, "refresh_token_revoke_failed", err)
		return
	}
	// PRD 21.5's nine audited classes contain no 登出 class, and homeos_audit_log.event carries that
	// enum as a CHECK constraint, so logout is logged through the service logger rather than by
	// inventing a tenth event value (the missing class is reported instead).
	if s.Logger != nil {
		s.Logger.Info("identity: 登出撤销 refresh token", "revoked", n, "ip", c.ClientIP())
	}

	msg := "已登出，本次会话的 refresh token 已撤销"
	if n == 0 {
		msg = "没有可撤销的 refresh token（已登出、已轮换或从未签发）"
	}
	c.JSON(http.StatusOK, LogoutResponse{Revoked: n, Message: msg})
}

// ==================== identity reads ====================

// userClaims maps an account row onto the response's account half. Nil pointer columns stay empty
// strings here rather than nulls, because the client reads user.name as a string.
func userClaims(u *model.HomeosUser) *UserClaims {
	if u == nil {
		return nil
	}
	out := &UserClaims{ID: u.ID, Phone: u.Phone}
	if u.Name != nil {
		out.Name = *u.Name
	}
	if u.Avatar != nil {
		out.Avatar = *u.Avatar
	}
	return out
}

func familyInfos(rows []repo.FamilyMembership) []FamilyInfo {
	out := make([]FamilyInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, FamilyInfo{ID: r.ID, Name: r.Name, Role: r.Role})
	}
	return out
}

// ==================== GET /auth/me ====================

// MeResponse is GET /auth/me's 200 body: who the token says the caller is, plus the families it can
// switch to and the scope the response was built under.
//
// Why an endpoint with no contract block is added rather than skipped: it is in svcauth's onboarding
// allowlist, so the client's 引导页 has one read it can make while it has nothing else -- and a
// bootstrap state the client cannot introspect is a bootstrap state it has to guess at.
type MeResponse struct {
	AccountID string       `json:"user_id"`
	Phone     string       `json:"phone"`
	Name      string       `json:"name,omitempty"`
	Avatar    string       `json:"avatar,omitempty"`
	Scope     string       `json:"scope,omitempty"`
	FamilyID  string       `json:"family_id,omitempty"`
	Role      string       `json:"role,omitempty"`
	PVer      int64        `json:"pver"`
	MemberID  string       `json:"member_id,omitempty"`
	Families  []FamilyInfo `json:"families"`
}

// Me handles GET /api/homeos/auth/me. Both session kinds are served, from the same two reads, and
// neither read takes a family id from the request: the account boundary is the token's sub, the
// family boundary is the token's fid (PRD 15.2「判定以 family_id 为界」), so this cannot become an
// enumeration endpoint.
func Me(c *gin.Context, s *Services) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	ctx := c.Request.Context()

	user, err := repo.FindUserByID(ctx, s.DB, sess.AccountID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "no_account", "message": "令牌指向的账号已不存在"})
			return
		}
		s.internal(c, "account_lookup_failed", err)
		return
	}
	memberships, err := repo.ListFamiliesForAccount(ctx, s.DB, user.ID)
	if err != nil {
		s.internal(c, "membership_lookup_failed", err)
		return
	}

	res := MeResponse{
		AccountID: user.ID,
		Phone:     user.Phone,
		Scope:     sess.Scope,
		Families:  familyInfos(memberships),
	}
	if user.Name != nil {
		res.Name = *user.Name
	}
	if user.Avatar != nil {
		res.Avatar = *user.Avatar
	}
	if !sess.IsOnboarding() {
		res.FamilyID = sess.FamilyID
		res.Role = sess.Role
		res.PVer = sess.PVersion
		res.MemberID = sess.MemberID
	}
	c.JSON(http.StatusOK, res)
}

// ==================== GET /.well-known/jwks.json ====================

// GetJWKS serves the public half of this process's signing key
// (contracts/openapi/homeos.yaml:76 「/.well-known/jwks.json」, keys[] = kty/kid/use/alg/n/e).
//
// The document is DERIVED from the key the signer actually holds (svcauth.Signer.JWKS reads
// s.Public()), so there is no stale-copy path: a deploy that rotates HOMEOS_JWT_PRIVATE_KEY_PEM gets a
// new n/e and, unless it pinned HOMEOS_JWT_KEY_ID, a new kid in the same restart. svc-finance's
// authz client fetches this through rclient to verify RS256 tokens (deploy/env.local.example's
// FINANCE_JWKS_URL is the verifying side's entry), which is why serving it is the difference between
// "the contract documents an endpoint" and "cross-service verification works".
func GetJWKS(c *gin.Context, s *Services) {
	if s.Signer == nil {
		s.internal(c, "signer_unavailable", errors.New("handler: no signer configured"))
		return
	}
	c.JSON(http.StatusOK, s.Signer.JWKS())
}

// ==================== tokens ====================

// tokenPair is one issued session. The refresh half is kept as a hash so the caller can store or
// rotate the row without ever persisting the token itself (PRD 20章).
type tokenPair struct {
	access      string
	refresh     string
	refreshHash string
	refreshExp  time.Time
	expiresIn   int
}

// signTokens delegates the whole of JWT to svcauth.Signer: RS256 end to end, one key, no header
// surgery and no secret literal in this package.
func (s *Services) signTokens(sess svcauth.Session) (*tokenPair, error) {
	access, refresh, hash, exp, err := s.Signer.IssueTokens(sess)
	if err != nil {
		return nil, err
	}
	return &tokenPair{
		access:      access,
		refresh:     refresh,
		refreshHash: hash,
		refreshExp:  exp,
		expiresIn:   int(svcauth.AccessTokenTTL.Seconds()),
	}, nil
}

// ==================== middleware + audit wiring ====================

// Middleware builds the request authenticator every identity-scoped route runs through. The gin keys
// it installs are "user_id" / "family_id" / "member_id" / "role" / "pver" -- the names the existing
// family and search handlers already read (see svcauth's Ctx* constants), which is why those handlers
// needed no rename.
//
// PRD 15.5 (「越权尝试全部落审计」) is why OnDenied is not optional in practice: without a sink a 401
// would leave no trace, and this service owns the audit table.
//
// The onboarding allowlist is built from s.RoutePrefix here rather than in cmd, so the two ends of the
// bootstrap cannot drift: the same string that Group()s the routes names the routes that are allowed
// without a family. A process that leaves RoutePrefix empty gets an allowlist of "" -prefixed keys,
// which matches nothing -- fail closed, and the startup log states the prefix it registered.
func (s *Services) Middleware() (*svcauth.Middleware, error) {
	mw, err := svcauth.NewMiddleware(s.Signer, s.resolveMember, s.auditDenied)
	if err != nil {
		return nil, err
	}
	mw.OnboardingRoutes = svcauth.OnboardingAllowedRoutes(s.RoutePrefix)
	return mw, nil
}

// resolveMember is svcauth.MemberResolver over homeos_members. repo's sentinel is translated to the
// auth package's so a missing member row answers 401 rather than 500 -- fail closed, and a token
// whose family no longer contains this account must not be upgraded to a stack trace (PRD 15.2).
func (s *Services) resolveMember(ctx context.Context, familyID, accountID string) (string, error) {
	memberID, err := repo.ResolveMemberByAccount(ctx, s.DB, familyID, accountID)
	if errors.Is(err, repo.ErrAccountNotInFamily) {
		return "", svcauth.ErrNoMember
	}
	return memberID, err
}

// auditDenied is the middleware's OnDenied sink: one denied audit row per refusal, with the token's
// family and subject when the refusal happened after verification.
func (s *Services) auditDenied(c *gin.Context, sess *svcauth.Session, event, reason string) {
	s.writeAudit(c.Request.Context(), s.auditRow(c, sess, event, model.AuditResultDenied, reason))
}

// auditRow builds one homeos_audit_log row. Column set is 0009's; the values come from the request
// and the session, and actor columns stay NULL when there is no authenticated caller.
func (s *Services) auditRow(c *gin.Context, sess *svcauth.Session, event, result, reason string) *model.HomeosAuditLog {
	now := time.Now().UTC()
	code := registry.HomeosCode
	row := &model.HomeosAuditLog{
		ID:         uuid.NewString(),
		Code:       &code,
		Event:      event,
		Result:     result,
		Reason:     &reason,
		CreatedAt:  now,
		OccurredAt: now,
	}
	if sess != nil {
		sub := sess.AccountID
		row.ActorUserID = &sub
		// family_id stays NULL for a family-less session: homeos_audit_log.family_id is a uuid column
		// and "" is not a uuid, so writing the token's empty fid would fail the insert and lose the
		// audit row PRD 15.5 asks for. NULL is also the honest value -- 该事件没有会话家庭.
		if sess.FamilyID != "" {
			fid := sess.FamilyID
			row.FamilyID = &fid
		}
		if sess.MemberID != "" {
			mid := sess.MemberID
			row.ActorMemberID = &mid
		}
	}
	if c != nil && c.Request != nil {
		ip := c.ClientIP()
		row.IP = &ip
		// user_agent is varchar(500) per 0009: an over-long header would fail the insert, so the
		// field is truncated rather than silently losing the whole audit row.
		ua := c.Request.UserAgent()
		if len(ua) > 500 {
			ua = ua[:500]
		}
		if ua != "" {
			row.UserAgent = &ua
		}
	}
	return row
}

// writeAudit persists one audited event.
//
// A failure is logged at ERROR and does not change the response: PRD 15.5 wants the row, but a service
// that answered 500 because its audit insert failed would trade an auditable refusal for an
// unavailable login. The previous version of this function first asked information_schema whether
// homeos_audit_log existed and downgraded to a WARN, because 0009 was unapplied in this checkout; the
// migrations are now applied through deploy/migrate.sh, so the table is expected and "not written" is
// an error, not a documented capability.
func (s *Services) writeAudit(ctx context.Context, row *model.HomeosAuditLog) {
	if row == nil {
		return
	}
	if err := repo.AppendAudit(ctx, s.DB, row); err != nil {
		if s.Logger != nil {
			s.Logger.Error("identity: 审计写入失败", "event", row.Event, "result", row.Result, "err", err.Error())
		}
	}
}

// ==================== shared answers ====================

// badRequest is the 400 shape. "message" is the key the client surfaces (web/src/utils/request.ts),
// and "error" is the stable machine-readable code the client can branch on.
func badRequest(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": code, "message": message})
}

// internal answers 500 with a fixed message and logs the detail: a login failure must not leak the
// SQL behind it, and PRD 15.2's fail-closed rule is about the answer, not about the explanation.
func (s *Services) internal(c *gin.Context, code string, err error) {
	if s.Logger != nil {
		s.Logger.Error("identity handler 内部错误", "code", code, "err", err.Error(), "path", c.Request.URL.Path)
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": code, "message": "服务暂时无法处理该请求"})
}
