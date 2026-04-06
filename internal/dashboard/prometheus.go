package dashboard

import (
	"fmt"
	"net/http"
	"strings"
)

// HandlePrometheus returns an http.HandlerFunc that serves metrics
// in Prometheus exposition text format (text/plain; version=0.0.4).
// It reads a snapshot from the provided Metrics and formats each
// metric with HELP and TYPE annotations.
func HandlePrometheus(m *Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.Refresh()
		snap := m.Snapshot()

		var b strings.Builder

		writeCounter(&b, "duman_bytes_in_total", "Total bytes received", fmt.Sprintf("%d", snap.BytesIn))
		writeCounter(&b, "duman_bytes_out_total", "Total bytes sent", fmt.Sprintf("%d", snap.BytesOut))
		writeCounter(&b, "duman_queries_total", "Total cover queries processed", fmt.Sprintf("%d", snap.QueriesIn))
		writeCounter(&b, "duman_tunnel_chunks_total", "Total tunnel chunks processed", fmt.Sprintf("%d", snap.TunnelChunks))
		writeCounter(&b, "duman_errors_total", "Total errors", fmt.Sprintf("%d", snap.ErrorCount))

		writeGauge(&b, "duman_active_streams", "Current active tunnel streams", fmt.Sprintf("%d", snap.ActiveStreams))
		writeGauge(&b, "duman_active_connections", "Current active connections", fmt.Sprintf("%d", snap.ActiveConns))
		writeGauge(&b, "duman_goroutines", "Current number of goroutines", fmt.Sprintf("%d", snap.Goroutines))
		writeGauge(&b, "duman_heap_alloc_mb", "Current heap allocation in MB", fmt.Sprintf("%g", snap.HeapAllocMB))
		writeGauge(&b, "duman_gc_pause_ms", "Last GC pause in milliseconds", fmt.Sprintf("%g", snap.LastGCPauseMs))
		writeGauge(&b, "duman_uptime_seconds", "Uptime in seconds", fmt.Sprintf("%g", snap.UptimeSeconds))

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(b.String()))
	}
}

func writeCounter(b *strings.Builder, name, help, value string) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	fmt.Fprintf(b, "%s %s\n\n", name, value)
}

func writeGauge(b *strings.Builder, name, help, value string) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s %s\n\n", name, value)
}

// RegisterMetrics registers the /metrics Prometheus endpoint on the
// ClientDashboard's mux, using the provided Metrics collector.
func (d *ClientDashboard) RegisterMetrics(m *Metrics) {
	d.mux.HandleFunc("/metrics", HandlePrometheus(m))
}

// RegisterMetrics registers the /metrics Prometheus endpoint on the
// RelayDashboard's mux, using the provided Metrics collector.
func (d *RelayDashboard) RegisterMetrics(m *Metrics) {
	d.mux.HandleFunc("/metrics", HandlePrometheus(m))
}
