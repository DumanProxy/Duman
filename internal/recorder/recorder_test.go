package recorder

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helper to create a temp recording file path.
func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.dumn")
}

func TestRecorder_WriteRead(t *testing.T) {
	path := tmpPath(t)

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	want := Record{
		Timestamp: now,
		Direction: Inbound,
		Protocol:  "pgwire",
		Data:      []byte("SELECT 1"),
		Metadata:  map[string]string{"session": "abc123", "user": "admin"},
	}

	if err := rec.Record(want); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("Count = %d, want 1", rec.Count())
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back.
	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	records := player.Records()
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	got := records[0]
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	if got.Direction != want.Direction {
		t.Errorf("Direction = %c, want %c", got.Direction, want.Direction)
	}
	if got.Protocol != want.Protocol {
		t.Errorf("Protocol = %q, want %q", got.Protocol, want.Protocol)
	}
	if !bytes.Equal(got.Data, want.Data) {
		t.Errorf("Data = %q, want %q", got.Data, want.Data)
	}
	if len(got.Metadata) != len(want.Metadata) {
		t.Fatalf("Metadata len = %d, want %d", len(got.Metadata), len(want.Metadata))
	}
	for k, v := range want.Metadata {
		if got.Metadata[k] != v {
			t.Errorf("Metadata[%q] = %q, want %q", k, got.Metadata[k], v)
		}
	}
}

func TestRecorder_MultipleRecords(t *testing.T) {
	path := tmpPath(t)

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	const n = 1000
	base := time.Now().Truncate(time.Nanosecond)
	protocols := []string{"pgwire", "mysql", "rest", "ws"}

	for i := 0; i < n; i++ {
		r := Record{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Direction: Inbound,
			Protocol:  protocols[i%len(protocols)],
			Data:      []byte(fmt.Sprintf("payload-%d", i)),
		}
		if i%3 == 0 {
			r.Metadata = map[string]string{"index": fmt.Sprintf("%d", i)}
		}
		if err := rec.Record(r); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}

	if rec.Count() != n {
		t.Fatalf("Count = %d, want %d", rec.Count(), n)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	records := player.Records()
	if len(records) != n {
		t.Fatalf("got %d records, want %d", len(records), n)
	}

	for i := 0; i < n; i++ {
		got := records[i]
		wantTS := base.Add(time.Duration(i) * time.Millisecond)
		if !got.Timestamp.Equal(wantTS) {
			t.Errorf("record %d: Timestamp = %v, want %v", i, got.Timestamp, wantTS)
		}
		wantProto := protocols[i%len(protocols)]
		if got.Protocol != wantProto {
			t.Errorf("record %d: Protocol = %q, want %q", i, got.Protocol, wantProto)
		}
		wantData := fmt.Sprintf("payload-%d", i)
		if string(got.Data) != wantData {
			t.Errorf("record %d: Data = %q, want %q", i, string(got.Data), wantData)
		}
		if i%3 == 0 {
			if got.Metadata["index"] != fmt.Sprintf("%d", i) {
				t.Errorf("record %d: Metadata[index] = %q, want %q", i, got.Metadata["index"], fmt.Sprintf("%d", i))
			}
		}
	}
}

func TestRecorder_Filter(t *testing.T) {
	path := tmpPath(t)

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	records := []Record{
		{Timestamp: now, Direction: Inbound, Protocol: "pgwire", Data: []byte("pg1")},
		{Timestamp: now.Add(time.Millisecond), Direction: Outbound, Protocol: "mysql", Data: []byte("my1")},
		{Timestamp: now.Add(2 * time.Millisecond), Direction: Inbound, Protocol: "pgwire", Data: []byte("pg2")},
		{Timestamp: now.Add(3 * time.Millisecond), Direction: Outbound, Protocol: "rest", Data: []byte("r1")},
		{Timestamp: now.Add(4 * time.Millisecond), Direction: Inbound, Protocol: "pgwire", Data: []byte("pg3")},
	}

	for i, r := range records {
		if err := rec.Record(r); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	pgRecords := player.Filter("pgwire")
	if len(pgRecords) != 3 {
		t.Fatalf("Filter(pgwire) = %d records, want 3", len(pgRecords))
	}
	for _, r := range pgRecords {
		if r.Protocol != "pgwire" {
			t.Errorf("filtered record has Protocol = %q, want pgwire", r.Protocol)
		}
	}

	mysqlRecords := player.Filter("mysql")
	if len(mysqlRecords) != 1 {
		t.Fatalf("Filter(mysql) = %d records, want 1", len(mysqlRecords))
	}

	wsRecords := player.Filter("ws")
	if len(wsRecords) != 0 {
		t.Fatalf("Filter(ws) = %d records, want 0", len(wsRecords))
	}
}

func TestRecorder_Duration(t *testing.T) {
	path := tmpPath(t)

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	base := time.Now().Truncate(time.Nanosecond)
	wantDuration := 500 * time.Millisecond

	records := []Record{
		{Timestamp: base, Direction: Inbound, Protocol: "pgwire", Data: []byte("start")},
		{Timestamp: base.Add(100 * time.Millisecond), Direction: Outbound, Protocol: "pgwire", Data: []byte("mid1")},
		{Timestamp: base.Add(250 * time.Millisecond), Direction: Inbound, Protocol: "mysql", Data: []byte("mid2")},
		{Timestamp: base.Add(wantDuration), Direction: Outbound, Protocol: "rest", Data: []byte("end")},
	}

	for i, r := range records {
		if err := rec.Record(r); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := player.Duration()
	if got != wantDuration {
		t.Errorf("Duration = %v, want %v", got, wantDuration)
	}
}

func TestRecorder_EmptyFile(t *testing.T) {
	path := tmpPath(t)

	// Write a valid file with zero records.
	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("Count = %d, want 0", rec.Count())
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	records := player.Records()
	if len(records) != 0 {
		t.Fatalf("got %d records, want 0", len(records))
	}

	if d := player.Duration(); d != 0 {
		t.Errorf("Duration = %v, want 0", d)
	}
}

func TestRecorder_InvalidMagic(t *testing.T) {
	path := tmpPath(t)

	// Write a file with bad magic bytes.
	if err := os.WriteFile(path, []byte("BADMxx\x00\x00"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error for invalid magic bytes")
	}
	t.Logf("got expected error: %v", err)
}

func TestRecorder_LargePayload(t *testing.T) {
	path := tmpPath(t)

	// 1 MB random payload.
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	want := Record{
		Timestamp: now,
		Direction: Outbound,
		Protocol:  "ws",
		Data:      payload,
		Metadata:  map[string]string{"size": "1MB"},
	}

	if err := rec.Record(want); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	records := player.Records()
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	got := records[0]
	if !bytes.Equal(got.Data, payload) {
		t.Error("1MB payload did not roundtrip correctly")
	}
	if got.Protocol != "ws" {
		t.Errorf("Protocol = %q, want ws", got.Protocol)
	}
	if got.Metadata["size"] != "1MB" {
		t.Errorf("Metadata[size] = %q, want 1MB", got.Metadata["size"])
	}
}
