package tunnel

import (
	"github.com/dumanproxy/duman/internal/crypto"
)

// Splitter breaks a data stream into fixed-size chunks.
type Splitter struct {
	chunkSize int
	streamID  uint32
	seq       uint64
	buf       []byte
}

// NewSplitter creates a new chunk splitter.
func NewSplitter(streamID uint32, chunkSize int) *Splitter {
	if chunkSize <= 0 {
		chunkSize = crypto.MaxPayloadSize
	}
	return &Splitter{
		chunkSize: chunkSize,
		streamID:  streamID,
	}
}

// Split consumes data and returns complete chunks.
// Partial data is buffered for the next call.
func (s *Splitter) Split(data []byte) []*crypto.Chunk {
	s.buf = append(s.buf, data...)

	var chunks []*crypto.Chunk
	for len(s.buf) >= s.chunkSize {
		payload := make([]byte, s.chunkSize)
		copy(payload, s.buf[:s.chunkSize])
		s.buf = s.buf[s.chunkSize:]

		chunks = append(chunks, &crypto.Chunk{
			StreamID: s.streamID,
			Sequence: s.seq,
			Type:     crypto.ChunkData,
			Payload:  payload,
		})
		s.seq++
	}

	return chunks
}

// Flush returns remaining buffered data as a final chunk.
// Returns nil if no data is buffered.
func (s *Splitter) Flush() *crypto.Chunk {
	if len(s.buf) == 0 {
		return nil
	}

	payload := make([]byte, len(s.buf))
	copy(payload, s.buf)
	s.buf = nil

	ch := &crypto.Chunk{
		StreamID: s.streamID,
		Sequence: s.seq,
		Type:     crypto.ChunkData,
		Flags:    crypto.FlagLastChunk,
		Payload:  payload,
	}
	s.seq++
	return ch
}

// NextSequence returns the next sequence number.
func (s *Splitter) NextSequence() uint64 {
	return s.seq
}

// Buffered returns the number of buffered bytes.
func (s *Splitter) Buffered() int {
	return len(s.buf)
}
