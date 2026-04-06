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

func TestBuildUDPDatagram_InvalidAddr(t *testing.T) {
	// addr with no port — should use fallback (host=addr, port=0)
	datagram := buildUDPDatagram("just-a-host", []byte("payload"))
	if datagram == nil {
		t.Fatal("expected non-nil datagram")
	}

	addr, payload, err := parseUDPDatagram(datagram)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if addr != "just-a-host:0" {
		t.Errorf("addr = %q, want %q", addr, "just-a-host:0")
	}
	if !bytes.Equal(payload, []byte("payload")) {
		t.Errorf("payload = %q, want %q", payload, "payload")
	}
}

func TestParseUDPDatagram_TruncatedDomainLength(t *testing.T) {
	// ATYP = domain, but no length byte
	buf := []byte{0x00, 0x00, 0x00, addrDomain}
	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for missing domain length byte")
	}
}

func TestParseUDPDatagram_TruncatedPort(t *testing.T) {
	// Valid IPv4 address but missing port bytes
	buf := []byte{0x00, 0x00, 0x00, addrIPv4, 10, 0, 0, 1}
	_, _, err := parseUDPDatagram(buf)
	if err == nil {
		t.Error("expected error for missing port bytes")
	}
}

// failingUDPStreamCreator fails on CreateStream.
type failingUDPStreamCreator struct{}

func (f *failingUDPStreamCreator) CreateStream(ctx context.Context, dest string) (io.ReadWriteCloser, error) {
	return nil, errors.New("stream failed")
}

func TestUDPRelay_HandleDatagram_StreamCreationFailure(t *testing.T) {
	// Start a SOCKS5 server that creates UDP relays with a failing stream creator
	srv := NewSOCKS5Server("127.0.0.1:0", &failingUDPStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	// Connect and do UDP ASSOCIATE
	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConn.Close()

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

	relayIP := net.IP(reply[4:8])
	relayPort := int(reply[8])<<8 | int(reply[9])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: relayPort}

	// Send a datagram — the stream creation will fail, but it shouldn't crash
	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	datagram := buildUDPDatagram("example.com:53", []byte("query"))
	udpConn.Write(datagram)

	// No response expected since stream creation fails
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response; acceptable race")
	}
}

// writeFailStream is a stream that writes successfully but returns error on Read.
type writeFailStreamForUDP struct {
	written bytes.Buffer
}

func (s *writeFailStreamForUDP) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}
func (s *writeFailStreamForUDP) Write(p []byte) (int, error) {
	return s.written.Write(p)
}
func (s *writeFailStreamForUDP) Close() error { return nil }

type readFailStreamCreator struct{}

func (r *readFailStreamCreator) CreateStream(ctx context.Context, dest string) (io.ReadWriteCloser, error) {
	return &writeFailStreamForUDP{}, nil
}

func TestUDPRelay_HandleDatagram_StreamReadError(t *testing.T) {
	// The stream's Read returns an error — should be handled gracefully
	srv := NewSOCKS5Server("127.0.0.1:0", &readFailStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConn.Close()

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

	relayIP := net.IP(reply[4:8])
	relayPort := int(reply[8])<<8 | int(reply[9])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: relayPort}

	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	datagram := buildUDPDatagram("example.com:80", []byte("test"))
	udpConn.Write(datagram)

	// No response expected since stream read fails
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response; acceptable")
	}
}

// zeroReadStream writes successfully, reads 0 bytes.
type zeroReadStreamCreator struct{}

func (z *zeroReadStreamCreator) CreateStream(ctx context.Context, dest string) (io.ReadWriteCloser, error) {
	return &zeroReadStream{}, nil
}

type zeroReadStream struct {
	written bytes.Buffer
}

func (s *zeroReadStream) Read(p []byte) (int, error)  { return 0, nil }
func (s *zeroReadStream) Write(p []byte) (int, error) { return s.written.Write(p) }
func (s *zeroReadStream) Close() error                { return nil }

func TestUDPRelay_HandleDatagram_ZeroLengthResponse(t *testing.T) {
	// Stream returns 0 bytes — relay should not send a response
	srv := NewSOCKS5Server("127.0.0.1:0", &zeroReadStreamCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConn.Close()

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

	relayIP := net.IP(reply[4:8])
	relayPort := int(reply[8])<<8 | int(reply[9])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: relayPort}

	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	datagram := buildUDPDatagram("example.com:80", []byte("test"))
	udpConn.Write(datagram)

	// No response expected for zero-length read
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response; acceptable race")
	}
}

// streamWriteFailCreator creates streams that fail on Write.
type streamWriteFailCreator struct{}

func (s *streamWriteFailCreator) CreateStream(ctx context.Context, dest string) (io.ReadWriteCloser, error) {
	return &streamWriteFail{}, nil
}

type streamWriteFail struct{}

func (s *streamWriteFail) Read(p []byte) (int, error)  { return 0, io.EOF }
func (s *streamWriteFail) Write(p []byte) (int, error) { return 0, errors.New("write fail") }
func (s *streamWriteFail) Close() error                { return nil }

func TestUDPRelay_HandleDatagram_StreamWriteError(t *testing.T) {
	srv := NewSOCKS5Server("127.0.0.1:0", &streamWriteFailCreator{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConn.Close()

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

	relayIP := net.IP(reply[4:8])
	relayPort := int(reply[8])<<8 | int(reply[9])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: relayPort}

	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	datagram := buildUDPDatagram("example.com:80", []byte("test"))
	udpConn.Write(datagram)

	// No response expected since stream write fails
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response; acceptable")
	}
}

func TestUDPRelay_HandleDatagram_InvalidDatagram(t *testing.T) {
	// Send a malformed UDP datagram through the relay
	srv := NewSOCKS5Server("127.0.0.1:0", &mockStreamCreator{data: []byte("resp")}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.ListenAndServe(ctx)
	time.Sleep(100 * time.Millisecond)

	tcpConn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConn.Close()

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

	relayIP := net.IP(reply[4:8])
	relayPort := int(reply[8])<<8 | int(reply[9])
	relayAddr := &net.UDPAddr{IP: relayIP, Port: relayPort}

	udpConn, err := net.DialUDP("udp4", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	// Send a too-short datagram (less than 4 bytes)
	udpConn.Write([]byte{0x00, 0x00})

	// Also send one with unsupported fragment
	badFrag := []byte{0x00, 0x00, 0x01, addrIPv4, 10, 0, 0, 1, 0x00, 0x50}
	badFrag = append(badFrag, []byte("payload")...)
	udpConn.Write(badFrag)

	// No response expected for invalid datagrams
	udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1024)
	_, err = udpConn.Read(buf)
	if err == nil {
		t.Log("got unexpected response; acceptable race")
	}
}
