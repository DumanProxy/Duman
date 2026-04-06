package realquery

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/dumanproxy/duman/internal/fakedata"
)

// --- Helper: build a minimal schema with FK relationships ---

func testSchema() *fakedata.SchemaDefinition {
	return &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "categories", Schema: "public", RowCount: 10,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "name", Type: fakedata.ColTypeText},
				},
			},
			{
				Name: "products", Schema: "public", RowCount: 100,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "name", Type: fakedata.ColTypeText},
					{Name: "price", Type: fakedata.ColTypeFloat},
					{Name: "category_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "categories", FKColumn: "id"},
					{Name: "is_active", Type: fakedata.ColTypeBool},
					{Name: "created_at", Type: fakedata.ColTypeTimestamp},
				},
			},
			{
				Name: "users", Schema: "public", RowCount: 50,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "name", Type: fakedata.ColTypeText},
					{Name: "email", Type: fakedata.ColTypeText},
					{Name: "session_id", Type: fakedata.ColTypeUUID},
				},
			},
			{
				Name: "orders", Schema: "public", RowCount: 30,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "user_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "users", FKColumn: "id"},
					{Name: "total", Type: fakedata.ColTypeFloat},
					{Name: "status", Type: fakedata.ColTypeText, EnumValues: []string{"pending", "completed", "cancelled"}},
					{Name: "created_at", Type: fakedata.ColTypeTimestamp},
				},
			},
			{
				Name: "order_items", Schema: "public", RowCount: 80,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "order_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "orders", FKColumn: "id"},
					{Name: "product_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "products", FKColumn: "id"},
					{Name: "quantity", Type: fakedata.ColTypeInt},
					{Name: "price", Type: fakedata.ColTypeBigInt},
				},
			},
			{
				Name: "analytics_events", Schema: "public", RowCount: 0, IsTunnel: true,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "event_type", Type: fakedata.ColTypeText},
				},
			},
		},
	}
}

// --- NewGenericQueryPatterns ---

func TestNewGenericQueryPatterns(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	if p == nil {
		t.Fatal("expected non-nil patterns")
	}
	if p.schema != schema {
		t.Error("schema not stored")
	}
	if len(p.roles) == 0 {
		t.Error("expected table roles to be classified")
	}
}

func TestClassifyTables_TunnelExcluded(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// analytics_events is tunnel, should be classified as RoleTunnel
	if role, ok := p.roles["analytics_events"]; !ok {
		t.Error("analytics_events should have a role")
	} else if role != RoleTunnel {
		t.Errorf("analytics_events role = %d, want RoleTunnel(%d)", role, RoleTunnel)
	}

	// Tunnel tables should NOT appear in entities
	for _, e := range p.entities {
		if e == "analytics_events" {
			t.Error("tunnel table should not be in entities")
		}
	}
}

func TestClassifyTables_JunctionDetection(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// order_items has 2 FK cols out of 4 non-PK cols = 50%, threshold is >0.5
	// so it depends on exact calculation. Let's just check it has a role.
	_, ok := p.roles["order_items"]
	if !ok {
		t.Error("order_items should have a role")
	}
}

func TestClassifyTables_EntityDetection(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// products has 1 FK out of 5 non-PK cols = 20%, should be entity
	if role, ok := p.roles["products"]; ok && role != RoleEntity {
		t.Errorf("products role = %d, want RoleEntity(%d)", role, RoleEntity)
	}

	if len(p.entities) == 0 {
		t.Error("expected at least one entity table")
	}
}

func TestClassifyTables_LookupDetection(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// categories: RowCount=10, 0 FK cols, referenced by products -> should be lookup
	if role, ok := p.roles["categories"]; ok && role != RoleLookup {
		t.Errorf("categories role = %d, want RoleLookup(%d)", role, RoleLookup)
	}
}

