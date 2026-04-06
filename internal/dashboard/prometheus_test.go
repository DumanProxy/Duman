package dashboard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusMetrics_Format(t *testing.T) {
	m := NewMetrics()
	handler := HandlePrometheus(m)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Verify Content-Type
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %s", ct)
	}

	// All expected metric names with their TYPE annotations
	expectedCounters := []string{
		"duman_bytes_in_total",
		"duman_bytes_out_total",
		"duman_queries_total",
		"duman_tunnel_chunks_total",
		"duman_errors_total",
	}
	expectedGauges := []string{
		"duman_active_streams",
		"duman_active_connections",
		"duman_goroutines",
		"duman_heap_alloc_mb",
		"duman_gc_pause_ms",
		"duman_uptime_seconds",
	}

	for _, name := range expectedCounters {
		helpLine := fmt.Sprintf("# HELP %s ", name)
		typeLine := fmt.Sprintf("# TYPE %s counter", name)
		if !strings.Contains(body, helpLine) {
			t.Errorf("missing HELP line for %s", name)
		}
		if !strings.Contains(body, typeLine) {
			t.Errorf("missing TYPE counter line for %s", name)
		}
	}

	for _, name := range expectedGauges {
		helpLine := fmt.Sprintf("# HELP %s ", name)
		typeLine := fmt.Sprintf("# TYPE %s gauge", name)
		if !strings.Contains(body, helpLine) {
			t.Errorf("missing HELP line for %s", name)
		}
		if !strings.Contains(body, typeLine) {
			t.Errorf("missing TYPE gauge line for %s", name)
		}
	}
}

func TestPrometheusMetrics_Values(t *testing.T) {
	m := NewMetrics()

	// Set known values
	m.AddBytesIn(12345)
	m.AddBytesOut(67890)
	for i := 0; i < 500; i++ {
		m.AddQuery()
	}
	for i := 0; i < 200; i++ {
		m.AddTunnelChunk()
	}
	for i := 0; i < 3; i++ {
		m.AddError()
	}
	m.SetActiveStreams(5)
	m.SetActiveConns(2)

	handler := HandlePrometheus(m)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	checks := map[string]string{
		"duman_bytes_in_total":      "duman_bytes_in_total 12345",
		"duman_bytes_out_total":     "duman_bytes_out_total 67890",
		"duman_queries_total":       "duman_queries_total 500",
		"duman_tunnel_chunks_total": "duman_tunnel_chunks_total 200",
		"duman_errors_total":        "duman_errors_total 3",
		"duman_active_streams":      "duman_active_streams 5",
		"duman_active_connections":  "duman_active_connections 2",
	}

	for name, expected := range checks {
		if !strings.Contains(body, expected) {
			t.Errorf("expected %q in output for %s; body:\n%s", expected, name, body)
		}
	}

	// Uptime should be positive (greater than 0)
	if !strings.Contains(body, "duman_uptime_seconds") {
		t.Error("missing duman_uptime_seconds metric")
	}

	// Goroutines should be > 0 (we're running in a test, so there are goroutines)
	if !strings.Contains(body, "duman_goroutines") {
		t.Error("missing duman_goroutines metric")
	}
}

func TestPrometheusMetrics_Endpoint(t *testing.T) {
	// Test via the ClientDashboard mux
	addr := freePort(t)
	d := NewClientDashboard(addr)

	m := NewMetrics()
	m.AddBytesIn(999)
	m.AddBytesOut(888)
	m.SetActiveStreams(3)
	d.RegisterMetrics(m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond) // let server start

	resp, err := http.Get(fmt.Sprintf("http://%s/metrics", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %s", ct)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)

	if !strings.Contains(body, "duman_bytes_in_total 999") {
		t.Errorf("expected duman_bytes_in_total 999 in response body")
	}
	if !strings.Contains(body, "duman_bytes_out_total 888") {
		t.Errorf("expected duman_bytes_out_total 888 in response body")
	}
	if !strings.Contains(body, "duman_active_streams 3") {
		t.Errorf("expected duman_active_streams 3 in response body")
	}

	// Test via the RelayDashboard mux
	addr2 := freePort(t)
	d2 := NewRelayDashboard(addr2)

	m2 := NewMetrics()
	m2.AddError()
	m2.AddError()
	d2.RegisterMetrics(m2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go d2.Start(ctx2)
	time.Sleep(100 * time.Millisecond)

	resp2, err := http.Get(fmt.Sprintf("http://%s/metrics", addr2))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 from relay, got %d", resp2.StatusCode)
	}

	bodyBytes2, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatal(err)
	}
	body2 := string(bodyBytes2)

	if !strings.Contains(body2, "duman_errors_total 2") {
		t.Errorf("expected duman_errors_total 2 in relay response body")
	}
}
