package governor

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// BandwidthProber measures available bandwidth through a relay connection.
type BandwidthProber struct {
	sendFunc func(data []byte) error // sends data through relay
	governor *Governor
	logger   *slog.Logger

	mu              sync.Mutex
	lastDetected    int64 // bytes/sec
	lastProbeTime   time.Time
	probeInterval   time.Duration
	fallbackBPS     int64
}

// ProberConfig configures the bandwidth prober.
type ProberConfig struct {
	SendFunc      func(data []byte) error
	Governor      *Governor
	ProbeInterval time.Duration // default 10 minutes
	FallbackBPS   int64         // fallback if auto-detect fails
	Logger        *slog.Logger
}

// NewBandwidthProber creates a bandwidth prober.
func NewBandwidthProber(cfg ProberConfig) *BandwidthProber {
	if cfg.ProbeInterval <= 0 {
		cfg.ProbeInterval = 10 * time.Minute
	}
	if cfg.FallbackBPS <= 0 {
		cfg.FallbackBPS = 10 * 1024 * 1024 // 10 MB/s
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &BandwidthProber{
		sendFunc:      cfg.SendFunc,
		governor:      cfg.Governor,
		logger:        cfg.Logger,
		probeInterval: cfg.ProbeInterval,
		fallbackBPS:   cfg.FallbackBPS,
	}
}

// Probe sends a burst of data and measures throughput.
// Returns estimated bandwidth in bytes/sec.
func (p *BandwidthProber) Probe() int64 {
	if p.sendFunc == nil {
		return p.fallbackBPS
	}

	// Send a burst of 64KB (small enough to not disrupt, large enough to measure)
	probeData := make([]byte, 64*1024)
	for i := range probeData {
		probeData[i] = byte(i & 0xFF)
	}

	start := time.Now()
	rounds := 4
	totalBytes := 0

	for i := 0; i < rounds; i++ {
		if err := p.sendFunc(probeData); err != nil {
			p.logger.Debug("bandwidth probe send failed", "round", i, "err", err)
			break
		}
		totalBytes += len(probeData)
	}

	elapsed := time.Since(start)
	if elapsed <= 0 || totalBytes == 0 {
		return p.fallbackBPS
	}

	bps := int64(float64(totalBytes) / elapsed.Seconds())

	// Sanity check: if result is unreasonably low or high, use fallback
	if bps < 1024 || bps > 10*1024*1024*1024 { // <1KB/s or >10GB/s
		p.logger.Warn("bandwidth probe result out of range", "bps", bps)
		return p.fallbackBPS
	}

	p.mu.Lock()
	p.lastDetected = bps
	p.lastProbeTime = time.Now()
	p.mu.Unlock()

	p.logger.Info("bandwidth detected",
		"mbps", float64(bps)/(1024*1024),
		"elapsed", elapsed)

	return bps
}

// Run periodically probes bandwidth and updates the governor.
func (p *BandwidthProber) Run(ctx context.Context) error {
	// Initial probe
	bps := p.Probe()
	if p.governor != nil {
		p.governor.SetTotalBandwidth(bps)
	}

	ticker := time.NewTicker(p.probeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			bps := p.Probe()
			if p.governor != nil {
				p.governor.SetTotalBandwidth(bps)
			}
		}
	}
}

// LastDetected returns the last detected bandwidth in bytes/sec.
func (p *BandwidthProber) LastDetected() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastDetected
}