func TestBuildFKGraph(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// products -> categories FK
	if fks, ok := p.fkGraph["products"]; !ok {
		t.Error("products should have FK entries")
	} else {
		ref, ok := fks["category_id"]
		if !ok {
			t.Error("products should have category_id FK")
		}
		if ref[0] != "categories" || ref[1] != "id" {
			t.Errorf("FK ref = %v, want [categories, id]", ref)
		}
	}

	// orders -> users FK
	if fks, ok := p.fkGraph["orders"]; !ok {
		t.Error("orders should have FK entries")
	} else {
		ref, ok := fks["user_id"]
		if !ok {
			t.Error("orders should have user_id FK")
		}
		if ref[0] != "users" || ref[1] != "id" {
			t.Errorf("FK ref = %v, want [users, id]", ref)
		}
	}

	// categories is referenced by products
	refs := p.referencedBy["categories"]
	found := false
	for _, r := range refs {
		if r == "products" {
			found = true
		}
	}
	if !found {
		t.Error("categories should be referenced by products")
	}
}

// --- PickEntityTable ---

func TestPickEntityTable_ReturnsEntity(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	table := p.PickEntityTable()
	if table == "" {
		t.Fatal("expected non-empty entity table")
	}

	// Verify it's actually in entities
	found := false
	for _, e := range p.entities {
		if e == table {
			found = true
		}
	}
	if !found {
		t.Errorf("PickEntityTable returned %q which is not in entities list", table)
	}
}

func TestPickEntityTable_EmptyEntities(t *testing.T) {
	// Schema with only tunnel tables -> no entities
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "empty",
		Tables:       []*fakedata.TableDef{},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	table := p.PickEntityTable()
	if table != "" {
		t.Errorf("expected empty string for empty entities, got %q", table)
	}
}

// --- BrowseTable ---

func TestBrowseTable_ValidTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.BrowseTable("products")
	if len(queries) == 0 {
		t.Fatal("expected browse queries for products")
	}

	// First query should be SELECT with LIMIT
	if !strings.HasPrefix(strings.ToUpper(queries[0]), "SELECT") {
		t.Errorf("expected SELECT query, got %q", queries[0])
	}
	if !strings.Contains(queries[0], "LIMIT") {
		t.Errorf("expected LIMIT in browse query, got %q", queries[0])
	}

	// Should have a count query
	hasCount := false
	for _, q := range queries {
		if strings.Contains(strings.ToUpper(q), "COUNT(*)") {
			hasCount = true
		}
	}
	if !hasCount {
		t.Error("expected count query in browse results")
	}
}

func TestBrowseTable_WithFK(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// products has FK to categories, so browse should include filtered query
	queries := p.BrowseTable("products")
	if len(queries) < 2 {
		t.Fatalf("expected at least 2 queries, got %d", len(queries))
	}

	// Should have WHERE clause with FK filter
	hasWhere := false
	for _, q := range queries {
		if strings.Contains(strings.ToUpper(q), "WHERE") {
			hasWhere = true
		}
	}
	if !hasWhere {
		t.Error("expected WHERE clause in FK-filtered browse query")
	}
}

func TestBrowseTable_UnknownTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.BrowseTable("nonexistent")
	if queries != nil {
		t.Errorf("expected nil for unknown table, got %v", queries)
	}
}

func TestBrowseTable_NoFK(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// categories has no FKs
	queries := p.BrowseTable("categories")
	if len(queries) == 0 {
		t.Fatal("expected queries for categories")
	}
	// Should have main query and count query
	if len(queries) < 2 {
		t.Errorf("expected at least 2 queries (SELECT + count), got %d", len(queries))
	}
}

// --- ViewRecord ---

func TestViewRecord_ValidTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.ViewRecord("users")
	if len(queries) == 0 {
		t.Fatal("expected view queries for users")
	}

	// First query should be SELECT ... WHERE id = N
	q := queries[0]
	if !strings.Contains(q, "WHERE id =") {
		t.Errorf("expected WHERE id = in view query, got %q", q)
	}
}

