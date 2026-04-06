package client

import (
	"context"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/relay"
	"github.com/dumanproxy/duman/internal/tunnel"
)

func TestNew(t *testing.T) {
	cfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret: "dGVzdC1zZWNyZXQ=", // base64("test-secret")
			ChunkSize:    16384,
			Cipher:       "auto",
			ResponseMode: "poll",
		},
		Scenario: "ecommerce",
		Relays: []config.RelayEntry{
			{Address: "127.0.0.1:5432", Username: "test", Password: "pass", Database: "db"},
		},
	}

	c, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionID() == "" {
		t.Error("expected non-empty session ID")
	}
	if c.SOCKSAddr() != "" {
		t.Error("expected empty SOCKS addr before Run")
	}
}

func TestNew_RawSecret(t *testing.T) {
	cfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret: "raw-secret-not-base64",
			ChunkSize:    16384,
			Cipher:       "auto",
			ResponseMode: "poll",
		},
		Scenario: "ecommerce",
	}

	c, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNew_NoRelays(t *testing.T) {
	cfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret: "secret",
			ChunkSize:    16384,
			Cipher:       "auto",
			ResponseMode: "poll",
		},
		Scenario: "ecommerce",
	}

	c, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := generateSessionID()
	if len(id) < 32 {
		t.Errorf("session ID too short: %q", id)
	}

	// Verify uniqueness
	id2 := generateSessionID()
	if id == id2 {
		t.Errorf("session IDs should be unique, got %q twice", id)
	}
}

func TestGenerateSessionID_Format(t *testing.T) {
	id := generateSessionID()
	// Should contain 4 dashes (UUID-like: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
	dashes := 0
	for _, c := range id {
		if c == '-' {
			dashes++
		}
	}
	if dashes != 4 {
		t.Errorf("expected 4 dashes in session ID %q, got %d", id, dashes)
	}
}

func TestStreamCreator_CreateStream(t *testing.T) {
	mgr := tunnel.NewStreamManager(100, 64)
	sc := &streamCreator{mgr: mgr}

	ctx := context.Background()
	rwc, err := sc.CreateStream(ctx, "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	if rwc == nil {
		t.Fatal("expected non-nil")
	}
	rwc.Close()
}

func TestNew_MultipleRelays(t *testing.T) {
	cfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret: "secret",
			ChunkSize:    16384,
			Cipher:       "auto",
			ResponseMode: "poll",
		},
		Scenario: "ecommerce",
		Relays: []config.RelayEntry{
			{Address: "127.0.0.1:5432", Username: "u1", Password: "p1", Database: "db1", Weight: 10},
			{Address: "127.0.0.1:5433", Username: "u2", Password: "p2", Database: "db2", Weight: 5},
		},
	}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.SOCKSAddr() != "" {
		t.Error("expected empty addr before Run")
	}
}

func TestClient_Run(t *testing.T) {
	// Start a relay
	relayCfg := &config.RelayConfig{}
	relayCfg.Listen.PostgreSQL = "127.0.0.1:0"
	relayCfg.Auth.Method = "md5"
	relayCfg.Auth.Users = map[string]string{"test_user": "test_pass"}
	relayCfg.Tunnel.SharedSecret = "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE="
	relayCfg.Tunnel.Role = "exit"
	relayCfg.FakeData.Scenario = "ecommerce"
	relayCfg.FakeData.Seed = 42
	relayCfg.Exit.MaxIdleSecs = 300
	relayCfg.TLS.Mode = "self_signed"
	relayCfg.Log.Level = "info"
	relayCfg.Log.Format = "text"
	relayCfg.Log.Output = "stderr"

	r, err := relay.New(relayCfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	// Wait for relay to start
	for i := 0; i < 50; i++ {
		if r.Addr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.Addr() == "" {
		t.Fatal("relay did not start in time")
	}

	// Build client config pointing to relay
	clientCfg := &config.ClientConfig{
		Proxy: config.ProxyConfig{Listen: "127.0.0.1:0"},
		Tunnel: config.TunnelConfig{
			SharedSecret: "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE=",
			ChunkSize:    16384,
			Cipher:       "auto",
			ResponseMode: "poll",
		},
		Scenario: "ecommerce",
		Relays: []config.RelayEntry{
			{Address: r.Addr(), Username: "test_user", Password: "test_pass", Database: "analytics", Weight: 10},
		},
	}

	c, err := New(clientCfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Run with short timeout
	runCtx, runCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer runCancel()

	err = c.Run(runCtx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v, want DeadlineExceeded", err)
	}
}
