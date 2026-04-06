package benchmark

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/client"
	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/relay"
)

// TestSpeedtest measures tunnel throughput end-to-end:
// SOCKS5 → interleave engine → pgwire → relay → exit engine → TCP dest
//
// Run with: go test ./test/benchmark/ -run TestSpeedtest -v -count=1 -timeout 60s
func TestSpeedtest(t *testing.T) {
	const (
		downloadDuration = 5 * time.Second
		uploadDuration   = 5 * time.Second
		reportInterval   = 1 * time.Second
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start download server (sends random data as fast as possible)
	dlServer := startDataServer(t, ctx, "download")
	// 2. Start upload server (receives and discards data)
	ulServer := startDataServer(t, ctx, "upload")

	// 3. Start relay
	r := startSpeedRelay(t, ctx)

	// 4. Start client
	c, socksAddr := startSpeedClient(t, ctx, r.Addr())
	_ = c

	t.Logf("=== DUMAN TUNNEL SPEEDTEST ===")
	t.Logf("SOCKS5:    %s", socksAddr)
	t.Logf("Relay:     %s", r.Addr())
	t.Logf("DL Server: %s", dlServer)
	t.Logf("UL Server: %s", ulServer)
	t.Logf("")

	// 5. Download test
	t.Logf("--- DOWNLOAD TEST (%s) ---", downloadDuration)
	dlBytes := runTransfer(t, socksAddr, dlServer, "download", downloadDuration, reportInterval)
	dlSpeed := float64(dlBytes) / downloadDuration.Seconds()
	t.Logf("Download: %s in %s = %s/s",
		fmtBytes(dlBytes), downloadDuration, fmtBytes(int64(dlSpeed)))

	// Small pause between tests
	time.Sleep(500 * time.Millisecond)

	// 6. Upload test
	t.Logf("")
	t.Logf("--- UPLOAD TEST (%s) ---", uploadDuration)
	ulBytes := runTransfer(t, socksAddr, ulServer, "upload", uploadDuration, reportInterval)
	ulSpeed := float64(ulBytes) / uploadDuration.Seconds()
	t.Logf("Upload: %s in %s = %s/s",
		fmtBytes(ulBytes), uploadDuration, fmtBytes(int64(ulSpeed)))

	// 7. Summary
	t.Logf("")
	t.Logf("=== RESULTS ===")
	t.Logf("Download: %s/s (%s total)", fmtBytes(int64(dlSpeed)), fmtBytes(dlBytes))
	t.Logf("Upload:   %s/s (%s total)", fmtBytes(int64(ulSpeed)), fmtBytes(ulBytes))
}

// runTransfer connects through SOCKS5 and transfers data in the given direction.
func runTransfer(t *testing.T, socksAddr, destAddr, direction string, duration, reportInterval time.Duration) int64 {
	t.Helper()

	conn := socks5Connect(t, socksAddr, destAddr)
	defer conn.Close()

	var totalBytes atomic.Int64
	done := make(chan struct{})

	// Progress reporter
	go func() {
		ticker := time.NewTicker(reportInterval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Seconds()
				bytes := totalBytes.Load()
				speed := float64(bytes) / elapsed
				t.Logf("  [%4.1fs] %s transferred, %s/s",
					elapsed, fmtBytes(bytes), fmtBytes(int64(speed)))
			}
		}
	}()

	deadline := time.Now().Add(duration)
	conn.SetDeadline(deadline)

	buf := make([]byte, 64*1024) // 64KB buffer

	switch direction {
	case "download":
		// Tell server to start sending
		conn.Write([]byte("START\n"))
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				totalBytes.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
	case "upload":
		// Fill buffer with random data
		rand.Read(buf)
		// Tell server we're uploading
		conn.Write([]byte("UPLOAD\n"))
		for {
			n, err := conn.Write(buf)
			if n > 0 {
				totalBytes.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
	}

	close(done)
	return totalBytes.Load()
}

// startDataServer starts a TCP server for speed testing.
// "download" mode: sends random data continuously after receiving START.
// "upload" mode: reads and discards everything.
func startDataServer(t *testing.T, ctx context.Context, mode string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				// Read initial command
				cmd := make([]byte, 64)
				n, err := c.Read(cmd)
				if err != nil {
					return
				}
				command := strings.TrimSpace(string(cmd[:n]))

				switch {
				case command == "START" || mode == "download":
					// Send random data as fast as possible
					buf := make([]byte, 64*1024)
					rand.Read(buf)
					for {
						select {
						case <-ctx.Done():
							return
						default:
						}
						_, err := c.Write(buf)
						if err != nil {
							return
						}
					}
				default:
					// Upload mode: just read and discard
					io.Copy(io.Discard, c)
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

// socks5Connect performs a SOCKS5 handshake and returns the tunneled connection.
func socks5Connect(t *testing.T, socksAddr, destAddr string) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", socksAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5: %v", err)
	}

	// Greeting
	conn.Write([]byte{0x05, 0x01, 0x00})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("socks5 greeting: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("socks5 greeting failed: %x", resp)
	}

	// CONNECT
	host, portStr, _ := net.SplitHostPort(destAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	_ = host
	connectReq := []byte{
		0x05, 0x01, 0x00,
		0x01,
		127, 0, 0, 1,
		byte(port >> 8), byte(port),
	}
	conn.Write(connectReq)

	connResp := make([]byte, 10)
	if _, err := io.ReadFull(conn, connResp); err != nil {
		t.Fatalf("socks5 connect: %v", err)
	}
	if connResp[1] != 0x00 {
		t.Fatalf("socks5 connect failed: status=%d", connResp[1])
	}

	return conn
}

func startSpeedRelay(t *testing.T, ctx context.Context) *relay.Relay {
	t.Helper()

	cfg := &config.RelayConfig{}
	cfg.Listen.PostgreSQL = "127.0.0.1:0"
	cfg.Auth.Users = map[string]string{"sensor_writer": "test_password"}
	cfg.Tunnel.SharedSecret = "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE="
	cfg.FakeData.Scenario = "ecommerce"
	cfg.FakeData.Seed = 42
	cfg.Exit.MaxIdleSecs = 300

	r, err := relay.New(cfg, nil)
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}

	go r.Run(ctx)

	for i := 0; i < 50; i++ {
		if r.Addr() != "" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("relay did not start")
	return nil
}

func startSpeedClient(t *testing.T, ctx context.Context, relayAddr string) (*client.Client, string) {
	t.Helper()

	cfg := &config.ClientConfig{
		Proxy:    config.ProxyConfig{Listen: "127.0.0.1:0"},
		Scenario: "ecommerce",
		Tunnel: config.TunnelConfig{
			SharedSecret:   "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE=",
			ChunkSize:      16384,
			Cipher:         "auto",
			ResponseMode:   "poll",
			BurstSpacingMs: 1,
			ReadingPauseMs: 1,
		},
		Relays: []config.RelayEntry{
			{Address: relayAddr, Username: "sensor_writer", Password: "test_password", Database: "analytics", Weight: 10},
		},
	}

	c, err := client.New(cfg, nil)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	go c.Run(ctx)

	var socksAddr string
	for i := 0; i < 100; i++ {
		socksAddr = c.SOCKSAddr()
		if socksAddr != "" {
			return c, socksAddr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("SOCKS5 proxy did not start")
	return nil, ""
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