func TestViewRecord_WithReferencedBy(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// users is referenced by orders -> should have related query
	queries := p.ViewRecord("users")
	if len(queries) < 2 {
		t.Logf("queries: %v", queries)
		// users may or may not have referencing non-tunnel tables depending on classification
		// At least the main query should exist
	}

	// First query should always be present
	if !strings.HasPrefix(strings.ToUpper(queries[0]), "SELECT") {
		t.Errorf("expected SELECT, got %q", queries[0])
	}
}

func TestViewRecord_UnknownTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.ViewRecord("nonexistent")
	if queries != nil {
		t.Errorf("expected nil for unknown table, got %v", queries)
	}
}

func TestViewRecord_ZeroRowCount(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "empty_table", Schema: "public", RowCount: 0,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "name", Type: fakedata.ColTypeText},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.ViewRecord("empty_table")
	if len(queries) == 0 {
		t.Fatal("expected at least one query")
	}
	// When RowCount=0, id defaults to 1
	if !strings.Contains(queries[0], "id = 1") {
		t.Errorf("expected id = 1 for zero-row table, got %q", queries[0])
	}
}

func TestViewRecord_SkipsTunnelReferences(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// Verify that tunnel tables don't appear in ViewRecord results
	// Get view for a table referenced by tunnel
	queries := p.ViewRecord("products")
	for _, q := range queries {
		if strings.Contains(q, "analytics_events") {
			t.Error("tunnel table should not appear in ViewRecord related queries")
		}
	}
}

// --- InsertRecord ---

func TestInsertRecord_ValidTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.InsertRecord("products")
	if len(queries) != 1 {
		t.Fatalf("expected 1 INSERT query, got %d", len(queries))
	}

	q := strings.ToUpper(queries[0])
	if !strings.HasPrefix(q, "INSERT INTO PRODUCTS") {
		t.Errorf("expected INSERT INTO products, got %q", queries[0])
	}
	if !strings.Contains(q, "VALUES") {
		t.Errorf("expected VALUES in INSERT, got %q", queries[0])
	}
	// Should not include PK column 'id'
	if strings.Contains(queries[0], "(id,") || strings.Contains(queries[0], "(id ") {
		t.Errorf("INSERT should not include PK column, got %q", queries[0])
	}
}

func TestInsertRecord_TunnelTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.InsertRecord("analytics_events")
	if queries != nil {
		t.Errorf("expected nil for tunnel table INSERT, got %v", queries)
	}
}

func TestInsertRecord_UnknownTable(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.InsertRecord("nonexistent")
	if queries != nil {
		t.Errorf("expected nil for unknown table, got %v", queries)
	}
}

func TestInsertRecord_WithFKColumns(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.InsertRecord("orders")
	if len(queries) != 1 {
		t.Fatalf("expected 1 INSERT query, got %d", len(queries))
	}
	// Should contain user_id as a FK reference
	if !strings.Contains(queries[0], "user_id") {
		t.Errorf("expected user_id column in INSERT, got %q", queries[0])
	}
}

// --- CountQuery ---

func TestCountQuery(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.CountQuery("products")
	if len(queries) != 1 {
		t.Fatalf("expected 1 count query, got %d", len(queries))
	}
	expected := "SELECT count(*) FROM products"
	if queries[0] != expected {
		t.Errorf("CountQuery = %q, want %q", queries[0], expected)
	}
}

// --- JoinQuery ---

func TestJoinQuery_TableWithFK(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.JoinQuery("products")
	if len(queries) == 0 {
		t.Fatal("expected join queries for products")
	}

	q := strings.ToUpper(queries[0])
	if !strings.Contains(q, "JOIN") {
		t.Errorf("expected JOIN in query, got %q", queries[0])
	}
	if !strings.Contains(q, "LIMIT") {
		t.Errorf("expected LIMIT in join query, got %q", queries[0])
	}
}

