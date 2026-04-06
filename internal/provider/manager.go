package provider

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Manager manages multiple relay providers with weighted selection.
// Supports mixed protocol providers (PostgreSQL, MySQL, REST) simultaneously.
type Manager struct {
	providers    []weightedProvider
	mu           sync.RWMutex
	rng          *rand.Rand
	logger       *slog.Logger
	healthCancel context.CancelFunc
}

type weightedProvider struct {
	provider Provider
	weight   int
}

// NewManager creates a provider manager.
func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
		logger: logger,
	}
}

// Add adds a provider with a weight.
func (m *Manager) Add(p Provider, weight int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if weight <= 0 {
		weight = 1
	}
	m.providers = append(m.providers, weightedProvider{provider: p, weight: weight})
}

// ConnectAll connects all providers with staggered timing.
func (m *Manager) ConnectAll(ctx context.Context) error {
	m.mu.RLock()
	providers := make([]weightedProvider, len(m.providers))
	copy(providers, m.providers)
	m.mu.RUnlock()

	for i, wp := range providers {
		m.logger.Info("connecting provider",
			"type", wp.provider.Type(),
			"index", i+1,
			"total", len(providers))

		if err := wp.provider.Connect(ctx); err != nil {
			return err
		}
		// Stagger connections (30s-2m between each, except first)
		if i < len(providers)-1 {
			delay := time.Duration(30+m.rng.Intn(90)) * time.Second
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Start health checker
	healthCtx, cancel := context.WithCancel(ctx)
	m.healthCancel = cancel
	go m.healthCheckLoop(healthCtx)

	return nil
}

// healthCheckLoop periodically checks all providers and logs status changes.
func (m *Manager) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			for _, wp := range m.providers {
				healthy := wp.provider.IsHealthy()
				if !healthy {
					m.logger.Warn("provider unhealthy",
						"type", wp.provider.Type())
				}
			}
			m.mu.RUnlock()
		}
	}
}

// Select returns a healthy provider using weighted random selection.
func (m *Manager) Select() Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var healthy []weightedProvider
	totalWeight := 0
	for _, wp := range m.providers {
		if wp.provider.IsHealthy() {
			healthy = append(healthy, wp)
			totalWeight += wp.weight
		}
	}

	if len(healthy) == 0 {
		return nil
	}

	r := m.rng.Intn(totalWeight)
	selected := healthy[0].provider
	cumulative := 0
	for _, wp := range healthy {
		cumulative += wp.weight
		if r < cumulative {
			selected = wp.provider
			break
		}
	}

	return selected
}

// SelectByType returns a healthy provider of the specified type.
func (m *Manager) SelectByType(providerType string) Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matching []weightedProvider
	totalWeight := 0
	for _, wp := range m.providers {
		if wp.provider.IsHealthy() && wp.provider.Type() == providerType {
			matching = append(matching, wp)
			totalWeight += wp.weight
		}
	}

	if len(matching) == 0 {
		return nil
	}

	r := m.rng.Intn(totalWeight)
	cumulative := 0
	for _, wp := range matching {
		cumulative += wp.weight
		if r < cumulative {
			return wp.provider
		}
	}
	return matching[0].provider
}

// ProtocolCounts returns the count of providers by type.
func (m *Manager) ProtocolCounts() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[string]int)
	for _, wp := range m.providers {
		counts[wp.provider.Type()]++
	}
	return counts
}

// All returns all providers.
func (m *Manager) All() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Provider
	for _, wp := range m.providers {
		result = append(result, wp.provider)
	}
	return result
}

// HealthyCount returns the number of healthy providers.
func (m *Manager) HealthyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, wp := range m.providers {
		if wp.provider.IsHealthy() {
			count++
		}
	}
	return count
}

// CloseAll closes all providers and stops health checking.
func (m *Manager) CloseAll() {
	if m.healthCancel != nil {
		m.healthCancel()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, wp := range m.providers {
		wp.provider.Close()
	}
}
