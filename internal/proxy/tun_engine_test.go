package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// mockTUNDevice is a controllable TUNDevice for testing.
type mockTUNDevice struct {
	name     string
	mtu      int
	readBuf  chan []byte   // packets to return from Read
	written  [][]byte     // packets received by Write
	mu       sync.Mutex
	closed   bool
	readErr  error        // if set, Read returns this error
}

func newMockTUNDevice(name string, mtu int) *mockTUNDevice {
	return &mockTUNDevice{
		name:    name,
		mtu:     mtu,
		readBuf: make(chan []byte, 100),
	}
}

func (m *mockTUNDevice) Name() string { return m.name }
func (m *mockTUNDevice) MTU() int     { return m.mtu }

func (m *mockTUNDevice) Read(buf []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	pkt, ok := <-m.readBuf
	if !ok {
		return 0, io.EOF
	}
	n := copy(buf, pkt)
	return n, nil
}

func (m *mockTUNDevice) Write(buf []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.written = append(m.written, cp)
	return len(buf), nil
}

func (m *mockTUNDevice) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.readBuf)
	}
	return nil
}

func (m *mockTUNDevice) getWritten() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]byte, len(m.written))
	copy(result, m.written)
	return result
}

// controllableStreamCreator allows controlling stream behavior per-destination.
type controllableStreamCreator struct {
	streams map[string]*controllableStream
	err     error
	mu      sync.Mutex
}

type controllableStream struct {
	readData  bytes.Buffer
	writeData bytes.Buffer
	closed    bool
	mu        sync.Mutex
}

func (s *controllableStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readData.Len() == 0 {
		return 0, io.EOF
	}
	return s.readData.Read(p)
}

func (s *controllableStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeData.Write(p)
}

func (s *controllableStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func newControllableStreamCreator() *controllableStreamCreator {
	return &controllableStreamCreator{
		streams: make(map[string]*controllableStream),
	}
}

func (c *controllableStreamCreator) CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	s := &controllableStream{}
	c.streams[destination] = s
	return s, nil
}

// buildIPv4Packet constructs a minimal IPv4 packet with the given parameters.
func buildIPv4Packet(proto byte, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	ihl := 20
	var transportHeader []byte
	if proto == ipProtoTCP || proto == ipProtoUDP {
		transportHeader = make([]byte, 8)
		binary.BigEndian.PutUint16(transportHeader[0:2], srcPort)
		binary.BigEndian.PutUint16(transportHeader[2:4], dstPort)
		if proto == ipProtoUDP {
			udpLen := 8 + len(payload)
			binary.BigEndian.PutUint16(transportHeader[4:6], uint16(udpLen))
		}
	}

	totalLen := ihl + len(transportHeader) + len(payload)
	pkt := make([]byte, totalLen)

	// IPv4 header
	pkt[0] = 0x45 // version=4, IHL=5 (20 bytes)
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[8] = 64    // TTL
	pkt[9] = proto // protocol

	copy(pkt[12:16], srcIP.To4())
	copy(pkt[16:20], dstIP.To4())

	// Transport header
	copy(pkt[ihl:], transportHeader)

	// Payload
	copy(pkt[ihl+len(transportHeader):], payload)

	return pkt
}

// buildIPv6Packet constructs a minimal IPv6 packet with the given parameters.
func buildIPv6Packet(nextHeader byte, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	ipHeaderLen := 40
	var transportHeader []byte
	if nextHeader == ipProtoTCP || nextHeader == ipProtoUDP {
		transportHeader = make([]byte, 8)
		binary.BigEndian.PutUint16(transportHeader[0:2], srcPort)
		binary.BigEndian.PutUint16(transportHeader[2:4], dstPort)
	}

	totalLen := ipHeaderLen + len(transportHeader) + len(payload)
	pkt := make([]byte, totalLen)

	// IPv6 header
	pkt[0] = 0x60 // version=6
	pkt[6] = nextHeader
	pkt[7] = 64 // hop limit

	payloadLen := len(transportHeader) + len(payload)
	binary.BigEndian.PutUint16(pkt[4:6], uint16(payloadLen))

	copy(pkt[8:24], srcIP.To16())
	copy(pkt[24:40], dstIP.To16())

	// Transport header
	copy(pkt[ipHeaderLen:], transportHeader)

	// Payload
	copy(pkt[ipHeaderLen+len(transportHeader):], payload)

	return pkt
}

