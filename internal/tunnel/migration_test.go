package tunnel

import (
	"bytes"
	"testing"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

func TestMigration_ExportImport(t *testing.T) {
	sm1 := NewStreamManager(64, 128)

	// Manually create a stream and register it (bypass NewStream to avoid CONNECT on channel).
	s := &Stream{
		ID:        42,
		State:     StateEstablished,
		splitter:  NewSplitter(42, 64),
		assembler: NewAssembler(),
		outQueue:  sm1.outQueue,
		inData:    make(chan []byte, 256),
	}
	// Advance the splitter sequence to simulate prior data sent.
	s.splitter.seq = 10
	sm1.streams.Store(uint32(42), s)

	// Export migration state.
	state, err := sm1.ExportMigration(42)
	if err != nil {
		t.Fatalf("ExportMigration: %v", err)
	}
	if state.StreamID != 42 {
		t.Errorf("StreamID = %d, want 42", state.StreamID)
	}
	if state.Sequence != 10 {
		t.Errorf("Sequence = %d, want 10", state.Sequence)
	}

	// Import on a new manager.
	sm2 := NewStreamManager(64, 128)
	if err := sm2.ImportMigration(state); err != nil {
		t.Fatalf("ImportMigration: %v", err)
	}

	// Verify the stream exists on the new manager.
	restored, ok := sm2.GetStream(42)
	if !ok {
		t.Fatal("stream 42 not found after import")
	}
	if restored.State != StateEstablished {
		t.Errorf("State = %d, want StateEstablished", restored.State)
	}
	if restored.splitter.NextSequence() != 10 {
		t.Errorf("NextSequence = %d, want 10", restored.splitter.NextSequence())
	}
}

func TestMigration_MarshalRoundtrip(t *testing.T) {
	original := &MigrationState{
		StreamID:   7,
		Sequence:   99,
		SessionKey: []byte("supersecretkey1234567890abcdef!!"),
		Pending: [][]byte{
			{0x01, 0x02, 0x03},
			{0xAA, 0xBB, 0xCC, 0xDD},
		},
		Timestamp: time.Now().Truncate(time.Nanosecond),
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	restored, err := UnmarshalMigration(data)
	if err != nil {
		t.Fatalf("UnmarshalMigration: %v", err)
	}

	if restored.StreamID != original.StreamID {
		t.Errorf("StreamID = %d, want %d", restored.StreamID, original.StreamID)
	}
	if restored.Sequence != original.Sequence {
		t.Errorf("Sequence = %d, want %d", restored.Sequence, original.Sequence)
	}
	if !bytes.Equal(restored.SessionKey, original.SessionKey) {
		t.Errorf("SessionKey mismatch")
	}
	if len(restored.Pending) != len(original.Pending) {
		t.Fatalf("Pending length = %d, want %d", len(restored.Pending), len(original.Pending))
	}
	for i := range original.Pending {
		if !bytes.Equal(restored.Pending[i], original.Pending[i]) {
			t.Errorf("Pending[%d] mismatch", i)
		}
	}
	if !restored.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", restored.Timestamp, original.Timestamp)
	}
}

func TestMigration_MarshalRoundtrip_EmptyFields(t *testing.T) {
	original := &MigrationState{
		StreamID:  1,
		Sequence:  0,
		Timestamp: time.Unix(0, 1000000),
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	restored, err := UnmarshalMigration(data)
	if err != nil {
		t.Fatalf("UnmarshalMigration: %v", err)
	}

	if restored.StreamID != 1 {
		t.Errorf("StreamID = %d", restored.StreamID)
	}
	if restored.Sequence != 0 {
		t.Errorf("Sequence = %d", restored.Sequence)
	}
	if len(restored.SessionKey) != 0 {
		t.Errorf("SessionKey should be empty, got %d bytes", len(restored.SessionKey))
	}
	if len(restored.Pending) != 0 {
		t.Errorf("Pending should be empty, got %d", len(restored.Pending))
	}
}

func TestMigration_InvalidStream(t *testing.T) {
	sm := NewStreamManager(64, 128)

	_, err := sm.ExportMigration(999)
	if err == nil {
		t.Fatal("expected error for non-existent stream")
	}
}

func TestMigration_ReplayPending(t *testing.T) {
	sm1 := NewStreamManager(64, 128)

	// Create a stream with buffered (partial) data.
	s := &Stream{
		ID:        5,
		State:     StateEstablished,
		splitter:  NewSplitter(5, 64),
		assembler: NewAssembler(),
		outQueue:  sm1.outQueue,
		inData:    make(chan []byte, 256),
	}
	// Write partial data that won't fill a chunk (stays in splitter buffer).
	s.splitter.Split([]byte("partial-data"))
	sm1.streams.Store(uint32(5), s)

	// Export should capture the buffered data as pending.
	state, err := sm1.ExportMigration(5)
	if err != nil {
		t.Fatalf("ExportMigration: %v", err)
	}
	if len(state.Pending) != 1 {
		t.Fatalf("Pending length = %d, want 1", len(state.Pending))
	}

	// Verify pending chunk is valid by unmarshaling it.
	ch, err := crypto.UnmarshalChunk(state.Pending[0])
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}
	if string(ch.Payload) != "partial-data" {
		t.Errorf("pending payload = %q, want %q", ch.Payload, "partial-data")
	}
	if ch.StreamID != 5 {
		t.Errorf("pending StreamID = %d, want 5", ch.StreamID)
	}

	// Import on a new manager and verify pending chunks are replayed.
	sm2 := NewStreamManager(64, 256)

	if err := sm2.ImportMigration(state); err != nil {
		t.Fatalf("ImportMigration: %v", err)
	}

	// The pending chunk should have been sent to the output queue.
	select {
	case replayed := <-sm2.outQueue:
		if replayed.StreamID != 5 {
			t.Errorf("replayed StreamID = %d, want 5", replayed.StreamID)
		}
		if string(replayed.Payload) != "partial-data" {
			t.Errorf("replayed payload = %q, want %q", replayed.Payload, "partial-data")
		}
	default:
		t.Fatal("expected replayed chunk in output queue")
	}

	// Verify stream is registered.
	if _, ok := sm2.GetStream(5); !ok {
		t.Fatal("stream 5 not found after import")
	}
}

func TestMigration_UnmarshalTooShort(t *testing.T) {
	_, err := UnmarshalMigration([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestMigration_ImportNilState(t *testing.T) {
	sm := NewStreamManager(64, 128)
	err := sm.ImportMigration(nil)
	if err == nil {
		t.Fatal("expected error for nil state")
	}
}

func TestMigration_ImportDuplicateStream(t *testing.T) {
	sm := NewStreamManager(64, 128)

	state := &MigrationState{
		StreamID:  1,
		Sequence:  5,
		Timestamp: time.Now(),
	}

	// First import should succeed.
	if err := sm.ImportMigration(state); err != nil {
		t.Fatalf("first ImportMigration: %v", err)
	}

	// Second import with same stream ID should fail.
	err := sm.ImportMigration(state)
	if err == nil {
		t.Fatal("expected error for duplicate stream ID")
	}
}

func TestMigration_ExportClosedStream(t *testing.T) {
	sm := NewStreamManager(64, 128)

	s := &Stream{
		ID:        10,
		State:     StateClosed,
		splitter:  NewSplitter(10, 64),
		assembler: NewAssembler(),
		outQueue:  sm.outQueue,
		inData:    make(chan []byte, 256),
	}
	sm.streams.Store(uint32(10), s)

	_, err := sm.ExportMigration(10)
	if err == nil {
		t.Fatal("expected error for closed stream")
	}
}
