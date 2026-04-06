package statistical

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/interleave"
	"github.com/dumanproxy/duman/internal/realquery"
)

// --- Kolmogorov-Smirnov test implementation ---

// ksStatistic computes the KS D-statistic between a sample and a reference CDF.
// cdf(x) returns the cumulative probability P(X <= x) for the reference distribution.
func ksStatistic(samples []float64, cdf func(float64) float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)

	d := 0.0
	for i, x := range sorted {
		empirical := float64(i+1) / float64(n)
		empiricalPrev := float64(i) / float64(n)
		theoretical := cdf(x)
		d1 := math.Abs(empirical - theoretical)
		d2 := math.Abs(empiricalPrev - theoretical)
		if d1 > d {
			d = d1
		}
		if d2 > d {
			d = d2
		}
	}
	return d
}

// ksCriticalValue returns the critical value for the KS test at significance level alpha.
// For large n: D_critical = c(alpha) / sqrt(n)
// c(0.05) ≈ 1.358, c(0.01) ≈ 1.628
func ksCriticalValue(n int, alpha float64) float64 {
	c := 1.358 // alpha = 0.05
	if alpha <= 0.01 {
		c = 1.628
	}
	return c / math.Sqrt(float64(n))
}

// uniformCDF returns a CDF for uniform distribution on [min, max].
func uniformCDF(min, max float64) func(float64) float64 {
	return func(x float64) float64 {
		if x < min {
			return 0
		}
		if x > max {
			return 1
		}
		return (x - min) / (max - min)
	}
}

// --- Traffic simulation ---

// trafficEvent records a single query event during simulation.
type trafficEvent struct {
	query     string
	queryType string // SELECT, INSERT, DELETE, UPDATE, OTHER
	isTunnel  bool   // true if this was an analytics_events INSERT (tunnel carrier)
	timestamp time.Duration
}

func classifyQuery(q string) string {
	upper := strings.ToUpper(strings.TrimSpace(q))
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return "SELECT"
	case strings.HasPrefix(upper, "INSERT"):
		return "INSERT"
	case strings.HasPrefix(upper, "DELETE"):
		return "DELETE"
	case strings.HasPrefix(upper, "UPDATE"):
		return "UPDATE"
	default:
		return "OTHER"
	}
}

func isTunnelQuery(q string) bool {
	return strings.Contains(strings.ToLower(q), "analytics_events")
}

// simulateTraffic generates simulated traffic events using the interleave+realquery engines.
// It generates `numBursts` burst cycles and records all query events.
func simulateTraffic(scenario string, seed int64, numBursts int, profile *interleave.TimingProfile) []trafficEvent {
	qe := realquery.NewEngine(scenario, seed)
	ratio := interleave.NewRatio(3)
	tc := interleave.NewTimingController(profile, seed)
	rng := rand.New(rand.NewSource(seed))

	var events []trafficEvent
	clock := time.Duration(0)

	for burst := 0; burst < numBursts; burst++ {
		// Burst phase
		batch := qe.NextBurst()
		coverSent := 0

		for _, query := range batch.Queries {
			events = append(events, trafficEvent{
				query:     query,
				queryType: classifyQuery(query),
				isTunnel:  isTunnelQuery(query),
				timestamp: clock,
			})
			coverSent++

			// After N cover queries, inject tunnel chunk (simulated)
			if coverSent >= ratio.Current() {
				// 50% chance of tunnel data being available
				if rng.Intn(2) == 0 {
					tunnelQ := qe.RandomAnalyticsEvent()
					events = append(events, trafficEvent{
						query:     tunnelQ,
						queryType: classifyQuery(tunnelQ),
						isTunnel:  true,
						timestamp: clock,
					})
					ratio.Update(rng.Intn(20)) // simulate queue depth
				} else {
					// No tunnel data, send cover analytics
					coverQ := qe.RandomAnalyticsEvent()
					events = append(events, trafficEvent{
						query:     coverQ,
						queryType: classifyQuery(coverQ),
						isTunnel:  isTunnelQuery(coverQ),
						timestamp: clock,
					})
				}
				coverSent = 0
			}

			clock += tc.BurstSpacing()
		}

		// Reading phase
		clock += tc.ReadingPause()

		// Background events during reading
		bgInterval := tc.BackgroundInterval()
		if bgInterval > 0 {
			bgQ := qe.RandomAnalyticsEvent()
			events = append(events, trafficEvent{
				query:     bgQ,
				queryType: classifyQuery(bgQ),
				isTunnel:  isTunnelQuery(bgQ),
				timestamp: clock,
			})
			clock += bgInterval
		}
	}

	return events
}

