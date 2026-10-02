package obs

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// Metrics is one service's metric set, held in its own Registry so a process exposes only its own
// numbers (PRD 22.2 第 10 条「每服务可独立运维」, 21.5「当期在运的每个服务分别出图」).
//
// code is a CONSTANT label: it is bound to the Registerer at construction (prometheus.
// WrapRegistererWith), so every series this package registers carries it without any call site
// having to pass it -- which is what stops a future handler from publishing an unlabelled series
// (§1.2「指标（常量标签 code）」, §10.1 末条, PRD 22.2 第 10 条).
//
// Only the two §10.3 items a skeleton can produce are registered. The rest of that list
// (outbox 积压、死信数、端到端收敛、跨服务调用耗时与降级、同步冲突、投递回执率、依赖调用量) gets no
// collector here: their numbers come from S2/S5/S6/S13, and a zero gauge standing in for a metric
// nobody measures yet reads as data on a dashboard.
type Metrics struct {
	registry prometheus.Registerer

	// requests counts served requests per route, method and status code. 错误率 (§10.3) is the
	// 5xx share of it; keeping status_code as a label rather than a boolean lets the 4xx/5xx split
	// be read without a second series.
	requests *prometheus.CounterVec

	// duration is the latency histogram behind 接口 P95/P99 (§10.3, PRD 21.5). Quantiles are
	// computed by the query layer over these buckets, which is why the buckets are the standard
	// Prometheus latency set (5ms .. 10s) rather than a set invented for this card.
	duration *prometheus.HistogramVec

	// gatherer serves the registry to /metrics.
	prom *prometheus.Registry
}

// routeUnmatched is the route label value for requests no registered route accepted. It is a fixed
// string on purpose: using the request path would make the label unbounded and turn /metrics into
// a memory leak on a scanner.
const routeUnmatched = "<unmatched>"

// NewMetrics registers this service's metrics. The code constant label is taken from the registry
// row, so it cannot disagree with the route prefix and schema the same row hands to the service.
func NewMetrics(d registry.Domain) *Metrics {
	prom := prometheus.NewRegistry()
	// WrapRegistererWith applies code to every registered collector at collection time, so the
	// label set of each metric below intentionally does not repeat it.
	reg := prometheus.WrapRegistererWith(prometheus.Labels{"code": d.Code}, prom)

	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Requests served by this code's service, labelled by route, method and status code (docs/p1-tech-plan.md §10.3 错误率).",
			},
			[]string{"route", "method", "status_code"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "Request latency of this code's service (docs/p1-tech-plan.md §10.3 接口 P95/P99).",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"route", "method"},
		),
		prom: prom,
	}

	reg.MustRegister(m.requests, m.duration)
	// Process liveness and resource use are the documented minimum for a service that must be
	// separately graphable (PRD 21.5) and are the only runtime series this card registers: they are
	// real data now, not a placeholder for a future business metric.
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Observe records one served request. The caller passes the matched route, not the URL, so the
// cardinality of the route label is bounded by the route table (which is what 门禁 4's AST check
// keeps inside /api/{code}/*, PRD 22.5 第 4 道).
func (m *Metrics) Observe(route, method string, statusCode int, seconds float64) {
	if route == "" {
		route = routeUnmatched
	}
	m.requests.WithLabelValues(route, method, strconv.Itoa(statusCode)).Inc()
	m.duration.WithLabelValues(route, method).Observe(seconds)
}

// Handler returns the /metrics handler. The constant code label is part of every series it writes;
// nothing at the call site adds it.
func (m *Metrics) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(m.prom, promhttp.HandlerOpts{})
	return gin.WrapH(h)
}
