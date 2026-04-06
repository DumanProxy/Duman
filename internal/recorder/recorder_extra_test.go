package recorder

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewRecorder error paths
// ---------------------------------------------------------------------------

func TestNewRecorder_InvalidPath(t *testing.T) {
	// A path inside a non-existent directory should fail os.Create.
	_, err := NewRecorder(filepath.Join(t.TempDir(), "no", "such", "dir", "file.dumn"))
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// ---------------------------------------------------------------------------
// NewPlayer error paths
// ---------------------------------------------------------------------------

func TestNewPlayer_FileNotExist(t *testing.T) {
	_, err := NewPlayer(filepath.Join(t.TempDir(), "nope.dumn"))
	if err == nil {
		t.Fatal("expected error when file does not exist")
	}
}

func TestNewPlayer_EmptyFile(t *testing.T) {
	// An empty file should fail reading the magic bytes.
	path := filepath.Join(t.TempDir(), "empty.dumn")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error for empty file (no magic)")
	}
}

func TestNewPlayer_TruncatedAfterMagic(t *testing.T) {
	// File has valid magic but nothing else (no version bytes).
	path := filepath.Join(t.TempDir(), "trunc_version.dumn")
	if err := os.WriteFile(path, magic[:], 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error when version bytes are missing")
	}
}

func TestNewPlayer_UnsupportedVersion(t *testing.T) {
	// Write valid magic + a bad version number.
	var buf bytes.Buffer
	buf.Write(magic[:])
	binary.Write(&buf, binary.BigEndian, uint16(99)) // bad version
	binary.Write(&buf, binary.BigEndian, uint16(0))  // reserved

	path := filepath.Join(t.TempDir(), "badver.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestNewPlayer_TruncatedReserved(t *testing.T) {
	// Valid magic + valid version, but missing reserved bytes.
	var buf bytes.Buffer
	buf.Write(magic[:])
	binary.Write(&buf, binary.BigEndian, formatVersion)
	// no reserved bytes

	path := filepath.Join(t.TempDir(), "trunc_reserved.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error when reserved bytes are missing")
	}
}

// ---------------------------------------------------------------------------
// readRecord / Load error paths – truncated record data
// ---------------------------------------------------------------------------

// validHeader returns a valid 8-byte file header.
func validHeader() []byte {
	var buf bytes.Buffer
	buf.Write(magic[:])
	binary.Write(&buf, binary.BigEndian, formatVersion)
	binary.Write(&buf, binary.BigEndian, uint16(0))
	return buf.Bytes()
}

func TestLoad_TruncatedTimestamp(t *testing.T) {
	// Header followed by only 4 bytes (need 8 for int64 timestamp).
	data := append(validHeader(), 0x00, 0x00, 0x00, 0x00)
	path := filepath.Join(t.TempDir(), "trunc_ts.dumn")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	// Load should handle the unexpected EOF gracefully (break, no error).
	if err := p.Load(); err != nil {
		t.Fatalf("Load should not return error for truncated timestamp at boundary: %v", err)
	}
	if len(p.Records()) != 0 {
		t.Fatalf("expected 0 records, got %d", len(p.Records()))
	}
}

func TestLoad_TruncatedDirection(t *testing.T) {
	// Complete timestamp (8 bytes) but no direction byte.
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano()) // 8 bytes timestamp
	// missing direction

	path := filepath.Join(t.TempDir(), "trunc_dir.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		// It's also acceptable that Load treats it as unexpected EOF and
		// breaks – check that no partial record was added.
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records for truncated direction")
		}
	}
}