// --- Tests ---

// TestQueryTypeDistribution verifies that query type distribution matches
// expected patterns for a real application.
func TestQueryTypeDistribution(t *testing.T) {
	events := simulateTraffic("ecommerce", 42, 500, interleave.ProfileCasualBrowser)

	counts := map[string]int{}
	for _, e := range events {
		counts[e.queryType]++
	}
	total := float64(len(events))

	selectPct := float64(counts["SELECT"]) / total * 100
	insertPct := float64(counts["INSERT"]) / total * 100
	deletePct := float64(counts["DELETE"]) / total * 100

	t.Logf("Total events: %d", len(events))
	t.Logf("SELECT: %.1f%%, INSERT: %.1f%%, DELETE: %.1f%%, UPDATE: %.1f%%, OTHER: %.1f%%",
		selectPct, insertPct, deletePct,
		float64(counts["UPDATE"])/total*100,
		float64(counts["OTHER"])/total*100)

	// Real-world web apps are heavily SELECT-dominant (60-80%)
	if selectPct < 40 || selectPct > 90 {
		t.Errorf("SELECT percentage %.1f%% outside expected range [40%%, 90%%]", selectPct)
	}

	// INSERT should be present (analytics + cart + orders) but not dominant
	if insertPct < 5 || insertPct > 45 {
		t.Errorf("INSERT percentage %.1f%% outside expected range [5%%, 45%%]", insertPct)
	}

	// Must have at least 3 different query types
	if len(counts) < 3 {
		t.Errorf("Only %d query types observed, want at least 3", len(counts))
	}
}

// TestTunnelPayloadFrequency verifies that tunnel-bearing (analytics_events)
// queries make up a plausible fraction of total traffic.
func TestTunnelPayloadFrequency(t *testing.T) {
	events := simulateTraffic("ecommerce", 42, 500, interleave.ProfileCasualBrowser)

	tunnelCount := 0
	for _, e := range events {
		if e.isTunnel {
			tunnelCount++
		}
	}

	total := float64(len(events))
	tunnelPct := float64(tunnelCount) / total * 100

	t.Logf("Tunnel-bearing queries: %d / %d (%.1f%%)", tunnelCount, len(events), tunnelPct)

	// Tunnel data should be 10-40% of all queries (cover ratio 3:1 means ~25% base)
	if tunnelPct < 5 || tunnelPct > 50 {
		t.Errorf("Tunnel percentage %.1f%% outside expected range [5%%, 50%%]", tunnelPct)
	}
}

// TestBurstSizeDistribution verifies that burst sizes follow discrete uniform
// distribution using a chi-squared test (more appropriate than KS for discrete data).
func TestBurstSizeDistribution(t *testing.T) {
	profiles := []*interleave.TimingProfile{
		interleave.ProfileCasualBrowser,
		interleave.ProfilePowerUser,
		interleave.ProfileAPIWorker,
		interleave.ProfileDashboardMonitor,
	}

	for _, profile := range profiles {
		t.Run(profile.Name, func(t *testing.T) {
			tc := interleave.NewTimingController(profile, 42)
			n := 3000
			counts := map[int]int{}
			for i := 0; i < n; i++ {
				counts[tc.BurstSize()]++
			}

			k := profile.BurstSizeMax - profile.BurstSizeMin + 1 // number of categories
			expected := float64(n) / float64(k)

			chiSq := 0.0
			for v := profile.BurstSizeMin; v <= profile.BurstSizeMax; v++ {
				diff := float64(counts[v]) - expected
				chiSq += diff * diff / expected
			}

			// Chi-squared critical value at alpha=0.05, df=k-1
			// df=1→3.84, df=2→5.99, df=3→7.81, df=4→9.49, df=7→14.07
			criticals := map[int]float64{1: 3.84, 2: 5.99, 3: 7.81, 4: 9.49, 5: 11.07, 6: 12.59, 7: 14.07, 8: 15.51}
			df := k - 1
			critical, ok := criticals[df]
			if !ok {
				critical = 20.0 // generous fallback
			}

			t.Logf("Chi-squared=%.3f, critical=%.3f (df=%d, k=%d, n=%d)", chiSq, critical, df, k, n)
			if chiSq > critical {
				t.Errorf("Burst size distribution fails chi-squared test (χ²=%.3f > critical=%.3f), profile=%s",
					chiSq, critical, profile.Name)
			}
		})
	}
}

