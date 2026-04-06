package pgwire

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
)

// Notification represents an async notification to be sent to a client.
type Notification struct {
	Channel string
	Payload string
}

// NotificationBroker manages LISTEN/NOTIFY subscriptions across connections.
type NotificationBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan<- Notification]struct{} // channel → set of subscriber chans
}

// NewNotificationBroker creates a new broker.
func NewNotificationBroker() *NotificationBroker {
	return &NotificationBroker{
		subscribers: make(map[string]map[chan<- Notification]struct{}),
	}
}

// Subscribe registers a subscriber for a given channel.
func (b *NotificationBroker) Subscribe(channel string, ch chan<- Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[channel] == nil {
		b.subscribers[channel] = make(map[chan<- Notification]struct{})
	}
	b.subscribers[channel][ch] = struct{}{}
}

// Unsubscribe removes a subscriber from all channels.
func (b *NotificationBroker) Unsubscribe(ch chan<- Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for channel, subs := range b.subscribers {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(b.subscribers, channel)
		}
	}
}

// Notify sends a notification to all subscribers of a channel.
// Non-blocking: if a subscriber's channel is full, the notification is dropped.
func (b *NotificationBroker) Notify(channel, payload string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := Notification{Channel: channel, Payload: payload}
	for ch := range b.subscribers[channel] {
		select {
		case ch <- n:
		default:
			// Drop if subscriber is slow
		}
	}
}

// QueryHandler processes incoming queries from clients.
type QueryHandler interface {
	HandleSimpleQuery(query string) (*QueryResult, error)
	HandleParse(name, query string, paramOIDs []int32) error
	HandleBind(portal, stmt string, params [][]byte) error
	HandleExecute(portal string, maxRows int32) (*QueryResult, error)
	HandleDescribe(objectType byte, name string) (*QueryResult, error)
}

// QueryResult represents a query execution result.
type QueryResult struct {
	Type    ResultType
	Columns []ColumnDef
	Rows    [][][]byte // each row is slice of column values (nil = NULL)
	Tag     string     // "SELECT 20", "INSERT 0 1", etc.
	Error   *ErrorDetail
}

// ResultType identifies the kind of result.
type ResultType int

const (
	ResultRows    ResultType = iota // SELECT: columns + rows
	ResultCommand                   // INSERT/UPDATE/DELETE: tag only
	ResultError                     // error response
	ResultEmpty                     // empty query
)

// ErrorDetail holds PostgreSQL error info.
type ErrorDetail struct {
	Severity string // "ERROR", "FATAL"
	Code     string // SQLSTATE code
	Message  string
}

// ServerConfig configures the PgWire server.
type ServerConfig struct {
	ListenAddr    string
	TLSConfig     *tls.Config
	Auth          *MD5Auth
	QueryHandler  QueryHandler
	ServerVersion string // "16.2"
	MaxConns      int
	Logger        *slog.Logger
	Broker        *NotificationBroker // optional: enables LISTEN/NOTIFY push mode
}

// Server accepts PostgreSQL wire protocol connections.
type Server struct {
	config     ServerConfig
	listenerMu sync.Mutex
	listener   net.Listener
	wg         sync.WaitGroup
	logger     *slog.Logger
}

// NewServer creates a new PgWire server.
func NewServer(config ServerConfig) *Server {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if config.ServerVersion == "" {
		config.ServerVersion = "16.2"
	}
	if config.MaxConns == 0 {
		config.MaxConns = 100
	}
	return &Server{config: config, logger: logger}
}

