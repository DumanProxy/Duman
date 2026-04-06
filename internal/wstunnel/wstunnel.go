// Package wstunnel implements a WebSocket-based tunnel transport for Duman.
// It provides a WebSocket server and client that exchange binary frames
// containing encrypted tunnel chunks, using only the Go standard library.
// The WebSocket handshake and framing are implemented per RFC 6455.
package wstunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebSocket opcodes per RFC 6455 Section 5.2.
const (
	OpText   byte = 1
	OpBinary byte = 2
	OpClose  byte = 8
	OpPing   byte = 9
	OpPong   byte = 10
)

// wsGUID is the magic GUID used in the WebSocket handshake per RFC 6455.
const wsGUID = "258EAFA5-E914-47DA-95CA-5AB9B16F6357"

// Errors returned by frame operations.
var (
	ErrConnClosed     = errors.New("wstunnel: connection closed")
	ErrInvalidFrame   = errors.New("wstunnel: invalid frame")
	ErrPayloadTooLong = errors.New("wstunnel: payload too long")
)

// ---------- Frame helpers ----------

// writeFrame writes a single WebSocket frame to w.
// If masked is true, a random 4-byte mask is applied to the payload (required
// for client-to-server frames per RFC 6455 Section 5.3).
func writeFrame(w io.Writer, opcode byte, payload []byte, masked bool) error {
	// First byte: FIN bit (0x80) | opcode.
	header := []byte{0x80 | (opcode & 0x0F)}

	length := len(payload)
	var maskBit byte
	if masked {
		maskBit = 0x80
	}

	switch {
	case length <= 125:
		header = append(header, maskBit|byte(length))
	case length <= 65535:
		header = append(header, maskBit|126)
		header = append(header, byte(length>>8), byte(length))
	default:
		header = append(header, maskBit|127)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		header = append(header, b...)
	}

	if _, err := w.Write(header); err != nil {
		return err
	}

	if masked {
		maskKey := make([]byte, 4)
		if _, err := io.ReadFull(rand.Reader, maskKey); err != nil {
			return fmt.Errorf("wstunnel: generate mask key: %w", err)
		}
		if _, err := w.Write(maskKey); err != nil {
			return err
		}
		// Apply mask to payload bytes before writing.
		masked := make([]byte, len(payload))
		for i, b := range payload {
			masked[i] = b ^ maskKey[i%4]
		}
		_, err := w.Write(masked)
		return err
	}

	_, err := w.Write(payload)
	return err
}

// readFrame reads a single WebSocket frame from r.
// It returns the opcode, the unmasked payload, and any error.
func readFrame(r io.Reader) (opcode byte, payload []byte, err error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}

	opcode = header[0] & 0x0F
	isMasked := (header[1] & 0x80) != 0
	length := uint64(header[1] & 0x7F)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext)
	}

	// Safety limit: 16 MiB.
	if length > 16*1024*1024 {
		return 0, nil, ErrPayloadTooLong
	}

	var maskKey []byte
	if isMasked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(r, maskKey); err != nil {
			return 0, nil, err
		}
	}

	payload = make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}

	if isMasked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return opcode, payload, nil
}

// ---------- TunnelProcessor ----------

// TunnelProcessor is the interface used by WSServer to process incoming
// tunnel chunks and produce response data. This mirrors the pattern used
// in the rest of the Duman codebase.
type TunnelProcessor interface {
	ProcessChunk(data []byte) ([]byte, error)
}

// ---------- WSServer ----------

// WSServerConfig configures a WebSocket tunnel server.
type WSServerConfig struct {
	// Path is the HTTP path that accepts WebSocket upgrades (default "/ws").
	Path string
	// Processor handles incoming binary tunnel chunks.
	Processor TunnelProcessor
	// PingInterval controls the server-side keepalive ping frequency.
	// Zero means no server-initiated pings.
	PingInterval time.Duration
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// WSServer is a WebSocket tunnel server that upgrades HTTP connections
// and exchanges binary frames containing encrypted tunnel chunks.
type WSServer struct {
	config WSServerConfig
	logger *slog.Logger
}

// NewWSServer creates a new WebSocket tunnel server.
func NewWSServer(cfg WSServerConfig) *WSServer {
	if cfg.Path == "" {
		cfg.Path = "/ws"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WSServer{
		config: cfg,
		logger: logger,
	}
}

// ServeHTTP implements http.Handler. It performs the WebSocket upgrade
// handshake and then enters a frame read/write loop.
func (s *WSServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.config.Path {
		http.NotFound(w, r)
		return
	}

	// Validate upgrade headers.
	if !headerContains(r.Header, "Upgrade", "websocket") {
		http.Error(w, "expected WebSocket upgrade", http.StatusBadRequest)
		return
	}
	if !headerContains(r.Header, "Connection", "upgrade") {
		http.Error(w, "expected Connection: Upgrade", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "unsupported WebSocket version", http.StatusBadRequest)
		return
	}

	// Compute accept key.
	acceptKey := computeAcceptKey(key)

	// Hijack the connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support hijack", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write the HTTP 101 Switching Protocols response.
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
		"\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		s.logger.Error("write upgrade response", "err", err)
		return
	}
	if err := brw.Flush(); err != nil {
		s.logger.Error("flush upgrade response", "err", err)
		return
	}

	s.logger.Debug("websocket connection established", "remote", conn.RemoteAddr())

	// Start keepalive pinger if configured.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var writeMu sync.Mutex

	if s.config.PingInterval > 0 {
		go func() {
			ticker := time.NewTicker(s.config.PingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					writeMu.Lock()
					err := writeFrame(brw, OpPing, []byte("ping"), false)
					if err == nil {
						err = brw.Flush()
					}
					writeMu.Unlock()
					if err != nil {
						return
					}
				}
			}
		}()
	}

	// Frame processing loop.
	s.serveLoop(brw, &writeMu)
}

