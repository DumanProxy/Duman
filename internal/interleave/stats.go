package interleave

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Stats tracks tunnel and cover query throughput.
type Stats struct {
	TunnelChunks  atomic.Int64
	TunnelBytes   atomic.Int64
	CoverQueries  atomic.Int64
	CoverBytes    atomic.Int64
	ResponseBytes atomic.Int64

	startTime time.Time
	logger    *slog.Logger
}

// NewStats creates a new stats tracker.
func NewStats(logger *slog.Logger) *Stats {
	return &Stats{
		startTime: time.Now(),
		logger:    logger,
	}
}

// RecordTunnel records a tunnel chunk send.
func (s *Stats) RecordTunnel(payloadBytes int) {
	s.TunnelChunks.Add(1)
	s.TunnelBytes.Add(int64(payloadBytes))
}

// RecordCover records a cover query send.
func (s *Stats) RecordCover(queryBytes int) {
	s.CoverQueries.Add(1)
	s.CoverBytes.Add(int64(queryBytes))
}

// RecordResponse records response bytes received.
func (s *Stats) RecordResponse(bytes int) {
	s.ResponseBytes.Add(int64(bytes))
}

// Snapshot returns current stats values.
func (s *Stats) Snapshot() StatsSnapshot {
	elapsed := time.Since(s.startTime).Seconds()
	if elapsed < 0.001 {
		elapsed = 0.001
	}

	tunnelChunks := s.TunnelChunks.Load()
	tunnelBytes := s.TunnelBytes.Load()
	coverQueries := s.CoverQueries.Load()
	coverBytes := s.CoverBytes.Load()
	respBytes := s.ResponseBytes.Load()

	return StatsSnapshot{
		Elapsed:       time.Since(s.startTime),
		TunnelChunks:  tunnelChunks,
		TunnelBytes:   tunnelBytes,
		CoverQueries:  coverQueries,
		CoverBytes:    coverBytes,
		ResponseBytes: respBytes,
		TunnelBPS:     float64(tunnelBytes) / elapsed,
		CoverQPS:      float64(coverQueries) / elapsed,
		TotalBPS:      float64(tunnelBytes+coverBytes) / elapsed,
	}
}

// StatsSnapshot is a point-in-time view of stats.
type StatsSnapshot struct {
	Elapsed       time.Duration
	TunnelChunks  int64
	TunnelBytes   int64
	CoverQueries  int64
	CoverBytes    int64
	ResponseBytes int64
	TunnelBPS     float64 // tunnel bytes per second
	CoverQPS      float64 // cover queries per second
	TotalBPS      float64 // total bytes per second (tunnel + cover)
}

// RunReporter starts a goroutine that periodically logs stats.
// It stops when ctx is cancelled.
func (s *Stats) RunReporter(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final summary
			snap := s.Snapshot()
			s.logger.Info("tunnel stats (final)",
				"elapsed", snap.Elapsed.Round(time.Millisecond),
				"tunnel_chunks", snap.TunnelChunks,
				"tunnel_bytes", formatBytes(snap.TunnelBytes),
				"cover_queries", snap.CoverQueries,
				"cover_bytes", formatBytes(snap.CoverBytes),
				"resp_bytes", formatBytes(snap.ResponseBytes),
				"tunnel_throughput", formatBPS(snap.TunnelBPS),
				"cover_qps", int64(snap.CoverQPS),
				"total_throughput", formatBPS(snap.TotalBPS),
			)
			return
		case <-ticker.C:
			snap := s.Snapshot()
			s.logger.Info("tunnel stats",
				"tunnel_chunks", snap.TunnelChunks,
				"tunnel_bytes", formatBytes(snap.TunnelBytes),
				"cover_queries", snap.CoverQueries,
				"tunnel_throughput", formatBPS(snap.TunnelBPS),
				"cover_qps", int64(snap.CoverQPS),
			)
		}
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatBPS(bps float64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", bps/float64(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", bps/float64(1<<10))
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}
