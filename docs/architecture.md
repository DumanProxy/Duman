# Duman Architecture

## System Overview

```
                          ISP / Deep Packet Inspection
                          ~~~~~~~~~~~~~~~~~~~~~~~~~~~~
                          Sees: PostgreSQL, MySQL, REST,
                          WebSocket, HTTPS browsing, P2P
                          ~~~~~~~~~~~~~~~~~~~~~~~~~~~~

[Application] --> [SOCKS5 Proxy] --> [Interleaving Engine] --> [Wire Protocol] --> [Relay] --> [Internet]
                   :1080              Cover + Tunnel            PG/MySQL/REST      :5432
                                      queries mixed             wire format        :3306
                                                                                   :443
```

## Packet Lifecycle

### Outbound (Client -> Internet)

```
1. Application sends data to SOCKS5 proxy (127.0.0.1:1080)
   |
2. StreamManager creates/reuses stream for this connection
   |
3. Splitter chunks data into <=16KB segments
   |  - Each chunk: [type][flags][streamID][seqNo][payloadLen][payload]
   |
4. Crypto layer encrypts chunk
   |  - HKDF derives per-session key from shared secret
   |  - ChaCha20-Poly1305 or AES-256-GCM
   |  - AAD: sessionID + seqNo (replay protection)
   |  - Optional: X25519 PFS ephemeral key exchange
   |
5. Interleaving Engine embeds chunk
   |  - Encrypted chunk -> base64 -> INSERT INTO analytics_events
   |  - Cover queries interleaved: SELECT, JOIN, COUNT
   |  - Timing profile: casual_browser, power_user, api_worker
   |  - EWMA ratio controller: adjusts cover:tunnel ratio
   |
6. Wire Protocol sends over TCP
   |  - PostgreSQL: standard wire protocol messages
   |  - MySQL: COM_QUERY packets
   |  - REST: POST /api/v2/analytics/events
   |  - WebSocket: RFC 6455 frames
   |
7. Relay receives, extracts tunnel chunk from INSERT
   |  - HMAC verification (constant-time)
   |  - Decrypt with session key
   |
8. Assembler reconstructs original data from chunks
   |
9. Exit Engine forwards to destination on the internet
```

### Inbound (Internet -> Client)

```
1. Destination responds to Exit Engine
   |
2. Splitter chunks response data
   |
3. Crypto encrypts response chunks
   |
4. Response queue stores chunks for client pickup
   |  - Push mode: LISTEN/NOTIFY (1-5ms latency)
   |  - Poll mode: client SELECTs periodically (50-200ms)
   |
5. Client retrieves via SELECT FROM analytics_responses
   |
6. Assembler reconstructs response
   |
7. SOCKS5 proxy delivers to application
```

## Component Architecture

```
cmd/
  duman-client/main.go          CLI: start, keygen, status, version
  duman-relay/main.go           CLI: start, keygen, status, version

internal/
  +------------------+    +------------------+    +------------------+
  |     config/      |    |      log/        |    |    dashboard/    |
  | YAML + hot-reload|    | Structured slog  |    | SSE + pprof     |
  +------------------+    +------------------+    | Prometheus /metrics
                                                  +------------------+

  +------------------+    +------------------+    +------------------+
  |     crypto/      |    |     tunnel/      |    |     proxy/       |
  | ChaCha20/AES-GCM |    | Splitter         |    | SOCKS5 server    |
  | HKDF key derive  |    | Assembler        |    | UDP ASSOCIATE    |
  | HMAC auth tokens |    | StreamManager    |    | TUN device       |
  | X25519 PFS       |    | Exit engine      |    | DNS cache        |
  | Chunk marshal    |    | Migration        |    | Routing rules    |
  | Traffic padding  |    | Canary detection |    | Kill switch      |
  +------------------+    +------------------+    +------------------+

  +------------------+    +------------------+    +------------------+
  |     pgwire/      |    |   mysqlwire/     |    |    restapi/      |
  | PG wire protocol |    | MySQL protocol   |    | REST facade      |
  | SCRAM-SHA-256    |    | native + sha2    |    | Swagger docs     |
  | Server + Client  |    | Server + Client  |    | Server + Client  |
  +------------------+    +------------------+    +------------------+

  +------------------+    +------------------+    +------------------+
  |    fakedata/     |    |   realquery/     |    |   interleave/    |
  | Dynamic schemas  |    | Cover patterns   |    | Timing profiles  |
  | DDL parser       |    | ecommerce/iot/   |    | EWMA ratio ctrl  |
  | Data generators  |    | saas/blog/proj   |    | Burst scheduler  |
  | Schema mutations |    | Generic patterns |    | Jitter injection |
  | Multi-scenario   |    +------------------+    +------------------+
  +------------------+

  +------------------+    +------------------+    +------------------+
  |    provider/     |    |     pool/        |    |     relay/       |
  | PG provider      |    | Relay pool mgmt  |    | Orchestration    |
  | MySQL provider   |    | Health checking  |    | Rate limiting    |
  | REST provider    |    | Seed rotation    |    | Health endpoint  |
  | Circuit breaker  |    | Load balancing   |    | Chain forwarding |
  | Cert pinning     |    | DNS discovery    |    | ACME TLS         |
  | Provider manager |    | Tier system      |    | Forwarder        |
  +------------------+    +------------------+    +------------------+

  +------------------+    +------------------+    +------------------+
  |    phantom/      |    |  smokescreen/    |    |    governor/     |
  | Chrome HTTP      |    | P2P cover traffic|    | Token bucket     |
  | Regional profiles|    | Random peers     |    | Bandwidth alloc  |
  | Browse patterns  |    | Decoy HTTPS      |    | Line speed detect|
  +------------------+    +------------------+    +------------------+

  +------------------+    +------------------+
  |    recorder/     |    |    wstunnel/     |
  | Traffic capture  |    | WebSocket tunnel |
  | Replay debug     |    | RFC 6455         |
  +------------------+    +------------------+
```

