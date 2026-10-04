// scope.go —— svc-finance 的家庭边界（PRD 15.2「判定以 family_id 为界」、15.5「越权尝试落审计」、
// 18.2 验收第 8 条「100 次跨家庭越权探测 → 拒绝率 100%，且落审计日志」）。
//
// # 为什么需要这个文件
//
// 中间件（packages/auth/auth.go:577-587）只做了一件半的事：它比对**客户端声明**的
// family_id 与 token 的 fid，但声明值来自 query 与「Content-Type 以 application/json 开头」的
// body（claimedFamilyIDs 在 auth.go:632-635 提前 return）。Gin 的 ShouldBindJSON **不看
// Content-Type**，所以 `Content-Type: text/plain` + JSON body 带着外家庭的 family_id 就能穿过
// 中间件、被 handler 绑定并写进对方家庭的账本（实测 POST /api/finance/transactions → 201）。
// multipart 路由（POST /voice-entry，`form:"family_id"`）同理。
//
// 更根本的是：中间件管不住**根本没带 family_id** 的请求。15 条按 id 操作的路由
// （GET/PUT/DELETE /transactions/:id、/bills/:id/pay、/loans/:id/payoff …）压根没有 family 参数，
// repo 又是 `WHERE id = ?`，于是 owner token + 别人的资源 id = 200 + 对方整行数据。
//
// # 本服务采用的模型（照抄 svc-homeos，不发明第三种）
//
// sess.FamilyID 是**唯一**的家庭作用域来源：
//   - handler 一律把 session 的 family 传给 repo（见 homeos 的
//     handler/family_modules.go:106、notifications.go:136、home_summary.go:195+）；
//   - repo 对已存在资源的每一次读写都是 `WHERE id = ? AND family_id = ?`，0 行 → ErrNotFound → 404
//     （404 也是不泄露的形状：调用方无法从状态码区分「不存在」与「属于别人」）；
//   - 客户端仍可带 family_id（契约兼容），但它只被**校验**、绝不被**使用**：
//     与 token 不一致即 403 + 走 OnDenied 审计（PRD 15.5 的越权尝试）。
//
// 缺 session 一律拒绝（fail closed），不是「那就不过滤可见性」——
// 原 ListTransactions 在拿不到 session 时直接跳过 L3 私有过滤，就是 fail-open。

package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
)

// DenyFunc 是中间件 OnDenied 审计口的别名（packages/auth.Middleware.OnDenied 的签名）。
//
// handler 拒绝一次越权时必须经它记账，而不是只回一个 403：PRD 15.5 要的是「越权尝试**全部**
// 落审计」。finance 侧不重复落库（homeos 的 homeos_audit_log 是唯一落点，见 handler/auth.go
// 的 auditDenied 注释），但必须把事件交给同一个 sink，好让计数/日志/未来的落库只有一条路。
type DenyFunc = func(c *gin.Context, sess *svcauth.Session, event, reason string)

// auditEventCrossFamily 与中间件、homeos 用的是同一个事件名，避免验收脚本按事件统计时漏掉一半。
const auditEventCrossFamily = "cross_family_attempt"

// scopeFamily 返回本请求唯一的家庭作用域：session 的 family_id。
//
// 三个分支，全部 fail closed：
//  1. 没有 session（中间件没跑 / onboarding token 无 fid / 值为空）→ 401，并且**不执行任何查询**。
//     401 而不是 403：这里没有「已认证但权限不足」可说，认证本身就没给出一家人。
//  2. 客户端声明的 family_id（query / form / body 任意形态，由 handler 把它绑定到的值传进来）
//     与 token 不一致 → 403 + 审计。这是越权探测，不是格式错误，所以不是 400。
//  3. 一致（或根本没声明）→ 放行，返回 sess 与 sess.FamilyID。
//
// claimed 里允许出现空串：客户端没带这个字段时绑定结果是 ""，空值不算声明
// （?family_id= 与不带等价），否则每个省略该字段的合法请求都会被 403。
//
// ok == false 时响应已经写好，调用方必须立即 return。
func scopeFamily(c *gin.Context, onDenied DenyFunc, claimed ...string) (*svcauth.Session, string, bool) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil || sess == nil || sess.FamilyID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"code":    "no_session_family",
			"message": "登录状态缺失，无法确定家庭范围",
		})
		return nil, "", false
	}

	for _, f := range claimed {
		if f == "" {
			continue
		}
		if f != sess.FamilyID {
			deny(c, onDenied, sess, auditEventCrossFamily,
				"请求声明的 family_id 与 token 家庭不一致")
			return sess, "", false
		}
	}

	return sess, sess.FamilyID, true
}

