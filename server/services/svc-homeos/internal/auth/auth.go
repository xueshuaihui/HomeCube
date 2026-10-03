// Package auth is svc-homeos' token issuer and request authenticator.
//
// Why it lives here rather than in packages/authz: packages/authz is the shared SDK every service
// embeds and it deliberately only VERIFIES (PRD 15.6「鉴权 SDK 由 svc-homeos 发布、各服务共用同一
// 实现」). Signing keys are the issuing service's own secret, so the private half stays inside
// svc-homeos while every service, this one included, verifies through authz.Verify.
//
// Algorithm: RS256, because authz.Verify rejects every non-RSA signing method outright
// (policy.go「unexpected signing method」). The previous handler code built an RS256 token and then
// overwrote its header with HS256 while signing with a []byte secret, so SignedString always
// errored and POST /auth/login could only answer 500.
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/xueshuaihui/HomeCube/server/packages/authz"
)

// Env keys. These three are NOT in deploy/env.local.example today, which is a reported回写项: the
// repository has no documented key-material entry, while contracts/openapi/homeos.yaml publishes
// /.well-known/jwks.json and therefore presupposes an asymmetric key pair. They are named with the
// same {CODE}_ prefix the cmd already uses for HOMEOS_DSN / NATS_URL / UPLOAD_DIR / HOMEOS_ADDR, and
// NewSigner now refuses to start when neither of the two key entries is present.
const (
	// EnvPrivateKeyPEMPath is a file path to a PEM-encoded RSA private key (PKCS#1 or PKCS#8).
	EnvPrivateKeyPEMPath = "HOMEOS_JWT_PRIVATE_KEY_PEM"
	// EnvPrivateKeyPEM is the same PEM inline, for compose env blocks; literal "\n" is accepted.
	EnvPrivateKeyPEM = "HOMEOS_JWT_PRIVATE_KEY"
	// EnvKeyID names the current key in /.well-known/jwks.json.
	EnvKeyID = "HOMEOS_JWT_KEY_ID"
)

// Token lifetimes are PRD 3.4.1's (access 短、refresh 轮换); the 15-minute access token is also what
// bounds how long a stale role snapshot can survive after a permission change, which 15.6 closes by
// pver invalidation rather than by a shorter token.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// ScopeOnboarding is the claim value that marks a token as family-less, and OnboardingTokenTTL /
// OnboardingRefreshTTL bound that session.
//
// Why this exists at all: PRD 3.4.1 lists 「创建家庭」 as a 家庭核心 action and
// docs/p1-page-structure-navigation.md §4.1 row ① gives `homeos/auth/family-create` the entry
// condition 「登录后无家庭」 -- i.e. 登录成功 is defined as reachable for an account with zero
// families. Refusing the token in that state (the previous 403 no_family) made the route
// unreachable, because POST /families needs a token and the token needed a family. The fix is not to
// open an unauthenticated建家口: it is to issue a token whose authority is exactly the bootstrap, and
// to enforce that authority server-side. An onboarding token therefore carries no fid and no role, so
// authz.Verify -- the shared SDK every service runs -- refuses it on every family-scoped route by
// itself (「missing fid (family_id)」); the middleware's allowlist below is the second, explicit gate.
//
// The two TTLs are card-level choices (the documents define 短 access / 轮换 refresh for FAMILY
// sessions and say nothing about a family-less one), reported as such: 10 minutes is long enough to
// fill in the建家表单, and the 1-hour refresh lets the 引导页 survive a refresh without extending the
// family-less window to the 30 days a real session gets.
const (
	ScopeOnboarding      = "onboarding"
	OnboardingTokenTTL   = 10 * time.Minute
	OnboardingRefreshTTL = time.Hour
)

