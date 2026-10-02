package obs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Tests of the /healthz contract: docs/p1-tech-plan.md §10.1 末条「每服务 /healthz 报告『本 schema
// 可连 + JetStream 已连』」 and PRD 14.5 第 9 项「当期存在的每个服务的 /healthz、/metrics 均可被拉取」.
// The two probes that need a live Postgres or NATS are exercised against the real containers in the
// card report; here the check bodies are substituted so the HANDLING of a failing dependency -- which
// is the part that must not be 恒 200 -- is what gets asserted.

type healthBody struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"checks"`
}

func upCheck(name string) Check {
	return Check{Name: name, Probe: func(context.Context) error { return nil }}
}

func downCheck(name string, err error) Check {
	return Check{Name: name, Probe: func(context.Context) error { return err }}
}

func getHealth(t *testing.T, checks ...Check) (int, healthBody) {
	t.Helper()

	d, ok := registry.ByCode("homeos")
	if !ok {
		t.Fatal("registry has no homeos row")
	}
	engine := newTestEngine(t, d, checks...)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	engine.ServeHTTP(rec, req)

	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v -- raw: %s", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestHealthzIs200OnlyWhenEveryCheckIsUp(t *testing.T) {
	code, body := getHealth(t,
		upCheck("schema:homeos"),
		upCheck("jetstream"),
	)

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200 when both checks of §10.1 are up (body %s)", code, mustJSON(t, body))
	}
	if body.Status != healthOK {
		t.Errorf("status field = %q, want %q", body.Status, healthOK)
	}
	if body.Code != "homeos" {
		t.Errorf("code field = %q, want the registry code homeos", body.Code)
	}
	if len(body.Checks) != 2 {
		t.Fatalf("checks = %+v, want the two documented probes", body.Checks)
	}
	for _, c := range body.Checks {
		if c.Status != statusUp {
			t.Errorf("check %q = %q, want up", c.Name, c.Status)
		}
	}
}

// TestHealthzIsNotAlways200 is the judgement the card asks for: a down dependency must show up as a
// failed request, not as a healthy one.
func TestHealthzIsNotAlways200(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []Check
	}{
		{
			name:   "schema unreachable",
			checks: []Check{downCheck("schema:homeos", errors.New("dial tcp 127.0.0.1:1: connect: connection refused")), upCheck("jetstream")},
		},
		{
			name:   "jetstream unreachable",
			checks: []Check{upCheck("schema:homeos"), downCheck("jetstream", errors.New("nats: connection closed"))},
		},
		{
			name:   "both unreachable",
			checks: []Check{downCheck("schema:homeos", errors.New("ping failed")), downCheck("jetstream", errors.New("no jetstream"))},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getHealth(t, tc.checks...)

			if code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503: %s must not answer 200", code, tc.name)
			}
			if body.Status != healthFailure {
				t.Errorf("status field = %q, want %q", body.Status, healthFailure)
			}
			if len(body.Checks) != len(tc.checks) {
				t.Fatalf("checks = %+v, want one entry per probe", body.Checks)
			}
			var down int
			for _, c := range body.Checks {
				if c.Status == statusDown {
					down++
					if c.Detail == "" {
						t.Errorf("failed check %q carries no detail: the response must say why", c.Name)
					}
				}
			}
			wantDown := 0
			for _, c := range tc.checks {
				if err := c.Probe(context.Background()); err != nil {
					wantDown++
				}
			}
			if down != wantDown {
				t.Errorf("%d checks reported down, want %d", down, wantDown)
			}
		})
	}
}

func TestPreflightRefusesToStartOnAFailingCheck(t *testing.T) {
	d, _ := registry.ByCode("finance")
	health := NewHealth(d, NewLogger(d),
		upCheck("jetstream"),
		downCheck("schema:finance", errors.New("current_schema()=public，与本域 schema finance 不符")),
	)

	err := health.Preflight(context.Background())
	if err == nil {
		t.Fatal("Preflight accepted a failing dependency")
	}
	for _, want := range []string{"schema:finance", "1/2", "current_schema()"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Preflight error = %v, want it to name %q", err, want)
		}
	}

	ok := NewHealth(d, NewLogger(d), upCheck("schema:finance"), upCheck("jetstream"))
	if err := ok.Preflight(context.Background()); err != nil {
		t.Errorf("Preflight with healthy checks = %v, want nil", err)
	}
}

// TestCheckNamesComeFromTheRegistryRow pins the two probe names to the registry cells they are built
// from: "schema:{code}" is Domain.Schema (§1.3 row 2) and the JetStream probe is the single
// 「JetStream 已连」 of §10.1 末条.
func TestCheckNamesComeFromTheRegistryRow(t *testing.T) {
	for _, d := range registry.Implemented() {
		if got := SchemaCheck(d, nil).Name; got != "schema:"+d.Schema {
			t.Errorf("SchemaCheck(%s).Name = %q, want %q", d.Code, got, "schema:"+d.Schema)
		}
		if got := JetStreamCheck(nil).Name; got != "jetstream" {
			t.Errorf("JetStreamCheck.Name = %q, want jetstream", got)
		}
		names := NewHealth(d, NewLogger(d), SchemaCheck(d, nil), JetStreamCheck(nil)).CheckNames()
		if len(names) != 2 {
			t.Errorf("CheckNames = %v, want the two checks of §10.1", names)
		}
	}
}

// TestProbesFailWithoutAConnection: neither probe may report 「可用」 when its dependency object is
// missing or never connected. This is the unit half of the check; the live half (a real Postgres and
// a real NATS, then taking NATS away) is in the card report.
func TestProbesFailWithoutAConnection(t *testing.T) {
	d, _ := registry.ByCode("finance")

	if err := SchemaCheck(d, nil).Probe(context.Background()); err == nil {
		t.Error("SchemaCheck accepted a nil database handle")
	}
	if err := JetStreamCheck(nil).Probe(context.Background()); err == nil {
		t.Error("JetStreamCheck accepted a nil connection")
	}
	// A zero-value Conn is one that never connected: the probe reads live state, not construction.
	if err := JetStreamCheck(&nats.Conn{}).Probe(context.Background()); err == nil {
		t.Error("JetStreamCheck accepted an unconnected *nats.Conn")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
