package proxy

import "errors"

// TUNDevice is the cross-platform interface for a TUN network device.
// It provides raw IP packet read/write access for transparent tunneling.
type TUNDevice interface {
	// Name returns the OS-assigned device name (e.g. "tun0", "utun3").
	Name() string

	// Read reads a single IP packet from the TUN device into buf.
	// Returns the number of bytes read.
	Read(buf []byte) (int, error)

	// Write writes a single IP packet to the TUN device.
	// Returns the number of bytes written.
	Write(buf []byte) (int, error)

	// Close shuts down the TUN device and releases resources.
	Close() error

	// MTU returns the maximum transmission unit configured for this device.
	MTU() int
}

// TUNConfig holds configuration for opening a TUN device.
type TUNConfig struct {
	Name    string // device name hint (OS may assign a different name)
	Address string // CIDR address to assign (e.g. "10.0.0.1/24")
	MTU     int    // MTU size; 0 defaults to 1500
	DNS     string // optional DNS override address
}

// DefaultMTU is the default MTU used when TUNConfig.MTU is zero.
const DefaultMTU = 1500

// ErrTUNNotSupported is returned on platforms without TUN support.
var ErrTUNNotSupported = errors.New("TUN device not supported on this platform")

// normalizeConfig fills in defaults for a TUNConfig.
func normalizeConfig(cfg *TUNConfig) {
	if cfg.MTU <= 0 {
		cfg.MTU = DefaultMTU
	}
	if cfg.Name == "" {
		cfg.Name = "duman0"
	}
}

// OpenTUN opens and configures a TUN device. Platform-specific implementations
// are in tun_linux.go, tun_darwin.go, tun_windows.go, and tun_other.go.
// This function dispatches to the platform-specific openTUN implementation.
func OpenTUN(cfg TUNConfig) (TUNDevice, error) {
	normalizeConfig(&cfg)
	return openTUN(cfg)
}
