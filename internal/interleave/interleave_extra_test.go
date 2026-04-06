package interleave

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// timing.go tests
// ---------------------------------------------------------------------------

func TestProfileByName_AllProfiles(t *testing.T) {
	tests := []struct {
		name    string
		want    *TimingProfile
	}{
		{"casual_browser", ProfileCasualBrowser},
		{"power_user", ProfilePowerUser},
		{"api_worker", ProfileAPIWorker},
		{"dashboard_monitor", ProfileDashboardMonitor},
	}

	for _, tt := range tests {
		got := ProfileByName(tt.name)
		if got != tt.want {
			t.Errorf("ProfileByName(%q) = %v, want %v", tt.name, got, tt.want)
		}
		if got.Name != tt.name {
			t.Errorf("ProfileByName(%q).Name = %q, want %q", tt.name, got.Name, tt.name)
		}
	}
}

func TestProfileByName_Default(t *testing.T) {
	got := ProfileByName("unknown_profile")
	if got != ProfileCasualBrowser {
		t.Errorf("ProfileByName(unknown) = %v, want ProfileCasualBrowser", got)
	}

	got2 := ProfileByName("")
	if got2 != ProfileCasualBrowser {
		t.Errorf("ProfileByName(\"\") = %v, want ProfileCasualBrowser", got2)
	}
}

func TestNewTimingController(t *testing.T) {
	tc := NewTimingController(ProfilePowerUser, 42)
	if tc == nil {
		t.Fatal("NewTimingController returned nil")
	}
	if tc.profile != ProfilePowerUser {
		t.Errorf("profile mismatch")
	}
	if tc.rng == nil {
		t.Fatal("rng should not be nil")
	}
}

func TestTimingController_Profile(t *testing.T) {
	tc := NewTimingController(ProfileAPIWorker, 1)
	got := tc.Profile()
	if got != ProfileAPIWorker {
		t.Errorf("Profile() returned wrong profile")
	}
}

func TestTimingController_BurstSize(t *testing.T) {
	tc := NewTimingController(ProfileCasualBrowser, 123)

	// ProfileCasualBrowser: BurstSizeMin=2, BurstSizeMax=4
	for i := 0; i < 100; i++ {
		size := tc.BurstSize()
		if size < ProfileCasualBrowser.BurstSizeMin || size > ProfileCasualBrowser.BurstSizeMax {
			t.Fatalf("BurstSize() = %d, out of range [%d, %d]",
				size, ProfileCasualBrowser.BurstSizeMin, ProfileCasualBrowser.BurstSizeMax)
		}
	}
}

func TestTimingController_BurstSize_EqualMinMax(t *testing.T) {
	// When BurstSizeMin == BurstSizeMax, r <= 0, should return BurstSizeMin
	profile := &TimingProfile{
		Name:         "equal_burst",
		BurstSizeMin: 5,
		BurstSizeMax: 5,
	}
	tc := NewTimingController(profile, 42)

	for i := 0; i < 10; i++ {
		size := tc.BurstSize()
		if size != 5 {
			t.Fatalf("BurstSize() = %d, want 5 (min==max)", size)
		}
	}
}

func TestTimingController_BurstSize_MinGreaterThanMax(t *testing.T) {
	// When BurstSizeMin > BurstSizeMax, r < 0, should return BurstSizeMin
	profile := &TimingProfile{
		Name:         "inverted_burst",
		BurstSizeMin: 10,
		BurstSizeMax: 3,
	}
	tc := NewTimingController(profile, 42)

	size := tc.BurstSize()
	if size != 10 {
		t.Fatalf("BurstSize() = %d, want 10 (min > max)", size)
	}
}

