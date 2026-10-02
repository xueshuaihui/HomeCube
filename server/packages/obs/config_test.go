package obs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Tests of the config gate. The判据 is docs/p1-tech-plan.md §2.1 (每服务只用自己那串 DSN，形如
// user=hc_finance dbname=homecube search_path=finance), §1.3 row 2 校验点 (search_path={code}、账号名
// hc_{code}), PRD 22.2 第 2 条 (禁止共用账号), PRD 14.7 + §8.1 (附件目录 uploads/{family_id}/) and
// deploy/env.local.example (the seven key names and the CHANGE_ME placeholder convention).

func TestValidateDSNAcceptsTheDocumentedShape(t *testing.T) {
	for _, d := range registry.Implemented() {
		raw := fmt.Sprintf("user=%s password=secret dbname=homecube search_path=%s", d.DBAccount, d.Schema)
		got, err := ValidateDSN(raw, d)
		if err != nil {
			t.Errorf("ValidateDSN(%q, %s) = %v, want nil: this is §2.1's own DSN shape", raw, d.Code, err)
			continue
		}
		if got != raw {
			t.Errorf("ValidateDSN returned %q, want the value unchanged %q", got, raw)
		}
	}
}

func TestValidateDSNRejects(t *testing.T) {
	d, ok := registry.ByCode("finance")
	if !ok {
		t.Fatal("registry has no finance row")
	}

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			// PRD 22.2 第 2 条「禁止…共用账号」.
			name: "shared account",
			raw:  "user=hc_homeos password=s dbname=homecube search_path=finance",
			want: "专属账号",
		},
		{
			name: "account of another service",
			raw:  "user=postgres password=s dbname=homecube search_path=finance",
			want: "专属账号",
		},
		{
			// §1.3 row 2 校验点「连接串 search_path={code}」.
			name: "wrong search_path",
			raw:  "user=hc_finance password=s dbname=homecube search_path=homeos",
			want: "不符",
		},
		{
			name: "missing search_path",
			raw:  "user=hc_finance password=s dbname=homecube",
			want: "缺少 search_path",
		},
		{
			name: "missing password",
			raw:  "user=hc_finance dbname=homecube search_path=finance",
			want: "缺少 password",
		},
		{
			name: "missing dbname",
			raw:  "user=hc_finance password=s search_path=finance",
			want: "缺少 dbname",
		},
		{
			name: "unfilled placeholder",
			raw:  "user=hc_finance password=CHANGE_ME dbname=homecube search_path=finance",
			want: "占位符",
		},
		{
			// §2.1 registers the keyword/value form.
			name: "url form",
			raw:  "postgres://hc_finance:s@localhost:5432/homecube?search_path=finance",
			want: "关键字/值形态",
		},
		{
			name: "empty",
			raw:  "",
			want: "为空",
		},
		{
			name: "duplicated key",
			raw:  "user=hc_finance user=hc_homeos password=s dbname=homecube search_path=finance",
			want: "出现两次",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateDSN(tc.raw, d)
			if err == nil {
				t.Fatalf("ValidateDSN(%q) accepted a DSN that violates §2.1/22.2, want an error mentioning %q", tc.raw, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ValidateDSN(%q) error = %v, want it to mention %q", tc.raw, err, tc.want)
			}
		})
	}
}

func TestValidateDSNKeepsQuotedValues(t *testing.T) {
	d, _ := registry.ByCode("finance")
	// A password containing spaces and an escaped quote is legal libpq keyword/value form and must
	// not be mis-split into extra keys.
	raw := `user=hc_finance password='a b\'s "c"' dbname=homecube search_path=finance`
	if _, err := ValidateDSN(raw, d); err != nil {
		t.Errorf("ValidateDSN(%q) = %v, want nil", raw, err)
	}
}

func TestParseDSNFields(t *testing.T) {
	fields, err := parseDSNFields(`user=hc_finance password='has spaces' dbname=homecube search_path=finance`)
	if err != nil {
		t.Fatalf("parseDSNFields: %v", err)
	}
	want := map[string]string{
		"user": "hc_finance", "password": "has spaces",
		"dbname": "homecube", "search_path": "finance",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("fields[%q] = %q, want %q", k, fields[k], v)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("parsed %d fields, want %d: %v", len(fields), len(want), fields)
	}

	if _, err := parseDSNFields("user=hc_finance password='unclosed"); err == nil {
		t.Error("parseDSNFields accepted an unclosed quoted value")
	}
}

