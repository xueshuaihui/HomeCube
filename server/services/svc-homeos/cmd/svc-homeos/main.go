// Command svc-homeos is the HomeOS 底座 service (docs/p1-tech-plan.md §1.1, PRD 22.2 第 1 条).
//
// Shape of this file is fixed by two things outside it: PRD 22.4「每服务一个 cmd、一个镜像、一条发布
// 线」 and the root Makefile's dev-homeos target, which runs
// `go -C server run ./services/svc-homeos/cmd/svc-homeos` (docs/p1-tech-plan.md §十三). The process
// therefore lives at server/services/svc-homeos/cmd/svc-homeos and its cwd is server/.
//
// What is delivered here is the runnable skeleton plus its identity boundary: config validation, the
// two §10.1 health checks, the /api/homeos/ route group and /metrics, the token issuer (internal/auth)
// and the request authenticator mounted on every route that reads a family or a member. §1.1 states
// 「鉴权在每个服务的中间件里由同一 SDK 完成」, so the middleware installed below calls
// packages/authz for the claim rules and puts the resolved session into the gin context; the business
// handlers contain no family judgement of their own. The event side -- the outbox 投递器 and the
// durable consumer for finance.due.registered -- is wired below (PRD 3.4.6, §3.3, §3.4, §3.6): every
// step of it is fail-fast, because a service that comes up without its subscription would answer the
// 首页's B 区 with an empty cell and no error to show for it.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/xueshuaihui/HomeCube/server/packages/obs"
	svcauth "github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/consumer"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/handler"
)

// code is this process's identity: one registry row, looked up rather than assumed --
// obs.ResolveDomain refuses a code the table does not declare as built in the current phase.
const code = "homeos"

// dueEventSourceCode is the domain whose event this process consumes (§3.6「svc-finance: 写自己的实体
// （含 due_at）→ 同事务 outbox → finance.due.registered」，consumers: [svc-homeos] in
// contracts/events/finance.yaml）。它走 obs.ResolveDomain 而不是字符串拼接：来源域的流名 HC_FINANCE 与
// subject pattern finance.> 由 registry 派生，而未出生 / 未实建的域会在启动期就被拒（§1.3「一张域表是
// 唯一真源」），订阅一条不存在的流不是可以留到运行期的错。
const dueEventSourceCode = "finance"

// addrEnvKey is the OPTIONAL environment key that overrides the default listen address. It is not a
// key deploy/env.local.example registers today (§十三 hands ports to that file but the file carries
// no port/addr entry), so the key name and the derived default port are this card's unregistered
// choice and are reported for回写, not asserted as documented. When the key is absent the address
// falls back to a default derived from registry.Implemented() order (homeos -> :8080), so this
// service and svc-finance (:8081) no longer collide under `make dev-homeos` + `make dev-finance`.
const addrEnvKey = "HOMEOS_ADDR"

