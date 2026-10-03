// home_summary.go is GET /api/homeos/home/summary -- the 首屏's ONE business request
// (contracts/openapi/homeos.yaml /home/summary L365, PRD 17.2 「首页首屏只有一个业务请求」,
// tech plan §九, navigation doc §3.4 聚合接口形状).
//
// What this file adds is the wiring, not the answers. Every zone's data comes from a helper that
// already existed and had no caller: the A 区 name册 from repo.ListMembers, the C 区 set from
// s.composeFaceEntries filtered through mountedFaces and each cell's 状态句 from s.faceHeadline
// (both in faces.go), the B 区 block from repo.HomeSummaryDueToday filtered by that same mountedFaces
// output (dueFaceCodes below -- 17.8 定版 ⑯ counts 到期中心注册项 among the five consumers of one
// composition), the D 区 page from
// repo.ListDynamics and the single `unread` from repo.UnreadNotificationCounts. Re-implementing any
// of those here would give the 首页 an idea of the face set that could drift from the 开通页's -- and
// the identity PRD 17.8 定版 ⑯ states as 「同一份服务端合成，客户端只拿自己可见的那一份」 is only
// worth something while there is exactly one composition behind all three consumers, so
// home_summary_test.go asserts the equality across the HTTP boundary rather than here.
//
// Two rules bind every line below, and both are refusals rather than defaults:
//
//   - `family_id`, role and member id come from the session the auth middleware installed, never from
//     the request (PRD 15.2 「判定以 family_id 为界」). There is no family parameter to ignore.
//   - `?period=` is validated against the contract's pattern and the month's own range BEFORE the
//     first query runs, and an illegal value answers 400. It never falls back to the current month
//     (PRD 4.5.3 「接口收到无法解析的 period 返回 400，不得静默回落到当前月」, 定版 ⑬, 18.2#9 「非法
//     编码返回 400 不回落」). The ABSENT parameter is a different case and does take the default
//     window -- 家庭时区的当前月 (nav §3.4 line 「period 来自 shell 级时间窗 store，缺省为家庭时区的当前月」).
//
// The five reads are five queries against this service's own tables and nothing else: no synchronous
// call to svc-finance (tech plan §六 首页格子不得为渲染而打他服务, PRD 18.1 跨服务测量口径 counts a
// fan-out of more than one other service as a defect). The finance cell's numbers arrive through
// homeos_proj_finance, which the subscriber writes (16.3).
package handler

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
	// IANA names arrive from homeos_families.timezone (0006 NOT NULL, DEFAULT 'Asia/Shanghai'), and
	// 17.2 时段按家庭时区 / 定版 ⑬ 「时间窗一律按家庭时区计算」 make that name load-bearing for this
	// response's two windows. A slim container image carries no /usr/share/zoneinfo, so the database
	// is embedded rather than assumed; without it every 首屏 would quietly compute its 「今日」 in UTC.
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// periodPattern is homeos.yaml's `pattern: '^\d{4}-(\d{2}|Q[1-4])$'` written verbatim, so the gate
// this handler enforces and the gate the contract declares are one text rather than two that can
// disagree. Note what the pattern cannot say: `\d{2}` accepts 13, and time.Date would normalize
// 2026-13 into 2027-01 -- a wrong window, i.e. the silent fallback the documents forbid, arrived at
// by arithmetic. parsePeriod therefore checks the month's range itself (see the 2026-13 case in
// home_summary_test.go).
var periodPattern = regexp.MustCompile(`^\d{4}-(\d{2}|Q[1-4])$`)

// summaryDynamicLimit is D 区's page size: PRD 17.2 「家庭动态流前 20 条」 and the contract's
// dynamics.items maxItems: 20. repo.ListDynamics applies the same 20 as its own default policy
// (clampLimit), so the two readings of 「前 20 条」 are one number.
const summaryDynamicLimit = 20

// ==================== response shape (contract-exact) ====================

// HomeSummaryResponse is the contract's object: five keys, no sixth. The contract declares no
// `required` list, but every key here is populated from born data or from an empty collection --
// unlike due_today.count below, which is a number this checkout cannot honestly state and is
// therefore absent rather than invented.
type HomeSummaryResponse struct {
	Family   FamilyBlock   `json:"family"`
	DueToday DueTodayBlock `json:"due_today"`
	Faces    []FaceStatus  `json:"faces"`
	Dynamics DynamicsBlock `json:"dynamics"`
	Unread   int64         `json:"unread"`
}

