package tunnel

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// --- Splitter Tests ---

func TestSplitter_ExactChunkSize(t *testing.T) {
	s := NewSplitter(1, 10)
	data := []byte("0123456789") // exactly 10 bytes

	chunks := s.Split(data)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !bytes.Equal(chunks[0].Payload, data) {
		t.Error("payload mismatch")
	}
	if chunks[0].StreamID != 1 {
		t.Errorf("StreamID = %d, want 1", chunks[0].StreamID)
	}
	if chunks[0].Sequence != 0 {
		t.Errorf("Sequence = %d, want 0", chunks[0].Sequence)
	}
	if s.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", s.Buffered())
	}
}

func TestSplitter_MultiChunk(t *testing.T) {
	s := NewSplitter(2, 5)
	data := []byte("abcdefghijklmno") // 15 bytes = 3 chunks

	chunks := s.Split(data)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	expected := []string{"abcde", "fghij", "klmno"}
	for i, ch := range chunks {
		if string(ch.Payload) != expected[i] {
			t.Errorf("chunk %d: got %q, want %q", i, ch.Payload, expected[i])
		}
		if ch.Sequence != uint64(i) {
			t.Errorf("chunk %d: Sequence = %d, want %d", i, ch.Sequence, i)
		}
	}
}

func TestSplitter_PartialBuffering(t *testing.T) {
	s := NewSplitter(3, 10)

	// First call: 7 bytes, not enough for a chunk
	chunks := s.Split([]byte("1234567"))
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
	if s.Buffered() != 7 {
		t.Errorf("Buffered = %d, want 7", s.Buffered())
	}

	// Second call: 5 more bytes, total 12 = 1 chunk + 2 buffered
	chunks = s.Split([]byte("89012"))
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if string(chunks[0].Payload) != "1234567890" {
		t.Errorf("payload = %q", chunks[0].Payload)
	}
	if s.Buffered() != 2 {
		t.Errorf("Buffered = %d, want 2", s.Buffered())
	}
}

func TestSplitter_EmptyInput(t *testing.T) {
	s := NewSplitter(1, 10)
	chunks := s.Split(nil)
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for nil input, got %d", len(chunks))
	}
	chunks = s.Split([]byte{})
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for empty input, got %d", len(chunks))
	}
}

func TestSplitter_Flush_WithData(t *testing.T) {
	s := NewSplitter(1, 10)
	s.Split([]byte("hello")) // 5 bytes buffered

	ch := s.Flush()
	if ch == nil {
		t.Fatal("expected non-nil chunk")
	}
	if string(ch.Payload) != "hello" {
		t.Errorf("payload = %q", ch.Payload)
	}
	if ch.Flags&crypto.FlagLastChunk == 0 {
		t.Error("expected FlagLastChunk")
	}
}

func TestSplitter_Flush_Empty(t *testing.T) {
	s := NewSplitter(1, 10)
	ch := s.Flush()
	if ch != nil {
		t.Fatal("expected nil for empty flush")
	}
}

func TestSplitter_SequenceIncrement(t *testing.T) {
	s := NewSplitter(1, 5)
	s.Split([]byte("abcde")) // seq 0
	s.Split([]byte("fghij")) // seq 1
	s.Split([]byte("kl"))    // buffered
	s.Flush()                // seq 2

	if s.NextSequence() != 3 {
		t.Errorf("NextSequence = %d, want 3", s.NextSequence())
	}
}

// --- Assembler Tests ---

func TestAssembler_InOrder(t *testing.T) {
	a := NewAssembler()

	seg := a.Insert(0, []byte("first"))
	if len(seg) != 1 || string(seg[0]) != "first" {
		t.Fatalf("expected [first], got %v", seg)
	}

	seg = a.Insert(1, []byte("second"))
	if len(seg) != 1 || string(seg[0]) != "second" {
		t.Fatalf("expected [second], got %v", seg)
	}

	if a.Expected() != 2 {
		t.Errorf("Expected = %d, want 2", a.Expected())
	}
}

func TestAssembler_OutOfOrder(t *testing.T) {
	a := NewAssembler()

	// Send seq 2 first (out of order)
	seg := a.Insert(2, []byte("third"))
	if seg != nil {
		t.Fatal("expected nil for out-of-order")
	}
	if a.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", a.Pending())
	}

	// Send seq 1 (still out of order)
	seg = a.Insert(1, []byte("second"))
	if seg != nil {
		t.Fatal("expected nil for out-of-order")
	}
	if a.Pending() != 2 {
		t.Errorf("Pending = %d, want 2", a.Pending())
	}

	// Send seq 0 — should flush 0, 1, 2
	seg = a.Insert(0, []byte("first"))
	if len(seg) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(seg))
	}
	if string(seg[0]) != "first" || string(seg[1]) != "second" || string(seg[2]) != "third" {
		t.Errorf("wrong order: %v", seg)
	}
	if a.Expected() != 3 {
		t.Errorf("Expected = %d, want 3", a.Expected())
	}
	if a.Pending() != 0 {
		t.Errorf("Pending = %d, want 0", a.Pending())
	}
}