func TestValidateNATSURL(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		wantErr bool
	}{
		{"nats://nats:4222", false},
		{"tls://nats.example.net:4222", false},
		{"nats://127.0.0.1:4222", false},
		{"nats://localhost", true},       // PRD 10.4 registers nats://{host}:{port}
		{"http://localhost", true},       // no port
		{"redis://localhost:6379", true}, // not a NATS scheme
		{"", true},
	} {
		err := ValidateNATSURL(tc.raw)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateNATSURL(%q) = nil, want an error", tc.raw)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateNATSURL(%q) = %v, want nil", tc.raw, err)
		}
	}
}

func TestResolveUploadsRoot(t *testing.T) {
	// A data root of its own per case: t.Chdir makes the process working directory the declared root,
	// which is exactly the rule under test.
	base := realPath(t, t.TempDir())
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "escape"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// root/linkout -> outside/escape : a symlink that points out of the data root.
	if err := os.Symlink(filepath.Join(outside, "escape"), filepath.Join(root, "linkout")); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)

	// The expected root is base/root/uploads: base is already symlink-free, so joining the rest gives
	// the same form os.Getwd() reports.
	good := filepath.Join(base, "root", "uploads")

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{
			// The documented value of UPLOAD_DIR (deploy/env.local.example, §8.1, PRD 14.7).
			name: "documented value",
			raw:  "./uploads/{family_id}",
			want: good,
		},
		{
			name: "documented shape without the dot slash",
			raw:  "uploads/{family_id}",
			want: good,
		},
		{
			name: "trailing slash",
			raw:  "uploads/{family_id}/",
			want: good,
		},
		{name: "empty", raw: "", wantErr: "为空"},
		{name: "only spaces", raw: "   ", wantErr: "为空"},
		{name: "dot dot", raw: "../../etc/{family_id}", wantErr: "含 .. 片段"},
		{name: "inner dot dot", raw: "uploads/../evil/{family_id}", wantErr: "含 .. 片段"},
		{name: "absolute path", raw: "/var/tmp/uploads/{family_id}", wantErr: "绝对路径"},
		{name: "no placeholder", raw: "./uploads", wantErr: "最后一段必须是 {family_id}"},
		{name: "placeholder not last", raw: "./{family_id}/uploads", wantErr: "最后一段必须是 {family_id}"},
		{name: "placeholder twice", raw: "./{family_id}/{family_id}", wantErr: "只允许出现一次"},
		{name: "placeholder alone leaves no root", raw: "{family_id}", wantErr: "没有目录根"},
		{name: "resolves onto a file", raw: "./afile/{family_id}", wantErr: "不是目录"},
		{name: "symlink escapes the data root", raw: "./linkout/{family_id}", wantErr: "逃出数据根"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveUploadsRoot(tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveUploadsRoot(%q) = %q, want an error mentioning %q", tc.raw, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("ResolveUploadsRoot(%q) error = %v, want it to mention %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveUploadsRoot(%q) = %v, want %q", tc.raw, err, tc.want)
			}
			if got != tc.want {
				t.Errorf("ResolveUploadsRoot(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("ResolveUploadsRoot(%q) = %q, want an absolute path", tc.raw, got)
			}
		})
	}
}

// TestResolveUploadsRootDoesNotCreateTheDirectory pins the card boundary: this card resolves and
// validates the root, the attachment write path is S11-S12.
func TestResolveUploadsRootDoesNotCreateTheDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := ResolveUploadsRoot("./uploads/{family_id}"); err != nil {
		t.Fatalf("ResolveUploadsRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads")); !os.IsNotExist(err) {
		t.Errorf("the uploads root was created by config loading (err=%v); creating it belongs to the write path (S11-S12)", err)
	}
}

