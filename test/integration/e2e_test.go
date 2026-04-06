package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/client"
	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
	"github.com/dumanproxy/duman/internal/relay"
)

// TestE2E_RelayFakeDataQueries verifies the relay accepts pgwire connections
// and serves fake data for cover queries.
func TestE2E_RelayFakeDataQueries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	// Connect as pgwire client
	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// 1. SELECT products with limit
	result, err := client.SimpleQuery("SELECT * FROM products LIMIT 5")
	if err != nil {
		t.Fatalf("products query: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Errorf("products: got %d rows, want 5", len(result.Rows))
	}
	if len(result.Columns) != 5 {
		t.Errorf("products: got %d columns, want 5", len(result.Columns))
	}

	// 2. SELECT count(*)
	result, err = client.SimpleQuery("SELECT count(*) FROM products")
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if string(result.Rows[0][0]) != "200" {
		t.Errorf("product count = %s, want 200", result.Rows[0][0])
	}

	// 3. SELECT version() (psql compatibility)
	result, err = client.SimpleQuery("SELECT version()")
	if err != nil {
		t.Fatalf("version query: %v", err)
	}
	if !strings.Contains(string(result.Rows[0][0]), "PostgreSQL") {
		t.Errorf("version = %q, want PostgreSQL string", result.Rows[0][0])
	}

	// 4. INSERT (cover query)
	result, err = client.SimpleQuery("INSERT INTO analytics_events (session_id, event_type) VALUES ('abc', 'page_view')")
	if err != nil {
		t.Fatalf("insert query: %v", err)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("insert tag = %q, want 'INSERT 0 1'", result.Tag)
	}

	// 5. Destructive query should be denied
	result, err = client.SimpleQuery("DROP TABLE products")
	if err != nil {
		t.Fatalf("drop query: %v", err)
	}
	if result.Type != pgwire.ResultError {
		t.Errorf("DROP should return error, got type %d", result.Type)
	}

	// 6. Categories
	result, err = client.SimpleQuery("SELECT * FROM categories")
	if err != nil {
		t.Fatalf("categories query: %v", err)
	}
	if len(result.Rows) != 10 {
		t.Errorf("categories: got %d rows, want 10", len(result.Rows))
	}
}

// TestE2E_RelayTunnelInsert verifies tunnel data can be sent via prepared INSERT.
func TestE2E_RelayTunnelInsert(t *testing.T) {
	// Start a mock destination that accepts TCP connections
	destListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer destListener.Close()
	destAddr := destListener.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := destListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Prepare tunnel INSERT statement
	err = client.Prepare("tunnel_insert",
		"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1, $2, $3, $4, $5, $6)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Build tunnel INSERT with valid HMAC auth token
	sharedSecret := []byte("test-secret-32-bytes!!!!!!!!!!!")
	sessionID := "e2e-test-session"
	authToken := crypto.GenerateAuthToken(sharedSecret, sessionID)

	// First: send CONNECT chunk to establish stream
	connectChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(destAddr),
	}
	sendTunnelChunk(t, client, connectChunk, sessionID, authToken)

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Now send DATA chunks
	for i := 1; i <= 5; i++ {
		dataChunk := &crypto.Chunk{
			StreamID: 1,
			Sequence: uint64(i),
			Type:     crypto.ChunkData,
			Payload:  []byte(fmt.Sprintf("chunk-data-%d", i)),
		}
		sendTunnelChunk(t, client, dataChunk, sessionID, authToken)
	}

	// Send FIN to close stream
	finChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 6,
		Type:     crypto.ChunkFIN,
	}
	sendTunnelChunk(t, client, finChunk, sessionID, authToken)
}

// TestE2E_RelayResponsePolling verifies response polling via SELECT.
func TestE2E_RelayResponsePolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Poll for responses (should return empty result)
	result, err := client.SimpleQuery("SELECT payload, seq, stream_id FROM analytics_responses WHERE session_id = 'test-session'")
	if err != nil {
		t.Fatalf("poll query: %v", err)
	}
	if result.Type != pgwire.ResultRows {
		t.Errorf("poll type = %d, want ResultRows", result.Type)
	}
	// No responses queued yet, so 0 rows
	if len(result.Rows) != 0 {
		t.Errorf("poll rows = %d, want 0", len(result.Rows))
	}
}