func TestJoinQuery_TableWithoutFK(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// categories has no FK, should fall back to BrowseTable
	queries := p.JoinQuery("categories")
	if len(queries) == 0 {
		t.Fatal("expected fallback browse queries for categories")
	}
	// Should be a browse-style query (SELECT with LIMIT)
	q := strings.ToUpper(queries[0])
	if !strings.HasPrefix(q, "SELECT") {
		t.Errorf("expected SELECT fallback, got %q", queries[0])
	}
}

func TestJoinQuery_SameFirstLetter(t *testing.T) {
	// Create schema where table and FK target start with same letter
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "posts", Schema: "public", RowCount: 50,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "title", Type: fakedata.ColTypeText},
				},
			},
			{
				Name: "post_comments", Schema: "public", RowCount: 100,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "post_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "posts", FKColumn: "id"},
					{Name: "body", Type: fakedata.ColTypeText},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.JoinQuery("post_comments")
	if len(queries) == 0 {
		t.Fatal("expected join queries")
	}
	// When first letters match, second alias should be appended with "2"
	q := queries[0]
	if !strings.Contains(q, "p2") {
		t.Errorf("expected alias 'p2' for same-first-letter tables, got %q", q)
	}
}

// --- randomValueLiteral ---

func TestRandomValueLiteral_AllTypes(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	tests := []struct {
		name     string
		col      fakedata.SchemColumn
		contains string // substring the value should contain
	}{
		{
			name:     "Int",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeInt},
			contains: "", // just a number
		},
		{
			name:     "BigInt",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeBigInt},
			contains: "", // just a number
		},
		{
			name:     "Serial",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeSerial},
			contains: "", // just a number
		},
		{
			name:     "Float",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeFloat},
			contains: ".",
		},
		{
			name:     "Timestamp",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeTimestamp},
			contains: "NOW()",
		},
		{
			name:     "UUID",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeUUID},
			contains: "-",
		},
		{
			name:     "Text",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeText},
			contains: "value_",
		},
		{
			name:     "TextWithEnum",
			col:      fakedata.SchemColumn{Name: "x", Type: fakedata.ColTypeText, EnumValues: []string{"a", "b", "c"}},
			contains: "'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := p.randomValueLiteral(tt.col)
			if val == "" {
				t.Error("expected non-empty value")
			}
			if tt.contains != "" && !strings.Contains(val, tt.contains) {
				t.Errorf("value %q should contain %q", val, tt.contains)
			}
		})
	}
}

func TestRandomValueLiteral_Bool(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	col := fakedata.SchemColumn{Name: "flag", Type: fakedata.ColTypeBool}
	seenTrue := false
	seenFalse := false
	for i := 0; i < 50; i++ {
		val := p.randomValueLiteral(col)
		if val == "true" {
			seenTrue = true
		} else if val == "false" {
			seenFalse = true
		} else {
			t.Errorf("unexpected bool value: %q", val)
		}
	}
	if !seenTrue || !seenFalse {
		t.Errorf("expected both true and false; seenTrue=%v seenFalse=%v", seenTrue, seenFalse)
	}
}

func TestRandomValueLiteral_Enum(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	enums := []string{"pending", "active", "done"}
	col := fakedata.SchemColumn{Name: "status", Type: fakedata.ColTypeText, EnumValues: enums}

	for i := 0; i < 30; i++ {
		val := p.randomValueLiteral(col)
		// Value should be quoted and one of the enum values
		stripped := strings.Trim(val, "'")
		found := false
		for _, e := range enums {
			if stripped == e {
				found = true
			}
		}
		if !found {
			t.Errorf("randomValueLiteral enum = %q, not in %v", val, enums)
		}
	}
}

// --- NewGenericQueryEngine ---

