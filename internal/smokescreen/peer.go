package smokescreen

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand"
	"net"
	"sync"
	"time"
)

// TrafficProfile defines the traffic pattern for a P2P connection type.
type TrafficProfile struct {
	Name         string
	MinBPS       int // minimum bytes/sec
	MaxBPS       int // maximum bytes/sec
	Symmetric    bool // true if upload ≈ download
	BurstSize    int // bytes per burst
	BurstPause   time.Duration
}

var (
	ProfileVideoCall = &TrafficProfile{
		Name: "video_call", MinBPS: 128 * 1024, MaxBPS: 640 * 1024,
		Symmetric: true, BurstSize: 1400, BurstPause: 20 * time.Millisecond,
	}
	ProfileMessaging = &TrafficProfile{
		Name: "messaging", MinBPS: 1024, MaxBPS: 6 * 1024,
		Symmetric: false, BurstSize: 256, BurstPause: 200 * time.Millisecond,
	}
	ProfileFileSync = &TrafficProfile{
		Name: "file_sync", MinBPS: 64 * 1024, MaxBPS: 256 * 1024,
		Symmetric: false, BurstSize: 16384, BurstPause: 50 * time.Millisecond,
	}
	ProfileGaming = &TrafficProfile{
		Name: "gaming", MinBPS: 12 * 1024, MaxBPS: 64 * 1024,
		Symmetric: true, BurstSize: 512, BurstPause: 16 * time.Millisecond,
	}
)

// AllProfiles returns all available traffic profiles.
func AllProfiles() []*TrafficProfile {
	return []*TrafficProfile{
		ProfileVideoCall, ProfileMessaging, ProfileFileSync, ProfileGaming,
	}
}

// Peer represents a single P2P cover connection.
type Peer struct {
	addr    string
	profile *TrafficProfile
	conn    net.Conn
	logger  *slog.Logger
	rng     *mrand.Rand
}

// newPeer creates a new peer connection.
func newPeer(addr string, profile *TrafficProfile, rng *mrand.Rand, logger *slog.Logger) *Peer {
	return &Peer{
		addr:    addr,
		profile: profile,
		logger:  logger,
		rng:     rng,
	}
}

// connect establishes a TLS connection to the peer address.
func (p *Peer) connect(ctx context.Context) error {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: true, // random IPs won't have valid certs
			MinVersion:         tls.VersionTLS12,
		},
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
	}
	conn, err := dialer.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return err
	}
	p.conn = conn
	return nil
}

// run sends cover traffic matching the profile until ctx is cancelled.
func (p *Peer) run(ctx context.Context) {
	if p.conn == nil {
		return
	}
	defer p.conn.Close()

	buf := make([]byte, p.profile.BurstSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Generate random data
		rand.Read(buf)

		p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := p.conn.Write(buf); err != nil {
			return
		}

		// If symmetric, also read
		if p.profile.Symmetric {
			p.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			io.ReadFull(p.conn, buf[:64]) // partial read is fine for cover
		}

		select {
		case <-time.After(p.profile.BurstPause):
		case <-ctx.Done():
			return
		}
	}
}

// SmokeScreen manages multiple P2P cover connections.
type SmokeScreen struct {
	peerCount int
	profiles  []*TrafficProfile
	logger    *slog.Logger
	rng       *mrand.Rand

	mu      sync.Mutex
	peers   []*Peer
	running bool
}

// SmokeScreenConfig configures the smoke screen.
type SmokeScreenConfig struct {
	PeerCount int // number of concurrent P2P connections
	Profiles  []*TrafficProfile
	Seed      int64
	Logger    *slog.Logger
}

// NewSmokeScreen creates a P2P smoke screen.
func NewSmokeScreen(cfg SmokeScreenConfig) *SmokeScreen {
	if cfg.PeerCount <= 0 {
		cfg.PeerCount = 3
	}
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = AllProfiles()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &SmokeScreen{
		peerCount: cfg.PeerCount,
		profiles:  cfg.Profiles,
		rng:       mrand.New(mrand.NewSource(cfg.Seed)),
		logger:    cfg.Logger,
	}
}

// Run starts P2P cover connections and blocks until ctx is cancelled.
func (ss *SmokeScreen) Run(ctx context.Context) error {
	ss.mu.Lock()
	ss.running = true
	ss.mu.Unlock()

	defer func() {
		ss.mu.Lock()
		ss.running = false
		ss.mu.Unlock()
	}()

	var wg sync.WaitGroup

	for i := 0; i < ss.peerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ss.peerLoop(ctx, idx)
		}(i)
	}

	wg.Wait()
	return ctx.Err()
}

// peerLoop maintains a single P2P peer connection with reconnect.
func (ss *SmokeScreen) peerLoop(ctx context.Context, idx int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		profile := ss.profiles[ss.rng.Intn(len(ss.profiles))]
		addr := ss.randomPeerAddr()

		peer := newPeer(addr, profile, ss.rng, ss.logger)

		connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := peer.connect(connCtx)
		cancel()

		if err != nil {
			// Connection failure is expected (random IPs mostly won't respond)
			// This is fine — the connection attempt itself generates traffic
			ss.logger.Debug("peer connect failed (expected)", "addr", addr, "err", err)
			// Wait before retry
			select {
			case <-time.After(time.Duration(10+ss.rng.Intn(50)) * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		ss.mu.Lock()
		ss.peers = append(ss.peers, peer)
		ss.mu.Unlock()

		ss.logger.Debug("peer connected", "addr", addr, "profile", profile.Name)

		// Run cover traffic for a random duration (30s - 5min)
		duration := time.Duration(30+ss.rng.Intn(270)) * time.Second
		peerCtx, peerCancel := context.WithTimeout(ctx, duration)
		peer.run(peerCtx)
		peerCancel()

		// Remove from peers list
		ss.mu.Lock()
		for j, p := range ss.peers {
			if p == peer {
				ss.peers = append(ss.peers[:j], ss.peers[j+1:]...)
				break
			}
		}
		ss.mu.Unlock()

		// Brief pause before next peer
		select {
		case <-time.After(time.Duration(5+ss.rng.Intn(15)) * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// randomPeerAddr generates a random IP:port that looks like a residential peer.
func (ss *SmokeScreen) randomPeerAddr() string {
	// Generate random non-reserved IPv4 addresses
	var ip net.IP
	for {
		b := make([]byte, 4)
		b[0] = byte(ss.rng.Intn(224))
		b[1] = byte(ss.rng.Intn(256))
		b[2] = byte(ss.rng.Intn(256))
		b[3] = byte(1 + ss.rng.Intn(254))
		ip = net.IP(b)

		// Skip reserved ranges
		if ip[0] == 0 || ip[0] == 10 || ip[0] == 127 {
			continue
		}
		if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
			continue
		}
		if ip[0] == 192 && ip[1] == 168 {
			continue
		}
		break
	}

	// Random high port (looks like P2P)
	port := 10000 + ss.rng.Intn(55000)
	return fmt.Sprintf("%s:%d", ip.String(), port)
}

// IsRunning returns whether the smoke screen is active.
func (ss *SmokeScreen) IsRunning() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.running
}

// ActivePeers returns the current number of active peer connections.
func (ss *SmokeScreen) ActivePeers() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.peers)
}
