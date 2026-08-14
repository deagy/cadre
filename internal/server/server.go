package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/deagy/cadre/cli/internal/production"
)

// Server represents the HTTP server for health checks, metrics, and observability.
type Server struct {
	config           *production.Config
	httpServer       *http.Server
	healthChecker    *production.HealthChecker
	readinessCheck   *production.ReadinessChecker
	metricsCollector *MetricsCollector
	logger           *production.ProductionLogger
	mu               sync.RWMutex
	isRunning        bool
}

// NewServer creates a new HTTP server instance.
func NewServer(config *production.Config, logger *production.ProductionLogger) *Server {
	s := &Server{
		config:           config,
		healthChecker:    production.NewHealthChecker(config.Version),
		readinessCheck:   production.NewReadinessChecker(),
		metricsCollector: NewMetricsCollector(),
		logger:           logger,
	}

	mux := http.NewServeMux()

	// Health and readiness endpoints
	mux.HandleFunc(config.HealthCheckPath, s.handleHealth)
	mux.HandleFunc(config.ReadyCheckPath, s.handleReady)

	// Metrics endpoint
	mux.HandleFunc("/metrics", s.handleMetrics)

	// Status endpoint
	mux.HandleFunc("/status", s.handleStatus)

	// Pprof endpoints for profiling (optional)
	mux.HandleFunc("/debug/pprof/", s.handlePprof)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:      s.metricsMiddleware(mux),
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	}

	return s
}

// RegisterHealthCheck registers a health check for a component.
func (s *Server) RegisterHealthCheck(name string, checkFn production.HealthCheckFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.healthChecker.RegisterCheck(name, checkFn)
}

// RegisterReadinessCheck registers a readiness check for a component.
func (s *Server) RegisterReadinessCheck(name string, checkFn production.ReadinessCheckFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.readinessCheck.RegisterCheck(name, checkFn)
}

// Start starts the HTTP server in a goroutine.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.isRunning = true
	s.mu.Unlock()

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if s.logger != nil {
				s.logger.Error("HTTP server error", err, map[string]interface{}{
					"addr": s.httpServer.Addr,
				})
			}
		}
	}()

	if s.logger != nil {
		s.logger.Info("HTTP server started", map[string]interface{}{
			"address": s.httpServer.Addr,
			"health":  s.config.HealthCheckPath,
			"ready":   s.config.ReadyCheckPath,
			"metrics": "/metrics",
		})
	}

	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return nil
	}

	s.isRunning = false

	if err := s.httpServer.Shutdown(ctx); err != nil {
		if s.logger != nil {
			s.logger.Error("HTTP server shutdown error", err, nil)
		}

		return err
	}

	if s.logger != nil {
		s.logger.Info("HTTP server shutdown complete", nil)
	}

	return nil
}

// Handler functions

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	status := s.healthChecker.CheckAll()
	s.mu.RUnlock()

	s.recordMetric("http_requests_total", 1, map[string]string{
		"endpoint": "health",
		"status":   fmt.Sprintf("%d", http.StatusOK),
	})

	w.Header().Set("Content-Type", "application/json")

	statusCode := http.StatusOK
	if status.Status != "healthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `{"status":"%s","timestamp":"%s","version":"%s","components":%d}`,
		status.Status, status.Timestamp.Format(time.RFC3339), status.Version, len(status.Components))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	isReady := s.readinessCheck.IsReady()
	s.mu.RUnlock()

	s.recordMetric("http_requests_total", 1, map[string]string{
		"endpoint": "ready",
		"status":   fmt.Sprintf("%d", http.StatusOK),
	})

	w.Header().Set("Content-Type", "application/json")

	statusCode := http.StatusOK
	status := "ready"
	if !isReady {
		statusCode = http.StatusServiceUnavailable
		status = "not_ready"
	}

	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `{"status":"%s","timestamp":"%s"}`, status, time.Now().Format(time.RFC3339))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	metrics := s.metricsCollector.Export()
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)

	_, _ = fmt.Fprint(w, metrics)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	health := s.healthChecker.CheckAll()
	isReady := s.readinessCheck.IsReady()
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_, _ = fmt.Fprintf(w, `{"health":"%s","ready":%v,"version":"%s"}`,
		health.Status, isReady, s.config.Version)
}

func (s *Server) handlePprof(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, `<html><body><h1>pprof Profiling</h1>
<p><a href="/debug/pprof/heap">Heap</a></p>
<p><a href="/debug/pprof/goroutine">Goroutines</a></p>
<p><a href="/debug/pprof/profile">CPU (30s)</a></p>
</body></html>`)
}

// Middleware

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Handle the request
		next.ServeHTTP(wrapped, r)

		// Record metrics
		duration := time.Since(start).Milliseconds()
		s.recordMetric("http_request_duration_ms", float64(duration), map[string]string{
			"endpoint": r.URL.Path,
			"method":   r.Method,
		})

		s.recordMetric("http_requests_total", 1, map[string]string{
			"endpoint": r.URL.Path,
			"method":   r.Method,
			"status":   fmt.Sprintf("%d", wrapped.statusCode),
		})

		if s.logger != nil {
			s.logger.Info(fmt.Sprintf("%s %s", r.Method, r.URL.Path), map[string]interface{}{
				"status":    wrapped.statusCode,
				"duration":  duration,
				"method":    r.Method,
				"path":      r.URL.Path,
				"remote_ip": r.RemoteAddr,
			})
		}
	})
}

func (s *Server) recordMetric(name string, value float64, labels map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metricsCollector.Record(name, value, labels)
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
