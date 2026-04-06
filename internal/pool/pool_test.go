package pool

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- Pool tests ----

func TestNewPool_Defaults(t *testing.T) {
	p := NewPool(PoolConfig{})
	if p.maxActive != 3 {
		t.Errorf("maxActive = %d, want 3", p.maxActive)
	}
	if len(p.relays) != 0 {
		t.Errorf("relays = %d, want 0", len(p.relays))
	}
}

func TestNewPool_WithRelays(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "r1:5432", Protocol: "postgresql", Tier: TierCommunity, Weight: 10},
			{Address: "r2:5432", Protocol: "mysql", Tier: TierVerified, Weight: 20},
			{Address: "r3:443", Protocol: "rest", Tier: TierTrusted, Weight: 30},
		},
		MaxActive: 2,
	})

	if len(p.relays) != 3 {
		t.Fatalf("relays = %d, want 3", len(p.relays))
	}
	active := p.ActiveRelays()
	if len(active) != 2 {
		t.Errorf("active = %d, want 2", len(active))
	}
}

func TestPool_AddRelay(t *testing.T) {
	p := NewPool(PoolConfig{})
	p.AddRelay(RelayInfo{Address: "a:1234", Protocol: "postgresql"})
	p.AddRelay(RelayInfo{Address: "b:1234", Protocol: "mysql"})

	stats := p.Stats()
	if stats.Total != 2 {
		t.Errorf("total = %d, want 2", stats.Total)
	}
}

func TestPool_AddRelay_Duplicate(t *testing.T) {
	p := NewPool(PoolConfig{})
	p.AddRelay(RelayInfo{Address: "a:1234"})
	p.AddRelay(RelayInfo{Address: "a:1234"}) // duplicate

	stats := p.Stats()
	if stats.Total != 1 {
		t.Errorf("total = %d, want 1 (duplicate should be ignored)", stats.Total)
	}
}

func TestPool_AddRelay_DefaultWeight(t *testing.T) {
	p := NewPool(PoolConfig{})
	p.AddRelay(RelayInfo{Address: "a:1234", Weight: 0})

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.relays[0].Weight != 1 {
		t.Errorf("weight = %d, want 1 (default)", p.relays[0].Weight)
	}
}

func TestPool_RemoveRelay(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1234"},
			{Address: "b:1234"},
		},
	})

	p.RemoveRelay("a:1234")

	stats := p.Stats()
	if stats.Total != 1 {
		t.Errorf("total = %d, want 1", stats.Total)
	}
}

func TestPool_RemoveRelay_AlsoRemovesFromActive(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1234", Tier: TierTrusted, Weight: 100},
		},
		MaxActive: 5,
	})

	active := p.ActiveRelays()
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1 before removal", len(active))
	}

	p.RemoveRelay("a:1234")
	active = p.ActiveRelays()
	if len(active) != 0 {
		t.Errorf("active = %d, want 0 after removal", len(active))
	}
}

func TestPool_MarkFailed(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1234"},
		},
	})

	p.MarkFailed("a:1234")
	p.MarkFailed("a:1234")

	p.mu.RLock()
	r := p.find("a:1234")
	p.mu.RUnlock()

	if r.State != StateFailed {
		t.Errorf("state = %v, want StateFailed", r.State)
	}
	if r.FailCount != 2 {
		t.Errorf("failCount = %d, want 2", r.FailCount)
	}
}

func TestPool_MarkBlocked(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1234"},
		},
	})

	p.MarkBlocked("a:1234")

	p.mu.RLock()
	r := p.find("a:1234")
	p.mu.RUnlock()

	if r.State != StateBlocked {
		t.Errorf("state = %v, want StateBlocked", r.State)
	}
}

