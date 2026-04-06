package governor

import (
	"context"
	"math"
	"sync"
	"time"
)

// Component identifies a traffic source for bandwidth allocation.
type Component int

const (
	ComponentTunnel  Component = iota // encrypted tunnel data
	ComponentCover                    // cover SQL queries
	ComponentPhantom                  // phantom browser HTTP
	ComponentP2P                      // P2P smoke screen
	ComponentDecoy                    // decoy connections
)

// Config configures the bandwidth governor.
type Config struct {
	TotalBandwidth int64 // total bandwidth in bytes/sec (0 = auto-detect)

	// Budget percentages (must sum to 100)
	TunnelPct  int // default 40
	CoverPct   int // default 25
	PhantomPct int // default 20
	P2PPct     int // default 10
	DecoyPct   int // default 5
}

// Governor implements adaptive token bucket rate limiting across components.
type Governor struct {
	mu       sync.Mutex
	total    int64 // total bandwidth bytes/sec
	buckets  [5]*tokenBucket
	budgets  [5]float64 // current percentage allocation per component
	basePcts [5]float64 // base percentage allocation
}

// tokenBucket implements a basic token bucket rate limiter.
type tokenBucket struct {
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	lastFill time.Time
}

func newTokenBucket(rate float64) *tokenBucket {
	return &tokenBucket{
		tokens:   rate, // start full
		capacity: rate * 2,
		rate:     rate,
		lastFill: time.Now(),
	}
}

func (tb *tokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastFill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastFill = now
}

func (tb *tokenBucket) tryConsume(bytes int64) bool {
	tb.refill()
	if tb.tokens >= float64(bytes) {
		tb.tokens -= float64(bytes)
		return true
	}
	return false
}

func (tb *tokenBucket) waitTime(bytes int64) time.Duration {
	tb.refill()
	deficit := float64(bytes) - tb.tokens
	if deficit <= 0 {
		return 0
	}
	return time.Duration(deficit / tb.rate * float64(time.Second))
}

func (tb *tokenBucket) setRate(rate float64) {
	tb.refill()
	tb.rate = rate
	tb.capacity = rate * 2
}

// NewGovernor creates a bandwidth governor.
func NewGovernor(cfg Config) *Governor {
	if cfg.TotalBandwidth <= 0 {
		cfg.TotalBandwidth = 10 * 1024 * 1024 // default 10 MB/s
	}
	if cfg.TunnelPct <= 0 {
		cfg.TunnelPct = 40
	}
	if cfg.CoverPct <= 0 {
		cfg.CoverPct = 25
	}
	if cfg.PhantomPct <= 0 {
		cfg.PhantomPct = 20
	}
	if cfg.P2PPct <= 0 {
		cfg.P2PPct = 10
	}
	if cfg.DecoyPct <= 0 {
		cfg.DecoyPct = 5
	}

	total := float64(cfg.TotalBandwidth)
	basePcts := [5]float64{
		float64(cfg.TunnelPct) / 100,
		float64(cfg.CoverPct) / 100,
		float64(cfg.PhantomPct) / 100,
		float64(cfg.P2PPct) / 100,
		float64(cfg.DecoyPct) / 100,
	}

	g := &Governor{
		total:    cfg.TotalBandwidth,
		basePcts: basePcts,
		budgets:  basePcts,
	}

	for i := range g.buckets {
		rate := total * basePcts[i]
		g.buckets[i] = newTokenBucket(rate)
	}

	return g
}

// Wait blocks until the specified number of bytes can be sent for the component.
func (g *Governor) Wait(ctx context.Context, comp Component, bytes int64) error {
	for {
		g.mu.Lock()
		bucket := g.buckets[comp]
		if bucket.tryConsume(bytes) {
			g.mu.Unlock()
			return nil
		}
		wait := bucket.waitTime(bytes)
		g.mu.Unlock()

		// Cap wait to avoid long blocks
		if wait > 5*time.Second {
			wait = 5 * time.Second
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// TryConsume attempts to consume bytes without blocking.
// Returns true if the bytes were consumed.
func (g *Governor) TryConsume(comp Component, bytes int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buckets[comp].tryConsume(bytes)
}

// Adapt adjusts bandwidth allocation based on tunnel queue pressure.
// tunnelPressure: 0.0 (idle) to 1.0 (maximum demand)
func (g *Governor) Adapt(tunnelPressure float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	tunnelPressure = math.Max(0, math.Min(1, tunnelPressure))

	// Adaptive allocation:
	// High pressure: tunnel gets more, phantom/P2P get less
	// Low pressure: tunnel gets less, phantom/cover get more (stealth mode)
	var newPcts [5]float64

	if tunnelPressure > 0.8 {
		// Heavy tunnel demand: prioritize tunnel
		newPcts[ComponentTunnel] = 0.65
		newPcts[ComponentCover] = 0.20
		newPcts[ComponentPhantom] = 0.08
		newPcts[ComponentP2P] = 0.05
		newPcts[ComponentDecoy] = 0.02
	} else if tunnelPressure > 0.4 {
		// Moderate demand: balanced
		newPcts[ComponentTunnel] = 0.45
		newPcts[ComponentCover] = 0.25
		newPcts[ComponentPhantom] = 0.15
		newPcts[ComponentP2P] = 0.10
		newPcts[ComponentDecoy] = 0.05
	} else if tunnelPressure > 0.1 {
		// Light demand: base allocation
		newPcts = g.basePcts
	} else {
		// Idle: maximize stealth (phantom + cover)
		newPcts[ComponentTunnel] = 0.10
		newPcts[ComponentCover] = 0.30
		newPcts[ComponentPhantom] = 0.35
		newPcts[ComponentP2P] = 0.15
		newPcts[ComponentDecoy] = 0.10
	}

	total := float64(g.total)
	for i := range g.buckets {
		g.budgets[i] = newPcts[i]
		g.buckets[i].setRate(total * newPcts[i])
	}
}

// SetTotalBandwidth updates the total bandwidth (e.g., after auto-detection).
func (g *Governor) SetTotalBandwidth(bps int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.total = bps
	total := float64(bps)
	for i := range g.buckets {
		g.buckets[i].setRate(total * g.budgets[i])
	}
}

// Stats returns current allocation stats.
func (g *Governor) Stats() map[string]float64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	names := [5]string{"tunnel", "cover", "phantom", "p2p", "decoy"}
	stats := make(map[string]float64, 5)
	for i, name := range names {
		stats[name+"_pct"] = g.budgets[i] * 100
		stats[name+"_rate"] = g.buckets[i].rate
	}
	stats["total_bps"] = float64(g.total)
	return stats
}
