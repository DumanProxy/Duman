//go:build windows

package proxy

import (
	"errors"
	"sync"
)

// ProcessRule defines a routing rule for a specific process.
type ProcessRule struct {
	ProcessName string
	Action      RouteAction
}

// ProcessRouter routes traffic based on the originating process.
// On Windows, full per-process routing requires WFP (Windows Filtering Platform)
// via the Fwpm* API family. This implementation provides a stub that tracks
// rules and processes but does not install actual WFP filters.
//
// A production implementation would use:
//   - FwpmEngineOpen0 to open a WFP session
//   - FwpmFilterAdd0 with FWPM_CONDITION_ALE_APP_ID to match by executable path
//   - FwpmCalloutAdd0 for custom callout handling
//
// Alternatively, a simpler approach can correlate connections to processes
// by querying the TCP/UDP table via GetExtendedTcpTable / GetExtendedUdpTable.
type ProcessRouter struct {
	rules []ProcessRule
	pids  map[int]string // pid -> process name
	mu    sync.Mutex
}

// NewProcessRouter creates a new process router with the given rules.
func NewProcessRouter(rules []ProcessRule) *ProcessRouter {
	return &ProcessRouter{
		rules: rules,
		pids:  make(map[int]string),
	}
}

// Setup initializes per-process routing. On Windows, this is a no-op stub
// because full WFP integration requires elevated privileges and the WFP API.
func (pr *ProcessRouter) Setup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// WFP-based implementation would:
	// 1. Call FwpmEngineOpen0 to get a WFP engine handle
	// 2. Create a sublayer for Duman rules
	// 3. Add filters matching target application paths
	return nil
}

// Cleanup removes any routing state. On Windows, this is a no-op stub.
func (pr *ProcessRouter) Cleanup() error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	// WFP-based implementation would:
	// 1. Remove all filters in the Duman sublayer
	// 2. Remove the sublayer
	// 3. Call FwpmEngineClose0
	pr.pids = make(map[int]string)
	return nil
}

// AddProcess registers a process for routing consideration.
// On Windows, this tracks the PID and process name for lookup by other
// components (e.g., matching connections via GetExtendedTcpTable).
func (pr *ProcessRouter) AddProcess(pid int, name string) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	if pid <= 0 {
		return errors.New("invalid pid")
	}

	pr.pids[pid] = name
	return nil
}

// LookupProcess returns the routing action for a given PID.
// Returns ActionTunnel if the process should be tunneled, ActionDirect if not.
// Returns an error if the PID is not registered.
func (pr *ProcessRouter) LookupProcess(pid int) (RouteAction, error) {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	name, ok := pr.pids[pid]
	if !ok {
		return ActionTunnel, errors.New("pid not registered")
	}

	for _, rule := range pr.rules {
		if rule.ProcessName == name {
			return rule.Action, nil
		}
	}

	// Default: tunnel all unmatched processes.
	return ActionTunnel, nil
}