// TestReadingPauseDistribution verifies that reading pause durations follow
// uniform distribution within the profile's range.
func TestReadingPauseDistribution(t *testing.T) {
	profiles := []*interleave.TimingProfile{
		interleave.ProfileCasualBrowser,
		interleave.ProfilePowerUser,
		interleave.ProfileAPIWorker,
		interleave.ProfileDashboardMonitor,
	}

	for _, profile := range profiles {
		t.Run(profile.Name, func(t *testing.T) {
			tc := interleave.NewTimingController(profile, 42)
			samples := make([]float64, 1000)
			for i := range samples {
				samples[i] = float64(tc.ReadingPause())
			}

			min := float64(profile.ReadingPauseMin)
			max := float64(profile.ReadingPauseMax)
			cdf := uniformCDF(min, max)
			d := ksStatistic(samples, cdf)
			critical := ksCriticalValue(len(samples), 0.05)

			t.Logf("KS D=%.4f, critical=%.4f (n=%d)", d, critical, len(samples))
			if d > critical {
				t.Errorf("Reading pause distribution fails KS test (D=%.4f > critical=%.4f), profile=%s",
					d, critical, profile.Name)
			}
		})
	}
}

// TestBurstSpacingDistribution verifies inter-query timing within bursts.
func TestBurstSpacingDistribution(t *testing.T) {
	profile := interleave.ProfileCasualBrowser
	tc := interleave.NewTimingController(profile, 42)
	samples := make([]float64, 1000)
	for i := range samples {
		samples[i] = float64(tc.BurstSpacing())
	}

	// BurstSpacing = base + jitter - base/8, where jitter ∈ [0, base/4]
	// Range = [base - base/8, base + base/4 - base/8] = [7/8*base, 9/8*base]
	base := float64(profile.BurstSpacing)
	min := base - base/8
	max := base + base/4 - base/8
	cdf := uniformCDF(min, max)
	d := ksStatistic(samples, cdf)
	critical := ksCriticalValue(len(samples), 0.05)

	t.Logf("KS D=%.4f, critical=%.4f (n=%d, min=%.0fns, max=%.0fns)", d, critical, len(samples), min, max)
	if d > critical {
		t.Errorf("Burst spacing distribution fails KS test (D=%.4f > critical=%.4f)", d, critical)
	}
}

// TestInterQueryTimingDistribution verifies that inter-query timing within a
// simulated session follows a plausible pattern — not too regular (bot-like)
// and not too random.
func TestInterQueryTimingDistribution(t *testing.T) {
	events := simulateTraffic("ecommerce", 42, 200, interleave.ProfilePowerUser)

	// Compute inter-event intervals
	var intervals []float64
	for i := 1; i < len(events); i++ {
		dt := float64(events[i].timestamp - events[i-1].timestamp)
		if dt > 0 {
			intervals = append(intervals, dt)
		}
	}

	if len(intervals) < 10 {
		t.Fatalf("Too few intervals: %d", len(intervals))
	}

	// Compute coefficient of variation (CV = stddev/mean)
	// Real traffic: CV typically 1.0-3.0 (bursty)
	// Bot traffic: CV < 0.2 (too regular)
	mean := 0.0
	for _, v := range intervals {
		mean += v
	}
	mean /= float64(len(intervals))

	variance := 0.0
	for _, v := range intervals {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(intervals))
	stddev := math.Sqrt(variance)
	cv := stddev / mean

	t.Logf("Inter-query timing: mean=%.2fms, stddev=%.2fms, CV=%.3f (n=%d)",
		mean/1e6, stddev/1e6, cv, len(intervals))

	// CV should indicate bursty traffic, not perfectly regular
	if cv < 0.1 {
		t.Errorf("CV=%.3f is too regular (bot-like), expected > 0.1", cv)
	}
	// But not completely chaotic
	if cv > 50.0 {
		t.Errorf("CV=%.3f is too chaotic, expected < 50.0", cv)
	}
}

// TestQueryPatternDiversity verifies that the query engine produces diverse
// query patterns — not a small set of repeated queries.
func TestQueryPatternDiversity(t *testing.T) {
	events := simulateTraffic("ecommerce", 42, 300, interleave.ProfilePowerUser)

	// Count unique query templates (normalize numbers to X)
	uniqueQueries := map[string]bool{}
	for _, e := range events {
		// Normalize numbers to reduce variation
		normalized := normalizeQuery(e.query)
		uniqueQueries[normalized] = true
	}

	uniqueRatio := float64(len(uniqueQueries)) / float64(len(events))
	t.Logf("Unique query templates: %d / %d events (%.1f%%)", len(uniqueQueries), len(events), uniqueRatio*100)

	// Should have at least 10 unique templates
	if len(uniqueQueries) < 10 {
		t.Errorf("Only %d unique query templates, expected at least 10", len(uniqueQueries))
	}

	// Unique ratio should be > 5% (not just repeating the same query)
	if uniqueRatio < 0.05 {
		t.Errorf("Query uniqueness ratio %.3f too low, expected > 0.05", uniqueRatio)
	}
}

