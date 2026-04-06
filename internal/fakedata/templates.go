package fakedata

import (
	"fmt"
	"math/rand"

	"github.com/dumanproxy/duman/internal/pgwire"
)

// TemplateBuilder constructs a schema from a built-in template with optional mutations.
type TemplateBuilder struct {
	templateName string
	seed         int64
	mutate       bool
}

// NewTemplateBuilder creates a builder for a named template.
func NewTemplateBuilder(name string, seed int64, mutate bool) *TemplateBuilder {
	return &TemplateBuilder{templateName: name, seed: seed, mutate: mutate}
}

// Build generates the SchemaDefinition.
func (b *TemplateBuilder) Build() (*SchemaDefinition, error) {
	var schema *SchemaDefinition
	switch b.templateName {
	case "ecommerce", "":
		schema = ecommerceTemplate()
	case "iot":
		schema = iotTemplate()
	case "saas":
		schema = saasTemplate()
	case "blog":
		schema = blogTemplate()
	case "project":
		schema = projectTemplate()
	default:
		return nil, fmt.Errorf("unknown template: %s", b.templateName)
	}

	schema.Seed = b.seed

	// Add tunnel infrastructure tables
	schema.Tables = append(schema.Tables, TunnelInfrastructureTables("")...)

	if b.mutate {
		rng := rand.New(rand.NewSource(b.seed))
		schema.Mutations = applyMutations(schema, rng)
	}

	return schema, nil
}

// --- Table/Column synonym pools for mutations ---

var tableSynonyms = map[string][]string{
	"products":   {"items", "goods", "inventory", "merchandise", "listings", "catalog"},
	"categories": {"departments", "sections", "groups", "tags", "collections"},
	"users":      {"customers", "accounts", "members", "profiles", "people"},
	"orders":     {"purchases", "transactions", "invoices", "bookings", "sales"},
	"cart_items":  {"basket_items", "shopping_cart", "cart_entries", "bag_items"},
	"order_items": {"line_items", "order_lines", "purchase_items", "invoice_lines"},
	"reviews":    {"ratings", "feedback", "testimonials", "comments"},
	"sessions":   {"auth_sessions", "login_sessions", "user_sessions"},
	// IoT
	"devices":    {"sensors", "nodes", "endpoints", "units"},
	"metrics":    {"measurements", "readings", "telemetry", "datapoints"},
	"alerts":     {"alarms", "notifications", "incidents", "warnings"},
	// SaaS
	"organizations": {"companies", "workspaces", "teams", "tenants"},
	"subscriptions": {"plans", "memberships", "licenses", "billing_plans"},
	"usage_events":  {"activity_logs", "audit_events", "usage_logs"},
	// Blog
	"posts":    {"articles", "entries", "stories", "content"},
	"authors":  {"writers", "contributors", "editors", "creators"},
	// Project
	"projects": {"workspaces", "boards", "initiatives", "programs"},
	"tasks":    {"issues", "tickets", "items", "work_items", "stories"},
	"sprints":  {"iterations", "cycles", "milestones", "releases"},
}

var columnSynonyms = map[string][]string{
	"name":       {"title", "label", "display_name"},
	"price":      {"cost", "amount", "unit_price", "value"},
	"stock":      {"quantity", "inventory_count", "qty_available", "units"},
	"status":     {"state", "condition", "phase"},
	"created_at": {"date_created", "creation_date", "timestamp", "created"},
	"updated_at": {"date_updated", "modification_date", "last_modified"},
	"email":      {"email_address", "contact_email", "mail"},
	"rating":     {"score", "stars", "grade"},
	"total":      {"grand_total", "sum", "net_amount"},
}

