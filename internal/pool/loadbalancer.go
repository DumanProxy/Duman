package pool

import (
	"math"
	"sync"
	"time"
)

// LoadReport holds real-time load metrics for a relay.
type LoadReport struct {
	RelayAddr        string
	ConnectedClients int
	BandwidthUsageMBps float64
	CPUPercent       float64
	AvgLatencyMs     float64
	Timestamp        time.Time
}

// LoadBalancer tracks per-relay load metrics and selects the least-loaded
// relay from a set of candidates using a weighted scoring function.
type LoadBalancer struct {
	mu    sync.RWMutex
	loads map[string]LoadReport
}

// NewLoadBalancer creates an empty LoadBalancer.
func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		loads: make(map[string]LoadReport),
	}
}

// UpdateLoad stores or updates the load report for a relay.
func (lb *LoadBalancer) UpdateLoad(report LoadReport) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.loads[report.RelayAddr] = report
}

// SelectRelay picks the least-loaded relay from candidates using a weighted
// score. Lower score = better. The scoring formula normalises each metric
// across the candidate set and applies fixed weights:
//
//	score = 0.4*normClients + 0.3*normBandwidth + 0.2*normLatency + 0.1*normCPU
//
// If no load data exists for a candidate it is assigned a mid-range score of
// 0.5. Returns an empty string when candidates is empty.
func (lb *LoadBalancer) SelectRelay(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	lb.mu.RLock()
	defer lb.mu.RUnlock()

	// Gather reports for candidates that have load data.
	type entry struct {
		addr  string
		report LoadReport
		known bool
	}

	entries := make([]entry, len(candidates))
	for i, addr := range candidates {
		r, ok := lb.loads[addr]
		entries[i] = entry{addr: addr, report: r, known: ok}
	}

	// Find min/max for each metric across known entries.
	var (
		minClients, maxClients     float64
		minBW, maxBW               float64
		minLatency, maxLatency     float64
		minCPU, maxCPU             float64
		first                      = true
	)
	for _, e := range entries {
		if !e.known {
			continue
		}
		c := float64(e.report.ConnectedClients)
		bw := e.report.BandwidthUsageMBps
		lat := e.report.AvgLatencyMs
		cpu := e.report.CPUPercent

		if first {
			minClients, maxClients = c, c
			minBW, maxBW = bw, bw
			minLatency, maxLatency = lat, lat
			minCPU, maxCPU = cpu, cpu
			first = false
		} else {
			minClients = math.Min(minClients, c)
			maxClients = math.Max(maxClients, c)
			minBW = math.Min(minBW, bw)
			maxBW = math.Max(maxBW, bw)
			minLatency = math.Min(minLatency, lat)
			maxLatency = math.Max(maxLatency, lat)
			minCPU = math.Min(minCPU, cpu)
			maxCPU = math.Max(maxCPU, cpu)
		}
	}

	// normalise maps a value into [0,1] given a min/max range.
	// When min == max all known entries share the same value, so return 0.5
	// (mid-range, same as unknown relays) since there is no comparative
	// information to distinguish them.
	normalise := func(val, lo, hi float64) float64 {
		if hi == lo {
			return 0.5
		}
		return (val - lo) / (hi - lo)
	}

	// weightedScore computes the composite score from four normalised metrics.
	// Factored out so that both known and unknown relays follow the exact same
	// arithmetic path, avoiding IEEE 754 rounding differences.
	weightedScore := func(nc, nb, nl, ncpu float64) float64 {
		return 0.4*nc + 0.3*nb + 0.2*nl + 0.1*ncpu
	}

	// midNorm is the assumed normalised value for unknown relay metrics.
	midNorm := normalise(1, 1, 1) // 0.5 via the same function
	midScore := weightedScore(midNorm, midNorm, midNorm, midNorm)

	bestScore := math.MaxFloat64
	bestAddr := candidates[0]

	for _, e := range entries {
		var score float64
		if !e.known {
			score = midScore
		} else {
			nc := normalise(float64(e.report.ConnectedClients), minClients, maxClients)
			nb := normalise(e.report.BandwidthUsageMBps, minBW, maxBW)
			nl := normalise(e.report.AvgLatencyMs, minLatency, maxLatency)
			ncpu := normalise(e.report.CPUPercent, minCPU, maxCPU)
			score = weightedScore(nc, nb, nl, ncpu)
		}
		if score < bestScore {
			bestScore = score
			bestAddr = e.addr
		}
	}

	return bestAddr
}

// GetLoad returns the current load report for a relay and whether it exists.
func (lb *LoadBalancer) GetLoad(addr string) (LoadReport, bool) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	r, ok := lb.loads[addr]
	return r, ok
}

// RemoveRelay removes a relay's load data from the balancer.
func (lb *LoadBalancer) RemoveRelay(addr string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	delete(lb.loads, addr)
}

// AllLoads returns a copy of all tracked load reports.
func (lb *LoadBalancer) AllLoads() map[string]LoadReport {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	out := make(map[string]LoadReport, len(lb.loads))
	for k, v := range lb.loads {
		out[k] = v
	}
	return out
}

// PruneStale removes load reports whose Timestamp is older than maxAge.
func (lb *LoadBalancer) PruneStale(maxAge time.Duration) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for addr, r := range lb.loads {
		if r.Timestamp.Before(cutoff) {
			delete(lb.loads, addr)
		}
	}
}

// Size returns the number of relays being tracked.
func (lb *LoadBalancer) Size() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return len(lb.loads)
}
