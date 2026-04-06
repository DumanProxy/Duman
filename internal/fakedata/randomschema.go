package fakedata

import (
	"fmt"
	"math/rand"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// RandomSchemaBuilder generates a completely unique schema from a seed.
type RandomSchemaBuilder struct {
	seed int64
}

// NewRandomSchemaBuilder creates a builder for random schema generation.
func NewRandomSchemaBuilder(seed int64) *RandomSchemaBuilder {
	return &RandomSchemaBuilder{seed: seed}
}

// Build generates a unique SchemaDefinition.
func (b *RandomSchemaBuilder) Build() (*SchemaDefinition, error) {
	rng := rand.New(rand.NewSource(b.seed))

	// Pick a domain
	domains := []string{"retail", "crm", "analytics", "logistics", "education", "healthcare", "finance", "social"}
	domain := domains[rng.Intn(len(domains))]

	tables := domainTablePool[domain]
	if tables == nil {
		tables = domainTablePool["retail"] // fallback
	}

	// Pick 4-8 tables
	tableCount := 4 + rng.Intn(5)
	if tableCount > len(tables) {
		tableCount = len(tables)
	}

	// Shuffle and pick
	perm := rng.Perm(len(tables))
	var selectedDefs []*randomTableDef
	for i := 0; i < tableCount; i++ {
		selectedDefs = append(selectedDefs, tables[perm[i]])
	}

	schema := &SchemaDefinition{
		ScenarioName: "random",
		Seed:         b.seed,
	}

	// Track table names for FK resolution
	tableNames := make([]string, len(selectedDefs))
	for i, td := range selectedDefs {
		tableNames[i] = td.name
	}

	// Generate table definitions
	for _, td := range selectedDefs {
		tableDef := td.toTableDef(rng, tableNames)
		schema.Tables = append(schema.Tables, tableDef)
	}

	// Add tunnel infrastructure
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)

	return schema, nil
}

// randomTableDef is a template for random table generation.
type randomTableDef struct {
	name     string
	columns  []randomColumnDef
	rowCount [2]int // min, max
}

type randomColumnDef struct {
	name     string
	colType  ColumnType
	pgOID    int32
	typeSize int16
	typeMod  int32
	hint     ColumnHint
	isPK     bool
	fkPrefix string   // if non-empty, FK to table matching this prefix
	enumVals []string // for status/enum columns
	minVal   float64
	maxVal   float64
}

func (td *randomTableDef) toTableDef(rng *rand.Rand, allTables []string) *TableDef {
	rowCount := td.rowCount[0] + rng.Intn(td.rowCount[1]-td.rowCount[0]+1)

	table := &TableDef{
		Name:     td.name,
		Schema:   "public",
		Owner:    "app_user",
		RowCount: rowCount,
	}

	for _, cd := range td.columns {
		col := SchemColumn{
			Name:       cd.name,
			Type:       cd.colType,
			PgOID:      cd.pgOID,
			TypeSize:   cd.typeSize,
			TypeMod:    cd.typeMod,
			IsPK:       cd.isPK,
			Hint:       cd.hint,
			EnumValues: cd.enumVals,
			MinVal:     cd.minVal,
			MaxVal:     cd.maxVal,
			Nullable:   !cd.isPK,
		}

		// Resolve FK
		if cd.fkPrefix != "" {
			for _, tn := range allTables {
				if tn == cd.fkPrefix || tn == cd.fkPrefix+"s" {
					col.IsFK = true
					col.FKTable = tn
					col.FKColumn = "id"
					break
				}
			}
		}

		table.Columns = append(table.Columns, col)
	}

	// Optionally add 0-2 extra random columns
	extras := rng.Intn(3)
	for i := 0; i < extras; i++ {
		col := randomExtraColumn(rng, i)
		table.Columns = append(table.Columns, col)
	}

	return table
}

func randomExtraColumn(rng *rand.Rand, idx int) SchemColumn {
	choices := []SchemColumn{
		{Name: fmt.Sprintf("notes_%d", idx), Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintDescription},
		{Name: fmt.Sprintf("metadata_%d", idx), Type: ColTypeJSON, PgOID: pgwire.OIDJSONB, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintJSONMeta},
		{Name: fmt.Sprintf("is_active_%d", idx), Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1, Nullable: true},
		{Name: fmt.Sprintf("score_%d", idx), Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Nullable: true, Hint: HintRating},
		{Name: fmt.Sprintf("updated_at_%d", idx), Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Nullable: true, Hint: HintDate},
	}
	return choices[rng.Intn(len(choices))]
}

// --- Domain Table Pools ---