// Onboarding routes. The closed set an onboarding-scoped token may reach, as "METHOD /full/path"
// keys matched against gin's c.FullPath(). It is deliberately tiny and it is data-level, not
// presentation-level: "the UI just doesn't show it" is not an enforcement, and an onboarding token
// that could read /families/{id}/... could read another household's data with a token this service
// signed itself (PRD 15.2「判定以 family_id 为界」, 22.2 第 5 条).
//
//	POST {prefix}/families       -- 创建家庭, the act this whole scope exists to allow (PRD 3.4.1, 17.8)
//	GET  {prefix}/families       -- the CALLER'S OWN account-family list, which is how the client knows
//	                               it is in 「登录后无家庭」 state. It reads homeos_members by the token's
//	                               sub only; it never takes a family id from the request, so it cannot
//	                               become a way to enumerate somebody else's household.
//	GET  {prefix}/auth/me        -- session introspection (scope, families), same account-only reads
//	POST {prefix}/auth/refresh   -- rotate the onboarding pair so the建家页 survives its own 10 minutes
//	POST {prefix}/auth/logout     -- revoking one's own refresh row is always safe
const (
	OnboardingRouteCreateFamily = "POST %s/families"
	OnboardingRouteListFamilies = "GET %s/families"
	OnboardingRouteMe           = "GET %s/auth/me"
	OnboardingRouteRefresh      = "POST %s/auth/refresh"
	OnboardingRouteLogout       = "POST %s/auth/logout"
)

// OnboardingAllowedRoutes builds the enforcement set for this process's route prefix, so cmd and
// tests never hand-write the same path list twice (registry.RoutePrefix is the single source, §1.3).
func OnboardingAllowedRoutes(routePrefix string) map[string]bool {
	// registry's RoutePrefix values carry a trailing slash ("/api/homeos/") while gin's c.FullPath()
	// never does, so the separator is normalised here rather than at every call site.
	prefix := strings.TrimSuffix(routePrefix, "/")
	templates := []string{
		OnboardingRouteCreateFamily,
		OnboardingRouteListFamilies,
		OnboardingRouteMe,
		OnboardingRouteRefresh,
		OnboardingRouteLogout,
	}
	set := make(map[string]bool, len(templates))
	for _, t := range templates {
		set[fmt.Sprintf(t, prefix)] = true
	}
	return set
}

// ErrTokenInvalid is what the middleware answers 401 with; the wrapped text from authz.Verify is
// logged, never returned, so a client cannot probe claim-validation internals.
var ErrTokenInvalid = errors.New("token invalid")

// Signer holds the key pair this process signs with and verifies its own tokens by.
type Signer struct {
	kid     string
	private *rsa.PrivateKey
}

// NewSigner loads this process's RS256 key material and refuses to start without it.
//
// There is deliberately no fallback. The previous behaviour -- generate a 2048-bit pair in-process
// when nothing is configured, and only log a warning -- is what this card removes: every restart
// silently invalidated every issued access token AND every refresh token row it had stored, and the
// public half published at /.well-known/jwks.json changed underneath the other services' authz
// caches (PRD 15.6「各服务鉴权缓存失效」assumes one stable key; tech plan §4.1「svc-homeos 是唯一签发方
// 与 RS256 私钥持有者」). A deploy that forgot the key must be a startup failure with a message that
// names the two env keys, not a running service that answers 401 to all of its own users.
func NewSigner(log *slog.Logger) (*Signer, error) {
	pemBytes, source, err := readKeyPEM()
	if err != nil {
		return nil, err
	}
	if pemBytes == nil {
		return nil, fmt.Errorf(
			"svc-homeos 启动被拒绝：未配置 RS256 签名私钥。请设置 %s（PEM 文件路径，PKCS#1 或 PKCS#8）"+
				"或 %s（PEM 内联，行分隔写成字面反斜杠 n），并可用 %s 指定 jwks 里的 kid；"+
				"deploy/env.local.example 目前三条都未登记（已上报的回写项）。"+
				"本进程不会生成进程内临时密钥：临时密钥会使每次重启后所有已签发 token 立即失效、"+
				"/.well-known/jwks.json 的公钥随之改变（PRD 15.6、技术方案 §4.1）",
			EnvPrivateKeyPEMPath, EnvPrivateKeyPEM, EnvKeyID)
	}

	key, err := parsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 的 RS256 私钥: %w", source, err)
	}
	// ONE derivation for both the log line, the JWT header and /.well-known/jwks.json: the kid used to
	// be logged as the raw thumbprint while Signer.kid held $HOMEOS_JWT_KEY_ID, so a deploy that pinned
	// an env kid printed a kid it never published.
	kid := configuredKeyID(key)
	if log != nil {
		log.Info("RS256 签名密钥已加载", "source", source, "kid", kid, "bits", key.N.BitLen())
	}
	return &Signer{kid: kid, private: key}, nil
}

// KeyID is the kid this process publishes in /.well-known/jwks.json.
func (s *Signer) KeyID() string { return s.kid }

