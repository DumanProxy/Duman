package governor

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestGovernor_DefaultConfig(t *testing.T) {
	g := NewGovernor(Config{})

	stats := g.Stats()
	if stats["total_bps"] != float64(10*1024*1024) {
		t.Errorf("default total = %.0f, want %d", stats["total_bps"], 10*1024*1024)
	}
	if stats["tunnel_pct"] != 40 {
		t.Errorf("tunnel pct = %.0f, want 40", stats["tunnel_pct"])
	}
}

func TestGovernor_TryConsume(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 1000, // 1000 bytes/sec
		TunnelPct:      50,
	})

	// Tunnel bucket has 500 bytes/sec rate, starts with 500 tokens
	if !g.TryConsume(ComponentTunnel, 100) {
		t.Error("expected to consume 100 bytes from tunnel bucket")
	}
	if !g.TryConsume(ComponentTunnel, 100) {
		t.Error("expected to consume another 100 bytes")
	}
}

func TestGovernor_TryConsume_Exhausted(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 100, // 100 bytes/sec
		TunnelPct:      50,  // 50 bytes/sec for tunnel, capacity=100
	})

	// Drain the bucket completely
	g.TryConsume(ComponentTunnel, 100)

	// Trying to consume a large amount should fail (only a tiny refill since drain)
	if g.TryConsume(ComponentTunnel, 1000) {
		t.Error("expected consume to fail when bucket exhausted")
	}
}

func TestGovernor_Wait(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 100000, // 100KB/sec
		TunnelPct:      100,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := g.Wait(ctx, ComponentTunnel, 1000)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Wait failed: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Wait took too long: %v", elapsed)
	}
}

func TestGovernor_Wait_ContextCancelled(t *testing.T) {
	g := NewGovernor(Config{
		TotalBandwidth: 1, // extremely slow
		TunnelPct:      100,
	})

	// Drain bucket
	g.TryConsume(ComponentTunnel, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := g.Wait(ctx, ComponentTunnel, 1000000) // huge request
	if err == nil {
		t.Error("expected context error")
	}
}

func TestGovernor_Adapt_HighPressure(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	g.Adapt(0.9) // high tunnel demand

	stats := g.Stats()
	if stats["tunnel_pct"] != 65 {
		t.Errorf("high pressure tunnel pct = %.0f, want 65", stats["tunnel_pct"])
	}
	if stats["phantom_pct"] != 8 {
		t.Errorf("high pressure phantom pct = %.0f, want 8", stats["phantom_pct"])
	}
}

func TestGovernor_Adapt_Idle(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	g.Adapt(0.0) // idle — stealth mode

	stats := g.Stats()
	if stats["tunnel_pct"] != 10 {
		t.Errorf("idle tunnel pct = %.0f, want 10", stats["tunnel_pct"])
	}
	if stats["phantom_pct"] != 35 {
		t.Errorf("idle phantom pct = %.0f, want 35", stats["phantom_pct"])
	}
}

func TestGovernor_Adapt_Moderate(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 10000})

	g.Adapt(0.6) // moderate demand

	stats := g.Stats()
	if stats["tunnel_pct"] != 45 {
		t.Errorf("moderate tunnel pct = %.0f, want 45", stats["tunnel_pct"])
	}
}

func TestGovernor_SetTotalBandwidth(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 1000, TunnelPct: 50})

	g.SetTotalBandwidth(2000)

	stats := g.Stats()
	if stats["total_bps"] != 2000 {
		t.Errorf("total = %.0f, want 2000", stats["total_bps"])
	}
	// Tunnel rate should double (50% of 2000 = 1000)
	if math.Abs(stats["tunnel_rate"]-1000) > 1 {
		t.Errorf("tunnel rate = %.0f, want 1000", stats["tunnel_rate"])
	}
}

func TestGovernor_AllComponents(t *testing.T) {
	g := NewGovernor(Config{TotalBandwidth: 100000})

	components := []Component{
		ComponentTunnel, ComponentCover, ComponentPhantom,
		ComponentP2P, ComponentDecoy,
	}

	for _, c := range components {
		if !g.TryConsume(c, 100) {
			t.Errorf("component %d should have tokens", c)
		}
	}
}

func TestBandwidthProber_FallbackOnNilFunc(t *testing.T) {
	p := NewBandwidthProber(ProberConfig{
		FallbackBPS: 5 * 1024 * 1024,
	})

	bps := p.Probe()
	if bps != 5*1024*1024 {
		t.Errorf("expected fallback %d, got %d", 5*1024*1024, bps)
	}
}

func TestBandwidthProber_WithSendFunc(t *testing.T) {
	totalSent := 0
	p := NewBandwidthProber(ProberConfig{
		SendFunc: func(data []byte) error {
			totalSent += len(data)
			return nil
		},
		FallbackBPS: 1024,
	})

	bps := p.Probe()
	if totalSent == 0 {
		t.Error("expected sendFunc to be called")
	}
	if bps < 1024 {
		t.Errorf("detected bps %d too low", bps)
	}
}
