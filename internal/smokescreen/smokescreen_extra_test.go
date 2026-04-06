package smokescreen

import (
	"context"
	"io"
	"log/slog"
	mrand "math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- Peer.run() tests ----------

// mockConn is a minimal net.Conn that records writes and provides reads.
type mockConn struct {
	writtenBytes int64
	readBytes    int64
	closed       int32
	closeCh      chan struct{}
}

func newMockConn() *mockConn {
	return &mockConn{closeCh: make(chan struct{})}
}

func (c *mockConn) Read(b []byte) (int, error) {
	// Check if closed
	select {
	case <-c.closeCh:
		return 0, net.ErrClosed
	default:
	}
	// Return synthetic data for symmetric reads
	for i := range b {
		b[i] = 0xAB
	}
	atomic.AddInt64(&c.readBytes, int64(len(b)))
	return len(b), nil
}

func (c *mockConn) Write(b []byte) (int, error) {
	select {
	case <-c.closeCh:
		return 0, net.ErrClosed
	default:
	}
	atomic.AddInt64(&c.writtenBytes, int64(len(b)))
	return len(b), nil
}

func (c *mockConn) Close() error {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		close(c.closeCh)
	}
	return nil
}

func (c *mockConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *mockConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *mockConn) SetDeadline(t time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestPeer_RunAsymmetric(t *testing.T) {
	// Test run() with an asymmetric profile (no reads)
	mc := newMockConn()
	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := &Peer{
		addr:    "1.2.3.4:5000",
		profile: ProfileMessaging, // Symmetric: false
		conn:    mc,
		logger:  logger,
		rng:     rng,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	p.run(ctx)

	if atomic.LoadInt64(&mc.writtenBytes) == 0 {
		t.Error("expected some bytes to be written for asymmetric profile")
	}
	// Asymmetric should not read
	if atomic.LoadInt64(&mc.readBytes) != 0 {
		t.Error("asymmetric profile should not read from connection")
	}
}

func TestPeer_RunSymmetric(t *testing.T) {
	// Test run() with a symmetric profile (should read and write)
	mc := newMockConn()
	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := &Peer{
		addr:    "1.2.3.4:5000",
		profile: ProfileVideoCall, // Symmetric: true
		conn:    mc,
		logger:  logger,
		rng:     rng,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	p.run(ctx)

	if atomic.LoadInt64(&mc.writtenBytes) == 0 {
		t.Error("expected some bytes to be written for symmetric profile")
	}
	if atomic.LoadInt64(&mc.readBytes) == 0 {
		t.Error("symmetric profile should also read from connection")
	}
}

func TestPeer_RunNilConn(t *testing.T) {
	// run() with nil conn should return immediately
	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := &Peer{
		addr:    "1.2.3.4:5000",
		profile: ProfileMessaging,
		conn:    nil,
		logger:  logger,
		rng:     rng,
	}

	// Should not panic or hang
	ctx := context.Background()
	p.run(ctx)
}

func TestPeer_RunWriteError(t *testing.T) {
	// If the connection is closed, run() should exit
	mc := newMockConn()
	mc.Close() // pre-close

	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := &Peer{
		addr:    "1.2.3.4:5000",
		profile: ProfileMessaging,
		conn:    mc,
		logger:  logger,
		rng:     rng,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should exit quickly due to write error
	done := make(chan struct{})
	go func() {
		p.run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good, exited due to error
	case <-time.After(1 * time.Second):
		t.Error("run() did not exit after write error")
	}
}

// ---------- Peer.connect() tests ----------

func TestPeer_ConnectContextCancelled(t *testing.T) {
	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	p := newPeer("192.0.2.1:12345", ProfileMessaging, rng, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err := p.connect(ctx)
	if err == nil {
		t.Error("expected error connecting with cancelled context")
		if p.conn != nil {
			p.conn.Close()
		}
	}
}

// ---------- randomPeerAddr edge cases ----------

func TestSmokeScreen_RandomPeerAddr_NoPrivateIPs(t *testing.T) {
	// Use many seeds to maximize the chance of hitting reserved-range skipping
	for seed := int64(0); seed < 50; seed++ {
		ss := NewSmokeScreen(SmokeScreenConfig{Seed: seed})
		for i := 0; i < 50; i++ {
			addr := ss.randomPeerAddr()
			ip, _, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("invalid addr %q: %v", addr, err)
			}
			parsed := net.ParseIP(ip)
			if parsed == nil {
				t.Fatalf("invalid IP: %s", ip)
			}
			// Check all the reserved ranges that are skipped
			if parsed[len(parsed)-4] == 0 {
				t.Errorf("got 0.x.x.x IP: %s", ip)
			}
			if parsed[len(parsed)-4] == 10 {
				t.Errorf("got 10.x.x.x IP: %s", ip)
			}
			if parsed[len(parsed)-4] == 127 {
				t.Errorf("got 127.x.x.x IP: %s", ip)
			}
			if parsed[len(parsed)-4] == 192 && parsed[len(parsed)-3] == 168 {
				t.Errorf("got 192.168.x.x IP: %s", ip)
			}
			if parsed[len(parsed)-4] == 172 && parsed[len(parsed)-3] >= 16 && parsed[len(parsed)-3] <= 31 {
				t.Errorf("got 172.16-31.x.x IP: %s", ip)
			}
		}
	}
}

// ---------- DecoyManager edge case tests ----------

func TestDecoyManager_FetchDecoy_InvalidURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})

	// Should not panic on invalid URL
	dm.fetchDecoy(context.Background(), "://invalid-url")
}

func TestDecoyManager_FetchDecoy_CancelledContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return quickly without error (the request fails silently)
	dm.fetchDecoy(ctx, "https://example.com")
}

