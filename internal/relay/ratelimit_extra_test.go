package relay

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_BurstExact(t *testing.T) {
	rl := NewRateLimiter(0.1, 5) // very slow refill
	// All 5 burst tokens should be allowed
	for i := 0; i < 5; i++ {
		if !rl.Allow("ip1") {
			t.Fatalf("request %d should be allowed within burst of 5", i)
		}
	}
	// 6th should be rejected
	if rl.Allow("ip1") {
		t.Fatal("request beyond burst should be rejected")
	}
}

func TestRateLimiter_RefillDoesNotExceedBurst(t *testing.T) {
	rl := NewRateLimiter(1000, 3) // very fast refill, burst of 3
	// Exhaust all tokens
	for i := 0; i < 3; i++ {
		rl.Allow("ip1")
	}
	// Wait long enough to refill well beyond burst
	time.Sleep(50 * time.Millisecond)

	// Should get exactly 3 more (burst cap)
	count := 0
	for rl.Allow("ip1") {
		count++
		if count > 10 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 tokens after refill (burst cap), got %d", count)
	}
}

func TestRateLimiter_MultipleKeys_Independent(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	keys := []string{"192.168.1.1", "192.168.1.2", "10.0.0.1"}

	for _, key := range keys {
		if !rl.Allow(key) {
			t.Fatalf("first request for %s should be allowed", key)
		}
		if !rl.Allow(key) {
			t.Fatalf("second request for %s should be allowed", key)
		}
		if rl.Allow(key) {
			t.Fatalf("third request for %s should be rejected", key)
		}
	}
	if rl.Size() != 3 {
		t.Fatalf("size = %d, want 3", rl.Size())
	}
}

func TestRateLimiter_Reset_OnlyAffectsTarget(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	rl.Allow("a")
	rl.Allow("a")
	rl.Allow("b")
	rl.Allow("b")

	if rl.Size() != 2 {
		t.Fatalf("size = %d, want 2", rl.Size())
	}

	rl.Reset("a")
	if rl.Size() != 1 {
		t.Fatalf("size after reset = %d, want 1", rl.Size())
	}

	// "a" should be allowed again (fresh bucket)
	if !rl.Allow("a") {
		t.Fatal("a should be allowed after reset")
	}
	// "b" should still be exhausted
	if rl.Allow("b") {
		t.Fatal("b should still be exhausted")
	}
}

func TestRateLimiter_Reset_NonexistentKey(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	rl.Allow("a")
	// Resetting a non-existent key should not panic or affect others
	rl.Reset("nonexistent")
	if rl.Size() != 1 {
		t.Fatalf("size = %d, want 1", rl.Size())
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(100, 10)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("ip-%d", id%5)
			for j := 0; j < 50; j++ {
				rl.Allow(key)
			}
		}(i)
	}

	wg.Wait()
	// Just ensure no panics/races. Size should be <= 5.
	if rl.Size() > 5 {
		t.Fatalf("size = %d, want <= 5", rl.Size())
	}
}

func TestRateLimiter_ZeroRate(t *testing.T) {
	rl := NewRateLimiter(0, 3) // zero refill rate
	// Should get exactly burst tokens
	for i := 0; i < 3; i++ {
		if !rl.Allow("ip1") {
			t.Fatalf("request %d should be allowed (burst)", i)
		}
	}
	// No refill ever
	time.Sleep(50 * time.Millisecond)
	if rl.Allow("ip1") {
		t.Fatal("should not refill with zero rate")
	}
}

func TestRateLimiter_Size_EmptyAfterReset(t *testing.T) {
	rl := NewRateLimiter(10, 5)
	rl.Allow("a")
	rl.Allow("b")
	rl.Reset("a")
	rl.Reset("b")
	if rl.Size() != 0 {
		t.Fatalf("size = %d, want 0", rl.Size())
	}
}

func TestRateLimiter_GradualRefill(t *testing.T) {
	// Rate of 100/s, burst of 1
	rl := NewRateLimiter(100, 1)
	if !rl.Allow("ip") {
		t.Fatal("first should be allowed")
	}
	if rl.Allow("ip") {
		t.Fatal("second should be rejected immediately")
	}
	// After 15ms, at least 1 token should have refilled (100/s * 0.015s = 1.5)
	time.Sleep(15 * time.Millisecond)
	if !rl.Allow("ip") {
		t.Fatal("should have refilled after 15ms")
	}
}

func TestRateLimiter_CleanupLoop(t *testing.T) {
	// Create a rate limiter with a very short cleanup interval.
	rl := &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    10,
		burst:   5,
		cleanup: 50 * time.Millisecond, // very short for testing
	}
	go rl.cleanupLoop()

	// Add some entries
	rl.Allow("stale1")
	rl.Allow("stale2")
	if rl.Size() != 2 {
		t.Fatalf("size = %d, want 2", rl.Size())
	}

	// Backdate the entries so they appear stale
	rl.mu.Lock()
	for _, b := range rl.buckets {
		b.lastCheck = time.Now().Add(-10 * time.Minute)
	}
	rl.mu.Unlock()

	// Wait for at least one cleanup cycle
	time.Sleep(150 * time.Millisecond)

	if rl.Size() != 0 {
		t.Fatalf("expected all stale entries to be cleaned up, size = %d", rl.Size())
	}
}

func TestRateLimiter_CleanupKeepsFresh(t *testing.T) {
	rl := &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    10,
		burst:   5,
		cleanup: 100 * time.Millisecond,
	}
	go rl.cleanupLoop()

	// Add entries: "stale" is backdated, "fresh" has a recent timestamp
	rl.Allow("stale")

	rl.mu.Lock()
	rl.buckets["stale"].lastCheck = time.Now().Add(-10 * time.Minute)
	// Create fresh entry with a timestamp far in the future so it won't expire
	rl.buckets["fresh"] = &bucket{tokens: 5, lastCheck: time.Now().Add(10 * time.Minute)}
	rl.mu.Unlock()

	// Wait for one cleanup cycle
	time.Sleep(200 * time.Millisecond)

	if rl.Size() != 1 {
		t.Fatalf("expected 1 entry (fresh), size = %d", rl.Size())
	}

	// Fresh key should still be tracked
	rl.mu.Lock()
	_, hasFresh := rl.buckets["fresh"]
	rl.mu.Unlock()
	if !hasFresh {
		t.Fatal("fresh entry should not have been cleaned up")
	}
}
