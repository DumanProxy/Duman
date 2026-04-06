//go:build windows

package proxy

import (
	"fmt"
	"net"
	"os/exec"
)

// windowsTUN is a stub implementation for Windows.
// Full implementation requires the Wintun driver (https://www.wintun.net/).
type windowsTUN struct {
	name string
	mtu  int
}

func openTUN(cfg TUNConfig) (TUNDevice, error) {
	// TODO: Implement Wintun adapter creation.
	// The Wintun driver must be installed separately. This stub validates
	// configuration and returns an error directing users to install it.
	//
	// Full implementation would:
	//   1. Load wintun.dll
	//   2. Call WintunCreateAdapter(name, tunnelType, guid)
	//   3. Call WintunStartSession(adapter, capacity)
	//   4. Use WintunAllocateSendPacket / WintunSendPacket for writes
	//   5. Use WintunReceivePacket / WintunReleaseReceivePacket for reads

	return nil, fmt.Errorf("TUN device requires the Wintun driver on Windows; "+
		"download from https://www.wintun.net/ and place wintun.dll in the application directory")
}

// configureAddress configures the IP address using netsh (for future use).
func configureWindowsAddress(name, cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}

	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("only IPv4 addresses are supported")
	}

	mask := net.IP(ipNet.Mask).String()

	// netsh interface ip set address name="duman0" static 10.0.0.1 255.255.255.0
	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		fmt.Sprintf("name=%q", name), "static", ip4.String(), mask)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh set address: %w: %s", err, out)
	}
	return nil
}

// configureWindowsMTU sets the MTU using netsh (for future use).
func configureWindowsMTU(name string, mtu int) error {
	cmd := exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		name, fmt.Sprintf("mtu=%d", mtu), "store=persistent")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh set mtu: %w: %s", err, out)
	}
	return nil
}
