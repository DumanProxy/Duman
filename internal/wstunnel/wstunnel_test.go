package wstunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- Mock TunnelProcessor ----------

// mockProcessor echoes back the received data prefixed with "echo:".
type mockProcessor struct {
	mu     sync.Mutex
	chunks [][]byte
	err    error
}

func (m *mockProcessor) ProcessChunk(data []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, append([]byte(nil), data...))
	if m.err != nil {
		return nil, m.err
	}
	resp := make([]byte, 0, len("echo:")+len(data))
	resp = append(resp, "echo:"...)
	resp = append(resp, data...)
	return resp, nil
}

func (m *mockProcessor) getChunks() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.chunks))
	copy(out, m.chunks)
	return out
}

// ---------- TestWSFrameReadWrite ----------

func TestWSFrameReadWrite(t *testing.T) {
	tests := []struct {
		name    string
		opcode  byte
		payload []byte
		masked  bool
	}{
		{"empty binary", OpBinary, nil, false},
		{"small text", OpText, []byte("hello world"), false},
		{"binary 125 bytes", OpBinary, makeBytes(125), false},
		{"binary 126 bytes (extended)", OpBinary, makeBytes(126), false},
		{"binary 256 bytes", OpBinary, makeBytes(256), false},
		{"binary 65536 bytes (64-bit len)", OpBinary, makeBytes(65536), false},
		{"masked small", OpBinary, []byte("masked payload"), true},
		{"masked 256 bytes", OpBinary, makeBytes(256), true},
		{"ping", OpPing, []byte("keepalive"), false},
		{"pong", OpPong, []byte("keepalive"), false},
		{"close", OpClose, buildClosePayload(1000, "bye"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, tt.opcode, tt.payload, tt.masked); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}

			opcode, payload, err := readFrame(&buf)
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}

			if opcode != tt.opcode {
				t.Errorf("opcode: got %d, want %d", opcode, tt.opcode)
			}

			wantPayload := tt.payload
			if wantPayload == nil {
				wantPayload = []byte{}
			}
			if !bytes.Equal(payload, wantPayload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(payload), len(wantPayload))
			}
		})
	}
}

// ---------- TestWSMasking ----------

func TestWSMasking(t *testing.T) {
	// Write a masked frame, then read it back and verify the payload is
	// correctly unmasked. Also verify the raw bytes have the mask bit set.
	original := []byte("sensitive tunnel data")

	var buf bytes.Buffer
	if err := writeFrame(&buf, OpBinary, original, true); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	raw := buf.Bytes()

	// Second byte should have mask bit set.
	if raw[1]&0x80 == 0 {
		t.Fatal("mask bit not set in masked frame")
	}

	// The payload in the raw bytes should NOT match the original (unless
	// the mask key happens to be all zeros, which is astronomically unlikely).
	// We check after the header + mask key.
	headerLen := 2 // basic header
	payloadLen := raw[1] & 0x7F
	if payloadLen == 126 {
		headerLen += 2
	} else if payloadLen == 127 {
		headerLen += 8
	}
	maskKeyStart := headerLen
	payloadStart := maskKeyStart + 4
	rawPayload := raw[payloadStart:]

	// With overwhelming probability, masked payload differs from original.
	if bytes.Equal(rawPayload, original) {
		t.Error("masked payload should differ from original (extremely unlikely to be equal)")
	}

	// Reading the frame should unmask correctly.
	opcode, payload, err := readFrame(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpBinary {
		t.Errorf("opcode: got %d, want %d", opcode, OpBinary)
	}
	if !bytes.Equal(payload, original) {
		t.Error("unmasked payload does not match original")
	}
}

// ---------- TestWSHandshake ----------

func TestWSHandshake(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Replace http:// with ws:// for the client URL.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	client := NewWSClient(WSClientConfig{
		ServerURL: wsURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send a message.
	msg := []byte("hello from client")
	if err := client.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Receive response.
	resp, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}

	expected := append([]byte("echo:"), msg...)
	if !bytes.Equal(resp, expected) {
		t.Errorf("response: got %q, want %q", resp, expected)
	}

	// Verify the processor received the chunk.
	chunks := proc.getChunks()
	if len(chunks) != 1 {
		t.Fatalf("processor received %d chunks, want 1", len(chunks))
	}
	if !bytes.Equal(chunks[0], msg) {
		t.Errorf("processor chunk: got %q, want %q", chunks[0], msg)
	}
}

