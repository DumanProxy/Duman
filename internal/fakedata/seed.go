package fakedata

// TableInfo holds metadata for a database table (used by \dt response).
type TableInfo struct {
	Schema string
	Name   string
	Type   string
	Owner  string
}
