package relay

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// ---------- Forwarder Tests ----------

func TestNewForwarder(t *testing.T) {
	f := NewForwarder("127.0.0.1:9999", nil)
	if f == nil {
		t.Fatal("expected non-nil forwarder")
	}
	if f.targetAddr != "127.0.0.1:9999" {
		t.Fatalf("targetAddr = %q, want 127.0.0.1:9999", f.targetAddr)
	}
	if f.healthy {
		t.Fatal("expected healthy=false initially")
	}
}

func TestNewForwarder_WithLogger(t *testing.T) {
	logger := slog.Default()
	f := NewForwarder("127.0.0.1:9999", logger)
	if f.logger != logger {
		t.Fatal("expected logger to be set")
	}
}

func TestForwarder_IsHealthy_Initial(t *testing.T) {
	f := NewForwarder("127.0.0.1:9999", nil)
	if f.IsHealthy() {
		t.Fatal("expected not healthy before connect")
	}
}

func TestForwarder_Close_NotConnected(t *testing.T) {
	f := NewForwarder("127.0.0.1:9999", nil)
	if err := f.Close(); err != nil {
		t.Fatalf("Close on unconnected forwarder: %v", err)
	}
	if f.IsHealthy() {
		t.Fatal("expected not healthy after close")
	}
}

func TestForwarder_ProcessChunk_NotConnected(t *testing.T) {
	f := NewForwarder("127.0.0.1:9999", nil)
	ch := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte("hello"),
	}
	err := f.ProcessChunk(ch)
	if err == nil {
		t.Fatal("expected error when not connected")
	}
	if f.IsHealthy() {
		t.Fatal("expected not healthy after failed write")
	}
}

func TestForwarder_ConnectAndProcessChunk(t *testing.T) {
	// Start a TCP listener to accept the forwarder connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var received []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the length-prefixed frame
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil && err != io.EOF {
			return
		}
		received = buf[:n]
	}()

	f := NewForwarder(ln.Addr().String(), nil)
	ctx := context.Background()
	if err := f.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !f.IsHealthy() {
		t.Fatal("expected healthy after connect")
	}

	ch := &crypto.Chunk{
		StreamID: 42,
		Sequence: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte("test payload"),
	}
	if err := f.ProcessChunk(ch); err != nil {
		t.Fatalf("ProcessChunk: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.IsHealthy() {
		t.Fatal("expected not healthy after close")
	}

	wg.Wait()

	// Verify the frame: 4 bytes length prefix + marshaled chunk
	if len(received) < 4 {
		t.Fatalf("received too short: %d bytes", len(received))
	}
	frameLen := int(received[0])<<24 | int(received[1])<<16 | int(received[2])<<8 | int(received[3])
	if frameLen != len(received)-4 {
		t.Fatalf("frame length = %d, payload = %d", frameLen, len(received)-4)
	}

	// Unmarshal the chunk to verify round-trip
	parsed, err := crypto.UnmarshalChunk(received[4:])
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}
	if parsed.StreamID != 42 {
		t.Errorf("StreamID = %d, want 42", parsed.StreamID)
	}
	if string(parsed.Payload) != "test payload" {
		t.Errorf("Payload = %q, want %q", parsed.Payload, "test payload")
	}
}

func TestForwarder_ConnectFails(t *testing.T) {
	// Use a port that's not listening.
	f := NewForwarder("127.0.0.1:1", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := f.Connect(ctx)
	if err == nil {
		f.Close()
		t.Fatal("expected error connecting to non-listening port")
	}
}

func TestForwarder_ProcessChunk_WriteFails(t *testing.T) {
	// Start a listener, accept, then close immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Close immediately to cause a write failure
		conn.Close()
	}()

	f := NewForwarder(ln.Addr().String(), nil)
	ctx := context.Background()
	if err := f.Connect(ctx); err != nil {
		ln.Close()
		t.Fatalf("Connect: %v", err)
	}
	ln.Close()
	wg.Wait()

	// Give the connection time to be detected as closed
	time.Sleep(50 * time.Millisecond)

	ch := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte("data"),
	}

	// Write may or may not fail on the first attempt depending on OS buffering,
	// but repeated writes to a closed connection will eventually fail.
	var writeErr error
	for i := 0; i < 10; i++ {
		writeErr = f.ProcessChunk(ch)
		if writeErr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if writeErr == nil {
		t.Fatal("expected write error to closed connection")
	}
	if f.IsHealthy() {
		t.Fatal("expected not healthy after write failure")
	}
	f.Close()
}

// ---------- ForwardListener Tests ----------

func TestNewForwardListener(t *testing.T) {
	handler := func(ch *crypto.Chunk) error { return nil }
	fl := NewForwardListener("127.0.0.1:0", handler, nil)
	if fl == nil {
		t.Fatal("expected non-nil forward listener")
	}
}

func TestNewForwardListener_WithLogger(t *testing.T) {
	logger := slog.Default()
	handler := func(ch *crypto.Chunk) error { return nil }
	fl := NewForwardListener("127.0.0.1:0", handler, logger)
	if fl.logger != logger {
		t.Fatal("expected logger to be set")
	}
}

