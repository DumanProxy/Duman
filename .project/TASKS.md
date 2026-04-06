# DUMAN — Steganographic SQL/API Tunnel

## TASKS

**Version:** 1.0.0
**Total Tasks:** 127
**Estimated Timeline:** ~32 weeks (solo developer)
**Prerequisite:** Read SPECIFICATION.md and IMPLEMENTATION.md first.

---

## Task Legend

| Symbol | Meaning |
|---|---|
| `[P]` | Priority: Critical path, blocks other tasks |
| `[S]` | Standard: Important but not blocking |
| `[E]` | Enhancement: Can be deferred |
| `→` | Depends on (must complete first) |
| `~` | Estimated effort in days |

---

## Milestone 1: MVP (v0.1.0) — Single PgSQL Relay + Basic Tunnel

**Goal:** End-to-end tunnel working through a single PostgreSQL relay.
**Tasks:** 34
**Estimated:** ~8 weeks

---

### 1.1 Project Bootstrap

**Task 1** `[P]` `~0.5d`
Initialize Go module and project structure.
Create `go.mod` with module path `github.com/dumanproxy/duman`. Add four allowed dependencies (`golang.org/x/crypto`, `golang.org/x/net`, `golang.org/x/sys`, `gopkg.in/yaml.v3`). Create all directory stubs under `internal/`. Create `cmd/duman-client/main.go` and `cmd/duman-relay/main.go` with placeholder `main()`. Create `Makefile` with `build`, `test`, `clean`, `cross` targets. Verify `go build ./...` passes.

**Task 2** `[P]` `~0.5d`
Implement configuration system.
→ Task 1
Create `internal/config/client.go` with `ClientConfig` struct and YAML parsing via `gopkg.in/yaml.v3`. Create `internal/config/relay.go` with `RelayConfig` struct. Implement `Validate()` on both configs with sensible defaults (listen `127.0.0.1:1080`, scenario `ecommerce`, chunk size `16384`). Create `configs/duman-client.example.yaml` and `configs/duman-relay.example.yaml` with full comments. Unit test: load example configs, verify defaults, verify validation errors.

**Task 3** `[P]` `~0.5d`
Implement structured logging.
→ Task 1
Create `internal/log/log.go` using Go 1.21+ `log/slog`. Support `json` and `text` formats. Support `debug`, `info`, `warn`, `error` levels. Support output to `stderr`, `stdout`, or file path. Add WARNING comment: debug level logs query content, never use in production. Wire into both client and relay main.go.

---

### 1.2 Crypto Module

**Task 4** `[P]` `~1d`
Implement HKDF key derivation.
→ Task 1
Create `internal/crypto/keys.go`. Implement `DeriveSessionKey(sharedSecret, sessionID)` using HKDF-SHA256 from `golang.org/x/crypto/hkdf`. Implement `DeriveDirectionalKeys(sessionKey)` producing `clientKey` and `relayKey`. Define constants: `KeySize=32`, `NonceSize=12`, `TagSize=16`, `HMACSize=6`. Unit test: verify deterministic output (same input = same key), verify different session IDs produce different keys, verify directional keys are different from each other.

**Task 5** `[P]` `~1d`
Implement cipher with auto-detection.
→ Task 4
Create `internal/crypto/cipher.go`. Implement `NewCipher(key, cipherType)` supporting `CipherAuto`, `CipherChaCha20`, `CipherAES256GCM`. Auto-detect: AES-256-GCM for amd64/arm64, ChaCha20-Poly1305 otherwise. Implement `Seal(dst, plaintext, aad, seq)` and `Open(dst, ciphertext, aad, seq)`. Nonce construction: 4 zero bytes + 8-byte big-endian sequence number. Unit test: encrypt/decrypt roundtrip for both ciphers, verify different sequences produce different ciphertext, verify tampered ciphertext fails, verify AAD mismatch fails.

**Task 6** `[P]` `~1d`
Implement chunk serialization and encryption.
→ Task 5
Create `internal/crypto/chunk.go`. Define `Chunk` struct with `StreamID(uint32)`, `Sequence(uint64)`, `Type(ChunkType)`, `Flags(ChunkFlags)`, `Payload([]byte)`. Define chunk types: `Data`, `Connect`, `DNSResolve`, `FIN`, `ACK`, `WindowUpdate`. Implement `Marshal()` and `UnmarshalChunk(data)` with 16-byte header. Implement `EncryptChunk(chunk, cipher, sessionID)` and `DecryptChunk(ciphertext, cipher, sessionID, streamID, seq)`. AAD = sessionID + streamID + sequence. Unit test: marshal/unmarshal roundtrip, encrypt/decrypt roundtrip, max payload size validation, invalid data handling.

**Task 7** `[P]` `~0.5d`
Implement HMAC tunnel authentication.
→ Task 4
Create `internal/crypto/auth.go`. Implement `GenerateAuthToken(sharedSecret, sessionID)` producing `px_` + 12-char hex HMAC (looks like tracking pixel ID). Implement `VerifyAuthToken(token, sharedSecret, sessionID)` with 30-second window tolerance (current + previous window). Unit test: generate and verify token, verify expired token fails, verify wrong secret fails, verify token format matches `px_[0-9a-f]{12}`.

**Task 8** `[S]` `~0.5d`
Implement keygen CLI command.
→ Task 4
Add `keygen` subcommand to both client and relay CLI. Generate 32 cryptographically random bytes using `crypto/rand`. Output as base64 string suitable for config `shared_secret` field. Print to stdout with copy-paste friendly format.

---

### 1.3 PostgreSQL Wire Protocol

**Task 9** `[P]` `~2d`
Implement PgWire message serialization.
→ Task 1
Create `internal/pgwire/messages.go`. Define all frontend (7) and backend (11) message type constants. Implement `ReadMessage(reader, isStartup)` with length validation (4 bytes min, 64MB max). Implement `WriteMessage(writer, type, payload)`. Implement payload builders: `BuildRowDescription`, `BuildDataRow`, `BuildCommandComplete`, `BuildErrorResponse`, `BuildReadyForQuery`, `BuildParameterStatus`, `BuildBackendKeyData`, `BuildNotificationResponse`. Define `ColumnDef` struct and all common PostgreSQL type OIDs (int4, int8, float8, text, varchar, timestamptz, bool, numeric, bytea, jsonb, uuid). Unit test: roundtrip serialization for every message type, NULL handling in DataRow, multi-column RowDescription, error response with SQLSTATE codes.

**Task 10** `[P]` `~1.5d`
Implement PgWire MD5 authentication.
→ Task 9
Create `internal/pgwire/auth.go`. Implement MD5Auth: `GenerateSalt()` (4 random bytes), `Verify(username, response, salt)` computing `md5(md5(password + username) + salt)`. Unit test: verify against known PostgreSQL MD5 hash values, verify wrong password fails, verify unknown user fails.

