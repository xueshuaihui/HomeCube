// Package registry holds the one and only domain table of HomeCube:
// 「一个 code，七处生效」 (docs/p1-tech-plan.md §1.3) and
// 「code 是全文唯一命名权威」 (docs/prd-homecube.md 16.1).
//
// # Why this file exists
//
// §1.3 designates this table as 唯一真源 and says registry.Domains() is shared by
// 服务启动、迁移工具、前端构建脚本与 CI. The table is therefore written exactly once, in
// table below, and every consumer takes it either through the Go API (Domains /
// Implemented / Unborn / ByCode / BundleSet / ImplementedAt) or through the export command in
// ./cmd/registryd (--format=json full table, --format=codes code list, --format=bundles bundle
// domains, --format=meta currentPhase + implemented + unborn + bundles) for the non-Go consumers
// (pages.json 三查, nginx/compose generation, gate scripts).
//
// What "唯一真源" covers, stated precisely: no Go code may spell a code or a derived name a
// second time, and a non-Go consumer must read it from registryd rather than re-derive it. This
// claim is about Go sources; the repository does hold two non-Go 抄本 of the code list today, and
// neither is reachable from this table, so renaming a cell here does not propagate to them:
//
//   - the root Makefile hard-codes the P1 pair: the migration-directory loop at Makefile:74 lists
//     server/migrations/{homeos,finance}, and the two dev targets carry the cmd paths
//     (Makefile:187-188, 207-208). Their own headers point the skeleton at S1-C (Makefile:77,
//     174-175), so the values live on until that card replaces them with a registryd call.
//   - docs/p1-dev-brief.md:74 (「文档校验器」第 2 条) spells all seven codes in a route regex
//     -- a document-side validator, not a build input.
//
// registry_prd_test.go pins this table against PRD 16.1 so drift between the table and its
// requirement source cannot go silent; the two 抄本 above are outside this package's reach and are
// reported as a residue rather than asserted here.
//
// # Clause map for every field (nothing here is invented)
//
//   - Code          -- PRD 16.1 table column「code」; PRD 16.1 命名说明「code 是全文唯一命名权威」.
//   - ServiceName   -- naming class 1 svc-{code}: PRD 16.1 命名说明, 22.2 第 1 条, §1.3 row 1
//     (校验点：镜像名、Compose 服务名、容器名).
//   - Schema        -- naming class 2 {code}: PRD 16.1 命名说明, §1.3 row 2, §2.1.
//   - DBAccount     -- §1.3 row 2 校验点「账号名 hc_{code}」, §2.1「CREATE ROLE hc_finance」, PRD 22.2 第 2 条.
//   - RoutePrefix   -- naming class 3 /api/{code}/*: PRD 16.1 column「接口前缀」, 22.2 第 8 条,
//     §1.3 row 3. The cell stores the prefix form the documents also use (卷首第 12 项, 22.2 第 1
//     条, §1.1); RoutePrefixGlob() gives the class' glob shape. Domain's doc comment lists both.
//   - SubjectPrefix -- naming class 4 {code}.: PRD 16.1 column「事件前缀」, §1.3 row 4, §3.1.
//   - TablePrefix   -- naming class 5 {code}_: PRD 16.1 columns「表名前缀」AND「迁移文件前缀」
//     ({code}_{序号}_{描述}, PRD 16.1 命名说明, 16.6, 22.2 第 6 条, §2.2) -- the cell stores the
//     {code}_ prefix once, and MigrationFileName() derives the whole documented file name out of
//     it, so the two PRD columns are two shapes of one stored value rather than two dimensions.
//     §1.3 row 5; registry_prd_test.go checks both columns against the PRD transcription.
//   - BundlePath    -- naming class 6 pages/{code}: PRD 16.1 column「前端分包」, 17.7 第 4 条, §1.3 row 6.
//   - MigrationDir  -- naming class 7 migrations/{code}/: §1.2 仓库布局, §1.3 row 7, §2.2.
//   - BirthPhase    -- PRD 11.4 六面迭代路线 (财务 P1 / 采购 P2 / 饮食 P3 / 家人 P4 / 成长 P5 /
//     出行 P6) and the PRD 卷首 term row「出生期」; navigation doc §3.5 restates the same six
//     values. HomeOS is the 底座 delivered with P1 (PRD 14.5 第 1 项, 11.3 P1 行).
//   - Implemented   -- 当期是否实建: PRD 卷首第 12 项, 22.2 第 1 条, 22.4, tech plan 定版 E ★ --
//     P1 实建 svc-homeos + svc-finance, 其余五域「只以 registry 条目存在」.
//
// # Derived naming classes exposed as methods (also nothing invented)
//
// Each of these is a naming form the documents spell out of a code but that is NOT one of
// PRD 16.1's seven naming classes, so it is computed from a stored cell instead of stored:
//
//   - RoutePrefixGlob()     -- /api/{code}/*, the form PRD 16.1 column「接口前缀」writes.
//   - StreamName()          -- HC_{CODE} uppercase: §3.1 流表 registers HC_HOMEOS / HC_FINANCE
//     literally and names the other five as「HC_PURCHASE … HC_GROWTH（5 条）」.
//   - ArchiveSubjectPattern -- arch.{code}.>, §3.1 row HC_ARCHIVE.
//   - DeadLetterSubjectPattern() -- dl.{code}.>, §3.1 row HC_DL.
//   - VersionTable()        -- schema_migrations_{code}, §2.2「golang-migrate 一个实例一条序列、
//     各自版本表 schema_migrations_{code}」.
//   - ProjectionTable(src)  -- {code}_proj_{src}, §2.3 底座侧表表 + §6; PRD 16.3 writes
//     「{code}_proj_{来源域}」.
//   - MigrationFileName()   -- {code}_{序号}_{描述}.up.sql, PRD 16.1 命名说明 / 16.6 / 22.2 第 6 条
//     for the stem and §1.2 仓库布局 ({code}_0007_desc.up.sql) for the golang-migrate suffix.
//   - OpenAPIContractPath() -- contracts/openapi/{code}.yaml, §1.2 仓库布局 openapi/{code}.yaml.
//   - EventsContractPath()  -- contracts/events/{code}.yaml, §1.2 and §3.2
//     「server/contracts/events/{code}.yaml」.
//   - StreamSubjectPattern()-- {code}.>, §3.1 流表 subjects 列.
//   - DSNSearchPath()       -- search_path={code}, §1.3 row 2 校验点 and §2.1 的 DSN 示例.
//
// Deliberately NOT implemented: the JetStream consumer name of §3.4. That row states the pattern
// {consumerCode}-{event_type} and gives the example homeos-finance-transaction-created, but §3.1
// names events in dot form («subject 命名即事件名：{code}.{object}.{action}，小写点分»), so the
// pattern alone would produce homeos-finance.transaction.created -- and §3.4's own dead-letter
// subject dl.{consumerCode}.{event_type} needs those dots to survive. Pattern, example and the
// dead-letter row therefore cannot all hold at once, and no derivation follows from the text
// rather than being read into it. Inventing one here would put an undocumented naming rule into
// the 唯一真源, so this package exposes no ConsumerName() and the conflict is reported to the card
// owner instead.
//
// # Fields deliberately NOT stored
//
// Ports, environment key names and image names get no field here. PRD 16.1 registers exactly
// seven naming classes and PRD 卷首第 12 项 enumerates the same seven (服务名、schema、路由、subject、
// 表前缀、分包、迁移目录); a port, an env key and an image name are not among them. Ports belong to
// deploy/env.local.example (tech plan §十三), images and container names to the S1-D compose
// file (they are derived from ServiceName there). JetStream stream names (HC_{CODE}), the
// archive/dead-letter subjects, the version table, the projection table, the migration file name
// and the two contract paths are likewise not stored as fields -- each is a derivation of a
// stored cell (§3.1 / §2.2 / §2.3 / PRD 16.1 命名说明 / §1.2) and every one of them IS exposed as
// the method named in the clause map above, so the table keeps one value per naming class.
//
// # Birth phase is a three-state model, not an equality
//
// PRD 卷首第 12 项 says the seven codes' naming domains are registered once in P1 while「其余五个
// 服务在各自出生期创建」, and 11.7 第 1 条 / 22.2 第 1 条 state the same discipline as「服务随面
// 出生」. Read against a phase, each row is in exactly one of three states:
//
//   - 未出生 -- BirthPhase > phase (BornAt false): no service directory, no schema, no migration
//     file, no bundle, no stream may exist yet (PRD 卷首第 12 项, 11.7 第 1 条, tech plan §1.2 and
//     定版 E ★). Unborn() / UnbornSetAt() are this set.
//   - 已出生 -- BirthPhase <= phase (Born true): the service, schema, sequence and bundle exist and
//     stay in place in every later phase; BundleSet() and ImplementedAt() derive from this
//     predicate (tech plan §1.3 row 6「分包集合 = 已出生业务域」, PRD 22.5 第 4 道「出生期 ≤ 当期」).
//   - 当期新增出生 -- BirthPhase == phase (NewlyBornAt): this phase's delivery, and the object its
//     end-of-phase acceptance judges (定版 M ★「期末验收对象 = 当期那一个面」, PRD 14.5 第 1 项).
//
// The two assertions that follow are not symmetric and must not be collapsed into one equality on
// the phase number: 禁止提前建 is 实建 ⇒ BirthPhase <= CurrentPhase (PRD 11.7 第 1 条), while the
// back direction only binds the BirthPhase == CurrentPhase slice (当期新增出生必须已实建). Because a
// face born in an earlier phase is not un-built afterwards, the declared Implemented flags and the
// phase-derived born set agree as whole sets at any phase -- which is exactly the equivalence
// §1.3 row 6 writes as「已出生业务域 = registry 实建域 − homeos」. The phase is therefore an
// argument (BundleSetAt / ImplementedAt / UnbornSetAt / NewbornSetAt / BornAt / NewlyBornAt) and
// BundleSet() / Implemented() / Unborn() delegate to CurrentPhase, so registry_prd_test.go can
// assert the model at P2 and P6 instead of only where the two readings coincide in P1.
//
// # Order is a contract
//
// Domains() returns PRD 16.1 row order, and the home-screen service matrix renders in
// 「registry 登记顺序」: PRD 17.1「顺序恒为 registry 登记顺序（当前为 财务/采购/饮食/出行/家人/成长）」,
// PRD 17.2 matrix paragraph (「顺序恒为 registry 登记顺序」), PRD 14.5 第 8 项, tech plan §九
// (「faces[] 只含已挂载面、顺序按 registry 登记序」) and navigation doc §1.4 and §3.2 第 1 条.
// The order below is homeos then 财务/采购/饮食/出行/家人/成长 -- note that BirthPhase is NOT the
// sort key (trip is P6 yet sits fourth, ahead of kin P4 and growth P5). Reordering this slice
// reorders the home screen, so registry_test.go pins it.
package registry

