// faces.go is the ONE place svc-homeos composes the face set.
//
// PRD 17.8 定版 ⑯ names four layers behind that set -- the registry 面目录, the family's
// homeos_family_module rows, 服务是否已出生 and the 15.3 `scope=module` verdict for the calling
// role -- and it says the merge is 「同一份服务端合成，客户端只拿自己可见的那一份」. Two endpoints then
// consume the same merge: `GET /family/modules` renders the whole catalog with its flags (the 开通页
// needs 未启用 and 即将上线 visible, §4.6), and `home/summary`'s `faces[]` renders only the mounted-and-
// visible subset (PRD 17.2 「服务端只返回这个集合，客户端不补齐、不占位」). Because both read
// composeFaceEntries, their two views of one household cannot disagree -- which is the acceptance
// criterion of 五处同源, asserted in faces_test.go rather than asserted here.
//
// Nothing in this file lists faces. The enumeration is registry.Domains() (tech plan §1.3 唯一真源),
// homeos is subtracted by its registry constant because the 底座 is not a 面 (PRD 17.1 「首页自身不进
// 矩阵」, registry.HomeosCode's own doc comment), and every label comes from the row's Title/Icon
// cells (navigation doc §1.4 「声明本面图标与标题（registry 域表提供）」, §3.3 「来自 registry，
// 不硬编码」). A face added to the registry appears in both endpoints without touching this file.
package handler

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// Availability is the contract's two-value enum (homeos.yaml faces[].availability and
// /family/modules items[].availability). nav §3.3 states there is NO third value: 未启用 and 未出生
// are carried by `enabled`/`born`, and ㉙ 的「依赖未就绪」 deliberately stays out of this field.
const (
	faceAvailable   = "available"
	faceUnavailable = "unavailable"
)

// FaceEntry is one catalog position with the four layers merged. It is the shared shape behind both
// endpoints: /family/modules serializes it whole, home/summary filters it through mountedFaces and
// then adds the cell's 状态句.
type FaceEntry struct {
	// Code is the registry code, the only naming authority for a face (PRD 16.1).
	Code string
	// Name and Icon are the registry row's Title/Icon cells -- never a client-side string.
	Name string
	Icon string
	// Enabled is the FAMILY reading: a homeos_family_module row with enabled = true. 「无行即未启用」
	// (PRD 17.8) makes a missing row false rather than unknown.
	Enabled bool
	// Visible is the ROLE reading: authz's scope=module read verdict for this session's role
	// (PRD 15.3 row per face). It is not a field on homeos_family_module -- the table has none.
	Visible bool
	// Born is the SERVICE reading: registry's BirthPhase <= CurrentPhase.
	Born bool
	// Availability is nav §3.3's cell state, decided here rather than by the client.
	Availability string
	// Version is the row's optimistic-lock version (0 when the family has no row for this face).
	Version int64
}

// mounted reports the entry a home screen may draw: 已启用 ∩ 服务已出生 ∩ 该角色可见 (PRD 17.8's
// 「该成员可见的面集合」 row).
func (e FaceEntry) mounted() bool { return e.Enabled && e.Born && e.Visible }

// composeFaceEntries merges the four layers for one family and one caller.
//
// familyID and claims both come from the session the auth middleware installed, never from the
// request: a caller-supplied family id on this read would be the cross-tenant bug this whole function
// exists to avoid (PRD 15.2 「判定以 family_id 为界」).
func (s *Services) composeFaceEntries(
	ctx context.Context,
	claims *authz.Claims,
	familyID string,
) ([]FaceEntry, error) {
	rows, err := repo.ListFamilyModules(ctx, s.DB, familyID)
	if err != nil {
		return nil, err
	}
	type rowState struct {
		enabled bool
		version int64
	}
	byCode := make(map[string]rowState, len(rows))
	for _, r := range rows {
		byCode[r.Code] = rowState{enabled: r.Enabled, version: r.Version}
	}

	domains := registry.Domains()
	out := make([]FaceEntry, 0, len(domains))
	for _, d := range domains {
		// 首页自身不进矩阵 (PRD 17.1): the base is the page the cells sit on, not one of them.
		if d.Code == registry.HomeosCode {
			continue
		}
		state := byCode[d.Code]
		out = append(out, FaceEntry{
			Code:         d.Code,
			Name:         d.Title,
			Icon:         d.Icon,
			Enabled:      state.enabled,
			Version:      state.version,
			Visible:      authz.Can(ctx, claims, authz.ScopeModule, d.Code, authz.ActionRead, nil),
			Born:         d.Born(),
			Availability: faceAvailability(d),
		})
	}
	return out, nil
}

