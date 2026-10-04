// faces_test.go asserts 定版 ⑯'s 五处同源 in the only form that can actually be checked from here:
// one composition, two renderings, identical answer.
//
// PRD 17.8 says 首页矩阵、「＋」目标、搜索分组、到期中心注册项、动态流筛选 all read 「同一份服务端合成」,
// and the two endpoints at the top of that list are `GET /family/modules` (the whole catalog with its
// four flags, because the 开通页 must show 未启用 and 即将上线 -- nav §4.6) and `home/summary`'s
// `faces[]` (only the mounted-and-visible subset, because 17.2 says 「服务端只返回这个集合，客户端不补齐、
// 不占位」). Those two views disagreeing about one household is the defect the acceptance criterion names,
// and it is not visible in either response on its own. It IS visible in an equality test: filter the
// HTTP response the 开通页 gets on `enabled ∧ visible` and compare it, 逐项, against the very slice the
// 首页 would render -- `mountedFaces` over the same composition, run with the same session claims. If
// either side ever grows its own idea of which faces a member sees, this test is what says so.
//
// The `home/summary` HANDLER is not this card's deliverable and is still unmounted, so this file is
// currently the ONLY thing that exercises composeFaceEntries and mountedFaces; `mounted()`'s third term
// (服务已出生) has no HTTP caller at all until that endpoint lands. The role loop is run over real
// sessions -- a token issued by this process's own signer, verified by the shared SDK, resolved through
// svcauth.Middleware into homeos_members -- because `visible` is a 15.3 role reading, so a hand-built
// claims struct would be testing the test rather than the gate.
//
// Everything the fixture seeds is derived from `server/packages/registry`: the face it mounts, the face
// it refuses as 未出生, the code list the catalog is compared against. A face added to registry joins
// this assertion without a line edited here, which is the point (PRD 17.8 「新增一面 = 追加一条 registry
// 条目」, and 门禁 4 fails a hardcoded 面清单).
package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// ==================== fixtures ====================

// catalogCodes is the face list the contract says GET returns, derived from registry in registration
// order with 底座 excluded (PRD 17.1 「首页自身不进矩阵」). The test spells no face code, so a rename or a
// new face in registry moves the expectation and the handler together.
func catalogCodes() []string {
	out := make([]string, 0, len(registry.Domains()))
	for _, d := range registry.Domains() {
		if d.Code == registry.HomeosCode {
			continue
		}
		out = append(out, d.Code)
	}
	return out
}

// firstBornFace / firstUnbornFace answer the two catalog positions the write path cares about the same
// way -- from the registry row, never from a literal.
func firstBornFace(t *testing.T) registry.Domain {
	t.Helper()
	for _, d := range registry.Domains() {
		if d.Code != registry.HomeosCode && d.Born() {
			return d
		}
	}
	t.Fatalf("registry 在当前阶段 %s 没有任何已出生的面，测试无法验证「挂载」这一半", registry.CurrentPhase)
	return registry.Domain{}
}

func firstUnbornFace(t *testing.T) registry.Domain {
	t.Helper()
	for _, d := range registry.Domains() {
		if d.Code != registry.HomeosCode && !d.Born() {
			return d
		}
	}
	t.Fatalf("registry 在当前阶段 %s 没有未出生的面，无法验证「即将上线只读」这一半", registry.CurrentPhase)
	return registry.Domain{}
}

// addOutboxTable puts the one table PUT needs on top of auth_test.go's identity fixture: 定版 ㉖ wants
// the face's own change event plus the pver event in homeos.family.module.updated's transaction, and
// repo.AppendOutboxEnvelope writes 0002's column set (subject / envelope / status / attempts /
// created_at), not packages/bus's wider OutboxMessage struct.
func addOutboxTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`CREATE TABLE homeos_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT, family_id TEXT, subject TEXT NOT NULL,
			envelope TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending','sent')),
			attempts INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL)`).Error)
}

// addAccountWithRole seeds a second account occupying the session family under one role, which is how
// the 15.3 rows get exercised: homeos_members.role is what svcauth.Middleware resolves and what authz
// reads, so the role under test is a stored value rather than a claim the request invented.
func addAccountWithRole(t *testing.T, db *gorm.DB, familyID, phone, name, role string) string {
	t.Helper()
	accountID := uuid.NewString()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_users (id, phone, name, created_at, updated_at)
		 VALUES (?, ?, ?, datetime('now'), datetime('now'))`,
		accountID, phone, name).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_members (id, family_id, user_id, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`,
		uuid.NewString(), familyID, accountID, role).Error)
	return accountID
}

