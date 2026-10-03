// Command svc-finance is the 财务面 service (docs/p1-tech-plan.md §1.1, §八; PRD 22.2 第 1 条).
//
// Its path is the one PRD 22.4 registers per service (「每服务一个 cmd」) and the one the root
// Makefile's dev-finance target runs with `go -C server run ./services/svc-finance/cmd/svc-finance`,
// so its cwd is server/ (§十三).
//
// Delivered here: config validation against registry row "finance", the two §10.1 health checks, the
// /api/finance/ route group, /metrics and the outbox 投递器 wired to a real JetStream connection
// (§3.1 stream HC_FINANCE). Not delivered: the authz middleware (S4), the JWKS pull
// that FINANCE_JWKS_URL is for (§4.1, S4), the consumers (S2), and every business
// handler of PRD 4.8 (S7-S13).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/adapter/asr"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/obs"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/handler"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/service"

	"gorm.io/gorm"
)

// outboxPublisher adapts bus.InsertOutboxMessageWithFamily to service.BusPublisher.
type outboxPublisher struct {
	code string
}

func (p *outboxPublisher) Publish(ctx context.Context, db *gorm.DB, subject string, envelope map[string]any) error {
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	familyID, _ := envelope["family_id"].(string)
	return bus.InsertOutboxMessageWithFamily(db, p.code, familyID, subject, string(data))
}

// code is this process's identity: one registry row, looked up rather than assumed.
const code = "finance"

// addrEnvKey is the OPTIONAL environment key that overrides the default listen address; like
// svc-homeos it is not a key deploy/env.local.example registers today (§十三 hands ports there but
// no port/addr entry exists), so the key name and derived default port are reported for回写. With
// the key absent the address falls back to the registry.Implemented()-derived default (finance is the
// second implemented row -> :8081), so this service and svc-homeos (:8080) no longer collide under
// `make dev-homeos` + `make dev-finance`.
const addrEnvKey = "FINANCE_ADDR"

// The two 「保留」 cells of §3.1's 流规划 table for this service's stream:
// 「HC_HOMEOS | homeos.> | max_age=90d，R=1」 and 「HC_FINANCE | finance.> | 同上 | 建」. Both numbers
// are therefore read off the document, not coined: 90 days of retention and R=1 (single node --
// 「不做集群」 per PRD 14.6). They are consts rather than env keys because §十三's env file registers
// no retention key and a home deployment must not be able to shrink the hot window below what
// §3.1 and PRD 21.4 「热 90 天」 promise.
const (
	eventStreamMaxAge   = 90 * 24 * time.Hour
	eventStreamReplicas = 1
)

// streamCreateTimeout bounds how long startup waits for the 「建流」 call. §3.1/§10.1 register no
// number for it (card-level choice, reported): it only has to be short enough that a broker that
// accepts TCP but never answers the JetStream API cannot leave the process hanging before
// fail-fast.
const streamCreateTimeout = 10 * time.Second