// faceAvailability answers nav §3.3's two-state cell from server-side inputs only.
//
// `available` is that document's own conjunction: 已启用 + 服务已出生 + 模块权限允许. The third term is
// not tested here because a cell that fails it never reaches faces[] at all (mountedFaces drops it),
// so the only remaining inputs are the two service-side ones: an unborn face has no service to be
// reachable, and a row the registry declares born while the checkout does not build it
// (Born && !Implemented) is the server's own 「该面服务不可达」 case. registry_prd_test.go pins the two
// sets equal at every phase, so in P1 the second branch is unreachable and every mounted cell answers
// `available` -- that is the honest state of the code, not a placeholder: the two documented
// unavailable triggers left over are 「服务不可达」 measured by a synchronous probe of the other service,
// which tech plan §六 forbids on this path (首页格子不得为了渲染而去同步调 svc-finance), and 「分包
// 校验/下载失败」, which is a client-side fact the client discovers after this response is built (the
// 重试 in §3.3 is exactly the client re-running §6.11). Both are reported as contract/PRD items the
// server cannot feed in P1.
func faceAvailability(d registry.Domain) string {
	if !d.Born() || !d.Implemented {
		return faceUnavailable
	}
	return faceAvailable
}

// mountedFaces is THE navigation set (PRD 17.8: 首页矩阵、「＋」目标、搜索分组、到期中心注册项、动态流筛选
// 五处同源). Order is registry registration order and is never re-sorted (PRD 17.1, nav §3.2).
func mountedFaces(entries []FaceEntry) []FaceEntry {
	out := make([]FaceEntry, 0, len(entries))
	for _, e := range entries {
		if e.mounted() {
			out = append(out, e)
		}
	}
	return out
}

// FaceStatus is one home/summary faces[] item.
//
// `badge` is deliberately ABSENT from this struct rather than always 0. The contract types it
// (homeos.yaml faces[].badge: integer) but declares no required list, so omission is a legal answer;
// inventing a number is not. PRD 17.1 第 3 条 defines badge as 「待处理数」 and explicitly separates it
// from 「未读」, and 0004 -- the table that is 「home/summary 中财务那一格 headline 与角标的唯一数据源」 --
// states in its own DDL comment that the 角标 counting column's shape is not given by the documents and
// was therefore not created. No per-face 待处理 count exists in P1, so the field is not sent, and the
// client's `v-if="face.badge > 0"` renders no badge.
type FaceStatus struct {
	Code         string     `json:"code"`
	Name         string     `json:"name"`
	Availability string     `json:"availability"`
	Headline     string     `json:"headline"`
	AsOf         *time.Time `json:"as_of,omitempty"`
}