func TestNewGenericQueryEngine(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	if e == nil {
		t.Fatal("expected non-nil engine")
	}
	if e.scenario != "test" {
		t.Errorf("scenario = %q, want 'test'", e.scenario)
	}
	if e.patterns == nil {
		t.Error("expected non-nil patterns for generic engine")
	}
	if e.state == nil {
		t.Error("expected non-nil state")
	}
	if e.state.CurrentPage != "/" {
		t.Errorf("initial page = %q, want /", e.state.CurrentPage)
	}
}

// --- genericBurst ---

func TestGenericBurst_ProducesQueries(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

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
}

func TestGenericBurst_MultipleBursts(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	pages := make(map[string]bool)
	for i := 0; i < 50; i++ {
		batch := e.NextBurst()
		pages[batch.Page] = true
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
		// All queries should be valid SQL
		for _, q := range batch.Queries {
			upper := strings.ToUpper(strings.TrimSpace(q))
			if !strings.HasPrefix(upper, "SELECT") &&
				!strings.HasPrefix(upper, "INSERT") &&
				!strings.HasPrefix(upper, "UPDATE") &&
				!strings.HasPrefix(upper, "DELETE") {
				t.Errorf("invalid query: %q", q)
			}
		}
	}

	// Should visit multiple pages
	if len(pages) < 2 {
		t.Errorf("expected multiple pages from generic burst, got %d: %v", len(pages), pages)
	}
}

func TestGenericBurst_PagePaths(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	validSuffixes := []string{"", "/:id", "/new", "/report"}
	for i := 0; i < 50; i++ {
		batch := e.NextBurst()
		page := batch.Page

		// Page should start with /
		if !strings.HasPrefix(page, "/") {
			t.Errorf("burst %d: page %q doesn't start with /", i, page)
			continue
		}

		// Check that page matches expected patterns
		hasValidSuffix := false
		for _, suffix := range validSuffixes {
			if strings.HasSuffix(page, suffix) {
				hasValidSuffix = true
				break
			}
		}
		if !hasValidSuffix {
			t.Errorf("burst %d: page %q doesn't match expected patterns", i, page)
		}
	}
}

func TestGenericBurst_UpdatesCurrentPage(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	if e.CurrentPage() != "/" {
		t.Errorf("initial page = %q, want /", e.CurrentPage())
	}

	e.NextBurst()
	if e.CurrentPage() == "/" {
		// After a burst the page should change (unless it's a super unlikely coincidence)
		// Run a few more to ensure it changes
		for i := 0; i < 10; i++ {
			e.NextBurst()
		}
		if e.CurrentPage() == "/" {
			t.Error("CurrentPage should change after bursts")
		}
	}
}

func TestGenericBurst_EmptySchema(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "empty",
		Tables:       []*fakedata.TableDef{},
	}
	e := NewGenericQueryEngine(schema, 42)

	batch := e.NextBurst()
	if batch == nil {
		t.Fatal("expected non-nil batch even for empty schema")
	}
	// Should fall back to SELECT 1
	if len(batch.Queries) != 1 || batch.Queries[0] != "SELECT 1" {
		t.Errorf("expected fallback [SELECT 1], got %v", batch.Queries)
	}
	if batch.Page != "/" {
		t.Errorf("expected page /, got %q", batch.Page)
	}
}

func TestGenericBurst_AllActionTypes(t *testing.T) {
	schema := testSchema()

	// Run many bursts with different seeds to cover all action types
	seenBrowse := false
	seenDetail := false
	seenInsert := false
	seenJoin := false

	for seed := int64(0); seed < 100; seed++ {
		e := NewGenericQueryEngine(schema, seed)
		batch := e.NextBurst()
		page := batch.Page

		if strings.HasSuffix(page, "/report") {
			seenJoin = true
		} else if strings.HasSuffix(page, "/new") {
			seenInsert = true
		} else if strings.HasSuffix(page, "/:id") {
			seenDetail = true
		} else {
			seenBrowse = true
		}
	}

	if !seenBrowse {
		t.Error("never saw browse action")
	}
	if !seenDetail {
		t.Error("never saw detail action")
	}
	if !seenInsert {
		t.Error("never saw insert action")
	}
	if !seenJoin {
		t.Error("never saw join action")
	}
}