import (
	"fmt"
	"strings"
)

// Domain is one row of the domain table: the code, its seven naming dimensions,
// the database account of the schema dimension, and the birth registration.
//
// Shape of the stored cells, stated exactly: SubjectPrefix ("finance."), TablePrefix
// ("finance_"), BundlePath ("pages/finance"), MigrationDir ("migrations/finance"), Schema,
// DBAccount and ServiceName are stored in the literal form the documents write them, so a
// comparison against PRD 16.1 needs no reformatting of those seven. RoutePrefix is the exception
// because the requirement documents themselves write the same route in two shapes:
//
//   - GLOB /api/{code}/* -- PRD 16.1 命名说明「接口路由 /api/{code}/*」and its table column
//     「接口前缀」, PRD 22.2 第 8 条「路由必须分域：/api/{code}/*」, PRD 22.5 第 4 道「检查路由是否
//     全部落在 /api/{code}/*」, PRD 17.7 第 5 条, tech plan §1.3 row 3 取值.
//   - PREFIX /api/{code}/ -- PRD 卷首第 12 项「按 /api/{code}/ 前缀转发」, PRD 22.2 第 1 条「Nginx
//     按 /api/{code}/ 前缀反代」, tech plan §1.1 拓扑图「/api/homeos/  /api/finance/」and its
//     「未出生面的 /api/{code}/ 前缀由 Nginx 直接返回「即将上线」」.
//
// The cell stores the normalized PREFIX form, because a prefix is what a Gin route group registers
// and what an Nginx location concatenates onto; RoutePrefixGlob() yields the glob shape PRD 16.1's
// table writes. registry_prd_test.go therefore compares RoutePrefixGlob() against the PRD cell
// verbatim -- it no longer trims the document text to make a comparison pass -- and registry_test.go
// pins the stored prefix shape.
//
// Which value gate 4's AST check takes: RoutePrefix. PRD 22.5 第 4 道 asks whether every route
// falls inside the domain's own route domain and tech plan §1.3 row 3 校验点 names the check
// 「Gin 路由组 AST 检查」 -- a route group is declared with a prefix, not with a glob, so the AST
// of a registered group is compared against RoutePrefix. RoutePrefixGlob() exists for text that has
// to match the document (and for an Nginx location comment), not for route registration.
//
// registry_test.go enforces the shape of each cell against its code; registry_prd_test.go
// compares each cell against a transcription of the PRD 16.1 table.
type Domain struct {
	// Code is the naming authority for the whole stack (PRD 16.1).
	Code string `json:"code"`

	// ServiceName is svc-{code}: image name, Compose service name, container name (§1.3 row 1).
	ServiceName string `json:"serviceName"`

	// Schema is this domain's own schema; the service DSN carries search_path={code}
	// (§1.3 row 2 校验点, §2.1「每个服务只用自己那串 DSN」).
	Schema string `json:"schema"`

	// DBAccount is hc_{code}, the domain's dedicated Postgres role whose grants ARE the
	// isolation boundary (§1.3 row 2 校验点, §2.1, PRD 22.2 第 2 条).
	DBAccount string `json:"dbAccount"`

	// RoutePrefix is /api/{code}/ in the normalized PREFIX shape: the service's Gin route group and
	// the prefix Nginx reverse-proxies on (PRD 卷首第 12 项「按 /api/{code}/ 前缀转发」, PRD 22.2 第 1
	// 条, tech plan §1.1 拓扑图). The route-domain rules are written in the GLOB shape /api/{code}/*
	// (PRD 16.1 column「接口前缀」, 22.2 第 8 条「路由必须分域」, 22.5 第 4 道「检查路由是否全部落在
	// /api/{code}/*」); RoutePrefixGlob() returns that shape, and the Domain doc comment states which
	// of the two gate 4's AST check compares against. For the five unborn prefixes Nginx answers
	//「即将上线」directly and no container sits behind them -- tech plan §1.1; the wording follows
	// PRD 17.2 的四词分工（定版 ㉙：「即将上线」= 这个面还没出生）, a different path from「未启用」=
	// 本家庭未挂载 (PRD 17.8).
	RoutePrefix string `json:"routePrefix"`

	// SubjectPrefix is {code}.: the leading segment of every event this domain publishes, and
	// the source-stream subject pattern is this value plus ">" (PRD 16.1「事件前缀」, §1.3 row 4,
	// §3.1; event names are {code}.{object}.{action}).
	SubjectPrefix string `json:"subjectPrefix"`

	// TablePrefix is {code}_ kept inside the schema as the deliberate double insurance of
	// PRD 16.1. The same cell is the stem of the migration file name, and MigrationFileName()
	// spells that documented name out of it (PRD 16.1 命名说明, 16.6, 22.2 第 6 条, §2.2): the two
	// PRD 16.1 columns「表名前缀」and「迁移文件前缀」are two shapes of one stored value, so this cell
	// is checked against the first column verbatim and against the second through the derivation
	// (registry_prd_test.go compares both). Gate 2 fails on a table or a migration file whose
	// prefix does not match the service/schema it lives in -- including「提前替还没出生的面建表」
	// (§2.1).
	TablePrefix string `json:"tablePrefix"`

	// BundlePath is pages/{code}, the uni-app subPackage root (PRD 16.1「前端分包」, 17.7 第 4 条,
	// §1.3 row 6). For homeos this root is the main package rather than a subPackage, which is
	// why BundleSet() subtracts it (navigation doc §2.4 第 2 查).
	BundlePath string `json:"bundlePath"`

	// MigrationDir is migrations/{code}, this domain's independent sequence root (§1.2 仓库布局,
	// §1.3 row 7, §2.2「每服务一条独立序列」, version table schema_migrations_{code}).
	MigrationDir string `json:"migrationDir"`

	// BirthPhase is the phase in which this domain's service, schema, migration sequence and
	// bundle are created: P1-P6 from PRD 11.4 (HomeOS = P1 as part of the 底座, PRD 14.5 第 1 项),
	// restated per face in navigation doc §3.5.
	BirthPhase string `json:"birthPhase"`

	// Implemented reports whether this domain's service is really built in the current phase
	// (P1: homeos + finance -- PRD 卷首第 12 项, 22.2 第 1 条, tech plan 定版 E ★). It is the
	// DECLARED half of the 已出生 state: true requires BirthPhase <= CurrentPhase (禁止提前建,
	// PRD 11.7 第 1 条), and every row with BirthPhase == CurrentPhase requires true (期末验收对象,
	// 定版 M ★); both directions are asserted in registry_prd_test.go, not just documented here.
	// false means "registry entry only", and CI asserts ABSENCE over that set:「registry 里没有实建
	// 服务的那五个域，任何目录、迁移文件、分包、subject 配置出现即门禁 2/4 失败」(§1.2, §1.3,
	// PRD 11.7 第 1 条).
	Implemented bool `json:"implemented"`
}