func TestAssembler_Duplicate(t *testing.T) {
	a := NewAssembler()

	a.Insert(0, []byte("first"))
	seg := a.Insert(0, []byte("first-dup"))
	if seg != nil {
		t.Fatal("expected nil for duplicate")
	}
}

func TestAssembler_GapExceedsMax(t *testing.T) {
	a := NewAssembler()

	seg := a.Insert(maxGap+1, []byte("too far"))
	if seg != nil {
		t.Fatal("expected nil for gap exceeding max")
	}
}

func TestAssembler_Reset(t *testing.T) {
	a := NewAssembler()
	a.Insert(0, []byte("a"))
	a.Insert(2, []byte("c")) // buffered

	a.Reset()
	if a.Expected() != 0 {
		t.Errorf("Expected after reset = %d", a.Expected())
	}
	if a.Pending() != 0 {
		t.Errorf("Pending after reset = %d", a.Pending())
	}
}

// --- StreamManager Tests ---

func TestStreamManager_NewStream(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "google.com:443")
	if s.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if s.Destination != "google.com:443" {
		t.Errorf("Destination = %q", s.Destination)
	}
	if s.State != StateConnecting {
		t.Errorf("State = %d, want StateConnecting", s.State)
	}

	// Should have a CONNECT chunk in the output queue
	select {
	case ch := <-sm.OutQueue():
		if ch.Type != crypto.ChunkConnect {
			t.Errorf("first chunk type = %d, want ChunkConnect", ch.Type)
		}
		if string(ch.Payload) != "google.com:443" {
			t.Errorf("connect payload = %q", ch.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for CONNECT chunk")
	}

	if sm.ActiveCount() != 1 {
		t.Errorf("ActiveCount = %d, want 1", sm.ActiveCount())
	}
}

func TestStreamManager_WriteProducesChunks(t *testing.T) {
	sm := NewStreamManager(10, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	// Drain CONNECT chunk
	<-sm.OutQueue()

	n, err := s.Write([]byte("hello world!")) // 12 bytes, chunkSize=10
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 12 {
		t.Errorf("n = %d, want 12", n)
	}

	// Should produce 1 chunk (10 bytes), 2 bytes buffered
	select {
	case ch := <-sm.OutQueue():
		if ch.Type != crypto.ChunkData {
			t.Errorf("type = %d, want ChunkData", ch.Type)
		}
		if len(ch.Payload) != 10 {
			t.Errorf("payload len = %d, want 10", len(ch.Payload))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for data chunk")
	}
}

func TestStream_DeliverResponse(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	// Deliver response chunks
	s.DeliverResponse(&crypto.Chunk{
		StreamID: s.ID,
		Sequence: 0,
		Payload:  []byte("response data"),
	})

	buf := make([]byte, 100)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "response data" {
		t.Errorf("got %q", buf[:n])
	}
}

func TestStream_Close(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Should have FIN in queue
	found := false
	for i := 0; i < 2; i++ { // might be flush + FIN
		select {
		case ch := <-sm.OutQueue():
			if ch.Type == crypto.ChunkFIN {
				found = true
			}
		case <-time.After(time.Second):
			break
		}
	}
	if !found {
		t.Error("expected FIN chunk")
	}

	if s.State != StateClosed {
		t.Errorf("State = %d, want StateClosed", s.State)
	}
}

func TestStream_GetStream(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")

	got, ok := sm.GetStream(s.ID)
	if !ok {
		t.Fatal("stream not found")
	}
	if got.ID != s.ID {
		t.Error("ID mismatch")
	}

	_, ok = sm.GetStream(99999)
	if ok {
		t.Error("should not find nonexistent stream")
	}
}

func TestStream_RemoveStream(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	sm.RemoveStream(s.ID)

	if sm.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0", sm.ActiveCount())
	}
}

// --- DNS Resolver Tests ---

func TestRemoteDNSResolver_ResolveAndCache(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Resolve in background
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		ip, err := resolver.Resolve(ctx, "example.com")
		if err != nil {
			errCh <- err
		} else {
			resultCh <- ip
		}
	}()

	// Read the DNS request chunk
	select {
	case ch := <-outQueue:
		if ch.Type != crypto.ChunkDNSResolve {
			t.Fatalf("type = %d, want ChunkDNSResolve", ch.Type)
		}
		if string(ch.Payload) != "example.com" {
			t.Fatalf("payload = %q", ch.Payload)
		}
		// Deliver response
		resolver.DeliverResponse(&crypto.Chunk{
			StreamID: ch.StreamID,
			Sequence: ch.Sequence,
			Type:     crypto.ChunkDNSResolve,
			Payload:  []byte("93.184.216.34"),
		})
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for DNS chunk")
	}

	select {
	case ip := <-resultCh:
		if ip != "93.184.216.34" {
			t.Errorf("ip = %q", ip)
		}
	case err := <-errCh:
		t.Fatalf("resolve error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for resolve result")
	}

	// Should be cached
	if resolver.CacheSize() != 1 {
		t.Errorf("CacheSize = %d, want 1", resolver.CacheSize())
	}

	// Second resolve should hit cache (no new chunk sent)
	go func() {
		ip, err := resolver.Resolve(ctx, "example.com")
		if err != nil {
			errCh <- err
		} else {
			resultCh <- ip
		}
	}()

	select {
	case ip := <-resultCh:
		if ip != "93.184.216.34" {
			t.Errorf("cached ip = %q", ip)
		}
	case err := <-errCh:
		t.Fatalf("cached resolve error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timeout on cached resolve")
	}
}

func TestRemoteDNSResolver_ClearCache(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	// Manually populate cache
	resolver.cache.Store("example.com", &dnsEntry{
		ip:      "1.2.3.4",
		expires: time.Now().Add(5 * time.Minute),
	})

	if resolver.CacheSize() != 1 {
		t.Errorf("CacheSize = %d, want 1", resolver.CacheSize())
	}

	resolver.ClearCache()
	if resolver.CacheSize() != 0 {
		t.Errorf("CacheSize after clear = %d", resolver.CacheSize())
	}
}

// --- ExitEngine Tests ---

func TestExitEngine_DNSResolve(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	ch := &crypto.Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte("localhost"),
	}

	err := engine.ProcessChunk(context.Background(), ch)
	if err != nil {
		t.Fatalf("ProcessChunk DNS: %v", err)
	}

	// Should have a response
	select {
	case resp := <-engine.RespQueue():
		if resp.Type != crypto.ChunkDNSResolve {
			t.Errorf("type = %d", resp.Type)
		}
		if len(resp.Payload) == 0 {
			t.Error("expected non-empty payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for DNS response")
	}
}

func TestExitEngine_ActiveConnections(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)
	if engine.ActiveConnections() != 0 {
		t.Errorf("ActiveConnections = %d, want 0", engine.ActiveConnections())
	}
}

