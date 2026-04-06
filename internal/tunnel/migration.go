package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// MigrationState captures the state needed to resume a stream on a new relay.
type MigrationState struct {
	StreamID   uint32
	Sequence   uint64    // next expected sequence number
	SessionKey []byte    // encrypted session key for the new relay
	Pending    [][]byte  // unacknowledged chunks to replay
	Timestamp  time.Time
}

// ExportMigration captures the current stream state for migration.
func (sm *StreamManager) ExportMigration(streamID uint32) (*MigrationState, error) {
	s, ok := sm.GetStream(streamID)
	if !ok {
		return nil, fmt.Errorf("stream %d not found", streamID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.State == StateClosed {
		return nil, fmt.Errorf("stream %d is closed", streamID)
	}

	// Capture the next sequence number from the splitter.
	seq := s.splitter.NextSequence()

	// Capture any buffered data in the splitter as pending chunks.
	var pending [][]byte
	if s.splitter.Buffered() > 0 {
		// Flush remaining buffered data to capture as pending.
		flush := s.splitter.Flush()
		if flush != nil {
			data, err := flush.Marshal()
			if err == nil {
				pending = append(pending, data)
			}
		}
	}

	return &MigrationState{
		StreamID:  streamID,
		Sequence:  seq,
		Pending:   pending,
		Timestamp: time.Now(),
	}, nil
}

// ImportMigration restores a stream from migration state on this manager.
func (sm *StreamManager) ImportMigration(state *MigrationState) error {
	if state == nil {
		return errors.New("nil migration state")
	}

	// Check if stream ID already exists.
	if _, ok := sm.GetStream(state.StreamID); ok {
		return fmt.Errorf("stream %d already exists", state.StreamID)
	}

	// Create a restored stream without sending a CONNECT chunk.
	sctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		ID:        state.StreamID,
		State:     StateEstablished,
		splitter:  NewSplitter(state.StreamID, sm.chunkSize),
		assembler: NewAssembler(),
		outQueue:  sm.outQueue,
		inData:    make(chan []byte, 256),
		ctx:       sctx,
		cancel:    cancel,
	}

	// Advance the splitter sequence to match the exported state.
	s.splitter.seq = state.Sequence

	sm.streams.Store(state.StreamID, s)

	// Replay pending chunks.
	for _, raw := range state.Pending {
		ch, err := crypto.UnmarshalChunk(raw)
		if err != nil {
			continue
		}
		select {
		case s.outQueue <- ch:
		default:
			// Queue full, skip.
		}
	}

	return nil
}

// Marshal serializes the MigrationState to a binary format.
//
// Wire format:
//
//	[StreamID:4][Sequence:8][TimestampUnixNano:8][SessionKeyLen:4][SessionKey:...][PendingCount:4][for each: Len:4 + Data:...]
func (ms *MigrationState) Marshal() ([]byte, error) {
	// Calculate total size.
	size := 4 + 8 + 8 + 4 + len(ms.SessionKey) + 4
	for _, p := range ms.Pending {
		size += 4 + len(p)
	}

	buf := make([]byte, size)
	offset := 0

	binary.BigEndian.PutUint32(buf[offset:], ms.StreamID)
	offset += 4

	binary.BigEndian.PutUint64(buf[offset:], ms.Sequence)
	offset += 8

	binary.BigEndian.PutUint64(buf[offset:], uint64(ms.Timestamp.UnixNano()))
	offset += 8

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(ms.SessionKey)))
	offset += 4
	copy(buf[offset:], ms.SessionKey)
	offset += len(ms.SessionKey)

	binary.BigEndian.PutUint32(buf[offset:], uint32(len(ms.Pending)))
	offset += 4
	for _, p := range ms.Pending {
		binary.BigEndian.PutUint32(buf[offset:], uint32(len(p)))
		offset += 4
		copy(buf[offset:], p)
		offset += len(p)
	}

	return buf, nil
}

// UnmarshalMigration deserializes binary data into a MigrationState.
func UnmarshalMigration(data []byte) (*MigrationState, error) {
	if len(data) < 28 {
		return nil, errors.New("migration data too short")
	}

	offset := 0
	ms := &MigrationState{}

	ms.StreamID = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	ms.Sequence = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	tsNano := binary.BigEndian.Uint64(data[offset:])
	ms.Timestamp = time.Unix(0, int64(tsNano))
	offset += 8

	if offset+4 > len(data) {
		return nil, errors.New("migration data truncated at session key length")
	}
	skLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	if offset+skLen > len(data) {
		return nil, errors.New("migration data truncated at session key")
	}
	if skLen > 0 {
		ms.SessionKey = make([]byte, skLen)
		copy(ms.SessionKey, data[offset:offset+skLen])
	}
	offset += skLen

	if offset+4 > len(data) {
		return nil, errors.New("migration data truncated at pending count")
	}
	pendingCount := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	ms.Pending = make([][]byte, 0, pendingCount)
	for i := 0; i < pendingCount; i++ {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("migration data truncated at pending[%d] length", i)
		}
		pLen := int(binary.BigEndian.Uint32(data[offset:]))
		offset += 4
		if offset+pLen > len(data) {
			return nil, fmt.Errorf("migration data truncated at pending[%d] data", i)
		}
		p := make([]byte, pLen)
		copy(p, data[offset:offset+pLen])
		ms.Pending = append(ms.Pending, p)
		offset += pLen
	}

	return ms, nil
}