func TestTUNEngine_ProcessPacket_IPv4_TCP_Tunnel(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter([]RoutingRule{
		{Domain: "*", Action: ActionTunnel},
	}, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	srcIP := net.ParseIP("10.0.0.1").To4()
	dstIP := net.ParseIP("93.184.216.34").To4()
	pkt := buildIPv4Packet(ipProtoTCP, srcIP, dstIP, 12345, 443, []byte("hello"))

	ctx := context.Background()
	err = engine.processPacket(ctx, pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// Give the tunnel goroutine a moment to finish
	time.Sleep(50 * time.Millisecond)

	dest := net.JoinHostPort(dstIP.String(), "443")
	streams.mu.Lock()
	stream, ok := streams.streams[dest]
	streams.mu.Unlock()
	if !ok {
		t.Fatalf("expected stream for %s", dest)
	}

	stream.mu.Lock()
	written := stream.writeData.Bytes()
	stream.mu.Unlock()

	if !bytes.Equal(written, pkt) {
		t.Errorf("expected packet to be written to stream, got %d bytes vs %d bytes", len(written), len(pkt))
	}
}

func TestTUNEngine_ProcessPacket_IPv4_Direct(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect) // default direct

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("GET /"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// No stream should be created for direct traffic
	streams.mu.Lock()
	count := len(streams.streams)
	streams.mu.Unlock()
	if count != 0 {
		t.Error("no stream should be created for direct traffic")
	}
}

func TestTUNEngine_ProcessPacket_IPv4_Block(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionBlock)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("blocked"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	streams.mu.Lock()
	count := len(streams.streams)
	streams.mu.Unlock()
	if count != 0 {
		t.Error("no stream should be created for blocked traffic")
	}
}

func TestTUNEngine_ProcessPacket_IPv4_KillSwitch(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel) // default: tunnel
	ks := NewKillSwitch(router)
	ks.Enable()
	ks.Activate()

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:     dev,
		Router:     router,
		Streams:    streams,
		KillSwitch: ks,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("93.184.216.34").To4(), 12345, 443, []byte("data"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// Kill switch should have blocked the packet, no stream created
	streams.mu.Lock()
	count := len(streams.streams)
	streams.mu.Unlock()
	if count != 0 {
		t.Error("kill switch should block tunneled traffic")
	}
}

func TestTUNEngine_ProcessPacket_IPv6_TCP(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	srcIP := net.ParseIP("fd00::1")
	dstIP := net.ParseIP("2001:db8::1")
	pkt := buildIPv6Packet(ipProtoTCP, srcIP, dstIP, 12345, 443, []byte("v6data"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	dest := net.JoinHostPort(dstIP.String(), "443")
	streams.mu.Lock()
	_, ok := streams.streams[dest]
	streams.mu.Unlock()
	if !ok {
		t.Fatalf("expected stream for %s", dest)
	}
}

func TestTUNEngine_ProcessPacket_IPv6_UDP(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	srcIP := net.ParseIP("fd00::1")
	dstIP := net.ParseIP("2001:db8::2")
	pkt := buildIPv6Packet(ipProtoUDP, srcIP, dstIP, 54321, 8080, []byte("udpv6"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	dest := net.JoinHostPort(dstIP.String(), strconv.Itoa(8080))
	streams.mu.Lock()
	_, ok := streams.streams[dest]
	streams.mu.Unlock()
	if !ok {
		t.Fatalf("expected stream for %s", dest)
	}
}

func TestTUNEngine_ProcessPacket_EmptyPacket(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Empty packet should be no-op
	err = engine.processPacket(context.Background(), nil)
	if err != nil {
		t.Fatalf("processPacket on empty: %v", err)
	}

	err = engine.processPacket(context.Background(), []byte{})
	if err != nil {
		t.Fatalf("processPacket on empty slice: %v", err)
	}
}

func TestTUNEngine_ProcessPacket_UnsupportedVersion(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// IP version 3 (unsupported)
	pkt := []byte{0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error for unsupported IP version")
	}
}

func TestTUNEngine_ProcessPacket_IPv4_TooShort(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// IPv4 header less than 20 bytes
	pkt := []byte{0x45, 0, 0, 10} // version=4, IHL=5, but only 4 bytes
	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error for too-short IPv4 packet")
	}
}

func TestTUNEngine_ProcessPacket_IPv4_InvalidIHL(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// IHL = 1 (4 bytes, less than minimum 20)
	pkt := make([]byte, 20)
	pkt[0] = 0x41 // version=4, IHL=1
	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error for invalid IHL")
	}
}

func TestTUNEngine_ProcessPacket_IPv6_TooShort(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// IPv6 packet less than 40 bytes
	pkt := make([]byte, 20)
	pkt[0] = 0x60 // version=6
	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error for too-short IPv6 packet")
	}
}

func TestTUNEngine_ProcessPacket_IPv4_NoTransportPort(t *testing.T) {
	// Test when protocol is neither TCP nor UDP (e.g., ICMP)
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	// ICMP (protocol 1) packet — no port extraction
	pkt := make([]byte, 28)
	pkt[0] = 0x45 // version=4, IHL=5
	binary.BigEndian.PutUint16(pkt[2:4], uint16(28))
	pkt[9] = 1 // ICMP protocol
	copy(pkt[12:16], net.ParseIP("10.0.0.1").To4())
	copy(pkt[16:20], net.ParseIP("10.0.0.2").To4())

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}
}

func TestTUNEngine_ProcessPacket_IPv6_NoTransportPort(t *testing.T) {
	// Test IPv6 with non-TCP/UDP next header (e.g., ICMPv6 = 58)
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := make([]byte, 48)
	pkt[0] = 0x60 // version=6
	pkt[6] = 58   // ICMPv6
	binary.BigEndian.PutUint16(pkt[4:6], 8) // payload length
	copy(pkt[8:24], net.ParseIP("fd00::1").To16())
	copy(pkt[24:40], net.ParseIP("fd00::2").To16())

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}
}

func TestTUNEngine_ProcessPacket_StreamCreationFailure(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	streams.err = errors.New("stream creation failed")
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("data"))

	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error when stream creation fails")
	}
}

