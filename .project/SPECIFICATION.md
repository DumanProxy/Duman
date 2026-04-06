# DUMAN — Steganographic SQL/API Tunnel

## SPECIFICATION

> **"The best place to hide a needle is in a stack of other needles."**

**Version:** 1.0.0
**Status:** Draft
**GitHub:** github.com/dumanproxy/duman
**License:** MIT
**Language:** Go 1.23+ (stdlib-first, #NOFORKANYMORE)
**Dependencies:** golang.org/x/crypto, golang.org/x/net, golang.org/x/sys, gopkg.in/yaml.v3

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Problem Statement](#2-problem-statement)
3. [The Core Idea](#3-the-core-idea)
4. [Why SQL Interleaving is Undetectable](#4-why-sql-interleaving-is-undetectable)
5. [System Architecture](#5-system-architecture)
6. [Client Architecture](#6-client-architecture)
7. [Relay (Server) Architecture](#7-relay-server-architecture)
8. [Wire Protocols](#8-wire-protocols)
9. [Split Tunnel & Traffic Routing](#9-split-tunnel--traffic-routing)
10. [Real Query Engine](#10-real-query-engine)
11. [Interleaving Engine](#11-interleaving-engine)
12. [Tunnel Chunk Embedding](#12-tunnel-chunk-embedding)
13. [Fake Data Engine](#13-fake-data-engine)
14. [REST API Facade Layer](#14-rest-api-facade-layer)
15. [Response Channel](#15-response-channel)
16. [Crypto Layer](#16-crypto-layer)
17. [Multi-Relay & Multi-Protocol](#17-multi-relay--multi-protocol)
18. [Noise Layers](#18-noise-layers)
19. [Bandwidth Governor](#19-bandwidth-governor)
20. [Relay Pool & Rotation](#20-relay-pool--rotation)
21. [Security Model](#21-security-model)
22. [ISP Deep Packet Inspection Analysis](#22-isp-deep-packet-inspection-analysis)
23. [Application Scenarios](#23-application-scenarios)
24. [Configuration Reference](#24-configuration-reference)
25. [Performance Targets](#25-performance-targets)
26. [Project Structure](#26-project-structure)
27. [Milestones](#27-milestones)

---

## 1. Executive Summary

Duman is a steganographic tunneling system that hides encrypted internet traffic inside legitimate-looking SQL database queries and REST API calls. Unlike traditional VPNs and tunnels that try to hide **what they are**, Duman hides **what it carries**.

The system consists of two components:

- **duman-client** — A single Go binary (~15 MB) that runs on the user's machine. It provides a local SOCKS5 proxy (and optional TUN device) for applications. Tunneled traffic is encrypted, chunked, and embedded into PostgreSQL/MySQL queries as BYTEA payloads in analytics INSERT statements. Normal (non-tunneled) traffic passes through the user's regular internet connection untouched.

- **duman-relay** — A single Go binary (~15 MB) that runs on a server. It impersonates a PostgreSQL and/or MySQL database (speaking perfect wire protocol, responding to psql/DBeaver with realistic fake data). It extracts tunnel chunks from authenticated INSERT queries, decrypts them, and forwards the traffic to the internet as an exit node.

An ISP performing deep packet inspection sees a developer connected to cloud databases and REST APIs — a completely normal traffic pattern. Even with hypothetical TLS decryption, the tunnel queries are syntactically and behaviorally identical to real analytics INSERT statements. Without the shared encryption key, distinguishing tunnel traffic from cover traffic is cryptographically impossible.

The system is fully self-hosted. No third-party platforms, no centralized infrastructure, no rate limits. Anyone can run their own relay with a single binary and their own domain.

---

## 2. Problem Statement

### 2.1 Traditional Tunnels Are Detectable

Every existing tunnel solution has the same fundamental weakness: it creates traffic that doesn't look like anything normal.

**VPN protocols (WireGuard, OpenVPN, IPSec):** Operate on non-standard ports or produce recognizable handshake patterns. DPI fingerprints them within milliseconds. Even with port randomization, the byte-level protocol patterns are catalogued.

**Obfuscated protocols (Shadowsocks, V2Ray, Trojan):** Disguise themselves as TLS/HTTPS but produce statistical anomalies — uniform packet sizes, symmetric upload/download ratios, missing HTTP semantics. ML-based DPI classifiers detect these with >95% accuracy by analyzing traffic shape.

**Domain fronting (CDN-based tunnels):** Relies on CDN providers allowing the technique. Most major CDNs (CloudFront, Cloudflare, Google) have explicitly blocked it. The remaining options are fragile and bandwidth-limited.

**DNS tunnels (iodine, dnscat2):** Extremely slow (50-100 Kbps). DNS queries have strict size limits. High query volume to a single domain is trivially flagged.

**HTTP tunnels (meek, websocket tunnels):** Better throughput but produce detectable patterns — persistent WebSocket connections with high throughput, uniform chunk sizes, missing typical HTTP browsing patterns.

### 2.2 The Detection Arms Race

The core problem: traditional tunnels try to make encrypted data look like "nothing" or like "something generic." But DPI evolves faster than obfuscation:

1. Protocol fingerprinting catches known protocols
2. Statistical analysis catches uniform/synthetic patterns
3. ML classifiers catch any traffic that doesn't match known application profiles
4. Active probing connects to suspected servers and tests their responses

### 2.3 Duman's Approach

Instead of hiding what the tunnel **is**, Duman hides what it **carries**:

- The protocol is real: PostgreSQL/MySQL wire protocol, every byte spec-compliant
- The traffic pattern is real: application-consistent query sequences with natural timing
- The data format is real: BYTEA payloads in analytics tables, exactly like Segment/Mixpanel/PostHog
- The server is real: connect with psql, run queries, get realistic data back
- Active probing fails: the relay IS a PostgreSQL server, just without an actual database engine

The tunnel data is hidden among cover data using the same syntax, same data types, same timing, same table structure. Distinguishing tunnel from cover requires the encryption key.

---

## 3. The Core Idea

Every tunnel ever built tries to hide what it IS. Duman hides what it CARRIES.

```
Traditional tunnel:
  Client ──[unknown encrypted blob]──► Server
  ISP: "What protocol is this? I don't recognize it. Flag it."

Duman:
  Client ──[SELECT * FROM products WHERE id = 42]──► Relay
  Client ──[INSERT INTO analytics (payload) VALUES ('\xAB..')]──► Relay
  Client ──[UPDATE cart SET qty = 2 WHERE user_id = 5]──► Relay
  Client ──[INSERT INTO analytics (payload) VALUES ('\xCD..')]──► Relay
  ISP: "PostgreSQL queries to a database. Normal developer activity."

  But: the 2nd and 4th queries carry encrypted tunnel data in the payload column.
  The 1st and 3rd queries are real-looking cover queries with real-looking responses.
  ISP cannot tell which is which. EVEN WITH FULL WIRE PROTOCOL DECRYPTION.
```

The relay is a self-hosted server running a fake PostgreSQL/MySQL engine. It speaks perfect wire protocol. It responds to cover queries with realistic fake data. It extracts tunnel chunks from specially-authenticated INSERT queries. No real database installed. Single Go binary, ~15 MB.

### 3.1 The Name

"Duman" means "smoke" in Turkish. Like smoke obscuring what's behind it — the traffic is visible, but what it carries is hidden. The system doesn't make traffic invisible; it makes it indistinguishable from normal traffic.

---

## 4. Why SQL Interleaving is Undetectable

### 4.1 Wire Protocol Level

A DPI device inspecting the connection (hypothetically, past TLS) sees:

```
Query 1: SELECT id, name, price FROM products WHERE category_id = 3 LIMIT 20
Response: RowDescription + 20 DataRows with realistic product data
→ Normal SQL query ✓

Query 2: INSERT INTO analytics_events (session_id, event_type, payload)
         VALUES ('a8f3e2c1-...', 'page_view', E'\\x89ABCDEF...')
Response: CommandComplete "INSERT 0 1"
→ Normal analytics INSERT with binary payload ✓   ← THIS IS TUNNEL

Query 3: SELECT count(*) FROM orders WHERE created_at > '2024-01-01'
Response: RowDescription + 1 DataRow: 1847
→ Normal aggregate query ✓

Query 4: INSERT INTO analytics_events (session_id, event_type, payload)
         VALUES ('a8f3e2c1-...', 'click', E'\\x01234567...')
Response: CommandComplete "INSERT 0 1"
→ Normal analytics INSERT with binary payload ✓   ← THIS IS ALSO TUNNEL
```

The DPI cannot distinguish Query 2 and 4 as tunnel. They are syntactically identical to real analytics INSERTs. The payload column is BYTEA — binary data is expected. Every analytics system (Segment, Mixpanel, PostHog) writes binary blobs to databases.

### 4.2 Behavioral Level

```
Normal developer's PostgreSQL session:
  - Mix of SELECT, INSERT, UPDATE queries
  - Analytics INSERT every 5-30 seconds
  - BYTEA payloads in analytics (tracking pixels, session replay data)
  - Persistent connection, periodic activity
  - Burst during page loads, idle during reading

Duman client's PostgreSQL session:
  - Mix of SELECT, INSERT, UPDATE queries        (identical)
  - Analytics INSERT every 5-30 seconds           (identical)
  - BYTEA payloads in analytics                   (identical)
  - Persistent connection, periodic activity      (identical)
  - Burst during page loads, idle during reading  (identical)

Statistical difference: ZERO
```

The Real Query Engine produces genuinely realistic query patterns modeled on real application behavior. The timing profiles match real page-load bursts, reading pauses, and background analytics writes.

### 4.3 Even With Full Database Access

If an attacker gains access to the relay and reads the analytics_events table:

```
id | session_id | event_type       | page_url          | payload
───┼────────────┼──────────────────┼───────────────────┼──────────────
1  | a8f3e2c1   | page_view        | /products         | NULL
2  | a8f3e2c1   | page_view        | /products/42      | \\x89ABCDEF...
3  | a8f3e2c1   | click            | /products/42      | \\x01234567...
4  | a8f3e2c1   | add_to_cart      | /cart             | NULL
5  | a8f3e2c1   | conversion_pixel | /checkout         | \\xFEDCBA98...
6  | a8f3e2c1   | page_view        | /checkout/confirm | NULL
```

Some rows have payload, some don't. Normal analytics behavior. Rows 2, 3, 5 have encrypted BYTEA. Are they tunnel? Or real analytics? Without the encryption key, impossible to tell. Conversion tracking pixels are ALWAYS encrypted binary — that's industry standard.

### 4.4 Active Probing Resistance

An investigator connecting with psql gets a fully functional database experience:

```
$ psql -h relay.example.com -U sensor_writer -d telemetry

telemetry=> \dt
              List of relations
 Schema |       Name        | Type  |     Owner
--------+-------------------+-------+---------------
 public | analytics_events  | table | sensor_writer
 public | categories        | table | sensor_writer
 public | products          | table | sensor_writer
 ...
(10 rows)

telemetry=> SELECT * FROM products WHERE price > 500 LIMIT 3;
 id  |          name          | price  | category_id | stock
-----+------------------------+--------+-------------+------
  12 | Samsung 65" OLED TV    | 1299.99|      1      |   15
  28 | MacBook Pro 14" M3     | 1999.99|      1      |    8
  45 | Canon EOS R5 Camera    |  899.99|      1      |   23

telemetry=> DROP TABLE products;
ERROR:  permission denied for table products
```

Looks and feels like a real database. Every query returns consistent, realistic data. The fake engine responds to metadata queries (\dt, \d, SHOW TABLES), returns proper error messages, and maintains session state.

---

## 5. System Architecture

### 5.1 High-Level Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  USER'S MACHINE                                                             │
│                                                                             │
│  ┌─────────────┐                                                            │
│  │ Browser     │──── Direct to ISP ──── Internet (normal traffic)           │
│  │ Spotify     │     (untouched)                                            │
│  │ Email       │                                                            │
│  └─────────────┘                                                            │
│                                                                             │
│  ┌─────────────┐    ┌──────────────────────────────────────────────────┐    │
│  │ Firefox     │──► │ DUMAN CLIENT                                    │    │
│  │ Telegram    │    │                                                  │    │
│  │ curl        │    │  SOCKS5 ─► Splitter ─► Encryptor ─► Interleaver │    │
│  │ (tunneled)  │    │  Proxy     (16KB)      (ChaCha20)   (SQL mix)   │    │
│  └─────────────┘    │                                         │        │    │
│                     │  TUN/TAP (optional) ─► same pipeline    │        │    │
│                     │                                         │        │    │
│                     │  ┌──────────┐  ┌──────────┐  ┌─────────▼──────┐ │    │
│                     │  │ Real     │  │ Phantom  │  │ Provider       │ │    │
│                     │  │ Query    │  │ Browser  │  │ Layer          │ │    │
│                     │  │ Engine   │  │ (noise)  │  │                │ │    │
│                     │  │          │  │          │  │ PgWire Client  │ │    │
│                     │  │ Cover    │  │ Real     │  │ MySQL Client   │ │    │
│                     │  │ queries  │  │ HTTP     │  │ REST Client    │ │    │
│                     │  └──────────┘  └──────────┘  └───────┬────────┘ │    │
│                     │                                      │          │    │
│                     │  ┌──────────┐  ┌──────────────────┐  │          │    │
│                     │  │ Smoke    │  │ Bandwidth        │  │          │    │
│                     │  │ Screen   │  │ Governor         │  │          │    │
│                     │  │ (P2P)    │  │ (adaptive alloc) │  │          │    │
│                     │  └──────────┘  └──────────────────┘  │          │    │
│                     └──────────────────────────────────────┼──────────┘    │
│                                                            │               │
│  ISP sees: normal browsing + database connections + API calls              │
└────────────────────────────────────────────────────────────┼───────────────┘
                                                             │
                        ┌───────────────┬───────────────┬────┘
                        ▼               ▼               ▼
                ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
                │ Relay-A      │ │ Relay-B      │ │ Relay-C      │
                │ Fake PgSQL   │ │ Fake MySQL   │ │ REST API     │
                │ port 5432    │ │ port 3306    │ │ port 443     │
                │              │ │              │ │              │
                │ Cover query  │ │ Cover query  │ │ Cover GET    │
                │  → fake data │ │  → fake data │ │  → fake JSON │
                │ Tunnel INSERT│ │ Tunnel INSERT│ │ Tunnel POST  │
                │  → extract   │ │  → extract   │ │  → extract   │
                │  → decrypt   │ │  → decrypt   │ │  → decrypt   │
                │  → forward   │ │  → forward   │ │  → forward   │
                │  → internet  │ │  → internet  │ │  → internet  │
                └──────────────┘ └──────────────┘ └──────────────┘
```

### 5.2 Data Flow: Tunnel Request

```
1. App (Firefox) → SOCKS5 connect google.com:443
2. Duman client accepts TCP connection
3. TCP stream → 16KB chunk splitter
4. Each chunk → ChaCha20-Poly1305 encrypt (session key)
5. Encrypted chunk → interleaving queue
6. Interleaver mixes:
   a. Cover query: SELECT * FROM products WHERE category = 3
   b. Cover query: SELECT count(*) FROM orders ...
   c. Cover query: SELECT name FROM categories WHERE id = 3
   d. TUNNEL:      INSERT INTO analytics_events (...) VALUES (..., E'\\x[encrypted]')
   e. Cover query: SELECT count(*) FROM cart_items ...
7. All queries sent via PostgreSQL wire protocol (TLS) to Relay-A
8. Relay-A:
   a. Cover queries → Fake Data Engine → realistic responses
   b. Tunnel INSERT → HMAC verify → extract BYTEA → decrypt → forward to google.com
9. google.com responds → Relay encrypts response → stores in analytics_responses
10. Client: SELECT payload FROM analytics_responses WHERE session_id = $1
    OR: LISTEN/NOTIFY push
11. Client decrypts → forwards to Firefox via SOCKS5
12. Firefox renders google.com
```

### 5.3 Data Flow: Normal (Non-Tunneled) Traffic

```
1. Chrome opens youtube.com
2. DNS resolves normally through system resolver
3. TCP connection goes directly to YouTube's IP
4. ISP sees: normal HTTPS to YouTube
5. Duman is not involved at all — zero interception, zero overhead
```

---

## 6. Client Architecture

### 6.1 Component Overview

The client is a single Go binary (`duman-client`, ~15 MB) with these components:

| Component | Responsibility |
|---|---|
| **SOCKS5 Proxy** | Local proxy on 127.0.0.1:1080. Apps connect here for tunneled traffic. |
| **TUN Device** (optional) | Virtual network interface for system-wide or rule-based routing. |
| **Stream Splitter** | Breaks TCP streams into fixed-size chunks (default 16KB). |
| **Crypto Engine** | Encrypts/decrypts chunks with ChaCha20-Poly1305 or AES-256-GCM. |
| **Interleaving Engine** | Mixes cover queries and tunnel chunks with natural timing. |
| **Real Query Engine** | Generates realistic, application-consistent SQL cover queries. |
| **Provider Layer** | Manages connections to relays (PgWire, MySQL, REST providers). |
| **Phantom Browser** | Generates real HTTP browsing traffic as additional noise. |
| **Smoke Screen** | Generates P2P cover traffic to residential IPs. |
| **Bandwidth Governor** | Dynamically allocates bandwidth across tunnel/cover/noise. |
| **Pool Manager** | Manages relay rotation, health checks, failover. |

### 6.2 Startup Sequence

```
1. Load configuration (duman-client.yaml)
2. Initialize crypto (derive session keys from shared secret)
3. Start Phantom Browser (generates real HTTP traffic — looks like user opened browser)
4. Connect to Relay-A PostgreSQL (staggered, looks like app boot)
   - TLS handshake → SCRAM-SHA-256 auth → ParameterStatus exchange
   - Run initial cover queries (SELECT version(), SHOW server_version, etc.)
5. Connect to Relay-B MySQL (staggered +30s to +2m, looks like second service)
   - TLS handshake → mysql_native_password auth
   - Run initial cover queries
6. Connect to Relay-C REST API (staggered +30s to +2m)
   - HTTPS, bearer token auth
   - Initial GET /api/v2/status health check
7. Seed cover state (initial SELECT queries to populate app state machine)
8. Start Real Query Engine (background cover query generation begins)
9. Start Interleaving Engine (cover + tunnel mixing)
10. Start P2P Smoke Screen (if enabled)
11. Start SOCKS5 Proxy on configured address (default 127.0.0.1:1080)
12. Start TUN Device (if enabled, requires elevated privileges)
13. Ready — applications can connect
```

### 6.3 SOCKS5 Proxy

The primary interface for applications. Standard SOCKS5 (RFC 1928) with:

- **CONNECT** support (TCP tunneling)
- **UDP ASSOCIATE** support (UDP tunneling via chunked encapsulation)
- **Authentication:** None (local only, bound to 127.0.0.1) or username/password
- **DNS:** Remote resolution through relay (prevents DNS leaks)

```go
type SOCKS5Proxy struct {
    listenAddr  string              // "127.0.0.1:1080"
    auth        *SOCKS5Auth         // nil = no auth (local only)
    tunnel      *TunnelManager      // handles stream → chunk → encrypt → interleave
    dnsResolver *RemoteDNSResolver  // resolves DNS through relay, not locally
}

// Connection flow:
// 1. App connects to SOCKS5 proxy
// 2. SOCKS5 handshake (version, auth, connect request)
// 3. DNS resolution through relay (if domain name)
// 4. Create tunnel stream (assigned stream ID)
// 5. Bidirectional proxy: app ↔ SOCKS5 ↔ tunnel ↔ relay ↔ internet
```

### 6.4 TUN Device (Optional)

For system-wide or rule-based routing without per-app SOCKS5 configuration.

```go
type TUNDevice struct {
    name       string            // "duman0"
    subnet     string            // "10.10.0.0/16"
    mtu        int               // 1400 (accounts for tunnel overhead)
    routeRules []RouteRule       // which traffic to capture
    tunnel     *TunnelManager
}

type RouteRule struct {
    Type    RouteRuleType  // domain, ip_range, process, port
    Match   string         // "*.google.com", "192.168.0.0/16", "firefox", "443"
    Action  RouteAction    // tunnel, direct, block
}
```

**Platform-specific implementation:**
- **Linux:** TUN via `/dev/net/tun` + `iptables` MARK rules + policy routing. Per-process routing via cgroup v2 net_cls + iptables owner match.
- **macOS:** utun via `SystemConfiguration` framework + `pf` rules. Per-process routing via Network Extension framework (requires entitlement).
- **Windows:** Wintun driver + route table manipulation. Per-process routing via WFP (Windows Filtering Platform) callout driver.

### 6.5 Stream Management

Each SOCKS5 connection or TUN flow becomes a tunnel stream:

```go
type TunnelStream struct {
    StreamID    uint32           // unique per session
    Destination string           // "google.com:443"
    State       StreamState      // connecting, established, closing, closed
    
    // Outbound (app → relay → internet)
    sendBuffer  *ChunkBuffer     // incoming app data → 16KB chunks
    sendSeq     uint64           // chunk sequence number (monotonic)
    
    // Inbound (internet → relay → app)
    recvBuffer  *ReorderBuffer   // handles out-of-order chunk arrival
    recvSeq     uint64           // expected next sequence number
    
    // Flow control
    windowSize  int              // chunks in flight before backpressure
    rtt         time.Duration    // estimated round-trip time
}

type ChunkBuffer struct {
    chunkSize   int              // 16384 (16KB default)
    pending     [][]byte         // buffered data not yet chunked
    ready       chan *Chunk       // complete chunks ready for encryption
}
```

### 6.6 DNS Resolution

All DNS for tunneled traffic resolves through the relay to prevent DNS leaks:

```go
type RemoteDNSResolver struct {
    providers []Provider
}

// DNS query embedded as a special tunnel chunk with flag DNS_RESOLVE
// Relay resolves the domain, returns IP in response chunk
// Client caches result with relay-provided TTL

func (r *RemoteDNSResolver) Resolve(domain string) (net.IP, error) {
    // Check local cache first
    if ip, ok := r.cache.Get(domain); ok {
        return ip, nil
    }
    
    // Send DNS resolve request through tunnel
    chunk := &Chunk{
        Type:    ChunkTypeDNS,
        Payload: []byte(domain),
    }
    response := r.sendAndWait(chunk)
    
    // Cache and return
    ip := net.IP(response.Payload)
    r.cache.Set(domain, ip, response.TTL)
    return ip, nil
}
```

---

## 7. Relay (Server) Architecture

### 7.1 Component Overview

The relay is a single Go binary (`duman-relay`, ~15 MB) with these components:

| Component | Responsibility |
|---|---|
| **PgWire Server** | PostgreSQL wire protocol handler (from scratch, ~1500 LOC). |
| **MySQL Server** | MySQL wire protocol handler (from scratch, ~1200 LOC). |
| **REST API Server** | HTTPS endpoint handler with realistic API responses. |
| **Fake Data Engine** | Generates realistic, consistent fake data for cover queries. |
| **Tunnel Auth** | Validates HMAC in INSERT queries to identify tunnel chunks. |
| **Tunnel Engine** | Extracts, decrypts, and processes tunnel chunks. |
| **Exit Engine** | Maintains outbound connection pool to internet destinations. |
| **Response Queue** | Stores encrypted response chunks for client retrieval. |
| **TLS Manager** | Automatic Let's Encrypt certificate via ACME. |

### 7.2 What ISP / Port Scanner Sees

```
Port scan:  5432/tcp open postgresql PostgreSQL 16.2
TLS check:  Valid Let's Encrypt certificate for db.iot-analytics.io
Protocol:   PostgreSQL v3 wire protocol, every byte spec-compliant
Auth:       MD5 or SCRAM-SHA-256 (rejects wrong passwords)
Queries:    Real SQL syntax, real result sets, real error messages
psql test:  \dt shows tables, SELECT returns rows, \d shows columns
Version:    Server identifies as "PostgreSQL 16.2 on x86_64-pc-linux-gnu"

It IS a PostgreSQL server. Except there's no PostgreSQL installed.
```

### 7.3 Dual Operation

Every incoming query is classified into exactly one of two categories:

```go
type FakeDatabaseRelay struct {
    pgHandler     *PgWireHandler
    mysqlHandler  *MySQLWireHandler
    restHandler   *RESTHandler
    
    dataEngine    *FakeDataEngine      // responds to cover queries
    tunnelEngine  *TunnelEngine        // processes tunnel chunks
    authVerifier  *TunnelAuthVerifier  // distinguishes cover from tunnel
}

func (fdr *FakeDatabaseRelay) HandleQuery(query ParsedQuery) Response {
    if fdr.authVerifier.IsTunnelQuery(query) {
        // TUNNEL: extract encrypted chunk, forward to destination
        chunk := fdr.extractChunk(query)
        fdr.tunnelEngine.Process(chunk)
        return CommandComplete("INSERT 0 1")
    }
    
    // COVER: generate realistic fake response
    return fdr.dataEngine.Execute(query)
}
```

**Cover query:** Any query WITHOUT a valid HMAC in the metadata field. Gets a realistic fake response from the Fake Data Engine.

**Tunnel query:** An INSERT with a valid HMAC in a specific field (disguised as a tracking pixel ID). Gets an "INSERT 0 1" acknowledgment while the BYTEA payload is extracted, decrypted, and forwarded.

### 7.4 Exit Engine

The relay acts as an exit node, maintaining outbound connections:

```go
type ExitEngine struct {
    connPool    map[string]net.Conn   // destination → persistent connection
    poolMu      sync.RWMutex
    dialer      *net.Dialer
    maxIdle     time.Duration         // 5 minutes
    maxConns    int                   // 1000
}

// For each tunnel stream:
// 1. Client sends CONNECT chunk (stream_id, destination)
// 2. Exit engine dials destination (or reuses pooled connection)
// 3. Subsequent DATA chunks forwarded to destination
// 4. Response data encrypted, queued for client retrieval
// 5. FIN chunk closes the connection
```

### 7.5 Startup Sequence (Relay)

```
1. Load configuration (duman-relay.yaml)
2. Initialize TLS (ACME Let's Encrypt or provided certificate)
3. Generate fake data based on configured scenario + seed
4. Start TLS listener on configured port(s):
   - PostgreSQL: 5432 (default)
   - MySQL: 3306 (default)
   - REST API: 443 (default)
5. Initialize exit engine (outbound connection pool)
6. Accept connections:
   a. Authenticate (MD5/SCRAM for SQL, token for API)
   b. For each query/request:
      - Is it a tunnel query? (HMAC check)
        → Extract → decrypt → forward to internet → queue response
      - Is it a cover query?
        → Fake Data Engine → realistic response
      - Is it a metadata query? (\dt, SHOW TABLES)
        → Fake schema information
      - Is it unknown/unsupported?
        → Realistic SQL error message
```

### 7.6 Memory-Only Operation

The relay stores NOTHING to disk. All state is in-memory and ephemeral:

- Fake data: generated from seed at startup, held in memory
- Tunnel chunks: processed immediately, never persisted
- Response queue: ring buffer with configurable max size, auto-expired
- Connection state: in-memory maps, garbage collected on disconnect
- No logs by default (optional structured logging to stdout only)

---

## 8. Wire Protocols

### 8.1 PostgreSQL Wire Protocol (From Scratch)

Implemented from scratch in Go, ~1500 LOC. Full PostgreSQL v3 wire protocol specification compliance.

**Client → Relay messages (7 types):**

| Byte | Name | Purpose |
|---|---|---|
| — | StartupMessage | Connection init (user, database, params) |
| `p` | PasswordMessage | Auth response (MD5 hash or SCRAM) |
| `Q` | Query | Simple query (SQL text) |
| `P` | Parse | Prepared statement define |
| `B` | Bind | Prepared statement params (**TUNNEL CHUNKS GO HERE**) |
| `E` | Execute | Run prepared statement |
| `S` | Sync | Batch delimiter |

**Relay → Client messages (11 types):**

| Byte | Name | Purpose |
|---|---|---|
| `R` | Authentication | Auth challenge (MD5 salt, SCRAM challenge) or AuthOK |
| `S` | ParameterStatus | Server version, encoding, timezone, etc. |
| `K` | BackendKeyData | Process ID + cancel key |
| `T` | RowDescription | Result column definitions (**COVER RESPONSES**) |
| `D` | DataRow | Result row data (**COVER DATA + TUNNEL RESPONSES**) |
| `C` | CommandComplete | Query finished ("SELECT 20", "INSERT 0 1") |
| `Z` | ReadyForQuery | Server idle, ready for next query |
| `E` | ErrorResponse | SQL error with SQLSTATE code (**PROBE RESISTANCE**) |
| `1` | ParseComplete | Prepared statement registered |
| `2` | BindComplete | Parameters bound |
| `A` | NotificationResponse | Async push (**TUNNEL RESPONSE SIGNAL**) |

**Authentication flow:**

```
Client: StartupMessage {user: "sensor_writer", database: "telemetry"}
Relay:  AuthenticationMD5Password {salt: [4 random bytes]}
Client: PasswordMessage {md5(md5(password + user) + salt)}
Relay:  AuthenticationOK
Relay:  ParameterStatus {server_version: "16.2"}
Relay:  ParameterStatus {server_encoding: "UTF8"}
Relay:  ParameterStatus {client_encoding: "UTF8"}
Relay:  ParameterStatus {DateStyle: "ISO, MDY"}
Relay:  ParameterStatus {TimeZone: "UTC"}
Relay:  BackendKeyData {pid: 12345, secret: 0xABCDEF01}
Relay:  ReadyForQuery {status: 'I' (idle)}
```

This is byte-identical to a real PostgreSQL 16.2 server. DPI and active probes cannot distinguish.

**SCRAM-SHA-256 flow (recommended for production):**

```
Client: StartupMessage {user: "sensor_writer", database: "telemetry"}
Relay:  AuthenticationSASL {mechanisms: ["SCRAM-SHA-256"]}
Client: SASLInitialResponse {mechanism: "SCRAM-SHA-256", data: client-first-message}
Relay:  AuthenticationSASLContinue {data: server-first-message (salt, iteration count)}
Client: SASLResponse {data: client-final-message (proof)}
Relay:  AuthenticationSASLFinal {data: server-final-message (server signature)}
Relay:  AuthenticationOK
... (same ParameterStatus sequence)
```

### 8.2 MySQL Wire Protocol (From Scratch)

Implemented from scratch in Go, ~1200 LOC. Same concept, different byte format.

**Key differences from PostgreSQL:**
- Packet header: 3-byte length + 1-byte sequence number
- `COM_QUERY` for simple queries
- `COM_STMT_PREPARE` / `COM_STMT_EXECUTE` for prepared statements
- BLOB type instead of BYTEA
- `mysql_native_password` or `caching_sha2_password` auth
- Result sets: column count packet → column definition packets → EOF → row packets → EOF

**Authentication flow:**

```
Relay:  Handshake {protocol_version: 10, server_version: "8.0.36",
                    auth_plugin: "caching_sha2_password", salt: [20 bytes]}
Client: HandshakeResponse {user: "writer", database: "analytics",
                            auth_data: SHA256(password, salt)}
Relay:  OK {affected_rows: 0, status: SERVER_STATUS_AUTOCOMMIT}
```

### 8.3 Prepared Statement Binary Mode

After initial setup, tunnel chunks flow as pure binary with zero SQL text overhead:

```
Setup (once per connection):
  Client: Parse "INSERT INTO analytics_events
                  (session_id, event_type, page_url, user_agent, metadata, payload)
                  VALUES ($1, $2, $3, $4, $5, $6)"
  Relay:  ParseComplete

Each tunnel chunk (no SQL text, pure binary):
  Client: Bind {
    params: [
      session_uuid,                    // TEXT: "a8f3e2c1-4b5d-4c6e-8f7a-..."
      event_type,                      // TEXT: "conversion_pixel"
      page_url,                        // TEXT: "/checkout/confirm"
      user_agent,                      // TEXT: "Mozilla/5.0 ..."
      metadata_json_with_hmac,         // TEXT: '{"pixel_id":"px_a8f3e2c10b5d",...}'
      raw_encrypted_bytes              // BYTEA: [16384 bytes]
    ]
  }
  Client: Execute
  Client: Sync

  Relay:  BindComplete
  Relay:  CommandComplete "INSERT 0 1"
  Relay:  ReadyForQuery

Wire overhead per chunk:
  Bind header:       5 bytes
  Param count:       2 bytes
  Param formats:     12 bytes (6 × 2)
  Param lengths:     24 bytes (6 × 4)
  Param data:        ~98 bytes (text params) + 16384 bytes (payload)
  Execute + Sync:    10 bytes
  Response:          ~30 bytes (BindComplete + CommandComplete + ReadyForQuery)
  ─────────────────────────────
  Total overhead:    ~181 bytes on 16384 byte payload = 1.1%
  SQL text overhead: ZERO (prepared statement reuse)
  Encoding overhead: ZERO (BYTEA is native binary, no base64)
```

---

## 9. Split Tunnel & Traffic Routing

### 9.1 Core Principle

The user's machine has two traffic flows running simultaneously:

**Direct traffic** — Normal internet usage (Chrome, Spotify, email, system updates). Goes directly to the ISP without any Duman involvement. Zero overhead, zero interception. This IS the camouflage — ISP sees normal internet activity alongside database connections.

**Tunneled traffic** — Only specific applications or destinations routed through Duman. Enters via SOCKS5 proxy or TUN device rules. Encrypted, chunked, embedded in SQL queries, sent to relays.

This split is critical for both performance and stealth:
- Performance: only selected traffic bears tunnel overhead
- Stealth: normal browsing traffic running alongside database connections is the most natural profile for a developer

### 9.2 Three Routing Modes

#### Mode 1: SOCKS5 Proxy (Manual / Per-App)

Simplest mode. User configures specific applications to use `127.0.0.1:1080` as SOCKS5 proxy:

```
Firefox  → SOCKS5 (127.0.0.1:1080) → Duman → Relay → Internet
Chrome   → Direct → ISP → Internet (untouched)
Spotify  → Direct → ISP → Internet (untouched)
Telegram → SOCKS5 (127.0.0.1:1080) → Duman → Relay → Internet
```

**Pros:** Simple, no elevated privileges needed, per-app control.
**Cons:** Each app must be individually configured. Some apps don't support SOCKS5.

#### Mode 2: TUN Device + Rule-Based Routing

Virtual network interface captures traffic based on rules:

```yaml
routing:
  mode: tun
  rules:
    # By domain (resolved via relay DNS)
    - match: "*.google.com"
      action: tunnel
    - match: "*.twitter.com"
      action: tunnel
    
    # By IP range
    - match: "10.0.0.0/8"
      action: direct       # local network always direct
    - match: "192.168.0.0/16"
      action: direct
    
    # By port
    - match: "port:22"
      action: tunnel       # SSH through tunnel
    
    # Default
    - match: "*"
      action: direct       # everything else goes direct
```

**Pros:** System-wide, works with all apps, granular control.
**Cons:** Requires elevated privileges (root/admin) for TUN device.

#### Mode 3: Per-Process Routing

Captures traffic from specific processes regardless of destination:

```yaml
routing:
  mode: process
  rules:
    - process: "firefox"
      action: tunnel
    - process: "telegram-desktop"
      action: tunnel
    - process: "curl"
      action: tunnel
    - process: "*"
      action: direct
```

**Implementation:**
- **Linux:** cgroup v2 net_cls classifier + iptables `-m owner --pid-owner` + MARK + policy routing
- **macOS:** Network Extension framework with per-app VPN profile
- **Windows:** WFP (Windows Filtering Platform) callout driver with process ID matching

**Pros:** Transparent to apps, no per-app configuration needed.
**Cons:** Platform-specific implementation, elevated privileges needed.

### 9.3 DNS Leak Prevention

For tunneled traffic, DNS must also go through the relay:

```
Tunneled app requests google.com:
  1. App → SOCKS5 CONNECT "google.com:443"
  2. Duman client does NOT resolve locally
  3. DNS query sent as tunnel chunk to relay
  4. Relay resolves google.com (1.1.1.1 DoH or system resolver)
  5. Relay returns IP to client
  6. Client establishes tunnel stream to that IP

Non-tunneled app requests youtube.com:
  1. Chrome → system DNS resolver → ISP DNS (or configured DoH)
  2. Normal resolution, Duman not involved
```

For TUN mode, DNS interception rules ensure tunneled destinations resolve through relay:

```go
type DNSInterceptor struct {
    tunnelDomains  []string        // domains that should resolve through relay
    localResolver  net.Resolver    // system resolver for direct traffic
    relayResolver  *RemoteDNSResolver
}

func (di *DNSInterceptor) Resolve(domain string) (net.IP, bool) {
    if di.shouldTunnel(domain) {
        ip, _ := di.relayResolver.Resolve(domain)
        return ip, true // true = route through tunnel
    }
    ip, _ := di.localResolver.LookupHost(domain)
    return ip, false // false = route direct
}
```

### 9.4 ISP's View of Split Tunnel

```
ISP traffic analysis for this user:

Connection           Destination            Volume     Pattern
────────────────────────────────────────────────────────────────
HTTPS (TLS 1.3)      youtube.com            500 MB     Video streaming (direct)
HTTPS (TLS 1.3)      accounts.google.com    2 MB       Login (direct)
HTTPS (TLS 1.3)      open.spotify.com       80 MB      Music streaming (direct)
PgSQL (TLS 1.3)      db.iot-analytics.io    200 MB     Database queries (tunnel)
MySQL (TLS 1.3)      db.shop-metrics.io     100 MB     Database queries (tunnel)
HTTPS (TLS 1.3)      api.weather-data.io    50 MB      API calls (tunnel)
HTTPS (TLS 1.3)      github.com             30 MB      Normal browsing (direct)
HTTPS (TLS 1.3)      stackoverflow.com      15 MB      Normal browsing (direct)

Profile: "Full-stack developer streaming music while working on a project"
Suspicion level: ZERO
```

---

## 10. Real Query Engine

The engine that makes Duman undetectable. Generates realistic, application-consistent SQL queries that tell a coherent story of a real application being used.

### 10.1 Application State Machine

Each scenario maintains state so queries are logically consistent:

```go
type AppStateMachine struct {
    Scenario     AppScenario       // ecommerce, iot, saas, blog, project
    CurrentPage  string            // where the "user" is in the app
    SessionStart time.Time         // session began
    CartItems    []int             // e-commerce: items in cart
    SelectedID   int               // current product/device/post being viewed
    UserID       int               // simulated logged-in user
    QueryHistory []QueryRecord     // recent queries for consistency
    NavHistory   []string          // page navigation path
    Timestamp    time.Time         // for time-based queries
}

// State transitions model real user navigation:
// E-commerce: /products → /products/42 → /cart → /checkout
// IoT:        /dashboard → /devices/sensor-1 → /devices/sensor-1/metrics
// SaaS:       /dashboard → /settings → /billing → /usage
// Blog:       / → /posts/hello-world → /posts/hello-world#comments
// Project:    /board → /tasks/123 → /tasks/123/comments

// Each transition triggers the queries that page would naturally make
// Transitions follow realistic probabilities:
//   Browse → View product: 60%
//   View → Add to cart: 25%
//   View → Back to browse: 45%
//   View → View related: 30%
//   Cart → Checkout: 40%
//   Cart → Continue shopping: 60%
```

### 10.2 E-Commerce Scenario Queries

```go
func (sm *AppStateMachine) BrowseProducts() QueryBatch {
    cat := sm.randomCategory()
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT id, name, price, image_url, rating FROM products WHERE category_id = %d AND stock > 0 ORDER BY created_at DESC LIMIT 20", cat),
            fmt.Sprintf("SELECT name FROM categories WHERE id = %d", cat),
            fmt.Sprintf("SELECT count(*) FROM products WHERE category_id = %d AND stock > 0", cat),
            fmt.Sprintf("SELECT count(*) FROM cart_items WHERE user_id = %d", sm.UserID),
        },
        BurstSpacing: 15 * time.Millisecond, // all fire within ~60ms like a real page load
    }
}

func (sm *AppStateMachine) ViewProduct() QueryBatch {
    pid := sm.SelectedID
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT * FROM products WHERE id = %d", pid),
            fmt.Sprintf("SELECT url, alt_text FROM product_images WHERE product_id = %d ORDER BY sort_order", pid),
            fmt.Sprintf("SELECT r.id, r.rating, r.comment, u.name, r.created_at FROM reviews r JOIN users u ON r.user_id = u.id WHERE r.product_id = %d ORDER BY r.created_at DESC LIMIT 10", pid),
            fmt.Sprintf("SELECT id, name, price FROM products WHERE category_id = (SELECT category_id FROM products WHERE id = %d) AND id != %d LIMIT 4", pid, pid),
        },
        BurstSpacing: 20 * time.Millisecond,
    }
}

func (sm *AppStateMachine) AddToCart() QueryBatch {
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("INSERT INTO cart_items (user_id, product_id, quantity) VALUES (%d, %d, 1) ON CONFLICT (user_id, product_id) DO UPDATE SET quantity = cart_items.quantity + 1", sm.UserID, sm.SelectedID),
            fmt.Sprintf("SELECT p.name, p.price, ci.quantity FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = %d", sm.UserID),
        },
    }
}

func (sm *AppStateMachine) Checkout() QueryBatch {
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT p.name, p.price, ci.quantity FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = %d", sm.UserID),
            fmt.Sprintf("SELECT sum(p.price * ci.quantity) AS total FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = %d", sm.UserID),
            fmt.Sprintf("SELECT address_line1, city, postal_code, country FROM addresses WHERE user_id = %d AND is_default = true", sm.UserID),
        },
    }
}

func (sm *AppStateMachine) Search(term string) QueryBatch {
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT id, name, price, rating FROM products WHERE name ILIKE '%%%s%%' OR description ILIKE '%%%s%%' ORDER BY rating DESC LIMIT 20", term, term),
            fmt.Sprintf("SELECT count(*) FROM products WHERE name ILIKE '%%%s%%' OR description ILIKE '%%%s%%'", term, term),
        },
    }
}
```

### 10.3 IoT Dashboard Scenario Queries

```go
func (sm *AppStateMachine) DeviceOverview() QueryBatch {
    return QueryBatch{
        Queries: []string{
            "SELECT id, name, type, status, last_seen, firmware_version FROM devices ORDER BY last_seen DESC LIMIT 50",
            "SELECT count(*) FILTER (WHERE status = 'online') AS online, count(*) FILTER (WHERE status = 'offline') AS offline, count(*) FILTER (WHERE status = 'warning') AS warning FROM devices",
            "SELECT d.name, a.severity, a.message, a.created_at FROM alerts a JOIN devices d ON a.device_id = d.id WHERE a.resolved = false ORDER BY a.created_at DESC LIMIT 10",
        },
    }
}

func (sm *AppStateMachine) DeviceMetrics() QueryBatch {
    dev := sm.SelectedID
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT recorded_at, cpu_usage, memory_mb, disk_pct, temperature, network_in, network_out FROM metrics WHERE device_id = '%d' AND recorded_at > now() - interval '1 hour' ORDER BY recorded_at", dev),
            fmt.Sprintf("SELECT avg(cpu_usage), max(cpu_usage), avg(memory_mb), max(temperature) FROM metrics WHERE device_id = '%d' AND recorded_at > now() - interval '24 hours'", dev),
            fmt.Sprintf("SELECT * FROM alerts WHERE device_id = '%d' AND resolved = false ORDER BY severity DESC", dev),
        },
    }
}

func (sm *AppStateMachine) WriteMetric() QueryBatch {
    dev := sm.randomDevice()
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("INSERT INTO metrics (device_id, cpu_usage, memory_mb, disk_pct, temperature, network_in, network_out) VALUES ('%d', %.1f, %d, %.1f, %.1f, %d, %d)",
                dev, randFloat(5, 95), randInt(128, 2048), randFloat(10, 90),
                randFloat(18, 45), randInt(1000, 50000), randInt(500, 30000)),
        },
    }
}
```

### 10.4 SaaS Dashboard Scenario Queries

```go
func (sm *AppStateMachine) SaaSDashboard() QueryBatch {
    return QueryBatch{
        Queries: []string{
            "SELECT count(*) AS total_users, count(*) FILTER (WHERE last_login > now() - interval '7 days') AS active_users FROM users",
            "SELECT plan_name, count(*) FROM subscriptions WHERE status = 'active' GROUP BY plan_name ORDER BY count DESC",
            "SELECT date_trunc('day', created_at) AS day, count(*) FROM users WHERE created_at > now() - interval '30 days' GROUP BY day ORDER BY day",
            "SELECT sum(amount) AS mrr FROM invoices WHERE status = 'paid' AND period_start >= date_trunc('month', now())",
            "SELECT feature_name, count(*) FROM usage_events WHERE recorded_at > now() - interval '24 hours' GROUP BY feature_name ORDER BY count DESC LIMIT 10",
        },
    }
}

func (sm *AppStateMachine) SaaSUserDetail() QueryBatch {
    uid := sm.SelectedID
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT u.*, o.name AS org_name FROM users u LEFT JOIN orgs o ON u.org_id = o.id WHERE u.id = %d", uid),
            fmt.Sprintf("SELECT plan_name, status, current_period_start, current_period_end FROM subscriptions WHERE user_id = %d ORDER BY created_at DESC LIMIT 1", uid),
            fmt.Sprintf("SELECT id, amount, status, created_at FROM invoices WHERE user_id = %d ORDER BY created_at DESC LIMIT 10", uid),
            fmt.Sprintf("SELECT feature_name, count(*), max(recorded_at) AS last_used FROM usage_events WHERE user_id = %d AND recorded_at > now() - interval '30 days' GROUP BY feature_name ORDER BY count DESC", uid),
        },
    }
}
```

### 10.5 Blog/CMS Scenario Queries

```go
func (sm *AppStateMachine) BlogHomepage() QueryBatch {
    return QueryBatch{
        Queries: []string{
            "SELECT p.id, p.title, p.slug, p.excerpt, p.featured_image, p.published_at, a.name AS author FROM posts p JOIN authors a ON p.author_id = a.id WHERE p.status = 'published' ORDER BY p.published_at DESC LIMIT 10",
            "SELECT t.name, count(*) FROM tags t JOIN post_tags pt ON t.id = pt.tag_id GROUP BY t.name ORDER BY count DESC LIMIT 20",
            "SELECT count(*) FROM posts WHERE status = 'published'",
        },
    }
}

func (sm *AppStateMachine) BlogPost() QueryBatch {
    pid := sm.SelectedID
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT p.*, a.name AS author, a.avatar_url FROM posts p JOIN authors a ON p.author_id = a.id WHERE p.id = %d", pid),
            fmt.Sprintf("SELECT t.name FROM tags t JOIN post_tags pt ON t.id = pt.tag_id WHERE pt.post_id = %d", pid),
            fmt.Sprintf("SELECT c.id, c.body, c.created_at, u.name FROM comments c JOIN users u ON c.user_id = u.id WHERE c.post_id = %d AND c.approved = true ORDER BY c.created_at ASC", pid),
            fmt.Sprintf("SELECT id, title, slug FROM posts WHERE id != %d AND status = 'published' ORDER BY published_at DESC LIMIT 5", pid),
            fmt.Sprintf("UPDATE posts SET view_count = view_count + 1 WHERE id = %d", pid),
        },
    }
}
```

### 10.6 Project Management Scenario Queries

```go
func (sm *AppStateMachine) ProjectBoard() QueryBatch {
    return QueryBatch{
        Queries: []string{
            "SELECT id, title, status, priority, assignee_id, due_date FROM tasks WHERE project_id = 1 AND sprint_id = (SELECT id FROM sprints WHERE project_id = 1 AND status = 'active') ORDER BY priority DESC, created_at",
            "SELECT status, count(*) FROM tasks WHERE project_id = 1 AND sprint_id = (SELECT id FROM sprints WHERE project_id = 1 AND status = 'active') GROUP BY status",
            "SELECT u.id, u.name, u.avatar_url, count(t.id) AS task_count FROM users u LEFT JOIN tasks t ON u.id = t.assignee_id AND t.sprint_id = (SELECT id FROM sprints WHERE project_id = 1 AND status = 'active') WHERE u.id IN (SELECT user_id FROM project_members WHERE project_id = 1) GROUP BY u.id, u.name, u.avatar_url",
        },
    }
}

func (sm *AppStateMachine) TaskDetail() QueryBatch {
    tid := sm.SelectedID
    return QueryBatch{
        Queries: []string{
            fmt.Sprintf("SELECT t.*, u.name AS assignee_name FROM tasks t LEFT JOIN users u ON t.assignee_id = u.id WHERE t.id = %d", tid),
            fmt.Sprintf("SELECT c.body, c.created_at, u.name FROM task_comments c JOIN users u ON c.user_id = u.id WHERE c.task_id = %d ORDER BY c.created_at ASC", tid),
            fmt.Sprintf("SELECT id, filename, size, uploaded_at FROM attachments WHERE task_id = %d", tid),
            fmt.Sprintf("SELECT action, field_name, old_value, new_value, u.name, h.created_at FROM task_history h JOIN users u ON h.user_id = u.id WHERE h.task_id = %d ORDER BY h.created_at DESC LIMIT 20", tid),
        },
    }
}
```

### 10.7 Query Timing Profiles

Real applications don't send queries at uniform intervals. The timing profiles model genuine user behavior:

```go
type TimingProfile struct {
    // Page load: N queries in a short burst (like a real page rendering)
    BurstSize     [2]int            // [4, 8] — random within range
    BurstWindow   time.Duration     // 200ms — all queries fire within this window
    IntraBurst    time.Duration     // 15-25ms between queries in a burst
    
    // User reading: silence while "user" reads the page
    ReadingPause  [2]time.Duration  // [2s, 30s] — random pause
    
    // Background: periodic events during reading pause
    BackgroundInterval [2]time.Duration // [5s, 30s]
    BackgroundType     []string         // ["analytics_write", "metric_update", "heartbeat"]
    
    // Navigation: probability of user clicking to a new page
    NavProbability float64           // 0.7 = 70% chance of navigating
    
    // Session: overall session behavior
    SessionDuration  [2]time.Duration // [5m, 45m]
    IdleTimeout      time.Duration    // 5m — longer idle = new "session"
}

// Named profiles matching real usage patterns:
var Profiles = map[string]TimingProfile{
    "casual_browser": {
        BurstSize: [2]int{3, 5}, ReadingPause: [2]time.Duration{8*s, 30*s},
        NavProbability: 0.5, // slow, thoughtful browsing
    },
    "power_user": {
        BurstSize: [2]int{5, 10}, ReadingPause: [2]time.Duration{2*s, 8*s},
        NavProbability: 0.85, // rapid clicking through pages
    },
    "api_worker": {
        BurstSize: [2]int{2, 4}, ReadingPause: [2]time.Duration{1*s, 5*s},
        NavProbability: 0.95, // automated-looking but varied timing
    },
    "dashboard_monitor": {
        BurstSize: [2]int{4, 8}, ReadingPause: [2]time.Duration{15*s, 60*s},
        NavProbability: 0.3, // long stares at dashboard, occasional drill-down
    },
}
```

---

## 11. Interleaving Engine

The orchestrator that mixes cover queries and tunnel chunks naturally.

### 11.1 Core Algorithm

```go
type InterleavingEngine struct {
    queryEngine    *RealQueryEngine     // generates cover queries
    tunnelQueue    chan *EncryptedChunk  // pending tunnel data
    providers      []Provider            // relay connections
    timingProfile  *TimingProfile
    
    // Ratio control
    coverPerTunnel int   // default 3: three cover queries per tunnel query
    
    // CRITICAL: tunnel queries use the SAME syntax, SAME table, SAME column types
    // as cover analytics queries. Only difference: authenticated HMAC in metadata.
}

func (ie *InterleavingEngine) Run(ctx context.Context) {
    for {
        // 1. Generate a "page load" burst
        batch := ie.queryEngine.NextBurst()
        
        for i, query := range batch.Queries {
            // Send cover query to a relay
            provider := ie.selectProvider()
            provider.SendQuery(query)
            
            // After every N cover queries, try to inject a tunnel chunk
            if (i+1) % ie.coverPerTunnel == 0 {
                select {
                case chunk := <-ie.tunnelQueue:
                    // Tunnel data pending — send as analytics INSERT
                    ie.sendTunnelAsAnalytics(provider, chunk)
                default:
                    // No tunnel data — send another cover query instead
                    // NEVER leave a timing gap where tunnel "should" be
                    extra := ie.queryEngine.RandomAnalyticsEvent()
                    provider.SendQuery(extra)
                }
            }
            
            // Inter-query delay within burst (natural spacing)
            time.Sleep(batch.BurstSpacing + jitter(5*time.Millisecond))
        }
        
        // 2. "Reading pause" — simulate user reading the page
        ie.backgroundPhase()
    }
}

func (ie *InterleavingEngine) backgroundPhase() {
    pause := randomDuration(ie.timingProfile.ReadingPause[0], ie.timingProfile.ReadingPause[1])
    bgInterval := randomDuration(ie.timingProfile.BackgroundInterval[0], ie.timingProfile.BackgroundInterval[1])
    ticker := time.NewTicker(bgInterval)
    timer := time.NewTimer(pause)
    defer ticker.Stop()
    defer timer.Stop()
    
    for {
        select {
        case <-timer.C:
            return // reading pause over, next page load burst
            
        case <-ticker.C:
            // Background activity during "reading"
            provider := ie.selectProvider()
            
            // Priority: send tunnel data if available, else send cover
            select {
            case chunk := <-ie.tunnelQueue:
                ie.sendTunnelAsAnalytics(provider, chunk)
            default:
                event := ie.queryEngine.RandomBackgroundQuery()
                provider.SendQuery(event)
            }
        }
    }
}
```

### 11.2 Adaptive Ratio

The cover-to-tunnel ratio adjusts based on tunnel demand:

```go
type AdaptiveRatio struct {
    baseRatio    int           // 3 (3 cover per 1 tunnel)
    currentRatio int           // adjusts dynamically
    queueDepth   int           // current tunnel queue size
    
    // High tunnel demand → reduce ratio (more tunnel, less cover)
    // Low tunnel demand → increase ratio (more cover, better camouflage)
    minRatio     int           // 1 (minimum: 1 cover per 1 tunnel)
    maxRatio     int           // 8 (maximum: 8 cover per 1 tunnel)
}

func (ar *AdaptiveRatio) Adjust(queueDepth int) {
    switch {
    case queueDepth > 100:
        ar.currentRatio = ar.minRatio // heavy traffic, prioritize tunnel
    case queueDepth > 50:
        ar.currentRatio = 2
    case queueDepth > 10:
        ar.currentRatio = ar.baseRatio
    case queueDepth == 0:
        ar.currentRatio = ar.maxRatio // no tunnel, maximize cover
    }
}
```

### 11.3 Traffic Composition Example

```
Typical 30-second window:

Query #  | Type               | SQL                                          | Tunnel?
─────────┼────────────────────┼──────────────────────────────────────────────┼────────
1        | Page load          | SELECT * FROM products WHERE category = 3    | No
2        | Page load          | SELECT name FROM categories WHERE id = 3     | No
3        | Page load          | SELECT count(*) FROM products WHERE ...      | No
4        | Analytics INSERT   | INSERT INTO analytics_events (...) VALUES ..  | YES
5        | Page load          | SELECT count(*) FROM cart_items WHERE ...     | No
6-8      | Idle               | (silence — user reading)                     | —
9        | Background metric  | INSERT INTO metrics (device_id, cpu...) ...  | No
10       | Analytics INSERT   | INSERT INTO analytics_events (...) VALUES .. | YES
11-15    | Idle               | (silence)                                    | —
16       | Navigation burst   | SELECT * FROM products WHERE id = 42         | No
17       | Navigation burst   | SELECT url FROM product_images WHERE ...     | No
18       | Analytics INSERT   | INSERT INTO analytics_events (...) VALUES .. | YES
19       | Navigation burst   | SELECT r.*, u.name FROM reviews r JOIN ...   | No
20       | Background metric  | INSERT INTO metrics (...) VALUES ...         | No

Tunnel queries:  3 out of 20 = 15%
Cover queries:  17 out of 20 = 85%
Pattern: indistinguishable from a real application
```

---

## 12. Tunnel Chunk Embedding

### 12.1 The Perfect Disguise: Analytics INSERT

Every modern application has an analytics/events table. This is Duman's hiding spot.

**Cover analytics INSERT (real-looking, no tunnel data):**

```sql
INSERT INTO analytics_events
  (session_id, event_type, page_url, user_agent, metadata, payload, created_at)
VALUES (
  'a8f3e2c1-4b5d-4c6e-8f7a-9b0c1d2e3f4a',
  'page_view',
  '/products/electronics',
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0',
  '{"referrer": "google.com", "viewport": "1920x1080", "duration_ms": 4200}',
  NULL,
  '2024-03-07 14:23:01+00'
);
```

**Tunnel analytics INSERT (encrypted chunk hidden inside, identical format):**

```sql
INSERT INTO analytics_events
  (session_id, event_type, page_url, user_agent, metadata, payload, created_at)
VALUES (
  'a8f3e2c1-4b5d-4c6e-8f7a-9b0c1d2e3f4a',
  'conversion_pixel',
  '/checkout/confirm',
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0',
  '{"pixel_id": "px_a8f3e2c10b5d", "campaign": "spring_sale", "value": 42.50}',
  E'\\x89ABCDEF0123456789ABCDEF0123456789ABCDEF...',
  '2024-03-07 14:23:05+00'
);
```

**The differences:**
- `event_type`: Uses types that naturally carry binary data — `conversion_pixel`, `heatmap_data`, `session_replay`, `error_report`
- `metadata.pixel_id`: Contains HMAC-based tunnel authentication token disguised as tracking pixel ID
- `payload`: Encrypted tunnel chunk as BYTEA

**Why it works:** Real analytics systems store binary blobs constantly — screenshots for session replay (FullStory, Hotjar), heatmap coordinate data, conversion tracking pixel data, error stack traces with core dumps. A BYTEA payload in an analytics table is the most normal thing in software engineering.

### 12.2 Authentication: Which INSERT is Tunnel?

The relay needs to know which INSERTs carry tunnel data vs. which are cover. The authentication is hidden in a natural-looking metadata field:

```go
// Client side: generate auth token that looks like a tracking pixel ID
func tunnelAuthToken(sharedSecret []byte, sessionID string, timestamp int64) string {
    mac := hmac.New(sha256.New, sharedSecret)
    mac.Write([]byte(sessionID))
    
    // 30-second window — same token valid for 30 seconds
    window := timestamp / 30
    binary.Write(mac, binary.BigEndian, window)
    
    hash := mac.Sum(nil)
    return "px_" + hex.EncodeToString(hash[:6]) // "px_a8f3e2c10b5d"
}

// Relay side: verify HMAC in metadata.pixel_id
func isTunnelQuery(metadata map[string]interface{}, sharedSecret []byte, sessionID string) bool {
    pixelID, ok := metadata["pixel_id"].(string)
    if !ok || !strings.HasPrefix(pixelID, "px_") {
        return false
    }
    
    now := time.Now().Unix()
    // Check current window and previous window (clock skew tolerance)
    for _, ts := range []int64{now, now - 30} {
        expected := tunnelAuthToken(sharedSecret, sessionID, ts)
        if hmac.Equal([]byte(pixelID), []byte(expected)) {
            return true
        }
    }
    return false
}
```

To an ISP or attacker, `"pixel_id": "px_a8f3e2c10b5d"` looks like a tracking pixel identifier from an ad network. Facebook Pixel, Google Analytics, Segment — they all use similar ID formats.

### 12.3 Chunk Structure

```go
type TunnelChunk struct {
    StreamID   uint32   // which tunnel stream this belongs to
    Sequence   uint64   // ordering within stream
    Type       ChunkType // DATA, CONNECT, DNS_RESOLVE, FIN, ACK, WINDOW_UPDATE
    Flags      uint8    // compressed, last_chunk, urgent
    Payload    []byte   // encrypted application data (max 16KB)
}

type ChunkType uint8
const (
    ChunkTypeData         ChunkType = 0x01  // application data
    ChunkTypeConnect      ChunkType = 0x02  // new stream (destination in payload)
    ChunkTypeDNSResolve   ChunkType = 0x03  // DNS query through relay
    ChunkTypeFIN          ChunkType = 0x04  // close stream
    ChunkTypeACK          ChunkType = 0x05  // acknowledge received chunks
    ChunkTypeWindowUpdate ChunkType = 0x06  // flow control
)

// Serialized format (before encryption):
// [4 bytes: stream_id] [8 bytes: sequence] [1 byte: type] [1 byte: flags]
// [2 bytes: payload_length] [N bytes: payload]
// Total header: 16 bytes
// Max payload: 16368 bytes (16KB - 16 byte header)
// Encrypted with 16-byte Poly1305 tag → max 16384 bytes in BYTEA
```

### 12.4 Event Types Used for Tunnel Chunks

Not all analytics event types carry binary payloads. Duman only uses event types where BYTEA is natural:

| Event Type | BYTEA Natural? | Used for Tunnel? | Real-World Analogue |
|---|---|---|---|
| `page_view` | No (usually NULL) | No — cover only | Standard pageview tracking |
| `click` | Rarely | No — cover only | Click tracking |
| `conversion_pixel` | Yes — always | **Yes** | Ad conversion tracking pixel data |
| `heatmap_data` | Yes — always | **Yes** | Heatmap coordinate dumps |
| `session_replay` | Yes — always | **Yes** | Session replay screenshot chunks |
| `error_report` | Yes — often | **Yes** | Error stack traces + core dumps |
| `ab_test_data` | Yes — sometimes | **Yes** | A/B test variant data |
| `add_to_cart` | No | No — cover only | E-commerce event |
| `form_submit` | No | No — cover only | Form submission tracking |

---

## 13. Fake Data Engine

The relay responds to cover queries with realistic, internally consistent fake data. No real database — all generated procedurally from a deterministic seed.

### 13.1 Design

```go
type FakeDataEngine struct {
    scenario    AppScenario         // determines which tables exist
    rng         *rand.Rand          // deterministic from seed (same seed = same data)
    
    // In-memory "tables" — generated at startup, evolve over time
    products    []FakeProduct       // 200 products (e-commerce scenario)
    users       []FakeUser          // 100 users
    orders      []FakeOrder         // grows over time (~5/hour)
    categories  []FakeCategory      // 10 categories
    devices     []FakeDevice        // 30 IoT devices (IoT scenario)
    metrics     *RingBuffer         // last 24h of metrics (rolling window)
    posts       []FakePost          // 50 posts (blog scenario)
    tasks       []FakeTask          // 100 tasks (project scenario)
    
    // Query parser — understands ~30 SQL patterns
    parser      *SimpleSQLParser
}
```

### 13.2 Query Understanding

The relay doesn't need a full SQL parser. It needs to understand ~30 query patterns per scenario:

```go
func (fde *FakeDataEngine) Execute(query string) Response {
    parsed := fde.parser.Parse(query)
    
    switch {
    // Product queries
    case parsed.Matches("SELECT % FROM products WHERE category_id = $1 %"):
        return fde.productsByCategory(parsed.IntParam(1), parsed)
    case parsed.Matches("SELECT % FROM products WHERE id = $1"):
        return fde.productByID(parsed.IntParam(1))
    case parsed.Matches("SELECT count(*) FROM products WHERE %"):
        return fde.productCount(parsed)
    case parsed.Matches("SELECT % FROM products WHERE % ILIKE %"):
        return fde.productSearch(parsed)
        
    // Cart queries
    case parsed.Matches("INSERT INTO cart_items %"):
        return CommandComplete("INSERT 0 1")
    case parsed.Matches("SELECT % FROM cart_items % WHERE user_id = $1"):
        return fde.cartItems(parsed)
        
    // Order queries
    case parsed.Matches("INSERT INTO orders %"):
        fde.orders = append(fde.orders, fde.generateOrder())
        return CommandComplete("INSERT 0 1")
    case parsed.Matches("SELECT count(*) FROM orders WHERE %"):
        return fde.orderCount(parsed)
        
    // Analytics (cover — acknowledge with no processing)
    case parsed.Matches("INSERT INTO analytics_events %"):
        return CommandComplete("INSERT 0 1")
        
    // Response polling
    case parsed.Matches("SELECT payload FROM analytics_responses WHERE %"):
        // This could be tunnel response fetch — handled by tunnel engine
        return fde.responseQuery(parsed)
        
    // Metrics
    case parsed.Matches("SELECT % FROM metrics WHERE device_id = $1 %"):
        return fde.deviceMetrics(parsed)
    case parsed.Matches("INSERT INTO metrics %"):
        return CommandComplete("INSERT 0 1")
        
    // Device queries
    case parsed.Matches("SELECT % FROM devices %"):
        return fde.deviceList(parsed)
        
    // Metadata queries (psql \dt, \d, \l, SHOW, etc.)
    case parsed.IsMetaQuery():
        return fde.handleMetaQuery(parsed)
    
    // UPDATE queries
    case parsed.Matches("UPDATE % SET % WHERE %"):
        return CommandComplete("UPDATE 1")
        
    default:
        // Unknown query — return empty result set (not an error)
        return EmptyResult()
    }
}
```

### 13.3 Data Generation

```go
// Products have realistic names, prices, and relationships
func (fde *FakeDataEngine) generateProducts() {
    categories := []FakeCategory{
        {1, "Electronics"}, {2, "Clothing"}, {3, "Books"},
        {4, "Home & Garden"}, {5, "Sports"}, {6, "Toys"},
        {7, "Food & Beverage"}, {8, "Beauty"}, {9, "Automotive"}, {10, "Health"},
    }
    
    for i := 0; i < 200; i++ {
        cat := categories[i%len(categories)]
        fde.products = append(fde.products, FakeProduct{
            ID:         i + 1,
            Name:       generateProductName(cat.Name, fde.rng),
            Price:      generatePrice(cat.Name, fde.rng),
            CategoryID: cat.ID,
            Stock:      fde.rng.Intn(100),
            Rating:     3.5 + fde.rng.Float64()*1.5,     // 3.5-5.0
            ImageURL:   fmt.Sprintf("/images/products/%d.jpg", i+1),
            CreatedAt:  randomPastDate(180, fde.rng),     // within last 6 months
        })
    }
}

// generateProductName produces realistic names per category:
//   Electronics: "Sony WH-1000XM5 Wireless Headphones"
//   Clothing:    "Levi's 501 Original Fit Jeans"
//   Books:       "The Midnight Library: A Novel"
//   IoT devices: "Raspberry Pi 4 Model B 8GB"
//   etc.

// CRITICAL: Same seed always produces same data (deterministic RNG)
// But data EVOLVES: orders increase, metrics update, devices go online/offline
// This makes the database feel alive over time
```

### 13.4 Schema Metadata (psql/DBeaver Compatibility)

The relay responds to metadata queries with realistic schema information:

```go
func (fde *FakeDataEngine) handleMetaQuery(parsed *ParsedQuery) Response {
    switch parsed.MetaType {
    case MetaListTables: // \dt
        tables := fde.scenario.Tables()
        // Returns: analytics_events, analytics_responses, categories,
        //          cart_items, devices, metrics, orders, products, reviews, users
        return fde.formatTableList(tables)
        
    case MetaDescribeTable: // \d products
        tableName := parsed.TableName
        columns := fde.scenario.ColumnsFor(tableName)
        return fde.formatColumnList(columns)
        
    case MetaShowVersion: // SELECT version() or SHOW server_version
        return SingleRow("PostgreSQL 16.2 on x86_64-pc-linux-gnu, compiled by gcc 12.3.0, 64-bit")
        
    case MetaShowDatabases: // \l
        return fde.formatDatabaseList()
        
    case MetaShowSetting: // SHOW timezone, SHOW client_encoding, etc.
        return fde.formatSetting(parsed.SettingName)
    }
    return EmptyResult()
}
```

### 13.5 Error Handling (Probe Resistance)

The relay returns realistic PostgreSQL error messages:

```go
func (fde *FakeDataEngine) handleError(query string) Response {
    // Permission denied for destructive operations
    if isDestructive(query) { // DROP, TRUNCATE, ALTER, DELETE without WHERE
        return ErrorResponse{
            Severity: "ERROR",
            Code:     "42501",
            Message:  "permission denied for table " + extractTableName(query),
        }
    }
    
    // Syntax error for malformed queries
    if !isSyntacticallyValid(query) {
        return ErrorResponse{
            Severity: "ERROR",
            Code:     "42601",
            Message:  fmt.Sprintf("syntax error at or near \"%s\"", extractNearToken(query)),
            Position: findErrorPosition(query),
        }
    }
    
    // Table not found for unknown tables
    table := extractTableName(query)
    if !fde.scenario.HasTable(table) {
        return ErrorResponse{
            Severity: "ERROR",
            Code:     "42P01",
            Message:  fmt.Sprintf("relation \"%s\" does not exist", table),
        }
    }
    
    return EmptyResult()
}
```

---

## 14. REST API Facade Layer

In addition to SQL protocols, relays can serve a REST API on port 443. Same concept: real-looking API endpoints with real-looking responses, tunnel data hidden in analytics/webhook POST bodies.

### 14.1 Endpoints

```
GET  /api/v2/products                      → real-looking product list JSON
GET  /api/v2/products/:id                  → real-looking product detail JSON
GET  /api/v2/products/search?q=...         → search results JSON
GET  /api/v2/categories                    → category list JSON
GET  /api/v2/dashboard/stats               → dashboard metrics JSON
GET  /api/v2/dashboard/charts/revenue      → time series JSON
POST /api/v2/analytics/events              → tunnel chunk (or cover analytics)
GET  /api/v2/analytics/sync?session=xxx    → tunnel response fetch
GET  /api/v2/status                        → service health check JSON
GET  /api/v2/health                        → liveness probe
GET  /docs                                 → OpenAPI/Swagger UI page
GET  /docs/openapi.json                    → OpenAPI specification
```

### 14.2 Cover API Responses

```json
// GET /api/v2/products?category=electronics&limit=5
{
  "data": [
    {
      "id": 12,
      "name": "Samsung 65\" OLED TV",
      "price": 1299.99,
      "rating": 4.7,
      "stock": 15,
      "image_url": "/images/products/12.jpg"
    },
    ...
  ],
  "pagination": {
    "total": 48,
    "page": 1,
    "per_page": 5,
    "pages": 10
  }
}

// GET /api/v2/dashboard/stats
{
  "users": {"total": 1247, "active_today": 89, "new_this_week": 23},
  "revenue": {"mtd": 45230.50, "ytd": 312450.00, "currency": "USD"},
  "orders": {"today": 12, "pending": 3, "shipped": 8}
}

// GET /api/v2/status
{
  "status": "healthy",
  "version": "2.4.1",
  "uptime": "14d 6h 23m",
  "database": "connected",
  "cache": "connected"
}
```

### 14.3 Tunnel via REST

```
POST /api/v2/analytics/events
Content-Type: application/json
Authorization: Bearer <api_key>

{
  "session_id": "a8f3e2c1-4b5d-4c6e-8f7a-9b0c1d2e3f4a",
  "event_type": "conversion_pixel",
  "page_url": "/checkout/confirm",
  "metadata": {
    "pixel_id": "px_a8f3e2c10b5d",    ← HMAC auth token
    "campaign": "spring_sale"
  },
  "payload": "iavN7wEjRWeJ0jRWeJ..."   ← base64 encoded encrypted chunk
}

Response:
{
  "status": "accepted",
  "event_id": "evt_9f8e7d6c5b4a",
  "sync": {                             ← inline response data (if available)
    "data": "base64_encrypted_response_chunk"
  }
}
```

### 14.4 OpenAPI/Swagger Page

The relay serves a real Swagger UI page at `/docs` with full API documentation. An investigator visiting the URL sees a professional API documentation page describing a product/analytics service. This further reinforces the facade.

---

## 15. Response Channel

### 15.1 SQL Response: SELECT from Response Table

```sql
-- Client polls for tunnel responses (looks like analytics sync query)
SELECT payload FROM analytics_responses
WHERE session_id = $1 AND consumed = FALSE
ORDER BY seq ASC LIMIT 50;

-- Mark as consumed
UPDATE analytics_responses SET consumed = TRUE WHERE id = ANY($1);
```

The relay maintains an in-memory ring buffer of response chunks per session. The `analytics_responses` table doesn't exist on disk — it's served from memory like all other fake data.

### 15.2 Push Mode: LISTEN/NOTIFY (PostgreSQL)

```sql
-- Client starts listening (standard PostgreSQL async notification)
LISTEN tunnel_resp;

-- When relay has response chunks ready, it sends notification:
-- (internally generated, not a real PostgreSQL NOTIFY)
NotificationResponse { channel: "tunnel_resp", payload: "" }

-- Client immediately fetches
SELECT payload FROM analytics_responses WHERE session_id = $1 AND consumed = FALSE;
```

Push latency: 1-5ms. ISP sees: "database connection with async notifications" — a normal pattern for real-time dashboards.

### 15.3 REST Response: Inline or Polling

**Inline mode (embedded in POST response):**

```json
POST /api/v2/analytics/events → 200
{
  "status": "accepted",
  "sync": {
    "chunks": [
      {"seq": 1, "data": "base64_encrypted_chunk_1"},
      {"seq": 2, "data": "base64_encrypted_chunk_2"}
    ]
  }
}
```

**Polling mode:**

```json
GET /api/v2/analytics/sync?session=abc&after=5 → 200
{
  "chunks": [
    {"seq": 6, "data": "base64_encrypted_chunk"},
    {"seq": 7, "data": "base64_encrypted_chunk"}
  ],
  "has_more": false
}
```

### 15.4 Response Ordering & Reassembly

Response chunks may arrive out of order (especially with multi-relay). The client uses a reorder buffer:

```go
type ReorderBuffer struct {
    streamID     uint32
    expected     uint64                // next expected sequence number
    buffer       map[uint64][]byte     // out-of-order chunks waiting
    maxGap       int                   // max chunks to buffer (1000)
    output       chan []byte            // in-order output
}

func (rb *ReorderBuffer) Insert(seq uint64, data []byte) {
    if seq == rb.expected {
        rb.output <- data
        rb.expected++
        // Flush any buffered consecutive chunks
        for {
            if next, ok := rb.buffer[rb.expected]; ok {
                rb.output <- next
                delete(rb.buffer, rb.expected)
                rb.expected++
            } else {
                break
            }
        }
    } else if seq > rb.expected {
        rb.buffer[seq] = data // buffer for later
    }
    // seq < expected = duplicate, ignore
}
```

---

## 16. Crypto Layer

### 16.1 Key Hierarchy

```
Configuration:
  shared_secret: 32 bytes (configured in both client and relay)

Session setup:
  session_id:  random UUID generated per connection
  session_key: HKDF-SHA256(shared_secret, "duman-session-v1" || session_id)
  
Per-direction keys:
  client_key:  HKDF-SHA256(session_key, "client-to-relay")
  relay_key:   HKDF-SHA256(session_key, "relay-to-client")

Auth token:
  HMAC-SHA256(shared_secret, session_id || floor(timestamp / 30))[:6]
  Formatted as "px_" + hex = "px_a8f3e2c10b5d"
```

### 16.2 Chunk Encryption

```
Algorithm selection (auto-detect):
  - AES-256-GCM if AES-NI hardware support detected
  - ChaCha20-Poly1305 otherwise (faster in software)

Per-chunk encryption:
  Nonce:     12 bytes = 4 zero bytes || 8 bytes big-endian sequence number
  Key:       client_key (outbound) or relay_key (inbound)
  AAD:       session_id || stream_id || sequence (authenticated but not encrypted)
  Plaintext: chunk header (16 bytes) + application data
  Output:    ciphertext + 16-byte authentication tag

  Total overhead per chunk: 16 bytes (Poly1305/GCM tag)
```

### 16.3 Perfect Forward Secrecy (Optional)

For additional security, an ephemeral X25519 key exchange can be performed at session start:

```go
// Client generates ephemeral X25519 keypair
// Sends public key in first analytics INSERT metadata as "device_fingerprint"
// Relay responds with its ephemeral public key in analytics_responses
// Both derive shared secret: X25519(ephemeral_private, peer_public)
// Session key: HKDF(X25519_shared || pre_shared_secret, ...)

// This provides PFS: compromising the pre-shared secret later
// does not compromise past sessions.
```

---

## 17. Multi-Relay & Multi-Protocol

### 17.1 Protocol Mix

Run multiple relays with different protocols for maximum stealth:

```
Relay-A: PostgreSQL (port 5432) — db.iot-analytics.io
Relay-B: MySQL (port 3306)      — db.shop-metrics.io
Relay-C: REST API (port 443)    — api.weather-data.io

ISP sees: "Developer connected to 2 databases and 1 API service"
Each relay independently processes tunnel chunks and cover queries
```

### 17.2 Chunk Distribution

```go
type MultiRelayDistributor struct {
    relays  []Provider
    weights []float64  // [0.45, 0.30, 0.25]
}

func (mrd *MultiRelayDistributor) SelectRelay() Provider {
    // Weighted random selection
    r := rand.Float64()
    cumulative := 0.0
    for i, w := range mrd.weights {
        cumulative += w
        if r < cumulative {
            return mrd.relays[i]
        }
    }
    return mrd.relays[len(mrd.relays)-1]
}
```

### 17.3 Exit Modes

**Mode A: Independent Exit (Simple)**

Each relay independently connects to the destination:

```
Client → Relay-A (chunk 1) → Exit A → Internet
Client → Relay-B (chunk 2) → Exit B → Internet
Client → Relay-C (chunk 3) → Exit C → Internet
```

Simple but destination sees multiple IPs. Suitable for general browsing where destination IP doesn't matter.

**Mode B: Single Exit (Stealth)**

All chunks routed to one designated exit relay:

```
Client → Relay-A → forward → Relay-C (exit) → Internet
Client → Relay-B → forward → Relay-C (exit) → Internet
Client → Relay-C (exit) → Internet
```

Destination sees single IP. Better for services that track IP consistency. Exit relay rotates periodically (15-30 min).

**Mode C: Chain (Maximum Anonymity)**

Multi-hop: Client → Relay-A → Relay-B → Relay-C (exit) → Internet. Each hop only knows the previous and next hop. Highest latency but maximum anonymity.

---

## 18. Noise Layers

### 18.1 Phantom Browser

A headless HTTP client that performs real web browsing directly (NOT through the tunnel). Generates genuine traffic to popular websites:

```go
type PhantomBrowser struct {
    region    string            // "turkey", "europe", "global"
    profiles  []BrowsingProfile
    client    *http.Client      // with realistic TLS fingerprint
}

type BrowsingProfile struct {
    Sites     []string          // ["google.com.tr", "youtube.com", "hurriyet.com.tr"]
    Pattern   BrowsePattern     // search_and_browse, video_watch, social_scroll, news_read
    Duration  time.Duration     // how long to browse each site
    Frequency time.Duration     // how often to switch sites
}

// Regional profiles:
// Turkey: google.com.tr, youtube.com, twitter.com, hurriyet.com.tr, trendyol.com
// Europe: google.com, youtube.com, twitter.com, bbc.com, amazon.de
// Global: google.com, youtube.com, reddit.com, github.com, stackoverflow.com
```

ISP sees normal browsing activity alongside database connections. This is the most natural traffic profile for a developer.

### 18.2 P2P Smoke Screen

TLS connections to residential peer IPs. Cover-only mode — encrypted noise with no real data:

```go
type SmokeScreen struct {
    peerCount  int             // 4 default
    profiles   []CoverProfile
}

type CoverProfile string
const (
    CoverVideoCall  CoverProfile = "video_call"   // 1-5 Mbps, symmetric
    CoverMessaging  CoverProfile = "messaging"     // 10-50 Kbps, bursty
    CoverFileSync   CoverProfile = "file_sync"     // 500 Kbps-2 Mbps, asymmetric
    CoverGaming     CoverProfile = "gaming"        // 100-500 Kbps, low latency pattern
)
```

Zero legal risk — these are just encrypted TLS connections with random data. ISP sees: "Video calls and messaging with colleagues." Normal remote work activity.

### 18.3 Decoy Connections

Direct HTTPS connections to popular developer sites as additional noise:

```go
type DecoyConnections struct {
    targets []string  // github.com, stackoverflow.com, npmjs.com, pkg.go.dev
    count   int       // 3 simultaneous
}
```

### 18.4 Complete ISP View

```
Connection           Protocol      Destination             Layer
─────────────────────────────────────────────────────────────────────
HTTPS (TLS 1.3)      HTTP          youtube.com             Direct (normal browsing)
HTTPS (TLS 1.3)      HTTP          accounts.google.com     Direct (normal browsing)
HTTPS (TLS 1.3)      HTTP          spotify.com             Direct (music streaming)
PostgreSQL (TLS)     pgwire        db.iot-analytics.io     Tunnel + Cover SQL
MySQL (TLS)          mysqlwire     db.shop-metrics.io      Tunnel + Cover SQL
HTTPS               REST          api.weather-data.io     Tunnel + Cover API
HTTPS               HTTP          google.com.tr           Phantom (real browse)
HTTPS               HTTP          hurriyet.com.tr         Phantom (real browse)
TLS                  TCP           78.162.x.x (residential) P2P cover (VideoCall)
TLS                  TCP           85.97.x.x (residential)  P2P cover (Messaging)
HTTPS               HTTP          github.com              Decoy
HTTPS               HTTP          stackoverflow.com       Decoy

12 connections, mix of:
  - Normal browsing (YouTube, Spotify, Google — direct)
  - Database traffic (developer's app backend — tunnel)
  - API traffic (developer's cloud service — tunnel)
  - Web browsing (developer research — phantom)
  - Residential connections (video calls with team — P2P cover)
  - Developer sites (normal developer behavior — decoy)

ISP profile: "Full-stack developer working on a project while streaming music"
Tunnel signal: 3 connections out of 12 carry tunnel data
Signal-to-noise: 25% — and the 25% is indistinguishable from cover queries
```

---

## 19. Bandwidth Governor

### 19.1 Bandwidth Allocation

```go
type BandwidthGovernor struct {
    totalBandwidth  int64   // detected or configured (bits/sec)
    
    // Budget allocation (percentages, must sum to 100)
    tunnelBudget    float64 // 50% — actual tunnel throughput
    coverBudget     float64 // 15% — cover queries (SQL + API)
    phantomBudget   float64 // 15% — phantom browsing
    p2pBudget       float64 //  5% — P2P smoke screen
    overheadBudget  float64 // 15% — TLS, framing, protocol overhead
}
```

### 19.2 Example: 100 Mbps Connection

```
100 Mbps total:

SQL tunnel (PgSQL + MySQL):    40 Mbps  (binary BYTEA, very low overhead)
API tunnel (REST):             10 Mbps  (JSON + base64, higher overhead)
Cover queries (SQL):           10 Mbps  (realistic SELECT/INSERT)
Cover queries (API):            5 Mbps  (realistic GET/POST)
Phantom browser:               15 Mbps  (real web browsing)
P2P cover:                      5 Mbps  (residential noise)
Overhead:                      15 Mbps  (TLS, framing, padding)
─────────────────────────────────────
Effective tunnel throughput:   ~50 Mbps
```

### 19.3 Adaptive Allocation

```go
// Heavy tunnel demand → reduce phantom + cover, increase tunnel
// Light tunnel demand → increase phantom (more camouflage)
// Zero tunnel demand → phantom + cover only (stealth mode)

func (bg *BandwidthGovernor) Adjust(tunnelDemand float64) {
    switch {
    case tunnelDemand > 0.8: // tunnel wants 80%+ of capacity
        bg.tunnelBudget = 0.65
        bg.coverBudget = 0.10
        bg.phantomBudget = 0.05
        bg.p2pBudget = 0.05
        bg.overheadBudget = 0.15
    case tunnelDemand < 0.1: // minimal tunnel usage
        bg.tunnelBudget = 0.10
        bg.coverBudget = 0.20
        bg.phantomBudget = 0.35
        bg.p2pBudget = 0.15
        bg.overheadBudget = 0.20
    default: // normal operation
        bg.tunnelBudget = 0.50
        bg.coverBudget = 0.15
        bg.phantomBudget = 0.15
        bg.p2pBudget = 0.05
        bg.overheadBudget = 0.15
    }
}
```

---

## 20. Relay Pool & Rotation

### 20.1 Pool Management

Large-scale deployment supports 100+ relays. The client maintains a pool and rotates connections:

```go
type RelayPool struct {
    relays       []RelayConfig
    active       []Provider       // 3 active connections (default)
    maxActive    int              // how many simultaneous connections
    
    // Health
    healthCheck  time.Duration    // 30 seconds
    failedRelays map[string]time.Time  // blocked relay → when it failed
    
    // Rotation
    rotationSeed string           // deterministic schedule from seed
    rotationInterval time.Duration // fast: 30s-5min, slow: 15-30min for exit
}
```

### 20.2 Relay Tiers

```go
type RelayTier int
const (
    TierTrusted    RelayTier = 3  // exit-capable, long-running, verified operator
    TierVerified   RelayTier = 2  // 3+ months uptime, passed audits
    TierCommunity  RelayTier = 1  // newly contributed, relay-only (no exit)
)

// Exit slot: only Trusted tier relays
// Relay slots: any tier
// New community relays: probation period before becoming Verified
```

### 20.3 Rotation Schedule

```go
// Seed-based stochastic schedule — deterministic but unpredictable
// Both client and relay pool know the schedule from the same seed

type RotationSchedule struct {
    seed     string
    rng      *rand.Rand
    
    // Fast rotation: relay slots switch every 30s-5min
    // Slow rotation: exit slot switches every 15-30min
    // Pre-warm: new relay connection established 30s before switch
    // Overlap: old and new relay both active during transition
}

func (rs *RotationSchedule) NextSwitch() (relay RelayConfig, at time.Time) {
    // Deterministic next relay + time from seed
    // Both client and pool coordinator compute the same schedule
}
```

### 20.4 Anti-Censorship

```go
// Blocked relay detection:
// 1. TCP connect fails → mark as blocked
// 2. TLS handshake fails → mark as blocked
// 3. Auth fails (wrong response format) → mark as blocked (MITM suspected)
// 4. Latency spike > 5x baseline → mark as suspicious

// Auto-reroute: skip blocked relays, use next in rotation schedule
// Community relay contribution: anyone runs the Go binary with their own domain
```

---

## 21. Security Model

### 21.1 Threat Model: What Each Party Knows

**ISP (passive observer):**
- Sees: PostgreSQL/MySQL/HTTPS connections to cloud IPs
- Sees: TLS-encrypted traffic (cannot read queries)
- Cannot: distinguish cover queries from tunnel queries
- Cannot: determine that the relay is not a real database
- Conclusion: "Normal developer activity"

**ISP (active, hypothetical TLS MitM):**
- Sees: SQL queries and responses in cleartext
- Sees: Mix of SELECT, INSERT, UPDATE with realistic data
- Sees: analytics_events INSERTs with BYTEA payloads
- Cannot: distinguish BYTEA tunnel data from BYTEA analytics data
- Cannot: decrypt BYTEA without shared secret
- Conclusion: "Application with analytics, normal"

**Active prober (connects to relay):**
- Sees: Real PostgreSQL experience (\dt, SELECT, metadata)
- Sees: Realistic fake data, proper error messages
- Cannot: send tunnel queries without shared secret
- Cannot: distinguish relay from real PostgreSQL 16.2
- Conclusion: "Real database server"

**Relay operator:**
- Sees: All queries (cover + tunnel)
- Knows: Which queries are tunnel (HMAC auth)
- Can: Decrypt tunnel chunks
- Sees: Destination of tunnel traffic (exit node)
- Does NOT: Store anything to disk (memory only, ephemeral)
- Risk: Relay operator is trusted (self-hosted model)

### 21.2 What Duman Does NOT Protect Against

- **Endpoint compromise:** If the user's machine is compromised, tunnel is irrelevant
- **Relay compromise:** If an attacker controls the relay, they can see decrypted traffic (self-hosted mitigates this)
- **Timing correlation:** A sufficiently powerful adversary monitoring both the user's ISP and the relay's ISP could theoretically correlate traffic timing (mitigated by noise layers and timing jitter)
- **Volume analysis at scale:** Sustained high-throughput database connections may draw attention if the destination IPs are known to host relays (mitigated by relay rotation and community pool)

### 21.3 Self-Hosted Advantage

The relay is your own server. No third-party platforms, no TOS violations, no rate limits, no platform inspection, no logging you don't control. You bring your own domain, your own server, your own TLS certificate. The single Go binary runs anywhere Linux runs.

---

## 22. ISP Deep Packet Inspection Analysis

### 22.1 Protocol-Level Analysis

```
DPI device classification:
  Port 5432 → "PostgreSQL" ✓
  TLS 1.3 → encrypted ✓
  Wire protocol → valid PostgreSQL v3 messages ✓
  Server banner → "PostgreSQL 16.2" ✓
  Auth flow → SCRAM-SHA-256 (standard) ✓
  Result: "Normal database connection"
```

### 22.2 Statistical / ML Analysis

```
ML model analyzing traffic patterns:
  - Mix of SELECT (variable-size results) and INSERT (fixed pattern): normal ✓
  - Burst pattern during "page loads" (4-8 queries in 50-200ms): normal ✓
  - Idle periods during "reading" (2-30s): normal ✓
  - Periodic analytics writes (5-30s intervals): normal ✓
  - BYTEA payloads in analytics events (15% of queries): normal ✓
  - 85% of queries have no BYTEA: normal ✓
  - Query text diversity (20+ distinct query patterns): normal ✓
  - Session duration (5-45 minutes): normal ✓
  Result: "Normal application database usage"
```

### 22.3 Targeted Investigation

```
Investigator connects to relay with psql:
  - Sees tables, data, schema: "Real database" ✓
  - Sees analytics_events with BYTEA: "Analytics system" ✓
  - Cannot decrypt BYTEA without shared secret
  - Cannot prove BYTEA is tunnel vs real analytics

Investigator monitors traffic volume:
  - 50 Mbps to a PostgreSQL server: unusual for small database
  - BUT: session replays (FullStory, Hotjar) transfer GB of data daily
  - AND: IoT metrics from 1000+ devices = massive INSERT volume
  - AND: real-time analytics pipelines are inherently high-throughput
  - Plausible explanation: "Heavy analytics/IoT workload"
  
Investigator checks server hosting:
  - Cloud VM (Hetzner, DigitalOcean, AWS): normal for databases ✓
  - Valid domain with DNS history: normal ✓
  - Let's Encrypt TLS: normal ✓
  - Single service on the VM: normal for database servers ✓
```

---

## 23. Application Scenarios

### 23.1 Scenario Table

| Scenario | Tables | Cover Queries | Tunnel Table | Best For |
|---|---|---|---|---|
| **E-commerce** | products, users, orders, cart_items, reviews, categories, product_images, addresses | Product browse, cart ops, search, checkout | analytics_events | General purpose, natural BYTEA volume |
| **IoT Dashboard** | devices, metrics, alerts, config, firmware | Device list, metrics read/write, alerts, firmware check | analytics_events | High INSERT volume, justifies heavy write traffic |
| **SaaS Dashboard** | users, orgs, subscriptions, invoices, usage_events, features | User/org mgmt, billing, usage stats, MRR | analytics_events | Business application, natural analytics |
| **Blog/CMS** | posts, comments, tags, media, authors, post_tags | Post list, comment read/write, search, tag cloud | analytics_events | Content site, natural page view analytics |
| **Project Management** | projects, tasks, sprints, task_comments, attachments, users, task_history | Task CRUD, sprint planning, board view, search | analytics_events | Collaboration tool, natural event tracking |

**All scenarios use the same tunnel table:** `analytics_events`. This table exists in every modern application. The scenario determines what OTHER tables exist and what cover queries look like.

### 23.2 Scenario Selection Guide

- **E-commerce:** Default choice. Most believable for any audience. High query variety.
- **IoT Dashboard:** Best when high INSERT volume is needed. IoT devices writing metrics every second is a perfectly normal pattern that justifies heavy database writes.
- **SaaS Dashboard:** Good for business-oriented relay domains. Natural analytics and usage tracking.
- **Blog/CMS:** Lowest resource usage. Good for smaller relays.
- **Project Management:** Good variety of query types. Natural event log pattern.

---

## 24. Configuration Reference

### 24.1 Client Configuration

```yaml
# duman-client.yaml

# ─── Proxy & Routing ───────────────────────────────────────────────────

proxy:
  listen: "127.0.0.1:1080"       # SOCKS5 proxy address
  auth:                            # optional SOCKS5 auth
    username: ""
    password: ""

routing:
  mode: socks5                     # socks5 | tun | process
  
  # TUN mode settings (requires elevated privileges)
  tun:
    name: "duman0"
    subnet: "10.10.0.0/16"
    mtu: 1400
    dns_intercept: true
  
  # Rule-based routing (for tun and process modes)
  rules:
    - match: "*.google.com"
      action: tunnel
    - match: "10.0.0.0/8"
      action: direct
    - match: "port:22"
      action: tunnel
    - match: "*"
      action: direct

# ─── Scenario ──────────────────────────────────────────────────────────

scenario: ecommerce                # ecommerce | iot | saas | blog | project

# ─── Relays ────────────────────────────────────────────────────────────

relays:
  - type: postgresql
    host: "db.iot-analytics.io"
    port: 5432
    database: "telemetry"
    user: "sensor_writer"
    password: "strong-password-here"
    weight: 0.45
    
  - type: mysql
    host: "db.shop-metrics.io"
    port: 3306
    database: "analytics"
    user: "writer"
    password: "strong-password-here"
    weight: 0.30
    
  - type: rest
    url: "https://api.weather-data.io"
    api_key: "your-api-key"
    weight: 0.25

# ─── Auth & Crypto ─────────────────────────────────────────────────────

auth:
  shared_secret: "base64-encoded-32-byte-secret"

crypto:
  algorithm: auto                  # auto | chacha20 | aes256gcm
  pfs: false                       # enable ephemeral key exchange

# ─── Interleaving ──────────────────────────────────────────────────────

interleaving:
  cover_per_tunnel: 3              # cover queries per tunnel query (1-8)
  adaptive: true                   # auto-adjust ratio based on tunnel demand
  timing_profile: casual_browser   # casual_browser | power_user | api_worker | dashboard_monitor

# ─── Tunnel ────────────────────────────────────────────────────────────

tunnel:
  chunk_size: 16384                # bytes per chunk (default 16KB)
  max_streams: 256                 # concurrent tunnel streams
  window_size: 32                  # chunks in flight before backpressure
  response_mode: push              # push (LISTEN/NOTIFY) | poll (SELECT)

# ─── Noise Layers ──────────────────────────────────────────────────────

noise:
  phantom_browser:
    enabled: true
    region: turkey                 # turkey | europe | global
    bandwidth_pct: 15              # % of total bandwidth

  smoke_screen:
    enabled: true
    peer_count: 4
    profiles: [video_call, messaging]
    bandwidth_pct: 5

  decoy:
    enabled: true
    targets: [github.com, stackoverflow.com, pkg.go.dev]
    count: 3

# ─── Bandwidth ─────────────────────────────────────────────────────────

governor:
  auto_detect: true                # auto-detect line speed
  max_bandwidth: 0                 # 0 = unlimited (use auto_detect)
  tunnel_budget: 0.50              # 50% for tunnel
  cover_budget: 0.15               # 15% for cover queries
  phantom_budget: 0.15             # 15% for phantom browsing
  p2p_budget: 0.05                 # 5% for P2P noise
  overhead_budget: 0.15            # 15% for protocol overhead

# ─── Relay Pool ────────────────────────────────────────────────────────

pool:
  enabled: false                   # enable multi-relay pool rotation
  rotation_seed: "seed-string"
  max_active: 3
  health_check_interval: 30s
  fast_rotation: 2m               # relay slot rotation
  slow_rotation: 20m              # exit slot rotation

# ─── Logging ───────────────────────────────────────────────────────────

log:
  level: info                      # debug | info | warn | error
  format: json                     # json | text
  output: stderr                   # stderr | stdout | file path
```

### 24.2 Relay Configuration

```yaml
# duman-relay.yaml

# ─── Server ────────────────────────────────────────────────────────────

server:
  protocol: postgresql             # postgresql | mysql | rest | all
  listen: ":5432"                  # listen address
  domain: "db.iot-analytics.io"    # server domain (for TLS + server banner)
  
  tls:
    mode: acme                     # acme | manual | self_signed
    acme_email: "admin@example.com"
    cert_file: ""                  # for manual mode
    key_file: ""                   # for manual mode

# ─── Scenario ──────────────────────────────────────────────────────────

scenario: iot                      # must match client scenario for consistent data

# ─── Fake Data ─────────────────────────────────────────────────────────

fake_data:
  seed: "deterministic-seed"       # same seed = same fake data always
  product_count: 200
  user_count: 100
  device_count: 30
  post_count: 50
  task_count: 100

# ─── Database Identity ────────────────────────────────────────────────

identity:
  postgresql:
    version: "16.2"
    system_id: "7354920841537843201"
  mysql:
    version: "8.0.36"
    server_id: 1

# ─── Auth ──────────────────────────────────────────────────────────────

auth:
  # Database auth (for incoming connections)
  users:
    - username: "sensor_writer"
      password: "strong-password-here"
      database: "telemetry"
    - username: "readonly"
      password: "another-password"
      database: "telemetry"
  
  # API auth (for REST facade)
  api_keys:
    - key: "your-api-key"
      name: "primary"
  
  # Tunnel auth
  tunnel_secret: "base64-encoded-32-byte-secret"

# ─── Tunnel Exit ───────────────────────────────────────────────────────

tunnel:
  role: exit                       # exit | relay | both
  max_connections: 1000            # outbound connection pool size
  idle_timeout: 5m                 # close idle exit connections after
  
  # Forward mode (when role = relay, forward to exit relay)
  forward_to: ""                   # exit relay address (if role = relay)

# ─── Limits ────────────────────────────────────────────────────────────

limits:
  max_clients: 100                 # simultaneous client connections
  max_streams_per_client: 256
  response_buffer_size: 1000       # max response chunks in memory per session
  response_ttl: 5m                 # expire uncollected response chunks

# ─── Logging ───────────────────────────────────────────────────────────

log:
  level: info
  format: json
  output: stderr
  # WARNING: debug level logs query content — never use in production
```

---

## 25. Performance Targets

### 25.1 Profile Comparison

| Profile | SQL Relays | API Relays | Tunnel Throughput | Added Latency | Cover % | Use Case |
|---|---|---|---|---|---|---|
| **Speed** | 2 PgSQL | 0 | 70-80 Mbps | +15ms | 70% | Maximum throughput |
| **Balanced** | 2 PgSQL | 1 REST | 50-60 Mbps | +25ms | 80% | Daily use |
| **Stealth** | 2 PgSQL + 1 MySQL | 1 REST | 40-50 Mbps | +35ms | 85% | High surveillance |
| **Paranoid** | 2 PgSQL + 1 MySQL | 1 REST + all noise | 30-40 Mbps | +50ms | 90% | Maximum stealth |

### 25.2 Per-Component Overhead

| Component | Overhead | Notes |
|---|---|---|
| Chunk encryption | <1ms per 16KB | AES-NI or ChaCha20 |
| SQL embedding (prepared stmt) | 1.1% bandwidth | 181 bytes per 16KB |
| SQL embedding (simple query) | 3-5% bandwidth | Full SQL text |
| REST embedding | 33% bandwidth | Base64 encoding |
| Interleaving timing | 15-50ms added latency | Natural timing gaps |
| TLS | ~2% bandwidth | Standard TLS overhead |
| Cover queries | 15-30% bandwidth | Configurable ratio |

### 25.3 Resource Usage

**Client:**
- CPU: <5% on modern hardware (crypto + interleaving)
- Memory: ~50 MB base + ~1 MB per active stream
- Disk: None (no persistent state)

**Relay:**
- CPU: <10% on modern hardware (crypto + fake data + exit)
- Memory: ~100 MB base + ~2 MB per connected client
- Disk: None (all in-memory)
- Network: bandwidth is the primary resource

---

## 26. Project Structure

```
duman/
├── cmd/
│   ├── duman-client/
│   │   └── main.go                    # Client entry point + CLI
│   └── duman-relay/
│       └── main.go                    # Relay entry point + CLI
│
├── internal/
│   ├── crypto/                        # Encryption, HMAC, key derivation
│   │   ├── cipher.go                  # ChaCha20-Poly1305 / AES-256-GCM
│   │   ├── keys.go                    # HKDF key derivation
│   │   ├── chunk.go                   # Chunk encrypt/decrypt
│   │   └── auth.go                    # HMAC tunnel auth ("px_" tokens)
│   │
│   ├── pgwire/                        # PostgreSQL wire protocol (from scratch)
│   │   ├── client.go                  # Client-side (duman-client sends queries)
│   │   ├── server.go                  # Server-side (duman-relay accepts connections)
│   │   ├── messages.go                # All message type serialization/deserialization
│   │   ├── auth.go                    # MD5 / SCRAM-SHA-256 authentication
│   │   └── types.go                   # OID type mapping, data encoding
│   │
│   ├── mysqlwire/                     # MySQL wire protocol (from scratch)
│   │   ├── client.go                  # Client-side
│   │   ├── server.go                  # Server-side
│   │   ├── messages.go                # Packet serialization
│   │   └── auth.go                    # mysql_native_password / caching_sha2_password
│   │
│   ├── restapi/                       # REST API facade
│   │   ├── client.go                  # REST client (sends POST analytics)
│   │   ├── server.go                  # REST server (serves fake API + extracts tunnel)
│   │   ├── routes.go                  # Endpoint definitions
│   │   └── swagger.go                 # OpenAPI spec generation
│   │
│   ├── tunnel/                        # Tunnel stream management
│   │   ├── stream.go                  # Stream lifecycle (connect, data, fin)
│   │   ├── splitter.go                # TCP stream → fixed-size chunks
│   │   ├── assembler.go               # Chunk reassembly + reordering
│   │   ├── exit.go                    # Exit node (relay → internet)
│   │   └── dns.go                     # Remote DNS resolution
│   │
│   ├── interleave/                    # Interleaving engine
│   │   ├── engine.go                  # Core mix loop
│   │   ├── timing.go                  # Natural timing profiles
│   │   └── ratio.go                   # Adaptive cover-to-tunnel ratio
│   │
│   ├── realquery/                     # Real Query Engine
│   │   ├── engine.go                  # Query generation orchestrator
│   │   ├── state.go                   # Application state machine
│   │   ├── ecommerce.go              # E-commerce scenario queries
│   │   ├── iot.go                     # IoT dashboard queries
│   │   ├── saas.go                    # SaaS dashboard queries
│   │   ├── blog.go                    # Blog/CMS queries
│   │   └── project.go                # Project management queries
│   │
│   ├── fakedata/                      # Fake Data Engine (relay-side)
│   │   ├── engine.go                  # Query execution + response generation
│   │   ├── generator.go              # Realistic data generation (names, prices, etc.)
│   │   ├── parser.go                  # Simple SQL pattern matcher (~30 patterns)
│   │   ├── schema.go                  # Fake table schemas (\dt, \d, SHOW)
│   │   └── seed.go                    # Deterministic data seeding from config seed
│   │
│   ├── provider/                      # Provider abstraction (client-side relay connections)
│   │   ├── provider.go               # Provider interface
│   │   ├── pg_provider.go            # PostgreSQL relay provider
│   │   ├── mysql_provider.go         # MySQL relay provider
│   │   ├── rest_provider.go          # REST API relay provider
│   │   └── manager.go                # Multi-provider orchestration + weighted distribution
│   │
│   ├── proxy/                         # Client proxy layer
│   │   ├── socks5.go                  # SOCKS5 proxy implementation
│   │   ├── tun.go                     # TUN device (Linux/macOS/Windows)
│   │   └── routing.go                # Rule-based traffic routing
│   │
│   ├── phantom/                       # Phantom Browser noise layer
│   │   ├── browser.go                 # HTTP client with realistic TLS fingerprint
│   │   ├── session.go                 # Browsing session simulation
│   │   └── profiles.go               # Regional browsing profiles
│   │
│   ├── smokescreen/                   # P2P Smoke Screen noise layer
│   │   ├── peer.go                    # Peer connection management
│   │   ├── cover.go                   # Cover traffic profiles
│   │   └── discovery.go              # Peer discovery (if pool-based)
│   │
│   ├── governor/                      # Bandwidth Governor
│   │   ├── governor.go               # Adaptive bandwidth allocation
│   │   └── detect.go                 # Line speed auto-detection
│   │
│   ├── pool/                          # Relay Pool & Rotation
│   │   ├── pool.go                    # Pool management
│   │   ├── schedule.go               # Seed-based rotation schedule
│   │   ├── health.go                  # Health checking
│   │   └── tiers.go                   # Tier classification
│   │
│   ├── config/
│   │   ├── client.go                  # Client config parsing + validation
│   │   └── relay.go                   # Relay config parsing + validation
│   │
│   └── log/
│       └── log.go                     # Structured logging (slog-based)
│
├── configs/
│   ├── duman-client.example.yaml
│   └── duman-relay.example.yaml
│
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 27. Milestones

### MVP (v0.1.0) — Single PgSQL Relay + Basic Tunnel

**Goal:** End-to-end tunnel working through a single PostgreSQL relay.

- PostgreSQL wire protocol (client + server, ~1500 LOC)
- Chunk encryption (ChaCha20-Poly1305 / AES-256-GCM auto-detect)
- HMAC tunnel authentication ("px_" tokens in metadata)
- Basic Fake Data Engine (1 scenario: e-commerce, ~10 query patterns)
- Basic interleaving (fixed 3:1 ratio, simple timing)
- SOCKS5 proxy (CONNECT only)
- Stream management (splitter, assembler, reorder buffer)
- Exit engine (relay → internet forwarding)
- Response channel (SELECT polling mode)
- Remote DNS resolution through relay
- Config parsing + CLI (start, stop, status, keygen)
- Basic logging

### v0.2.0 — Full Real Query Engine + Stealth

**Goal:** Undetectable traffic patterns.

- All 5 scenarios (e-commerce, IoT, SaaS, blog, project)
- Application state machine (consistent query sequences with navigation)
- Natural timing profiles (4 profiles: casual, power, api, dashboard)
- Fake data seeder (200 products, 100 users, 30 devices, 50 posts, 100 tasks)
- psql/DBeaver full compatibility (\dt, \d, SELECT, metadata queries, error messages)
- LISTEN/NOTIFY push mode for responses (1-5ms latency)
- Prepared statement binary mode (zero SQL text overhead)
- Adaptive cover-to-tunnel ratio

### v0.3.0 — Multi-Protocol

**Goal:** Support MySQL and REST alongside PostgreSQL.

- MySQL wire protocol (client + server, ~1200 LOC)
- REST API facade layer (server + client)
- Multi-relay distribution (PgSQL + MySQL + REST weighted)
- Provider manager with health checking
- Exit modes: independent, single exit, chain
- OpenAPI/Swagger documentation page on relay

### v0.4.0 — Split Tunnel & Advanced Routing

**Goal:** Flexible traffic routing beyond SOCKS5.

- TUN device (Linux, macOS, Windows)
- Rule-based routing (domain, IP range, port, process)
- Per-process routing (cgroup on Linux, NetworkExtension on macOS, WFP on Windows)
- DNS leak prevention (full DNS interception for tunneled traffic)
- UDP support via SOCKS5 UDP ASSOCIATE
- Transparent proxy mode

### v0.5.0 — Noise Layers

**Goal:** Full traffic camouflage.

- Phantom Browser (regional profiles, realistic TLS fingerprint)
- P2P Smoke Screen (4 cover profiles, residential peer connections)
- Decoy connections (popular developer sites)
- Bandwidth Governor (adaptive allocation)
- Line speed auto-detection

### v0.6.0 — Relay Pool & Rotation

**Goal:** Large-scale deployment with 100+ relays.

- Relay pool support with tier classification
- Seed-based stochastic rotation schedule
- Pre-warm overlap (zero-interruption rotation)
- Health checking and auto-failover
- Anti-censorship (block detection + auto-reroute)
- Community relay onboarding tools

### v1.0.0 — Production

**Goal:** Production-ready release.

- Embedded dashboard (client + relay status, metrics, connection info)
- Perfect forward secrecy (ephemeral X25519 key exchange)
- Comprehensive test suite (unit, integration, protocol conformance)
- Performance optimization and benchmarking
- Security audit
- Documentation (user guide, deployment guide, relay operator guide)
- Community relay network launch
