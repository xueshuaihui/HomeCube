package obs

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Config is one service's runtime configuration, read from the environment keys
// deploy/env.local.example registers (docs/p1-tech-plan.md §十三「端口与 env 在
// deploy/env.local.example」).
//
// Only the keys a running skeleton actually consumes are read: HOMEOS_DSN / FINANCE_DSN (§2.1),
// NATS_URL (§3.1, PRD 10.4) and UPLOAD_DIR (PRD 14.7, §8.1 -- and per env.local.example's own note
// the attachment directory belongs to svc-homeos alone, so svc-finance does not read it). The other
// three keys of that file are not this process's: FINANCE_JWKS_URL belongs to the authz SDK (S4,
// §4.1) and BACKUP_DIR / SOAK_DIR belong to the make targets (S1-E).
//
// The listen address is NOT a key deploy/env.local.example registers today. §十三 hands 「端口」 to
// that file but the file carries no port/addr key at all, so reading one here would invent a
// 登记面 (PRD 22.2 第 7 条). This card therefore resolves the address as: an explicit -addr flag
// from cmd wins; otherwise the optional {CODE}_ADDR env key (HOMEOS_ADDR / FINANCE_ADDR) is used if
// set; otherwise the default is DERIVED from registry.Implemented() registration order (base port
// 8080 + index), so the two P1 services land on different ports (:8080 / :8081) and `make
// dev-homeos` + `make dev-finance` can both run outside containers (§十三). The {CODE}_ADDR key name
// and the derived default-port rule are unregistered faces and are reported for回写, NOT asserted
// as documented. Under `make up` each service is its own container (§10.1) so ports never collide
// regardless.
type Config struct {
	// Domain is the registry row this process serves; every name below is derived from it.
	Domain registry.Domain
	// DSN is the validated service DSN of §2.1's keyword/value form.
	DSN string
	// NATSURL is the JetStream endpoint (§3.1, PRD 10.4「连接形态 nats://{host}:{port}」).
	NATSURL string
	// Addr is the listen address (flag > {CODE}_ADDR env > registry-derived default).
	Addr string
	// UploadsRoot is the absolute, validated attachment root with the {family_id} placeholder
	// removed. Empty for a service that declares no UPLOAD_DIR key.
	UploadsRoot string
}

// ConfigRequest names the environment keys this service reads, so each cmd states its own inputs
// and a missing value fails with the key name in the message.
type ConfigRequest struct {
	Domain registry.Domain
	// DSNKey is the required DSN key of this service (§2.1「每个服务只用自己那串 DSN」).
	DSNKey string
	// NATSURLKey is the required NATS endpoint key.
	NATSURLKey string
	// UploadsDirKey is the attachment-directory key; empty means this service has none.
	UploadsDirKey string
	// Addr is the listen address handed in by cmd's -addr flag. Empty means "not given on the
	// command line" and is only meaningful when ResolveDefaultAddrWhenEmpty is set (see below);
	// without that opt-in an empty Addr is rejected, because a caller that requires an address must
	// supply one.
	Addr string
	// AddrEnvKey is the optional env key that carries the listen address when -addr is absent, e.g.
	// "HOMEOS_ADDR". Empty means "consult no env key" (used by tests that assert the flag-only
	// contract).
	AddrEnvKey string
	// ResolveDefaultAddrWhenEmpty lets LoadConfig fall back to the env key and then the
	// registry-derived default port when Addr is empty. cmd sets it true; tests that must reject an
	// empty Addr leave it false so the strict flag contract is preserved.
	ResolveDefaultAddrWhenEmpty bool
}

// placeholderValue is the unfilled value deploy/env.local.example documents:
// 「用法：cp deploy/env.local.example deploy/env.local，再把所有 CHANGE_ME 换掉」. Rejecting it is
// what turns a copied-but-unfilled env into a start-up error instead of a silent misconfiguration.
const placeholderValue = "CHANGE_ME"

// defaultListenPortBase is the first port of the derived default listen range. Neither the base nor
// the offset rule is registered in any document -- deploy/env.local.example (the file §十三 names for
// ports) carries no port key, and registry.go deliberately stores no port field ("Ports ... get no
// field here"). This is the card's minimal fallback so the two P1 services do not collide by
// default; it is reported for回写 rather than asserted as documented.
const defaultListenPortBase = 8080

// DefaultListenAddr derives a service's default listen address from its code WITHOUT a hand-copied
// code->port table: the port is defaultListenPortBase plus the code's index in
// registry.Implemented() (registration order). For P1 that is homeos -> :8080 and finance -> :8081,
// so `make dev-homeos` and `make dev-finance` can both run outside containers (§十三) instead of the
// previous :8080/:8080 bind conflict. The set and order come from the registry (the 唯一真源), so a
// new service born in a later phase takes the next free slot with no code change here; a code the
// table does not declare implemented is refused.
func DefaultListenAddr(code string) (string, error) {
	for i, d := range registry.Implemented() {
		if d.Code == code {
			return fmt.Sprintf(":%d", defaultListenPortBase+i), nil
		}
	}
	return "", fmt.Errorf("code %q 不在 registry.Implemented() 里，无法按登记序派生默认监听端口（§1.3 唯一真源）", code)
}