func TestPool_MarkHealthy(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1234"},
		},
	})

	p.MarkFailed("a:1234")
	p.MarkHealthy("a:1234", 50*time.Millisecond)

	p.mu.RLock()
	r := p.find("a:1234")
	p.mu.RUnlock()

	if r.State != StateHealthy {
		t.Errorf("state = %v, want StateHealthy", r.State)
	}
	if r.FailCount != 0 {
		t.Errorf("failCount = %d, want 0 after healthy", r.FailCount)
	}
	if r.Latency != 50*time.Millisecond {
		t.Errorf("latency = %v, want 50ms", r.Latency)
	}
}

func TestPool_SelectActive_PrefersHigherTier(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "community:1", Tier: TierCommunity, Weight: 100},
			{Address: "trusted:1", Tier: TierTrusted, Weight: 1},
			{Address: "verified:1", Tier: TierVerified, Weight: 50},
		},
		MaxActive: 2,
	})

	active := p.SelectActive()
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}
	// Trusted should be first, verified second.
	if active[0].Tier != TierTrusted {
		t.Errorf("first active tier = %v, want TierTrusted", active[0].Tier)
	}
	if active[1].Tier != TierVerified {
		t.Errorf("second active tier = %v, want TierVerified", active[1].Tier)
	}
}

func TestPool_SelectActive_ExcludesBlockedAndFailed(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1", Tier: TierTrusted, Weight: 100},
			{Address: "b:1", Tier: TierTrusted, Weight: 90},
			{Address: "c:1", Tier: TierCommunity, Weight: 1},
		},
		MaxActive: 3,
	})

	p.MarkBlocked("a:1")
	p.MarkFailed("b:1")
	active := p.SelectActive()

	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
	if active[0].Address != "c:1" {
		t.Errorf("active[0] = %q, want c:1", active[0].Address)
	}
}

func TestPool_SelectActive_PrefersLowerLatency(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "slow:1", Tier: TierCommunity, Weight: 10},
			{Address: "fast:1", Tier: TierCommunity, Weight: 10},
		},
		MaxActive: 1,
	})

	p.MarkHealthy("slow:1", 200*time.Millisecond)
	p.MarkHealthy("fast:1", 20*time.Millisecond)

	active := p.SelectActive()
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
	if active[0].Address != "fast:1" {
		t.Errorf("active = %q, want fast:1", active[0].Address)
	}
}

func TestPool_Stats(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "a:1", Protocol: "postgresql", Tier: TierCommunity},
			{Address: "b:1", Protocol: "mysql", Tier: TierVerified},
			{Address: "c:1", Protocol: "rest", Tier: TierTrusted},
		},
		MaxActive: 2,
	})

	p.MarkFailed("a:1")
	p.MarkBlocked("b:1")

	stats := p.Stats()
	if stats.Total != 3 {
		t.Errorf("total = %d, want 3", stats.Total)
	}
	if stats.Healthy != 1 {
		t.Errorf("healthy = %d, want 1", stats.Healthy)
	}
	if stats.Failed != 1 {
		t.Errorf("failed = %d, want 1", stats.Failed)
	}
	if stats.Blocked != 1 {
		t.Errorf("blocked = %d, want 1", stats.Blocked)
	}
	if stats.ByTier[TierCommunity] != 1 {
		t.Errorf("community = %d, want 1", stats.ByTier[TierCommunity])
	}
	if stats.ByProtocol["postgresql"] != 1 {
		t.Errorf("postgresql = %d, want 1", stats.ByProtocol["postgresql"])
	}
}

// ---- Health check tests ----

func TestHealthChecker_Defaults(t *testing.T) {
	p := NewPool(PoolConfig{})
	hc := NewHealthChecker(HealthCheckConfig{Pool: p})

	if hc.interval != 30*time.Second {
		t.Errorf("interval = %v, want 30s", hc.interval)
	}
	if hc.timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", hc.timeout)
	}
	if hc.failThreshold != 3 {
		t.Errorf("failThreshold = %d, want 3", hc.failThreshold)
	}
}