// HomeosCode is the base domain's code. It is exported because two documented consumers must
// subtract it by name instead of re-spelling a code: the bundle set (§1.3「分包集合 = 已出生业务域
// = registry 实建域 − homeos」, navigation doc §2.4 第 2 查, PRD 22.5 第 4 道) and the home
// matrix, which lists faces only -- 首页自身不进矩阵 (PRD 17.1, navigation doc §3.2 第 1 条).
const HomeosCode = "homeos"

// CurrentPhase names the phase this checkout delivers (P1 = 底座 + 财务面整面, tech plan 卷首
// 「范围」/「当期形状」and §十一 S1 row). PRD 22.5 第 4 道 and navigation doc §2.4 第 2 查 define the
// bundle set as「出生期 ≤ 当期」的域集合 minus homeos, so the comparison needs this input; the
// per-face values come from PRD 11.4.
//
// It is the argument the no-suffix API passes (BundleSet / Implemented / Unborn), never a value a
// consumer re-spells: the phase predicates take the phase as an explicit parameter
// (BundleSetAt / ImplementedAt / UnbornSetAt / NewbornSetAt, Domain.BornAt / Domain.NewlyBornAt)
// so the birth-phase model can be asserted at P2 and P6 rather than only at the one phase where
// every reading happens to agree. Bumping this constant to "P2" is a P2 开工动作; it must come
// with marking purchase implemented, and the invariants below then hold again -- they do not
// require re-editing any other line of this package.
const CurrentPhase = "P1"

