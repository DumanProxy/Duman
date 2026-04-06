//go:build linux

package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

// ProcessRule defines a routing rule for a specific process.
type ProcessRule struct {
	ProcessName string
	Action      RouteAction
}

// ProcessRouter routes traffic based on the originating process.
// On Linux, it uses cgroup v2 net_cls classification and iptables MARK rules
// to selectively route traffic from specific processes through or around the tunnel.
type ProcessRouter struct {
	rules     []ProcessRule
	cgroupDir string
	mark      uint32
	mu        sync.Mutex
}

// NewProcessRouter creates a new process router with the given rules.
func NewProcessRouter(rules []ProcessRule) *ProcessRouter {
	return &ProcessRouter{
		rules:     rules,
		cgroupDir: "/sys/fs/cgroup/duman",
		mark:      0x444D, // "DM" in hex
	}
}

// Setup creates the cgroup hierarchy and adds iptables rules for process-based routing.
func (pr *ProcessRouter) Setup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Create the cgroup directory for Duman-managed processes.
	if err := os.MkdirAll(pr.cgroupDir, 0755); err != nil {
		return fmt.Errorf("create cgroup dir: %w", err)
	}

	// Add iptables rule to mark packets from our cgroup.
	// Packets marked with our mark will be routed through the tunnel.
	markStr := fmt.Sprintf("0x%x", pr.mark)
	cmd := exec.Command("iptables", "-t", "mangle", "-A", "OUTPUT",
		"-m", "cgroup", "--path", "duman",
		"-j", "MARK", "--set-mark", markStr)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables mark rule: %w: %s", err, out)
	}

	return nil
}

// Cleanup removes cgroup and iptables rules created by Setup.
func (pr *ProcessRouter) Cleanup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Remove iptables rule.
	markStr := fmt.Sprintf("0x%x", pr.mark)
	cmd := exec.Command("iptables", "-t", "mangle", "-D", "OUTPUT",
		"-m", "cgroup", "--path", "duman",
		"-j", "MARK", "--set-mark", markStr)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Log but don't fail — rule may not exist.
		_ = fmt.Errorf("iptables cleanup: %w: %s", err, out)
	}

	// Remove cgroup directory.
	if err := os.Remove(pr.cgroupDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup dir: %w", err)
	}

	return nil
}

// AddProcess adds a process to the Duman cgroup for traffic routing.
// The process will be subject to the routing rules based on its name.
func (pr *ProcessRouter) AddProcess(pid int, name string) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Check if any rule matches this process.
	matched := false
	for _, rule := range pr.rules {
		if rule.ProcessName == name {
			matched = true
			if rule.Action == ActionDirect {
				// Direct traffic: do not add to cgroup.
				return nil
			}
			break
		}
	}

	if !matched {
		// No rule matched; default is to tunnel.
		// If no rules are configured, all processes are tunneled.
	}

	// Add the PID to the cgroup's cgroup.procs file.
	procsPath := filepath.Join(pr.cgroupDir, "cgroup.procs")
	f, err := os.OpenFile(procsPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open cgroup.procs: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		return fmt.Errorf("write pid to cgroup: %w", err)
	}

	return nil
}
