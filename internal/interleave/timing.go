package interleave

import (
	"math/rand"
	"time"
)

// TimingProfile defines the timing characteristics of a simulated user type.
type TimingProfile struct {
	Name string

	// Burst characteristics
	BurstSizeMin int // minimum queries per burst
	BurstSizeMax int // maximum queries per burst
	BurstSpacing time.Duration // delay between queries within a burst

	// Reading pause between bursts
	ReadingPauseMin time.Duration
	ReadingPauseMax time.Duration

	// Background activity during reading pause
	BackgroundIntervalMin time.Duration
	BackgroundIntervalMax time.Duration

	// Navigation probability: chance of switching to a new page after a burst
	NavProbability float64
}

// Predefined timing profiles matching real user behavior patterns.
var (
	// ProfileCasualBrowser simulates a casual user slowly browsing.
	ProfileCasualBrowser = &TimingProfile{
		Name:                  "casual_browser",
		BurstSizeMin:          2,
		BurstSizeMax:          4,
		BurstSpacing:          25 * time.Millisecond,
		ReadingPauseMin:       8 * time.Second,
		ReadingPauseMax:       45 * time.Second,
		BackgroundIntervalMin: 10 * time.Second,
		BackgroundIntervalMax: 30 * time.Second,
		NavProbability:        0.6,
	}

	// ProfilePowerUser simulates a fast, experienced user.
	ProfilePowerUser = &TimingProfile{
		Name:                  "power_user",
		BurstSizeMin:          3,
		BurstSizeMax:          6,
		BurstSpacing:          12 * time.Millisecond,
		ReadingPauseMin:       1 * time.Second,
		ReadingPauseMax:       8 * time.Second,
		BackgroundIntervalMin: 3 * time.Second,
		BackgroundIntervalMax: 10 * time.Second,
		NavProbability:        0.8,
	}

	// ProfileAPIWorker simulates an automated API worker / batch process.
	ProfileAPIWorker = &TimingProfile{
		Name:                  "api_worker",
		BurstSizeMin:          5,
		BurstSizeMax:          12,
		BurstSpacing:          5 * time.Millisecond,
		ReadingPauseMin:       500 * time.Millisecond,
		ReadingPauseMax:       3 * time.Second,
		BackgroundIntervalMin: 2 * time.Second,
		BackgroundIntervalMax: 5 * time.Second,
		NavProbability:        0.95,
	}

	// ProfileDashboardMonitor simulates a user watching a monitoring dashboard.
	ProfileDashboardMonitor = &TimingProfile{
		Name:                  "dashboard_monitor",
		BurstSizeMin:          2,
		BurstSizeMax:          3,
		BurstSpacing:          20 * time.Millisecond,
		ReadingPauseMin:       15 * time.Second,
		ReadingPauseMax:       60 * time.Second,
		BackgroundIntervalMin: 5 * time.Second,
		BackgroundIntervalMax: 15 * time.Second,
		NavProbability:        0.3,
	}
)

// ProfileByName returns a timing profile by name.
func ProfileByName(name string) *TimingProfile {
	switch name {
	case "casual_browser":
		return ProfileCasualBrowser
	case "power_user":
		return ProfilePowerUser
	case "api_worker":
		return ProfileAPIWorker
	case "dashboard_monitor":
		return ProfileDashboardMonitor
	default:
		return ProfileCasualBrowser
	}
}

// TimingController applies a timing profile to control interleaving pacing.
type TimingController struct {
	profile *TimingProfile
	rng     *rand.Rand
}

// NewTimingController creates a timing controller for the given profile.
func NewTimingController(profile *TimingProfile, seed int64) *TimingController {
	return &TimingController{
		profile: profile,
		rng:     rand.New(rand.NewSource(seed)),
	}
}

// BurstSize returns a random burst size within the profile's range.
func (tc *TimingController) BurstSize() int {
	r := tc.profile.BurstSizeMax - tc.profile.BurstSizeMin
	if r <= 0 {
		return tc.profile.BurstSizeMin
	}
	return tc.profile.BurstSizeMin + tc.rng.Intn(r+1)
}

// BurstSpacing returns the inter-query delay with slight jitter.
func (tc *TimingController) BurstSpacing() time.Duration {
	base := tc.profile.BurstSpacing
	jitter := time.Duration(tc.rng.Intn(int(base/4) + 1))
	return base + jitter - base/8
}

// ReadingPause returns a random reading pause duration.
func (tc *TimingController) ReadingPause() time.Duration {
	min := tc.profile.ReadingPauseMin
	max := tc.profile.ReadingPauseMax
	r := max - min
	if r <= 0 {
		return min
	}
	return min + time.Duration(tc.rng.Int63n(int64(r)))
}

// BackgroundInterval returns a random background activity interval.
func (tc *TimingController) BackgroundInterval() time.Duration {
	min := tc.profile.BackgroundIntervalMin
	max := tc.profile.BackgroundIntervalMax
	r := max - min
	if r <= 0 {
		return min
	}
	return min + time.Duration(tc.rng.Int63n(int64(r)))
}

// ShouldNavigate returns true if the user should navigate to a new page.
func (tc *TimingController) ShouldNavigate() bool {
	return tc.rng.Float64() < tc.profile.NavProbability
}

// Profile returns the underlying timing profile.
func (tc *TimingController) Profile() *TimingProfile {
	return tc.profile
}
