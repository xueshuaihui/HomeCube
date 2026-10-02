package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Filesystem side of tech plan §1.3:「CI 校验的是『已存在的东西必须合法』与『不该存在的东西必须
// 不存在』两件事」. A registry that only asserts the absence half is a no-op -- §1.2 says the
// five domains with no real service must have no directory, migration file, bundle or stream,
// while the two domains registered as implemented must have them. Both directions are checked.
//
// Clauses: PRD 11.7 第 1 条 (业务服务、schema、迁移序列与前端分包一律不得提前创建), PRD 22.4
// (P1 只存在 svc-homeos 与 svc-finance 两个服务目录与一个 pages/finance 分包), PRD 22.5 第 4 道,
// tech plan §1.2/§1.3 与定版 E ★.

func TestUnbornDomainsHaveNoDirectories(t *testing.T) {
	root := repoRoot(t)

	unborn := registry.Unborn()
	if len(unborn) == 0 {
		t.Fatal("Unborn() is empty: nothing to assert, so the 「不该存在」 half of §1.3 would be a no-op")
	}

	// Self-protection: an absence assertion under a NON-EXISTENT probe root is vacuously true --
	// that is exactly the假绿 this card removes. The old version probed 「web/pages/{code}」 for the
	// frontend dimension, but the uni-app project keeps its page tree at 「web/src/pages/」 (confirmed
	// by web/src/pages.json + web/src/pages/{homeos,finance} on disk, and mirrored by
	// web/scripts/check-pages.mjs which resolves every bundle under SRC=web/src). Probing the wrong
	// root made 「no bundle directory」 trivially hold for all five unborn domains. So before trusting
	// any absence below, require the three probe ROOTS themselves to exist; a missing root means the
	// layout moved, not that the unborn domains are clean.
	for _, r := range []string{
		filepath.Join(root, "server", "services"),
		filepath.Join(root, "server", "migrations"),
		webBundleRoot(root),
	} {
		if !isDir(r) {
			t.Fatalf("probe root %s is absent: asserting 「unborn domain has no directory」 under a "+
				"non-existent root is vacuous -- this is precisely the假绿 §1.3 「不该存在」 half was made "+
				"to avoid. Either the repository layout moved (update webBundleRoot) or the checkout is "+
				"incomplete; the absence assertions below cannot be trusted.", r)
		}
	}

	for _, d := range unborn {
		for _, probe := range []struct {
			label string
			path  string
		}{
			// 服务目录：由登记的服务名拼出，验证 ServiceName 这一维与磁盘同源。
			{"服务目录 server/services/" + d.ServiceName, filepath.Join(root, "server", "services", d.ServiceName)},
			// 迁移序列目录（§1.2 仓库布局）。
			{"迁移目录 server/" + d.MigrationDir, filepath.Join(root, "server", d.MigrationDir)},
			// 前端分包目录（PRD 16.1 前端分包列 / navigation doc §2.4 第 2 查），落在 uni-app 的
			// src/pages 层而非 web/pages（见上方 root 守卫；「web → src」这段前缀是工程约定，是否
			// 进 registry 表属待回写的文档问题，本卡取真实布局）。
			{"分包目录 web/src/" + d.BundlePath, webBundleDir(root, d)},
		} {
			if _, err := os.Stat(probe.path); err == nil {
				t.Errorf("unborn domain %q (birth phase %s) has %s at %s: PRD 11.7 第 1 条 and tech plan "+
					"定版 E ★ forbid creating it ahead of its birth phase; gate 2/4 must fail this path",
					d.Code, d.BirthPhase, probe.label, probe.path)
			} else if !os.IsNotExist(err) {
				t.Errorf("unborn domain %q: cannot stat %s: %v", d.Code, probe.path, err)
			}
		}
	}
}

