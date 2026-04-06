#!/usr/bin/env bash
#
# Duman Relay Installer for Linux
# Usage: curl -sSL https://raw.githubusercontent.com/.../install-relay.sh | sudo bash
#    or: sudo bash scripts/install-relay.sh [--from-local dist/duman-relay-linux-amd64]
#
set -euo pipefail

# --- Configuration ---
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/duman"
DATA_DIR="/var/lib/duman"
LOG_DIR="/var/log/duman"
SERVICE_USER="duman"
SERVICE_NAME="duman-relay"
VERSION="${DUMAN_VERSION:-0.1.0}"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*" >&2; }

# --- Root check ---
if [[ $EUID -ne 0 ]]; then
    err "This script must be run as root (use sudo)"
    exit 1
fi

# --- Detect architecture ---
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)       err "Unsupported architecture: $ARCH"; exit 1 ;;
esac

BINARY_NAME="duman-relay-linux-${GOARCH}"

echo ""
echo -e "${CYAN}╔══════════════════════════════════════╗${NC}"
echo -e "${CYAN}║     Duman Relay Installer v${VERSION}     ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════╝${NC}"
echo ""

# --- Install binary ---
LOCAL_BINARY=""
for arg in "$@"; do
    case "$arg" in
        --from-local) LOCAL_BINARY="next" ;;
        *) [[ "$LOCAL_BINARY" == "next" ]] && LOCAL_BINARY="$arg" ;;
    esac
done

if [[ -n "$LOCAL_BINARY" && "$LOCAL_BINARY" != "next" ]]; then
    info "Installing from local binary: $LOCAL_BINARY"
    cp "$LOCAL_BINARY" "${INSTALL_DIR}/duman-relay"
elif [[ -f "dist/${BINARY_NAME}" ]]; then
    info "Installing from dist/${BINARY_NAME}"
    cp "dist/${BINARY_NAME}" "${INSTALL_DIR}/duman-relay"
elif [[ -f "bin/${BINARY_NAME}" ]]; then
    info "Installing from bin/${BINARY_NAME}"
    cp "bin/${BINARY_NAME}" "${INSTALL_DIR}/duman-relay"
else
    info "Downloading duman-relay v${VERSION} for ${GOARCH}..."
    DOWNLOAD_URL="https://github.com/dumanproxy/duman/releases/download/v${VERSION}/${BINARY_NAME}.tar.gz"
    TMP=$(mktemp -d)
    trap "rm -rf $TMP" EXIT
    curl -sSL "$DOWNLOAD_URL" -o "${TMP}/duman-relay.tar.gz"
    tar xzf "${TMP}/duman-relay.tar.gz" -C "$TMP"
    cp "${TMP}/${BINARY_NAME}" "${INSTALL_DIR}/duman-relay"
fi

chmod +x "${INSTALL_DIR}/duman-relay"
ok "Binary installed: ${INSTALL_DIR}/duman-relay"

# --- Verify binary ---
"${INSTALL_DIR}/duman-relay" --version || true

# --- Create service user ---
if ! id "$SERVICE_USER" &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    ok "Created system user: $SERVICE_USER"
else
    ok "User $SERVICE_USER already exists"
fi

# --- Create directories ---
mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chown "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR" "$LOG_DIR"
ok "Created directories: $CONFIG_DIR, $DATA_DIR, $LOG_DIR"

# --- Generate shared secret ---
SHARED_SECRET=$("${INSTALL_DIR}/duman-relay" keygen 2>/dev/null || openssl rand -base64 32)
ok "Generated shared secret"

# --- Create config ---
if [[ ! -f "${CONFIG_DIR}/relay.yaml" ]]; then
    cat > "${CONFIG_DIR}/relay.yaml" << YAML
# Duman Relay Configuration — v${VERSION}
# Generated on $(date -Iseconds)

listen:
  postgresql: ":5432"

auth:
  method: "md5"
  users:
    sensor_writer: "$(openssl rand -hex 16 2>/dev/null || head -c 32 /dev/urandom | base64)"

tunnel:
  shared_secret: "${SHARED_SECRET}"
  role: "exit"

fake_data:
  scenario: "ecommerce"
  seed: $(shuf -i 1-99999 -n 1)
  mode: "template"
  mutate: true

exit:
  max_conns: 1000
  max_idle_secs: 300

log:
  level: "info"
  format: "text"
  output: "${LOG_DIR}/relay.log"
YAML
    chmod 600 "${CONFIG_DIR}/relay.yaml"
    chown "$SERVICE_USER:$SERVICE_USER" "${CONFIG_DIR}/relay.yaml"
    ok "Config created: ${CONFIG_DIR}/relay.yaml"
else
    warn "Config already exists: ${CONFIG_DIR}/relay.yaml (not overwritten)"
fi

# --- Create systemd service ---
cat > "/etc/systemd/system/${SERVICE_NAME}.service" << SERVICE
[Unit]
Description=Duman Relay — PostgreSQL Steganographic Tunnel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
ExecStart=${INSTALL_DIR}/duman-relay -c ${CONFIG_DIR}/relay.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR} ${LOG_DIR}
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
ok "Systemd service created: ${SERVICE_NAME}"

# --- Firewall hint ---
info "Opening port 5432 (if ufw is active)..."
if command -v ufw &>/dev/null && ufw status | grep -q "active"; then
    ufw allow 5432/tcp comment "Duman Relay" || true
    ok "Firewall rule added"
else
    warn "No active ufw detected — make sure port 5432 is open"
fi

# --- Start service ---
echo ""
info "Starting duman-relay..."
systemctl enable "${SERVICE_NAME}" --now
sleep 1

if systemctl is-active --quiet "${SERVICE_NAME}"; then
    ok "duman-relay is running!"
else
    err "duman-relay failed to start. Check: journalctl -u ${SERVICE_NAME} -f"
fi

# --- Summary ---
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║            Installation Complete!                ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════╝${NC}"
echo ""
echo "  Binary:       ${INSTALL_DIR}/duman-relay"
echo "  Config:       ${CONFIG_DIR}/relay.yaml"
echo "  Logs:         ${LOG_DIR}/relay.log"
echo "  Service:      systemctl status ${SERVICE_NAME}"
echo ""
echo -e "  ${YELLOW}SHARED SECRET (copy to client config):${NC}"
echo -e "  ${CYAN}${SHARED_SECRET}${NC}"
echo ""
echo "  Get auth password from config:"
echo "    grep 'sensor_writer' ${CONFIG_DIR}/relay.yaml"
echo ""
echo "  Commands:"
echo "    systemctl status duman-relay     # Check status"
echo "    systemctl restart duman-relay    # Restart"
echo "    journalctl -u duman-relay -f     # Live logs"
echo ""
