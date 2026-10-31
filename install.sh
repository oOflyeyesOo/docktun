#!/usr/bin/env bash
# ==============================================================================
# DockTun - Automated Installer
# Installs Docker (if missing) and starts DockTun via Docker Compose
# ==============================================================================

set -euo pipefail

# Text formatting
C_RESET='\033[0m'
C_BOLD='\033[1m'
C_GREEN='\033[0;32m'
C_CYAN='\033[0;36m'
C_YELLOW='\033[0;33m'
C_RED='\033[0;31m'

info() { echo -e "${C_CYAN}ℹ${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠${C_RESET} $1"; }
error() { echo -e "${C_RED}✖${C_RESET} $1"; exit 1; }

# Parse command-line arguments
while [ $# -gt 0 ]; do
    case "$1" in
        -p|--port)
            PORT="$2"
            shift 2
            ;;
        -d|--dir)
            DOCKTUN_DIR="$2"
            shift 2
            ;;
        -h|--help)
            echo "DockTun Automated Installer"
            echo "Usage: sudo ./install.sh [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  -p, --port <port>   Web dashboard port (default: 8080)"
            echo "  -d, --dir <path>    Installation directory (default: /opt/docktun)"
            echo "  -h, --help          Show this help message"
            echo ""
            echo "Examples:"
            echo "  sudo ./install.sh --port 8080"
            echo "  sudo ./install.sh -p 9000 -d /srv/docktun"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

# Root privilege check
if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    error "This installer requires root privileges.\n  Quick run:  curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/install.sh | sudo bash\n  Local file: sudo bash install.sh"
fi

echo -e "${C_CYAN}${C_BOLD}"
cat << 'EOF'
    ____             __  ______           
   / __ \____  _____/ /_/_  __/_  ______ 
  / / / / __ \/ ___/ //_// / / / / / __ \
 / /_/ / /_/ / /__/ ,<  / / / /_/ / / / /
/_____/\____/\___/_/|_|/_/  \__,_/_/ /_/ 

Container & Remote Access Infrastructure
EOF
echo -e "${C_RESET}"

# ------------------------------------------------------------------------------
# 1. Install Docker & Compose if missing
# ------------------------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
    if command -v synopkg >/dev/null 2>&1 || [ -f /etc.defaults/VERSION ]; then
        error "Install and start Container Manager (or Docker) from Synology Package Center, then run this installer again."
    fi
    info "Docker not found. Installing Docker Engine..."
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL https://get.docker.com | sh
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- https://get.docker.com | sh
    else
        error "Neither curl nor wget found. Please install curl and re-run."
    fi
    success "Docker installed successfully."
fi

# Ensure Docker daemon is running
if ! docker info >/dev/null 2>&1; then
    info "Starting Docker service..."
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now docker >/dev/null 2>&1 || systemctl start docker >/dev/null 2>&1 || true
    elif command -v service >/dev/null 2>&1; then
        service docker start >/dev/null 2>&1 || true
    fi
    sleep 2
    if ! docker info >/dev/null 2>&1; then
        error "Docker daemon could not be started. Please verify your Docker service."
    fi
fi
success "Docker daemon is active."

