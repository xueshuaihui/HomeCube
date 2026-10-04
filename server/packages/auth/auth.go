// Package auth is the shared token issuer + request authenticator (PRD 15.6
// 「鉴权 SDK 由 svc-homeos 发布、各服务共用同一实现」).
//
// # 为什么落在 packages/auth 而不是 services/svc-homeos/internal/auth
//
// 它原来在 svc-homeos 的 internal/ 下，而 svc-finance 也要用其中的中间件
// （NewSigner / NewMiddleware / SessionFrom）。Go 语言规则禁止跨服务引用 internal 包：
//
//	services/svc-finance/internal/handler/finance.go:13:2:
//	    use of internal package .../services/svc-homeos/internal/auth not allowed
//
// 也就是说 finance 服务**在本机就编译不过**，整个 P1 无法交付。
// 鉴权 SDK 本来就是「发布一份、各服务共用」的东西，位置就该在 packages/ 下 ——
// 这次移动是让代码回到它本来就该在的地方，不是为绕过编译临时开的后门。
//
// # 与 packages/authz 的分工（边界没有变）
//
// authz 是共享 SDK 且**只做验签**（PRD 15.6）。签发方（svc-homeos）持有私钥，
// 私钥不出 svc-homeos；其余服务（含 finance）通过 authz.Verify 验签。
//
// Algorithm: RS256, because authz.Verify rejects every non-RSA signing method outright
// (policy.go「unexpected signing method」). The previous handler code built an RS256 token and then
// overwrote its header with HS256 while signing with a []byte secret, so SignedString always
// errored and POST /auth/login could only answer 500.
package auth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
//	POST {prefix}/families           -- 创建家庭, the act this whole scope exists to allow (PRD 3.4.1, 17.8)
//	GET  {prefix}/families         -- the CALLER'S OWN account-family list, which is how the client knows
//	                               it is in 「登录后无家庭」 state. It reads homeos_members by the token's
//	                               sub only; it never takes a family id from the request, so it cannot
//	                               become a way to enumerate somebody else's household.
//	POST {prefix}/family/invite/accept -- 加入已有家庭 (PRD 3.4.1 接口表): the OTHER way out of
//	                               「登录后无家庭」. The route reads no family data with the OLD session --
//	                               the family boundary only exists after the code is spent, and the
//	                               handler re-signs a fresh family session from the rows the accept
//	                               transaction wrote -- so the token needs a family to act IN, it needs
//	                               NOT to have one yet. Still authenticated: an anonymous POST is 401 at
//	                               the middleware, and an invalid code is 404 from the handler.
//	GET  {prefix}/auth/me        -- session introspection (scope, families), same account-only reads
//	POST {prefix}/auth/refresh   -- rotate the onboarding pair so the建家页 survives its own 10 minutes
//	POST {prefix}/auth/logout     -- revoking one's own refresh row is always safe
const (
	OnboardingRouteCreateFamily = "POST %s/families"
	OnboardingRouteListFamilies = "GET %s/families"
	OnboardingRouteAcceptInvite = "POST %s/family/invite/accept"
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
		OnboardingRouteAcceptInvite,
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

		// 家庭边界强制校验（PRD 15.2「判定以 family_id 为界」）。
		//
		// 为什么必须在中间件、而不是靠每个 handler 自己比对：token 里的 claims.FamilyID
		// 只能证明「你是 X 家庭的人」，不能证明「你请求的 family_id 就是 X 家庭」。
		// 只要 handler 直接读客户端传来的 family_id，任何一个 B 家庭的合法用户改一下
		// URL 里的 family_id 就能读走 A 家庭的全部数据 —— 签名完全合法，无需任何伪造。
		//
		// 实测（越权复现）：用 13800138001（B 家庭）的 access_token 请求
		//   GET /api/finance/transactions?family_id=<A家庭的 UUID>
		// 返回 200 和 A 家庭流水的完整字段（id / family_id / amount_cents / account_id …）。
		//
		// 这里的检查覆盖请求里出现的**所有** family_id 形态（query 与所有编码的 body 都不放过，
		// 见 claimedFamilyIDs 的文件头），任何一个与 token 的 fid 不一致就 403 + 落审计。
		// 放在中间件的好处是新增 handler 默认就是安全的 —— 不需要记得在每个 handler 里写这行。
		//
		// 注意这一层只是「声明值一致性」检查，不是家庭边界本身：路径里的资源 id 属不属于本家庭
		// 只有查库才知道，那由各服务 handler 以 sess.FamilyID 为唯一作用域来源来保证
		// （svc-finance 见 internal/handler/scope.go + repo 的 `WHERE id = ? AND family_id = ?`）。
		if !isFamilyScopedRoute(c.Request) {
			if claimed := claimedFamilyIDs(c.Request); len(claimed) > 0 {
				for _, f := range claimed {
					if f != sess.FamilyID {
						m.rejectForeignClaim(c, sess, f)
						return
					}
				}
			}
		}

		c.Next()
	}
}

