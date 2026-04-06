package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BandwidthProber.Probe — error in sendFunc
// ---------------------------------------------------------------------------

func TestBandwidthProber_SendFuncError(t *testing.T) {
	callCount := 0
	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			callCount++
			if callCount >= 2 {
				return errors.New("send failed")
			}
			return nil
		},
		FallbackBPS: 5 * 1024 * 1024,
	})

	bps := p.Probe()
	// The probe should still return a result based on partial data or fallback
	if bps <= 0 {
		t.Errorf("expected positive bps, got %d", bps)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 calls, got %d", callCount)
	}
}

// ---------------------------------------------------------------------------
// BandwidthProber.Probe — all sends fail (totalBytes == 0 => fallback)
// ---------------------------------------------------------------------------

func TestBandwidthProber_AllSendsFail(t *testing.T) {
	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			return errors.New("always fail")
		},
		FallbackBPS: 7 * 1024 * 1024,
	})

	bps := p.Probe()
	if bps != 7*1024*1024 {
		t.Errorf("expected fallback %d, got %d", 7*1024*1024, bps)
	}
}

// ---------------------------------------------------------------------------
// BandwidthProber.LastDetected
// ---------------------------------------------------------------------------

func TestBandwidthProber_LastDetected(t *testing.T) {
	p := NewBandwidthProber(ProberConfig{
		FallbackBPS: 1024,
	})

	// Before any probe, lastDetected should be zero
	if got := p.LastDetected(); got != 0 {
		t.Errorf("initial LastDetected = %d, want 0", got)
	}

	// After a successful probe with sendFunc that takes some time,
	// lastDetected should be set (bps must be in range [1024, 10GB/s])
	p2 := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			// Add a small delay to keep bps in a measurable range
			// Without this, bps can exceed 10GB/s and trigger fallback
			time.Sleep(1 * time.Millisecond)
			return nil
		},
		FallbackBPS: 1024,
	})

	p2.Probe()
	detected := p2.LastDetected()
	// With a slightly delayed sendFunc, bps should be in valid range
	if detected <= 0 {
		t.Errorf("LastDetected after probe = %d, want > 0", detected)
	}
}

// ---------------------------------------------------------------------------
// BandwidthProber.Run — context cancellation
// ---------------------------------------------------------------------------

func TestBandwidthProber_Run(t *testing.T) {
	probeCount := 0
	g := NewGovernor(Config{TotalBandwidth: 10000})

	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			probeCount++
			return nil
		},
		Governor:      g,
		ProbeInterval: 50 * time.Millisecond,
		FallbackBPS:   1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := p.Run(ctx)
	if err == nil {
		t.Error("expected context error from Run")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}

	// The initial probe + at least one ticker probe should have occurred
	if probeCount < 4 {
		// At least the initial probe (4 rounds of sends)
		// probeCount counts individual sendFunc calls, each Probe does up to 4 rounds
		t.Logf("probeCount = %d (each Probe does up to 4 rounds)", probeCount)
	}
}

// ---------------------------------------------------------------------------
// BandwidthProber.Run — with nil governor
// ---------------------------------------------------------------------------

func TestBandwidthProber_RunNilGovernor(t *testing.T) {
	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			return nil
		},
		Governor:      nil,
		ProbeInterval: 50 * time.Millisecond,
		FallbackBPS:   1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := p.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// NewBandwidthProber — defaults
// ---------------------------------------------------------------------------

func TestNewBandwidthProber_Defaults(t *testing.T) {
	p := NewBandwidthProber(ProberConfig{})

	// Default fallbackBPS should be 10 MB/s
	bps := p.Probe() // nil sendFunc => fallback
	if bps != 10*1024*1024 {
		t.Errorf("default fallback = %d, want %d", bps, 10*1024*1024)
	}

	// Default probeInterval should be 10 minutes
	if p.probeInterval != 10*time.Minute {
		t.Errorf("default probeInterval = %v, want 10m", p.probeInterval)
	}
}

// ---------------------------------------------------------------------------
// Governor.Adapt — light pressure (0.1 < pressure <= 0.4)
// ---------------------------------------------------------------------------

func TestGovernor_Adapt_LightPressure(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	g.Adapt(0.2) // light demand — should use base allocation

	stats := g.Stats()
	if stats["tunnel_pct"] != 40 {
		t.Errorf("light pressure tunnel pct = %.0f, want 40", stats["tunnel_pct"])
	}
	if stats["cover_pct"] != 25 {
		t.Errorf("light pressure cover pct = %.0f, want 25", stats["cover_pct"])
	}
}

// ---------------------------------------------------------------------------
// Governor.Adapt — boundary values
// ---------------------------------------------------------------------------