## Interleaving Algorithm

The interleaving engine is the core steganography component. It makes tunnel traffic indistinguishable from legitimate database usage.

```
Timeline (casual_browser profile):
    t=0s     SELECT * FROM products WHERE category_id = 3 LIMIT 20;    [cover]
    t=0.8s   SELECT COUNT(*) FROM orders WHERE status = 'pending';      [cover]
    t=2.1s   INSERT INTO analytics_events (payload) VALUES ('...');     [TUNNEL]
    t=2.3s   SELECT p.name, c.name FROM products p JOIN categories...;  [cover]
    t=4.5s   SELECT * FROM users WHERE id = 42;                        [cover]
    t=5.2s   INSERT INTO analytics_events (payload) VALUES ('...');     [TUNNEL]
    t=5.4s   INSERT INTO analytics_events (payload) VALUES ('...');     [TUNNEL]
    t=7.8s   SELECT * FROM products ORDER BY created_at DESC LIMIT 10; [cover]

    Cover:Tunnel ratio ≈ 3:1 (adapts via EWMA based on queue depth)
    Inter-query timing follows human browsing patterns
```

### EWMA Ratio Controller

```
                          Queue Depth
    Queue > 100  ──>  ratio = 1:1  (max tunnel throughput)
    Queue 50-100 ──>  ratio = 2:1
    Queue 10-50  ──>  ratio = 3:1  (base)
    Queue 0      ──>  ratio = 8:1  (max cover, stealth mode)

    Transitions smoothed with EWMA (alpha = 0.3) + hysteresis (±10%)
    Prevents oscillation and makes ratio changes gradual
```

## Wire Protocol Layer

### PostgreSQL Wire Protocol

```
Client -> Relay:
  StartupMessage { version: 3.0, user: "sensor_writer", database: "telemetry" }
  <-- AuthenticationSASL { mechanisms: ["SCRAM-SHA-256"] }
  SASLInitialResponse { mechanism: "SCRAM-SHA-256", data: client-first }
  <-- SASLContinue { data: server-first }
  SASLResponse { data: client-final }
  <-- AuthenticationSASLFinal { data: server-final }
  <-- AuthenticationOk
  <-- ParameterStatus { server_version: "16.1", ... }
  <-- ReadyForQuery { status: 'I' }

  Query { "SELECT * FROM products LIMIT 5" }
  <-- RowDescription { fields: [id, name, price, stock] }
  <-- DataRow { values: [1, "Wireless Headphones", 79.99, 150] }
  <-- DataRow { ... }
  <-- CommandComplete { tag: "SELECT 5" }
  <-- ReadyForQuery { status: 'I' }

  Query { "INSERT INTO analytics_events (session_id, event_type, payload, metadata)
           VALUES ('sess123', 'page_view', E'\\x<encrypted_chunk>', '{...}')" }
  <-- CommandComplete { tag: "INSERT 0 1" }          ← tunnel chunk delivered
  <-- ReadyForQuery { status: 'I' }
```

## Security Architecture

