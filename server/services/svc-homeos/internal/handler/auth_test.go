// auth_test.go covers the identity handlers against a real database (in-memory sqlite, the shape the
// svc-finance handler tests already use) rather than against stubs: the defect this card fixes WAS the
// handlers inventing a user, a family and a signature. Every assertion below therefore reads either a
// stored row or a token the shared SDK can verify.
//
// The tables are created from the APPLIED migration sequence's column set: 0006's four identity tables
// plus homeos_families.pver, homeos_audit_log and homeos_invitations.revoked_* from 0009, and
// homeos_family_module / homeos_dynamic from 0007 (POST /families writes faces + a dynamic entry in one
// transaction, so the fixture has to have those rows to land in). homeos_users still has no deleted_at
// and homeos_families still has none either, because no migration in the sequence adds one -- the model
// used to declare both, which is what made repo.FindUserByPhone fail; see model/identity.go.
package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

const testPhone = "13800000001"

// seededPVer is an arbitrary non-1 value: asserting it in the response is what proves pver is READ from
// homeos_families rather than defaulted by the handler (PRD 15.6, and hard rule 1's "never invent a
// pver").
const seededPVer = int64(7)

// newSignerForTest writes a PKCS#1 RS256 key into the test's own temp dir and points the documented
// env key at it, so the signer is loaded through the same path production uses.
func newSignerForTest(t *testing.T) *svcauth.Signer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "jwt.pem")
	require.NoError(t, os.WriteFile(file, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600))

	t.Setenv(svcauth.EnvPrivateKeyPEMPath, file)
	signer, err := svcauth.NewSigner(nil)
	require.NoError(t, err)
	return signer
}

// TestNewSigner_RefusesWithoutKeyMaterial is deliverable 4's regression guard: the previous behaviour
// generated an in-process key, which silently invalidated every issued token on restart.
func TestNewSigner_RefusesWithoutKeyMaterial(t *testing.T) {
	t.Setenv(svcauth.EnvPrivateKeyPEMPath, "")
	t.Setenv(svcauth.EnvPrivateKeyPEM, "")

	signer, err := svcauth.NewSigner(nil)
	require.Error(t, err, "缺少 RS256 私钥必须启动失败，而不是退回临时密钥")
	assert.Nil(t, signer)
	assert.Contains(t, err.Error(), svcauth.EnvPrivateKeyPEMPath)
	assert.Contains(t, err.Error(), svcauth.EnvPrivateKeyPEM)
}

// setupIdentityDB builds the applied sequence's column set and seeds one account that owns one family.
func setupIdentityDB(t *testing.T) (*gorm.DB, string, string, string) {
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
		`CREATE TABLE homeos_sms_codes (
			id TEXT PRIMARY KEY, phone TEXT NOT NULL, code TEXT NOT NULL,
			expires_at DATETIME NOT NULL, used BOOLEAN NOT NULL DEFAULT 0, created_at DATETIME NOT NULL)`,
		`CREATE TABLE homeos_refresh_tokens (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, token_hash TEXT NOT NULL,
			expires_at DATETIME NOT NULL, revoked BOOLEAN NOT NULL DEFAULT 0, created_at DATETIME NOT NULL)`,
		// homeos_invitations: 0006 的三态 CHECK + 0009 的 revoked_*。id / family_id / inviter_id 在
		// Postgres 都是 uuid NOT NULL，这里以 TEXT 承载同样的非空纪律（handler 写入的必须是真 UUID）。
		`CREATE TABLE homeos_invitations (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, code TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL, inviter_id TEXT NOT NULL, invitee_phone TEXT,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending','accepted','expired')),
			expires_at DATETIME NOT NULL, revoked_at DATETIME, revoked_by TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		// 0007's two projection tables: POST /families writes one face row per selected code plus the
		// family.created dynamic inside its transaction.
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
		// 0009's audit sink: the CHECK constraints are reproduced, because "the nine classes are the
		// document's" is only real if a tenth value fails to land.
		`CREATE TABLE homeos_audit_log (
			id TEXT PRIMARY KEY, family_id TEXT, target_family_id TEXT, code TEXT,
			event TEXT NOT NULL CHECK (event IN ('login','permission_change','cross_family_attempt',
				'export','delete','l3_read','rule_toggle','dead_letter_replay','on_behalf_write')),
			actor_member_id TEXT, actor_user_id TEXT, action TEXT, entity TEXT, entity_id TEXT,
			result TEXT NOT NULL DEFAULT 'allowed' CHECK (result IN ('allowed','denied')),
			reason TEXT, ip TEXT, user_agent TEXT,
			created_at DATETIME NOT NULL, occurred_at DATETIME NOT NULL)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}

	const (
		accountID = "11111111-1111-4111-8111-111111111111"
		familyID  = "22222222-2222-4222-8222-222222222222"
		memberID  = "33333333-3333-4333-8333-333333333333"
	)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_users (id, phone, name, created_at, updated_at) VALUES (?, ?, ?, datetime('now'), datetime('now'))`,
		accountID, testPhone, "小明").Error)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_families (id, name, owner_id, timezone, currency, pver, created_at, updated_at)
		 VALUES (?, ?, ?, 'Asia/Shanghai', 'CNY', ?, datetime('now'), datetime('now'))`,
		familyID, "真实家庭", accountID, seededPVer).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_members (id, family_id, user_id, role, created_at, updated_at)
		 VALUES (?, ?, ?, 'owner', datetime('now'), datetime('now'))`,
		memberID, familyID, accountID).Error)

	return db, accountID, familyID, memberID
}

// newBootstrapDB seeds ONLY an account: the family-less state the onboarding scope exists for. The
// verification code row is inserted by the caller through the real SMS path.
func newBootstrapPhone(t *testing.T, db *gorm.DB, phone string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_sms_codes (id, phone, code, expires_at, used, created_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, datetime('now', '+5 minutes'), 0, datetime('now'))`,
		phone, svcauth.DevFixedSMSCode).Error)
}

