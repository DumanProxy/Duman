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
	socks5Version  = 0x05
	authNone       = 0x00
	cmdConnect     = 0x01
	cmdUDPAssociate = 0x03
	addrIPv4       = 0x01
	addrDomain     = 0x03
	addrIPv6       = 0x04
)

// StreamCreator creates a tunnel stream for a destination.
type StreamCreator interface {
	CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error)
}

// SOCKS5Server implements a SOCKS5 proxy that tunnels through Duman.
type SOCKS5Server struct {
	listenAddr string
	streams    StreamCreator
	listener   net.Listener
	logger     *slog.Logger
	wg         sync.WaitGroup
}

// NewSOCKS5Server creates a new SOCKS5 proxy server.
func NewSOCKS5Server(addr string, streams StreamCreator, logger *slog.Logger) *SOCKS5Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &SOCKS5Server{
		listenAddr: addr,
		streams:    streams,
		logger:     logger,
	}
}

// ListenAndServe starts the SOCKS5 proxy.
func (s *SOCKS5Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("socks5 listen: %w", err)
	}
	s.listener = ln
	s.logger.Info("SOCKS5 proxy listening", "addr", s.listenAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
				s.logger.Debug("socks5 accept error", "err", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// Addr returns the listener address.
func (s *SOCKS5Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *SOCKS5Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// 1. Version/method negotiation
	if err := s.negotiateAuth(conn); err != nil {
		s.logger.Debug("socks5 auth negotiate failed", "err", err)
		return
	}

	// 2. Read request (CONNECT or UDP ASSOCIATE)
	cmd, dest, err := s.readRequest(conn)
	if err != nil {
		s.logger.Debug("socks5 request failed", "err", err)
		return
	}

	switch cmd {
	case cmdConnect:
		s.handleConnect(ctx, conn, dest)
	case cmdUDPAssociate:
		s.handleUDPAssociate(ctx, conn)
	}
}

func (s *SOCKS5Server) handleConnect(ctx context.Context, conn net.Conn, dest string) {
	s.logger.Debug("socks5 connect", "dest", dest)

	// Create tunnel stream
	stream, err := s.streams.CreateStream(ctx, dest)
	if err != nil {
		s.sendReply(conn, 0x05) // connection refused
		s.logger.Debug("socks5 stream creation failed", "err", err, "dest", dest)
		return
	}
	defer stream.Close()

	// Send success reply
	s.sendReply(conn, 0x00) // success

	// Bidirectional proxy
	s.relay(conn, stream)
}

func (s *SOCKS5Server) handleUDPAssociate(ctx context.Context, conn net.Conn) {
	s.logger.Debug("socks5 udp associate")

	relay, err := NewUDPRelay(conn, s.streams, s.logger)
	if err != nil {
		s.sendReply(conn, 0x01) // general failure
		s.logger.Debug("socks5 udp relay creation failed", "err", err)
		return
	}

	// Send success reply with the UDP relay bind address
	s.sendUDPReply(conn, 0x00, relay.Addr())

	// Run the relay until the TCP association closes or context is cancelled
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()

	relay.Run(relayCtx)
}

func (s *SOCKS5Server) negotiateAuth(conn net.Conn) error {
	// Read version + number of methods
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != socks5Version {
		return errors.New("unsupported SOCKS version")
	}

	numMethods := int(buf[1])
	methods := make([]byte, numMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}

	// We only support no-auth
	hasNoAuth := false
	for _, m := range methods {
		if m == authNone {
			hasNoAuth = true
			break
		}
	}
	if !hasNoAuth {
		conn.Write([]byte{socks5Version, 0xFF}) // no acceptable method
		return errors.New("no acceptable auth method")
	}

	// Send method selection (no auth)
	_, err := conn.Write([]byte{socks5Version, authNone})
	return err
}

func (s *SOCKS5Server) readRequest(conn net.Conn) (byte, string, error) {
	// VER CMD RSV ATYP
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return 0, "", err
	}
	if buf[0] != socks5Version {
		return 0, "", errors.New("bad version")
	}
	cmd := buf[1]
	if cmd != cmdConnect && cmd != cmdUDPAssociate {
		s.sendReply(conn, 0x07) // command not supported
		return 0, "", fmt.Errorf("unsupported command: %d", cmd)
	}

	var host string
	switch buf[3] {
	case addrIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return 0, "", err
		}
		host = net.IP(addr).String()

	case addrDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return 0, "", err
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return 0, "", err
		}
		host = string(domain)

	case addrIPv6:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return 0, "", err
		}
		host = net.IP(addr).String()

	default:
		return 0, "", fmt.Errorf("unsupported address type: %d", buf[3])
	}

	// Read port (2 bytes big-endian)
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return 0, "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	return cmd, net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func (s *SOCKS5Server) sendReply(conn net.Conn, status byte) {
	// VER REP RSV ATYP ADDR PORT
	reply := []byte{
		socks5Version, status, 0x00, addrIPv4,
		0, 0, 0, 0, // bind addr
		0, 0, // bind port
	}
	conn.Write(reply)
}

func (s *SOCKS5Server) sendUDPReply(conn net.Conn, status byte, bindAddr net.Addr) {
	reply := []byte{
		socks5Version, status, 0x00, addrIPv4,
		0, 0, 0, 0, // bind addr (filled below)
		0, 0, // bind port (filled below)
	}

	if status == 0x00 && bindAddr != nil {
		if udpAddr, ok := bindAddr.(*net.UDPAddr); ok {
			ip4 := udpAddr.IP.To4()
			if ip4 != nil {
				copy(reply[4:8], ip4)
			}
			binary.BigEndian.PutUint16(reply[8:10], uint16(udpAddr.Port))
		}
	}

	conn.Write(reply)
}

func (s *SOCKS5Server) relay(client net.Conn, stream io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)

	// client → stream
	go func() {
		defer wg.Done()
		io.Copy(stream, client)
	}()

	// stream → client
	go func() {
		defer wg.Done()
		io.Copy(client, stream)
	}()

	wg.Wait()
}