// LoadConfig reads and validates this service's environment. Every problem found is reported at
// once (errors.Join) and the caller exits: a service must not start 带病 (§2.1's account-is-permission
// boundary and §10.1's health gate both presuppose a process that refused to come up wrong).
func LoadConfig(req ConfigRequest) (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	cfg.Domain = req.Domain

	dsn, err := requiredEnv(req.DSNKey)
	if err != nil {
		errs = append(errs, err)
	} else if dsn, err = ValidateDSN(dsn, req.Domain); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", req.DSNKey, err))
	} else {
		cfg.DSN = dsn
	}

	natsURL, err := requiredEnv(req.NATSURLKey)
	if err != nil {
		errs = append(errs, err)
	} else if err := ValidateNATSURL(natsURL); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", req.NATSURLKey, err))
	} else {
		cfg.NATSURL = natsURL
	}

	// Listen-address resolution: explicit -addr flag wins; else the optional {CODE}_ADDR env key;
	// else a default derived from registry.Implemented() order (so the two P1 services don't collide).
	// The env-fallback/derive only runs when the caller opted in (cmd); otherwise an empty Addr stays
	// an error, preserving the flag-only contract other tests assert.
	addr := req.Addr
	addrProblem := false
	if addr == "" && req.ResolveDefaultAddrWhenEmpty {
		if req.AddrEnvKey != "" {
			v, present := os.LookupEnv(req.AddrEnvKey)
			v = strings.TrimSpace(v)
			switch {
			case !present || v == "":
				// Unset or blank: fall through to the derived default below.
			case v == placeholderValue:
				errs = append(errs, fmt.Errorf("环境变量 %s 仍是占位符 %s：填真实监听地址（host:port），或删掉该键以回落到按 registry.Implemented() 派生的默认端口", req.AddrEnvKey, placeholderValue))
				addrProblem = true
			default:
				addr = v
			}
		}
		if addr == "" && !addrProblem {
			def, err := DefaultListenAddr(req.Domain.Code)
			if err != nil {
				errs = append(errs, err)
				addrProblem = true
			} else {
				addr = def
			}
		}
	}

	if !addrProblem {
		if _, port, err := net.SplitHostPort(addr); err != nil {
			errs = append(errs, fmt.Errorf("监听地址 %q 不合法: %w（cmd 的 -addr 取值；缺省时依次取 %s 环境变量、按 registry.Implemented() 派生的默认端口；形如 :8080）",
				addr, err, req.AddrEnvKey))
		} else if port == "0" {
			errs = append(errs, fmt.Errorf("监听地址 %q 的端口为 0", addr))
		} else {
			cfg.Addr = addr
		}
	}

	if req.UploadsDirKey != "" {
		raw, err := requiredEnv(req.UploadsDirKey)
		if err != nil {
			errs = append(errs, err)
		} else {
			root, err := ResolveUploadsRoot(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s=%q: %w", req.UploadsDirKey, raw, err))
			} else {
				cfg.UploadsRoot = root
			}
		}
	}

	return cfg, errors.Join(errs...)
}

// requiredEnv reads one environment key and rejects absence, emptiness and the documented
// CHANGE_ME placeholder.
func requiredEnv(key string) (string, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("环境变量 %s 未设置（键名见 deploy/env.local.example）", key)
	}
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("环境变量 %s 为空", key)
	}
	if v == placeholderValue {
		return "", fmt.Errorf("环境变量 %s 仍是占位符 %s，请填真实值", key, placeholderValue)
	}
	return v, nil
}