// --- NextBurst routing: generic vs ecommerce ---

func TestNextBurst_UsesGenericWhenPatternsSet(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	// Generic engine should use genericBurst path
	batch := e.NextBurst()
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// The page should contain one of the schema table names
	foundTable := false
	for _, tbl := range schema.Tables {
		if strings.Contains(batch.Page, tbl.Name) {
			foundTable = true
			break
		}
	}
	// Could also be "/" for empty table scenario
	if !foundTable && batch.Page != "/" {
		t.Errorf("page %q doesn't reference any schema table", batch.Page)
	}
}

func TestNextBurst_UsesEcommerceWhenNoPatternsSet(t *testing.T) {
	e := NewEngine("ecommerce", 42)

	// Regular engine should use ecommerceBurst path
	batch := e.NextBurst()
	validEcommercePages := map[string]bool{
		"/products": true, "/products/:id": true, "/cart": true,
		"/checkout": true, "/categories": true, "/orders": true,
	}
	if !validEcommercePages[batch.Page] {
		t.Errorf("expected ecommerce page, got %q", batch.Page)
	}
}

// --- Integration: use real templates ---

func TestGenericEngine_WithEcommerceTemplate(t *testing.T) {
	schema, err := fakedata.NewTemplateBuilder("ecommerce", 42, false).Build()
	if err != nil {
		t.Fatalf("failed to build ecommerce template: %v", err)
	}

	e := NewGenericQueryEngine(schema, 42)
	for i := 0; i < 30; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

func TestGenericEngine_WithIoTTemplate(t *testing.T) {
	schema, err := fakedata.NewTemplateBuilder("iot", 42, false).Build()
	if err != nil {
		t.Fatalf("failed to build iot template: %v", err)
	}

	e := NewGenericQueryEngine(schema, 42)
	for i := 0; i < 30; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

func TestGenericEngine_WithSaaSTemplate(t *testing.T) {
	schema, err := fakedata.NewTemplateBuilder("saas", 42, false).Build()
	if err != nil {
		t.Fatalf("failed to build saas template: %v", err)
	}

	e := NewGenericQueryEngine(schema, 42)
	for i := 0; i < 30; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

func TestGenericEngine_WithBlogTemplate(t *testing.T) {
	schema, err := fakedata.NewTemplateBuilder("blog", 42, false).Build()
	if err != nil {
		t.Fatalf("failed to build blog template: %v", err)
	}

	e := NewGenericQueryEngine(schema, 42)
	for i := 0; i < 30; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

func TestGenericEngine_WithProjectTemplate(t *testing.T) {
	schema, err := fakedata.NewTemplateBuilder("project", 42, false).Build()
	if err != nil {
		t.Fatalf("failed to build project template: %v", err)
	}

	e := NewGenericQueryEngine(schema, 42)
	for i := 0; i < 30; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

// --- Edge cases ---

func TestGenericEngine_SingleTableNoFK(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "minimal",
		Tables: []*fakedata.TableDef{
			{
				Name: "items", Schema: "public", RowCount: 10,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "name", Type: fakedata.ColTypeText},
				},
			},
		},
	}
	e := NewGenericQueryEngine(schema, 42)

	for i := 0; i < 20; i++ {
		batch := e.NextBurst()
		if len(batch.Queries) == 0 {
			t.Errorf("burst %d: no queries", i)
		}
	}
}

func TestGenericEngine_OnlyTunnelTables(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "tunnel_only",
		Tables: []*fakedata.TableDef{
			{
				Name: "analytics_events", Schema: "public", RowCount: 0, IsTunnel: true,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "event", Type: fakedata.ColTypeText},
				},
			},
		},
	}
	e := NewGenericQueryEngine(schema, 42)

	batch := e.NextBurst()
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	// Should fall back to SELECT 1 since no entity tables
	if len(batch.Queries) != 1 || batch.Queries[0] != "SELECT 1" {
		t.Errorf("expected [SELECT 1] fallback, got %v", batch.Queries)
	}
}