// FamilyBlock is A 区. The contract's family block lists id / name / members[] / member_count and
// NOTHING else -- no role, no timezone, no currency, although homeos_families stores all three and
// nav §3.4's sample response shows them. They are not sent: adding a key the contract does not
// declare is how a handler starts shipping what the schema cannot be tested against, and the client
// already reports the missing timezone as a 定版冲突 (web/src/stores/home.ts FALLBACK_TIMEZONE).
// Reported, not patched here.
type FamilyBlock struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Members     []FamilyMemberCell `json:"members"`
	MemberCount int                `json:"member_count"`
}

// FamilyMemberCell is one member row, cut down to the contract's four cells. repo.MemberView carries
// user_id / role / has_account / joined_at as well, and 17.2 is explicit that 首页不用虚线、灰度或
// 角标把家庭成员分成两级 -- 「有没有账号」是治理属性，只在成员管理页显式标注 -- so those fields stay
// on GET /members and do not ride into the 首屏. `relation` is 定版 ⑭'s 称谓 sole source for the
// greeting; a NULL here is the 「无值时回落成员名」 case the client owns.
type FamilyMemberCell struct {
	MemberID string  `json:"member_id"`
	Name     string  `json:"name"`
	Relation *string `json:"relation"`
	Avatar   *string `json:"avatar"`
}

// DueTodayBlock is B 区, and `count` is a pointer for one reason.
//
// repo.HomeSummaryDueToday answers Answerable=false whenever svc-homeos has no 到期 registration table
// to count, and its own doc states the consequence: 「then Count is 0 because nothing was counted, NOT
// because nothing is due … must not print Count as a real number」. The contract gives this block two
// properties and no required list, so an absent `count` is the one legal way to say 「没有数过」 --
// `count: 0` would assert 今日无到期, and `count: null` would break the declared `type: integer`
// (the schema marks nullability where it means it, see family.members[].avatar). `items` stays a
// real array: sending zero entries is a fact about this response, while the count above is a claim
// about the family's day.
//
// Which branch a deployment is in is a migration fact, and it is stated here rather than assumed:
// homeos_0008_due_registration IS in the applied sequence, so a migrated checkout has the table and
// always gets a number. What it does not have is a producer -- nothing publishes finance.due.registered
// (only internal/consumer/finance_consumer.go mentions that subject), so the number a P1 首页 shows is
// a true count of an empty registration table, i.e. 0. That is reported as a B 区 dependency gap;
// withholding the count anyway would be the handler deciding the table is meaningless, which is the
// repo's call to make and not this one's. The gap between the contract's two-property block and a
// three-valued answer (没数过 / 数了 / 数了且为 0) is likewise reported, not closed by inventing a field
// or editing the yaml.
type DueTodayBlock struct {
	Count *int64              `json:"count,omitempty"`
	Items []repo.DueTodayItem `json:"items"`
}

// DynamicsBlock is D 区. The contract declares only `items` here (nav §3.4's sample also shows a
// `cursor`, which the 首页 never uses -- 「查看全部」 goes to GET /dynamics, which owns paging), so no
// cursor is emitted; the next page's token belongs to that endpoint.
type DynamicsBlock struct {
	Items []DynamicCell `json:"items"`
}

// DynamicCell is one feed entry, exactly the contract's six cells. summary and actor_name are the
// sentence and the name the writer froze at event time (0007: 「人名与整句都在写入时定稿」), which is
// what 17.2's 「文案由服务端拼好整句下发」 asks for and what 18.3#9's 「前端二次累加为 0」 presumes.
// entity / entity_id are deliberately not sent: the contract's home/summary item has no such key
// (nav §3.4 notes they are 落地参数 rather than 渲染要素), and the 深链 belongs to /dynamics.
type DynamicCell struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	ActorName string    `json:"actor_name"`
	Action    string    `json:"action"`
	Summary   string    `json:"summary"`
	At        time.Time `json:"at"`
}

// ==================== handler ====================

