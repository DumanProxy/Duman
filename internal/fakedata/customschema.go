package fakedata

// CustomSchemaBuilder builds a schema from user-provided DDL.
type CustomSchemaBuilder struct {
	ddl  string
	seed int64
}

// NewCustomSchemaBuilder creates a builder from DDL SQL.
func NewCustomSchemaBuilder(ddl string, seed int64) *CustomSchemaBuilder {
	return &CustomSchemaBuilder{ddl: ddl, seed: seed}
}

// Build parses the DDL and returns a SchemaDefinition.
func (b *CustomSchemaBuilder) Build() (*SchemaDefinition, error) {
	schema, err := ParseDDL(b.ddl)
	if err != nil {
		return nil, err
	}

	schema.Seed = b.seed
	schema.ScenarioName = "custom"

	// Set default row counts based on table characteristics
	for _, t := range schema.Tables {
		if t.RowCount == 0 {
			t.RowCount = inferRowCount(t)
		}
		if t.Schema == "" {
			t.Schema = "public"
		}
		if t.Owner == "" {
			t.Owner = "sensor_writer"
		}
	}

	// Add tunnel infrastructure tables
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)

	return schema, nil
}

// inferRowCount guesses a reasonable row count based on table characteristics.
func inferRowCount(t *TableDef) int {
	fkCount := 0
	for _, col := range t.Columns {
		if col.IsFK {
			fkCount++
		}
	}

	totalCols := len(t.Columns)

	switch {
	case fkCount == 0 && totalCols <= 3:
		// Lookup/reference table (like categories)
		return 10 + (totalCols * 5)
	case fkCount == 0:
		// Independent entity table
		return 50 + (totalCols * 10)
	case fkCount >= 2:
		// Junction/child table (like order_items, post_tags)
		return 100 + (fkCount * 50)
	default:
		// Regular entity with one FK
		return 100
	}
}