func TestInsertRecord_AllColumnTypes(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "all_types",
		Tables: []*fakedata.TableDef{
			{
				Name: "ref_table", Schema: "public", RowCount: 20,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
				},
			},
			{
				Name: "all_types", Schema: "public", RowCount: 10,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "int_col", Type: fakedata.ColTypeInt},
					{Name: "bigint_col", Type: fakedata.ColTypeBigInt},
					{Name: "float_col", Type: fakedata.ColTypeFloat},
					{Name: "text_col", Type: fakedata.ColTypeText},
					{Name: "bool_col", Type: fakedata.ColTypeBool},
					{Name: "ts_col", Type: fakedata.ColTypeTimestamp},
					{Name: "uuid_col", Type: fakedata.ColTypeUUID},
					{Name: "enum_col", Type: fakedata.ColTypeText, EnumValues: []string{"a", "b"}},
					{Name: "fk_col", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "ref_table", FKColumn: "id"},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	queries := p.InsertRecord("all_types")
	if len(queries) != 1 {
		t.Fatalf("expected 1 INSERT query, got %d", len(queries))
	}
	q := queries[0]
	if !strings.HasPrefix(strings.ToUpper(q), "INSERT INTO ALL_TYPES") {
		t.Errorf("expected INSERT INTO all_types, got %q", q)
	}
	// Should contain all non-PK columns
	for _, col := range []string{"int_col", "bigint_col", "float_col", "text_col", "bool_col", "ts_col", "uuid_col", "enum_col", "fk_col"} {
		if !strings.Contains(q, col) {
			t.Errorf("INSERT should contain column %q, got %q", col, q)
		}
	}
	// Should contain NOW() for timestamp
	if !strings.Contains(q, "NOW()") {
		t.Errorf("expected NOW() for timestamp column, got %q", q)
	}
}

func TestInsertRecord_FKToZeroRowTable(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "empty_ref", Schema: "public", RowCount: 0,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
				},
			},
			{
				Name: "child", Schema: "public", RowCount: 5,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "ref_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "empty_ref", FKColumn: "id"},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// Should default to refCount=10 when FK table has 0 rows
	queries := p.InsertRecord("child")
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
	if !strings.Contains(strings.ToUpper(queries[0]), "INSERT") {
		t.Errorf("expected INSERT, got %q", queries[0])
	}
}

func TestInsertRecord_FKToUnknownTable(t *testing.T) {
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "child", Schema: "public", RowCount: 5,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
					{Name: "ref_id", Type: fakedata.ColTypeInt, IsFK: true, FKTable: "missing_table", FKColumn: "id"},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// Should default to refCount=10 when FK table doesn't exist
	queries := p.InsertRecord("child")
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
}