// GetHomeSummary handles GET /api/homeos/home/summary.
//
// Read gate: authz's homeos:module_config row + read, i.e. the SAME gate GET /family/modules uses.
// That is a documented reading rather than a convenience. PRD 15.3's 面配置 row grants R to
// 成人成员/儿童/访客 and spells out what the read is for -- 「只读，用于渲染首页矩阵、「＋」目标、搜索
// 分组等五个消费位」 -- so 首页矩阵 is exactly what that格子 authorizes, and 17.8's 定版 ⑯ then does the
// per-role 裁剪 INSIDE the composition (authz.Can scope=module per face), which is why a 儿童 or 访客
// legitimately gets `faces: []` rather than a 403 (17.8 第 4 条: 「儿童与访客等角色按…裁剪后可以是 0 格
// —— 此时 C 区整区不渲染，A/B/D 三区照常」: refusing them the request would delete those three zones).
//
// The two endpoints sharing one gate is also what makes the 同源 identity testable at all: a role
// that could reach one and not the other would produce two households' worth of answers about the
// same family, which is the defect 五处同源 exists to prevent.
//
// Signature is (c, s, mw) like the /family/modules pair, because svcauth.Require writes the 15.5
// denied-audit row through the middleware's OnDenied sink.
func GetHomeSummary(c *gin.Context, s *Services, mw *svcauth.Middleware) {
	sess, err := svcauth.SessionFrom(c)
	if err != nil {
		s.internal(c, "session_unavailable", err)
		return
	}
	if !svcauth.Require(c, mw, sess, authz.ResourceHomeOSModuleConfig, authz.ActionRead) {
		return
	}
	ctx := c.Request.Context()

	// ① period, before anything is read: the refusal is a property of the request, so it must not
	// depend on the database being reachable.
	spec, ok := parsePeriod(c.Query("period"))
	if !ok {
		badRequest(c, "invalid_period",
			"period 必须形如 YYYY-MM 或 YYYY-Qn（契约 pattern ^\\d{4}-(\\d{2}|Q[1-4])$，且月份须在 01-12 之内）；非法值返回 400，不回落为当前月（PRD 4.5.3 定版 ⑬、18.2#9）")
		return
	}

	// ② The family row: name for A 区, and the two per-family settings (timezone, currency) that decide
	// where the windows fall and how a money sentence reads. Both are read, never assumed --
	// 17.2 「时段按家庭时区」 and 14.7's 「金额一律以分为最小单位」, whose display side needs to know
	// which currency the cents are denominated in.
	fam, err := repo.GetFamily(ctx, s.DB, sess.FamilyID)
	if err != nil {
		if errors.Is(err, repo.ErrNoRow) {
			// The session names a family this checkout no longer has -- the same state the middleware
			// answers 401 for when the member row is gone (PRD 15.2 fail closed).
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "登录状态无效或不属于当前家庭"})
			return
		}
		s.internal(c, "family_load_failed", err)
		return
	}
	loc, err := time.LoadLocation(fam.Timezone)
	if err == nil && fam.Timezone == "" {
		err = errors.New("empty timezone")
	}
	if err != nil {
		// A timezone name the system does not know cannot be guessed at, and 500-ing the 首屏 over a
		// formatting boundary would take the three zones that need no window down with it. UTC is the
		// one non-family-specific choice left, and it is logged rather than quiet.
		loc = time.UTC
		s.warnTimezone(ctx, fam.Timezone, err)
	}

	// ③ The two windows B 区's repo call takes: the family's day for 到期, the request's period (or the
	// 缺省 current month) for the finance projection sums.
	now := time.Now()
	dayStart, dayEnd := dayWindow(now, loc)
	periodFrom, periodTo := spec.bounds(loc, now)

	// ④ A 区: the full 名册 including 无账号被记录成员 (17.2 「members[] 已是全量，客户端不数列表长度、
	// 不为 N 另发请求」), and member_count is that same slice's length, so the 「+N」 overflow figure and
	// the list can never be about two different sets.
	members, err := repo.ListMembers(ctx, s.DB, sess.FamilyID)
	if err != nil {
		s.internal(c, "members_load_failed", err)
		return
	}

	// ⑤ C 区: the ONE composition, filtered to the mounted-and-visible set (faces.go). Unborn and
	// unmounted faces appear nowhere in this response -- no placeholder cell, no greyed entry, no
	// 「即将上线」 row (17.2 「服务端只返回这个集合，客户端不补齐、不占位」, nav §3.3 「没有第三态」).
	entries, err := s.composeFaceEntries(ctx, sess.Claims, sess.FamilyID)
	if err != nil {
		s.internal(c, "modules_load_failed", err)
		return
	}

	// ⑥ B 区 / D 区 / 未读. A failure here is this service's own table being unreachable, which is a
	// 500 rather than an empty zone: 17.2's single request either answers the 首页 or says it cannot.
	//
	// B 区 is filtered by the SAME composition C 区 was just drawn from (dueFaceCodes below), because
	// PRD 17.8 定版 ⑯ puts 到期中心注册项 inside 五处同源 and its 聚合 rule reads 「一律按本家庭的已挂载
	// 面集合过滤」. Reading the registration table unfiltered would show a 被停用的财务面 its 账单到期
	// while C 区 shows no 财务格子 -- two answers about one household from one response, i.e. the exact
	// divergence 同源 exists to prevent, and the reason the set travels with the query instead of being
	// re-derived (18.2#12③) or applied by the client (17.7 第 4 条).
	due, err := repo.HomeSummaryDueToday(ctx, s.DB, sess.FamilyID, repo.DueWindow{
		DayStart:   dayStart,
		DayEnd:     dayEnd,
		PeriodFrom: periodFrom,
		PeriodTo:   periodTo,
	}, dueFaceCodes(entries))
	if err != nil {
		s.internal(c, "due_today_load_failed", err)
		return
	}
	// memberID is passed with onlyUnread=false: the 首页 shows the family's feed, not the caller's
	// unread subset (17.2 D 区 「家庭动态流前 20 条」), and repo.ListDynamics refuses an unread filter it
	// cannot attribute to a member. Per-entry 15.3/15.4 裁剪 is the same seam GET /dynamics will use --
	// 17.2 「与 GET /api/homeos/dynamics 同一个口径」 -- and is not this handler's copy to make.
	dynamics, _, err := repo.ListDynamics(ctx, s.DB, sess.FamilyID, sess.MemberID, nil, false, nil, summaryDynamicLimit)
	if err != nil {
		s.internal(c, "dynamics_load_failed", err)
		return
	}
	// `unread` is the MESSAGE count, not the dynamics count: PRD 17.1 第 2 条 names one caliber --
	// homeos_notification.read_at IS NULL aggregated by member_id -- and nav §3.4 requires the 首页 and
	// GET /notifications to agree on that number. The per-type map is dropped: the contract types
	// `unread` as a plain integer here, and the three type tabs are the message page's own read of
	// /notifications.
	_, unread, err := repo.UnreadNotificationCounts(ctx, s.DB, sess.FamilyID, sess.MemberID)
	if err != nil {
		s.internal(c, "unread_count_failed", err)
		return
	}

	c.JSON(http.StatusOK, HomeSummaryResponse{
		Family: FamilyBlock{
			ID:          fam.ID,
			Name:        fam.Name,
			Members:     summaryMembers(members),
			MemberCount: len(members),
		},
		DueToday: summaryDueToday(due),
		Faces:    s.summaryFaces(ctx, sess.FamilyID, fam.Currency, entries, periodFrom, periodTo),
		Dynamics: DynamicsBlock{Items: summaryDynamics(dynamics)},
		Unread:   unread,
	})
}

