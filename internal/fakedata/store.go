package fakedata

import (
	"fmt"
	"sort"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// GenericStore holds all generated data for all tables.
type GenericStore struct {
	schema  *SchemaDefinition
	tables  map[string]*TableStore
	indices map[string]map[string]map[string][]int // table → column → value → row indices
}

// TableStore holds rows for a single table.
type TableStore struct {
	Def     *TableDef
	Columns []pgwire.ColumnDef
	Rows    [][][]byte
}

// NewGenericStore creates an empty store for the given schema.
func NewGenericStore(schema *SchemaDefinition) *GenericStore {
	store := &GenericStore{
		schema:  schema,
		tables:  make(map[string]*TableStore),
		indices: make(map[string]map[string]map[string][]int),
	}

	// Initialize table stores with column definitions (even for empty tables)
	for _, t := range schema.Tables {
		store.tables[t.Name] = &TableStore{
			Def:     t,
			Columns: t.PgColumns(),
		}
	}

	return store
}

// LoadTable loads generated rows into a table.
func (s *GenericStore) LoadTable(table *TableDef, rows [][][]byte) {
	ts, ok := s.tables[table.Name]
	if !ok {
		ts = &TableStore{
			Def:     table,
			Columns: table.PgColumns(),
		}
		s.tables[table.Name] = ts
	}
	ts.Rows = rows
}

// BuildIndices creates search indices for WHERE filtering.
func (s *GenericStore) BuildIndices() {
	for tableName, ts := range s.tables {
		if len(ts.Rows) == 0 {
			continue
		}
		colIndex := make(map[string]map[string][]int)
		for colIdx, col := range ts.Def.Columns {
			valMap := make(map[string][]int)
			for rowIdx, row := range ts.Rows {
				if colIdx < len(row) && row[colIdx] != nil {
					val := string(row[colIdx])
					valMap[val] = append(valMap[val], rowIdx)
				}
			}
			if len(valMap) > 0 {
				colIndex[col.Name] = valMap
			}
		}
		s.indices[tableName] = colIndex
	}
}

// Query executes a parsed SELECT query against the store.
func (s *GenericStore) Query(pq *ParsedQuery) *pgwire.QueryResult {
	ts, ok := s.tables[pq.Table]
	if !ok {
		return emptySelectResult()
	}

	if pq.IsCount {
		count := s.filteredCount(pq)
		return countResult(count)
	}

	// Get matching row indices
	indices := s.matchRows(pq)

	// Apply LIMIT
	if pq.Limit > 0 && pq.Limit < len(indices) {
		indices = indices[:pq.Limit]
	}

	// Build result rows
	rows := make([][][]byte, len(indices))
	for i, idx := range indices {
		rows[i] = ts.Rows[idx]
	}

	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: ts.Columns,
		Rows:    rows,
		Tag:     fmt.Sprintf("SELECT %d", len(rows)),
	}
}

// matchRows returns row indices matching the WHERE clause.
func (s *GenericStore) matchRows(pq *ParsedQuery) []int {
	ts := s.tables[pq.Table]
	if ts == nil || len(ts.Rows) == 0 {
		return nil
	}

	if len(pq.Where) == 0 {
		// No filter: return all indices
		indices := make([]int, len(ts.Rows))
		for i := range indices {
			indices[i] = i
		}
		return indices
	}

	tableIdx := s.indices[pq.Table]
	if tableIdx == nil {
		// No index, linear scan
		return s.linearScan(pq)
	}

	// Use index for each WHERE condition, intersect results
	var result []int
	first := true
	for col, val := range pq.Where {
		colIdx, ok := tableIdx[col]
		if !ok {
			// Column not indexed, try linear scan for this column
			continue
		}
		matchedRows := colIdx[val]
		if first {
			result = make([]int, len(matchedRows))
			copy(result, matchedRows)
			first = false
		} else {
			result = intersectSorted(result, matchedRows)
		}
	}

	if first {
		// No indexed columns matched, fall back to linear scan
		return s.linearScan(pq)
	}

	return result
}

// linearScan does a brute-force row match for WHERE conditions.
func (s *GenericStore) linearScan(pq *ParsedQuery) []int {
	ts := s.tables[pq.Table]
	if ts == nil {
		return nil
	}

	var result []int
	for rowIdx, row := range ts.Rows {
		match := true
		for col, val := range pq.Where {
			colIdx := ts.Def.ColumnIndex(col)
			if colIdx < 0 || colIdx >= len(row) {
				match = false
				break
			}
			if row[colIdx] == nil || string(row[colIdx]) != val {
				match = false
				break
			}
		}
		if match {
			result = append(result, rowIdx)
		}
	}
	return result
}

// filteredCount returns the count of rows matching a WHERE clause.
func (s *GenericStore) filteredCount(pq *ParsedQuery) int {
	if len(pq.Where) == 0 {
		ts := s.tables[pq.Table]
		if ts == nil {
			return 0
		}
		return len(ts.Rows)
	}
	return len(s.matchRows(pq))
}

// GetTableColumns returns pgwire column definitions for a table.
func (s *GenericStore) GetTableColumns(table string) []pgwire.ColumnDef {
	ts, ok := s.tables[table]
	if !ok {
		return nil
	}
	return ts.Columns
}

// GetTables returns TableInfo list for \dt metadata.
func (s *GenericStore) GetTables() []TableInfo {
	// Sort by name for deterministic output
	names := make([]string, 0, len(s.tables))
	for name := range s.tables {
		names = append(names, name)
	}
	sort.Strings(names)

	var tables []TableInfo
	for _, name := range names {
		ts := s.tables[name]
		tables = append(tables, TableInfo{
			Schema: ts.Def.Schema,
			Name:   ts.Def.Name,
			Type:   "table",
			Owner:  ts.Def.Owner,
		})
	}
	return tables
}

// RowCount returns the number of rows in a table.
func (s *GenericStore) RowCount(table string) int {
	ts, ok := s.tables[table]
	if !ok {
		return 0
	}
	return len(ts.Rows)
}

// Schema returns the underlying schema definition.
func (s *GenericStore) Schema() *SchemaDefinition {
	return s.schema
}

// TableNames returns all table names.
func (s *GenericStore) TableNames() []string {
	names := make([]string, 0, len(s.tables))
	for name := range s.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// intersectSorted returns the intersection of two sorted int slices.
func intersectSorted(a, b []int) []int {
	// Sort both to ensure sorted order
	sort.Ints(a)
	sort.Ints(b)

	var result []int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			result = append(result, a[i])
			i++
			j++
		} else if a[i] < b[j] {
			i++
		} else {
			j++
		}
	}
	return result
}

func emptySelectResult() *pgwire.QueryResult {
	return &pgwire.QueryResult{
		Type:    pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{{Name: "result", OID: pgwire.OIDText, TypeSize: -1, TypeMod: -1}},
		Tag:     "SELECT 0",
	}
}

func countResult(n int) *pgwire.QueryResult {
	return &pgwire.QueryResult{
		Type: pgwire.ResultRows,
		Columns: []pgwire.ColumnDef{
			{Name: "count", OID: pgwire.OIDInt8, TypeSize: 8, TypeMod: -1},
		},
		Rows: [][][]byte{
			{[]byte(fmt.Sprintf("%d", n))},
		},
		Tag: "SELECT 1",
	}
}