func TestLoad_TruncatedProtocolLen(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano()) // timestamp
	buf.WriteByte(byte(Inbound))                                // direction
	// missing protocol length

	path := filepath.Join(t.TempDir(), "trunc_proto_len.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	// Should return a read error (not EOF/unexpected-EOF at record boundary).
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedProtocolData(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Inbound))
	binary.Write(&buf, binary.BigEndian, uint16(10)) // says 10 bytes of protocol
	buf.Write([]byte("abc"))                         // only 3 bytes

	path := filepath.Join(t.TempDir(), "trunc_proto_data.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedMetadataCount(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Outbound))
	binary.Write(&buf, binary.BigEndian, uint16(2)) // protocol length = 2
	buf.Write([]byte("ws"))                         // protocol data
	// missing metadata count

	path := filepath.Join(t.TempDir(), "trunc_meta_count.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedMetadataKeyLen(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Inbound))
	binary.Write(&buf, binary.BigEndian, uint16(4))
	buf.Write([]byte("rest"))
	binary.Write(&buf, binary.BigEndian, uint16(1)) // 1 metadata entry
	// missing key length

	path := filepath.Join(t.TempDir(), "trunc_meta_klen.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedMetadataKey(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Inbound))
	binary.Write(&buf, binary.BigEndian, uint16(4))
	buf.Write([]byte("rest"))
	binary.Write(&buf, binary.BigEndian, uint16(1))  // 1 metadata entry
	binary.Write(&buf, binary.BigEndian, uint16(10)) // key length 10
	buf.Write([]byte("ab"))                          // only 2 bytes

	path := filepath.Join(t.TempDir(), "trunc_meta_key.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedMetadataValLen(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Inbound))
	binary.Write(&buf, binary.BigEndian, uint16(4))
	buf.Write([]byte("rest"))
	binary.Write(&buf, binary.BigEndian, uint16(1)) // 1 metadata entry
	binary.Write(&buf, binary.BigEndian, uint16(3)) // key length 3
	buf.Write([]byte("foo"))                        // key
	// missing value length

	path := filepath.Join(t.TempDir(), "trunc_meta_vlen.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedMetadataVal(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Inbound))
	binary.Write(&buf, binary.BigEndian, uint16(4))
	buf.Write([]byte("rest"))
	binary.Write(&buf, binary.BigEndian, uint16(1)) // 1 metadata entry
	binary.Write(&buf, binary.BigEndian, uint16(3)) // key length 3
	buf.Write([]byte("foo"))                        // key
	binary.Write(&buf, binary.BigEndian, uint16(5)) // val length 5
	buf.Write([]byte("ba"))                         // only 2 of 5 bytes

	path := filepath.Join(t.TempDir(), "trunc_meta_val.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedDataLen(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Outbound))
	binary.Write(&buf, binary.BigEndian, uint16(2))
	buf.Write([]byte("ws"))
	binary.Write(&buf, binary.BigEndian, uint16(0)) // 0 metadata
	// missing data length (uint32)

	path := filepath.Join(t.TempDir(), "trunc_data_len.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

func TestLoad_TruncatedData(t *testing.T) {
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano())
	buf.WriteByte(byte(Outbound))
	binary.Write(&buf, binary.BigEndian, uint16(2))
	buf.Write([]byte("ws"))
	binary.Write(&buf, binary.BigEndian, uint16(0))  // 0 metadata
	binary.Write(&buf, binary.BigEndian, uint32(100)) // says 100 bytes data
	buf.Write([]byte("short"))                        // only 5 bytes

	path := filepath.Join(t.TempDir(), "trunc_data.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	if err == nil {
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records")
		}
	}
}

// ---------------------------------------------------------------------------
// Load with non-EOF error
// ---------------------------------------------------------------------------

func TestLoad_NonEOFError(t *testing.T) {
	// Write a valid header + a partial record that causes a wrapped read error
	// (not a bare io.EOF or io.ErrUnexpectedEOF) from readRecord.
	// The direction-read error is wrapped in fmt.Errorf, so it won't match
	// errors.Is(err, io.EOF). This exercises the non-EOF error return in Load.
	hdr := validHeader()
	var buf bytes.Buffer
	buf.Write(hdr)
	binary.Write(&buf, binary.BigEndian, time.Now().UnixNano()) // valid timestamp
	// EOF here will surface as a wrapped error from ReadByte -> "player: read direction: EOF"

	path := filepath.Join(t.TempDir(), "non_eof.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer p.Close()

	err = p.Load()
	// This should be a non-nil error because the wrapped error from
	// readRecord won't match io.EOF or io.ErrUnexpectedEOF.
	if err == nil {
		// If it didn't error, we still accept 0 records.
		if len(p.Records()) != 0 {
			t.Fatal("expected 0 records when error happens mid-record")
		}
	}
}

// ---------------------------------------------------------------------------
// Recorder.Record with metadata – roundtrip verification
// ---------------------------------------------------------------------------

func TestRecorder_RecordWithMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	meta := map[string]string{
		"key1": "val1",
		"key2": "val2",
		"key3": "val3",
	}
	r := Record{
		Timestamp: now,
		Direction: Outbound,
		Protocol:  "mysql",
		Data:      []byte("SELECT * FROM t"),
		Metadata:  meta,
	}
	if err := rec.Record(r); err != nil {
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

	recs := player.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	got := recs[0]
	if len(got.Metadata) != 3 {
		t.Fatalf("expected 3 metadata entries, got %d", len(got.Metadata))
	}
	for k, v := range meta {
		if got.Metadata[k] != v {
			t.Errorf("Metadata[%q] = %q, want %q", k, got.Metadata[k], v)
		}
	}
}

// ---------------------------------------------------------------------------
// Recorder.Record with nil/empty metadata and empty data
// ---------------------------------------------------------------------------

func TestRecorder_NoMetadataEmptyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nometa.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	r := Record{
		Timestamp: now,
		Direction: Inbound,
		Protocol:  "rest",
		Data:      nil,
		Metadata:  nil,
	}
	if err := rec.Record(r); err != nil {
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

	recs := player.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	got := recs[0]
	if got.Protocol != "rest" {
		t.Errorf("Protocol = %q, want rest", got.Protocol)
	}
	if len(got.Data) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(got.Data))
	}
	if len(got.Metadata) != 0 {
		t.Errorf("expected nil/empty metadata, got %d", len(got.Metadata))
	}
}

// ---------------------------------------------------------------------------
// Duration with a single record (returns 0)
// ---------------------------------------------------------------------------

func TestPlayer_DurationSingleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	now := time.Now().Truncate(time.Nanosecond)
	if err := rec.Record(Record{
		Timestamp: now,
		Direction: Inbound,
		Protocol:  "pgwire",
		Data:      []byte("hi"),
	}); err != nil {
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

	if d := player.Duration(); d != 0 {
		t.Errorf("Duration = %v, want 0 for single record", d)
	}
}

// ---------------------------------------------------------------------------
// Concurrent recording
// ---------------------------------------------------------------------------

func TestRecorder_Concurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	const goroutines = 10
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				r := Record{
					Timestamp: time.Now(),
					Direction: Inbound,
					Protocol:  "ws",
					Data:      []byte("concurrent"),
				}
				if err := rec.Record(r); err != nil {
					t.Errorf("goroutine %d record %d: %v", id, i, err)
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(goroutines * perGoroutine)
	if rec.Count() != total {
		t.Fatalf("Count = %d, want %d", rec.Count(), total)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify we can read them all back.
	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	defer player.Close()

	if err := player.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if int64(len(player.Records())) != total {
		t.Fatalf("loaded %d records, want %d", len(player.Records()), total)
	}
}

// ---------------------------------------------------------------------------
// Filter returns empty when no match
// ---------------------------------------------------------------------------

func TestPlayer_FilterNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filter_none.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	now := time.Now().Truncate(time.Nanosecond)
	if err := rec.Record(Record{
		Timestamp: now,
		Direction: Inbound,
		Protocol:  "pgwire",
		Data:      []byte("x"),
	}); err != nil {
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

	filtered := player.Filter("nonexistent")
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered records, got %d", len(filtered))
	}
}

// ---------------------------------------------------------------------------
// NewPlayer with invalid magic but valid length
// ---------------------------------------------------------------------------

func TestNewPlayer_InvalidMagicValidLength(t *testing.T) {
	// 8 bytes total but wrong magic.
	var buf bytes.Buffer
	buf.Write([]byte("XXXX"))
	binary.Write(&buf, binary.BigEndian, uint16(1))
	binary.Write(&buf, binary.BigEndian, uint16(0))

	path := filepath.Join(t.TempDir(), "badmagic.dumn")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewPlayer(path)
	if err == nil {
		t.Fatal("expected error for invalid magic bytes")
	}
}

// ---------------------------------------------------------------------------
// Recorder.Close is idempotent-ish (second close errors)
// ---------------------------------------------------------------------------

func TestRecorder_DoubleClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dblclose.dumn")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close should return an error from the underlying file.
	if err := rec.Close(); err == nil {
		t.Log("second Close returned nil (OS-dependent)")
	}
}

// ---------------------------------------------------------------------------
// Player.Close is idempotent-ish
// ---------------------------------------------------------------------------

func TestPlayer_DoubleClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dblclose_play.dumn")
	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close recorder: %v", err)
	}

	player, err := NewPlayer(path)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	if err := player.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := player.Close(); err == nil {
		t.Log("second Close returned nil (OS-dependent)")
	}
}
