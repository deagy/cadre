package production

import (
	"crypto/subtle"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// AuthValidator validates API authentication.
type AuthValidator struct {
	apiKeys map[string]bool
	mu      sync.RWMutex
}

// NewAuthValidator creates a new auth validator.
func NewAuthValidator() *AuthValidator {
	return &AuthValidator{
		apiKeys: make(map[string]bool),
	}
}

// AddAPIKey adds a valid API key.
func (av *AuthValidator) AddAPIKey(key string) {
	av.mu.Lock()
	defer av.mu.Unlock()

	av.apiKeys[key] = true
}

// ValidateAPIKey validates an API key using constant-time comparison.
func (av *AuthValidator) ValidateAPIKey(providedKey string, expectedKey string) bool {
	av.mu.RLock()
	defer av.mu.RUnlock()

	return subtle.ConstantTimeCompare([]byte(providedKey), []byte(expectedKey)) == 1
}

// InputValidator validates and sanitizes user input.
type InputValidator struct {
	maxInputSize int
}

// NewInputValidator creates a new input validator.
func NewInputValidator(maxSize int) *InputValidator {
	return &InputValidator{
		maxInputSize: maxSize,
	}
}

// ValidateTaskID validates a task ID format.
func (iv *InputValidator) ValidateTaskID(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	if len(taskID) > 100 {
		return fmt.Errorf("task ID exceeds maximum length")
	}

	if !isValidTaskID(taskID) {
		return fmt.Errorf("task ID contains invalid characters")
	}

	return nil
}

// ValidateFilePath validates a file path.
func (iv *InputValidator) ValidateFilePath(path string) error {
	if path == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	if len(path) > 500 {
		return fmt.Errorf("file path exceeds maximum length")
	}

	// Prevent path traversal
	if strings.Contains(path, "..") {
		return fmt.Errorf("file path contains invalid traversal patterns")
	}

	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("file path must be relative")
	}

	return nil
}

// ValidateInputSize validates that input doesn't exceed maximum size.
func (iv *InputValidator) ValidateInputSize(input string) error {
	if len(input) > iv.maxInputSize {
		return fmt.Errorf("input exceeds maximum size: %d bytes", len(input))
	}

	return nil
}

// SanitizeInput removes potentially dangerous characters.
func (iv *InputValidator) SanitizeInput(input string) string {
	// Remove control characters
	result := strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}

		return r
	}, input)

	return strings.TrimSpace(result)
}

// SecretsManager handles secure storage and retrieval of secrets.
type SecretsManager struct {
	secrets map[string]string
	mu      sync.RWMutex
}

// NewSecretsManager creates a new secrets manager.
func NewSecretsManager() *SecretsManager {
	return &SecretsManager{
		secrets: make(map[string]string),
	}
}

// SetSecret stores a secret securely.
func (sm *SecretsManager) SetSecret(name, value string) error {
	if name == "" {
		return fmt.Errorf("secret name cannot be empty")
	}

	if value == "" {
		return fmt.Errorf("secret value cannot be empty")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.secrets[name] = value
	return nil
}

// GetSecret retrieves a secret securely.
func (sm *SecretsManager) GetSecret(name string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	value, exists := sm.secrets[name]
	if !exists {
		return "", fmt.Errorf("secret not found: %s", name)
	}

	return value, nil
}

// DeleteSecret removes a secret.
func (sm *SecretsManager) DeleteSecret(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.secrets[name]; !exists {
		return fmt.Errorf("secret not found: %s", name)
	}

	delete(sm.secrets, name)
	return nil
}

// RateLimitValidator tracks and validates rate limits per client.
type RateLimitValidator struct {
	limits map[string]int64
	mu     sync.RWMutex
}

// NewRateLimitValidator creates a new rate limit validator.
func NewRateLimitValidator() *RateLimitValidator {
	return &RateLimitValidator{
		limits: make(map[string]int64),
	}
}

// CheckRateLimit checks if a client is within rate limits.
func (rlv *RateLimitValidator) CheckRateLimit(clientID string, maxRequests int64) bool {
	rlv.mu.Lock()
	defer rlv.mu.Unlock()

	current := rlv.limits[clientID]
	if current >= maxRequests {
		return false
	}

	rlv.limits[clientID] = current + 1
	return true
}

// ResetRateLimit resets the rate limit counter for a client.
func (rlv *RateLimitValidator) ResetRateLimit(clientID string) {
	rlv.mu.Lock()
	defer rlv.mu.Unlock()

	delete(rlv.limits, clientID)
}

// SecurityHeaders defines secure HTTP headers for responses.
type SecurityHeaders struct {
	ContentSecurityPolicy string
	StrictTransportSec    string
	XContentTypeOptions   string
	XFrameOptions         string
	XSSProtection         string
}

// NewSecurityHeaders creates security headers with sensible defaults.
func NewSecurityHeaders() *SecurityHeaders {
	return &SecurityHeaders{
		ContentSecurityPolicy: "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'",
		StrictTransportSec:    "max-age=31536000; includeSubDomains",
		XContentTypeOptions:   "nosniff",
		XFrameOptions:         "DENY",
		XSSProtection:         "1; mode=block",
	}
}

// Helper functions

func isValidTaskID(taskID string) bool {
	// Allow alphanumeric, dashes, underscores
	pattern := `^[a-zA-Z0-9\-_]+$`
	matched, _ := regexp.MatchString(pattern, taskID)
	return matched
}

// PermissionValidator checks if an action is authorized.
type PermissionValidator struct {
	permissions map[string][]string // user -> allowed actions
	mu          sync.RWMutex
}

// NewPermissionValidator creates a new permission validator.
func NewPermissionValidator() *PermissionValidator {
	return &PermissionValidator{
		permissions: make(map[string][]string),
	}
}

// GrantPermission grants a permission to a user.
func (pv *PermissionValidator) GrantPermission(userID, action string) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	if _, exists := pv.permissions[userID]; !exists {
		pv.permissions[userID] = []string{}
	}

	pv.permissions[userID] = append(pv.permissions[userID], action)
}

// HasPermission checks if a user has a specific permission.
func (pv *PermissionValidator) HasPermission(userID, action string) bool {
	pv.mu.RLock()
	defer pv.mu.RUnlock()

	actions, exists := pv.permissions[userID]
	if !exists {
		return false
	}

	for _, a := range actions {
		if a == action || a == "*" {
			return true
		}
	}

	return false
}

// RevokePermission revokes a permission from a user.
func (pv *PermissionValidator) RevokePermission(userID, action string) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	actions, exists := pv.permissions[userID]
	if !exists {
		return
	}

	var filtered []string
	for _, a := range actions {
		if a != action {
			filtered = append(filtered, a)
		}
	}

	pv.permissions[userID] = filtered
}
