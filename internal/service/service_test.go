package service

import "testing"

// mockService is a minimal Service implementation for testing.
type mockService struct {
	name    string
	running bool
}

func (m *mockService) Start() error {
	m.running = true
	return nil
}

func (m *mockService) Stop() error {
	m.running = false
	return nil
}

func (m *mockService) IsRunning() bool {
	return m.running
}

func (m *mockService) Name() string {
	return m.name
}

// TestConfig_Fields verifies that Config struct fields are correctly
// assigned and retrievable.
func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Name:        "DumanTunnel",
		DisplayName: "Duman Steganographic Tunnel",
		Description: "Tunnels traffic through steganographic SQL queries",
		ExecPath:    `C:\Program Files\Duman\duman.exe`,
		Args:        []string{"--config", "relay.yaml"},
	}

	if cfg.Name != "DumanTunnel" {
		t.Errorf("Name = %q, want %q", cfg.Name, "DumanTunnel")
	}
	if cfg.DisplayName != "Duman Steganographic Tunnel" {
		t.Errorf("DisplayName = %q, want %q", cfg.DisplayName, "Duman Steganographic Tunnel")
	}
	if cfg.Description != "Tunnels traffic through steganographic SQL queries" {
		t.Errorf("Description = %q, want %q", cfg.Description, "Tunnels traffic through steganographic SQL queries")
	}
	if cfg.ExecPath != `C:\Program Files\Duman\duman.exe` {
		t.Errorf("ExecPath = %q, want %q", cfg.ExecPath, `C:\Program Files\Duman\duman.exe`)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "--config" || cfg.Args[1] != "relay.yaml" {
		t.Errorf("Args = %v, want [--config relay.yaml]", cfg.Args)
	}
}

// TestServiceInterface verifies that the Service interface can be
// implemented and that the lifecycle methods work as expected.
func TestServiceInterface(t *testing.T) {
	var svc Service = &mockService{name: "TestSvc"}

	if svc.Name() != "TestSvc" {
		t.Errorf("Name() = %q, want %q", svc.Name(), "TestSvc")
	}

	if svc.IsRunning() {
		t.Error("IsRunning() = true before Start, want false")
	}

	if err := svc.Start(); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}

	if !svc.IsRunning() {
		t.Error("IsRunning() = false after Start, want true")
	}

	if err := svc.Stop(); err != nil {
		t.Fatalf("Stop() unexpected error: %v", err)
	}

	if svc.IsRunning() {
		t.Error("IsRunning() = true after Stop, want false")
	}
}
