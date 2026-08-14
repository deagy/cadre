package production

import (
	"sync"
	"time"
)

// HealthStatus represents the health status of a component.
type HealthStatus struct {
	Status     string                     `json:"status"`
	Timestamp  time.Time                  `json:"timestamp"`
	Version    string                     `json:"version"`
	Components map[string]ComponentHealth `json:"components"`
}

// ComponentHealth represents the health of a single component.
type ComponentHealth struct {
	Status    string        `json:"status"`
	Message   string        `json:"message"`
	Duration  time.Duration `json:"duration_ms"`
	Timestamp time.Time     `json:"timestamp"`
}

// HealthChecker performs health checks on components.
type HealthChecker struct {
	mu        sync.RWMutex
	checks    map[string]HealthCheckFunc
	version   string
	startTime time.Time
}

// HealthCheckFunc is a function that performs a health check.
type HealthCheckFunc func() (string, error)

// NewHealthChecker creates a new health checker.
func NewHealthChecker(version string) *HealthChecker {
	return &HealthChecker{
		checks:    make(map[string]HealthCheckFunc),
		version:   version,
		startTime: time.Now(),
	}
}

// RegisterCheck registers a health check for a component.
func (hc *HealthChecker) RegisterCheck(name string, checkFn HealthCheckFunc) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.checks[name] = checkFn
}

// Check performs a health check on a specific component.
func (hc *HealthChecker) Check(name string) (ComponentHealth, error) {
	hc.mu.RLock()
	checkFn, exists := hc.checks[name]
	hc.mu.RUnlock()

	if !exists {
		return ComponentHealth{
			Status:    "unknown",
			Message:   "check not registered",
			Timestamp: time.Now(),
		}, nil
	}

	start := time.Now()
	message, err := checkFn()
	duration := time.Since(start)

	status := "healthy"
	if err != nil {
		status = "unhealthy"
		message = err.Error()
	}

	return ComponentHealth{
		Status:    status,
		Message:   message,
		Duration:  duration,
		Timestamp: time.Now(),
	}, nil
}

// CheckAll performs all registered health checks.
func (hc *HealthChecker) CheckAll() HealthStatus {
	hc.mu.RLock()
	checks := make(map[string]HealthCheckFunc, len(hc.checks))
	for name, fn := range hc.checks {
		checks[name] = fn
	}
	hc.mu.RUnlock()

	components := make(map[string]ComponentHealth)
	overallStatus := "healthy"

	for name, checkFn := range checks {
		start := time.Now()
		message, err := checkFn()
		duration := time.Since(start)

		status := "healthy"
		if err != nil {
			status = "unhealthy"
			message = err.Error()
			overallStatus = "unhealthy"
		}

		components[name] = ComponentHealth{
			Status:    status,
			Message:   message,
			Duration:  duration,
			Timestamp: time.Now(),
		}
	}

	return HealthStatus{
		Status:     overallStatus,
		Timestamp:  time.Now(),
		Version:    hc.version,
		Components: components,
	}
}

// ReadinessChecker checks if the application is ready to serve traffic.
type ReadinessChecker struct {
	mu     sync.RWMutex
	checks map[string]ReadinessCheckFunc
	ready  bool
}

// ReadinessCheckFunc checks if a component is ready.
type ReadinessCheckFunc func() bool

// NewReadinessChecker creates a new readiness checker.
func NewReadinessChecker() *ReadinessChecker {
	return &ReadinessChecker{
		checks: make(map[string]ReadinessCheckFunc),
		ready:  true,
	}
}

// RegisterCheck registers a readiness check.
func (rc *ReadinessChecker) RegisterCheck(name string, checkFn ReadinessCheckFunc) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.checks[name] = checkFn
}

// IsReady checks if all components are ready.
func (rc *ReadinessChecker) IsReady() bool {
	rc.mu.RLock()
	checks := make(map[string]ReadinessCheckFunc, len(rc.checks))
	for name, fn := range rc.checks {
		checks[name] = fn
	}
	rc.mu.RUnlock()

	for _, checkFn := range checks {
		if !checkFn() {
			return false
		}
	}

	return true
}

// SetReady sets the overall readiness state.
func (rc *ReadinessChecker) SetReady(ready bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.ready = ready
}
