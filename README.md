# Duman

**Steganographic SQL/API tunnel that hides encrypted traffic inside legitimate database queries.**

Duman disguises tunneled internet traffic as normal PostgreSQL, MySQL, REST API, and WebSocket communications. An ISP performing deep packet inspection sees a developer connected to cloud databases — a completely normal traffic pattern.

## How It Works

```
[Browser] → [SOCKS5 Proxy] → [Interleaving Engine] → [PostgreSQL Wire Protocol] → [Relay Server] → [Internet]
                                     ↑                         ↑
                              Cover queries              Tunnel chunks hidden
                              (SELECT, JOIN)             in INSERT payloads
```

1. **duman-client** runs locally, providing a SOCKS5 proxy (or TUN device)
2. Application traffic is encrypted, chunked, and embedded into analytics INSERT statements
3. Cover queries (SELECT, JOIN, COUNT) interleave with tunnel queries at natural timing
4. **duman-relay** speaks perfect wire protocol — connect with `psql` and get realistic data back
5. The relay extracts tunnel chunks, decrypts, and forwards traffic to the internet

Without the shared encryption key, distinguishing tunnel queries from cover queries is cryptographically impossible.

## Features

- **Multi-protocol tunneling** — PostgreSQL, MySQL, REST API, WebSocket transports
- **Dynamic schema system** — DDL parsing, random generation, per-session mutations
- **Statistical indistinguishability** — Kolmogorov-Smirnov tested traffic patterns
- **Split tunneling** — TUN device with domain/IP/port routing rules, DNS interception
- **Noise layers** — Phantom browser, P2P smokescreen, decoy HTTPS connections
- **Relay pool** — Health checking, seed-based rotation, circuit breaker, rate limiting
- **Perfect Forward Secrecy** — X25519 ephemeral key exchange
- **SCRAM-SHA-256** — RFC 5802 + RFC 7677 authentication
- **Cross-platform** — Linux, macOS, Windows (amd64, arm64)
- **Zero external runtime** — Single static binary, no dependencies

## Quick Start

### Generate shared secret

```bash
duman-client keygen
# → Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw=
```

### Configure relay

```yaml
# duman-relay.yaml
listen:
  postgresql: ":5432"
auth:
  users:
    sensor_writer: "your_password"
tunnel:
  shared_secret: "Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw="
fake_data:
  scenario: "ecommerce"
```

### Configure client

```yaml
# duman-client.yaml
proxy:
  listen: "127.0.0.1:1080"
tunnel:
  shared_secret: "Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw="
relays:
  - address: "db.example.com:5432"
    protocol: "postgresql"
    username: "sensor_writer"
    password: "your_password"
scenario: "ecommerce"
```

### Run

```bash
# Start relay on your server
duman-relay -c duman-relay.yaml

# Start client locally
duman-client -c duman-client.yaml

# Use it — all traffic through the SOCKS5 proxy is tunneled
curl --proxy socks5h://127.0.0.1:1080 https://example.com
```

### Verify the relay looks real

```bash
# Connect with psql — it responds like a real PostgreSQL database
psql -h db.example.com -U sensor_writer -d telemetry

telemetry=> SELECT * FROM products LIMIT 5;
 id |         name          |  price  | stock
----+-----------------------+---------+-------
  1 | Wireless Headphones   |   79.99 |   150
  2 | USB-C Hub             |   45.50 |   300
  3 | Mechanical Keyboard   |  129.00 |    75
  4 | Portable SSD 1TB      |   89.99 |   200
  5 | Webcam HD 1080p       |   59.95 |   125
(5 rows)

telemetry=> \dt
              List of relations
 Schema |       Name        | Type  |     Owner
--------+-------------------+-------+----------------
 public | products          | table | sensor_writer
 public | categories        | table | sensor_writer
 public | users             | table | sensor_writer
 public | orders            | table | sensor_writer
 public | order_items       | table | sensor_writer
 ...
```

## Build

```bash
# Build both binaries
make build

# Run tests (24 packages, 41K LOC)
make test

# Cross-compile all platforms
make cross
```

## Architecture