func TestHealthChecker_MockSuccess(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "good:1"},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{Pool: p, Interval: time.Second})
	hc.checkFunc = func(relay *RelayInfo) (time.Duration, error) {
		return 5 * time.Millisecond, nil
	}

	err := hc.CheckRelay(p.relays[0])
	if err != nil {
		t.Fatalf("CheckRelay: %v", err)
	}

	p.mu.RLock()
	r := p.find("good:1")
	p.mu.RUnlock()

	if r.State != StateHealthy {
		t.Errorf("state = %v, want healthy", r.State)
	}
	if r.Latency != 5*time.Millisecond {
		t.Errorf("latency = %v, want 5ms", r.Latency)
	}
}

func TestHealthChecker_MockTimeout(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "timeout:1"},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{Pool: p, FailureThreshold: 2})
	hc.checkFunc = func(relay *RelayInfo) (time.Duration, error) {
		return 0, fmt.Errorf("tcp connect timeout")
	}

	// First failure: marked failed.
	_ = hc.CheckRelay(p.relays[0])
	p.mu.RLock()
	r := p.find("timeout:1")
	state1 := r.State
	p.mu.RUnlock()
	if state1 != StateFailed {
		t.Errorf("after 1 failure: state = %v, want failed", state1)
	}

	// Second failure: exceeds threshold, marked blocked.
	_ = hc.CheckRelay(p.relays[0])
	p.mu.RLock()
	r = p.find("timeout:1")
	state2 := r.State
	p.mu.RUnlock()
	if state2 != StateBlocked {
		t.Errorf("after 2 failures: state = %v, want blocked", state2)
	}
}

func TestHealthChecker_MockTLSFail(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "dpi:1"},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{Pool: p, FailureThreshold: 1})
	hc.checkFunc = func(relay *RelayInfo) (time.Duration, error) {
		return 0, fmt.Errorf("tls handshake failed (likely DPI interference)")
	}

	_ = hc.CheckRelay(p.relays[0])
	p.mu.RLock()
	r := p.find("dpi:1")
	p.mu.RUnlock()

	if r.State != StateBlocked {
		t.Errorf("state = %v, want blocked after TLS fail with threshold=1", r.State)
	}
}

func TestHealthChecker_RunContextCancel(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "r:1"},
		},
	})

	checked := 0
	hc := NewHealthChecker(HealthCheckConfig{Pool: p, Interval: 50 * time.Millisecond})
	hc.checkFunc = func(relay *RelayInfo) (time.Duration, error) {
		checked++
		return time.Millisecond, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := hc.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Run returned %v, want context.DeadlineExceeded", err)
	}
	// Should have run at least the initial check.
	if checked < 1 {
		t.Errorf("checked %d times, want >=1", checked)
	}
}

func TestHealthChecker_BlockDetectionAutoReroute(t *testing.T) {
	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: "blocked:1", Tier: TierTrusted, Weight: 100},
			{Address: "backup:1", Tier: TierCommunity, Weight: 1},
		},
		MaxActive: 1,
	})

	// Initially the trusted relay should be active.
	active := p.ActiveRelays()
	if len(active) != 1 || active[0].Address != "blocked:1" {
		t.Fatalf("initial active = %v, want [blocked:1]", active)
	}

	hc := NewHealthChecker(HealthCheckConfig{Pool: p, FailureThreshold: 2})
	hc.checkFunc = func(relay *RelayInfo) (time.Duration, error) {
		if relay.Address == "blocked:1" {
			return 0, fmt.Errorf("tcp timeout")
		}
		return 10 * time.Millisecond, nil
	}

	// Run two rounds of checks to trigger block detection.
	ctx := context.Background()
	hc.checkAll(ctx)
	hc.checkAll(ctx)

	// After blocking, the backup relay should be active.
	active = p.ActiveRelays()
	if len(active) != 1 || active[0].Address != "backup:1" {
		t.Errorf("after block: active = %v, want [backup:1]", active)
	}
}

