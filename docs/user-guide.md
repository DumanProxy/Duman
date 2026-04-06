# Duman User Guide

## Installation

### From Binary

Download the latest release for your platform from [GitHub Releases](https://github.com/dumanproxy/duman/releases).

```bash
# Linux / macOS
chmod +x duman-client
sudo mv duman-client /usr/local/bin/

# Windows — add to PATH or place in a directory on PATH
```

### From Source

```bash
git clone https://github.com/dumanproxy/duman.git
cd duman
make build
# Binaries: bin/duman-client, bin/duman-relay
```

### Docker

```bash
docker pull dumanproxy/duman-client
docker pull dumanproxy/duman-relay
```

## Quick Start

### 1. Generate a shared secret

```bash
duman-client keygen
# Output: Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw=
```

Share this key securely between client and relay. Anyone with this key can use the tunnel.

### 2. Set up the relay

On your server, create `/etc/duman/duman-relay.yaml`:

```yaml
listen:
  postgresql: ":5432"
auth:
  users:
    sensor_writer: "strong_password_here"
tunnel:
  shared_secret: "Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw="
fake_data:
  scenario: "ecommerce"
```

Start the relay:

```bash
duman-relay -c /etc/duman/duman-relay.yaml
```

### 3. Configure the client

Create `duman-client.yaml`:

```yaml
proxy:
  listen: "127.0.0.1:1080"
tunnel:
  shared_secret: "Kx7mP2q9R1wZ4vN8bT6yA3cF5hJ0dL+sE9gU2iO7pXw="
relays:
  - address: "your-server.com:5432"
    protocol: "postgresql"
    username: "sensor_writer"
    password: "strong_password_here"
scenario: "ecommerce"
```

Start the client:

```bash
duman-client -c duman-client.yaml
```

### 4. Use it

Configure your applications to use the SOCKS5 proxy at `127.0.0.1:1080`:

```bash
# curl
curl --proxy socks5h://127.0.0.1:1080 https://example.com

# Firefox — Settings > Network > Manual proxy > SOCKS Host: 127.0.0.1, Port: 1080

# System-wide (Linux)
export ALL_PROXY=socks5h://127.0.0.1:1080
```

### 5. Verify the relay looks real

```bash
psql -h your-server.com -U sensor_writer -d telemetry
# You'll see a real-looking PostgreSQL database with tables, data, and working queries
```

## Configuration Reference

### Client Configuration

```yaml
# SOCKS5 proxy settings
proxy:
  listen: "127.0.0.1:1080"      # Bind address for SOCKS5 proxy
  kill_switch: false              # Block traffic if all relays down

# Tunnel encryption
tunnel:
  shared_secret: "base64key..."   # 32-byte key (from duman-client keygen)
  padding: false                  # Pad all chunks to fixed size
  jitter_ms: 0                    # Random delay per chunk (0-50ms)

# Crypto settings
crypto:
  cipher: "auto"                  # auto, chacha20, aes256gcm
  pfs: false                      # Enable Perfect Forward Secrecy (X25519)

# Relay connections
relays:
  - address: "db.example.com:5432"
    protocol: "postgresql"         # postgresql, mysql, rest, websocket
    username: "sensor_writer"
    password: "password"
    tls_pin: ""                    # sha256/... certificate pin (optional)
    weight: 10                     # Load balancing weight

# Cover traffic scenario
scenario: "ecommerce"              # ecommerce, iot, saas, blog, project

# Schema configuration
schema:
  mode: "template"                 # template, custom, random
  mutate: false                    # Randomize table/column names per session
  seed: 0                          # Deterministic seed (0 = random)
  custom_ddl: ""                   # SQL DDL for custom mode

# Interleaving
interleave:
  profile: "casual_browser"        # casual_browser, power_user, api_worker, dashboard_monitor
  base_ratio: 3                    # Cover:tunnel ratio (3 = 3:1)

# Noise layers
noise:
  phantom_browser:
    enabled: false
    region: "global"               # turkey, europe, global
  smoke_screen:
    enabled: false
    peer_count: 3
  decoy:
    enabled: false
    targets: []                    # Custom decoy URLs

# Bandwidth governor
governor:
  enabled: false
  total_mbps: 0                    # 0 = auto-detect

# Relay pool
pool:
  max_active: 3                    # Simultaneous relay connections
  rotation_interval: "5m"          # How often to rotate active relay
  health_check_interval: "30s"

# Dashboard
dashboard:
  listen: "127.0.0.1:9090"
  enabled: true

# Logging
log:
  level: "info"                    # debug, info, warn, error
  format: "text"                   # text, json
```

### Relay Configuration

```yaml
# Protocol listeners
listen:
  postgresql: ":5432"
  mysql: ":3306"                   # Optional
  rest: ":443"                     # Optional
  websocket: ":8443"               # Optional

# Authentication
auth:
  users:
    sensor_writer: "password"
    analytics_bot: "another_password"

# Tunnel
tunnel:
  shared_secret: "base64key..."
  forward_to: ""                   # Chain mode: next relay address

# Fake data engine
fake_data:
  scenario: "ecommerce"
  mutate: false
  seed: 0

# TLS
tls:
  mode: "self_signed"              # self_signed, acme, manual
  cert_file: ""                    # For manual mode
  key_file: ""
  acme_domain: ""                  # For ACME mode

# Rate limiting
rate_limit:
  requests_per_second: 100
  burst: 200

# Health endpoint
health:
  listen: ":9091"

# Dashboard
dashboard:
  listen: "127.0.0.1:9091"
  enabled: true

# Logging
log:
  level: "info"
  format: "text"
```

## Routing Modes

### SOCKS5 Proxy (Default)

Configure applications individually to use the proxy. Best for selective tunneling.

```yaml
proxy:
  listen: "127.0.0.1:1080"
```

### TUN Device (System-wide)

Routes all matching traffic through the tunnel at the network level. Requires root/admin.

```yaml
proxy:
  mode: "tun"
  tun:
    name: "duman0"
    mtu: 1500
routing:
  rules:
    - match: "*.google.com"
      action: "tunnel"
    - match: "10.0.0.0/8"
      action: "direct"
    - match: "*"
      action: "tunnel"            # Default: tunnel everything
```

### Split Tunneling

Route specific traffic through the tunnel, everything else goes direct:

```yaml
routing:
  rules:
    - match: "*.blocked-site.com"
      action: "tunnel"
    - match: "*.internal.corp"
      action: "direct"
    - match: "*"
      action: "direct"            # Default: direct connection
```

## Scenarios

Each scenario provides a realistic database cover story:

| Scenario | Tables | Use Case |
|----------|--------|----------|
| `ecommerce` | products, orders, users, categories, order_items, reviews, inventory, shipping, payments, coupons | Online store analytics |
| `iot` | sensors, readings, devices, alerts, firmware, locations, thresholds, maintenance | IoT telemetry platform |
| `saas` | tenants, subscriptions, events, invoices, features, usage, tickets, audit_log | SaaS platform metrics |
| `blog` | posts, comments, authors, tags, categories, media, pages, settings | Content management |
| `project` | tasks, sprints, members, timelog, milestones, labels, attachments, comments | Project management |

### Custom Schema

Provide your own database DDL for a unique fingerprint:

```yaml
schema:
  mode: "custom"
  custom_ddl: |
    CREATE TABLE patients (id SERIAL PRIMARY KEY, name VARCHAR(255), admitted_at TIMESTAMP);
    CREATE TABLE records (id SERIAL PRIMARY KEY, patient_id INT REFERENCES patients(id), diagnosis TEXT);
    CREATE TABLE medications (id SERIAL PRIMARY KEY, name VARCHAR(100), dosage VARCHAR(50));
```

### Schema Mutations

Enable mutations so each session has a unique database schema:

```yaml
schema:
  mode: "template"
  mutate: true
  seed: 42
# products → items, users → customers, name → title, price → cost
```

## Noise Layer Tuning

### Phantom Browser

Generates realistic HTTPS browsing traffic to popular websites:

```yaml
noise:
  phantom_browser:
    enabled: true
    region: "turkey"         # Sites: google.com.tr, youtube.com, trendyol.com, etc.
    # region: "europe"       # Sites: google.com, bbc.com, amazon.de, etc.
    # region: "global"       # Sites: google.com, youtube.com, github.com, etc.
```

### P2P Smokescreen

Generates encrypted P2P-like traffic to random IPs:

```yaml
noise:
  smoke_screen:
    enabled: true
    peer_count: 5
    profiles:
      - "video_call"         # Symmetric 1-5 Mbps
      - "messaging"          # Bursty 10-50 Kbps
```

### Decoy HTTPS

Connects to legitimate developer sites:

```yaml
noise:
  decoy:
    enabled: true
    targets:
      - "https://github.com"
      - "https://stackoverflow.com"
      - "https://pkg.go.dev"
```

## Security Profiles

### Speed (Minimum Protection)

```yaml
tunnel: { padding: false, jitter_ms: 0 }
crypto: { pfs: false }
noise: { phantom_browser: { enabled: false }, smoke_screen: { enabled: false } }
```

### Balanced (Recommended)

```yaml
tunnel: { padding: false, jitter_ms: 10 }
crypto: { pfs: true }
noise: { phantom_browser: { enabled: true, region: "global" } }
```

### Stealth (High Security)

```yaml
tunnel: { padding: true, jitter_ms: 25 }
crypto: { pfs: true, cipher: "chacha20" }
noise:
  phantom_browser: { enabled: true }
  smoke_screen: { enabled: true, peer_count: 5 }
  decoy: { enabled: true }
interleave: { profile: "casual_browser", base_ratio: 5 }
```

### Paranoid (Maximum Security)

```yaml
tunnel: { padding: true, jitter_ms: 50 }
crypto: { pfs: true, cipher: "chacha20" }
noise:
  phantom_browser: { enabled: true }
  smoke_screen: { enabled: true, peer_count: 10 }
  decoy: { enabled: true }
interleave: { profile: "casual_browser", base_ratio: 8 }
pool: { max_active: 5, rotation_interval: "30s" }
```

## Troubleshooting

### Connection refused to relay

- Verify the relay is running and listening on the correct port
- Check firewall rules allow incoming connections
- Verify the domain/IP resolves correctly

### Authentication failed

- Verify username and password match between client and relay configs
- Check that the shared_secret is identical on both sides (case-sensitive, base64)

### Slow throughput

- Check relay latency: `duman-client status`
- Reduce noise layers if bandwidth is limited
- Increase `interleave.base_ratio` to send more tunnel vs cover queries
- Disable padding to reduce bandwidth overhead
- Switch profile to `api_worker` for maximum throughput

### psql shows "protocol error"

- Ensure client and relay use the same scenario
- If using schema mutations, ensure both use the same seed
- Check that no other PostgreSQL server is running on the same port

### Dashboard not loading

- Check dashboard is enabled in config
- Verify the dashboard port isn't blocked by firewall
- Access via `http://127.0.0.1:9090` (client) or `:9091` (relay)

## CLI Commands

### duman-client

```
duman-client start -c config.yaml     Start the client
duman-client keygen                    Generate a new shared secret
duman-client status                    Show connection status (from dashboard API)
duman-client version                   Show version information
```

### duman-relay

```
duman-relay start -c config.yaml       Start the relay
duman-relay keygen                     Generate a new shared secret
duman-relay status                     Show server status (from dashboard API)
duman-relay version                    Show version information
```