# Determine Docker Compose command
COMPOSE_CMD=""
if docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE_VERSION=$(docker-compose version --short 2>/dev/null || true)
    COMPOSE_MAJOR=${COMPOSE_VERSION#v}
    COMPOSE_MAJOR=${COMPOSE_MAJOR%%.*}
    if [[ ! "$COMPOSE_MAJOR" =~ ^[0-9]+$ ]] || [ "$COMPOSE_MAJOR" -lt 2 ]; then
        error "Docker Compose v2 or newer is required. Update Synology Container Manager or install the Compose plugin."
    fi
    COMPOSE_CMD="docker-compose"
else
    info "Docker Compose plugin not found. Attempting installation..."
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -qq && apt-get install -y -qq docker-compose-plugin >/dev/null 2>&1 || true
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y -q docker-compose-plugin >/dev/null 2>&1 || true
    elif command -v yum >/dev/null 2>&1; then
        yum install -y -q docker-compose-plugin >/dev/null 2>&1 || true
    fi

    if docker compose version >/dev/null 2>&1; then
        COMPOSE_CMD="docker compose"
    else
        error "Docker Compose v2 is required. Please install 'docker-compose-plugin' and re-run."
    fi
fi
success "Using: $COMPOSE_CMD"

# ------------------------------------------------------------------------------
# 2. Setup DockTun Directory & compose.yml
# ------------------------------------------------------------------------------
INSTALL_DIR="${DOCKTUN_DIR:-/opt/docktun}"
PORT="${PORT:-8080}"
if [[ ! "$PORT" =~ ^[0-9]{1,5}$ ]] || [ "$((10#$PORT))" -lt 1 ] || [ "$((10#$PORT))" -gt 65535 ]; then
    error "PORT must be a number between 1 and 65535."
fi

info "Preparing installation in ${C_BOLD}${INSTALL_DIR}${C_RESET} on port ${C_BOLD}${PORT}${C_RESET}..."
mkdir -p "${INSTALL_DIR}/projects"
cd "${INSTALL_DIR}"

COMPOSE_FILE=""
for candidate in compose.yml compose.yaml docker-compose.yml docker-compose.yaml; do
    if [ -f "$candidate" ]; then
        if [ -n "$COMPOSE_FILE" ]; then
            error "Multiple Compose files found in $INSTALL_DIR. Keep one active Compose file before running the installer."
        fi
        COMPOSE_FILE="$candidate"
    fi
done

if [ -n "$COMPOSE_FILE" ]; then
    info "Keeping existing ${COMPOSE_FILE}; your saved settings will not be overwritten."
else
COMPOSE_FILE="compose.yml"
cat > "$COMPOSE_FILE" << EOF
name: docktun
services:
  docktun:
    image: ghcr.io/ooflyeyesoo/docktun:latest
    container_name: docktun
    restart: unless-stopped
    ports:
      - "${PORT}:3001"
    environment:
      DOCKER_NETWORK: docktun
      TAILSCALE_STATE_VOLUME: docktun-tailscale-state
      WIREGUARD_PORT: 51820
      DB_PATH: /app/data/app.db
      CLOUDFLARED_CONFIG_PATH: /etc/cloudflared/config.yml
      CLOUDFLARED_CONTAINER: docktun-cloudflared
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - docktun-data:/app/data
      - docktun-cloudflared:/etc/cloudflared
      - docktun-wireguard:/app/data/wireguard
      - ./projects:/opt/projects
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:3001/healthz"]
      interval: 30s
      timeout: 5s
      start_period: 30s
      retries: 3
    networks:
      - docktun

volumes:
  docktun-data:
  docktun-cloudflared:
  docktun-wireguard:

networks:
  docktun:
    name: docktun
    driver: bridge
EOF

success "Configuration saved to ${INSTALL_DIR}/${COMPOSE_FILE}"
fi
$COMPOSE_CMD -f "$COMPOSE_FILE" config --quiet
if ! $COMPOSE_CMD -f "$COMPOSE_FILE" config --services | grep -qx docktun; then
    error "Existing Compose file does not contain a docktun service. Use a separate installation directory."
fi

# ------------------------------------------------------------------------------
# 3. Launch DockTun
# ------------------------------------------------------------------------------
info "Pulling image and starting DockTun container..."
$COMPOSE_CMD -f "$COMPOSE_FILE" pull docktun
$COMPOSE_CMD -f "$COMPOSE_FILE" up -d --no-deps docktun

info "Waiting for DockTun to initialize..."
SETUP_TOKEN=""
READY=false
for i in $(seq 1 15); do
    if docker exec docktun curl -fsS http://127.0.0.1:3001/healthz >/dev/null 2>&1; then
        SETUP_TOKEN=$(docker exec docktun cat /app/data/setup.token 2>/dev/null || true)
        READY=true
        break
    fi
    sleep 1
done
if [ "$READY" != true ]; then
    error "DockTun did not become ready. Check: $COMPOSE_CMD -f $COMPOSE_FILE logs docktun"
fi

# Detect host IP
SERVER_IP=""
if command -v hostname >/dev/null 2>&1; then
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
fi
if [ -z "$SERVER_IP" ] && command -v ip >/dev/null 2>&1; then
    SERVER_IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{print $7}' || true)
fi
SERVER_IP="${SERVER_IP:-<your-server-ip>}"
# Existing installations may use a different port or a loopback-only binding.
BOUND_PORT=$($COMPOSE_CMD -f "$COMPOSE_FILE" port docktun 3001 2>/dev/null | head -n 1 || true)
if [ -n "$BOUND_PORT" ]; then
    PORT=${BOUND_PORT##*:}
    BIND_ADDRESS=${BOUND_PORT%:*}
    BIND_ADDRESS=${BIND_ADDRESS#[}
    BIND_ADDRESS=${BIND_ADDRESS%]}
    if [ "$BIND_ADDRESS" != "0.0.0.0" ] && [ "$BIND_ADDRESS" != "::" ]; then
        SERVER_IP="$BIND_ADDRESS"
    fi
fi
if [[ "$SERVER_IP" == *:* ]]; then SERVER_IP="[$SERVER_IP]"; fi

# ------------------------------------------------------------------------------
# 4. Completion Banner
# ------------------------------------------------------------------------------
echo ""
echo -e "${C_GREEN}${C_BOLD}===================================================================${C_RESET}"
echo -e "${C_GREEN}${C_BOLD}  🎉 DockTun successfully installed & running!                    ${C_RESET}"
echo -e "${C_GREEN}${C_BOLD}===================================================================${C_RESET}"
echo ""
echo -e "  🌐 ${C_BOLD}Web Dashboard:${C_RESET}  ${C_CYAN}http://${SERVER_IP}:${PORT}${C_RESET}"

if [ -n "$SETUP_TOKEN" ]; then
    echo ""
    echo -e "  🔑 ${C_BOLD}Ownership Setup Token:${C_RESET}"
    echo -e "     ${C_YELLOW}${C_BOLD}${SETUP_TOKEN}${C_RESET}"
    echo -e "     (Enter this token in your browser to create your admin account)"
else
    echo ""
    echo -e "  🔑 ${C_BOLD}To retrieve your Ownership Setup Token, run:${C_RESET}"
    echo -e "     ${C_YELLOW}docker exec docktun cat /app/data/setup.token${C_RESET}"
fi

echo ""
echo -e "  📁 ${C_BOLD}Location:${C_RESET}       ${INSTALL_DIR}"
echo -e "  ⚙️  ${C_BOLD}Manage Stack:${C_RESET}   cd ${INSTALL_DIR} && ${COMPOSE_CMD} [logs -f | restart | down]"
echo ""
