package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// Forwarder relays encrypted tunnel chunks to a designated exit relay via TCP.
// Used when the relay role is "relay" (as opposed to "exit").
type Forwarder struct {
	targetAddr string
	conn       net.Conn
	mu         sync.Mutex
	logger     *slog.Logger
	healthy    bool
}

// NewForwarder creates a relay forwarder targeting the given address.
func NewForwarder(targetAddr string, logger *slog.Logger) *Forwarder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Forwarder{
		targetAddr: targetAddr,
		logger:     logger,
	}
}

// Connect establishes a TCP connection to the exit relay.
func (f *Forwarder) Connect(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", f.targetAddr)
	if err != nil {
		return fmt.Errorf("forwarder dial %s: %w", f.targetAddr, err)
	}

	f.conn = conn
	f.healthy = true
	f.logger.Info("forwarder connected", "target", f.targetAddr)
	return nil
}

// ProcessChunk implements fakedata.TunnelProcessor by forwarding the chunk
// to the exit relay as a length-prefixed binary frame.
// Frame format: [4-byte big-endian length][marshaled chunk]
func (f *Forwarder) ProcessChunk(ch *crypto.Chunk) error {
	data, err := ch.Marshal()
	if err != nil {
		return fmt.Errorf("marshal chunk: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.conn == nil {
		f.healthy = false
		return fmt.Errorf("forwarder not connected")
	}

	// Write length-prefixed frame
	frame := make([]byte, 4+len(data))
	frame[0] = byte(len(data) >> 24)
	frame[1] = byte(len(data) >> 16)
	frame[2] = byte(len(data) >> 8)
	frame[3] = byte(len(data))
	copy(frame[4:], data)

	if _, err := f.conn.Write(frame); err != nil {
		f.healthy = false
		f.logger.Warn("forwarder write failed", "err", err)
		return err
	}
	return nil
}

// IsHealthy returns whether the forwarder connection is alive.
func (f *Forwarder) IsHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy
}

// Close closes the forwarder connection.
func (f *Forwarder) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.healthy = false
	if f.conn != nil {
		return f.conn.Close()
	}
	return nil
}

// ForwardListener accepts forwarded chunks from other relays.
// Used on exit relays to receive chunks from relay-mode relays.
type ForwardListener struct {
	addr     string
	listener net.Listener
	handler  func(ch *crypto.Chunk) error
	logger   *slog.Logger
}

// NewForwardListener creates a listener for relay-to-relay forwarded chunks.
func NewForwardListener(addr string, handler func(ch *crypto.Chunk) error, logger *slog.Logger) *ForwardListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &ForwardListener{
		addr:    addr,
		handler: handler,
		logger:  logger,
	}
}

// ListenAndServe starts accepting forwarded connections.
func (fl *ForwardListener) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", fl.addr)
	if err != nil {
		return fmt.Errorf("forward listen: %w", err)
	}
	fl.listener = ln

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				fl.logger.Warn("forward accept error", "err", err)
				continue
			}
		}
		go fl.handleConn(conn)
	}
}

// handleConn reads length-prefixed chunk frames from a relay peer.
func (fl *ForwardListener) handleConn(conn net.Conn) {
	defer conn.Close()
	fl.logger.Info("relay peer connected", "remote", conn.RemoteAddr())

	header := make([]byte, 4)
	for {
		if _, err := readFull(conn, header); err != nil {
			return
		}
		length := int(header[0])<<24 | int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if length <= 0 || length > 1<<20 {
			fl.logger.Warn("invalid forward frame length", "length", length)
			return
		}

		data := make([]byte, length)
		if _, err := readFull(conn, data); err != nil {
			return
		}

		ch, err := crypto.UnmarshalChunk(data)
		if err != nil {
			fl.logger.Warn("unmarshal forwarded chunk", "err", err)
			continue
		}

		if err := fl.handler(ch); err != nil {
			fl.logger.Warn("process forwarded chunk", "err", err)
		}
	}
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		nn, err := conn.Read(buf[n:])
		n += nn
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