func TestLoadConfigFailFast(t *testing.T) {
	d, _ := registry.ByCode("homeos")

	// Nothing set: every required key is named in the error, so the operator learns what to fill.
	for _, k := range []string{"HOMEOS_DSN", "NATS_URL", "UPLOAD_DIR"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	_, err := LoadConfig(ConfigRequest{
		Domain:        d,
		DSNKey:        "HOMEOS_DSN",
		NATSURLKey:    "NATS_URL",
		UploadsDirKey: "UPLOAD_DIR",
		Addr:          ":8080",
	})
	if err == nil {
		t.Fatal("LoadConfig accepted an empty environment, want a fail-fast error")
	}
	for _, want := range []string{"HOMEOS_DSN", "NATS_URL", "UPLOAD_DIR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadConfig error = %v, want it to name the missing key %s", err, want)
		}
	}

	// The documented CHANGE_ME placeholder must not start a process either.
	t.Setenv("HOMEOS_DSN", "user=hc_homeos password=CHANGE_ME dbname=homecube search_path=homeos")
	t.Setenv("NATS_URL", "nats://CHANGE_ME:CHANGE_ME")
	t.Setenv("UPLOAD_DIR", "./uploads/{family_id}")
	_, err = LoadConfig(ConfigRequest{
		Domain: d, DSNKey: "HOMEOS_DSN", NATSURLKey: "NATS_URL", UploadsDirKey: "UPLOAD_DIR", Addr: ":8080",
	})
	if err == nil {
		t.Fatal("LoadConfig accepted the unfilled CHANGE_ME placeholder")
	}
	if !strings.Contains(err.Error(), "占位符") || !strings.Contains(err.Error(), "CHANGE_ME") {
		t.Errorf("LoadConfig error = %v, want it to point at the CHANGE_ME placeholder", err)
	}
}

