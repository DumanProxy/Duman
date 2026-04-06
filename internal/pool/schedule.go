package pool

import (
	"math/rand"
	"sort"
	"time"
)

// RotationSchedule computes a deterministic relay rotation schedule from
// a shared seed. Both client and relay can independently derive the same
// schedule without communication, enabling coordinated relay switching.
//
// Two slot types operate at different frequencies:
//   - Fast slots (relay): rotate every 30s-5min for traffic relays
//   - Slow slots (exit): rotate every 15-30min for exit nodes
type RotationSchedule struct {
	seed        int64
	relays      []*RelayInfo
	rng         *rand.Rand
	fastSlotMin time.Duration
	fastSlotMax time.Duration
	slowSlotMin time.Duration
	slowSlotMax time.Duration
	preWarmDur  time.Duration
}

// ScheduleConfig configures the rotation schedule parameters.
type ScheduleConfig struct {
	Seed            int64
	FastSlotMin     time.Duration // default 30s
	FastSlotMax     time.Duration // default 5min
	SlowSlotMin     time.Duration // default 15min
	SlowSlotMax     time.Duration // default 30min
	PreWarmDuration time.Duration // default 30s
}

// NewSchedule creates a deterministic rotation schedule. The relay list is
// sorted by address to ensure consistent ordering regardless of input order.
func NewSchedule(relays []*RelayInfo, cfg ScheduleConfig) *RotationSchedule {
	if cfg.FastSlotMin <= 0 {
		cfg.FastSlotMin = 30 * time.Second
	}
	if cfg.FastSlotMax <= 0 {
		cfg.FastSlotMax = 5 * time.Minute
	}
	if cfg.SlowSlotMin <= 0 {
		cfg.SlowSlotMin = 15 * time.Minute
	}
	if cfg.SlowSlotMax <= 0 {
		cfg.SlowSlotMax = 30 * time.Minute
	}
	if cfg.PreWarmDuration <= 0 {
		cfg.PreWarmDuration = 30 * time.Second
	}

	// Sort relays deterministically by address.
	sorted := make([]*RelayInfo, len(relays))
	copy(sorted, relays)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Address < sorted[j].Address
	})

	return &RotationSchedule{
		seed:        cfg.Seed,
		relays:      sorted,
		rng:         rand.New(rand.NewSource(cfg.Seed)),
		fastSlotMin: cfg.FastSlotMin,
		fastSlotMax: cfg.FastSlotMax,
		slowSlotMin: cfg.SlowSlotMin,
		slowSlotMax: cfg.SlowSlotMax,
		preWarmDur:  cfg.PreWarmDuration,
	}
}

// CurrentSlots computes which relays should be active at time t.
// Returns relay slots (fast-rotating) and an exit slot (slow-rotating).
// If no exit-capable relays exist, exitSlot is nil.
func (s *RotationSchedule) CurrentSlots(t time.Time) (relaySlots []*RelayInfo, exitSlot *RelayInfo) {
	if len(s.relays) == 0 {
		return nil, nil
	}

	// Separate relays by exit capability.
	var relayPool []*RelayInfo
	var exitPool []*RelayInfo
	for _, r := range s.relays {
		if r.State == StateBlocked {
			continue
		}
		if r.Tier.CanExit() {
			exitPool = append(exitPool, r)
		}
		relayPool = append(relayPool, r)
	}

	// Compute fast slot index.
	if len(relayPool) > 0 {
		fastIdx := s.slotIndex(t, s.fastSlotMin, s.fastSlotMax, len(relayPool))
		relaySlots = []*RelayInfo{relayPool[fastIdx]}

		// Add a second relay slot if pool is large enough.
		if len(relayPool) > 1 {
			secondIdx := (fastIdx + 1) % len(relayPool)
			relaySlots = append(relaySlots, relayPool[secondIdx])
		}
	}

	// Compute slow slot for exit node.
	if len(exitPool) > 0 {
		slowIdx := s.slotIndex(t, s.slowSlotMin, s.slowSlotMax, len(exitPool))
		exitSlot = exitPool[slowIdx]
	}

	return relaySlots, exitSlot
}

// NextRotation returns the time of the next fast-slot rotation after t.
func (s *RotationSchedule) NextRotation(t time.Time) time.Time {
	epoch := s.epoch()
	slotDur := s.deterministicDuration(s.fastSlotMin, s.fastSlotMax)

	elapsed := t.Sub(epoch)
	if elapsed < 0 {
		return epoch
	}

	currentSlotStart := epoch.Add((elapsed / slotDur) * slotDur)
	return currentSlotStart.Add(slotDur)
}

// ShouldPreWarm reports whether the current time is within the pre-warm
// window before the next rotation.
func (s *RotationSchedule) ShouldPreWarm(t time.Time) bool {
	next := s.NextRotation(t)
	return next.Sub(t) <= s.preWarmDur
}

// slotIndex computes a deterministic slot index for time t given the slot
// duration range and pool size. The same seed + time always yields the
// same index.
func (s *RotationSchedule) slotIndex(t time.Time, minDur, maxDur time.Duration, poolSize int) int {
	if poolSize == 0 {
		return 0
	}

	epoch := s.epoch()
	slotDur := s.deterministicDuration(minDur, maxDur)

	elapsed := t.Sub(epoch)
	if elapsed < 0 {
		elapsed = 0
	}

	slotNumber := int64(elapsed / slotDur)

	// Use slot number + seed to deterministically pick a relay index.
	// This ensures both client and relay compute the same index.
	slotRng := rand.New(rand.NewSource(s.seed + slotNumber))
	return slotRng.Intn(poolSize)
}

// deterministicDuration returns a fixed duration within [min, max] derived
// from the seed. The same seed always produces the same duration.
func (s *RotationSchedule) deterministicDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	// Fresh RNG from seed for this calculation to be reproducible.
	r := rand.New(rand.NewSource(s.seed))
	spread := int64(max - min)
	return min + time.Duration(r.Int63n(spread))
}

// epoch returns a fixed reference point for slot calculations.
// Using Unix epoch (1970-01-01T00:00:00Z) as the common baseline.
func (s *RotationSchedule) epoch() time.Time {
	return time.Unix(0, 0)
}
