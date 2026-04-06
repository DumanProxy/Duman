package tunnel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/dumanproxy/duman/internal/crypto"
)

// ExitEngine manages outbound connections to the internet.
type ExitEngine struct {
	connections sync.Map // streamID → net.Conn
	respQueue   chan *crypto.Chunk
	dialer      *net.Dialer
	maxIdle     time.Duration
	logger      *slog.Logger
	mu          sync.Mutex
}

// NewExitEngine creates a new exit engine.
func NewExitEngine(logger *slog.Logger, maxIdleSecs int, respQueueSize int) *ExitEngine {
	if maxIdleSecs <= 0 {
		maxIdleSecs = 300
	}
	if respQueueSize <= 0 {
		respQueueSize = 4096
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ExitEngine{
		respQueue: make(chan *crypto.Chunk, respQueueSize),
		dialer:    &net.Dialer{Timeout: 10 * time.Second},
		maxIdle:   time.Duration(maxIdleSecs) * time.Second,
		logger:    logger,
	}
}

// ProcessChunk handles an incoming tunnel chunk.
func (e *ExitEngine) ProcessChunk(ctx context.Context, ch *crypto.Chunk) error {
	switch ch.Type {
	case crypto.ChunkConnect:
		return e.handleConnect(ctx, ch)
	case crypto.ChunkData:
		return e.handleData(ch)
	case crypto.ChunkFIN:
		return e.handleFIN(ch)
	case crypto.ChunkDNSResolve:
		return e.handleDNS(ch)
	default:
		return fmt.Errorf("unknown chunk type: %d", ch.Type)
	}
}

// RespQueue returns the response chunk queue.
func (e *ExitEngine) RespQueue() <-chan *crypto.Chunk {
	return e.respQueue
}

func (e *ExitEngine) handleConnect(ctx context.Context, ch *crypto.Chunk) error {
	dest := string(ch.Payload)
	e.logger.Debug("exit: connect", "stream", ch.StreamID, "dest", dest)

	conn, err := e.dialer.DialContext(ctx, "tcp", dest)
	if err != nil {
		return fmt.Errorf("dial %s: %w", dest, err)
	}

	e.connections.Store(ch.StreamID, conn)

	// Start read loop for response data
	go e.readLoop(ctx, ch.StreamID, conn)

	return nil
}

func (e *ExitEngine) handleData(ch *crypto.Chunk) error {
	v, ok := e.connections.Load(ch.StreamID)
	if !ok {
		return fmt.Errorf("no connection for stream %d", ch.StreamID)
	}
	conn := v.(net.Conn)

	_, err := conn.Write(ch.Payload)
	return err
}

func (e *ExitEngine) handleFIN(ch *crypto.Chunk) error {
	v, ok := e.connections.LoadAndDelete(ch.StreamID)
	if !ok {
		return nil // already closed
	}
	conn := v.(net.Conn)
	return conn.Close()
}

func (e *ExitEngine) handleDNS(ch *crypto.Chunk) error {
	domain := string(ch.Payload)
	e.logger.Debug("exit: dns resolve", "domain", domain)

	ips, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("dns resolve %s: %w", domain, err)
	}

	var result string
	if len(ips) > 0 {
		result = ips[0]
	}

	resp := &crypto.Chunk{
		StreamID: ch.StreamID,
		Sequence: ch.Sequence,
		Type:     crypto.ChunkDNSResolve,
		Payload:  []byte(result),
	}

	select {
	case e.respQueue <- resp:
	default:
		e.logger.Warn("exit: response queue full, dropping DNS response")
	}

	return nil
}

func (e *ExitEngine) readLoop(ctx context.Context, streamID uint32, conn net.Conn) {
	defer func() {
		e.connections.Delete(streamID)
		conn.Close()
	}()

	buf := make([]byte, crypto.MaxPayloadSize)
	var seq uint64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(e.maxIdle))
		n, err := conn.Read(buf)
		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buf[:n])

			resp := &crypto.Chunk{
				StreamID: streamID,
				Sequence: seq,
				Type:     crypto.ChunkData,
				Payload:  payload,
			}
			seq++

			select {
			case e.respQueue <- resp:
			case <-ctx.Done():
				return
			}
		}

		if err != nil {
			if err != io.EOF {
				e.logger.Debug("exit: read error", "stream", streamID, "err", err)
			}
			// Send FIN response
			fin := &crypto.Chunk{
				StreamID: streamID,
				Sequence: seq,
				Type:     crypto.ChunkFIN,
			}
			select {
			case e.respQueue <- fin:
			default:
			}
			return
		}
	}
}

// CloseAll closes all active connections.
func (e *ExitEngine) CloseAll() {
	e.connections.Range(func(key, value any) bool {
		conn := value.(net.Conn)
		conn.Close()
		e.connections.Delete(key)
		return true
	})
}

// ActiveConnections returns the number of active outbound connections.
func (e *ExitEngine) ActiveConnections() int {
	count := 0
	e.connections.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
