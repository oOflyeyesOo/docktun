#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE="${SCRIPT_DIR}/.env.example"
CLOUDFLARED_DIR="${SCRIPT_DIR}/cloudflared"
DATA_DIR="${SCRIPT_DIR}/data"
BACKUP_ROOT="${SCRIPT_DIR}/.upgrade-backups"
BACKUP_DIR="${BACKUP_ROOT}/upgrade-$(date +%Y%m%d-%H%M%S)"

C_RESET='\033[0m'
C_RED='\033[31m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_CYAN='\033[36m'

info() { echo -e "${C_CYAN}ℹ [INFO]${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔ [SUCCESS]${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠ [WARN]${C_RESET} $1"; }
error() { echo -e "${C_RED}✖ [ERROR]${C_RESET} $1"; }

ensure_docker_compose() {
  if docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD=(docker compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE_CMD=(docker-compose)
  else
    error "Docker Compose is not available."
    exit 1
  fi
}

backup_file() {
  local src="$1"
  local name="$(basename "$src")"
  if [ -e "$src" ]; then
    mkdir -p "$BACKUP_DIR"
    cp -a "$src" "$BACKUP_DIR/$name"
    info "Backed up $src"
  fi
}

if [ ! -d "$SCRIPT_DIR" ]; then
  error "Script directory not found: $SCRIPT_DIR"
  exit 1
fi

# Check for git updates if deployed from git repository
if [ -d "$SCRIPT_DIR/.git" ] && command -v git >/dev/null 2>&1; then
  info "Checking for latest DockTun updates from git..."
  git -C "$SCRIPT_DIR" pull --ff-only 2>/dev/null || warn "git pull skipped (local changes detected or no upstream set). Continuing with local files."
fi

info "Preparing upgrade preserving tunnel and auth settings..."
mkdir -p "$BACKUP_ROOT" "$CLOUDFLARED_DIR" "$DATA_DIR"

if [ ! -f "$ENV_FILE" ] && [ -f "$ENV_EXAMPLE" ]; then
  cp "$ENV_EXAMPLE" "$ENV_FILE"
  warn "Created .env from .env.example before upgrade."
fi

# Preserve critical files before the update.
backup_file "$ENV_FILE"
backup_file "$CLOUDFLARED_DIR/config.yml"
backup_file "$CLOUDFLARED_DIR/credentials.json"
backup_file "$CLOUDFLARED_DIR/token"
backup_file "$SCRIPT_DIR/data/app.db"
backup_file "$SCRIPT_DIR/docker-compose.override.yml"

if [ -f "$ENV_FILE" ]; then
  TUNNEL_NAME="$(grep -E '^TUNNEL_NAME=' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2- || true)"
  ADMIN_USERNAME="$(grep -E '^ADMIN_USERNAME=' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2- || true)"
  ADMIN_PASSWORD="$(grep -E '^ADMIN_PASSWORD=' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2- || true)"
  JWT_SECRET="$(grep -E '^JWT_SECRET=' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2- || true)"

  if [ -n "$TUNNEL_NAME" ]; then
    info "Preserving tunnel name: ${TUNNEL_NAME}"
  fi
  if [ -n "$ADMIN_USERNAME" ]; then
    info "Preserving admin user: ${ADMIN_USERNAME}"
  fi
fi

ensure_docker_compose

# Migrate stale 'backend:' service name to unified 'docktun:' in docker-compose.override.yml if present
if [ -f "$SCRIPT_DIR/docker-compose.override.yml" ]; then
  sed -i -E 's/^[[:space:]]*backend:[[:space:]]*$/  docktun:/g' "$SCRIPT_DIR/docker-compose.override.yml" 2>/dev/null || true
fi

info "Pulling latest cloudflared base image..."
"${COMPOSE_CMD[@]}" pull cloudflared || true

info "Building and launching updated DockTun stack..."
"${COMPOSE_CMD[@]}" up -d --build --remove-orphans

# Clean up internal containers without Cloudflare ingress from 'cloudflared' network
if docker network inspect cloudflared >/dev/null 2>&1; then
  info "Pruning internal containers from 'cloudflared' network (keeping only DockTun & routed services)..."
  CF_ATTACHED=$(docker network inspect cloudflared -f '{{range $k, $v := .Containers}}{{$v.Name}} {{end}}' 2>/dev/null || true)
  for c in $CF_ATTACHED; do
    if [[ ! "$c" =~ ^(docktun|docktun-backend|docktun-frontend|docktun-cloudflared|docktun-zeroconf|docktun-tailscale|docktun-wireguard)$ ]]; then
      if [ -f "$CLOUDFLARED_DIR/config.yml" ] && ! grep -q "$c" "$CLOUDFLARED_DIR/config.yml" 2>/dev/null; then
        info "Disconnected internal container '$c' from 'cloudflared' network"
        docker network disconnect cloudflared "$c" 2>/dev/null || true
      fi
    fi
  done
fi

if [ -f "$ENV_FILE" ]; then
  success "Upgrade completed. Existing tunnel/auth settings were preserved in $ENV_FILE."
else
  warn "Upgrade completed, but .env was missing and had to be recreated from .env.example."
fi

info "Backup directory: $BACKUP_DIR"
info "If anything looks wrong, restore the backup files from that directory."
