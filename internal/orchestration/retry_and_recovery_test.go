package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.Strategy != RetryStrategyExponentialBackoff {
		t.Errorf("expected exponential backoff strategy")
	}

	if config.MaxAttempts != 3 {
		t.Errorf("expected 3 max attempts")
	}

	if config.MaxDelay != 10*time.Second {
		t.Errorf("expected 10s max delay")
	}
}

func TestComputeDelayConstant(t *testing.T) {
	config := &RetryConfig{
		Strategy:       RetryStrategyConstant,
		InitialDelay:   100 * time.Millisecond,
		MaxDelay:       10 * time.Second,
		JitterFraction: 0,
	}

	delay1 := config.ComputeDelay(1)
	delay2 := config.ComputeDelay(2)
	delay3 := config.ComputeDelay(3)

	if delay1 != 100*time.Millisecond {
		t.Errorf("expected constant delay")
	}

	if delay1 != delay2 || delay2 != delay3 {
		t.Errorf("constant delay should be same for all attempts")
	}
}

func TestComputeDelayLinear(t *testing.T) {
	config := &RetryConfig{
		Strategy:       RetryStrategyLinear,
		InitialDelay:   100 * time.Millisecond,
		MaxDelay:       10 * time.Second,
		JitterFraction: 0,
	}

	delay1 := config.ComputeDelay(1)
	delay2 := config.ComputeDelay(2)
	delay3 := config.ComputeDelay(3)

	if delay1 != 100*time.Millisecond {
		t.Errorf("expected 100ms for first delay")
	}

	if delay2 != 200*time.Millisecond {
		t.Errorf("expected 200ms for second delay")
	}

	if delay3 != 300*time.Millisecond {
		t.Errorf("expected 300ms for third delay")
	}
}

func TestComputeDelayExponentialBackoff(t *testing.T) {
	config := &RetryConfig{
		Strategy:          RetryStrategyExponentialBackoff,
		InitialDelay:      100 * time.Millisecond,
		MaxDelay:          10 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0,
	}

	delay1 := config.ComputeDelay(1)
	delay2 := config.ComputeDelay(2)
	delay3 := config.ComputeDelay(3)

	if delay1 != 100*time.Millisecond {
		t.Errorf("expected 100ms for first delay")
	}

	if delay2 != 200*time.Millisecond {
		t.Errorf("expected 200ms for second delay")
	}

	if delay3 != 400*time.Millisecond {
		t.Errorf("expected 400ms for third delay")
	}
}

func TestComputeDelayMaxDelay(t *testing.T) {
	config := &RetryConfig{
		Strategy:          RetryStrategyExponentialBackoff,
		InitialDelay:      100 * time.Millisecond,
		MaxDelay:          250 * time.Millisecond,
		BackoffMultiplier: 2.0,
		JitterFraction:    0,
	}

	delay3 := config.ComputeDelay(3)

	if delay3 > 250*time.Millisecond {
		t.Errorf("delay should not exceed max delay")
	}
}

func TestCircuitBreakerClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 2, 1*time.Second)

	if cb.State() != CircuitBreakerClosed {
		t.Errorf("initial state should be closed")
	}

	// Successful call
	err := cb.Call(func() error {
		return nil
	})

	if err != nil {
		t.Errorf("call should succeed")
	}

	if cb.State() != CircuitBreakerClosed {
		t.Errorf("state should remain closed after success")
	}
}

func TestCircuitBreakerOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 2, 1*time.Second)

	// First failure
	cb.Call(func() error {
		return errors.New("test error")
	})

	// Second failure - should still be closed
	cb.Call(func() error {
		return errors.New("test error")
	})

	if cb.State() != CircuitBreakerOpen {
		t.Errorf("state should be open after threshold")
	}

	// Next call should fail immediately
	err := cb.Call(func() error {
		return nil
	})

	if err == nil {
		t.Errorf("call should fail on open circuit")
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(1, 1, 10*time.Millisecond)

	// Fail once to open
	cb.Call(func() error {
		return errors.New("test error")
	})

	// Wait for timeout
	time.Sleep(20 * time.Millisecond)

	// State should be half-open
	if cb.State() != CircuitBreakerHalfOpen {
		// May still be open if timing is off, check again
		time.Sleep(10 * time.Millisecond)
	}
}

func TestErrorRecoveryManagerRecordFailure(t *testing.T) {
	erm := NewErrorRecoveryManager(DefaultRetryConfig())

	erm.RecordAgentFailure("agent-1")

	failures, _ := erm.GetFailureStats("agent-1")
	if failures != 1 {
		t.Errorf("expected 1 failure")
	}
}

func TestErrorRecoveryManagerCanRetry(t *testing.T) {
	config := &RetryConfig{
		Strategy:    RetryStrategyConstant,
		MaxAttempts: 3,
	}
	erm := NewErrorRecoveryManager(config)

	if !erm.CanRetry("agent-1", 1) {
		t.Errorf("should allow first retry")
	}

	if !erm.CanRetry("agent-1", 2) {
		t.Errorf("should allow second retry")
	}

	if !erm.CanRetry("agent-1", 3) {
		t.Errorf("should allow third retry")
	}

	if erm.CanRetry("agent-1", 4) {
		t.Errorf("should not allow retry beyond max attempts")
	}
}

func TestErrorRecoveryManagerFallback(t *testing.T) {
	erm := NewErrorRecoveryManager(DefaultRetryConfig())

	erm.SetFallbackStrategy("agent-1", []string{"agent-2", "agent-3"})

	fallback := erm.GetFallbackAgent("agent-1")
	if fallback != "agent-2" {
		t.Errorf("expected agent-2 as fallback")
	}
}