func TestTUNEngine_ProcessPacket_TunnelPacket_WriteResponseToTUN(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)

	// Build a stream that returns response data
	respData := []byte("response-from-tunnel")
	streams := &mockStreamCreator{data: respData}
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("93.184.216.34").To4(), 12345, 443, []byte("request"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// Give the read goroutine time to process
	time.Sleep(100 * time.Millisecond)

	written := dev.getWritten()
	if len(written) == 0 {
		t.Error("expected response data to be written to TUN device")
	}
}

func TestTUNEngine_Run_ContextCancel(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Send one packet through
	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, nil)
	dev.readBuf <- pkt

	time.Sleep(50 * time.Millisecond)
	cancel()
	dev.Close() // unblock Read

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestTUNEngine_Run_ZeroMTU(t *testing.T) {
	// Device with zero MTU should use DefaultMTU
	dev := newMockTUNDevice("test0", 0)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	dev.Close() // unblock Read

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestTUNEngine_Run_ReadError(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Close the device to cause a read error
	dev.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// The Run loop should encounter read errors; cancel to stop
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-errCh:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}

func TestTUNEngine_Run_ZeroLengthRead(t *testing.T) {
	// Test that a zero-length read is skipped.
	// We use a custom device that returns 0, nil.
	dev := &zeroReadTUNDevice{name: "test0", mtu: 1500}
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = engine.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

type zeroReadTUNDevice struct {
	name    string
	mtu     int
	count   int
	mu      sync.Mutex
}

func (z *zeroReadTUNDevice) Name() string { return z.name }
func (z *zeroReadTUNDevice) MTU() int     { return z.mtu }
func (z *zeroReadTUNDevice) Read(buf []byte) (int, error) {
	z.mu.Lock()
	z.count++
	c := z.count
	z.mu.Unlock()
	// First few reads return 0 bytes, then block
	if c <= 3 {
		return 0, nil
	}
	// Block until context cancelled
	time.Sleep(time.Second)
	return 0, errors.New("timeout")
}
func (z *zeroReadTUNDevice) Write(buf []byte) (int, error) { return len(buf), nil }
func (z *zeroReadTUNDevice) Close() error                  { return nil }

func TestTUNEngine_HandleDNS_IPv4(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)
	dnsInterceptor := NewDNSInterceptor(router, "8.8.8.8:53", "192.168.1.1:53")

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		DNS:     dnsInterceptor,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build a DNS query packet (A record for example.com)
	dnsQuery, _, _ := buildDNSQuery("example.com", dnsTypeA)

	// Build an IPv4/UDP packet to port 53 carrying the DNS query.
	// We need a proper UDP header (8 bytes) before the DNS payload.
	srcIP := net.ParseIP("10.0.0.1").To4()
	dstIP := net.ParseIP("8.8.8.8").To4()

	ihl := 20
	udpHeaderLen := 8
	totalLen := ihl + udpHeaderLen + len(dnsQuery)
	pkt := make([]byte, totalLen)

	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[9] = ipProtoUDP
	copy(pkt[12:16], srcIP)
	copy(pkt[16:20], dstIP)

	// UDP header
	binary.BigEndian.PutUint16(pkt[ihl:ihl+2], 54321) // src port
	binary.BigEndian.PutUint16(pkt[ihl+2:ihl+4], 53)  // dst port
	binary.BigEndian.PutUint16(pkt[ihl+4:ihl+6], uint16(udpHeaderLen+len(dnsQuery)))

	copy(pkt[ihl+udpHeaderLen:], dnsQuery)

	// This will call handleDNS, which calls dns.HandleDNSPacket
	// The DNS resolution over the network will fail, but the code path
	// for DNS interception is exercised.
	_ = engine.processPacket(context.Background(), pkt)
}

func TestTUNEngine_HandleDNS_IPv6(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect) // direct => use system DNS
	dnsInterceptor := NewDNSInterceptor(router, "8.8.8.8:53", "192.168.1.1:53")

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		DNS:     dnsInterceptor,
	})
	if err != nil {
		t.Fatal(err)
	}

	dnsQuery, _, _ := buildDNSQuery("test.example.com", dnsTypeA)

	srcIP := net.ParseIP("fd00::1")
	dstIP := net.ParseIP("fd00::53")

	ipHeaderLen := 40
	udpHeaderLen := 8
	totalLen := ipHeaderLen + udpHeaderLen + len(dnsQuery)
	pkt := make([]byte, totalLen)

	pkt[0] = 0x60
	pkt[6] = ipProtoUDP
	pkt[7] = 64
	binary.BigEndian.PutUint16(pkt[4:6], uint16(udpHeaderLen+len(dnsQuery)))

	copy(pkt[8:24], srcIP.To16())
	copy(pkt[24:40], dstIP.To16())

	binary.BigEndian.PutUint16(pkt[ipHeaderLen:ipHeaderLen+2], 54321) // src port
	binary.BigEndian.PutUint16(pkt[ipHeaderLen+2:ipHeaderLen+4], 53) // dst port
	binary.BigEndian.PutUint16(pkt[ipHeaderLen+4:ipHeaderLen+6], uint16(udpHeaderLen+len(dnsQuery)))

	copy(pkt[ipHeaderLen+udpHeaderLen:], dnsQuery)

	_ = engine.processPacket(context.Background(), pkt)
}

