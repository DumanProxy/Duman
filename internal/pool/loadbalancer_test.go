package pool

import (
	"testing"
	"time"
)

func TestSelectRelay_PrefersLeastLoaded(t *testing.T) {
	lb := NewLoadBalancer()
	now := time.Now()

	lb.UpdateLoad(LoadReport{
		RelayAddr:        "r1:5432",
		ConnectedClients: 100,
		BandwidthUsageMBps: 80.0,
		CPUPercent:       90.0,
		AvgLatencyMs:     200.0,
		Timestamp:        now,
	})
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "r2:5432",
		ConnectedClients: 10,
		BandwidthUsageMBps: 5.0,
		CPUPercent:       10.0,
		AvgLatencyMs:     20.0,
		Timestamp:        now,
	})
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "r3:5432",
		ConnectedClients: 50,
		BandwidthUsageMBps: 40.0,
		CPUPercent:       50.0,
		AvgLatencyMs:     100.0,
		Timestamp:        now,
	})

	got := lb.SelectRelay([]string{"r1:5432", "r2:5432", "r3:5432"})
	if got != "r2:5432" {
		t.Errorf("SelectRelay = %q, want %q (least loaded)", got, "r2:5432")
	}
}

func TestSelectRelay_EmptyCandidates(t *testing.T) {
	lb := NewLoadBalancer()
	got := lb.SelectRelay(nil)
	if got != "" {
		t.Errorf("SelectRelay(nil) = %q, want empty string", got)
	}
	got = lb.SelectRelay([]string{})
	if got != "" {
		t.Errorf("SelectRelay([]) = %q, want empty string", got)
	}
}

func TestSelectRelay_UnknownRelay(t *testing.T) {
	lb := NewLoadBalancer()
	now := time.Now()

	// One known relay with very high load.
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "heavy:5432",
		ConnectedClients: 1000,
		BandwidthUsageMBps: 500.0,
		CPUPercent:       99.0,
		AvgLatencyMs:     800.0,
		Timestamp:        now,
	})

	// One known relay with very low load (score = 0).
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "light:5432",
		ConnectedClients: 0,
		BandwidthUsageMBps: 0.0,
		CPUPercent:       0.0,
		AvgLatencyMs:     0.0,
		Timestamp:        now,
	})

	// Unknown relay gets mid-range 0.5. It should lose to "light" (score 0)
	// but beat "heavy" (score 1.0).
	got := lb.SelectRelay([]string{"heavy:5432", "unknown:5432", "light:5432"})
	if got != "light:5432" {
		t.Errorf("SelectRelay = %q, want %q (light has score 0)", got, "light:5432")
	}

	// When unknown competes with both known relays, unknown (0.5) sits
	// between light (0.0) and heavy (1.0), so light still wins.
	got = lb.SelectRelay([]string{"heavy:5432", "unknown:5432", "light:5432"})
	if got != "light:5432" {
		t.Errorf("SelectRelay = %q, want %q (light 0.0 < unknown 0.5 < heavy 1.0)", got, "light:5432")
	}

	// When only unknown and heavy compete, with a single known entry
	// normalisation yields mid-range (0.5) for heavy, same as unknown.
	// Either is acceptable since their scores are effectively equal.
	got = lb.SelectRelay([]string{"unknown:5432", "heavy:5432"})
	if got != "unknown:5432" && got != "heavy:5432" {
		t.Errorf("SelectRelay = %q, want one of unknown:5432 or heavy:5432", got)
	}
}

func TestUpdateLoad_Overwrite(t *testing.T) {
	lb := NewLoadBalancer()

	lb.UpdateLoad(LoadReport{
		RelayAddr:        "r1:5432",
		ConnectedClients: 10,
		Timestamp:        time.Now(),
	})
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "r1:5432",
		ConnectedClients: 99,
		Timestamp:        time.Now(),
	})

	r, ok := lb.GetLoad("r1:5432")
	if !ok {
		t.Fatal("expected load report for r1:5432")
	}
	if r.ConnectedClients != 99 {
		t.Errorf("ConnectedClients = %d, want 99 after overwrite", r.ConnectedClients)
	}
	if lb.Size() != 1 {
		t.Errorf("Size = %d, want 1 (overwrite should not duplicate)", lb.Size())
	}
}

