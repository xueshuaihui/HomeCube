// Command registryd exports the registry domain table to the consumers that are not Go.
//
// Why it exists: docs/p1-tech-plan.md §1.3 makes registry.Domains() the 唯一真源 and names its
// consumers as 服务启动、迁移工具、前端构建脚本与 CI. The last two are shell/Node (web's
// pages.json 三查 per docs/p1-page-structure-navigation.md §2.4, the nginx/compose generation of
// S1-D, and the gate scripts of PRD 22.5 第 2/4 道), so they need the table without ever
// re-spelling a code. This command prints what the package API returns and holds no table of
// its own.
//
// --format=meta and --format=bundles exist for one specific reason: the 第 2 查 of the pages.json
// 三查 is「分包集合 = 已出生域 − homeos」, and PRD 22.5 第 4 道 spells the same criterion as
// 「出生期 ≤ 当期」的域集合 minus homeos. Both the current phase number and the derived bundle set
// live in the Go table only, so without these two exports a shell/Node gate script would have to
// carry its own copy of「当期」-- exactly the second source §1.3「一张域表是唯一真源」exists to
// forbid.
//
// Usage:
//
//	go run ./packages/registry/cmd/registryd --format=json    # whole table, registration order
//	go run ./packages/registry/cmd/registryd --format=codes   # one code per line, same order
//	go run ./packages/registry/cmd/registryd --format=bundles # one bundle-set code per line
//	go run ./packages/registry/cmd/registryd --format=meta    # currentPhase/implemented/unborn/
//	                                                          # bundles/bundleRoots as one JSON
//
// The order of json, codes and bundles is Domains()/BundleSet() order, which is a contract
// (PRD 17.1 / 17.2 and navigation doc §3.2 derive the home-screen matrix order from it; §1.3 row 6
// derives the bundle set from it), so a consumer may pipe --format=codes or --format=bundles
// straight into an ordered comparison. json and codes keep the exact shape they shipped with in
// the first S1-B delivery, because S1-D (nginx/compose) and S1-E (backup scripts) write against
// them.
//
// Imports: only packages/registry and the standard library. Nothing under services/ is
// imported, in this file or in any package/* file (PRD 22.4, 22.5 第 4 道).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// meta is the --format=meta document: what a non-Go consumer needs in order to run a check that
// the documents phrase in terms of the current phase, without restating a phase number or a code
// list. Field order is the order the keys are marshalled in.
type meta struct {
	// CurrentPhase is registry.CurrentPhase, the phase this checkout delivers (PRD 卷首第 12 项,
	// 22.5 第 4 道「出生期 ≤ 当期」).
	CurrentPhase string `json:"currentPhase"`
	// Implemented is registry.Implemented() as codes, registration order: the domains whose
	// service/schema/sequence really exist now (P1 = homeos + finance, PRD 22.2 第 1 条).
	Implemented []string `json:"implemented"`
	// Unborn is registry.Unborn() as codes: BirthPhase > CurrentPhase, the set CI asserts ABSENCE
	// over (PRD 11.7 第 1 条, tech plan §1.2, 定版 E ★).
	Unborn []string `json:"unborn"`
	// Bundles is registry.BundleSet(): the already-born face codes that must be subPackages
	// entries (§1.3 row 6, navigation doc §2.4 第 2 查). homeos is already subtracted.
	Bundles []string `json:"bundles"`
	// BundleRoots is the same set as pages.json subPackages[].root values, taken from each
	// domain's stored BundlePath rather than prefixed by the consumer (§1.3 row 6 says the build
	// script must not spell "pages/" itself).
	BundleRoots []string `json:"bundleRoots"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "registryd: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable body of the command: it writes the export to out and returns an error
// instead of exiting, so registryd_test.go can assert the exported content equals the table.
func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("registryd", flag.ContinueOnError)
	fs.SetOutput(out)
	format := fs.String("format", "json", "export shape: json (full table) | codes (one code per line) | bundles (bundle-set codes) | meta (currentPhase+implemented+unborn+bundles as JSON)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch *format {
	case "json":
		// The full table: every row of registry.Domains(), which is the same slice the services,
		// the migration tool and BundleSet() are computed from.
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(registry.Domains()); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		return nil
	case "codes":
		for _, d := range registry.Domains() {
			if _, err := fmt.Fprintln(out, d.Code); err != nil {
				return fmt.Errorf("write code %q: %w", d.Code, err)
			}
		}
		return nil
	case "bundles":
		// One line per bundle-set code, i.e. registry.BundleSet() in order: the set the pages.json
		// 第 2 查 compares subPackages against (navigation doc §2.4 第 2 查, PRD 22.5 第 4 道).
		for _, code := range registry.BundleSet() {
			if _, err := fmt.Fprintln(out, code); err != nil {
				return fmt.Errorf("write bundle code %q: %w", code, err)
			}
		}
		return nil
	case "meta":
		// One JSON document with the phase-derived sets, so a shell/Node consumer reads the current
		// phase and the bundle set from the table instead of carrying its own copy (§1.3).
		bundles := registry.BundleSet()
		m := meta{
			CurrentPhase: registry.CurrentPhase,
			Implemented:  codes(registry.Implemented()),
			Unborn:       codes(registry.Unborn()),
			Bundles:      bundles,
			BundleRoots:  bundleRoots(bundles),
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(m); err != nil {
			return fmt.Errorf("encode meta: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown --format %q: want json, codes, bundles or meta", *format)
	}
}

// codes projects a domain slice onto its codes, keeping the slice's order.
func codes(ds []registry.Domain) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

// bundleRoots resolves bundle codes through ByCode so a root is always the stored BundlePath
// (§1.3 row 6: the consumer must not prefix "pages/" itself). A code the table does not carry is
// skipped rather than guessed at, and it cannot happen for BundleSet()'s own output.
func bundleRoots(cs []string) []string {
	out := make([]string, 0, len(cs))
	for _, code := range cs {
		d, ok := registry.ByCode(code)
		if !ok {
			continue
		}
		out = append(out, d.BundlePath)
	}
	return out
}
