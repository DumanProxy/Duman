#!/usr/bin/env bash
# Duman Relay — Standalone Firewall Configuration
# Usage: sudo bash setup-firewall.sh [--port 5432] [--ssh-port 22]
#
# Configures UFW to allow only the relay port and SSH, denying everything else.

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
RELAY_PORT="5432"
SSH_PORT="22"
RATE_LIMIT_SSH="yes"
DRY_RUN="no"

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
usage() {
    cat <<USAGE
Usage: sudo $0 [OPTIONS]

Options:
  --port       <port>   Duman relay port to allow (default: 5432)
  --ssh-port   <port>   SSH port to allow (default: 22)
  --no-rate-limit       Disable SSH rate limiting
  --dry-run             Show what would be done without applying
  --help                Show this message

This script:
  1. Installs UFW if not present
  2. Resets UFW to defaults
  3. Sets default deny incoming / allow outgoing
  4. Allows SSH (with optional rate limiting)
  5. Allows the Duman relay port
  6. Enables UFW
USAGE
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --port)          RELAY_PORT="$2";     shift 2 ;;
        --ssh-port)      SSH_PORT="$2";       shift 2 ;;
        --no-rate-limit) RATE_LIMIT_SSH="no"; shift ;;
        --dry-run)       DRY_RUN="yes";       shift ;;
        --help|-h)       usage ;;
        *)               fail "Unknown option: $1" ;;
    esac
done

# ---------------------------------------------------------------------------
# Validate
# ---------------------------------------------------------------------------
validate_port() {
    local port="$1" label="$2"
    if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
        fail "Invalid ${label} port: ${port} (must be 1-65535)"
    fi
}

validate_port "$RELAY_PORT" "relay"
validate_port "$SSH_PORT" "SSH"

# ---------------------------------------------------------------------------
# Root check
# ---------------------------------------------------------------------------
if [[ $EUID -ne 0 ]]; then
    fail "This script must be run as root. Use: sudo bash $0 ..."
fi

# ---------------------------------------------------------------------------
# Dry-run wrapper
# ---------------------------------------------------------------------------
run() {
    if [[ "$DRY_RUN" == "yes" ]]; then
        info "[DRY RUN] $*"
    else
        "$@"
    fi
}

# ---------------------------------------------------------------------------
# 1. Install UFW if not present
# ---------------------------------------------------------------------------
info "Checking UFW installation..."
if ! command -v ufw &>/dev/null; then
    info "Installing UFW..."
    run apt-get update -qq
    run apt-get install -y -qq ufw
    success "UFW installed"
else
    success "UFW is already installed"
fi

# ---------------------------------------------------------------------------
# 2. Reset to clean state
# ---------------------------------------------------------------------------
info "Resetting UFW to defaults..."
run ufw --force reset >/dev/null 2>&1

# ---------------------------------------------------------------------------
# 3. Default policies
# ---------------------------------------------------------------------------
info "Setting default policies..."
run ufw default deny incoming >/dev/null
run ufw default allow outgoing >/dev/null
success "Default: deny incoming, allow outgoing"

# ---------------------------------------------------------------------------
# 4. Allow SSH
# ---------------------------------------------------------------------------
if [[ "$RATE_LIMIT_SSH" == "yes" ]]; then
    info "Allowing SSH on port ${SSH_PORT}/tcp (rate limited)..."
    run ufw limit "${SSH_PORT}/tcp" comment "SSH - rate limited" >/dev/null
    success "SSH port ${SSH_PORT} allowed with rate limiting (max 6 connections/30s)"
else
    info "Allowing SSH on port ${SSH_PORT}/tcp..."
    run ufw allow "${SSH_PORT}/tcp" comment "SSH" >/dev/null
    success "SSH port ${SSH_PORT} allowed"
fi

# ---------------------------------------------------------------------------
# 5. Allow Duman relay port
# ---------------------------------------------------------------------------
info "Allowing Duman relay on port ${RELAY_PORT}/tcp..."
run ufw allow "${RELAY_PORT}/tcp" comment "Duman Relay" >/dev/null
success "Relay port ${RELAY_PORT} allowed"

# ---------------------------------------------------------------------------
# 6. Enable UFW
# ---------------------------------------------------------------------------
info "Enabling UFW..."
if [[ "$DRY_RUN" == "yes" ]]; then
    info "[DRY RUN] ufw --force enable"
else
    ufw --force enable >/dev/null
fi
success "UFW is active"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
printf "${BOLD}${GREEN}========================================${NC}\n"
printf "${BOLD}${GREEN}  Firewall configured successfully!${NC}\n"
printf "${BOLD}${GREEN}========================================${NC}\n"
echo ""

if [[ "$DRY_RUN" == "no" ]]; then
    info "Current rules:"
    echo ""
    ufw status verbose
    echo ""
else
    info "Dry run complete. No changes were applied."
    echo ""
    info "Rules that would be applied:"
    echo "  - Default deny incoming"
    echo "  - Default allow outgoing"
    if [[ "$RATE_LIMIT_SSH" == "yes" ]]; then
        echo "  - SSH (${SSH_PORT}/tcp) — rate limited"
    else
        echo "  - SSH (${SSH_PORT}/tcp) — allowed"
    fi
    echo "  - Duman Relay (${RELAY_PORT}/tcp) — allowed"
    echo ""
fi

printf "${YELLOW}Tips:${NC}\n"
echo "  ufw status numbered    # Show rules with numbers"
echo "  ufw delete <num>       # Delete a rule by number"
echo "  ufw allow 3306/tcp     # Add MySQL wire-protocol port"
echo "  ufw allow 443/tcp      # Add REST/HTTPS port"
echo "  ufw disable            # Disable firewall"
echo ""
