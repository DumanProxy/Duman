package proxy

import (
	"sync"
	"testing"
)

func TestKillSwitch_ConcurrentAccess(t *testing.T) {
	router := NewRouter([]RoutingRule{
		{Domain: "*.tunnel.com", Action: ActionTunnel},
	}, ActionDirect)
	ks := NewKillSwitch(router)

	var wg sync.WaitGroup

	// Concurrent enable/disable
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			ks.Enable()
			ks.Activate()
		}()
		go func() {
			defer wg.Done()
			_ = ks.ShouldBlock("app.tunnel.com:443")
			_ = ks.IsEnabled()
			_ = ks.IsActivated()
		}()
		go func() {
			defer wg.Done()
			ks.Deactivate()
			ks.Disable()
		}()
	}
	wg.Wait()
}

func TestKillSwitch_BlockOnlyTunneledTraffic(t *testing.T) {
	rules := []RoutingRule{
		{Domain: "*.tunnel.com", Action: ActionTunnel},
		{Domain: "*.direct.com", Action: ActionDirect},
		{Domain: "*.blocked.com", Action: ActionBlock},
	}
	router := NewRouter(rules, ActionDirect)
	ks := NewKillSwitch(router)
	ks.Enable()
	ks.Activate()

	tests := []struct {
		dest   string
		block  bool
	}{
		{"app.tunnel.com:443", true},    // tunneled => blocked
		{"app.direct.com:80", false},    // direct => not blocked
		{"app.blocked.com:80", false},   // already blocked by router, kill switch only blocks tunnel
		{"unknown.com:80", false},       // default direct => not blocked
	}
	for _, tt := range tests {
		got := ks.ShouldBlock(tt.dest)
		if got != tt.block {
			t.Errorf("ShouldBlock(%q) = %v, want %v", tt.dest, got, tt.block)
		}
	}
}

func TestKillSwitch_EnableActivateDeactivateEnable(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	ks := NewKillSwitch(router)

	// Enable and activate
	ks.Enable()
	ks.Activate()
	if !ks.ShouldBlock("anything:80") {
		t.Error("should block when enabled+activated")
	}

	// Deactivate
	ks.Deactivate()
	if ks.ShouldBlock("anything:80") {
		t.Error("should not block when deactivated")
	}

	// Re-activate
	ks.Activate()
	if !ks.ShouldBlock("anything:80") {
		t.Error("should block when re-activated")
	}

	// Disable clears both
	ks.Disable()
	if ks.ShouldBlock("anything:80") {
		t.Error("should not block after disable")
	}
	if ks.IsEnabled() {
		t.Error("should not be enabled after disable")
	}
	if ks.IsActivated() {
		t.Error("should not be activated after disable")
	}

	// Activate without enable should not block
	ks.Activate()
	if ks.ShouldBlock("anything:80") {
		t.Error("should not block when activated but not enabled")
	}
}

func TestKillSwitch_NotEnabledNotActivated(t *testing.T) {
	router := NewRouter(nil, ActionTunnel)
	ks := NewKillSwitch(router)

	// Neither enabled nor activated
	if ks.ShouldBlock("anything:80") {
		t.Error("should not block when neither enabled nor activated")
	}
}
