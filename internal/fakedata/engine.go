package fakedata

import (
	"github.com/dumanproxy/duman/internal/pgwire"
)

// Executor is the interface satisfied by both Engine and GenericEngine.
type Executor interface {
	Execute(query string) *pgwire.QueryResult
}

// Engine wraps GenericEngine for backwards compatibility.
// NewEngine("ecommerce", seed) creates a GenericEngine via TemplateBuilder.
type Engine struct {
	generic *GenericEngine
}

// NewEngine creates a fake data engine for the given scenario.
// This is the backwards-compatible constructor that creates a GenericEngine internally.
func NewEngine(scenario string, seed int64) *Engine {
	if scenario == "" {
		scenario = "ecommerce"
	}

	builder := NewTemplateBuilder(scenario, seed, false)
	schema, err := builder.Build()
	if err != nil {
		// Fallback to ecommerce on unknown scenario
		builder = NewTemplateBuilder("ecommerce", seed, false)
		schema, _ = builder.Build()
	}

	generic := NewGenericEngine(schema, seed)
	return &Engine{generic: generic}
}

// Execute processes a SQL query and returns a result.
func (e *Engine) Execute(query string) *pgwire.QueryResult {
	return e.generic.Execute(query)
}

// Data returns the underlying GenericStore (replaces old ScenarioData accessor).
func (e *Engine) Data() *GenericStore {
	return e.generic.Store()
}

// Schema returns the schema definition.
func (e *Engine) Schema() *SchemaDefinition {
	return e.generic.Schema()
}

// Generic returns the underlying GenericEngine.
func (e *Engine) Generic() *GenericEngine {
	return e.generic
}
