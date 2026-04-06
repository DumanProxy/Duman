#!/usr/bin/env bash
# Duman Relay — DigitalOcean/Ubuntu Deployment Script
# Usage: curl -sL <url> | bash -s -- --domain relay.example.com --secret <key>
#        Or: bash digitalocean.sh --domain relay.example.com --secret <key>
#
# Deploys duman-relay as a systemd service on Ubuntu 20.04+/Debian 11+.

set -euo pipefail

# ---------------------------------------------------------------------------
# Colors & helpers
# ---------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()    { printf "${CYAN}[INFO]${NC}  %s\n" "$*"; }
success() { printf "${GREEN}[OK]${NC}    %s\n" "$*"; }
warn()    { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
fail()    { printf "${RED}[FAIL]${NC}  %s\n" "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
DOMAIN=""
SECRET=""
PORT="5432"
SCENARIO="ecommerce"
REPO_URL="https://github.com/dumanproxy/duman.git"
GO_VERSION="1.23.4"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/duman"
DATA_DIR="/var/lib/duman"
SERVICE_FILE="/etc/systemd/system/duman-relay.service"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
usage() {
    cat <<USAGE
Usage: $0 --domain <domain> --secret <key> [OPTIONS]

Required:
  --domain   <fqdn>       Domain or IP for the relay (e.g. relay.example.com)
  --secret   <base64key>  Shared secret (base64). Generate with: duman-relay keygen

Options:
  --port     <port>       PostgreSQL wire-protocol listen port (default: 5432)
  --scenario <name>       Fake-data scenario: ecommerce|iot|saas|blog|project (default: ecommerce)
  --repo     <url>        Git repo URL (default: ${REPO_URL})
  --help                  Show this message
USAGE
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --domain)   DOMAIN="$2";   shift 2 ;;
        --secret)   SECRET="$2";   shift 2 ;;
        --port)     PORT="$2";     shift 2 ;;
        --scenario) SCENARIO="$2"; shift 2 ;;
        --repo)     REPO_URL="$2"; shift 2 ;;
        --help|-h)  usage ;;
        *)          fail "Unknown option: $1" ;;
    esac
done

[[ -z "$DOMAIN" ]] && fail "Missing required argument: --domain"
[[ -z "$SECRET" ]] && fail "Missing required argument: --secret"

# ---------------------------------------------------------------------------
# 1. Root check
# ---------------------------------------------------------------------------
info "Checking permissions..."
if [[ $EUID -ne 0 ]]; then
    fail "This script must be run as root. Use: sudo bash $0 ..."
fi
success "Running as root"

# ---------------------------------------------------------------------------
# 2. OS check
# ---------------------------------------------------------------------------
info "Detecting OS..."
if [[ -f /etc/os-release ]]; then
    . /etc/os-release
    info "Detected: ${PRETTY_NAME:-$ID}"
else
    warn "Cannot detect OS — proceeding anyway (assumes Debian/Ubuntu-like)"
fi

# ---------------------------------------------------------------------------
# 3. Install Go 1.23+ if not present
# ---------------------------------------------------------------------------
install_go() {
    local current_ver=""
    if command -v go &>/dev/null; then
        current_ver="$(go version | grep -oP 'go\K[0-9]+\.[0-9]+' || true)"
    fi

    # Check if installed version >= 1.23
    if [[ -n "$current_ver" ]]; then
        local major minor
        major="$(echo "$current_ver" | cut -d. -f1)"
        minor="$(echo "$current_ver" | cut -d. -f2)"
        if (( major > 1 || (major == 1 && minor >= 23) )); then
            success "Go ${current_ver} already installed (>= 1.23)"
            return
        fi
        warn "Go ${current_ver} is too old; installing Go ${GO_VERSION}"
    else
        info "Go not found; installing Go ${GO_VERSION}"
    fi

    local arch
    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    case "$arch" in
        amd64|x86_64)  arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)             fail "Unsupported architecture: $arch" ;;
    esac

    local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
    local url="https://go.dev/dl/${tarball}"

    info "Downloading ${url} ..."
    apt-get update -qq && apt-get install -y -qq wget git >/dev/null
    wget -q --show-progress -O "/tmp/${tarball}" "$url"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${tarball}"
    rm -f "/tmp/${tarball}"

    # Ensure go is on PATH for this session and future logins
    export PATH="/usr/local/go/bin:$PATH"
    if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
        echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
    fi

    success "Go $(go version | awk '{print $3}') installed"
}

install_go

# ---------------------------------------------------------------------------
# 4. Create duman system user
# ---------------------------------------------------------------------------
info "Creating duman user..."
if id duman &>/dev/null; then
    success "User 'duman' already exists"
else
    useradd --system --no-create-home --shell /usr/sbin/nologin duman
    success "User 'duman' created"
fi

# ---------------------------------------------------------------------------
# 5. Clone/download and build duman-relay
# ---------------------------------------------------------------------------
BUILD_DIR="/tmp/duman-build-$$"

