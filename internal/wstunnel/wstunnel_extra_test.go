package wstunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- IsConnected ----------

func TestWSClient_IsConnected(t *testing.T) {
	// Before connecting, IsConnected should return false.
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if client.IsConnected() {
		t.Error("should not be connected before Connect()")
	}

	// After close, IsConnected should return false.
	client.Close()
	if client.IsConnected() {
		t.Error("should not be connected after Close()")
	}
}

func TestWSClient_IsConnected_AfterConnect(t *testing.T) {
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

	if !client.IsConnected() {
		t.Error("should be connected after successful Connect()")
	}

	client.Close()
	if client.IsConnected() {
		t.Error("should not be connected after Close()")
	}
}

// ---------- Connect when closed ----------

func TestWSClient_Connect_WhenClosed(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	client.Close()

	ctx := context.Background()
	err := client.Connect(ctx)
	if err != ErrConnClosed {
		t.Errorf("Connect after close: got %v, want ErrConnClosed", err)
	}
}

// ---------- Recv when closed / nil ----------

func TestWSClient_Recv_WhenClosed(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	client.Close()

	_, err := client.Recv()
	if err != ErrConnClosed {
		t.Errorf("Recv after close: got %v, want ErrConnClosed", err)
	}
}

func TestWSClient_Recv_NilBrw(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// brw is nil because we never connected
	_, err := client.Recv()
	if err != ErrConnClosed {
		t.Errorf("Recv with nil brw: got %v, want ErrConnClosed", err)
	}
}

// ---------- Send when nil brw ----------

func TestWSClient_Send_NilBrw(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := client.Send([]byte("test"))
	if err != ErrConnClosed {
		t.Errorf("Send with nil brw: got %v, want ErrConnClosed", err)
	}
}

// ---------- Close idempotent ----------