// signedToken is the ONE place a token this service issues is assembled: RS256, this process's key,
// and a header carrying the same kid JWKS() publishes. Both read s.kid, which configuredKeyID derived
// once in NewSigner from $HOMEOS_JWT_KEY_ID (or the RFC 7638 thumbprint), so the header and the
// document cannot drift and the derivation exists nowhere else.
//
// The header field is the issuer's half of a kid-based lookup: /.well-known/jwks.json publishes keys[]
// with a kid (contracts/openapi/homeos.yaml), so a verifier that picks its key by kid -- the form
// svc-finance's FINANCE_JWKS_URL pull takes, tech plan §4.1/S4 -- has nothing to match here and must
// reject a token signed by the right key.
func (s *Signer) signedToken(claims jwt.Claims) *jwt.Token {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if s.kid != "" {
		token.Header["kid"] = s.kid
	}
	return token
}

// Public exposes the verification half, which authz.Verify takes.
func (s *Signer) Public() *rsa.PublicKey { return &s.private.PublicKey }

// Verify parses and validates an RS256 token through the shared SDK, so the claim requirements
// (non-empty sub / fid / role, exp) are enforced by the same code every other service runs.
func (s *Signer) Verify(tokenString string) (*authz.Claims, error) {
	claims, err := authz.Verify(tokenString, s.Public())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	return claims, nil
}

// Session is the authenticated caller as the handlers see it: the token's claim snapshot plus the
// family-scoped member row this account occupies in the session family.
type Session struct {
	AccountID string // homeos_users.id == claims.sub
	FamilyID  string // claims.fid, the only family boundary a request may cross (PRD 15.2)
	MemberID  string // homeos_members.id for (fid, sub); empty only for accounts with no member row
	Role      string // claims.Role, the role snapshot of PRD 3.4.1
	PVersion  int64  // claims.pver
	Scope     string // ScopeOnboarding for a family-less bootstrap session, "" for a real one
	Claims    *authz.Claims
}

// IsOnboarding reports the family-less bootstrap session. Handlers use it instead of testing
// FamilyID == "", so "no family yet" and "wiring bug" stay distinguishable: a family session always
// has a fid (authz.Verify requires it), and an onboarding session never does.
func (s *Session) IsOnboarding() bool { return s != nil && s.Scope == ScopeOnboarding }

// OnboardingClaims is the claim set of a family-less session: the shared SDK's struct plus the scope
// marker. Encoding-wise it is the SDK's struct verbatim (Go promotes the embedded value's JSON fields
// inline), so the token keeps sub / fid / role / pver / jti / iat / exp and only ADDS "scope";
// packages/authz is not touched, and every other service's authz.Claims decoder ignores the extra
// field the same way it ignores any unknown claim. Semantically fid, role stay empty -- which is
// exactly why authz.Verify refuses these tokens on family routes instead of accepting them quietly.
type OnboardingClaims struct {
	authz.Claims
	Scope string `json:"scope"`
}

// IssueTokens mints the access/refresh pair for one session family. pver is read from the family row
// by the caller, never assumed (PRD 15.6).
//
// The claim set is exactly authz.Claims' -- sub / fid / role / pver / jti / iat / exp. No member id
// goes into the token: the middleware resolves it from homeos_members on every request, so a
// re-issued role or a re-added account cannot be shadowed by a stale claim, and other services keep
// verifying with the unmodified SDK struct.
func (s *Signer) IssueTokens(sess Session) (accessToken string, refreshToken string, refreshHash string, refreshExp time.Time, err error) {
	now := time.Now()

	access, err := s.signedToken(s.claims(sess, uuid.NewString(), now, now.Add(AccessTokenTTL))).SignedString(s.private)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("签发 access token: %w", err)
	}

	refreshExp = now.Add(RefreshTokenTTL)
	refresh, err := s.signedToken(s.claims(sess, uuid.NewString(), now, refreshExp)).SignedString(s.private)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("签发 refresh token: %w", err)
	}

	return access, refresh, HashToken(refresh), refreshExp, nil
}

// claims builds the SDK's claim struct for one session at one expiry.
func (s *Signer) claims(sess Session, jti string, iat, exp time.Time) *authz.Claims {
	return &authz.Claims{
		Subject:  sess.AccountID,
		FamilyID: sess.FamilyID,
		Role:     sess.Role,
		PVersion: sess.PVersion,
		JTI:      jti,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(iat),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        jti,
		},
	}
}

