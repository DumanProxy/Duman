package interleave

import (
	"context"
	"testing"
	"time"
)

func TestJitter_ZeroMax(t *testing.T) {
	j := NewJitter(0, 42)

	start := time.Now()
	err := j.Wait(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 5*time.Millisecond {
		t.Fatalf("zero max should return immediately, took %v", elapsed)
	}

	// NextDelay should also return 0
	d := j.NextDelay()
	if d != 0 {
		t.Fatalf("zero max NextDelay = %v, want 0", d)
	}
}

func TestJitter_DelayRange(t *testing.T) {
	maxMs := 50
	j := NewJitter(maxMs, 12345)

	for i := 0; i < 1000; i++ {
		d := j.NextDelay()
		if d < 0 {
			t.Fatalf("sample %d: negative delay %v", i, d)
		}
		if d > time.Duration(maxMs)*time.Millisecond {
			t.Fatalf("sample %d: delay %v exceeds max %dms", i, d, maxMs)
		}
	}
}

func TestJitter_Distribution(t *testing.T) {
	maxMs := 100
	j := NewJitter(maxMs, 99999)

	// Divide range into 4 buckets and check each gets some hits
	buckets := make([]int, 4)
	const samples = 1000

	for i := 0; i < samples; i++ {
		d := j.NextDelay()
		ms := int(d / time.Millisecond)
		bucket := ms * 4 / (maxMs + 1)
		if bucket >= 4 {
			bucket = 3
		}
		buckets[bucket]++
	}

	// Each bucket should have at least 10% of samples (expect ~25% each)
	minExpected := samples / 10
	for i, count := range buckets {
		if count < minExpected {
			t.Fatalf("bucket %d has %d samples (min %d): distribution too skewed, buckets=%v",
				i, count, minExpected, buckets)
		}
	}
}

func TestJitter_ContextCancel(t *testing.T) {
	j := NewJitter(10000, 42) // 10 second max - should not actually wait

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	err := j.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("cancelled context should return fast, took %v", elapsed)
	}
}

func TestJitter_Deterministic(t *testing.T) {
	seed := int64(777)
	maxMs := 200

	j1 := NewJitter(maxMs, seed)
	j2 := NewJitter(maxMs, seed)

	for i := 0; i < 100; i++ {
		d1 := j1.NextDelay()
		d2 := j2.NextDelay()
		if d1 != d2 {
			t.Fatalf("sample %d: seed %d produced different delays: %v vs %v", i, seed, d1, d2)
		}
	}
}

func TestJitter_NegativeMax(t *testing.T) {
	j := NewJitter(-5, 42)

	d := j.NextDelay()
	if d != 0 {
		t.Fatalf("negative max NextDelay = %v, want 0", d)
	}

	err := j.Wait(context.Background())
	if err != nil {
		t.Fatalf("negative max Wait should succeed: %v", err)
	}
}
