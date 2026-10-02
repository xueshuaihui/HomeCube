package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Check is one liveness probe of this service. docs/p1-tech-plan.md §10.1 末条 fixes what /healthz
// must report -- 「本 schema 可连 + JetStream 已连」 -- and nothing else, so exactly two checks are
// registered by NewHealth and both are live connections rather than cached flags.
type Check struct {
	// Name is the check's identifier in the response, e.g. "schema:finance".
	Name string
	// Probe runs the real round trip. It is invoked per /healthz request and once at startup
	// (Preflight), so a dependency that goes away after boot shows up as a failed check.
	Probe func(context.Context) error
}

// Health aggregates a service's checks.
type Health struct {
	domain  registry.Domain
	checks  []Check
	timeout time.Duration
	log     *slog.Logger
}

// healthTimeout is how long one /healthz request may spend probing. Two seconds is short enough for
// the compose health check §10.1 runs every service under and long enough for a same-host Postgres
// round trip; the documents give no number, so this is reported as a card-level choice.
const healthTimeout = 2 * time.Second

// NewHealth builds the checker set for one service.
func NewHealth(d registry.Domain, log *slog.Logger, checks ...Check) *Health {
	return &Health{domain: d, checks: checks, timeout: healthTimeout, log: log}
}

// Checks returns the registered check names, used by Preflight's message and by the startup log.
func (h *Health) CheckNames() []string {
	names := make([]string, 0, len(h.checks))
	for _, c := range h.checks {
		names = append(names, c.Name)
	}
	return names
}

// result is one check's answer in the response body.
type result struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Status values of a check and of the whole endpoint. The documents say /healthz must 「报告『本
// schema 可连 + JetStream 已连』」 but register no code or wording, so these two pairs are this
// card's choice and are reported as such.
const (
	statusUp   = "up"
	statusDown = "down"

	healthOK       = "ok"
	healthFailure  = "unavailable"
	preflightLabel = "startup preflight"
)

// probe runs every check with the shared timeout and returns the results plus the count of failed
// ones.
func (h *Health) probe(ctx context.Context) ([]result, int) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	results := make([]result, 0, len(h.checks))
	failed := 0
	for _, c := range h.checks {
		r := result{Name: c.Name, Status: statusUp}
		if err := c.Probe(ctx); err != nil {
			r.Status = statusDown
			r.Detail = err.Error()
			failed++
			h.log.Warn("healthcheck_failed",
				slog.String("check", c.Name),
				slog.String("err", err.Error()),
			)
		}
		results = append(results, r)
	}
	return results, failed
}

// Handler serves GET /healthz.
//
// Failure semantics (a card-level choice; §10.1 only says what the endpoint must report):
//
//   - every check up  -> 200 with status "ok";
//   - any check down   -> 503 with status "unavailable" and that check's Detail. There is no 200 for
//     a degraded service: a 200 would make the compose health check (§10.1) and 14.5 第 9 项's
//     「当期存在的每个服务的 /healthz 均可被拉取」 indistinguishable from a healthy one.
//   - a probe that panics or blocks is cut off by healthTimeout and reported as a failed check.
//
// The body is the same shape in both states, so a caller can always read the per-check list.
func (h *Health) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		results, failed := h.probe(c.Request.Context())

		status := healthOK
		code := http.StatusOK
		if failed > 0 {
			status = healthFailure
			code = http.StatusServiceUnavailable
		}

		c.Header("Content-Type", "application/json; charset=utf-8")
		c.Writer.WriteHeader(code)
		// Encoding by hand instead of c.JSON so the response cannot mask a marshal error as a 200.
		_ = json.NewEncoder(c.Writer).Encode(gin.H{
			"code":    h.domain.Code,
			"service": h.domain.ServiceName,
			"status":  status,
			"checks":  results,
		})
	}
}

// Preflight runs the checks once at startup.
//
// The documents require the service to be able to report both dependencies and give no grace state:
// §2.1's account-is-the-boundary model and §10.1's 「健康检查」 both assume a process that came up
// against real dependencies, so a failing probe here stops the start instead of leaving a process
// serving 503 forever. The error lists every failing check with its detail.
func (h *Health) Preflight(ctx context.Context) error {
	results, failed := h.probe(ctx)
	if failed == 0 {
		return nil
	}
	var errs []error
	for _, r := range results {
		if r.Status == statusDown {
			errs = append(errs, fmt.Errorf("%s: %s", r.Name, r.Detail))
		}
	}
	return fmt.Errorf("%s: %d/%d 项检查未通过 -> %w", preflightLabel, failed, len(results), errors.Join(errs...))
}

