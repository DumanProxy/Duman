# Duman Relay Deployment Guide

## Requirements

- Linux server (Ubuntu 22.04+ recommended)
- Public IP address
- Domain name (recommended for TLS)
- 512 MB RAM minimum, 1 GB recommended
- Port 5432 (PostgreSQL) and/or 3306 (MySQL) and/or 443 (REST/WebSocket) open

## Quick Deploy

### One-Line Install (Ubuntu/Debian)

```bash
curl -sL https://raw.githubusercontent.com/dumanproxy/duman/main/deploy/digitalocean.sh | \
  bash -s -- --domain relay.example.com --secret "$(duman-client keygen)"
```

### Docker

```bash
curl -sL https://raw.githubusercontent.com/dumanproxy/duman/main/deploy/docker-deploy.sh | \
  bash -s -- --domain relay.example.com --secret "your_shared_secret"
```

## Manual Setup

### 1. Build the relay binary

```bash
git clone https://github.com/dumanproxy/duman.git
cd duman
CGO_ENABLED=0 go build -o /usr/local/bin/duman-relay ./cmd/duman-relay
```

### 2. Create a system user

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin duman
```

### 3. Create configuration

```bash
mkdir -p /etc/duman
cat > /etc/duman/duman-relay.yaml <<EOF
listen:
  postgresql: ":5432"

auth:
  users:
    sensor_writer: "$(openssl rand -base64 24)"

tunnel:
  shared_secret: "YOUR_SHARED_SECRET_HERE"

fake_data:
  scenario: "ecommerce"

tls:
  mode: "self_signed"

rate_limit:
  requests_per_second: 100
  burst: 200

dashboard:
  listen: "127.0.0.1:9091"
  enabled: true

log:
  level: "info"
  format: "json"
EOF

chmod 600 /etc/duman/duman-relay.yaml
chown duman:duman /etc/duman/duman-relay.yaml
```

### 4. Create systemd service

```bash
cp deploy/duman-relay.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now duman-relay
```

### 5. Configure firewall

```bash
ufw allow 22/tcp     # SSH
ufw allow 5432/tcp   # PostgreSQL
ufw --force enable
```

### 6. Verify

```bash
# Check service status
systemctl status duman-relay

# Check it responds as a real PostgreSQL server
psql -h localhost -U sensor_writer -d telemetry -c "SELECT * FROM products LIMIT 3;"

# Check dashboard
curl http://127.0.0.1:9091/api/stats
```

## Domain Setup

### Choosing a domain

Pick a domain that looks like a legitimate database service:

- `db.yourcompany.com`
- `analytics-pg.example.com`
- `telemetry.yourservice.io`
- `metrics-db.example.com`

Avoid suspicious names like `tunnel`, `proxy`, `vpn`, etc.

### DNS Configuration

Create an A record pointing to your server's IP:

```
db.example.com  A  203.0.113.42
```

### TLS with Let's Encrypt

```yaml
tls:
  mode: "acme"
  acme_domain: "db.example.com"
```

The relay will automatically obtain and renew TLS certificates via Let's Encrypt.

For ACME to work, port 80 must be temporarily accessible for the HTTP-01 challenge. You can use a reverse proxy or DNS-01 challenge as alternatives.

## Cloud Provider Guides

### DigitalOcean

1. Create a Droplet: Ubuntu 24.04, 1 GB RAM, Regular CPU
2. SSH into the droplet
3. Run the one-line installer:

```bash
curl -sL https://raw.githubusercontent.com/dumanproxy/duman/main/deploy/digitalocean.sh | \
  bash -s -- --domain db.example.com --secret "your_key" --scenario ecommerce
```

### Hetzner Cloud

1. Create a CX22 server (2 vCPU, 4 GB RAM) with Ubuntu 24.04
2. SSH in and follow the same steps as DigitalOcean

### AWS EC2

1. Launch a `t3.micro` instance with Ubuntu 24.04 AMI
2. Security Group: allow TCP 5432 from 0.0.0.0/0, TCP 22 from your IP
3. SSH in and follow the manual setup

### Docker Compose

```bash
# On your server
git clone https://github.com/dumanproxy/duman.git
cd duman

# Edit docker-compose.yml with your settings
# Then:
docker compose up -d
```

## Multi-Protocol Setup

Run PostgreSQL, MySQL, and REST simultaneously:

```yaml
listen:
  postgresql: ":5432"
  mysql: ":3306"
  rest: ":443"

auth:
  users:
    pg_user: "pg_password"
    mysql_user: "mysql_password"
    api_user: "api_key_here"
```

Open all relevant ports in the firewall:

```bash
ufw allow 5432/tcp  # PostgreSQL
ufw allow 3306/tcp  # MySQL
ufw allow 443/tcp   # REST/HTTPS
```

## Monitoring

### Dashboard

Access the relay dashboard at `http://127.0.0.1:9091` (localhost only by default):

- Connected clients
- Tunnel throughput
- Cover query rate
- Fake data engine stats

### Prometheus

Metrics are exported at `/metrics` on the dashboard port:

```bash
curl http://127.0.0.1:9091/metrics
```

Key metrics:
- `duman_bytes_in_total` / `duman_bytes_out_total` — throughput
- `duman_active_connections` — connected clients
- `duman_queries_total` — query count
- `duman_tunnel_chunks_total` — tunnel chunks processed
- `duman_errors_total` — error count

### Health Check

```bash
curl http://127.0.0.1:9091/health
# {"status":"ok","uptime":"24h3m12s","clients":2,"version":"1.0.0"}
```

### Profiling

pprof endpoints available at `http://127.0.0.1:9091/debug/pprof/`:

```bash
go tool pprof http://127.0.0.1:9091/debug/pprof/profile?seconds=30
go tool pprof http://127.0.0.1:9091/debug/pprof/heap
```

## Security Hardening

### File Permissions

```bash
chmod 600 /etc/duman/duman-relay.yaml
chown duman:duman /etc/duman/duman-relay.yaml
```

### Rate Limiting

```yaml
rate_limit:
  requests_per_second: 50
  burst: 100
```

### Dashboard Access

Keep the dashboard bound to localhost. Use SSH tunneling to access remotely:

```bash
ssh -L 9091:127.0.0.1:9091 user@relay-server
# Then access http://127.0.0.1:9091 locally
```

### Automatic Updates

Create a cron job to check for updates:

```bash
# /etc/cron.daily/duman-update
#!/bin/bash
cd /opt/duman && git pull && make build && systemctl restart duman-relay
```

## Troubleshooting

### Relay won't start

```bash
# Check logs
journalctl -u duman-relay -f

# Common issues:
# - Port already in use (another PostgreSQL instance)
# - Permission denied (config file permissions)
# - Invalid config syntax
```

### Clients can't connect

```bash
# Test port is open
nc -zv relay.example.com 5432

# Check firewall
ufw status

# Test with psql
psql -h relay.example.com -U sensor_writer -d telemetry
```

### High memory usage

```bash
# Check with pprof
go tool pprof http://127.0.0.1:9091/debug/pprof/heap

# Common causes:
# - Too many concurrent clients (increase rate limits)
# - Large fake data cache (reduce row counts)
```
