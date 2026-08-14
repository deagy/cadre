package production

import (
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	config := &Config{
		Port:         8080,
		Host:         "localhost",
		CacheSize:    1000,
		RateLimitRPS: 100.0,
		MaxAgents:    10,
		Environment:  "production",
	}

	if err := config.Validate(); err != nil {
		t.Errorf("Valid config should not error: %v", err)
	}

	// Test invalid port
	config.Port = 70000
	if err := config.Validate(); err == nil {
		t.Errorf("Invalid port should error")
	}

	// Test invalid cache size
	config.Port = 8080
	config.CacheSize = -1
	if err := config.Validate(); err == nil {
		t.Errorf("Invalid cache size should error")
	}

	// Test invalid environment
	config.CacheSize = 1000
	config.Environment = "invalid"
	if err := config.Validate(); err == nil {
		t.Errorf("Invalid environment should error")
	}
}

func TestShutdownManager(t *testing.T) {
	sm := NewShutdownManager(5 * time.Second)
	sm.Start()

	select {
	case <-sm.Wait():
		t.Errorf("Should not receive shutdown signal immediately")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}
}

func TestConnectionDrainer(t *testing.T) {
	cd := NewConnectionDrainer(5 * time.Second)

	cd.Acquire()
	cd.Acquire()

	if count := cd.ActiveCount(); count != 2 {
		t.Errorf("Active count should be 2, got %d", count)
	}

	cd.Release()
	cd.Release()

	if count := cd.ActiveCount(); count != 0 {
		t.Errorf("Active count should be 0, got %d", count)
	}
}

func TestHealthChecker(t *testing.T) {
	hc := NewHealthChecker("1.0.0")

	// Register a healthy check
	hc.RegisterCheck("database", func() (string, error) {
		return "connected", nil
	})

	status := hc.CheckAll()

	if status.Status != "healthy" {
		t.Errorf("Overall status should be healthy")
	}

	if status.Version != "1.0.0" {
		t.Errorf("Version should be 1.0.0")
	}

	if len(status.Components) != 1 {
		t.Errorf("Should have 1 component")
	}

	if status.Components["database"].Status != "healthy" {
		t.Errorf("Database component should be healthy")
	}
}

func TestHealthCheckerUnhealthy(t *testing.T) {
	hc := NewHealthChecker("1.0.0")

	hc.RegisterCheck("database", func() (string, error) {
		return "", ErrConnectionFailed()
	})

	status := hc.CheckAll()

	if status.Status != "unhealthy" {
		t.Errorf("Overall status should be unhealthy")
	}

	if status.Components["database"].Status != "unhealthy" {
		t.Errorf("Database component should be unhealthy")
	}
}

func TestReadinessChecker(t *testing.T) {
	rc := NewReadinessChecker()

	rc.RegisterCheck("db", func() bool {
		return true
	})

	if !rc.IsReady() {
		t.Errorf("Should be ready when all checks pass")
	}

	rc.RegisterCheck("cache", func() bool {
		return false
	})

	if rc.IsReady() {
		t.Errorf("Should not be ready when any check fails")
	}
}

func TestAuthValidator(t *testing.T) {
	av := NewAuthValidator()
	key := "test-api-key-123"

	av.AddAPIKey(key)

	if !av.ValidateAPIKey(key, key) {
		t.Errorf("Valid API key should validate")
	}

	if av.ValidateAPIKey("wrong-key", key) {
		t.Errorf("Invalid API key should not validate")
	}
}

func TestInputValidator(t *testing.T) {
	iv := NewInputValidator(1000)

	// Valid task ID
	if err := iv.ValidateTaskID("TASK-001"); err != nil {
		t.Errorf("Valid task ID should not error")
	}

	// Invalid task ID
	if err := iv.ValidateTaskID(""); err == nil {
		t.Errorf("Empty task ID should error")
	}

	// Valid file path
	if err := iv.ValidateFilePath("src/main.go"); err != nil {
		t.Errorf("Valid file path should not error")
	}

	// Invalid file path (traversal)
	if err := iv.ValidateFilePath("../etc/passwd"); err == nil {
		t.Errorf("Path traversal should error")
	}

	// Input size validation
	large := ""
	for i := 0; i < 1001; i++ {
		large += "a"
	}

	if err := iv.ValidateInputSize(large); err == nil {
		t.Errorf("Oversized input should error")
	}
}

func TestInputSanitization(t *testing.T) {
	iv := NewInputValidator(1000)

	input := "normal\x00text\x01here"
	sanitized := iv.SanitizeInput(input)

	if sanitized == input {
		t.Errorf("Should remove control characters")
	}

	if len(sanitized) >= len(input) {
		t.Errorf("Sanitized text should be shorter")
	}
}

