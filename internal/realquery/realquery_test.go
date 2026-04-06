package realquery

import (
	"strings"
	"testing"
	"time"
)

func TestEngine_NextBurst(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	batch := e.NextBurst()
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(batch.Queries) == 0 {
		t.Fatal("expected at least one query")
	}
	if batch.Page == "" {
		t.Error("expected non-empty page")
	}

	// Verify queries are valid SQL
	for _, q := range batch.Queries {
		q = strings.TrimSpace(q)
		upper := strings.ToUpper(q)
		if !strings.HasPrefix(upper, "SELECT") &&
			!strings.HasPrefix(upper, "INSERT") &&
			!strings.HasPrefix(upper, "UPDATE") &&
			!strings.HasPrefix(upper, "DELETE") {
			t.Errorf("invalid query: %q", q)
		}
	}
}

func TestEngine_MultipleBursts(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	pages := make(map[string]bool)
	for i := 0; i < 20; i++ {
		batch := e.NextBurst()
		pages[batch.Page] = true
	}

	// Should visit multiple different pages
	if len(pages) < 2 {
		t.Errorf("expected multiple pages, got %d: %v", len(pages), pages)
	}
}

func TestEngine_RandomAnalyticsEvent(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	q := e.RandomAnalyticsEvent()
	if !strings.HasPrefix(strings.ToUpper(q), "INSERT INTO ANALYTICS_EVENTS") {
		t.Errorf("expected analytics INSERT, got %q", q)
	}
}

func TestEngine_RandomBackgroundQuery(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	q := e.RandomBackgroundQuery()
	if !strings.HasPrefix(strings.ToUpper(q), "SELECT") {
		t.Errorf("expected SELECT, got %q", q)
	}
}

func TestEngine_BurstSpacing(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	d := e.BurstSpacing()
	if d < 15*time.Millisecond || d > 25*time.Millisecond {
		t.Errorf("BurstSpacing = %v, want 15-25ms", d)
	}
}

func TestEngine_ReadingPause(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	d := e.ReadingPause()
	if d < 2*time.Second || d > 30*time.Second {
		t.Errorf("ReadingPause = %v, want 2-30s", d)
	}
}

func TestEngine_CurrentPage(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	if e.CurrentPage() != "/" {
		t.Errorf("initial page = %q, want /", e.CurrentPage())
	}

	e.NextBurst()
	if e.CurrentPage() == "" {
		t.Error("page should be set after burst")
	}
}

func TestRandomUUID(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	uuid := randomUUID(e.rng)

	if len(uuid) != 36 {
		t.Errorf("uuid length = %d, want 36", len(uuid))
	}
	// Should have hyphens at positions 8, 13, 18, 23
	if uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Errorf("uuid format wrong: %q", uuid)
	}
}

func TestEngine_BurstSizes(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	for i := 0; i < 50; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) < 1 || len(batch.Queries) > 10 {
			t.Errorf("burst %d: size = %d, expected 1-10", i, len(batch.Queries))
		}
	}
}

func TestEngine_AddToCart(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	// Populate ViewedIDs so addToCart picks from them
	e.state.ViewedIDs = []int{42, 100}

	queries := e.addToCart()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
	if !strings.HasPrefix(strings.ToUpper(queries[0]), "INSERT") {
		t.Errorf("expected INSERT, got %q", queries[0])
	}
	if !strings.HasPrefix(strings.ToUpper(queries[1]), "SELECT") {
		t.Errorf("expected SELECT for cart count, got %q", queries[1])
	}
}

func TestEngine_AddToCart_NoViewed(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	// ViewedIDs is empty, so addToCart uses a random product ID
	queries := e.addToCart()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
	if !strings.HasPrefix(strings.ToUpper(queries[0]), "INSERT INTO CART_ITEMS") {
		t.Errorf("expected INSERT INTO CART_ITEMS, got %q", queries[0])
	}
}

func TestEngine_AddToCart_UpdatesCartState(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	before := len(e.state.CartItems)
	e.addToCart()
	after := len(e.state.CartItems)
	if after != before+1 {
		t.Errorf("CartItems should grow by 1: before=%d, after=%d", before, after)
	}
}

func TestEngine_DefaultScenario(t *testing.T) {
	e := NewEngine("unknown_scenario", 42)

	batch := e.NextBurst()
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(batch.Queries) == 0 {
		t.Error("expected at least one query")
	}
	if batch.Page == "" {
		t.Error("expected non-empty page")
	}
}

func TestHexEncode(t *testing.T) {
	// 16 zero bytes should produce a 36-char UUID-formatted string
	b := make([]byte, 16)
	result := hexEncode(b)
	if len(result) != 36 {
		t.Errorf("hexEncode length = %d, want 36", len(result))
	}
	// Check hyphen positions: 8, 13, 18, 23
	if result[8] != '-' || result[13] != '-' || result[18] != '-' || result[23] != '-' {
		t.Errorf("hexEncode hyphens wrong: %q", result)
	}
}

func TestHexEncode_AllFF(t *testing.T) {
	b := make([]byte, 16)
	for i := range b {
		b[i] = 0xFF
	}
	result := hexEncode(b)
	if len(result) != 36 {
		t.Errorf("hexEncode length = %d, want 36", len(result))
	}
	// All hex digits should be 'f'
	for i, c := range result {
		if c == '-' {
			continue
		}
		if c != 'f' {
			t.Errorf("hexEncode[%d] = %c, want 'f' for 0xFF input", i, c)
		}
	}
}

func TestHexEncode_KnownValue(t *testing.T) {
	b := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	result := hexEncode(b)
	expected := "01234567-89ab-cdef-0123-456789abcdef"
	if result != expected {
		t.Errorf("hexEncode = %q, want %q", result, expected)
	}
}
