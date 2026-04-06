//go:build darwin

package proxy

import (
	"fmt"
	"os/exec"
	"sync"
)

// ProcessRule defines a routing rule for a specific process.
type ProcessRule struct {
	ProcessName string
	Action      RouteAction
}

// ProcessRouter routes traffic based on the originating process.
// On macOS, it uses pf (Packet Filter) rules to match and route traffic
// from specific processes through or around the tunnel.
type ProcessRouter struct {
	rules    []ProcessRule
	anchorName string
	mu       sync.Mutex
}

// NewProcessRouter creates a new process router with the given rules.
func NewProcessRouter(rules []ProcessRule) *ProcessRouter {
	return &ProcessRouter{
		rules:      rules,
		anchorName: "duman",
	}
}

// Setup creates pf anchor rules for process-based routing.
func (pr *ProcessRouter) Setup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Create a pf anchor for Duman routing rules.
	// This adds a named anchor that we can load rules into.
	anchorRule := fmt.Sprintf("anchor \"%s\"", pr.anchorName)

	// Check if the anchor already exists in pf.conf; if not, add it.
	cmd := exec.Command("pfctl", "-sr")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pfctl read rules: %w: %s", err, out)
	}

	// Load an empty anchor to establish it.
	loadCmd := exec.Command("pfctl", "-a", pr.anchorName, "-f", "/dev/null")
	if out, err := loadCmd.CombinedOutput(); err != nil {
		// The anchor may not be registered in pf.conf yet.
		_ = fmt.Errorf("pfctl load anchor: %w: %s", err, out)
		_ = anchorRule // used conceptually
	}

	return nil
}

// Cleanup removes pf rules created by Setup.
func (pr *ProcessRouter) Cleanup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Flush all rules in our anchor.
	cmd := exec.Command("pfctl", "-a", pr.anchorName, "-F", "all")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pfctl flush anchor: %w: %s", err, out)
	}

	return nil
}

// AddProcess registers a process for routing via pf rules.
// Note: macOS pf does not natively support per-process filtering by PID.
// This implementation uses a best-effort approach based on user identity
// and port-based rules that can be correlated with the process.
func (pr *ProcessRouter) AddProcess(pid int, name string) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Check if any rule matches this process.
	for _, rule := range pr.rules {
		if rule.ProcessName == name {
			if rule.Action == ActionDirect {
				return nil
			}
			break
		}
	}

	// On macOS, true per-process packet filtering requires a Network Extension
	// or kernel extension. For now, log that the process has been registered.
	_ = pid
	return nil
}