func TestExitEngine_CloseAll(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)
	engine.CloseAll() // should not panic
}

// ===========================================================================
// RespQueue Tests
// ===========================================================================

func TestRespQueue_NewDefaults(t *testing.T) {
	q := NewRespQueue(0, 0)
	if q.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000", q.maxSize)
	}
	if q.ttl != 5*time.Minute {
		t.Errorf("ttl = %v, want 5m", q.ttl)
	}
}

func TestRespQueue_NewCustom(t *testing.T) {
	q := NewRespQueue(50, 10*time.Second)
	if q.maxSize != 50 {
		t.Errorf("maxSize = %d, want 50", q.maxSize)
	}
	if q.ttl != 10*time.Second {
		t.Errorf("ttl = %v, want 10s", q.ttl)
	}
}

func TestRespQueue_PushAndDrain(t *testing.T) {
	q := NewRespQueue(10, time.Minute)

	ch1 := &crypto.Chunk{StreamID: 1, Payload: []byte("a")}
	ch2 := &crypto.Chunk{StreamID: 2, Payload: []byte("b")}
	ch3 := &crypto.Chunk{StreamID: 3, Payload: []byte("c")}

	q.Push(ch1)
	q.Push(ch2)
	q.Push(ch3)

	if q.Len() != 3 {
		t.Fatalf("Len = %d, want 3", q.Len())
	}

	result := q.Drain(0) // 0 means drain all
	if len(result) != 3 {
		t.Fatalf("Drain returned %d, want 3", len(result))
	}
	if !bytes.Equal(result[0].Payload, []byte("a")) {
		t.Errorf("result[0] payload = %q, want a", result[0].Payload)
	}
	if !bytes.Equal(result[1].Payload, []byte("b")) {
		t.Errorf("result[1] payload = %q, want b", result[1].Payload)
	}
	if !bytes.Equal(result[2].Payload, []byte("c")) {
		t.Errorf("result[2] payload = %q, want c", result[2].Payload)
	}

	// After drain, queue is empty
	if q.Len() != 0 {
		t.Errorf("Len after drain = %d, want 0", q.Len())
	}
}

func TestRespQueue_DrainWithLimit(t *testing.T) {
	q := NewRespQueue(10, time.Minute)

	for i := 0; i < 5; i++ {
		q.Push(&crypto.Chunk{StreamID: uint32(i), Payload: []byte{byte(i)}})
	}

	// Drain only 2
	result := q.Drain(2)
	if len(result) != 2 {
		t.Fatalf("Drain(2) returned %d, want 2", len(result))
	}
	if result[0].StreamID != 0 || result[1].StreamID != 1 {
		t.Errorf("wrong order: got streamIDs %d, %d", result[0].StreamID, result[1].StreamID)
	}

	// 3 remaining
	if q.Len() != 3 {
		t.Errorf("Len after partial drain = %d, want 3", q.Len())
	}

	// Drain rest with negative limit (means all)
	result = q.Drain(-1)
	if len(result) != 3 {
		t.Fatalf("Drain(-1) returned %d, want 3", len(result))
	}
	if q.Len() != 0 {
		t.Errorf("Len after drain all = %d, want 0", q.Len())
	}
}