// ValidateDSN enforces §2.1's documented DSN shape -- 「每个服务只用自己那串 DSN
// （user=hc_finance dbname=homecube search_path=finance）」 -- against this domain's registry row, and
// returns the value unchanged when it agrees.
//
// Three cells are checked, all of them naming authority rather than taste:
//
//   - user must equal Domain.DBAccount (hc_{code}). The account IS the isolation boundary
//     (PRD 22.2 第 2 条「禁止跨 schema SQL、DBLink、只读视图、共用账号」), so a shared or foreign
//     role must not be able to boot a service.
//   - search_path must equal Domain.DSNSearchPath() (search_path={code}), §1.3 row 2 校验点.
//   - dbname and password must be present and filled; the database name's VALUE is deliberately not
//     checked -- registry.go states that §2.1 registers one cluster and one database and that the
//     name is a deploy concern, so it is outside the domain table.
//
// A URL-form DSN is refused rather than guessed at, because §2.1's shape is the keyword/value one.
func ValidateDSN(raw string, d registry.Domain) (string, error) {
	if strings.Contains(raw, "://") {
		return "", fmt.Errorf("DSN 必须是 §2.1 的关键字/值形态（user=… dbname=… search_path=…），不是 URL 形态")
	}

	fields, err := parseDSNFields(raw)
	if err != nil {
		return "", err
	}

	var errs []error
	if got := fields["user"]; got != d.DBAccount {
		errs = append(errs, fmt.Errorf("user=%q，但 registry 登记的专属账号是 %s（§2.1「CREATE ROLE hc_{code} LOGIN」、PRD 22.2 第 2 条禁止共用账号）",
			got, d.DBAccount))
	}

	key, want, ok := strings.Cut(d.DSNSearchPath(), "=")
	if !ok {
		return "", fmt.Errorf("registry 的 DSNSearchPath() 返回值 %q 不是 key=value 形态", d.DSNSearchPath())
	}
	if got, present := fields[key]; !present {
		errs = append(errs, fmt.Errorf("DSN 缺少 %s=%s（§2.1 的 DSN 形态、§1.3 row 2 校验点「连接串 search_path={code}」）", key, want))
	} else if got != want {
		errs = append(errs, fmt.Errorf("DSN 的 %s=%s 与本域 schema %s 不符（§2.1「每个服务只用自己那串 DSN」）", key, got, want))
	}

	for _, required := range []string{"dbname", "password"} {
		v, present := fields[required]
		if !present || v == "" {
			errs = append(errs, fmt.Errorf("DSN 缺少 %s=（§2.1 的 CREATE ROLE ... LOGIN PASSWORD 给了口令，缺它连不上）", required))
		} else if v == placeholderValue {
			errs = append(errs, fmt.Errorf("DSN 的 %s 仍是占位符 %s", required, placeholderValue))
		}
	}

	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return raw, nil
}

// parseDSNFields reads the libpq keyword/value form: space-separated key=value pairs whose value may
// be single-quoted, with a doubled quote or a backslash as the escape for a quote inside it (the
// password is a quoted value in practice, and the env line itself is quoted once more by the shell
// before it gets here).
func parseDSNFields(raw string) (map[string]string, error) {
	fields := map[string]string{}

	for i := 0; i < len(raw); {
		for i < len(raw) && raw[i] == ' ' {
			i++
		}
		if i >= len(raw) {
			break
		}

		keyStart := i
		for i < len(raw) && raw[i] != '=' && raw[i] != ' ' {
			i++
		}
		key := raw[keyStart:i]
		if i >= len(raw) || raw[i] != '=' {
			return nil, fmt.Errorf("DSN 片段 %q 不是 key=value 形态", raw[keyStart:])
		}
		i++ // past '='

		var value string
		if i < len(raw) && raw[i] == '\'' {
			i++
			var b strings.Builder
			closed := false
			for i < len(raw) {
				switch {
				case raw[i] == '\\' && i+1 < len(raw):
					b.WriteByte(raw[i+1])
					i += 2
				case raw[i] == '\'' && i+1 < len(raw) && raw[i+1] == '\'':
					b.WriteByte('\'')
					i += 2
				case raw[i] == '\'':
					i++
					closed = true
				default:
					b.WriteByte(raw[i])
					i++
				}
				if closed {
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("DSN 的 %s 值有未闭合的单引号", key)
			}
			if i < len(raw) && raw[i] != ' ' {
				return nil, fmt.Errorf("DSN 的 %s 值闭合后紧跟内容 %q", key, raw[i:])
			}
			value = b.String()
		} else {
			valueStart := i
			for i < len(raw) && raw[i] != ' ' {
				i++
			}
			value = raw[valueStart:i]
		}

		if key == "" {
			return nil, fmt.Errorf("DSN 在偏移 %d 处有空的键名", keyStart)
		}
		if _, seen := fields[key]; seen {
			return nil, fmt.Errorf("DSN 的键 %s 出现两次，无法判定生效值", key)
		}
		fields[key] = value
	}

	if len(fields) == 0 {
		return nil, errors.New("DSN 为空")
	}
	return fields, nil
}

// ValidateNATSURL checks the endpoint against the form PRD 10.4 and §3.1 register:
// 「连接形态 nats://{host}:{port}，内网」.
func ValidateNATSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("NATS_URL 解析失败: %w", err)
	}
	switch u.Scheme {
	case "nats", "tls", "http", "https":
	default:
		return fmt.Errorf("NATS_URL 协议段是 %q，PRD 10.4 登记的连接形态是 nats://{host}:{port}", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("NATS_URL 缺 host:port")
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return fmt.Errorf("NATS_URL 的 host 段 %q 必须带端口（PRD 10.4「nats://{host}:{port}」）: %w", u.Host, err)
	}
	return nil
}

