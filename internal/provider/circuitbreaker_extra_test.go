package provider

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_HalfOpen_AllowsRequests(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    1,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  3,
	})

	// Trip the breaker
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after 1 failure (threshold=1)")
	}

	// Wait for reset timeout
	time.Sleep(15 * time.Millisecond)

	// Allow should transition to half-open
	if !cb.Allow() {
		t.Fatal("should allow after reset timeout")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open")
	}

	// Should continue to allow in half-open state
	if !cb.Allow() {
		t.Fatal("should allow in half-open state")
	}
}

func TestCircuitBreaker_OpenDoesNotAllowBeforeTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    1,
		ResetTimeout: 1 * time.Second, // long timeout
	})

	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}

	// Should not allow immediately (well before timeout)
	if cb.Allow() {
		t.Fatal("open circuit should not allow before reset timeout")
	}
}

func TestCircuitBreaker_SuccessInClosedState(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 5})

	// Record some failures but not enough to trip
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.Failures() != 2 {
		t.Errorf("Failures = %d, want 2", cb.Failures())
	}

	// Success should reset failures to 0
	cb.RecordSuccess()
	if cb.Failures() != 0 {
		t.Errorf("Failures after success = %d, want 0", cb.Failures())
	}
	if cb.State() != CircuitClosed {
		t.Fatal("should still be closed")
	}
}

func TestCircuitBreaker_SuccessInClosedDoesNotChangeState(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 5})

	// Record success without any failures
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatal("should remain closed")
	}
	if cb.Failures() != 0 {
		t.Errorf("Failures = %d, want 0", cb.Failures())
	}
}

func TestCircuitBreaker_MultipleTripsAndResets(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  1,
	})

	// First trip
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after first trip")
	}

	// Recover
	time.Sleep(15 * time.Millisecond)
	cb.Allow() // half-open
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed after recovery")
	}

	// Second trip
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after second trip")
	}

	// Reset manually
	cb.Reset()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed after Reset")
	}
	if cb.Failures() != 0 {
		t.Errorf("Failures after Reset = %d, want 0", cb.Failures())
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    100,
		ResetTimeout: 50 * time.Millisecond,
		HalfOpenMax:  10,
	})

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 100

	// Concurrent failures
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				cb.Allow()
				if j%2 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
			}
		}()
	}

	wg.Wait()

	// Just verify it doesn't panic and state is valid
	state := cb.State()
	if state != CircuitClosed && state != CircuitOpen && state != CircuitHalfOpen {
		t.Errorf("invalid state after concurrent access: %d", state)
	}
}

func TestCircuitBreaker_FailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 10})

	for i := 1; i <= 5; i++ {
		cb.RecordFailure()
		if cb.Failures() != i {
			t.Errorf("after %d failures, Failures() = %d", i, cb.Failures())
		}
	}

	cb.RecordSuccess()
	if cb.Failures() != 0 {
		t.Errorf("Failures after success = %d, want 0", cb.Failures())
	}
}

func TestCircuitBreaker_NegativeConfig(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    -10,
		ResetTimeout: -5 * time.Second,
		HalfOpenMax:  -3,
	})

	// All negative values should default
	if cb.threshold != 5 {
		t.Errorf("threshold = %d, want 5 (default)", cb.threshold)
	}
	if cb.resetTimeout != 30*time.Second {
		t.Errorf("resetTimeout = %v, want 30s (default)", cb.resetTimeout)
	}
	if cb.halfOpenMax != 2 {
		t.Errorf("halfOpenMax = %d, want 2 (default)", cb.halfOpenMax)
	}
}

func TestCircuitBreaker_ExactThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 3})

	// Record exactly threshold-1 failures
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed at threshold-1 failures")
	}

	// Record the threshold-th failure
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open at exactly threshold failures")
	}
}

func TestCircuitBreaker_HalfOpenToOpen_FailureResetsSuccessCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    1,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  3,
	})

	// Trip the breaker
	cb.RecordFailure()

	// Wait and transition to half-open
	time.Sleep(15 * time.Millisecond)
	cb.Allow()
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open")
	}

	// Record one success
	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should still be half-open (need 3 successes)")
	}

	// Record a failure - should re-open
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after failure in half-open")
	}

	// Recover again and verify success counter was reset
	time.Sleep(15 * time.Millisecond)
	cb.Allow()
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open again")
	}

	// Need all 3 successes (not just 2 more)
	cb.RecordSuccess()
	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should still be half-open after 2 successes")
	}
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed after 3 successes")
	}
}
