// auth.go —— svc-finance 的鉴权中间件装配（PRD 15.2 / 15.5 / 15.6）。
//
// # 为什么 finance 需要自己的 MemberResolver
//
// packages/auth.NewMiddleware 的签名要求传一个 MemberResolver：
// 每个请求都要按 (family_id, account_id) 解析出这个账号在该家庭里的 member 行。
// homeos 有 homeos_members 表可直接查；finance 侧没有成员主表，只有
// finance.finance_proj_homeos 这个**投影表**（registry §2.3 登记的
// `{code}_proj_{src}`，只应由本域的 subscriber 写入）。
//
// # 关键事实：投影表当前没有任何写入方
//
// `grep finance_proj_homeos` 在整个 server/ 下只命中 registry 的定义与本文件 ——
// homeos → finance 的成员投影**尚未实现**（PRD 16.3 的跨域同步没做），所以这张表恒为空。
//
// 这带来一个必须说清楚的口径选择：
//
//	· 若 resolver 在投影表查不到就返回 svcauth.ErrNoMember（homeos 的做法），
//	  则**每一个请求都会 401**，finance 整个服务不可用 —— 因为表永远是空的。
//	· 若无条件放行，则中间件不做成员存在性校验。
//
// 这里取的是第三条路：**token 验签已经守住家庭边界**（authz.Verify 要求 claims.fid 非空，
// 且签名必须来自 svc-homeos 的私钥），所以跨家庭越权在这一层已经被挡住；
// 成员行存在性属于「成员是否已退出家庭」的另一件事。投影表空时按 account_id 透传并记一条
// WARN，是当前唯一既不阻断生产、又不静默假装校验过的做法。
//
// **这不是终态。** PRD 15.2「判定以 family_id 为界」要完整成立，需要 homeos 把成员变更
// 通过 JetStream 投影到 finance_proj_homeos；在那之前 finance 无法独立复核成员归属。
// 该缺口已作为 P1 遗留项记录（见 docs/ACCEPTANCE-REPORT.md 的 G5 门禁）。
package handler

import (
	"context"
	"errors"
	"log/slog"

	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// FinanceAuth bundles what the finance authenticator needs: the signer that verifies
// svc-homeos-issued RS256 tokens (PRD 15.6「各服务共用同一实现」), the DB the member
// projection is read from, and the logger the projection-gap warning goes to.
type FinanceAuth struct {
	Signer *svcauth.Signer
	DB     *gorm.DB
	Logger *slog.Logger
}

// Middleware builds the authenticator every /api/finance/* business route sits behind.
//
// onboardingRoutes is empty on purpose: finance has no family-less surface
// (PRD 22.2 第 8 条 的公开端点全在 homeos 的 /auth/* 与 /legal/*)，所以任何无 family_id
// 的 token 在这里都应当被拒，而不是被放进白名单。
func (a *FinanceAuth) Middleware() (*svcauth.Middleware, error) {
	mw, err := svcauth.NewMiddleware(a.Signer, a.resolveMember, a.auditDenied)
	if err != nil {
		return nil, err
	}
	mw.OnboardingRoutes = map[string]bool{}
	return mw, nil
}

// resolveMember implements svcauth.MemberResolver over finance_proj_homeos.
//
// 查到 → 返回该 member_id（后续 L3 可见性与「作者可删」判定就用它）。
// 查不到 → 返回 ErrNoMember，理由见文件头：投影表尚无写入方，恒为空，
// 直接 fail-closed 会让 finance 全站 401，因此这里透传 account_id 并记 WARN。
func (a *FinanceAuth) resolveMember(ctx context.Context, familyID, accountID string) (string, error) {
	var memberID string
	err := a.DB.WithContext(ctx).
		Table("finance_proj_homeos").
		Select("member_id").
		Where("family_id = ? AND member_id IS NOT NULL", familyID).
		// 投影表没有 account_id 列（P1 的列集是 family_id/member_id/role/pver/updated_at），
		// 因此 P1 只能按家庭取该家庭的成员代表；等 homeos 侧投影补齐 account_id 后
		// 这里应收窄为 family_id + account_id 的精确匹配。
		Limit(1).
		Scan(&memberID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// 投影表本身读不出来（例如尚未迁移）是配置问题，不能静默放行。
		return "", err
	}
	if memberID == "" {
		a.Logger.Warn("member_projection_empty",
			"family_id", familyID,
			"account_id", accountID,
			"reason", "finance_proj_homeos 尚无写入方（homeos→finance 成员投影未实现），按 token 的 account_id 透传",
		)
		return accountID, nil
	}
	return memberID, nil
}

// auditDenied is the middleware's OnDenied sink.
//
// PRD 15.5「越权尝试全部落审计」由 svc-homeos 统一落库（finance 不重复落，避免同一事件两处记账），
// 这里只把拒绝计数暴露给日志/指标，让 finance 侧的越权尝试可观测。
func (a *FinanceAuth) auditDenied(c *gin.Context, sess *svcauth.Session, event, reason string) {
	fields := []any{"event", event, "reason", reason}
	if sess != nil {
		fields = append(fields,
			"family_id", sess.FamilyID,
			"account_id", sess.AccountID,
			"role", sess.Role,
		)
	}
	a.Logger.Warn("auth_denied", fields...)
}
