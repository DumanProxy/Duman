package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// --- Client Dashboard Tests ---

func TestClientDashboard_Index(t *testing.T) {
	addr := freePort(t)
	d := NewClientDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond) // let server start

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %s", ct)
	}

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "Duman Client") {
		t.Fatal("HTML does not contain expected title")
	}
	if !strings.Contains(body, "/events") {
		t.Fatal("HTML does not reference SSE endpoint")
	}
}

func TestClientDashboard_StatsAPI(t *testing.T) {
	addr := freePort(t)
	d := NewClientDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Update stats
	d.UpdateStats(ClientStats{
		Relays: []RelayStatus{
			{Address: "relay1.example.com:443", Protocol: "https", Status: "healthy", Latency: 15 * time.Millisecond},
		},
		TunnelStreams: 5,
		Throughput:    1024.0,
		CoverRate:     2.5,
		BandwidthAlloc: map[string]float64{
			"relay1": 0.6,
			"relay2": 0.4,
		},
		NoiseStatus: map[string]bool{
			"cover_queries": true,
			"fake_inserts":  false,
		},
		StartTime: time.Now().Add(-10 * time.Minute),
	})

	resp, err := http.Get(fmt.Sprintf("http://%s/api/stats", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var stats ClientStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}

	if stats.TunnelStreams != 5 {
		t.Fatalf("expected 5 streams, got %d", stats.TunnelStreams)
	}
	if len(stats.Relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(stats.Relays))
	}
	if stats.Relays[0].Status != "healthy" {
		t.Fatalf("expected healthy, got %s", stats.Relays[0].Status)
	}
	if stats.Throughput != 1024.0 {
		t.Fatalf("expected throughput 1024, got %f", stats.Throughput)
	}
	if stats.Uptime <= 0 {
		t.Fatal("expected positive uptime")
	}
}

func TestClientDashboard_SSE(t *testing.T) {
	addr := freePort(t)
	d := NewClientDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Update stats before connecting SSE
	d.UpdateStats(ClientStats{
		TunnelStreams: 42,
		Throughput:    9999.0,
		StartTime:    time.Now(),
		BandwidthAlloc: map[string]float64{},
		NoiseStatus:    map[string]bool{},
	})

	// Connect to SSE
	resp, err := http.Get(fmt.Sprintf("http://%s/events", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	// Read one SSE event (should arrive within ~2 seconds)
	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(5 * time.Second)
	got := false

	for {
		select {
		case <-deadline:
			if !got {
				t.Fatal("timeout waiting for SSE event")
			}
			return
		default:
		}

		// Use a channel to handle scanner in non-blocking way
		ch := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				ch <- scanner.Text()
			}
		}()

		select {
		case <-deadline:
			if !got {
				t.Fatal("timeout waiting for SSE event")
			}
			return
		case line := <-ch:
			if strings.HasPrefix(line, "data: ") {
				data := line[6:]
				var stats ClientStats
				if err := json.Unmarshal([]byte(data), &stats); err != nil {
					t.Fatalf("failed to parse SSE data: %v", err)
				}
				if stats.TunnelStreams != 42 {
					t.Fatalf("expected 42 streams in SSE, got %d", stats.TunnelStreams)
				}
				got = true
				return
			}
		}
	}
}

func TestClientDashboard_UpdateStatsReflected(t *testing.T) {
	addr := freePort(t)
	d := NewClientDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Initial check
	resp, err := http.Get(fmt.Sprintf("http://%s/api/stats", addr))
	if err != nil {
		t.Fatal(err)
	}
	var stats1 ClientStats
	json.NewDecoder(resp.Body).Decode(&stats1)
	resp.Body.Close()

	if stats1.TunnelStreams != 0 {
		t.Fatalf("expected 0 initial streams, got %d", stats1.TunnelStreams)
	}

	// Update
	d.UpdateStats(ClientStats{
		TunnelStreams:  100,
		StartTime:     time.Now(),
		BandwidthAlloc: map[string]float64{},
		NoiseStatus:    map[string]bool{},
	})

	resp, err = http.Get(fmt.Sprintf("http://%s/api/stats", addr))
	if err != nil {
		t.Fatal(err)
	}
	var stats2 ClientStats
	json.NewDecoder(resp.Body).Decode(&stats2)
	resp.Body.Close()

	if stats2.TunnelStreams != 100 {
		t.Fatalf("expected 100 streams after update, got %d", stats2.TunnelStreams)
	}
}

// --- Relay Dashboard Tests ---

func TestRelayDashboard_Index(t *testing.T) {
	addr := freePort(t)
	d := NewRelayDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "Duman Relay") {
		t.Fatal("HTML does not contain expected title")
	}
}

func TestRelayDashboard_StatsAPI(t *testing.T) {
	addr := freePort(t)
	d := NewRelayDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	d.UpdateStats(RelayStats{
		ClientCount:      10,
		SessionDurations: []time.Duration{5 * time.Minute, 10 * time.Minute},
		TunnelThroughput: 50000.0,
		CoverQueryRate:   3.7,
		ExitPoolSize:     5,
		FakeEngineStats: map[string]int{
			"analytics_events": 1000,
			"user_sessions":    500,
		},
		StartTime: time.Now().Add(-1 * time.Hour),
	})

	resp, err := http.Get(fmt.Sprintf("http://%s/api/stats", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var stats RelayStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}

	if stats.ClientCount != 10 {
		t.Fatalf("expected 10 clients, got %d", stats.ClientCount)
	}
	if stats.ExitPoolSize != 5 {
		t.Fatalf("expected exit pool 5, got %d", stats.ExitPoolSize)
	}
	if stats.FakeEngineStats["analytics_events"] != 1000 {
		t.Fatal("fake engine stats mismatch")
	}
}

func TestRelayDashboard_SSE(t *testing.T) {
	addr := freePort(t)
	d := NewRelayDashboard(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	d.UpdateStats(RelayStats{
		ClientCount:     7,
		StartTime:       time.Now(),
		FakeEngineStats: map[string]int{},
	})

	resp, err := http.Get(fmt.Sprintf("http://%s/events", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	scanner := bufio.NewScanner(resp.Body)
	deadline := time.After(5 * time.Second)

	for {
		ch := make(chan string, 1)
		go func() {
			if scanner.Scan() {
				ch <- scanner.Text()
			}
		}()

		select {
		case <-deadline:
			t.Fatal("timeout waiting for SSE event")
			return
		case line := <-ch:
			if strings.HasPrefix(line, "data: ") {
				var stats RelayStats
				if err := json.Unmarshal([]byte(line[6:]), &stats); err != nil {
					t.Fatalf("failed to parse SSE data: %v", err)
				}
				if stats.ClientCount != 7 {
					t.Fatalf("expected 7 clients in SSE, got %d", stats.ClientCount)
				}
				return
			}
		}
	}
}

func TestClientDashboard_DefaultAddr(t *testing.T) {
	d := NewClientDashboard("")
	if d.Addr() != "127.0.0.1:9090" {
		t.Fatalf("expected default addr 127.0.0.1:9090, got %s", d.Addr())
	}
}

func TestRelayDashboard_DefaultAddr(t *testing.T) {
	d := NewRelayDashboard("")
	if d.Addr() != "127.0.0.1:9091" {
		t.Fatalf("expected default addr 127.0.0.1:9091, got %s", d.Addr())
	}
}