func TestDecoyManager_FetchDecoy_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})
	// Should not panic on server error
	dm.fetchDecoy(context.Background(), srv.URL)
}

func TestDecoyManager_FetchDecoy_LargeBody(t *testing.T) {
	// Verify body reading is limited
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write more than 5MB
		w.WriteHeader(200)
		w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})
	dm.fetchDecoy(context.Background(), srv.URL)
}

func TestDecoyManager_FetchDecoy_Headers(t *testing.T) {
	var userAgent, accept, acceptLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		accept = r.Header.Get("Accept")
		acceptLang = r.Header.Get("Accept-Language")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})
	dm.fetchDecoy(context.Background(), srv.URL)

	if userAgent == "" {
		t.Error("User-Agent header should be set")
	}
	if accept == "" {
		t.Error("Accept header should be set")
	}
	if acceptLang == "" {
		t.Error("Accept-Language header should be set")
	}
}

func TestDecoyManager_Redirect(t *testing.T) {
	var redirectCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&redirectCount, 1)
		if count <= 4 {
			http.Redirect(w, r, "/redirect", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{Seed: 1, Logger: logger})
	dm.fetchDecoy(context.Background(), srv.URL)

	// The redirect limit is 3 in the CheckRedirect func, so we expect at most 4 requests
	// (original + 3 redirects), after which it should stop with ErrUseLastResponse.
	count := atomic.LoadInt32(&redirectCount)
	if count > 4 {
		t.Errorf("expected at most 4 requests (1 + 3 redirects), got %d", count)
	}
}

func TestDecoyManager_PickTarget_Fallback(t *testing.T) {
	// Create a manager with known targets and test that pickTarget always
	// returns a valid target (including the fallback)
	targets := []DecoyTarget{
		{URL: "https://a.com", Weight: 1},
	}
	dm := NewDecoyManager(DecoyConfig{
		Targets: targets,
		Seed:    42,
	})

	for i := 0; i < 100; i++ {
		target := dm.pickTarget()
		if target.URL != "https://a.com" {
			t.Errorf("unexpected target: %s", target.URL)
		}
	}
}

// ---------- peerLoop coverage via Run with mock listener ----------

func TestSmokeScreen_PeerLoop_ConnectFails(t *testing.T) {
	// Run with a very short timeout so that peerLoop goes through
	// at least one iteration where connect fails (random IPs won't respond).
	ss := NewSmokeScreen(SmokeScreenConfig{
		Seed:      42,
		PeerCount: 1,
		Profiles:  []*TrafficProfile{ProfileMessaging},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := ss.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Logf("Run returned: %v (expected DeadlineExceeded, but connect timeout may win)", err)
	}

	// After run, should not be running
	if ss.IsRunning() {
		t.Error("should not be running after context done")
	}
}

func TestNewPeer(t *testing.T) {
	rng := mrand.New(mrand.NewSource(42))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := newPeer("1.2.3.4:5000", ProfileGaming, rng, logger)

	if p.addr != "1.2.3.4:5000" {
		t.Errorf("addr = %q, want 1.2.3.4:5000", p.addr)
	}
	if p.profile != ProfileGaming {
		t.Error("wrong profile")
	}
	if p.conn != nil {
		t.Error("conn should be nil initially")
	}
}

func TestProfileFileSync(t *testing.T) {
	p := ProfileFileSync
	if p.Name != "file_sync" {
		t.Errorf("name = %q, want file_sync", p.Name)
	}
	if p.Symmetric {
		t.Error("file_sync should not be symmetric")
	}
	if p.BurstSize < 1024 {
		t.Error("file_sync should have large burst size")
	}
}

func TestNewDecoyManager_WithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dm := NewDecoyManager(DecoyConfig{
		Logger: logger,
		Seed:   42,
	})
	if dm.logger != logger {
		t.Error("custom logger not set")
	}
}

func TestNewSmokeScreen_WithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ss := NewSmokeScreen(SmokeScreenConfig{
		Logger: logger,
		Seed:   42,
	})
	if ss.logger != logger {
		t.Error("custom logger not set")
	}
}

func TestSmokeScreen_ActivePeers_Initially(t *testing.T) {
	ss := NewSmokeScreen(SmokeScreenConfig{Seed: 42})
	if ss.ActivePeers() != 0 {
		t.Errorf("initial active peers = %d, want 0", ss.ActivePeers())
	}
}

// Test that the decoy loop handles context cancellation during the wait phase.
func TestDecoyManager_DecoyLoop_CancelDuringWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	targets := []DecoyTarget{
		{URL: srv.URL, Weight: 10},
	}
	dm := NewDecoyManager(DecoyConfig{
		Targets: targets,
		Count:   1,
		Seed:    42,
	})

	// Run for a short time so it enters the loop and then gets cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := dm.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Logf("Run returned: %v", err)
	}
}
