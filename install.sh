#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE="${SCRIPT_DIR}/.env.example"
CLOUDFLARED_DIR="${SCRIPT_DIR}/cloudflared"

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
  warn "This install script must run as root."
  echo -e "Run: ${C_BOLD}sudo ./install.sh${C_RESET}"
  exit 1
fi

header "Tunlify Installer"

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
      warn "Unknown package manager. Please install: ${pkgs[*]} manually."
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

if ! command -v docker >/dev/null 2>&1; then
  warn "Docker is not installed."
  read -rp "Would you like to install Docker automatically? [Y/n]: " INSTALL_DOCKER
  INSTALL_DOCKER=${INSTALL_DOCKER:-Y}
  if [[ "$INSTALL_DOCKER" =~ ^[Yy]$ ]]; then
    info "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
  else
    error "Docker is required. Exiting."
    exit 1
  fi
fi

if ! docker info >/dev/null 2>&1; then
  info "Starting Docker service..."
  systemctl start docker 2>/dev/null || service docker start 2>/dev/null || true
  if ! docker info >/dev/null 2>&1; then
    error "Docker daemon is not running. Please start Docker."
    exit 1
  fi
fi

if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD="docker-compose"
else
  info "Installing Docker Compose plugin..."
  if [ "$PKG_MANAGER" = "apt" ]; then
    apt-get install -y -qq docker-compose-plugin >/dev/null 2>&1 || true
  fi
  if docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
  else
    error "Docker Compose is not available. Exiting."
    exit 1
  fi
fi

success "Docker and Compose verified: $($COMPOSE_CMD version)"

header "Preflight Checks"

VIRT_TYPE="bare-metal"
if command -v systemd-detect-virt >/dev/null 2>&1; then
  VIRT_TYPE=$(systemd-detect-virt 2>/dev/null || echo "none")
elif [ -f /sys/class/dmi/id/product_name ]; then
  VIRT_TYPE=$(cat /sys/class/dmi/id/product_name 2>/dev/null || echo "unknown")
fi
info "Environment: ${VIRT_TYPE}"

if [[ "$VIRT_TYPE" =~ lxc|lxd ]]; then
  info "Detected LXC/LXD environment."
  [ -w /sys/fs/cgroup ] && success "cgroup writable"
  modprobe overlay >/dev/null 2>&1 || true
  if grep -q overlay /proc/filesystems 2>/dev/null; then
    success "OverlayFS available"
  fi
fi

IP_FWD=$(sysctl -n net.ipv4.ip_forward 2>/dev/null || echo "0")
if [ "$IP_FWD" != "1" ]; then
  warn "IPv4 forwarding is disabled. Enabling it..."
  sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
  mkdir -p /etc/sysctl.d
  echo "net.ipv4.ip_forward = 1" > /etc/sysctl.d/99-docker-ip-forward.conf
  success "IPv4 forwarding enabled"
else
  success "IPv4 forwarding is enabled"
fi

if [ -c /dev/net/tun ]; then
  success "/dev/net/tun exists"
else
  info "Creating /dev/net/tun"
  mkdir -p /dev/net
  mknod /dev/net/tun c 10 200 2>/dev/null || true
  chmod 666 /dev/net/tun 2>/dev/null || true
  if [ -c /dev/net/tun ]; then
    success "/dev/net/tun created"
  else
    warn "/dev/net/tun could not be created. VPN features may not work."
  fi
fi

if lsmod 2>/dev/null | grep -q br_netfilter; then
  success "br_netfilter loaded"
else
  modprobe br_netfilter >/dev/null 2>&1 || true
  if lsmod 2>/dev/null | grep -q br_netfilter; then
    success "br_netfilter loaded"
  else
    warn "br_netfilter not available; Docker bridge filtering may be limited"
  fi
fi

modprobe overlay >/dev/null 2>&1 || true
if grep -q overlay /proc/filesystems 2>/dev/null; then
  success "OverlayFS kernel support detected"
fi

if command -v timedatectl >/dev/null 2>&1; then
  if timedatectl status 2>/dev/null | grep -qiE 'synchronized: yes|NTP service: active'; then
    success "System time synchronization looks good"
  else
    warn "System clock might not be synchronized. Cloudflare auth may fail."
  fi
fi

header "Cloudflared Setup"

if command -v cloudflared >/dev/null 2>&1; then
  success "cloudflared already installed: $(cloudflared --version 2>&1 | head -n1)"
else
  info "Installing cloudflared..."
  SYS_ARCH=$(uname -m)
  case "$SYS_ARCH" in
    x86_64) CF_ARCH="amd64" ;;
    aarch64|arm64) CF_ARCH="arm64" ;;
    armv7l|armhf) CF_ARCH="arm" ;;
    i386|i686) CF_ARCH="386" ;;
    *) error "Unsupported architecture: $SYS_ARCH"; exit 1 ;;
  esac
  curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CF_ARCH}" -o /usr/local/bin/cloudflared
  chmod +x /usr/local/bin/cloudflared
  success "cloudflared installed"
fi

mkdir -p "$SCRIPT_DIR/cloudflared" "$SCRIPT_DIR/data" "$SCRIPT_DIR/projects"

EXISTING_ENV=false
EXISTING_TUNNEL=false
EXISTING_CREDS=false

if [ -f "$SCRIPT_DIR/.env" ]; then
  EXISTING_ENV=true
fi