func TestTUNEngine_HandleDNS_ShortUDP(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)
	dnsInterceptor := NewDNSInterceptor(router, "8.8.8.8:53", "192.168.1.1:53")

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		DNS:     dnsInterceptor,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build an IPv4 packet to port 53 but with a very short payload
	// that makes the UDP header too short
	pkt := make([]byte, 24) // only 4 bytes after 20-byte IP header
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 24)
	pkt[9] = ipProtoUDP
	copy(pkt[12:16], net.ParseIP("10.0.0.1").To4())
	copy(pkt[16:20], net.ParseIP("8.8.8.8").To4())
	binary.BigEndian.PutUint16(pkt[22:24], 53) // dst port in transport header

	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error for short UDP packet")
	}
}

func TestTUNEngine_NewTUNEngine_NilLogger(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	router := NewRouter(nil, ActionDirect)
	streams := newControllableStreamCreator()

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		Logger:  nil, // should default
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestTUNEngine_RoutePacket_UnknownAction(t *testing.T) {
	// Test with a router that returns an unusual action
	// Since routePacket checks kill switch then calls router.Decide,
	// and the Router only returns known actions, we test the kill switch
	// not blocking when it is nil
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect) // direct

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		// No kill switch
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("test"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}
}

func TestTUNEngine_TunnelPacket_StreamWriteError(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := &writeErrorStreamCreator{}
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("data"))

	err = engine.processPacket(context.Background(), pkt)
	if err == nil {
		t.Error("expected error when stream write fails")
	}
}

// writeErrorStreamCreator creates streams that fail on Write.
type writeErrorStreamCreator struct{}

func (w *writeErrorStreamCreator) CreateStream(ctx context.Context, destination string) (io.ReadWriteCloser, error) {
	return &writeErrorStream{}, nil
}

type writeErrorStream struct{}

func (s *writeErrorStream) Read(p []byte) (int, error)  { return 0, io.EOF }
func (s *writeErrorStream) Write(p []byte) (int, error) { return 0, errors.New("write failed") }
func (s *writeErrorStream) Close() error                { return nil }