**Task 11** `[P]` `~3d`
Implement PgWire server (relay-side).
→ Task 9, Task 10
Create `internal/pgwire/server.go`. Implement `Server` struct with `ListenAndServe(ctx)`. Handle connection lifecycle: accept → SSLRequest detection → TLS upgrade → read startup message → parse parameters (username, database) → MD5 authentication → send AuthOK + ParameterStatus sequence (server_version, server_encoding, client_encoding, DateStyle, TimeZone, integer_datetimes, standard_conforming_strings) + BackendKeyData + ReadyForQuery. Implement query loop: dispatch `MsgQuery` (simple query), `MsgParse`/`MsgBind`/`MsgExecute`/`MsgSync` (extended query), `MsgTerminate`, `MsgDescribe`. Define `QueryHandler` interface. Send proper results via `sendResult()` (rows, command, error, empty). Handle unknown message types silently (probe resistance). Goroutine-per-connection with `context.Context` cancellation. 32KB buffered reader/writer. Integration test: connect with real `psql` binary, verify auth works, verify ParameterStatus matches real PostgreSQL.

**Task 12** `[P]` `~2d`
Implement PgWire client (client-side).
→ Task 9, Task 10
Create `internal/pgwire/client.go`. Implement `Connect(ctx, config)` with TLS, startup message, MD5 auth, readUntilReady. Implement `SimpleQuery(query)` returning `QueryResult`. Implement `Prepare(name, query)` registering prepared statement. Implement `PreparedInsert(stmtName, params)` for fast binary tunnel chunk delivery. Implement `Listen(channel)` and `ReadNotification(ctx)` for push-mode response channel. Thread-safe with mutex. Integration test: connect client to server, send query, verify response matches.

---

### 1.4 Tunnel Stream Management

**Task 13** `[P]` `~1d`
Implement chunk splitter.
→ Task 6
Create `internal/tunnel/splitter.go`. Implement `Splitter` with configurable chunk size. `Split(data)` returns zero or more complete chunks, buffering partial data. `Flush()` returns remaining buffered data. Unit test: exact chunk size input, multi-chunk input, partial buffering across calls, empty input, flush with and without pending data.

**Task 14** `[P]` `~1d`
Implement chunk assembler (reorder buffer).
→ Task 6
Create `internal/tunnel/assembler.go`. Implement `Assembler` with expected sequence tracking. `Insert(seq, data)` returns in-order segments: if seq matches expected, deliver and flush consecutive buffered chunks; if seq > expected, buffer; if seq < expected, ignore (duplicate). Max gap = 1000 chunks. Unit test: in-order delivery, out-of-order with reordering, duplicate handling, gap exceeding max, consecutive flush after gap fill.

**Task 15** `[P]` `~2d`
Implement stream manager.
→ Task 13, Task 14, Task 6
Create `internal/tunnel/stream.go`. Implement `StreamManager` managing all active streams with `sync.Map`. Implement `NewStream(ctx, destination)` creating stream, sending CONNECT chunk. Implement `Stream` with `Write(data)` (split → chunk → queue), `Read(buf)` (from assembler), `DeliverResponse(chunk)`, `Close()` (send FIN). Stream states: Connecting, Established, Closing, Closed. Output queue channel for interleaving engine consumption. Unit test: create stream, write data, verify chunks produced, deliver response chunks, verify reassembly, close stream.

**Task 16** `[P]` `~1.5d`
Implement exit engine (relay-side).
→ Task 6
Create `internal/tunnel/exit.go`. Implement `ExitEngine` with outbound connection pool. Handle chunk types: `Connect` (dial destination, start readLoop), `Data` (forward to connection), `FIN` (close connection), `DNSResolve` (net.LookupHost, return result). `readLoop` goroutine reads from destination, creates response chunks, queues to respQueue. Unit test: mock destination server, verify connect/data/fin lifecycle, verify DNS resolution, verify response chunk generation.

---

### 1.5 Basic Fake Data Engine

**Task 17** `[P]` `~1d`
Implement simple SQL parser.
→ Task 1
Create `internal/fakedata/parser.go`. Implement regex-based parser recognizing: query type (SELECT/INSERT/UPDATE/DELETE/META), table name, WHERE conditions as key-value map, LIMIT, ORDER BY, COUNT(*), JOIN detection, destructive query detection (DROP/TRUNCATE/ALTER). Implement `isMetaQuery()` detecting psql commands (`pg_catalog`, `information_schema`, `SHOW`, `SELECT version()`). Unit test: parse all ~15 e-commerce query patterns from Real Query Engine, verify table extraction, verify WHERE parameter extraction, verify meta query detection.

**Task 18** `[P]` `~2d`
Implement fake data generator (e-commerce scenario).
→ Task 17
Create `internal/fakedata/generator.go` and `internal/fakedata/seed.go`. Generate from deterministic RNG (seed-based): 10 categories with realistic names, 200 products with realistic names/prices per category (Electronics: "Sony WH-1000XM5 Headphones" at $299.99), 100 users with names/emails, 50 initial orders. Implement `generateProductName(category, rng)` with per-category name templates. Implement `generatePrice(category, rng)` with realistic price ranges per category. Unit test: same seed produces identical data, product names are realistic, prices match category ranges.

**Task 19** `[P]` `~2d`
Implement fake data engine query execution.
→ Task 17, Task 18
Create `internal/fakedata/engine.go`. Implement `Execute(query)` dispatching to pattern matchers. Cover ~15 patterns for e-commerce: `SELECT FROM products WHERE category_id`, `SELECT FROM products WHERE id`, `SELECT count(*) FROM products`, `INSERT INTO cart_items`, `SELECT FROM cart_items`, `INSERT INTO orders`, `SELECT count(*) FROM orders`, `INSERT INTO analytics_events` (acknowledge), `SELECT FROM analytics_responses` (tunnel response), `UPDATE SET WHERE`. Handle destructive queries with `42501 permission denied`. Handle unknown queries with empty result set. Return proper `QueryResult` with columns and rows. Unit test: execute every supported pattern, verify column types, verify row counts, verify error responses.

**Task 20** `[P]` `~1d`
Implement schema metadata (psql compatibility).
→ Task 18, Task 19
Create `internal/fakedata/schema.go`. Implement responses for: `\dt` (list tables — 10 tables with proper Schema/Name/Type/Owner columns), `\d <table>` (describe table with Column/Type/Nullable/Default), `SELECT version()`, `SHOW server_version`, `SHOW timezone`, and common `pg_catalog` queries that psql/DBeaver issue on connect. Unit test: verify `\dt` returns all scenario tables, verify `\d products` returns correct column definitions.

---

### 1.6 Basic Interleaving

**Task 21** `[P]` `~1d`
Implement basic Real Query Engine (e-commerce only).
→ Task 1
Create `internal/realquery/engine.go`, `internal/realquery/state.go`, `internal/realquery/ecommerce.go`. Implement `AppStateMachine` with page navigation (products → product detail → cart → checkout). Implement `BrowseProducts()`, `ViewProduct()`, `AddToCart()`, `Checkout()` query batches. Implement `RandomAnalyticsEvent()` and `RandomBackgroundQuery()`. Implement basic timing: burst spacing 15-25ms, reading pause 2-30s. Unit test: verify state transitions produce valid page paths, verify query SQL syntax is valid, verify burst sizes.

**Task 22** `[P]` `~2d`
Implement interleaving engine.
→ Task 21, Task 15, Task 12
Create `internal/interleave/engine.go` and `internal/interleave/ratio.go`. Implement `Engine.Run(ctx)` with burst phase + reading phase loop. Burst phase: send cover queries from Real Query Engine, inject tunnel chunks every N cover queries from tunnel queue. Reading phase: background analytics/metric writes, inject tunnel chunks when available. Fixed 3:1 cover-to-tunnel ratio for MVP. When no tunnel data pending, send cover analytics instead (never leave timing gap). Unit test: verify cover-to-tunnel ratio, verify no timing gaps, verify tunnel chunks sent as analytics INSERT format.

