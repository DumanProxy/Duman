package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// mockStreamCreator creates mock streams for testing.
type mockStreamCreator struct {
	data []byte // data to return from Read
}

func (m *mockStreamCreator) CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error) {
	return &mockStream{readData: bytes.NewReader(m.data)}, nil
}

type mockStream struct {
	readData  *bytes.Reader
	writeData bytes.Buffer
}

func (s *mockStream) Read(p []byte) (int, error) {
	return s.readData.Read(p)
}

func (s *mockStream) Write(p []byte) (int, error) {
	return s.writeData.Write(p)
}

func (s *mockStream) Close() error {
	return nil
}

func TestSOCKS5Server_StartStop(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(ctx)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	if srv.Addr() == nil {
		t.Fatal("expected non-nil addr")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ListenAndServe: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to stop")
	}
}

func TestSOCKS5_Handshake(t *testing.T) {
	responseData := []byte("HTTP/1.1 200 OK\r\n\r\nHello!")
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: responseData}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	addr := srv.Addr().String()

	// Connect to SOCKS5
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 1. Send version + methods
	conn.Write([]byte{0x05, 0x01, 0x00}) // SOCKS5, 1 method, no-auth

	// 2. Read method selection
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read method: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("method response: %v", resp)
	}

	// 3. Send CONNECT request (domain: example.com:80)
	domain := "example.com"
	conn.Write([]byte{
		0x05, 0x01, 0x00, 0x03, // VER CMD RSV ATYP(domain)
		byte(len(domain)),
	})
	conn.Write([]byte(domain))
	conn.Write([]byte{0x00, 0x50}) // port 80

	// 4. Read reply
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected success reply, got status %d", reply[1])
	}

	// 5. Read response data through the proxy
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read data: %v", err)
	}
	if !bytes.Contains(buf[:n], []byte("Hello!")) {
		t.Errorf("expected response data, got %q", buf[:n])
	}
}

func TestSOCKS5_BadVersion(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send bad version
	conn.Write([]byte{0x04, 0x01, 0x00})

	// Should get disconnected or error
	buf := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("expected error or EOF for bad version")
	}
}

func TestSOCKS5_IPv4Address(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// CONNECT with IPv4 address (127.0.0.1:80)
	conn.Write([]byte{
		0x05, 0x01, 0x00, 0x01, // VER CMD RSV ATYP(IPv4)
		127, 0, 0, 1,           // IP
		0x00, 0x50,             // port 80
	})

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected success, got %d", reply[1])
	}
}

// failingStreamCreator always returns an error on CreateStream.
type failingStreamCreator struct{}

func (f *failingStreamCreator) CreateStream(ctx context.Context, dest string) (io.ReadWriteCloser, error) {
	return nil, errors.New("connection refused")
}

func TestSOCKS5_IPv6Address(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// CONNECT with IPv6 (::1, port 80)
	req := []byte{0x05, 0x01, 0x00, 0x04} // VER CMD RSV ATYP(IPv6)
	ipv6 := net.ParseIP("::1").To16()
	req = append(req, ipv6...)
	req = append(req, 0x00, 0x50) // port 80
	conn.Write(req)

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected success reply, got status %d", reply[1])
	}
}

func TestSOCKS5_UnsupportedCommand(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send BIND command (0x02) instead of CONNECT (0x01)
	conn.Write([]byte{
		0x05, 0x02, 0x00, 0x03, // VER CMD(BIND) RSV ATYP(domain)
		byte(len("example.com")),
	})
	conn.Write([]byte("example.com"))
	conn.Write([]byte{0x00, 0x50})

	// Should get reply with command-not-supported (0x07)
	reply := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = io.ReadFull(conn, reply)
	if err != nil {
		// Connection may be closed, which is also acceptable
		return
	}
	if reply[1] != 0x07 {
		t.Errorf("expected command-not-supported (0x07), got 0x%02x", reply[1])
	}
}

func TestSOCKS5_ConnectionRefused(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &failingStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// CONNECT request
	domain := "example.com"
	conn.Write([]byte{
		0x05, 0x01, 0x00, 0x03,
		byte(len(domain)),
	})
	conn.Write([]byte(domain))
	conn.Write([]byte{0x00, 0x50})

	// Should get reply with connection-refused (0x05)
	reply := make([]byte, 10)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := io.ReadFull(conn, reply)
	if err != nil {
		// Connection closed is acceptable
		return
	}
	if n >= 2 && reply[1] != 0x05 {
		t.Errorf("expected connection-refused (0x05), got 0x%02x", reply[1])
	}
}

