package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
)

// IP protocol numbers.
const (
	ipProtoTCP = 6
	ipProtoUDP = 17
)

// IP version constants.
const (
	ipVersion4 = 4
	ipVersion6 = 6
)

// TUNEngineConfig holds configuration for the TUN packet processing engine.
type TUNEngineConfig struct {
	Device     TUNDevice
	Router     *Router
	Streams    StreamCreator
	DNS        *DNSInterceptor
	KillSwitch *KillSwitch
	Logger     *slog.Logger
}

// TUNEngine reads raw IP packets from a TUN device, applies routing rules,
// and forwards tunneled traffic through the steganographic tunnel.
type TUNEngine struct {
	device     TUNDevice
	router     *Router
	streams    StreamCreator
	dns        *DNSInterceptor
	killSwitch *KillSwitch
	logger     *slog.Logger

	// activeConns tracks active TCP connections for stream reuse.
	activeConns sync.Map // "srcIP:srcPort->dstIP:dstPort" -> io.ReadWriteCloser
}

// NewTUNEngine creates and validates a TUN engine.
func NewTUNEngine(cfg TUNEngineConfig) (*TUNEngine, error) {
	if cfg.Device == nil {
		return nil, fmt.Errorf("TUN device is required")
	}
	if cfg.Router == nil {
		return nil, fmt.Errorf("router is required")
	}
	if cfg.Streams == nil {
		return nil, fmt.Errorf("stream creator is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &TUNEngine{
		device:     cfg.Device,
		router:     cfg.Router,
		streams:    cfg.Streams,
		dns:        cfg.DNS,
		killSwitch: cfg.KillSwitch,
		logger:     logger,
	}, nil
}

// Run starts the main packet processing loop. It reads IP packets from the
// TUN device and processes them according to routing rules. Blocks until
// ctx is cancelled or a fatal error occurs.
func (e *TUNEngine) Run(ctx context.Context) error {
	mtu := e.device.MTU()
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	buf := make([]byte, mtu+4) // extra room for headers

	e.logger.Info("TUN engine started",
		"device", e.device.Name(),
		"mtu", mtu,
	)

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("TUN engine stopping")
			return ctx.Err()
		default:
		}

		n, err := e.device.Read(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				e.logger.Debug("TUN read error", "err", err)
				continue
			}
		}

		if n == 0 {
			continue
		}

		if err := e.processPacket(ctx, buf[:n]); err != nil {
			e.logger.Debug("packet processing error", "err", err)
		}
	}
}

// processPacket parses an IP packet header, extracts the destination,
// consults the router, and either tunnels, passes directly, or blocks.
func (e *TUNEngine) processPacket(ctx context.Context, pkt []byte) error {
	if len(pkt) == 0 {
		return nil
	}

	version := pkt[0] >> 4
	switch version {
	case ipVersion4:
		return e.processIPv4(ctx, pkt)
	case ipVersion6:
		return e.processIPv6(ctx, pkt)
	default:
		return fmt.Errorf("unsupported IP version: %d", version)
	}
}

// processIPv4 handles an IPv4 packet.
func (e *TUNEngine) processIPv4(ctx context.Context, pkt []byte) error {
	if len(pkt) < 20 {
		return fmt.Errorf("IPv4 packet too short: %d bytes", len(pkt))
	}

	// Parse IPv4 header
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || ihl > len(pkt) {
		return fmt.Errorf("invalid IPv4 IHL: %d", ihl)
	}

	protocol := pkt[9]
	dstIP := net.IP(pkt[16:20])

	// Extract port from transport header
	dstPort := 0
	if (protocol == ipProtoTCP || protocol == ipProtoUDP) && len(pkt) >= ihl+4 {
		dstPort = int(binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4]))
	}

	dest := net.JoinHostPort(dstIP.String(), strconv.Itoa(dstPort))

	// Check DNS interception (UDP port 53)
	if protocol == ipProtoUDP && dstPort == 53 && e.dns != nil {
		return e.handleDNS(pkt, ihl)
	}

	return e.routePacket(ctx, dest, pkt)
}