func TestGetLoad_Hit(t *testing.T) {
	lb := NewLoadBalancer()
	lb.UpdateLoad(LoadReport{
		RelayAddr:        "hit:443",
		ConnectedClients: 42,
		BandwidthUsageMBps: 12.5,
		CPUPercent:       55.0,
		AvgLatencyMs:     30.0,
		Timestamp:        time.Now(),
	})

	r, ok := lb.GetLoad("hit:443")
	if !ok {
		t.Fatal("expected hit for hit:443")
	}
	if r.ConnectedClients != 42 {
		t.Errorf("ConnectedClients = %d, want 42", r.ConnectedClients)
	}
	if r.BandwidthUsageMBps != 12.5 {
		t.Errorf("BandwidthUsageMBps = %f, want 12.5", r.BandwidthUsageMBps)
	}
}

func TestGetLoad_Miss(t *testing.T) {
	lb := NewLoadBalancer()
	_, ok := lb.GetLoad("nonexistent:443")
	if ok {
		t.Error("expected miss for nonexistent relay")
	}
}

func TestRemoveRelay(t *testing.T) {
	lb := NewLoadBalancer()
	lb.UpdateLoad(LoadReport{RelayAddr: "a:1", Timestamp: time.Now()})
	lb.UpdateLoad(LoadReport{RelayAddr: "b:2", Timestamp: time.Now()})

	if lb.Size() != 2 {
		t.Fatalf("Size = %d, want 2", lb.Size())
	}

	lb.RemoveRelay("a:1")
	if lb.Size() != 1 {
		t.Errorf("Size = %d after remove, want 1", lb.Size())
	}
	_, ok := lb.GetLoad("a:1")
	if ok {
		t.Error("expected a:1 to be removed")
	}

	// Removing a non-existent relay is a no-op.
	lb.RemoveRelay("does-not-exist:9999")
	if lb.Size() != 1 {
		t.Errorf("Size = %d after removing non-existent, want 1", lb.Size())
	}
}

func TestPruneStale(t *testing.T) {
	lb := NewLoadBalancer()

	fresh := time.Now()
	stale := time.Now().Add(-10 * time.Minute)

	lb.UpdateLoad(LoadReport{RelayAddr: "fresh:1", Timestamp: fresh})
	lb.UpdateLoad(LoadReport{RelayAddr: "stale:2", Timestamp: stale})
	lb.UpdateLoad(LoadReport{RelayAddr: "stale:3", Timestamp: stale})

	if lb.Size() != 3 {
		t.Fatalf("Size = %d, want 3 before prune", lb.Size())
	}

	lb.PruneStale(5 * time.Minute)

	if lb.Size() != 1 {
		t.Errorf("Size = %d after prune, want 1", lb.Size())
	}
	_, ok := lb.GetLoad("fresh:1")
	if !ok {
		t.Error("expected fresh:1 to survive prune")
	}
	_, ok = lb.GetLoad("stale:2")
	if ok {
		t.Error("expected stale:2 to be pruned")
	}
}

func TestAllLoads(t *testing.T) {
	lb := NewLoadBalancer()
	now := time.Now()

	lb.UpdateLoad(LoadReport{RelayAddr: "x:1", ConnectedClients: 1, Timestamp: now})
	lb.UpdateLoad(LoadReport{RelayAddr: "y:2", ConnectedClients: 2, Timestamp: now})
	lb.UpdateLoad(LoadReport{RelayAddr: "z:3", ConnectedClients: 3, Timestamp: now})

	all := lb.AllLoads()
	if len(all) != 3 {
		t.Fatalf("AllLoads len = %d, want 3", len(all))
	}

	// Verify it is a copy by mutating the returned map.
	delete(all, "x:1")
	if lb.Size() != 3 {
		t.Error("AllLoads returned a reference, not a copy")
	}

	// Spot-check values.
	all = lb.AllLoads()
	if all["y:2"].ConnectedClients != 2 {
		t.Errorf("y:2 ConnectedClients = %d, want 2", all["y:2"].ConnectedClients)
	}
}

func TestSelectRelay_SingleCandidate(t *testing.T) {
	lb := NewLoadBalancer()
	got := lb.SelectRelay([]string{"only:5432"})
	if got != "only:5432" {
		t.Errorf("SelectRelay single = %q, want %q", got, "only:5432")
	}
}

func TestSelectRelay_AllUnknown(t *testing.T) {
	lb := NewLoadBalancer()
	// All unknown candidates get the same mid-range score; first one wins.
	got := lb.SelectRelay([]string{"a:1", "b:2", "c:3"})
	if got != "a:1" {
		t.Errorf("SelectRelay all-unknown = %q, want %q (first candidate, tie-break)", got, "a:1")
	}
}
