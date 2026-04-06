package fakedata

import (
	"regexp"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// oidToTypeName converts a PostgreSQL OID to a human-readable type name.
func oidToTypeName(oid int32) string {
	switch oid {
	case pgwire.OIDInt4:
		return "integer"
	case pgwire.OIDInt8:
		return "bigint"
	case pgwire.OIDFloat8:
		return "double precision"
	case pgwire.OIDText:
		return "text"
	case pgwire.OIDVarchar:
		return "character varying(255)"
	case pgwire.OIDTimestampTZ:
		return "timestamp with time zone"
	case pgwire.OIDBool:
		return "boolean"
	case pgwire.OIDNumeric:
		return "numeric"
	case pgwire.OIDBytea:
		return "bytea"
	case pgwire.OIDJSONB:
		return "jsonb"
	case pgwire.OIDUUID:
		return "uuid"
	default:
		return "text"
	}
}

// --- Meta query detection helpers (used by genericengine.go) ---

func isListTablesQuery(q string) bool {
	return contains(q, "pg_catalog.pg_class") && contains(q, "relkind") ||
		contains(q, "information_schema.tables")
}

// reExtractTable extracts table name from pg_attribute+pg_class queries used by \d <table>.
// Matches patterns like: relname = 'tablename' or relname='tablename'
var reExtractRelname = regexp.MustCompile(`(?i)relname\s*=\s*'([^']+)'`)

func extractDescribeTable(q string) string {
	if contains(q, "pg_catalog.pg_attribute") && contains(q, "pg_catalog.pg_class") {
		if m := reExtractRelname.FindStringSubmatch(q); m != nil {
			return m[1]
		}
	}
	return ""
}

func isVersionQuery(q string) bool {
	return contains(q, "version()") || contains(q, "VERSION()")
}

func isShowServerVersion(q string) bool {
	return contains(q, "SHOW server_version") || contains(q, "show server_version")
}

func isShowTimezone(q string) bool {
	return contains(q, "SHOW timezone") || contains(q, "show timezone") ||
		contains(q, "SHOW TimeZone") || contains(q, "show TimeZone")
}

func isSetQuery(q string) bool {
	upper := toUpper(q)
	return hasPrefix(upper, "SET ")
}

func isListenQuery(q string) bool {
	upper := toUpper(q)
	return hasPrefix(upper, "LISTEN ")
}

// --- Task 45: Additional catalog query detectors ---

func isPgTypeQuery(q string) bool {
	return contains(q, "pg_catalog.pg_type") || contains(q, "pg_type")
}

func isPgNamespaceQuery(q string) bool {
	return contains(q, "pg_catalog.pg_namespace") || contains(q, "pg_namespace")
}

func isPgDatabaseQuery(q string) bool {
	// Match "pg_catalog.pg_database" or "FROM pg_database" but not "pg_database_size" etc.
	return contains(q, "pg_catalog.pg_database") ||
		(contains(q, "pg_database") && !contains(q, "pg_database_"))
}

func isInformationSchemaColumnsQuery(q string) bool {
	return contains(q, "information_schema.columns")
}

// isPgAttributeClassQuery detects combined pg_attribute + pg_class queries (psql \d <table>).
func isPgAttributeClassQuery(q string) bool {
	return contains(q, "pg_catalog.pg_attribute") && contains(q, "pg_catalog.pg_class")
}

// --- Task 46: EXPLAIN and transaction detection ---

func isExplainQuery(q string) bool {
	upper := toUpper(q)
	return hasPrefix(upper, "EXPLAIN ")
}

func isTransactionQuery(q string) bool {
	upper := toUpper(q)
	return hasPrefix(upper, "BEGIN") ||
		hasPrefix(upper, "COMMIT") ||
		hasPrefix(upper, "ROLLBACK") ||
		hasPrefix(upper, "START TRANSACTION") ||
		hasPrefix(upper, "END")
}

func extractTransactionTag(q string) string {
	upper := toUpper(q)
	switch {
	case hasPrefix(upper, "BEGIN"), hasPrefix(upper, "START TRANSACTION"):
		return "BEGIN"
	case hasPrefix(upper, "COMMIT"), hasPrefix(upper, "END"):
		return "COMMIT"
	case hasPrefix(upper, "ROLLBACK"):
		return "ROLLBACK"
	default:
		return "COMMAND OK"
	}
}

// --- String helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toUpper(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	return string(b)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