// deny 写一次「已认证但被拒」的 403，并把事件交给审计口。
//
// 403 而非 401：调用方拿的是**有效** token，只是试图伸手到别人的家庭 —— 把两者混成一个码，
// 客户端就会为一次越权探测去重新登录（并可能反复重试），而正确反应是停下并报警。
// onDenied 为 nil 时（未接线的测试/内部调用）仍然拒答，只是没有审计记录可写。
func deny(c *gin.Context, onDenied DenyFunc, sess *svcauth.Session, event, reason string) {
	if onDenied != nil {
		onDenied(c, sess, event, reason)
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error":   "forbidden",
		"code":    "cross_family_denied",
		"message": "无权访问其他家庭的数据",
	})
}

// rejectScopedError 是「按路径 id 操作已存在资源」这条路的统一出口。
//
// repo 已经带上 family_id 条件（WHERE id = ? AND family_id = ?），所以它返回 ErrNotFound 有
// 两种现实：这个 id 真的不存在，或者它属于别的家庭。响应**必须**是同一个形状（404，
// 不能一个 404 一个 403 —— 那等于把「这条 id 是别人家的」免费告诉探测者），
// 但审计只该记下后一种可能，所以这里对 ErrNotFound 一律记一条 cross_family_attempt：
// 一次探测最有价值的证据就是「谁在什么时候拿哪个 id 试过」。
// 非 ErrNotFound 的错误（乐观锁 409、真正的 500）交回 respondRepoError 分类，不记审计。
func rejectScopedError(c *gin.Context, onDenied DenyFunc, op, resource string, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		sess, _ := svcauth.SessionFrom(c)
		if onDenied != nil {
			onDenied(c, sess, auditEventCrossFamily,
				op+": 资源在当前家庭内不存在（跨家庭探测或 id 过期）")
		}
		c.JSON(http.StatusNotFound, gin.H{"error": op + ": " + err.Error()})
		return
	}
	respondRepoError(c, op, err)
}

// canViewTransaction 是 L3 可见性（PRD 15.4「private = 仅作者与 owner 可见」）在**单条读**上的
// 落点：列表路径一直有这道过滤，但 GET /transactions/:id 完全没有，于是按 id 直读能把别人
// （甚至本家庭其他成员）的 private 流水整行取走。
//
// 语义与列表侧逐字相同（visibility/created_by 的含义都没改，只是同一个判定换了调用点）：
// 非 private 直接可见；private 只有作者本人或 owner 可见；created_by 为 NULL（未知作者）时
// isAuthor 必为 false → 只剩 owner，方向是 fail closed。
func canViewTransaction(tx *model.FinanceTransaction, sess *svcauth.Session) bool {
	if tx == nil || sess == nil {
		return false
	}
	if tx.Visibility != "private" {
		return true
	}
	isAuthor := tx.CreatedBy != nil && *tx.CreatedBy == sess.MemberID
	return isAuthor || sess.Role == authz.RoleOwner
}

// filterVisible 对列表套用 canViewTransaction，抽掉「列表里有别人 private 行」这一类。
func filterVisible(txs []model.FinanceTransaction, sess *svcauth.Session) []model.FinanceTransaction {
	filtered := make([]model.FinanceTransaction, 0, len(txs))
	for _, tx := range txs {
		if canViewTransaction(&tx, sess) {
			filtered = append(filtered, tx)
		}
	}
	return filtered
}
