package fakedata

import (
	"fmt"
	"sort"
	"sync"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// knownScenarios is the set of valid scenario names.
var knownScenarios = map[string]bool{
	"ecommerce": true,
	"iot":       true,
	"saas":      true,
	"blog":      true,
	"project":   true,
}

// MultiEngine manages multiple simultaneous scenario engines.
type MultiEngine struct {
	engines         map[string]*Engine
	mu              sync.RWMutex
	defaultScenario string
}

// NewMultiEngine creates a multi-engine with one initial engine for defaultScenario.
func NewMultiEngine(defaultScenario string, seed int64) *MultiEngine {
	if defaultScenario == "" {
		defaultScenario = "ecommerce"
	}
	me := &MultiEngine{
		engines:         make(map[string]*Engine),
		defaultScenario: defaultScenario,
	}
	me.engines[defaultScenario] = NewEngine(defaultScenario, seed)
	return me
}

// AddScenario adds a new scenario engine. Returns an error if the scenario
// name is not one of the known scenarios or if it already exists.
func (me *MultiEngine) AddScenario(name string, seed int64) error {
	if !knownScenarios[name] {
		return fmt.Errorf("unknown scenario: %q", name)
	}
	me.mu.Lock()
	defer me.mu.Unlock()
	if _, exists := me.engines[name]; exists {
		return fmt.Errorf("scenario %q already exists", name)
	}
	me.engines[name] = NewEngine(name, seed)
	return nil
}

// RemoveScenario removes a scenario engine. The default scenario cannot be
// removed. Returns an error if the name is the default or not found.
func (me *MultiEngine) RemoveScenario(name string) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	if name == me.defaultScenario {
		return fmt.Errorf("cannot remove default scenario %q", name)
	}
	if _, exists := me.engines[name]; !exists {
		return fmt.Errorf("scenario %q not found", name)
	}
	delete(me.engines, name)
	return nil
}

// Execute processes a SQL query against the specified scenario engine.
// If scenario is empty, the default scenario is used.
func (me *MultiEngine) Execute(scenario, query string) *pgwire.QueryResult {
	if scenario == "" {
		scenario = me.defaultScenario
	}
	me.mu.RLock()
	eng, ok := me.engines[scenario]
	me.mu.RUnlock()
	if !ok {
		return &pgwire.QueryResult{
			Type:  pgwire.ResultError,
			Error: &pgwire.ErrorDetail{Message: fmt.Sprintf("scenario %q not found", scenario)},
		}
	}
	return eng.Execute(query)
}

// Scenarios returns a sorted list of active scenario names.
func (me *MultiEngine) Scenarios() []string {
	me.mu.RLock()
	defer me.mu.RUnlock()
	names := make([]string, 0, len(me.engines))
	for name := range me.engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ScenarioCount returns the number of active scenario engines.
func (me *MultiEngine) ScenarioCount() int {
	me.mu.RLock()
	defer me.mu.RUnlock()
	return len(me.engines)
}

// DefaultScenario returns the name of the default scenario.
func (me *MultiEngine) DefaultScenario() string {
	return me.defaultScenario
}

// HasScenario reports whether the named scenario is active.
func (me *MultiEngine) HasScenario(name string) bool {
	me.mu.RLock()
	defer me.mu.RUnlock()
	_, ok := me.engines[name]
	return ok
}