// SchemaCheck is the 「本 schema 可连」 probe (§10.1 末条).
//
// Three things, each a real round trip on the service's own DSN:
//
//  1. Ping -- the connection is alive.
//  2. current_schema() must equal Domain.Schema -- this proves the DSN's search_path took effect and
//     that the account can actually see its own schema (§2.1「GRANT USAGE ON SCHEMA {code}」). If the
//     schema were dropped or missing from the search path, current_schema() comes back empty or
//     wrong and the check fails rather than quietly reading another schema.
//  3. at least one table whose name starts with Domain.TablePrefix is visible to this account --
//     §1.3 row 5 puts the {code}_ prefix inside the schema as 「双保险」, and information_schema.tables
//     only lists objects the current role has some privilege on, so this query proves both that the
//     sequence ran (a schema with no tables is an unmigrated one) and that the grants are in place.
//     The prefix comes from the registry row, so no table name is spelled here.
func SchemaCheck(d registry.Domain, db *gorm.DB) Check {
	return Check{
		Name: fmt.Sprintf("schema:%s", d.Schema),
		Probe: func(ctx context.Context) error {
			if db == nil {
				return errors.New("本服务没有数据库句柄（§2.1 的 DSN 未连上）")
			}
			sqlDB, err := db.DB()
			if err != nil {
				return fmt.Errorf("取底层连接池: %w", err)
			}
			if err := sqlDB.PingContext(ctx); err != nil {
				return fmt.Errorf("Ping 本服务 DSN 失败: %w", err)
			}

			var gotSchema *string
			if err := sqlDB.QueryRowContext(ctx, "SELECT current_schema()").Scan(&gotSchema); err != nil {
				return fmt.Errorf("查询 current_schema() 失败: %w", err)
			}
			if gotSchema == nil || *gotSchema != d.Schema {
				got := "<null>"
				if gotSchema != nil {
					got = *gotSchema
				}
				return fmt.Errorf("current_schema()=%s，与本域 schema %s 不符（§2.1 的 search_path 未生效或 schema 不存在）",
					got, d.Schema)
			}

			var prefixed int
			// Exact prefix match rather than LIKE, because {code}_ contains the LIKE single-character
			// wildcard.
			if err := sqlDB.QueryRowContext(ctx,
				"SELECT count(*) FROM information_schema.tables"+
					" WHERE table_schema = $1 AND substr(table_name, 1, length($2)) = $2",
				d.Schema, d.TablePrefix).Scan(&prefixed); err != nil {
				return fmt.Errorf("查询本域前缀表失败: %w", err)
			}
			if prefixed == 0 {
				return fmt.Errorf("schema %s 内没有任何 %s 前缀的表对本账号可见（迁移序列未重放，或 §2.1 的 GRANT 未落）",
					d.Schema, d.TablePrefix)
			}
			return nil
		},
	}
}

// JetStreamCheck is the 「JetStream 已连」 probe (§10.1 末条).
//
// It re-derives a JetStream context from the live connection and asks the server for the account's
// JetStream status (JS.API.INFO). That is the lightest call that proves all three things the check
// claims: the TCP connection is up, the server has JetStream enabled, and this account may use it.
//
// It deliberately does NOT ask for this domain's stream (Domain.StreamName(), HC_{CODE}): creating
// the streams is S2's work (§3.1「P1 状态: 建」 is the流规划, and env.local.example says 「流的创建与
// subject 授权不在本文件配置，属 S2 总线卡」). Requiring a stream that no card has created yet would
// make /healthz fail on a correct checkout.
func JetStreamCheck(conn *nats.Conn) Check {
	return Check{
		Name: "jetstream",
		Probe: func(ctx context.Context) error {
			if conn == nil {
				return errors.New("没有 NATS 连接")
			}
			if !conn.IsConnected() {
				return fmt.Errorf("与 %v 的连接已断开（lasterr: %v）", conn.Servers(), conn.LastError())
			}
			js, err := conn.JetStream()
			if err != nil {
				return fmt.Errorf("取 JetStream 上下文: %w", err)
			}
			info, err := js.AccountInfo(nats.Context(ctx))
			if err != nil {
				return fmt.Errorf("JetStream AccountInfo 失败（§3.1 的四条流跑在这个载体上）: %w", err)
			}
			if info == nil {
				return errors.New("JetStream AccountInfo 返回空")
			}
			return nil
		},
	}
}
