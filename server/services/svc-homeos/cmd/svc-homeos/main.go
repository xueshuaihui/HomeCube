// Command svc-homeos is the HomeOS 底座 service (docs/p1-tech-plan.md §1.1, PRD 22.2 第 1 条).
//
// Shape of this file is fixed by two things outside it: PRD 22.4「每服务一个 cmd、一个镜像、一条发布
// 线」 and the root Makefile's dev-homeos target, which runs
// `go -C server run ./services/svc-homeos/cmd/svc-homeos` (docs/p1-tech-plan.md §十三). The process
// therefore lives at server/services/svc-homeos/cmd/svc-homeos and its cwd is server/.
//
// What is delivered here is the runnable skeleton only: config validation, the two §10.1 health
// checks, the /api/homeos/ route group and /metrics. Business handlers are S3 onwards, the authz
// middleware is S4 (§1.1「鉴权在每个服务的中间件里由同一 SDK 完成」 -- that SDK does not exist yet),
// and the outbox 投递器 with its durable consumers is S2.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xueshuaihui/HomeCube/server/packages/obs"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/handler"
)

// code is this process's identity: one registry row, looked up rather than assumed --
// obs.ResolveDomain refuses a code the table does not declare as built in the current phase.
const code = "homeos"

// addrEnvKey is the OPTIONAL environment key that overrides the default listen address. It is not a
// key deploy/env.local.example registers today (§十三 hands ports to that file but the file carries
// no port/addr entry), so the key name and the derived default port are this card's unregistered
// choice and are reported for回写, not asserted as documented. When the key is absent the address
// falls back to a default derived from registry.Implemented() order (homeos -> :8080), so this
// service and svc-finance (:8081) no longer collide under `make dev-homeos` + `make dev-finance`.
const addrEnvKey = "HOMEOS_ADDR"

func main() {
	addr := flag.String("addr", "", "监听地址（host:port）；缺省时取 $HOMEOS_ADDR，仍无则按 registry.Implemented() 登记序派生默认端口")
	flag.Parse()

	if err := run(*addr); err != nil {
		// The logger may not exist yet (config errors precede it), so failures go to stderr and the
		// process exits non-zero: a service must not start 带病 (§2.1, §10.1's health gate).
		slog.Error("svc-homeos 启动失败", "err", err.Error())
		os.Exit(1)
	}
}

func run(addr string) error {
	d, err := obs.ResolveDomain(code)
	if err != nil {
		return err
	}

	cfg, err := obs.LoadConfig(obs.ConfigRequest{
		Domain: d,
		// HOMEOS_DSN / NATS_URL / UPLOAD_DIR: the three keys of deploy/env.local.example this
		// process consumes. UPLOAD_DIR belongs to homeos alone -- env.local.example records that
		// the attachment directory is the homeos File table's (PRD 14.7, §8.1).
		DSNKey:        "HOMEOS_DSN",
		NATSURLKey:    "NATS_URL",
		UploadsDirKey: "UPLOAD_DIR",
		Addr:          addr,
		AddrEnvKey:    addrEnvKey,
		// Empty -addr resolves through $HOMEOS_ADDR then the registry-derived default (homeos :8080).
		ResolveDefaultAddrWhenEmpty: true,
	})
	if err != nil {
		return err
	}

	svc, err := obs.Open(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	// Register legal endpoints under the homeos domain's route prefix (/api/homeos)
	group := svc.Engine.Group(d.RoutePrefix)
	group.GET("/legal/privacy-policy", handler.GetPrivacyPolicy)
	group.GET("/legal/user-agreement", handler.GetUserAgreement)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = svc.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