func TestTUNEngine_Run_ProcessPacketError(t *testing.T) {
	// Run with a device that sends invalid packets — exercises the
	// "packet processing error" debug log path
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Send invalid packet (unsupported IP version)
	dev.readBuf <- []byte{0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	time.Sleep(50 * time.Millisecond)
	cancel()
	dev.Close()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestTUNEngine_HandleDNS_NilDNSInterceptor(t *testing.T) {
	// When DNS interceptor is nil, DNS packets should be treated as regular packets
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect) // direct

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
		DNS:     nil, // no DNS interceptor
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build IPv4 UDP to port 53
	pkt := buildIPv4Packet(ipProtoUDP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 54321, 53, []byte("dns-query"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}
}

func TestTUNEngine_TunnelPacket_DeviceWriteError(t *testing.T) {
	// Create a TUN device that fails on Write
	dev := &writeFailTUNDevice{name: "test0", mtu: 1500, readBuf: make(chan []byte, 10)}

	streams := &mockStreamCreator{data: []byte("response-data")}
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoTCP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 80, []byte("request"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// Give the background goroutine time to attempt writing to TUN
	time.Sleep(100 * time.Millisecond)
}

type writeFailTUNDevice struct {
	name    string
	mtu     int
	readBuf chan []byte
}

func (d *writeFailTUNDevice) Name() string { return d.name }
func (d *writeFailTUNDevice) MTU() int     { return d.mtu }
func (d *writeFailTUNDevice) Read(buf []byte) (int, error) {
	pkt, ok := <-d.readBuf
	if !ok {
		return 0, io.EOF
	}
	return copy(buf, pkt), nil
}
func (d *writeFailTUNDevice) Write(buf []byte) (int, error) {
	return 0, errors.New("TUN write failed")
}
func (d *writeFailTUNDevice) Close() error {
	close(d.readBuf)
	return nil
}

func TestTUNEngine_IPv4_UDP_NonDNS(t *testing.T) {
	// IPv4 UDP to a port other than 53 (non-DNS)
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionTunnel)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv4Packet(ipProtoUDP, net.ParseIP("10.0.0.1").To4(),
		net.ParseIP("8.8.8.8").To4(), 12345, 8080, []byte("udp-data"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	dest := net.JoinHostPort("8.8.8.8", "8080")
	streams.mu.Lock()
	_, ok := streams.streams[dest]
	streams.mu.Unlock()
	if !ok {
		t.Fatalf("expected stream for %s", dest)
	}
}

func TestTUNEngine_IPv6_Direct(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv6Packet(ipProtoTCP, net.ParseIP("fd00::1"), net.ParseIP("fd00::2"), 12345, 80, []byte("data"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	// No stream should be created for direct traffic
	streams.mu.Lock()
	count := len(streams.streams)
	streams.mu.Unlock()
	if count != 0 {
		t.Error("no stream should be created for direct IPv6 traffic")
	}
}

func TestTUNEngine_Run_ReadErrorAfterCancel(t *testing.T) {
	// Test the path where ctx is cancelled while Read blocks, then Read returns error.
	// The Run loop should detect ctx.Done and return ctx.Err.
	dev := &delayedErrorTUNDevice{
		name:   "test0",
		mtu:    1500,
		errCh:  make(chan struct{}),
	}
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionDirect)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Let the engine start and block on Read
	time.Sleep(50 * time.Millisecond)

	// Cancel context first, then unblock Read with an error
	cancel()
	close(dev.errCh)

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

type delayedErrorTUNDevice struct {
	name  string
	mtu   int
	errCh chan struct{}
}

func (d *delayedErrorTUNDevice) Name() string { return d.name }
func (d *delayedErrorTUNDevice) MTU() int     { return d.mtu }
func (d *delayedErrorTUNDevice) Read(buf []byte) (int, error) {
	<-d.errCh // block until signalled
	return 0, errors.New("device closed")
}
func (d *delayedErrorTUNDevice) Write(buf []byte) (int, error) { return len(buf), nil }
func (d *delayedErrorTUNDevice) Close() error                  { return nil }

func TestTUNEngine_IPv6_Block(t *testing.T) {
	dev := newMockTUNDevice("test0", 1500)
	streams := newControllableStreamCreator()
	router := NewRouter(nil, ActionBlock)

	engine, err := NewTUNEngine(TUNEngineConfig{
		Device:  dev,
		Router:  router,
		Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}

	pkt := buildIPv6Packet(ipProtoTCP, net.ParseIP("fd00::1"), net.ParseIP("fd00::2"), 12345, 80, []byte("blocked"))

	err = engine.processPacket(context.Background(), pkt)
	if err != nil {
		t.Fatalf("processPacket: %v", err)
	}

	streams.mu.Lock()
	count := len(streams.streams)
	streams.mu.Unlock()
	if count != 0 {
		t.Error("no stream should be created for blocked IPv6 traffic")
	}
}