// ==================== zone mappers ====================

// summaryMembers projects repo.MemberView onto the contract's four cells. The 全量 list arrives from
// one query, so A 区 needs no second count query and the two cannot disagree (定版 ⑮).
func summaryMembers(rows []repo.MemberView) []FamilyMemberCell {
	out := make([]FamilyMemberCell, 0, len(rows))
	for _, r := range rows {
		out = append(out, FamilyMemberCell{
			MemberID: r.MemberID,
			Name:     r.Name,
			Relation: r.Relation,
			Avatar:   r.Avatar,
		})
	}
	return out
}

// summaryDueToday maps the repo's honest B 区 block onto the contract's two properties, and is the
// only place Answerable is translated. See DueTodayBlock's doc for why false means a missing key
// rather than a zero.
func summaryDueToday(due repo.DueToday) DueTodayBlock {
	items := due.Items
	if items == nil {
		// A real empty array, so the client's `items.length > 0` test renders the 「今日暂无」 state
		// rather than tripping over a missing key (17.2 「今日只有 1~2 项时小卡按实际条数排布，不填空位」).
		items = []repo.DueTodayItem{}
	}
	block := DueTodayBlock{Items: items}
	if due.Answerable {
		count := due.Count
		block.Count = &count
	}
	return block
}

// dueFaceCodes names the face set B 区's due read filters on, spelled in the currency
// homeos_due_registration.source_system stores (registry codes, PRD 16.1 / 0008:29-30).
//
// The set is `mountedFaces` of the one composition -- not "every code the family has an enabled row
// for". The two differ for a 儿童/访客 session, and the difference is the point: 15.3's scope=module
// row is already applied inside composeFaceEntries (定版 ⑯), so a ward whose role is 「-」 on 财务 must
// not get 财务's 账单到期 into B 区 either, even though C 区 correctly shows them no 财务格子.
//
// The 底座 code is then appended, and that is not a second composition: composeFaceEntries subtracts
// registry.HomeosCode on purpose, because PRD 17.1 「首页自身不进矩阵」 means the 底座 has no cell to
// draw (faces.go). homeos_due_registration still stores 底座-sourced registrations -- the 待办 side of
// B 区's 「今日到期与待办」 (17.2) is the 底座's own data, and TIME-1's homeos_todos lands there -- and
// PRD 17.8's rule filters 「被停用面」 items out. The 底座 is not a face a family can 停用: there is no
// homeos_family_module row for it and 17.8 第 4 条 forbids a 0 面家庭, so dropping its code would delete
// a zone the document says must keep rendering (17.8 第 4 条 「…此时 C 区整区不渲染，A/B/D 三区照常」).
// Nothing here re-decides mountability; the only added term is the code the composition documents as
// deliberately omitted.
func dueFaceCodes(entries []FaceEntry) []string {
	mounted := mountedFaces(entries)
	out := make([]string, 0, len(mounted)+1)
	for _, e := range mounted {
		out = append(out, e.Code)
	}
	return append(out, registry.HomeosCode)
}

