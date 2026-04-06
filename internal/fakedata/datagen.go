package fakedata

import (
	"math/rand"
	"strings"
)

// DataGenerator generates realistic data for any schema.
type DataGenerator struct {
	rng       *rand.Rand
	schema    *SchemaDefinition
	rowCounts map[string]int // table → actual generated row count (for FK lookups)
}

// NewDataGenerator creates a generator for the given schema.
func NewDataGenerator(schema *SchemaDefinition, seed int64) *DataGenerator {
	return &DataGenerator{
		rng:       rand.New(rand.NewSource(seed)),
		schema:    schema,
		rowCounts: make(map[string]int),
	}
}

// GenerateAll populates a GenericStore from the schema.
// Tables are generated in dependency order (FK targets first).
func (g *DataGenerator) GenerateAll() *GenericStore {
	store := NewGenericStore(g.schema)

	// Build generation order: tables without FKs first, then tables with FKs
	order := g.topologicalOrder()

	for _, table := range order {
		if table.IsTunnel {
			// Tunnel tables get no data rows, just schema
			g.rowCounts[table.Name] = 0
			continue
		}
		rows := make([][][]byte, table.RowCount)
		for i := 0; i < table.RowCount; i++ {
			rows[i] = g.generateRow(table, i)
		}
		g.rowCounts[table.Name] = table.RowCount
		store.LoadTable(table, rows)
	}

	store.BuildIndices()
	return store
}

// topologicalOrder returns tables sorted so FK targets come before referencing tables.
func (g *DataGenerator) topologicalOrder() []*TableDef {
	visited := make(map[string]bool)
	var result []*TableDef

	tableMap := make(map[string]*TableDef)
	for _, t := range g.schema.Tables {
		tableMap[t.Name] = t
	}

	var visit func(t *TableDef)
	visit = func(t *TableDef) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true

		// Visit FK dependencies first
		for _, col := range t.Columns {
			if col.IsFK && col.FKTable != "" {
				if dep, ok := tableMap[col.FKTable]; ok {
					visit(dep)
				}
			}
		}
		result = append(result, t)
	}

	for _, t := range g.schema.Tables {
		visit(t)
	}
	return result
}

// generateRow generates a single row for a table.
func (g *DataGenerator) generateRow(table *TableDef, rowIndex int) [][]byte {
	row := make([][]byte, len(table.Columns))
	for i, col := range table.Columns {
		row[i] = g.generateValue(&col, table, rowIndex)
	}
	return row
}

// generateValue generates a single column value based on hint and type.
func (g *DataGenerator) generateValue(col *SchemColumn, table *TableDef, rowIndex int) []byte {
	// Primary key serial
	if col.IsPK && (col.Type == ColTypeSerial || col.Type == ColTypeInt) {
		return GenSerial(rowIndex)
	}

	// Foreign key
	if col.IsFK && col.FKTable != "" {
		maxID := g.rowCounts[col.FKTable]
		if maxID <= 0 {
			// FK target not yet generated, use RowCount from schema
			if t := g.schema.TableByName(col.FKTable); t != nil {
				maxID = t.RowCount
			}
			if maxID <= 0 {
				maxID = 10
			}
		}
		return GenForeignKey(g.rng, maxID)
	}

	// Use hint if available
	switch col.Hint {
	case HintName:
		return GenName(g.rng)
	case HintFirstName:
		return GenFirstName(g.rng)
	case HintLastName:
		return GenLastName(g.rng)
	case HintEmail:
		return GenEmail(g.rng, rowIndex)
	case HintUsername:
		return GenUsername(g.rng, rowIndex)
	case HintPrice:
		return GenPrice(g.rng, col.MinVal, col.MaxVal)
	case HintQuantity:
		return GenQuantity(g.rng)
	case HintStatus:
		return GenStatus(g.rng, col.EnumValues)
	case HintRating:
		return GenRating(g.rng)
	case HintTitle:
		return GenTitle(g.rng)
	case HintDescription:
		return GenDescription(g.rng)
	case HintProductName:
		// Try to find category context
		category := g.guessCategoryForRow(table, rowIndex)
		return GenProductName(g.rng, category)
	case HintCompanyName:
		return GenCompanyName(g.rng)
	case HintURL, HintPageURL:
		return GenURL(g.rng)
	case HintAddress:
		return GenAddress(g.rng)
	case HintPhone:
		return GenPhone(g.rng)
	case HintCountry:
		return GenCountry(g.rng)
	case HintColor:
		return GenColor(g.rng)
	case HintSlug:
		return GenSlug(g.rng)
	case HintIPAddress:
		return GenIPAddress(g.rng)
	case HintUserAgent:
		return GenUserAgent(g.rng)
	case HintEventType:
		return GenEventType(g.rng)
	case HintJSONMeta:
		return GenJSONMeta(g.rng)
	case HintDate:
		return GenDate(g.rng)
	}

	// Fall back to type-based generation
	return g.generateByType(col)
}

