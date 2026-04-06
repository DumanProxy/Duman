package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
)

const (
	// maxUDPPacketSize is the maximum size of a UDP datagram we handle.
	maxUDPPacketSize = 65535

	// udpSOCKS5HeaderMin is the minimum SOCKS5 UDP header size:
	// 2 (RSV) + 1 (FRAG) + 1 (ATYP) + 1 (min addr) + 2 (port) = 7
	// For IPv4: 2 + 1 + 1 + 4 + 2 = 10
	udpSOCKS5HeaderMinIPv4 = 10
)

// UDPRelay implements SOCKS5 UDP ASSOCIATE relay functionality.
// It listens on a local UDP port, parses SOCKS5 UDP datagrams from the client,
// tunnels the payload to the destination via StreamCreator, and relays responses back.
type UDPRelay struct {
	tcpConn net.Conn       // the SOCKS5 TCP association connection
	udpConn *net.UDPConn   // the local UDP relay socket
	streams StreamCreator  // creates tunnel streams for each destination
	logger  *slog.Logger

	// clientAddr is the address of the SOCKS5 client sending UDP datagrams.
	// Set on the first received datagram.
	clientAddr *net.UDPAddr
	mu         sync.Mutex
}

// NewUDPRelay creates a new UDP relay bound to an ephemeral local port.
// The tcpConn is the SOCKS5 TCP association connection; when it closes,
// the relay should be shut down.
func NewUDPRelay(tcpConn net.Conn, streams StreamCreator, logger *slog.Logger) (*UDPRelay, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Listen on an ephemeral UDP port on the loopback interface.
	udpAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	udpConn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("udp relay listen: %w", err)
	}

	return &UDPRelay{
		tcpConn: tcpConn,
		udpConn: udpConn,
		streams: streams,
		logger:  logger,
	}, nil
}

// Addr returns the local address of the UDP relay socket.
func (u *UDPRelay) Addr() net.Addr {
	if u.udpConn == nil {
		return nil
	}
	return u.udpConn.LocalAddr()
}

// Run starts the UDP relay loop. It blocks until the context is cancelled
// or the TCP association connection is closed.
func (u *UDPRelay) Run(ctx context.Context) {
	defer u.udpConn.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Monitor the TCP association connection. When it closes, cancel everything.
	go func() {
		buf := make([]byte, 1)
		for {
			_, err := u.tcpConn.Read(buf)
			if err != nil {
				cancel()
				return
			}
		}
	}()

	// Also close the UDP conn when context is done so reads unblock.
	go func() {
		<-ctx.Done()
		u.udpConn.Close()
	}()

	// Main receive loop: read UDP datagrams from the client, parse the SOCKS5
	// UDP header, tunnel the payload, and relay responses back.
	buf := make([]byte, maxUDPPacketSize)
	for {
		n, remoteAddr, err := u.udpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				u.logger.Debug("udp relay read error", "err", err)
				return
			}
		}

		u.mu.Lock()
		if u.clientAddr == nil {
			u.clientAddr = remoteAddr
		}
		u.mu.Unlock()

		data := make([]byte, n)
		copy(data, buf[:n])

		go u.handleDatagram(ctx, data, remoteAddr)
	}
}

