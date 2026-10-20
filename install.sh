#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE="${SCRIPT_DIR}/.env.example"
CLOUDFLARED_DIR="${SCRIPT_DIR}/cloudflared"
CLOUDFLARED_CONFIG="${CLOUDFLARED_DIR}/config.yml"
CLOUDFLARED_CREDS="${CLOUDFLARED_DIR}/credentials.json"

C_RESET='\033[0m'
C_RED='\033[0;31m'
C_GREEN='\033[0;32m'
C_YELLOW='\033[0;33m'
C_CYAN='\033[0;36m'
C_BLUE='\033[0;34m'
C_BOLD='\033[1m'

info() { echo -e "${C_CYAN}ℹ [INFO]${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔ [SUCCESS]${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠ [WARN]${C_RESET} $1"; }
error() { echo -e "${C_RED}✖ [ERROR]${C_RESET} $1"; }
header() {
  echo -e "\n${C_BOLD}${C_BLUE}===================================================================${C_RESET}"
  echo -e "${C_BOLD}${C_CYAN}  $1${C_RESET}"
  echo -e "${C_BOLD}${C_BLUE}===================================================================${C_RESET}\n"
}

if [ "$EUID" -ne 0 ]; then
  warn "This installer must run as root."
  echo -e "Use: ${C_BOLD}sudo ./install.sh${C_RESET}"
  exit 1
fi

mkdir -p "$CLOUDFLARED_DIR" "$SCRIPT_DIR/data"

# Guard against stale directory collisions that previously broke the installer.
if [ -d "$CLOUDFLARED_CONFIG" ]; then
  warn "Detected stale directory at ${CLOUDFLARED_CONFIG}. Moving it aside to preserve it."
  mv "$CLOUDFLARED_CONFIG" "${CLOUDFLARED_CONFIG}.stale-$(date +%s)"
fi
if [ -d "$CLOUDFLARED_CREDS" ]; then
  warn "Detected stale directory at ${CLOUDFLARED_CREDS}. Moving it aside to preserve it."
  mv "$CLOUDFLARED_CREDS" "${CLOUDFLARED_CREDS}.stale-$(date +%s)"
fi

# Load existing .env if present.
if [ -f "$ENV_FILE" ]; then
  set -a
  . "$ENV_FILE"
  set +a
fi

# Track previous values.
EXISTING_ENV=false
EXISTING_TUNNEL=false
EXISTING_CREDENTIALS=false

if [ -f "$ENV_FILE" ]; then
  EXISTING_ENV=true
fi
if [ -f "$CLOUDFLARED_CONFIG" ]; then
  EXISTING_TUNNEL=true
fi
if [ -f "$CLOUDFLARED_CREDS" ] && [ -s "$CLOUDFLARED_CREDS" ]; then
  EXISTING_CREDENTIALS=true
fi

if [ "$EXISTING_ENV" = true ] || [ "$EXISTING_TUNNEL" = true ] || [ "$EXISTING_CREDENTIALS" = true ]; then
  info "Existing Tunlify config detected. Reusing prior settings automatically."
fi

# Detect package manager and dependencies.
PKG_MANAGER=""
if command -v apt-get >/dev/null 2>&1; then
  PKG_MANAGER="apt"
elif command -v dnf >/dev/null 2>&1; then
  PKG_MANAGER="dnf"
elif command -v yum >/dev/null 2>&1; then
  PKG_MANAGER="yum"
elif command -v apk >/dev/null 2>&1; then
  PKG_MANAGER="apk"
elif command -v pacman >/dev/null 2>&1; then
  PKG_MANAGER="pacman"
fi

install_packages() {
  local pkgs=("$@")
  info "Installing packages: ${pkgs[*]}"
  case "$PKG_MANAGER" in
    apt)
      apt-get update -qq
      apt-get install -y -qq "${pkgs[@]}"
      ;;
    dnf|yum)
      "$PKG_MANAGER" install -y -q "${pkgs[@]}"
      ;;
    apk)
      apk add --no-cache "${pkgs[@]}"
      ;;
    pacman)
      pacman -Sy --noconfirm "${pkgs[@]}"
      ;;
    *)
      warn "Unknown package manager; install these manually: ${pkgs[*]}"
      ;;
  esac
}

NEEDED=()
for tool in curl jq openssl; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    NEEDED+=("$tool")
  fi
done
if ! command -v lspci >/dev/null 2>&1; then
  NEEDED+=("pciutils")
fi
if [ ${#NEEDED[@]} -gt 0 ]; then
  install_packages "${NEEDED[@]}"
fi

# Docker checks.
if ! command -v docker >/dev/null 2>&1; then
  error "Docker is required but not installed."
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  info "Starting Docker service..."
  systemctl start docker 2>/dev/null || service docker start 2>/dev/null || true
  if ! docker info >/dev/null 2>&1; then
    error "Docker daemon could not be started."
    exit 1
  fi
fi

if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD=(docker-compose)
else
  error "Docker Compose is not available."
  exit 1
fi

# Ensure cloudflared is available.
if ! command -v cloudflared >/dev/null 2>&1; then
  info "Installing cloudflared..."
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) CLOUD_ARCH="amd64" ;;
    aarch64|arm64) CLOUD_ARCH="arm64" ;;
    armv7l|armhf) CLOUD_ARCH="arm" ;;
    *) error "Unsupported architecture: $ARCH"; exit 1 ;;
  esac
  curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CLOUD_ARCH}" -o /usr/local/bin/cloudflared
  chmod +x /usr/local/bin/cloudflared
  success "cloudflared installed"