// faceHeadline builds one cell's 今日状态 sentence from the numbers this service actually holds, and
// reports the freshness the sentence was computed at.
//
// The data source is looked up, not listed: the cell asks whether THIS checkout has a local
// projection of that face's domain (registry-derived table name, schema-checked through
// Migrator().HasTable) and, if so, sums its monthly buckets over the request's period. In P1 exactly
// one such table exists -- homeos_proj_finance (0004) -- so exactly one cell gets a money sentence,
// and every wording below is a reading of 0004's declared columns. A face without a projection table
// gets the 「暂无可显示的数据」 sentence rather than a fabricated one, and a projection query that fails
// degrades that single cell (logged) instead of turning the 首屏 single request into a 500.
func (s *Services) faceHeadline(
	ctx context.Context,
	familyID string,
	entry FaceEntry,
	currency string,
	from time.Time,
	to time.Time,
) (string, *time.Time) {
	exists, err := repo.ProjectionExists(ctx, s.DB, entry.Code)
	if err != nil {
		s.warnFace(ctx, "面投影名解析失败", entry.Code, err)
		return headlineNoSource, nil
	}
	if !exists {
		// No projection of this domain in this checkout: nothing was computed, so nothing is claimed.
		return headlineNoSource, nil
	}

	summary, err := repo.FaceProjectionSummary(ctx, s.DB, familyID, entry.Code, from, to)
	if err != nil {
		s.warnFace(ctx, "面投影读取失败", entry.Code, err)
		return headlineNoData, nil
	}
	if !summary.HasData() {
		return headlineNoData, nil
	}

	var parts []string
	switch {
	case summary.BudgetRemainingCents == nil:
		// No bucket of the period carries a budget: 定版⑬'s 「未设该周期预算」 state. The收支 figure
		// still comes from real buckets, so the cell says that instead of a budget it does not have.
		parts = append(parts, headlineSpend(summary, currency))
	case summary.BudgetBuckets < summary.BucketCount:
		// Partial coverage. Summing the covered months and naming how many are covered is the honest
		// form; multiplying the covered months up to the full period is the 假预算剩余 定版⑬ forbids.
		remaining := formatMoney(*summary.BudgetRemainingCents, currency)
		parts = append(parts,
			"已设预算 "+strconv.FormatInt(summary.BudgetBuckets, 10)+" 个月，剩余 "+remaining)
	default:
		remaining := *summary.BudgetRemainingCents
		if remaining < 0 {
			parts = append(parts, "预算超支 "+formatMoney(-remaining, currency))
		} else {
			parts = append(parts, "预算剩余 "+formatMoney(remaining, currency))
		}
	}
	if summary.OverBudgetMonths > 0 {
		parts = append(parts, strconv.FormatInt(summary.OverBudgetMonths, 10)+" 个月已超支")
	}
	return strings.Join(parts, " · "), summary.AsOf
}

// headlineNoSource and headlineNoData are the two sentences that keep the cell honest: the first says
// this checkout computes nothing for that face, the second says it computed and this family has no
// bucket inside the period. Neither is a number.
const (
	headlineNoSource = "本周期暂无可显示的数据"
	headlineNoData   = "本周期暂无记账"
)

// headlineSpend states the period's expense when no budget figure exists to state.
func headlineSpend(summary repo.FaceProjection, currency string) string {
	if summary.ExpenseCents == 0 && summary.IncomeCents == 0 {
		return headlineNoData
	}
	return "本周期支出 " + formatMoney(summary.ExpenseCents, currency)
}

// formatMoney renders 分 as the family's currency amount. Amounts are stored as integer cents
// (PRD 14.7, tech plan §2.2) and only become a display string here: the client receives a finished
// sentence and must not add anything up (18.3#9 「前端二次累加为 0」).
func formatMoney(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	yuan := cents / 100
	frac := cents % 100
	body := strconv.FormatInt(yuan, 10)
	if frac != 0 {
		// Two decimals, zero-padded, and kept whole: 1205 cents is 12.05, not 12.5, and the 128 in
		// PRD 17.2's example stays 128 because it has no fraction at all.
		body += "." + pad2(frac)
	}
	if currency == "" || currency == "CNY" {
		return sign + "¥" + body
	}
	return sign + currency + " " + body
}

// pad2 renders the cent part as two digits.
func pad2(frac int64) string {
	s := strconv.FormatInt(frac, 10)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// warnFace logs a per-cell degradation. A cell that lost its data source must be visible in the log
// without failing the 首屏 request (17.2: 四区一个请求; a broken projection read is not an outage of the
// other three zones).
func (s *Services) warnFace(ctx context.Context, message, code string, err error) {
	if s.Logger == nil {
		return
	}
	s.Logger.Warn("home/summary 面格子降级",
		"face", code, "reason", message, "err", err.Error())
}