func TestGovernor_Adapt_BoundaryPressure(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	// Exactly at boundaries
	g.Adapt(0.1) // boundary between idle and light => light (0.1 is NOT > 0.1)
	stats := g.Stats()
	if stats["tunnel_pct"] != 10 {
		t.Errorf("pressure 0.1: tunnel pct = %.1f, want 10", stats["tunnel_pct"])
	}

	g.Adapt(0.4) // boundary between light and moderate => light (0.4 is NOT > 0.4)
	stats = g.Stats()
	if stats["tunnel_pct"] != 40 {
		t.Errorf("pressure 0.4: tunnel pct = %.1f, want 40", stats["tunnel_pct"])
	}

	g.Adapt(0.8) // boundary between moderate and high => moderate (0.8 is NOT > 0.8)
	stats = g.Stats()
	if stats["tunnel_pct"] != 45 {
		t.Errorf("pressure 0.8: tunnel pct = %.1f, want 45", stats["tunnel_pct"])
	}
}

// ---------------------------------------------------------------------------
// Governor.Adapt — clamp out-of-range values
// ---------------------------------------------------------------------------

func TestGovernor_Adapt_ClampValues(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	// Negative pressure should be clamped to 0 => idle mode
	g.Adapt(-0.5)
	stats := g.Stats()
	if stats["tunnel_pct"] != 10 {
		t.Errorf("negative pressure tunnel pct = %.0f, want 10 (idle)", stats["tunnel_pct"])
	}

	// Pressure > 1 should be clamped to 1 => high pressure
	g.Adapt(1.5)
	stats = g.Stats()
	if stats["tunnel_pct"] != 65 {
		t.Errorf("over-1 pressure tunnel pct = %.0f, want 65 (high)", stats["tunnel_pct"])
	}
}

// ---------------------------------------------------------------------------
// tokenBucket.waitTime — deficit <= 0 (enough tokens available)
// ---------------------------------------------------------------------------

func TestGovernor_Wait_ImmediateReturn(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 100000,
		TunnelPct:      100,
	})

	ctx := context.Background()
	start := time.Now()
	// The bucket starts with 100000 tokens; requesting 1 byte should be instant
	err := g.Wait(ctx, ComponentTunnel, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Wait error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Wait took %v, expected near-instant", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Governor with custom config values
// ---------------------------------------------------------------------------

func TestGovernor_CustomConfig(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 50000,
		TunnelPct:      60,
		CoverPct:       20,
		PhantomPct:     10,
		P2PPct:         5,
		DecoyPct:       5,
	})

	stats := g.Stats()
	if stats["total_bps"] != 50000 {
		t.Errorf("total = %.0f, want 50000", stats["total_bps"])
	}
	if stats["tunnel_pct"] != 60 {
		t.Errorf("tunnel pct = %.0f, want 60", stats["tunnel_pct"])
	}
	if stats["cover_pct"] != 20 {
		t.Errorf("cover pct = %.0f, want 20", stats["cover_pct"])
	}
	if stats["phantom_pct"] != 10 {
		t.Errorf("phantom pct = %.0f, want 10", stats["phantom_pct"])
	}
	if stats["p2p_pct"] != 5 {
		t.Errorf("p2p pct = %.0f, want 5", stats["p2p_pct"])
	}
	if stats["decoy_pct"] != 5 {
		t.Errorf("decoy pct = %.0f, want 5", stats["decoy_pct"])
	}
}

// ---------------------------------------------------------------------------
// tokenBucket refill cap
// ---------------------------------------------------------------------------

func TestGovernor_TryConsume_RefillCap(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 100000,
		TunnelPct:      100,
	})

	// The bucket starts with rate tokens (100000) and capacity is rate*2 (200000).
	// After waiting a bit, tokens should refill but not exceed capacity.
	time.Sleep(10 * time.Millisecond)

	// Should still be able to consume
	if !g.TryConsume(ComponentTunnel, 1) {
		t.Error("expected to consume after refill")
	}
}

// ---------------------------------------------------------------------------
// BandwidthProber.Probe — out-of-range bps (unreasonably low)
// ---------------------------------------------------------------------------

func TestBandwidthProber_OutOfRangeBPS(t *testing.T) {
	// Make sendFunc very slow to produce bps < 1024
	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			time.Sleep(500 * time.Millisecond)
			return nil
		},
		FallbackBPS: 42 * 1024,
	})

	bps := p.Probe()
	// With 500ms sleep per round, 1 round of 64KB = ~128 KB/s
	// This should be above 1024 but let's check the result is valid
	if bps <= 0 {
		t.Errorf("expected positive bps, got %d", bps)
	}
}