func TestHealthChecker_BackoffDuration(t *testing.T) {
	p := NewPool(PoolConfig{})
	hc := NewHealthChecker(HealthCheckConfig{Pool: p, Interval: 10 * time.Second})

	tests := []struct {
		failCount int
		wantMin   time.Duration
		wantMax   time.Duration
	}{
		{0, 10 * time.Second, 10 * time.Second},
		{1, 10 * time.Second, 10 * time.Second},
		{2, 20 * time.Second, 20 * time.Second},
		{3, 40 * time.Second, 40 * time.Second},
		{100, 30 * time.Minute, 30 * time.Minute}, // capped
	}

	for _, tt := range tests {
		got := hc.backoffDuration(tt.failCount)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("backoff(%d) = %v, want [%v, %v]", tt.failCount, got, tt.wantMin, tt.wantMax)
		}
	}
}

// generateTestTLSConfig creates a self-signed TLS certificate at runtime for testing.
func generateTestTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  key,
		}},
	}
}

// TestHealthChecker_RealTCPCheck tests the actual TCP/TLS check against a
// real TLS listener.
func TestHealthChecker_RealTCPCheck(t *testing.T) {
	tlsCfg := generateTestTLSConfig(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	// Accept connections in background, perform TLS handshake, then close.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tlsConn := tls.Server(conn, tlsCfg)
			_ = tlsConn.Handshake()
			// Keep connection alive briefly so the client handshake completes.
			time.Sleep(50 * time.Millisecond)
			tlsConn.Close()
		}
	}()

	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: ln.Addr().String()},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{
		Pool:    p,
		Timeout: 5 * time.Second,
	})

	err = hc.CheckRelay(p.relays[0])
	if err != nil {
		t.Fatalf("CheckRelay against real TLS: %v", err)
	}

	p.mu.RLock()
	r := p.find(ln.Addr().String())
	p.mu.RUnlock()

	if r.State != StateHealthy {
		t.Errorf("state = %v, want healthy", r.State)
	}
	if r.Latency <= 0 {
		t.Error("expected positive latency")
	}
}

// TestHealthChecker_RealTCPCheck_ConnectFail tests against a port that nobody
// is listening on.
func TestHealthChecker_RealTCPCheck_ConnectFail(t *testing.T) {
	// Find a port that isn't listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // close immediately so nobody listens

	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: addr},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{
		Pool:    p,
		Timeout: 1 * time.Second,
	})

	err = hc.CheckRelay(p.relays[0])
	if err == nil {
		t.Error("expected error for closed port")
	}

	p.mu.RLock()
	r := p.find(addr)
	p.mu.RUnlock()

	if r.State != StateFailed {
		t.Errorf("state = %v, want failed", r.State)
	}
}

// TestHealthChecker_RealTCPCheck_TLSFail tests TLS handshake failure when a
// non-TLS server is running (plain TCP).
func TestHealthChecker_RealTCPCheck_TLSFail(t *testing.T) {
	// Start a plain TCP server (no TLS).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Send garbage to fail TLS handshake.
			conn.Write([]byte("not tls"))
			conn.Close()
		}
	}()

	p := NewPool(PoolConfig{
		Relays: []RelayInfo{
			{Address: ln.Addr().String()},
		},
	})

	hc := NewHealthChecker(HealthCheckConfig{
		Pool:    p,
		Timeout: 2 * time.Second,
	})

	err = hc.CheckRelay(p.relays[0])
	if err == nil {
		t.Error("expected TLS handshake error")
	}

	p.mu.RLock()
	r := p.find(ln.Addr().String())
	p.mu.RUnlock()

	if r.State != StateFailed {
		t.Errorf("state = %v, want failed", r.State)
	}
}

// ---- Schedule tests ----