// serveLoop reads frames from the client and processes them.
func (s *WSServer) serveLoop(brw *bufio.ReadWriter, writeMu *sync.Mutex) {
	for {
		opcode, payload, err := readFrame(brw)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.logger.Debug("read frame error", "err", err)
			}
			return
		}

		switch opcode {
		case OpBinary, OpText:
			var resp []byte
			if s.config.Processor != nil {
				resp, err = s.config.Processor.ProcessChunk(payload)
				if err != nil {
					s.logger.Error("process chunk error", "err", err)
					// Send close frame with error.
					writeMu.Lock()
					_ = writeFrame(brw, OpClose, buildClosePayload(1011, err.Error()), false)
					_ = brw.Flush()
					writeMu.Unlock()
					return
				}
			}
			if resp != nil {
				writeMu.Lock()
				err = writeFrame(brw, OpBinary, resp, false)
				if err == nil {
					err = brw.Flush()
				}
				writeMu.Unlock()
				if err != nil {
					s.logger.Debug("write response error", "err", err)
					return
				}
			}

		case OpPing:
			writeMu.Lock()
			err = writeFrame(brw, OpPong, payload, false)
			if err == nil {
				err = brw.Flush()
			}
			writeMu.Unlock()
			if err != nil {
				return
			}

		case OpPong:
			// Keepalive acknowledged; nothing to do.

		case OpClose:
			// Echo the close frame back.
			writeMu.Lock()
			_ = writeFrame(brw, OpClose, payload, false)
			_ = brw.Flush()
			writeMu.Unlock()
			return

		default:
			s.logger.Debug("unknown opcode", "opcode", opcode)
		}
	}
}

// ---------- WSClient ----------

// WSClientConfig configures a WebSocket tunnel client.
type WSClientConfig struct {
	// ServerURL is the WebSocket server URL (e.g., "ws://localhost:8080/ws").
	ServerURL string
	// ReconnectDelay is the time to wait between reconnect attempts.
	// Zero means 1 second.
	ReconnectDelay time.Duration
	// MaxReconnects limits reconnection attempts. Zero means unlimited.
	MaxReconnects int
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// WSClient is a WebSocket tunnel client that connects to a WSServer,
// sends binary frames, and receives responses.
type WSClient struct {
	config WSClientConfig
	logger *slog.Logger

	mu       sync.Mutex
	conn     net.Conn
	brw      *bufio.ReadWriter
	closed   bool
	closedCh chan struct{}
}

// NewWSClient creates a new WebSocket tunnel client.
func NewWSClient(cfg WSClientConfig) *WSClient {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 1 * time.Second
	}
	return &WSClient{
		config:   cfg,
		logger:   logger,
		closedCh: make(chan struct{}),
	}
}

// Connect performs the WebSocket handshake with the server.
func (c *WSClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnClosed
	}

	return c.connectLocked(ctx)
}

// connectLocked performs the actual connection. Must be called with c.mu held.
func (c *WSClient) connectLocked(ctx context.Context) error {
	// Parse the server URL to extract host and path.
	host, path, useTLS, err := parseWSURL(c.config.ServerURL)
	if err != nil {
		return fmt.Errorf("wstunnel: parse url: %w", err)
	}
	_ = useTLS // TLS not implemented in stdlib-only version; plain TCP only.

	// Dial TCP.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("wstunnel: dial: %w", err)
	}

	brw := bufio.NewReadWriter(
		bufio.NewReaderSize(conn, 32*1024),
		bufio.NewWriterSize(conn, 32*1024),
	)

	// Generate a random WebSocket key.
	keyBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, keyBytes); err != nil {
		conn.Close()
		return fmt.Errorf("wstunnel: generate key: %w", err)
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)

	// Send HTTP upgrade request.
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := brw.WriteString(req); err != nil {
		conn.Close()
		return err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return err
	}

	// Read the response status line.
	statusLine, err := brw.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("wstunnel: read status: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return fmt.Errorf("wstunnel: unexpected status: %s", strings.TrimSpace(statusLine))
	}

	// Read headers until blank line.
	var acceptKey string
	for {
		line, err := brw.ReadString('\n')
		if err != nil {
			conn.Close()
			return fmt.Errorf("wstunnel: read header: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			acceptKey = strings.TrimSpace(line[len("sec-websocket-accept:"):])
		}
	}

	// Verify the accept key.
	expected := computeAcceptKey(wsKey)
	if acceptKey != expected {
		conn.Close()
		return fmt.Errorf("wstunnel: invalid accept key: got %q, want %q", acceptKey, expected)
	}

	// Close any previous connection.
	if c.conn != nil {
		c.conn.Close()
	}

	c.conn = conn
	c.brw = brw

	c.logger.Debug("websocket client connected", "server", c.config.ServerURL)
	return nil
}