var extraColumnPool = map[string][]SchemColumn{
	"products": {
		{Name: "description", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintDescription},
		{Name: "sku", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Nullable: true, Hint: HintSlug},
		{Name: "weight", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Nullable: true, MinVal: 0.1, MaxVal: 50.0},
		{Name: "is_active", Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1, Nullable: true},
		{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Nullable: true, Hint: HintDate},
	},
	"users": {
		{Name: "phone", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Nullable: true, Hint: HintPhone},
		{Name: "address", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintAddress},
		{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Nullable: true, Hint: HintDate},
		{Name: "is_verified", Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1, Nullable: true},
		{Name: "country", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Nullable: true, Hint: HintCountry},
	},
	"orders": {
		{Name: "notes", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintDescription},
		{Name: "shipping_address", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Nullable: true, Hint: HintAddress},
		{Name: "updated_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Nullable: true, Hint: HintDate},
	},
}

// applyMutations mutates table/column names and adds extra columns.
func applyMutations(schema *SchemaDefinition, rng *rand.Rand) *MutationSpec {
	spec := &MutationSpec{
		TableRenames:  make(map[string]string),
		ColumnRenames: make(map[string]map[string]string),
		ExtraColumns:  make(map[string][]SchemColumn),
	}

	for _, table := range schema.Tables {
		if table.IsTunnel {
			continue // never mutate tunnel tables
		}

		origName := table.Name

		// Table rename (50% chance)
		if synonyms, ok := tableSynonyms[table.Name]; ok && rng.Intn(2) == 0 {
			newName := synonyms[rng.Intn(len(synonyms))]
			spec.TableRenames[origName] = newName
			table.Name = newName
		}

		// Column renames (30% chance per column)
		colRenames := make(map[string]string)
		for i := range table.Columns {
			col := &table.Columns[i]
			if col.IsPK {
				continue // don't rename PKs
			}
			if synonyms, ok := columnSynonyms[col.Name]; ok && rng.Intn(3) == 0 {
				newName := synonyms[rng.Intn(len(synonyms))]
				colRenames[col.Name] = newName
				col.Name = newName
			}
		}
		if len(colRenames) > 0 {
			spec.ColumnRenames[origName] = colRenames
		}

		// Extra columns (0-2 per table, 40% chance)
		if extras, ok := extraColumnPool[origName]; ok && rng.Intn(5) < 2 {
			count := 1 + rng.Intn(2)
			if count > len(extras) {
				count = len(extras)
			}
			// Shuffle and pick
			perm := rng.Perm(len(extras))
			var added []SchemColumn
			for j := 0; j < count; j++ {
				extra := extras[perm[j]]
				// Check if column name already exists
				exists := false
				for _, c := range table.Columns {
					if c.Name == extra.Name {
						exists = true
						break
					}
				}
				if !exists {
					table.Columns = append(table.Columns, extra)
					added = append(added, extra)
				}
			}
			if len(added) > 0 {
				spec.ExtraColumns[origName] = added
			}
		}
	}

	// Update FK references to renamed tables
	for _, table := range schema.Tables {
		for i := range table.Columns {
			col := &table.Columns[i]
			if col.IsFK {
				if newName, ok := spec.TableRenames[col.FKTable]; ok {
					col.FKTable = newName
				}
			}
		}
	}

	return spec
}

// --- Built-in Templates ---

func ecommerceTemplate() *SchemaDefinition {
	owner := "sensor_writer"
	return &SchemaDefinition{
		ScenarioName: "ecommerce",
		Tables: []*TableDef{
			{Name: "categories", Schema: "public", Owner: owner, RowCount: 10, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
			}},
			{Name: "products", Schema: "public", Owner: owner, RowCount: 200, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintProductName},
				{Name: "price", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice, MinVal: 5.0, MaxVal: 2500.0},
				{Name: "category_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "categories", FKColumn: "id"},
				{Name: "stock", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintQuantity},
			}},
			{Name: "users", Schema: "public", Owner: owner, RowCount: 100, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintName},
				{Name: "email", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEmail},
			}},
			{Name: "orders", Schema: "public", Owner: owner, RowCount: 50, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "total", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice, MinVal: 5.0, MaxVal: 500.0},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"pending", "processing", "shipped", "delivered", "cancelled"}},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "cart_items", Schema: "public", Owner: owner, RowCount: 0, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "product_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "products", FKColumn: "id"},
				{Name: "quantity", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintQuantity},
			}},
			{Name: "order_items", Schema: "public", Owner: owner, RowCount: 100, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "order_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "orders", FKColumn: "id"},
				{Name: "product_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "products", FKColumn: "id"},
				{Name: "quantity", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintQuantity},
				{Name: "price", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice, MinVal: 5.0, MaxVal: 2500.0},
			}},
			{Name: "reviews", Schema: "public", Owner: owner, RowCount: 150, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "product_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "products", FKColumn: "id"},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "rating", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintRating},
				{Name: "text", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
			}},
			{Name: "sessions", Schema: "public", Owner: owner, RowCount: 30, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeUUID, PgOID: pgwire.OIDUUID, TypeSize: 16, TypeMod: -1, IsPK: true},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
		},
	}
}

