package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Tests of the route assembly: docs/p1-tech-plan.md §1.1/§1.3 row 3 (路由 /api/{code}/*, 2 组 +
// 5 个 404 前缀), §10.1 末条 (both operational endpoints), PRD 22.2 第 8 条 (「不允许出现根路径业务
// 接口」) and 22.5 第 4 道 (「CI 检查路由是否全部落在 /api/{code}/*」 -- this is its runtime twin).

func newTestEngine(t *testing.T, d registry.Domain, checks ...Check) *gin.Engine {
	t.Helper()

	log := NewLogger(d)
	m := NewMetrics(d)
	if len(checks) == 0 {
		checks = []Check{upCheck("schema:" + d.Schema), upCheck("jetstream")}
	}
	engine, err := newEngine(d, log, m, NewHealth(d, log, checks...))
	if err != nil {
		t.Fatalf("newEngine(%s): %v", d.Code, err)
	}
	return engine
}

func routeSet(engine *gin.Engine) map[string]bool {
	set := map[string]bool{}
	for _, r := range engine.Routes() {
		set[r.Path] = true
	}
	return set
}

func TestEngineRegistersItsOwnPrefixAndTheTwoOperationalEndpoints(t *testing.T) {
	implemented := registry.Implemented()
	if len(implemented) == 0 {
		t.Fatal("registry.Implemented() is empty: nothing to assert")
	}

	for _, d := range implemented {
		t.Run(d.Code, func(t *testing.T) {
			engine := newTestEngine(t, d)
			routes := routeSet(engine)

			for _, want := range []string{
				d.RoutePrefix + "healthz", // the route-group form, built from registry RoutePrefix
				"/healthz",                // the literal of PRD 22.2 第 10 条 / §10.1 末条
				"/metrics",
			} {
				if !routes[want] {
					t.Errorf("no route for %s; routes = %v", want, engine.Routes())
				}
			}

			// 22.2 第 8 条: the route group is the only business surface, and this process registers
			// no other domain's prefix -- neither an implemented sibling nor an unborn one.
			for _, r := range engine.Routes() {
				if r.Path == "/healthz" || r.Path == "/metrics" {
					continue
				}
				if !strings.HasPrefix(r.Path, d.RoutePrefix) {
					t.Errorf("route %s %s is outside %s and is not an operational endpoint", r.Method, r.Path, d.RoutePrefix)
				}
			}
		})
	}
}

// TestEngineServesNoForeignPrefix drives the request through the sibling service's engine: svc-finance
// must answer 404 for /api/homeos/*, which is what makes 「svc-homeos 单独重启不影响 svc-finance」 a
// statement about two processes rather than one binary with two route groups.
func TestEngineServesNoForeignPrefix(t *testing.T) {
	implemented := registry.Implemented()
	for _, mine := range implemented {
		for _, other := range registry.Domains() {
			if other.Code == mine.Code {
				continue
			}
			engine := newTestEngine(t, mine)

			for _, path := range []string{other.RoutePrefix, other.RoutePrefix + "healthz", other.RoutePrefix + "transactions"} {
				rec := httptest.NewRecorder()
				engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s answered GET %s with %d, want 404 (route domain of %s, PRD 22.2 第 8 条)",
						mine.ServiceName, path, rec.Code, other.Code)
				}
			}
		}
	}
}

func TestHealthzIsReachableThroughTheRouteGroup(t *testing.T) {
	d, _ := registry.ByCode("finance")
	engine := newTestEngine(t, d, downCheck("schema:finance", context.DeadlineExceeded), upCheck("jetstream"))

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, d.RoutePrefix+"healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("prefixed /healthz = %d, want 503 when the schema probe fails; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"finance"`) {
		t.Errorf("body does not name the registry code: %s", rec.Body.String())
	}
}

func TestMetricsEndpointIsScrapeableAndCarriesTheCodeLabel(t *testing.T) {
	for _, d := range registry.Implemented() {
		engine := newTestEngine(t, d)

		// One request through the route group so a business-side series exists, then the scrape.
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, d.RoutePrefix+"healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s healthz = %d, want 200 with both checks up: %s", d.Code, rec.Code, rec.Body.String())
		}

		rec = httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /metrics = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{`http_requests_total`, `http_request_duration_seconds`, `code="` + d.Code + `"`} {
			if !strings.Contains(body, want) {
				t.Errorf("/metrics of %s is missing %s", d.Code, want)
			}
		}
	}
}

func TestUnknownRouteIs404NotAStubSuccess(t *testing.T) {
	d, _ := registry.ByCode("homeos")
	engine := newTestEngine(t, d)

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/homeos/transactions", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET %stransactions = %d, want 404: this card delivers no business handler",
			d.RoutePrefix, rec.Code)
	}
}

// TestAssertOwnsNoForeignRouteCatchesAMistake: the guard is only real if it fires. An engine with a
// foreign prefix registered must be refused before it serves anything.
func TestAssertOwnsNoForeignRouteCatchesAMistake(t *testing.T) {
	mine, _ := registry.ByCode("finance")
	foreign, _ := registry.ByCode("homeos")

	engine := gin.New()
	engine.GET(mine.RoutePrefix+"healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/metrics", func(c *gin.Context) { c.Status(http.StatusOK) })
	// The mistake: someone mounts a route in another domain's route domain.
	engine.GET(foreign.RoutePrefix+"dead-letters", func(c *gin.Context) { c.Status(http.StatusOK) })

	err := assertOwnsNoForeignRoute(mine, engine)
	if err == nil {
		t.Fatal("assertOwnsNoForeignRoute accepted a foreign-prefix route")
	}
	if !strings.Contains(err.Error(), foreign.RoutePrefix) {
		t.Errorf("error = %v, want it to name the offending prefix %s", err, foreign.RoutePrefix)
	}

	// And the correct engine passes.
	clean := gin.New()
	clean.GET(mine.RoutePrefix+"healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	clean.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	clean.GET("/metrics", func(c *gin.Context) { c.Status(http.StatusOK) })
	if err := assertOwnsNoForeignRoute(mine, clean); err != nil {
		t.Errorf("assertOwnsNoForeignRoute rejected a correct engine: %v", err)
	}
}

func TestResolveDomainRefusesWhatTheTableDoesNotBuild(t *testing.T) {
	for _, d := range registry.Implemented() {
		got, err := ResolveDomain(d.Code)
		if err != nil {
			t.Errorf("ResolveDomain(%q) = %v, want the registry row", d.Code, err)
			continue
		}
		if got != d {
			t.Errorf("ResolveDomain(%q) returned %+v, want %+v", d.Code, got, d)
		}
	}

	// 未出生: the five remaining rows are registry entries only (PRD 卷首第 12 项, 11.7 第 1 条).
	for _, d := range registry.Unborn() {
		if _, err := ResolveDomain(d.Code); err == nil {
			t.Errorf("ResolveDomain(%q) started a service for a domain born in %s", d.Code, d.BirthPhase)
		} else if !strings.Contains(err.Error(), d.BirthPhase) {
			t.Errorf("ResolveDomain(%q) error = %v, want it to cite the birth phase %s", d.Code, err, d.BirthPhase)
		}
	}

	if _, err := ResolveDomain("travel"); err == nil {
		t.Error("ResolveDomain accepted an unregistered code")
	} else if !strings.Contains(err.Error(), "registry 里没有") {
		t.Errorf("error = %v, want it to say the code is not registered", err)
	}
}
