package tunnel

import (
	"sync"
)

const maxGap = 1000 // maximum out-of-order gap before dropping

// Assembler reorders out-of-order chunks and delivers them in sequence.
type Assembler struct {
	mu       sync.Mutex
	expected uint64
	buffer   map[uint64][]byte // seq → payload
}

// NewAssembler creates a new chunk assembler.
func NewAssembler() *Assembler {
	return &Assembler{
		buffer: make(map[uint64][]byte),
	}
}

// Insert adds a chunk and returns any in-order data segments.
// If seq matches expected, deliver immediately and flush consecutive buffered.
// If seq > expected, buffer for later.
// If seq < expected, ignore (duplicate).
func (a *Assembler) Insert(seq uint64, data []byte) [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()

	if seq < a.expected {
		// Duplicate, ignore
		return nil
	}

	if seq > a.expected+maxGap {
		// Too far ahead, drop
		return nil
	}

	if seq > a.expected {
		// Out of order, buffer
		payload := make([]byte, len(data))
		copy(payload, data)
		a.buffer[seq] = payload
		return nil
	}

	// seq == expected: deliver and flush consecutive
	var segments [][]byte

	// Add current data
	payload := make([]byte, len(data))
	copy(payload, data)
	segments = append(segments, payload)
	a.expected++

	// Flush consecutive buffered chunks
	for {
		buffered, ok := a.buffer[a.expected]
		if !ok {
			break
		}
		segments = append(segments, buffered)
		delete(a.buffer, a.expected)
		a.expected++
	}

	return segments
}

// Expected returns the next expected sequence number.
func (a *Assembler) Expected() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.expected
}

// Pending returns the number of buffered out-of-order chunks.
func (a *Assembler) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.buffer)
}

// Reset clears the assembler state.
func (a *Assembler) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expected = 0
	a.buffer = make(map[uint64][]byte)
}