// ListenAndServe starts listening for connections.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listenerMu.Lock()
	s.listener = ln
	s.listenerMu.Unlock()

	s.logger.Info("PostgreSQL server listening", "addr", s.config.ListenAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
				s.logger.Error("accept error", "err", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// Addr returns the listener address (useful for tests with port 0).
func (s *Server) Addr() net.Addr {
	s.listenerMu.Lock()
	ln := s.listener
	s.listenerMu.Unlock()
	if ln == nil {
		return nil
	}
	return ln.Addr()
}

// Broker returns the server's notification broker (may be nil if push mode is disabled).
func (s *Server) Broker() *NotificationBroker {
	return s.config.Broker
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReaderSize(conn, 32*1024)
	bw := bufio.NewWriterSize(conn, 32*1024)

	// 1. Read startup message
	startupMsg, err := ReadMessage(br, true)
	if err != nil {
		s.logger.Debug("startup read error", "err", err)
		return
	}

	params, err := ParseStartupMessage(startupMsg.Payload)
	if err != nil {
		s.logger.Debug("startup parse error", "err", err)
		return
	}

	// Handle SSL request
	if _, isSSL := params["__ssl"]; isSSL {
		if s.config.TLSConfig != nil {
			// Accept SSL
			bw.Write([]byte{'S'})
			bw.Flush()

			tlsConn := tls.Server(conn, s.config.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				s.logger.Debug("TLS handshake error", "err", err)
				return
			}

			// Re-read startup after TLS
			br = bufio.NewReaderSize(tlsConn, 32*1024)
			bw = bufio.NewWriterSize(tlsConn, 32*1024)

			startupMsg, err = ReadMessage(br, true)
			if err != nil {
				return
			}
			params, err = ParseStartupMessage(startupMsg.Payload)
			if err != nil {
				return
			}
		} else {
			// Reject SSL
			bw.Write([]byte{'N'})
			bw.Flush()

			// Client should send regular startup
			startupMsg, err = ReadMessage(br, true)
			if err != nil {
				return
			}
			params, err = ParseStartupMessage(startupMsg.Payload)
			if err != nil {
				return
			}
		}
	}

	username := params["user"]
	database := params["database"]
	s.logger.Debug("client connecting", "user", username, "database", database)

	// 2. MD5 Authentication
	if s.config.Auth != nil {
		salt, err := s.config.Auth.GenerateSalt()
		if err != nil {
			s.logger.Error("salt generation error", "err", err)
			return
		}

		// Send AuthenticationMD5Password
		WriteMessage(bw, MsgAuthentication, BuildAuthMD5(salt))
		bw.Flush()

		// Read password response
		pwMsg, err := ReadMessage(br, false)
		if err != nil || pwMsg.Type != MsgPassword {
			s.logger.Debug("no password response")
			return
		}

		// Password is null-terminated string
		pwResponse := strings.TrimRight(string(pwMsg.Payload), "\x00")

		if !s.config.Auth.Verify(username, pwResponse, salt) {
			errPayload := BuildErrorResponse("FATAL", "28P01",
				fmt.Sprintf("password authentication failed for user \"%s\"", username))
			WriteMessage(bw, MsgErrorResponse, errPayload)
			bw.Flush()
			return
		}
	}

	// 3. Send AuthenticationOK
	WriteMessage(bw, MsgAuthentication, BuildAuthOK())

	// 4. Send ParameterStatus sequence
	paramStatuses := []struct{ name, value string }{
		{"server_version", s.config.ServerVersion},
		{"server_encoding", "UTF8"},
		{"client_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"TimeZone", "UTC"},
		{"integer_datetimes", "on"},
		{"standard_conforming_strings", "on"},
	}
	for _, ps := range paramStatuses {
		WriteMessage(bw, MsgParameterStatus, BuildParameterStatus(ps.name, ps.value))
	}

	// 5. Send BackendKeyData
	WriteMessage(bw, MsgBackendKeyData, BuildBackendKeyData(12345, 0x1234ABCD))

	// 6. Send ReadyForQuery
	WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(TxIdle))
	bw.Flush()

	// 7. Query loop with async notification support
	txStatus := TxIdle

	// Notification channel for push mode (buffered to avoid blocking the broker)
	notifyCh := make(chan Notification, 64)
	defer func() {
		if s.config.Broker != nil {
			s.config.Broker.Unsubscribe(notifyCh)
		}
	}()

	// Read messages from client in a goroutine so we can select on notifications
	type msgOrErr struct {
		msg *Message
		err error
	}
	msgCh := make(chan msgOrErr, 1)

	readNext := func() {
		msg, err := ReadMessage(br, false)
		msgCh <- msgOrErr{msg, err}
	}
	go readNext()

	const connPID int32 = 12345

	for {
		// Flush any buffered responses before blocking for the next message.
		// During pipelining, msgCh already has the next message queued (from
		// the readNext goroutine), so the select below won't block and we skip
		// this flush — responses accumulate until Sync. When the pipeline is
		// drained, msgCh is empty and we flush here before blocking.
		if bw.Buffered() > 0 {
			bw.Flush()
		}

		select {
		case <-ctx.Done():
			return

		case n := <-notifyCh:
			// Send async NotificationResponse to the client
			payload := BuildNotificationResponse(connPID, n.Channel, n.Payload)
			WriteMessage(bw, MsgNotificationResp, payload)
			bw.Flush()

		case me := <-msgCh:
			if me.err != nil {
				return
			}
			msg := me.msg

			switch msg.Type {
			case MsgQuery:
				query := ParseQuery(msg.Payload)

				// Detect LISTEN and register with broker
				upper := strings.ToUpper(strings.TrimSpace(query))
				if strings.HasPrefix(upper, "LISTEN ") && s.config.Broker != nil {
					channel := strings.TrimSpace(query[7:])
					if idx := strings.IndexByte(channel, ';'); idx >= 0 {
						channel = channel[:idx]
					}
					channel = strings.TrimSpace(channel)
					s.config.Broker.Subscribe(channel, notifyCh)
				}

				s.handleSimpleQuery(bw, query, txStatus)

			case MsgParse:
				stmtName, query, paramOIDs, err := ParseParse(msg.Payload)
				if err != nil {
					s.sendError(bw, "ERROR", "42000", err.Error())
					WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
					bw.Flush()
					go readNext()
					continue
				}
				if s.config.QueryHandler != nil {
					if err := s.config.QueryHandler.HandleParse(stmtName, query, paramOIDs); err != nil {
						s.sendError(bw, "ERROR", "42000", err.Error())
					} else {
						WriteMessage(bw, MsgParseComplete, nil)
					}
				} else {
					WriteMessage(bw, MsgParseComplete, nil)
				}
				// No explicit flush — handled by flush-before-block at top of loop.

			case MsgBind:
				portal, stmt, params, err := ParseBind(msg.Payload)
				if err != nil {
					s.sendError(bw, "ERROR", "42000", err.Error())
					bw.Flush()
					go readNext()
					continue
				}
				if s.config.QueryHandler != nil {
					if err := s.config.QueryHandler.HandleBind(portal, stmt, params); err != nil {
						s.sendError(bw, "ERROR", "42000", err.Error())
					} else {
						WriteMessage(bw, MsgBindComplete, nil)
					}
				} else {
					WriteMessage(bw, MsgBindComplete, nil)
				}
				// No explicit flush — handled by flush-before-block at top of loop.

			case MsgExecute:
				portal := ""
				if len(msg.Payload) > 0 {
					for i, b := range msg.Payload {
						if b == 0 {
							portal = string(msg.Payload[:i])
							break
						}
					}
				}
				if s.config.QueryHandler != nil {
					result, err := s.config.QueryHandler.HandleExecute(portal, 0)
					if err != nil {
						s.sendError(bw, "ERROR", "42000", err.Error())
					} else if result != nil {
						s.sendResult(bw, result)
					}
				}
				// No explicit flush — handled by flush-before-block at top of loop.

			case MsgSync:
				WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
				bw.Flush() // Sync always flushes immediately (protocol requirement).

			case MsgDescribe:
				if s.config.QueryHandler != nil && len(msg.Payload) > 0 {
					objectType := msg.Payload[0]
					name := ""
					if len(msg.Payload) > 1 {
						name = strings.TrimRight(string(msg.Payload[1:]), "\x00")
					}
					result, err := s.config.QueryHandler.HandleDescribe(objectType, name)
					if err != nil {
						s.sendError(bw, "ERROR", "42000", err.Error())
					} else if result != nil && result.Type == ResultRows && len(result.Columns) > 0 {
						WriteMessage(bw, MsgRowDescription, BuildRowDescription(result.Columns))
					} else {
						WriteMessage(bw, MsgNoData, nil)
					}
				} else {
					WriteMessage(bw, MsgNoData, nil)
				}
				// No explicit flush — handled by flush-before-block at top of loop.

			case MsgClose:
				WriteMessage(bw, MsgCloseComplete, nil)
				// No explicit flush — handled by flush-before-block at top of loop.

			case MsgFlush:
				bw.Flush()

			case MsgTerminate:
				return

			default:
				// Unknown message type — silently ignore for probe resistance
				s.logger.Debug("unknown message type", "type", msg.Type)
			}

			// Start reading the next message
			go readNext()
		}
	}
}

func (s *Server) handleSimpleQuery(bw *bufio.Writer, query string, txStatus byte) {
	query = strings.TrimSpace(query)

	if query == "" {
		WriteMessage(bw, MsgEmptyQuery, nil)
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
		bw.Flush()
		return
	}

	// Handle BEGIN/COMMIT/ROLLBACK for transaction tracking
	upper := strings.ToUpper(query)
	if upper == "BEGIN" || upper == "BEGIN;" {
		WriteMessage(bw, MsgCommandComplete, BuildCommandComplete("BEGIN"))
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(TxInTx))
		bw.Flush()
		return
	}
	if upper == "COMMIT" || upper == "COMMIT;" {
		WriteMessage(bw, MsgCommandComplete, BuildCommandComplete("COMMIT"))
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(TxIdle))
		bw.Flush()
		return
	}
	if upper == "ROLLBACK" || upper == "ROLLBACK;" {
		WriteMessage(bw, MsgCommandComplete, BuildCommandComplete("ROLLBACK"))
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(TxIdle))
		bw.Flush()
		return
	}

	if s.config.QueryHandler == nil {
		s.sendError(bw, "ERROR", "42601", "no query handler configured")
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
		bw.Flush()
		return
	}

	result, err := s.config.QueryHandler.HandleSimpleQuery(query)
	if err != nil {
		s.sendError(bw, "ERROR", "42000", err.Error())
		WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
		bw.Flush()
		return
	}

	s.sendResult(bw, result)
	WriteMessage(bw, MsgReadyForQuery, BuildReadyForQuery(txStatus))
	bw.Flush()
}

func (s *Server) sendResult(bw *bufio.Writer, result *QueryResult) {
	switch result.Type {
	case ResultRows:
		WriteMessage(bw, MsgRowDescription, BuildRowDescription(result.Columns))
		for _, row := range result.Rows {
			WriteMessage(bw, MsgDataRow, BuildDataRow(row))
		}
		WriteMessage(bw, MsgCommandComplete, BuildCommandComplete(result.Tag))

	case ResultCommand:
		WriteMessage(bw, MsgCommandComplete, BuildCommandComplete(result.Tag))

	case ResultError:
		if result.Error != nil {
			s.sendError(bw, result.Error.Severity, result.Error.Code, result.Error.Message)
		}

	case ResultEmpty:
		WriteMessage(bw, MsgEmptyQuery, nil)
	}
}

func (s *Server) sendError(bw *bufio.Writer, severity, code, message string) {
	WriteMessage(bw, MsgErrorResponse, BuildErrorResponse(severity, code, message))
}