// table is the domain table: §1.3's 唯一真源. Rows follow PRD 16.1 order because Domains()
// order is a contract (see the package doc). This is the only place a code is spelled out.
var table = []Domain{
	{
		Code:          "homeos",
		ServiceName:   "svc-homeos",
		Schema:        "homeos",
		DBAccount:     "hc_homeos",
		RoutePrefix:   "/api/homeos/",
		SubjectPrefix: "homeos.",
		TablePrefix:   "homeos_",
		BundlePath:    "pages/homeos",
		MigrationDir:  "migrations/homeos",
		BirthPhase:    "P1",
		Implemented:   true,
	},
	{
		Code:          "finance",
		ServiceName:   "svc-finance",
		Schema:        "finance",
		DBAccount:     "hc_finance",
		RoutePrefix:   "/api/finance/",
		SubjectPrefix: "finance.",
		TablePrefix:   "finance_",
		BundlePath:    "pages/finance",
		MigrationDir:  "migrations/finance",
		BirthPhase:    "P1",
		Implemented:   true,
	},
	{
		Code:          "purchase",
		ServiceName:   "svc-purchase",
		Schema:        "purchase",
		DBAccount:     "hc_purchase",
		RoutePrefix:   "/api/purchase/",
		SubjectPrefix: "purchase.",
		TablePrefix:   "purchase_",
		BundlePath:    "pages/purchase",
		MigrationDir:  "migrations/purchase",
		BirthPhase:    "P2",
		Implemented:   false,
	},
	{
		Code:          "diet",
		ServiceName:   "svc-diet",
		Schema:        "diet",
		DBAccount:     "hc_diet",
		RoutePrefix:   "/api/diet/",
		SubjectPrefix: "diet.",
		TablePrefix:   "diet_",
		BundlePath:    "pages/diet",
		MigrationDir:  "migrations/diet",
		BirthPhase:    "P3",
		Implemented:   false,
	},
	{
		// Registered fourth because the home matrix order of PRD 17.1 is
		// 财务/采购/饮食/出行/家人/成长; its BirthPhase is P6 (PRD 11.4 出行面 row, navigation doc
		// §3.5). Registration order and birth order differ on purpose -- do not "fix" it.
		Code:          "trip",
		ServiceName:   "svc-trip",
		Schema:        "trip",
		DBAccount:     "hc_trip",
		RoutePrefix:   "/api/trip/",
		SubjectPrefix: "trip.",
		TablePrefix:   "trip_",
		BundlePath:    "pages/trip",
		MigrationDir:  "migrations/trip",
		BirthPhase:    "P6",
		Implemented:   false,
	},
	{
		Code:          "kin",
		ServiceName:   "svc-kin",
		Schema:        "kin",
		DBAccount:     "hc_kin",
		RoutePrefix:   "/api/kin/",
		SubjectPrefix: "kin.",
		TablePrefix:   "kin_",
		BundlePath:    "pages/kin",
		MigrationDir:  "migrations/kin",
		BirthPhase:    "P4",
		Implemented:   false,
	},
	{
		Code:          "growth",
		ServiceName:   "svc-growth",
		Schema:        "growth",
		DBAccount:     "hc_growth",
		RoutePrefix:   "/api/growth/",
		SubjectPrefix: "growth.",
		TablePrefix:   "growth_",
		BundlePath:    "pages/growth",
		MigrationDir:  "migrations/growth",
		BirthPhase:    "P5",
		Implemented:   false,
	},
}

