package rclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Request defines a cross-service read request.
// All three elements must be explicitly declared - no defaults.
type Request struct {
	Target  string                           // Service name only (no URL)
	Path    string                           // GET path only
	Timeout time.Duration                    // Required timeout
	Retry   int                              // Number of retries
	Degrade func(context.Context, error) any // Degradation function: returns fallback value on failure
}

// Response is the result from a cross-service call.
type Response struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
}

// Client provides cross-service read-only HTTP client capabilities.
type Client struct {
	httpClient *http.Client
	resolver   ServiceResolver
	code       string // This service's code for metrics labeling
}

// ServiceResolver resolves service names to base URLs.
type ServiceResolver interface {
	Resolve(serviceName string) (string, error)
}

var (
	crossserviceCallSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:        "crossservice_call_seconds",
			Help:        "Duration of cross-service calls in seconds",
			Buckets:     []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			ConstLabels: map[string]string{},
		},
		[]string{"target", "code"},
	)

	degradeCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "crossservice_degrade_total",
			Help:        "Total number of degraded responses returned",
			ConstLabels: map[string]string{},
		},
		[]string{"target", "code"},
	)
)

// NewClient creates a new cross-service read client.
func NewClient(resolver ServiceResolver, code string) *Client {
	return &Client{
		httpClient: &http.Client{},
		resolver:   resolver,
		code:       code,
	}
}

// Call executes a cross-service read request.
// Target must be a service name, Path must be a GET path.
// At most 1 hop per request to the same target.
func (c *Client) Call(ctx context.Context, req Request) (*Response, error) {
	if req.Target == "" {
		return nil, fmt.Errorf("rclient: target service name is required")
	}
	if req.Path == "" {
		return nil, fmt.Errorf("rclient: path is required")
	}
	if req.Timeout <= 0 {
		return nil, fmt.Errorf("rclient: timeout must be positive")
	}
	if req.Retry < 0 {
		return nil, fmt.Errorf("rclient: retry count cannot be negative")
	}

	start := time.Now()
	labels := prometheus.Labels{"target": req.Target, "code": c.code}

	// Resolve service name to base URL
	baseURL, err := c.resolver.Resolve(req.Target)
	if err != nil {
		crossserviceCallSeconds.With(labels).Observe(time.Since(start).Seconds())
		return nil, fmt.Errorf("rclient: failed to resolve service %s: %w", req.Target, err)
	}

	url := baseURL + req.Path

	// Create request with timeout
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		crossserviceCallSeconds.With(labels).Observe(time.Since(start).Seconds())
		return nil, fmt.Errorf("rclient: failed to create request: %w", err)
	}

	// Execute with retry logic
	var lastErr error
	for attempt := 0; attempt <= req.Retry; attempt++ {
		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		crossserviceCallSeconds.With(labels).Observe(time.Since(start).Seconds())

		return &Response{
			StatusCode: resp.StatusCode,
			Body:       body,
			Headers:    resp.Header,
		}, nil
	}

	// All attempts failed
	crossserviceCallSeconds.With(labels).Observe(time.Since(start).Seconds())

	if req.Degrade != nil {
		degradeCounter.With(labels).Inc()
		// Call degradation function to get fallback value
		fallback := req.Degrade(ctx, lastErr)
		// Return degraded response with fallback data
		fallbackJSON, err := json.Marshal(fallback)
		if err != nil {
			return nil, fmt.Errorf("rclient: failed to marshal fallback: %w", err)
		}
		return &Response{
			StatusCode: 200,
			Body:       fallbackJSON,
			Headers:    make(http.Header),
		}, nil
	}

	return nil, fmt.Errorf("rclient: all %d attempts failed for %s: %w", req.Retry+1, req.Target, lastErr)
}

// DecodeJSON unmarshals the response body into the provided target.
func (r *Response) DecodeJSON(target interface{}) error {
	return json.Unmarshal(r.Body, target)
}