// newServices builds the handler bundle the way cmd does, including the route prefix the onboarding
// allowlist is derived from -- an empty prefix would deny everything and hide a wiring bug, so the test
// states the same "/api/homeos" main.go mounts.
func newServices(t *testing.T, db *gorm.DB) *Services {
	t.Helper()
	d, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok, "registry 必须有 homeos 这一行")
	return &Services{
		DB:          db,
		Signer:      newSignerForTest(t),
		SMS:         svcauth.NewLocalFixedCodeProvider(),
		Logger:      nil,
		RoutePrefix: d.RoutePrefix,
	}
}

// newIdentityRouter mounts the routes cmd/svc-homeos mounts, on the same prefix, so the onboarding
// allowlist the middleware enforces is the one the process would really run.
func newIdentityRouter(t *testing.T, s *Services) (*gin.Engine, *svcauth.Middleware) {
	t.Helper()

	mw, err := s.Middleware()
	require.NoError(t, err)

	d, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	public := r.Group(d.RoutePrefix)
	// The four public /auth/* routes cmd/svc-homeos main.go mounts, plus jwks: TestFamilylessLogin-
	// BootstrapsAFamilyEndToEnd drives the WHOLE bootstrap through the router (验证码 -> 登录 -> 轮换),
	// so leaving /auth/sms-code or /auth/refresh unmounted made it fail on a 404 of the fixture's own
	// making before it ever reached the assertions that matter.
	public.POST("/auth/sms-code", func(c *gin.Context) { SendSMSCode(c, s) })
	public.POST("/auth/login", func(c *gin.Context) { Login(c, s) })
	public.POST("/auth/refresh", func(c *gin.Context) { Refresh(c, s) })
	public.POST("/auth/logout", func(c *gin.Context) { Logout(c, s) })
	public.GET("/.well-known/jwks.json", func(c *gin.Context) { GetJWKS(c, s) })

	protected := public.Group("", mw.Handler())
	{
		protected.POST("/families", func(c *gin.Context) { CreateFamily(c, s) })
		protected.GET("/families", func(c *gin.Context) { ListFamilies(c, s) })
		protected.GET("/auth/me", func(c *gin.Context) { Me(c, s) })
		protected.POST("/family/switch", func(c *gin.Context) { SwitchFamily(c, s) })
		// 邀请三接口，与 cmd/svc-homeos 一致按 PRD 3.4.1 的路径挂载：
		// allowlist、404/409 分支与 onboarding 接受路径都在这张真实路由表上测。
		protected.POST("/family/invite/accept", func(c *gin.Context) { AcceptInvite(c, s) })
		protected.POST("/members/invite", func(c *gin.Context) { CreateInvite(c, s) })
		protected.GET("/family/invites", func(c *gin.Context) { ListInvites(c, s) })
		protected.DELETE("/family/invites/:id", func(c *gin.Context) { RevokeInvite(c, s) })
		protected.GET("/search", func(c *gin.Context) { c.JSON(http.StatusNoContent, nil) })
	}
	return r, mw
}

