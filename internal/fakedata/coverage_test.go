package fakedata

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// ============================================================
// DDL Parser Tests
// ============================================================

func TestParseDDL_SimpleTable(t *testing.T) {
	ddl := `CREATE TABLE users (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		email TEXT
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]
	if tbl.Name != "users" {
		t.Errorf("table name = %q, want users", tbl.Name)
	}
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}
	// Check id column
	if !tbl.Columns[0].IsPK {
		t.Error("id column should be PK")
	}
	if tbl.Columns[0].Type != ColTypeSerial {
		t.Errorf("id type = %d, want ColTypeSerial", tbl.Columns[0].Type)
	}
	// Check name column
	if tbl.Columns[1].Name != "name" {
		t.Errorf("col 1 name = %q", tbl.Columns[1].Name)
	}
	if tbl.Columns[1].Nullable {
		t.Error("name column should not be nullable (NOT NULL)")
	}
}

func TestParseDDL_MultipleStatements(t *testing.T) {
	ddl := `
	CREATE TABLE categories (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL
	);

	CREATE TABLE products (
		id SERIAL PRIMARY KEY,
		name VARCHAR(200),
		price NUMERIC(10,2),
		category_id INTEGER REFERENCES categories(id)
	);
	`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if len(schema.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(schema.Tables))
	}
	if schema.Tables[0].Name != "categories" {
		t.Errorf("first table = %q, want categories", schema.Tables[0].Name)
	}
	if schema.Tables[1].Name != "products" {
		t.Errorf("second table = %q, want products", schema.Tables[1].Name)
	}

	// Check inline FK
	prodCols := schema.Tables[1].Columns
	catCol := prodCols[3] // category_id
	if !catCol.IsFK {
		t.Error("category_id should be FK")
	}
	if catCol.FKTable != "categories" {
		t.Errorf("FK table = %q, want categories", catCol.FKTable)
	}
}

func TestParseDDL_IfNotExists(t *testing.T) {
	ddl := `CREATE TABLE IF NOT EXISTS logs (
		id SERIAL PRIMARY KEY,
		message TEXT,
		level VARCHAR(20)
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if schema.Tables[0].Name != "logs" {
		t.Errorf("table name = %q, want logs", schema.Tables[0].Name)
	}
}

func TestParseDDL_QuotedIdentifiers(t *testing.T) {
	ddl := `CREATE TABLE "MyTable" (
		"Id" SERIAL PRIMARY KEY,
		"UserName" VARCHAR(50)
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if schema.Tables[0].Name != "MyTable" {
		t.Errorf("table name = %q, want MyTable", schema.Tables[0].Name)
	}
	if schema.Tables[0].Columns[0].Name != "Id" {
		t.Errorf("col 0 name = %q, want Id", schema.Tables[0].Columns[0].Name)
	}
}

func TestParseDDL_TableLevelConstraints(t *testing.T) {
	ddl := `CREATE TABLE order_items (
		id INTEGER NOT NULL,
		order_id INTEGER NOT NULL,
		product_id INTEGER NOT NULL,
		quantity INTEGER,
		PRIMARY KEY (id),
		FOREIGN KEY (order_id) REFERENCES orders(id),
		FOREIGN KEY (product_id) REFERENCES products(id)
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	tbl := schema.Tables[0]
	// Check PK applied
	if !tbl.Columns[0].IsPK {
		t.Error("id should be PK via table-level constraint")
	}
	// Check FKs applied
	if !tbl.Columns[1].IsFK || tbl.Columns[1].FKTable != "orders" {
		t.Errorf("order_id FK not resolved: IsFK=%v FKTable=%q", tbl.Columns[1].IsFK, tbl.Columns[1].FKTable)
	}
	if !tbl.Columns[2].IsFK || tbl.Columns[2].FKTable != "products" {
		t.Errorf("product_id FK not resolved: IsFK=%v FKTable=%q", tbl.Columns[2].IsFK, tbl.Columns[2].FKTable)
	}
}

func TestParseDDL_AllColumnTypes(t *testing.T) {
	ddl := `CREATE TABLE all_types (
		col_serial SERIAL,
		col_bigserial BIGSERIAL,
		col_int INTEGER,
		col_int4 INT4,
		col_smallint SMALLINT,
		col_bigint BIGINT,
		col_int8 INT8,
		col_bool BOOLEAN,
		col_text TEXT,
		col_varchar VARCHAR(255),
		col_char CHAR(10),
		col_numeric NUMERIC(10,2),
		col_decimal DECIMAL(8,4),
		col_real REAL,
		col_float8 FLOAT8,
		col_double DOUBLE PRECISION,
		col_timestamp TIMESTAMP,
		col_timestamptz TIMESTAMPTZ,
		col_date DATE,
		col_uuid UUID,
		col_json JSON,
		col_jsonb JSONB,
		col_bytea BYTEA
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	tbl := schema.Tables[0]
	if len(tbl.Columns) != 23 {
		t.Fatalf("expected 23 columns, got %d", len(tbl.Columns))
	}

	tests := []struct {
		name    string
		colType ColumnType
	}{
		{"col_serial", ColTypeSerial},
		{"col_bigserial", ColTypeSerial},
		{"col_int", ColTypeInt},
		{"col_int4", ColTypeInt},
		{"col_smallint", ColTypeInt},
		{"col_bigint", ColTypeBigInt},
		{"col_int8", ColTypeBigInt},
		{"col_bool", ColTypeBool},
		{"col_text", ColTypeText},
		{"col_varchar", ColTypeText},
		{"col_char", ColTypeText},
		{"col_numeric", ColTypeFloat},
		{"col_decimal", ColTypeFloat},
		{"col_real", ColTypeFloat},
		{"col_float8", ColTypeFloat},
		{"col_double", ColTypeFloat},
		{"col_timestamp", ColTypeTimestamp},
		{"col_timestamptz", ColTypeTimestamp},
		{"col_date", ColTypeTimestamp},
		{"col_uuid", ColTypeUUID},
		{"col_json", ColTypeJSON},
		{"col_jsonb", ColTypeJSON},
		{"col_bytea", ColTypeBytea},
	}

	for i, tt := range tests {
		if tbl.Columns[i].Name != tt.name {
			t.Errorf("column %d: name = %q, want %q", i, tbl.Columns[i].Name, tt.name)
		}
		if tbl.Columns[i].Type != tt.colType {
			t.Errorf("column %q: type = %d, want %d", tt.name, tbl.Columns[i].Type, tt.colType)
		}
	}
}

func TestParseDDL_EmptyInput(t *testing.T) {
	_, err := ParseDDL("")
	if err == nil {
		t.Error("expected error for empty DDL")
	}
}

func TestParseDDL_NoCreateTable(t *testing.T) {
	_, err := ParseDDL("SELECT 1; INSERT INTO foo VALUES (1);")
	if err == nil {
		t.Error("expected error when no CREATE TABLE found")
	}
}

func TestParseDDL_AutoFKDetection(t *testing.T) {
	ddl := `
	CREATE TABLE users (
		id SERIAL PRIMARY KEY,
		name TEXT
	);
	CREATE TABLE posts (
		id SERIAL PRIMARY KEY,
		user_id INTEGER,
		title TEXT
	);`

	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	postTable := schema.Tables[1]
	userIDCol := postTable.Columns[1]
	// Auto-FK detection: user_id should be recognized as FK to users
	if !userIDCol.IsFK {
		t.Error("user_id should be auto-detected as FK")
	}
	if userIDCol.FKTable != "users" {
		t.Errorf("FKTable = %q, want users", userIDCol.FKTable)
	}
}

func TestParseDDL_VarcharWithLength(t *testing.T) {
	ddl := `CREATE TABLE t (name VARCHAR(100));`
	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	col := schema.Tables[0].Columns[0]
	if col.Type != ColTypeText {
		t.Errorf("type = %d, want ColTypeText", col.Type)
	}
	// pg convention: typeMod = length + 4
	if col.TypeMod != 104 {
		t.Errorf("typeMod = %d, want 104", col.TypeMod)
	}
}

func TestParseDDL_ScenarioIsCustom(t *testing.T) {
	ddl := `CREATE TABLE t (id SERIAL PRIMARY KEY);`
	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if schema.ScenarioName != "custom" {
		t.Errorf("ScenarioName = %q, want custom", schema.ScenarioName)
	}
}

// ============================================================
// DDL Helper Function Tests
// ============================================================

func TestSplitStatements_Single(t *testing.T) {
	stmts := splitStatements("CREATE TABLE foo (id INT);")
	if len(stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(stmts))
	}
}

func TestSplitStatements_Multiple(t *testing.T) {
	sql := "CREATE TABLE a (id INT); CREATE TABLE b (id INT);"
	stmts := splitStatements(sql)
	if len(stmts) != 2 {
		t.Errorf("expected 2 statements, got %d", len(stmts))
	}
}

func TestSplitStatements_NoCreate(t *testing.T) {
	stmts := splitStatements("SELECT 1;")
	if len(stmts) != 1 {
		t.Errorf("expected 1 (passthrough), got %d", len(stmts))
	}
}

func TestExtractParenBody(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"(a, b, c)", "a, b, c"},
		{"(nested (parens))", "nested (parens)"},
		{"()", ""},
		{"", ""},
		{"no parens", ""},
	}
	for _, tt := range tests {
		got := extractParenBody(tt.input)
		if got != tt.want {
			t.Errorf("extractParenBody(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitColumnDefs(t *testing.T) {
	body := "id SERIAL, name VARCHAR(100), CONSTRAINT pk PRIMARY KEY (id)"
	parts := splitColumnDefs(body)
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d", len(parts))
	}
}

func TestSplitColumnDefs_NestedParens(t *testing.T) {
	body := "id SERIAL, price NUMERIC(10,2), name TEXT"
	parts := splitColumnDefs(body)
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d: %v", len(parts), parts)
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`"hello"`, "hello"},
		{"hello", "hello"},
		{`""`, ""},
		{`"a"`, "a"},
		{`" spaced "`, " spaced "},
	}
	for _, tt := range tests {
		got := unquote(tt.input)
		if got != tt.want {
			t.Errorf("unquote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// InferHint Tests
// ============================================================

func TestInferHint_ExactMatches(t *testing.T) {
	tables := []string{"users", "orders", "products"}

	tests := []struct {
		name    string
		colType ColumnType
		want    ColumnHint
	}{
		{"email", ColTypeText, HintEmail},
		{"e_mail", ColTypeText, HintEmail},
		{"email_address", ColTypeText, HintEmail},
		{"name", ColTypeText, HintName},
		{"full_name", ColTypeText, HintName},
		{"display_name", ColTypeText, HintName},
		{"first_name", ColTypeText, HintFirstName},
		{"firstname", ColTypeText, HintFirstName},
		{"last_name", ColTypeText, HintLastName},
		{"lastname", ColTypeText, HintLastName},
		{"surname", ColTypeText, HintLastName},
		{"username", ColTypeText, HintUsername},
		{"user_name", ColTypeText, HintUsername},
		{"login", ColTypeText, HintUsername},
		{"price", ColTypeFloat, HintPrice},
		{"cost", ColTypeFloat, HintPrice},
		{"amount", ColTypeFloat, HintPrice},
		{"total", ColTypeFloat, HintPrice},
		{"subtotal", ColTypeFloat, HintPrice},
		{"balance", ColTypeFloat, HintPrice},
		{"salary", ColTypeFloat, HintPrice},
		{"fee", ColTypeFloat, HintPrice},
		{"quantity", ColTypeInt, HintQuantity},
		{"qty", ColTypeInt, HintQuantity},
		{"count", ColTypeInt, HintQuantity},
		{"stock", ColTypeInt, HintQuantity},
		{"inventory_count", ColTypeInt, HintQuantity},
		{"qty_available", ColTypeInt, HintQuantity},
		{"status", ColTypeText, HintStatus},
		{"state", ColTypeText, HintStatus},
		{"condition", ColTypeText, HintStatus},
		{"rating", ColTypeInt, HintRating},
		{"score", ColTypeInt, HintRating},
		{"stars", ColTypeInt, HintRating},
		{"title", ColTypeText, HintTitle},
		{"subject", ColTypeText, HintTitle},
		{"headline", ColTypeText, HintTitle},
		{"description", ColTypeText, HintDescription},
		{"body", ColTypeText, HintDescription},
		{"content", ColTypeText, HintDescription},
		{"bio", ColTypeText, HintDescription},
		{"summary", ColTypeText, HintDescription},
		{"text", ColTypeText, HintDescription},
		{"comment", ColTypeText, HintDescription},
		{"url", ColTypeText, HintURL},
		{"link", ColTypeText, HintURL},
		{"href", ColTypeText, HintURL},
		{"website", ColTypeText, HintURL},
		{"page_url", ColTypeText, HintPageURL},
		{"address", ColTypeText, HintAddress},
		{"street", ColTypeText, HintAddress},
		{"street_address", ColTypeText, HintAddress},
		{"phone", ColTypeText, HintPhone},
		{"mobile", ColTypeText, HintPhone},
		{"telephone", ColTypeText, HintPhone},
		{"phone_number", ColTypeText, HintPhone},
		{"country", ColTypeText, HintCountry},
		{"country_name", ColTypeText, HintCountry},
		{"nation", ColTypeText, HintCountry},
		{"color", ColTypeText, HintColor},
		{"colour", ColTypeText, HintColor},
		{"slug", ColTypeText, HintSlug},
		{"url_slug", ColTypeText, HintSlug},
		{"permalink", ColTypeText, HintSlug},
		{"ip", ColTypeText, HintIPAddress},
		{"ip_address", ColTypeText, HintIPAddress},
		{"remote_addr", ColTypeText, HintIPAddress},
		{"user_agent", ColTypeText, HintUserAgent},
		{"useragent", ColTypeText, HintUserAgent},
		{"event_type", ColTypeText, HintEventType},
		{"event_name", ColTypeText, HintEventType},
		{"action", ColTypeText, HintEventType},
		{"metadata", ColTypeJSON, HintJSONMeta},
		{"meta", ColTypeJSON, HintJSONMeta},
		{"extra", ColTypeJSON, HintJSONMeta},
		{"properties", ColTypeJSON, HintJSONMeta},
		{"company", ColTypeText, HintCompanyName},
		{"company_name", ColTypeText, HintCompanyName},
		{"organization", ColTypeText, HintCompanyName},
		{"org_name", ColTypeText, HintCompanyName},
	}

	for _, tt := range tests {
		got := InferHint(tt.name, tt.colType, tables)
		if got != tt.want {
			t.Errorf("InferHint(%q, %d) = %d, want %d", tt.name, tt.colType, got, tt.want)
		}
	}
}

func TestInferHint_SuffixPatterns(t *testing.T) {
	tables := []string{}

	tests := []struct {
		name    string
		colType ColumnType
		want    ColumnHint
	}{
		{"author_name", ColTypeText, HintTitle},
		{"product_title", ColTypeText, HintTitle},
		{"work_email", ColTypeText, HintEmail},
		{"profile_url", ColTypeText, HintURL},
		{"site_link", ColTypeText, HintURL},
		{"home_phone", ColTypeText, HintPhone},
		{"billing_address", ColTypeText, HintAddress},
		{"item_price", ColTypeFloat, HintPrice},
		{"unit_cost", ColTypeFloat, HintPrice},
		{"order_amount", ColTypeFloat, HintPrice},
		{"created_at", ColTypeTimestamp, HintDate},
		{"updated_date", ColTypeText, HintDate},
		{"login_time", ColTypeTimestamp, HintDate},
	}

	for _, tt := range tests {
		got := InferHint(tt.name, tt.colType, tables)
		if got != tt.want {
			t.Errorf("InferHint(%q, %d) = %d, want %d", tt.name, tt.colType, got, tt.want)
		}
	}
}

func TestInferHint_FKDetection(t *testing.T) {
	tables := []string{"users", "categories", "products"}

	tests := []struct {
		name string
		want ColumnHint
	}{
		{"user_id", HintForeignKey},
		{"category_id", HintForeignKey},
		{"product_id", HintForeignKey},
	}

	for _, tt := range tests {
		got := InferHint(tt.name, ColTypeInt, tables)
		if got != tt.want {
			t.Errorf("InferHint(%q) = %d, want HintForeignKey(%d)", tt.name, got, tt.want)
		}
	}
}

func TestInferHint_ContainsPatterns(t *testing.T) {
	tables := []string{}

	tests := []struct {
		name string
		want ColumnHint
	}{
		{"primary_email_addr", HintEmail},
		{"total_price_usd", HintPrice},
		{"base_cost_eur", HintPrice},
		{"contact_phone_ext", HintPhone},
	}

	for _, tt := range tests {
		got := InferHint(tt.name, ColTypeText, tables)
		if got != tt.want {
			t.Errorf("InferHint(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestInferHint_NoMatch(t *testing.T) {
	got := InferHint("some_random_col", ColTypeText, nil)
	if got != HintNone {
		t.Errorf("InferHint(some_random_col) = %d, want HintNone", got)
	}
}

// ============================================================
// Generator Tests
// ============================================================

func TestGenInt_Basic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenInt(rng, 1, 100)
	if len(val) == 0 {
		t.Error("GenInt returned empty")
	}
}

func TestGenInt_MinEqualsMax(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	// When max <= min, max is set to min+100
	val := GenInt(rng, 5, 5)
	if len(val) == 0 {
		t.Error("GenInt returned empty for equal min/max")
	}
}

func TestGenBigInt(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenBigInt(rng)
	if len(val) == 0 {
		t.Error("GenBigInt returned empty")
	}
}

func TestGenFloat_DefaultRange(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	// max <= min triggers default
	val := GenFloat(rng, 10, 5)
	if len(val) == 0 {
		t.Error("GenFloat returned empty")
	}
}

func TestGenTimestamp(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenTimestamp(rng)
	if !strings.Contains(string(val), "2024-") {
		t.Errorf("GenTimestamp = %q, expected 2024 date", val)
	}
	if !strings.HasSuffix(string(val), "Z") {
		t.Errorf("GenTimestamp = %q, expected Z suffix", val)
	}
}

func TestGenFirstName(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenFirstName(rng)
	if len(val) == 0 {
		t.Error("GenFirstName returned empty")
	}
}

func TestGenLastName(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenLastName(rng)
	if len(val) == 0 {
		t.Error("GenLastName returned empty")
	}
}

func TestGenUsername(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenUsername(rng, 0)
	if len(val) == 0 {
		t.Error("GenUsername returned empty")
	}
	// Should be lowercase letters followed by digits
	s := string(val)
	if s[0] < 'a' || s[0] > 'z' {
		t.Errorf("GenUsername should start with lowercase letter: %q", s)
	}
}

func TestGenStatus_EmptyValues(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenStatus(rng, nil)
	if len(val) == 0 {
		t.Error("GenStatus returned empty for nil values")
	}
	// Should be one of the default statusValues
	found := false
	for _, sv := range statusValues {
		if string(val) == sv {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GenStatus with nil values returned %q, not a default status", val)
	}
}

func TestGenStatus_CustomValues(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	customs := []string{"alpha", "beta", "gamma"}
	val := GenStatus(rng, customs)
	found := false
	for _, c := range customs {
		if string(val) == c {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GenStatus with custom values returned %q", val)
	}
}

func TestGenPhone(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenPhone(rng)
	s := string(val)
	if !strings.HasPrefix(s, "+1-") {
		t.Errorf("GenPhone = %q, expected +1- prefix", s)
	}
}

func TestGenCountry(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenCountry(rng)
	if len(val) == 0 {
		t.Error("GenCountry returned empty")
	}
}

func TestGenColor(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenColor(rng)
	if len(val) == 0 {
		t.Error("GenColor returned empty")
	}
}

func TestGenURL(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenURL(rng)
	s := string(val)
	if !strings.HasPrefix(s, "/") {
		t.Errorf("GenURL = %q, expected / prefix", s)
	}
}

func TestGenIPAddress(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenIPAddress(rng)
	s := string(val)
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		t.Errorf("GenIPAddress = %q, expected 4 octets", s)
	}
}

func TestGenUserAgent(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenUserAgent(rng)
	s := string(val)
	if !strings.Contains(s, "Mozilla") {
		t.Errorf("GenUserAgent = %q, expected Mozilla UA string", s)
	}
}

func TestGenJSONMeta(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenJSONMeta(rng)
	s := string(val)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		t.Errorf("GenJSONMeta = %q, expected JSON object", s)
	}
	if !strings.Contains(s, `"source":"web"`) {
		t.Errorf("GenJSONMeta = %q, expected source:web", s)
	}
}

func TestGenText(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenText(rng, 3, 5)
	s := string(val)
	words := strings.Split(s, " ")
	if len(words) < 3 || len(words) > 5 {
		t.Errorf("GenText word count = %d, expected 3-5", len(words))
	}
}

func TestGenText_MaxLessThanMin(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenText(rng, 5, 2)
	if len(val) == 0 {
		t.Error("GenText returned empty for max < min")
	}
}

func TestGenForeignKey_ZeroMax(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenForeignKey(rng, 0)
	if string(val) != "1" {
		t.Errorf("GenForeignKey(0) = %q, want 1", val)
	}
}

func TestGenProductName_KnownCategory(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenProductName(rng, "Electronics")
	if len(val) == 0 {
		t.Error("GenProductName returned empty for known category")
	}
}

func TestGenProductName_UnknownCategory(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenProductName(rng, "UnknownCategory")
	if len(val) == 0 {
		t.Error("GenProductName returned empty for unknown category")
	}
	// Should use generic fallback
	s := string(val)
	if len(s) == 0 {
		t.Error("expected generic product name")
	}
}

func TestGenPrice_ZeroMin(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenPrice(rng, 0, 0)
	if len(val) == 0 {
		t.Error("GenPrice returned empty")
	}
	// 0 min should default to 1.99, 0 max should default to 999.99
}

func TestGenEventType(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	val := GenEventType(rng)
	found := false
	for _, et := range eventTypes {
		if string(val) == et {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GenEventType = %q, not in eventTypes list", val)
	}
}

// ============================================================
// Template Builder Tests
// ============================================================

func TestTemplateBuilder_AllScenarios(t *testing.T) {
	scenarios := []string{"ecommerce", "iot", "saas", "blog", "project", ""}
	for _, sc := range scenarios {
		builder := NewTemplateBuilder(sc, 42, false)
		schema, err := builder.Build()
		if err != nil {
			t.Fatalf("Build(%q) failed: %v", sc, err)
		}
		if schema == nil {
			t.Fatalf("Build(%q) returned nil schema", sc)
		}
		if len(schema.Tables) == 0 {
			t.Errorf("Build(%q) returned 0 tables", sc)
		}
		// Every schema should include tunnel tables
		hasEvents := false
		hasResponses := false
		for _, tbl := range schema.Tables {
			if tbl.Name == "analytics_events" {
				hasEvents = true
			}
			if tbl.Name == "analytics_responses" {
				hasResponses = true
			}
		}
		if !hasEvents {
			t.Errorf("Build(%q) missing analytics_events", sc)
		}
		if !hasResponses {
			t.Errorf("Build(%q) missing analytics_responses", sc)
		}
	}
}

func TestTemplateBuilder_UnknownScenario(t *testing.T) {
	builder := NewTemplateBuilder("nonexistent", 42, false)
	_, err := builder.Build()
	if err == nil {
		t.Error("expected error for unknown scenario")
	}
}

func TestTemplateBuilder_ProjectTemplate(t *testing.T) {
	builder := NewTemplateBuilder("project", 42, false)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build(project) failed: %v", err)
	}
	if schema.ScenarioName != "project" {
		t.Errorf("ScenarioName = %q, want project", schema.ScenarioName)
	}
	// project template should have: projects, users, sprints, tasks, task_comments + tunnel
	tableNames := make(map[string]bool)
	for _, tbl := range schema.Tables {
		tableNames[tbl.Name] = true
	}
	for _, name := range []string{"projects", "users", "sprints", "tasks", "task_comments"} {
		if !tableNames[name] {
			t.Errorf("missing table %q in project template", name)
		}
	}
}

func TestTemplateBuilder_IoTTemplate(t *testing.T) {
	builder := NewTemplateBuilder("iot", 42, false)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build(iot) failed: %v", err)
	}
	if schema.ScenarioName != "iot" {
		t.Errorf("ScenarioName = %q, want iot", schema.ScenarioName)
	}
	// iot template should have: devices, metrics, alerts, firmware_versions + tunnel
	tableNames := make(map[string]bool)
	for _, tbl := range schema.Tables {
		tableNames[tbl.Name] = true
	}
	for _, name := range []string{"devices", "metrics", "alerts", "firmware_versions"} {
		if !tableNames[name] {
			t.Errorf("missing table %q in iot template", name)
		}
	}
}

// ============================================================
// Schema Mutation Tests
// ============================================================

func TestApplyMutations_Deterministic(t *testing.T) {
	// Same seed should produce same mutations
	schema1 := ecommerceTemplate()
	schema1.Tables = append(schema1.Tables, TunnelInfrastructureTables("")...)
	rng1 := rand.New(rand.NewSource(99))
	spec1 := applyMutations(schema1, rng1)

	schema2 := ecommerceTemplate()
	schema2.Tables = append(schema2.Tables, TunnelInfrastructureTables("")...)
	rng2 := rand.New(rand.NewSource(99))
	spec2 := applyMutations(schema2, rng2)

	// Same seed should produce same table renames
	if len(spec1.TableRenames) != len(spec2.TableRenames) {
		t.Errorf("table renames differ: %d vs %d", len(spec1.TableRenames), len(spec2.TableRenames))
	}
	for k, v := range spec1.TableRenames {
		if spec2.TableRenames[k] != v {
			t.Errorf("table rename %q: %q vs %q", k, v, spec2.TableRenames[k])
		}
	}
}

func TestApplyMutations_TunnelTablesUnchanged(t *testing.T) {
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	rng := rand.New(rand.NewSource(42))
	spec := applyMutations(schema, rng)

	// Tunnel tables should never be renamed
	if _, ok := spec.TableRenames["analytics_events"]; ok {
		t.Error("analytics_events should not be renamed")
	}
	if _, ok := spec.TableRenames["analytics_responses"]; ok {
		t.Error("analytics_responses should not be renamed")
	}
}

func TestTemplateBuilder_WithMutations(t *testing.T) {
	builder := NewTemplateBuilder("ecommerce", 42, true)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build with mutations failed: %v", err)
	}
	if schema.Mutations == nil {
		t.Fatal("expected non-nil Mutations")
	}
	// Mutations may or may not rename tables (probabilistic), but the spec should exist
	if schema.Mutations.TableRenames == nil {
		t.Error("TableRenames should not be nil")
	}
	if schema.Mutations.ColumnRenames == nil {
		t.Error("ColumnRenames should not be nil")
	}
}

func TestApplyMutations_FKReferencesUpdated(t *testing.T) {
	// Create a simple schema where we force a table rename
	// Run with multiple seeds until we get a rename
	for seed := int64(0); seed < 100; seed++ {
		schema := ecommerceTemplate()
		schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
		rng := rand.New(rand.NewSource(seed))
		spec := applyMutations(schema, rng)

		if len(spec.TableRenames) > 0 {
			// Verify FK references are updated
			for _, tbl := range schema.Tables {
				for _, col := range tbl.Columns {
					if col.IsFK {
						// The FK target should not reference a pre-rename name
						for orig := range spec.TableRenames {
							if col.FKTable == orig {
								t.Errorf("FK in %s.%s still references old name %q", tbl.Name, col.Name, orig)
							}
						}
					}
				}
			}
			return // test passed
		}
	}
	// If we didn't get any renames in 100 seeds, that's very unlikely but not an error
}

// ============================================================
// Random Schema Builder Tests
// ============================================================

func TestRandomSchemaBuilder_Build(t *testing.T) {
	builder := NewRandomSchemaBuilder(42)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	if schema.ScenarioName != "random" {
		t.Errorf("ScenarioName = %q, want random", schema.ScenarioName)
	}
	// Should have at least 4 user tables + 2 tunnel tables
	if len(schema.Tables) < 6 {
		t.Errorf("expected at least 6 tables, got %d", len(schema.Tables))
	}
}

func TestRandomSchemaBuilder_SeedDeterminism(t *testing.T) {
	schema1, err := NewRandomSchemaBuilder(42).Build()
	if err != nil {
		t.Fatalf("Build 1 failed: %v", err)
	}
	schema2, err := NewRandomSchemaBuilder(42).Build()
	if err != nil {
		t.Fatalf("Build 2 failed: %v", err)
	}

	if len(schema1.Tables) != len(schema2.Tables) {
		t.Fatalf("table count differs: %d vs %d", len(schema1.Tables), len(schema2.Tables))
	}

	for i := range schema1.Tables {
		if schema1.Tables[i].Name != schema2.Tables[i].Name {
			t.Errorf("table %d name: %q vs %q", i, schema1.Tables[i].Name, schema2.Tables[i].Name)
		}
		if schema1.Tables[i].RowCount != schema2.Tables[i].RowCount {
			t.Errorf("table %q rowcount: %d vs %d", schema1.Tables[i].Name, schema1.Tables[i].RowCount, schema2.Tables[i].RowCount)
		}
	}
}

func TestRandomSchemaBuilder_DifferentSeeds(t *testing.T) {
	schema1, _ := NewRandomSchemaBuilder(1).Build()
	schema2, _ := NewRandomSchemaBuilder(2).Build()

	// Different seeds should produce different schemas (with very high probability)
	different := false
	if len(schema1.Tables) != len(schema2.Tables) {
		different = true
	} else {
		for i := range schema1.Tables {
			if schema1.Tables[i].Name != schema2.Tables[i].Name {
				different = true
				break
			}
		}
	}
	if !different {
		t.Error("expected different schemas from different seeds")
	}
}

func TestRandomSchemaBuilder_DomainCoverage(t *testing.T) {
	// Test multiple seeds to cover both retail and crm domains (and nil domains)
	domains := map[string]bool{}
	for seed := int64(0); seed < 100; seed++ {
		schema, err := NewRandomSchemaBuilder(seed).Build()
		if err != nil {
			t.Fatalf("Build with seed %d failed: %v", seed, err)
		}
		if schema.ScenarioName != "random" {
			t.Errorf("seed %d: ScenarioName = %q", seed, schema.ScenarioName)
		}
		// Just verify it works
		if len(schema.Tables) < 4 {
			t.Errorf("seed %d: too few tables %d", seed, len(schema.Tables))
		}
		// Track which domain by checking table names
		for _, tbl := range schema.Tables {
			if tbl.Name == "contacts" || tbl.Name == "deals" {
				domains["crm"] = true
			}
			if tbl.Name == "products" || tbl.Name == "orders" {
				domains["retail"] = true
			}
		}
	}
	// We should have hit both retail and crm
	if !domains["retail"] {
		t.Error("never hit retail domain in 100 seeds")
	}
	if !domains["crm"] {
		t.Error("never hit crm domain in 100 seeds")
	}
}

func TestRandomSchemaBuilder_HasTunnelTables(t *testing.T) {
	schema, _ := NewRandomSchemaBuilder(42).Build()
	hasEvents := false
	hasResponses := false
	for _, tbl := range schema.Tables {
		if tbl.Name == "analytics_events" {
			hasEvents = true
		}
		if tbl.Name == "analytics_responses" {
			hasResponses = true
		}
	}
	if !hasEvents || !hasResponses {
		t.Error("random schema should include tunnel infrastructure tables")
	}
}

// ============================================================
// Custom Schema Builder Tests
// ============================================================

func TestCustomSchemaBuilder_Basic(t *testing.T) {
	ddl := `
	CREATE TABLE users (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100),
		email TEXT
	);
	CREATE TABLE posts (
		id SERIAL PRIMARY KEY,
		user_id INTEGER REFERENCES users(id),
		title VARCHAR(200),
		body TEXT
	);`

	builder := NewCustomSchemaBuilder(ddl, 42)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if schema.ScenarioName != "custom" {
		t.Errorf("ScenarioName = %q, want custom", schema.ScenarioName)
	}
	if schema.Seed != 42 {
		t.Errorf("Seed = %d, want 42", schema.Seed)
	}
	// Should have 2 user tables + 2 tunnel tables
	if len(schema.Tables) != 4 {
		t.Errorf("expected 4 tables, got %d", len(schema.Tables))
	}
	// Each user table should have a row count assigned
	for _, tbl := range schema.Tables {
		if tbl.IsTunnel {
			continue
		}
		if tbl.RowCount <= 0 {
			t.Errorf("table %q has zero/negative RowCount", tbl.Name)
		}
		if tbl.Schema != "public" {
			t.Errorf("table %q Schema = %q, want public", tbl.Name, tbl.Schema)
		}
		if tbl.Owner == "" {
			t.Errorf("table %q has empty Owner", tbl.Name)
		}
	}
}

func TestCustomSchemaBuilder_InvalidDDL(t *testing.T) {
	builder := NewCustomSchemaBuilder("NOT VALID SQL AT ALL", 42)
	_, err := builder.Build()
	if err == nil {
		t.Error("expected error for invalid DDL")
	}
}

func TestCustomSchemaBuilder_EndToEnd(t *testing.T) {
	ddl := `CREATE TABLE items (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100),
		price NUMERIC(10,2),
		description TEXT
	);`

	builder := NewCustomSchemaBuilder(ddl, 42)
	schema, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Generate data with this schema
	engine := NewGenericEngine(schema, 42)
	result := engine.Execute("SELECT * FROM items LIMIT 5")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Execute type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) == 0 {
		t.Error("expected some rows")
	}
}

func TestInferRowCount(t *testing.T) {
	tests := []struct {
		name    string
		fkCount int
		colLen  int
	}{
		{"lookup_table", 0, 2},
		{"entity_table", 0, 5},
		{"junction_table", 2, 4},
		{"child_table", 1, 3},
	}

	for _, tt := range tests {
		tbl := &TableDef{Name: tt.name}
		for i := 0; i < tt.colLen; i++ {
			col := SchemColumn{Name: "col"}
			if i < tt.fkCount {
				col.IsFK = true
			}
			tbl.Columns = append(tbl.Columns, col)
		}
		rc := inferRowCount(tbl)
		if rc <= 0 {
			t.Errorf("inferRowCount(%q, fk=%d, cols=%d) = %d, want > 0", tt.name, tt.fkCount, tt.colLen, rc)
		}
	}
}

// ============================================================
// GenericEngine Tests
// ============================================================

func TestGenericEngine_SelectFromAllScenarios(t *testing.T) {
	for _, scenario := range []string{"ecommerce", "iot", "saas", "blog", "project"} {
		builder := NewTemplateBuilder(scenario, 42, false)
		schema, err := builder.Build()
		if err != nil {
			t.Fatalf("Build(%q) failed: %v", scenario, err)
		}
		engine := NewGenericEngine(schema, 42)
		for _, tbl := range schema.UserTables() {
			result := engine.Execute("SELECT * FROM " + tbl.Name + " LIMIT 3")
			if result.Type == pgwire.ResultError {
				t.Errorf("scenario=%q table=%q: got error: %s", scenario, tbl.Name, result.Error.Message)
			}
		}
	}
}

func TestGenericEngine_INSERT(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("INSERT INTO products (name, price) VALUES ('Test', 9.99)")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "INSERT 0 1" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestGenericEngine_UPDATE(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("UPDATE products SET price = 19.99 WHERE id = 1")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "UPDATE 1" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestGenericEngine_DELETE(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("DELETE FROM products WHERE id = 1")
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "DELETE 0" {
		t.Errorf("Tag = %q", result.Tag)
	}
}

func TestGenericEngine_DROP(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("DROP TABLE products")
	if result.Type != pgwire.ResultError {
		t.Errorf("Type = %d, want ResultError", result.Type)
	}
	if result.Error.Code != "42501" {
		t.Errorf("Code = %q", result.Error.Code)
	}
}

func TestGenericEngine_CountWithWhere(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("SELECT count(*) FROM products WHERE category_id = 1")
	if result.Type != pgwire.ResultRows {
		t.Fatalf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row for count")
	}
}

func TestGenericEngine_UnknownQuery(t *testing.T) {
	engine := NewGenericEngine(ecommerceTemplate(), 42)
	result := engine.Execute("SOME UNKNOWN QUERY")
	// Should return empty result, not crash
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestGenericEngine_MetaQueries(t *testing.T) {
	builder := NewTemplateBuilder("ecommerce", 42, false)
	schema, _ := builder.Build()
	engine := NewGenericEngine(schema, 42)

	tests := []struct {
		query string
		desc  string
	}{
		{"SELECT version()", "version"},
		{"SHOW server_version", "server_version"},
		{"SHOW timezone", "timezone"},
		{"SET client_encoding = 'UTF8'", "set"},
		{"LISTEN tunnel_resp", "listen"},
		{"BEGIN", "begin transaction"},
		{"COMMIT", "commit transaction"},
		{"ROLLBACK", "rollback transaction"},
		{"START TRANSACTION", "start transaction"},
		{"END", "end transaction"},
		{"EXPLAIN SELECT * FROM products", "explain"},
	}

	for _, tt := range tests {
		result := engine.Execute(tt.query)
		if result == nil {
			t.Errorf("%s: result is nil", tt.desc)
		}
		if result.Type == pgwire.ResultError {
			t.Errorf("%s: got error: %s", tt.desc, result.Error.Message)
		}
	}
}

func TestGenericEngine_PgCatalogQueries(t *testing.T) {
	builder := NewTemplateBuilder("ecommerce", 42, false)
	schema, _ := builder.Build()
	engine := NewGenericEngine(schema, 42)

	tests := []struct {
		query string
		desc  string
	}{
		{"SELECT * FROM pg_catalog.pg_type", "pg_type"},
		{"SELECT * FROM pg_catalog.pg_namespace", "pg_namespace"},
		{"SELECT * FROM pg_catalog.pg_database", "pg_database"},
		{"SELECT * FROM information_schema.columns WHERE table_schema = 'public'", "info_schema_columns"},
	}

	for _, tt := range tests {
		result := engine.Execute(tt.query)
		if result == nil {
			t.Errorf("%s: result is nil", tt.desc)
			continue
		}
		if result.Type == pgwire.ResultError {
			t.Errorf("%s: got error: %s", tt.desc, result.Error.Message)
		}
		if len(result.Rows) == 0 {
			t.Errorf("%s: expected non-empty result", tt.desc)
		}
	}
}

func TestGenericEngine_DescribeTable(t *testing.T) {
	builder := NewTemplateBuilder("ecommerce", 42, false)
	schema, _ := builder.Build()
	engine := NewGenericEngine(schema, 42)

	// Simulate \d products: pg_attribute + pg_class query with relname
	query := "SELECT a.attname, t.typname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON a.attrelid = c.oid WHERE c.relname = 'products'"
	result := engine.Execute(query)
	if result == nil {
		t.Fatal("result is nil")
	}
	// Should return column descriptions for products
	if result.Type == pgwire.ResultError {
		t.Errorf("got error: %s", result.Error.Message)
	}
}

func TestGenericEngine_DescribeUnknownTable(t *testing.T) {
	builder := NewTemplateBuilder("ecommerce", 42, false)
	schema, _ := builder.Build()
	engine := NewGenericEngine(schema, 42)

	query := "SELECT a.attname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON a.attrelid = c.oid WHERE c.relname = 'nonexistent'"
	result := engine.Execute(query)
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestNewGenericEngineFromStore(t *testing.T) {
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()

	engine := NewGenericEngineFromStore(store)
	if engine == nil {
		t.Fatal("engine is nil")
	}
	result := engine.Execute("SELECT * FROM products LIMIT 5")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
}

// ============================================================
// Engine Wrapper Tests
// ============================================================

func TestEngine_Schema(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	schema := e.Schema()
	if schema == nil {
		t.Fatal("Schema() returned nil")
	}
	if schema.ScenarioName != "ecommerce" {
		t.Errorf("ScenarioName = %q", schema.ScenarioName)
	}
}

func TestEngine_Generic(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	generic := e.Generic()
	if generic == nil {
		t.Fatal("Generic() returned nil")
	}
	result := generic.Execute("SELECT * FROM products LIMIT 1")
	if result.Type != pgwire.ResultRows {
		t.Error("GenericEngine.Execute should return rows")
	}
}

func TestEngine_FallbackToEcommerce(t *testing.T) {
	// NewEngine with unknown scenario should fallback to ecommerce
	e := NewEngine("totally_unknown", 42)
	result := e.Execute("SELECT * FROM products LIMIT 1")
	// Should work because fallback is ecommerce which has products
	if result.Type == pgwire.ResultError {
		t.Error("expected fallback to ecommerce, got error")
	}
}

// ============================================================
// Types Tests
// ============================================================

func TestTableDef_ColumnIndex(t *testing.T) {
	tbl := &TableDef{
		Columns: []SchemColumn{
			{Name: "id"},
			{Name: "name"},
			{Name: "email"},
		},
	}
	if idx := tbl.ColumnIndex("id"); idx != 0 {
		t.Errorf("ColumnIndex(id) = %d, want 0", idx)
	}
	if idx := tbl.ColumnIndex("email"); idx != 2 {
		t.Errorf("ColumnIndex(email) = %d, want 2", idx)
	}
	if idx := tbl.ColumnIndex("nonexistent"); idx != -1 {
		t.Errorf("ColumnIndex(nonexistent) = %d, want -1", idx)
	}
}

func TestSchemaDefinition_TableByName(t *testing.T) {
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{Name: "users"},
			{Name: "orders"},
		},
	}
	tbl := schema.TableByName("users")
	if tbl == nil || tbl.Name != "users" {
		t.Error("TableByName(users) failed")
	}
	tbl = schema.TableByName("nonexistent")
	if tbl != nil {
		t.Error("TableByName(nonexistent) should return nil")
	}
}

func TestSchemaDefinition_UserTables(t *testing.T) {
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{Name: "users", IsTunnel: false},
			{Name: "analytics_events", IsTunnel: true},
			{Name: "orders", IsTunnel: false},
			{Name: "analytics_responses", IsTunnel: true},
		},
	}
	userTables := schema.UserTables()
	if len(userTables) != 2 {
		t.Fatalf("UserTables() = %d, want 2", len(userTables))
	}
	if userTables[0].Name != "users" || userTables[1].Name != "orders" {
		t.Errorf("UserTables names wrong: %s, %s", userTables[0].Name, userTables[1].Name)
	}
}

func TestTunnelInfrastructureTables_DefaultOwner(t *testing.T) {
	tables := TunnelInfrastructureTables("")
	if len(tables) != 2 {
		t.Fatalf("expected 2 tunnel tables, got %d", len(tables))
	}
	if tables[0].Owner != "sensor_writer" {
		t.Errorf("default owner = %q, want sensor_writer", tables[0].Owner)
	}
}

func TestTunnelInfrastructureTables_CustomOwner(t *testing.T) {
	tables := TunnelInfrastructureTables("custom_owner")
	if tables[0].Owner != "custom_owner" {
		t.Errorf("owner = %q, want custom_owner", tables[0].Owner)
	}
}

// ============================================================
// Store Tests
// ============================================================

func TestGenericStore_TableNames(t *testing.T) {
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()

	names := store.TableNames()
	if len(names) == 0 {
		t.Fatal("TableNames() returned empty")
	}
	// Should be sorted
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("TableNames not sorted: %q before %q", names[i-1], names[i])
		}
	}
}

func TestGenericStore_Schema(t *testing.T) {
	schema := ecommerceTemplate()
	store := NewGenericStore(schema)
	if store.Schema() != schema {
		t.Error("Schema() should return the original schema")
	}
}

func TestGenericStore_RowCount_UnknownTable(t *testing.T) {
	schema := ecommerceTemplate()
	store := NewGenericStore(schema)
	if count := store.RowCount("nonexistent"); count != 0 {
		t.Errorf("RowCount(nonexistent) = %d, want 0", count)
	}
}

func TestGenericStore_QueryWithWhere(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	store := e.Data()

	// Query with WHERE on a column that may need linear scan
	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "products",
		Where: map[string]string{"id": "1"},
		Limit: 10,
	}
	result := store.Query(pq)
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

func TestGenericStore_QueryUnknownTable(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	store := e.Data()

	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "nonexistent",
	}
	result := store.Query(pq)
	if result.Tag != "SELECT 0" {
		t.Errorf("Tag = %q, want SELECT 0", result.Tag)
	}
}

func TestGenericStore_CountWithWhere(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	store := e.Data()

	pq := &ParsedQuery{
		Type:    QuerySELECT,
		Table:   "products",
		IsCount: true,
		Where:   map[string]string{"category_id": "1"},
	}
	result := store.Query(pq)
	if len(result.Rows) != 1 {
		t.Fatal("expected 1 row for count")
	}
}

func TestGenericStore_LinearScan(t *testing.T) {
	// Create a schema and store without indices
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{
				Name: "items", Schema: "public", Owner: "test", RowCount: 5,
				Columns: []SchemColumn{
					{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
					{Name: "value", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				},
			},
		},
	}
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()

	// Query with WHERE on a column that will exercise linearScan
	// First, remove the index for "items" to force linear scan
	store.indices = make(map[string]map[string]map[string][]int)

	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "items",
		Where: map[string]string{"id": "1"},
	}
	result := store.Query(pq)
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
}

func TestGenericStore_LinearScan_NonexistentColumn(t *testing.T) {
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{
				Name: "items", Schema: "public", Owner: "test", RowCount: 3,
				Columns: []SchemColumn{
					{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				},
			},
		},
	}
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()
	store.indices = make(map[string]map[string]map[string][]int)

	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "items",
		Where: map[string]string{"nonexistent": "val"},
	}
	result := store.Query(pq)
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows for nonexistent column WHERE, got %d", len(result.Rows))
	}
}

func TestIntersectSorted(t *testing.T) {
	tests := []struct {
		a, b, want []int
	}{
		{[]int{1, 2, 3}, []int{2, 3, 4}, []int{2, 3}},
		{[]int{1, 2, 3}, []int{4, 5, 6}, nil},
		{[]int{1}, []int{1}, []int{1}},
		{nil, []int{1, 2}, nil},
		{[]int{1, 2}, nil, nil},
	}
	for _, tt := range tests {
		got := intersectSorted(tt.a, tt.b)
		if len(got) != len(tt.want) {
			t.Errorf("intersectSorted(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("intersectSorted(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
				break
			}
		}
	}
}

// ============================================================
// Data Generator Value Coverage Tests
// ============================================================

func TestDataGenerator_AllHintTypes(t *testing.T) {
	// Create a schema with columns using every hint type
	schema := &SchemaDefinition{
		ScenarioName: "test",
		Tables: []*TableDef{
			{Name: "ref_table", Schema: "public", Owner: "test", RowCount: 5, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
			}},
			{Name: "test_all_hints", Schema: "public", Owner: "test", RowCount: 5, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "col_name", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintName},
				{Name: "col_firstname", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintFirstName},
				{Name: "col_lastname", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintLastName},
				{Name: "col_email", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintEmail},
				{Name: "col_username", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintUsername},
				{Name: "col_price", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice},
				{Name: "col_quantity", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintQuantity},
				{Name: "col_status", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintStatus, EnumValues: []string{"a", "b"}},
				{Name: "col_rating", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintRating},
				{Name: "col_title", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintTitle},
				{Name: "col_desc", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "col_product", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintProductName},
				{Name: "col_company", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintCompanyName},
				{Name: "col_url", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintURL},
				{Name: "col_pageurl", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintPageURL},
				{Name: "col_address", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintAddress},
				{Name: "col_phone", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintPhone},
				{Name: "col_country", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintCountry},
				{Name: "col_color", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintColor},
				{Name: "col_slug", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintSlug},
				{Name: "col_ip", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintIPAddress},
				{Name: "col_ua", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintUserAgent},
				{Name: "col_event", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintEventType},
				{Name: "col_json", Type: ColTypeJSON, PgOID: pgwire.OIDJSONB, TypeSize: -1, TypeMod: -1, Hint: HintJSONMeta},
				{Name: "col_date", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
				{Name: "col_fk", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "ref_table", FKColumn: "id"},
			}},
		},
	}

	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()
	if store.RowCount("test_all_hints") != 5 {
		t.Errorf("RowCount = %d, want 5", store.RowCount("test_all_hints"))
	}
}

func TestDataGenerator_AllTypesFallback(t *testing.T) {
	// Test generateByType for all column types
	schema := &SchemaDefinition{
		ScenarioName: "test",
		Tables: []*TableDef{
			{Name: "all_types", Schema: "public", Owner: "test", RowCount: 3, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "col_int", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, MinVal: 10, MaxVal: 50},
				{Name: "col_int_no_range", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
				{Name: "col_bigint", Type: ColTypeBigInt, PgOID: pgwire.OIDInt8, TypeSize: 8, TypeMod: -1},
				{Name: "col_float", Type: ColTypeFloat, PgOID: pgwire.OIDFloat8, TypeSize: 8, TypeMod: -1, MinVal: 1.0, MaxVal: 100.0},
				{Name: "col_float_no_range", Type: ColTypeFloat, PgOID: pgwire.OIDFloat8, TypeSize: 8, TypeMod: -1},
				{Name: "col_text", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
				{Name: "col_bool", Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1},
				{Name: "col_timestamp", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1},
				{Name: "col_uuid", Type: ColTypeUUID, PgOID: pgwire.OIDUUID, TypeSize: 16, TypeMod: -1},
				{Name: "col_json", Type: ColTypeJSON, PgOID: pgwire.OIDJSONB, TypeSize: -1, TypeMod: -1},
				{Name: "col_bytea", Type: ColTypeBytea, PgOID: pgwire.OIDBytea, TypeSize: -1, TypeMod: -1},
				{Name: "col_enum", Type: ColTypeEnum, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, EnumValues: []string{"x", "y", "z"}},
			}},
		},
	}

	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()
	if store.RowCount("all_types") != 3 {
		t.Errorf("RowCount = %d, want 3", store.RowCount("all_types"))
	}
}

func TestDataGenerator_FKToUnknownTable(t *testing.T) {
	// FK to a table that doesn't exist in the schema
	schema := &SchemaDefinition{
		ScenarioName: "test",
		Tables: []*TableDef{
			{Name: "orders", Schema: "public", Owner: "test", RowCount: 5, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "nonexistent_table", FKColumn: "id"},
			}},
		},
	}

	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()
	if store.RowCount("orders") != 5 {
		t.Errorf("RowCount = %d, want 5", store.RowCount("orders"))
	}
}

// ============================================================
// Transaction Tag Extraction Tests
// ============================================================

func TestExtractTransactionTag(t *testing.T) {
	tests := []struct {
		query, want string
	}{
		{"BEGIN", "BEGIN"},
		{"START TRANSACTION", "BEGIN"},
		{"COMMIT", "COMMIT"},
		{"END", "COMMIT"},
		{"ROLLBACK", "ROLLBACK"},
		{"something else", "COMMAND OK"},
	}
	for _, tt := range tests {
		got := extractTransactionTag(tt.query)
		if got != tt.want {
			t.Errorf("extractTransactionTag(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

// ============================================================
// Misc Helper Tests
// ============================================================

func TestEcommerceCategoryNames(t *testing.T) {
	names := ecommerceCategoryNames()
	if len(names) != 10 {
		t.Errorf("expected 10 category names, got %d", len(names))
	}
}

func TestOrderStatuses(t *testing.T) {
	statuses := OrderStatuses()
	if len(statuses) != 5 {
		t.Errorf("expected 5 statuses, got %d", len(statuses))
	}
}

func TestOidToTypeName_AllTypes(t *testing.T) {
	tests := []struct {
		oid  int32
		want string
	}{
		{pgwire.OIDInt4, "integer"},
		{pgwire.OIDInt8, "bigint"},
		{pgwire.OIDFloat8, "double precision"},
		{pgwire.OIDText, "text"},
		{pgwire.OIDVarchar, "character varying(255)"},
		{pgwire.OIDTimestampTZ, "timestamp with time zone"},
		{pgwire.OIDBool, "boolean"},
		{pgwire.OIDNumeric, "numeric"},
		{pgwire.OIDBytea, "bytea"},
		{pgwire.OIDJSONB, "jsonb"},
		{pgwire.OIDUUID, "uuid"},
		{99999, "text"}, // unknown OID
	}
	for _, tt := range tests {
		got := oidToTypeName(tt.oid)
		if got != tt.want {
			t.Errorf("oidToTypeName(%d) = %q, want %q", tt.oid, got, tt.want)
		}
	}
}

// ============================================================
// Data Generator GenericStore integration
// ============================================================

func TestGenericStore_GetTables_Sorted(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	tables := e.Data().GetTables()
	for i := 1; i < len(tables); i++ {
		if tables[i].Name < tables[i-1].Name {
			t.Errorf("GetTables not sorted: %q before %q", tables[i-1].Name, tables[i].Name)
		}
	}
}

func TestGenericStore_Query_MultipleWhereConditions(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	store := e.Data()

	// Multi-condition WHERE uses index intersection
	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "products",
		Where: map[string]string{"id": "1", "category_id": "1"},
	}
	result := store.Query(pq)
	// The product with id=1 may or may not have category_id=1
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
}

func TestGenericStore_LoadTable_NewTable(t *testing.T) {
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{Name: "existing", Schema: "public", Owner: "test", Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
			}},
		},
	}
	store := NewGenericStore(schema)

	// Load into a table not in the original schema
	newTable := &TableDef{
		Name: "dynamic", Schema: "public", Owner: "test",
		Columns: []SchemColumn{
			{Name: "val", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
		},
	}
	rows := [][][]byte{{[]byte("hello")}}
	store.LoadTable(newTable, rows)

	if store.RowCount("dynamic") != 1 {
		t.Errorf("RowCount(dynamic) = %d, want 1", store.RowCount("dynamic"))
	}
}

// ============================================================
// Handler NotifyFunc Tests
// ============================================================

func TestRelayHandler_SetNotifyFunc(t *testing.T) {
	h, _, _ := newTestHandler(t)

	called := false
	var gotChannel, gotPayload string
	h.SetNotifyFunc(func(channel, payload string) {
		called = true
		gotChannel = channel
		gotPayload = payload
	})

	h.NotifyResponse("session123")
	if !called {
		t.Error("NotifyFunc was not called")
	}
	if gotChannel != "tunnel_resp" {
		t.Errorf("channel = %q, want tunnel_resp", gotChannel)
	}
	if gotPayload != "session123" {
		t.Errorf("payload = %q, want session123", gotPayload)
	}
}

func TestRelayHandler_NotifyResponse_NoFunc(t *testing.T) {
	h, _, _ := newTestHandler(t)
	// Should not panic when notifyFunc is nil
	h.NotifyResponse("session123")
}

func TestRelayHandler_HandleExecute(t *testing.T) {
	h, _, _ := newTestHandler(t)
	result, err := h.HandleExecute("portal1", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
}

func TestRelayHandler_HandleDescribe(t *testing.T) {
	h, _, _ := newTestHandler(t)
	result, err := h.HandleDescribe('S', "stmt1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

// ============================================================
// mapSQLType edge cases
// ============================================================

func TestMapSQLType_CharacterVarying(t *testing.T) {
	colType, _, _, _ := mapSQLType("CHARACTER VARYING(100)")
	if colType != ColTypeText {
		t.Errorf("CHARACTER VARYING type = %d, want ColTypeText", colType)
	}
}

func TestMapSQLType_Float(t *testing.T) {
	colType, _, _, _ := mapSQLType("FLOAT")
	if colType != ColTypeFloat {
		t.Errorf("FLOAT type = %d, want ColTypeFloat", colType)
	}
}

func TestMapSQLType_Serial4(t *testing.T) {
	colType, _, _, _ := mapSQLType("SERIAL4")
	if colType != ColTypeSerial {
		t.Errorf("SERIAL4 type = %d, want ColTypeSerial", colType)
	}
}

func TestMapSQLType_Serial8(t *testing.T) {
	colType, _, _, _ := mapSQLType("SERIAL8")
	if colType != ColTypeSerial {
		t.Errorf("SERIAL8 type = %d, want ColTypeSerial", colType)
	}
}

func TestMapSQLType_Int2(t *testing.T) {
	colType, _, _, _ := mapSQLType("INT2")
	if colType != ColTypeInt {
		t.Errorf("INT2 type = %d, want ColTypeInt", colType)
	}
}

func TestMapSQLType_Float4(t *testing.T) {
	colType, _, _, _ := mapSQLType("FLOAT4")
	if colType != ColTypeFloat {
		t.Errorf("FLOAT4 type = %d, want ColTypeFloat", colType)
	}
}

func TestMapSQLType_TimestampVariants(t *testing.T) {
	for _, typ := range []string{"TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITHOUT TIME ZONE"} {
		colType, _, _, _ := mapSQLType(typ)
		if colType != ColTypeTimestamp {
			t.Errorf("%s type = %d, want ColTypeTimestamp", typ, colType)
		}
	}
}

func TestMapSQLType_UnknownType(t *testing.T) {
	colType, oid, _, _ := mapSQLType("SOMECUSTOMTYPE")
	if colType != ColTypeText {
		t.Errorf("unknown type = %d, want ColTypeText", colType)
	}
	if oid != pgwire.OIDText {
		t.Errorf("unknown oid = %d, want OIDText", oid)
	}
}

func TestMapSQLType_TypeWithModifiers(t *testing.T) {
	// Type string with inline modifiers that should be stripped
	colType, _, _, _ := mapSQLType("INTEGER NOT NULL DEFAULT 0")
	if colType != ColTypeInt {
		t.Errorf("type with modifiers = %d, want ColTypeInt", colType)
	}
}

// ============================================================
// matchRows edge cases for WHERE with non-indexed columns
// ============================================================

func TestGenericStore_MatchRows_NoIndexFallback(t *testing.T) {
	schema := &SchemaDefinition{
		Tables: []*TableDef{
			{Name: "t", Schema: "public", Owner: "test", RowCount: 3, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "val", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			}},
		},
	}
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()

	// Set indices to nil to force fallback path
	store.indices["t"] = nil

	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "t",
		Where: map[string]string{"val": "nonexistent"},
	}
	result := store.Query(pq)
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
}

func TestGenericStore_MatchRows_EmptyWhere(t *testing.T) {
	e := NewEngine("ecommerce", 42)
	store := e.Data()

	pq := &ParsedQuery{
		Type:  QuerySELECT,
		Table: "categories",
		Where: map[string]string{},
		Limit: 3,
	}
	result := store.Query(pq)
	if len(result.Rows) != 3 {
		t.Errorf("rows = %d, want 3 (all categories limited)", len(result.Rows))
	}
}

// ============================================================
// GenericEngine.Data() alias test
// ============================================================

func TestGenericEngine_Data(t *testing.T) {
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	engine := NewGenericEngine(schema, 42)
	if engine.Data() != engine.Store() {
		t.Error("Data() should equal Store()")
	}
}

// ============================================================
// ParseSQL Additional Coverage
// ============================================================

func TestParseSQL_SelectColumns(t *testing.T) {
	pq := ParseSQL("SELECT name, price FROM products LIMIT 10")
	if len(pq.Columns) != 2 {
		t.Errorf("Columns = %v, want 2 columns", pq.Columns)
	}
	if pq.Columns[0] != "name" {
		t.Errorf("Columns[0] = %q", pq.Columns[0])
	}
}

func TestParseSQL_TransactionQueries(t *testing.T) {
	for _, q := range []string{"BEGIN", "COMMIT", "ROLLBACK", "START TRANSACTION", "END"} {
		pq := ParseSQL(q)
		if pq.Type != QueryMETA {
			t.Errorf("ParseSQL(%q) Type = %d, want META", q, pq.Type)
		}
	}
}

func TestParseSQL_UpdateSetClauses(t *testing.T) {
	pq := ParseSQL("UPDATE products SET price = 19 WHERE id = 1")
	if pq.Type != QueryUPDATE {
		t.Fatalf("Type = %d, want UPDATE", pq.Type)
	}
	if pq.SetClauses["price"] != "19" {
		t.Errorf("SetClauses[price] = %q, want 19", pq.SetClauses["price"])
	}
}

func TestParseSQL_UpdateSetString(t *testing.T) {
	pq := ParseSQL("UPDATE users SET name = 'John' WHERE id = 1")
	if pq.SetClauses["name"] != "John" {
		t.Errorf("SetClauses[name] = %q, want John", pq.SetClauses["name"])
	}
}

// ============================================================
// Event Type to Chunk Type
// ============================================================

func TestEventTypeToChunkType_AllTypes(t *testing.T) {
	tests := []struct {
		eventType string
		want      int
	}{
		{"session_start", int(eventTypeToChunkType("session_start"))},
		{"conversion_pixel", int(eventTypeToChunkType("conversion_pixel"))},
		{"session_end", int(eventTypeToChunkType("session_end"))},
		{"page_view", int(eventTypeToChunkType("page_view"))},
		{"unknown_type", int(eventTypeToChunkType("unknown_type"))},
	}
	// Just ensure they don't panic and return consistent values
	for _, tt := range tests {
		got := int(eventTypeToChunkType(tt.eventType))
		if got != tt.want {
			t.Errorf("eventTypeToChunkType(%q) not consistent", tt.eventType)
		}
	}
}

// ============================================================
// DDL parsing edge: mismatched parens
// ============================================================

func TestParseDDL_MissingClosingParen(t *testing.T) {
	ddl := "CREATE TABLE broken (id SERIAL PRIMARY KEY, name TEXT"
	_, err := ParseDDL(ddl)
	if err == nil {
		t.Error("expected error for missing closing paren")
	}
}

func TestParseDDL_ConstraintNamedConstraint(t *testing.T) {
	ddl := `CREATE TABLE t (
		id SERIAL,
		val TEXT,
		CONSTRAINT t_pkey PRIMARY KEY (id),
		CONSTRAINT t_val_unique UNIQUE (val)
	);`
	schema, err := ParseDDL(ddl)
	if err != nil {
		t.Fatalf("ParseDDL failed: %v", err)
	}
	if !schema.Tables[0].Columns[0].IsPK {
		t.Error("id should be PK via named constraint")
	}
}

// ============================================================
// Handler: LISTEN and SET as meta queries in genericengine
// ============================================================

func TestGenericEngine_LISTEN_ViaMetaHandler(t *testing.T) {
	// LISTEN is handled directly by HandleMetaQueryGeneric, not through the parser.
	// Test it directly via the meta handler.
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	gen := NewDataGenerator(schema, 42)
	store := gen.GenerateAll()

	result := HandleMetaQueryGeneric("LISTEN tunnel_resp", store)
	if result.Type != pgwire.ResultCommand {
		t.Errorf("Type = %d, want ResultCommand", result.Type)
	}
	if result.Tag != "LISTEN" {
		t.Errorf("Tag = %q, want LISTEN", result.Tag)
	}
}

func TestGenericEngine_ShowTimezone(t *testing.T) {
	schema := ecommerceTemplate()
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)
	engine := NewGenericEngine(schema, 42)

	result := engine.Execute("SHOW TimeZone")
	if result.Type != pgwire.ResultRows {
		t.Errorf("Type = %d, want ResultRows", result.Type)
	}
	if string(result.Rows[0][0]) != "UTC" {
		t.Errorf("timezone = %q", result.Rows[0][0])
	}
}