---

### 1.7 Provider Layer

**Task 23** `[P]` `~1d`
Implement provider interface and PostgreSQL provider.
→ Task 12
Create `internal/provider/provider.go` with `Provider` interface (Connect, SendQuery, SendTunnelInsert, FetchResponses, Close, Type, IsHealthy). Create `internal/provider/pg_provider.go` implementing the interface using PgWire client. `SendTunnelInsert` builds analytics INSERT with HMAC auth token in metadata.pixel_id and encrypted payload as BYTEA $6 parameter via `PreparedInsert`. `FetchResponses` sends `SELECT payload FROM analytics_responses WHERE session_id = $1 AND consumed = FALSE ORDER BY seq ASC LIMIT 50`. Unit test: mock PgWire server, verify provider sends correct queries, verify tunnel INSERT format.

**Task 24** `[P]` `~0.5d`
Implement provider manager.
→ Task 23
Create `internal/provider/manager.go`. Implement weighted random selection with health checking. Implement `ConnectAll()` with staggered timing (30s-2m between connections). Implement `Select()` skipping unhealthy providers. Unit test: verify weighted selection distribution, verify unhealthy provider skipped.

---

### 1.8 SOCKS5 Proxy

**Task 25** `[P]` `~2d`
Implement SOCKS5 proxy server.
→ Task 15
Create `internal/proxy/socks5.go`. Implement RFC 1928 SOCKS5: version negotiation, no-auth method, CONNECT command, IPv4/domain/IPv6 address types. On CONNECT: create tunnel stream via StreamManager, bidirectional proxy (app ↔ stream) with goroutine pair. Bind to 127.0.0.1 only. Unit test: connect with `curl --socks5`, verify bidirectional data flow.

---

### 1.9 DNS Resolution

**Task 26** `[P]` `~1d`
Implement remote DNS resolver.
→ Task 15, Task 16
Create `internal/tunnel/dns.go`. Implement `RemoteDNSResolver` sending DNS queries as `ChunkDNSResolve` type through tunnel. Implement DNS cache with 5-minute TTL. Client sends domain name, relay resolves via `net.LookupHost`, returns IP. Unit test: resolve domain through mock relay, verify caching, verify cache expiry.

---

### 1.10 Relay Integration

**Task 27** `[P]` `~2d`
Implement relay query handler (bridges fake data + tunnel).
→ Task 11, Task 19, Task 16, Task 7
Create the `QueryHandler` implementation that bridges PgWire server with FakeData engine and Tunnel engine. For each incoming query: parse metadata JSON from INSERT parameters, check HMAC via `VerifyAuthToken`. If valid tunnel: extract BYTEA payload, decrypt chunk, pass to ExitEngine. If cover: pass to FakeDataEngine, return result. For response polling (`SELECT FROM analytics_responses`): return queued response chunks from ExitEngine.respQueue. For LISTEN: register client for push notifications. Integration test: full relay accepts connection, handles cover queries with fake data, handles tunnel queries with exit.

**Task 28** `[P]` `~1d`
Implement relay response queue.
→ Task 27
Implement in-memory ring buffer for response chunks per session. Max 1000 chunks, 5-minute TTL per chunk. Support polling (SELECT query) and push (generate NotificationResponse). Thread-safe with mutex. Unit test: queue/dequeue lifecycle, TTL expiry, overflow handling.

---

### 1.11 Client Integration

**Task 29** `[P]` `~2d`
Implement client main orchestrator.
→ Task 22, Task 24, Task 25, Task 26, Task 2, Task 3
Wire everything together in `cmd/duman-client/main.go`. Startup sequence: load config → init crypto → connect to relay(s) with provider manager → seed cover state → start Real Query Engine → start Interleaving Engine → start SOCKS5 proxy → ready. Graceful shutdown on SIGINT/SIGTERM via context cancellation. Integration test: start client, connect with browser via SOCKS5, verify traffic flows through relay.

**Task 30** `[P]` `~1d`
Implement relay main orchestrator.
→ Task 27, Task 28, Task 2, Task 3
Wire everything in `cmd/duman-relay/main.go`. Startup sequence: load config → init TLS (self-signed for MVP, ACME in v0.3) → generate fake data → start PgWire server → start exit engine → accept connections. Graceful shutdown. Integration test: start relay, connect with psql, verify fake data responses.

---

### 1.12 CLI & Testing

**Task 31** `[S]` `~1d`
Implement CLI commands for client and relay.
→ Task 29, Task 30
Implement `start` (foreground), `stop` (signal daemon), `status` (print connection info), `keygen`, `version` for both binaries. Use `flag` package (stdlib). Parse `-c/--config` and `-v/--verbose` flags.

**Task 32** `[S]` `~1d`
Write end-to-end integration test.
→ Task 29, Task 30
Create `test/integration/e2e_test.go`. Start relay in goroutine. Start client in goroutine pointing to relay. Connect to SOCKS5 proxy. Make HTTP request to a mock destination server. Verify request arrives at destination. Verify response returns to client. Verify cover queries were sent alongside tunnel. This is the critical MVP acceptance test.

**Task 33** `[S]` `~1d`
Write PgWire protocol conformance test.
→ Task 11
Create `test/conformance/pgwire_test.go`. Connect to relay with real `psql` or `pgx` Go driver. Verify: `\dt` returns tables, `\d products` returns columns, `SELECT * FROM products LIMIT 5` returns rows, `SELECT count(*) FROM products` returns 200, `DROP TABLE products` returns permission denied, `SELECT version()` returns "PostgreSQL 16.2...". Compare message bytes with real PostgreSQL where possible.

**Task 34** `[S]` `~0.5d`
Create example configs and README.
→ Task 32
Finalize example config files with full documentation. Write README.md with quick start guide: (1) generate key, (2) configure relay, (3) start relay, (4) configure client, (5) start client, (6) set browser SOCKS5 proxy.

---

## Milestone 2: v0.2.0 — Full Real Query Engine + Stealth

**Goal:** Undetectable traffic patterns with all 5 scenarios.
**Tasks:** 22
**Estimated:** ~5 weeks

---

### 2.1 Additional Scenarios

**Task 35** `[P]` `~1.5d`
Implement IoT dashboard scenario queries.
→ Task 21
Create `internal/realquery/iot.go`. Implement `DeviceOverview()`, `DeviceMetrics()`, `WriteMetric()`, `AlertList()`, `DeviceDetail()`, `FirmwareCheck()` query batches. State machine: dashboard → device list → device detail → metrics → alerts. Background metric writes every 5-15s (IoT pattern). Unit test: verify query SQL, verify state transitions.

**Task 36** `[P]` `~1.5d`
Implement SaaS dashboard scenario queries.
→ Task 21
Create `internal/realquery/saas.go`. Implement `SaaSDashboard()`, `SaaSUserDetail()`, `BillingOverview()`, `UsageStats()`, `OrgManagement()` query batches. State machine: dashboard → user list → user detail → billing → usage. Unit test: verify query SQL.