// Domains returns the whole table in registration order. The slice is a copy so no consumer
// can corrupt the single source; the ORDER is the contract behind the home-screen matrix
// (PRD 17.1, 17.2, navigation doc §3.2) and must not be re-sorted by callers or by phase.
func Domains() []Domain {
	out := make([]Domain, len(table))
	copy(out, table)
	return out
}

// Implemented returns the domains DECLARED as really built in this checkout (P1: homeos +
// finance -- PRD 卷首第 12 项, 22.2 第 1 条, tech plan 定版 E ★). Service startup, the migration
// tool and the compose/nginx generation enumerate this set; §1.3:「只有登记为实建的域才要求文件存
// 在」. It is the flag, so it says nothing about any phase other than this checkout's; use
// ImplementedAt(phase) for the phase-derived set (the two must be equal at CurrentPhase, which
// registry_prd_test.go asserts).
func Implemented() []Domain {
	out := []Domain{}
	for _, d := range table {
		if d.Implemented {
			out = append(out, d)
		}
	}
	return out
}

// ImplementedAt returns the domains whose service, schema, migration sequence and bundle must
// already exist by the given phase, derived from BirthPhase alone: 已出生 = BirthPhase <= phase
// (PRD 卷首第 12 项「其余五个服务在各自出生期创建」, 22.5 第 4 道「出生期 ≤ 当期」). Includes
// homeos, which is a 底座 rather than a face (PRD 14.5 第 1 项); subtract it for the bundle set
// (BundleSetAt) and for the home matrix (首页自身不进矩阵, PRD 17.1).
//
// An unparsable phase yields the empty set -- fail closed, so a typo in CurrentPhase cannot
// authorise directories (see phaseLE).
func ImplementedAt(phase string) []Domain {
	out := []Domain{}
	for _, d := range table {
		if d.BornAt(phase) {
			out = append(out, d)
		}
	}
	return out
}