func TestImplementedDomainsHaveTheirDirectories(t *testing.T) {
	// The other half of §1.3:「只有登记为实建的域才要求文件存在」. Asserting absence alone would
	// pass on an empty repository, which is what the review calls a no-op -- and asserting that a
	// directory merely EXISTS passes on a directory holding nothing but a .gitkeep, which is what
	// server/services/svc-{homeos,finance}/ and server/migrations/{homeos,finance}/ hold today.
	// The criterion is not "a folder was made", it is 「实建 2 服务 + 两条迁移序列」:
	//
	//   - 服务：tech plan §十一 S1 行「实建 2 服务」、PRD 14.5 第 1 项「实建 svc-homeos + svc-finance」、
	//     PRD 22.4「每服务一个 cmd、一个镜像、一条发布线」. The cmd path shape
	//     server/services/svc-{code}/cmd/svc-{code}/main.go is the one the root Makefile's
	//     dev-homeos / dev-finance targets already check and document (Makefile §dev 目标头注释), so
	//     this asserts a shape that exists in the repository rather than one invented here.
	//   - 迁移：§2.2「每服务一条独立序列」+ §十一 S1 行「两条迁移序列」+ PRD 22.5 第 1 道「逐服务在空库
	//     重放自己那条序列」. An empty sequence directory replays nothing, so the directory is not
	//     the判据 -- at least one *.up.sql under server/migrations/{code}/ is.
	//
	// THIS TEST IS EXPECTED TO FAIL until S1-C delivers the two service skeletons and the two
	// migration sequences (the .gitkeep-only state above). That red is the point: it is what stops
	// §十一 S1's 「实建 2 服务 + 两条迁移序列」 from being read as passed. It must not be softened
	// back to os.Stat, skipped, or satisfied with empty placeholder files (brief §6 禁令 1/2/3).
	root := repoRoot(t)

	implemented := registry.Implemented()
	if len(implemented) == 0 {
		t.Fatal("Implemented() is empty: nothing to assert")
	}

	for _, d := range implemented {
		// 服务入口：cmd/{svc}/main.go，不是「目录存在」。
		mainPath := filepath.Join(root, "server", "services", d.ServiceName, "cmd", d.ServiceName, "main.go")
		if info, err := os.Stat(mainPath); err != nil {
			t.Errorf("implemented domain %q has no service entrypoint at %s: PRD 14.5 第 1 项 and tech plan "+
				"§十一 S1 行 register %s as 实建 in %s, so its cmd must exist (\"每服务一个 cmd\"，PRD 22.4). "+
				"Absent -> belongs to S1-C (%v)", d.Code, mainPath, d.ServiceName, registry.CurrentPhase, err)
		} else if info.IsDir() {
			t.Errorf("implemented domain %q: %s is a directory, not the main.go entrypoint", d.Code, mainPath)
		}

		// 迁移序列：目录里至少一支 *.up.sql，不是空目录。
		migDir := filepath.Join(root, "server", d.MigrationDir)
		ups, err := listUpMigrations(t, migDir)
		if err != nil {
			t.Errorf("implemented domain %q: cannot read its migration sequence at %s: §2.2 registers one "+
				"independent sequence per service and §十一 S1 行 counts two for P1 (%v)", d.Code, migDir, err)
			continue
		}
		if len(ups) == 0 {
			t.Errorf("implemented domain %q has no *.up.sql under %s: an empty sequence cannot be replayed, "+
				"so 门禁 1 (PRD 22.5 第 1 道「逐服务在空库重放自己那条序列」) has nothing to run and §十一 S1 行's "+
				"「两条迁移序列」 is not delivered -- a directory holding only .gitkeep does not satisfy it (S1-C)",
				d.Code, migDir)
		}
	}
}

