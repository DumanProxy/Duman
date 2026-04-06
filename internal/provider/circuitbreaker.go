package provider

import (
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal operation
	CircuitOpen                         // failing, reject requests
	CircuitHalfOpen                     // testing recovery
)

// CircuitBreaker implements the circuit breaker pattern for providers.
// It prevents cascading failures by stopping requests to unhealthy providers.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         CircuitState
	failures      int
	successes     int
	threshold     int           // failures before opening
	resetTimeout  time.Duration // time before half-open
	halfOpenMax   int           // successes needed to close
	lastFailure   time.Time
}

// CircuitBreakerConfig configures a circuit breaker.
type CircuitBreakerConfig struct {
	Threshold    int           // consecutive failures to trip (default 5)
	ResetTimeout time.Duration // time before trying again (default 30s)
	HalfOpenMax  int           // successes needed to close (default 2)
}

// NewCircuitBreaker creates a circuit breaker with the given config.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 2
	}
	return &CircuitBreaker{
		state:        CircuitClosed,
		threshold:    cfg.Threshold,
		resetTimeout: cfg.ResetTimeout,
		halfOpenMax:  cfg.HalfOpenMax,
	}
}

// Allow returns true if the request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = CircuitHalfOpen
			cb.successes = 0
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	if cb.state == CircuitHalfOpen {
		cb.successes++
		if cb.successes >= cb.halfOpenMax {
			cb.state = CircuitClosed
		}
	}
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cb.threshold {
		cb.state = CircuitOpen
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
}

// Failures returns the current failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures
}
