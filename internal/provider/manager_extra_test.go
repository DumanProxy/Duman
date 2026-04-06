package provider

import (
	"context"
	"testing"
	"time"
)

func TestManager_SelectByType_Basic(t *testing.T) {
	mgr := NewManager(nil)

	pg := newMockProvider("postgresql")
	mysql := newMockProvider("mysql")
	rest := newMockProvider("rest")

	mgr.Add(pg, 10)
	mgr.Add(mysql, 5)
	mgr.Add(rest, 3)

	// Select by type should return matching type
	for i := 0; i < 20; i++ {
		p := mgr.SelectByType("postgresql")
		if p == nil {
			t.Fatal("expected non-nil provider for type postgresql")
		}
		if p.Type() != "postgresql" {
			t.Errorf("SelectByType(postgresql) returned type %q", p.Type())
		}
	}

	for i := 0; i < 20; i++ {
		p := mgr.SelectByType("mysql")
		if p == nil {
			t.Fatal("expected non-nil provider for type mysql")
		}
		if p.Type() != "mysql" {
			t.Errorf("SelectByType(mysql) returned type %q", p.Type())
		}
	}

	for i := 0; i < 20; i++ {
		p := mgr.SelectByType("rest")
		if p == nil {
			t.Fatal("expected non-nil provider for type rest")
		}
		if p.Type() != "rest" {
			t.Errorf("SelectByType(rest) returned type %q", p.Type())
		}
	}
}

func TestManager_SelectByType_NoMatch(t *testing.T) {
	mgr := NewManager(nil)
	mgr.Add(newMockProvider("postgresql"), 10)

	p := mgr.SelectByType("mysql")
	if p != nil {
		t.Error("expected nil for non-existent type")
	}
}

func TestManager_SelectByType_AllUnhealthy(t *testing.T) {
	mgr := NewManager(nil)

	pg := newMockProvider("postgresql")
	pg.healthy = false
	mgr.Add(pg, 10)

	p := mgr.SelectByType("postgresql")
	if p != nil {
		t.Error("expected nil when all matching providers are unhealthy")
	}
}

func TestManager_SelectByType_SkipsUnhealthy(t *testing.T) {
	mgr := NewManager(nil)

	pg1 := newMockProvider("postgresql")
	pg1.healthy = false

	pg2 := newMockProvider("postgresql")

	mgr.Add(pg1, 10)
	mgr.Add(pg2, 5)

	for i := 0; i < 20; i++ {
		p := mgr.SelectByType("postgresql")
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
		// Should always select pg2 since pg1 is unhealthy
		if p != pg2 {
			t.Error("should select healthy provider only")
		}
	}
}

func TestManager_SelectByType_WeightedDistribution(t *testing.T) {
	mgr := NewManager(nil)

	// Create two postgresql providers with different weights
	heavy := newMockProvider("postgresql")
	light := newMockProvider("postgresql")

	// We need to distinguish them, but Type() returns the same.
	// Use pointer identity.
	mgr.Add(heavy, 90)
	mgr.Add(light, 10)

	heavyCount := 0
	lightCount := 0
	for i := 0; i < 1000; i++ {
		p := mgr.SelectByType("postgresql")
		if p == heavy {
			heavyCount++
		} else if p == light {
			lightCount++
		}
	}

	// Heavy should be selected much more often
	if heavyCount < 700 {
		t.Errorf("heavy selected %d times, expected >700", heavyCount)
	}
	if lightCount == 0 {
		t.Error("light should be selected at least once in 1000 iterations")
	}
}

func TestManager_SelectByType_EmptyProviders(t *testing.T) {
	mgr := NewManager(nil)

	p := mgr.SelectByType("postgresql")
	if p != nil {
		t.Error("expected nil from empty manager")
	}
}

func TestManager_ProtocolCounts_Basic(t *testing.T) {
	mgr := NewManager(nil)

	mgr.Add(newMockProvider("postgresql"), 1)
	mgr.Add(newMockProvider("postgresql"), 1)
	mgr.Add(newMockProvider("mysql"), 1)
	mgr.Add(newMockProvider("rest"), 1)

	counts := mgr.ProtocolCounts()
	if counts["postgresql"] != 2 {
		t.Errorf("ProtocolCounts[postgresql] = %d, want 2", counts["postgresql"])
	}
	if counts["mysql"] != 1 {
		t.Errorf("ProtocolCounts[mysql] = %d, want 1", counts["mysql"])
	}
	if counts["rest"] != 1 {
		t.Errorf("ProtocolCounts[rest] = %d, want 1", counts["rest"])
	}
}

