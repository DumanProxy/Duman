package fakedata

import "github.com/dumanproxy/duman/internal/pgwire"

// ColumnType represents the semantic data type for schema columns.
type ColumnType int

const (
	ColTypeSerial    ColumnType = iota // auto-increment integer PK
	ColTypeInt                         // plain integer (int4)
	ColTypeBigInt                      // bigint (int8)
	ColTypeFloat                       // numeric/float/decimal
	ColTypeText                        // text/varchar
	ColTypeBool                        // boolean
	ColTypeTimestamp                    // timestamp with time zone
	ColTypeUUID                        // uuid
	ColTypeJSON                        // jsonb
	ColTypeBytea                       // bytea (binary)
	ColTypeEnum                        // restricted set of string values
)

// ColumnHint tells the data generator what kind of values to produce.
type ColumnHint int

const (
	HintNone        ColumnHint = iota
	HintName                   // person name (first + last)
	HintFirstName              // first name only
	HintLastName               // last name only
	HintEmail                  // email address
	HintPrice                  // monetary value
	HintQuantity               // small positive integer (1-100)
	HintStatus                 // enum-like status field
	HintURL                    // url string
	HintDate                   // date-only or timestamp
	HintRating                 // 1-5 rating
	HintTitle                  // title/heading text
	HintDescription            // longer text paragraph
	HintAddress                // street address
	HintPhone                  // phone number
	HintCountry                // country name
	HintColor                  // color name
	HintSlug                   // url-safe identifier
	HintForeignKey             // FK reference to another table
	HintProductName            // realistic product name
	HintCompanyName            // company/org name
	HintUsername               // username string
	HintIPAddress              // IP address
	HintUserAgent              // browser user agent
	HintJSONMeta               // JSON metadata object
	HintPageURL                // page URL path
	HintEventType              // analytics event type
)

// SchemColumn defines a single column in a table schema.
type SchemColumn struct {
	Name       string
	Type       ColumnType
	PgOID      int32
	TypeSize   int16
	TypeMod    int32
	Nullable   bool
	IsPK       bool
	IsFK       bool
	FKTable    string
	FKColumn   string
	Hint       ColumnHint
	EnumValues []string // for ColTypeEnum or HintStatus
	MinVal     float64  // for numeric range generation
	MaxVal     float64  // for numeric range generation
}

// ToPgColumnDef converts to pgwire.ColumnDef for wire protocol responses.
func (c *SchemColumn) ToPgColumnDef() pgwire.ColumnDef {
	return pgwire.ColumnDef{
		Name:     c.Name,
		OID:      c.PgOID,
		TypeSize: c.TypeSize,
		TypeMod:  c.TypeMod,
	}
}

// TableDef defines a single table in the schema.
type TableDef struct {
	Name     string
	Schema   string // "public"
	Owner    string // for \dt metadata
	Columns  []SchemColumn
	RowCount int  // how many rows to generate
	IsTunnel bool // true for analytics_events/analytics_responses (infrastructure)
}

// ColumnIndex returns the index of the named column, or -1.
func (t *TableDef) ColumnIndex(name string) int {
	for i, c := range t.Columns {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// PgColumns returns pgwire.ColumnDef slice for wire protocol.
func (t *TableDef) PgColumns() []pgwire.ColumnDef {
	cols := make([]pgwire.ColumnDef, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = c.ToPgColumnDef()
	}
	return cols
}

// SchemaDefinition holds the complete database schema.
type SchemaDefinition struct {
	Tables       []*TableDef
	Seed         int64
	ScenarioName string // "ecommerce", "iot", "saas", "blog", "project", "random", "custom"
	Mutations    *MutationSpec
}

// TableByName returns the TableDef with the given name, or nil.
func (s *SchemaDefinition) TableByName(name string) *TableDef {
	for _, t := range s.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// UserTables returns non-tunnel tables (for cover query generation).
func (s *SchemaDefinition) UserTables() []*TableDef {
	var tables []*TableDef
	for _, t := range s.Tables {
		if !t.IsTunnel {
			tables = append(tables, t)
		}
	}
	return tables
}

// MutationSpec describes how a built-in template was mutated.
type MutationSpec struct {
	TableRenames  map[string]string            // original → mutated name
	ColumnRenames map[string]map[string]string // table → { original → mutated }
	ExtraColumns  map[string][]SchemColumn     // table → extra columns added
}

// SchemaBuilder constructs a SchemaDefinition from various sources.
type SchemaBuilder interface {
	Build() (*SchemaDefinition, error)
}

// TunnelInfrastructureTables returns the fixed analytics_events/analytics_responses tables.
// These are always present in every schema and never mutated.
func TunnelInfrastructureTables(owner string) []*TableDef {
	if owner == "" {
		owner = "sensor_writer"
	}
	return []*TableDef{
		{
			Name: "analytics_events", Schema: "public", Owner: owner, IsTunnel: true,
			RowCount: 0,
			Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "session_id", Type: ColTypeUUID, PgOID: pgwire.OIDUUID, TypeSize: 16, TypeMod: -1, Hint: HintNone},
				{Name: "event_type", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEventType},
				{Name: "page_url", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintPageURL},
				{Name: "user_agent", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintUserAgent},
				{Name: "metadata", Type: ColTypeJSON, PgOID: pgwire.OIDJSONB, TypeSize: -1, TypeMod: -1, Hint: HintJSONMeta},
				{Name: "payload", Type: ColTypeBytea, PgOID: pgwire.OIDBytea, TypeSize: -1, TypeMod: -1},
			},
		},
		{
			Name: "analytics_responses", Schema: "public", Owner: owner, IsTunnel: true,
			RowCount: 0,
			Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "session_id", Type: ColTypeUUID, PgOID: pgwire.OIDUUID, TypeSize: 16, TypeMod: -1},
				{Name: "seq", Type: ColTypeBigInt, PgOID: pgwire.OIDInt8, TypeSize: 8, TypeMod: -1},
				{Name: "payload", Type: ColTypeBytea, PgOID: pgwire.OIDBytea, TypeSize: -1, TypeMod: -1},
				{Name: "consumed", Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1},
			},
		},
	}
}
