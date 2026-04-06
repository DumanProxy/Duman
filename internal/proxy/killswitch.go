package proxy

import "sync"

// KillSwitch blocks all tunneled traffic when all relays are down.
// When activated, any destination that would normally be tunneled is
// blocked instead, preventing data leaks when the tunnel is unavailable.
type KillSwitch struct {
	router    *Router
	enabled   bool // whether the kill switch feature is turned on
	activated bool // true when all relays are down
	mu        sync.Mutex
}

// NewKillSwitch creates a kill switch tied to the given router.
func NewKillSwitch(router *Router) *KillSwitch {
	return &KillSwitch{
		router: router,
	}
}

// Enable turns on the kill switch feature. When enabled and activated,
// traffic that would be tunneled is blocked.
func (ks *KillSwitch) Enable() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.enabled = true
}

// Disable turns off the kill switch feature. Traffic flows normally
// regardless of relay state.
func (ks *KillSwitch) Disable() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.enabled = false
	ks.activated = false
}

// Activate signals that all relays are down. If the kill switch is enabled,
// tunneled traffic will be blocked.
func (ks *KillSwitch) Activate() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.activated = true
}

// Deactivate signals that at least one relay has reconnected.
// Tunneled traffic resumes normally.
func (ks *KillSwitch) Deactivate() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.activated = false
}

// ShouldBlock returns true if the destination should be blocked.
// A destination is blocked when the kill switch is both enabled and
// activated, and the router would normally tunnel the traffic.
func (ks *KillSwitch) ShouldBlock(dest string) bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	if !ks.enabled || !ks.activated {
		return false
	}

	// Only block traffic that would be tunneled
	return ks.router.Decide(dest) == ActionTunnel
}

// IsEnabled returns whether the kill switch is enabled.
func (ks *KillSwitch) IsEnabled() bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.enabled
}

// IsActivated returns whether the kill switch is currently activated.
func (ks *KillSwitch) IsActivated() bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.activated
}