func TestWSClient_Close_Idempotent(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// First close
	if err := client.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close should also succeed (no-op)
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// ---------- Reconnect when closed ----------

func TestWSClient_Reconnect_WhenClosed(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	client.Close()

	ctx := context.Background()
	err := client.Reconnect(ctx)
	if err != ErrConnClosed {
		t.Errorf("Reconnect after close: got %v, want ErrConnClosed", err)
	}
}

// ---------- Reconnect max attempts exhausted ----------

func TestWSClient_Reconnect_MaxAttemptsExhausted(t *testing.T) {
	// Use a port that immediately refuses connections (port 1 on localhost)
	client := NewWSClient(WSClientConfig{
		ServerURL:      "ws://127.0.0.1:1/ws",
		ReconnectDelay: 10 * time.Millisecond,
		MaxReconnects:  2,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.Reconnect(ctx)
	if err == nil {
		t.Fatal("expected error from Reconnect, got nil")
	}
	if !strings.Contains(err.Error(), "reconnect failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- Reconnect context cancelled during delay ----------

func TestWSClient_Reconnect_ContextCancelled(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL:      "ws://127.0.0.1:1/ws",
		ReconnectDelay: 5 * time.Second,
		MaxReconnects:  10,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := client.Reconnect(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------- Reconnect closes existing connection ----------

func TestWSClient_Reconnect_ClosesExistingConnection(t *testing.T) {
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
		ServerURL:      wsURL,
		ReconnectDelay: 10 * time.Millisecond,
		MaxReconnects:  3,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Reconnect should close existing connection and make a new one
	if err := client.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	// Should still work
	if err := client.Send([]byte("after-reconnect")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.HasPrefix(resp, []byte("echo:")) {
		t.Errorf("unexpected response: %q", resp)
	}
}

// ---------- Server: missing headers ----------

func TestWSServer_MissingConnectionHeader(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Path:   "/ws",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Send request with Upgrade but no Connection header
	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	// Do NOT set Connection header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWSServer_MissingWebSocketKey(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Path:   "/ws",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	// Do NOT set Sec-WebSocket-Key

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestWSServer_WrongWebSocketVersion(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Path:   "/ws",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGVzdA==")
	req.Header.Set("Sec-WebSocket-Version", "8") // wrong version

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// ---------- Server: nil processor ----------

func TestWSServer_NilProcessor(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Path:   "/ws",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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

	// Send data with nil processor - should not crash, no response expected
	if err := client.Send([]byte("test")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Close gracefully
	client.Close()
}

// ---------- Server: default path ----------

func TestWSServer_DefaultPath(t *testing.T) {
	srv := NewWSServer(WSServerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if srv.config.Path != "/ws" {
		t.Errorf("default path = %q, want /ws", srv.config.Path)
	}
}

func TestWSServer_DefaultLogger(t *testing.T) {
	srv := NewWSServer(WSServerConfig{})
	if srv.logger == nil {
		t.Error("logger should not be nil")
	}
}

// ---------- Client: default config ----------

func TestWSClient_DefaultConfig(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://localhost/ws",
	})
	if client.config.ReconnectDelay != 1*time.Second {
		t.Errorf("default ReconnectDelay = %v, want 1s", client.config.ReconnectDelay)
	}
	if client.logger == nil {
		t.Error("logger should not be nil")
	}
}

// ---------- Recv: server sends ping, pong, close, unknown ----------

func TestWSClient_Recv_ServerSendsPing(t *testing.T) {
	// Set up a raw server that sends a ping then a binary frame
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the HTTP upgrade request
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		request := string(buf[:n])

		// Extract the key
		var wsKey string
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				wsKey = strings.TrimSpace(line[len("sec-websocket-key:"):])
			}
		}

		// Send 101 response
		acceptKey := computeAcceptKey(wsKey)
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
			"\r\n"
		conn.Write([]byte(resp))

		// Send a ping frame (server -> client, unmasked)
		writeFrame(conn, OpPing, []byte("hello"), false)

		// Then send a binary frame
		writeFrame(conn, OpBinary, []byte("actual-data"), false)

		// Read the pong and any other frames
		time.Sleep(200 * time.Millisecond)
	}()

	addr := ln.Addr().String()
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://" + addr + "/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Recv should transparently handle the ping and return the binary data
	data, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(data, []byte("actual-data")) {
		t.Errorf("got %q, want %q", data, "actual-data")
	}

	<-serverDone
}

func TestWSClient_Recv_ServerSendsClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		request := string(buf[:n])

		var wsKey string
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				wsKey = strings.TrimSpace(line[len("sec-websocket-key:"):])
			}
		}

		acceptKey := computeAcceptKey(wsKey)
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
			"\r\n"
		conn.Write([]byte(resp))

		// Send a close frame
		writeFrame(conn, OpClose, buildClosePayload(1000, "goodbye"), false)

		time.Sleep(200 * time.Millisecond)
	}()

	addr := ln.Addr().String()
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://" + addr + "/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_, err = client.Recv()
	if err != ErrConnClosed {
		t.Errorf("Recv after server close: got %v, want ErrConnClosed", err)
	}

	<-serverDone
}

func TestWSClient_Recv_ServerSendsPong(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		request := string(buf[:n])

		var wsKey string
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				wsKey = strings.TrimSpace(line[len("sec-websocket-key:"):])
			}
		}

		acceptKey := computeAcceptKey(wsKey)
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
			"\r\n"
		conn.Write([]byte(resp))

		// Send pong (unusual, but should be ignored by client)
		writeFrame(conn, OpPong, []byte("unsolicited"), false)

		// Then send actual data
		writeFrame(conn, OpBinary, []byte("real-data"), false)

		time.Sleep(200 * time.Millisecond)
	}()

	addr := ln.Addr().String()
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://" + addr + "/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Should skip the pong and return the binary data
	data, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(data, []byte("real-data")) {
		t.Errorf("got %q, want %q", data, "real-data")
	}

	<-serverDone
}

func TestWSClient_Recv_ServerSendsUnknownOpcode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		request := string(buf[:n])

		var wsKey string
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				wsKey = strings.TrimSpace(line[len("sec-websocket-key:"):])
			}
		}

		acceptKey := computeAcceptKey(wsKey)
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
			"\r\n"
		conn.Write([]byte(resp))

		// Send unknown opcode (3)
		writeFrame(conn, 3, []byte("unknown"), false)

		// Then actual data
		writeFrame(conn, OpBinary, []byte("valid-data"), false)

		time.Sleep(200 * time.Millisecond)
	}()

	addr := ln.Addr().String()
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://" + addr + "/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Should skip unknown opcode and return the binary data
	data, err := client.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(data, []byte("valid-data")) {
		t.Errorf("got %q, want %q", data, "valid-data")
	}

	<-serverDone
}

// ---------- Server serveLoop: ping from client ----------

func TestWSServer_ClientSendsPing(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Manual handshake
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	conn.Write([]byte(req))

	// Read 101
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("expected 101, got: %s", string(buf[:n]))
	}

	// Send a ping from client (masked)
	writeFrame(conn, OpPing, []byte("client-ping"), true)

	// Should receive pong back
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpPong {
		t.Errorf("expected pong (10), got %d", opcode)
	}
	if !bytes.Equal(payload, []byte("client-ping")) {
		t.Errorf("pong payload: got %q, want %q", payload, "client-ping")
	}
}