**Task 37** `[P]` `~1d`
Implement blog/CMS scenario queries.
→ Task 21
Create `internal/realquery/blog.go`. Implement `BlogHomepage()`, `BlogPost()`, `Comments()`, `TagCloud()`, `Search()` query batches. State machine: homepage → post → comments → search → related posts. Unit test: verify query SQL.

**Task 38** `[P]` `~1d`
Implement project management scenario queries.
→ Task 21
Create `internal/realquery/project.go`. Implement `ProjectBoard()`, `TaskDetail()`, `SprintView()`, `TaskHistory()`, `CreateComment()` query batches. State machine: board → task → comments → history → sprint. Unit test: verify query SQL.

---

### 2.2 Enhanced Fake Data

**Task 39** `[P]` `~1.5d`
Extend fake data generator for all scenarios.
→ Task 18
Add generators: IoT devices (30 devices with name/type/status/firmware), metrics ring buffer (24h rolling), alerts. SaaS: organizations, subscriptions, invoices, usage events. Blog: posts with titles/slugs/content, comments, tags, authors. Project: projects, tasks with status/priority/assignee, sprints, task comments, attachments, task history. All deterministic from seed.

**Task 40** `[P]` `~2d`
Extend fake data engine for all scenario query patterns.
→ Task 39, Task 17
Add ~15 additional query patterns per scenario to the SQL parser and engine. Total: ~30 patterns for e-commerce + ~15 IoT + ~15 SaaS + ~10 blog + ~10 project = ~80 patterns. Each pattern returns properly typed columns with realistic data. Unit test: execute every pattern, verify results.

---

### 2.3 Natural Timing

**Task 41** `[P]` `~1d`
Implement timing profiles.
→ Task 22
Create `internal/interleave/timing.go`. Implement 4 named profiles: `casual_browser` (long reads, slow nav), `power_user` (fast clicking, short reads), `api_worker` (rapid automated queries), `dashboard_monitor` (long stares, periodic refresh). Each profile defines: burst size range, burst window, reading pause range, background interval range, nav probability. Make configurable in YAML.

