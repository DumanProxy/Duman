package relay

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowWithinBurst(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	for i := 0; i < 5; i++ {
		if !rl.Allow("10.0.0.1") {
			t.Fatalf("request %d should be allowed within burst", i)
		}
	}
}

func TestRateLimiter_RejectOverBurst(t *testing.T) {
	rl := NewRateLimiter(1, 3) // 1/sec, burst of 3
	for i := 0; i < 3; i++ {
		if !rl.Allow("10.0.0.1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if rl.Allow("10.0.0.1") {
		t.Fatal("4th request should be rejected")
	}
}

func TestRateLimiter_Refill(t *testing.T) {
	rl := NewRateLimiter(100, 2)
	// Exhaust bucket
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	if rl.Allow("10.0.0.1") {
		t.Fatal("should be exhausted")
	}
	// Wait for refill
	time.Sleep(25 * time.Millisecond)
	if !rl.Allow("10.0.0.1") {
		t.Fatal("should have refilled after wait")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	rl.Allow("a")
	rl.Allow("a")
	if rl.Allow("a") {
		t.Fatal("key 'a' should be exhausted")
	}
	// Key 'b' should be independent
	if !rl.Allow("b") {
		t.Fatal("key 'b' should be allowed")
	}
}

func TestRateLimiter_Reset(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	rl.Allow("x")
	rl.Allow("x")
	if rl.Allow("x") {
		t.Fatal("should be exhausted")
	}
	rl.Reset("x")
	if !rl.Allow("x") {
		t.Fatal("should be allowed after reset")
	}
}

func TestRateLimiter_Size(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	if rl.Size() != 0 {
		t.Fatalf("empty limiter should have size 0")
	}
	rl.Allow("a")
	rl.Allow("b")
	rl.Allow("c")
	if rl.Size() != 3 {
		t.Fatalf("size = %d, want 3", rl.Size())
	}
}
