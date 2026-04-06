package pool

import "fmt"

// Tier represents the trust level of a relay in the pool.
type Tier int

const (
	// TierCommunity is a new relay that can only act as a relay node (no exit).
	TierCommunity Tier = 1
	// TierVerified is a relay that has been running 3+ months with good uptime.
	TierVerified Tier = 2
	// TierTrusted is a verified relay with exit capability from a known operator.
	TierTrusted Tier = 3
)

// String returns a human-readable tier name.
func (t Tier) String() string {
	switch t {
	case TierCommunity:
		return "community"
	case TierVerified:
		return "verified"
	case TierTrusted:
		return "trusted"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// CanExit reports whether relays of this tier are allowed to serve as exit nodes.
// Only TierTrusted relays may route traffic to the public internet.
func (t Tier) CanExit() bool {
	return t == TierTrusted
}

// Promotion thresholds.
const (
	promoteVerifiedDays    = 90   // 3 months minimum uptime
	promoteVerifiedUptime  = 95.0 // 95% uptime required for verified
	promoteTrustedDays     = 180  // 6 months minimum uptime
	promoteTrustedUptime   = 99.0 // 99% uptime required for trusted
)

// AutoPromote determines the appropriate tier for a relay based on its
// operational history. It never demotes — it returns the highest tier
// the relay qualifies for, or the relay's current tier if it already
// exceeds the computed result.
func AutoPromote(relay *RelayInfo, uptimeDays int, uptimePercent float64) Tier {
	computed := TierCommunity

	if uptimeDays >= promoteTrustedDays && uptimePercent >= promoteTrustedUptime {
		computed = TierTrusted
	} else if uptimeDays >= promoteVerifiedDays && uptimePercent >= promoteVerifiedUptime {
		computed = TierVerified
	}

	// Never demote: return the higher of current and computed tier.
	if relay.Tier > computed {
		return relay.Tier
	}
	return computed
}