func TestTimingController_BurstSpacing(t *testing.T) {
	tc := NewTimingController(ProfilePowerUser, 42)

	// ProfilePowerUser.BurstSpacing = 12ms
	// jitter is in [0, BurstSpacing/4] = [0, 3ms]
	// result = base + jitter - base/8 = 12 + [0,3] - 1 = [11, 14]ms
	for i := 0; i < 100; i++ {
		spacing := tc.BurstSpacing()
		if spacing < 0 {
			t.Fatalf("BurstSpacing() = %v, should not be negative", spacing)
		}
		// Upper bound: base + base/4 = 12 + 3 = 15ms
		maxExpected := ProfilePowerUser.BurstSpacing + ProfilePowerUser.BurstSpacing/4
		if spacing > maxExpected {
			t.Fatalf("BurstSpacing() = %v, exceeds expected max %v", spacing, maxExpected)
		}
	}
}

func TestTimingController_ReadingPause(t *testing.T) {
	tc := NewTimingController(ProfileCasualBrowser, 42)

	// ProfileCasualBrowser: ReadingPauseMin=8s, ReadingPauseMax=45s
	for i := 0; i < 100; i++ {
		pause := tc.ReadingPause()
		if pause < ProfileCasualBrowser.ReadingPauseMin {
			t.Fatalf("ReadingPause() = %v, below min %v", pause, ProfileCasualBrowser.ReadingPauseMin)
		}
		if pause >= ProfileCasualBrowser.ReadingPauseMax {
			t.Fatalf("ReadingPause() = %v, at or above max %v", pause, ProfileCasualBrowser.ReadingPauseMax)
		}
	}
}

func TestTimingController_ReadingPause_EqualMinMax(t *testing.T) {
	profile := &TimingProfile{
		Name:            "equal_pause",
		ReadingPauseMin: 5 * time.Second,
		ReadingPauseMax: 5 * time.Second,
	}
	tc := NewTimingController(profile, 42)

	pause := tc.ReadingPause()
	if pause != 5*time.Second {
		t.Fatalf("ReadingPause() = %v, want 5s (min==max)", pause)
	}
}

func TestTimingController_BackgroundInterval(t *testing.T) {
	tc := NewTimingController(ProfileAPIWorker, 42)

	// ProfileAPIWorker: BackgroundIntervalMin=2s, BackgroundIntervalMax=5s
	for i := 0; i < 100; i++ {
		interval := tc.BackgroundInterval()
		if interval < ProfileAPIWorker.BackgroundIntervalMin {
			t.Fatalf("BackgroundInterval() = %v, below min %v",
				interval, ProfileAPIWorker.BackgroundIntervalMin)
		}
		if interval >= ProfileAPIWorker.BackgroundIntervalMax {
			t.Fatalf("BackgroundInterval() = %v, at or above max %v",
				interval, ProfileAPIWorker.BackgroundIntervalMax)
		}
	}
}

func TestTimingController_BackgroundInterval_EqualMinMax(t *testing.T) {
	profile := &TimingProfile{
		Name:                  "equal_bg",
		BackgroundIntervalMin: 3 * time.Second,
		BackgroundIntervalMax: 3 * time.Second,
	}
	tc := NewTimingController(profile, 42)

	interval := tc.BackgroundInterval()
	if interval != 3*time.Second {
		t.Fatalf("BackgroundInterval() = %v, want 3s (min==max)", interval)
	}
}

func TestTimingController_ShouldNavigate(t *testing.T) {
	// ProfileAPIWorker has NavProbability=0.95, so most calls should return true
	tc := NewTimingController(ProfileAPIWorker, 42)

	trueCount := 0
	const samples = 1000
	for i := 0; i < samples; i++ {
		if tc.ShouldNavigate() {
			trueCount++
		}
	}

	// With 0.95 probability, we expect ~950 true. Allow generous margin.
	if trueCount < 800 {
		t.Errorf("ShouldNavigate() true count = %d/%d, expected ~950 for NavProbability=0.95",
			trueCount, samples)
	}
}

