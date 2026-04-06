package interleave

import (
	"math/rand"
	"sync"
)

// Ratio manages the adaptive cover-to-tunnel ratio.
// Uses EWMA smoothing and hysteresis to prevent oscillation.
type Ratio struct {
	mu      sync.Mutex
	base    int // default ratio
	current int
	rng     *rand.Rand

	// EWMA of queue depth for smooth decisions
	ewmaDepth float64
	alpha     float64 // EWMA smoothing factor (0.2 = slow, 0.5 = fast)

	// Hysteresis: require sustained change before adjusting
	stableCount int // consecutive updates at same target
	lastTarget  int
}

// NewRatio creates a new ratio manager.
func NewRatio(base int) *Ratio {
	if base <= 0 {
		base = 3
	}
	return &Ratio{
		base:    base,
		current: base,
		rng:     rand.New(rand.NewSource(42)),
		alpha:   0.3,
	}
}

// Current returns the current cover-to-tunnel ratio.
func (r *Ratio) Current() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Update adjusts the ratio based on tunnel queue depth.
// Uses EWMA smoothing to avoid reacting to transient spikes,
// and hysteresis to require sustained pressure before changing.
//
// Thresholds (on smoothed depth):
//
//	>100 → ratio 1 (emergency flush)
//	50-100 → ratio 2
//	10-50 → base ratio (default 3)
//	0-10 → 2*base, capped at 8 (stealth mode)
func (r *Ratio) Update(queueDepth int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Emergency: react immediately to raw queue depth spike,
	// bypassing EWMA smoothing and hysteresis entirely.
	if queueDepth > 100 {
		r.ewmaDepth = float64(queueDepth)
		r.current = 1
		r.lastTarget = 1
		r.stableCount = 1
		return
	}

	// Update EWMA
	r.ewmaDepth = r.alpha*float64(queueDepth) + (1-r.alpha)*r.ewmaDepth
	smoothed := r.ewmaDepth

	var target int
	switch {
	case smoothed > 100:
		target = 1
	case smoothed > 50:
		target = 2
	case smoothed > 10:
		target = r.base
	default:
		target = r.base * 2
		if target > 8 {
			target = 8
		}
	}

	// Hysteresis: require 3 consecutive updates at the same target
	// before committing to a change (except emergency flush)
	if target == r.lastTarget {
		r.stableCount++
	} else {
		r.stableCount = 1
		r.lastTarget = target
	}

	// Smoothed emergency: also react immediately
	if target == 1 {
		r.current = 1
		return
	}

	// Normal: require sustained pressure
	if r.stableCount < 3 {
		return
	}

	// Smooth transition: move one step toward target
	if r.current < target {
		r.current++
	} else if r.current > target {
		r.current--
	}
}

// SmoothedDepth returns the EWMA-smoothed queue depth (for monitoring).
func (r *Ratio) SmoothedDepth() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ewmaDepth
}

func (r *Ratio) jitter() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.Intn(5)
}
