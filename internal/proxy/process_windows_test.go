//go:build windows

package proxy

import (
	"testing"
)

func TestProcessRouter_NewProcessRouter(t *testing.T) {
	rules := []ProcessRule{
		{ProcessName: "firefox", Action: ActionTunnel},
		{ProcessName: "chrome", Action: ActionDirect},
	}
	pr := NewProcessRouter(rules)
	if pr == nil {
		t.Fatal("expected non-nil ProcessRouter")
	}
	if len(pr.rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(pr.rules))
	}
}

func TestProcessRouter_Setup(t *testing.T) {
	pr := NewProcessRouter(nil)
	err := pr.Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

func TestProcessRouter_Cleanup(t *testing.T) {
	pr := NewProcessRouter(nil)
	_ = pr.AddProcess(100, "test")
	if len(pr.pids) != 1 {
		t.Fatalf("expected 1 pid, got %d", len(pr.pids))
	}

	err := pr.Cleanup()
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(pr.pids) != 0 {
		t.Errorf("expected pids to be cleared, got %d", len(pr.pids))
	}
}

func TestProcessRouter_AddProcess(t *testing.T) {
	pr := NewProcessRouter(nil)

	err := pr.AddProcess(1234, "firefox")
	if err != nil {
		t.Fatalf("AddProcess: %v", err)
	}

	// Invalid PID
	err = pr.AddProcess(0, "test")
	if err == nil {
		t.Error("expected error for pid=0")
	}
	err = pr.AddProcess(-1, "test")
	if err == nil {
		t.Error("expected error for negative pid")
	}
}

func TestProcessRouter_LookupProcess_Match(t *testing.T) {
	rules := []ProcessRule{
		{ProcessName: "firefox", Action: ActionTunnel},
		{ProcessName: "chrome", Action: ActionDirect},
	}
	pr := NewProcessRouter(rules)
	_ = pr.AddProcess(100, "firefox")
	_ = pr.AddProcess(200, "chrome")
	_ = pr.AddProcess(300, "curl")

	// firefox should be tunneled
	action, err := pr.LookupProcess(100)
	if err != nil {
		t.Fatalf("LookupProcess(100): %v", err)
	}
	if action != ActionTunnel {
		t.Errorf("expected ActionTunnel for firefox, got %v", action)
	}

	// chrome should be direct
	action, err = pr.LookupProcess(200)
	if err != nil {
		t.Fatalf("LookupProcess(200): %v", err)
	}
	if action != ActionDirect {
		t.Errorf("expected ActionDirect for chrome, got %v", action)
	}

	// curl has no matching rule, should default to tunnel
	action, err = pr.LookupProcess(300)
	if err != nil {
		t.Fatalf("LookupProcess(300): %v", err)
	}
	if action != ActionTunnel {
		t.Errorf("expected ActionTunnel for unmatched process, got %v", action)
	}
}

func TestProcessRouter_LookupProcess_NotRegistered(t *testing.T) {
	pr := NewProcessRouter(nil)
	_, err := pr.LookupProcess(999)
	if err == nil {
		t.Error("expected error for unregistered PID")
	}
}

func TestProcessRouter_SetupAndCleanup(t *testing.T) {
	rules := []ProcessRule{
		{ProcessName: "app", Action: ActionBlock},
	}
	pr := NewProcessRouter(rules)

	if err := pr.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_ = pr.AddProcess(42, "app")
	action, err := pr.LookupProcess(42)
	if err != nil {
		t.Fatalf("LookupProcess: %v", err)
	}
	if action != ActionBlock {
		t.Errorf("expected ActionBlock, got %v", action)
	}

	if err := pr.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	_, err = pr.LookupProcess(42)
	if err == nil {
		t.Error("expected error after cleanup (pids cleared)")
	}
}