func TestTimingController_ShouldNavigate_LowProbability(t *testing.T) {
	// Use a profile with very low nav probability
	profile := &TimingProfile{
		Name:           "low_nav",
		NavProbability: 0.05,
	}
	tc := NewTimingController(profile, 42)

	trueCount := 0
	const samples = 1000
	for i := 0; i < samples; i++ {
		if tc.ShouldNavigate() {
			trueCount++
		}
	}

	// With 0.05 probability, we expect ~50 true. Allow generous margin.
	if trueCount > 200 {
		t.Errorf("ShouldNavigate() true count = %d/%d, expected ~50 for NavProbability=0.05",
			trueCount, samples)
	}
}

func TestTimingController_Deterministic(t *testing.T) {
	seed := int64(999)
	tc1 := NewTimingController(ProfilePowerUser, seed)
	tc2 := NewTimingController(ProfilePowerUser, seed)

	for i := 0; i < 50; i++ {
		if tc1.BurstSize() != tc2.BurstSize() {
			t.Fatal("BurstSize not deterministic with same seed")
		}
		if tc1.BurstSpacing() != tc2.BurstSpacing() {
			t.Fatal("BurstSpacing not deterministic with same seed")
		}
		if tc1.ReadingPause() != tc2.ReadingPause() {
			t.Fatal("ReadingPause not deterministic with same seed")
		}
		if tc1.BackgroundInterval() != tc2.BackgroundInterval() {
			t.Fatal("BackgroundInterval not deterministic with same seed")
		}
		if tc1.ShouldNavigate() != tc2.ShouldNavigate() {
			t.Fatal("ShouldNavigate not deterministic with same seed")
		}
	}
}

// ---------------------------------------------------------------------------
// ratio.go tests - SmoothedDepth and additional Update paths
// ---------------------------------------------------------------------------

func TestRatio_SmoothedDepth_Initial(t *testing.T) {
	r := NewRatio(3)
	d := r.SmoothedDepth()
	if d != 0 {
		t.Errorf("SmoothedDepth() = %f, want 0 initially", d)
	}
}

func TestRatio_SmoothedDepth_AfterUpdates(t *testing.T) {
	r := NewRatio(3)

	// Feed some queue depths and verify EWMA moves
	r.Update(50)
	d1 := r.SmoothedDepth()
	if d1 <= 0 {
		t.Errorf("SmoothedDepth() = %f after Update(50), want > 0", d1)
	}

	r.Update(50)
	d2 := r.SmoothedDepth()
	if d2 <= d1 {
		t.Errorf("SmoothedDepth() should increase: %f -> %f", d1, d2)
	}
}

func TestRatio_SmoothedDepth_Emergency(t *testing.T) {
	r := NewRatio(3)
	r.Update(200) // Emergency: sets ewmaDepth = queueDepth
	d := r.SmoothedDepth()
	if d != 200 {
		t.Errorf("SmoothedDepth() = %f after emergency Update(200), want 200", d)
	}
}

func TestRatio_Update_SmoothedEmergency(t *testing.T) {
	// Test the smoothed emergency path (target == 1 via EWMA, not raw >100)
	// Start with high EWMA from emergency, then keep feeding high values
	// that are <= 100 raw but keep EWMA > 100
	r := NewRatio(3)
	r.Update(200) // emergency raw: sets EWMA=200, current=1

	// Now feed 100 (not raw emergency) but EWMA stays > 100
	// EWMA = 0.3*100 + 0.7*200 = 30 + 140 = 170 > 100 → target=1 (smoothed emergency)
	r.Update(100)
	if r.Current() != 1 {
		t.Errorf("Current = %d, want 1 (smoothed emergency path)", r.Current())
	}
}

func TestRatio_Update_HysteresisBlocks(t *testing.T) {
	// Test that non-emergency changes are blocked when stableCount < 3
	r := NewRatio(3)

	// Feed alternating values to prevent stableCount from reaching 3
	r.Update(5)  // EWMA=1.5, target=6 (base*2=6)
	r.Update(30) // EWMA~10.05, target=3 (normal)
	r.Update(5)  // EWMA~8.535, target=6 (stealth)

	// Current should still be 3 (initial) because hysteresis blocks changes
	if r.Current() != 3 {
		t.Errorf("Current = %d, want 3 (hysteresis should block)", r.Current())
	}
}

