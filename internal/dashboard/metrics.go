package dashboard

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics collects runtime and application metrics for dashboard display.
type Metrics struct {
	// Counters (atomic)
	bytesIn      atomic.Int64
	bytesOut     atomic.Int64
	queriesIn    atomic.Int64
	tunnelChunks atomic.Int64
	errorCount   atomic.Int64

	// Gauges (mutex-protected)
	mu             sync.RWMutex
	activeStreams   int
	activeConns     int
	goroutines     int
	heapAllocMB    float64
	lastGCPauseMs  float64

	startTime time.Time
}

// MetricsSnapshot is a point-in-time copy of all metrics.
type MetricsSnapshot struct {
	BytesIn       int64   `json:"bytes_in"`
	BytesOut      int64   `json:"bytes_out"`
	QueriesIn     int64   `json:"queries_in"`
	TunnelChunks  int64   `json:"tunnel_chunks"`
	ErrorCount    int64   `json:"error_count"`
	ActiveStreams int     `json:"active_streams"`
	ActiveConns   int     `json:"active_conns"`
	Goroutines    int     `json:"goroutines"`
	HeapAllocMB   float64 `json:"heap_alloc_mb"`
	LastGCPauseMs float64 `json:"last_gc_pause_ms"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// NewMetrics creates a new metrics collector.
func NewMetrics() *Metrics {
	return &Metrics{
		startTime: time.Now(),
	}
}

// AddBytesIn increments incoming byte counter.
func (m *Metrics) AddBytesIn(n int64) { m.bytesIn.Add(n) }

// AddBytesOut increments outgoing byte counter.
func (m *Metrics) AddBytesOut(n int64) { m.bytesOut.Add(n) }

// AddQuery increments query counter.
func (m *Metrics) AddQuery() { m.queriesIn.Add(1) }

// AddTunnelChunk increments tunnel chunk counter.
func (m *Metrics) AddTunnelChunk() { m.tunnelChunks.Add(1) }

// AddError increments error counter.
func (m *Metrics) AddError() { m.errorCount.Add(1) }

// SetActiveStreams updates active stream count.
func (m *Metrics) SetActiveStreams(n int) {
	m.mu.Lock()
	m.activeStreams = n
	m.mu.Unlock()
}

// SetActiveConns updates active connection count.
func (m *Metrics) SetActiveConns(n int) {
	m.mu.Lock()
	m.activeConns = n
	m.mu.Unlock()
}

// Refresh updates runtime gauges (goroutines, memory, GC).
func (m *Metrics) Refresh() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	m.mu.Lock()
	m.goroutines = runtime.NumGoroutine()
	m.heapAllocMB = float64(mem.HeapAlloc) / (1024 * 1024)
	if mem.NumGC > 0 {
		m.lastGCPauseMs = float64(mem.PauseNs[(mem.NumGC+255)%256]) / 1e6
	}
	m.mu.Unlock()
}

// Snapshot returns a point-in-time copy of all metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MetricsSnapshot{
		BytesIn:       m.bytesIn.Load(),
		BytesOut:      m.bytesOut.Load(),
		QueriesIn:     m.queriesIn.Load(),
		TunnelChunks:  m.tunnelChunks.Load(),
		ErrorCount:    m.errorCount.Load(),
		ActiveStreams: m.activeStreams,
		ActiveConns:   m.activeConns,
		Goroutines:    m.goroutines,
		HeapAllocMB:   m.heapAllocMB,
		LastGCPauseMs: m.lastGCPauseMs,
		UptimeSeconds: time.Since(m.startTime).Seconds(),
	}
}