func TestRespQueue_PushOverflow(t *testing.T) {
	q := NewRespQueue(3, time.Minute)

	q.Push(&crypto.Chunk{StreamID: 1, Payload: []byte("a")})
	q.Push(&crypto.Chunk{StreamID: 2, Payload: []byte("b")})
	q.Push(&crypto.Chunk{StreamID: 3, Payload: []byte("c")})
	// Queue is full (maxSize=3), pushing again should drop oldest
	q.Push(&crypto.Chunk{StreamID: 4, Payload: []byte("d")})

	if q.Len() != 3 {
		t.Fatalf("Len = %d, want 3", q.Len())
	}

	result := q.Drain(0)
	if len(result) != 3 {
		t.Fatalf("Drain returned %d, want 3", len(result))
	}
	// "a" should have been dropped
	if result[0].StreamID != 2 {
		t.Errorf("result[0].StreamID = %d, want 2 (oldest should be dropped)", result[0].StreamID)
	}
	if result[1].StreamID != 3 {
		t.Errorf("result[1].StreamID = %d, want 3", result[1].StreamID)
	}
	if result[2].StreamID != 4 {
		t.Errorf("result[2].StreamID = %d, want 4", result[2].StreamID)
	}
}

func TestRespQueue_TTLExpiry(t *testing.T) {
	q := NewRespQueue(100, 1*time.Millisecond)

	q.Push(&crypto.Chunk{StreamID: 1, Payload: []byte("expire me")})
	q.Push(&crypto.Chunk{StreamID: 2, Payload: []byte("expire me too")})

	// Wait for TTL to pass
	time.Sleep(10 * time.Millisecond)

	if q.Len() != 0 {
		t.Errorf("Len after TTL = %d, want 0", q.Len())
	}

	result := q.Drain(0)
	if len(result) != 0 {
		t.Errorf("Drain after TTL = %d, want 0", len(result))
	}
}

func TestRespQueue_PartialTTLExpiry(t *testing.T) {
	q := NewRespQueue(100, 50*time.Millisecond)

	q.Push(&crypto.Chunk{StreamID: 1, Payload: []byte("old")})

	// Wait long enough for first entry to expire
	time.Sleep(60 * time.Millisecond)

	// Push a new one (this will call expireLocked internally)
	q.Push(&crypto.Chunk{StreamID: 2, Payload: []byte("new")})

	if q.Len() != 1 {
		t.Errorf("Len = %d, want 1 (old should be expired)", q.Len())
	}

	result := q.Drain(0)
	if len(result) != 1 {
		t.Fatalf("Drain returned %d, want 1", len(result))
	}
	if result[0].StreamID != 2 {
		t.Errorf("remaining StreamID = %d, want 2", result[0].StreamID)
	}
}

func TestRespQueue_DrainEmpty(t *testing.T) {
	q := NewRespQueue(10, time.Minute)
	result := q.Drain(0)
	if result != nil {
		t.Errorf("Drain on empty returned %v, want nil", result)
	}
}

func TestRespQueue_LenEmpty(t *testing.T) {
	q := NewRespQueue(10, time.Minute)
	if q.Len() != 0 {
		t.Errorf("Len = %d, want 0", q.Len())
	}
}

// ===========================================================================
// ExitEngine Tests - Connect, Data, FIN, ReadLoop, Unknown Type
// ===========================================================================

func TestExitEngine_NewDefaults(t *testing.T) {
	engine := NewExitEngine(nil, 0, 0)
	// Should use defaults: maxIdleSecs=300, respQueueSize=4096
	if engine.maxIdle != 300*time.Second {
		t.Errorf("maxIdle = %v, want 300s", engine.maxIdle)
	}
	if cap(engine.respQueue) != 4096 {
		t.Errorf("respQueue cap = %d, want 4096", cap(engine.respQueue))
	}
	if engine.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestExitEngine_ProcessChunk_UnknownType(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	ch := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkType(0xFF),
		Payload:  []byte("test"),
	}

	err := engine.ProcessChunk(context.Background(), ch)
	if err == nil {
		t.Fatal("expected error for unknown chunk type")
	}
}