// ---------- TestWSPingPong ----------

func TestWSPingPong(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:         "/ws",
		Processor:    proc,
		PingInterval: 50 * time.Millisecond,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Connect a raw TCP client so we can observe ping frames directly.
	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Perform the handshake manually.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := encodeBase64(keyBytes)

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	// Read the 101 response (consume all headers).
	reader := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(reader)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if !strings.Contains(string(reader[:n]), "101") {
		t.Fatalf("expected 101, got: %s", string(reader[:n]))
	}

	// Wait for a ping frame from the server.
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read ping: %v", err)
	}
	if opcode != OpPing {
		t.Fatalf("expected ping (9), got opcode %d", opcode)
	}

	// Respond with pong (masked, as we're the client).
	if err := writeFrame(conn, OpPong, payload, true); err != nil {
		t.Fatalf("write pong: %v", err)
	}

	// Wait for another ping to confirm keepalive is repeating.
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	opcode2, _, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read second ping: %v", err)
	}
	if opcode2 != OpPing {
		t.Fatalf("expected second ping (9), got opcode %d", opcode2)
	}
}

// ---------- TestWSServer_ProcessChunks ----------

func TestWSServer_ProcessChunks(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	client := NewWSClient(WSClientConfig{
		ServerURL: wsURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send multiple chunks.
	messages := [][]byte{
		[]byte("chunk-0001"),
		makeBytes(500),
		makeBytes(1024),
		[]byte("final-chunk"),
	}

	for i, msg := range messages {
		if err := client.Send(msg); err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
		resp, err := client.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		expected := append([]byte("echo:"), msg...)
		if !bytes.Equal(resp, expected) {
			t.Errorf("response[%d]: got %d bytes, want %d bytes", i, len(resp), len(expected))
		}
	}

	// Verify all chunks were processed.
	chunks := proc.getChunks()
	if len(chunks) != len(messages) {
		t.Fatalf("processor received %d chunks, want %d", len(chunks), len(messages))
	}
	for i, msg := range messages {
		if !bytes.Equal(chunks[i], msg) {
			t.Errorf("chunk[%d] mismatch", i)
		}
	}
}

// ---------- TestWSClient_Reconnect ----------

func TestWSClient_Reconnect(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// Start first server.
	ts1 := httptest.NewServer(srv)
	wsURL := "ws" + strings.TrimPrefix(ts1.URL, "http") + "/ws"

	client := NewWSClient(WSClientConfig{
		ServerURL:      wsURL,
		ReconnectDelay: 50 * time.Millisecond,
		MaxReconnects:  10,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect to first server.
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Exchange a message to verify connection works.
	if err := client.Send([]byte("pre-disconnect")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := client.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}

	// Get the address from ts1 and close it.
	addr := ts1.Listener.Addr().String()
	ts1.Close()

	// Start a new server on the same address.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("re-listen on %s: %v", addr, err)
	}
	ts2 := httptest.NewUnstartedServer(srv)
	ts2.Listener = ln
	ts2.Start()
	defer ts2.Close()

	// Reconnect.
	if err := client.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	// Verify the reconnected session works.
	msg := []byte("post-reconnect")
	if err := client.Send(msg); err != nil {
		t.Fatalf("Send after reconnect: %v", err)
	}
	resp, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv after reconnect: %v", err)
	}
	expected := append([]byte("echo:"), msg...)
	if !bytes.Equal(resp, expected) {
		t.Errorf("response after reconnect: got %q, want %q", resp, expected)
	}
}

// ---------- TestWSServer_NonWSPath ----------

func TestWSServer_NonWSPath(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Path:   "/ws",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Request a non-WebSocket path should return 404.
	resp, err := http.Get(ts.URL + "/other")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	// Request the WS path without upgrade headers should return 400.
	resp, err = http.Get(ts.URL + "/ws")
	if err != nil {
		t.Fatalf("GET /ws: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status without upgrade: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// ---------- TestWSCloseHandshake ----------

func TestWSCloseHandshake(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	client := NewWSClient(WSClientConfig{
		ServerURL: wsURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close should send a close frame and not error.
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Subsequent operations should fail.
	if err := client.Send([]byte("after close")); err != ErrConnClosed {
		t.Errorf("Send after close: got %v, want ErrConnClosed", err)
	}
}

// ---------- TestWSProcessorError ----------

func TestWSProcessorError(t *testing.T) {
	proc := &mockProcessor{err: fmt.Errorf("processing failed")}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	client := NewWSClient(WSClientConfig{
		ServerURL: wsURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send a chunk that will cause a processor error.
	if err := client.Send([]byte("will-fail")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The server should close the connection after the processor error.
	// Recv should return a close frame or an error.
	_, err := client.Recv()
	if err == nil {
		t.Error("expected error from Recv after processor error, got nil")
	}
}

// ---------- TestParseWSURL ----------

func TestParseWSURL(t *testing.T) {
	tests := []struct {
		url    string
		host   string
		path   string
		tls    bool
		hasErr bool
	}{
		{"ws://localhost:8080/ws", "localhost:8080", "/ws", false, false},
		{"wss://example.com/tunnel", "example.com:443", "/tunnel", true, false},
		{"ws://127.0.0.1:9090/path/to/ws", "127.0.0.1:9090", "/path/to/ws", false, false},
		{"http://host:80/ws", "host:80", "/ws", false, false},
		{"ws://host", "host:80", "/", false, false},
		{"wss://host", "host:443", "/", true, false},
		{"", "", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			host, path, tls, err := parseWSURL(tt.url)
			if tt.hasErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tt.host {
				t.Errorf("host: got %q, want %q", host, tt.host)
			}
			if path != tt.path {
				t.Errorf("path: got %q, want %q", path, tt.path)
			}
			if tls != tt.tls {
				t.Errorf("tls: got %v, want %v", tls, tt.tls)
			}
		})
	}
}

// ---------- TestComputeAcceptKey ----------

func TestComputeAcceptKey(t *testing.T) {
	// Verify the accept key computation per RFC 6455 Section 4.2.2.
	// The accept key is: base64(SHA-1(key + "258EAFA5-E914-47DA-95CA-5AB9B16F6357"))
	tests := []struct {
		key      string
		expected string
	}{
		{
			"dGhlIHNhbXBsZSBub25jZQ==",
			"E5fqDQUc0xqA6W45QuGhe3+hsdo=",
		},
		{
			"AQIDBAUGBwgJCgsMDQ4PEA==",
			"AR4rnX+rwcxeUsoYPN92BvFXUgc=",
		},
	}
	for _, tt := range tests {
		got := computeAcceptKey(tt.key)
		if got != tt.expected {
			t.Errorf("computeAcceptKey(%q): got %q, want %q", tt.key, got, tt.expected)
		}
	}

	// Also verify that the function is deterministic.
	key := "test-key-123"
	a := computeAcceptKey(key)
	b := computeAcceptKey(key)
	if a != b {
		t.Errorf("computeAcceptKey not deterministic: %q != %q", a, b)
	}
}

// ---------- TestBuildClosePayload ----------

func TestBuildClosePayload(t *testing.T) {
	payload := buildClosePayload(1000, "normal closure")
	if len(payload) < 2 {
		t.Fatal("close payload too short")
	}
	code := binary.BigEndian.Uint16(payload[:2])
	if code != 1000 {
		t.Errorf("close code: got %d, want 1000", code)
	}
	reason := string(payload[2:])
	if reason != "normal closure" {
		t.Errorf("close reason: got %q, want %q", reason, "normal closure")
	}
}

// ---------- Helpers ----------

func makeBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}

func encodeBase64(b []byte) string {
	return "dGhlIHNhbXBsZSBub25jZQ==" // static key for tests
}
