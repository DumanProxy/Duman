package pool

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"
)

// HealthChecker periodically probes relays in the pool and updates their
// state based on connectivity results. It detects:
//   - TCP connect timeout → likely ISP IP blocking
//   - TLS handshake failure → likely DPI interference
//   - Connection success → relay is healthy
//
// Failed relays are retried with exponential backoff. After exceeding
// the failure threshold, a relay is marked as blocked.
type HealthChecker struct {
	pool          *Pool
	interval      time.Duration
	timeout       time.Duration
	failThreshold int
	logger        *slog.Logger

	// checkFunc can be overridden in tests. If nil, the default TCP/TLS
	// check is used.
	checkFunc func(relay *RelayInfo) (time.Duration, error)
}

// HealthCheckConfig configures the health checker.
type HealthCheckConfig struct {
	Pool             *Pool
	Interval         time.Duration // default 30s
	Timeout          time.Duration // default 10s
	FailureThreshold int           // default 3, failures before marking blocked
	Logger           *slog.Logger
}

// NewHealthChecker creates a health checker for the given pool.
func NewHealthChecker(cfg HealthCheckConfig) *HealthChecker {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &HealthChecker{
		pool:          cfg.Pool,
		interval:      cfg.Interval,
		timeout:       cfg.Timeout,
		failThreshold: cfg.FailureThreshold,
		logger:        cfg.Logger,
	}
}

// Run starts the health check loop. It blocks until ctx is cancelled.
func (hc *HealthChecker) Run(ctx context.Context) error {
	// Run an immediate check on startup.
	hc.checkAll(ctx)

	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			hc.checkAll(ctx)
		}
	}
}

// checkAll probes every relay in the pool concurrently.
func (hc *HealthChecker) checkAll(ctx context.Context) {
	hc.pool.mu.RLock()
	relays := make([]*RelayInfo, len(hc.pool.relays))
	copy(relays, hc.pool.relays)
	hc.pool.mu.RUnlock()

	var wg sync.WaitGroup
	for _, r := range relays {
		// Skip blocked relays unless enough time has passed (exponential backoff).
		if r.State == StateBlocked {
			backoff := hc.backoffDuration(r.FailCount)
			if time.Since(r.LastCheck) < backoff {
				continue
			}
		}

		wg.Add(1)
		go func(relay *RelayInfo) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := hc.CheckRelay(relay); err != nil {
				hc.logger.Debug("health check failed",
					"address", relay.Address,
					"error", err)
			}
		}(r)
	}
	wg.Wait()

	// Recompute active set after health checks.
	hc.pool.SelectActive()
}

// CheckRelay performs a single health check on the given relay.
// It uses TCP connect followed by a TLS handshake to detect blocking.
func (hc *HealthChecker) CheckRelay(relay *RelayInfo) error {
	var latency time.Duration
	var err error

	if hc.checkFunc != nil {
		latency, err = hc.checkFunc(relay)
	} else {
		latency, err = hc.tcpTLSCheck(relay)
	}

	if err != nil {
		hc.pool.MarkFailed(relay.Address)

		// Check if we've exceeded the failure threshold.
		hc.pool.mu.RLock()
		r := hc.pool.find(relay.Address)
		failCount := 0
		if r != nil {
			failCount = r.FailCount
		}
		hc.pool.mu.RUnlock()

		if failCount >= hc.failThreshold {
			hc.pool.MarkBlocked(relay.Address)
			hc.logger.Warn("relay blocked after repeated failures",
				"address", relay.Address,
				"fail_count", failCount)
		}
		return err
	}

	hc.pool.MarkHealthy(relay.Address, latency)
	return nil
}

// tcpTLSCheck performs a real TCP connect and optional TLS handshake.
// Returns latency on success or a descriptive error on failure.
func (hc *HealthChecker) tcpTLSCheck(relay *RelayInfo) (time.Duration, error) {
	start := time.Now()

	// Phase 1: TCP connect.
	conn, err := net.DialTimeout("tcp", relay.Address, hc.timeout)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return 0, fmt.Errorf("tcp connect timeout (likely IP blocked): %w", err)
		}
		return 0, fmt.Errorf("tcp connect failed: %w", err)
	}

	// Phase 2: TLS handshake (to detect DPI interference).
	// Use InsecureSkipVerify because relay certificates may be self-signed.
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true,
	})
	_ = tlsConn.SetDeadline(time.Now().Add(hc.timeout))

	if err := tlsConn.Handshake(); err != nil {
		_ = conn.Close()
		return 0, fmt.Errorf("tls handshake failed (likely DPI interference): %w", err)
	}

	latency := time.Since(start)
	_ = tlsConn.Close()

	return latency, nil
}

// backoffDuration returns an exponential backoff duration based on the
// number of consecutive failures. Capped at 30 minutes.
func (hc *HealthChecker) backoffDuration(failCount int) time.Duration {
	if failCount <= 0 {
		return hc.interval
	}
	// Base: interval * 2^(failCount-1), capped at 30 minutes.
	d := float64(hc.interval) * math.Pow(2, float64(failCount-1))
	maxBackoff := 30 * time.Minute
	if d > float64(maxBackoff) {
		return maxBackoff
	}
	return time.Duration(d)
}