// isFamilyScopedRoute 判断这条路由是否**本身就是**「跨家庭」语义，因此不能被
// 「family_id 必须等于 token 的 fid」这条规则拦。
//
// 目前只有一类：POST /family/switch —— 它的用途就是切换到**另一个**家庭，
// 请求体里的 family_id 按定义不等于当前 token 的 fid。它的归属校验由 handler 自己
// 负责且更完整（handler 会查该账号是否真属于目标家庭，并落 PRD 15.5 的越权审计，
// 见 handler/family.go:416 的「切换家庭请求了非本人所属的家庭」）。
//
// 如果中间件先拦掉，handler 那段就永远走不到：返回码从 handler 的 403 变成中间件的 401，
// 且那条审计行消失 —— 这正是本函数存在的原因：不要让通用规则吃掉专用语义。
// 将来若新增「加入家庭 / 邀请接受」这类同语义路由，必须一并登记到这里，
// 否则它们会被误判成越权。
func isFamilyScopedRoute(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/family/switch")
}

// claimedFamilyKeys 是要在请求里收集的同名取值。
//
// 键名覆盖 fid / family_id / familyId 三种：仓里三种都出现过 —— middleware 文档里的
// /members/snapshot?fid=、finance 全部读接口的 ?family_id=、以及部分 handler 的 body。
// 漏掉任何一种，那条路径就是绕过口。空值不算声明（?family_id= 等价于没传，handler 自己会拒）。
var claimedFamilyKeys = []string{"fid", "family_id", "familyId"}

// claimedFamilyIDs 收集请求里出现的所有 family_id 取值（query string 与请求体）。
//
// # 为什么请求体的解析不能看 Content-Type（这是本次缺陷的根因 A）
//
// 旧实现在这里有一句
//
//	ct := r.Header.Get("Content-Type")
//	if !strings.HasPrefix(ct, "application/json") { return out }
//
// 于是「body 是 JSON，但 Content-Type 写成 text/plain / 不写 / 写成 multipart」的请求
// 根本不会被检查 —— 而 gin 的 ShouldBindJSON **同样不看 Content-Type**（它无条件
// json.Unmarshal 整个 body，见 gin@v1.12.0/binding/json.go）。两边口径不一致，中间件
// 这一层就变成一个可以按一个 header 关掉的开关。
//
// 实测（本次复现的缺陷）：
//
//	POST /api/finance/transactions
//	Content-Type: text/plain
//	{"family_id":"<别人家>","amount_cents":-1000,...}
//	→ 旧：201，流水写进了别人家；application/json 同样内容 → 401
//
// multipart 更糟：form:"family_id" 走的是 form mapper，body 是 multipart 分段，
// 任何「按 Content-Type 决定要不要看」的写法都要为每种编码补一个分支，早晚漏一种。
//
// 现在的做法：把 body 的前 maxClaimedFamilyBodyBytes 字节**原样缓冲**（读多少就还多少，
// 绝不截断，handler 看到的字节流和没有中间件时完全一致），然后对这段字节依次尝试
// JSON / urlencoded / multipart 三种编码来提取声明值。尝试失败只是「这一种编码没命中」，
// 不影响 handler 自己怎么绑。
//
// # 这不是安全边界
//
// 这里只处理「客户端**声明**的 family_id」。安全边界在 handler：作用域一律取
// sess.FamilyID，按 id 读写都带 `AND family_id = ?`（见 svc-finance 的 handler/scope.go 与
// repo/finance.go）。中间件看不见路径里的资源 id 属不属于本家庭 —— 那要查库才知道。
// 所以本函数的作用是「让改 family_id 这种探测在所有编码下都被同一口径拒掉」，
// 而不是唯一的防线。
func claimedFamilyIDs(r *http.Request) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	q := r.URL.Query()
	for _, key := range claimedFamilyKeys {
		add(q.Get(key))
	}

	raw, oversized := bufferBody(r)
	if raw == nil || oversized {
		// 体积超过缓冲上限：不在这里深解析（可能切在 JSON/multipart 中间，
		// 解析出来的半截结果既不可信也可能误判），但字节已经原样还给 handler。
		// 这类请求仍由 handler 侧的 sess.FamilyID 兜住，不依赖本函数。
		return out
	}

	for _, v := range claimedFamilyIDsInBytes(raw, r.Header.Get("Content-Type")) {
		add(v)
	}
	return out
}

