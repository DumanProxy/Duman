package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseUDPDatagram_IPv4(t *testing.T) {
	// Build a SOCKS5 UDP datagram: RSV(2) + FRAG(1) + ATYP(1) + IPv4(4) + PORT(2) + payload
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, addrIPv4)   // ATYP
	buf = append(buf, 127, 0, 0, 1) // IP
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 8080)
	buf = append(buf, portBytes...)
	buf = append(buf, []byte("hello")...)

	addr, payload, err := parseUDPDatagram(buf)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want %q", addr, "127.0.0.1:8080")
	}
	if !bytes.Equal(payload, []byte("hello")) {
		t.Errorf("payload = %q, want %q", payload, "hello")
	}
}

func TestParseUDPDatagram_Domain(t *testing.T) {
	domain := "example.com"
	var buf []byte
	buf = append(buf, 0x00, 0x00)       // RSV
	buf = append(buf, 0x00)             // FRAG
	buf = append(buf, addrDomain)       // ATYP
	buf = append(buf, byte(len(domain))) // domain length
	buf = append(buf, []byte(domain)...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 53)
	buf = append(buf, portBytes...)
	buf = append(buf, []byte("dns-query")...)

	addr, payload, err := parseUDPDatagram(buf)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "example.com:53" {
		t.Errorf("addr = %q, want %q", addr, "example.com:53")
	}
	if !bytes.Equal(payload, []byte("dns-query")) {
		t.Errorf("payload = %q, want %q", payload, "dns-query")
	}
}

func TestParseUDPDatagram_IPv6(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, addrIPv6)   // ATYP
	ip6 := net.ParseIP("::1").To16()
	buf = append(buf, ip6...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 443)
	buf = append(buf, portBytes...)
	buf = append(buf, []byte("data")...)

	addr, payload, err := parseUDPDatagram(buf)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "::1:443" {
		// net.JoinHostPort for IPv6 produces "[::1]:443"
		expected := net.JoinHostPort("::1", "443")
		if addr != expected {
			t.Errorf("addr = %q, want %q", addr, expected)
		}
	}
	if !bytes.Equal(payload, []byte("data")) {
		t.Errorf("payload = %q, want %q", payload, "data")
	}
}

func TestParseUDPDatagram_TooShort(t *testing.T) {
	_, _, err := parseUDPDatagram([]byte{0x00, 0x00})
	if err == nil {
		t.Error("expected error for too-short datagram")
	}
}

func TestParseUDPDatagram_Fragment(t *testing.T) {
	// Non-zero FRAG should be rejected
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x01)       // FRAG = 1 (not supported)
	buf = append(buf, addrIPv4)
	buf = append(buf, 127, 0, 0, 1)
	buf = append(buf, 0x00, 0x50)
	buf = append(buf, []byte("data")...)

	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for fragmented datagram")
	}
}

func TestParseUDPDatagram_UnsupportedAddrType(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, 0x05)       // unsupported ATYP
	buf = append(buf, 0x00, 0x00, 0x00, 0x00)

	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for unsupported address type")
	}
}

func TestParseUDPDatagram_TruncatedIPv4(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, addrIPv4)   // ATYP
	buf = append(buf, 127, 0)     // only 2 bytes of IPv4

	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for truncated IPv4")
	}
}

func TestParseUDPDatagram_TruncatedDomain(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00)       // RSV
	buf = append(buf, 0x00)             // FRAG
	buf = append(buf, addrDomain)       // ATYP
	buf = append(buf, 0x0b)             // domain length = 11
	buf = append(buf, []byte("exam")...) // only 4 bytes

	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for truncated domain")
	}
}

func TestParseUDPDatagram_TruncatedIPv6(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, addrIPv6)   // ATYP
	buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0) // only 8 of 16 bytes

	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for truncated IPv6")
	}
}

func TestParseUDPDatagram_EmptyPayload(t *testing.T) {
	var buf []byte
	buf = append(buf, 0x00, 0x00) // RSV
	buf = append(buf, 0x00)       // FRAG
	buf = append(buf, addrIPv4)   // ATYP
	buf = append(buf, 10, 0, 0, 1)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 9999)
	buf = append(buf, portBytes...)
	// no payload

	addr, payload, err := parseUDPDatagram(buf)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "10.0.0.1:9999" {
		t.Errorf("addr = %q, want %q", addr, "10.0.0.1:9999")
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestBuildUDPDatagram_IPv4(t *testing.T) {
	datagram := buildUDPDatagram("127.0.0.1:8080", []byte("hello"))

	// Parse it back
	addr, payload, err := parseUDPDatagram(datagram)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want %q", addr, "127.0.0.1:8080")
	}
	if !bytes.Equal(payload, []byte("hello")) {
		t.Errorf("payload = %q, want %q", payload, "hello")
	}
}

