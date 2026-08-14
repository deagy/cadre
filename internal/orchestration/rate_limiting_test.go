package orchestration

import (
	"strings"
	"testing"
	"time"
)

func TestTokenBucketLimiterAllow(t *testing.T) {
	limiter := NewTokenBucketLimiter(5, 1.0)

	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Errorf("should allow request %d", i+1)
		}
	}

	if limiter.Allow() {
		t.Errorf("should not allow request 6")
	}
}

func TestTokenBucketLimiterRefill(t *testing.T) {
	limiter := NewTokenBucketLimiter(1, 10.0) // 10 tokens per second

	if !limiter.Allow() {
		t.Errorf("should allow first request")
	}

	if limiter.Allow() {
		t.Errorf("should not allow second request")
	}

	time.Sleep(150 * time.Millisecond) // Wait 150ms, should have ~1 token

	if !limiter.Allow() {
		t.Errorf("should allow request after refill")
	}
}

func TestTokenBucketLimiterAllowN(t *testing.T) {
	limiter := NewTokenBucketLimiter(10, 1.0)

	if !limiter.AllowN(5) {
		t.Errorf("should allow 5 tokens")
	}

	if !limiter.AllowN(5) {
		t.Errorf("should allow another 5 tokens")
	}

	if limiter.AllowN(1) {
		t.Errorf("should not allow 1 more token")
	}
}

func TestQuotaManagerSetQuota(t *testing.T) {
	qm := NewQuotaManager()

	qm.SetQuota("api-calls", 100, time.Minute)

	quota := qm.GetQuota("api-calls")
	if quota == nil {
		t.Fatalf("quota should exist")
	}

	if quota.MaxRequests != 100 {
		t.Errorf("expected 100 max requests")
	}
}

func TestQuotaManagerAcquire(t *testing.T) {
	qm := NewQuotaManager()
	qm.SetQuota("test-resource", 3, time.Minute)

	if !qm.Acquire("test-resource") {
		t.Errorf("should acquire first unit")
	}

	if !qm.Acquire("test-resource") {
		t.Errorf("should acquire second unit")
	}

	if !qm.Acquire("test-resource") {
		t.Errorf("should acquire third unit")
	}

	if qm.Acquire("test-resource") {
		t.Errorf("should not acquire fourth unit")
	}
}

func TestQuotaManagerAcquireN(t *testing.T) {
	qm := NewQuotaManager()
	qm.SetQuota("test-resource", 10, time.Minute)

	if !qm.AcquireN("test-resource", 5) {
		t.Errorf("should acquire 5 units")
	}

	if !qm.AcquireN("test-resource", 5) {
		t.Errorf("should acquire 5 more units")
	}

	if qm.AcquireN("test-resource", 1) {
		t.Errorf("should not acquire 1 more unit")
	}
}

func TestQuotaManagerNoQuota(t *testing.T) {
	qm := NewQuotaManager()

	// No quota set, should always allow
	if !qm.Acquire("unknown") {
		t.Errorf("should allow when no quota set")
	}
}

func TestPerAgentRateLimiter(t *testing.T) {
	limiter := NewPerAgentRateLimiter(10.0)

	if !limiter.Allow("agent-1") {
		t.Errorf("should allow first request")
	}
}

func TestPerAgentRateLimiterCustomLimit(t *testing.T) {
	limiter := NewPerAgentRateLimiter(1.0) // 1 request per second default
	limiter.SetAgentLimit("agent-1", 10.0) // 10 requests per second for agent-1

	// agent-1 should allow more requests than default
	for i := 0; i < 10; i++ {
		if !limiter.Allow("agent-1") {
			t.Errorf("agent-1 should allow request %d", i+1)
		}
	}

	if limiter.Allow("agent-1") {
		t.Errorf("agent-1 should not allow 11th request")
	}
}

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{
		Resource:   "api-calls",
		RetryAfter: 5 * time.Second,
	}

	errStr := err.Error()
	if errStr == "" {
		t.Errorf("error string should not be empty")
	}

	if !strings.Contains(errStr, "api-calls") {
		t.Errorf("error should contain resource name")
	}
}
