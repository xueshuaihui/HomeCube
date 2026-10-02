package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Service is one running HomeCube service: its registry row, its two documented dependencies, its
// operational endpoints and its Gin engine.
//
// The wiring lives here because PRD 22.4 gives every service its own cmd while §1.2 forbids
// services/A -> services/B imports -- two copies of this assembly would be two copies of the
// /healthz and route-domain rules, and §1.3's「一张域表是唯一真源」is about not having those.
type Service struct {
	Config  Config
	Logger  *slog.Logger
	Metrics *Metrics
	Health  *Health
	Engine  *gin.Engine

	db   *gorm.DB
	conn *nats.Conn
}

// Open connects the two dependencies of §1.1 and builds the engine. It does not bind a port yet and
// does not run the preflight -- that is Run, so a test can drive the engine without a listener.
func Open(cfg Config) (*Service, error) {
	log := NewLogger(cfg.Domain)

	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger: NewGormLogger(log),
		// The tables are created by the two migration sequences (PRD 22.2 第 6 条, §2.2); a service
		// must never alter schema at runtime (§2.1「建 schema 与建账号的动作…不由服务进程在运行期自触发」).
		// GORM does not auto-migrate by default; the comment records that this is a boundary, not an
		// omission.
	})
	if err != nil {
		return nil, fmt.Errorf("连接本域数据库（schema=%s）: %w", cfg.Domain.Schema, err)
	}

	conn, err := nats.Connect(cfg.NATSURL,
		// The connection identity is the registry's service name, so §3.1's streams show which
		// service is attached.
		nats.Name(cfg.Domain.ServiceName),
		// Reconnect forever: a NATS restart must not take the process down with it (§10.1 runs the
		// two services against one nats container, and 14.5 第 1 项 requires one service's restart to
		// not affect the other -- the symmetric case must hold too).
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("连接 JetStream（%s）: %w", cfg.NATSURL, err)
	}

	metrics := NewMetrics(cfg.Domain)
	health := NewHealth(cfg.Domain, log,
		SchemaCheck(cfg.Domain, db),
		JetStreamCheck(conn),
	)

	engine, err := newEngine(cfg.Domain, log, metrics, health)
	if err != nil {
		return nil, err
	}

	return &Service{
		Config:  cfg,
		Logger:  log,
		Metrics: metrics,
		Health:  health,
		Engine:  engine,
		db:      db,
		conn:    conn,
	}, nil
}

// DB returns the GORM database connection for this service.
// Services use this to access their schema-scoped database.
func (s *Service) DB() *gorm.DB {
	return s.db
}