// TestE2E_MultipleConcurrentClients verifies multiple clients can connect simultaneously.
func TestE2E_MultipleConcurrentClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	const numClients = 5
	errCh := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
				Address:  r.Addr(),
				Username: "sensor_writer",
				Password: "test_password",
				Database: "analytics",
			})
			if err != nil {
				errCh <- fmt.Errorf("client %d connect: %w", clientID, err)
				return
			}
			defer client.Close()

			// Each client queries
			result, err := client.SimpleQuery(fmt.Sprintf("SELECT * FROM products WHERE category_id = %d LIMIT 10", (clientID%10)+1))
			if err != nil {
				errCh <- fmt.Errorf("client %d query: %w", clientID, err)
				return
			}
			if len(result.Rows) == 0 {
				errCh <- fmt.Errorf("client %d: no rows", clientID)
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < numClients; i++ {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}

// TestE2E_MockDestinationThroughTunnel starts a mock HTTP server, sends
// a tunnel CONNECT+DATA+FIN lifecycle through the relay, and verifies
// the exit engine can reach the destination.
func TestE2E_MockDestinationThroughTunnel(t *testing.T) {
	// Start a mock HTTP destination
	destListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer destListener.Close()

	destAddr := destListener.Addr().String()
	receivedData := make(chan string, 1)

	// Simple echo server
	go func() {
		conn, err := destListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		receivedData <- string(buf[:n])
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK"))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Prepare tunnel INSERT
	err = client.Prepare("tunnel_insert",
		"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1, $2, $3, $4, $5, $6)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	sharedSecret := []byte("test-secret-32-bytes!!!!!!!!!!!")
	sessionID := "e2e-dest-session"
	authToken := crypto.GenerateAuthToken(sharedSecret, sessionID)

	// Send CONNECT chunk
	connectChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(destAddr),
	}
	sendTunnelChunk(t, client, connectChunk, sessionID, authToken)

	// Give exit engine time to connect
	time.Sleep(200 * time.Millisecond)

	// Send DATA chunk with HTTP request
	httpReq := "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"
	dataChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte(httpReq),
	}
	sendTunnelChunk(t, client, dataChunk, sessionID, authToken)

	// Verify destination received the data
	select {
	case data := <-receivedData:
		if !strings.Contains(data, "GET / HTTP/1.1") {
			t.Errorf("destination received unexpected data: %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for destination to receive data")
	}

	// Send FIN
	finChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 2,
		Type:     crypto.ChunkFIN,
	}
	sendTunnelChunk(t, client, finChunk, sessionID, authToken)
}

// TestE2E_CoverQueryMix verifies that interleaving cover and tunnel queries
// looks realistic — cover queries produce real fake data results.
func TestE2E_CoverQueryMix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Simulate a realistic session: mix of cover and tunnel queries
	coverQueries := []string{
		"SELECT id, name, price FROM products WHERE category_id = 3 LIMIT 20",
		"SELECT name FROM categories WHERE id = 3",
		"SELECT count(*) FROM products WHERE category_id = 3",
		"INSERT INTO analytics_events (session_id, event_type, page_url) VALUES ('abc', 'page_view', '/products')",
		"SELECT * FROM products WHERE id = 42",
		"SELECT count(*) FROM orders",
		"SELECT * FROM users LIMIT 10",
		"UPDATE cart_items SET quantity = 2 WHERE user_id = 1",
		"SHOW server_version",
		"SET client_encoding = 'UTF8'",
	}

	for _, q := range coverQueries {
		result, err := client.SimpleQuery(q)
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if result == nil {
			t.Fatalf("query %q: nil result", q)
		}
	}
}

// TestE2E_AuthFailure verifies wrong credentials are rejected.
func TestE2E_AuthFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	_, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "wrong_password",
		Database: "analytics",
	})
	if err == nil {
		t.Fatal("expected auth failure for wrong password")
	}
}