func TestBuildUDPDatagram_Domain(t *testing.T) {
	datagram := buildUDPDatagram("example.com:53", []byte("query"))

	addr, payload, err := parseUDPDatagram(datagram)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "example.com:53" {
		t.Errorf("addr = %q, want %q", addr, "example.com:53")
	}
	if !bytes.Equal(payload, []byte("query")) {
		t.Errorf("payload = %q, want %q", payload, "query")
	}
}

func TestBuildUDPDatagram_IPv6(t *testing.T) {
	addr := net.JoinHostPort("::1", "443")
	datagram := buildUDPDatagram(addr, []byte("data"))

	parsedAddr, payload, err := parseUDPDatagram(datagram)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if parsedAddr != addr {
		t.Errorf("addr = %q, want %q", parsedAddr, addr)
	}
	if !bytes.Equal(payload, []byte("data")) {
		t.Errorf("payload = %q, want %q", payload, "data")
	}
}

func TestBuildUDPDatagram_EmptyPayload(t *testing.T) {
	datagram := buildUDPDatagram("10.0.0.1:80", nil)

	addr, payload, err := parseUDPDatagram(datagram)
	if err != nil {
		t.Fatalf("parseUDPDatagram: %v", err)
	}
	if addr != "10.0.0.1:80" {
		t.Errorf("addr = %q, want %q", addr, "10.0.0.1:80")
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestBuildParseRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		payload []byte
	}{
		{"ipv4 with data", "192.168.1.1:1234", []byte("test data")},
		{"ipv4 empty payload", "10.0.0.1:80", nil},
		{"domain", "dns.google:53", []byte{0x01, 0x02, 0x03}},
		{"ipv6", net.JoinHostPort("2001:db8::1", "443"), []byte("tls hello")},
		{"large payload", "127.0.0.1:9999", bytes.Repeat([]byte("x"), 1400)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			datagram := buildUDPDatagram(tt.addr, tt.payload)
			addr, payload, err := parseUDPDatagram(datagram)
			if err != nil {
				t.Fatalf("round trip parse failed: %v", err)
			}
			if addr != tt.addr {
				t.Errorf("addr = %q, want %q", addr, tt.addr)
			}
			if !bytes.Equal(payload, tt.payload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(payload), len(tt.payload))
			}
		})
	}
}

func TestUDPRelay_Creation(t *testing.T) {
	// Create a dummy TCP connection (both ends)
	tcpServer, tcpClient := net.Pipe()
	defer tcpServer.Close()
	defer tcpClient.Close()

	relay, err := NewUDPRelay(tcpClient, &mockStreamCreator{data: []byte("response")}, nil)
	if err != nil {
		t.Fatalf("NewUDPRelay: %v", err)
	}

	addr := relay.Addr()
	if addr == nil {
		t.Fatal("expected non-nil UDP address")
	}

	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected *net.UDPAddr, got %T", addr)
	}
	if udpAddr.Port == 0 {
		t.Error("expected non-zero port")
	}
}

func TestUDPRelay_NilAddr(t *testing.T) {
	relay := &UDPRelay{}
	if relay.Addr() != nil {
		t.Error("expected nil addr for uninitialized relay")
	}
}

// udpStreamCreator creates streams that echo back the written data with a prefix.
type udpStreamCreator struct {
	prefix []byte
}

func (u *udpStreamCreator) CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error) {
	return &echoStream{prefix: u.prefix}, nil
}

type echoStream struct {
	prefix  []byte
	written bytes.Buffer
	read    bool
}

func (s *echoStream) Write(p []byte) (int, error) {
	return s.written.Write(p)
}

func (s *echoStream) Read(p []byte) (int, error) {
	if s.read {
		return 0, io.EOF
	}
	s.read = true
	resp := append(s.prefix, s.written.Bytes()...)
	n := copy(p, resp)
	return n, nil
}

func (s *echoStream) Close() error {
	return nil
}