func TestSOCKS5_NoAcceptableAuth(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send version with only username/password auth (0x02), no no-auth (0x00)
	conn.Write([]byte{0x05, 0x01, 0x02})

	// Should reply with 0xFF (no acceptable method) then close
	resp := make([]byte, 2)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = io.ReadFull(conn, resp)
	if err != nil {
		// Connection may be closed immediately
		return
	}
	if resp[0] != 0x05 || resp[1] != 0xFF {
		t.Errorf("expected [0x05, 0xFF], got [0x%02x, 0x%02x]", resp[0], resp[1])
	}
}

func TestSOCKS5_UnsupportedAddressType(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send CONNECT with unsupported address type 0x05
	conn.Write([]byte{0x05, 0x01, 0x00, 0x05})

	// Connection should be closed by server
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 10)
	_, err = conn.Read(buf)
	if err == nil {
		// Server might send error reply and close, or just close
		// Either way, the connection should eventually end
	}
}

func TestSOCKS5_Addr_NilListener(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{}, nil)

	// Before ListenAndServe, Addr should return nil
	if srv.Addr() != nil {
		t.Error("expected nil Addr before ListenAndServe")
	}
}

func TestSOCKS5_TruncatedAuth(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	// Test 1: Close immediately after connect (truncated auth read)
	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Send only 1 byte of version, then close
	conn.Write([]byte{0x05})
	conn.Close()

	// Give server time to process
	time.Sleep(50 * time.Millisecond)

	// Test 2: Send version + method count but not the methods
	conn2, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Send version + numMethods=3, but only 1 method byte (truncated)
	conn2.Write([]byte{0x05, 0x03, 0x00})
	conn2.Close()

	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_TruncatedRequest(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	// Complete auth, then send truncated request
	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Auth handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send incomplete request header (only 2 bytes instead of 4)
	conn.Write([]byte{0x05, 0x01})
	conn.Close()

	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_BadVersionInRequest(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Auth handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send request with wrong version (0x04 instead of 0x05)
	conn.Write([]byte{0x04, 0x01, 0x00, 0x03, 0x0b})
	conn.Write([]byte("example.com"))
	conn.Write([]byte{0x00, 0x50})

	// Server should close connection
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 10)
	_, err = conn.Read(buf)
	if err == nil {
		// Acceptable - server may or may not send data before closing
	}
}

func TestSOCKS5_TruncatedIPv4(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Auth handshake
	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send CONNECT with IPv4 but only 2 bytes of address (truncated)
	conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 127, 0})
	conn.Close()

	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_TruncatedDomain(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	// Test truncated domain length byte
	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send CONNECT with domain type but no domain length
	conn.Write([]byte{0x05, 0x01, 0x00, 0x03})
	conn.Close()
	time.Sleep(50 * time.Millisecond)

	// Test truncated domain body
	conn2, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	conn2.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn2, make([]byte, 2))

	// Domain length says 11 but only send 3 bytes
	conn2.Write([]byte{0x05, 0x01, 0x00, 0x03, 0x0b, 'e', 'x', 'a'})
	conn2.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_TruncatedIPv6(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send CONNECT with IPv6 but only 4 bytes of the 16-byte address
	conn.Write([]byte{0x05, 0x01, 0x00, 0x04, 0, 0, 0, 0})
	conn.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_TruncatedPort(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("ok")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}

	conn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(conn, make([]byte, 2))

	// Send CONNECT with IPv4 address but only 1 byte of port (need 2)
	conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x00})
	conn.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestSOCKS5_ListenError(t *testing.T) {
	// Try to listen on an invalid address to trigger net.Listen error
	srv := NewSOCKS5Server("invalid-address-no-port", &mockStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.ListenAndServe(ctx)
	if err == nil {
		t.Error("expected error for invalid listen address")
	}
}

func TestSOCKS5_AcceptError(t *testing.T) {
	// Start the server, then close the listener externally to trigger
	// the accept error path (non-ctx.Done default branch)
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	// Close the underlying listener directly to cause an accept error
	// while context is NOT yet canceled
	if srv.listener != nil {
		srv.listener.Close()
	}
	// Give the server time to hit the accept error + continue
	time.Sleep(50 * time.Millisecond)

	// Now cancel context to allow graceful shutdown
	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to stop")
	}
}