// familyIDSuffix is the placeholder segment PRD 14.7 and §8.1 write into the attachment path:
// 「附件目录 uploads/{family_id}/」/「单机磁盘 ./uploads/{family_id}/」. Interpolating it is S11-S12's
// job (the attachment write path); this card resolves and validates the ROOT only, so the value
// after stripping the placeholder is the directory the process is allowed to touch.
const familyIDSuffix = "{family_id}"

// ResolveUploadsRoot turns the documented UPLOAD_DIR template into an absolute attachment root and
// refuses a value that could land the process outside the data root it declared.
//
// The rules, and why each one is here:
//
//   - The value must contain {family_id} exactly once and as its LAST segment. PRD 14.7 registers the
//     shape uploads/{family_id}/; a value that does not carry the placeholder would make every
//     family share one directory, and a placeholder in the middle would make the "root" depend on
//     how S11-S12 interpolates it -- a question the documents leave open (reported).
//   - No ".." segment anywhere, before or after cleaning.
//   - Relative values are resolved against the process working directory, which is the data root
//     this card declares (§8.1 writes the path as ./uploads/{family_id}/, i.e. relative to cwd --
//     under make dev-{code} that cwd is server/ because the target runs `go -C server run`).
//   - Absolute values are refused: they escape the declared data root by construction, and the
//     documents only ever write the relative form. A container that needs an absolute root must set
//     its WORKDIR, which keeps the check meaningful.
//   - After cleaning and (when the path already exists) symlink resolution, the result must still be
//     inside the data root, and must not be an existing non-directory.
//
// The directory is NOT created: writing attachments is S11-S12.
func ResolveUploadsRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("UPLOAD_DIR 为空")
	}
	if strings.Contains(raw, "://") {
		return "", fmt.Errorf("UPLOAD_DIR 不是本地路径形态（PRD 14.7「附件目录 uploads/{family_id}/」）")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("无法确定数据根（进程工作目录）: %w", err)
	}
	cleaned := filepath.Clean(raw)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("UPLOAD_DIR 必须是相对数据根（进程 cwd）的路径，§8.1 写的是 ./uploads/{family_id}/，收到的是绝对路径 %q", cleaned)
	}

	// The .. test runs on the RAW segments, before cleaning: filepath.Clean resolves uploads/../evil
	// away, and a check placed after it would only ever see the leading ones.
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, p := range strings.Split(raw, string(filepath.Separator)) {
		if p == ".." {
			return "", fmt.Errorf("UPLOAD_DIR 含 .. 片段，拒绝（%q）", raw)
		}
	}
	if parts[len(parts)-1] != familyIDSuffix {
		return "", fmt.Errorf("UPLOAD_DIR 的最后一段必须是 %s（PRD 14.7 登记的形状是 uploads/%s/），收到 %q",
			familyIDSuffix, familyIDSuffix, raw)
	}
	for _, p := range parts[:len(parts)-1] {
		if p == familyIDSuffix {
			return "", fmt.Errorf("UPLOAD_DIR 的 %s 只允许出现一次且在最后一段（%q）", familyIDSuffix, raw)
		}
	}

	// Strip the placeholder segment: the root is the directory that will hold one sub-directory per
	// family.
	rootParts := parts[:len(parts)-1]
	if len(rootParts) == 0 || strings.Join(rootParts, string(filepath.Separator)) == "" {
		return "", fmt.Errorf("UPLOAD_DIR 去掉 %s 之后没有目录根（%q）", familyIDSuffix, raw)
	}

	rel := filepath.Join(rootParts...)
	root := filepath.Join(cwd, rel)

	if err := withinRoot(root, cwd); err != nil {
		return "", err
	}
	// A pre-existing path may be a symlink; resolving it is the only way to know where the write would
	// really land. Absence is not an error here -- the first attachment write creates it (S11-S12),
	// and this card declares no write path.
	if _, err := os.Lstat(root); err == nil {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("UPLOAD_DIR 的 %s 无法解析符号链接: %w", root, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return "", fmt.Errorf("UPLOAD_DIR 的 %s 无法检查: %w", real, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("UPLOAD_DIR 解析出的 %s 已存在且不是目录", real)
		}
		if err := withinRoot(real, cwd); err != nil {
			return "", err
		}
		root = real
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("UPLOAD_DIR 的 %s 无法检查: %w", root, err)
	}

	return root, nil
}

// withinRoot reports whether path stays inside root once both are absolute.
func withinRoot(path, root string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("无法判定 %s 是否在数据根 %s 内: %w", path, root, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("UPLOAD_DIR 解析结果 %s 逃出数据根（进程 cwd）%s", path, root)
	}
	return nil
}