func TestSchedule_Determinism_SameSeed(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "r1:1", Tier: TierCommunity},
		{Address: "r2:1", Tier: TierCommunity},
		{Address: "r3:1", Tier: TierTrusted},
		{Address: "r4:1", Tier: TierCommunity},
		{Address: "r5:1", Tier: TierTrusted},
	}

	cfg := ScheduleConfig{Seed: 42}

	s1 := NewSchedule(relays, cfg)
	s2 := NewSchedule(relays, cfg)

	// Check multiple time points.
	for _, offset := range []time.Duration{0, time.Minute, time.Hour, 24 * time.Hour} {
		t1 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC).Add(offset)

		relay1, exit1 := s1.CurrentSlots(t1)
		relay2, exit2 := s2.CurrentSlots(t1)

		if len(relay1) != len(relay2) {
			t.Fatalf("offset %v: relay slot count mismatch: %d vs %d", offset, len(relay1), len(relay2))
		}
		for i := range relay1 {
			if relay1[i].Address != relay2[i].Address {
				t.Errorf("offset %v: relay[%d] mismatch: %q vs %q", offset, i, relay1[i].Address, relay2[i].Address)
			}
		}
		if (exit1 == nil) != (exit2 == nil) {
			t.Errorf("offset %v: exit nil mismatch", offset)
		}
		if exit1 != nil && exit2 != nil && exit1.Address != exit2.Address {
			t.Errorf("offset %v: exit mismatch: %q vs %q", offset, exit1.Address, exit2.Address)
		}
	}
}

func TestSchedule_DifferentSeed_DifferentSchedule(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "r1:1", Tier: TierCommunity},
		{Address: "r2:1", Tier: TierCommunity},
		{Address: "r3:1", Tier: TierCommunity},
		{Address: "r4:1", Tier: TierCommunity},
		{Address: "r5:1", Tier: TierCommunity},
		{Address: "r6:1", Tier: TierCommunity},
		{Address: "r7:1", Tier: TierCommunity},
		{Address: "r8:1", Tier: TierCommunity},
		{Address: "r9:1", Tier: TierCommunity},
		{Address: "r10:1", Tier: TierCommunity},
	}

	s1 := NewSchedule(relays, ScheduleConfig{Seed: 100})
	s2 := NewSchedule(relays, ScheduleConfig{Seed: 999})

	// Check across many time points; at least some should differ.
	differences := 0
	baseTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		t1 := baseTime.Add(time.Duration(i) * time.Minute)
		r1, _ := s1.CurrentSlots(t1)
		r2, _ := s2.CurrentSlots(t1)

		if len(r1) > 0 && len(r2) > 0 && r1[0].Address != r2[0].Address {
			differences++
		}
	}

	if differences == 0 {
		t.Error("different seeds produced identical schedules across 100 time points")
	}
}

func TestSchedule_EmptyRelays(t *testing.T) {
	s := NewSchedule(nil, ScheduleConfig{Seed: 1})
	relay, exit := s.CurrentSlots(time.Now())
	if relay != nil {
		t.Error("expected nil relays for empty pool")
	}
	if exit != nil {
		t.Error("expected nil exit for empty pool")
	}
}

func TestSchedule_OnlyRelayNoExit(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "r1:1", Tier: TierCommunity},
		{Address: "r2:1", Tier: TierVerified},
	}

	s := NewSchedule(relays, ScheduleConfig{Seed: 1})
	slots, exit := s.CurrentSlots(time.Now())

	if len(slots) == 0 {
		t.Error("expected relay slots")
	}
	if exit != nil {
		t.Error("expected nil exit when no trusted relays")
	}
}

func TestSchedule_ExitSlotOnlyTrusted(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "c1:1", Tier: TierCommunity},
		{Address: "t1:1", Tier: TierTrusted},
		{Address: "c2:1", Tier: TierCommunity},
	}

	s := NewSchedule(relays, ScheduleConfig{Seed: 42})

	// Over many time points, exit should always be the trusted relay.
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		_, exit := s.CurrentSlots(baseTime.Add(time.Duration(i) * time.Hour))
		if exit == nil {
			t.Fatal("expected exit slot")
		}
		if exit.Tier != TierTrusted {
			t.Errorf("exit tier = %v, want TierTrusted", exit.Tier)
		}
	}
}

