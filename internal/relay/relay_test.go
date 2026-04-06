package relay

import (
	"context"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/config"
	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/pgwire"
)

func testConfig() *config.RelayConfig {
	cfg := &config.RelayConfig{}
	cfg.Listen.PostgreSQL = "127.0.0.1:0"
	cfg.Auth.Method = "md5"
	cfg.Auth.Users = map[string]string{"test": "pass"}
	cfg.Tunnel.SharedSecret = "dGVzdC1zZWNyZXQtMzItYnl0ZXMhISEhISEhISEhISE=" // base64
	cfg.Tunnel.Role = "exit"
	cfg.FakeData.Scenario = "ecommerce"
	cfg.FakeData.Seed = 42
	cfg.Exit.MaxIdleSecs = 300
	cfg.TLS.Mode = "self_signed"
	cfg.Log.Level = "info"
	cfg.Log.Format = "text"
	cfg.Log.Output = "stderr"
	return cfg
}

func TestNew(t *testing.T) {
	cfg := testConfig()
	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay")
	}
}

func TestNew_RawSecret(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.SharedSecret = "not-base64-raw-secret" // not valid base64

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay")
	}
}

func TestRelay_AddrBeforeRun(t *testing.T) {
	cfg := testConfig()
	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Before Run, the listener is not yet open so Addr returns "".
	if addr := r.Addr(); addr != "" {
		t.Fatalf("expected empty addr before Run, got %q", addr)
	}
}

func TestRelay_RunAndStop(t *testing.T) {
	cfg := testConfig()
	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	// Give the listener time to bind.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for relay to stop")
	}
}

func TestRelay_ClientConnection(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.Users = map[string]string{"sensor_writer": "test_pass"}

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Run(ctx)
	// Wait for the listener to be ready.
	time.Sleep(200 * time.Millisecond)

	addr := r.Addr()
	if addr == "" {
		t.Fatal("expected non-empty addr after Run")
	}

	// Connect with pgwire client.
	client, err := pgwire.Connect(ctx, pgwire.ClientConfig{
		Address:  addr,
		Username: "sensor_writer",
		Password: "test_pass",
		Database: "analytics",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Query fake data.
	result, err := client.SimpleQuery("SELECT * FROM products LIMIT 5")
	if err != nil {
		t.Fatalf("SimpleQuery: %v", err)
	}
	if len(result.Rows) != 5 {
		t.Errorf("rows = %d, want 5", len(result.Rows))
	}

	// Query version.
	result, err = client.SimpleQuery("SELECT version()")
	if err != nil {
		t.Fatalf("version query: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Error("expected 1 row for version")
	}
}

func TestExitProcessor_ProcessChunk(t *testing.T) {
	cfg := testConfig()
	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	proc := &exitProcessor{engine: r.exitEngine}

	ch := &crypto.Chunk{
		StreamID: 1,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte("localhost"),
	}

	err = proc.ProcessChunk(ch)
	if err != nil {
		t.Fatalf("ProcessChunk: %v", err)
	}
}
