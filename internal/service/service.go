// Package service provides Windows service lifecycle management for Duman.
//
// On Windows, Install and Uninstall interact with the Windows Service
// Control Manager (SCM) via golang.org/x/sys/windows/svc/mgr.
// RunAsService detects whether the process was launched by the SCM and,
// if so, runs the provided Service implementation under service control.
//
// On non-Windows platforms, Install and Uninstall return an error and
// RunAsService returns (false, nil), allowing the caller to fall back to
// interactive execution.
package service

// Service represents a system service lifecycle.
type Service interface {
	// Start begins the service's main work. It should return promptly;
	// long-running work must be spawned in a separate goroutine.
	Start() error

	// Stop signals the service to shut down gracefully.
	Stop() error

	// IsRunning reports whether the service is currently active.
	IsRunning() bool

	// Name returns the service name registered with the OS.
	Name() string
}

// Config holds service registration parameters.
type Config struct {
	// Name is the short service name used by the SCM (e.g. "DumanTunnel").
	Name string

	// DisplayName is the human-readable name shown in services.msc.
	DisplayName string

	// Description explains what the service does.
	Description string

	// ExecPath is the absolute path to the service executable.
	ExecPath string

	// Args are additional command-line arguments passed on service start.
	Args []string
}
