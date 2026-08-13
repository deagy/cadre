package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/production"
)

func newTestConfig() *production.Config {
	return &production.Config{
		Port:            8080,
		Host:            "localhost",
		HealthCheckPath: "/health",
		ReadyCheckPath:  "/ready",
		Version:         "1.0.0",
		LogOutput:       "stdout",
	}
}

func TestServerCreation(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)
	if s == nil {
		t.Errorf("Server should be created")
	}
}

func TestHealthEndpoint(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)
	s.RegisterHealthCheck("test", func() (string, error) {
		return "ok", nil
	})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "healthy") {
		t.Errorf("Response should contain 'healthy'")
	}
}

func TestReadyEndpoint(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)
	s.RegisterReadinessCheck("test", func() bool {
		return true
	})

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "ready") {
		t.Errorf("Response should contain 'ready'")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)
	s.recordMetric("http_requests_total", 1, map[string]string{
		"method": "GET",
		"path":   "/test",
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "http_requests_total") {
		t.Errorf("Response should contain metrics")
	}
}

func TestStatusEndpoint(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "health") || !strings.Contains(body, "ready") {
		t.Errorf("Response should contain health and ready status")
	}
}

func TestMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector()

	mc.Record("http_requests_total", 1, map[string]string{"method": "GET"})
	mc.Record("http_requests_total", 1, map[string]string{"method": "POST"})

	mc.Record("http_request_duration_ms", 50, map[string]string{"path": "/test"})
	mc.Record("http_request_duration_ms", 150, map[string]string{"path": "/test"})

	export := mc.Export()

	if !strings.Contains(export, "http_requests_total") {
		t.Errorf("Export should contain request counter")
	}

	if !strings.Contains(export, "http_request_duration_ms") {
		t.Errorf("Export should contain duration histogram")
	}
}

func TestHistogramPercentile(t *testing.T) {
	h := NewHistogram()

	for i := 1; i <= 100; i++ {
		h.Observe(float64(i))
	}

	p50 := h.Percentile(50)
	if p50 == 0 {
		t.Errorf("P50 should be calculated")
	}

	p95 := h.Percentile(95)
	if p95 <= p50 {
		t.Errorf("P95 should be greater than P50")
	}
}

func TestRecordError(t *testing.T) {
	mc := NewMetricsCollector()

	mc.RecordError("timeout")
	mc.RecordError("timeout")
	mc.RecordError("connection_refused")

	if mc.errorCount != 3 {
		t.Errorf("Expected 3 errors, got %d", mc.errorCount)
	}
}

func TestResponseWriter(t *testing.T) {
	baseWriter := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: baseWriter, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", rw.statusCode)
	}

	if baseWriter.Code != http.StatusNotFound {
		t.Errorf("Base writer should have 404")
	}
}

func TestUnhealthyComponent(t *testing.T) {
	config := newTestConfig()
	logger, err := production.NewProductionLogger(config, "test")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	s := NewServer(config, logger)

	s.RegisterHealthCheck("database", func() (string, error) {
		return "", &testError{"connection failed"}
	})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for unhealthy, got %d", w.Code)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
