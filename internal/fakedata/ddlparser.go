package fakedata

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// DDL parsing regexes
var (
	reCreateTable   = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?("?\w+"?)\s*\(`)
	reColumnDef     = regexp.MustCompile(`(?i)^\s*("?\w+"?)\s+(\w+(?:\s*\([^)]*\))?(?:\s+\w+)*)\s*(.*)`)
	reConstraint    = regexp.MustCompile(`(?i)^\s*(?:CONSTRAINT\s+\w+\s+)?(?:PRIMARY\s+KEY|FOREIGN\s+KEY|UNIQUE|CHECK)`)
	reInlineFK      = regexp.MustCompile(`(?i)REFERENCES\s+("?\w+"?)\s*\(\s*("?\w+"?)\s*\)`)
	reInlinePK      = regexp.MustCompile(`(?i)PRIMARY\s+KEY`)
	reInlineNotNull = regexp.MustCompile(`(?i)NOT\s+NULL`)
	reVarcharLen    = regexp.MustCompile(`(?i)(?:VARCHAR|CHARACTER\s+VARYING)\s*\(\s*(\d+)\s*\)`)
	reNumericPrec   = regexp.MustCompile(`(?i)(?:NUMERIC|DECIMAL)\s*\(\s*(\d+)\s*(?:,\s*(\d+))?\s*\)`)
	reFKConstraint  = regexp.MustCompile(`(?i)FOREIGN\s+KEY\s*\(\s*("?\w+"?)\s*\)\s*REFERENCES\s+("?\w+"?)\s*\(\s*("?\w+"?)\s*\)`)
	rePKConstraint  = regexp.MustCompile(`(?i)PRIMARY\s+KEY\s*\(\s*("?\w+"?)\s*\)`)
)

// ParseDDL parses one or more CREATE TABLE statements into a SchemaDefinition.
func ParseDDL(sql string) (*SchemaDefinition, error) {
	schema := &SchemaDefinition{
		ScenarioName: "custom",
	}

	// Split on CREATE TABLE boundaries
	statements := splitStatements(sql)

	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		table, err := parseSingleCreate(stmt)
		if err != nil {
			return nil, fmt.Errorf("parse DDL: %w", err)
		}
		if table != nil {
			schema.Tables = append(schema.Tables, table)
		}
	}

	if len(schema.Tables) == 0 {
		return nil, fmt.Errorf("no CREATE TABLE statements found")
	}

	// Resolve FK hints across tables
	tableNames := make([]string, len(schema.Tables))
	for i, t := range schema.Tables {
		tableNames[i] = t.Name
	}
	for _, t := range schema.Tables {
		for i := range t.Columns {
			col := &t.Columns[i]
			if col.Hint == HintNone {
				col.Hint = InferHint(col.Name, col.Type, tableNames)
			}
			// Auto-detect FK from _id suffix
			if !col.IsFK && col.Hint == HintForeignKey && strings.HasSuffix(strings.ToLower(col.Name), "_id") {
				prefix := strings.TrimSuffix(strings.ToLower(col.Name), "_id")
				for _, tn := range tableNames {
					lower := strings.ToLower(tn)
					if lower == prefix || lower == prefix+"s" ||
						strings.TrimSuffix(lower, "s") == prefix ||
						strings.TrimSuffix(lower, "es") == prefix {
						col.IsFK = true
						col.FKTable = tn
						col.FKColumn = "id"
						break
					}
				}
			}
		}
	}

	return schema, nil
}

// splitStatements splits SQL text on CREATE TABLE boundaries.
func splitStatements(sql string) []string {
	// Find all CREATE TABLE positions
	locs := reCreateTable.FindAllStringIndex(sql, -1)
	if len(locs) == 0 {
		return []string{sql}
	}

	var stmts []string
	for i, loc := range locs {
		start := loc[0]
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			end = len(sql)
		}
		stmts = append(stmts, sql[start:end])
	}
	return stmts
}

// parseSingleCreate parses one CREATE TABLE statement.
func parseSingleCreate(stmt string) (*TableDef, error) {
	// Extract table name
	match := reCreateTable.FindStringSubmatch(stmt)
	if match == nil {
		return nil, nil // not a CREATE TABLE
	}
	tableName := unquote(match[1])

	// Extract everything between the outer parentheses
	bodyStart := strings.Index(stmt, "(")
	if bodyStart < 0 {
		return nil, fmt.Errorf("missing opening parenthesis in CREATE TABLE %s", tableName)
	}

	// Find matching closing parenthesis
	body := extractParenBody(stmt[bodyStart:])
	if body == "" {
		return nil, fmt.Errorf("missing closing parenthesis in CREATE TABLE %s", tableName)
	}

	// Split body into column/constraint definitions
	parts := splitColumnDefs(body)

	table := &TableDef{
		Name:     tableName,
		Schema:   "public",
		Owner:    "sensor_writer",
		RowCount: 100, // default
	}

	// Track constraints to apply after column parsing
	var fkConstraints []fkConstraintDef
	var pkColumn string

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Check for table-level constraints
		if reConstraint.MatchString(part) {
			// FK constraint
			if m := reFKConstraint.FindStringSubmatch(part); m != nil {
				fkConstraints = append(fkConstraints, fkConstraintDef{
					column:   unquote(m[1]),
					refTable: unquote(m[2]),
					refCol:   unquote(m[3]),
				})
			}
			// PK constraint
			if m := rePKConstraint.FindStringSubmatch(part); m != nil {
				pkColumn = unquote(m[1])
			}
			continue
		}

		// Parse column definition
		col, err := parseColumnDef(part)
		if err != nil {
			continue // skip unparseable lines
		}
		if col != nil {
			table.Columns = append(table.Columns, *col)
		}
	}

	// Apply table-level constraints
	for i := range table.Columns {
		if table.Columns[i].Name == pkColumn {
			table.Columns[i].IsPK = true
		}
		for _, fk := range fkConstraints {
			if table.Columns[i].Name == fk.column {
				table.Columns[i].IsFK = true
				table.Columns[i].FKTable = fk.refTable
				table.Columns[i].FKColumn = fk.refCol
			}
		}
	}

	if len(table.Columns) == 0 {
		return nil, fmt.Errorf("no columns found in CREATE TABLE %s", tableName)
	}

	return table, nil
}

type fkConstraintDef struct {
	column   string
	refTable string
	refCol   string
}

// parseColumnDef parses a single column definition.
func parseColumnDef(def string) (*SchemColumn, error) {
	m := reColumnDef.FindStringSubmatch(def)
	if m == nil {
		return nil, fmt.Errorf("cannot parse column: %s", def)
	}

	name := unquote(m[1])
	typeStr := strings.TrimSpace(m[2])
	rest := m[3]

	colType, pgOID, typeSize, typeMod := mapSQLType(typeStr)

	col := &SchemColumn{
		Name:     name,
		Type:     colType,
		PgOID:    pgOID,
		TypeSize: typeSize,
		TypeMod:  typeMod,
		Nullable: true,
	}

	// Check inline modifiers
	full := typeStr + " " + rest
	if reInlinePK.MatchString(full) {
		col.IsPK = true
		col.Nullable = false
	}
	if reInlineNotNull.MatchString(full) {
		col.Nullable = false
	}
	if m := reInlineFK.FindStringSubmatch(full); m != nil {
		col.IsFK = true
		col.FKTable = unquote(m[1])
		col.FKColumn = unquote(m[2])
	}

	return col, nil
}

// mapSQLType maps a SQL type string to internal types.
func mapSQLType(sqlType string) (ColumnType, int32, int16, int32) {
	upper := strings.ToUpper(strings.TrimSpace(sqlType))

	// Remove extra modifiers after the type (NOT NULL, DEFAULT, etc.)
	// Only keep the type portion
	for _, kw := range []string{" NOT ", " NULL", " DEFAULT ", " PRIMARY ", " REFERENCES ", " UNIQUE", " CHECK "} {
		if idx := strings.Index(upper, kw); idx >= 0 {
			upper = upper[:idx]
		}
	}
	upper = strings.TrimSpace(upper)

	switch {
	case upper == "SERIAL" || upper == "SERIAL4":
		return ColTypeSerial, pgwire.OIDInt4, 4, -1
	case upper == "BIGSERIAL" || upper == "SERIAL8":
		return ColTypeSerial, pgwire.OIDInt8, 8, -1
	case upper == "INTEGER" || upper == "INT" || upper == "INT4":
		return ColTypeInt, pgwire.OIDInt4, 4, -1
	case upper == "SMALLINT" || upper == "INT2":
		return ColTypeInt, pgwire.OIDInt4, 4, -1
	case upper == "BIGINT" || upper == "INT8":
		return ColTypeBigInt, pgwire.OIDInt8, 8, -1
	case upper == "BOOLEAN" || upper == "BOOL":
		return ColTypeBool, pgwire.OIDBool, 1, -1
	case upper == "TEXT":
		return ColTypeText, pgwire.OIDText, -1, -1
	case strings.HasPrefix(upper, "VARCHAR") || strings.HasPrefix(upper, "CHARACTER VARYING"):
		typeMod := int32(-1)
		if m := reVarcharLen.FindStringSubmatch(sqlType); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				typeMod = int32(n + 4) // pg convention
			}
		}
		return ColTypeText, pgwire.OIDVarchar, -1, typeMod
	case strings.HasPrefix(upper, "CHAR(") || upper == "CHAR":
		return ColTypeText, pgwire.OIDVarchar, -1, -1
	case strings.HasPrefix(upper, "NUMERIC") || strings.HasPrefix(upper, "DECIMAL"):
		return ColTypeFloat, pgwire.OIDNumeric, -1, -1
	case upper == "REAL" || upper == "FLOAT4":
		return ColTypeFloat, pgwire.OIDFloat8, 8, -1
	case upper == "DOUBLE PRECISION" || upper == "FLOAT8" || upper == "FLOAT":
		return ColTypeFloat, pgwire.OIDFloat8, 8, -1
	case upper == "TIMESTAMP" || upper == "TIMESTAMPTZ" ||
		upper == "TIMESTAMP WITH TIME ZONE" || upper == "TIMESTAMP WITHOUT TIME ZONE":
		return ColTypeTimestamp, pgwire.OIDTimestampTZ, 8, -1
	case upper == "DATE":
		return ColTypeTimestamp, pgwire.OIDTimestampTZ, 8, -1
	case upper == "UUID":
		return ColTypeUUID, pgwire.OIDUUID, 16, -1
	case upper == "JSONB":
		return ColTypeJSON, pgwire.OIDJSONB, -1, -1
	case upper == "JSON":
		return ColTypeJSON, pgwire.OIDJSONB, -1, -1
	case upper == "BYTEA":
		return ColTypeBytea, pgwire.OIDBytea, -1, -1
	default:
		return ColTypeText, pgwire.OIDText, -1, -1
	}
}

// extractParenBody extracts the content between matching parentheses.
func extractParenBody(s string) string {
	if len(s) == 0 || s[0] != '(' {
		return ""
	}
	depth := 0
	for i, ch := range s {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i]
			}
		}
	}
	return ""
}

// splitColumnDefs splits column definitions by commas, respecting parentheses.
func splitColumnDefs(body string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, ch := range body {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	if start < len(body) {
		parts = append(parts, body[start:])
	}
	return parts
}

// unquote removes surrounding double quotes from an identifier.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
