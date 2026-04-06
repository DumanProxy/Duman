# Duman Quick Start — Demo Setup

**Relay on a Linux server, client on Windows. Up and running in 5 minutes.**

---

## 1. Generate a Shared Secret

```bash
# With Go installed:
go run ./cmd/duman-client keygen

# Or with OpenSSL:
openssl rand -base64 32
```

Save the output — both the server and client need the same value.

---

## 2. Linux Server — Relay Setup

### Automated (recommended)

```bash
# Copy binary to the server
scp dist/duman-relay-linux-amd64 user@server:/tmp/

# SSH in and install
ssh user@server
sudo bash -c '
  cp /tmp/duman-relay-linux-amd64 /usr/local/bin/duman-relay
  chmod +x /usr/local/bin/duman-relay
  duman-relay --version
'
```

### Or use the install script:

```bash
# Clone the repo on the server or copy the script over
sudo bash scripts/install-relay.sh --from-local /tmp/duman-relay-linux-amd64
```

The script creates a systemd service, generates secrets, configures the firewall, and prints the shared secret + password you need for the client.

### Manual Config

Create `/etc/duman/relay.yaml` (or `duman-relay.yaml` in the working directory):

```yaml
listen:
  postgresql: ":5432"

auth:
  method: "md5"
  users:
    sensor_writer: "YOUR_STRONG_PASSWORD"

tunnel:
  shared_secret: "YOUR_SHARED_SECRET"
  role: "exit"

fake_data:
  scenario: "ecommerce"
  seed: 12345
  mode: "template"
  mutate: true

exit:
  max_idle_secs: 300

log:
  level: "info"
  format: "text"
  output: "stderr"
```

### Start

```bash
# Foreground (for testing):
duman-relay -c /etc/duman/relay.yaml -v

# With systemd (if you used the install script):
sudo systemctl start duman-relay
sudo systemctl status duman-relay
sudo journalctl -u duman-relay -f
```

### Firewall

```bash
sudo ufw allow 5432/tcp   # UFW
# or
sudo iptables -A INPUT -p tcp --dport 5432 -j ACCEPT
```

---

## 3. Windows Client — Setup

### Download / copy the binary

Place `dist/duman-client-windows-amd64.exe` wherever you like.

### Install with PowerShell:

```powershell
# Open an admin PowerShell, navigate to the repo
.\scripts\install-client.ps1 `
  -RelayAddress "SERVER_IP:5432" `
  -SharedSecret "YOUR_SHARED_SECRET" `
  -Password "YOUR_STRONG_PASSWORD"
```

### Manual Config

Create `%APPDATA%\Duman\client.yaml`:

```yaml
proxy:
  listen: "127.0.0.1:1080"
  mode: "socks5"

tunnel:
  shared_secret: "YOUR_SHARED_SECRET"
  chunk_size: 16384
  response_mode: "poll"
  cipher: "auto"

relays:
  - address: "SERVER_IP:5432"
    protocol: "postgresql"
    weight: 10
    database: "analytics"
    username: "sensor_writer"
    password: "YOUR_STRONG_PASSWORD"

scenario: "ecommerce"

schema:
  mode: "template"
  mutate: true
  seed: 12345

log:
  level: "info"
  format: "text"
  output: "stderr"
```

### Start

```cmd
duman-client.exe -c %APPDATA%\Duman\client.yaml -v
```

You should see:
```
INFO  client ready  socks5=127.0.0.1:1080
INFO  SOCKS5 proxy listening  addr=127.0.0.1:1080
```

---

## 4. Verify

### With curl (through the SOCKS5 proxy):

```bash
# Linux/Mac:
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me

# Windows (Git Bash or WSL):
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

The output should be the **server's IP**, not your local IP.

### With a browser:

**Firefox:**
1. Settings → Network Settings → Manual proxy
2. SOCKS Host: `127.0.0.1`, Port: `1080`
3. Select SOCKS v5
4. Check "Proxy DNS when using SOCKS v5"
5. Visit https://ifconfig.me — should show the relay server's IP

**Chrome (command line):**
```
chrome.exe --proxy-server="socks5://127.0.0.1:1080"
```

---

## 5. Troubleshooting

| Problem | Solution |
|---------|----------|
| `config error: proxy.listen is required` | Config file not found. Pass the path with `-c` |
| `connect relays: dial tcp: connection refused` | Is the relay running on the server? Is the port open? |
| `invalid tunnel auth token` | `shared_secret` mismatch — must be identical on both sides |
| `auth failed` | username/password mismatch between client and relay config |
| Cannot connect to SOCKS5 | Is the client running? Is it listening on `127.0.0.1:1080`? |
| No response data | Check `exit.max_idle_secs` on the relay |

### Debug mode:

```bash
# Relay:
duman-relay -c relay.yaml -v

# Client:
duman-client -c client.yaml -v
```

---

## Architecture

```
[Windows]                           [Linux Server]                    [Internet]

  Browser --> SOCKS5 --> duman-client --> PostgreSQL wire --> duman-relay --> Target
  (1080)     Tunnel chunks hidden         (5432)            Exit engine
             inside INSERT/SELECT                           dials target
             statements
```

Traffic looks like a real PostgreSQL analytics database.
DPI systems see `INSERT INTO analytics_events ...` — not encrypted tunnel data.