build_relay() {
    info "Building duman-relay from source..."

    rm -rf "$BUILD_DIR"
    mkdir -p "$BUILD_DIR"

    info "Cloning ${REPO_URL} ..."
    git clone --depth 1 "$REPO_URL" "$BUILD_DIR"

    cd "$BUILD_DIR"
    info "Compiling (CGO_ENABLED=0) ..."
    CGO_ENABLED=0 /usr/local/go/bin/go build \
        -ldflags="-s -w" \
        -o "${INSTALL_DIR}/duman-relay" \
        ./cmd/duman-relay

    cd /
    rm -rf "$BUILD_DIR"

    chmod 755 "${INSTALL_DIR}/duman-relay"
    success "duman-relay installed to ${INSTALL_DIR}/duman-relay"
}

build_relay

# ---------------------------------------------------------------------------
# 6. Generate configuration YAML
# ---------------------------------------------------------------------------
info "Generating configuration..."
mkdir -p "$CONFIG_DIR" "$DATA_DIR"

cat > "${CONFIG_DIR}/duman-relay.yaml" <<YAML
# Duman Relay Configuration — generated $(date -u +%Y-%m-%dT%H:%M:%SZ)
# Domain: ${DOMAIN}

listen:
  postgresql: ":${PORT}"

tls:
  mode: "self_signed"
  # Switch to ACME for automatic Let's Encrypt:
  # mode: "acme"
  # domain: "${DOMAIN}"

auth:
  method: "scram-sha-256"
  users:
    tunnel: "$(openssl rand -hex 16)"

tunnel:
  shared_secret: "${SECRET}"
  max_streams: 1000
  role: "exit"

fake_data:
  scenario: "${SCENARIO}"
  seed: $(( RANDOM * RANDOM ))
  mode: "template"
  mutate: false

exit:
  max_conns: 1000
  max_idle_secs: 300

log:
  level: "info"
  format: "text"
  output: "stderr"
YAML

chown root:duman "${CONFIG_DIR}/duman-relay.yaml"
chmod 640 "${CONFIG_DIR}/duman-relay.yaml"
chown duman:duman "$DATA_DIR"

success "Config written to ${CONFIG_DIR}/duman-relay.yaml"

# ---------------------------------------------------------------------------
# 7. Install systemd service
# ---------------------------------------------------------------------------
info "Installing systemd service..."

# Copy from repo if present, otherwise generate
if [[ -f "${SCRIPT_DIR}/duman-relay.service" ]]; then
    cp "${SCRIPT_DIR}/duman-relay.service" "$SERVICE_FILE"
    info "Copied service file from deploy/"
else
    cat > "$SERVICE_FILE" <<'SERVICE'
[Unit]
Description=Duman Relay - Steganographic Database Tunnel
Documentation=https://github.com/dumanproxy/duman
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=duman
Group=duman
ExecStart=/usr/local/bin/duman-relay -c /etc/duman/duman-relay.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/duman
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true

# Capabilities — only bind to privileged ports if needed
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
SERVICE
fi

success "Service file installed to ${SERVICE_FILE}"

# ---------------------------------------------------------------------------
# 8. Enable and start service
# ---------------------------------------------------------------------------
info "Enabling and starting duman-relay..."
systemctl daemon-reload
systemctl enable duman-relay
systemctl start duman-relay

# Brief wait then check status
sleep 2
if systemctl is-active --quiet duman-relay; then
    success "duman-relay is running"
else
    warn "Service may have failed to start. Checking logs:"
    journalctl -u duman-relay --no-pager -n 20
    fail "duman-relay failed to start — check logs above"
fi

# ---------------------------------------------------------------------------
# 9. Configure UFW firewall
# ---------------------------------------------------------------------------
info "Configuring firewall (UFW)..."
if command -v ufw &>/dev/null; then
    ufw --force enable >/dev/null 2>&1 || true
    ufw allow 22/tcp comment "SSH" >/dev/null
    ufw allow "${PORT}/tcp" comment "Duman Relay" >/dev/null
    ufw default deny incoming >/dev/null
    ufw default allow outgoing >/dev/null
    ufw reload >/dev/null
    success "UFW configured: allow SSH (22) + Duman (${PORT})"
else
    warn "UFW not found — skipping firewall configuration"
    warn "Make sure ports 22 and ${PORT} are accessible"
fi

# ---------------------------------------------------------------------------
# 10. Done
# ---------------------------------------------------------------------------
echo ""
printf "${BOLD}${GREEN}========================================${NC}\n"
printf "${BOLD}${GREEN}  Duman Relay deployed successfully!${NC}\n"
printf "${BOLD}${GREEN}========================================${NC}\n"
echo ""
info "Domain:   ${DOMAIN}"
info "Port:     ${PORT}"
info "Scenario: ${SCENARIO}"
info "Config:   ${CONFIG_DIR}/duman-relay.yaml"
info "Service:  systemctl status duman-relay"
info "Logs:     journalctl -fu duman-relay"
echo ""
printf "${CYAN}Connect from a client:${NC}\n"
echo ""
echo "  psql \"host=${DOMAIN} port=${PORT} dbname=tunnel user=tunnel sslmode=require\""
echo ""
printf "${YELLOW}Useful commands:${NC}\n"
echo "  systemctl restart duman-relay    # Restart"
echo "  systemctl stop duman-relay       # Stop"
echo "  journalctl -fu duman-relay       # Follow logs"
echo "  duman-relay keygen               # Generate new shared secret"
echo ""