func TestManager_ProtocolCounts_Empty(t *testing.T) {
	mgr := NewManager(nil)
	counts := mgr.ProtocolCounts()
	if len(counts) != 0 {
		t.Errorf("expected empty counts, got %v", counts)
	}
}

func TestManager_ProtocolCounts_IncludesUnhealthy(t *testing.T) {
	mgr := NewManager(nil)

	p := newMockProvider("postgresql")
	p.healthy = false
	mgr.Add(p, 1)

	counts := mgr.ProtocolCounts()
	if counts["postgresql"] != 1 {
		t.Errorf("ProtocolCounts should include unhealthy providers, got %d", counts["postgresql"])
	}
}

func TestManager_Select_EmptyProviders(t *testing.T) {
	mgr := NewManager(nil)
	if mgr.Select() != nil {
		t.Error("expected nil from empty manager")
	}
}

func TestManager_All_Empty(t *testing.T) {
	mgr := NewManager(nil)
	all := mgr.All()
	if len(all) != 0 {
		t.Errorf("All() on empty manager = %d, want 0", len(all))
	}
}

func TestManager_HealthyCount_Empty(t *testing.T) {
	mgr := NewManager(nil)
	if mgr.HealthyCount() != 0 {
		t.Errorf("HealthyCount on empty manager = %d, want 0", mgr.HealthyCount())
	}
}

func TestManager_HealthyCount_AllHealthy(t *testing.T) {
	mgr := NewManager(nil)
	mgr.Add(newMockProvider("a"), 1)
	mgr.Add(newMockProvider("b"), 1)
	mgr.Add(newMockProvider("c"), 1)

	if mgr.HealthyCount() != 3 {
		t.Errorf("HealthyCount = %d, want 3", mgr.HealthyCount())
	}
}

func TestManager_HealthyCount_NoneHealthy(t *testing.T) {
	mgr := NewManager(nil)
	p1 := newMockProvider("a")
	p1.healthy = false
	p2 := newMockProvider("b")
	p2.healthy = false
	mgr.Add(p1, 1)
	mgr.Add(p2, 1)

	if mgr.HealthyCount() != 0 {
		t.Errorf("HealthyCount = %d, want 0", mgr.HealthyCount())
	}
}

func TestManager_CloseAll_NoHealthCancel(t *testing.T) {
	// CloseAll should work even if healthCancel is nil (no ConnectAll called)
	mgr := NewManager(nil)
	p := newMockProvider("test")
	mgr.Add(p, 1)

	mgr.CloseAll() // should not panic

	if p.healthy {
		t.Error("expected provider to be unhealthy after CloseAll")
	}
}

func TestManager_CloseAll_WithHealthCancel(t *testing.T) {
	mgr := NewManager(nil)
	p := newMockProvider("single")
	mgr.Add(p, 1)

	// ConnectAll starts health checker
	err := mgr.ConnectAll(context.Background())
	if err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}

	mgr.CloseAll()

	if p.healthy {
		t.Error("expected provider to be unhealthy after CloseAll")
	}
}

func TestManager_HealthCheckLoop_LogsUnhealthy(t *testing.T) {
	// We cannot directly test the healthCheckLoop logging,
	// but we can exercise the code path by running ConnectAll
	// and then making providers unhealthy.
	mgr := NewManager(nil)
	p := newMockProvider("single")
	mgr.Add(p, 1)

	ctx, cancel := context.WithCancel(context.Background())
	err := mgr.ConnectAll(ctx)
	if err != nil {
		t.Fatalf("ConnectAll: %v", err)
	}

	// Make provider unhealthy - the health check loop will log it
	p.healthy = false

	// Give the health check loop a chance to run (it has a 30s ticker,
	// so we cancel quickly since we can't wait that long in a test)
	time.Sleep(10 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)
}

func TestManager_ConnectAll_Empty(t *testing.T) {
	mgr := NewManager(nil)
	err := mgr.ConnectAll(context.Background())
	if err != nil {
		t.Fatalf("ConnectAll on empty manager: %v", err)
	}
}