// mountFace seeds one enabled homeos_family_module row. The read-side test uses it because going
// through PUT would make the same-source assertion depend on the write path it does not belong to.
func mountFace(t *testing.T, db *gorm.DB, familyID, code string, version int64) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_family_module
		 (id, family_id, code, enabled, enabled_at, enabled_by, version, created_at, updated_at)
		 VALUES (lower(hex(randomblob(16))), ?, ?, 1, datetime('now'), NULL, ?, datetime('now'), datetime('now'))`,
		familyID, code, version).Error)
}

// newModulesRouter mounts the 面配置 pair exactly the way cmd/svc-homeos does -- same prefix, same
// middleware, same (c, services, mw) shape -- so the test drives the route list the process runs and
// not a private variant of it.
func newModulesRouter(t *testing.T, s *Services) *gin.Engine {
	t.Helper()

	mw, err := s.Middleware()
	require.NoError(t, err)
	d, ok := registry.ByCode(registry.HomeosCode)
	require.True(t, ok)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group(d.RoutePrefix)
	protected := group.Group("", mw.Handler())
	protected.GET("/family/modules", func(c *gin.Context) { GetFamilyModules(c, s, mw) })
	protected.PUT("/family/modules", func(c *gin.Context) { UpdateFamilyModule(c, s, mw) })
	return r
}

// sessionToken signs a real family session for one (account, family, role) triple through this
// process's own signer, which is what makes authz.Verify, the middleware's member resolution and
// authz.Can all read the same values the handler reads.
func sessionToken(t *testing.T, s *Services, accountID, familyID, role string) string {
	t.Helper()
	access, _, _, _, err := s.Signer.IssueTokens(svcauth.Session{
		AccountID: accountID,
		FamilyID:  familyID,
		Role:      role,
		PVersion:  seededPVer,
	})
	require.NoError(t, err)
	return access
}

const modulesPath = "/api/homeos/family/modules"

// ==================== 五处同源 ====================

// TestFamilyModulesFilterEqualsMountedFacesForEveryRole is the acceptance criterion: the 开通页's list
// filtered on enabled ∧ visible must be 逐项一致 with the 首页's navigation set, for the owner and for
// non-admin roles, because 「同一份服务端合成，客户端只拿自己可见的那一份」 (PRD 17.8 定版 ⑯).
func TestFamilyModulesFilterEqualsMountedFacesForEveryRole(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	s := newServices(t, db)

	born := firstBornFace(t)
	unborn := firstUnbornFace(t)
	mountFace(t, db, familyID, born.Code, 1)

	// One account per non-admin 15.3 row, all inside the same family, so the three roles are readings
	// of ONE household's rows rather than of three fixtures.
	memberAccount := addAccountWithRole(t, db, familyID, "13800000002", "成员", "member")
	wardAccount := addAccountWithRole(t, db, familyID, "13800000003", "被记录成员", "ward")
	guestAccount := addAccountWithRole(t, db, familyID, "13800000004", "访客", "guest")

	r := newModulesRouter(t, s)

	for _, tc := range []struct {
		role      string
		accountID string
		// wantMounted: the 15.3 row makes the born face visible to this role or not. Stated per role so
		// the equality below cannot pass by both sides being silently empty everywhere.
		wantMounted bool
	}{
		// owner: A on finance -> visible -> mounted.
		{role: "owner", accountID: ownerAccount, wantMounted: true},
		// member: M on finance (read is allowed, and a family-level read has no author to compare) -> mounted.
		{role: "member", accountID: memberAccount, wantMounted: true},
		// ward: 「-」 on finance -> not visible -> the 首页 draws no cell, and the 开通页 shows it disabled.
		{role: "ward", accountID: wardAccount, wantMounted: false},
		// guest: 「-」 on finance -> same as ward, and proof the guest row is not quietly promoted.
		{role: "guest", accountID: guestAccount, wantMounted: false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			token := sessionToken(t, s, tc.accountID, familyID, tc.role)

			w := doJSON(t, r, http.MethodGet, modulesPath, token, nil)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			var got FamilyModulesResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

			// ① The catalog is the registry, in registration order, minus the 底座 -- no second list.
			wantCodes := catalogCodes()
			gotCodes := make([]string, 0, len(got.Modules))
			for _, m := range got.Modules {
				gotCodes = append(gotCodes, m.Code)
			}
			assert.Equal(t, wantCodes, gotCodes, "面目录必须来自 registry.Domains() 的登记序（PRD 17.1/17.8）")
			assert.NotContains(t, gotCodes, registry.HomeosCode, "底座不是面（PRD 17.1「首页自身不进矩阵」）")

			// ② Labels come from the registry row, never from a client-side or handler-side copy.
			for _, m := range got.Modules {
				d, ok := registry.ByCode(m.Code)
				require.True(t, ok, "响应里的 code 必须是 registry 登记的域：%s", m.Code)
				assert.Equal(t, d.Title, m.Name, "面名必须取自 registry Title")
				assert.Equal(t, d.Icon, m.Icon, "面图标必须取自 registry Icon（17.6 禁 emoji）")
				assert.Equal(t, d.Born(), m.Born, "born 必须取自 registry 的出生阶段比较")
				// The cell state is faces.go's answer, and the response must carry exactly it -- a
				// second idea of 「available」 anywhere else is how the two views start to differ.
				assert.Equal(t, faceAvailability(d), m.Availability, "availability 必须由 faceAvailability(registry 行) 决定")
				if !d.Born() {
					// PRD 17.8 / nav §3.3: 未出生的格子没有可连的服务，只能是 unavailable。
					assert.Equal(t, "unavailable", m.Availability, "未出生的面必须是 unavailable（开通页的「即将上线」）")
					assert.False(t, m.Enabled, "未出生的面不可能处于已启用态（两个写入口都拒绝它）")
				}
			}

			// ③ THE criterion: this response filtered on enabled ∧ visible == the 首页's faces[] source.
			// The filter reads the JSON the 开通页 renders; mountedFaces is the slice home/summary would
			// build from the same composition. Two code paths, one household, no third opinion.
			var filtered []faceCellView
			for _, m := range got.Modules {
				if m.Enabled && m.Visible {
					filtered = append(filtered, newFaceCellView(m))
				}
			}
			claims, err := s.Signer.Verify(token)
			require.NoError(t, err)
			entries, err := s.composeFaceEntries(t.Context(), claims, familyID)
			require.NoError(t, err)
			var mounted []faceCellView
			for _, e := range mountedFaces(entries) {
				mounted = append(mounted, faceCellView{Code: e.Code, Name: e.Name, Icon: e.Icon, Availability: e.Availability})
			}

			assert.Equal(t, mounted, filtered,
				"GET /family/modules 按 enabled∧visible 过滤后必须与 home/summary 的 faces[] 源（mountedFaces）逐项一致，role=%s", tc.role)
			if tc.wantMounted {
				assert.NotEmpty(t, mounted, "role=%s 下这个断言不能空对空地成立（已启用且可见的面必须真的挂起来）", tc.role)
				assert.Contains(t, []string{born.Code}, mounted[0].Code)
			} else {
				assert.Empty(t, mounted, "role=%s 在 15.3 上对该面是「-」，首页不该有任何格子", tc.role)
				assert.Empty(t, filtered)
			}

			// ④ mounted()'s own three terms, asserted on the answer rather than on the predicate: a
			// face that is enabled+visible but not born would make ③ pass only by luck.
			for _, e := range entries {
				if e.Enabled && e.Visible {
					assert.True(t, e.Born, "面 %s 已启用且可见却未出生：首页格子与开通页将永久分歧", e.Code)
				}
				assert.Equal(t, e.Enabled && e.Born && e.Visible, e.mounted(), "面 %s 的挂载判据", e.Code)
			}

			// ⑤ The 未出生 face is a read-only 「即将上线」 row for this role as well: visible per 15.3
			// is fine (a member may be allowed to READ the purchase row), 挂载 is not.
			for _, m := range got.Modules {
				if m.Code == unborn.Code {
					assert.False(t, m.Born)
					assert.False(t, m.Enabled)
					assert.Equal(t, "unavailable", m.Availability)
				}
			}
		})
	}
}

// faceCellView is the four cells both renderings show of one face -- the identity 首页 cares about,
// since faces[] adds only a 状态句 on top of them.
type faceCellView struct {
	Code         string
	Name         string
	Icon         string
	Availability string
}

func newFaceCellView(m FamilyModuleItem) faceCellView {
	return faceCellView{Code: m.Code, Name: m.Name, Icon: m.Icon, Availability: m.Availability}
}

// ==================== 写路径：乐观锁 / 仅管理员 / 最后一面 / 未出生 ====================

// TestUpdateFamilyModuleLockAdminGateAndLastFaceFloor walks the 引导态 (PRD 17.8 强制选面) through the
// refusals the contract and PRD 15.3 name, and asserts the database state after each one: a 403 or a
// 409 must not have written, and the 200 must have written the row, the pver, the audit row and both
// event envelopes in the same transaction.
func TestUpdateFamilyModuleLockAdminGateAndLastFaceFloor(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addOutboxTable(t, db)
	s := newServices(t, db)

	born := firstBornFace(t)
	unborn := firstUnbornFace(t)
	memberAccount := addAccountWithRole(t, db, familyID, "13800000005", "成员", "member")

	r := newModulesRouter(t, s)
	ownerToken := sessionToken(t, s, ownerAccount, familyID, "owner")
	memberToken := sessionToken(t, s, memberAccount, familyID, "member")

	// setupIdentityDB's family is PRD 17.8's 半成品家庭: no homeos_family_module row at all, so the
	// family-level lock epoch is 0 and version 0 is the token that means 「我预期还没有行」.
	w := doJSON(t, r, http.MethodGet, modulesPath, ownerToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var half FamilyModulesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &half))
	require.Equal(t, int64(0), half.Version, "无行即未启用（17.8），空家庭的 epoch 必须是 0")
	for _, m := range half.Modules {
		require.False(t, m.Enabled)
	}

	// ① 15.3「仅管理员可调用」: a non-admin holding the CORRECT version still gets 403. The version is
	// deliberately the live one here -- if the gate were only the lock, this request would have written.
	w = doJSON(t, r, http.MethodPut, modulesPath, memberToken, gin.H{"code": born.Code, "enabled": true, "version": 0})
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "forbidden")
	assert.Equal(t, int64(0), countModuleRows(t, db, familyID), "非管理员的 403 必须什么都没写（PRD 15.3 面配置 A 行只有 owner）")

	// ② owner mounts the face at epoch 0 -- 强制选面 step ② (nav §6.11), the one write this endpoint exists for.
	auditBefore := countAuditRows(t, db, familyID) // ①'s 15.5 denied row is already in here
	w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": born.Code, "enabled": true, "version": 0})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var putResp UpdateFamilyModuleResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.Equal(t, int64(1), putResp.Version, "新 epoch 必须是事务内重读的行 version 之和")
	assert.Equal(t, seededPVer+1, putResp.PVer, "pver 必须与配置变更同事务前进（PRD 15.6）")

	assert.Equal(t, int64(1), countModuleRows(t, db, familyID))
	assert.Equal(t, int64(1), countEnabledRows(t, db, familyID))
	assert.Equal(t, int64(1), countOutboxRows(t, db, "homeos.family.module.updated"), "定版 ㉖：面自己的变更事件")
	assert.Equal(t, int64(1), countOutboxRows(t, db, "homeos.permission.updated"), "同事务的权限版本事件")
	auditAfterMount := countAuditRows(t, db, familyID)
	assert.Equal(t, auditBefore+1, auditAfterMount, "变更即落审计（15.5），且只落一行")

	// The epoch the NEXT GET answers is the one PUT returned -- otherwise the client's lock token is
	// one write behind and its next request is a spurious 409.
	w = doJSON(t, r, http.MethodGet, modulesPath, ownerToken, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var now FamilyModulesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &now))
	require.Equal(t, putResp.Version, now.Version, "PUT 回的新 epoch 必须等于随后 GET 的 epoch")

	// ③ 409 on a stale version (contract L361, PRD 17.8 乐观锁). version 0 is the epoch from before
	// the mount above, i.e. exactly what the 开通页 would resend after another member's write.
	w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": born.Code, "enabled": false, "version": 0})
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "version_conflict", errorCode(t, w.Body.Bytes()))
	assert.Equal(t, int64(1), countEnabledRows(t, db, familyID), "409 之后配置行必须还是提交前的样子")
	assert.Equal(t, int64(1), rowVersion(t, db, familyID, born.Code))
	assert.Equal(t, auditAfterMount, countAuditRows(t, db, familyID), "被锁挡下的请求不得留下审计行")
	assert.Equal(t, int64(2), countOutboxRowsAll(t, db), "409 的事务必须整体回滚")

	// ④ PRD 17.8 第 4 条「不允许出现 0 面家庭」: the FLOOR, with a perfectly current version. The lock
	// passes and the repo's in-transaction count is what refuses.
	w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": born.Code, "enabled": false, "version": now.Version})
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "cannot_disable_last_module", errorCode(t, w.Body.Bytes()))
	assert.Equal(t, int64(1), countEnabledRows(t, db, familyID), "停用最后一个面被拒后，面还在（17.8「停用不删数据」）")
	assert.True(t, rowEnabled(t, db, familyID, born.Code))
	assert.Equal(t, int64(1), rowVersion(t, db, familyID, born.Code), "被拒的写不得烧掉 version")
	assert.Equal(t, seededPVer+1, familyPVer(t, db, familyID), "被拒的写不得推进 pver")
	assert.Equal(t, auditAfterMount, countAuditRows(t, db, familyID), "被拒的写不得留下审计行")
	assert.Equal(t, int64(2), countOutboxRowsAll(t, db), "被拒的写不得留下事件")

	// ⑤ 未出生的面是只读格（17.8「可选项只列服务已出生的面」）: both directions refused, with the live
	// epoch, so the refusal is about the face and not about the lock.
	for _, enabled := range []bool{true, false} {
		w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": unborn.Code, "enabled": enabled, "version": now.Version})
		require.Equal(t, http.StatusBadRequest, w.Code, "未出生的面不可写（enabled=%v）：%s", enabled, w.Body.String())
		assert.Equal(t, "face_not_born", errorCode(t, w.Body.Bytes()))
	}
	assert.Equal(t, int64(0), countModuleRowsFor(t, db, familyID, unborn.Code), "被拒的未出生面不能留下一行")

	// ⑥ A code registry does not know is not a face: refused by the catalog lookup, so there is no
	// second list of acceptable codes anywhere in this handler (PRD 16.1 命名唯一真源).
	w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": "not_a_face", "enabled": true, "version": now.Version})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "unknown_module", errorCode(t, w.Body.Bytes()))

	// ⑦ The contract's three required fields: a missing version is a 400, and version 0 is NOT one of
	// them (UpdateFamilyModuleRequest's doc comment: 0 is the 半成品家庭's mount token).
	w = doJSON(t, r, http.MethodPut, modulesPath, ownerToken, gin.H{"code": born.Code, "enabled": true})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "invalid_request", errorCode(t, w.Body.Bytes()))

	// ⑧ No session, no read: the pair sits behind mw.Handler(), so the family boundary comes from the
	// token and never from the request (PRD 15.2).
	w = doJSON(t, r, http.MethodGet, modulesPath, "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ==================== db probes used by the assertions above ====================

func countModuleRows(t *testing.T, db *gorm.DB, familyID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_family_module WHERE family_id = ?", familyID).Scan(&n).Error)
	return n
}

func countModuleRowsFor(t *testing.T, db *gorm.DB, familyID, code string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(
		"SELECT count(*) FROM homeos_family_module WHERE family_id = ? AND code = ?", familyID, code).Scan(&n).Error)
	return n
}

func countEnabledRows(t *testing.T, db *gorm.DB, familyID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(
		"SELECT count(*) FROM homeos_family_module WHERE family_id = ? AND enabled = 1", familyID).Scan(&n).Error)
	return n
}

func rowVersion(t *testing.T, db *gorm.DB, familyID, code string) int64 {
	t.Helper()
	var v int64
	require.NoError(t, db.Raw(
		"SELECT version FROM homeos_family_module WHERE family_id = ? AND code = ?", familyID, code).Scan(&v).Error)
	return v
}

func rowEnabled(t *testing.T, db *gorm.DB, familyID, code string) bool {
	t.Helper()
	var on bool
	require.NoError(t, db.Raw(
		"SELECT enabled FROM homeos_family_module WHERE family_id = ? AND code = ?", familyID, code).Scan(&on).Error)
	return on
}

func familyPVer(t *testing.T, db *gorm.DB, familyID string) int64 {
	t.Helper()
	var pver int64
	require.NoError(t, db.Raw("SELECT pver FROM homeos_families WHERE id = ?", familyID).Scan(&pver).Error)
	return pver
}

func countOutboxRows(t *testing.T, db *gorm.DB, subject string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_outbox WHERE subject = ?", subject).Scan(&n).Error)
	return n
}

func countOutboxRowsAll(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_outbox").Scan(&n).Error)
	return n
}

func countAuditRows(t *testing.T, db *gorm.DB, familyID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT count(*) FROM homeos_audit_log WHERE family_id = ?", familyID).Scan(&n).Error)
	return n
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "错误响应必须是 JSON 对象：%s", string(body))
	code, _ := m["error"].(string)
	return code
}