// Run performs the startup preflight, then serves until ctx is cancelled, then shuts down.
func (s *Service) Run(ctx context.Context) error {
	if err := s.Health.Preflight(ctx); err != nil {
		return err
	}

	s.Logger.Info("service_started",
		"addr", s.Config.Addr,
		"route_prefix", s.Config.Domain.RoutePrefix,
		"schema", s.Config.Domain.Schema,
		"checks", s.Health.CheckNames(),
		"uploads_root", orNone(s.Config.UploadsRoot),
	)

	srv := &http.Server{
		Addr:    s.Config.Addr,
		Handler: s.Engine,
		// A read-header timeout is the defence against a client that opens a connection and sends
		// nothing; the documents set no number, so this is a card-level choice.
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		s.close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("监听 %s: %w", s.Config.Addr, err)
	case <-ctx.Done():
		// PRD 22.2 第 10 条只点名两个端点，没有写关闭必须留痕；但 14.5 第 1 项的判据是「杀一个服务，
		// 另一个仍可访问」，运维要能分辨「被 SIGTERM 正常关掉」和「崩了」，所以这里记两行。
		s.Logger.Info("service_stopping", "reason", ctx.Err(), "grace", shutdownGrace.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		s.close()
		if err != nil {
			return fmt.Errorf("优雅关闭: %w", err)
		}
		s.Logger.Info("service_stopped", "addr", s.Config.Addr)
		return nil
	}
}

// shutdownGrace bounds how long in-flight requests may finish after SIGTERM. Docker sends SIGTERM
// before the kill timeout (§10.1 compose), so a short grace keeps `make up` and a rollback snappy.
const shutdownGrace = 5 * time.Second

// Close releases the two dependency connections (used by tests and by Run).
func (s *Service) Close() { s.close() }

func (s *Service) close() {
	if s.db != nil {
		if sqlDB, err := s.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	if s.conn != nil {
		_ = s.conn.Drain()
		s.conn.Close()
	}
}

// orNone keeps the startup log honest about a service that owns no attachment directory: env.local
// .example records UPLOAD_DIR as the homeos side's key only.
func orNone(v string) string {
	if v == "" {
		return "<unused by this service>"
	}
	return v
}

// ResolveDomain turns a cmd's own code into its registry row, refusing a code the table does not
// declare as built in this checkout.
//
// The row is then the only source of names this process uses: RoutePrefix for the route group,
// Schema / DBAccount / DSNSearchPath for the DSN and the schema probe, TablePrefix for the
// prefix-table probe, ServiceName for the log and the NATS client identity (§1.3「一张域表是唯一真源」,
// §10.1 末条). The code string itself is this process's identity, declared once in its cmd, and it
// is not a second copy of the domain table: every fact about it is looked up here.
//
// The refusals are the three ways a skeleton can be wrong about 「服务随面出生」:
//   - an unregistered code -> PRD 11.7 第 1 条 / 定版 E ★ (no eighth naming domain);
//   - a registered but 未出生 code -> 「不建服务」 (PRD 卷首第 12 项, §1.2);
//   - a code the table does not declare Implemented -> same, via the Implemented() enumeration.
func ResolveDomain(code string) (registry.Domain, error) {
	d, ok := registry.ByCode(code)
	if !ok {
		var known []string
		for _, row := range registry.Domains() {
			known = append(known, row.Code)
		}
		return registry.Domain{}, fmt.Errorf("registry 里没有 code %q（已登记的七个: %s）",
			code, strings.Join(known, ", "))
	}

	implemented := false
	for _, row := range registry.Implemented() {
		if row.Code == d.Code {
			implemented = true
			break
		}
	}
	if !implemented {
		return registry.Domain{}, fmt.Errorf("code %q 未登记为实建（Implemented()==false, birth phase %s），"+
			"服务随面出生：本进程不该起来（PRD 卷首第 12 项、11.7 第 1 条、定版 E ★）", d.Code, d.BirthPhase)
	}
	if !d.BornAt(registry.CurrentPhase) {
		return registry.Domain{}, fmt.Errorf("code %q 的出生期 %s 晚于当期 %s，registry 的实建标记与出生期模型不一致",
			d.Code, d.BirthPhase, registry.CurrentPhase)
	}

	return d, nil
}

// newEngine assembles the routes and then proves they are all inside this domain's route prefix.
//
// The registration enumerates registry.Implemented() rather than a list written here (§1.3「这张表是
// 唯一真源」, §10.1 末条「当期两个服务都要可拉」): for every domain the table declares as really
// built, this process mounts a route group only when that domain IS this process's service. Every
// other implemented domain -- and every unborn one -- is refused below by assertOwnsNoForeignRoute.
func newEngine(d registry.Domain, log *slog.Logger, metrics *Metrics, health *Health) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()
	engine.Use(gin.Recovery(), metrics.Middleware(), LogRequests(log))

	for _, impl := range registry.Implemented() {
		if impl.Code != d.Code {
			// Another implemented service's prefix belongs to another process, another image and
			// another release line (PRD 22.4「每服务一个 cmd、一个镜像、一条发布线」).
			continue
		}
		group := engine.Group(impl.RoutePrefix)
		group.GET("healthz", health.Handler())
	}

	// The two operational endpoints in their documented literal form: PRD 22.2 第 10 条「暴露
	// /healthz、/metrics」 and §10.1 末条 write both without a prefix, while 22.2 第 8 条 demands
	// 「路由必须分域 /api/{code}/*」 and forbids 根路径业务接口. Both readings are served for
	// /healthz (the prefixed one is registered above from RoutePrefix); /metrics is served at the
	// documented root only, since it is not a business route and §10.1 末条 names it bare.
	// The ambiguity is reported as a document question rather than decided silently.
	engine.GET("/healthz", health.Handler())
	engine.GET("/metrics", metrics.Handler())

	engine.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"code":   d.Code,
			"status": "not_found",
			"detail": fmt.Sprintf("本进程只注册 %s 与两个运维端点（§10.1、PRD 22.2 第 8 条）", d.RoutePrefix),
		})
	})

	if err := assertOwnsNoForeignRoute(d, engine); err != nil {
		return nil, err
	}
	return engine, nil
}

// assertOwnsNoForeignRoute is the runtime half of 门禁 4's「CI 检查路由是否全部落在
// /api/{code}/*」(PRD 22.5 第 4 道, §1.3 row 3 校验点「Gin 路由组 AST 检查」). The AST check is S1-E's;
// a process that registered a route outside its own prefix would be caught here too, before it
// serves a single request.
//
// It enumerates registry.Domains(), so the five unborn domains are covered by the same loop as the
// two implemented ones: their prefixes must be answered by Nginx with 「即将上线」 (§1.1), never by
// this container.
func assertOwnsNoForeignRoute(d registry.Domain, engine *gin.Engine) error {
	ops := map[string]bool{
		"/healthz": true,
		"/metrics": true,
	}

	var errs []error
	for _, r := range engine.Routes() {
		if ops[r.Path] {
			continue
		}
		if strings.HasPrefix(r.Path, d.RoutePrefix) {
			continue
		}
		errs = append(errs, fmt.Errorf("路由 %s %s 不在本域 %s 内，也不是运维端点", r.Method, r.Path, d.RoutePrefix))
	}

	for _, other := range registry.Domains() {
		if other.Code == d.Code {
			continue
		}
		for _, r := range engine.Routes() {
			if strings.HasPrefix(r.Path, other.RoutePrefix) {
				errs = append(errs, fmt.Errorf("本进程注册了他域 %s 的前缀路由 %s %s（PRD 22.2 第 8 条、11.7 第 1 条）",
					other.Code, r.Method, r.Path))
			}
		}
	}

	return errors.Join(errs...)
}
