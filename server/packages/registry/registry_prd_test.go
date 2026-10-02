package registry_test

import (
	"strings"
	"testing"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// prdRow is a verbatim transcription of one row of the table in
// docs/prd-homecube.md 16.1「系统 code」. If that table changes, this fixture must change with
// it -- and until it does, the test below fails. That is the point: the registry may not drift
// away from its requirement source silently (PRD 22.2「七处必须一致，任何一处偏离即视为架构漂移」,
// tech plan §1.3「取值形态必须逐字可追溯」).
//
// Source: docs/prd-homecube.md 16.1, columns「code」「接口前缀」「事件前缀」「表名前缀」
// 「前端分包」「迁移文件前缀」, plus the naming paragraph underneath the table which registers
// the service name svc-{code} and the schema name {code}. The 账号 hc_{code} and the migration
// directory migrations/{code} come from docs/p1-tech-plan.md §1.3 row 2 校验点 / §2.1 and
// §1.2 / §1.3 row 7 respectively, and are checked in registry_test.go because PRD 16.1 does not
// carry those two columns.
type prdRow struct {
	code             string // PRD 16.1 column「code」
	serviceName      string // PRD 16.1 命名说明:「服务名（svc-{code} 容器与进程标识）」
	apiPrefix        string // PRD 16.1 column「接口前缀」
	eventPrefix      string // PRD 16.1 column「事件前缀」
	tablePrefix      string // PRD 16.1 column「表名前缀」
	bundle           string // PRD 16.1 column「前端分包」
	migrationFilePfx string // PRD 16.1 column「迁移文件前缀」
}

// prdTable161 is docs/prd-homecube.md 16.1, copied row by row, in document order.
var prdTable161 = []prdRow{
	{"homeos", "svc-homeos", "/api/homeos/*", "homeos.", "homeos_", "pages/homeos", "homeos_{序号}_{描述}"},
	{"finance", "svc-finance", "/api/finance/*", "finance.", "finance_", "pages/finance", "finance_{序号}_{描述}"},
	{"purchase", "svc-purchase", "/api/purchase/*", "purchase.", "purchase_", "pages/purchase", "purchase_{序号}_{描述}"},
	{"diet", "svc-diet", "/api/diet/*", "diet.", "diet_", "pages/diet", "diet_{序号}_{描述}"},
	{"trip", "svc-trip", "/api/trip/*", "trip.", "trip_", "pages/trip", "trip_{序号}_{描述}"},
	{"kin", "svc-kin", "/api/kin/*", "kin.", "kin_", "pages/kin", "kin_{序号}_{描述}"},
	{"growth", "svc-growth", "/api/growth/*", "growth.", "growth_", "pages/growth", "growth_{序号}_{描述}"},
}

// TestRegistryMatchesPRD161Table compares the registry against that transcription cell by
// cell, in both directions: every documented row must be registered, and the registry must not
// register a row the document does not carry.
func TestRegistryMatchesPRD161Table(t *testing.T) {
	docs := registry.Domains()

	if len(docs) != len(prdTable161) {
		t.Fatalf("registry has %d rows but PRD 16.1 carries %d: the two lists must be the same length "+
			"(卷首第 12 项: seven codes registered in P1)", len(docs), len(prdTable161))
	}

	// Row order: PRD 16.1 row order is also the home-matrix order contract of PRD 17.1.
	for i := range prdTable161 {
		if docs[i].Code != prdTable161[i].code {
			t.Errorf("row %d: registry code is %q, PRD 16.1 row %d says %q (document order is the "+
				"registration order)", i, docs[i].Code, i, prdTable161[i].code)
		}
	}

	byCode := map[string]registry.Domain{}
	for _, d := range docs {
		byCode[d.Code] = d
	}

	cells := 0
	for _, want := range prdTable161 {
		got, ok := byCode[want.code]
		if !ok {
			t.Errorf("PRD 16.1 registers code %q but the registry does not: the domain table is missing "+
				"a documented row", want.code)
			continue
		}

		checks := []struct {
			column string
			got    string
			wantV  string
		}{
			{"服务名 svc-{code} (PRD 16.1 命名说明)", got.ServiceName, want.serviceName},
			{"接口前缀 (PRD 16.1 column「接口前缀」, compared in the document's own glob shape via RoutePrefixGlob())",
				got.RoutePrefixGlob(), want.apiPrefix},
			{"事件前缀 / subject (PRD 16.1 column「事件前缀」)", got.SubjectPrefix, want.eventPrefix},
			{"表名前缀 (PRD 16.1 column「表名前缀」)", got.TablePrefix, want.tablePrefix},
			{"前端分包 (PRD 16.1 column「前端分包」)", got.BundlePath, want.bundle},
			{"迁移文件前缀 (PRD 16.1 column「迁移文件前缀」; its {code}_ head is the「表名前缀」cell)",
				got.TablePrefix + "{序号}_{描述}", want.migrationFilePfx},
			{"迁移文件名 MigrationFileName(7,\"desc\") (same column, placeholders filled with the literals " +
				"PRD 16.6「finance_0007_...」and tech plan §1.2 {code}_0007_desc.up.sql write)",
				got.MigrationFileName(7, "desc"), docMigrationName(want.migrationFilePfx, "0007")},
		}

		for _, c := range checks {
			cells++
			if c.got != c.wantV {
				t.Errorf("domain %q dimension %s: registry has %q, docs/prd-homecube.md 16.1 has %q -- "+
					"either the code or this fixture is stale (PRD 22.2「任何一处偏离即视为架构漂移」)",
					want.code, c.column, c.got, c.wantV)
			}
		}

		// The schema name is the code itself (PRD 16.1 命名说明「schema 名（每服务一个）」),
		// restated as the DSN 校验点 in tech plan §1.3 row 2.
		cells++
		if got.Schema != want.code {
			t.Errorf("domain %q schema: registry has %q, PRD 16.1 命名说明 requires the schema name to be "+
				"the code itself (%q)", want.code, got.Schema, want.code)
		}
	}

	// Reverse direction: no extra code in the registry.
	for _, d := range docs {
		found := false
		for _, want := range prdTable161 {
			if want.code == d.Code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("registry carries code %q, which docs/prd-homecube.md 16.1 does not register: an "+
				"invented naming domain (PRD 16.1「本表是面目录」-- a new face is added to the document first)", d.Code)
		}
	}

	t.Logf("compared %d cells against docs/prd-homecube.md 16.1", cells)
}

// docMigrationName turns PRD 16.1 column「迁移文件前缀」's placeholder cell
// ({code}_{序号}_{描述}) into the concrete file name the same documents write elsewhere: PRD 16.6
// shows「finance_0007_...」and「purchase_0001_...」, tech plan §1.2 shows
// migrations/{code}/{code}_0007_desc.up.sql. Filling the placeholders with THOSE literals keeps
// this fixture on the document side of the comparison -- the registry's MigrationFileName() is
// what gets checked, not a shape this test invents. The four-digit 序号 width is only ever
// exemplified (never stated as a rule) by those two sources; if the documents settle on another
// width, this line is where the mismatch surfaces.
func docMigrationName(docCell string, seq string) string {
	return strings.ReplaceAll(docCell, "{序号}_{描述}", seq+"_desc") + ".up.sql"
}

// prdBirthRow transcribes one row of docs/prd-homecube.md 11.4「六面迭代路线」, column「出生期」,
// together with the face name so a failure points at the document line, not just at a code.
type prdBirthRow struct {
	code  string
	face  string // PRD 11.4 column「面」
	phase string // PRD 11.4 column「出生期」
}

// prdBirth114 is docs/prd-homecube.md 11.4 read off the page (财务 P1 / 采购 P2 / 饮食 P3 /
// 家人 P4 / 成长 P5 / 出行 P6), plus HomeOS, whose phase is P1 because the 底座 is delivered
// inside P1 (PRD 14.5 第 1 项, 11.3 P1 行). The same six values are restated per face in
// docs/p1-page-structure-navigation.md §3.5, which is where trip=P6 and kin=P4 come from a
// second independent source.
var prdBirth114 = []prdBirthRow{
	{"homeos", "家庭操作系统（底座）", "P1"},
	{"finance", "财务面", "P1"},
	{"purchase", "采购面", "P2"},
	{"diet", "饮食面", "P3"},
	{"trip", "出行面", "P6"},
	{"kin", "家人面", "P4"},
	{"growth", "成长面", "P5"},
}

// TestBirthPhaseMatchesPRD114 pins each domain's BirthPhase to the schedule table. A registry
// row that claims a different phase would either pre-create a service (PRD 11.7 第 1 条) or
// leave a born face without a bundle, and both are gate-4 failures.
func TestBirthPhaseMatchesPRD114(t *testing.T) {
	for _, want := range prdBirth114 {
		got, ok := registry.ByCode(want.code)
		if !ok {
			t.Errorf("PRD 11.4 schedules the %s in %s but the registry has no code %q",
				want.face, want.phase, want.code)
			continue
		}
		if got.BirthPhase != want.phase {
			t.Errorf("domain %q (%s): BirthPhase = %q, docs/prd-homecube.md 11.4 says %q",
				want.code, want.face, got.BirthPhase, want.phase)
		}
	}

	if len(prdBirth114) != len(registry.Domains()) {
		t.Errorf("PRD 11.4 fixture has %d rows but the registry has %d: a domain was added to one "+
			"list and not the other", len(prdBirth114), len(registry.Domains()))
	}
}

// TestImplementedSetMatchesPRDThreeStates checks this checkout's declaration against PRD 11.4's
// birth schedule under the three-state model (PRD 卷首第 12 项, 11.7 第 1 条, 22.2 第 1 条, tech
// plan 定版 E ★ / M ★), in the direction the documents actually write:
//
//   - 实建 ⇒ BirthPhase <= CurrentPhase. 禁止提前建 (11.7 第 1 条「业务服务、schema、迁移序列与前端
//     分包一律不得提前创建」). This is an implication, NOT an equality on the phase number: a face
//     born in an earlier phase is still built, and pinning == here would make a P2 bump declare
//     homeos and finance illegal.
//   - BirthPhase == CurrentPhase ⇒ 实建. 当期新增出生 is this phase's delivery and its 期末验收对象
//     (定版 M ★, PRD 14.5 第 1 项); leaving such a row un-built means a face scheduled for the
//     phase has no service.
//   - BirthPhase > CurrentPhase ⇒ 不得实建 (未出生 state, PRD 卷首第 12 项「其余五个服务在各自出生期
//     创建」).
//
// Both sets are derived from the document rows rather than from the registry's own BirthPhase
// cells, so an unnoticed phase bump shows up here instead of in production. In P1 the expected
// 实建 set is homeos + finance, which is PRD 卷首第 12 项 / 22.4 的 the same pair.
func TestImplementedSetMatchesPRDThreeStates(t *testing.T) {
	// Rows the document schedules at or before / exactly at the current phase.
	bornByNow := map[string]bool{}
	newThisPhase := map[string]bool{}
	for _, row := range prdBirth114 {
		if phaseAtOrBefore(row.phase, registry.CurrentPhase) {
			bornByNow[row.code] = true
		}
		if row.phase == registry.CurrentPhase {
			newThisPhase[row.code] = true
		}
	}

	declared := map[string]bool{}
	for _, d := range registry.Implemented() {
		declared[d.Code] = true
		if !bornByNow[d.Code] {
			t.Errorf("domain %q is marked implemented but PRD 11.4 does not schedule it at or before %s: "+
				"服务随面出生 forbids building a face before its birth phase (PRD 11.7 第 1 条, 定版 E ★)",
				d.Code, registry.CurrentPhase)
		}
		for _, u := range registry.Unborn() {
			if u.Code == d.Code {
				t.Errorf("domain %q is in both Implemented() and Unborn(): CI would both require and forbid "+
					"its directories", d.Code)
			}
		}
	}

	for code := range bornByNow {
		if !declared[code] {
			// Only a row of THIS phase can fail this way without being a filesystem defect: an
			// earlier-phase row that is not built means a born face lost its service.
			t.Errorf("domain %q is scheduled at or before %s by PRD 11.4 but is not marked implemented: "+
				"已出生 ⇒ 实建 (§1.3 row 6「已出生业务域 = registry 实建域」, PRD 14.5 第 1 项)",
				code, registry.CurrentPhase)
		}
	}
	for code := range newThisPhase {
		if !declared[code] {
			t.Errorf("domain %q is born in the current phase %s (PRD 11.4) but is not implemented: 当期新增出生 "+
				"是本期交付物与期末验收对象 (定版 M ★)", code, registry.CurrentPhase)
		}
	}
	for code := range declared {
		if !bornByNow[code] {
			t.Errorf("domain %q is marked implemented although PRD 11.4 schedules it later than %s",
				code, registry.CurrentPhase)
		}
	}

	// The 未出生 half of the model, read off the document: every scheduled-later row must be in
	// Unborn() and no row of the current or an earlier phase may be.
	unborn := map[string]bool{}
	for _, d := range registry.Unborn() {
		unborn[d.Code] = true
	}
	for _, row := range prdBirth114 {
		if phaseAtOrBefore(row.phase, registry.CurrentPhase) && unborn[row.code] {
			t.Errorf("domain %q (PRD 11.4 phase %s) is reported Unborn() at %s: 已出生 rows must not be in the "+
				"absence-assertion set (PRD 卷首第 12 项, 11.7 第 1 条)", row.code, row.phase, registry.CurrentPhase)
		}
		if !phaseAtOrBefore(row.phase, registry.CurrentPhase) && !unborn[row.code] {
			t.Errorf("domain %q (PRD 11.4 phase %s) is missing from Unborn() at %s: its directories, bundle and "+
				"stream would escape the 「不该存在」 assertions", row.code, row.phase, registry.CurrentPhase)
		}
	}
}

// docPhaseOrder is PRD 11.3 / 卷首第 7 项's 期划分 sequence (P1 底座+财务 → P2 采购 → P3 饮食 →
// P4 家人 → P5 成长 → P6 出行), used here as the test-side ordering oracle so the assertions below
// compare the registry against the document rather than against the registry's own comparison
// helper.
var docPhaseOrder = []string{"P1", "P2", "P3", "P4", "P5", "P6"}

// phaseAtOrBefore orders two documented phase labels by PRD 11.3's sequence. Fatal-worthy input is
// impossible here (both sides come from fixtures in this file), so callers get false and the
// assertion that depends on it fails loudly.
func phaseAtOrBefore(a, b string) bool {
	ra, okA := phaseRank(a)
	rb, okB := phaseRank(b)
	return okA && okB && ra <= rb
}

func phaseRank(phase string) (int, bool) {
	for i, p := range docPhaseOrder {
		if p == phase {
			return i + 1, true
		}
	}
	return 0, false
}

// TestThreeStateModelAtDocumentPhases is the reason the phase predicates take an argument: with
// CurrentPhase pinned to P1 every one of the three readings below coincides, so a model that was
// wrong in the way 实建 ⟺ (BirthPhase == 当期) was wrong would still pass here. At P2 and P6 the
// sets must be what PRD 11.4 alone implies -- 已出生 is cumulative, 当期新增出生 is not, and the
// bundle set is「出生期 ≤ 当期」− homeos (PRD 22.5 第 4 道, navigation doc §2.4 第 2 查,
// tech plan §1.3 row 6).
func TestThreeStateModelAtDocumentPhases(t *testing.T) {
	cases := []struct {
		phase   string
		born    []string // 已出生: BirthPhase <= phase, registration order
		unborn  []string // 未出生: BirthPhase > phase
		newborn []string // 当期新增出生: BirthPhase == phase
		bundles []string // 分包集合: born minus homeos
	}{
		{
			phase:   "P1",
			born:    []string{"homeos", "finance"},
			unborn:  []string{"purchase", "diet", "trip", "kin", "growth"},
			newborn: []string{"homeos", "finance"},
			bundles: []string{"finance"},
		},
		{
			phase:   "P2",
			born:    []string{"homeos", "finance", "purchase"},
			unborn:  []string{"diet", "trip", "kin", "growth"},
			newborn: []string{"purchase"},
			bundles: []string{"finance", "purchase"},
		},
		{
			// P4 adds kin while diet (P3) is already born: 已出生 keeps every earlier row, which is
			// precisely what the retired == reading used to drop.
			phase:   "P4",
			born:    []string{"homeos", "finance", "purchase", "diet", "kin"},
			unborn:  []string{"trip", "growth"},
			newborn: []string{"kin"},
			bundles: []string{"finance", "purchase", "diet", "kin"},
		},
		{
			// P6: trip (registered fourth, born last) is the newborn; growth P5 is born but not new.
			phase:   "P6",
			born:    []string{"homeos", "finance", "purchase", "diet", "trip", "kin", "growth"},
			unborn:  []string{},
			newborn: []string{"trip"},
			bundles: []string{"finance", "purchase", "diet", "trip", "kin", "growth"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			bornDomains := registry.ImplementedAt(tc.phase)
			unbornDomains := registry.UnbornSetAt(tc.phase)
			newbornDomains := registry.NewbornSetAt(tc.phase)

			assertCodes(t, "ImplementedAt("+tc.phase+")", domainCodes(bornDomains), tc.born)
			assertCodes(t, "UnbornSetAt("+tc.phase+")", domainCodes(unbornDomains), tc.unborn)
			assertCodes(t, "NewbornSetAt("+tc.phase+")", domainCodes(newbornDomains), tc.newborn)
			assertCodes(t, "BundleSetAt("+tc.phase+")", registry.BundleSetAt(tc.phase), tc.bundles)

			// The two halves are a partition of the table at every phase, and 当期新增出生 is a
			// subset of 已出生 -- the three states do not overlap and do not leave a row out.
			born := map[string]bool{}
			for _, d := range bornDomains {
				born[d.Code] = true
			}
			for _, d := range unbornDomains {
				if born[d.Code] {
					t.Errorf("%s: %q is in both ImplementedAt and UnbornSetAt", tc.phase, d.Code)
				}
			}
			if len(born)+len(unbornDomains) != len(registry.Domains()) {
				t.Errorf("%s: 已出生 %d + 未出生 %d != 登记 %d 域: the three-state model lost a row",
					tc.phase, len(born), len(unbornDomains), len(registry.Domains()))
			}
			for _, d := range newbornDomains {
				if !born[d.Code] {
					t.Errorf("%s: %q is 当期新增出生 but not 已出生 -- contradictory states", tc.phase, d.Code)
				}
			}

			// 期末验收对象 (定版 M ★): exactly one face is delivered by a phase, and homeos is only
			// in P1's newborn set because the 底座 ships with P1 (PRD 14.5 第 1 项).
			faces := []string{}
			for _, d := range newbornDomains {
				if d.Code != registry.HomeosCode {
					faces = append(faces, d.Code)
				}
			}
			if len(faces) != 1 {
				t.Errorf("%s: 当期新增出生 faces = %v, want exactly one (PRD 卷首第 7 项「每期只交付一个面」)",
					tc.phase, faces)
			}

			// §1.3 row 6's equivalence holds at every phase, not just where the declared flags
			// happen to agree: 分包集合 = 已出生业务域 = 实建域 − homeos.
			assertCodes(t, "BundleSetAt vs ImplementedAt minus homeos ("+tc.phase+")",
				registry.BundleSetAt(tc.phase), minusHomeos(domainCodes(bornDomains)))

			// homeos is the main package, never a subPackage (§1.3 row 6 校验点, PRD 17.7 第 4 条).
			for _, c := range registry.BundleSetAt(tc.phase) {
				if c == registry.HomeosCode {
					t.Errorf("%s: BundleSetAt contains homeos", tc.phase)
				}
			}

			// The per-domain predicates agree with the set functions at the same phase.
			newborn := map[string]bool{}
			for _, d := range newbornDomains {
				newborn[d.Code] = true
			}
			for _, d := range registry.Domains() {
				if got := d.BornAt(tc.phase); got != born[d.Code] {
					t.Errorf("domain %q BornAt(%q) = %v, but ImplementedAt(%q) puts it the other way",
						d.Code, tc.phase, got, tc.phase)
				}
				if got := d.NewlyBornAt(tc.phase); got != newborn[d.Code] {
					t.Errorf("domain %q NewlyBornAt(%q) = %v, but NewbornSetAt(%q) puts it the other way",
						d.Code, tc.phase, got, tc.phase)
				}
			}
		})
	}
}

// domainCodes projects a domain slice onto its codes, keeping the slice's order.
func domainCodes(ds []registry.Domain) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

// minusHomeos drops the base domain from a code list, keeping order.
func minusHomeos(cs []string) []string {
	out := []string{}
	for _, c := range cs {
		if c != registry.HomeosCode {
			out = append(out, c)
		}
	}
	return out
}

func assertCodes(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