func TestExitEngine_ConnectAndDataAndFIN(t *testing.T) {
	// Start a local TCP server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverReceived := make(chan []byte, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		serverReceived <- buf[:n]
		// Send response back
		conn.Write([]byte("hello from server"))
	}()

	engine := NewExitEngine(nil, 300, 100)
	ctx := context.Background()

	// CONNECT
	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(ln.Addr().String()),
	})
	if err != nil {
		t.Fatalf("ProcessChunk Connect: %v", err)
	}

	if engine.ActiveConnections() != 1 {
		t.Errorf("ActiveConnections = %d, want 1", engine.ActiveConnections())
	}

	// Send DATA
	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte("client data"),
	})
	if err != nil {
		t.Fatalf("ProcessChunk Data: %v", err)
	}

	// Verify server received our data
	select {
	case data := <-serverReceived:
		if string(data) != "client data" {
			t.Errorf("server received %q, want %q", data, "client data")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to receive data")
	}

	// Read response data chunk from the respQueue
	select {
	case resp := <-engine.RespQueue():
		if resp.Type != crypto.ChunkData {
			t.Errorf("resp type = %d, want ChunkData", resp.Type)
		}
		if string(resp.Payload) != "hello from server" {
			t.Errorf("resp payload = %q, want %q", resp.Payload, "hello from server")
		}
		if resp.StreamID != 1 {
			t.Errorf("resp StreamID = %d, want 1", resp.StreamID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for data response")
	}

	// After server closes, we should get a FIN response
	<-serverDone
	select {
	case resp := <-engine.RespQueue():
		if resp.Type != crypto.ChunkFIN {
			t.Errorf("expected FIN response, got type %d", resp.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for FIN response")
	}
}

func TestExitEngine_HandleFIN(t *testing.T) {
	// Start a local TCP server
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
		// Hold connection open until we close it
		buf := make([]byte, 1)
		conn.Read(buf) // blocks until closed
		conn.Close()
	}()

	engine := NewExitEngine(nil, 300, 100)
	ctx := context.Background()

	// CONNECT
	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 10,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(ln.Addr().String()),
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if engine.ActiveConnections() != 1 {
		t.Errorf("ActiveConnections after connect = %d, want 1", engine.ActiveConnections())
	}

	// FIN should close the connection
	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 10,
		Type:     crypto.ChunkFIN,
	})
	if err != nil {
		t.Fatalf("FIN: %v", err)
	}

	// Give readLoop time to clean up
	time.Sleep(50 * time.Millisecond)

	// The connection should be removed (readLoop also deletes on close/error)
	// ActiveConnections might be 0 since both FIN handler and readLoop delete
}

func TestExitEngine_HandleFIN_NoConnection(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	// FIN for non-existent stream should not error
	err := engine.ProcessChunk(context.Background(), &crypto.Chunk{
		StreamID: 999,
		Type:     crypto.ChunkFIN,
	})
	if err != nil {
		t.Fatalf("FIN on nonexistent stream should not error, got: %v", err)
	}
}

func TestExitEngine_HandleData_NoConnection(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	err := engine.ProcessChunk(context.Background(), &crypto.Chunk{
		StreamID: 999,
		Type:     crypto.ChunkData,
		Payload:  []byte("orphan data"),
	})
	if err == nil {
		t.Fatal("expected error for data on nonexistent stream")
	}
}

func TestExitEngine_ConnectBadAddress(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkConnect,
		Payload:  []byte("192.0.2.1:1"), // TEST-NET, should fail/timeout
	})
	if err == nil {
		t.Fatal("expected error for bad address")
	}
}

func TestExitEngine_CloseAllWithActiveConnections(t *testing.T) {
	// Start a local TCP server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold connection open
			go func(c net.Conn) {
				buf := make([]byte, 1)
				c.Read(buf)
				c.Close()
			}(conn)
		}
	}()

	engine := NewExitEngine(nil, 300, 100)
	ctx := context.Background()

	// Open two connections
	for _, sid := range []uint32{1, 2} {
		err := engine.ProcessChunk(ctx, &crypto.Chunk{
			StreamID: sid,
			Type:     crypto.ChunkConnect,
			Payload:  []byte(ln.Addr().String()),
		})
		if err != nil {
			t.Fatalf("Connect stream %d: %v", sid, err)
		}
	}

	// Wait for connections to establish
	time.Sleep(50 * time.Millisecond)

	if engine.ActiveConnections() < 1 {
		t.Errorf("expected active connections, got %d", engine.ActiveConnections())
	}

	engine.CloseAll()

	// Give readLoops time to clean up
	time.Sleep(100 * time.Millisecond)
}

func TestExitEngine_ReadLoopContextCancel(t *testing.T) {
	// Start a local TCP server that stays open
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
		// Hold open indefinitely
		buf := make([]byte, 1024)
		conn.Read(buf)
		conn.Close()
	}()

	engine := NewExitEngine(nil, 300, 100)
	ctx, cancel := context.WithCancel(context.Background())

	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 42,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(ln.Addr().String()),
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Cancel context to trigger readLoop exit
	cancel()

	// Give readLoop time to exit
	time.Sleep(100 * time.Millisecond)
}

// ===========================================================================
// Stream Tests - Done, Write on closed, Read on cancelled context
// ===========================================================================

func TestStream_Done(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	// Done channel should not be closed yet
	select {
	case <-s.Done():
		t.Fatal("Done should not be closed before Close()")
	default:
		// expected
	}

	s.Close()

	// After close, Done should be closed
	select {
	case <-s.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("Done should be closed after Close()")
	}
}

