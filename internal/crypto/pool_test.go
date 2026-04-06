package crypto

import (
	"bytes"
	"testing"
)

func TestGetPutChunkBuffer(t *testing.T) {
	buf := GetChunkBuffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	if len(*buf) != MaxChunkSize {
		t.Fatalf("buffer length = %d, want %d", len(*buf), MaxChunkSize)
	}
	PutChunkBuffer(buf)

	// After put, get again — should reuse
	buf2 := GetChunkBuffer()
	if buf2 == nil {
		t.Fatal("expected non-nil buffer after reuse")
	}
	PutChunkBuffer(buf2)
}

func TestPutChunkBuffer_Nil(t *testing.T) {
	// Should not panic
	PutChunkBuffer(nil)
}

func TestPutChunkBuffer_TooSmall(t *testing.T) {
	small := make([]byte, 10)
	PutChunkBuffer(&small) // should be a no-op (too small)
}

func TestGetPutPayloadBuffer(t *testing.T) {
	buf := GetPayloadBuffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	if cap(*buf) < MaxChunkSize+TagSize {
		t.Fatalf("buffer capacity = %d, want >= %d", cap(*buf), MaxChunkSize+TagSize)
	}
	PutPayloadBuffer(buf)
}

func TestPutPayloadBuffer_Nil(t *testing.T) {
	PutPayloadBuffer(nil)
}

func TestPutPayloadBuffer_TooSmall(t *testing.T) {
	small := make([]byte, 10)
	PutPayloadBuffer(&small)
}

func TestMarshalReuse_Roundtrip(t *testing.T) {
	ch := &Chunk{
		StreamID: 42,
		Sequence: 100,
		Type:     ChunkData,
		Flags:    0,
		Payload:  []byte("hello world"),
	}

	buf := make([]byte, MaxChunkSize)
	data, err := ch.MarshalReuse(buf)
	if err != nil {
		t.Fatal(err)
	}

	got, err := UnmarshalChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.StreamID != ch.StreamID {
		t.Errorf("StreamID = %d, want %d", got.StreamID, ch.StreamID)
	}
	if got.Sequence != ch.Sequence {
		t.Errorf("Sequence = %d, want %d", got.Sequence, ch.Sequence)
	}
	if !bytes.Equal(got.Payload, ch.Payload) {
		t.Error("payload mismatch")
	}
}

func TestMarshalReuse_SmallBuffer(t *testing.T) {
	ch := &Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     ChunkData,
		Payload:  []byte("test"),
	}

	// Pass a too-small buffer; should allocate internally
	small := make([]byte, 2)
	data, err := ch.MarshalReuse(small)
	if err != nil {
		t.Fatal(err)
	}

	got, err := UnmarshalChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, ch.Payload) {
		t.Error("payload mismatch")
	}
}

func TestMarshalReuse_OversizePayload(t *testing.T) {
	ch := &Chunk{
		StreamID: 1,
		Sequence: 1,
		Type:     ChunkData,
		Payload:  make([]byte, MaxPayloadSize+1),
	}

	buf := make([]byte, MaxChunkSize+100)
	_, err := ch.MarshalReuse(buf)
	if err == nil {
		t.Fatal("expected error for oversize payload")
	}
}

func TestErrPayloadTooLarge_Message(t *testing.T) {
	err := ErrPayloadTooLarge
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}
