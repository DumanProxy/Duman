package realquery

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/dumanproxy/duman/internal/fakedata"
)

// TableRole classifies how a table is used in queries.
type TableRole int

const (
	RoleEntity   TableRole = iota // Main entity tables (users, products, orders)
	RoleLookup                    // Lookup/reference tables (categories, statuses)
	RoleJunction                  // Junction/association tables (order_items)
	RoleTunnel                    // Infrastructure tables (analytics_events/responses)
)

// GenericQueryPatterns generates realistic SQL queries from a schema definition.
type GenericQueryPatterns struct {
	schema       *fakedata.SchemaDefinition
	rng          *rand.Rand
	roles        map[string]TableRole
	entities     []string
	lookups      []string
	fkGraph      map[string]map[string][2]string // table → col → [refTable, refCol]
	referencedBy map[string][]string             // table → []referringTable
}

// NewGenericQueryPatterns creates patterns from a schema definition.
func NewGenericQueryPatterns(schema *fakedata.SchemaDefinition, rng *rand.Rand) *GenericQueryPatterns {
	p := &GenericQueryPatterns{
		schema:       schema,
		rng:          rng,
		roles:        make(map[string]TableRole),
		fkGraph:      make(map[string]map[string][2]string),
		referencedBy: make(map[string][]string),
	}
	p.buildFKGraph()
	p.classifyTables()
	return p
}

func (p *GenericQueryPatterns) buildFKGraph() {
	for _, t := range p.schema.Tables {
		for _, c := range t.Columns {
			if c.IsFK && c.FKTable != "" {
				if p.fkGraph[t.Name] == nil {
					p.fkGraph[t.Name] = make(map[string][2]string)
				}
				p.fkGraph[t.Name][c.Name] = [2]string{c.FKTable, c.FKColumn}
				p.referencedBy[c.FKTable] = append(p.referencedBy[c.FKTable], t.Name)
			}
		}
	}
}

func (p *GenericQueryPatterns) classifyTables() {
	for _, t := range p.schema.Tables {
		if t.IsTunnel {
			p.roles[t.Name] = RoleTunnel
			continue
		}

		fkCount := 0
		for _, c := range t.Columns {
			if c.IsFK {
				fkCount++
			}
		}

		nonIDCols := len(t.Columns) - 1
		refCount := len(p.referencedBy[t.Name])

		if nonIDCols > 0 && fkCount > 0 && float64(fkCount)/float64(nonIDCols) > 0.5 {
			p.roles[t.Name] = RoleJunction
		} else if refCount > fkCount && t.RowCount <= 30 {
			p.roles[t.Name] = RoleLookup
			p.lookups = append(p.lookups, t.Name)
		} else {
			p.roles[t.Name] = RoleEntity
			p.entities = append(p.entities, t.Name)
		}
	}

	// Ensure at least one entity
	if len(p.entities) == 0 {
		for _, t := range p.schema.UserTables() {
			p.entities = append(p.entities, t.Name)
		}
	}
}

// PickEntityTable picks a random entity table.
func (p *GenericQueryPatterns) PickEntityTable() string {
	if len(p.entities) == 0 {
		return ""
	}
	return p.entities[p.rng.Intn(len(p.entities))]
}

// BrowseTable generates a browse/list query for the given table.
func (p *GenericQueryPatterns) BrowseTable(table string) []string {
	t := p.schema.TableByName(table)
	if t == nil {
		return nil
	}

	limit := 10 + p.rng.Intn(20)
	mainQuery := fmt.Sprintf("SELECT * FROM %s LIMIT %d", table, limit)
	queries := []string{mainQuery}

	// Add count query
	queries = append(queries, fmt.Sprintf("SELECT count(*) FROM %s", table))

	// If table has FK, add a filtered browse
	if fks, ok := p.fkGraph[table]; ok {
		for col, ref := range fks {
			refTable := p.schema.TableByName(ref[0])
			if refTable != nil && refTable.RowCount > 0 {
				id := p.rng.Intn(refTable.RowCount) + 1
				queries[0] = fmt.Sprintf("SELECT * FROM %s WHERE %s = %d LIMIT %d", table, col, id, limit)
				queries = append(queries, fmt.Sprintf("SELECT * FROM %s WHERE id = %d", ref[0], id))
				break
			}
		}
	}

	return queries
}

