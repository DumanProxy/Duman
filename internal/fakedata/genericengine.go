package fakedata

import (
	"fmt"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// GenericEngine processes SQL queries against a GenericStore.
// This is the primary engine for all scenarios (built-in, random, and custom).
type GenericEngine struct {
	store  *GenericStore
	schema *SchemaDefinition
}

// NewGenericEngine creates a generic engine from a schema definition.
func NewGenericEngine(schema *SchemaDefinition, seed int64) *GenericEngine {
	gen := NewDataGenerator(schema, seed)
	store := gen.GenerateAll()
	return &GenericEngine{store: store, schema: schema}
}

// NewGenericEngineFromStore creates an engine from a pre-built store.
func NewGenericEngineFromStore(store *GenericStore) *GenericEngine {
	return &GenericEngine{store: store, schema: store.Schema()}
}

// Execute processes a SQL query and returns a result.
func (e *GenericEngine) Execute(query string) *pgwire.QueryResult {
	pq := ParseSQL(query)

	// Meta queries (psql \dt, \d, version, SHOW, SET, LISTEN)
	if pq.IsMeta {
		return HandleMetaQueryGeneric(query, e.store)
	}

	// Destructive queries (DROP, TRUNCATE, ALTER)
	if pq.Type == QueryDESTRUCTIVE {
		return &pgwire.QueryResult{
			Type: pgwire.ResultError,
			Error: &pgwire.ErrorDetail{
				Severity: "ERROR",
				Code:     "42501",
				Message:  fmt.Sprintf("permission denied for table %s", pq.Table),
			},
		}
	}

	switch pq.Type {
	case QuerySELECT:
		return e.store.Query(pq)
	case QueryINSERT:
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "INSERT 0 1"}
	case QueryUPDATE:
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "UPDATE 1"}
	case QueryDELETE:
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "DELETE 0"}
	default:
		return &pgwire.QueryResult{
			Type:    pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1}},
			Tag:     "SELECT 0",
		}
	}
}

// Store returns the underlying GenericStore.
func (e *GenericEngine) Store() *GenericStore {
	return e.store
}

// Schema returns the schema definition.
func (e *GenericEngine) Schema() *SchemaDefinition {
	return e.schema
}

// Data returns the store (alias for handler.go compatibility).
func (e *GenericEngine) Data() *GenericStore {
	return e.store
}

// HandleMetaQueryGeneric processes psql metadata queries using GenericStore.
func HandleMetaQueryGeneric(query string, store *GenericStore) *pgwire.QueryResult {
	// \dt - list tables
	if isListTablesQuery(query) {
		return listTablesGeneric(store.GetTables())
	}

	// \d <table> - describe table
	if table := extractDescribeTable(query); table != "" {
		cols := store.GetTableColumns(table)
		if cols == nil {
			return &pgwire.QueryResult{
				Type: pgwire.ResultError,
				Error: &pgwire.ErrorDetail{
					Severity: "ERROR",
					Code:     "42P01",
					Message:  fmt.Sprintf("relation \"%s\" does not exist", table),
				},
			}
		}
		return describeTableGeneric(table, cols)
	}

	// SELECT version()
	if isVersionQuery(query) {
		return &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "version", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{[]byte("PostgreSQL 16.2 on x86_64-pc-linux-gnu, compiled by gcc (GCC) 13.2.1, 64-bit")},
			},
			Tag: "SELECT 1",
		}
	}

	// SHOW server_version
	if isShowServerVersion(query) {
		return &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "server_version", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{[]byte("16.2")},
			},
			Tag: "SHOW",
		}
	}

	// SHOW timezone
	if isShowTimezone(query) {
		return &pgwire.QueryResult{
			Type: pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{
				{Name: "TimeZone", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
			},
			Rows: [][][]byte{
				{[]byte("UTC")},
			},
			Tag: "SHOW",
		}
	}

	// SET commands
	if isSetQuery(query) {
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "SET"}
	}

	// LISTEN commands
	if isListenQuery(query) {
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: "LISTEN"}
	}

	// --- Task 46: Transaction control ---
	if isTransactionQuery(query) {
		return &pgwire.QueryResult{Type: pgwire.ResultCommand, Tag: extractTransactionTag(query)}
	}

	// --- Task 46: EXPLAIN queries ---
	if isExplainQuery(query) {
		return explainResult()
	}

	// --- Task 45: pg_catalog.pg_type queries ---
	if isPgTypeQuery(query) {
		return pgTypeResult()
	}

	// --- Task 45: pg_catalog.pg_namespace queries ---
	if isPgNamespaceQuery(query) {
		return pgNamespaceResult()
	}

	// --- Task 45: pg_catalog.pg_database queries ---
	if isPgDatabaseQuery(query) {
		return pgDatabaseResult()
	}

	// --- Task 45: information_schema.columns queries ---
	if isInformationSchemaColumnsQuery(query) {
		return informationSchemaColumnsResult(store)
	}

	// --- Task 45: pg_attribute + pg_class combined queries (psql \d) ---
	if isPgAttributeClassQuery(query) {
		table := extractDescribeTable(query)
		if table != "" {
			cols := store.GetTableColumns(table)
			if cols != nil {
				return describeTableGeneric(table, cols)
			}
		}
		// Table not found or not extractable: return empty
		return &pgwire.QueryResult{
			Type:    pgwire.ResultRows,
			Columns: []pgwire.ColumnDef{{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1}},
			Rows:    nil,
			Tag:     "SELECT 0",
		}
	}

	// Default: empty result
	return &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
		},
		Rows: nil,
		Tag:  "SELECT 0",
	}
}