fi

# Preserve Cloudflare login state and tunnel info.
CF_CERT_PATH="${HOME}/.cloudflared/cert.pem"
if [ ! -f "$CF_CERT_PATH" ] && [ -f "/root/.cloudflared/cert.pem" ]; then
  CF_CERT_PATH="/root/.cloudflared/cert.pem"
fi

if [ -f "$CF_CERT_PATH" ]; then
  success "Cloudflare login cert found. Reusing existing Cloudflare session."
else
  info "Cloudflare login cert not found. Attempting automatic login..."
  cloudflared tunnel login || true
  if [ -f "${HOME}/.cloudflared/cert.pem" ] || [ -f "/root/.cloudflared/cert.pem" ]; then
    success "Cloudflare login completed."
  else
    warn "No Cloudflare cert found. Continuing with existing tunnel files if present."
  fi
fi

# Reuse previous env values if they exist, otherwise generate defaults.
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-ChangeMeNow123!}"
JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32)}"
TUNNEL_NAME="${TUNNEL_NAME:-tunnel-name}"
TUNNEL_ID="${TUNNEL_ID:-}"
TUNNEL_DOMAIN="${TUNNEL_DOMAIN:-docktun.example.com}"

# If previous credentials exist, load tunnel ID.
if [ -f "$CLOUDFLARED_CREDS" ] && [ -s "$CLOUDFLARED_CREDS" ] && [ -z "$TUNNEL_ID" ]; then
  TUNNEL_ID=$(jq -r '.TunnelID // empty' "$CLOUDFLARED_CREDS" 2>/dev/null || true)
fi

# Create tunnel if no tunnel ID is known.
if [ -z "$TUNNEL_ID" ]; then
  info "No existing tunnel ID detected. Creating a new tunnel..."
  TUNNEL_ID=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1 | grep -oE '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' | head -n1 || true)
fi

if [ -z "$TUNNEL_ID" ]; then
  error "Tunnel ID could not be determined. Existing tunnel data may be missing or invalid."
  exit 1
fi

# Ensure tunnel credentials are present in project folder.
if [ ! -f "$CLOUDFLARED_CREDS" ] || [ ! -s "$CLOUDFLARED_CREDS" ]; then
  for p in "${HOME}/.cloudflared/${TUNNEL_ID}.json" "/root/.cloudflared/${TUNNEL_ID}.json"; do
    if [ -f "$p" ]; then
      cp "$p" "$CLOUDFLARED_CREDS"
      success "Copied tunnel credentials to project folder"
      break
    fi
  done
fi

# Keep existing config if present; otherwise create one.
if [ ! -f "$CLOUDFLARED_CONFIG" ] || [ ! -s "$CLOUDFLARED_CONFIG" ]; then
  cat > "$CLOUDFLARED_CONFIG" <<EOF
tunnel: ${TUNNEL_ID}
credentials-file: /etc/cloudflared/credentials.json
protocol: http2

ingress:
  - hostname: ${TUNNEL_DOMAIN}
    service: http://tunlify-frontend:80
  - service: http_status:404
EOF
  chmod 644 "$CLOUDFLARED_CONFIG"
  success "Created cloudflared config"
else
  info "Existing cloudflared config retained."
fi

# Keep existing .env if present. Otherwise persist generated values.
if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<EOF
JWT_SECRET=${JWT_SECRET}
ADMIN_USERNAME=${ADMIN_USERNAME}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
PORT=8080
TUNNEL_NAME=${TUNNEL_NAME}
TUNNEL_ID=${TUNNEL_ID}
TUNNEL_PROTOCOL=http2
TUNNEL_DOMAIN=${TUNNEL_DOMAIN}
CLOUDFLARED_CONFIG_PATH=/etc/cloudflared/config.yml
CLOUDFLARED_CONTAINER=tunlify-cloudflared
DB_PATH=/app/data/app.db
LISTEN_ADDR=0.0.0.0:3001
SELF_PATH=/opt/projects/tunlify
PROJECTS_ROOT=./projects
EOF
  chmod 600 "$ENV_FILE"
  success "Saved generated environment values"
else
  info "Existing .env retained."
fi

# Start DockTun.
header "Launching Tunlify"
"${COMPOSE_CMD[@]}" up -d --build

success "Tunlify is running."
echo -e "Open: ${C_BOLD}http://localhost:8080${C_RESET}"
echo -e "Public URL: ${C_BOLD}https://${TUNNEL_DOMAIN}${C_RESET}"
echo -e "Admin user: ${C_BOLD}${ADMIN_USERNAME}${C_RESET}"
echo -e "Admin password: ${C_BOLD}${ADMIN_PASSWORD}${C_RESET}"
