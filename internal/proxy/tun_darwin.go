//go:build darwin

package proxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// macOS utun control socket constants
	utunControl = "com.apple.net.utun_control"
	utunOptIfname = 2
)

// darwinTUN implements TUNDevice for macOS using utun.
type darwinTUN struct {
	file *os.File
	name string
	mtu  int
}

func openTUN(cfg TUNConfig) (TUNDevice, error) {
	// Create a system socket for the utun control
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2) // SYSPROTO_CONTROL
	if err != nil {
		return nil, fmt.Errorf("socket AF_SYSTEM: %w", err)
	}

	// Get the control ID for utun
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControl)
	if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ioctl CTLIOCGINFO: %w", err)
	}

	// Parse requested utun unit number from config name
	unit := uint32(0) // 0 means auto-assign
	if strings.HasPrefix(cfg.Name, "utun") {
		if n, err := strconv.Atoi(cfg.Name[4:]); err == nil {
			unit = uint32(n) + 1 // utun numbering is unit-1
		}
	}

	// Connect to the utun control
	sa := &unix.SockaddrCtl{
		ID:   ctlInfo.Id,
		Unit: unit,
	}
	if err := unix.Connect(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connect utun: %w", err)
	}

	// Get the assigned interface name
	devName, err := unix.GetsockoptString(fd, 2, utunOptIfname) // SYSPROTO_CONTROL=2
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("getsockopt UTUN_OPT_IFNAME: %w", err)
	}
	devName = strings.TrimRight(devName, "\x00")

	// Set non-blocking is not needed for our use case; Go runtime handles it.

	file := os.NewFile(uintptr(fd), "/dev/"+devName)
	dev := &darwinTUN{
		file: file,
		name: devName,
		mtu:  cfg.MTU,
	}

	// Configure IP address and MTU via ifconfig
	if cfg.Address != "" {
		if err := dev.configureAddress(cfg.Address); err != nil {
			file.Close()
			return nil, fmt.Errorf("configure address: %w", err)
		}
	}

	if err := dev.configureMTU(cfg.MTU); err != nil {
		file.Close()
		return nil, fmt.Errorf("configure MTU: %w", err)
	}

	return dev, nil
}

// configureAddress sets the IP address using ifconfig.
func (d *darwinTUN) configureAddress(cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}

	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("only IPv4 addresses are supported")
	}

	// Calculate the destination address (first IP in the network for point-to-point)
	dest := make(net.IP, 4)
	copy(dest, ip4)

	// ifconfig utunX inet <addr> <dest> netmask <mask>
	mask := net.IP(ipNet.Mask).String()
	cmd := exec.Command("ifconfig", d.name, "inet", ip4.String(), ip4.String(), "netmask", mask)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig address: %w: %s", err, out)
	}

	// Add route for the subnet
	_, network, _ := net.ParseCIDR(cidr)
	if network != nil {
		cmd := exec.Command("route", "add", "-net", network.String(), "-interface", d.name)
		// Ignore route add errors (route may already exist)
		cmd.CombinedOutput()
	}

	return nil
}

// configureMTU sets the MTU using ifconfig.
func (d *darwinTUN) configureMTU(mtu int) error {
	cmd := exec.Command("ifconfig", d.name, "mtu", strconv.Itoa(mtu))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig mtu: %w: %s", err, out)
	}
	return nil
}

func (d *darwinTUN) Name() string                  { return d.name }
func (d *darwinTUN) MTU() int                      { return d.mtu }
func (d *darwinTUN) Read(buf []byte) (int, error)  { return d.file.Read(buf) }
func (d *darwinTUN) Write(buf []byte) (int, error) { return d.file.Write(buf) }
func (d *darwinTUN) Close() error                  { return d.file.Close() }
