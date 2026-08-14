package orchestration

import (
	"fmt"
	"sync"
	"time"
)

// RateLimiter defines the interface for rate limiting.
type RateLimiter interface {
	Allow() bool
	AllowN(tokens int) bool
	Wait()
}

// TokenBucketLimiter implements token bucket algorithm for rate limiting.
type TokenBucketLimiter struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	refillRate   float64 // tokens per second
	lastRefillAt time.Time
	maxWaitTime  time.Duration
}

// NewTokenBucketLimiter creates a new token bucket rate limiter.
func NewTokenBucketLimiter(capacity int, rps float64) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		tokens:       float64(capacity),
		capacity:     float64(capacity),
		refillRate:   rps,
		lastRefillAt: time.Now(),
		maxWaitTime:  30 * time.Second,
	}
}

// Allow checks if one token is available.
func (tb *TokenBucketLimiter) Allow() bool {
	return tb.AllowN(1)
}

// AllowN checks if n tokens are available.
func (tb *TokenBucketLimiter) AllowN(tokens int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(tokens) {
		tb.tokens -= float64(tokens)
		return true
	}

	return false
}

// Wait blocks until a token is available, respecting maxWaitTime timeout.
func (tb *TokenBucketLimiter) Wait() {
	deadline := time.Now().Add(tb.maxWaitTime)
	for {
		if tb.Allow() {
			return
		}
		if time.Now().After(deadline) {
			return // Timeout exceeded, return to avoid hanging forever
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (tb *TokenBucketLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefillAt).Seconds()
	newTokens := tb.tokens + elapsed*tb.refillRate
	if newTokens > tb.capacity {
		newTokens = tb.capacity
	}
	tb.tokens = newTokens
	tb.lastRefillAt = now
}

// QuotaLimit represents a quota for a resource.
type QuotaLimit struct {
	Name        string
	MaxRequests int
	Window      time.Duration
}

// QuotaManager manages resource quotas across multiple agents.
type QuotaManager struct {
	mu       sync.RWMutex
	limiters map[string]*TokenBucketLimiter
	quotas   map[string]*QuotaLimit
}

// NewQuotaManager creates a new quota manager.
func NewQuotaManager() *QuotaManager {
	return &QuotaManager{
		limiters: make(map[string]*TokenBucketLimiter),
		quotas:   make(map[string]*QuotaLimit),
	}
}

// SetQuota sets a quota for a resource.
func (qm *QuotaManager) SetQuota(name string, maxRequests int, window time.Duration) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qm.quotas[name] = &QuotaLimit{
		Name:        name,
		MaxRequests: maxRequests,
		Window:      window,
	}

	// Create rate limiter (requests per second)
	rps := float64(maxRequests) / window.Seconds()
	qm.limiters[name] = NewTokenBucketLimiter(maxRequests, rps)
}

// Acquire attempts to acquire one unit of quota.
func (qm *QuotaManager) Acquire(name string) bool {
	qm.mu.RLock()
	limiter, exists := qm.limiters[name]
	qm.mu.RUnlock()

	if !exists {
		return true // No quota set
	}

	return limiter.Allow()
}

// AcquireN attempts to acquire n units of quota.
func (qm *QuotaManager) AcquireN(name string, units int) bool {
	qm.mu.RLock()
	limiter, exists := qm.limiters[name]
	qm.mu.RUnlock()

	if !exists {
		return true
	}

	return limiter.AllowN(units)
}

// WaitQuota blocks until quota is available.
func (qm *QuotaManager) WaitQuota(name string) {
	qm.mu.RLock()
	limiter, exists := qm.limiters[name]
	qm.mu.RUnlock()

	if !exists {
		return
	}

	limiter.Wait()
}

// GetQuota returns the quota for a resource.
func (qm *QuotaManager) GetQuota(name string) *QuotaLimit {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	return qm.quotas[name]
}

// RateLimitError indicates a rate limit was exceeded.
type RateLimitError struct {
	Resource   string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded for %s, retry after %v", e.Resource, e.RetryAfter)
}

// PerAgentRateLimiter provides per-agent rate limiting.
type PerAgentRateLimiter struct {
	mu             sync.RWMutex
	limiters       map[string]*TokenBucketLimiter
	defaultLimiter *TokenBucketLimiter
}

// NewPerAgentRateLimiter creates a new per-agent rate limiter.
func NewPerAgentRateLimiter(defaultRPS float64) *PerAgentRateLimiter {
	return &PerAgentRateLimiter{
		limiters:       make(map[string]*TokenBucketLimiter),
		defaultLimiter: NewTokenBucketLimiter(int(defaultRPS), defaultRPS),
	}
}

// SetAgentLimit sets a rate limit for a specific agent.
func (parl *PerAgentRateLimiter) SetAgentLimit(agentID string, rps float64) {
	parl.mu.Lock()
	defer parl.mu.Unlock()

	parl.limiters[agentID] = NewTokenBucketLimiter(int(rps), rps)
}

// Allow checks if an agent can proceed.
func (parl *PerAgentRateLimiter) Allow(agentID string) bool {
	parl.mu.RLock()
	limiter, exists := parl.limiters[agentID]
	if !exists {
		limiter = parl.defaultLimiter
	}
	parl.mu.RUnlock()

	return limiter.Allow()
}

// Wait blocks until agent can proceed.
func (parl *PerAgentRateLimiter) Wait(agentID string) {
	parl.mu.RLock()
	limiter, exists := parl.limiters[agentID]
	if !exists {
		limiter = parl.defaultLimiter
	}
	parl.mu.RUnlock()

	limiter.Wait()
}