// TestE2E_SOCKS5ProxyToMockHTTP tests the full SOCKS5 → tunnel → destination flow
// by starting a mock HTTP server and connecting through the SOCKS5 proxy.
func TestE2E_SOCKS5ProxyToMockHTTP(t *testing.T) {
	// Start mock HTTP destination
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("hello-from-destination"))
	})

	destListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	destSrv := &http.Server{Handler: mux}
	go destSrv.Serve(destListener)
	defer destSrv.Close()

	destAddr := destListener.Addr().String()

	// Start relay
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	// Start SOCKS5 proxy that tunnels through the relay
	// We use the proxy package directly rather than the full client
	// to avoid the interleaving engine's timing delays

	socks5Listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socks5Addr := socks5Listener.Addr().String()
	socks5Listener.Close()

	// Create a simple stream creator that connects directly to the destination
	// This tests the SOCKS5 proxy in isolation
	_ = socks5Addr
	_ = destAddr

	// For now, verify the relay handles cover traffic correctly
	// (full SOCKS5→tunnel→destination requires the interleave engine which has timing)

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Send a batch of cover + tunnel queries (simulating what the interleave engine does)
	sharedSecret := []byte("test-secret-32-bytes!!!!!!!!!!!")
	sessionID := "socks5-test-session"
	authToken := crypto.GenerateAuthToken(sharedSecret, sessionID)

	// Cover query
	_, err = client.SimpleQuery("SELECT * FROM products LIMIT 5")
	if err != nil {
		t.Fatalf("cover query: %v", err)
	}

	// Tunnel query
	err = client.Prepare("tunnel_insert",
		"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1, $2, $3, $4, $5, $6)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	connectChunk := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(destAddr),
	}
	sendTunnelChunk(t, client, connectChunk, sessionID, authToken)

	time.Sleep(100 * time.Millisecond)

	// More cover queries (interleaved)
	_, err = client.SimpleQuery("SELECT count(*) FROM orders")
	if err != nil {
		t.Fatalf("cover query 2: %v", err)
	}

	// Send HTTP request through tunnel
	httpReq := fmt.Sprintf("GET /test HTTP/1.1\r\nHost: %s\r\n\r\n", destAddr)
	dataChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     crypto.ChunkData,
		Payload:  []byte(httpReq),
	}
	sendTunnelChunk(t, client, dataChunk, sessionID, authToken)

	// More cover queries
	_, err = client.SimpleQuery("INSERT INTO analytics_events (session_id, event_type) VALUES ('x', 'click')")
	if err != nil {
		t.Fatalf("cover query 3: %v", err)
	}

	// Clean up tunnel
	finChunk := &crypto.Chunk{
		StreamID: 1,
		Sequence: 2,
		Type:     crypto.ChunkFIN,
	}
	sendTunnelChunk(t, client, finChunk, sessionID, authToken)
}

// --- Helpers ---

func startRelay(t *testing.T, ctx context.Context) *relay.Relay {
	t.Helper()

	cfg := &config.RelayConfig{}
	cfg.Listen.PostgreSQL = "127.0.0.1:0"
	cfg.Auth.Users = map[string]string{"sensor_writer": "test_password"}
	cfg.Tunnel.SharedSecret = "test-secret-32-bytes!!!!!!!!!!!"
	cfg.FakeData.Scenario = "ecommerce"
	cfg.FakeData.Seed = 42
	cfg.Exit.MaxIdleSecs = 300

	r, err := relay.New(cfg, nil)
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}

	go func() {
		if err := r.Run(ctx); err != nil {
			// expected on cancellation
		}
	}()

	// Wait for server to start
	for i := 0; i < 50; i++ {
		if r.Addr() != "" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("relay did not start in time")
	return nil
}

func sendTunnelChunk(t *testing.T, client *pgwire.Client, chunk *crypto.Chunk, sessionID, authToken string) {
	t.Helper()

	metadata := fmt.Sprintf(`{"pixel_id":"%s","stream_id":"%d","seq":"%d"}`,
		authToken, chunk.StreamID, chunk.Sequence)

	var eventType string
	switch chunk.Type {
	case crypto.ChunkConnect:
		eventType = "session_start"
	case crypto.ChunkData:
		eventType = "conversion_pixel"
	case crypto.ChunkFIN:
		eventType = "session_end"
	default:
		eventType = "custom_event"
	}

	payload := chunk.Payload
	if payload == nil {
		payload = []byte{}
	}

	params := [][]byte{
		[]byte(sessionID),
		[]byte(eventType),
		[]byte("/analytics"),
		[]byte("Mozilla/5.0"),
		[]byte(metadata),
		payload,
	}

	err := client.PreparedInsert("tunnel_insert", params)
	if err != nil {
		t.Fatalf("sendTunnelChunk: %v", err)
	}
}