// ---------- Server serveLoop: client sends close ----------

func TestWSServer_ClientSendsClose(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("expected 101")
	}

	// Send close from client (masked)
	closePayload := buildClosePayload(1000, "bye")
	writeFrame(conn, OpClose, closePayload, true)

	// Should receive close echo
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpClose {
		t.Errorf("expected close (8), got %d", opcode)
	}
	if len(payload) >= 2 {
		code := binary.BigEndian.Uint16(payload[:2])
		if code != 1000 {
			t.Errorf("close code: got %d, want 1000", code)
		}
	}
}

// ---------- Server serveLoop: unknown opcode from client ----------

func TestWSServer_ClientSendsUnknownOpcode(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("expected 101")
	}

	// Send unknown opcode (masked, since we're client)
	writeFrame(conn, 3, []byte("unknown"), true)

	// Then send a normal binary frame
	writeFrame(conn, OpBinary, []byte("test"), true)

	// Should receive echo response for the binary frame (unknown opcode is silently skipped)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpBinary {
		t.Errorf("expected binary (2), got %d", opcode)
	}
	if !bytes.HasPrefix(payload, []byte("echo:")) {
		t.Errorf("expected echo response, got %q", payload)
	}
}

// ---------- Server serveLoop: pong from client ----------

func TestWSServer_ClientSendsPong(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("expected 101")
	}

	// Send unsolicited pong (masked)
	writeFrame(conn, OpPong, []byte("unsolicited"), true)

	// Then send binary data
	writeFrame(conn, OpBinary, []byte("after-pong"), true)

	// Should process the binary frame (pong was ignored)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpBinary {
		t.Errorf("expected binary (2), got %d", opcode)
	}
	expected := append([]byte("echo:"), []byte("after-pong")...)
	if !bytes.Equal(payload, expected) {
		t.Errorf("got %q, want %q", payload, expected)
	}
}

// ---------- readFrame: payload too long ----------

func TestReadFrame_PayloadTooLong(t *testing.T) {
	// Craft a frame header that claims a huge payload
	var buf bytes.Buffer
	buf.WriteByte(0x82) // FIN + binary
	buf.WriteByte(127)  // 64-bit length
	// Write length > 16MB
	lenBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(lenBytes, 20*1024*1024)
	buf.Write(lenBytes)

	_, _, err := readFrame(&buf)
	if err != ErrPayloadTooLong {
		t.Errorf("expected ErrPayloadTooLong, got %v", err)
	}
}

// ---------- readFrame: truncated header ----------

func TestReadFrame_TruncatedHeader(t *testing.T) {
	// Only 1 byte (need at least 2)
	buf := bytes.NewBuffer([]byte{0x82})
	_, _, err := readFrame(buf)
	if err == nil {
		t.Error("expected error for truncated header")
	}
}

// ---------- readFrame: truncated extended length ----------

func TestReadFrame_TruncatedExtendedLength(t *testing.T) {
	// Header says 126 (2-byte extended) but only 1 extra byte
	buf := bytes.NewBuffer([]byte{0x82, 126, 0x01})
	_, _, err := readFrame(buf)
	if err == nil {
		t.Error("expected error for truncated extended length")
	}
}

// ---------- readFrame: truncated 64-bit length ----------

func TestReadFrame_Truncated64BitLength(t *testing.T) {
	// Header says 127 (8-byte extended) but only 4 extra bytes
	buf := bytes.NewBuffer([]byte{0x82, 127, 0, 0, 0, 0})
	_, _, err := readFrame(buf)
	if err == nil {
		t.Error("expected error for truncated 64-bit length")
	}
}

// ---------- readFrame: truncated mask key ----------

func TestReadFrame_TruncatedMaskKey(t *testing.T) {
	// Masked frame with length 1 but no mask key data
	buf := bytes.NewBuffer([]byte{0x82, 0x81}) // masked, len=1, missing 4-byte mask + payload
	_, _, err := readFrame(buf)
	if err == nil {
		t.Error("expected error for truncated mask key")
	}
}

// ---------- writeFrame: writer error ----------

func TestWriteFrame_WriterError(t *testing.T) {
	w := &errorWriter{failAfter: 0}
	err := writeFrame(w, OpBinary, []byte("test"), false)
	if err == nil {
		t.Error("expected error from writer")
	}
}

type errorWriter struct {
	written  int
	failAfter int
}