**Task 42** `[P]` `~1d`
Implement adaptive cover-to-tunnel ratio.
→ Task 22
Enhance `internal/interleave/ratio.go`. Monitor tunnel queue depth continuously. Adjust ratio: queue >100 → min ratio (1:1), queue 50-100 → 2:1, queue 10-50 → base (3:1), queue 0 → max ratio (8:1). Smooth transitions (don't jump instantly). Unit test: verify ratio changes at each threshold.

---

### 2.4 Push Mode

**Task 43** `[P]` `~1.5d`
Implement LISTEN/NOTIFY push mode for responses.
→ Task 12, Task 28
Client: send `LISTEN tunnel_resp` after connect. Relay: when response chunk queued, send `NotificationResponse` to listening client. Client: on notification, immediately `SELECT payload FROM analytics_responses`. Achieves 1-5ms response latency vs 50-200ms polling. Make configurable: `tunnel.response_mode: push | poll`. Integration test: verify push latency < 10ms.

**Task 44** `[P]` `~1d`
Implement prepared statement binary mode.
→ Task 12, Task 23
After initial connection, automatically `Prepare` the analytics INSERT statement. All subsequent tunnel chunks use `PreparedInsert` (binary params, zero SQL text). Measure and verify: <1.1% wire overhead per 16KB chunk. Integration test: send 1000 chunks, verify zero SQL text on wire after initial prepare.

---

### 2.5 Enhanced psql Compatibility

**Task 45** `[S]` `~1.5d`
Full psql/DBeaver metadata query support.
→ Task 20
Handle all metadata queries that psql issues on connect: `pg_catalog.pg_type`, `pg_catalog.pg_namespace`, `pg_catalog.pg_class`, `pg_catalog.pg_attribute`, `pg_catalog.pg_constraint`, `information_schema.tables`, `information_schema.columns`. Handle DBeaver's additional queries: `pg_catalog.pg_proc`, `pg_catalog.pg_database`. Return realistic metadata matching the scenario's tables. Integration test: connect with DBeaver, verify no errors in connection log.

**Task 46** `[S]` `~1d`
Implement EXPLAIN and SET support.
→ Task 19
Handle `EXPLAIN SELECT ...` (return fake query plan). Handle `SET client_encoding`, `SET timezone`, `SET search_path` (acknowledge, no-op). Handle `BEGIN`/`COMMIT`/`ROLLBACK` (acknowledge, track transaction state for ReadyForQuery 'T'/'I'). Makes relay survive more aggressive client tools.

---

### 2.6 Testing

**Task 47** `[S]` `~1.5d`
Statistical traffic analysis test.
→ Task 42, Task 41
Create `test/statistical/traffic_test.go`. Generate 1 hour of interleaved traffic. Analyze: query type distribution (SELECT/INSERT/UPDATE percentages), inter-query timing distribution, BYTEA payload frequency (should be 10-20% of queries), burst pattern (queries per burst, burst spacing). Compare against real application traffic profiles using Kolmogorov-Smirnov test. All tests must pass with p-value > 0.05. This validates that Duman traffic is statistically indistinguishable from real application traffic.

**Task 48** `[S]` `~0.5d`
Benchmark crypto performance.
→ Task 5, Task 6
Create `internal/crypto/cipher_bench_test.go`. Benchmark: ChaCha20 encrypt/decrypt 16KB, AES-256-GCM encrypt/decrypt 16KB, HKDF key derivation, HMAC token generation. Verify: encrypt 16KB < 1ms on modern hardware. Report: throughput in MB/s for each cipher.

---

## Milestone 3: v0.3.0 — Multi-Protocol

**Goal:** MySQL and REST support alongside PostgreSQL.
**Tasks:** 18
**Estimated:** ~5 weeks

---

### 3.1 MySQL Wire Protocol

**Task 49** `[P]` `~2d`
Implement MySQL message serialization.
→ Task 1
Create `internal/mysqlwire/messages.go`. Implement MySQL packet format: 3-byte length + 1-byte sequence number. Implement read/write packet functions. Define command types (COM_QUERY, COM_STMT_PREPARE, COM_STMT_EXECUTE, etc.) and column types (BLOB for tunnel). Implement result set building: column count packet → column definition packets → EOF → row packets → EOF. Unit test: roundtrip serialization, multi-packet for large payloads.

**Task 50** `[P]` `~1d`
Implement MySQL authentication.
→ Task 49
Create `internal/mysqlwire/auth.go`. Implement `mysql_native_password` (SHA1-based challenge-response) and `caching_sha2_password` (SHA256). Implement handshake packet with server capabilities negotiation. Unit test: verify against known MySQL auth hashes.

**Task 51** `[P]` `~2.5d`
Implement MySQL server (relay-side).
→ Task 49, Task 50
Create `internal/mysqlwire/server.go`. Connection lifecycle: send handshake → receive auth response → verify → OK. Query loop: dispatch COM_QUERY, COM_STMT_PREPARE, COM_STMT_EXECUTE. Use same FakeDataEngine and TunnelEngine as PgWire (shared via QueryHandler interface). Server identifies as "MySQL 8.0.36". Integration test: connect with real `mysql` CLI, verify auth, verify query responses.

**Task 52** `[P]` `~1.5d`
Implement MySQL client (client-side).
→ Task 49, Task 50
Create `internal/mysqlwire/client.go`. Implement `Connect`, `SimpleQuery`, `Prepare`, `PreparedInsert` matching PgWire client interface. Use BLOB type instead of BYTEA for binary data. Integration test: connect client to server, verify roundtrip.

**Task 53** `[P]` `~1d`
Implement MySQL provider.
→ Task 52, Task 23
Create `internal/provider/mysql_provider.go` implementing Provider interface using MySQL client. Same tunnel INSERT format adapted for MySQL syntax (BLOB instead of BYTEA, `?` placeholders instead of `$1`). Unit test: verify tunnel INSERT format.

---

### 3.2 REST API Facade

**Task 54** `[P]` `~2d`
Implement REST API server (relay-side).
→ Task 19
Create `internal/restapi/server.go` and `internal/restapi/routes.go`. Implement endpoints: GET `/api/v2/products`, GET `/api/v2/products/:id`, GET `/api/v2/categories`, GET `/api/v2/dashboard/stats`, GET `/api/v2/status`, GET `/api/v2/health`, POST `/api/v2/analytics/events`, GET `/api/v2/analytics/sync`. Products/dashboard endpoints use FakeDataEngine for JSON responses. Analytics POST: check HMAC in metadata.pixel_id, extract base64 payload, process as tunnel chunk. Sync GET: return pending response chunks. API key auth via Authorization header. Integration test: curl all endpoints, verify JSON responses.

**Task 55** `[S]` `~1d`
Implement OpenAPI/Swagger documentation page.
→ Task 54
Create `internal/restapi/swagger.go`. Serve Swagger UI at `/docs` with embedded HTML. Serve OpenAPI 3.0 spec at `/docs/openapi.json` describing all endpoints with schemas. Makes relay look like a professional API service to investigators.

**Task 56** `[P]` `~1d`
Implement REST API client (client-side).
→ Task 54
Create `internal/restapi/client.go`. Implement HTTP client: POST analytics events (tunnel), GET sync (response fetch), GET products/status (cover). Use `net/http` with realistic headers. Integration test: client sends tunnel via REST, verify response received.

**Task 57** `[P]` `~0.5d`
Implement REST provider.
→ Task 56, Task 23
Create `internal/provider/rest_provider.go` implementing Provider interface. Tunnel chunks base64 encoded in JSON POST body. Responses fetched via polling GET. Unit test: verify request format.

---

### 3.3 Multi-Relay Distribution

**Task 58** `[P]` `~1d`
Implement multi-provider distribution.
→ Task 24, Task 53, Task 57
Enhance provider manager to support mixed protocol providers (PgSQL + MySQL + REST simultaneously). Weighted distribution across all providers. Staggered connection startup (30s-2m between each). Health check all providers periodically. Integration test: client with 3 providers (1 PgSQL + 1 MySQL + 1 REST), verify chunks distributed by weight.

**Task 59** `[S]` `~1d`
Implement exit relay modes.
→ Task 16
Add `role` config to relay: `exit` (forward to internet), `relay` (forward to another relay), `both`. For relay mode: encrypted chunk forwarding to designated exit relay via TCP. For chain mode: multi-hop forwarding. Config: `tunnel.forward_to` address. Unit test: relay-to-relay forwarding works.

---

### 3.4 TLS & ACME

**Task 60** `[P]` `~1.5d`
Implement ACME certificate management.
→ Task 11
Implement automatic Let's Encrypt certificate provisioning using `golang.org/x/crypto/acme/autocert`. Support HTTP-01 challenge on port 80 (or manual DNS-01). Store certificates in memory (no disk). Auto-renewal. Fallback to self-signed for development. Config: `tls.mode: acme | manual | self_signed`. Integration test: verify ACME works against Let's Encrypt staging.

---

### 3.5 Testing

**Task 61** `[S]` `~1d`
MySQL protocol conformance test.
→ Task 51
Connect to relay with real `mysql` CLI. Verify: `SHOW TABLES`, `DESCRIBE products`, `SELECT * FROM products LIMIT 5`, `SELECT COUNT(*) FROM products`, `DROP TABLE products` (permission denied). Compare behavior with real MySQL 8.0.

**Task 62** `[S]` `~1d`
Multi-protocol integration test.
→ Task 58
Start relay with all three protocols (PgSQL + MySQL + REST). Start client with all three providers. Send traffic through SOCKS5. Verify chunks distributed across all three. Verify responses collected from all three. Verify cover queries appropriate per protocol.

---

## Milestone 4: v0.4.0 — Split Tunnel & Advanced Routing

**Goal:** TUN device, rule-based routing, per-process routing.
**Tasks:** 16
**Estimated:** ~4 weeks

---

### 4.1 TUN Device

**Task 63** `[P]` `~2d`
Implement TUN device for Linux.
→ Task 15
Create `internal/proxy/tun_linux.go`. Open `/dev/net/tun` with `IFF_TUN | IFF_NO_PI`. Set IP address and MTU via netlink. Read IP packets from TUN, parse TCP/UDP headers, route to StreamManager or direct. Write response packets back to TUN. Requires root privileges. Integration test: create TUN device, route traffic, verify tunnel works.

**Task 64** `[S]` `~2d`
Implement TUN device for macOS.
→ Task 15
Create `internal/proxy/tun_darwin.go`. Use utun via `SystemConfiguration` framework. Configure with `ifconfig`. Add routes via `route add`. Integration test: same as Linux.

**Task 65** `[S]` `~2d`
Implement TUN device for Windows.
→ Task 15
Create `internal/proxy/tun_windows.go`. Use Wintun driver (embedded `wintun.dll`). Create adapter via `WintunCreateAdapter`. Configure via `netsh`. Integration test: same as Linux.

---

### 4.2 Rule-Based Routing

**Task 66** `[P]` `~1d`
Implement routing rules engine.
→ Task 63
Create `internal/proxy/routing.go`. Implement `Router` with `ShouldTunnel(dest)` checking rules in order: domain match (exact + wildcard `*.google.com`), IP range (CIDR), port match, default action. Parse rules from YAML config. Unit test: domain matching, wildcard, IP range, port, default fallback, rule priority.

**Task 67** `[P]` `~1d`
Implement DNS interception for TUN mode.
→ Task 26, Task 63
When TUN mode active, intercept DNS queries for tunneled domains. Route matching domains through relay DNS resolver. Non-matching domains use system resolver directly. Prevents DNS leaks for tunneled traffic. Unit test: tunneled domain resolved through relay, direct domain resolved locally.

---

### 4.3 Per-Process Routing

**Task 68** `[S]` `~2d`
Implement per-process routing for Linux.
→ Task 63
Use cgroup v2 net_cls classifier. Create cgroup for tunneled processes. Use iptables `-m owner --pid-owner` or `-m cgroup` to MARK packets. Policy routing sends marked packets to TUN. Config: `routing.rules: [{process: "firefox", action: tunnel}]`. Integration test: only Firefox traffic tunneled, Chrome direct.

**Task 69** `[S]` `~2d`
Implement per-process routing for macOS.
→ Task 64
Use Network Extension framework with per-app VPN profile (requires entitlement for distribution). For development: use `pf` rules with process matching. Integration test: same as Linux.

**Task 70** `[S]` `~2d`
Implement per-process routing for Windows.
→ Task 65
Use WFP (Windows Filtering Platform) callout driver with process ID matching. Requires driver signing for distribution. Integration test: same as Linux.

---

### 4.4 UDP Support

**Task 71** `[S]` `~1.5d`
Implement UDP tunneling via SOCKS5 UDP ASSOCIATE.
→ Task 25
Add SOCKS5 `cmdUDPAssociate` handler. Client creates UDP relay socket. UDP packets encapsulated in tunnel chunks with stream metadata. Relay unpacks and forwards as UDP. Supports DNS-over-UDP, QUIC, game traffic. Unit test: send UDP through SOCKS5, verify delivery.

---

### 4.5 Kill Switch

**Task 72** `[P]` `~1d`
Implement kill switch (leak prevention).
→ Task 66
If all relay connections fail: block all tunneled traffic (don't fallback to direct). Drop packets at TUN/routing level. Notify user via log. Resume when relay reconnects. Config: `proxy.kill_switch: true`. Unit test: disconnect all relays, verify tunneled traffic blocked, verify direct traffic unaffected.

---

### 4.6 Testing

**Task 73** `[S]` `~1d`
TUN mode integration test.
→ Task 63, Task 66
Start relay + client in TUN mode. Configure rules: `*.google.com` → tunnel, `*` → direct. Run `curl google.com` (tunneled) and `curl youtube.com` (direct). Verify Google traffic goes through relay, YouTube goes direct. Verify DNS leak prevention.

**Task 74** `[S]` `~0.5d`
Kill switch test.
→ Task 72
Start client with kill switch enabled. Disconnect relay. Verify tunneled connections fail immediately. Verify direct connections still work. Reconnect relay. Verify tunnel resumes.

---

## Milestone 5: v0.5.0 — Noise Layers

**Goal:** Full traffic camouflage with phantom browsing, P2P cover, decoys.
**Tasks:** 14
**Estimated:** ~4 weeks

---

### 5.1 Phantom Browser

**Task 75** `[P]` `~2d`
Implement phantom browser core.
→ Task 1
Create `internal/phantom/browser.go`. Implement HTTP client with realistic Chrome User-Agent and headers. Implement `browseSession()` with pattern-based behavior: search+browse, video watch (long dwell), social scroll (repeated GETs), news read, shopping browse. Fetch pages with `io.Copy(io.Discard, resp.Body)` (consume response, discard content). Random inter-page delays matching real browsing patterns. Unit test: verify requests have realistic headers, verify timing distribution.

**Task 76** `[P]` `~1d`
Implement regional browsing profiles.
→ Task 75
Create `internal/phantom/profiles.go`. Define profiles for `turkey` (google.com.tr, youtube.com, hurriyet.com.tr, trendyol.com, eksisozluk.com, etc.), `europe` (google.com, bbc.com, amazon.de, reddit.com, etc.), `global` (google.com, youtube.com, github.com, stackoverflow.com, etc.). Each site has weight (visit probability) and browse pattern. Config: `noise.phantom_browser.region`. Unit test: verify site selection matches weights.

**Task 77** `[S]` `~1d`
Implement TLS fingerprint mimicking.
→ Task 75
Configure `crypto/tls` client to produce TLS ClientHello similar to real Chrome: cipher suites, extensions, ALPN, supported groups, signature algorithms. Use `tls.Config` with explicit ordering. Not perfect JA3 spoofing (would require utls library), but close enough for DPI. Research: document what JA3 hash the phantom browser produces.

---

### 5.2 P2P Smoke Screen

**Task 78** `[P]` `~1.5d`
Implement P2P cover traffic generator.
→ Task 1
Create `internal/smokescreen/peer.go` and `internal/smokescreen/cover.go`. Implement TLS connections to randomly generated IPs (simulate residential peers). Generate random encrypted data matching traffic profiles: video call (symmetric 1-5 Mbps), messaging (bursty 10-50 Kbps), file sync (asymmetric 500K-2M), gaming (low latency 100-500 Kbps). Connections are cover-only: random data, no real content. Config: `noise.smoke_screen.peer_count`, `noise.smoke_screen.profiles`. Unit test: verify traffic patterns match profile specifications.

**Task 79** `[S]` `~1d`
Implement decoy connections.
→ Task 1
Create decoy HTTPS connections to popular developer sites (github.com, stackoverflow.com, pkg.go.dev, npmjs.com). Simple GET requests with realistic browsing patterns. Config: `noise.decoy.targets`, `noise.decoy.count`. Lower priority than phantom browser (less realistic).

---

### 5.3 Bandwidth Governor

**Task 80** `[P]` `~1.5d`
Implement bandwidth governor.
→ Task 22, Task 75, Task 78
Create `internal/governor/governor.go`. Implement token bucket rate limiter per component (tunnel, cover, phantom, P2P). Implement adaptive allocation: heavy tunnel demand → reduce phantom/cover; light tunnel → increase phantom (more camouflage); zero tunnel → phantom + cover only. Implement `WaitForTunnel(bytes)`, `WaitForPhantom(bytes)`, etc. Config: budget percentages in YAML. Unit test: verify rate limiting accuracy, verify adaptive switching.

**Task 81** `[P]` `~1d`
Implement line speed auto-detection.
→ Task 80
Create `internal/governor/detect.go`. Send a burst of data through one relay connection, measure throughput. Use RTT measurements to estimate bandwidth. Fallback to configured value if auto-detect fails. Run at startup and periodically (every 10 minutes). Unit test: verify detection works within 20% of actual speed.

---

### 5.4 Integration

**Task 82** `[P]` `~1.5d`
Wire noise layers into client orchestrator.
→ Task 75, Task 78, Task 79, Task 80, Task 29
Start phantom browser after first relay connect (looks like user opened browser). Start P2P smoke screen after all relays connected. Start decoy connections alongside. All traffic goes through bandwidth governor. Graceful shutdown stops all noise generators. Config: enable/disable each layer independently.

**Task 83** `[S]` `~1d`
ISP traffic profile test.
→ Task 82
Create `test/integration/isp_profile_test.go`. Start full client with all noise layers. Capture all outbound connections (tcpdump or in-memory). Verify ISP sees: direct browsing (phantom), database connections (tunnel), API connections (REST tunnel), P2P connections (smoke screen), developer site connections (decoy). Verify: connection count, protocol mix, traffic volume distribution.

---

## Milestone 6: v0.6.0 — Relay Pool & Rotation

**Goal:** Large-scale deployment with 100+ relays.
**Tasks:** 12
**Estimated:** ~3 weeks

---

### 6.1 Pool Management

**Task 84** `[P]` `~1.5d`
Implement relay pool.
→ Task 24
Create `internal/pool/pool.go`. Maintain list of relay configs (from config file or remote fetch). Select N active relays (default 3). Track relay state: healthy, failed, blocked. Support tier classification (Community, Verified, Trusted). Config: `pool.max_active`, `pool.health_check_interval`.

**Task 85** `[P]` `~1d`
Implement health checking.
→ Task 84
Create `internal/pool/health.go`. Periodic TCP connect + TLS handshake + auth test for each relay. Mark failed relays. Exponential backoff for retrying failed relays. Detect blocked relays (connect timeout, TLS fail, auth format mismatch suggesting MITM). Config: health check interval, failure threshold.

**Task 86** `[P]` `~1.5d`
Implement seed-based rotation schedule.
→ Task 84
Create `internal/pool/schedule.go`. Deterministic rotation from seed: both client and pool coordinator compute same schedule. Fast rotation for relay slots (30s-5min). Slow rotation for exit slot (15-30min). Pre-warm: connect to next relay 30s before switch. Overlap: old and new relay both active during transition (zero interruption). Unit test: verify deterministic schedule from seed, verify pre-warm timing.

---

### 6.2 Anti-Censorship

**Task 87** `[P]` `~1d`
Implement block detection and auto-reroute.
→ Task 85
Detect blocked relays: TCP timeout (ISP blocking IP), TLS handshake failure (DPI interference), auth protocol mismatch (active MITM), sustained high latency (throttling). Auto-skip blocked relays, advance rotation schedule. Log blocked relays for operator awareness. Unit test: simulate blocked relay, verify auto-reroute.

**Task 88** `[S]` `~1d`
Implement community relay onboarding.
→ Task 84
Document relay contribution process. Create `duman-relay init` command that generates config with new shared secret, suggests domain naming, outputs registration info. Relay operators share their config (domain, port, tier) via a JSON manifest. Client can load relay manifests from file or URL.

---

### 6.3 Tier Management

**Task 89** `[S]` `~1d`
Implement tier classification.
→ Task 84
Create `internal/pool/tiers.go`. Trusted (tier 3): exit-capable, operator verified, long-running. Verified (tier 2): 3+ months uptime, passed health audits. Community (tier 1): new, relay-only, no exit. Automatic promotion: Community → Verified after 90 days of 99%+ uptime. Exit slot only available to Trusted tier. Config: tier overrides per relay.

---

### 6.4 Testing

**Task 90** `[S]` `~1d`
Relay pool integration test.
→ Task 86, Task 87
Start 5 relays. Configure client with pool of 5, max active 3. Verify: 3 active connections, rotation occurs on schedule, blocked relay skipped, pre-warm works, zero-interruption switch. Simulate relay failure mid-session, verify failover.

**Task 91** `[S]` `~0.5d`
Pool rotation determinism test.
→ Task 86
Two clients with same seed + same pool. Verify both compute identical rotation schedule. Verify schedule is not predictable without seed.

---

## Milestone 7: v1.0.0 — Production

**Goal:** Production-ready release with dashboard, PFS, docs.
**Tasks:** 11
**Estimated:** ~3 weeks

---

### 7.1 Dashboard

**Task 92** `[S]` `~2d`
Implement embedded client dashboard.
Create embedded HTTP server on configurable port (default `127.0.0.1:9090`). Serve HTML/JS dashboard showing: active relay connections (status, latency, protocol), tunnel streams (count, throughput), cover query rate, bandwidth allocation, noise layer status. Use SSE (Server-Sent Events) for live updates. Minimal UI: single HTML page, no frontend framework.

**Task 93** `[S]` `~1.5d`
Implement embedded relay dashboard.
Create embedded HTTP server showing: connected clients (count, session duration), tunnel throughput, cover query rate, exit connection pool status, fake data engine stats. Protected with basic auth or local-only binding.

---

### 7.2 Security

**Task 94** `[S]` `~1.5d`
Implement perfect forward secrecy.
→ Task 5
Add ephemeral X25519 key exchange at session start. Client sends ephemeral public key in first analytics INSERT metadata as `device_fingerprint`. Relay responds with its ephemeral public key in response. Both derive shared secret: `X25519(ephemeral_private, peer_public)`. Session key: `HKDF(X25519_shared || pre_shared_secret, ...)`. Config: `crypto.pfs: true`.

**Task 95** `[S]` `~1d`
Implement SCRAM-SHA-256 authentication.
→ Task 10
Full RFC 5802 + RFC 7677 implementation for SCRAM-SHA-256 in PgWire. Replace MD5 as default auth for production. ~200 LOC using `golang.org/x/crypto/pbkdf2` + `crypto/hmac` + `crypto/sha256`. Integration test: connect with psql using SCRAM-SHA-256.

---

### 7.3 Performance

**Task 96** `[S]` `~1.5d`
Performance optimization pass.
Profile with `pprof`: identify bottlenecks in crypto, pgwire, interleaving. Optimize: use `sync.Pool` for chunk buffers and message buffers. Pre-allocate RowDescription/DataRow builders. Reduce allocations in hot path (interleaving loop). Benchmark before/after. Target: handle 1000 chunks/sec on single core.

**Task 97** `[S]` `~1d`
Implement transparent proxy mode.
Add `iptables`-based transparent proxy for Linux. Incoming redirected connections handled by SOCKS5 server with `SO_ORIGINAL_DST` to determine destination. No app configuration needed (system-wide via iptables rules). Document setup.

---

### 7.4 Testing & Quality

**Task 98** `[P]` `~2d`
Comprehensive test suite.
Write missing unit tests to achieve 80%+ coverage across all packages. Write integration tests for every milestone feature. Write end-to-end test: client + relay + mock destination, verify full tunnel lifecycle including reconnection, rotation, and noise layers. Create CI pipeline (GitHub Actions) running all tests.

**Task 99** `[S]` `~1d`
Security review checklist.
Verify: shared secret never logged (grep codebase), SOCKS5 binds to localhost only, config file permission warning, memory zeroing for keys, constant-time HMAC comparison, TLS minimum version 1.2, ACME works correctly, DNS leak prevention in all modes, kill switch functional.

---

### 7.5 Documentation

**Task 100** `[P]` `~1.5d`
Write user documentation.
Create `docs/user-guide.md`: installation, quick start, configuration reference, routing modes (SOCKS5/TUN/per-process), scenario selection, noise layer tuning, troubleshooting. Create `docs/deployment-guide.md`: relay setup, domain selection, TLS configuration, firewall rules, monitoring. Create `docs/relay-operator-guide.md`: contributing a relay, tier system, health requirements.

**Task 101** `[S]` `~0.5d`
Write README.md.
Project overview, key features, quick start (5 steps), architecture diagram (text), configuration example, performance numbers, security model summary, license. Link to full docs.

**Task 102** `[S]` `~0.5d`
Create release artifacts.
Build cross-platform binaries (Linux amd64/arm64, macOS amd64/arm64, Windows amd64). Create checksums (SHA256). Create GitHub Release with changelog. Tag v1.0.0.

---

## Post-v1.0 Enhancement Tasks

These tasks are not required for v1.0 but improve the system significantly.

---

### MCP Server Integration

**Task 103** `[E]` `~2d`
Implement MCP server for client management.
Expose client operations via MCP protocol: list active streams, show relay status, adjust bandwidth allocation, view cover query statistics, rotate relay manually. Enables LLM-native management of Duman client.

**Task 104** `[E]` `~2d`
Implement MCP server for relay management.
Expose relay operations: list connected clients, view exit connections, show fake data engine stats, hot-reload configuration, rotate TLS certificate. Enables LLM-native relay administration.

---

### Advanced Features

**Task 105** `[E]` `~1.5d`
Implement query recording and replay.
Record real application query patterns (with permission) from actual PostgreSQL connections. Replay recorded patterns as cover queries instead of synthetic ones. Achieves perfect statistical match with real application.

**Task 106** `[E]` `~2d`
Implement multiple simultaneous scenarios.
Allow different relays to use different scenarios (Relay-A: e-commerce, Relay-B: IoT). Each connection maintains its own scenario state. Increases diversity of cover traffic patterns.

**Task 107** `[E]` `~1.5d`
Implement connection migration.
When rotating relays, migrate active tunnel streams to new relay without dropping connections. Requires stream state serialization and relay-to-relay handoff protocol. Zero-interruption for long-lived connections.

**Task 108** `[E]` `~2d`
Implement WebSocket tunnel mode.
Add WebSocket as fourth transport alongside PgSQL/MySQL/REST. WebSocket to a "real-time dashboard" server is a natural traffic pattern. Supports bidirectional push without polling.

**Task 109** `[E]` `~1d`
Implement config hot-reload.
Watch config file for changes. Reload on SIGHUP or file change: relay list, noise settings, bandwidth allocation, routing rules. No restart required.

**Task 110** `[E]` `~1.5d`
Implement traffic recording for analysis.
Record all interleaved traffic (anonymized) for offline statistical analysis. Output as PCAP-like format. Tool to compare Duman traffic vs real PostgreSQL traffic side-by-side. Useful for tuning timing profiles.

---

### Platform-Specific

**Task 111** `[E]` `~2d`
Implement macOS Network Extension.
Full Network Extension framework integration for per-app VPN routing on macOS. Requires Apple Developer Program membership for entitlements. Enables system tray icon with connect/disconnect.

**Task 112** `[E]` `~2d`
Implement Windows service mode.
Run client as Windows Service (auto-start on boot). System tray icon with status indicator. WFP driver for per-app routing. Installer package (MSI or NSIS).

**Task 113** `[E]` `~3d`
Implement Android client.
Go-based client compiled for Android via gomobile. VPN Service API for system-wide tunneling. Minimal UI: connect/disconnect, relay status, bandwidth usage. APK distribution.

**Task 114** `[E]` `~3d`
Implement iOS client.
Go-based client compiled for iOS via gomobile. Network Extension with Packet Tunnel Provider. Minimal UI matching Android. Requires Apple Developer Program. App Store or TestFlight distribution.

---

### Security Enhancements

**Task 115** `[E]` `~1.5d`
Implement relay certificate pinning.
Pin relay TLS certificates in client config. Reject connections if certificate doesn't match pin. Prevents MITM even with compromised CA. Config: `relays[].tls_pin: "sha256/..."`.

**Task 116** `[E]` `~1d`
Implement canary token detection.
Detect if relay responses contain known canary tokens (injected by investigators to trace tunnel users). Compare response data against known canary patterns. Alert user if detected.

**Task 117** `[E]` `~1.5d`
Implement traffic padding.
Pad all tunnel chunks to exactly 16KB regardless of actual data size. Prevents size-based correlation attacks. Configurable: `tunnel.padding: true`. Trades bandwidth for security.

**Task 118** `[E]` `~1d`
Implement timing jitter injection.
Add random delay (0-50ms) to every tunnel chunk send. Prevents timing-based correlation between tunnel input and relay output. Configurable: `tunnel.jitter_ms: 50`. Trades latency for security.

---

### Operational

**Task 119** `[E]` `~1d`
Implement Prometheus metrics export.
Export metrics: tunnel throughput, cover query rate, relay latency, connection count, error rate, bandwidth usage per component. Endpoint: `/metrics` on dashboard port. Grafana dashboard template.

**Task 120** `[E]` `~1d`
Implement relay discovery protocol.
DNS-based relay discovery: query TXT records for relay pool manifest. Bootstrap without hardcoded relay list. Fallback: HTTP-based relay list endpoint.

**Task 121** `[E]` `~1.5d`
Implement relay load balancing.
Relay reports its current load (connected clients, bandwidth usage). Client prefers less-loaded relays. Distributed via pool manifest or gossip between relays.

**Task 122** `[E]` `~1d`
Implement relay-to-relay encrypted forwarding.
For chain mode: relay-to-relay communication encrypted with separate key. Each relay only knows previous and next hop. Client specifies chain path or auto-selects. Adds latency but maximum anonymity.

---

### Content & Documentation

**Task 123** `[E]` `~1d`
Create architecture documentation with diagrams.
Detailed diagrams: packet lifecycle, interleaving algorithm visualization, relay internal architecture, ISP perspective view. Use Mermaid or hand-drawn SVG.

**Task 124** `[E]` `~1d`
Create threat model documentation.
Formal threat model: assets, threats, mitigations, residual risks. Cover: passive ISP, active ISP with TLS MitM, active prober, relay compromise, endpoint compromise, timing correlation, volume analysis. STRIDE analysis.

**Task 125** `[E]` `~0.5d`
Create performance benchmarking suite.
Automated benchmarks: tunnel throughput at each profile (Speed/Balanced/Stealth/Paranoid), latency overhead, CPU/memory usage, crypto operations per second. Compare against WireGuard/Shadowsocks for reference.

**Task 126** `[E]` `~0.5d`
Create relay deployment scripts.
One-click deploy scripts for: DigitalOcean droplet, Hetzner cloud, AWS EC2, Docker container. Script: provision VM → install binary → generate config → start service → configure DNS → obtain TLS cert.

**Task 127** `[E]` `~0.5d`
Create Docker images.
Multi-stage Dockerfile: build stage (Go compile) → runtime stage (scratch or alpine). Images: `dumanproxy/duman-client`, `dumanproxy/duman-relay`. Docker Compose example with client + relay + mock destination. Publish to Docker Hub.

---

## Task Summary

| Milestone | Tasks | Priority Breakdown | Estimated |
|---|---|---|---|
| **v0.1.0 MVP** | 34 | 26P + 6S + 2E | ~8 weeks |
| **v0.2.0 Full Query Engine** | 22 | 14P + 8S | ~5 weeks |
| **v0.3.0 Multi-Protocol** | 18 | 13P + 5S | ~5 weeks |
| **v0.4.0 Split Tunnel** | 16 | 8P + 8S | ~4 weeks |
| **v0.5.0 Noise Layers** | 14 | 8P + 6S | ~4 weeks |
| **v0.6.0 Relay Pool** | 12 | 6P + 6S | ~3 weeks |
| **v1.0.0 Production** | 11 | 3P + 8S | ~3 weeks |
| **Post-v1.0 Enhancements** | 25 | 0P + 0S + 25E | backlog |
| **Total** | **127** | **78P + 47S + 27E** | **~32 weeks** |

---

## Critical Path

The minimum viable path to a working tunnel (MVP):

```
Task 1 (bootstrap)
  → Task 4 (HKDF) → Task 5 (cipher) → Task 6 (chunk) → Task 7 (HMAC auth)
  → Task 9 (PgWire messages) → Task 10 (MD5 auth)
  → Task 11 (PgWire server) + Task 12 (PgWire client)
  → Task 13 (splitter) + Task 14 (assembler) → Task 15 (stream manager)
  → Task 16 (exit engine)
  → Task 17 (SQL parser) → Task 18 (fake data gen) → Task 19 (fake data engine)
  → Task 21 (real query engine) → Task 22 (interleaving)
  → Task 23 (PG provider) → Task 24 (provider manager)
  → Task 25 (SOCKS5)
  → Task 26 (DNS resolver)
  → Task 27 (relay handler) + Task 28 (response queue)
  → Task 29 (client main) + Task 30 (relay main)
  → Task 32 (E2E test) ← MVP ACCEPTANCE
```

**Critical path length:** 20 tasks in sequence, ~6 weeks.
Remaining MVP tasks (config, logging, CLI, keygen, psql test, README) are parallel.