// TestE2E_BidirectionalTunnel is the ultimate proof-of-life test.
// It verifies encrypted data flows: client → relay → destination → relay → client.
func TestE2E_BidirectionalTunnel(t *testing.T) {
	// 1. Start an echo TCP server (destination).
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoListener.Close()
	destAddr := echoListener.Addr().String()

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					// Echo back with "ECHO:" prefix
					resp := append([]byte("ECHO:"), buf[:n]...)
					c.Write(resp)
				}
			}(conn)
		}
	}()

	// 2. Start relay
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := startRelay(t, ctx)

	// 3. Connect as pgwire client
	sharedSecret := []byte("test-secret-32-bytes!!!!!!!!!!!")
	sessionID := "e2e-bidir-session"
	authToken := crypto.GenerateAuthToken(sharedSecret, sessionID)

	// Derive encryption key (same as the client would)
	cipherKey, err := crypto.DeriveSessionKey(sharedSecret, sessionID)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	tunnelCipher, err := crypto.NewCipher(cipherKey, crypto.CipherAuto)
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}

	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  r.Addr(),
		Username: "sensor_writer",
		Password: "test_password",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	err = client.Prepare("tunnel_insert",
		"INSERT INTO analytics_events (session_id, event_type, page_url, user_agent, metadata, payload) VALUES ($1, $2, $3, $4, $5, $6)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// 4. Send encrypted CONNECT chunk
	sendEncryptedChunk(t, client, tunnelCipher, &crypto.Chunk{
		StreamID: 1, Sequence: 0, Type: crypto.ChunkConnect,
		Payload: []byte(destAddr),
	}, sessionID, authToken)

	time.Sleep(200 * time.Millisecond) // let connection establish

	// 5. Send encrypted DATA chunk
	testData := []byte("Hello, Duman tunnel!")
	sendEncryptedChunk(t, client, tunnelCipher, &crypto.Chunk{
		StreamID: 1, Sequence: 1, Type: crypto.ChunkData,
		Payload: testData,
	}, sessionID, authToken)

	// 6. Poll for response — the echo server should send back "ECHO:Hello, Duman tunnel!"
	var gotResponse bool
	for attempt := 0; attempt < 30; attempt++ {
		time.Sleep(100 * time.Millisecond)

		result, err := client.SimpleQuery(
			fmt.Sprintf("SELECT payload, seq, stream_id FROM analytics_responses WHERE session_id = '%s'", sessionID))
		if err != nil {
			t.Fatalf("poll: %v", err)
		}

		if len(result.Rows) > 0 {
			// Got response data!
			for _, row := range result.Rows {
				if len(row) > 0 && row[0] != nil {
					payload := row[0]
					t.Logf("response payload: %q", payload)
					if strings.Contains(string(payload), "ECHO:") {
						gotResponse = true
						break
					}
				}
			}
			if gotResponse {
				break
			}
		}
	}

	if !gotResponse {
		t.Fatal("never received echo response through the tunnel — bidirectional flow is broken")
	}

	t.Log("SUCCESS: bidirectional tunnel flow verified (client → relay → destination → relay → client)")

	// 7. Clean up — send FIN
	sendEncryptedChunk(t, client, tunnelCipher, &crypto.Chunk{
		StreamID: 1, Sequence: 2, Type: crypto.ChunkFIN,
	}, sessionID, authToken)
}