// handleDatagram processes a single SOCKS5 UDP datagram from the client.
func (u *UDPRelay) handleDatagram(ctx context.Context, data []byte, clientAddr *net.UDPAddr) {
	dest, payload, err := parseUDPDatagram(data)
	if err != nil {
		u.logger.Debug("udp relay parse error", "err", err)
		return
	}

	u.logger.Debug("udp relay datagram", "dest", dest, "payload_len", len(payload))

	// Create a tunnel stream for this datagram's destination.
	stream, err := u.streams.CreateStream(ctx, dest)
	if err != nil {
		u.logger.Debug("udp relay stream creation failed", "err", err, "dest", dest)
		return
	}
	defer stream.Close()

	// Write the payload to the tunnel stream.
	if _, err := stream.Write(payload); err != nil {
		u.logger.Debug("udp relay stream write failed", "err", err)
		return
	}

	// Read the response from the tunnel stream.
	respBuf := make([]byte, maxUDPPacketSize)
	n, err := stream.Read(respBuf)
	if err != nil && err != io.EOF {
		u.logger.Debug("udp relay stream read failed", "err", err)
		return
	}
	if n == 0 {
		return
	}

	// Build a SOCKS5 UDP datagram with the response and send it back to the client.
	respDatagram := buildUDPDatagram(dest, respBuf[:n])

	u.mu.Lock()
	addr := u.clientAddr
	u.mu.Unlock()

	if addr == nil {
		addr = clientAddr
	}

	if _, err := u.udpConn.WriteToUDP(respDatagram, addr); err != nil {
		u.logger.Debug("udp relay write to client failed", "err", err)
	}
}

// parseUDPDatagram parses a SOCKS5 UDP request datagram.
// Format: [2 RSV][1 FRAG][1 ATYP][variable ADDR][2 PORT][payload]
// Returns the destination address as "host:port" and the payload.
func parseUDPDatagram(data []byte) (addr string, payload []byte, err error) {
	if len(data) < 4 {
		return "", nil, errors.New("udp datagram too short")
	}

	// RSV (2 bytes, must be 0x0000) - we ignore these per spec
	// FRAG (1 byte) - fragment number
	frag := data[2]
	if frag != 0 {
		return "", nil, fmt.Errorf("udp fragmentation not supported (frag=%d)", frag)
	}

	atyp := data[3]
	offset := 4

	var host string
	switch atyp {
	case addrIPv4:
		if len(data) < offset+4+2 {
			return "", nil, errors.New("udp datagram too short for IPv4 address")
		}
		host = net.IP(data[offset : offset+4]).String()
		offset += 4

	case addrDomain:
		if len(data) < offset+1 {
			return "", nil, errors.New("udp datagram too short for domain length")
		}
		domainLen := int(data[offset])
		offset++
		if len(data) < offset+domainLen+2 {
			return "", nil, errors.New("udp datagram too short for domain")
		}
		host = string(data[offset : offset+domainLen])
		offset += domainLen

	case addrIPv6:
		if len(data) < offset+16+2 {
			return "", nil, errors.New("udp datagram too short for IPv6 address")
		}
		host = net.IP(data[offset : offset+16]).String()
		offset += 16

	default:
		return "", nil, fmt.Errorf("unsupported address type in udp datagram: %d", atyp)
	}

	if len(data) < offset+2 {
		return "", nil, errors.New("udp datagram too short for port")
	}
	port := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2

	addr = net.JoinHostPort(host, strconv.Itoa(int(port)))
	payload = data[offset:]
	return addr, payload, nil
}

// buildUDPDatagram builds a SOCKS5 UDP response datagram.
// Format: [2 RSV][1 FRAG][1 ATYP][variable ADDR][2 PORT][payload]
func buildUDPDatagram(addr string, payload []byte) []byte {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Fallback: treat entire addr as host with port 0
		host = addr
		portStr = "0"
	}
	port, _ := strconv.Atoi(portStr)

	var buf []byte

	// RSV + FRAG
	buf = append(buf, 0x00, 0x00, 0x00)

	ip := net.ParseIP(host)
	if ip == nil {
		// Domain name
		buf = append(buf, addrDomain)
		buf = append(buf, byte(len(host)))
		buf = append(buf, []byte(host)...)
	} else if ip4 := ip.To4(); ip4 != nil {
		// IPv4
		buf = append(buf, addrIPv4)
		buf = append(buf, ip4...)
	} else {
		// IPv6
		buf = append(buf, addrIPv6)
		buf = append(buf, ip.To16()...)
	}

	// Port (2 bytes big-endian)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	buf = append(buf, portBytes...)

	// Payload
	buf = append(buf, payload...)

	return buf
}