func iotTemplate() *SchemaDefinition {
	owner := "iot_admin"
	return &SchemaDefinition{
		ScenarioName: "iot",
		Tables: []*TableDef{
			{Name: "devices", Schema: "public", Owner: owner, RowCount: 30, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "type", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"temperature", "humidity", "pressure", "motion", "light", "co2", "power", "flow"}},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"online", "offline", "maintenance", "error"}},
				{Name: "firmware", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
				{Name: "location", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintAddress},
				{Name: "last_seen", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "metrics", Schema: "public", Owner: owner, RowCount: 500, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "device_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "devices", FKColumn: "id"},
				{Name: "metric_name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "value", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, MinVal: -40.0, MaxVal: 150.0},
				{Name: "unit", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"celsius", "fahrenheit", "percent", "hpa", "lux", "ppm", "watts", "liters"}},
				{Name: "recorded_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "alerts", Schema: "public", Owner: owner, RowCount: 50, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "device_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "devices", FKColumn: "id"},
				{Name: "severity", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"info", "warning", "critical", "emergency"}},
				{Name: "message", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "acknowledged", Type: ColTypeBool, PgOID: pgwire.OIDBool, TypeSize: 1, TypeMod: -1},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "firmware_versions", Schema: "public", Owner: owner, RowCount: 15, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "version", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
				{Name: "release_notes", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "released_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
		},
	}
}

func saasTemplate() *SchemaDefinition {
	owner := "saas_admin"
	return &SchemaDefinition{
		ScenarioName: "saas",
		Tables: []*TableDef{
			{Name: "organizations", Schema: "public", Owner: owner, RowCount: 20, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintCompanyName},
				{Name: "slug", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
				{Name: "plan", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"free", "starter", "professional", "enterprise"}},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "users", Schema: "public", Owner: owner, RowCount: 100, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "org_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "organizations", FKColumn: "id"},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintName},
				{Name: "email", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEmail},
				{Name: "role", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"owner", "admin", "member", "viewer"}},
				{Name: "last_login", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "subscriptions", Schema: "public", Owner: owner, RowCount: 20, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "org_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "organizations", FKColumn: "id"},
				{Name: "plan", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"free", "starter", "professional", "enterprise"}},
				{Name: "amount", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice, MinVal: 0, MaxVal: 999},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"active", "cancelled", "past_due", "trialing"}},
				{Name: "current_period_end", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "usage_events", Schema: "public", Owner: owner, RowCount: 300, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "org_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "organizations", FKColumn: "id"},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "event_type", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEventType},
				{Name: "quantity", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, Hint: HintQuantity},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "invoices", Schema: "public", Owner: owner, RowCount: 60, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "org_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "organizations", FKColumn: "id"},
				{Name: "amount", Type: ColTypeFloat, PgOID: pgwire.OIDNumeric, TypeSize: -1, TypeMod: -1, Hint: HintPrice, MinVal: 10, MaxVal: 5000},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"draft", "sent", "paid", "overdue", "void"}},
				{Name: "due_date", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
		},
	}
}

