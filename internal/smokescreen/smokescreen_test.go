package smokescreen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- TrafficProfile tests ---

func TestAllProfiles(t *testing.T) {
	profiles := AllProfiles()
	if len(profiles) != 4 {
		t.Fatalf("expected 4 profiles, got %d", len(profiles))
	}

	names := make(map[string]bool)
	for _, p := range profiles {
		if p.Name == "" {
			t.Error("profile has empty name")
		}
		if names[p.Name] {
			t.Errorf("duplicate profile name: %s", p.Name)
		}
		names[p.Name] = true
	}
}

func TestTrafficProfiles_Valid(t *testing.T) {
	for _, p := range AllProfiles() {
		if p.MinBPS <= 0 {
			t.Errorf("%s: MinBPS should be positive", p.Name)
		}
		if p.MaxBPS <= p.MinBPS {
			t.Errorf("%s: MaxBPS (%d) should be > MinBPS (%d)", p.Name, p.MaxBPS, p.MinBPS)
		}
		if p.BurstSize <= 0 {
			t.Errorf("%s: BurstSize should be positive", p.Name)
		}
		if p.BurstPause <= 0 {
			t.Errorf("%s: BurstPause should be positive", p.Name)
		}
	}
}

func TestProfileVideoCall(t *testing.T) {
	p := ProfileVideoCall
	if p.Name != "video_call" {
		t.Errorf("name = %q, want video_call", p.Name)
	}
	if !p.Symmetric {
		t.Error("video call should be symmetric")
	}
	if p.MinBPS < 100*1024 {
		t.Error("video call should have at least 100 KB/s min")
	}
}

func TestProfileMessaging(t *testing.T) {
	p := ProfileMessaging
	if p.Symmetric {
		t.Error("messaging should not be symmetric")
	}
	if p.BurstSize > 1024 {
		t.Error("messaging bursts should be small")
	}
}

func TestProfileGaming(t *testing.T) {
	p := ProfileGaming
	if !p.Symmetric {
		t.Error("gaming should be symmetric")
	}
	if p.BurstPause > 20*time.Millisecond {
		t.Error("gaming should have low latency burst pause")
	}
}

// --- SmokeScreen tests ---

func TestNewSmokeScreen_Defaults(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{})
	if ss.peerCount != 3 {
		t.Errorf("default peerCount = %d, want 3", ss.peerCount)
	}
	if len(ss.profiles) != 4 {
		t.Errorf("default profiles count = %d, want 4", len(ss.profiles))
	}
	if ss.IsRunning() {
		t.Error("should not be running initially")
	}
	if ss.ActivePeers() != 0 {
		t.Error("should have 0 active peers initially")
	}
}

func TestNewSmokeScreen_CustomConfig(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{
		PeerCount: 5,
		Profiles:  []*TrafficProfile{ProfileGaming},
		Seed:      99,
	})
	if ss.peerCount != 5 {
		t.Errorf("peerCount = %d, want 5", ss.peerCount)
	}
	if len(ss.profiles) != 1 {
		t.Errorf("profiles = %d, want 1", len(ss.profiles))
	}
}

func TestSmokeScreen_RandomPeerAddr(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{Seed: 42})

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		addr := ss.randomPeerAddr()
		seen[addr] = true

		// Should have ip:port format
		parts := strings.SplitN(addr, ":", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid addr format: %s", addr)
		}

		ip := parts[0]
		octets := strings.Split(ip, ".")
		if len(octets) != 4 {
			t.Fatalf("invalid IP: %s", ip)
		}

		// Should not be reserved ranges
		if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "127.") || strings.HasPrefix(ip, "0.") {
			t.Errorf("generated reserved IP: %s", ip)
		}
		if strings.HasPrefix(ip, "192.168.") {
			t.Errorf("generated private IP: %s", ip)
		}
	}

	// Should generate diverse addresses
	if len(seen) < 90 {
		t.Errorf("expected diverse addresses, got %d unique out of 100", len(seen))
	}
}

func TestSmokeScreen_Run_CancelledImmediately(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{Seed: 1, PeerCount: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ss.Run(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	if ss.IsRunning() {
		t.Error("should not be running after cancel")
	}
}

func TestSmokeScreen_Run_SetsRunning(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{Seed: 1, PeerCount: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- ss.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	if !ss.IsRunning() {
		t.Error("should be running")
	}

	<-done
	if ss.IsRunning() {
		t.Error("should not be running after done")
	}
}

// --- DecoyManager tests ---

func TestNewDecoyManager_Defaults(t *testing.T) {
	dm := NewDecoyManager(DecoyConfig{})
	if dm.count != 2 {
		t.Errorf("default count = %d, want 2", dm.count)
	}
	if len(dm.targets) != len(defaultDecoyTargets) {
		t.Errorf("targets = %d, want %d", len(dm.targets), len(defaultDecoyTargets))
	}
	if dm.totalWeight <= 0 {
		t.Error("totalWeight should be positive")
	}
	if dm.IsRunning() {
		t.Error("should not be running initially")
	}
}

func TestNewDecoyManager_CustomTargets(t *testing.T) {
	targets := []DecoyTarget{
		{URL: "https://example.com", Weight: 10},
		{URL: "https://test.com", Weight: 5},
	}
	dm := NewDecoyManager(DecoyConfig{
		Targets: targets,
		Count:   3,
		Seed:    42,
	})
	if dm.count != 3 {
		t.Errorf("count = %d, want 3", dm.count)
	}
	if len(dm.targets) != 2 {
		t.Errorf("targets = %d, want 2", len(dm.targets))
	}
	if dm.totalWeight != 15 {
		t.Errorf("totalWeight = %d, want 15", dm.totalWeight)
	}
}

func TestDecoyManager_PickTarget(t *testing.T) {
	dm := NewDecoyManager(DecoyConfig{Seed: 42})

	seen := make(map[string]int)
	for i := 0; i < 1000; i++ {
		target := dm.pickTarget()
		seen[target.URL]++
	}

	// Should pick diverse targets
	if len(seen) < 5 {
		t.Errorf("expected diverse targets, got %d unique", len(seen))
	}

	// Higher weight targets should appear more often
	if seen["https://github.com/"] < seen["https://www.rust-lang.org/"] {
		t.Error("github (weight 15) should appear more than rust-lang (weight 4)")
	}
}

func TestDecoyManager_FetchDecoy(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		// Verify headers
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Chrome") {
			t.Errorf("user-agent should contain Chrome, got %q", ua)
		}
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	dm := NewDecoyManager(DecoyConfig{Seed: 1})
	dm.fetchDecoy(context.Background(), srv.URL)

	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected 1 request, got %d", requestCount)
	}
}

func TestDecoyManager_Run_CancelledImmediately(t *testing.T) {
	dm := NewDecoyManager(DecoyConfig{Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := dm.Run(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if dm.IsRunning() {
		t.Error("should not be running after cancel")
	}
}

func TestDecoyManager_Run_SetsRunning(t *testing.T) {
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Count: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- dm.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	if !dm.IsRunning() {
		t.Error("should be running")
	}

	<-done
	if dm.IsRunning() {
		t.Error("should not be running after done")
	}
}

func TestDefaultDecoyTargets_Valid(t *testing.T) {
	for _, target := range defaultDecoyTargets {
		if target.URL == "" {
			t.Error("empty URL in default targets")
		}
		if !strings.HasPrefix(target.URL, "https://") {
			t.Errorf("decoy target should use HTTPS: %s", target.URL)
		}
		if target.Weight <= 0 {
			t.Errorf("target %s has non-positive weight %d", target.URL, target.Weight)
		}
	}
}