// TestProfilesProduceDifferentTraffic verifies that different timing profiles
// produce statistically distinguishable traffic patterns.
func TestProfilesProduceDifferentTraffic(t *testing.T) {
	profiles := []*interleave.TimingProfile{
		interleave.ProfileCasualBrowser,
		interleave.ProfileAPIWorker,
	}

	var burstCounts [2][]float64
	for i, profile := range profiles {
		events := simulateTraffic("ecommerce", 42, 200, profile)

		// Measure inter-event intervals
		for j := 1; j < len(events); j++ {
			dt := float64(events[j].timestamp - events[j-1].timestamp)
			if dt > 0 {
				burstCounts[i] = append(burstCounts[i], dt)
			}
		}
	}

	// Two-sample KS test between casual_browser and api_worker
	d := twoSampleKS(burstCounts[0], burstCounts[1])
	n1 := len(burstCounts[0])
	n2 := len(burstCounts[1])
	// Critical value for two-sample KS at alpha=0.05:
	// D_critical = 1.358 * sqrt((n1+n2)/(n1*n2))
	critical := 1.358 * math.Sqrt(float64(n1+n2)/float64(n1*n2))

	t.Logf("Two-sample KS: D=%.4f, critical=%.4f (n1=%d, n2=%d)", d, critical, n1, n2)

	// The distributions SHOULD be different (D > critical means they are different)
	if d < critical {
		t.Errorf("Casual browser and API worker traffic are not statistically distinguishable (D=%.4f < critical=%.4f)",
			d, critical)
	}
}

// TestConsistentSeedDeterminism verifies that same seed produces identical traffic.
func TestConsistentSeedDeterminism(t *testing.T) {
	events1 := simulateTraffic("ecommerce", 12345, 50, interleave.ProfileCasualBrowser)
	events2 := simulateTraffic("ecommerce", 12345, 50, interleave.ProfileCasualBrowser)

	if len(events1) != len(events2) {
		t.Fatalf("Different event counts: %d vs %d", len(events1), len(events2))
	}

	for i := range events1 {
		if events1[i].query != events2[i].query {
			t.Errorf("Event %d differs: %q vs %q", i, events1[i].query, events2[i].query)
			break
		}
	}
}

// TestDifferentSeedsProduceDifferentTraffic verifies that different seeds produce
// different traffic patterns.
func TestDifferentSeedsProduceDifferentTraffic(t *testing.T) {
	events1 := simulateTraffic("ecommerce", 42, 50, interleave.ProfileCasualBrowser)
	events2 := simulateTraffic("ecommerce", 99, 50, interleave.ProfileCasualBrowser)

	sameCount := 0
	minLen := len(events1)
	if len(events2) < minLen {
		minLen = len(events2)
	}
	for i := 0; i < minLen; i++ {
		if events1[i].query == events2[i].query {
			sameCount++
		}
	}

	samePct := float64(sameCount) / float64(minLen) * 100
	t.Logf("Same queries: %d / %d (%.1f%%)", sameCount, minLen, samePct)

	// Most queries should differ between seeds
	if samePct > 60 {
		t.Errorf("%.1f%% of queries are the same across different seeds, expected < 60%%", samePct)
	}
}

// --- Helpers ---

func normalizeQuery(q string) string {
	var result []byte
	inNumber := false
	for _, c := range []byte(q) {
		if c >= '0' && c <= '9' {
			if !inNumber {
				result = append(result, 'N')
				inNumber = true
			}
		} else {
			inNumber = false
			result = append(result, c)
		}
	}
	return string(result)
}

// twoSampleKS computes the two-sample KS D-statistic.
func twoSampleKS(a, b []float64) float64 {
	sa := make([]float64, len(a))
	sb := make([]float64, len(b))
	copy(sa, a)
	copy(sb, b)
	sort.Float64s(sa)
	sort.Float64s(sb)

	na := float64(len(sa))
	nb := float64(len(sb))
	ia, ib := 0, 0
	d := 0.0

	for ia < len(sa) && ib < len(sb) {
		if sa[ia] <= sb[ib] {
			ia++
		} else {
			ib++
		}
		diff := math.Abs(float64(ia)/na - float64(ib)/nb)
		if diff > d {
			d = diff
		}
	}

	return d
}
