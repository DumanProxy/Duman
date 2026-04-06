package relay

import (
	"context"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/config"
)

func TestNew_RelayRole(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.Role = "relay"
	cfg.Tunnel.ForwardTo = "127.0.0.1:9999"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay")
	}
	if r.forwarder == nil {
		t.Fatal("expected non-nil forwarder for relay role")
	}
	if r.exitEngine != nil {
		t.Fatal("expected nil exitEngine for relay role")
	}
}

func TestNew_BothRole(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.Role = "both"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay")
	}
	if r.exitEngine == nil {
		t.Fatal("expected non-nil exitEngine for both role")
	}
}

func TestNew_EmptyRole_DefaultsToExit(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.Role = "" // should default to "exit"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.exitEngine == nil {
		t.Fatal("expected non-nil exitEngine for default exit role")
	}
}

func TestNew_RandomMode(t *testing.T) {
	cfg := testConfig()
	cfg.FakeData.Mode = "random"
	cfg.FakeData.Scenario = "" // not needed for random mode

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay for random mode")
	}
}

func TestNew_CustomMode(t *testing.T) {
	cfg := testConfig()
	cfg.FakeData.Mode = "custom"
	cfg.FakeData.CustomDDL = "CREATE TABLE test (id INT PRIMARY KEY, name TEXT);"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay for custom mode")
	}
}

func TestNew_TemplateMode_Explicit(t *testing.T) {
	cfg := testConfig()
	cfg.FakeData.Mode = "template"
	cfg.FakeData.Scenario = "iot"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay for template/iot mode")
	}
}

func TestRelay_Addr_NilServer(t *testing.T) {
	r := &Relay{}
	if addr := r.Addr(); addr != "" {
		t.Fatalf("expected empty addr for nil server, got %q", addr)
	}
}

func TestRelay_Run_RelayRole_ForwarderConnectFails(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.Role = "relay"
	cfg.Tunnel.ForwardTo = "127.0.0.1:1" // nothing listening

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = r.Run(ctx)
	if err == nil {
		t.Fatal("expected error when forwarder cannot connect")
	}
}

func TestRelay_RunAndStop_BothRole(t *testing.T) {
	cfg := testConfig()
	cfg.Tunnel.Role = "both"

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

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

func TestBuildFakeEngine_DefaultMode(t *testing.T) {
	cfg := &config.RelayConfig{}
	cfg.FakeData.Scenario = "ecommerce"
	cfg.FakeData.Seed = 42
	cfg.FakeData.Mode = "" // should default to template

	engine, err := buildFakeEngine(cfg)
	if err != nil {
		t.Fatalf("buildFakeEngine: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBuildFakeEngine_RandomMode(t *testing.T) {
	cfg := &config.RelayConfig{}
	cfg.FakeData.Mode = "random"
	cfg.FakeData.Seed = 123

	engine, err := buildFakeEngine(cfg)
	if err != nil {
		t.Fatalf("buildFakeEngine(random): %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBuildFakeEngine_CustomMode(t *testing.T) {
	cfg := &config.RelayConfig{}
	cfg.FakeData.Mode = "custom"
	cfg.FakeData.CustomDDL = "CREATE TABLE users (id INT PRIMARY KEY, email TEXT NOT NULL);"
	cfg.FakeData.Seed = 99

	engine, err := buildFakeEngine(cfg)
	if err != nil {
		t.Fatalf("buildFakeEngine(custom): %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBuildFakeEngine_TemplateWithMutate(t *testing.T) {
	cfg := &config.RelayConfig{}
	cfg.FakeData.Mode = "template"
	cfg.FakeData.Scenario = "saas"
	cfg.FakeData.Seed = 42
	cfg.FakeData.Mutate = true

	engine, err := buildFakeEngine(cfg)
	if err != nil {
		t.Fatalf("buildFakeEngine(mutate): %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestBuildFakeEngine_AllScenarios(t *testing.T) {
	scenarios := []string{"ecommerce", "iot", "saas", "blog", "project"}
	for _, s := range scenarios {
		t.Run(s, func(t *testing.T) {
			cfg := &config.RelayConfig{}
			cfg.FakeData.Mode = "template"
			cfg.FakeData.Scenario = s
			cfg.FakeData.Seed = 42

			engine, err := buildFakeEngine(cfg)
			if err != nil {
				t.Fatalf("buildFakeEngine(%s): %v", s, err)
			}
			if engine == nil {
				t.Fatalf("expected non-nil engine for scenario %s", s)
			}
		})
	}
}

func TestNew_MultipleUsers(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.Users = map[string]string{
		"user1": "pass1",
		"user2": "pass2",
		"user3": "pass3",
	}

	r, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil relay")
	}
}