// UnbornSetAt returns the domains that are 未出生 in the given phase, BirthPhase > phase (PRD 卷首
// 第 12 项, 11.7 第 1 条, tech plan 定版 E ★). This is the complement of ImplementedAt(phase) over
// the registered rows: the two sets together are Domains() and share no element.
func UnbornSetAt(phase string) []Domain {
	out := []Domain{}
	for _, d := range table {
		if !d.BornAt(phase) {
			out = append(out, d)
		}
	}
	return out
}

// NewbornSetAt returns the domains born exactly in the given phase, BirthPhase == phase: the
// faces that phase delivers and whose 期末验收 judges them (tech plan 定版 M ★「期末验收对象 = 当期
// 那一个面」, PRD 11.7 第 1 条, 14.5 第 1 项). In P1 this is homeos + finance (the 底座 ships with
// P1, PRD 14.5 第 1 项); in P2 it is purchase alone.
func NewbornSetAt(phase string) []Domain {
	out := []Domain{}
	for _, d := range table {
		if d.NewlyBornAt(phase) {
			out = append(out, d)
		}
	}
	return out
}

// Unborn returns the 未出生 rows of this checkout, BirthPhase > CurrentPhase: the domains that must
// not have a service directory, schema, migration file, bundle or stream yet (PRD 11.7 第 1 条,
// 卷首第 12 项, tech plan §1.2, §1.3, 定版 E ★). CI asserts absence over exactly this set --
// registry_fs_test.go does it against the filesystem so the rule cannot rot into a comment.
//
// This is the phase-derived set, not "the ones with Implemented == false": the two coincide only
// while the declared flags agree with the phase model, and registry_test.go's
// TestUnbornIsComplementOfImplemented is what keeps that agreement from rotting. A row declared
// implemented before its birth phase is therefore caught by the invariant (实建 ⇒ 已出生) rather
// than disappearing from the absence assertions.
func Unborn() []Domain { return UnbornSetAt(CurrentPhase) }

// ByCode looks a domain up by its code. ok is false for an unregistered code, which is the
// hook a service uses to refuse startup and rclient uses to reject a target with no real
// service (§3.5「Target 写成未出生域时启动即失败」).
func ByCode(code string) (Domain, bool) {
	for _, d := range table {
		if d.Code == code {
			return d, true
		}
	}
	return Domain{}, false
}

// BundleSet returns this checkout's bundle set: the codes whose pages/{code} roots must appear as
// subPackages entries in web's pages.json. It delegates to BundleSetAt(CurrentPhase); see that
// function for the criterion.
//
// The tests pinning it are TestBundleSetAtP1IsFinance (the P1 value),
// TestBundleSetAgreesWithImplementedMinusHomeos (the §1.3 row 6 equivalence at this phase) and
// TestThreeStateModelAtDocumentPhases (the same formula evaluated at P1, P2, P4 and P6, where 已出生
// and 当期新增出生 no longer coincide).
func BundleSet() []string { return BundleSetAt(CurrentPhase) }

// BundleSetAt returns the codes whose pages/{code} roots must appear as subPackages entries in
// web's pages.json at the given phase:「分包集合 = 已出生业务域 = registry 实建域 − homeos」
// (§1.3 row 6), written per navigation doc §2.4 第 2 查 and PRD 22.5 第 4 道 as「出生期 ≤ 当期」的
// 域集合 minus homeos. It is derived from BirthPhase alone, so bumping CurrentPhase to "P2" adds
// purchase without touching this function, and homeos/finance stay in the set instead of dropping
// out of it -- the 已出生 state is cumulative, the 当期新增出生 state is not.
//
// homeos is subtracted because its pages are the main package and no pages/homeos subPackage
// exists -- leaving it in would fail the check on a correct project (§1.3 row 6 校验点,
// navigation doc §2.4 第 2 查). P1 returns exactly ["finance"]; P2 ["finance","purchase"]; P6
// every face. The order is registration order, and the build script takes each returned code's
// BundlePath from ByCode() rather than prefixing "pages/" itself (--format=meta already emits both
// lists for the non-Go consumers).
func BundleSetAt(phase string) []string {
	out := []string{}
	for _, d := range table {
		if d.Code != HomeosCode && d.BornAt(phase) {
			out = append(out, d.Code)
		}
	}
	return out
}