// listUpMigrations returns the names of the *.up.sql files directly inside dir. A sequence root is
// flat -- §2.2 gives golang-migrate one directory per service -- so sub-directories are not part of
// the count. A missing or unreadable dir comes back as an error, which the caller reports: the
// distinction between "no sequence directory" and "empty sequence" matters for the failure message,
// not for the verdict.
func listUpMigrations(t *testing.T, dir string) ([]string, error) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func TestNoDirectoryOutsideTheRegistry(t *testing.T) {
	// "已存在的东西必须合法" (§1.3) scanned from the disk rather than from the table: any service,
	// migration sequence or bundle directory whose name is not a legal value for the current phase is
	// either a typo, an invented eighth naming domain, or a directory created ahead of its birth phase,
	// and gate 2/4 owns it.
	//
	// The three probes cover the three on-disk spaces the registry names (tech plan §1.2 仓库布局):
	//
	//   - server/services/* and server/migrations/*: legal = every registered value (all seven
	//     ServiceName / code cells). §1.3 row 1 and row 7 name those spaces by the naming class. The
	//     「不该存在」 half for the five unborn rows is asserted by TestUnbornDomainsHaveNoDirectories,
	//     which can say WHY a directory is wrong; this test catches a name no registry row claims at
	//     all (an eighth domain / typo).
	//   - web/src/pages/*: legal = the codes the current phase is allowed to have a directory for,
	//     i.e. homeos (the MAIN package) plus the born faces (Implemented()). It is NOT the subPackage
	//     set: on the real uni-app layout web/src/pages holds BOTH the main-package page tree
	//     (src/pages/homeos) AND every subPackage root (src/pages/finance), so homeos is a legal
	//     DIRECTORY here even though 「pages/homeos as a subPackages[] root」 is illegal (§1.3 row 6,
	//     navigation doc §2.4 第 2 查) -- that registration-side rule is enforced by
	//     web/scripts/check-pages.mjs against pages.json, not by this filesystem scan. Listing every
	//     registered code (including the five unborn faces) here would wave a提前建的 pages/purchase
	//     directory through, which is the exact假绿 this card removes.
	root := repoRoot(t)

	services := map[string]bool{}
	for _, d := range registry.Domains() {
		services[d.ServiceName] = true
	}
	codes := map[string]bool{}
	for _, d := range registry.Domains() {
		codes[d.Code] = true
	}
	// The directory under web/src/pages is the last segment of the stored BundlePath (pages/{code}),
	// derived from the registry rather than re-spelled; only rows the current phase may materialise
	// (homeos main package + born faces = Implemented()) are legal on disk.
	bundleDirs := map[string]bool{}
	for _, d := range registry.Implemented() {
		bundleDirs[filepath.ToSlash(filepath.Base(d.BundlePath))] = true
	}
	// homeos is legitimately a web/src/pages directory (it is the main package); if it ever dropped
	// out of Implemented() the guard below would let a wrong layout through, so assert it is present.
	if !bundleDirs[registry.HomeosCode] {
		t.Fatal("homeos is not in the web/src/pages legal-name set: §2.4 says the main package pages " +
			"live under src/pages/homeos, so a legal checkout must keep it here")
	}

	probes := []struct {
		dir      string
		label    string
		expected map[string]bool
	}{
		{filepath.Join(root, "server", "services"), "server/services/* (must be a registered svc-{code})", services},
		{filepath.Join(root, "server", "migrations"), "server/migrations/* (must be a registered code)", codes},
		{webBundleRoot(root), "web/src/pages/* (must be homeos or a born-face code)", bundleDirs},
	}

	for _, p := range probes {
		for _, name := range listDirs(t, p.dir) {
			if !p.expected[name] {
				t.Errorf("%s contains %q, which no registry row authorises for that space at %s: the set of "+
					"directories must be derived from registry.Domains()/Implemented() (tech plan §1.3「一张域表"+
					"是唯一真源」), and a page directory that is neither homeos nor a born face is either a "+
					"typo, an eighth naming domain, or a bundle built ahead of its birth phase",
					p.label, name, registry.CurrentPhase)
			}
		}
	}
}

// webBundleRoot is the on-disk root that holds every uni-app page directory, derived from the
// registry rather than hard-coded: the registry's BundlePath ("pages/{code}") is relative to the
// uni-app source root web/src (confirmed by web/src/pages.json, web/src/main.ts and the presence of
// web/src/pages/{homeos,finance}), so the bundle root is web/src + Dir(BundlePath). Whether the
// "web -> src" prefix belongs in the registry table (PRD 16.1 前端分包 stores only "pages/{code}",
// with no src/ segment) is a document question reported to the card owner, not decided here.
func webBundleRoot(root string) string {
	homeos, ok := registry.ByCode(registry.HomeosCode)
	if !ok {
		// Unreachable for this table, but fail loudly rather than silently probing a wrong path.
		return filepath.Join(root, "web", "src", "pages")
	}
	return filepath.Join(root, "web", "src", filepath.ToSlash(filepath.Dir(homeos.BundlePath)))
}

// webBundleDir is the physical directory of one domain's bundle/page root: web/src + BundlePath.
func webBundleDir(root string, d registry.Domain) string {
	return filepath.Join(root, "web", "src", filepath.ToSlash(d.BundlePath))
}

// listDirs returns the immediate sub-directory names of dir. A missing dir is a test failure, not a
// "correct empty state": every space this test scans (server/services, server/migrations,
// web/src/pages) exists in a valid checkout, so a missing root means the layout moved or the
// checkout is incomplete -- exactly the condition under which the old 「web/pages」 probe returned nil
// and the absence/legality assertions became vacuous. Failing here keeps the probe honest.
func listDirs(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read %s: %v (a probe root that does not exist makes this check vacuous -- "+
			"the false green this card removes came from trusting a non-existent web/pages path)", dir, err)
	}

	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// repoRoot walks up from the test's working directory to the repository root, identified by the
// directory that carries both docs/prd-homecube.md and server/go.mod. Hard-coding ../../../ would
// silently break the moment this package moves, and this file asserts real paths.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get the working directory: %v", err)
	}

	for cur := dir; ; {
		if isFile(filepath.Join(cur, "docs", "prd-homecube.md")) && isFile(filepath.Join(cur, "server", "go.mod")) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("walked up from %s without finding a directory holding docs/prd-homecube.md and "+
				"server/go.mod -- the filesystem assertions cannot locate the repository root", dir)
		}
		cur = parent
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
