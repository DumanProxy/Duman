//go:build !linux && !darwin && !windows

package proxy

import "errors"

// ProcessRule defines a routing rule for a specific process.
type ProcessRule struct {
	ProcessName string
	Action      RouteAction
}

// ProcessRouter is not supported on this platform.
type ProcessRouter struct {
	rules []ProcessRule
}

// NewProcessRouter creates a new process router (unsupported on this platform).
func NewProcessRouter(rules []ProcessRule) *ProcessRouter {
	return &ProcessRouter{rules: rules}
}

// Setup returns an error indicating per-process routing is not supported.
func (pr *ProcessRouter) Setup() error {
	return errors.New("per-process routing not supported on this platform")
}

// Cleanup is a no-op on unsupported platforms.
func (pr *ProcessRouter) Cleanup() error {
	return nil
}

// AddProcess returns an error indicating per-process routing is not supported.
func (pr *ProcessRouter) AddProcess(pid int, name string) error {
	return errors.New("per-process routing not supported on this platform")
}