func TestForwardListener_ListenAndServe(t *testing.T) {
	var mu sync.Mutex
	var receivedChunks []*crypto.Chunk
	handler := func(ch *crypto.Chunk) error {
		mu.Lock()
		receivedChunks = append(receivedChunks, ch)
		mu.Unlock()
		return nil
	}

	fl := NewForwardListener("127.0.0.1:0", handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- fl.ListenAndServe(ctx)
	}()

	// Wait for listener to be ready
	time.Sleep(100 * time.Millisecond)

	if fl.listener == nil {
		t.Fatal("expected listener to be set after ListenAndServe starts")
	}

	// Connect and send a chunk frame
	conn, err := net.Dial("tcp", fl.listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	ch := &crypto.Chunk{
		StreamID: 7,
		Sequence: 3,
		Type:     crypto.ChunkData,
		Flags:    crypto.FlagCompressed,
		Payload:  []byte("forwarded data"),
	}
	data, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Write length-prefixed frame
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(data)))
	copy(frame[4:], data)

	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}
	conn.Close()

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(receivedChunks)
	mu.Unlock()

	if count != 1 {
		t.Fatalf("received %d chunks, want 1", count)
	}

	mu.Lock()
	got := receivedChunks[0]
	mu.Unlock()

	if got.StreamID != 7 {
		t.Errorf("StreamID = %d, want 7", got.StreamID)
	}
	if string(got.Payload) != "forwarded data" {
		t.Errorf("Payload = %q, want %q", got.Payload, "forwarded data")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Logf("ListenAndServe returned: %v (expected on cancellation)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ListenAndServe to return")
	}
}

func TestForwardListener_InvalidFrameLength(t *testing.T) {
	handler := func(ch *crypto.Chunk) error { return nil }
	fl := NewForwardListener("127.0.0.1:0", handler, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fl.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("tcp", fl.listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Send a frame with length 0 (invalid: <= 0)
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame[0:4], 0)
	conn.Write(frame)

	// Give handler time to process and close
	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

func TestForwardListener_InvalidChunkData(t *testing.T) {
	handler := func(ch *crypto.Chunk) error { return nil }
	fl := NewForwardListener("127.0.0.1:0", handler, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fl.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("tcp", fl.listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Send a frame with valid length but garbage data (too short for UnmarshalChunk)
	garbage := []byte("bad")
	frame := make([]byte, 4+len(garbage))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(garbage)))
	copy(frame[4:], garbage)
	conn.Write(frame)

	// Give handler time to log and continue
	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

func TestForwardListener_FrameTooLarge(t *testing.T) {
	handler := func(ch *crypto.Chunk) error { return nil }
	fl := NewForwardListener("127.0.0.1:0", handler, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fl.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("tcp", fl.listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Send a frame with length > 1<<20 (too large)
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame[0:4], uint32(1<<20+1))
	conn.Write(frame)

	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

func TestForwarder_ProcessChunk_MarshalError(t *testing.T) {
	// Start a TCP listener to accept the forwarder connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read everything
		buf := make([]byte, 4096)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	f := NewForwarder(ln.Addr().String(), nil)
	ctx := context.Background()
	if err := f.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer f.Close()

	// Create a chunk with payload exceeding MaxPayloadSize to trigger marshal error
	ch := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkData,
		Payload:  make([]byte, crypto.MaxPayloadSize+1),
	}
	err = f.ProcessChunk(ch)
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

func TestForwardListener_HandlerError(t *testing.T) {
	handler := func(ch *crypto.Chunk) error {
		return fmt.Errorf("intentional handler error")
	}

	fl := NewForwardListener("127.0.0.1:0", handler, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go fl.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("tcp", fl.listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Send a valid chunk that will trigger the handler error
	ch := &crypto.Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte("data"),
	}
	data, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(data)))
	copy(frame[4:], data)

	conn.Write(frame)
	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

// ---------- readFull Tests ----------

func TestReadFull(t *testing.T) {
	// Test with a pipe (exact-size reads)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	want := []byte("hello world 1234")

	go func() {
		// Write in small chunks to exercise the loop
		server.Write(want[:5])
		time.Sleep(10 * time.Millisecond)
		server.Write(want[5:10])
		time.Sleep(10 * time.Millisecond)
		server.Write(want[10:])
	}()

	buf := make([]byte, len(want))
	n, err := readFull(client, buf)
	if err != nil {
		t.Fatalf("readFull: %v", err)
	}
	if n != len(want) {
		t.Fatalf("readFull n = %d, want %d", n, len(want))
	}
	if string(buf) != string(want) {
		t.Fatalf("readFull data = %q, want %q", buf, want)
	}
}

func TestReadFull_ConnectionClosed(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	// Close the writer side to cause EOF
	server.Close()

	buf := make([]byte, 10)
	_, err := readFull(client, buf)
	if err == nil {
		t.Fatal("expected error on closed connection")
	}
}