// Identity env keys, named by the same {CODE}_ convention as HOMEOS_DSN / NATS_URL / UPLOAD_DIR /
// HOMEOS_ADDR above and declared here because this process is the one that consumes them (through
// svcauth.NewSigner, which reads them and refuses to start without the private half):
//
//	svcauth.EnvPrivateKeyPEMPath = "HOMEOS_JWT_PRIVATE_KEY_PEM"  RS256 私钥 PEM 的文件路径
//	svcauth.EnvPrivateKeyPEM     = "HOMEOS_JWT_PRIVATE_KEY"      同一个 PEM 的内联形式（compose env 用）
//	svcauth.EnvKeyID             = "HOMEOS_JWT_KEY_ID"           jwks 里该密钥的 kid（可缺省，取指纹）
//
// deploy/env.local.example and deploy/docker-compose.yml register none of the three today -- that is
// a reported回写项 for the deploy card. The absence is a startup failure, never an in-process key:
// an ephemeral pair would invalidate every token on restart and change the public half that
// /.well-known/jwks.json publishes (PRD 15.6, tech plan §4.1).
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

	// The RS256 key pair is this process's own secret (tech plan §4.1「svc-homeos 是唯一签发方与 RS256
	// 私钥持有者」). Loading it happens before any route is mounted, and a missing key aborts the
	// start: obs.Open has already connected Postgres and NATS by now, so a key-less service would
	// otherwise come up healthy and answer 401 to every session it issued before the restart.
	signer, err := svcauth.NewSigner(svc.Logger)
	if err != nil {
		return err
	}

	// services bundles what the identity handlers need beyond the handle: the signer above, the SMS
	// channel behind its seam (P1's local implementation is the documented fixed test code, see
	// packages/adapter/DEPENDENCIES.md 短信行) and the logger the audit sink degrades to.
	//
	// RoutePrefix is passed to the SAME string the router is grouped by (trimmed of registry's trailing
	// slash, which is what a gin FullPath never carries): it is what builds the onboarding allowlist, so
	// the set of routes a family-less token may reach is derived from the mount, not from a copy.
	services := &handler.Services{
		DB:          svc.DB(),
		Signer:      signer,
		SMS:         svcauth.NewLocalFixedCodeProvider(),
		Logger:      svc.Logger,
		RoutePrefix: strings.TrimSuffix(d.RoutePrefix, "/"),
	}
	// The authenticator resolves the caller's member row per request (PRD 15.2「判定以 family_id 为
	// 界」) and audits every refusal (15.5「越权尝试全部落审计」), both through the repo layer.
	mw, err := services.Middleware()
	if err != nil {
		return err
	}

	group := svc.Engine.Group(d.RoutePrefix)

	// Public routes, exactly the three families PRD 22.2 第 8 条 and §1.1 leave unauthenticated:
	// /auth/* (the code, login, refresh and logout themselves), /legal/* (the P1 placeholder texts),
	// the key-set endpoint other services verify against, and the two operational endpoints, which obs
	// mounts at /healthz and /metrics.
	//
	// /.well-known/jwks.json is mounted UNDER the domain prefix and there is no root-level twin, for two
	// reasons that are both documentary: contracts/openapi/homeos.yaml declares `servers: - url:
	// /api/homeos`, so its /.well-known/jwks.json line IS /api/homeos/.well-known/jwks.json; and
	// deploy/env.local.example:129 hands the verifying side exactly that URL
	// (FINANCE_JWKS_URL="…/api/homeos/.well-known/jwks.json"), which is the path nginx proxies here at
	// all -- a root route would be unreachable through the one base URL PRD 17.7 第 5 条 allows clients
	// and peers to use, and 22.2 第 8 条 forbids a root business route.
	authGroup := group.Group("/auth")
	{
		authGroup.POST("/sms-code", func(c *gin.Context) {
			handler.SendSMSCode(c, services)
		})
		authGroup.POST("/login", func(c *gin.Context) {
			handler.Login(c, services)
		})
		authGroup.POST("/refresh", func(c *gin.Context) {
			handler.Refresh(c, services)
		})
		authGroup.POST("/logout", func(c *gin.Context) {
			handler.Logout(c, services)
		})
	}
	group.GET("/.well-known/jwks.json", func(c *gin.Context) {
		handler.GetJWKS(c, services)
	})

	group.GET("/legal/privacy-policy", handler.GetPrivacyPolicy)
	group.GET("/legal/user-agreement", handler.GetUserAgreement)

	// Identity-scoped routes: every one of these reads user_id / family_id / member_id from the gin
	// context, so all of them run behind mw.Handler(). Leaving any of them on `group` would put an
	// empty subject in front of a family query -- the shape this card was opened for.
	//
	// svcauth.Require is deliberately not called on /families, /family/switch, /family/invite/accept or
	// /search, and that is a fact about those routes rather than an omission: the authz matrix keys are
	// homeos:governance (成员/权限管理), homeos:module_config (面配置) and homeos:time_collab, and none of
	// them is one of those resources -- 「创建家庭」和「加入家庭」 are account-level acts, not governance
	// acts inside the family being created. The two /family/modules routes below ARE the gated kind
	// (PRD 15.3 「面配置」: R for member/ward/guest, A for owner), so each of them calls
	// svcauth.Require with homeos:module_config -- read on GET, update on PUT -- and the middleware is
	// only the first half of the gate.
	//
	// Which of these an ONBOARDING (family-less) token may reach is decided by svcauth's allowlist,
	// built from services.RoutePrefix above: POST/GET /families and GET /auth/me are in it, every other
	// route here answers 403 insufficient_scope. That is the gate, not this list's ordering.
	protected := group.Group("", mw.Handler())
	{
		familyGroup := protected.Group("/families")
		{
			familyGroup.POST("", func(c *gin.Context) {
				handler.CreateFamily(c, services)
			})
			familyGroup.GET("", func(c *gin.Context) {
				handler.ListFamilies(c, services)
			})
			
			// Invitation management (PRD 3.4.1 邀请成员)
			familyGroup.POST("/:family_id/invites", func(c *gin.Context) {
				handler.CreateInvite(c, services)
			})
			familyGroup.GET("/:family_id/invites", func(c *gin.Context) {
				handler.ListInvites(c, services)
			})
			familyGroup.DELETE("/:family_id/invites/:invite_id", func(c *gin.Context) {
				handler.RevokeInvite(c, services)
			})
		}

		// Session introspection: the one read a family-less caller needs to see its own state, and the
		// only /auth/* route behind the middleware (the four login-side routes above must stay public).
		protected.GET("/auth/me", func(c *gin.Context) {
			handler.Me(c, services)
		})

		protected.POST("/family/invite/accept", func(c *gin.Context) {
			handler.AcceptInvite(c, services)
		})
		protected.POST("/family/switch", func(c *gin.Context) {
			handler.SwitchFamily(c, services)
		})

		// 面配置 pair (contracts/openapi/homeos.yaml /family/modules, PRD 3.4.1 面配置 row, 17.8, 15.3).
		// These two handlers take the middleware as a third argument because svcauth.Require writes the
		// 15.5 denied-audit row through its OnDenied sink -- the role gate is therefore the same
		// authz call every other service makes, with the same refusal recorder, and it runs on the
		// session's role rather than on anything the request claims. They get `services` (DB + logger +
		// prefix) rather than the bare `svc.DB()` that /search passes, because one composition reads
		// homeos_family_module and the other writes audit + outbox rows in the same transaction.
		protected.GET("/family/modules", func(c *gin.Context) {
			handler.GetFamilyModules(c, services, mw)
		})
		protected.PUT("/family/modules", func(c *gin.Context) {
			handler.UpdateFamilyModule(c, services, mw)
		})

		// 首页四区聚合 (contracts/openapi/homeos.yaml /home/summary, PRD 17.2 「首页首屏只有一个业务请求」).
		// Third consumer of faces.go's composition, and mounted with the SAME (c, services, mw) shape and
		// the SAME read gate (homeos:module_config + read) as GET /family/modules above -- PRD 15.3 面配置
		// row grants that R 「只读，用于渲染首页矩阵…」, and 17.8 定版 ⑯ then裁剪 the set per role inside the
		// composition, so the two endpoints cannot answer one household differently (asserted in
		// home_summary_test.go). Not in svcauth's onboarding allowlist: it reads a family, so a family-less
		// token gets 403 insufficient_scope from the middleware rather than an empty family boundary.
		protected.GET("/home/summary", func(c *gin.Context) {
			handler.GetHomeSummary(c, services, mw)
		})

		// 消息中心 pair (contracts/openapi/homeos.yaml /notifications + /notifications/read, PRD 3.6
		// Notification row, 17.1 未读口径). Both handlers take (c, services, mw) like the three routes
		// above, and both gate on PRD 15.3 row 3 「homeos:time_collab」 -- the row whose comment enumerates
		// reminder/board/vote/DYNAMIC -- read on GET, update on POST, so member/ward/guest (M, which
		// permits read+update) can read and clear their own inbox while nothing on either route accepts a
		// family or member id from the request. Not in svcauth's onboarding allowlist either: a family-less
		// token has no inbox, and 403 insufficient_scope is the honest answer rather than an empty one.
		protected.GET("/notifications", func(c *gin.Context) {
			handler.GetNotifications(c, services, mw)
		})
		protected.POST("/notifications/read", func(c *gin.Context) {
			handler.MarkNotificationsRead(c, services, mw)
		})

		// 动态流 pair (contracts/openapi/homeos.yaml /dynamics + /dynamics/read, PRD 3.7 动态流, 17.2 D 区).
		// Same row of the matrix and the same (c, services, mw) shape: GET /home/summary's D 区 and this
		// route are two pages over ONE stream, and home_summary.go's DynamicCell is the type both
		// serialize, so the 首页 strip and the full feed cannot start describing the same event
		// differently (定版 ⑯'s 同源 rule applied to the fifth consumer, 17.8's 动态流筛选). Read state is
		// per member through homeos_dynamic_read, keyed by the session's member id.
		protected.GET("/dynamics", func(c *gin.Context) {
			handler.GetDynamics(c, services, mw)
		})
		protected.POST("/dynamics/read", func(c *gin.Context) {
			handler.MarkDynamicsRead(c, services, mw)
		})

		// Per PRD 14.5 #7: global keyword search, scoped to the session family by GetSearch.
		protected.GET("/search", func(c *gin.Context) {
			handler.GetSearch(c, svc.DB())
		})

		// Internal endpoints for authz SDK and cross-service read-only access (PRD 15.6).
		// Not in svcauth's onboarding allowlist: requires a family context.
		protected.GET("/members/snapshot", func(c *gin.Context) {
			handler.GetMembersSnapshot(c, svc.DB())
		})

		// App bundle management and version check (PRD 3.4.8, tech plan §9.2).
		// Still goes through middleware auth but returns no family data.
		protected.GET("/app/bundles", handler.GetAppBundles)
		protected.GET("/app/version", handler.GetAppVersion)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The event side (BUS-2): the outbox 投递器 that moves this service's own homeos_outbox rows into
	// HC_HOMEOS (§3.3, PRD 3.4.6 发布/幂等/重试/死信) plus the durable consumer that feeds
	// homeos_due_registration from finance.due.registered (§3.6, PRD 14.5 第 4 项「到期中心可注册可触
	// 发」). Both ends belong to this process's lifecycle: SetupBus returns an error for any step that
	// cannot be built -- 建流、连 JetStream、注册 consumer——and that error aborts the start rather than
	// degrading into 「broker 不可用就先不订阅」. A service that starts without its subscription is
	// healthy and answers B 区 empty, which is the failure mode this card exists to close.
	//
	// Stop is deferred, not called from a second signal path: the same ctx the HTTP shutdown uses
	// cancels the delivery loop, then the consumer and the deliverer come down before svc.Close()
	// releases the connections obs.Open made (defer LIFO).
	source, err := obs.ResolveDomain(dueEventSourceCode)
	if err != nil {
		return err
	}
	busRT, err := consumer.SetupBus(ctx, consumer.BusConfig{
		Logger:  svc.Logger,
		Own:     d,
		Source:  source,
		DSN:     cfg.DSN,
		NATSURL: cfg.NATSURL,
	})
	if err != nil {
		return err
	}
	defer busRT.Stop()

	err = svc.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
