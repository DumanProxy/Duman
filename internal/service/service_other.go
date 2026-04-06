//go:build !windows

package service

import "errors"

// errNotSupported is returned by all service management functions on
// non-Windows platforms.
var errNotSupported = errors.New("service management not supported on this platform")

// Install is a no-op on non-Windows platforms.
func Install(cfg Config) error {
	return errNotSupported
}

// Uninstall is a no-op on non-Windows platforms.
func Uninstall(name string) error {
	return errNotSupported
}

// RunAsService always returns (false, nil) on non-Windows platforms,
// indicating the process should run interactively.
func RunAsService(svc Service) (bool, error) {
	return false, nil
}