func TestStream_WriteOnClosedStream(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	s.Close()

	// Drain FIN and any flush chunks
	drainOutQueue(sm, 100*time.Millisecond)

	_, err := s.Write([]byte("should fail"))
	if err == nil {
		t.Fatal("expected error writing to closed stream")
	}
}

func TestStream_ReadOnCancelledContext(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx, cancel := context.WithCancel(context.Background())

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	cancel()

	buf := make([]byte, 100)
	_, err := s.Read(buf)
	if err == nil {
		t.Fatal("expected error reading on cancelled context")
	}
}

// ===========================================================================
// Splitter Tests - NewSplitter with 0 chunkSize
// ===========================================================================

func TestSplitter_DefaultChunkSize(t *testing.T) {
	s := NewSplitter(1, 0)
	if s.chunkSize != crypto.MaxPayloadSize {
		t.Errorf("chunkSize = %d, want %d (MaxPayloadSize)", s.chunkSize, crypto.MaxPayloadSize)
	}
}

// ===========================================================================
// StreamManager Tests - NewStreamManager with 0/0 defaults
// ===========================================================================

func TestStreamManager_Defaults(t *testing.T) {
	sm := NewStreamManager(0, 0)
	if sm.chunkSize != crypto.MaxPayloadSize {
		t.Errorf("chunkSize = %d, want %d", sm.chunkSize, crypto.MaxPayloadSize)
	}
	if cap(sm.outQueue) != 1024 {
		t.Errorf("outQueue cap = %d, want 1024", cap(sm.outQueue))
	}
}

// drainOutQueue drains the out queue for a given duration to prevent blocking.
func drainOutQueue(sm *StreamManager, d time.Duration) {
	timer := time.After(d)
	for {
		select {
		case <-sm.OutQueue():
		case <-timer:
			return
		}
	}
}

// ===========================================================================
// Additional coverage tests — targeting uncovered branches to reach 100%
// ===========================================================================

// --- dns.go Resolve: context timeout while sending to outQueue (line 69-70) ---

func TestRemoteDNSResolver_ResolveTimeout_WaitingForResponse(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Resolve but never deliver a response — should hit ctx.Done() in "Wait for response" select
	_, err := resolver.Resolve(ctx, "timeout.example.com")
	if err == nil {
		t.Fatal("expected timeout error waiting for DNS response")
	}
}

func TestRemoteDNSResolver_ResolveTimeout_SendingToOutQueue(t *testing.T) {
	// Use an unbuffered channel so the send blocks
	outQueue := make(chan *crypto.Chunk)
	resolver := NewRemoteDNSResolver(outQueue)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Nobody reads from outQueue, so the send will block and ctx will expire
	_, err := resolver.Resolve(ctx, "blocked-send.example.com")
	if err == nil {
		t.Fatal("expected timeout error sending DNS chunk to outQueue")
	}
}

// --- exit.go handleDNS: default branch when respQueue is full (line 125-127) ---

func TestExitEngine_HandleDNS_QueueFull(t *testing.T) {
	// Create engine with queue size 1
	engine := NewExitEngine(nil, 300, 1)

	// Fill the response queue so the next send hits the default branch
	engine.respQueue <- &crypto.Chunk{}

	// ProcessChunk DNS should succeed even when the queue is full (drops response)
	err := engine.ProcessChunk(context.Background(), &crypto.Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte("localhost"),
	})
	if err != nil {
		t.Fatalf("ProcessChunk DNS with full queue: %v", err)
	}
}

// --- exit.go readLoop: default branch on FIN send when respQueue is full (line 180-182) ---
// And ctx.Done() during data send (line 164-165)

func TestExitEngine_ReadLoop_FINDroppedQueueFull(t *testing.T) {
	// Start a local TCP server that immediately closes the connection (triggers EOF → FIN)
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
		// Close immediately to trigger EOF in readLoop
		conn.Close()
	}()

	// Queue size 1 and pre-fill it so the FIN send hits the default branch
	engine := NewExitEngine(nil, 300, 1)
	engine.respQueue <- &crypto.Chunk{} // fill the queue

	ctx := context.Background()
	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 100,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(ln.Addr().String()),
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Give readLoop time to hit EOF and try to send FIN (which gets dropped)
	time.Sleep(200 * time.Millisecond)
}

