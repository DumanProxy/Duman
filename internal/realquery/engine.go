package realquery

import (
	"math/rand"
	"time"

	"github.com/dumanproxy/duman/internal/fakedata"
)

// QueryBatch is a set of queries to be sent as a burst.
type QueryBatch struct {
	Queries []string
	Page    string // current "page" in the app
}

// Engine generates realistic cover queries based on scenario.
type Engine struct {
	scenario string
	state    *AppState
	rng      *rand.Rand
	patterns *GenericQueryPatterns // nil for legacy ecommerce path
}

// AppState tracks the simulated application state machine.
type AppState struct {
	CurrentPage string
	CartItems   []int
	ViewedIDs   []int
	SessionAge  time.Duration
}

// NewEngine creates a real query engine for the given scenario.
func NewEngine(scenario string, seed int64) *Engine {
	return &Engine{
		scenario: scenario,
		rng:      rand.New(rand.NewSource(seed)),
		state: &AppState{
			CurrentPage: "/",
		},
	}
}

// NewGenericQueryEngine creates a query engine driven by a schema definition.
// This generates realistic queries for any schema, not just ecommerce.
func NewGenericQueryEngine(schema *fakedata.SchemaDefinition, seed int64) *Engine {
	rng := rand.New(rand.NewSource(seed))
	return &Engine{
		scenario: schema.ScenarioName,
		rng:      rng,
		patterns: NewGenericQueryPatterns(schema, rng),
		state: &AppState{
			CurrentPage: "/",
		},
	}
}

// NextBurst generates the next batch of cover queries.
func (e *Engine) NextBurst() *QueryBatch {
	// Use generic patterns if available
	if e.patterns != nil {
		return e.genericBurst()
	}
	switch e.scenario {
	case "ecommerce":
		return e.ecommerceBurst()
	default:
		return e.ecommerceBurst()
	}
}

// genericBurst generates a query burst using schema-driven patterns.
func (e *Engine) genericBurst() *QueryBatch {
	table := e.patterns.PickEntityTable()
	if table == "" {
		return &QueryBatch{Queries: []string{"SELECT 1"}, Page: "/"}
	}

	// Weighted random action: 40% browse, 30% detail, 15% insert, 15% join
	r := e.rng.Intn(100)
	var queries []string
	var page string

	switch {
	case r < 40:
		queries = e.patterns.BrowseTable(table)
		page = "/" + table
	case r < 70:
		queries = e.patterns.ViewRecord(table)
		page = "/" + table + "/:id"
	case r < 85:
		queries = e.patterns.InsertRecord(table)
		page = "/" + table + "/new"
	default:
		queries = e.patterns.JoinQuery(table)
		page = "/" + table + "/report"
	}

	if len(queries) == 0 {
		queries = e.patterns.CountQuery(table)
		page = "/" + table
	}

	e.state.CurrentPage = page
	return &QueryBatch{Queries: queries, Page: page}
}

// RandomAnalyticsEvent generates a random analytics INSERT.
func (e *Engine) RandomAnalyticsEvent() string {
	events := []string{"page_view", "click", "scroll", "impression", "conversion_pixel"}
	event := events[e.rng.Intn(len(events))]
	return "INSERT INTO analytics_events (session_id, event_type, page_url) VALUES ('" +
		randomUUID(e.rng) + "', '" + event + "', '" + e.state.CurrentPage + "')"
}

// RandomBackgroundQuery generates a random background query (metrics, counts).
func (e *Engine) RandomBackgroundQuery() string {
	queries := []string{
		"SELECT count(*) FROM orders WHERE status = 'pending'",
		"SELECT count(*) FROM cart_items",
		"SELECT count(*) FROM sessions WHERE created_at > NOW() - INTERVAL '1 hour'",
		"SELECT avg(total) FROM orders WHERE created_at > NOW() - INTERVAL '24 hours'",
	}
	return queries[e.rng.Intn(len(queries))]
}

// BurstSpacing returns a random inter-query delay within a burst.
func (e *Engine) BurstSpacing() time.Duration {
	return time.Duration(15+e.rng.Intn(10)) * time.Millisecond
}

// ReadingPause returns a random reading pause between bursts.
func (e *Engine) ReadingPause() time.Duration {
	return time.Duration(2+e.rng.Intn(28)) * time.Second
}

// CurrentPage returns the current simulated page.
func (e *Engine) CurrentPage() string {
	return e.state.CurrentPage
}

func randomUUID(rng *rand.Rand) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hexEncode(b)
}

func hexEncode(b []byte) string {
	const hex = "0123456789abcdef"
	buf := make([]byte, 36)
	idx := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			buf[idx] = '-'
			idx++
		}
		buf[idx] = hex[v>>4]
		buf[idx+1] = hex[v&0x0f]
		idx += 2
	}
	return string(buf)
}