// Born reports whether this domain is 已出生 in the current checkout (BirthPhase <= CurrentPhase):
// its service, schema, migration sequence and bundle exist, and it is in the bundle set if it is a
// face (PRD 卷首第 12 项, 22.5 第 4 道, tech plan §1.3 row 6). Equivalent to BornAt(CurrentPhase).
func (d Domain) Born() bool { return d.BornAt(CurrentPhase) }

// BornAt is the phase-injected form of Born: 已出生 at the given phase, BirthPhase <= phase.
// Comparison is on the phase number (PRD 11.4 labels the six phases P1..P6), and an unparsable
// label on either side answers false so a typo never grants a directory or a bundle.
func (d Domain) BornAt(phase string) bool { return phaseLE(d.BirthPhase, phase) }

// NewlyBorn reports whether this domain is 当期新增出生: born exactly in this checkout's phase,
// i.e. the face this phase delivers and its 期末验收对象 (定版 M ★, PRD 11.7 第 1 条).
func (d Domain) NewlyBorn() bool { return d.NewlyBornAt(CurrentPhase) }

// NewlyBornAt is the phase-injected form of NewlyBorn: BirthPhase == phase. Narrower than BornAt:
// a face born in an earlier phase is 已出生 but not this phase's delivery, which is why 「实建」 is
// pinned to BornAt and not to this predicate (registry_prd_test.go).
func (d Domain) NewlyBornAt(phase string) bool { return phaseEq(d.BirthPhase, phase) }

// RoutePrefixGlob is this domain's route in the shape PRD 16.1 column「接口前缀」writes,
// /api/{code}/* (also PRD 22.2 第 8 条「路由必须分域：/api/{code}/*」). It is RoutePrefix plus the
// trailing "*", so the stored prefix stays the authority and nothing trims the document text to
// make a comparison pass (see the Domain doc comment for which of the two shapes gate 4's AST
// check uses).
func (d Domain) RoutePrefixGlob() string { return d.RoutePrefix + "*" }

// StreamName is this domain's JetStream stream, HC_{CODE} with the code upper-cased (§3.1 流表:
// the rows read HC_HOMEOS with subjects homeos.> and HC_FINANCE with subjects finance.>, and the
// five unborn ones are registered as「HC_PURCHASE … HC_GROWTH（5 条）」-- the table's own cells are
// the uppercase code, so this is the documented rule rather than a coined one). The stream is
// created only in the domain's birth phase (§3.1 P1 状态列「建」/「不建，各面出生期建」, PRD 卷首
// 第 12 项), which is why Unborn() rows must never reach a stream config.
func (d Domain) StreamName() string { return "HC_" + strings.ToUpper(d.Code) }

// StreamSubjectPattern is this domain's source-stream subject pattern, {code}.> (§3.1 流表:
// HC_HOMEOS carries homeos.>, HC_FINANCE carries finance.>, the five unborn rows are registered as
// {code}.>). A derivation of SubjectPrefix, kept as a method so the table stores one value per
// naming class while the bus config still has a single source to read.
func (d Domain) StreamSubjectPattern() string { return d.SubjectPrefix + ">" }

// ArchiveSubjectPattern is the archive stream's subject for this domain, arch.{code}.>
// (§3.1 row HC_ARCHIVE「arch.{code}.>」, max_age=365d, 归档消费者 hc-archiver 从当期在运的流复制).
// Derived from SubjectPrefix, so arch.{code}.> and arch. + {code}. + > cannot drift apart.
func (d Domain) ArchiveSubjectPattern() string { return "arch." + d.SubjectPrefix + ">" }

// DeadLetterSubjectPattern is the dead-letter stream's subject for this domain, dl.{code}.>
// (§3.1 row HC_DL「dl.{code}.>」, max_age=90d; 重试超限后 nack+term 并写消费方自己的
// {code}_dead_letter). Derived from SubjectPrefix like the archive pattern.
func (d Domain) DeadLetterSubjectPattern() string { return "dl." + d.SubjectPrefix + ">" }

// VersionTable is this domain's golang-migrate version table, schema_migrations_{code}, living
// inside its own schema (§2.2「一个实例一条序列、各自版本表 schema_migrations_{code}（落在自己
// schema 内）」, §十一 S1 行「两条迁移序列」, PRD 16.6「版本表 per-service」). Gate 1 compares the
// version declared in code with the migration headers through this table, per sequence.
func (d Domain) VersionTable() string { return "schema_migrations_" + d.Code }

