package orchestration

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// RetryStrategy defines the retry strategy for failed operations.
type RetryStrategy int

const (
	RetryStrategyNone RetryStrategy = iota
	RetryStrategyConstant
	RetryStrategyLinear
	RetryStrategyExponentialBackoff
)

// RetryConfig defines retry behavior for an operation.
type RetryConfig struct {
	Strategy          RetryStrategy
	MaxAttempts       int
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
	JitterFraction    float64 // 0-1, adds randomness to prevent thundering herd
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		Strategy:          RetryStrategyExponentialBackoff,
		MaxAttempts:       3,
		InitialDelay:      100 * time.Millisecond,
		MaxDelay:          10 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.1,
	}
}

// ComputeDelay calculates the delay before the next retry attempt.
func (rc *RetryConfig) ComputeDelay(attemptNumber int) time.Duration {
	if rc == nil || attemptNumber < 1 {
		return 0
	}

	var baseDelay time.Duration
	switch rc.Strategy {
	case RetryStrategyConstant:
		baseDelay = rc.InitialDelay
	case RetryStrategyLinear:
		baseDelay = time.Duration(int64(rc.InitialDelay) * int64(attemptNumber))
	case RetryStrategyExponentialBackoff:
		multiplier := math.Pow(rc.BackoffMultiplier, float64(attemptNumber-1))
		nanoseconds := float64(rc.InitialDelay.Nanoseconds()) * multiplier
		baseDelay = time.Duration(int64(nanoseconds))
	default:
		return 0
	}

	// Cap at max delay
	if baseDelay > rc.MaxDelay {
		baseDelay = rc.MaxDelay
	}

	// Add jitter
	if rc.JitterFraction > 0 {
		jitter := time.Duration(
			float64(baseDelay) * rc.JitterFraction * (2*rand.Float64() - 1),
		)
		baseDelay += jitter
		if baseDelay < 0 {
			baseDelay = 0
		}
	}

	return baseDelay
}

// CircuitBreakerState represents the state of a circuit breaker.
type CircuitBreakerState int

const (
	CircuitBreakerClosed CircuitBreakerState = iota
	CircuitBreakerOpen
	CircuitBreakerHalfOpen
)

// CircuitBreaker prevents repeated calls to failing operations.
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitBreakerState
	failureCount     int
	successCount     int
	lastFailureTime  time.Time
	failureThreshold int
	successThreshold int
	timeout          time.Duration
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(failureThreshold int, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            CircuitBreakerClosed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
	}
}

// Call executes a function with circuit breaker protection.
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// If open, check if we should transition to half-open
	if cb.state == CircuitBreakerOpen {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = CircuitBreakerHalfOpen
			cb.successCount = 0
		} else {
			return fmt.Errorf("circuit breaker is open")
		}
	}

	// Execute the function
	err := fn()

	if err != nil {
		cb.failureCount++
		cb.lastFailureTime = time.Now()

		// Transition to open if threshold exceeded
		if cb.state == CircuitBreakerHalfOpen || cb.failureCount >= cb.failureThreshold {
			cb.state = CircuitBreakerOpen
		}

		return err
	}

	// Success
	cb.failureCount = 0

	if cb.state == CircuitBreakerHalfOpen {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = CircuitBreakerClosed
		}
	}

	return nil
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitBreakerClosed
	cb.failureCount = 0
	cb.successCount = 0
}

// ErrorRecoveryManager handles recovery strategies for orchestration failures.
type ErrorRecoveryManager struct {
	mu                 sync.RWMutex
	retryConfig        *RetryConfig
	circuitBreakers    map[string]*CircuitBreaker
	failedAgents       map[string]int
	recoveredAgents    map[string]int
	gracefulDegrade    bool
	fallbackStrategies map[string][]string // agentID -> list of fallback agents
}

// NewErrorRecoveryManager creates a new error recovery manager.
func NewErrorRecoveryManager(config *RetryConfig) *ErrorRecoveryManager {
	if config == nil {
		config = DefaultRetryConfig()
	}

	return &ErrorRecoveryManager{
		retryConfig:        config,
		circuitBreakers:    make(map[string]*CircuitBreaker),
		failedAgents:       make(map[string]int),
		recoveredAgents:    make(map[string]int),
		gracefulDegrade:    false,
		fallbackStrategies: make(map[string][]string),
	}
}

// RecordAgentFailure records a failure for an agent.
func (erm *ErrorRecoveryManager) RecordAgentFailure(agentID string) {
	erm.mu.Lock()
	defer erm.mu.Unlock()

	erm.failedAgents[agentID]++

	// Create circuit breaker if needed
	if _, exists := erm.circuitBreakers[agentID]; !exists {
		erm.circuitBreakers[agentID] = NewCircuitBreaker(3, 2, 30*time.Second)
	}
}

// RecordAgentSuccess records a success for a previously failed agent.
func (erm *ErrorRecoveryManager) RecordAgentSuccess(agentID string) {
	erm.mu.Lock()
	defer erm.mu.Unlock()

	if erm.failedAgents[agentID] > 0 {
		erm.recoveredAgents[agentID]++
		erm.failedAgents[agentID]--
	}

	// Reset circuit breaker
	if cb, exists := erm.circuitBreakers[agentID]; exists {
		cb.Reset()
	}
}

// CanRetry checks if an agent can be retried.
func (erm *ErrorRecoveryManager) CanRetry(agentID string, attemptNumber int) bool {
	erm.mu.RLock()
	defer erm.mu.RUnlock()

	if attemptNumber > erm.retryConfig.MaxAttempts {
		return false
	}

	// Check circuit breaker
	if cb, exists := erm.circuitBreakers[agentID]; exists {
		return cb.State() != CircuitBreakerOpen
	}

	return true
}