// newEventBus connects this service's JetStream endpoint (§3.1 载体, PRD 10.4) and ensures this
// domain's own stream exists, returning the wrapper the outbox 投递器 publishes through.
//
// Every name comes from the registry row (§1.3 「一张域表是唯一真源」), not from a string written
// here: Domain.StreamName() is HC_FINANCE and Domain.StreamSubjectPattern() is finance.> (§3.1 流表),
// so this service can neither create nor publish to another domain's stream.
//
// Both steps fail fast: an error here means the process does not start. The previous behaviour --
// building the deliverer over a nil wrapper -- wrote outbox rows that nothing ever moved into the
// bus, which is the defect this path closes (§3.3 「投递器: 每 500ms 批量 100 行 → JetStream Publish」).
// Falling back to nil when the broker is unreachable would be that same defect with new wording, and
// it is the same stance svc-homeos takes for a missing signing key: a service must not start 带病.
//
// CreateStream is CreateOrUpdateStream under the hood (packages/bus), so replaying `make dev-finance`
// against an existing stream is a no-op.
func newEventBus(d registry.Domain, url string) (bus.JetStreamWrapper, error) {
	js, err := bus.NewJetStreamWrapper(bus.JetStreamConfig{
		URL:      url,
		Code:     d.Code,
		MaxAge:   eventStreamMaxAge,
		Replicas: eventStreamReplicas,
	})
	if err != nil {
		return nil, fmt.Errorf("为投递器连接 JetStream（%s，§3.1 的流载体；obs.Open 的健康检查连接是另一条）: %w", url, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), streamCreateTimeout)
	defer cancel()

	if err := js.CreateStream(ctx, d.StreamName(), []string{d.StreamSubjectPattern()}); err != nil {
		js.Close()
		return nil, fmt.Errorf("确保本域流 %s（subjects %s）存在: %w", d.StreamName(), d.StreamSubjectPattern(), err)
	}

	return js, nil
}

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize finance repository, services, and handler
	financeRepo := repo.NewFinanceRepo(svc.DB())
	balanceService := service.NewBalanceService(svc.DB(), financeRepo)
	statisticsService := service.NewStatisticsService(svc.DB())
	exportService := service.NewExportService(svc.DB())

	// Initialize ASR adapter (stub for now)
	asrAdapter := asr.NewStubAdapter()

	// Connect the bus BEFORE the deliverer: the wrapper is a real JetStream connection and this
	// domain's stream HC_FINANCE is ensured on startup. A broker that will not answer means the
	// process does not come up at all (§3.3's 投递器 is the only thing that moves finance_outbox rows
	// into JetStream, so a process without it would accept writes whose events silently pile up).
	js, err := newEventBus(d, cfg.NATSURL)
	if err != nil {
		return err
	}
	// Deferred after svc.Close() and before the deliverer's own defer, so unwinding at exit is
	// Stop() -> Close() -> svc.Close(): the deliverer is drained before the connection it publishes
	// over is closed.
	defer js.Close()

	svc.Logger.Info("eventbus_ready",
		"stream", d.StreamName(),
		"subjects", d.StreamSubjectPattern(),
		"nats_url", cfg.NATSURL,
		"max_age", eventStreamMaxAge.String(),
		"replicas", eventStreamReplicas,
	)

	// Initialize outbox deliverer for async event publishing
	outboxDeliverer := bus.NewOutboxDeliverer(
		svc.DB(),
		js,
		bus.OutboxConfig{Code: code},
		func(msg string) { slog.Warn("outbox alert", "msg", msg) },
	)
	defer outboxDeliverer.Stop()

	// Start the outbox deliverer
	outboxDeliverer.Start(context.Background())

	// Initialize budget alert service
	budgetAlertService := service.NewBudgetAlertService(financeRepo, svc.DB())
	// The bill service owns the transaction that commits the bill row together with its
	// finance.due.registered outbox row (PRD 卷首第 3 条: 带时间语义的业务对象一律注册到 HomeOS).
	billService := service.NewBillService(financeRepo, svc.DB(), code)

	// Initialize recurring service for automatic periodic transaction posting
	recurringService := service.NewRecurringService(financeRepo, &outboxPublisher{code: code}, code)

	financeHandler := handler.NewFinanceHandler(financeRepo, balanceService, statisticsService, budgetAlertService, exportService, billService)
	voiceHandler := handler.NewVoiceHandler(financeRepo, asrAdapter)

	// Start background worker to execute due recurring rules every minute
	go func(ctx context.Context) {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		svc.Logger.Info("recurring_worker_started", "interval", "1m")

		for {
			select {
			case <-ticker.C:
				if err := recurringService.ExecuteDueRecurringRules(ctx, svc.DB()); err != nil {
					svc.Logger.Error("recurring_execution_failed", "err", err.Error())
				}
			case <-ctx.Done():
				svc.Logger.Info("recurring_worker_stopped")
				return
			}
		}
	}(ctx)

	// Register business routes under the finance domain's route prefix (/api/finance)
	group := svc.Engine.Group(d.RoutePrefix)

	// Existing flow list endpoint (mock)
	group.GET("/flow/list", handler.FlowListHandler)

	// Account endpoints
	group.POST("/accounts", financeHandler.CreateAccount)
	group.GET("/accounts", financeHandler.ListAccounts)
	group.PUT("/accounts/:id/archive", financeHandler.ArchiveAccount)

	// Category endpoints
	group.POST("/categories", financeHandler.CreateCategory)
	group.GET("/categories", financeHandler.ListCategories)
	group.PUT("/categories/:id/deactivate", financeHandler.DeactivateCategory)

	// Transaction endpoints
	group.POST("/transactions", financeHandler.CreateTransaction)
	group.GET("/transactions", financeHandler.ListTransactions)
	group.GET("/transactions/:id", financeHandler.GetTransaction)
	group.PUT("/transactions/:id", financeHandler.UpdateTransaction)
	group.DELETE("/transactions/:id", financeHandler.DeleteTransaction)

	// Ledger endpoints
	group.POST("/ledgers", financeHandler.CreateLedger)
	group.GET("/ledgers", financeHandler.ListLedgers)

	// Balance endpoints
	group.GET("/accounts/:id/balance", financeHandler.GetAccountBalance)

	// Statistics endpoints
	group.GET("/statistics/overview", financeHandler.GetOverviewStats)
	group.GET("/statistics/trend", financeHandler.GetTrendStats)
	group.GET("/statistics/category", financeHandler.GetCategoryStats)
	group.GET("/statistics/member", financeHandler.GetMemberStats)

	// Budget endpoints
	group.POST("/budgets", financeHandler.CreateBudget)
	group.GET("/budgets", financeHandler.ListBudgets)

	// Bill endpoints
	group.POST("/bills", financeHandler.CreateBill)
	group.GET("/bills", financeHandler.ListBills)
	group.PUT("/bills/:id/pay", financeHandler.PayBill)

	// Export endpoint
	group.GET("/export", financeHandler.ExportTransactions)

	// Voice entry endpoint
	group.POST("/voice-entry", voiceHandler.VoiceEntry)

	// Loan endpoints
	group.POST("/loans", financeHandler.CreateLoan)
	group.GET("/loans", financeHandler.ListLoans)
	group.PUT("/loans/:id/payoff", financeHandler.PayOffLoan)
	group.GET("/loans/:id/repayment-plans", financeHandler.GetRepaymentPlans)

	// Repayment plan endpoints
	group.PUT("/repayment-plans/:id/pay", financeHandler.PayRepaymentPlan)

	// Goal endpoints
	group.POST("/goals", financeHandler.CreateGoal)
	group.GET("/goals", financeHandler.ListGoals)
	group.PUT("/goals/:id/progress", financeHandler.UpdateGoalProgress)

	// Split settlement endpoints (S17-S18)
	group.POST("/split-settlements", financeHandler.CreateSplitSettlement)
	group.GET("/split-settlements", financeHandler.ListSplitSettlements)
	group.PUT("/split-settlements/:id/participants", financeHandler.AddParticipant)
	group.PUT("/split-settlements/:id/settle", financeHandler.SettleSplit)
	group.GET("/split-settlements/:id/participants", financeHandler.GetParticipants)

	// Credit card endpoints (S17-S18)
	group.POST("/credit-cards", financeHandler.CreateCreditCard)
	group.GET("/credit-cards", financeHandler.ListCreditCards)
	group.PUT("/credit-cards/:id/balance", financeHandler.UpdateCreditCardBalance)

	// Invoice endpoints (S17-S18)
	group.POST("/invoices", financeHandler.CreateInvoice)
	group.GET("/invoices", financeHandler.ListInvoices)
	group.PUT("/invoices/:id/reimburse", financeHandler.ReimburseInvoice)

	// Asset-liability report endpoints (S17-S18)
	group.POST("/reports/asset-liability", financeHandler.GenerateAssetLiabilityReport)
	group.GET("/reports/asset-liability", financeHandler.GetAssetLiabilityReport)

	// Recurring rule endpoints (P1-M1: 周期记账规则)
	group.GET("/recurring", financeHandler.ListRecurringRules)
	group.POST("/recurring", financeHandler.CreateRecurringRule)
	group.PUT("/recurring/:id", financeHandler.UpdateRecurringRule)
	group.DELETE("/recurring/:id", financeHandler.DeleteRecurringRule)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = svc.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