// generateByType generates a value based on column type alone.
func (g *DataGenerator) generateByType(col *SchemColumn) []byte {
	switch col.Type {
	case ColTypeSerial, ColTypeInt:
		min, max := 1, 10000
		if col.MinVal > 0 || col.MaxVal > 0 {
			min = int(col.MinVal)
			max = int(col.MaxVal)
		}
		return GenInt(g.rng, min, max)
	case ColTypeBigInt:
		return GenBigInt(g.rng)
	case ColTypeFloat:
		min, max := 0.01, 9999.99
		if col.MinVal > 0 || col.MaxVal > 0 {
			min = col.MinVal
			max = col.MaxVal
		}
		return GenFloat(g.rng, min, max)
	case ColTypeText:
		return GenText(g.rng, 2, 8)
	case ColTypeBool:
		return GenBool(g.rng)
	case ColTypeTimestamp:
		return GenTimestamp(g.rng)
	case ColTypeUUID:
		return GenUUID(g.rng)
	case ColTypeJSON:
		return GenJSONMeta(g.rng)
	case ColTypeBytea:
		return nil // binary columns generate nil by default
	case ColTypeEnum:
		return GenStatus(g.rng, col.EnumValues)
	default:
		return GenText(g.rng, 1, 5)
	}
}

// guessCategoryForRow attempts to find a category name for product name generation.
func (g *DataGenerator) guessCategoryForRow(table *TableDef, rowIndex int) string {
	// If this table has a FK to a categories-like table, use that to derive category
	categoryNames := []string{
		"Electronics", "Clothing", "Home & Garden", "Books", "Sports",
		"Beauty", "Toys", "Grocery", "Automotive", "Office",
	}
	// Simple: use rowIndex to cycle through categories
	if len(categoryNames) > 0 {
		return categoryNames[rowIndex%len(categoryNames)]
	}
	return ""
}

// InferHint infers a ColumnHint from column name and type.
func InferHint(name string, colType ColumnType, tableNames []string) ColumnHint {
	lower := strings.ToLower(name)

	// Exact matches first
	switch lower {
	case "email", "e_mail", "email_address":
		return HintEmail
	case "name", "full_name", "display_name":
		return HintName
	case "first_name", "firstname":
		return HintFirstName
	case "last_name", "lastname", "surname":
		return HintLastName
	case "username", "user_name", "login":
		return HintUsername
	case "price", "cost", "amount", "total", "subtotal", "balance", "salary", "fee":
		return HintPrice
	case "quantity", "qty", "count", "stock", "inventory_count", "qty_available":
		return HintQuantity
	case "status", "state", "condition":
		return HintStatus
	case "rating", "score", "stars":
		return HintRating
	case "title", "subject", "headline":
		return HintTitle
	case "description", "body", "content", "bio", "summary", "text", "comment":
		return HintDescription
	case "url", "link", "href", "website":
		return HintURL
	case "page_url":
		return HintPageURL
	case "address", "street", "street_address":
		return HintAddress
	case "phone", "mobile", "telephone", "phone_number":
		return HintPhone
	case "country", "country_name", "nation":
		return HintCountry
	case "color", "colour":
		return HintColor
	case "slug", "url_slug", "permalink":
		return HintSlug
	case "ip", "ip_address", "remote_addr":
		return HintIPAddress
	case "user_agent", "useragent":
		return HintUserAgent
	case "event_type", "event_name", "action":
		return HintEventType
	case "metadata", "meta", "extra", "properties":
		return HintJSONMeta
	case "company", "company_name", "organization", "org_name":
		return HintCompanyName
	}

	// Suffix patterns
	if strings.HasSuffix(lower, "_name") || strings.HasSuffix(lower, "_title") {
		return HintTitle
	}
	if strings.HasSuffix(lower, "_email") {
		return HintEmail
	}
	if strings.HasSuffix(lower, "_url") || strings.HasSuffix(lower, "_link") {
		return HintURL
	}
	if strings.HasSuffix(lower, "_phone") {
		return HintPhone
	}
	if strings.HasSuffix(lower, "_address") {
		return HintAddress
	}
	if strings.HasSuffix(lower, "_price") || strings.HasSuffix(lower, "_cost") || strings.HasSuffix(lower, "_amount") {
		return HintPrice
	}
	if strings.HasSuffix(lower, "_at") || strings.HasSuffix(lower, "_date") || strings.HasSuffix(lower, "_time") {
		if colType == ColTypeTimestamp || colType == ColTypeText {
			return HintDate
		}
	}

	// FK detection: column ends with _id and prefix matches a table name
	if strings.HasSuffix(lower, "_id") {
		prefix := strings.TrimSuffix(lower, "_id")
		for _, tn := range tableNames {
			// Match singular/plural: "user_id" matches "users"
			if strings.ToLower(tn) == prefix || strings.ToLower(tn) == prefix+"s" ||
				strings.TrimSuffix(strings.ToLower(tn), "s") == prefix ||
				strings.TrimSuffix(strings.ToLower(tn), "es") == prefix ||
				strings.TrimSuffix(strings.ToLower(tn), "ies")+"y" == prefix {
				return HintForeignKey
			}
		}
	}

	// Contains patterns
	if strings.Contains(lower, "email") {
		return HintEmail
	}
	if strings.Contains(lower, "price") || strings.Contains(lower, "cost") {
		return HintPrice
	}
	if strings.Contains(lower, "phone") {
		return HintPhone
	}

	return HintNone
}
