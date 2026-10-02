package obs

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Middleware observes every served request: one latency sample and one counter per status code, so
// §10.3's 接口 P95/P99 与 错误率 come out of the skeleton rather than out of a future handler.
//
// It is registered on the engine (not inside a route group), so the two operational endpoints are
// measured too -- the scrape itself included.
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		m.Observe(c.FullPath(), c.Request.Method, c.Writer.Status(), time.Since(start).Seconds())
	}
}

// LogRequests writes one structured line per request (docs/p1-tech-plan.md §1.2「结构化日志」).
//
// The route is the matched pattern, not the raw path: PRD 22.2 第 8 条 keeps every business route
// inside /api/{code}/* and the pattern is what identifies the endpoint in a log search, while the
// path would carry per-resource values (family and object ids) into every line.
func LogRequests(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = routeUnmatched
		}
		log.Info("http_request",
			slog.String("route", route),
			slog.String("method", c.Request.Method),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("duration_ms", max(time.Since(start).Milliseconds(), 0)),
		)
	}
}
