package registry_test

import (
	"strings"
	"testing"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// This file holds the criteria that do not need the document text: cell shape (七域 × 七维 逐格
// 非空且形态正确, tech plan §1.3), the order contract (PRD 17.1 / 17.2, navigation doc §3.2),
// the P1 implemented/unborn split (PRD 卷首第 12 项, 22.2 第 1 条, tech plan 定版 E ★) and the
// bundle set formula (§1.3 row 6, navigation doc §2.4 第 2 查, PRD 22.5 第 4 道).
// The verbatim comparison against PRD 16.1 is in registry_prd_test.go; the filesystem
// two-way assertions are in registry_fs_test.go.

// dim is one naming dimension: the dimension label used in the failure message, the value the
// table stores, and the value the documented form requires for that code.
type dim struct {
	label string
	got   func(registry.Domain) string
	want  func(registry.Domain) string
}

// dimensions are §1.3's seven rows plus the account value named by row 2's 校验点
// (「账号名 hc_{code}」, spelled out in §2.1). Cell count in the assertions below is derived
// from len(Domains()) x len(dimensions), so registering an eighth domain automatically widens
// the grid instead of leaving a dimension untested (禁令 7).
var dimensions = []dim{
	{
		label: "服务名 svc-{code} (§1.3 row 1 / PRD 16.1 命名说明)",
		got:   func(d registry.Domain) string { return d.ServiceName },
		want:  func(d registry.Domain) string { return "svc-" + d.Code },
	},
	{
		label: "schema {code} (§1.3 row 2 / PRD 16.1 命名说明)",
		got:   func(d registry.Domain) string { return d.Schema },
		want:  func(d registry.Domain) string { return d.Code },
	},
	{
		label: "账号 hc_{code} (§1.3 row 2 校验点 / §2.1)",
		got:   func(d registry.Domain) string { return d.DBAccount },
		want:  func(d registry.Domain) string { return "hc_" + d.Code },
	},
	{
		// The cell stores the prefix form the routing rules use (PRD 卷首第 12 项「按 /api/{code}/
		// 前缀转发」, 22.2 第 1 条, tech plan §1.1); §1.3 row 3 and PRD 16.1 write the same route as a
		// glob, which RoutePrefixGlob() reproduces.
		label: "路由 /api/{code}/ (PRD 卷首第 12 项 / 22.2 第 1 条 / §1.1；glob 形态见 RoutePrefixGlob)",
		got:   func(d registry.Domain) string { return d.RoutePrefix },
		want:  func(d registry.Domain) string { return "/api/" + d.Code + "/" },
	},
	{
		label: "subject {code}. (§1.3 row 4 / PRD 16.1 事件前缀)",
		got:   func(d registry.Domain) string { return d.SubjectPrefix },
		want:  func(d registry.Domain) string { return d.Code + "." },
	},
	{
		label: "表前缀 {code}_ (§1.3 row 5 / PRD 16.1 表名前缀)",
		got:   func(d registry.Domain) string { return d.TablePrefix },
		want:  func(d registry.Domain) string { return d.Code + "_" },
	},
	{
		label: "分包 pages/{code} (§1.3 row 6 / PRD 16.1 前端分包, 17.7 第 4 条)",
		got:   func(d registry.Domain) string { return d.BundlePath },
		want:  func(d registry.Domain) string { return "pages/" + d.Code },
	},
	{
		label: "迁移 migrations/{code} (§1.3 row 7 / §1.2 仓库布局, §2.2)",
		got:   func(d registry.Domain) string { return d.MigrationDir },
		want:  func(d registry.Domain) string { return "migrations/" + d.Code },
	},
}

func TestEveryDomainEveryDimension(t *testing.T) {
	domains := registry.Domains()
	if len(domains) == 0 {
		t.Fatal("registry.Domains() returned no rows: the domain table is empty, so every " +
			"downstream assertion in this file would be a no-op")
	}

	cells := 0
	for _, d := range domains {
		if d.Code == "" {
			t.Errorf("domain row %+v has an empty Code: every dimension is keyed off it", d)
			continue
		}
		for _, tc := range dimensions {
			cells++

			got := tc.got(d)
			// 非空：登记缺格与登记错格式是两种缺陷，必须分开报。
			if got == "" {
				t.Errorf("domain %q dimension %s: value is empty (tech plan §1.3 requires all "+
					"seven naming dimensions registered for all seven codes in P1)", d.Code, tc.label)
				continue
			}
			// 形态：值必须由 code 派生出文档写的那个形状。
			if want := tc.want(d); got != want {
				t.Errorf("domain %q dimension %s: got %q, want %q (form derived from the code, "+
					"tech plan §1.3 「任一处偏离即视为架构漂移」)", d.Code, tc.label, got, want)
			}
		}
	}

	if want := len(domains) * len(dimensions); cells != want {
		t.Errorf("grid size: visited %d cells, want %d (%d domains x %d dimensions)",
			cells, want, len(domains), len(dimensions))
	}
	t.Logf("checked %d cells = %d domains x %d dimensions", cells, len(domains), len(dimensions))
}

func TestDomainCountIsSeven(t *testing.T) {
	// PRD 卷首第 12 项, 16.1 (七个 code), 14.5 第 1 项, tech plan §1.3「P1 一次性登记七个 code」.
	if got := len(registry.Domains()); got != 7 {
		t.Errorf("len(Domains()) = %d, want 7 (homeos/finance/purchase/diet/trip/kin/growth -- "+
			"PRD 16.1, 卷首第 12 项)", got)
	}
}

func TestDomainsOrderIsRegistrationOrder(t *testing.T) {
	// 顺序即契约：PRD 17.1「顺序恒为 registry 登记顺序（当前为 财务/采购/饮食/出行/家人/成长）」,
	// PRD 17.2 (faces[] 顺序按 registry 登记序), navigation doc §3.2 第 1 条. homeos leads as the
	// base domain and is not part of the matrix (首页自身不进矩阵).
	want := []string{"homeos", "finance", "purchase", "diet", "trip", "kin", "growth"}

	got := registry.Domains()
	if len(got) != len(want) {
		t.Fatalf("Domains() length = %d, want %d (want order %v)", len(got), len(want), want)
	}
	for i, d := range got {
		if d.Code != want[i] {
			t.Errorf("Domains()[%d].Code = %q, want %q -- registration order is the home-matrix "+
				"order (PRD 17.1 / 17.2, navigation doc §3.2); position %d of %v",
				i, d.Code, want[i], i, want)
		}
	}

	// 顺序稳定性：同一进程内两次调用必须给出同一序列，否则「顺序即契约」只是巧合。
	again := registry.Domains()
	for i := range again {
		if again[i].Code != got[i].Code {
			t.Errorf("Domains() is not stable at index %d: first %q, second %q",
				i, got[i].Code, again[i].Code)
		}
	}
}

func TestDomainsReturnsACopy(t *testing.T) {
	// §1.3 makes this table the 唯一真源 shared by service startup, the migration tool, the
	// front-end build script and CI. A consumer must not be able to write into it.
	before := registry.Domains()
	if len(before) == 0 {
		t.Fatal("no rows to protect: Domains() is empty, so this test cannot prove anything")
	}
	before[0].Code = "mutated"
	after := registry.Domains()
	if after[0].Code == "mutated" {
		t.Error("Domains() exposed the table: mutating the returned slice changed the registry, " +
			"which breaks the single-source guarantee of tech plan §1.3")
	}
	if _, ok := registry.ByCode("mutated"); ok {
		t.Error(`ByCode("mutated") returned a row: the table was writable through Domains()`)
	}
}

func TestDocumentedP1PairIsHomeosAndFinance(t *testing.T) {
	// PRD 卷首第 12 项 / 22.2 第 1 条 / 22.4 / tech plan 定版 E ★: P1 实建 svc-homeos + svc-finance,
	// 其余五域只以 registry 条目存在. The claim is about phase P1, so it is evaluated through the
	// phase argument (ImplementedAt / NewbornSetAt / UnbornSetAt) rather than through CurrentPhase:
	// it stays true and stays a real assertion in a P2 or P6 checkout. Whether the DECLARED flags
	// agree with the phase-derived set at whatever phase this checkout is belongs to
	// TestImplementedSetMatchesPRDThreeStates (registry_prd_test.go) and
	// TestUnbornIsComplementOfImplemented below -- both are phase-aware, so bumping CurrentPhase
	// without marking the new face implemented goes red there, not here.
	if got := domainCodes(registry.ImplementedAt("P1")); strings.Join(got, ",") != "homeos,finance" {
		t.Errorf(`ImplementedAt("P1") = %v, want [homeos finance] (PRD 卷首第 12 项)`, got)
	}
	if got := domainCodes(registry.NewbornSetAt("P1")); strings.Join(got, ",") != "homeos,finance" {
		t.Errorf(`NewbornSetAt("P1") = %v, want [homeos finance] -- 当期新增出生 = 本期交付与期末验收对象 `+
			"(tech plan 定版 M ★, PRD 14.5 第 1 项把底座也算进 P1)", got)
	}

	unborn := domainCodes(registry.UnbornSetAt("P1"))
	if want := []string{"purchase", "diet", "trip", "kin", "growth"}; strings.Join(unborn, ",") != strings.Join(want, ",") {
		t.Errorf(`UnbornSetAt("P1") = %v, want %v (五个未出生域只以 registry 条目存在, PRD 11.4 / 11.7 第 1 条)`,
			unborn, want)
	}

	// No row of a later phase may be declared implemented in this checkout, whatever the checkout is:
	// 实建 ⇒ BirthPhase <= CurrentPhase (PRD 11.7 第 1 条). The same implication read off the document
	// fixture is asserted in registry_prd_test.go; this is the registry-side statement of it.
	for _, d := range registry.Implemented() {
		if !d.Born() {
			t.Errorf("domain %q is marked implemented but has BirthPhase %s > CurrentPhase %s: 提前建服务 "+
				"(PRD 11.7 第 1 条, tech plan 定版 E ★)", d.Code, d.BirthPhase, registry.CurrentPhase)
		}
	}
}

func TestUnbornIsComplementOfImplemented(t *testing.T) {
	// The five codes that exist only as registry entries (tech plan 卷首「当期形状」, §1.3, 定版 E ★).
	//
	// Unborn() is the PHASE-derived set (BirthPhase > CurrentPhase) and Implemented() is the DECLARED
	// flag set, so this test is not a tautology: it is the invariant §1.3 row 6 writes as
	// 「已出生业务域 = registry 实建域」. A row that is born but not declared implemented (a bump that
	// forgot the flag) and a row that is declared implemented before its birth phase (PRD 11.7 第 1
	// 条) both land here, and both would otherwise make the filesystem assertions in
	// registry_fs_test.go skip a domain.
	impl := map[string]bool{}
	for _, d := range registry.Implemented() {
		impl[d.Code] = true
	}

	unborn := map[string]bool{}
	for _, d := range registry.Unborn() {
		if impl[d.Code] {
			t.Errorf("code %q appears in both Implemented() and Unborn(): CI would both require and "+
				"forbid its directories", d.Code)
		}
		unborn[d.Code] = true
	}

	for _, d := range registry.Domains() {
		if !impl[d.Code] && !unborn[d.Code] {
			t.Errorf("code %q is in neither Implemented() nor Unborn(): the partition is incomplete, "+
				"so §1.3's 「不该存在的东西必须不存在」 check silently skips it (declared flag disagrees with "+
				"BirthPhase %s at CurrentPhase %s)", d.Code, d.BirthPhase, registry.CurrentPhase)
		}
		if impl[d.Code] == unborn[d.Code] {
			t.Errorf("code %q is in both or neither set (implemented=%v)", d.Code, impl[d.Code])
		}
	}

	// The P1 value is read off the documents (PRD 11.4 gives five faces later than P1), through the
	// phase argument so it stays a fact in later checkouts.
	if got := len(registry.UnbornSetAt("P1")); got != 5 {
		t.Errorf(`len(UnbornSetAt("P1")) = %d, want 5 (purchase/diet/trip/kin/growth per PRD 11.4)`, got)
	}
	if got, want := len(registry.Unborn()), len(registry.UnbornSetAt(registry.CurrentPhase)); got != want {
		t.Errorf("Unborn() = %d rows but UnbornSetAt(CurrentPhase) = %d: Unborn() must delegate", got, want)
	}
}

func TestBundleSetAtP1IsFinance(t *testing.T) {
	// §1.3 row 6:「分包集合 = 已出生业务域 = registry 实建域 − homeos」; navigation doc §2.4 第 2 查;
	// PRD 22.5 第 4 道. P1 has exactly one subPackage, pages/finance (tech plan 卷首「当期形状」:
	// 「1 个前端分包（pages/finance）」). Pinned through BundleSetAt("P1") so the documented P1 value
	// keeps being asserted even after this checkout moves on; the current-phase equivalence to the
	// declared flags is TestBundleSetAgreesWithImplementedMinusHomeos.
	got := registry.BundleSetAt("P1")

	if len(got) != 1 {
		t.Fatalf(`len(BundleSetAt("P1")) = %d, want 1; got %v`, len(got), got)
	}
	if got[0] != "finance" {
		t.Errorf(`BundleSetAt("P1")[0] = %q, want %q`, got[0], "finance")
	}

	// BundleSet() must be the same function at the current phase -- the delegation is what keeps
	// registryd --format=bundles and --format=meta honest.
	if cur := registry.BundleSetAt(registry.CurrentPhase); strings.Join(cur, ",") != strings.Join(registry.BundleSet(), ",") {
		t.Errorf("BundleSet() = %v but BundleSetAt(CurrentPhase) = %v", registry.BundleSet(), cur)
	}

	// homeos 必须被扣掉：它是主包不是分包，留着它 CI 会在正确的工程上失败。
	for _, phase := range append([]string{""}, docPhaseOrder...) {
		for _, code := range registry.BundleSetAt(phase) {
			if code == registry.HomeosCode {
				t.Errorf("BundleSetAt(%q) contains %q: HomeOS pages live in the main package, there is no "+
					"pages/homeos subPackage (§1.3 row 6 校验点, PRD 17.7 第 4 条)", phase, code)
			}
		}
	}
}

func TestBundleSetAtUnknownPhaseIsEmpty(t *testing.T) {
	// Fail closed: a typo in the phase must not hand out bundles, directories or streams
	// (phaseLE answers false for an unparsable label on either side).
	for _, phase := range []string{"", "p1", "P0", "P10", "Phase1", "P", "P1 "} {
		if got := registry.BundleSetAt(phase); len(got) != 0 {
			t.Errorf("BundleSetAt(%q) = %v, want empty: an unparsable phase must grant nothing", phase, got)
		}
		if got := registry.ImplementedAt(phase); len(got) != 0 {
			t.Errorf("ImplementedAt(%q) = %v, want empty (fail closed)", phase, got)
		}
		if got := len(registry.UnbornSetAt(phase)); got != len(registry.Domains()) {
			t.Errorf("ImplementedAt(%q) empty but UnbornSetAt(%q) = %d rows, want all %d: the two halves "+
				"must stay a partition", phase, phase, got, len(registry.Domains()))
		}
		for _, d := range registry.Domains() {
			if d.BornAt(phase) {
				t.Errorf("domain %q BornAt(%q) = true although the label is unparsable", d.Code, phase)
			}
			if d.NewlyBornAt(phase) {
				t.Errorf("domain %q NewlyBornAt(%q) = true although the label is unparsable", d.Code, phase)
			}
		}
	}
}

func TestThreeStatesAreExactlyOnePerDomain(t *testing.T) {
	// 三态互斥且全覆盖 (PRD 卷首第 12 项「其余五个服务在各自出生期创建」, 11.7 第 1 条, tech plan
	// 定版 M ★): over every documented phase, each registered row is in exactly one of 未出生
	// (BirthPhase > phase), 当期新增出生 (BirthPhase == phase) and 早期已出生 (BirthPhase < phase).
	// The retired model put 实建 on the middle state alone, which is what made the three readings
	// mutually exclusive from P2 onwards; this test is the shape-level guard against re-merging them.
	for _, phase := range docPhaseOrder {
		for _, d := range registry.Domains() {
			in := 0
			if !d.BornAt(phase) {
				in++
			}
			if d.NewlyBornAt(phase) {
				in++
			}
			if d.BornAt(phase) && !d.NewlyBornAt(phase) {
				in++
			}
			if in != 1 {
				t.Errorf("phase %s, domain %q (BirthPhase %s): %d of the three states apply, want exactly 1",
					phase, d.Code, d.BirthPhase, in)
			}
		}

		// 早期已出生 rows must exist from P2 on -- any phase strictly after some row's birth --
		// because that is the case the == model got wrong: those rows are built, they are not this
		// phase's delivery, and they are not unborn.
		var earlier int
		for _, d := range registry.Domains() {
			if d.BornAt(phase) && !d.NewlyBornAt(phase) {
				earlier++
			}
		}
		if phase != "P1" && earlier == 0 {
			t.Errorf("phase %s: no row is 早期已出生, so the model cannot be told apart from the retired == "+
				"reading here (PRD 11.4 puts 财务/采购/饮食 before P4)", phase)
		}
	}
}

func TestNoSuffixPredicatesDelegateToCurrentPhase(t *testing.T) {
	// Born()/NewlyBorn()/BundleSet()/Unborn() are the CurrentPhase shorthands the consumers use;
	// if one of them stopped delegating, registryd's meta export and the Go-side callers would
	// disagree with the phase-derived sets.
	for _, d := range registry.Domains() {
		if d.Born() != d.BornAt(registry.CurrentPhase) {
			t.Errorf("domain %q: Born() = %v but BornAt(CurrentPhase) = %v",
				d.Code, d.Born(), d.BornAt(registry.CurrentPhase))
		}
		if d.NewlyBorn() != d.NewlyBornAt(registry.CurrentPhase) {
			t.Errorf("domain %q: NewlyBorn() = %v but NewlyBornAt(CurrentPhase) = %v",
				d.Code, d.NewlyBorn(), d.NewlyBornAt(registry.CurrentPhase))
		}
	}
	if got, want := domainCodes(registry.Unborn()), domainCodes(registry.UnbornSetAt(registry.CurrentPhase)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Unborn() = %v but UnbornSetAt(CurrentPhase) = %v", got, want)
	}
}

func TestBundleSetAgreesWithImplementedMinusHomeos(t *testing.T) {
	// Two documented definitions of the same set: §1.3 says「registry 实建域 − homeos」, while
	// navigation doc §2.4 and PRD 22.5 第 4 道 say「出生期 ≤ 当期」− homeos. They must agree, or a
	// phase bump that forgets to mark a domain implemented produces a missing subPackage.
	want := map[string]bool{}
	for _, d := range registry.Implemented() {
		if d.Code != registry.HomeosCode {
			want[d.Code] = true
		}
	}

	got := map[string]bool{}
	for _, code := range registry.BundleSet() {
		if got[code] {
			t.Errorf("BundleSet() lists %q twice", code)
		}
		got[code] = true
	}

	for code := range want {
		if !got[code] {
			t.Errorf("BundleSet() misses %q: it is implemented but has no bundle entry, so pages.json "+
				"三查 (navigation doc §2.4) fails on a correct project", code)
		}
	}
	for code := range got {
		if !want[code] {
			t.Errorf("BundleSet() lists %q, which is not an implemented non-homeos domain", code)
		}
	}
}

func TestByCode(t *testing.T) {
	for _, d := range registry.Domains() {
		got, ok := registry.ByCode(d.Code)
		if !ok {
			t.Errorf("ByCode(%q) reported not found for a registered domain", d.Code)
			continue
		}
		if got != d {
			t.Errorf("ByCode(%q) returned a different row than Domains() carries: got %+v, want %+v",
				d.Code, got, d)
		}
	}

	for _, code := range []string{"", "homeOS", "meal", "family", "travel", "finance ", "svc-finance"} {
		if got, ok := registry.ByCode(code); ok {
			t.Errorf("ByCode(%q) unexpectedly matched row %+v: codes are lowercase single tokens from "+
				"PRD 16.1 (diet is diet, not meal; kin is kin, not family; trip is trip, not travel)",
				code, got)
		}
	}
}

func TestCodesAreUniqueAcrossEveryDimension(t *testing.T) {
	// A collision would make gate 2/4 checks ambiguous (two domains claiming one table prefix or
	// one route prefix is exactly the 「架构漂移」 §1.3 is written to prevent).
	seen := map[string]string{}
	for _, d := range registry.Domains() {
		for _, tc := range append(dimensions, dim{
			label: "code 本身 (PRD 16.1 code 列)",
			got:   func(x registry.Domain) string { return x.Code },
			want:  func(x registry.Domain) string { return x.Code },
		}) {
			v := tc.got(d)
			if prev, ok := seen[v]; ok && prev != d.Code {
				t.Errorf("dimension %s value %q is claimed by both %q and %q", tc.label, v, prev, d.Code)
			}
			seen[v] = d.Code
		}
	}
}

func TestDerivationsMatchDocumentedForms(t *testing.T) {
	// The methods named in the package doc's "Derived naming classes" map: each must produce the
	// documented shape out of a stored cell, for every registered domain (禁令 7 -- the same
	// derivation is checked for all seven codes, not just the two built ones).
	for _, d := range registry.Domains() {
		// §3.1: 流 HC_HOMEOS 的 subjects 是 homeos.>、HC_FINANCE 是 finance.> -- 即 {code}.>，无空格。
		if got, want := d.StreamSubjectPattern(), d.SubjectPrefix+">"; got != want {
			t.Errorf("domain %q StreamSubjectPattern() = %q, want %q", d.Code, got, want)
		}
		if got := d.StreamSubjectPattern(); strings.ContainsAny(got, " ") {
			t.Errorf("domain %q StreamSubjectPattern() = %q, but tech plan §3.1 registers the pattern "+
				"without whitespace", d.Code, got)
		}

		// §1.3 row 2 校验点 + §2.1 DSN 形态: search_path={code}。
		if got, want := d.DSNSearchPath(), "search_path="+d.Code; got != want {
			t.Errorf("domain %q DSNSearchPath() = %q, want %q (tech plan §2.1 DSN shape)", d.Code, got, want)
		}

		// 账号必须以 hc_ 开头且紧跟 schema（§2.1「CREATE ROLE hc_finance」）。
		if d.DBAccount != "hc_"+d.Schema {
			t.Errorf("domain %q DBAccount = %q, want %q (§1.3 row 2 校验点, §2.1)",
				d.Code, d.DBAccount, "hc_"+d.Schema)
		}

		// PRD 卷首第 12 项「按 /api/{code}/ 前缀转发」/ 22.2 第 1 条 / tech plan §1.1 拓扑图 register the
		// prefix shape the cell stores; PRD 16.1「接口前缀」, 22.2 第 8 条, 22.5 第 4 道 and §1.3 row 3
		// 取值 register the glob shape RoutePrefixGlob() produces.
		if got, want := d.RoutePrefix, "/api/"+d.Code+"/"; got != want {
			t.Errorf("domain %q RoutePrefix = %q, want the prefix form %q", d.Code, got, want)
		}
		if got, want := d.RoutePrefixGlob(), "/api/"+d.Code+"/*"; got != want {
			t.Errorf("domain %q RoutePrefixGlob() = %q, want the PRD 16.1 form %q", d.Code, got, want)
		}
		if !strings.HasSuffix(d.RoutePrefix, "/") || strings.Contains(d.RoutePrefix, "*") {
			t.Errorf("domain %q RoutePrefix = %q: the stored cell must be a concatenable prefix with no "+
				"glob character, or route groups and Nginx locations inherit the wrong shape", d.Code, d.RoutePrefix)
		}

		// §3.1 流表: HC_HOMEOS / HC_FINANCE 行 +「HC_PURCHASE … HC_GROWTH（5 条）」-- the code,
		// upper-cased, after "HC_".
		if got, want := d.StreamName(), "HC_"+strings.ToUpper(d.Code); got != want {
			t.Errorf("domain %q StreamName() = %q, want %q (tech plan §3.1)", d.Code, got, want)
		}

		// §3.1 rows HC_ARCHIVE / HC_DL: arch.{code}.> and dl.{code}.>, both on the same {code}. base
		// as the source stream so the three cannot drift apart.
		if got, want := d.ArchiveSubjectPattern(), "arch."+d.Code+".>"; got != want {
			t.Errorf("domain %q ArchiveSubjectPattern() = %q, want %q (tech plan §3.1 row HC_ARCHIVE)",
				d.Code, got, want)
		}
		if got, want := d.DeadLetterSubjectPattern(), "dl."+d.Code+".>"; got != want {
			t.Errorf("domain %q DeadLetterSubjectPattern() = %q, want %q (tech plan §3.1 row HC_DL)",
				d.Code, got, want)
		}

		// §2.2: 版本表 schema_migrations_{code}，落在自己 schema 内。
		if got, want := d.VersionTable(), "schema_migrations_"+d.Code; got != want {
			t.Errorf("domain %q VersionTable() = %q, want %q (tech plan §2.2)", d.Code, got, want)
		}

		// §2.3/§6 + PRD 16.3: {code}_proj_{src}. The two P1 instances are named in the documents and
		// are the golden cases below; here the shape must hold for any src.
		if got, want := d.ProjectionTable("homeos"), d.Code+"_proj_homeos"; got != want {
			t.Errorf("domain %q ProjectionTable(\"homeos\") = %q, want %q (tech plan §2.3, §6)",
				d.Code, got, want)
		}

		// §1.2 仓库布局: contracts/openapi/{code}.yaml、events/{code}.yaml (relative to server/, the
		// same root MigrationDir is relative to).
		if got, want := d.OpenAPIContractPath(), "contracts/openapi/"+d.Code+".yaml"; got != want {
			t.Errorf("domain %q OpenAPIContractPath() = %q, want %q (tech plan §1.2)", d.Code, got, want)
		}
		if got, want := d.EventsContractPath(), "contracts/events/"+d.Code+".yaml"; got != want {
			t.Errorf("domain %q EventsContractPath() = %q, want %q (tech plan §1.2, §3.2)", d.Code, got, want)
		}

		// PRD 16.1 命名说明 / 16.6 / 22.2 第 6 条: {code}_{序号}_{描述}, §1.2 adds the .up.sql suffix.
		name := d.MigrationFileName(7, "desc")
		if !strings.HasPrefix(name, d.TablePrefix) {
			t.Errorf("domain %q MigrationFileName(7, \"desc\") = %q does not start with the table prefix %q: "+
				"门禁 2 reads the owner off the file name", d.Code, name, d.TablePrefix)
		}
		if !strings.HasSuffix(name, ".up.sql") {
			t.Errorf("domain %q MigrationFileName(...) = %q: §1.2 registers the golang-migrate suffix",
				d.Code, name)
		}
		if strings.ContainsAny(name, " ") {
			t.Errorf("domain %q MigrationFileName(...) = %q contains a space", d.Code, name)
		}
	}
}

func TestDerivedNamesAreTheDocumentedExamples(t *testing.T) {
	// Golden values copied out of the documents' own lines, so a derivation cannot be "consistent"
	// and still wrong: §3.1 writes HC_HOMEOS / HC_FINANCE with subjects homeos.> / finance.>, §2.3 and
	// §6 name the two P1 projection tables, §2.2 names the version table, §1.2 writes
	// migrations/{code}/{code}_0007_desc.up.sql and PRD 16.6 writes「finance_0007_...」/
	// 「purchase_0001_...」.
	homeos, _ := registry.ByCode("homeos")
	finance, _ := registry.ByCode("finance")
	purchase, _ := registry.ByCode("purchase")

	cases := []struct {
		what string
		got  string
		want string
	}{
		{"HC_HOMEOS (§3.1 流表)", homeos.StreamName(), "HC_HOMEOS"},
		{"HC_FINANCE (§3.1 流表)", finance.StreamName(), "HC_FINANCE"},
		{"HC_PURCHASE (§3.1「HC_PURCHASE … HC_GROWTH」)", purchase.StreamName(), "HC_PURCHASE"},
		{"homeos.> (§3.1 subjects 列)", homeos.StreamSubjectPattern(), "homeos.>"},
		{"finance.> (§3.1 subjects 列)", finance.StreamSubjectPattern(), "finance.>"},
		{"arch.{code}.> (§3.1 row HC_ARCHIVE)", finance.ArchiveSubjectPattern(), "arch.finance.>"},
		{"dl.{code}.> (§3.1 row HC_DL)", finance.DeadLetterSubjectPattern(), "dl.finance.>"},
		{"schema_migrations_{code} (§2.2)", finance.VersionTable(), "schema_migrations_finance"},
		{"schema_migrations_{code} (§2.2)", homeos.VersionTable(), "schema_migrations_homeos"},
		{"finance_proj_homeos (§2.3, §6)", finance.ProjectionTable("homeos"), "finance_proj_homeos"},
		{"homeos_proj_finance (§6)", homeos.ProjectionTable("finance"), "homeos_proj_finance"},
		{"/api/homeos/* (PRD 16.1 接口前缀列)", homeos.RoutePrefixGlob(), "/api/homeos/*"},
		{"/api/finance/* (PRD 16.1 接口前缀列)", finance.RoutePrefixGlob(), "/api/finance/*"},
		{"/api/finance/ (tech plan §1.1 拓扑图, PRD 卷首第 12 项, PRD 22.2 第 1 条)", finance.RoutePrefix, "/api/finance/"},
		{"contracts/openapi/{code}.yaml (§1.2)", finance.OpenAPIContractPath(), "contracts/openapi/finance.yaml"},
		{"contracts/events/{code}.yaml (§1.2, §3.2)", finance.EventsContractPath(), "contracts/events/finance.yaml"},
		{"{code}_0007_desc.up.sql (§1.2)", finance.MigrationFileName(7, "desc"), "finance_0007_desc.up.sql"},
		{"migrations/{code} (§1.2)", finance.MigrationDir, "migrations/finance"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want the documented %q", c.what, c.got, c.want)
		}
	}

	// PRD 16.6 原文的两个例子：「finance_0007_...」与「purchase_0001_...」。The 序号 is zero-padded to
	// four digits in both, which is where migrationSeqWidth's value comes from; if the file name the
	// derivation produces does not start with the documented prefix, the 迁移文件前缀 column and 门禁 1/2
	// both lose their anchor.
	if got, want := finance.MigrationFileName(7, "add_ledger"), "finance_0007_add_ledger.up.sql"; got != want {
		t.Errorf(`finance.MigrationFileName(7, "add_ledger") = %q, want %q (PRD 16.6「finance_0007_...」)`, got, want)
	}
	if got, want := purchase.MigrationFileName(1, "add_shopping_list"), "purchase_0001_add_shopping_list.up.sql"; got != want {
		t.Errorf("purchase.MigrationFileName(1, ...) = %q, want %q (PRD 16.6「purchase_0001_...」)", got, want)
	}
	// The width pads, never truncates: seq 12345 must stay intact rather than silently collide.
	if got, want := finance.MigrationFileName(12345, "x"), "finance_12345_x.up.sql"; got != want {
		t.Errorf("finance.MigrationFileName(12345, \"x\") = %q, want %q (padding must not truncate)", got, want)
	}
}