```
cmd/
  duman-client/          CLI entry point (start, keygen, status, version)
  duman-relay/           CLI entry point (start, keygen, status, version)

internal/
  config/                YAML config + hot-reload
  crypto/                ChaCha20/AES-256-GCM, HKDF, HMAC, X25519 PFS, sync.Pool
  pgwire/                PostgreSQL wire protocol (server, client, SCRAM-SHA-256)
  mysqlwire/             MySQL wire protocol (server, client, native + caching_sha2)
  restapi/               REST API facade (routes, swagger, tunnel via analytics POST)
  wstunnel/              WebSocket transport (RFC 6455, stdlib-only)
  tunnel/                Stream splitter, assembler, exit engine, migration
  fakedata/              Dynamic schema, DDL parser, generators, mutations
  realquery/             Cover query patterns (ecommerce, IoT, SaaS, blog, project)
  interleave/            Timing profiles, EWMA ratio, burst scheduling
  provider/              Multi-protocol providers, circuit breaker, manager
  proxy/                 SOCKS5, UDP ASSOCIATE, TUN device, routing, DNS, kill switch
  phantom/               Chrome-like HTTP client, regional browsing profiles
  smokescreen/           P2P cover traffic, decoy HTTPS connections
  governor/              Token bucket bandwidth allocation, auto-detection
  pool/                  Relay pool, health checking, rotation schedule, tiers
  relay/                 Relay orchestration, forwarder, ACME, rate limiting, health
  dashboard/             Real-time web UI (SSE), metrics, pprof
  recorder/              Traffic capture and replay for debugging
  log/                   Structured logging (slog)

test/
  integration/           End-to-end tunnel roundtrip
  security/              No leaked secrets, TLS min version, constant-time HMAC
  statistical/           KS test, chi-squared, traffic indistinguishability
```

## Protocols

| Protocol | Port | Cover Story | Detection Resistance |
|----------|------|-------------|---------------------|
| PostgreSQL | 5432 | Cloud database | Perfect wire protocol, psql/DBeaver compatible |
| MySQL | 3306 | Analytics DB | Full handshake, prepared statements |
| REST API | 443 | E-commerce API | Swagger docs, realistic endpoints |
| WebSocket | 443 | Real-time app | RFC 6455 compliant, ping/pong |

## Security

- **Encryption**: ChaCha20-Poly1305 or AES-256-GCM (hardware accelerated)
- **Key derivation**: HKDF-SHA256 with per-session keys
- **Authentication**: SCRAM-SHA-256 (PostgreSQL), caching_sha2_password (MySQL)
- **Forward secrecy**: Optional X25519 ephemeral key exchange
- **Probe resistance**: Relay responds as a real database to any unauthenticated query
- **No hardcoded secrets**: Enforced by automated security tests

## Configuration

Full configuration reference: [configs/duman-client.example.yaml](configs/duman-client.example.yaml) and [configs/duman-relay.example.yaml](configs/duman-relay.example.yaml)

### Scenarios

| Scenario | Tables | Cover Pattern |
|----------|--------|--------------|
| `ecommerce` | products, orders, users, categories | Shopping analytics |
| `iot` | sensors, readings, devices, alerts | IoT telemetry |
| `saas` | tenants, subscriptions, events, invoices | SaaS platform |
| `blog` | posts, comments, authors, tags | Content platform |
| `project` | tasks, sprints, members, timelog | Project management |

### Custom Schema

Inject your own DDL for a unique database fingerprint:

```yaml
schema:
  mode: "custom"
  custom_ddl: |
    CREATE TABLE patients (id SERIAL PRIMARY KEY, name VARCHAR(255), admitted_at TIMESTAMP);
    CREATE TABLE records (id SERIAL PRIMARY KEY, patient_id INT REFERENCES patients(id), diagnosis TEXT);
```

### Schema Mutations

Enable mutations for per-session uniqueness — table/column names are randomized deterministically:

```yaml
schema:
  mode: "template"
  mutate: true
  seed: 42
# products → items, users → customers, name → title, price → cost, etc.
```

## Dashboards

Both client and relay serve real-time web dashboards:

- **Client**: `http://127.0.0.1:9090` — relay status, throughput, noise layers, bandwidth allocation
- **Relay**: `http://127.0.0.1:9091` — client count, session durations, query rates, fake engine stats
- **pprof**: `http://127.0.0.1:909x/debug/pprof/` — CPU/memory profiling

## CI

GitHub Actions runs on every push:
- Build (Linux, macOS, Windows)
- Tests with race detector
- Benchmarks (crypto, chunk marshaling)
- `go vet`
- Cross-compiled release binaries on tags

## License

MIT
