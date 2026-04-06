package tunnel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/dumanproxy/duman/internal/crypto"
)

// StreamState represents the lifecycle state of a tunnel stream.
type StreamState int

const (
	StateConnecting StreamState = iota
	StateEstablished
	StateClosing
	StateClosed
)

// Stream represents a single tunnel stream (one TCP connection proxied through the tunnel).
type Stream struct {
	ID          uint32
	Destination string
	State       StreamState

	splitter  *Splitter
	assembler *Assembler
	outQueue  chan *crypto.Chunk // chunks ready for encryption/sending
	inData    chan []byte        // reassembled data for the app
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// StreamManager manages all active tunnel streams.
type StreamManager struct {
	streams   sync.Map // uint32 → *Stream
	nextID    atomic.Uint32
	chunkSize int
	outQueue  chan *crypto.Chunk // shared output queue for interleaving engine
}

// NewStreamManager creates a new stream manager.
func NewStreamManager(chunkSize int, outQueueSize int) *StreamManager {
	if chunkSize <= 0 {
		chunkSize = crypto.MaxPayloadSize
	}
	if outQueueSize <= 0 {
		outQueueSize = 1024
	}
	return &StreamManager{
		chunkSize: chunkSize,
		outQueue:  make(chan *crypto.Chunk, outQueueSize),
	}
}

// NewStream creates and registers a new tunnel stream.
func (sm *StreamManager) NewStream(ctx context.Context, destination string) *Stream {
	id := sm.nextID.Add(1)
	sctx, cancel := context.WithCancel(ctx)

	s := &Stream{
		ID:          id,
		Destination: destination,
		State:       StateConnecting,
		splitter:    NewSplitter(id, sm.chunkSize),
		assembler:   NewAssembler(),
		outQueue:    sm.outQueue,
		inData:      make(chan []byte, 256),
		ctx:         sctx,
		cancel:      cancel,
	}

	sm.streams.Store(id, s)

	// Send CONNECT chunk
	connectChunk := &crypto.Chunk{
		StreamID: id,
		Sequence: 0,
		Type:     crypto.ChunkConnect,
		Payload:  []byte(destination),
	}
	s.outQueue <- connectChunk

	return s
}

// GetStream retrieves a stream by ID.
func (sm *StreamManager) GetStream(id uint32) (*Stream, bool) {
	v, ok := sm.streams.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Stream), true
}

// RemoveStream removes a stream from the manager.
func (sm *StreamManager) RemoveStream(id uint32) {
	sm.streams.Delete(id)
}

// OutQueue returns the shared output queue for the interleaving engine.
func (sm *StreamManager) OutQueue() <-chan *crypto.Chunk {
	return sm.outQueue
}

// ActiveCount returns the number of active streams.
func (sm *StreamManager) ActiveCount() int {
	count := 0
	sm.streams.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Write sends data through the tunnel stream.
func (s *Stream) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.State == StateClosed || s.State == StateClosing {
		return 0, errors.New("stream closed")
	}

	s.State = StateEstablished
	chunks := s.splitter.Split(data)

	// Flush any remaining partial data — without this, small writes
	// (< chunkSize) would sit in the buffer indefinitely, blocking
	// interactive traffic like HTTP requests through the tunnel.
	if flush := s.splitter.Flush(); flush != nil {
		chunks = append(chunks, flush)
	}

	for _, ch := range chunks {
		select {
		case s.outQueue <- ch:
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		}
	}
	return len(data), nil
}

// Read reads reassembled data from the stream.
func (s *Stream) Read(buf []byte) (int, error) {
	select {
	case data, ok := <-s.inData:
		if !ok {
			return 0, errors.New("stream closed")
		}
		n := copy(buf, data)
		return n, nil
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	}
}

// DeliverResponse delivers a response chunk from the relay.
func (s *Stream) DeliverResponse(ch *crypto.Chunk) {
	segments := s.assembler.Insert(ch.Sequence, ch.Payload)
	for _, seg := range segments {
		select {
		case s.inData <- seg:
		case <-s.ctx.Done():
			return
		}
	}
}

// Close sends FIN and closes the stream.
func (s *Stream) Close() error {
	s.mu.Lock()
	if s.State == StateClosed {
		s.mu.Unlock()
		return nil
	}
	s.State = StateClosing
	s.mu.Unlock()

	// Send FIN chunk
	fin := &crypto.Chunk{
		StreamID: s.ID,
		Sequence: s.splitter.NextSequence(),
		Type:     crypto.ChunkFIN,
	}

	// Flush any remaining data
	if flush := s.splitter.Flush(); flush != nil {
		select {
		case s.outQueue <- flush:
		case <-s.ctx.Done():
		}
	}

	select {
	case s.outQueue <- fin:
	case <-s.ctx.Done():
	}

	s.mu.Lock()
	s.State = StateClosed
	s.mu.Unlock()

	s.cancel()
	close(s.inData)
	return nil
}

// Done returns a channel that is closed when the stream is done.
func (s *Stream) Done() <-chan struct{} {
	return s.ctx.Done()
}
