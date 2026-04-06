#!/usr/bin/env bash
# Duman Relay — Docker Deployment Script
# Usage: bash docker-deploy.sh --domain relay.example.com --secret <key>
#
# Deploys duman-relay as a Docker container with auto-restart.

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
CONTAINER_NAME="duman-relay"
IMAGE_NAME="duman-relay"
IMAGE_TAG="latest"
REPO_URL="https://github.com/dumanproxy/duman.git"
CONFIG_DIR="${HOME}/.duman"
BUILD_LOCAL="auto"   # auto | yes | no

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
usage() {
    cat <<USAGE
Usage: $0 --domain <domain> --secret <key> [OPTIONS]

Required:
  --domain     <fqdn>       Domain or IP for the relay
  --secret     <base64key>  Shared secret (base64). Generate with: duman-relay keygen

Options:
  --port       <port>       Listen port (default: 5432)
  --scenario   <name>       Fake-data scenario (default: ecommerce)
  --image      <name:tag>   Docker image (default: duman-relay:latest)
  --name       <name>       Container name (default: duman-relay)
  --build                   Force build from source
  --no-build                Skip build, pull image only
  --repo       <url>        Git repo URL for building (default: ${REPO_URL})
  --config-dir <path>       Config directory (default: ~/.duman)
  --help                    Show this message
USAGE
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --domain)     DOMAIN="$2";         shift 2 ;;
        --secret)     SECRET="$2";         shift 2 ;;
        --port)       PORT="$2";           shift 2 ;;
        --scenario)   SCENARIO="$2";       shift 2 ;;
        --image)      IMAGE_NAME="${2%%:*}"; IMAGE_TAG="${2#*:}"; shift 2 ;;
        --name)       CONTAINER_NAME="$2"; shift 2 ;;
        --build)      BUILD_LOCAL="yes";   shift ;;
        --no-build)   BUILD_LOCAL="no";    shift ;;
        --repo)       REPO_URL="$2";       shift 2 ;;
        --config-dir) CONFIG_DIR="$2";     shift 2 ;;
        --help|-h)    usage ;;
        *)            fail "Unknown option: $1" ;;
    esac
done

[[ -z "$DOMAIN" ]] && fail "Missing required argument: --domain"
[[ -z "$SECRET" ]] && fail "Missing required argument: --secret"

FULL_IMAGE="${IMAGE_NAME}:${IMAGE_TAG}"

# ---------------------------------------------------------------------------
# 1. Check Docker
# ---------------------------------------------------------------------------
info "Checking Docker installation..."
if ! command -v docker &>/dev/null; then
    fail "Docker is not installed. Install it first: https://docs.docker.com/engine/install/"
fi

if ! docker info &>/dev/null; then
    fail "Docker daemon is not running, or current user lacks permission. Try: sudo usermod -aG docker \$USER"
fi

DOCKER_VER="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo 'unknown')"
success "Docker ${DOCKER_VER} is available"

# ---------------------------------------------------------------------------
# 2. Stop existing container (if any)
# ---------------------------------------------------------------------------
if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    info "Stopping existing container '${CONTAINER_NAME}'..."
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
    docker rm "$CONTAINER_NAME" >/dev/null 2>&1 || true
    success "Removed previous container"
fi

# ---------------------------------------------------------------------------
# 3. Pull or build image
# ---------------------------------------------------------------------------
acquire_image() {
    # Auto-detect: try pull first, fall back to build
    if [[ "$BUILD_LOCAL" == "no" ]]; then
        pull_image
        return
    fi

    if [[ "$BUILD_LOCAL" == "yes" ]]; then
        build_image
        return
    fi

    # auto mode: try pull, fall back to build
    info "Attempting to pull ${FULL_IMAGE}..."
    if docker pull "$FULL_IMAGE" 2>/dev/null; then
        success "Pulled ${FULL_IMAGE}"
        return
    fi

    warn "Image not found in registry — building from source"
    build_image
}

pull_image() {
    info "Pulling ${FULL_IMAGE}..."
    if docker pull "$FULL_IMAGE"; then
        success "Pulled ${FULL_IMAGE}"
    else
        fail "Failed to pull ${FULL_IMAGE}. Use --build to compile from source."
    fi
}