func TestSchedule_NextRotation(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "r:1", Tier: TierCommunity},
	}
	s := NewSchedule(relays, ScheduleConfig{Seed: 42})
	now := time.Now()

	next := s.NextRotation(now)
	if !next.After(now) {
		t.Errorf("next rotation %v should be after now %v", next, now)
	}
}

func TestSchedule_PreWarm(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "r:1", Tier: TierCommunity},
	}
	s := NewSchedule(relays, ScheduleConfig{
		Seed:            42,
		PreWarmDuration: 30 * time.Second,
	})

	now := time.Now()
	next := s.NextRotation(now)

	// 5 seconds before rotation: should pre-warm.
	justBefore := next.Add(-5 * time.Second)
	if !s.ShouldPreWarm(justBefore) {
		t.Error("should pre-warm 5s before rotation")
	}

	// 2 minutes before rotation: should not pre-warm (unless slot is very short).
	wellBefore := next.Add(-2 * time.Minute)
	if s.ShouldPreWarm(wellBefore) {
		t.Error("should not pre-warm 2min before rotation")
	}
}

func TestSchedule_BlockedRelaysExcluded(t *testing.T) {
	relays := []*RelayInfo{
		{Address: "blocked:1", Tier: TierCommunity, State: StateBlocked},
		{Address: "good:1", Tier: TierCommunity},
	}

	s := NewSchedule(relays, ScheduleConfig{Seed: 42})
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 50; i++ {
		slots, _ := s.CurrentSlots(baseTime.Add(time.Duration(i) * time.Minute))
		for _, r := range slots {
			if r.Address == "blocked:1" {
				t.Fatalf("blocked relay should never appear in slots")
			}
		}
	}
}

// ---- Tier tests ----

func TestTier_String(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierCommunity, "community"},
		{TierVerified, "verified"},
		{TierTrusted, "trusted"},
		{Tier(99), "unknown(99)"},
	}

	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestTier_CanExit(t *testing.T) {
	if TierCommunity.CanExit() {
		t.Error("community should not be able to exit")
	}
	if TierVerified.CanExit() {
		t.Error("verified should not be able to exit")
	}
	if !TierTrusted.CanExit() {
		t.Error("trusted should be able to exit")
	}
}

func TestAutoPromote_Community(t *testing.T) {
	r := &RelayInfo{Tier: TierCommunity}
	tier := AutoPromote(r, 10, 99.0)
	if tier != TierCommunity {
		t.Errorf("10 days: tier = %v, want community", tier)
	}
}

func TestAutoPromote_ToVerified(t *testing.T) {
	r := &RelayInfo{Tier: TierCommunity}
	tier := AutoPromote(r, 90, 95.5)
	if tier != TierVerified {
		t.Errorf("90 days 95.5%%: tier = %v, want verified", tier)
	}
}

func TestAutoPromote_ToTrusted(t *testing.T) {
	r := &RelayInfo{Tier: TierCommunity}
	tier := AutoPromote(r, 180, 99.5)
	if tier != TierTrusted {
		t.Errorf("180 days 99.5%%: tier = %v, want trusted", tier)
	}
}

func TestAutoPromote_NeverDemotes(t *testing.T) {
	r := &RelayInfo{Tier: TierTrusted}
	// Even with 0 uptime, should not demote.
	tier := AutoPromote(r, 0, 0)
	if tier != TierTrusted {
		t.Errorf("trusted with 0 uptime: tier = %v, want trusted (no demotion)", tier)
	}
}

func TestAutoPromote_InsufficientUptime(t *testing.T) {
	r := &RelayInfo{Tier: TierCommunity}
	// Enough days but not enough uptime percentage.
	tier := AutoPromote(r, 100, 90.0)
	if tier != TierCommunity {
		t.Errorf("100 days 90%%: tier = %v, want community", tier)
	}
}

