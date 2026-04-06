package crypto

import "sync"

// Buffer pools for reducing allocations in the hot path.
var (
	// chunkBufPool holds pre-allocated buffers for chunk serialization.
	chunkBufPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, MaxChunkSize)
			return &buf
		},
	}

	// payloadBufPool holds pre-allocated buffers for encrypted payloads.
	payloadBufPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 0, MaxChunkSize+TagSize)
			return &buf
		},
	}
)

// GetChunkBuffer returns a chunk-sized buffer from the pool.
// Must be returned with PutChunkBuffer when done.
func GetChunkBuffer() *[]byte {
	return chunkBufPool.Get().(*[]byte)
}

// PutChunkBuffer returns a buffer to the pool.
func PutChunkBuffer(buf *[]byte) {
	if buf != nil && cap(*buf) >= MaxChunkSize {
		*buf = (*buf)[:MaxChunkSize]
		chunkBufPool.Put(buf)
	}
}

// GetPayloadBuffer returns a buffer sized for encrypted payloads from the pool.
func GetPayloadBuffer() *[]byte {
	return payloadBufPool.Get().(*[]byte)
}

// PutPayloadBuffer returns an encrypted payload buffer to the pool.
func PutPayloadBuffer(buf *[]byte) {
	if buf != nil && cap(*buf) >= MaxChunkSize+TagSize {
		*buf = (*buf)[:0]
		payloadBufPool.Put(buf)
	}
}

// MarshalReuse serializes chunk into a pre-allocated buffer, avoiding allocation.
// Returns the used portion of buf. If buf is too small, allocates a new one.
func (ch *Chunk) MarshalReuse(buf []byte) ([]byte, error) {
	needed := ChunkHeaderSize + len(ch.Payload)
	if needed > MaxChunkSize {
		return nil, ErrPayloadTooLarge
	}

	if cap(buf) < needed {
		buf = make([]byte, needed)
	}
	buf = buf[:needed]

	marshalInto(buf, ch)
	return buf, nil
}

// marshalInto writes chunk data into a pre-allocated buffer.
func marshalInto(buf []byte, ch *Chunk) {
	_ = buf[ChunkHeaderSize-1] // bounds check hint
	buf[0] = byte(ch.StreamID >> 24)
	buf[1] = byte(ch.StreamID >> 16)
	buf[2] = byte(ch.StreamID >> 8)
	buf[3] = byte(ch.StreamID)
	buf[4] = byte(ch.Sequence >> 56)
	buf[5] = byte(ch.Sequence >> 48)
	buf[6] = byte(ch.Sequence >> 40)
	buf[7] = byte(ch.Sequence >> 32)
	buf[8] = byte(ch.Sequence >> 24)
	buf[9] = byte(ch.Sequence >> 16)
	buf[10] = byte(ch.Sequence >> 8)
	buf[11] = byte(ch.Sequence)
	buf[12] = byte(ch.Type)
	buf[13] = byte(ch.Flags)
	plen := len(ch.Payload)
	buf[14] = byte(plen >> 8)
	buf[15] = byte(plen)
	copy(buf[ChunkHeaderSize:], ch.Payload)
}

// ErrPayloadTooLarge is returned when chunk payload exceeds MaxPayloadSize.
var ErrPayloadTooLarge = errPayloadTooLarge{}

type errPayloadTooLarge struct{}

func (errPayloadTooLarge) Error() string {
	return "payload size exceeds maximum"
}
