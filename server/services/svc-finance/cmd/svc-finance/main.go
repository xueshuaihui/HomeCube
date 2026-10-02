// Command svc-finance is the 财务面 service (docs/p1-tech-plan.md §1.1, §八; PRD 22.2 第 1 条).
//
// Its path is the one PRD 22.4 registers per service (「每服务一个 cmd」) and the one the root
// Makefile's dev-finance target runs with `go -C server run ./services/svc-finance/cmd/svc-finance`,
// so its cwd is server/ (§十三).
//
// Delivered here: config validation against registry row "finance", the two §10.1 health checks, the
// /api/finance/ route group and /metrics. Not delivered: the authz middleware (S4), the JWKS pull
// that FINANCE_JWKS_URL is for (§4.1, S4), the outbox 投递器 and consumers (S2), and every business
// handler of PRD 4.8 (S7-S13).
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
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/handler"
)

// code is this process's identity: one registry row, looked up rather than assumed.
const code = "finance"

// addrEnvKey is the OPTIONAL environment key that overrides the default listen address; like
// svc-homeos it is not a key deploy/env.local.example registers today (§十三 hands ports there but
// no port/addr entry exists), so the key name and derived default port are reported for回写. With
// the key absent the address falls back to the registry.Implemented()-derived default (finance is the
// second implemented row -> :8081), so this service and svc-homeos (:8080) no longer collide under
// `make dev-homeos` + `make dev-finance`.
const addrEnvKey = "FINANCE_ADDR"

func main() {
	addr := flag.String("addr", "", "监听地址（host:port）；缺省时取 $FINANCE_ADDR，仍无则按 registry.Implemented() 登记序派生默认端口")
	flag.Parse()

	if err := run(*addr); err != nil {
		slog.Error("svc-finance 启动失败", "err", err.Error())
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
		// FINANCE_DSN + NATS_URL. No UPLOAD_DIR: env.local.example records that the attachment
		// directory belongs to svc-homeos alone (PRD 14.7 -- finance_transaction.receipt only stores
		// a file_id), so reading it here would be a second data source.
		DSNKey:     "FINANCE_DSN",
		NATSURLKey: "NATS_URL",
		Addr:       addr,
		AddrEnvKey: addrEnvKey,
		// Empty -addr resolves through $FINANCE_ADDR then the registry-derived default (finance :8081).
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

	// Register business routes under the finance domain's route prefix (/api/finance)
	group := svc.Engine.Group(d.RoutePrefix)
	group.GET("/flow/list", handler.FlowListHandler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = svc.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
