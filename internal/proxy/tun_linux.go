//go:build linux

package proxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// nativeEndian is the host byte order, used for netlink messages.
var nativeEndian binary.ByteOrder

func init() {
	buf := [2]byte{}
	*(*uint16)(unsafe.Pointer(&buf[0])) = 0x0102
	if buf[0] == 0x01 {
		nativeEndian = binary.BigEndian
	} else {
		nativeEndian = binary.LittleEndian
	}
}

const (
	tunDevice  = "/dev/net/tun"
	ifnamsiz   = 16
	iffTUN     = 0x0001
	iffNoPi    = 0x1000
	tunSetIff  = 0x400454ca // TUNSETIFF ioctl number
)

// ifreqFlags is the ifreq struct used for TUNSETIFF.
type ifreqFlags struct {
	Name  [ifnamsiz]byte
	Flags uint16
	_     [22]byte // padding to match C struct size
}

// linuxTUN implements TUNDevice for Linux using /dev/net/tun.
type linuxTUN struct {
	file *os.File
	name string
	mtu  int
}

func openTUN(cfg TUNConfig) (TUNDevice, error) {
	// Open the TUN clone device
	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tunDevice, err)
	}

	// Prepare ioctl request
	var ifr ifreqFlags
	copy(ifr.Name[:], cfg.Name)
	ifr.Flags = iffTUN | iffNoPi

	// Set the TUN interface
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(tunSetIff), uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Extract the assigned device name (may differ from requested)
	devName := string(ifr.Name[:])
	for i, b := range ifr.Name {
		if b == 0 {
			devName = string(ifr.Name[:i])
			break
		}
	}

	file := os.NewFile(uintptr(fd), tunDevice)
	dev := &linuxTUN{
		file: file,
		name: devName,
		mtu:  cfg.MTU,
	}

	// Configure the interface IP address and bring it up
	if cfg.Address != "" {
		if err := dev.configureAddress(cfg.Address); err != nil {
			file.Close()
			return nil, fmt.Errorf("configure address: %w", err)
		}
	}

	// Set MTU via netlink
	if err := dev.configureMTU(cfg.MTU); err != nil {
		file.Close()
		return nil, fmt.Errorf("configure MTU: %w", err)
	}

	return dev, nil
}

// configureAddress sets the IP address on the TUN interface using netlink.
func (d *linuxTUN) configureAddress(cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}

	// Get interface index
	iface, err := net.InterfaceByName(d.name)
	if err != nil {
		return fmt.Errorf("interface %q: %w", d.name, err)
	}

	// Create netlink socket
	sock, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("netlink socket: %w", err)
	}
	defer unix.Close(sock)

	// Build RTM_NEWADDR message
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("only IPv4 addresses are supported")
	}

	prefixLen, _ := ipNet.Mask.Size()

	// Netlink message header (16 bytes) + ifaddrmsg (8 bytes) + RTA_LOCAL attr + RTA_ADDRESS attr
	nlmsgLen := 16 + 8 + 8 + 8 // simplified: header + ifaddr + 2 attrs with 4-byte IPv4
	msg := make([]byte, nlmsgLen)

	// nlmsghdr
	nativeEndian.PutUint32(msg[0:4], uint32(nlmsgLen))  // nlmsg_len
	nativeEndian.PutUint16(msg[4:6], unix.RTM_NEWADDR)   // nlmsg_type
	nativeEndian.PutUint16(msg[6:8], unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_EXCL) // nlmsg_flags
	nativeEndian.PutUint32(msg[8:12], 1)                  // nlmsg_seq
	nativeEndian.PutUint32(msg[12:16], 0)                 // nlmsg_pid

	// ifaddrmsg
	msg[16] = unix.AF_INET              // ifa_family
	msg[17] = byte(prefixLen)           // ifa_prefixlen
	msg[18] = 0                         // ifa_flags
	msg[19] = unix.RT_SCOPE_UNIVERSE    // ifa_scope
	nativeEndian.PutUint32(msg[20:24], uint32(iface.Index)) // ifa_index

	// IFA_LOCAL (type=2) — local address attribute for ifaddrmsg
	const ifaLocal = 2
	nativeEndian.PutUint16(msg[24:26], 8) // rta_len
	nativeEndian.PutUint16(msg[26:28], ifaLocal)
	copy(msg[28:32], ip4)

	// IFA_ADDRESS (type=1) — interface address attribute for ifaddrmsg
	const ifaAddress = 1
	nativeEndian.PutUint16(msg[32:34], 8) // rta_len
	nativeEndian.PutUint16(msg[34:36], ifaAddress)
	copy(msg[36:40], ip4)

	sa := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Sendto(sock, msg, 0, sa); err != nil {
		return fmt.Errorf("netlink send: %w", err)
	}

	// Set interface UP
	return d.setInterfaceUp()
}

// configureMTU sets the MTU on the interface using an ioctl.
func (d *linuxTUN) configureMTU(mtu int) error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer unix.Close(sock)

	// ifreq for SIOCSIFMTU: name[16] + mtu as int32
	var ifr [40]byte
	copy(ifr[:], d.name)
	nativeEndian.PutUint32(ifr[16:20], uint32(mtu))

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), unix.SIOCSIFMTU, uintptr(unsafe.Pointer(&ifr[0])))
	if errno != 0 {
		return fmt.Errorf("ioctl SIOCSIFMTU: %w", errno)
	}
	return nil
}

// setInterfaceUp brings the TUN interface up using ioctl.
func (d *linuxTUN) setInterfaceUp() error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer unix.Close(sock)

	// Get current flags
	var ifr [40]byte
	copy(ifr[:], d.name)

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), unix.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr[0])))
	if errno != 0 {
		return fmt.Errorf("ioctl SIOCGIFFLAGS: %w", errno)
	}

	// Set IFF_UP
	flags := nativeEndian.Uint16(ifr[16:18])
	flags |= unix.IFF_UP | unix.IFF_RUNNING
	nativeEndian.PutUint16(ifr[16:18], flags)

	_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(sock), unix.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr[0])))
	if errno != 0 {
		return fmt.Errorf("ioctl SIOCSIFFLAGS: %w", errno)
	}
	return nil
}

func (d *linuxTUN) Name() string       { return d.name }
func (d *linuxTUN) MTU() int           { return d.mtu }
func (d *linuxTUN) Read(buf []byte) (int, error)  { return d.file.Read(buf) }
func (d *linuxTUN) Write(buf []byte) (int, error) { return d.file.Write(buf) }
func (d *linuxTUN) Close() error       { return d.file.Close() }