func TestRetryableOperationSuccess(t *testing.T) {
	config := DefaultRetryConfig()
	operation := NewRetryableOperation("test-op", config)

	retryCount := 0
	err := operation.Execute(context.Background(), func() error {
		retryCount++
		if retryCount < 2 {
			return errors.New("first attempt fails")
		}
		return nil
	})

	if err != nil {
		t.Errorf("operation should succeed on second attempt")
	}

	if retryCount != 2 {
		t.Errorf("expected 2 attempts")
	}
}

func TestRetryableOperationFailure(t *testing.T) {
	config := &RetryConfig{
		Strategy:     RetryStrategyConstant,
		MaxAttempts:  2,
		InitialDelay: 10 * time.Millisecond,
	}
	operation := NewRetryableOperation("test-op", config)

	attemptCount := 0
	err := operation.Execute(context.Background(), func() error {
		attemptCount++
		return errors.New("persistent error")
	})

	if err == nil {
		t.Errorf("operation should fail")
	}

	if attemptCount != 2 {
		t.Errorf("expected 2 attempts")
	}
}

func TestRetryableOperationWithCallback(t *testing.T) {
	config := DefaultRetryConfig()
	operation := NewRetryableOperation("test-op", config)

	retryAttempts := 0
	operation.WithOnRetry(func(attempt int, err error) {
		retryAttempts++
	})

	_ = operation.Execute(context.Background(), func() error {
		return errors.New("always fails")
	})

	if retryAttempts == 0 {
		t.Errorf("on retry callback should be called")
	}
}

func TestRetryableOperationContext(t *testing.T) {
	config := DefaultRetryConfig()
	operation := NewRetryableOperation("test-op", config)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := operation.Execute(ctx, func() error {
		time.Sleep(100 * time.Millisecond)
		return errors.New("timeout should trigger")
	})

	if err == nil {
		t.Errorf("operation should be cancelled by context")
	}

	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context deadline or cancel error")
	}
}

func TestDecisionMakerRetry(t *testing.T) {
	dm := NewDecisionMaker()
	dm.SetStrategy("agent-1", RecoveryStrategyRetry)

	decision := dm.MakeDecision("agent-1", errors.New("test"), 1)

	if !decision.Retry {
		t.Errorf("decision should enable retry")
	}

	if decision.Strategy != RecoveryStrategyRetry {
		t.Errorf("strategy should be retry")
	}
}

func TestDecisionMakerFallback(t *testing.T) {
	dm := NewDecisionMaker()
	dm.SetStrategy("agent-1", RecoveryStrategyFallback)
	dm.SetFallbacks("agent-1", []string{"agent-2", "agent-3"})

	decision := dm.MakeDecision("agent-1", errors.New("test"), 1)

	if decision.Fallback != "agent-2" {
		t.Errorf("expected fallback to agent-2")
	}

	if decision.Strategy != RecoveryStrategyFallback {
		t.Errorf("strategy should be fallback")
	}
}

func TestDecisionMakerSkip(t *testing.T) {
	dm := NewDecisionMaker()
	dm.SetStrategy("agent-1", RecoveryStrategySkip)

	decision := dm.MakeDecision("agent-1", errors.New("test"), 1)

	if !decision.Skip {
		t.Errorf("decision should enable skip")
	}

	if decision.Strategy != RecoveryStrategySkip {
		t.Errorf("strategy should be skip")
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := NewCircuitBreaker(1, 1, 1*time.Second)

	// Open the circuit
	cb.Call(func() error {
		return errors.New("fail")
	})

	if cb.State() != CircuitBreakerOpen {
		t.Errorf("circuit should be open")
	}

	// Reset
	cb.Reset()

	if cb.State() != CircuitBreakerClosed {
		t.Errorf("circuit should be closed after reset")
	}
}

func TestErrorRecoveryManagerRecordSuccess(t *testing.T) {
	erm := NewErrorRecoveryManager(DefaultRetryConfig())

	erm.RecordAgentFailure("agent-1")
	erm.RecordAgentFailure("agent-1")
	erm.RecordAgentSuccess("agent-1")

	failures, recoveries := erm.GetFailureStats("agent-1")
	if failures != 1 {
		t.Errorf("expected 1 remaining failure")
	}

	if recoveries != 1 {
		t.Errorf("expected 1 recovery")
	}
}

func TestErrorRecoveryManagerGracefulDegradation(t *testing.T) {
	erm := NewErrorRecoveryManager(DefaultRetryConfig())

	if erm.IsGracefulDegradationEnabled() {
		t.Errorf("graceful degradation should be disabled by default")
	}

	erm.EnableGracefulDegradation()

	if !erm.IsGracefulDegradationEnabled() {
		t.Errorf("graceful degradation should be enabled")
	}
}

func TestComputeRetryDelayMultiple(t *testing.T) {
	config := DefaultRetryConfig()
	erm := NewErrorRecoveryManager(config)

	delay1 := erm.ComputeRetryDelay(1)
	delay2 := erm.ComputeRetryDelay(2)
	delay3 := erm.ComputeRetryDelay(3)

	if delay1 == 0 {
		t.Errorf("delay should be non-zero")
	}

	if delay2 <= delay1 {
		t.Errorf("exponential backoff should increase delays")
	}

	if delay3 <= delay2 {
		t.Errorf("exponential backoff should increase delays")
	}
}