// ProjectionTable is this domain's local projection of another domain's data,
// {code}_proj_{src} (§2.3 底座侧表 row「{code}_proj_{src}」, §6, PRD 16.3「投影表命名
// {code}_proj_{来源域}」). src is the OTHER domain's registry code, e.g. ProjectionTable("homeos")
// on finance is finance_proj_homeos; §2.3/§6 name the two P1 instances finance_proj_homeos and
// homeos_proj_finance. Only this service's own subscriber writes it, and {code}_proj_* is on the
// 定版 ㉔ suffix whitelist (§10.2 第 2 道), so the src argument is not validated against the table
// here -- rclient's「Target 写成未出生域时启动即失败」(§3.5) is where an unknown target is refused.
func (d Domain) ProjectionTable(src string) string { return d.TablePrefix + "proj_" + src }

// MigrationFileName spells one migration file of this domain's own sequence the way the documents
// name it: {code}_{序号}_{描述}.up.sql (PRD 16.1 命名说明「迁移文件名 {code}_{序号}_{描述}（16.6）」,
// PRD 22.2 第 6 条「迁移文件带系统前缀并各成序列：{code}_{序号}_{描述}」, §2.2 for the per-service
// sequence, and §1.2 仓库布局's example migrations/{code}/{code}_0007_desc.up.sql for the
// golang-migrate suffix). The stem is TablePrefix plus the zero-padded sequence number plus the
// description; pass the description in the documents' lower-case underscore form ("desc").
//
// The padded width is the four digits the two documents' own examples show -- PRD 16.6 writes
// 「finance_0007_...」 and 「purchase_0001_...」, §1.2 writes {code}_0007_desc.up.sql -- and
// migrationSeqWidth is the single place it is stated. PRD 16.1 and 22.2 第 6 条 only say「序号」,
// so a fifth digit anywhere in P2-P6 is a document question, not a code decision.
func (d Domain) MigrationFileName(seq int, desc string) string {
	return fmt.Sprintf("%s%0*d_%s.up.sql", d.TablePrefix, migrationSeqWidth, seq, desc)
}

// OpenAPIContractPath is this domain's OpenAPI contract file, contracts/openapi/{code}.yaml
// (§1.2 仓库布局「contracts/  openapi/{code}.yaml、events/{code}.yaml，版本化」; §3.2 registers the
// events one at server/contracts/events/{code}.yaml). Like MigrationDir the path is relative to
// server/, the monorepo's only Go module root (§1.2, PRD 22.4); gate 5 diffs these files for
// backward compatibility (§10.2 第 5 道).
func (d Domain) OpenAPIContractPath() string { return "contracts/openapi/" + d.Code + ".yaml" }

// EventsContractPath is this domain's event-catalog file, contracts/events/{code}.yaml
// (§1.2 仓库布局; §3.2「server/contracts/events/{code}.yaml」: P1 即登记七个 code 的主题域与 10.3
// 十一条事件, 「登记目录 ≠ 创建流」, so an unborn domain has a catalog entry and no stream).
// Relative to server/, as OpenAPIContractPath() is.
func (d Domain) EventsContractPath() string { return "contracts/events/" + d.Code + ".yaml" }

// DSNSearchPath is the DSN fragment of this domain's service DSN, search_path={code}
// (§1.3 row 2 校验点, §2.1 example「user=hc_finance dbname=homecube search_path=finance」). The
// database name is NOT part of the domain table -- §2.1 registers one cluster and one
// database, and its name is a deploy/env concern.
func (d Domain) DSNSearchPath() string { return "search_path=" + d.Schema }

// migrationSeqWidth is the zero-padded width of the 序号 in a migration file name, read off the
// documented examples (PRD 16.6「finance_0007_...」/「purchase_0001_...」, tech plan §1.2
// {code}_0007_desc.up.sql). See MigrationFileName. PRD 16.1 and 22.2 第 6 条 only say「序号」, so
// the width is established by examples and no sentence states it as a rule; registry_test.go
// pins the derived names against those two example shapes.
const migrationSeqWidth = 4

// phaseLE reports whether birth is at or before the given phase (PRD 11.4 numbers the phases
// P1..P6, so the comparison is on the numeric suffix). An unparsable phase compares as never
// born, which makes a typo fail closed: the domain simply does not get a bundle or a directory.
func phaseLE(birth, now string) bool {
	b, okB := phaseNum(birth)
	n, okN := phaseNum(now)
	if !okB || !okN {
		return false
	}
	return b <= n
}

// phaseEq reports whether a domain is born exactly in the given phase -- the 当期新增出生 state of
// the three-state model (PRD 11.7 第 1 条, tech plan 定版 M ★). Unparsable input answers false for
// the same fail-closed reason as phaseLE.
func phaseEq(birth, now string) bool {
	b, okB := phaseNum(birth)
	n, okN := phaseNum(now)
	return okB && okN && b == n
}

// phaseNum reads the numeric part of a P1..P6 phase label.
func phaseNum(phase string) (int, bool) {
	if len(phase) != 2 || phase[0] != 'P' {
		return 0, false
	}
	digit := phase[1]
	if digit < '1' || digit > '9' {
		return 0, false
	}
	return int(digit - '0'), true
}
