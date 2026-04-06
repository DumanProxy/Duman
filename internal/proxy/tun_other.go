//go:build !linux && !darwin && !windows

package proxy

func openTUN(cfg TUNConfig) (TUNDevice, error) {
	return nil, ErrTUNNotSupported
}