func (w *errorWriter) Write(b []byte) (int, error) {
	if w.written >= w.failAfter {
		return 0, fmt.Errorf("write error")
	}
	w.written += len(b)
	return len(b), nil
}

// ---------- parseWSURL additional cases ----------

func TestParseWSURL_HTTPS(t *testing.T) {
	host, path, tls, err := parseWSURL("https://example.com/tunnel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "example.com:443" {
		t.Errorf("host = %q, want example.com:443", host)
	}
	if path != "/tunnel" {
		t.Errorf("path = %q, want /tunnel", path)
	}
	if !tls {
		t.Error("should be TLS")
	}
}

func TestParseWSURL_BareHostPort(t *testing.T) {
	host, path, tls, err := parseWSURL("myhost:9090/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "myhost:9090" {
		t.Errorf("host = %q, want myhost:9090", host)
	}
	if path != "/path" {
		t.Errorf("path = %q, want /path", path)
	}
	if tls {
		t.Error("should not be TLS for bare host")
	}
}

func TestParseWSURL_BareHostNoPath(t *testing.T) {
	host, path, _, err := parseWSURL("myhost:9090")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "myhost:9090" {
		t.Errorf("host = %q, want myhost:9090", host)
	}
	if path != "/" {
		t.Errorf("path = %q, want /", path)
	}
}

// ---------- headerContains additional ----------

func TestHeaderContains_MultipleValues(t *testing.T) {
	h := http.Header{}
	h.Add("Connection", "keep-alive, Upgrade")
	if !headerContains(h, "Connection", "upgrade") {
		t.Error("should find 'upgrade' in comma-separated values")
	}
}

func TestHeaderContains_NotFound(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive")
	if headerContains(h, "Connection", "upgrade") {
		t.Error("should not find 'upgrade' in 'keep-alive'")
	}
}

func TestHeaderContains_EmptyHeader(t *testing.T) {
	h := http.Header{}
	if headerContains(h, "Connection", "upgrade") {
		t.Error("should not find anything in empty headers")
	}
}

// ---------- connectLocked: non-101 response ----------

func TestWSClient_Connect_Non101Response(t *testing.T) {
	// Server that returns 200 instead of 101
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not a websocket"))
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	client := NewWSClient(WSClientConfig{
		ServerURL: wsURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error for non-101 response")
	}
	if !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------- connectLocked: invalid accept key ----------

func TestWSClient_Connect_InvalidAcceptKey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.Read(buf)

		// Send 101 with wrong accept key
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: wrongkey==\r\n" +
			"\r\n"
		conn.Write([]byte(resp))
	}()

	addr := ln.Addr().String()
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://" + addr + "/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error for invalid accept key")
	}
	if !strings.Contains(err.Error(), "invalid accept key") {
		t.Errorf("unexpected error: %v", err)
	}

	<-serverDone
}

// ---------- connectLocked: invalid URL ----------

func TestWSClient_Connect_InvalidURL(t *testing.T) {
	client := NewWSClient(WSClientConfig{
		ServerURL: "",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

// ---------- connectLocked: connection refused ----------

func TestWSClient_Connect_ConnectionRefused(t *testing.T) {
	// Use a port that's likely not in use
	client := NewWSClient(WSClientConfig{
		ServerURL: "ws://127.0.0.1:1/ws",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

// ---------- Text opcode support ----------

func TestWSServer_TextOpcode(t *testing.T) {
	proc := &mockProcessor{}
	srv := NewWSServer(WSServerConfig{
		Path:      "/ws",
		Processor: proc,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("expected 101")
	}

	// Send a text frame (masked, as client)
	writeFrame(conn, OpText, []byte("hello text"), true)

	// Should receive response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != OpBinary {
		t.Errorf("expected binary response (2), got %d", opcode)
	}
	if !bytes.HasPrefix(payload, []byte("echo:")) {
		t.Errorf("expected echo response, got %q", payload)
	}
}

// ---------- Close with active connection ----------

func TestWSClient_Close_WithActiveConnection(t *testing.T) {
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

	// Close with active connection
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Everything should return ErrConnClosed now
	if err := client.Send([]byte("test")); err != ErrConnClosed {
		t.Errorf("Send: got %v, want ErrConnClosed", err)
	}
	if _, err := client.Recv(); err != ErrConnClosed {
		t.Errorf("Recv: got %v, want ErrConnClosed", err)
	}
	if err := client.Connect(ctx); err != ErrConnClosed {
		t.Errorf("Connect: got %v, want ErrConnClosed", err)
	}
}