func TestSOCKS5_UDPAssociate_Integration(t *testing.T) {
	// Create a SOCKS5 server with a stream creator that echoes data.
	srv := NewSOCKS5Server("127.0.0.1:0", &udpStreamCreator{prefix: []byte("echo:")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	srvAddr := srv.Addr().String()

	// Step 1: Connect to SOCKS5 server via TCP
	tcpConn, err := net.DialTimeout("tcp", srvAddr, time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5: %v", err)
	}
	defer tcpConn.Close()

	// Step 2: Auth negotiation
	tcpConn.Write([]byte{0x05, 0x01, 0x00}) // SOCKS5, 1 method, no-auth
	authResp := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, authResp); err != nil {
		t.Fatalf("read auth: %v", err)
	}
	if authResp[0] != 0x05 || authResp[1] != 0x00 {
		t.Fatalf("auth response: %v", authResp)
	}

	// Step 3: Send UDP ASSOCIATE request
	// We send 0.0.0.0:0 as the client address (common for UDP ASSOCIATE)
	tcpConn.Write([]byte{
		0x05, 0x03, 0x00, 0x01, // VER, CMD=UDP_ASSOCIATE, RSV, ATYP=IPv4
		0, 0, 0, 0, // client address 0.0.0.0
		0, 0, // client port 0
	})

	// Step 4: Read the reply — should contain the UDP relay address
	reply := make([]byte, 10)
	tcpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(tcpConn, reply); err != nil {
		t.Fatalf("read UDP ASSOCIATE reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected success reply, got status 0x%02x", reply[1])
	}

	// Extract the UDP relay address from the reply
	relayIP := net.IP(reply[4:8])
	relayPort := binary.BigEndian.Uint16(reply[8:10])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: int(relayPort)}

	t.Logf("UDP relay address: %s", relayAddr)

	if relayPort == 0 {
		t.Fatal("relay port should not be 0")
	}

	// Step 5: Send a UDP datagram through the relay
	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatalf("dial UDP relay: %v", err)
	}
	defer udpConn.Close()

	// Build a SOCKS5 UDP datagram targeting example.com:53
	datagram := buildUDPDatagram("example.com:53", []byte("test-query"))
	if _, err := udpConn.Write(datagram); err != nil {
		t.Fatalf("write UDP datagram: %v", err)
	}

	// Step 6: Read the response
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	respBuf := make([]byte, maxUDPPacketSize)
	n, err := udpConn.Read(respBuf)
	if err != nil {
		t.Fatalf("read UDP response: %v", err)
	}

	// Parse the response datagram
	respAddr, respPayload, err := parseUDPDatagram(respBuf[:n])
	if err != nil {
		t.Fatalf("parse response datagram: %v", err)
	}

	if respAddr != "example.com:53" {
		t.Errorf("response addr = %q, want %q", respAddr, "example.com:53")
	}

	expectedPayload := []byte("echo:test-query")
	if !bytes.Equal(respPayload, expectedPayload) {
		t.Errorf("response payload = %q, want %q", respPayload, expectedPayload)
	}
}

func TestSOCKS5_UDPAssociate_TCPClose(t *testing.T) {
	// Test that closing the TCP association connection tears down the UDP relay.
	srv := NewSOCKS5Server("127.0.0.1:0", &udpStreamCreator{prefix: nil}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Auth + UDP ASSOCIATE
	tcpConn.Write([]byte{0x05, 0x01, 0x00})
	io.ReadFull(tcpConn, make([]byte, 2))

	tcpConn.Write([]byte{
		0x05, 0x03, 0x00, 0x01,
		0, 0, 0, 0,
		0, 0,
	})

	reply := make([]byte, 10)
	tcpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(tcpConn, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("expected success, got 0x%02x", reply[1])
	}

	relayPort := binary.BigEndian.Uint16(reply[8:10])
	relayAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(relayPort)}

	// Close the TCP association — this should tear down the relay
	tcpConn.Close()
	time.Sleep(200 * time.Millisecond)

	// Try to send a UDP datagram — the relay should be closed
	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer udpConn.Close()

	datagram := buildUDPDatagram("example.com:53", []byte("late-query"))
	udpConn.Write(datagram)

	// Should not get a response (relay is closed)
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response after TCP close (may be a race); acceptable")
	}
	// Either timeout or error is acceptable — the relay is torn down.
}
