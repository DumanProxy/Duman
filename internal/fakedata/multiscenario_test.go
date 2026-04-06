package fakedata

import (
	"testing"

	"github.com/dumanproxy/duman/internal/pgwire"
)

func TestNewMultiEngine(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if me == nil {
		t.Fatal("NewMultiEngine returned nil")
	}
	if me.DefaultScenario() != "ecommerce" {
		t.Errorf("DefaultScenario() = %q, want %q", me.DefaultScenario(), "ecommerce")
	}
	if me.ScenarioCount() != 1 {
		t.Errorf("ScenarioCount() = %d, want 1", me.ScenarioCount())
	}
	if !me.HasScenario("ecommerce") {
		t.Error("HasScenario(ecommerce) = false, want true")
	}
}

func TestNewMultiEngine_EmptyDefault(t *testing.T) {
	me := NewMultiEngine("", 42)
	if me.DefaultScenario() != "ecommerce" {
		t.Errorf("DefaultScenario() = %q, want %q", me.DefaultScenario(), "ecommerce")
	}
}

func TestAddScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if err := me.AddScenario("iot", 99); err != nil {
		t.Fatalf("AddScenario(iot) returned error: %v", err)
	}
	if me.ScenarioCount() != 2 {
		t.Errorf("ScenarioCount() = %d, want 2", me.ScenarioCount())
	}
	if !me.HasScenario("iot") {
		t.Error("HasScenario(iot) = false, want true")
	}
}

func TestAddScenario_Duplicate(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if err := me.AddScenario("ecommerce", 99); err == nil {
		t.Error("AddScenario(ecommerce) should return error for duplicate")
	}
}

func TestAddScenario_InvalidName(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	err := me.AddScenario("nosuchscenario", 99)
	if err == nil {
		t.Fatal("AddScenario with invalid name should return error")
	}
}

func TestRemoveScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if err := me.AddScenario("iot", 99); err != nil {
		t.Fatalf("AddScenario(iot): %v", err)
	}
	if err := me.RemoveScenario("iot"); err != nil {
		t.Fatalf("RemoveScenario(iot): %v", err)
	}
	if me.HasScenario("iot") {
		t.Error("HasScenario(iot) = true after removal")
	}
	if me.ScenarioCount() != 1 {
		t.Errorf("ScenarioCount() = %d, want 1", me.ScenarioCount())
	}
}

func TestRemoveScenario_Default(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	err := me.RemoveScenario("ecommerce")
	if err == nil {
		t.Fatal("RemoveScenario(default) should return error")
	}
}

func TestRemoveScenario_NotFound(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	err := me.RemoveScenario("iot")
	if err == nil {
		t.Fatal("RemoveScenario(nonexistent) should return error")
	}
}

func TestExecute_DefaultScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	result := me.Execute("", "SELECT 1")
	if result == nil {
		t.Fatal("Execute returned nil")
	}
	if result.Type == pgwire.ResultError {
		t.Errorf("Execute returned error: %v", result.Error)
	}
}

func TestExecute_SpecificScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if err := me.AddScenario("iot", 99); err != nil {
		t.Fatalf("AddScenario(iot): %v", err)
	}
	result := me.Execute("iot", "SELECT 1")
	if result == nil {
		t.Fatal("Execute returned nil")
	}
	if result.Type == pgwire.ResultError {
		t.Errorf("Execute(iot) returned error: %v", result.Error)
	}
}

func TestExecute_UnknownScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	result := me.Execute("nosuch", "SELECT 1")
	if result == nil {
		t.Fatal("Execute returned nil")
	}
	if result.Type != pgwire.ResultError {
		t.Errorf("Execute(nosuch) type = %d, want ResultError", result.Type)
	}
}

func TestScenarios_Sorted(t *testing.T) {
	me := NewMultiEngine("saas", 42)
	_ = me.AddScenario("ecommerce", 1)
	_ = me.AddScenario("blog", 2)
	_ = me.AddScenario("iot", 3)

	scenarios := me.Scenarios()
	expected := []string{"blog", "ecommerce", "iot", "saas"}
	if len(scenarios) != len(expected) {
		t.Fatalf("Scenarios() len = %d, want %d", len(scenarios), len(expected))
	}
	for i, name := range expected {
		if scenarios[i] != name {
			t.Errorf("Scenarios()[%d] = %q, want %q", i, scenarios[i], name)
		}
	}
}

func TestHasScenario(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if !me.HasScenario("ecommerce") {
		t.Error("HasScenario(ecommerce) = false, want true")
	}
	if me.HasScenario("iot") {
		t.Error("HasScenario(iot) = true, want false")
	}
	_ = me.AddScenario("iot", 99)
	if !me.HasScenario("iot") {
		t.Error("HasScenario(iot) = false after add, want true")
	}
}

func TestScenarioCount(t *testing.T) {
	me := NewMultiEngine("ecommerce", 42)
	if me.ScenarioCount() != 1 {
		t.Errorf("ScenarioCount() = %d, want 1", me.ScenarioCount())
	}
	_ = me.AddScenario("iot", 1)
	_ = me.AddScenario("blog", 2)
	if me.ScenarioCount() != 3 {
		t.Errorf("ScenarioCount() = %d, want 3", me.ScenarioCount())
	}
	_ = me.RemoveScenario("iot")
	if me.ScenarioCount() != 2 {
		t.Errorf("ScenarioCount() = %d, want 2 after removal", me.ScenarioCount())
	}
}