build_image() {
    local build_dir="/tmp/duman-docker-build-$$"
    info "Building ${FULL_IMAGE} from source..."

    if [[ -f "../Dockerfile.relay" ]]; then
        # We're inside the repo already
        info "Building from local repo..."
        docker build -t "$FULL_IMAGE" -f ../Dockerfile.relay ..
    else
        # Clone and build
        info "Cloning ${REPO_URL}..."
        rm -rf "$build_dir"
        git clone --depth 1 "$REPO_URL" "$build_dir"
        docker build -t "$FULL_IMAGE" -f "${build_dir}/Dockerfile.relay" "$build_dir"
        rm -rf "$build_dir"
    fi

    success "Built ${FULL_IMAGE}"
}

acquire_image

# ---------------------------------------------------------------------------
# 4. Generate configuration
# ---------------------------------------------------------------------------
info "Generating configuration..."
mkdir -p "$CONFIG_DIR"

AUTH_PASSWORD="$(openssl rand -hex 16 2>/dev/null || head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32)"
SEED="$((RANDOM * RANDOM))"

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
    tunnel: "${AUTH_PASSWORD}"

tunnel:
  shared_secret: "${SECRET}"
  max_streams: 1000
  role: "exit"

fake_data:
  scenario: "${SCENARIO}"
  seed: ${SEED}
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

chmod 600 "${CONFIG_DIR}/duman-relay.yaml"
success "Config written to ${CONFIG_DIR}/duman-relay.yaml"

# ---------------------------------------------------------------------------
# 5. Run container
# ---------------------------------------------------------------------------
info "Starting container '${CONTAINER_NAME}'..."

docker run -d \
    --name "$CONTAINER_NAME" \
    --restart unless-stopped \
    -p "${PORT}:${PORT}" \
    -v "${CONFIG_DIR}/duman-relay.yaml:/etc/duman/duman-relay.yaml:ro" \
    -e "DUMAN_LOG_LEVEL=info" \
    --memory=512m \
    --cpus=2 \
    "$FULL_IMAGE" \
    -c /etc/duman/duman-relay.yaml \
    >/dev/null

# Brief health check
sleep 2
if docker ps --filter "name=${CONTAINER_NAME}" --filter "status=running" -q | grep -q .; then
    success "Container '${CONTAINER_NAME}' is running"
else
    warn "Container may have failed. Recent logs:"
    docker logs --tail 20 "$CONTAINER_NAME" 2>&1
    fail "Container '${CONTAINER_NAME}' is not running"
fi

# ---------------------------------------------------------------------------
# 6. Success
# ---------------------------------------------------------------------------
CONTAINER_ID="$(docker ps -q --filter "name=${CONTAINER_NAME}" | head -1)"

echo ""
printf "${BOLD}${GREEN}========================================${NC}\n"
printf "${BOLD}${GREEN}  Duman Relay deployed via Docker!${NC}\n"
printf "${BOLD}${GREEN}========================================${NC}\n"
echo ""
info "Domain:    ${DOMAIN}"
info "Port:      ${PORT}"
info "Scenario:  ${SCENARIO}"
info "Container: ${CONTAINER_NAME} (${CONTAINER_ID:0:12})"
info "Image:     ${FULL_IMAGE}"
info "Config:    ${CONFIG_DIR}/duman-relay.yaml"
echo ""
printf "${CYAN}Connect from a client:${NC}\n"
echo ""
echo "  psql \"host=${DOMAIN} port=${PORT} dbname=tunnel user=tunnel sslmode=require\""
echo ""
printf "${YELLOW}Useful commands:${NC}\n"
echo "  docker logs -f ${CONTAINER_NAME}         # Follow logs"
echo "  docker restart ${CONTAINER_NAME}          # Restart"
echo "  docker stop ${CONTAINER_NAME}             # Stop"
echo "  docker rm -f ${CONTAINER_NAME}            # Remove"
echo "  docker exec ${CONTAINER_NAME} /duman-relay keygen   # New secret"
echo ""