if [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && [ -s "$SCRIPT_DIR/cloudflared/credentials.json" ]; then
  EXISTING_CREDS=true
fi

if [ -f "$SCRIPT_DIR/cloudflared/config.yml" ]; then
  EXISTING_TUNNEL=true
fi

if [ -d "$HOME/.cloudflared" ] || [ -d "/root/.cloudflared" ]; then
  find "$HOME/.cloudflared" /root/.cloudflared 2>/dev/null -name "*.json" | grep -q . && EXISTING_TUNNEL=true || true
fi

if [ "$EXISTING_ENV" = true ] || [ "$EXISTING_CREDS" = true ] || [ "$EXISTING_TUNNEL" = true ]; then
  warn "Existing Tunlify configuration detected."
  info "A previous tunnel and login config may already exist."
fi

header "Authentication Setup"

DEFAULT_USERNAME="${ADMIN_USERNAME:-admin}"
read -rp "Admin username [default: ${DEFAULT_USERNAME}]: " ADMIN_USERNAME
ADMIN_USERNAME=${ADMIN_USERNAME:-$DEFAULT_USERNAME}

while true; do
  echo -n "Enter Tunlify admin password: "
  read -rs ADMIN_PASSWORD
  echo
  echo -n "Confirm Tunlify admin password: "
  read -rs ADMIN_PASSWORD_CONFIRM
  echo
  if [ -z "$ADMIN_PASSWORD" ]; then
    warn "Password cannot be empty."
  elif [ "$ADMIN_PASSWORD" != "$ADMIN_PASSWORD_CONFIRM" ]; then
    warn "Passwords do not match."
  else
    break
  fi
done

JWT_SECRET=$(openssl rand -hex 32)
info "Generated JWT secret automatically"

header "Cloudflare Tunnel Setup"

CF_CERT_PATH="$HOME/.cloudflared/cert.pem"
if [ ! -f "$CF_CERT_PATH" ] && [ -f "/root/.cloudflared/cert.pem" ]; then
  CF_CERT_PATH="/root/.cloudflared/cert.pem"
fi

if [ -f "$CF_CERT_PATH" ]; then
  success "Cloudflare login session is already active"
  LOGIN_OK=true
else
  info "Launching Cloudflare login flow..."
  cloudflared tunnel login
  if [ -f "$CF_CERT_PATH" ]; then
    success "Cloudflare login successful"
    LOGIN_OK=true
  else
    warn "Cloudflare login did not complete yet. You may need to do it manually."
    LOGIN_OK=false
  fi
fi

read -rp "Tunnel name [default: tunlify-tunnel]: " TUNNEL_NAME
TUNNEL_NAME=${TUNNEL_NAME:-tunlify-tunnel}

if [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && [ -s "$SCRIPT_DIR/cloudflared/credentials.json" ]; then
  info "Using existing credentials.json found in project folder"
  TUNNEL_ID=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
else
  info "Creating Cloudflare tunnel: $TUNNEL_NAME"
  TUNNEL_CREATE_OUTPUT=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1 || true)
  echo "$TUNNEL_CREATE_OUTPUT"
  TUNNEL_ID=$(echo "$TUNNEL_CREATE_OUTPUT" | grep -oE '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' | head -n1 || true)
  if [ -n "$TUNNEL_ID" ]; then
    for p in "$HOME/.cloudflared/$TUNNEL_ID.json" "/root/.cloudflared/$TUNNEL_ID.json"; do
      if [ -f "$p" ]; then
        cp "$p" "$SCRIPT_DIR/cloudflared/credentials.json"
        success "Copied tunnel credentials to project folder"
        break
      fi
    done
  fi
fi

if [ -z "${TUNNEL_ID:-}" ]; then
  TUNNEL_ID=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
fi

if [ -z "${TUNNEL_ID:-}" ] || [ "$TUNNEL_ID" = "null" ]; then
  error "Tunnel ID is missing. Please login to Cloudflare and create a tunnel manually."
  exit 1
fi

read -rp "Public hostname for Tunlify UI [default: tunlify.example.com]: " TUNLIFY_DOMAIN
TUNLIFY_DOMAIN=${TUNLIFY_DOMAIN:-tunlify.example.com}

header "Writing Config"

cat > "$SCRIPT_DIR/.env" <<EOF
# Tunlify configuration
JWT_SECRET=${JWT_SECRET}
ADMIN_USERNAME=${ADMIN_USERNAME}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
PORT=8080
TUNNEL_NAME=${TUNNEL_NAME}
TUNNEL_ID=${TUNNEL_ID}
TUNNEL_PROTOCOL=http2
CLOUDFLARED_CONFIG_PATH=/etc/cloudflared/config.yml
CLOUDFLARED_CONTAINER=tunlify-cloudflared
DB_PATH=/app/data/app.db
LISTEN_ADDR=0.0.0.0:3001
SELF_PATH=/opt/projects/tunlify
PROJECTS_ROOT=./projects
EOF

chmod 600 "$SCRIPT_DIR/.env"

cat > "$SCRIPT_DIR/cloudflared/config.yml" <<EOF
tunnel: ${TUNNEL_ID}
credentials-file: /etc/cloudflared/credentials.json
protocol: http2

ingress:
  - hostname: ${TUNLIFY_DOMAIN}
    service: http://tunlify-frontend:80
  - service: http_status:404
EOF

chmod 644 "$SCRIPT_DIR/cloudflared/config.yml"
chmod 644 "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true

header "Launching Tunlify"

$COMPOSE_CMD up -d --build

success "Tunlify should now be running."
echo -e "Open: ${C_BOLD}http://localhost:8080${C_RESET}"
echo -e "Public URL: ${C_BOLD}https://${TUNLIFY_DOMAIN}${C_RESET}"
echo -e "Admin user: ${C_BOLD}${ADMIN_USERNAME}${C_RESET}"
echo -e "Admin password: ${C_BOLD}${ADMIN_PASSWORD}${C_RESET}"