func TestSecretsManager(t *testing.T) {
	sm := NewSecretsManager()

	secret := "super-secret-value"
	sm.SetSecret("db-password", secret)

	retrieved, err := sm.GetSecret("db-password")
	if err != nil {
		t.Errorf("Should retrieve secret: %v", err)
	}

	if retrieved != secret {
		t.Errorf("Retrieved secret should match stored value")
	}

	// Non-existent secret
	_, err = sm.GetSecret("nonexistent")
	if err == nil {
		t.Errorf("Should error on non-existent secret")
	}

	// Delete secret
	sm.DeleteSecret("db-password")
	_, err = sm.GetSecret("db-password")
	if err == nil {
		t.Errorf("Should error after deletion")
	}
}

func TestPermissionValidator(t *testing.T) {
	pv := NewPermissionValidator()

	pv.GrantPermission("user1", "read")
	pv.GrantPermission("user1", "write")

	if !pv.HasPermission("user1", "read") {
		t.Errorf("User should have read permission")
	}

	if !pv.HasPermission("user1", "write") {
		t.Errorf("User should have write permission")
	}

	if pv.HasPermission("user1", "delete") {
		t.Errorf("User should not have delete permission")
	}

	// Wildcard permission
	pv.GrantPermission("admin", "*")
	if !pv.HasPermission("admin", "delete") {
		t.Errorf("Admin should have all permissions")
	}

	// Revoke permission
	pv.RevokePermission("user1", "read")
	if pv.HasPermission("user1", "read") {
		t.Errorf("User should not have read permission after revocation")
	}
}

func TestServiceLoadBalancer(t *testing.T) {
	slb := NewServiceLoadBalancer()

	slb.AddInstance(ServiceInstance{
		ID:      "instance1",
		Host:    "localhost",
		Port:    8080,
		Healthy: true,
		Weight:  1,
	})

	slb.AddInstance(ServiceInstance{
		ID:      "instance2",
		Host:    "localhost",
		Port:    8081,
		Healthy: true,
		Weight:  1,
	})

	// Get instances
	instance1, err := slb.GetNextInstance()
	if err != nil {
		t.Errorf("Should get instance: %v", err)
	}

	if instance1.ID != "instance1" {
		t.Errorf("Should round-robin to first instance")
	}

	instance2, err := slb.GetNextInstance()
	if err != nil {
		t.Errorf("Should get instance: %v", err)
	}

	if instance2.ID != "instance2" {
		t.Errorf("Should round-robin to second instance")
	}

	// Mark unhealthy
	slb.MarkUnhealthy("instance2")
	next, err := slb.GetNextInstance()
	if err != nil {
		t.Errorf("Should still get healthy instance: %v", err)
	}

	if next.ID != "instance1" {
		t.Errorf("Should skip unhealthy instance")
	}
}

func TestRateLimitValidator(t *testing.T) {
	rlv := NewRateLimitValidator()

	// Check limit
	if !rlv.CheckRateLimit("client1", 5) {
		t.Errorf("First request should succeed")
	}

	if !rlv.CheckRateLimit("client1", 5) {
		t.Errorf("Second request should succeed")
	}

	// Hit limit
	for i := 0; i < 3; i++ {
		rlv.CheckRateLimit("client1", 5)
	}

	if rlv.CheckRateLimit("client1", 5) {
		t.Errorf("Should hit rate limit")
	}

	// Reset limit
	rlv.ResetRateLimit("client1")
	if !rlv.CheckRateLimit("client1", 5) {
		t.Errorf("Should succeed after reset")
	}
}

func TestDeploymentInfo(t *testing.T) {
	info := &DeploymentInfo{
		Environment: "production",
		Version:     "1.0.0",
	}

	if !info.IsProduction() {
		t.Errorf("Should detect production environment")
	}

	info.Environment = "development"
	if info.IsProduction() {
		t.Errorf("Should not detect development as production")
	}
}

func TestSecurityHeaders(t *testing.T) {
	headers := NewSecurityHeaders()

	if headers.ContentSecurityPolicy == "" {
		t.Errorf("Should have CSP header")
	}

	if headers.StrictTransportSec == "" {
		t.Errorf("Should have HSTS header")
	}

	if headers.XContentTypeOptions == "" {
		t.Errorf("Should have X-Content-Type-Options header")
	}

	if headers.XFrameOptions == "" {
		t.Errorf("Should have X-Frame-Options header")
	}

	if headers.XSSProtection == "" {
		t.Errorf("Should have X-XSS-Protection header")
	}
}

// Helper error function
func ErrConnectionFailed() error {
	return &connectionError{msg: "connection failed"}
}

type connectionError struct {
	msg string
}

func (e *connectionError) Error() string {
	return e.msg
}