func blogTemplate() *SchemaDefinition {
	owner := "blog_admin"
	return &SchemaDefinition{
		ScenarioName: "blog",
		Tables: []*TableDef{
			{Name: "authors", Schema: "public", Owner: owner, RowCount: 20, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintName},
				{Name: "email", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEmail},
				{Name: "bio", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
			}},
			{Name: "tags", Schema: "public", Owner: owner, RowCount: 30, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "slug", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
			}},
			{Name: "posts", Schema: "public", Owner: owner, RowCount: 100, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "author_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "authors", FKColumn: "id"},
				{Name: "title", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "slug", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
				{Name: "body", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"draft", "published", "archived"}},
				{Name: "published_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "comments", Schema: "public", Owner: owner, RowCount: 300, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "post_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "posts", FKColumn: "id"},
				{Name: "author_name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintName},
				{Name: "email", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEmail},
				{Name: "body", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "post_tags", Schema: "public", Owner: owner, RowCount: 200, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "post_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "posts", FKColumn: "id"},
				{Name: "tag_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "tags", FKColumn: "id"},
			}},
		},
	}
}

func projectTemplate() *SchemaDefinition {
	owner := "pm_admin"
	return &SchemaDefinition{
		ScenarioName: "project",
		Tables: []*TableDef{
			{Name: "projects", Schema: "public", Owner: owner, RowCount: 10, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "slug", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintSlug},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"active", "completed", "on_hold", "archived"}},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "users", Schema: "public", Owner: owner, RowCount: 30, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintName},
				{Name: "email", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintEmail},
				{Name: "role", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"admin", "developer", "designer", "tester", "pm"}},
			}},
			{Name: "sprints", Schema: "public", Owner: owner, RowCount: 20, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "project_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "projects", FKColumn: "id"},
				{Name: "name", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"planning", "active", "completed"}},
				{Name: "start_date", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
				{Name: "end_date", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "tasks", Schema: "public", Owner: owner, RowCount: 200, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "project_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "projects", FKColumn: "id"},
				{Name: "sprint_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "sprints", FKColumn: "id"},
				{Name: "assignee_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "title", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintTitle},
				{Name: "description", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "status", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"todo", "in_progress", "in_review", "done", "blocked"}},
				{Name: "priority", Type: ColTypeText, PgOID: pgwire.OIDVarchar, TypeSize: -1, TypeMod: 259, Hint: HintStatus,
					EnumValues: []string{"low", "medium", "high", "critical"}},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
			{Name: "task_comments", Schema: "public", Owner: owner, RowCount: 400, Columns: []SchemColumn{
				{Name: "id", Type: ColTypeSerial, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsPK: true},
				{Name: "task_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "tasks", FKColumn: "id"},
				{Name: "user_id", Type: ColTypeInt, PgOID: pgwire.OIDInt4, TypeSize: 4, TypeMod: -1, IsFK: true, FKTable: "users", FKColumn: "id"},
				{Name: "body", Type: ColTypeText, PgOID: pgwire.OIDText, TypeSize: -1, TypeMod: -1, Hint: HintDescription},
				{Name: "created_at", Type: ColTypeTimestamp, PgOID: pgwire.OIDTimestampTZ, TypeSize: 8, TypeMod: -1, Hint: HintDate},
			}},
		},
	}
}

// ecommerceCategoryNames returns the category names used in the ecommerce template.
func ecommerceCategoryNames() []string {
	return []string{
		"Electronics", "Clothing", "Home & Garden", "Books", "Sports",
		"Beauty", "Toys", "Grocery", "Automotive", "Office",
	}
}

// OrderStatuses returns common order status values.
func OrderStatuses() []string {
	return []string{"pending", "processing", "shipped", "delivered", "cancelled"}
}

// Ensure fmt import is used
var _ = fmt.Sprintf
