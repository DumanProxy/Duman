package interleave

import (
	"context"
	"math/rand"
	"time"
)

// Jitter adds random delay to tunnel chunk sends for timing decorrelation.
type Jitter struct {
	maxMs int
	rng   *rand.Rand
}

// NewJitter creates a new Jitter with the given maximum delay in milliseconds
// and a deterministic seed for reproducibility.
func NewJitter(maxMs int, seed int64) *Jitter {
	return &Jitter{
		maxMs: maxMs,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// Wait applies a random delay between 0 and maxMs milliseconds.
// Returns immediately if maxMs <= 0 or the context is cancelled.
func (j *Jitter) Wait(ctx context.Context) error {
	if j.maxMs <= 0 {
		return nil
	}

	delay := j.NextDelay()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// NextDelay returns the next delay duration without waiting.
func (j *Jitter) NextDelay() time.Duration {
	if j.maxMs <= 0 {
		return 0
	}
	ms := j.rng.Intn(j.maxMs + 1)
	return time.Duration(ms) * time.Millisecond
}
