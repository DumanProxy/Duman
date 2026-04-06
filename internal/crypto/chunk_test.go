package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"
)

func TestChunk_MarshalUnmarshal(t *testing.T) {
	ch := &Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     ChunkData,
		Flags:    FlagCompressed | FlagLastChunk,
		Payload:  []byte("hello world"),
	}

	data, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := UnmarshalChunk(data)
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}

	if got.StreamID != ch.StreamID {
		t.Errorf("StreamID = %d, want %d", got.StreamID, ch.StreamID)
	}
	if got.Sequence != ch.Sequence {
		t.Errorf("Sequence = %d, want %d", got.Sequence, ch.Sequence)
	}
	if got.Type != ch.Type {
		t.Errorf("Type = %d, want %d", got.Type, ch.Type)
	}
	if got.Flags != ch.Flags {
		t.Errorf("Flags = %d, want %d", got.Flags, ch.Flags)
	}
	if !bytes.Equal(got.Payload, ch.Payload) {
		t.Errorf("Payload mismatch")
	}
}

func TestChunk_EmptyPayload(t *testing.T) {
	ch := &Chunk{
		StreamID: 1,
		Sequence: 0,
		Type:     ChunkFIN,
		Flags:    0,
		Payload:  nil,
	}

	data, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != ChunkHeaderSize {
		t.Fatalf("len = %d, want %d", len(data), ChunkHeaderSize)
	}

	got, err := UnmarshalChunk(data)
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}
	if len(got.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(got.Payload))
	}
}

func TestChunk_MaxPayload(t *testing.T) {
	payload := make([]byte, MaxPayloadSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	ch := &Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     ChunkData,
		Payload:  payload,
	}

	data, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) != MaxChunkSize {
		t.Fatalf("len = %d, want %d", len(data), MaxChunkSize)
	}

	got, err := UnmarshalChunk(data)
	if err != nil {
		t.Fatalf("UnmarshalChunk: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatal("payload mismatch for max size")
	}
}

func TestChunk_OversizePayload(t *testing.T) {
	ch := &Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     ChunkData,
		Payload:  make([]byte, MaxPayloadSize+1),
	}
	_, err := ch.Marshal()
	if err == nil {
		t.Fatal("expected error for oversize payload")
	}
}

func TestUnmarshalChunk_TooSmall(t *testing.T) {
	_, err := UnmarshalChunk(make([]byte, 5))
	if err == nil {
		t.Fatal("expected error for too-small data")
	}
}

func TestUnmarshalChunk_PayloadLengthExceedsData(t *testing.T) {
	data := make([]byte, ChunkHeaderSize)
	// Set payload length to 100 but no actual payload
	data[14] = 0
	data[15] = 100

	_, err := UnmarshalChunk(data)
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestEncryptDecryptChunk(t *testing.T) {
	key := make([]byte, KeySize)
	rand.Read(key)

	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	ch := &Chunk{
		StreamID: 7,
		Sequence: 42,
		Type:     ChunkData,
		Flags:    FlagUrgent,
		Payload:  []byte("encrypted tunnel data here"),
	}

	sessionID := "sess-abc-123"
	ciphertext, err := EncryptChunk(ch, c, sessionID)
	if err != nil {
		t.Fatalf("EncryptChunk: %v", err)
	}

	got, err := DecryptChunk(ciphertext, c, sessionID, ch.StreamID, ch.Sequence)
	if err != nil {
		t.Fatalf("DecryptChunk: %v", err)
	}

	if got.StreamID != ch.StreamID {
		t.Errorf("StreamID = %d, want %d", got.StreamID, ch.StreamID)
	}
	if got.Sequence != ch.Sequence {
		t.Errorf("Sequence = %d, want %d", got.Sequence, ch.Sequence)
	}
	if got.Type != ch.Type {
		t.Errorf("Type = %d, want %d", got.Type, ch.Type)
	}
	if !bytes.Equal(got.Payload, ch.Payload) {
		t.Error("Payload mismatch")
	}
}

func TestDecryptChunk_WrongSessionID(t *testing.T) {
	key := make([]byte, KeySize)
	rand.Read(key)

	c, err := NewCipher(key, CipherAES256GCM)
	if err != nil {
		t.Fatal(err)
	}

	ch := &Chunk{StreamID: 1, Sequence: 1, Type: ChunkData, Payload: []byte("data")}
	ciphertext, err := EncryptChunk(ch, c, "session-a")
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptChunk(ciphertext, c, "session-b", ch.StreamID, ch.Sequence)
	if err == nil {
		t.Fatal("expected error for wrong session ID")
	}
}

func TestDecryptChunk_WrongSequence(t *testing.T) {
	key := make([]byte, KeySize)
	rand.Read(key)

	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}

	ch := &Chunk{StreamID: 1, Sequence: 1, Type: ChunkData, Payload: []byte("data")}
	ciphertext, err := EncryptChunk(ch, c, "session")
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptChunk(ciphertext, c, "session", ch.StreamID, 999)
	if err == nil {
		t.Fatal("expected error for wrong sequence")
	}
}

func TestAllChunkTypes(t *testing.T) {
	types := []ChunkType{ChunkData, ChunkConnect, ChunkDNSResolve, ChunkFIN, ChunkACK, ChunkWindowUpdate}
	for _, ct := range types {
		ch := &Chunk{StreamID: 1, Sequence: 1, Type: ct, Payload: []byte("test")}
		data, err := ch.Marshal()
		if err != nil {
			t.Fatalf("Marshal type %d: %v", ct, err)
		}
		got, err := UnmarshalChunk(data)
		if err != nil {
			t.Fatalf("Unmarshal type %d: %v", ct, err)
		}
		if got.Type != ct {
			t.Errorf("type = %d, want %d", got.Type, ct)
		}
	}
}

func TestAllChunkFlags(t *testing.T) {
	flags := []ChunkFlags{FlagCompressed, FlagLastChunk, FlagUrgent, FlagCompressed | FlagLastChunk | FlagUrgent}
	for _, f := range flags {
		ch := &Chunk{StreamID: 1, Sequence: 1, Type: ChunkData, Flags: f, Payload: []byte("x")}
		data, err := ch.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		got, err := UnmarshalChunk(data)
		if err != nil {
			t.Fatal(err)
		}
		if got.Flags != f {
			t.Errorf("flags = %d, want %d", got.Flags, f)
		}
	}
}

func TestBuildAAD(t *testing.T) {
	aad := buildAAD("session", 42, 100)
	if len(aad) != len("session")+12 {
		t.Fatalf("aad len = %d, want %d", len(aad), len("session")+12)
	}
	if string(aad[:7]) != "session" {
		t.Error("aad should start with session ID")
	}
}

func TestUnmarshalChunk_PayloadLengthExceedsMax(t *testing.T) {
	data := make([]byte, ChunkHeaderSize+MaxPayloadSize+1)
	// Set payload length to MaxPayloadSize+1
	binary.BigEndian.PutUint16(data[14:16], uint16(MaxPayloadSize+1))
	_, err := UnmarshalChunk(data)
	if err == nil {
		t.Fatal("expected error for payload exceeding max")
	}
}

func TestEncryptChunk_OversizePayload(t *testing.T) {
	key := make([]byte, KeySize)
	rand.Read(key)
	c, err := NewCipher(key, CipherChaCha20)
	if err != nil {
		t.Fatal(err)
	}
	ch := &Chunk{Payload: make([]byte, MaxPayloadSize+1)}
	_, err = EncryptChunk(ch, c, "session")
	if err == nil {
		t.Fatal("expected error for oversize payload")
	}
}