// ComputeRetryDelay computes the delay before the next retry.
func (erm *ErrorRecoveryManager) ComputeRetryDelay(attemptNumber int) time.Duration {
	erm.mu.RLock()
	defer erm.mu.RUnlock()

	return erm.retryConfig.ComputeDelay(attemptNumber)
}

// SetFallbackStrategy sets the fallback agents for a given agent.
func (erm *ErrorRecoveryManager) SetFallbackStrategy(agentID string, fallbacks []string) {
	erm.mu.Lock()
	defer erm.mu.Unlock()

	erm.fallbackStrategies[agentID] = fallbacks
}

// GetFallbackAgent returns the next fallback agent for a failed agent.
func (erm *ErrorRecoveryManager) GetFallbackAgent(agentID string) string {
	erm.mu.RLock()
	defer erm.mu.RUnlock()

	fallbacks, exists := erm.fallbackStrategies[agentID]
	if !exists || len(fallbacks) == 0 {
		return ""
	}

	return fallbacks[0]
}

// EnableGracefulDegradation enables graceful degradation mode.
func (erm *ErrorRecoveryManager) EnableGracefulDegradation() {
	erm.mu.Lock()
	defer erm.mu.Unlock()

	erm.gracefulDegrade = true
}

// IsGracefulDegradationEnabled checks if graceful degradation is enabled.
func (erm *ErrorRecoveryManager) IsGracefulDegradationEnabled() bool {
	erm.mu.RLock()
	defer erm.mu.RUnlock()

	return erm.gracefulDegrade
}

// GetFailureStats returns failure statistics for an agent.
func (erm *ErrorRecoveryManager) GetFailureStats(agentID string) (failures int, recoveries int) {
	erm.mu.RLock()
	defer erm.mu.RUnlock()

	return erm.failedAgents[agentID], erm.recoveredAgents[agentID]
}

// RetryableOperation wraps an operation with retry logic.
type RetryableOperation struct {
	name       string
	maxRetries int
	backoffFn  func(attempt int) time.Duration
	onRetry    func(attempt int, err error)
}

// NewRetryableOperation creates a new retryable operation.
func NewRetryableOperation(name string, retryConfig *RetryConfig) *RetryableOperation {
	if retryConfig == nil {
		retryConfig = DefaultRetryConfig()
	}

	return &RetryableOperation{
		name:       name,
		maxRetries: retryConfig.MaxAttempts,
		backoffFn:  retryConfig.ComputeDelay,
		onRetry:    nil,
	}
}

// WithOnRetry sets the retry callback.
func (ro *RetryableOperation) WithOnRetry(callback func(attempt int, err error)) *RetryableOperation {
	ro.onRetry = callback
	return ro
}

// Execute runs the operation with retries.
func (ro *RetryableOperation) Execute(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= ro.maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		if attempt < ro.maxRetries {
			if ro.onRetry != nil {
				ro.onRetry(attempt, err)
			}

			delay := ro.backoffFn(attempt)

			select {
			case <-time.After(delay):
				// Continue to next attempt
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("operation %q failed after %d attempts: %w", ro.name, ro.maxRetries, lastErr)
}

// RecoveryStrategy defines how to recover from failures.
type RecoveryStrategy int

const (
	RecoveryStrategyFail RecoveryStrategy = iota
	RecoveryStrategyRetry
	RecoveryStrategyFallback
	RecoveryStrategySkip
)

// RecoveryDecision represents a decision to recover from a failure.
type RecoveryDecision struct {
	Strategy RecoveryStrategy
	Details  string
	Retry    bool
	Fallback string
	Skip     bool
}

// DecisionMaker determines how to recover from a failure.
type DecisionMaker struct {
	mu         sync.RWMutex
	strategies map[string]RecoveryStrategy
	fallbacks  map[string][]string
}

// NewDecisionMaker creates a new recovery decision maker.
func NewDecisionMaker() *DecisionMaker {
	return &DecisionMaker{
		strategies: make(map[string]RecoveryStrategy),
		fallbacks:  make(map[string][]string),
	}
}

// SetStrategy sets the recovery strategy for an agent.
func (dm *DecisionMaker) SetStrategy(agentID string, strategy RecoveryStrategy) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	dm.strategies[agentID] = strategy
}

// SetFallbacks sets the fallback agents for an agent.
func (dm *DecisionMaker) SetFallbacks(agentID string, fallbacks []string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	dm.fallbacks[agentID] = fallbacks
}

// MakeDecision determines the recovery strategy for a failed agent.
func (dm *DecisionMaker) MakeDecision(agentID string, err error, attemptCount int) *RecoveryDecision {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	strategy, exists := dm.strategies[agentID]
	if !exists {
		strategy = RecoveryStrategyRetry
	}

	decision := &RecoveryDecision{
		Strategy: strategy,
	}

	switch strategy {
	case RecoveryStrategyRetry:
		decision.Retry = true
		decision.Details = "retrying operation"
	case RecoveryStrategyFallback:
		fallbacks, ok := dm.fallbacks[agentID]
		if ok && len(fallbacks) > 0 {
			decision.Fallback = fallbacks[0]
			decision.Details = fmt.Sprintf("falling back to %s", decision.Fallback)
		} else {
			decision.Retry = true
			decision.Details = "no fallback available, retrying"
		}
	case RecoveryStrategySkip:
		decision.Skip = true
		decision.Details = "skipping operation as configured"
	default:
		decision.Details = fmt.Sprintf("error: %v", err)
	}

	return decision
}