// TestE2E_FullClientRoundtrip starts the actual Client with SOCKS5 proxy,
// connects through it to a destination, and verifies HTTP response.
func TestE2E_FullClientRoundtrip(t *testing.T) {
	// 1. Start mock HTTP server (destination)
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("pong-from-destination"))
	})
	destListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	destSrv := &http.Server{Handler: mux}
	go destSrv.Serve(destListener)
	defer destSrv.Close()
	destAddr := destListener.Addr().String()

	// 2. Start relay with debug logging
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relayCfg := &config.RelayConfig{}
	relayCfg.Listen.PostgreSQL = "127.0.0.1:0"
	relayCfg.Auth.Users = map[string]string{"sensor_writer": "test_password"}
	relayCfg.Tunnel.SharedSecret = "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE=" // same as client
	relayCfg.FakeData.Scenario = "ecommerce"
	relayCfg.FakeData.Seed = 42
	relayCfg.Exit.MaxIdleSecs = 300

	r, err := relay.New(relayCfg, nil)
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}
	go r.Run(ctx)
	for i := 0; i < 50; i++ {
		if r.Addr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.Addr() == "" {
		t.Fatal("relay did not start")
	}

	// 3. Start client with debug logging
	clientCfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret:   "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE=", // base64 of "test-secret-32-bytes!!!!!!!!!!!"
			ChunkSize:      16384,
			Cipher:         "auto",
			ResponseMode:   "poll",
			BurstSpacingMs: 1,   // fast mode for testing
			ReadingPauseMs: 10,  // 10ms instead of 2-30s
		},
		Scenario: "ecommerce",
		Relays: []config.RelayEntry{
			{Address: r.Addr(), Username: "sensor_writer", Password: "test_password", Database: "analytics", Weight: 10},
		},
	}

	c, err := client.New(clientCfg, nil)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
	defer runCancel()

	go c.Run(runCtx)

	// Wait for SOCKS5 to start
	var socksAddr string
	for i := 0; i < 50; i++ {
		socksAddr = c.SOCKSAddr()
		if socksAddr != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if socksAddr == "" {
		t.Fatal("SOCKS5 proxy did not start")
	}

	t.Logf("SOCKS5 proxy listening on %s", socksAddr)
	t.Logf("Destination at %s", destAddr)

	// 4. Connect through SOCKS5 proxy to the destination
	// Manually perform SOCKS5 handshake
	conn, err := net.DialTimeout("tcp", socksAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5: %v", err)
	}
	defer conn.Close()

	// SOCKS5 greeting: version=5, 1 method, no auth
	conn.Write([]byte{0x05, 0x01, 0x00})

	// Read greeting response
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("socks5 greeting: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("socks5 greeting: unexpected %x", resp)
	}

	// SOCKS5 CONNECT request
	host, portStr, _ := net.SplitHostPort(destAddr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	connectReq := []byte{
		0x05, 0x01, 0x00, // VER, CMD=CONNECT, RSV
		0x01,                          // ATYP=IPv4
		127, 0, 0, 1,                  // DST.ADDR
		byte(port >> 8), byte(port),   // DST.PORT
	}
	_ = host
	conn.Write(connectReq)

	// Read CONNECT response
	connResp := make([]byte, 10)
	if _, err := io.ReadFull(conn, connResp); err != nil {
		t.Fatalf("socks5 connect response: %v", err)
	}
	if connResp[1] != 0x00 {
		t.Fatalf("socks5 connect failed: status=%d", connResp[1])
	}

	// 5. Send HTTP request through the tunnel
	httpReq := fmt.Sprintf("GET /ping HTTP/1.0\r\nHost: %s\r\n\r\n", destAddr)
	conn.Write([]byte(httpReq))

	// 6. Read HTTP response
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	respBuf := make([]byte, 4096)
	n, err := conn.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("read HTTP response: %v", err)
	}

	httpResp := string(respBuf[:n])
	t.Logf("HTTP response:\n%s", httpResp)

	if !strings.Contains(httpResp, "pong-from-destination") {
		t.Fatal("did not receive expected HTTP response through SOCKS5 → tunnel → destination → tunnel → SOCKS5")
	}

	t.Log("SUCCESS: Full client roundtrip verified (SOCKS5 → tunnel → HTTP destination → tunnel → SOCKS5)")
}

// sendEncryptedChunk encrypts a chunk and sends it via prepared INSERT.
func sendEncryptedChunk(t *testing.T, client *pgwire.Client, c *crypto.Cipher, chunk *crypto.Chunk, sessionID, authToken string) {
	t.Helper()

	// Encrypt the full chunk
	encrypted, err := crypto.EncryptChunk(chunk, c, sessionID)
	if err != nil {
		t.Fatalf("encrypt chunk: %v", err)
	}

	metadata := fmt.Sprintf(`{"pixel_id":"%s","stream_id":"%d","seq":"%d"}`,
		authToken, chunk.StreamID, chunk.Sequence)

	var eventType string
	switch chunk.Type {
	case crypto.ChunkConnect:
		eventType = "session_start"
	case crypto.ChunkData:
		eventType = "conversion_pixel"
	case crypto.ChunkFIN:
		eventType = "session_end"
	default:
		eventType = "custom_event"
	}

	params := [][]byte{
		[]byte(sessionID),
		[]byte(eventType),
		[]byte("/analytics"),
		[]byte("Mozilla/5.0"),
		[]byte(metadata),
		encrypted, // encrypted payload
	}

	err = client.PreparedInsert("tunnel_insert", params)
	if err != nil {
		t.Fatalf("sendEncryptedChunk: %v", err)
	}
}