// processIPv6 handles an IPv6 packet.
func (e *TUNEngine) processIPv6(ctx context.Context, pkt []byte) error {
	if len(pkt) < 40 {
		return fmt.Errorf("IPv6 packet too short: %d bytes", len(pkt))
	}

	// Parse IPv6 header
	nextHeader := pkt[6]
	dstIP := net.IP(pkt[24:40])

	// Extract port from transport header (immediately after 40-byte IPv6 header)
	dstPort := 0
	if (nextHeader == ipProtoTCP || nextHeader == ipProtoUDP) && len(pkt) >= 44 {
		dstPort = int(binary.BigEndian.Uint16(pkt[42:44]))
	}

	dest := net.JoinHostPort(dstIP.String(), strconv.Itoa(dstPort))

	// Check DNS interception
	if nextHeader == ipProtoUDP && dstPort == 53 && e.dns != nil {
		return e.handleDNS(pkt, 40)
	}

	return e.routePacket(ctx, dest, pkt)
}

// routePacket applies routing rules and handles the packet accordingly.
func (e *TUNEngine) routePacket(ctx context.Context, dest string, pkt []byte) error {
	// Check kill switch first
	if e.killSwitch != nil && e.killSwitch.ShouldBlock(dest) {
		e.logger.Debug("kill switch blocked", "dest", dest)
		return nil // silently drop
	}

	action := e.router.Decide(dest)
	switch action {
	case ActionTunnel:
		return e.tunnelPacket(ctx, dest, pkt)
	case ActionDirect:
		// Write back to TUN for normal OS routing
		// (In a full implementation, this would inject into the host network stack)
		e.logger.Debug("direct passthrough", "dest", dest)
		return nil
	case ActionBlock:
		e.logger.Debug("blocked", "dest", dest)
		return nil
	default:
		return fmt.Errorf("unknown route action: %d", action)
	}
}

// tunnelPacket sends a packet through the steganographic tunnel.
func (e *TUNEngine) tunnelPacket(ctx context.Context, dest string, pkt []byte) error {
	e.logger.Debug("tunneling", "dest", dest, "size", len(pkt))

	// Create a stream for this destination
	stream, err := e.streams.CreateStream(ctx, dest)
	if err != nil {
		return fmt.Errorf("create stream for %s: %w", dest, err)
	}

	// Write the raw IP packet through the tunnel
	if _, err := stream.Write(pkt); err != nil {
		stream.Close()
		return fmt.Errorf("write to tunnel stream: %w", err)
	}

	// Read response in a goroutine and write back to TUN
	go func() {
		defer stream.Close()
		buf := make([]byte, e.device.MTU()+4)
		for {
			n, err := stream.Read(buf)
			if err != nil {
				if err != io.EOF {
					e.logger.Debug("tunnel read error", "dest", dest, "err", err)
				}
				return
			}
			if n > 0 {
				if _, err := e.device.Write(buf[:n]); err != nil {
					e.logger.Debug("TUN write error", "err", err)
					return
				}
			}
		}
	}()

	return nil
}

// handleDNS intercepts a DNS packet and resolves via the DNS interceptor.
func (e *TUNEngine) handleDNS(pkt []byte, transportOffset int) error {
	// UDP header is 8 bytes; DNS payload starts after
	udpOffset := transportOffset
	if len(pkt) < udpOffset+8 {
		return fmt.Errorf("UDP header too short")
	}

	dnsPayload := pkt[udpOffset+8:]
	resp, err := e.dns.HandleDNSPacket(dnsPayload)
	if err != nil {
		return fmt.Errorf("DNS intercept: %w", err)
	}

	if resp == nil {
		return nil
	}

	// Build a response IP/UDP packet and write it back to TUN.
	// For now, log the interception; full packet rewriting requires
	// swapping src/dst IPs and ports and recalculating checksums.
	e.logger.Debug("DNS intercepted", "response_size", len(resp))

	// TODO: Build full IP+UDP response packet with swapped addresses
	// and write to TUN device.

	return nil
}
