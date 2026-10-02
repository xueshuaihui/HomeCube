package rclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockResolver struct {
	urls map[string]string
}

func (m *mockResolver) Resolve(serviceName string) (string, error) {
	if url, ok := m.urls[serviceName]; ok {
		return url, nil
	}
	return "", nil
}

func TestCall_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	resolver := &mockResolver{urls: map[string]string{"test-service": server.URL}}
	client := NewClient(resolver, "homeos")

	ctx := context.Background()
	resp, err := client.Call(ctx, Request{
		Target:  "test-service",
		Path:    "/api/test",
		Timeout: 5 * time.Second,
		Retry:   2,
		Degrade: false,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestCall_MissingTarget(t *testing.T) {
	client := NewClient(&mockResolver{}, "homeos")

	_, err := client.Call(context.Background(), Request{
		Path:    "/api/test",
		Timeout: 5 * time.Second,
	})

	if err == nil {
		t.Fatal("expected error for missing target")
	}
}

func TestCall_MissingPath(t *testing.T) {
	client := NewClient(&mockResolver{}, "homeos")

	_, err := client.Call(context.Background(), Request{
		Target:  "test-service",
		Timeout: 5 * time.Second,
	})

	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestCall_InvalidTimeout(t *testing.T) {
	client := NewClient(&mockResolver{}, "homeos")

	_, err := client.Call(context.Background(), Request{
		Target:  "test-service",
		Path:    "/api/test",
		Timeout: 0,
	})

	if err == nil {
		t.Fatal("expected error for zero timeout")
	}
}

func TestCall_NegativeRetry(t *testing.T) {
	client := NewClient(&mockResolver{}, "homeos")

	_, err := client.Call(context.Background(), Request{
		Target:  "test-service",
		Path:    "/api/test",
		Timeout: 5 * time.Second,
		Retry:   -1,
	})

	if err == nil {
		t.Fatal("expected error for negative retry")
	}
}

func TestCall_Degradation(t *testing.T) {
	resolver := &mockResolver{urls: map[string]string{"unreachable": "http://127.0.0.1:1"}}
	client := NewClient(resolver, "homeos")

	ctx := context.Background()
	resp, err := client.Call(ctx, Request{
		Target:  "unreachable",
		Path:    "/api/test",
		Timeout: 100 * time.Millisecond,
		Retry:   0,
		Degrade: true,
	})

	if err != nil {
		t.Fatalf("expected no error with degradation, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected status 200 for degraded response, got %d", resp.StatusCode)
	}
}

func TestDecodeJSON(t *testing.T) {
	resp := &Response{
		Body: []byte(`{"name":"test","value":42}`),
	}

	var result map[string]interface{}
	err := resp.DecodeJSON(&result)
	if err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if result["name"] != "test" {
		t.Errorf("expected name=test, got %v", result["name"])
	}
}