// ViewRecord generates a detail view query for a single record.
func (p *GenericQueryPatterns) ViewRecord(table string) []string {
	t := p.schema.TableByName(table)
	if t == nil {
		return nil
	}

	id := 1
	if t.RowCount > 0 {
		id = p.rng.Intn(t.RowCount) + 1
	}

	queries := []string{
		fmt.Sprintf("SELECT * FROM %s WHERE id = %d", table, id),
	}

	// Add related table queries (tables that reference this one)
	if refs, ok := p.referencedBy[table]; ok {
		for _, refTable := range refs {
			rt := p.schema.TableByName(refTable)
			if rt == nil || rt.IsTunnel {
				continue
			}
			for col, ref := range p.fkGraph[refTable] {
				if ref[0] == table {
					queries = append(queries, fmt.Sprintf("SELECT * FROM %s WHERE %s = %d LIMIT 5", refTable, col, id))
					break
				}
			}
		}
	}

	return queries
}

// InsertRecord generates an INSERT query for a non-tunnel table.
func (p *GenericQueryPatterns) InsertRecord(table string) []string {
	t := p.schema.TableByName(table)
	if t == nil || t.IsTunnel {
		return nil
	}

	var cols []string
	var vals []string
	for _, c := range t.Columns {
		if c.IsPK {
			continue
		}
		cols = append(cols, c.Name)
		if c.IsFK {
			refTable := p.schema.TableByName(c.FKTable)
			refCount := 10
			if refTable != nil && refTable.RowCount > 0 {
				refCount = refTable.RowCount
			}
			vals = append(vals, fmt.Sprintf("%d", p.rng.Intn(refCount)+1))
		} else {
			vals = append(vals, p.randomValueLiteral(c))
		}
	}

	return []string{
		fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(vals, ", ")),
	}
}

// CountQuery generates a count query.
func (p *GenericQueryPatterns) CountQuery(table string) []string {
	return []string{fmt.Sprintf("SELECT count(*) FROM %s", table)}
}

// JoinQuery generates a JOIN query between a table and one of its FK references.
func (p *GenericQueryPatterns) JoinQuery(table string) []string {
	fks, ok := p.fkGraph[table]
	if !ok || len(fks) == 0 {
		return p.BrowseTable(table)
	}

	for col, ref := range fks {
		a1 := string(table[0])
		a2 := string(ref[0][0])
		if a1 == a2 {
			a2 = a2 + "2"
		}
		limit := 10 + p.rng.Intn(15)
		q := fmt.Sprintf("SELECT %s.*, %s.* FROM %s %s JOIN %s %s ON %s.%s = %s.id LIMIT %d",
			a1, a2, table, a1, ref[0], a2, a1, col, a2, limit)
		return []string{q}
	}

	return nil
}

func (p *GenericQueryPatterns) randomValueLiteral(c fakedata.SchemColumn) string {
	switch c.Type {
	case fakedata.ColTypeInt, fakedata.ColTypeBigInt, fakedata.ColTypeSerial:
		return fmt.Sprintf("%d", p.rng.Intn(1000)+1)
	case fakedata.ColTypeFloat:
		return fmt.Sprintf("%.2f", float64(p.rng.Intn(10000))/100.0)
	case fakedata.ColTypeBool:
		if p.rng.Intn(2) == 0 {
			return "true"
		}
		return "false"
	case fakedata.ColTypeTimestamp:
		return "NOW()"
	case fakedata.ColTypeUUID:
		return fmt.Sprintf("'%s'", randomUUID(p.rng))
	default:
		if len(c.EnumValues) > 0 {
			return fmt.Sprintf("'%s'", c.EnumValues[p.rng.Intn(len(c.EnumValues))])
		}
		return fmt.Sprintf("'value_%d'", p.rng.Intn(1000))
	}
}