// Send sends a binary frame containing data to the server.
// Client-to-server frames are masked per RFC 6455.
func (c *WSClient) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnClosed
	}
	if c.brw == nil {
		return ErrConnClosed
	}

	if err := writeFrame(c.brw, OpBinary, data, true); err != nil {
		return err
	}
	return c.brw.Flush()
}

// Recv reads the next binary or text frame from the server.
// It transparently handles ping frames by responding with pong.
func (c *WSClient) Recv() ([]byte, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrConnClosed
	}
	brw := c.brw
	c.mu.Unlock()

	if brw == nil {
		return nil, ErrConnClosed
	}

	for {
		opcode, payload, err := readFrame(brw)
		if err != nil {
			return nil, err
		}

		switch opcode {
		case OpBinary, OpText:
			return payload, nil

		case OpPing:
			c.mu.Lock()
			_ = writeFrame(c.brw, OpPong, payload, true)
			_ = c.brw.Flush()
			c.mu.Unlock()

		case OpPong:
			// Ignore pong.

		case OpClose:
			// Server initiated close.
			c.mu.Lock()
			_ = writeFrame(c.brw, OpClose, payload, true)
			_ = c.brw.Flush()
			c.mu.Unlock()
			return nil, ErrConnClosed

		default:
			// Unknown opcode; skip.
		}
	}
}

// Reconnect attempts to re-establish the WebSocket connection.
// It retries up to MaxReconnects times (0 = unlimited) with ReconnectDelay
// between attempts. Returns nil on success or the last error on failure.
func (c *WSClient) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrConnClosed
	}

	// Close existing connection if any.
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.brw = nil
	}
	c.mu.Unlock()

	maxAttempts := c.config.MaxReconnects
	if maxAttempts == 0 {
		maxAttempts = int(^uint(0) >> 1) // MaxInt
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.config.ReconnectDelay):
			}
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return ErrConnClosed
		}
		err := c.connectLocked(ctx)
		c.mu.Unlock()

		if err == nil {
			c.logger.Debug("reconnected", "attempt", attempt+1)
			return nil
		}
		lastErr = err
		c.logger.Debug("reconnect attempt failed", "attempt", attempt+1, "err", err)
	}

	return fmt.Errorf("wstunnel: reconnect failed after %d attempts: %w", maxAttempts, lastErr)
}

// Close closes the client connection and prevents further use.
func (c *WSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closedCh)

	if c.brw != nil {
		// Send close frame (best-effort).
		_ = writeFrame(c.brw, OpClose, buildClosePayload(1000, ""), true)
		_ = c.brw.Flush()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected returns true if the client has an active connection.
func (c *WSClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}

// ---------- Helpers ----------

// computeAcceptKey computes the Sec-WebSocket-Accept value per RFC 6455.
func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// headerContains checks if any value in the header field contains the
// target string (case-insensitive).
func headerContains(h http.Header, key, target string) bool {
	target = strings.ToLower(target)
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for _, part := range strings.Split(v, ",") {
			if strings.Contains(strings.ToLower(strings.TrimSpace(part)), target) {
				return true
			}
		}
	}
	return false
}

// buildClosePayload builds a close frame payload with status code and reason.
func buildClosePayload(code uint16, reason string) []byte {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	copy(payload[2:], reason)
	return payload
}

// parseWSURL extracts host, path, and TLS flag from a WebSocket URL.
func parseWSURL(rawURL string) (host, path string, useTLS bool, err error) {
	// Minimal URL parser for ws:// and wss:// URLs.
	u := rawURL
	switch {
	case strings.HasPrefix(u, "wss://"):
		useTLS = true
		u = u[len("wss://"):]
	case strings.HasPrefix(u, "ws://"):
		u = u[len("ws://"):]
	case strings.HasPrefix(u, "http://"):
		u = u[len("http://"):]
	case strings.HasPrefix(u, "https://"):
		useTLS = true
		u = u[len("https://"):]
	default:
		// Assume bare host:port/path.
	}

	idx := strings.Index(u, "/")
	if idx >= 0 {
		host = u[:idx]
		path = u[idx:]
	} else {
		host = u
		path = "/"
	}

	if host == "" {
		return "", "", false, fmt.Errorf("empty host")
	}

	// Add default port if missing.
	if _, _, err := net.SplitHostPort(host); err != nil {
		if useTLS {
			host = host + ":443"
		} else {
			host = host + ":80"
		}
	}

	return host, path, useTLS, nil
}
