package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func runSpeedtest(socksAddr string) {
	if socksAddr == "" {
		socksAddr = "127.0.0.1:1080"
	}

	fmt.Println("=== DUMAN TUNNEL SPEEDTEST ===")
	fmt.Printf("SOCKS5 proxy: %s\n\n", socksAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start a local download server
	dlAddr := startLocalServer(ctx, "download")
	ulAddr := startLocalServer(ctx, "upload")

	fmt.Printf("Download server: %s\n", dlAddr)
	fmt.Printf("Upload server:   %s\n\n", ulAddr)

	// Download test
	fmt.Println("--- DOWNLOAD (5s) ---")
	dlBytes := transfer(socksAddr, dlAddr, "download", 5*time.Second)
	dlSpeed := float64(dlBytes) / 5.0

	fmt.Println()

	// Upload test
	fmt.Println("--- UPLOAD (5s) ---")
	ulBytes := transfer(socksAddr, ulAddr, "upload", 5*time.Second)
	ulSpeed := float64(ulBytes) / 5.0

	fmt.Println()
	fmt.Println("=== RESULTS ===")
	fmt.Printf("Download: %s/s (%s total)\n", bytesStr(int64(dlSpeed)), bytesStr(dlBytes))
	fmt.Printf("Upload:   %s/s (%s total)\n", bytesStr(int64(ulSpeed)), bytesStr(ulBytes))
}

func transfer(socksAddr, destAddr, direction string, duration time.Duration) int64 {
	conn, err := socks5Dial(socksAddr, destAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  connection failed: %v\n", err)
		return 0
	}
	defer conn.Close()

	var total atomic.Int64
	done := make(chan struct{})

	// Progress reporter
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Seconds()
				b := total.Load()
				speed := float64(b) / elapsed
				fmt.Printf("  [%4.1fs] %s transferred, %s/s\n",
					elapsed, bytesStr(b), bytesStr(int64(speed)))
			}
		}
	}()

	deadline := time.Now().Add(duration)
	conn.SetDeadline(deadline)

	buf := make([]byte, 64*1024)

	switch direction {
	case "download":
		conn.Write([]byte("START\n"))
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				total.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
	case "upload":
		rand.Read(buf)
		conn.Write([]byte("UPLOAD\n"))
		for {
			n, err := conn.Write(buf)
			if n > 0 {
				total.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
	}

	close(done)
	return total.Load()
}

func startLocalServer(ctx context.Context, mode string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				cmd := make([]byte, 64)
				n, err := c.Read(cmd)
				if err != nil {
					return
				}
				command := strings.TrimSpace(string(cmd[:n]))
				if command == "START" || mode == "download" {
					buf := make([]byte, 64*1024)
					rand.Read(buf)
					for {
						select {
						case <-ctx.Done():
							return
						default:
						}
						if _, err := c.Write(buf); err != nil {
							return
						}
					}
				} else {
					io.Copy(io.Discard, c)
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func socks5Dial(socksAddr, destAddr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", socksAddr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial SOCKS5: %w", err)
	}

	// Greeting
	conn.Write([]byte{0x05, 0x01, 0x00})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting failed: %x", resp)
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
		conn.Close()
		return nil, fmt.Errorf("socks5 connect: %w", err)
	}
	if connResp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: status=%d", connResp[1])
	}

	return conn, nil
}

func bytesStr(b int64) string {
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
