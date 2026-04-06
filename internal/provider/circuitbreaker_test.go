package provider

import (
	"testing"
	"time"
)

func TestCircuitBreaker_DefaultClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{})
	if cb.State() != CircuitClosed {
		t.Fatal("should start closed")
	}
	if !cb.Allow() {
		t.Fatal("closed circuit should allow")
	}
}

func TestCircuitBreaker_OpensOnThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 3})
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Fatal("should still be closed after 2 failures")
	}
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after 3 failures")
	}
	if cb.Allow() {
		t.Fatal("open circuit should not allow")
	}
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 3})
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	if cb.Failures() != 0 {
		t.Fatalf("failures = %d, want 0 after success", cb.Failures())
	}
	// Should need 3 more failures to open
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Fatal("should still be closed")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 50 * time.Millisecond,
	})
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}

	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("should allow after reset timeout (half-open)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open")
	}
}

func TestCircuitBreaker_HalfOpenClosesOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  2,
	})
	cb.RecordFailure()
	cb.RecordFailure()

	time.Sleep(15 * time.Millisecond)
	cb.Allow() // transitions to half-open

	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should still be half-open after 1 success")
	}
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed after 2 successes in half-open")
	}
}

func TestCircuitBreaker_HalfOpenReopensOnFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Threshold:    1,
		ResetTimeout: 10 * time.Millisecond,
	})
	cb.RecordFailure()

	time.Sleep(15 * time.Millisecond)
	cb.Allow() // half-open

	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should reopen on failure in half-open")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Threshold: 1})
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}
	cb.Reset()
	if cb.State() != CircuitClosed {
		t.Fatal("should be closed after reset")
	}
	if !cb.Allow() {
		t.Fatal("should allow after reset")
	}
}

func TestCircuitBreaker_Defaults(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{})
	if cb.threshold != 5 {
		t.Errorf("default threshold = %d, want 5", cb.threshold)
	}
	if cb.resetTimeout != 30*time.Second {
		t.Errorf("default resetTimeout = %v, want 30s", cb.resetTimeout)
	}
	if cb.halfOpenMax != 2 {
		t.Errorf("default halfOpenMax = %d, want 2", cb.halfOpenMax)
	}
}