var domainTablePool = map[string][]*randomTableDef{
	"retail": {
		{name: "products", rowCount: [2]int{100, 500}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintProductName},
			{name: "price", colType: ColTypeFloat, pgOID: pgwire.OIDNumeric, typeSize: -1, typeMod: -1, hint: HintPrice, minVal: 1, maxVal: 5000},
			{name: "category_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "categories"},
			{name: "stock", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, hint: HintQuantity},
		}},
		{name: "categories", rowCount: [2]int{5, 20}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintTitle},
		}},
		{name: "customers", rowCount: [2]int{50, 300}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintName},
			{name: "email", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintEmail},
			{name: "phone", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintPhone},
		}},
		{name: "orders", rowCount: [2]int{50, 500}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "customer_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "customer"},
			{name: "total", colType: ColTypeFloat, pgOID: pgwire.OIDNumeric, typeSize: -1, typeMod: -1, hint: HintPrice, minVal: 5, maxVal: 2000},
			{name: "status", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintStatus,
				enumVals: []string{"pending", "confirmed", "shipped", "delivered", "returned"}},
			{name: "created_at", colType: ColTypeTimestamp, pgOID: pgwire.OIDTimestampTZ, typeSize: 8, typeMod: -1, hint: HintDate},
		}},
		{name: "reviews", rowCount: [2]int{50, 300}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "product_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "product"},
			{name: "rating", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, hint: HintRating},
			{name: "comment", colType: ColTypeText, pgOID: pgwire.OIDText, typeSize: -1, typeMod: -1, hint: HintDescription},
		}},
		{name: "coupons", rowCount: [2]int{10, 50}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "code", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintSlug},
			{name: "discount", colType: ColTypeFloat, pgOID: pgwire.OIDNumeric, typeSize: -1, typeMod: -1, minVal: 5, maxVal: 50},
			{name: "expires_at", colType: ColTypeTimestamp, pgOID: pgwire.OIDTimestampTZ, typeSize: 8, typeMod: -1, hint: HintDate},
		}},
		{name: "suppliers", rowCount: [2]int{10, 30}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintCompanyName},
			{name: "contact_email", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintEmail},
			{name: "country", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintCountry},
		}},
	},
	"crm": {
		{name: "contacts", rowCount: [2]int{100, 500}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintName},
			{name: "email", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintEmail},
			{name: "phone", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintPhone},
			{name: "company_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "companie"},
		}},
		{name: "companies", rowCount: [2]int{20, 100}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintCompanyName},
			{name: "industry", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintTitle},
			{name: "country", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintCountry},
		}},
		{name: "deals", rowCount: [2]int{50, 300}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "contact_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "contact"},
			{name: "title", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintTitle},
			{name: "value", colType: ColTypeFloat, pgOID: pgwire.OIDNumeric, typeSize: -1, typeMod: -1, hint: HintPrice, minVal: 100, maxVal: 100000},
			{name: "stage", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintStatus,
				enumVals: []string{"lead", "qualified", "proposal", "negotiation", "won", "lost"}},
		}},
		{name: "activities", rowCount: [2]int{100, 500}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "contact_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "contact"},
			{name: "type", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintStatus,
				enumVals: []string{"call", "email", "meeting", "note", "task"}},
			{name: "description", colType: ColTypeText, pgOID: pgwire.OIDText, typeSize: -1, typeMod: -1, hint: HintDescription},
			{name: "created_at", colType: ColTypeTimestamp, pgOID: pgwire.OIDTimestampTZ, typeSize: 8, typeMod: -1, hint: HintDate},
		}},
		{name: "pipelines", rowCount: [2]int{3, 10}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "name", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintTitle},
			{name: "is_default", colType: ColTypeBool, pgOID: pgwire.OIDBool, typeSize: 1, typeMod: -1},
		}},
		{name: "tasks", rowCount: [2]int{50, 200}, columns: []randomColumnDef{
			{name: "id", colType: ColTypeSerial, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, isPK: true},
			{name: "contact_id", colType: ColTypeInt, pgOID: pgwire.OIDInt4, typeSize: 4, typeMod: -1, fkPrefix: "contact"},
			{name: "title", colType: ColTypeText, pgOID: pgwire.OIDVarchar, typeSize: -1, typeMod: 259, hint: HintTitle},
			{name: "due_date", colType: ColTypeTimestamp, pgOID: pgwire.OIDTimestampTZ, typeSize: 8, typeMod: -1, hint: HintDate},
			{name: "completed", colType: ColTypeBool, pgOID: pgwire.OIDBool, typeSize: 1, typeMod: -1},
		}},
	},
	// Other domains use retail as fallback for now; more can be added later
	"analytics":  nil,
	"logistics":  nil,
	"education":  nil,
	"healthcare": nil,
	"finance":    nil,
	"social":     nil,
}