// IssueOnboardingTokens mints the pair for an account that has verified its phone but occupies no
// family yet (PRD 3.4.1「创建家庭」, nav doc §4.1 row ①「登录后无家庭」).
//
// fid and role are EMPTY and scope is "onboarding": there is no family to name and no member row to
// snapshot a role from, and inventing either one -- a placeholder family id, a default "owner" role
// before any family exists -- is the fake data this service's cards keep being opened to remove.
// The two halves share the signing path and the key with real sessions, so the verifying side needs
// no second algorithm; the middleware's allowlist (OnboardingAllowedRoutes) is what keeps the
// authority narrow.
func (s *Signer) IssueOnboardingTokens(accountID string) (accessToken, refreshToken, refreshHash string, refreshExp time.Time, err error) {
	if accountID == "" {
		return "", "", "", time.Time{}, errors.New("auth: onboarding token needs a subject")
	}
	now := time.Now()
	sess := Session{AccountID: accountID, Scope: ScopeOnboarding}

	access, err := s.signedToken(&OnboardingClaims{
		Claims: *s.claims(sess, uuid.NewString(), now, now.Add(OnboardingTokenTTL)),
		Scope:  ScopeOnboarding,
	}).SignedString(s.private)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("签发 onboarding access token: %w", err)
	}

	refreshExp = now.Add(OnboardingRefreshTTL)
	refresh, err := s.signedToken(&OnboardingClaims{
		Claims: *s.claims(sess, uuid.NewString(), now, refreshExp),
		Scope:  ScopeOnboarding,
	}).SignedString(s.private)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("签发 onboarding refresh token: %w", err)
	}
	return access, refresh, HashToken(refresh), refreshExp, nil
}

// VerifyOnboarding parses a family-less bootstrap token.
//
// It mirrors packages/authz.Verify's rules (RS256 only, exp enforced by the parser, non-empty sub)
// and then INVERTS its claim requirements: an onboarding token must have no fid and no role, and must
// say scope="onboarding". Both directions are load-bearing. Without the fid/role check a FAMILY
// token could be replayed on the onboarding-only branch of a handler; without the scope check a
// token this service never issued as onboarding (any family token, which authz.Verify rejects only
// for its own callers) would be treated as one. It refuses a family token, so the two session kinds
// can never be confused -- and the middleware relies on that: it tries Verify first, so a real
// session never falls through to this path.
func (s *Signer) VerifyOnboarding(tokenString string) (*authz.Claims, error) {
	var parsed OnboardingClaims
	token, err := jwt.ParseWithClaims(tokenString, &parsed, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.Public(), nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("%w: token not valid", ErrTokenInvalid)
	}
	if parsed.Scope == "" {
		return nil, fmt.Errorf("%w: no scope claim, this is not an onboarding token", ErrTokenInvalid)
	}
	if parsed.Scope != ScopeOnboarding {
		return nil, fmt.Errorf("%w: scope %q is not %q", ErrTokenInvalid, parsed.Scope, ScopeOnboarding)
	}
	if parsed.Claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing subject (account_id)", ErrTokenInvalid)
	}
	if parsed.Claims.FamilyID != "" || parsed.Claims.Role != "" {
		return nil, fmt.Errorf("%w: an onboarding token must carry no fid and no role", ErrTokenInvalid)
	}
	return &parsed.Claims, nil
}

// HashToken is the storable form of a refresh token (PRD 20章: 密钥与凭据不落明文).
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// JWK is one entry of /.well-known/jwks.json, matching the field set the contract block lists
// (kty / kid / use / alg / n / e).
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSDoc is the whole document.
type JWKSDoc struct {
	Keys []JWK `json:"keys"`
}

