package fakedata

import (
	"regexp"
	"strconv"
	"strings"
)

// QueryType identifies SQL statement type.
type QueryType int

const (
	QuerySELECT QueryType = iota
	QueryINSERT
	QueryUPDATE
	QueryDELETE
	QueryMETA
	QueryDESTRUCTIVE
	QueryUNKNOWN
)

// ParsedQuery holds parsed SQL query information.
type ParsedQuery struct {
	Type       QueryType
	Table      string
	Where      map[string]string
	Limit      int
	OrderBy    string
	IsCount    bool
	IsJoin     bool
	IsMeta     bool
	Raw        string
	Columns    []string
	SetClauses map[string]string
}

var (
	reTable     = regexp.MustCompile(`(?i)(?:FROM|INTO|UPDATE|TABLE)\s+([a-z_][a-z0-9_]*)`)
	reWhere     = regexp.MustCompile(`(?i)WHERE\s+(.+?)(?:\s+ORDER|\s+LIMIT|\s+GROUP|\s*;?\s*$)`)
	reWhereKV   = regexp.MustCompile(`(?i)([a-z_][a-z0-9_.]*)\s*=\s*(?:'([^']*)'|(\d+))`)
	reLimit     = regexp.MustCompile(`(?i)LIMIT\s+(\d+)`)
	reOrderBy   = regexp.MustCompile(`(?i)ORDER\s+BY\s+([a-z_][a-z0-9_.]*(?:\s+(?:ASC|DESC))?)`)
	reCount     = regexp.MustCompile(`(?i)SELECT\s+count\s*\(\s*\*\s*\)`)
	reJoin      = regexp.MustCompile(`(?i)\s+JOIN\s+`)
	reDestruct  = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE|ALTER)\s+`)
	reSet       = regexp.MustCompile(`(?i)SET\s+(.+?)\s+WHERE`)
	reSetKV     = regexp.MustCompile(`(?i)([a-z_][a-z0-9_.]*)\s*=\s*(?:'([^']*)'|(\d+))`)
	reColumns   = regexp.MustCompile(`(?i)SELECT\s+(.+?)\s+FROM`)
)

// metaPatterns identifies psql/DBeaver metadata queries.
var metaPatterns = []string{
	"pg_catalog",
	"information_schema",
	"pg_type",
	"pg_namespace",
	"pg_class",
	"pg_attribute",
	"pg_constraint",
	"pg_proc",
	"pg_database",
	"SHOW ",
	"SELECT version()",
	"select version()",
	"SET ",
	"set ",
	"EXPLAIN ",
	"explain ",
}

// transactionKeywords identifies transaction control statements (detected separately from metaPatterns).
var transactionKeywords = []string{
	"BEGIN",
	"COMMIT",
	"ROLLBACK",
	"START TRANSACTION",
	"END",
}

// ParseSQL parses a SQL query into structured information.
func ParseSQL(query string) *ParsedQuery {
	pq := &ParsedQuery{
		Raw:   query,
		Where: make(map[string]string),
	}

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	// Detect destructive queries
	if reDestruct.MatchString(trimmed) {
		pq.Type = QueryDESTRUCTIVE
		if m := reTable.FindStringSubmatch(trimmed); m != nil {
			pq.Table = strings.ToLower(m[1])
		}
		return pq
	}

	// Detect query type FIRST (before meta check, since UPDATE contains SET)
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		pq.Type = QuerySELECT
	case strings.HasPrefix(upper, "INSERT"):
		pq.Type = QueryINSERT
	case strings.HasPrefix(upper, "UPDATE"):
		pq.Type = QueryUPDATE
	case strings.HasPrefix(upper, "DELETE"):
		pq.Type = QueryDELETE
	default:
		// Only check meta for non-DML statements
		if isMetaQuery(trimmed) {
			pq.Type = QueryMETA
			pq.IsMeta = true
			pq.Raw = trimmed
			return pq
		}
		pq.Type = QueryUNKNOWN
		return pq
	}

	// Check meta for SELECT statements (e.g. SELECT version(), pg_catalog queries)
	if pq.Type == QuerySELECT && isMetaQuery(trimmed) {
		pq.Type = QueryMETA
		pq.IsMeta = true
		return pq
	}

	// Extract table
	if m := reTable.FindStringSubmatch(trimmed); m != nil {
		pq.Table = strings.ToLower(m[1])
	}

	// Extract WHERE conditions
	if m := reWhere.FindStringSubmatch(trimmed); m != nil {
		kvs := reWhereKV.FindAllStringSubmatch(m[1], -1)
		for _, kv := range kvs {
			key := strings.ToLower(kv[1])
			val := kv[2]
			if val == "" {
				val = kv[3]
			}
			pq.Where[key] = val
		}
	}

	// Extract LIMIT
	if m := reLimit.FindStringSubmatch(trimmed); m != nil {
		pq.Limit, _ = strconv.Atoi(m[1])
	}

	// Extract ORDER BY
	if m := reOrderBy.FindStringSubmatch(trimmed); m != nil {
		pq.OrderBy = m[1]
	}

	// Detect COUNT(*)
	pq.IsCount = reCount.MatchString(trimmed)

	// Detect JOIN
	pq.IsJoin = reJoin.MatchString(trimmed)

	// Extract SELECT columns
	if pq.Type == QuerySELECT && !pq.IsCount {
		if m := reColumns.FindStringSubmatch(trimmed); m != nil {
			cols := strings.Split(m[1], ",")
			for _, c := range cols {
				c = strings.TrimSpace(c)
				if c != "*" && c != "" {
					pq.Columns = append(pq.Columns, c)
				}
			}
		}
	}

	// Extract SET clauses for UPDATE
	if pq.Type == QueryUPDATE {
		pq.SetClauses = make(map[string]string)
		if m := reSet.FindStringSubmatch(trimmed); m != nil {
			kvs := reSetKV.FindAllStringSubmatch(m[1], -1)
			for _, kv := range kvs {
				key := strings.ToLower(kv[1])
				val := kv[2]
				if val == "" {
					val = kv[3]
				}
				pq.SetClauses[key] = val
			}
		}
	}

	return pq
}

func isMetaQuery(q string) bool {
	for _, pat := range metaPatterns {
		if strings.Contains(q, pat) {
			return true
		}
	}
	// Check transaction control statements
	upper := strings.ToUpper(strings.TrimSpace(q))
	for _, kw := range transactionKeywords {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}
