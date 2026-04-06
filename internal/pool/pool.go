package pool

import (
	"log/slog"
	"sort"
	"sync"
	"time"
)

// RelayState represents the operational state of a relay.
type RelayState int

const (
	// StateHealthy means the relay is reachable and functioning normally.
	StateHealthy RelayState = iota
	// StateFailed means the relay failed a recent health check.
	StateFailed
	// StateBlocked means the relay has been detected as blocked (ISP/DPI).
	StateBlocked
	// StatePreWarming means the relay is being prepared for upcoming rotation.
	StatePreWarming
)

// String returns a human-readable state name.
func (s RelayState) String() string {
	switch s {
	case StateHealthy:
		return "healthy"
	case StateFailed:
		return "failed"
	case StateBlocked:
		return "blocked"
	case StatePreWarming:
		return "pre-warming"
	default:
		return "unknown"
	}
}

// RelayInfo holds metadata and runtime state for a single relay.
type RelayInfo struct {
	Address   string        // host:port
	Protocol  string        // postgresql, mysql, rest
	Tier      Tier          // community, verified, trusted
	State     RelayState    // current operational state
	Weight    int           // selection weight (higher = preferred)
	FailCount int           // consecutive health check failures
	LastCheck time.Time     // last successful health check
	Latency   time.Duration // last measured latency
}

// PoolStats holds aggregate statistics about the pool.
type PoolStats struct {
	Total      int
	Healthy    int
	Failed     int
	Blocked    int
	PreWarming int
	Active     int
	ByTier     map[Tier]int
	ByProtocol map[string]int
}

// Pool manages a collection of relay nodes with state tracking and
// active set selection. It sits above the provider.Manager layer:
// Pool decides which relays are active, Manager handles weighted
// selection among active providers.
type Pool struct {
	relays    []*RelayInfo
	maxActive int
	active    []*RelayInfo
	mu        sync.RWMutex
	logger    *slog.Logger
}

// PoolConfig configures a new Pool.
type PoolConfig struct {
	Relays    []RelayInfo
	MaxActive int // default 3
	Logger    *slog.Logger
}

// NewPool creates a relay pool from the given configuration.
func NewPool(cfg PoolConfig) *Pool {
	if cfg.MaxActive <= 0 {
		cfg.MaxActive = 3
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	p := &Pool{
		maxActive: cfg.MaxActive,
		logger:    cfg.Logger,
	}

	for i := range cfg.Relays {
		r := cfg.Relays[i] // copy
		if r.Weight <= 0 {
			r.Weight = 1
		}
		if r.State == 0 {
			r.State = StateHealthy
		}
		p.relays = append(p.relays, &r)
	}

	p.active = p.selectBest()
	return p
}

// ActiveRelays returns a snapshot of the currently active relay set.
func (p *Pool) ActiveRelays() []*RelayInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*RelayInfo, len(p.active))
	copy(out, p.active)
	return out
}

// AddRelay adds a new relay to the pool. If the relay address already exists,
// the call is a no-op.
func (p *Pool) AddRelay(info RelayInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range p.relays {
		if r.Address == info.Address {
			return
		}
	}

	if info.Weight <= 0 {
		info.Weight = 1
	}
	p.relays = append(p.relays, &info)
	p.logger.Info("relay added", "address", info.Address, "protocol", info.Protocol, "tier", info.Tier)
}

// RemoveRelay removes a relay by address. Also removes it from the active set.
func (p *Pool) RemoveRelay(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, r := range p.relays {
		if r.Address == addr {
			p.relays = append(p.relays[:i], p.relays[i+1:]...)
			break
		}
	}

	for i, r := range p.active {
		if r.Address == addr {
			p.active = append(p.active[:i], p.active[i+1:]...)
			break
		}
	}

	p.logger.Info("relay removed", "address", addr)
}

// MarkFailed increments the failure count for a relay and sets its state
// to StateFailed.
func (p *Pool) MarkFailed(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range p.relays {
		if r.Address == addr {
			r.FailCount++
			r.State = StateFailed
			p.logger.Warn("relay marked failed",
				"address", addr,
				"fail_count", r.FailCount)
			return
		}
	}
}

// MarkBlocked marks a relay as blocked (e.g., by ISP or DPI).
// Blocked relays are excluded from active selection.
func (p *Pool) MarkBlocked(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range p.relays {
		if r.Address == addr {
			r.State = StateBlocked
			r.FailCount = 0
			p.logger.Warn("relay marked blocked", "address", addr)
			return
		}
	}
}

// MarkHealthy resets a relay to healthy state with the measured latency.
func (p *Pool) MarkHealthy(addr string, latency time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range p.relays {
		if r.Address == addr {
			r.State = StateHealthy
			r.FailCount = 0
			r.Latency = latency
			r.LastCheck = time.Now()
			return
		}
	}
}

// SelectActive recomputes the active relay set by choosing the best N relays
// from the pool (where N = maxActive). Returns the new active set.
func (p *Pool) SelectActive() []*RelayInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.active = p.selectBest()
	return copyRelays(p.active)
}

// selectBest picks the best relays for the active set. Must be called with
// the lock held. Selection criteria:
//  1. Only healthy or pre-warming relays are candidates.
//  2. Higher tier relays are preferred.
//  3. Within the same tier, higher weight is preferred.
//  4. Within the same weight, lower latency is preferred.
func (p *Pool) selectBest() []*RelayInfo {
	var candidates []*RelayInfo
	for _, r := range p.relays {
		if r.State == StateHealthy || r.State == StatePreWarming {
			candidates = append(candidates, r)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		// Higher tier first.
		if a.Tier != b.Tier {
			return a.Tier > b.Tier
		}
		// Higher weight first.
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		// Lower latency first (zero latency sorts last — no measurement yet).
		if a.Latency != b.Latency {
			if a.Latency == 0 {
				return false
			}
			if b.Latency == 0 {
				return true
			}
			return a.Latency < b.Latency
		}
		return false
	})

	n := p.maxActive
	if n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

// Stats returns aggregate pool statistics.
func (p *Pool) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	s := PoolStats{
		Total:      len(p.relays),
		Active:     len(p.active),
		ByTier:     make(map[Tier]int),
		ByProtocol: make(map[string]int),
	}
	for _, r := range p.relays {
		switch r.State {
		case StateHealthy:
			s.Healthy++
		case StateFailed:
			s.Failed++
		case StateBlocked:
			s.Blocked++
		case StatePreWarming:
			s.PreWarming++
		}
		s.ByTier[r.Tier]++
		s.ByProtocol[r.Protocol]++
	}
	return s
}

// find returns the relay with the given address, or nil. Caller must hold lock.
func (p *Pool) find(addr string) *RelayInfo {
	for _, r := range p.relays {
		if r.Address == addr {
			return r
		}
	}
	return nil
}

func copyRelays(src []*RelayInfo) []*RelayInfo {
	out := make([]*RelayInfo, len(src))
	copy(out, src)
	return out
}