// JWKS publishes the public half so other services can verify without sharing a secret
// (contracts/openapi/homeos.yaml /.well-known/jwks.json).
func (s *Signer) JWKS() JWKSDoc {
	pub := s.Public()
	return JWKSDoc{Keys: []JWK{{
		Kty: "RSA",
		Kid: s.kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
}

// -----------------------------------------------------------------------------
// SMS provider seam
// -----------------------------------------------------------------------------

// DevFixedSMSCode is P1's local SMS implementation's code. It is not this card's invention:
// packages/adapter/DEPENDENCIES.md's 短信 row reads 「阿里云短信 / 腾讯云短信 | 固定测试验证码 |
// P2 商用前 | 登录验证码通道」, i.e. the chosen P1 substitute for the unbuilt provider IS a fixed
// test code, and docs/p1-tech-plan.md §4.1 records the same. What this card fixes is WHERE the value
// lives: behind a provider seam, so the handler holds no literal and swapping in Aliyun/Tencent at
// P2 is one implementation, not a code hunt through the login path.
//
// Reported: packages/adapter/ has asr / ocr / push but NO sms package, so the seam is declared here
// (the issuing service's own copy of it) rather than in packages/adapter/sms -- that directory is
// owned by the adapter card and is outside this card's file list.
const DevFixedSMSCode = "123456"

// DefaultSMSCodeTTL is the code's validity window (5 minutes); it is stored per row by the repo, so
// the expiry is enforced by the database read rather than by the client's patience.
const DefaultSMSCodeTTL = 5 * time.Minute

// SMSProvider is the external-service boundary PRD 14.x/19.x require for 短信: the caller asks for a
// code to be delivered and gets back the value that was generated plus the window it is valid for,
// which the caller then stores. SendCode does NOT persist anything -- storage (single-use, rate
// limit) is repo.IssueSMSCode's job, so no provider can quietly become the source of truth.
type SMSProvider interface {
	// SendCode delivers one verification code to phone and reports the code it delivered with the
	// TTL that code is valid for.
	SendCode(ctx context.Context, phone string) (code string, ttl time.Duration, err error)
	// Name identifies the channel in logs and in the response message, so a stub can never be
	// mistaken for a real delivery.
	Name() string
}

// LocalFixedCodeProvider is SMSProvider's P1 local implementation: it delivers nothing and returns
// the documented fixed code. It is honest about itself through Name().
type LocalFixedCodeProvider struct {
	Code string
	TTL  time.Duration
}

// NewLocalFixedCodeProvider returns P1's stub channel with the documented code and window.
func NewLocalFixedCodeProvider() *LocalFixedCodeProvider {
	return &LocalFixedCodeProvider{Code: DevFixedSMSCode, TTL: DefaultSMSCodeTTL}
}

// Name states which implementation is answering, including the reason it can answer at all.
func (p *LocalFixedCodeProvider) Name() string {
	return "local_stub(固定测试验证码，见 packages/adapter/DEPENDENCIES.md 短信行)"
}

// SendCode returns the fixed code. A real provider draws this from crypto/rand and calls the
// carrier; the handler cannot tell the two apart, which is the point of the seam.
func (p *LocalFixedCodeProvider) SendCode(_ context.Context, phone string) (string, time.Duration, error) {
	if strings.TrimSpace(phone) == "" {
		return "", 0, errors.New("auth: sms provider requires a phone")
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = DefaultSMSCodeTTL
	}
	return p.Code, ttl, nil
}

// -----------------------------------------------------------------------------
// middleware
// -----------------------------------------------------------------------------

// Gin context keys. "user_id" / "family_id" / "role" keep the names the pre-existing handlers read;
// "member_id" and "auth_session" are added by this card.
const (
	CtxAccountID = "user_id"
	CtxFamilyID  = "family_id"
	CtxMemberID  = "member_id"
	CtxRole      = "role"
	CtxPVersion  = "pver"
	CtxScope     = "scope"
	CtxSession   = "auth_session"
)

// MemberResolver looks up the caller's member row inside the session family. Returning an error that
// is not ErrNoMember aborts the request with 500 rather than degrading to an anonymous caller --
// fail closed (PRD 15.2).
type MemberResolver func(ctx context.Context, familyID, accountID string) (memberID string, err error)

// ErrNoMember is the "this account is not a member of the session family" answer.
var ErrNoMember = errors.New("no member row for this account in the session family")

// Middleware enforces PRD 3.4.1「JWT 必须带 family_id 与角色快照」and 15.6「鉴权在每个服务的中间件里
// 由同一 SDK 完成」, which tech plan §1.1 states as the reason business handlers contain no
// family/role judgement of their own.
type Middleware struct {
	Signer  *Signer
	Resolve MemberResolver
	// OnDenied records a rejected request. PRD 15.5 requires 越权尝试全部落审计, so a request that
	// gets 401/403 has to leave a row behind; nil is not accepted (see NewMiddleware).
	OnDenied func(c *gin.Context, sess *Session, event, reason string)
	// OnboardingRoutes is the closed "METHOD /full/path" set an onboarding-scoped token may reach
	// (OnboardingAllowedRoutes). A nil or empty set denies every route, which is the fail-closed
	// default: a process that forgot to wire its prefix gets "403 insufficient_scope" rather than an
	// accidentally open建家口 or a token that can read a family.
	OnboardingRoutes map[string]bool
}

// NewMiddleware refuses a middleware without a member resolver: an authenticator that cannot say
// which member is calling would silently hand handlers an empty member_id.
func NewMiddleware(signer *Signer, resolve MemberResolver, onDenied func(c *gin.Context, sess *Session, event, reason string)) (*Middleware, error) {
	if signer == nil {
		return nil, errors.New("auth: signer required")
	}
	if resolve == nil {
		return nil, errors.New("auth: MemberResolver required")
	}
	return &Middleware{Signer: signer, Resolve: resolve, OnDenied: onDenied}, nil
}

// Handler is the gin middleware.
func (m *Middleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := bearer(c.GetHeader("Authorization"))
		if err != nil {
			m.reject(c, nil, "cross_family_attempt", err.Error())
			return
		}
		claims, err := m.Signer.Verify(raw)
		if err != nil {
			// The family path is tried FIRST, so a real session can never be downgraded to the
			// onboarding branch by a handler bug; only a token authz.Verify refuses -- a token with no
			// fid and scope="onboarding", which Verify rejects as 「missing fid」 -- is considered here.
			onboarding, onboardingErr := m.Signer.VerifyOnboarding(raw)
			if onboardingErr != nil {
				m.reject(c, nil, "cross_family_attempt", err.Error())
				return
			}
			m.allowOnboarding(c, onboarding)
			return
		}

		memberID, err := m.Resolve(c.Request.Context(), claims.FamilyID, claims.Subject)
		if err != nil {
			if errors.Is(err, ErrNoMember) {
				// A token whose family no longer contains this account is the classic
				// "left / removed / switched" case: 401, and audited as an attempt.
				m.reject(c, nil, "cross_family_attempt", "member row absent for token family")
				return
			}
			m.reject(c, nil, "cross_family_attempt", "member resolution failed: "+err.Error())
			return
		}

		sess := &Session{
			AccountID: claims.Subject,
			FamilyID:  claims.FamilyID,
			MemberID:  memberID,
			Role:      claims.Role,
			PVersion:  claims.PVersion,
			Claims:    claims,
		}
		c.Set(CtxAccountID, sess.AccountID)
		c.Set(CtxFamilyID, sess.FamilyID)
		c.Set(CtxMemberID, sess.MemberID)
		c.Set(CtxRole, sess.Role)
		c.Set(CtxPVersion, sess.PVersion)
		c.Set(CtxSession, sess)
		c.Next()
	}
}

// allowOnboarding runs the onboarding-only half of the gate: install a session with NO family_id and
// admit the request only if its route is in the closed bootstrap set.
//
// CtxFamilyID / CtxMemberID / CtxRole are deliberately not set. Handlers read those by name, so a
// family-scoped handler reached by an onboarding token finds no session value at all and answers its
// own 401 -- an empty family boundary is never silently handed to a family query (PRD 15.2, and the
// reason this card's whole bootstrap is enforced here rather than in the client's route table).
func (m *Middleware) allowOnboarding(c *gin.Context, claims *authz.Claims) {
	sess := &Session{AccountID: claims.Subject, Scope: ScopeOnboarding, Claims: claims}
	route := c.Request.Method + " " + c.FullPath()
	if !m.OnboardingRoutes[route] {
		m.rejectOnboarding(c, sess, route)
		return
	}
	c.Set(CtxAccountID, sess.AccountID)
	c.Set(CtxScope, sess.Scope)
	c.Set(CtxSession, sess)
	c.Next()
}

// rejectOnboarding answers 403 insufficient_scope -- 403, not 401: the caller IS authenticated, the
// session simply has no family to act inside, and conflating the two would tell the client its login
// broke when the real answer is 「先创建家庭」. The attempt is audited as PRD 15.5's 越权尝试.
func (m *Middleware) rejectOnboarding(c *gin.Context, sess *Session, route string) {
	reason := "onboarding token 尝试访问家庭域路由 " + route
	if m.OnDenied != nil {
		m.OnDenied(c, sess, "cross_family_attempt", reason)
	}
	c.AbortWithStatusJSON(403, gin.H{
		"error":   "insufficient_scope",
		"code":    "no_family",
		"message": "当前会话尚无家庭，只能创建家庭或读取本人账号信息；请先创建或加入一个家庭",
	})
}

// reject answers 401 and audits. The body carries a "message" because the client's request.ts
// surfaces exactly that key when a call fails (web/src/utils/request.ts).
func (m *Middleware) reject(c *gin.Context, sess *Session, event, reason string) {
	if m.OnDenied != nil {
		m.OnDenied(c, sess, event, reason)
	}
	c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized", "message": "登录状态无效或不属于当前家庭"})
}

func bearer(header string) (string, error) {
	const prefix = "Bearer "
	if header == "" {
		return "", errors.New("missing Authorization header")
	}
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("Authorization header must be Bearer <token>")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", errors.New("empty bearer token")
	}
	return token, nil
}

// SessionFrom reads the authenticated session the middleware installed. Handlers use it instead of
// picking family_id/role out of the gin context by hand, so there is one place that decides what an
// unauthenticated handler does (it is a bug, and it answers 500 rather than running unscoped).
func SessionFrom(c *gin.Context) (*Session, error) {
	v, ok := c.Get(CtxSession)
	if !ok {
		return nil, errors.New("auth middleware did not run for this route")
	}
	sess, ok := v.(*Session)
	if !ok || sess == nil {
		return nil, errors.New("auth middleware stored an unexpected value")
	}
	return sess, nil
}

// Require performs the shared SDK's scope/module + action check for this session (PRD 15.2, 15.3)
// and audits a refusal the way 15.5 demands. resource is an authz resource key, e.g.
// authz.ResourceHomeOSModuleConfig or a registry code.
func Require(c *gin.Context, m *Middleware, sess *Session, resource, action string) bool {
	if authz.Can(c.Request.Context(), sess.Claims, authz.ScopeModule, resource, action, nil) {
		return true
	}
	if m != nil && m.OnDenied != nil {
		m.OnDenied(c, sess, "cross_family_attempt", "role "+sess.Role+" may not "+action+" "+resource)
	}
	c.JSON(403, gin.H{"error": "forbidden", "message": "没有该操作的权限"})
	return false
}

// -----------------------------------------------------------------------------
// key material
// -----------------------------------------------------------------------------

func readKeyPEM() ([]byte, string, error) {
	if path := strings.TrimSpace(os.Getenv(EnvPrivateKeyPEMPath)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("读取 %s=%s: %w", EnvPrivateKeyPEMPath, path, err)
		}
		return raw, EnvPrivateKeyPEMPath, nil
	}
	if inline := strings.TrimSpace(os.Getenv(EnvPrivateKeyPEM)); inline != "" {
		return []byte(strings.ReplaceAll(inline, `\n`, "\n")), EnvPrivateKeyPEM, nil
	}
	return nil, "", nil
}

func parsePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("PEM 解码失败：内容不是 PEM 块")
	}
	switch {
	case strings.Contains(block.Type, "OPENSSH"):
		return nil, errors.New("不支持 OPENSSH 格式的私钥，请用 PKCS#8（openssl genpkey -algorithm RSA）")
	// 顺序重要："RSA PRIVATE KEY" 同样包含子串 "PRIVATE KEY"，先判定 PKCS#1 才不会把
	// openssl genrsa 的默认输出错交给 ParsePKCS8PrivateKey（那会报 "use ParsePKCS1PrivateKey instead"）。
	case strings.Contains(block.Type, "RSA PRIVATE KEY"):
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析 PKCS#1 私钥: %w", err)
		}
		return key, nil
	case strings.Contains(block.Type, "PRIVATE KEY"):
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析 PKCS#8 私钥: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS#8 私钥不是 RSA，实际 %T", parsed)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("不认识的 PEM 块类型 %q：需要 PKCS#1（RSA PRIVATE KEY）或 PKCS#8（PRIVATE KEY）", block.Type)
	}
}

// configuredKeyID honours $HOMEOS_JWT_KEY_ID and otherwise uses the RFC 7638 thumbprint, which is
// stable per key and therefore needs no operator action in development.
func configuredKeyID(key *rsa.PrivateKey) string {
	if v := strings.TrimSpace(os.Getenv(EnvKeyID)); v != "" {
		return v
	}
	return keyIDFor(&key.PublicKey)
}

func keyIDFor(pub *rsa.PublicKey) string {
	sum := sha256.Sum256([]byte(pub.N.String() + "|" + strconv.FormatInt(int64(pub.E), 10)))
	return "homeos-" + base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}