// claimedFamilyIDsInBytes 依次按三种编码尝试从已缓冲的请求体里取出声明的 family_id。
//
// 三种编码都跑一遍而不是「按 Content-Type 选一种」：Content-Type 正是攻击者可控的输入，
// 用它来决定「要不要检查」就等于把开关交给攻击者。
func claimedFamilyIDsInBytes(raw []byte, contentType string) []string {
	var out []string

	// 1) JSON：不看 Content-Type，只要字节流本身是 JSON 对象就算。
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err == nil {
		for _, key := range claimedFamilyKeys {
			if s, ok := body[key].(string); ok {
				out = append(out, s)
			}
		}
		// 嵌套一层也收：{"data":{"family_id":"..."}}
		if nested, ok := body["data"].(map[string]any); ok {
			for _, key := range claimedFamilyKeys {
				if s, ok := nested[key].(string); ok {
					out = append(out, s)
				}
			}
		}
	}

	// 2) urlencoded / 裸 text：family_id=<uuid>&amount=1
	if vals, err := url.ParseQuery(string(raw)); err == nil {
		for _, key := range claimedFamilyKeys {
			for _, s := range vals[key] {
				out = append(out, s)
			}
		}
	}

	// 3) multipart/form-data：voice-entry 这类上传口，form:"family_id" 是个普通分段。
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		_, params, err := mime.ParseMediaType(contentType)
		if err == nil && params["boundary"] != "" {
			mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break // 缓冲可能被截断，读不动就停，剩余由 handler 自己处理
				}
				for _, key := range claimedFamilyKeys {
					if p.FormName() == key {
						if v, err := io.ReadAll(io.LimitReader(p, 256)); err == nil {
							out = append(out, string(v))
						}
					}
				}
				_ = p.Close()
			}
		}
	}

	// 4) multipart 分段但 Content-Type 被改写了（攻击者把 multipart body 标成 text/plain）：
	// 退化为按分段文本扫一遍，宁可多判也不漏判。
	if !strings.HasPrefix(strings.ToLower(contentType), "multipart/") && bytes.Contains(raw, []byte(`name="family_id"`)) {
		for _, m := range familyFormPartRe.FindAllSubmatch(raw, -1) {
			out = append(out, string(m[1]))
		}
	}

	return out
}

// familyFormPartRe 匹配 multipart 里一个名为 family_id 的**普通字段**分段
// （没有 Content-Type、内容不含 CRLF，即 UUID 这种短值）。
var familyFormPartRe = regexp.MustCompile(`(?s)name="(?:fid|family_id|familyId)"\r?\n\r?\n([^\r\n]{1,256})`)

// maxClaimedFamilyBodyBytes 是「够读完一个 JSON 对象的量」，避免为了安全检查把
// 大 body 整个读进内存。1 MiB 远大于任何家庭接口的请求体。
const maxClaimedFamilyBodyBytes = 1 << 20

// bodyBufferLimitPlusOne 让 oversized 可判定：真的读出第 max+1 个字节，说明 body 超过上限。
const bodyBufferLimitPlusOne = maxClaimedFamilyBodyBytes + 1

// bufferBody 读出请求体的前 maxClaimedFamilyBodyBytes+1 个字节，并把读到的字节**原样放回**，
// 使 handler 之后读到完整、未被改写的流。返回 (读到的字节, 是否超过上限)。
//
// 旧实现用 io.ReadAll(io.LimitReader(...)) 再把 r.Body 换成只含这段前缀的 reader，
// 等于悄悄把超过 1 MiB 的 body 截断了（大附件的 multipart 上传会被弄坏），
// 而且截断后的 JSON 解析必然失败 —— 越权检查就静默失效。
func bufferBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, false
	}
	buf := make([]byte, bodyBufferLimitPlusOne)
	n, err := io.ReadFull(r.Body, buf)
	if n > 0 {
		read := buf[:n]
		// 不 Close 原 body：剩余字节还要由 handler 继续读，Close 之后读会报错。
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(read), r.Body))
		if err == nil { // 读满了 max+1，说明超过上限
			return read[:maxClaimedFamilyBodyBytes], true
		}
		return read, false
	}
	return nil, err != nil
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

// rejectForeignClaim 拒一次「声明的 family_id ≠ token 的 fid」：403 + cross_family_denied + 审计。
//
// 403 而不是 401：调用方拿的是**有效** token，只是伸手要进别人的家庭 —— 回 401 会让客户端把
// 越权探测当成登录过期去重新登录重试，掩盖告警（与 rejectOnboarding 同一口径）。
//
// 旧行为是各 handler 自己比对后回 401；检查上移到中间件后若沿用 401，PRD 15.5 的越权审计
// 就只剩 handler 那一半（且多数 handler 根本没比）。所以这里必须同时做两件事：
//  1. 经 OnDenied 落一条 cross_family_attempt —— 与 svc-finance scope.go 的 deny、
//     homeos 的 auditDenied 用同一个事件名，验收脚本按事件统计时不会漏；
//  2. 响应体和 svc-finance handler 层的同名拒绝保持同一形状（error/code/message 逐字一致），
//     让探测者无论被哪一层拦下都只看到同一个 403，不泄露「哪层先拦」这一信息。
func (m *Middleware) rejectForeignClaim(c *gin.Context, sess *Session, claimed string) {
	reason := "请求声明的 family_id 与 token 家庭不一致: claimed=" + claimed
	if m.OnDenied != nil {
		m.OnDenied(c, sess, "cross_family_attempt", reason)
	}
	c.AbortWithStatusJSON(403, gin.H{
		"error":   "forbidden",
		"code":    "cross_family_denied",
		"message": "无权访问其他家庭的数据",
	})
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