```
Shared Secret (base64, 32 bytes)
         |
         v
    HKDF-SHA256 ──> Per-Session Key (32 bytes)
         |               |
         |               +──> ChaCha20-Poly1305 or AES-256-GCM
         |               |    (selected based on hardware)
         |               |
         |          Optional PFS:
         |          X25519 ephemeral ──> DH shared secret
         |               |
         |               +──> HKDF(DH_shared || pre_shared, sessionID)
         |                    = PFS session key
         |
    HMAC-SHA256 ──> Auth Tokens (per-query verification)
         |          Format: "HMAC:" + hex(HMAC(key, sessionID+timestamp))
         |          Window: 30 seconds, accepts previous window
         |
    SCRAM-SHA-256 ──> PostgreSQL auth (RFC 5802 + RFC 7677)
         |            SaltedPassword = Hi(password, salt, iterations)
         |            ClientKey = HMAC(SaltedPassword, "Client Key")
         |            StoredKey = H(ClientKey)
         |
    Certificate Pinning ──> TLS verification
                             sha256/base64(SubjectPublicKeyInfo)
```

## ISP Perspective

What an ISP performing deep packet inspection sees:

```
Source          Dest              Protocol    Content
────────────────────────────────────────────────────────────────
192.168.1.100   db.example.com    PostgreSQL  Developer connected to cloud DB
                :5432                         Normal SELECT/INSERT/JOIN queries
                                              Analytics data collection

192.168.1.100   analytics.co      MySQL       Analytics database connection
                :3306                         Dashboard queries, report generation

192.168.1.100   api.service.com   HTTPS       REST API calls to SaaS platform
                :443                          Standard JSON request/response

192.168.1.100   app.example.com   WSS         WebSocket real-time dashboard
                :443                          Periodic data updates

192.168.1.100   google.com.tr     HTTPS       Normal web browsing (phantom)
192.168.1.100   youtube.com       HTTPS       Video streaming (phantom)
192.168.1.100   github.com        HTTPS       Developer browsing (decoy)

192.168.1.100   <random IPs>      TLS         P2P application (smokescreen)
                                              Video call / file sync patterns
```

All traffic patterns match legitimate developer activity. Without the shared encryption key, distinguishing tunnel queries from cover queries is cryptographically impossible.

## Data Flow Diagram

```
                    ┌─────────────────────────────────────────┐
                    │              CLIENT HOST                 │
                    │                                          │
                    │  [Browser/App]                           │
                    │       │                                  │
                    │       v                                  │
                    │  [SOCKS5 :1080]──>[StreamManager]        │
                    │       │                │                 │
                    │       │          [Splitter]              │
                    │       │                │                 │
                    │       │          [Crypto Encrypt]        │
                    │       │                │                 │
                    │       │     ┌──────────┼──────────┐     │
                    │       │     │   [Interleaver]     │     │
                    │       │     │    Cover + Tunnel   │     │
                    │       │     └──────────┼──────────┘     │
                    │       │                │                 │
                    │       │     ┌──────────┼──────────┐     │
                    │       │     │  [Provider Manager] │     │
                    │       │     │  PG│MySQL│REST│WS   │     │
                    │       │     └────┼─────┼────┼─────┘     │
                    │       │          │     │    │            │
                    │  [Phantom]  [Smokescreen]  [Governor]   │
                    │  browsing    P2P cover    bandwidth     │
                    └──────────────┼─────┼────┼───────────────┘
                                   │     │    │
                    ═══════════════╪═════╪════╪═══════════════
                              NETWORK (ISP can see)
                    ═══════════════╪═════╪════╪═══════════════
                                   │     │    │
                    ┌──────────────┼─────┼────┼───────────────┐
                    │              RELAY SERVER                 │
                    │              │     │    │                 │
                    │     ┌────────┼─────┼────┼────────┐      │
                    │     │   [Protocol Servers]        │      │
                    │     │   PG:5432│MySQL:3306│REST   │      │
                    │     └────────┼─────┼────┼────────┘      │
                    │              │     │    │                 │
                    │     [Auth] SCRAM/native/APIkey            │
                    │              │                            │
                    │     [Query Handler]                       │
                    │       │            │                      │
                    │  [FakeData]   [Tunnel Extract]            │
                    │  engine        │                          │
                    │               [Crypto Decrypt]            │
                    │                │                          │
                    │           [Assembler]                     │
                    │                │                          │
                    │           [Exit Engine]                   │
                    │                │                          │
                    └────────────────┼──────────────────────────┘
                                     │
                                     v
                                [Internet]
```