// ---- Manifest tests ----

func TestLoadManifest_FromFile(t *testing.T) {
	manifest := RelayManifest{
		Version: "1.0",
		Relays: []ManifestEntry{
			{Address: "relay1.example.com:5432", Protocol: "postgresql", Tier: 1, Domain: "example.com", Weight: 10},
			{Address: "relay2.example.com:443", Protocol: "rest", Tier: 3, Domain: "example.com", Weight: 20},
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if loaded.Version != "1.0" {
		t.Errorf("version = %q, want 1.0", loaded.Version)
	}
	if len(loaded.Relays) != 2 {
		t.Fatalf("relays = %d, want 2", len(loaded.Relays))
	}
	if loaded.Relays[0].Address != "relay1.example.com:5432" {
		t.Errorf("relay[0].address = %q", loaded.Relays[0].Address)
	}
}

func TestLoadManifest_FileNotFound(t *testing.T) {
	_, err := LoadManifest("/nonexistent/manifest.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadManifest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadManifest(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadManifestURL(t *testing.T) {
	manifest := RelayManifest{
		Version: "2.0",
		Relays: []ManifestEntry{
			{Address: "r1:443", Protocol: "rest", Tier: 2, Weight: 5},
		},
	}
	data, _ := json.Marshal(manifest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer srv.Close()

	loaded, err := LoadManifestURL(srv.URL)
	if err != nil {
		t.Fatalf("LoadManifestURL: %v", err)
	}
	if loaded.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", loaded.Version)
	}
	if len(loaded.Relays) != 1 {
		t.Fatalf("relays = %d, want 1", len(loaded.Relays))
	}
}

func TestLoadManifestURL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := LoadManifestURL(srv.URL)
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}

func TestManifest_ToRelayInfos(t *testing.T) {
	m := &RelayManifest{
		Version: "1.0",
		Relays: []ManifestEntry{
			{Address: "r1:5432", Protocol: "postgresql", Tier: 1, Weight: 10},
			{Address: "r2:443", Protocol: "rest", Tier: 3, Weight: 0},   // weight 0 → 1
			{Address: "r3:3306", Protocol: "mysql", Tier: 99, Weight: 5}, // invalid tier → community
		},
	}

	infos := m.ToRelayInfos()
	if len(infos) != 3 {
		t.Fatalf("infos = %d, want 3", len(infos))
	}

	if infos[0].Tier != TierCommunity {
		t.Errorf("infos[0].tier = %v, want community", infos[0].Tier)
	}
	if infos[1].Weight != 1 {
		t.Errorf("infos[1].weight = %d, want 1 (default)", infos[1].Weight)
	}
	if infos[1].Tier != TierTrusted {
		t.Errorf("infos[1].tier = %v, want trusted", infos[1].Tier)
	}
	if infos[2].Tier != TierCommunity {
		t.Errorf("infos[2].tier = %v, want community (invalid tier normalized)", infos[2].Tier)
	}
	// All should start healthy.
	for i, info := range infos {
		if info.State != StateHealthy {
			t.Errorf("infos[%d].state = %v, want healthy", i, info.State)
		}
	}
}

func TestManifest_ToRelayInfos_Empty(t *testing.T) {
	m := &RelayManifest{Version: "1.0"}
	infos := m.ToRelayInfos()
	if len(infos) != 0 {
		t.Errorf("infos = %d, want 0", len(infos))
	}
}

// ---- RelayState tests ----

func TestRelayState_String(t *testing.T) {
	tests := []struct {
		state RelayState
		want  string
	}{
		{StateHealthy, "healthy"},
		{StateFailed, "failed"},
		{StateBlocked, "blocked"},
		{StatePreWarming, "pre-warming"},
		{RelayState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("RelayState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

