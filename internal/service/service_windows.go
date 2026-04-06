//go:build windows

package service

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Install registers the service with the Windows Service Control Manager.
// The service is created with automatic start type so it launches at boot.
func Install(cfg Config) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	// Check whether the service already exists.
	s, err := m.OpenService(cfg.Name)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", cfg.Name)
	}

	s, err = m.CreateService(cfg.Name, cfg.ExecPath, mgr.Config{
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		StartType:   mgr.StartAutomatic,
	}, cfg.Args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	return nil
}

// Uninstall removes the named service from the Windows Service Control Manager.
func Uninstall(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service %q: %w", name, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", name, err)
	}

	return nil
}

// RunAsService detects whether the current process was started by the
// Windows Service Control Manager. If so, it runs the provided Service
// under SCM control and returns (true, err). If the process is running
// interactively, it returns (false, nil) so the caller can proceed with
// normal execution.
func RunAsService(s Service) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("detect service environment: %w", err)
	}
	if !isService {
		return false, nil
	}

	h := &windowsHandler{svc: s}
	err = svc.Run(s.Name(), h)
	if err != nil {
		return true, fmt.Errorf("run service: %w", err)
	}
	return true, nil
}

// windowsHandler adapts a Service to the svc.Handler interface required
// by the Windows service runtime.
type windowsHandler struct {
	svc Service
}

// Execute is called by the Windows service runtime. It must report
// status transitions and respond to control signals.
func (h *windowsHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	// Accepted control commands.
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	// Tell the SCM we are starting.
	s <- svc.Status{State: svc.StartPending}

	if err := h.svc.Start(); err != nil {
		// Return a non-zero exit code to signal failure.
		return true, 1
	}

	// Service is now running.
	s <- svc.Status{State: svc.Running, Accepts: accepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			// Reply with current status.
			s <- c.CurrentStatus
			// Double-tap per Microsoft recommendation for Interrogate.
			time.Sleep(100 * time.Millisecond)
			s <- c.CurrentStatus

		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			_ = h.svc.Stop()
			return false, 0

		default:
			// Ignore unrecognised commands.
		}
	}

	return false, 0
}