// summaryFaces turns the shared composition into faces[] cells. Order is untouched (mountedFaces
// preserves registry registration order, which PRD 17.1/17.2 pin as the matrix's row order), and each
// cell's sentence and freshness come from s.faceHeadline, which degrades a cell whose projection this
// checkout does not compute instead of failing the 首屏 (faces.go's doc states that choice).
//
// No `badge` key is emitted: FaceStatus does not carry one. PRD 17.1 第 3 条 separates 待处理数 from
// 未读 and nav §3.5 defines the finance cell's 待处理 as 「待缴账单数」, which lives in finance's own
// tables svc-homeos may not read (tech plan §六); 0004's DDL says the same thing about its own table
// 「角标（badge）本身的计数列形态文档未给 -> 本卡不落」. The projection supplies 收支 与 预算剩余 sums
// and an updated_at, and no per-face 待处理 count exists in born data at P1, so the field is absent
// (contract has no required list) rather than 0 -- a 0 there is a claim that nothing is pending,
// which is exactly the 假数字 定版 ⑬ and 门禁「不写死假数据」 refuse. The client renders
// `v-if="face.badge > 0"`, so an absent badge draws no角标.
func (s *Services) summaryFaces(
	ctx context.Context,
	familyID string,
	currency string,
	entries []FaceEntry,
	from time.Time,
	to time.Time,
) []FaceStatus {
	mounted := mountedFaces(entries)
	out := make([]FaceStatus, 0, len(mounted))
	for _, e := range mounted {
		headline, asOf := s.faceHeadline(ctx, familyID, e, currency, from, to)
		out = append(out, FaceStatus{
			Code:         e.Code,
			Name:         e.Name,
			Availability: e.Availability,
			Headline:     headline,
			AsOf:         asOf,
		})
	}
	return out
}

// summaryDynamics projects the feed rows onto the contract's six cells, capped at D 区's page size.
// repo.ListDynamics already limits to the 20 it was handed, so the cap is an assertion about this
// response rather than a second implementation of paging; the contract's maxItems: 20 is then
// satisfied without the client truncating (17.2 「客户端不自行截断」).
func summaryDynamics(rows []model.HomeosDynamic) []DynamicCell {
	n := len(rows)
	if n > summaryDynamicLimit {
		n = summaryDynamicLimit
	}
	out := make([]DynamicCell, 0, n)
	for _, r := range rows[:n] {
		out = append(out, DynamicCell{
			ID:        r.ID,
			Code:      r.Code,
			ActorName: r.ActorName,
			Action:    r.Action,
			Summary:   r.Summary,
			At:        r.At,
		})
	}
	return out
}