func listTablesGeneric(tables []TableInfo) *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "Schema", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Name", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Type", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Owner", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
	}

	var rows [][][]byte
	for _, t := range tables {
		rows = append(rows, [][]byte{
			[]byte(t.Schema),
			[]byte(t.Name),
			[]byte(t.Type),
			[]byte(t.Owner),
		})
	}

	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}

func describeTableGeneric(table string, columns []pgwire.ColumnDef) *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "Column", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Type", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Nullable", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "Default", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
	}

	var rows [][][]byte
	for _, c := range columns {
		typeName := oidToTypeName(c.OID)
		nullable := "YES"
		if c.Name == "id" {
			nullable = "NO"
		}
		rows = append(rows, [][]byte{
			[]byte(c.Name),
			[]byte(typeName),
			[]byte(nullable),
			nil,
		})
	}

	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}

// --- Task 46: EXPLAIN result ---

func explainResult() *pgwire.QueryResult {
	plan := [][]byte{
		[]byte("Seq Scan  (cost=0.00..35.50 rows=2550 width=36)"),
	}
	return &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "QUERY PLAN", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1},
		},
		Rows: [][][]byte{plan},
		Tag:  "EXPLAIN",
	}
}

// --- Task 45: Catalog result builders ---

func pgTypeResult() *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "oid", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "typname", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "typnamespace", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "typlen", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "typtype", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
	}
	// Common PostgreSQL types
	typeEntries := []struct {
		oid, ns, length string
		name, typtype   string
	}{
		{"16", "11", "1", "bool", "b"},
		{"17", "11", "-1", "bytea", "b"},
		{"20", "11", "8", "int8", "b"},
		{"23", "11", "4", "int4", "b"},
		{"25", "11", "-1", "text", "b"},
		{"701", "11", "8", "float8", "b"},
		{"1043", "11", "-1", "varchar", "b"},
		{"1184", "11", "8", "timestamptz", "b"},
		{"1700", "11", "-1", "numeric", "b"},
		{"2950", "11", "16", "uuid", "b"},
		{"3802", "11", "-1", "jsonb", "b"},
	}
	var rows [][][]byte
	for _, t := range typeEntries {
		rows = append(rows, [][]byte{
			[]byte(t.oid), []byte(t.name), []byte(t.ns), []byte(t.length), []byte(t.typtype),
		})
	}
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}

func pgNamespaceResult() *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "oid", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "nspname", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "nspowner", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
	}
	rows := [][][]byte{
		{[]byte("11"), []byte("pg_catalog"), []byte("10")},
		{[]byte("2200"), []byte("public"), []byte("10")},
	}
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}

func pgDatabaseResult() *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "oid", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "datname", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "datdba", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "encoding", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "datcollate", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
	}
	rows := [][][]byte{
		{[]byte("16384"), []byte("telemetry"), []byte("10"), []byte("6"), []byte("en_US.UTF-8")},
	}
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     "SELECT 1",
	}
}

func informationSchemaColumnsResult(store *GenericStore) *pgwire.QueryResult {
	cols := []pgwire.ColumnDef{
		{Name: "table_catalog", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "table_schema", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "table_name", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "column_name", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "ordinal_position", OID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1},
		{Name: "data_type", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
		{Name: "is_nullable", OID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: -1},
	}
	var rows [][][]byte
	for _, tableName := range store.TableNames() {
		tableCols := store.GetTableColumns(tableName)
		for i, c := range tableCols {
			nullable := "YES"
			if c.Name == "id" {
				nullable = "NO"
			}
			rows = append(rows, [][]byte{
				[]byte("telemetry"),
				[]byte("public"),
				[]byte(tableName),
				[]byte(c.Name),
				[]byte(fmt.Sprintf("%d", i+1)),
				[]byte(oidToTypeName(c.OID)),
				[]byte(nullable),
			})
		}
	}
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: cols,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}
