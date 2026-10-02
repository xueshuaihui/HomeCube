package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// registryd exists so the non-Go consumers (web's pages.json 三查, compose/nginx generation, the
// gate scripts) read the domain table from registry.Domains() instead of hard-coding a code
// list (tech plan §1.3). These tests pin that the export really is the table: same rows, same
// order, same cell values -- not a hand-written printout.

func TestJSONExportEqualsTable(t *testing.T) {
	var buf strings.Builder
	if err := run([]string{"--format=json"}, &buf); err != nil {
		t.Fatalf("run(--format=json) returned an error: %v", err)
	}

	var exported []registry.Domain
	if err := json.Unmarshal([]byte(buf.String()), &exported); err != nil {
		t.Fatalf("--format=json did not emit parseable JSON: %v\noutput:\n%s", err, buf.String())
	}

	want := registry.Domains()
	if len(exported) != len(want) {
		t.Fatalf("--format=json exported %d rows, registry.Domains() has %d", len(exported), len(want))
	}
	for i := range want {
		if exported[i] != want[i] {
			t.Errorf("--format=json row %d differs from registry.Domains()[%d]:\n exported %+v\n table    %+v",
				i, i, exported[i], want[i])
		}
	}
}

func TestCodesExportIsOrderedCodeList(t *testing.T) {
	var buf strings.Builder
	if err := run([]string{"--format=codes"}, &buf); err != nil {
		t.Fatalf("run(--format=codes) returned an error: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := registry.Domains()
	if len(lines) != len(want) {
		t.Fatalf("--format=codes emitted %d lines, want %d (one per domain): %q",
			len(lines), len(want), lines)
	}
	for i, line := range lines {
		if line != want[i].Code {
			t.Errorf("--format=codes line %d = %q, want %q (Domains() order is the home-matrix "+
				"contract, PRD 17.1)", i, line, want[i].Code)
		}
	}
}

func TestDefaultFormatIsJSON(t *testing.T) {
	// The build scripts may call the binary without a flag; pin the default so --format cannot
	// silently change meaning. []string{} rather than nil: flag.Parse(nil) would fall back to
	// os.Args[1:], which inside a test binary is the -test.* flag set.
	var buf strings.Builder
	if err := run([]string{}, &buf); err != nil {
		t.Fatalf("run with no flags returned an error: %v", err)
	}
	if !json.Valid([]byte(buf.String())) {
		t.Errorf("default output is not JSON: %q", firstLine(buf.String()))
	}

	var decoded []registry.Domain
	if err := json.Unmarshal([]byte(buf.String()), &decoded); err != nil {
		t.Fatalf("default output is not the domain table: %v", err)
	}
	if len(decoded) != len(registry.Domains()) {
		t.Errorf("default export has %d rows, want %d", len(decoded), len(registry.Domains()))
	}
}

func TestUnknownFormatFails(t *testing.T) {
	// A typo in a CI step must fail loudly rather than emit a partial table.
	var buf strings.Builder
	if err := run([]string{"--format=yaml"}, &buf); err == nil {
		t.Fatalf("run(--format=yaml) returned no error; output: %q", buf.String())
	}
}

func TestUnknownFormatErrorListsEveryAcceptedValue(t *testing.T) {
	// The card for this export asks for the expected-value list on the error path: a gate script
	// that fails on --format=yaml has to learn from the message which shapes exist, otherwise the
	// reader has to go back to the source. One accepted value missing from the message would send
	// the reader to the wrong option.
	var buf strings.Builder
	err := run([]string{"--format=yaml"}, &buf)
	if err == nil {
		t.Fatalf("run(--format=yaml) returned no error; output: %q", buf.String())
	}
	if buf.Len() != 0 {
		t.Errorf("run(--format=yaml) wrote %d bytes to the export stream before failing: an unknown "+
			"format must emit no partial table, since a caller cannot tell partial from full output "+
			"(%q)", buf.Len(), buf.String())
	}
	msg := err.Error()
	for _, want := range []string{"json", "codes", "bundles", "meta"} {
		if !strings.Contains(msg, want) {
			t.Errorf("unknown-format error %q does not name the accepted value %q (tech plan §1.3: the "+
				"bundle/meta shapes are what the pages.json 三查 and the gate scripts call, so they must "+
				"appear in the message a failing CI step reads)", msg, want)
		}
	}
	if !strings.Contains(msg, "yaml") {
		t.Errorf("unknown-format error %q does not echo the offending value -- a log line that says "+
			"only \"unknown format\" cannot be debugged from the workflow output", msg)
	}
}

func TestBundlesExportIsTheBundleSetInOrder(t *testing.T) {
	// --format=bundles exists so the pages.json 第 2 查 can compare subPackages against
	// 「分包集合 = 已出生业务域 − homeos」without carrying its own copy of the phase or the code list
	// (tech plan §1.3 row 6, navigation doc §2.4 第 2 查, PRD 22.5 第 4 道「出生期 ≤ 当期」). The
	// output therefore has to be registry.BundleSet() -- same members, same order, nothing else.
	var buf strings.Builder
	if err := run([]string{"--format=bundles"}, &buf); err != nil {
		t.Fatalf("run(--format=bundles) returned an error: %v", err)
	}

	got := splitLines(buf.String())
	want := registry.BundleSet()
	if len(got) != len(want) {
		t.Fatalf("--format=bundles emitted %d lines %q, BundleSet() has %d %q",
			len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("--format=bundles line %d = %q, BundleSet()[%d] = %q: the order is registration "+
				"order (PRD 17.1), which the caller's ordered comparison depends on", i, got[i], i, want[i])
		}
	}

	// Two properties that hold at every phase, independent of the current one.
	for _, code := range got {
		if code == registry.HomeosCode {
			t.Errorf("--format=bundles lists %q: HomeOS's pages are the main package, so a "+
				"pages/homeos subPackage is illegal (§1.3 row 6 校验点, navigation doc §2.4 第 2 查)", code)
		}
		d, ok := registry.ByCode(code)
		if !ok {
			t.Errorf("--format=bundles lists %q, which no registry row carries: a second source of "+
				"truth next to registry.Domains()", code)
			continue
		}
		if !d.Born() {
			t.Errorf("--format=bundles lists %q whose birth phase %s is later than %s: the bundle set "+
				"is 已出生域 (BirthPhase <= 当期), so this would demand a directory PRD 11.7 第 1 条 forbids",
				code, d.BirthPhase, registry.CurrentPhase)
		}
	}
}

func TestMetaExportKeysMatchTheRegistryAPI(t *testing.T) {
	// --format=meta is the one-call shape for a shell/Node gate: it must carry the current phase
	// plus all four derived sets, each equal to the Go API at the same instant. Decoded as a map of
	// raw messages rather than into the meta struct on purpose -- unmarshalling into that struct
	// would still pass if a json tag were renamed, and the tag names are what the consumer reads.
	var buf strings.Builder
	if err := run([]string{"--format=meta"}, &buf); err != nil {
		t.Fatalf("run(--format=meta) returned an error: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(buf.String()), &doc); err != nil {
		t.Fatalf("--format=meta did not emit one JSON object: %v\noutput:\n%s", err, buf.String())
	}
	for _, key := range []string{"currentPhase", "implemented", "unborn", "bundles", "bundleRoots"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("--format=meta is missing the %q key: the gate scripts call it by name "+
				"(navigation doc §2.4 第 2 查 needs currentPhase+bundles, PRD 22.5 第 4 道 needs the sets)", key)
		}
	}
	for key := range doc {
		switch key {
		case "currentPhase", "implemented", "unborn", "bundles", "bundleRoots":
		default:
			t.Errorf("--format=meta carries the undocumented key %q: a consumer can only rely on the "+
				"keys the clause comments name (tech plan §1.3)", key)
		}
	}

	if got := decodeString(t, doc, "currentPhase"); got != registry.CurrentPhase {
		t.Errorf("--format=meta currentPhase = %q, registry.CurrentPhase = %q", got, registry.CurrentPhase)
	}

	cases := []struct {
		key  string
		want []string
	}{
		{"implemented", codesOf(registry.Implemented())},
		{"unborn", codesOf(registry.Unborn())},
		{"bundles", registry.BundleSet()},
	}
	for _, c := range cases {
		got := decodeCodes(t, doc, c.key)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("--format=meta %s = %q, the registry API returns %q", c.key, got, c.want)
		}
	}

	// bundleRoots is the pages.json subPackages[].root shape: the stored BundlePath of each bundle
	// code, in the same order -- the consumer must not prefix "pages/" itself (§1.3 row 6).
	gotRoots := decodeCodes(t, doc, "bundleRoots")
	bundles := registry.BundleSet()
	if len(gotRoots) != len(bundles) {
		t.Fatalf("--format=meta bundleRoots has %d entries, bundles has %d: %q vs %q",
			len(gotRoots), len(bundles), gotRoots, bundles)
	}
	for i, code := range bundles {
		d, ok := registry.ByCode(code)
		if !ok {
			t.Fatalf("BundleSet() returned %q, which ByCode does not know", code)
		}
		if gotRoots[i] != d.BundlePath {
			t.Errorf("--format=meta bundleRoots[%d] = %q, want %q (the %s row's stored BundlePath)",
				i, gotRoots[i], d.BundlePath, code)
		}
	}

	// implemented and unborn must not overlap and must not invent an eighth code.
	implemented := decodeCodes(t, doc, "implemented")
	unborn := decodeCodes(t, doc, "unborn")
	for _, a := range implemented {
		for _, b := range unborn {
			if a == b {
				t.Errorf("--format=meta lists %q in both implemented and unborn", a)
			}
		}
	}
}

// codesOf projects a domain slice onto codes without importing this package's own helper, so the
// comparison above is against the registry API rather than against the code under test.
func codesOf(ds []registry.Domain) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func decodeString(t *testing.T, doc map[string]json.RawMessage, key string) string {
	t.Helper()

	raw, ok := doc[key]
	if !ok {
		t.Fatalf("--format=meta has no %q key", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("--format=meta %q is not a JSON string: %v (%s)", key, err, raw)
	}
	return s
}

func decodeCodes(t *testing.T, doc map[string]json.RawMessage, key string) []string {
	t.Helper()

	raw, ok := doc[key]
	if !ok {
		t.Fatalf("--format=meta has no %q key", key)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("--format=meta %q is not a JSON array of strings: %v (%s)", key, err, raw)
	}
	return got
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestExportContainsNoHardCodedExtraRows(t *testing.T) {
	// Reverse check on the export path: nothing may appear in the output that the table does not
	// carry (an extra code here would be a second source of truth next to registry.Domains()).
	var buf strings.Builder
	if err := run([]string{"--format=codes"}, &buf); err != nil {
		t.Fatalf("run(--format=codes) returned an error: %v", err)
	}
	for _, code := range []string{"meal", "family", "travel", "education", "note"} {
		if strings.Contains(buf.String(), code) {
			t.Errorf("--format=codes mentions %q, which is not a PRD 16.1 code: output: %q",
				code, strings.TrimSpace(buf.String()))
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