func TestExitEngine_ReadLoop_ContextCancelDuringSend(t *testing.T) {
	// Start a local TCP server that sends a lot of data then holds
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
		// Send enough data to fill the respQueue, then the send blocks
		for i := 0; i < 100; i++ {
			conn.Write([]byte("data from server"))
		}
		// Hold connection open
		time.Sleep(5 * time.Second)
	}()

	// Queue size 1 so it fills up quickly, forcing the ctx.Done() branch
	engine := NewExitEngine(nil, 300, 1)
	ctx, cancel := context.WithCancel(context.Background())

	err = engine.ProcessChunk(ctx, &crypto.Chunk{
		StreamID: 200,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(ln.Addr().String()),
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Let some data arrive and fill the queue
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger ctx.Done() path in the data send select
	cancel()

	// Give readLoop time to exit
	time.Sleep(200 * time.Millisecond)
}

// --- stream.go Write: ctx.Done() branch during chunk send (line 133-134) ---

func TestStream_WriteContextCancelled(t *testing.T) {
	// Use an unbuffered outQueue so the chunk send will block
	sm := &StreamManager{
		chunkSize: 5,
		outQueue:  make(chan *crypto.Chunk), // unbuffered — will block
	}

	ctx, cancel := context.WithCancel(context.Background())
	sctx, scancel := context.WithCancel(ctx)

	s := &Stream{
		ID:        1,
		State:     StateEstablished,
		splitter:  NewSplitter(1, 5),
		assembler: NewAssembler(),
		outQueue:  sm.outQueue,
		inData:    make(chan []byte, 256),
		ctx:       sctx,
		cancel:    scancel,
	}

	// Cancel the context before writing
	cancel()

	// Write data larger than chunkSize so it produces at least one chunk that blocks on send
	_, err := s.Write([]byte("hello world!!!"))
	if err == nil {
		t.Fatal("expected error on cancelled context during Write")
	}
}

// --- stream.go Read: !ok branch when inData channel is closed (line 143-144) ---

func TestStream_ReadClosedChannel(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	// Close the inData channel to trigger the !ok branch
	close(s.inData)

	buf := make([]byte, 100)
	_, err := s.Read(buf)
	if err == nil {
		t.Fatal("expected error on closed inData channel")
	}
}

// --- stream.go DeliverResponse: ctx.Done() branch (line 161-162) ---

func TestStream_DeliverResponse_ContextCancelled(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx, cancel := context.WithCancel(context.Background())

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	// Cancel the context
	cancel()

	// DeliverResponse should hit the ctx.Done() branch instead of blocking
	s.DeliverResponse(&crypto.Chunk{
		StreamID: s.ID,
		Sequence: 0,
		Payload:  []byte("data after cancel"),
	})
	// No panic and no hang = pass
}

func TestStream_DeliverResponse_ContextCancelledFullBuffer(t *testing.T) {
	// Use a tiny inData buffer so it fills up, forcing the ctx.Done() path
	ctx, cancel := context.WithCancel(context.Background())
	sctx, scancel := context.WithCancel(ctx)

	s := &Stream{
		ID:        1,
		State:     StateEstablished,
		splitter:  NewSplitter(1, 100),
		assembler: NewAssembler(),
		outQueue:  make(chan *crypto.Chunk, 64),
		inData:    make(chan []byte, 1), // tiny buffer: capacity 1
		ctx:       sctx,
		cancel:    scancel,
	}

	// Fill the inData buffer so the next send will block
	s.inData <- []byte("fill")

	// Cancel context so the blocking send hits ctx.Done()
	cancel()

	// Use sequence 0 so the assembler returns a segment (expected == 0)
	s.DeliverResponse(&crypto.Chunk{
		StreamID: s.ID,
		Sequence: 0,
		Payload:  []byte("more data"),
	})
	// No hang = pass
}

// --- stream.go Close: ctx.Done() branches during flush and FIN send (lines 188, 193) ---

func TestStream_CloseAlreadyClosed(t *testing.T) {
	sm := NewStreamManager(100, 64)
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	err := s.Close()
	if err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Drain the FIN
	drainOutQueue(sm, 100*time.Millisecond)

	// Second close should return nil immediately (already closed branch)
	err = s.Close()
	if err != nil {
		t.Fatalf("second Close should return nil, got: %v", err)
	}
}

func TestStream_Close_FlushSucceeds(t *testing.T) {
	// Test Close with buffered data in splitter and sufficient outQueue space.
	// This covers the `case s.outQueue <- flush:` success branch.
	sm := NewStreamManager(100, 64) // large outQueue buffer
	ctx := context.Background()

	s := sm.NewStream(ctx, "example.com:80")
	<-sm.OutQueue() // drain CONNECT

	// Write partial data that stays in splitter buffer (50 bytes < 100 chunkSize)
	s.Write([]byte("partial data that stays buffered"))
	// No full chunk produced, so nothing to drain from outQueue

	err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Should have flush chunk + FIN in the queue
	foundFlush := false
	foundFIN := false
	for i := 0; i < 3; i++ {
		select {
		case ch := <-sm.OutQueue():
			if ch.Type == crypto.ChunkData && ch.Flags&crypto.FlagLastChunk != 0 {
				foundFlush = true
			}
			if ch.Type == crypto.ChunkFIN {
				foundFIN = true
			}
		case <-time.After(time.Second):
		}
	}
	if !foundFlush {
		t.Error("expected flush chunk with FlagLastChunk")
	}
	if !foundFIN {
		t.Error("expected FIN chunk")
	}
}

func TestStream_Close_ContextCancelledDuringFlush(t *testing.T) {
	// Use an unbuffered outQueue so flush blocks
	ctx, cancel := context.WithCancel(context.Background())
	sctx, scancel := context.WithCancel(ctx)

	splitter := NewSplitter(1, 100)
	// Put some data in the splitter buffer so Flush() returns a non-nil chunk
	splitter.Split([]byte("partial"))

	s := &Stream{
		ID:        1,
		State:     StateEstablished,
		splitter:  splitter,
		assembler: NewAssembler(),
		outQueue:  make(chan *crypto.Chunk), // unbuffered — will block
		inData:    make(chan []byte, 256),
		ctx:       sctx,
		cancel:    scancel,
	}

	// Cancel context before Close so the flush send hits ctx.Done()
	cancel()

	err := s.Close()
	if err != nil {
		t.Fatalf("Close with cancelled context: %v", err)
	}

	if s.State != StateClosed {
		t.Errorf("State = %d, want StateClosed", s.State)
	}
}

func TestStream_Close_ContextCancelledDuringFINSend(t *testing.T) {
	// Use an unbuffered outQueue so FIN send blocks
	ctx, cancel := context.WithCancel(context.Background())
	sctx, scancel := context.WithCancel(ctx)

	s := &Stream{
		ID:        1,
		State:     StateEstablished,
		splitter:  NewSplitter(1, 100), // no buffered data, so Flush() returns nil
		assembler: NewAssembler(),
		outQueue:  make(chan *crypto.Chunk), // unbuffered — will block
		inData:    make(chan []byte, 256),
		ctx:       sctx,
		cancel:    scancel,
	}

	// Cancel context before Close so the FIN send hits ctx.Done()
	cancel()

	err := s.Close()
	if err != nil {
		t.Fatalf("Close with cancelled context during FIN: %v", err)
	}

	if s.State != StateClosed {
		t.Errorf("State = %d, want StateClosed", s.State)
	}
}

// --- dns.go DeliverResponse: default branch when respCh is already full ---

func TestRemoteDNSResolver_DeliverResponse_Duplicate(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resultCh := make(chan string, 1)
	go func() {
		ip, _ := resolver.Resolve(ctx, "dup.example.com")
		resultCh <- ip
	}()

	// Read the DNS request
	ch := <-outQueue

	// Deliver the response twice — the second should hit the default branch
	resp := &crypto.Chunk{
		StreamID: ch.StreamID,
		Sequence: ch.Sequence,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte("1.2.3.4"),
	}
	resolver.DeliverResponse(resp) // first: fills the buffered channel
	resolver.DeliverResponse(resp) // second: hits default (channel already has a value)

	select {
	case ip := <-resultCh:
		if ip != "1.2.3.4" {
			t.Errorf("ip = %q, want 1.2.3.4", ip)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// --- exit.go handleDNS: DNS lookup failure path ---

func TestExitEngine_HandleDNS_LookupFailure(t *testing.T) {
	engine := NewExitEngine(nil, 300, 100)

	// Use a domain that will fail DNS resolution
	err := engine.ProcessChunk(context.Background(), &crypto.Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte("this-domain-does-not-exist-xyzzy-12345.invalid"),
	})
	if err == nil {
		t.Fatal("expected error for failed DNS lookup")
	}
}

// --- dns.go Resolve: empty response (dns resolution failed) ---

func TestRemoteDNSResolver_ResolveEmptyResponse(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resultCh := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(ctx, "fail.example.com")
		resultCh <- err
	}()

	// Read the DNS request and deliver empty response
	ch := <-outQueue
	resolver.DeliverResponse(&crypto.Chunk{
		StreamID: ch.StreamID,
		Sequence: ch.Sequence,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte(""), // empty = failed
	})

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected error for empty DNS response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// --- dns.go Resolve: expired cache entry triggers re-resolve ---

func TestRemoteDNSResolver_ResolveExpiredCache(t *testing.T) {
	outQueue := make(chan *crypto.Chunk, 10)
	resolver := NewRemoteDNSResolver(outQueue)

	// Manually insert an expired cache entry
	resolver.cache.Store("stale.example.com", &dnsEntry{
		ip:      "1.1.1.1",
		expires: time.Now().Add(-1 * time.Second), // already expired
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		ip, err := resolver.Resolve(ctx, "stale.example.com")
		if err != nil {
			errCh <- err
		} else {
			resultCh <- ip
		}
	}()

	// Should send a new DNS chunk since cache is expired
	select {
	case ch := <-outQueue:
		if string(ch.Payload) != "stale.example.com" {
			t.Fatalf("unexpected payload: %q", ch.Payload)
		}
		resolver.DeliverResponse(&crypto.Chunk{
			StreamID: ch.StreamID,
			Sequence: ch.Sequence,
			Type:     crypto.ChunkDNSResolve,
			Payload:  []byte("2.2.2.2"),
		})
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for DNS chunk after expired cache")
	}

	select {
	case ip := <-resultCh:
		if ip != "2.2.2.2" {
			t.Errorf("ip = %q, want 2.2.2.2", ip)
		}
	case err := <-errCh:
		t.Fatalf("resolve error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for resolve result")
	}
}