func TestRatio_Update_StepUpAndDown(t *testing.T) {
	// Start with base=5, initial current=5. Empty queue → target=min(5*2,8)=8.
	// After 3 consecutive updates at same target, hysteresis clears.
	// Then each update steps current one closer to target.
	r := NewRatio(5)

	// Update(0) x3: stableCount reaches 3, current steps 5→6
	for i := 0; i < 3; i++ {
		r.Update(0)
	}
	if r.Current() != 6 {
		t.Errorf("after 3 updates: Current = %d, want 6", r.Current())
	}

	// Update(0) again: current steps 6→7
	r.Update(0)
	if r.Current() != 7 {
		t.Errorf("after 4 updates: Current = %d, want 7", r.Current())
	}

	// Now feed high queue to bring target down to 2 (EWMA 50-100).
	// Feed 80 repeatedly to get EWMA close to 80.
	for i := 0; i < 20; i++ {
		r.Update(80)
	}
	// EWMA converges toward 80, target=2. After hysteresis + stepping, current goes down.
	if r.Current() >= 7 {
		t.Errorf("Current = %d, want < 7 after sustained high queue", r.Current())
	}
}

// ---------------------------------------------------------------------------
// jitter.go tests - Wait successful path
// ---------------------------------------------------------------------------

func TestJitter_Wait_Success(t *testing.T) {
	// Use a small maxMs so the wait completes quickly
	j := NewJitter(5, 42) // max 5ms

	ctx := context.Background()
	start := time.Now()
	err := j.Wait(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Wait() returned error: %v", err)
	}
	// Should complete within a reasonable time (5ms delay + overhead)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Wait() took %v, expected < 100ms for maxMs=5", elapsed)
	}
}

func TestJitter_Wait_DeadlineExceeded(t *testing.T) {
	// Use a very large maxMs so the jitter delay is long,
	// but cancel the context via deadline before it fires
	j := NewJitter(10000, 42) // 10s max

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := j.Wait(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("Wait() = %v, want context.DeadlineExceeded", err)
	}
}

// ---------------------------------------------------------------------------
// Timing profile field verification
// ---------------------------------------------------------------------------

func TestTimingProfiles_FieldsNonZero(t *testing.T) {
	profiles := []*TimingProfile{
		ProfileCasualBrowser,
		ProfilePowerUser,
		ProfileAPIWorker,
		ProfileDashboardMonitor,
	}

	for _, p := range profiles {
		if p.Name == "" {
			t.Errorf("profile has empty Name")
		}
		if p.BurstSizeMin <= 0 {
			t.Errorf("%s: BurstSizeMin = %d, want > 0", p.Name, p.BurstSizeMin)
		}
		if p.BurstSizeMax < p.BurstSizeMin {
			t.Errorf("%s: BurstSizeMax (%d) < BurstSizeMin (%d)", p.Name, p.BurstSizeMax, p.BurstSizeMin)
		}
		if p.BurstSpacing <= 0 {
			t.Errorf("%s: BurstSpacing = %v, want > 0", p.Name, p.BurstSpacing)
		}
		if p.ReadingPauseMin <= 0 {
			t.Errorf("%s: ReadingPauseMin = %v, want > 0", p.Name, p.ReadingPauseMin)
		}
		if p.ReadingPauseMax <= p.ReadingPauseMin {
			t.Errorf("%s: ReadingPauseMax (%v) <= ReadingPauseMin (%v)", p.Name, p.ReadingPauseMax, p.ReadingPauseMin)
		}
		if p.NavProbability <= 0 || p.NavProbability > 1 {
			t.Errorf("%s: NavProbability = %f, want (0, 1]", p.Name, p.NavProbability)
		}
	}
}