// doJSON sends one request through the router with an optional bearer token.
func doJSON(t *testing.T, r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// postJSON runs one request through the handler and returns the recorder.
func postJSON(t *testing.T, h gin.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "203.0.113.7:1234"
	h(c)
	return w
}

// TestSendSMSCodeThenLoginIssuesVerifiableTokensForRealRows is the end-to-end of deliverable 3.
func TestSendSMSCodeThenLoginIssuesVerifiableTokensForRealRows(t *testing.T) {
	db, accountID, familyID, memberID := setupIdentityDB(t)
	s := newServices(t, db)

	w := postJSON(t, func(c *gin.Context) { SendSMSCode(c, s) }, "/api/homeos/auth/sms-code",
		gin.H{"phone": testPhone})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var sent SMSCodeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sent))
	assert.Equal(t, int(svcauth.DefaultSMSCodeTTL.Seconds()), sent.ExpiresIn)
	assert.NotContains(t, w.Body.String(), svcauth.DevFixedSMSCode, "响应体不得回带验证码")

	var stored int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_sms_codes WHERE phone = ? AND used = 0", testPhone).
		Scan(&stored).Error)
	assert.Equal(t, int64(1), stored, "验证码必须落库，否则谈不上单次有效与过期")

	w = postJSON(t, func(c *gin.Context) { Login(c, s) }, "/api/homeos/auth/login",
		gin.H{"phone": testPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var res LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, familyID, res.FamilyID)
	assert.Equal(t, "owner", res.Role)
	assert.Equal(t, int(svcauth.AccessTokenTTL.Seconds()), res.ExpiresIn)
	assert.NotEmpty(t, res.AccessToken)
	assert.NotEmpty(t, res.RefreshToken)
	// pver is read from homeos_families (0009's column), which is why the seeded 7 -- not a constant 0
	// or 1 -- comes back. A handler-side default would be the fake data hard rule 1 forbids.
	assert.Equal(t, seededPVer, res.PVer)
	assert.Empty(t, res.Scope, "家庭会话不带 scope")
	// Real rows, and specifically NOT the removed 「示例家庭 / family-001」.
	require.NotNil(t, res.User)
	assert.Equal(t, accountID, res.User.ID)
	assert.Equal(t, testPhone, res.User.Phone)
	assert.Equal(t, "小明", res.User.Name)
	require.Len(t, res.Families, 1)
	assert.Equal(t, "真实家庭", res.Families[0].Name)
	assert.NotContains(t, w.Body.String(), "示例家庭")
	assert.NotContains(t, w.Body.String(), "family-001")

	// The access token must be RS256 and carry the SDK's claims -- authz.Verify rejects a non-RSA
	// signing method, which is what made the old header swap unworkable.
	claims, err := s.Signer.Verify(res.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, accountID, claims.Subject)
	assert.Equal(t, familyID, claims.FamilyID)
	assert.Equal(t, "owner", claims.Role)

	// The refresh half is stored as a hash and stays live until logout.
	var (
		rowTokenHash string
		rowRevoked   bool
	)
	require.NoError(t, db.Raw("SELECT token_hash, revoked FROM homeos_refresh_tokens WHERE user_id = ?", accountID).
		Row().Scan(&rowTokenHash, &rowRevoked))
	assert.Equal(t, svcauth.HashToken(res.RefreshToken), rowTokenHash, "库里只能有 refresh token 的散列")
	assert.False(t, rowRevoked)

	// ③ The same code cannot be spent twice (PRD 3.4.1 验证码单次有效).
	w = postJSON(t, func(c *gin.Context) { Login(c, s) }, "/api/homeos/auth/login",
		gin.H{"phone": testPhone, "code": svcauth.DevFixedSMSCode})
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	// ④ logout revokes exactly that stored row (deliverable 5).
	w = postJSON(t, func(c *gin.Context) { Logout(c, s) }, "/api/homeos/auth/logout",
		gin.H{"refresh_token": res.RefreshToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out LogoutResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, int64(1), out.Revoked)

	require.NoError(t, db.Raw("SELECT token_hash, revoked FROM homeos_refresh_tokens WHERE user_id = ?", accountID).
		Row().Scan(&rowTokenHash, &rowRevoked))
	assert.True(t, rowRevoked)

	// A revoked refresh token no longer rotates.
	w = postJSON(t, func(c *gin.Context) { Refresh(c, s) }, "/api/homeos/auth/refresh",
		gin.H{"refresh_token": res.RefreshToken})
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	// The middleware's resolver finds the seeded member row -- the value every protected handler reads
	// as member_id, and the reason a removed member answers 401 instead of running with an empty scope.
	resolved, err := s.resolveMember(context.Background(), familyID, accountID)
	require.NoError(t, err)
	assert.Equal(t, memberID, resolved)
}

// TestLoginRejectsWrongCodeAndForeignFamily pins the two refusals the old stub could not express:
// an unverified code never reaches the token step, and a family_id the account does not occupy is
// not a session the service will sign.
func TestLoginRejectsWrongCodeAndForeignFamily(t *testing.T) {
	db, _, _, _ := setupIdentityDB(t)
	s := newServices(t, db)

	require.NoError(t, db.Exec(
		`INSERT INTO homeos_sms_codes (id, phone, code, expires_at, used, created_at)
		 VALUES ('a1', ?, '123456', datetime('now', '+5 minutes'), 0, datetime('now'))`, testPhone).Error)

	w := postJSON(t, func(c *gin.Context) { Login(c, s) }, "/api/homeos/auth/login",
		gin.H{"phone": testPhone, "code": "000000"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// A family this account is not a member of (PRD 15.2 boundary).
	other := "99999999-9999-4999-8999-999999999999"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_families (id, name, owner_id, timezone, currency, created_at, updated_at)
		 VALUES (?, '别人的家庭', '44444444-4444-4444-8444-444444444444', 'Asia/Shanghai', 'CNY', datetime('now'), datetime('now'))`,
		other).Error)
	w = postJSON(t, func(c *gin.Context) { Login(c, s) }, "/api/homeos/auth/login",
		gin.H{"phone": testPhone, "code": "123456", "family_id": other})
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "not_family_member")
}

// TestProtectedRoutesRequireBearerToken is deliverable 2's guard: the middleware is what puts
// user_id / family_id into the context, so a route behind it must answer 401 anonymously and 200 with
// a token -- and the 200 body must be the caller's own family, read through the seeded row.
func TestProtectedRoutesRequireBearerToken(t *testing.T) {
	db, _, familyID, _ := setupIdentityDB(t)
	s := newServices(t, db)
	r, _ := newIdentityRouter(t, s)

	w := doJSON(t, r, http.MethodGet, "/api/homeos/families", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "无 token 必须 401，而不是放行到空 family_id")

	// No member row for the token's subject -> still 401 (fail closed), not an empty session.
	bogus, err := s.signTokens(svcauth.Session{
		AccountID: "55555555-5555-5555-8555-555555555555", FamilyID: familyID, Role: "owner",
	})
	require.NoError(t, err)
	w = doJSON(t, r, http.MethodGet, "/api/homeos/families", bogus.access, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	newBootstrapPhone(t, db, testPhone)
	w = doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": testPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))

	w = doJSON(t, r, http.MethodGet, "/api/homeos/families", res.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var list ListFamiliesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Families, 1)
	assert.Equal(t, familyID, list.Families[0].ID)
	assert.Equal(t, "真实家庭", list.Families[0].Name)
	assert.Equal(t, "owner", list.Families[0].Role)

	// /family/switch to a family the caller does not belong to: 403 + audited attempt.
	w = doJSON(t, r, http.MethodPost, "/api/homeos/family/switch", res.AccessToken,
		gin.H{"family_id": "99999999-9999-4999-8999-999999999999"})
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())

	// PRD 15.6「越权尝试全部落审计日志（21.5）」+ 0009's sink, one row per refusal this sequence really
	// contains. The flat `== 1` this block replaces could not be true under any coherent reading: the
	// sequence holds THREE rejected requests, and two of them are 21.5's third class (跨家庭访问尝试)
	// by definition --
	//   ① a token that verifies but whose subject has no member row in the token's family (the
	//      removed/left-member replay, auth.go's ErrNoMember branch);
	//   ② POST /family/switch for a family this account does not occupy (PRD 15.2 boundary).
	// The third row is the anonymous `GET /families` with NO Authorization header at the top of this
	// test: there is no principal and no token family, so nothing was crossed and 21.5's class does not
	// describe it -- svcauth.Middleware.reject audits the bearer-parse failure anyway. That is the
	// middleware OVER-auditing (it also feeds 15.6's 连续 5 次触发通知管理员 counter with
	// unauthenticated noise), and it is stated here instead of being folded into an exact count; the
	// total is therefore pinned as a floor of the two real attempts.
	countDenied := func(like string) int64 {
		var n int64
		require.NoError(t, db.Raw(`SELECT count(*) FROM homeos_audit_log
			WHERE event = ? AND result = ? AND reason LIKE ?`,
			model.AuditEventCrossFamilyAttempt, model.AuditResultDenied, like).Scan(&n).Error)
		return n
	}
	assert.Equal(t, int64(1), countDenied("member row absent%"),
		"PRD 15.6：令牌家庭内已无本人成员行的重放必须落一条 denied 审计")
	assert.Equal(t, int64(1), countDenied("切换家庭请求了非本人所属的家庭%"),
		"PRD 15.6：切换到非本人所属家庭必须落一条 denied 审计")

	var denied int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_audit_log WHERE event = ? AND result = ?",
		model.AuditEventCrossFamilyAttempt, model.AuditResultDenied).Scan(&denied).Error)
	assert.GreaterOrEqual(t, denied, int64(2), "两处越权尝试各一条（0009 已应用，不再降级为日志）")

	// GET /auth/me with a family session: the snapshot the client's store reads.
	w = doJSON(t, r, http.MethodGet, "/api/homeos/auth/me", res.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var me MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &me))
	assert.Equal(t, familyID, me.FamilyID)
	assert.Equal(t, "owner", me.Role)
	assert.Equal(t, seededPVer, me.PVer)
	assert.Empty(t, me.Scope)
	assert.Len(t, me.Families, 1)
}

// TestRefreshRotatesAndOldTokenDies covers PRD 3.4.1's refresh 轮换 through the stored rows.
func TestRefreshRotatesAndOldTokenDies(t *testing.T) {
	db, accountID, _, _ := setupIdentityDB(t)
	s := newServices(t, db)

	require.NoError(t, db.Exec(
		`INSERT INTO homeos_sms_codes (id, phone, code, expires_at, used, created_at)
		 VALUES ('a3', ?, '123456', datetime('now', '+5 minutes'), 0, datetime('now'))`, testPhone).Error)
	login := postJSON(t, func(c *gin.Context) { Login(c, s) }, "/api/homeos/auth/login",
		gin.H{"phone": testPhone, "code": "123456"})
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	var res LoginResponse
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &res))

	w := postJSON(t, func(c *gin.Context) { Refresh(c, s) }, "/api/homeos/auth/refresh",
		gin.H{"refresh_token": res.RefreshToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var rotated RefreshResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rotated))
	assert.NotEqual(t, res.RefreshToken, rotated.RefreshToken)
	assert.Equal(t, int(svcauth.AccessTokenTTL.Seconds()), rotated.ExpiresIn)

	// The old row is revoked, the new one is live, and the contract's fields are the only ones sent.
	var revokedOld, liveNew int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_refresh_tokens WHERE user_id = ? AND revoked = 1", accountID).
		Scan(&revokedOld).Error)
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_refresh_tokens WHERE user_id = ? AND revoked = 0", accountID).
		Scan(&liveNew).Error)
	assert.Equal(t, int64(1), revokedOld)
	assert.Equal(t, int64(1), liveNew)

	// An ACCESS token cannot be used on the refresh endpoint: its hash was never stored.
	w = postJSON(t, func(c *gin.Context) { Refresh(c, s) }, "/api/homeos/auth/refresh",
		gin.H{"refresh_token": res.AccessToken})
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	// 单次有效 also still holds one rotation later.
	w = postJSON(t, func(c *gin.Context) { Refresh(c, s) }, "/api/homeos/auth/refresh",
		gin.H{"refresh_token": res.RefreshToken})
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	// And the rotated token works, with the same subject and role: re-verified through the signer,
	// because a rotation that produced an unverifiable token would lock the user out silently.
	w = postJSON(t, func(c *gin.Context) { Refresh(c, s) }, "/api/homeos/auth/refresh",
		gin.H{"refresh_token": rotated.RefreshToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var second RefreshResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &second))
	claims, err := s.Signer.Verify(second.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, accountID, claims.Subject)
	assert.Equal(t, "owner", claims.Role)
}

// TestFamilylessLoginBootstrapsAFamilyEndToEnd is deliverable 1's whole card as one test: a phone with
// no account and no family walks 验证码 -> 登录 (200, onboarding scope) -> GET /families (empty, not 403)
// -> POST /families (201 + a REAL family token) -> family-scoped read (200, session family_id).
//
// Before this card step ② answered 403 no_family, which made ④ unreachable because ④ needs a token
// that only ② can give -- the deadlock. The assertions below are on stored rows, not on response bodies
// alone: a family that exists only in the JSON is not a created family.
func TestFamilylessLoginBootstrapsAFamilyEndToEnd(t *testing.T) {
	const bootstrapPhone = "13900000099"
	db, _, _, _ := setupIdentityDB(t)
	s := newServices(t, db)
	r, _ := newIdentityRouter(t, s)

	// ① 验证码: the real SMS path, so the row repo.IssueSMSCode writes is the row Login spends.
	w := doJSON(t, r, http.MethodPost, "/api/homeos/auth/sms-code", "", gin.H{"phone": bootstrapPhone})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// ② 登录: 200 + onboarding, and NO family (PRD 3.4.1「创建家庭」+ nav §4.1 row ①「登录后无家庭」).
	w = doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": bootstrapPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, "无家庭登录必须 200，而不是 403 no_family：%s", w.Body.String())

	var onboarding LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &onboarding))
	assert.Equal(t, svcauth.ScopeOnboarding, onboarding.Scope)
	assert.Empty(t, onboarding.FamilyID, "无家庭时 family_id 必须留空，占位 id 就是假数据")
	assert.Empty(t, onboarding.Role)
	assert.Zero(t, onboarding.PVer, "没有家庭行可读 pver，就必须是 0 而不是编一个 1")
	assert.Equal(t, int(svcauth.OnboardingTokenTTL.Seconds()), onboarding.ExpiresIn)
	require.NotNil(t, onboarding.User)
	assert.Equal(t, bootstrapPhone, onboarding.User.Phone)
	assert.Len(t, onboarding.Families, 0)

	// The account row is the phone's own, created by the login path.
	var accountID string
	require.NoError(t, db.Raw("SELECT id FROM homeos_users WHERE phone = ?", bootstrapPhone).Row().Scan(&accountID))

	// authz.Verify -- the check EVERY service runs -- must refuse this token: an onboarding token
	// therefore cannot be replayed against another service's family routes either, by construction.
	// ErrorIs(ErrTokenInvalid) is what proves the refusal came out of the SDK verification path
	// (Signer.Verify wraps every authz.Verify rejection with it), not from an unrelated error; the
	// reason text is pinned because the security property is specifically 「no fid to authorize
	// against」 -- svcauth Middleware.Handler relies on Verify rejecting onboarding tokens exactly
	// this way (auth.go「which Verify rejects as 「missing fid」」).
	_, err := s.Signer.Verify(onboarding.AccessToken)
	require.ErrorIs(t, err, svcauth.ErrTokenInvalid, "onboarding token 必须被共享 SDK 拒绝")
	require.ErrorContains(t, err, "missing fid", "拒绝理由必须是缺少 family_id，否则就是别处的接线错误")
	claims, err := s.Signer.VerifyOnboarding(onboarding.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, accountID, claims.Subject)
	assert.Empty(t, claims.FamilyID)

	// ③ The account-scoped read works; a family-scoped route does not.
	w = doJSON(t, r, http.MethodGet, "/api/homeos/families", onboarding.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var empty ListFamiliesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &empty))
	assert.Len(t, empty.Families, 0)

	for _, denied := range []struct{ method, path string }{
		{http.MethodPost, "/api/homeos/family/switch"},
		{http.MethodGet, "/api/homeos/search?q=x"},
	} {
		w = doJSON(t, r, denied.method, denied.path, onboarding.AccessToken, gin.H{"family_id": "22222222-2222-4222-8222-222222222222"})
		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s 不得对 onboarding 会话开放", denied.method, denied.path)
		assert.Contains(t, w.Body.String(), "insufficient_scope")
		assert.Contains(t, w.Body.String(), "no_family")
	}

	// A family token must not be mistaken for an onboarding one in either direction.
	_, err = s.Signer.VerifyOnboarding(onboarding.RefreshToken)
	require.NoError(t, err, "refresh 半边同样是 onboarding token，/auth/refresh 才能轮换它")

	// ④ 建家 + 强制选面 in one atomic act (PRD 17.8).
	w = doJSON(t, r, http.MethodPost, "/api/homeos/families", onboarding.AccessToken, gin.H{
		"name": "自举家庭", "timezone": "Asia/Shanghai", "currency": "CNY",
		"modules": []string{"finance"},
	})
	require.Equal(t, http.StatusCreated, w.Code, "POST /families 必须用 onboarding token 建家成功：%s", w.Body.String())

	var created CreateFamilyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, created.ID, created.FamilyID)
	assert.Equal(t, "owner", created.Role, "创建者必须是 owner 成员行")
	assert.Equal(t, int64(1), created.PVer, "新家庭的 pver 取自写入的家庭行（0009 的 DEFAULT 1）")
	assert.NotEmpty(t, created.AccessToken)

	// Rows, not just the response: family + owner member (has_account) + one enabled face.
	var ownerMemberID string
	var hasAccount bool
	require.NoError(t, db.Raw(`SELECT id, (user_id IS NOT NULL) FROM homeos_members
		WHERE family_id = ? AND role = 'owner'`, created.ID).Row().Scan(&ownerMemberID, &hasAccount))
	assert.Equal(t, accountID, memberAccountID(t, db, ownerMemberID))
	assert.True(t, hasAccount, "创建者行必须带 user_id（/members 的 has_account）")

	var faces int
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_family_module WHERE family_id = ? AND code = 'finance' AND enabled", created.ID).
		Scan(&faces).Error)
	assert.Equal(t, 1, faces, "PRD 17.8：创建即挂载所选面，不存在 0 面家庭")

	// ⑤ A family-scoped read with the NEW token: the session family is the created one.
	w = doJSON(t, r, http.MethodGet, "/api/homeos/families", created.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var mine ListFamiliesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mine))
	require.Len(t, mine.Families, 1)
	assert.Equal(t, created.ID, mine.Families[0].ID)
	assert.Equal(t, "owner", mine.Families[0].Role)

	w = doJSON(t, r, http.MethodGet, "/api/homeos/auth/me", created.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var me MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &me))
	assert.Equal(t, created.ID, me.FamilyID)
	assert.Equal(t, ownerMemberID, me.MemberID)

	// The old onboarding refresh is still a live row until it is rotated or revoked; rotating it now
	// must UPGRADE to a family session instead of keeping the caller bootstrap-scoped.
	w = doJSON(t, r, http.MethodPost, "/api/homeos/auth/refresh", "", gin.H{"refresh_token": onboarding.RefreshToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var upgraded RefreshResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &upgraded))
	assert.Empty(t, upgraded.Scope, "已有家庭后轮换出的必须是家庭会话")
	_, err = s.Signer.Verify(upgraded.AccessToken)
	require.NoError(t, err)
	claims, err = s.Signer.VerifyOnboarding(upgraded.AccessToken)
	assert.Error(t, err, "升级后的 access token 不得再被当作 onboarding token")
	assert.Nil(t, claims)

	// ⑥ PRD 15.5: the refused family-scoped attempts left audit rows.
	var deniedRows int
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_audit_log WHERE event = ? AND actor_user_id = ?",
		model.AuditEventCrossFamilyAttempt, accountID).Scan(&deniedRows).Error)
	assert.GreaterOrEqual(t, deniedRows, 2, "onboarding 越权访问家庭域路由必须逐次落审计")
}

// memberAccountID reads a member row's user_id, so the assertion above says "the OWNER is this account"
// rather than "some owner row exists".
func memberAccountID(t *testing.T, db *gorm.DB, memberID string) string {
	t.Helper()
	var uid string
	require.NoError(t, db.Raw("SELECT user_id FROM homeos_members WHERE id = ?", memberID).Row().Scan(&uid))
	return uid
}

// TestCreateFamilyStepOneShapeAndUnbornFace pins what POST /families accepts and refuses about 面.
//
// The `modules` half of the old "缺 modules 也必须 400" expectation was the bug this card fixes, not a
// rule: PRD 3.4.1's 接口表 defines this endpoint as 「创建家庭（名称、时区、币种、头像）；成功后必须接续
// 强制选面（PUT family/modules，17.8），不允许存在 0 面家庭」, and nav doc §4.1 row ① plus §6.11 give page
// `homeos/auth/family-create` exactly those four fields and 「提交后 redirectTo homeos/family/modules 的
// 引导态完成强制选面」 as its navigation. Requiring the face list here made step ① unpostable, i.e. it
// dead-locked the 引导 at the page whose whole job is to leave 登录后无家庭.
//
// What is still refused, and still asserted: an 未出生 face (17.8 「可选项只列服务已出生的面」) -- through
// the registry, not a hand-written list -- and a refused request leaving no family row behind.
func TestCreateFamilyStepOneShapeAndUnbornFace(t *testing.T) {
	const bootstrapPhone = "13900000098"
	db, _, _, _ := setupIdentityDB(t)
	s := newServices(t, db)
	r, _ := newIdentityRouter(t, s)

	newBootstrapPhone(t, db, bootstrapPhone)
	w := doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": bootstrapPhone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var onboarding LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &onboarding))

	// ① the 定版 step-① body: absent `modules`, and an explicitly empty list, are both accepted and
	//    both produce PRD's 半成品家庭 -- zero enabled face rows, i.e. 「无行即未启用」, which is what
	//    leaves 强制选面 as the only way out instead of a skippable step.
	for _, body := range []gin.H{
		{"name": "半成品家庭甲", "timezone": "Asia/Shanghai", "currency": "CNY"},
		{"name": "半成品家庭乙", "timezone": "Asia/Shanghai", "currency": "CNY", "modules": []string{}},
	} {
		w = doJSON(t, r, http.MethodPost, "/api/homeos/families", onboarding.AccessToken, body)
		require.Equal(t, http.StatusCreated, w.Code, "定版第①步入参（无 modules）必须被接受：%s", w.Body.String())
		var created CreateFamilyResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
		assert.Contains(t, created.Message, "强制选面", "未完成选面时响应必须说出还差哪一步")

		var faces int
		require.NoError(t, db.Raw("SELECT count(*) FROM homeos_family_module WHERE family_id = ? AND enabled",
			created.ID).Scan(&faces).Error)
		assert.Zero(t, faces, "17.8：无行即未启用，选面未发生就不许有已启用面")
	}

	// ② an 未出生 face is a client error, and a refused 建家 leaves nothing behind.
	w = doJSON(t, r, http.MethodPost, "/api/homeos/families", onboarding.AccessToken, gin.H{
		"name": "未出生面家庭", "timezone": "Asia/Shanghai", "currency": "CNY", "modules": []string{"diet"},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code, "未出生面必须 400：%s", w.Body.String())

	var families int
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_families WHERE name = '未出生面家庭'").Scan(&families).Error)
	assert.Zero(t, families, "被拒绝的建家请求不得留下半个家庭")
}

// TestOnboardingMiddlewareDeniesEveryRouteNotOnTheAllowlist is the fail-closed half: a middleware built
// without a prefix (i.e. a process that forgot to wire RoutePrefix) must deny, not admit.
func TestOnboardingMiddlewareDeniesEveryRouteNotOnTheAllowlist(t *testing.T) {
	db, _, _, _ := setupIdentityDB(t)
	s := newServices(t, db)

	mw, err := svcauth.NewMiddleware(s.Signer, s.resolveMember, s.auditDenied)
	require.NoError(t, err)
	require.Nil(t, mw.OnboardingRoutes, "NewMiddleware 不得自己猜允许清单")

	_, access, _, _, err := s.Signer.IssueOnboardingTokens("11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/homeos/families", mw.Handler(), func(c *gin.Context) { c.JSON(http.StatusTeapot, nil) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/homeos/families", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code, "未配置清单时必须拒绝，而不是放行")
}

// TestJWKSServesTheKidTheSignerUses is deliverable 2's guard: the document a peer verifies against must
// carry the kid this process actually signs with, and the key material must be the public half of the
// loaded private key -- a stale or hard-coded JWK would let svc-finance "verify" tokens it should reject.
func TestJWKSServesTheKidTheSignerUses(t *testing.T) {
	db, _, _, _ := setupIdentityDB(t)
	s := newServices(t, db)
	r, _ := newIdentityRouter(t, s)

	w := doJSON(t, r, http.MethodGet, "/api/homeos/.well-known/jwks.json", "", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc svcauth.JWKSDoc
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Keys, 1)
	assert.Equal(t, s.Signer.KeyID(), doc.Keys[0].Kid)
	assert.Equal(t, "RSA", doc.Keys[0].Kty)
	assert.Equal(t, "RS256", doc.Keys[0].Alg)
	assert.Equal(t, "sig", doc.Keys[0].Use)
	assert.NotContains(t, w.Body.String(), "\"d\"", "jwks 只能下发公钥半")

	// The issued token's header kid must be the same string, which is how a peer picks the key.
	tokens, err := s.signTokens(svcauth.Session{AccountID: "a", FamilyID: "f", Role: "owner"})
	require.NoError(t, err)
	header := jwtHeaderOf(t, tokens.access)
	assert.Equal(t, doc.Keys[0].Kid, header["kid"])
	assert.Equal(t, "RS256", header["alg"])
}

// jwtHeaderOf decodes a JWT's header segment without verifying it, to read kid/alg.
func jwtHeaderOf(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var header map[string]any
	require.NoError(t, json.Unmarshal(raw, &header))
	return header
}

// loginAs runs 验证码 -> 登录 through the router and returns the pair the real Login issued.
// An account phone that does not exist yet is created by the login path itself (PRD 3.4.1), which
// is exactly the family-less state whose accept-join this test drives.
func loginAs(t *testing.T, r *gin.Engine, db *gorm.DB, phone string) LoginResponse {
	t.Helper()
	newBootstrapPhone(t, db, phone)
	w := doJSON(t, r, http.MethodPost, "/api/homeos/auth/login", "",
		gin.H{"phone": phone, "code": svcauth.DevFixedSMSCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	return res
}

// TestInviteFlowOnboardingAcceptIsTheWholeJoinCard fixes and pins the two defects that made joining
// a family impossible end to end:
//
//  1. the invite trio was registered as /families/:family_id/invites* -- a shape neither
//     PRD 3.4.1's 接口表 nor web/src/pages/homeos/family/invite.vue uses; the PRD paths
//     (POST /members/invite, GET /family/invites, DELETE /family/invites/{id}) answered the
//     router's 404. They are now mounted, family-scoped from the SESSION (PRD 15.2).
//  2. POST /family/invite/accept was NOT in svcauth's onboarding allowlist, so exactly the
//     users the join flow exists for -- an account with no family -- got 403 insufficient_scope
//     / no_family at the middleware. Accept is now allowlisted: it stays authenticated (an
//     anonymous POST is 401 at the middleware), it reads no family data with the OLD session,
//     and the handler re-signs a REAL family session from the rows the accept transaction wrote.
func TestInviteFlowOnboardingAcceptIsTheWholeJoinCard(t *testing.T) {
	const (
		joinerPhone = "13900000077"
		expiredWho  = "13900000078"
		cappedWho   = "13900000079"
	)
	db, _, familyID, ownerMemberID := setupIdentityDB(t)
	// repo.AcceptInvitation 在同事务里发 homeos.permission.updated：借用其他测试文件
	// 已在 identity fixture 之上叠加 0002 outbox 的那个 helper。
	addOutboxTable(t, db)
	s := newServices(t, db)
	r, _ := newIdentityRouter(t, s)

	owner := loginAs(t, r, db, testPhone)
	require.Equal(t, familyID, owner.FamilyID, "owner 登录后的会话家庭就是种子家庭")

	// 列表先于创建可读：空列表是 200 + 空 items，不是 404，也不是错误态假数据。
	w := doJSON(t, r, http.MethodGet, "/api/homeos/family/invites?status=pending", owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var emptyList struct {
		Items []repo.InvitationView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &emptyList))
	assert.Empty(t, emptyList.Items)

	// ① POST /members/invite (PRD path): family comes from the session, body carries only the
	//    invitee shape. 201 + the three-state view.
	w = doJSON(t, r, http.MethodPost, "/api/homeos/members/invite", owner.AccessToken,
		gin.H{"role": "member", "expires_in_days": 7})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var invite repo.InvitationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &invite))
	assert.NotEmpty(t, invite.Code)
	assert.Equal(t, "pending", invite.Status)
	// homeos_invitations.ID / inviter_id are Postgres uuid NOT NULL: the id must PARSE as a UUID
	// and the inviter must be the owner's real member row, never "" (the pre-fix empty-member trap).
	_, err := uuid.Parse(invite.ID)
	require.NoError(t, err, "invitation id 必须是真 UUID")
	assert.Equal(t, ownerMemberID, invite.InviterID)

	// ② An onboarding token (family-less account) accepts: this exact call answered
	//    403 no_family before svcauth.OnboardingRouteAcceptInvite existed.
	joiner := loginAs(t, r, db, joinerPhone)
	require.Equal(t, svcauth.ScopeOnboarding, joiner.Scope, "joiner 登录时必须是 onboarding 会话")
	require.Empty(t, joiner.FamilyID)

	w = doJSON(t, r, http.MethodPost, "/api/homeos/family/invite/accept", joiner.AccessToken,
		gin.H{"invite_code": invite.Code})
	require.Equal(t, http.StatusOK, w.Code, "onboarding token 接受邀请必须 200，而不是 403 no_family：%s", w.Body.String())
	var joined AcceptInviteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &joined))
	assert.Equal(t, familyID, joined.FamilyID)
	assert.Equal(t, "member", joined.Role)
	assert.Equal(t, seededPVer+1, joined.PVer, "pver 必须取自接受事务 Bump 后的家庭行")
	assert.NotEmpty(t, joined.AccessToken)
	assert.NotEmpty(t, joined.RefreshToken)

	// The returned pair is a REAL family session: the shared SDK's Verify -- which refused the
	// onboarding token -- accepts this one, and it carries the joined family + granted role.
	claims, err := s.Signer.Verify(joined.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, familyID, claims.FamilyID)
	assert.Equal(t, "member", claims.Role)

	// Rows, not just the response body: code spent, member row with the joiner's own account id.
	var inviteStatus string
	require.NoError(t, db.Raw("SELECT status FROM homeos_invitations WHERE code = ?", invite.Code).
		Row().Scan(&inviteStatus))
	assert.Equal(t, "accepted", inviteStatus)
	var memberRole string
	require.NoError(t, db.Raw(`SELECT role FROM homeos_members WHERE family_id = ? AND user_id = ?`,
		familyID, joiner.User.ID).Row().Scan(&memberRole))
	assert.Equal(t, "member", memberRole)

	// ③ The spent code cannot be spent again: repo looks the code up under pending, so a replay
	//    is the leak-free 404, and an unknown code answers the same thing.
	for _, code := range []string{invite.Code, "ZZZZ9999"} {
		w = doJSON(t, r, http.MethodPost, "/api/homeos/family/invite/accept", joiner.AccessToken,
			gin.H{"invite_code": code})
		assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "invite_not_found")
	}

	// ④ An expired code is 409, and the expired invitation is what the list shows under status
	//    correction (三态里的 expired 是时间的函数).
	w = doJSON(t, r, http.MethodPost, "/api/homeos/members/invite", owner.AccessToken, gin.H{"role": "guest"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var second repo.InvitationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &second))
	require.NoError(t, db.Exec(`UPDATE homeos_invitations SET expires_at = datetime('now', '-1 day') WHERE id = ?`,
		second.ID).Error)

	expiredJoiner := loginAs(t, r, db, expiredWho)
	w = doJSON(t, r, http.MethodPost, "/api/homeos/family/invite/accept", expiredJoiner.AccessToken,
		gin.H{"invite_code": second.Code})
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "invite_expired")

	// ⑤ The 12-member cap (PRD 18.1) refuses the JOIN, not just the invite: fill the family to 12
	//    (owner + joiner + 10 无账号 rows) and the next accept is 409.
	w = doJSON(t, r, http.MethodPost, "/api/homeos/members/invite", owner.AccessToken, gin.H{"role": "member"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var third repo.InvitationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &third))
	for i := 0; i < 10; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO homeos_members (id, family_id, user_id, role, created_at, updated_at)
			 VALUES (?, ?, NULL, 'member', datetime('now'), datetime('now'))`,
			uuid.NewString(), familyID).Error)
	}
	capped := loginAs(t, r, db, cappedWho)
	w = doJSON(t, r, http.MethodPost, "/api/homeos/family/invite/accept", capped.AccessToken,
		gin.H{"invite_code": third.Code})
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "member_cap_reached")

	// ⑥ DELETE /family/invites/{id} (PRD path) revokes the still-pending third invite: expired +
	//    revoked_* through repo.RevokeInvitation; a second revoke is 409, and the revoked code can
	//    no longer be spent (404 at the pending-only lookup).
	w = doJSON(t, r, http.MethodDelete, "/api/homeos/family/invites/"+third.ID, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var revokedRow struct {
		Status    string `json:"status"`
		RevokedAt string `json:"revoked_at"`
	}
	require.NoError(t, db.Raw("SELECT status, COALESCE(revoked_at, '') AS revoked_at FROM homeos_invitations WHERE id = ?",
		third.ID).Row().Scan(&revokedRow.Status, &revokedRow.RevokedAt))
	assert.Equal(t, "expired", revokedRow.Status)
	assert.NotEmpty(t, revokedRow.RevokedAt, "手工撤销必须落 revoked_at，与超时过期可区分")

	w = doJSON(t, r, http.MethodPost, "/api/homeos/family/invite/accept", capped.AccessToken,
		gin.H{"invite_code": third.Code})
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	w = doJSON(t, r, http.MethodDelete, "/api/homeos/family/invites/"+third.ID, owner.AccessToken, nil)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "invite_not_pending")

	// ⑦ The roster read honours ?status: nothing is pending any more (the spent/expired/corrected
	//    rows are), and the full list carries all three invitations newest-first.
	w = doJSON(t, r, http.MethodGet, "/api/homeos/family/invites?status=pending", owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &emptyList))
	assert.Empty(t, emptyList.Items, w.Body.String())

	w = doJSON(t, r, http.MethodGet, "/api/homeos/family/invites", owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var fullList struct {
		Items []repo.InvitationView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &fullList))
	require.Len(t, fullList.Items, 3)
	for _, item := range fullList.Items {
		assert.NotEqual(t, "pending", item.Status, "过期/已用/已撤销的行都不得再以 pending 出现在列表里")
	}

	// ⑧ The trio is family-scoped: a family-less token gets 403 on list/create/revoke -- only
	//    ACCEPT is allowlisted -- and the invented old paths answer the router's 404, not a handler.
	brandNew := loginAs(t, r, db, "13900000080")
	require.Equal(t, svcauth.ScopeOnboarding, brandNew.Scope)
	for _, denied := range []struct{ method, path string }{
		{http.MethodPost, "/api/homeos/members/invite"},
		{http.MethodGet, "/api/homeos/family/invites"},
		{http.MethodDelete, "/api/homeos/family/invites/some-id"},
	} {
		w = doJSON(t, r, denied.method, denied.path, brandNew.AccessToken, gin.H{"role": "member"})
		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s 不得对 onboarding 会话开放", denied.method, denied.path)
		assert.Contains(t, w.Body.String(), "insufficient_scope")
	}

	w = doJSON(t, r, http.MethodPost, "/api/homeos/families/"+familyID+"/invites", owner.AccessToken,
		gin.H{"role": "member"})
	assert.Equal(t, http.StatusNotFound, w.Code, "旧自造路径必须已删除，而不是别名保留：%s", w.Body.String())
}
