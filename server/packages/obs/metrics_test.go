package obs

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Tests of §1.2's 「指标（常量标签 code）」 and §10.1 末条's 「/metrics 带常量标签 code」.

func TestCodeLabelIsConstantOnEverySeries(t *testing.T) {
	for _, d := range registry.Implemented() {
		m := NewMetrics(d)
		m.Observe(d.RoutePrefix+"healthz", "GET", 200, 0.012)
		m.Observe("", "GET", 404, 0.5)

		families, err := m.prom.Gather()
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if len(families) == 0 {
			t.Fatal("Gather returned nothing")
		}

		seen := 0
		for _, f := range families {
			for _, metric := range f.GetMetric() {
				label := map[string]string{}
				for _, lp := range metric.GetLabel() {
					label[lp.GetName()] = lp.GetValue()
				}
				if got, ok := label["code"]; !ok {
					t.Errorf("series of %s has no code label: %v", f.GetName(), label)
				} else if got != d.Code {
					t.Errorf("series of %s has code=%q, want the registry code %q", f.GetName(), got, d.Code)
				}
				seen++
			}
		}
		if seen == 0 {
			t.Errorf("no series gathered for %s", d.Code)
		}
	}
}

// TestMetricSetIsOnlyWhatASkeletonCanProduce: §10.3's list is long, and the items whose producers do
// not exist yet (outbox 积压 S2、死信数 S2、收敛时间 S5、跨服务调用 S6、同步冲突 S5、投递回执 S5、
// 依赖调用量 S13) must not appear as zeroed placeholders here.
func TestMetricSetIsOnlyWhatASkeletonCanProduce(t *testing.T) {
	d, _ := registry.ByCode("homeos")
	m := NewMetrics(d)
	m.Observe("/api/homeos/healthz", "GET", 200, 0.01)

	families, err := m.prom.Gather()
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]*dto.MetricFamily{}
	for _, f := range families {
		names[f.GetName()] = f
	}

	for _, want := range []string{"http_requests_total", "http_request_duration_seconds"} {
		if _, ok := names[want]; !ok {
			t.Errorf("metric %s is missing: §10.3 counts 接口 P95/P99 与 错误率 among the items this card must produce", want)
		}
	}

	for _, notWant := range []string{
		"outbox_pending", "dead_letters_total", "convergence_seconds",
		"crossservice_call_seconds", "sync_conflicts_total", "delivery_receipt_total",
	} {
		for name := range names {
			if strings.Contains(name, notWant) {
				t.Errorf("metric %q is registered: its producer belongs to S2/S5/S6/S13 and an empty collector would be a fake data source", name)
			}
		}
	}
}

// TestRouteLabelIsBounded: an unmatched request must not turn its path into a label value, or every
// scan of the service would leak a new series.
func TestRouteLabelIsBounded(t *testing.T) {
	d, _ := registry.ByCode("finance")
	m := NewMetrics(d)

	// Matched requests carry their route PATTERN (that is what the middleware passes: c.FullPath()),
	// unmatched ones carry nothing at all.
	const matched = "/api/finance/transactions"
	unmatched := 0
	for i := 0; i < 3; i++ {
		m.Observe(matched, "GET", 200, 0.01)
		m.Observe("", "GET", 404, 0.01)
		m.Observe("", "GET", 404, 0.02)
		unmatched += 2
	}

	families, err := m.prom.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "http_requests_total" {
			continue
		}

		var unmatchedSeries int
		var unmatchedTotal float64
		var matchedSeries int
		for _, metric := range f.GetMetric() {
			route := ""
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "route" {
					route = lp.GetValue()
				}
			}
			switch route {
			case routeUnmatched:
				unmatchedSeries++
				unmatchedTotal += metric.GetCounter().GetValue()
			case matched:
				matchedSeries++
			default:
				t.Errorf("unexpected route label %q: a path that is not a route pattern must never reach the label", route)
			}
		}

		if unmatchedSeries != 1 {
			t.Errorf("%d series carry %q, want exactly 1: every unmatched request must merge onto one bounded value",
				unmatchedSeries, routeUnmatched)
		}
		if unmatchedTotal != float64(unmatched) {
			t.Errorf("the merged series counts %v, want %d: the samples must not be lost by merging", unmatchedTotal, unmatched)
		}
		if matchedSeries != 1 {
			t.Errorf("%d series for the matched pattern %q, want 1", matchedSeries, matched)
		}
	}
}