// ==================== period / window math ====================

// periodSpec is a validated ?period value held as (year, first month, month span) rather than as two
// instants, because the instants only exist once the family's timezone is known (定版 ⑬: 跨时区家庭的
// 「2026-Q3」以家庭时区为边界).
type periodSpec struct {
	year   int
	month  int
	months int
	// explicit records 「the caller did send a period」, which is what separates the default window
	// from a refusal: an absent parameter is documented to take the current month, a present-but-bad
	// one never does.
	explicit bool
}

// parsePeriod validates the raw query value. ok=false means 400; ok=true with explicit=false means
// 「absent, use the family's current month」.
//
// Two checks, not one. The contract's pattern decides the FORM (four digits, then a two-digit month
// or Q1-Q4); a second range check decides the VALUE, because `\d{2}` is not a calendar and 2026-13
// would otherwise reach time.Date and normalize into 2027-01 -- a 12-months-off window handed to the
// 首屏 with a 200. The quarter arm needs no range check: the pattern's Q[1-4] already is one, which
// is why 2026-Q5 stops at the first gate.
//
// A bare year (`2026`) is refused here. That is the contract as written -- its pattern has no year
// arm -- while PRD 4.5.3 / 定版 ⑬ / nav §1.3 第 19 条 all name three 档 with 年 encoded as `YYYY`, and
// svc-finance's own ValidatePeriod accepts it. The 首页's 年 档 therefore cannot reach this endpoint;
// reported as a contract defect (the yaml's pattern vs the yaml's own description line
// 「YYYY-MM / YYYY-Qn / YYYY」), not worked around by widening the gate past the schema this handler
// is contract-tested against.
func parsePeriod(raw string) (periodSpec, bool) {
	if raw == "" {
		return periodSpec{}, true
	}
	if !periodPattern.MatchString(raw) {
		return periodSpec{}, false
	}
	year, err := strconv.Atoi(raw[:4])
	if err != nil {
		return periodSpec{}, false
	}
	if raw[5] == 'Q' {
		// Q1 -> months 1..3, Q4 -> months 10..12: 自然季按家庭时区 (PRD 4.5.3 「1-3 月为 Q1」).
		return periodSpec{year: year, month: (int(raw[6]-'1') * 3) + 1, months: 3, explicit: true}, true
	}
	month, err := strconv.Atoi(raw[5:7])
	if err != nil || month < 1 || month > 12 {
		return periodSpec{}, false
	}
	return periodSpec{year: year, month: month, months: 1, explicit: true}, true
}

// bounds turns the spec into the half-open [from, to) the projection sums are taken over. AddDate on
// the month's first day is the engine-neutral way of getting a month that actually ends (a fixed
// 30-day span would drop days from every 31-day month and lose a month from 2026-02).
func (p periodSpec) bounds(loc *time.Location, now time.Time) (time.Time, time.Time) {
	year, month, span := p.year, p.month, p.months
	if !p.explicit {
		year, month, span = now.Year(), int(now.Month()), 1
	}
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	return from, from.AddDate(0, span, 0)
}

// dayWindow is B 区's 「今日」 in the family's timezone (17.2 「时段按家庭时区」), which is not the
// device's day and not UTC for a 00:30 household hour. Half-open, so an item at exactly midnight
// tomorrow is tomorrow's business rather than counted twice.
func dayWindow(now time.Time, loc *time.Location) (time.Time, time.Time) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

// warnTimezone logs the one degradation this handler can make. It is loud on purpose: 「今日」 computed
// at the wrong boundary is a wrong-looking 首页 that still returns 200, so the operator needs a line
// connecting it to the family row that carried the bad name.
func (s *Services) warnTimezone(ctx context.Context, timezone string, err error) {
	if s.Logger == nil {
		return
	}
	s.Logger.Warn("home/summary 家庭时区不可解析，窗口按 UTC 计算",
		"timezone", timezone, "err", err.Error())
}