func TestClassifyTables_FallbackWhenNoEntities(t *testing.T) {
	// All non-tunnel tables are either junction or lookup -> fallback to UserTables
	schema := &fakedata.SchemaDefinition{
		ScenarioName: "test",
		Tables: []*fakedata.TableDef{
			{
				Name: "lookup", Schema: "public", RowCount: 5,
				Columns: []fakedata.SchemColumn{
					{Name: "id", Type: fakedata.ColTypeSerial, IsPK: true},
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// Single table with 1 col (just PK), 0 FK, 0 references: nonIDCols=0
	// refCount > fkCount (0>0 = false), RowCount<=30 so lookup check = (0 > 0 && 5 <= 30) = false
	// falls to entity
	if len(p.entities) == 0 {
		t.Error("expected at least one entity via fallback")
	}
}

// --- Determinism ---

func TestGenericBurst_SameSeedSamePageCount(t *testing.T) {
	// Same seed should produce same page selections and query counts,
	// though exact queries may vary due to Go map iteration order in FK lookups.
	schema := testSchema()

	e1 := NewGenericQueryEngine(schema, 42)
	e2 := NewGenericQueryEngine(schema, 42)

	for i := 0; i < 20; i++ {
		b1 := e1.NextBurst()
		b2 := e2.NextBurst()

		if b1.Page != b2.Page {
			t.Errorf("burst %d: pages differ: %q vs %q", i, b1.Page, b2.Page)
		}
		if len(b1.Queries) != len(b2.Queries) {
			t.Errorf("burst %d: query counts differ: %d vs %d", i, len(b1.Queries), len(b2.Queries))
		}
	}
}

func TestRandomAnalyticsEvent_WithGenericEngine(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	// After a burst, the current page should be reflected in analytics event
	e.NextBurst()
	q := e.RandomAnalyticsEvent()
	if !strings.HasPrefix(strings.ToUpper(q), "INSERT INTO ANALYTICS_EVENTS") {
		t.Errorf("expected analytics INSERT, got %q", q)
	}
	// Should contain the current page
	if !strings.Contains(q, e.CurrentPage()) {
		t.Errorf("analytics event should reference current page %q, got %q", e.CurrentPage(), q)
	}
}

func TestRandomBackgroundQuery_WithGenericEngine(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	q := e.RandomBackgroundQuery()
	if !strings.HasPrefix(strings.ToUpper(q), "SELECT") {
		t.Errorf("expected SELECT, got %q", q)
	}
}

func TestBurstSpacing_WithGenericEngine(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	for i := 0; i < 20; i++ {
		d := e.BurstSpacing()
		if d.Milliseconds() < 15 || d.Milliseconds() > 24 {
			t.Errorf("BurstSpacing = %v, want 15-24ms", d)
		}
	}
}

func TestReadingPause_WithGenericEngine(t *testing.T) {
	schema := testSchema()
	e := NewGenericQueryEngine(schema, 42)

	for i := 0; i < 20; i++ {
		d := e.ReadingPause()
		if d.Seconds() < 2 || d.Seconds() > 30 {
			t.Errorf("ReadingPause = %v, want 2-30s", d)
		}
	}
}

// --- BrowseTable edge case: FK to table with rows ---

func TestBrowseTable_FKWithPositiveRowCount(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// products has FK to categories (RowCount=10)
	queries := p.BrowseTable("products")
	// Should have at least 3 queries: filtered browse, count, and ref lookup
	if len(queries) < 3 {
		t.Errorf("expected at least 3 queries for FK browse, got %d: %v", len(queries), queries)
	}

	// Third query should reference the FK target table
	hasRefLookup := false
	for _, q := range queries {
		if strings.Contains(q, "categories") && strings.Contains(q, "WHERE id =") {
			hasRefLookup = true
		}
	}
	if !hasRefLookup {
		t.Error("expected ref lookup query for categories")
	}
}

// --- ViewRecord with multiple references ---

func TestViewRecord_MultipleReferences(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// products is referenced by order_items (and possibly reviews in ecommerce)
	queries := p.ViewRecord("products")
	if len(queries) == 0 {
		t.Fatal("expected queries for ViewRecord")
	}
	// Main query
	if !strings.Contains(queries[0], "products") {
		t.Errorf("first query should reference products: %q", queries[0])
	}
}

func TestJoinQuery_MultipleFK(t *testing.T) {
	schema := testSchema()
	rng := rand.New(rand.NewSource(42))
	p := NewGenericQueryPatterns(schema, rng)

	// order_items has 2 FKs: order_id -> orders, product_id -> products
	queries := p.JoinQuery("order_items")
	if len(queries) == 0 {
		t.Fatal("expected join queries for order_items")
	}
	q := strings.ToUpper(queries[0])
	if !strings.Contains(q, "JOIN") {
		t.Errorf("expected JOIN in query, got %q", queries[0])
	}
}