func TestLoadConfigAcceptsAFilledEnvironment(t *testing.T) {
	d, _ := registry.ByCode("finance")
	dir := t.TempDir()
	t.Chdir(dir)

	t.Setenv("FINANCE_DSN", "user=hc_finance password=s dbname=homecube search_path=finance")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")

	cfg, err := LoadConfig(ConfigRequest{
		Domain: d, DSNKey: "FINANCE_DSN", NATSURLKey: "NATS_URL", Addr: "127.0.0.1:18080",
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Domain.Code != "finance" {
		t.Errorf("cfg.Domain.Code = %q, want finance", cfg.Domain.Code)
	}
	// svc-finance declares no UPLOAD_DIR key, so it gets no attachment root: the directory belongs to
	// homeos's File table only (deploy/env.local.example, PRD 14.7).
	if cfg.UploadsRoot != "" {
		t.Errorf("cfg.UploadsRoot = %q, want empty for a service that declares no uploads key", cfg.UploadsRoot)
	}
}

func TestLoadConfigRejectsBadAddr(t *testing.T) {
	d, _ := registry.ByCode("homeos")
	t.Setenv("HOMEOS_DSN", "user=hc_homeos password=s dbname=homecube search_path=homeos")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")

	for _, addr := range []string{"", "localhost", "8080", ":0"} {
		_, err := LoadConfig(ConfigRequest{
			Domain: d, DSNKey: "HOMEOS_DSN", NATSURLKey: "NATS_URL", Addr: addr,
		})
		if err == nil {
			t.Errorf("LoadConfig accepted listen address %q", addr)
			continue
		}
		if !strings.Contains(err.Error(), "监听地址") {
			t.Errorf("LoadConfig(%q) error = %v, want it to mention 监听地址", addr, err)
		}
	}
}

// realPath resolves symlinks so the comparison is against the same form os.Getwd() reports (on
// macOS a temp dir under /var is a symlink to /private/var).
func realPath(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", p, err)
	}
	return got
}

// TestDefaultListenAddrDerivesDistinctPorts is the regression for the two-服务默认端口冲突假绿: the
// previous cmd defaulted BOTH services to :8080, so the second `make dev-*` exited with
// "bind: address already in use" even though §十三 requires the two to run side by side outside
// containers. The derived default comes from registry.Implemented() registration order (no
// hand-copied code->port table), so homeos (row 0) and finance (row 1) land on distinct ports and
// an unimplemented code is refused rather than granted a port.
func TestDefaultListenAddrDerivesDistinctPorts(t *testing.T) {
	impl := registry.Implemented()
	if len(impl) < 2 {
		t.Fatalf("registry.Implemented() has %d rows; the collision regression needs at least two", len(impl))
	}

	seen := map[string]string{}
	for i, d := range impl {
		addr, err := DefaultListenAddr(d.Code)
		if err != nil {
			t.Fatalf("DefaultListenAddr(%q): %v", d.Code, err)
		}
		want := fmt.Sprintf(":%d", defaultListenPortBase+i)
		if addr != want {
			t.Errorf("DefaultListenAddr(%q) = %q, want %q (base %d + registration index %d)", d.Code, addr, want, defaultListenPortBase, i)
		}
		if other, dup := seen[addr]; dup {
			t.Errorf("derived default port collision: %q and %q both map to %s", other, d.Code, addr)
		}
		seen[addr] = d.Code
	}

	// homeos and finance specifically must not share the old :8080/::8080 shape.
	home, err := DefaultListenAddr("homeos")
	if err != nil {
		t.Fatal(err)
	}
	fin, err := DefaultListenAddr("finance")
	if err != nil {
		t.Fatal(err)
	}
	if home == fin {
		t.Errorf("homeos and finance derive the same default %q -- the collision this fix removes", home)
	}

	// A code the table does not declare implemented gets no port.
	if _, err := DefaultListenAddr("purchase"); err == nil {
		t.Error("DefaultListenAddr(purchase) succeeded, but purchase is 未出生/未实建 (PRD 卷首第 12 项)")
	}
}

// TestLoadConfigResolvesAddrFlagEnvDefault proves the resolution order: -addr flag wins; else the
// {CODE}_ADDR env key; else the registry-derived default. It also keeps the fail-closed rules: an
// empty Addr without the opt-in is still rejected, and a CHANGE_ME env Addr is an error.
func TestLoadConfigResolvesAddrFlagEnvDefault(t *testing.T) {
	d, _ := registry.ByCode("finance")
	t.Setenv("FINANCE_DSN", "user=hc_finance password=s dbname=homecube search_path=finance")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")

	newReq := func(addr string) ConfigRequest {
		return ConfigRequest{
			Domain: d, DSNKey: "FINANCE_DSN", NATSURLKey: "NATS_URL",
			Addr: addr, AddrEnvKey: "FINANCE_ADDR", ResolveDefaultAddrWhenEmpty: true,
		}
	}

	// (1) no flag, no env -> derived default (:8081, finance is Implemented() row 1).
	t.Setenv("FINANCE_ADDR", "")
	cfg, err := LoadConfig(newReq(""))
	if err != nil {
		t.Fatalf("derived-default case: %v", err)
	}
	if cfg.Addr != ":8081" {
		t.Errorf("empty flag + empty env -> Addr = %q, want the derived :8081", cfg.Addr)
	}

	// (2) env key present -> used.
	t.Setenv("FINANCE_ADDR", "127.0.0.1:19090")
	cfg, err = LoadConfig(newReq(""))
	if err != nil {
		t.Fatalf("env case: %v", err)
	}
	if cfg.Addr != "127.0.0.1:19090" {
		t.Errorf("empty flag + env set -> Addr = %q, want the env value", cfg.Addr)
	}

	// (3) explicit flag beats env.
	cfg, err = LoadConfig(newReq(":7777"))
	if err != nil {
		t.Fatalf("flag case: %v", err)
	}
	if cfg.Addr != ":7777" {
		t.Errorf("explicit flag -> Addr = %q, want :7777 (flag precedence over env)", cfg.Addr)
	}

	// (4) CHANGE_ME env is rejected (no silent bind).
	t.Setenv("FINANCE_ADDR", placeholderValue)
	if _, err := LoadConfig(newReq("")); err == nil || !strings.Contains(err.Error(), "占位符") {
		t.Errorf("CHANGE_ME env Addr: err = %v, want a 占位符 rejection", err)
	}

	// (5) without the opt-in, an empty Addr is still rejected (flag-only contract preserved).
	t.Setenv("FINANCE_ADDR", "127.0.0.1:19090")
	if _, err := LoadConfig(ConfigRequest{
		Domain: d, DSNKey: "FINANCE_DSN", NATSURLKey: "NATS_URL", Addr: "",
	}); err == nil || !strings.Contains(err.Error(), "监听地址") {
		t.Errorf("empty Addr without opt-in: err = %v, want 监听地址 rejection", err)
	}
}
