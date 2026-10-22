#!/usr/bin/env bash
# ==============================================================================
# DockTun / Tunlify Clean Uninstaller
# ==============================================================================
# Wipes containers, local SQLite database, .env credentials, and cached images
# for a fresh reinstall, while strictly preserving your Cloudflare Tunnel
# configuration and credentials.
# ==============================================================================

set -eo pipefail

C_RESET='\033[0m'
C_BOLD='\033[1m'
C_RED='\033[0;31m'
C_GREEN='\033[0;32m'
C_YELLOW='\033[0;33m'
C_BLUE='\033[0;34m'
C_CYAN='\033[0;36m'

info() { echo -e "${C_CYAN}ℹ [INFO]${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔ [SUCCESS]${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠ [WARN]${C_RESET} $1"; }
error() { echo -e "${C_RED}✖ [ERROR]${C_RESET} $1"; }

header() {
    echo -e "\n${C_BOLD}${C_BLUE}===================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  $1${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}===================================================================${C_RESET}\n"
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 1. Superuser Check
if [ "$EUID" -ne 0 ]; then
    warn "This script requires superuser privileges to manage Docker containers and system volumes."
    echo -e "Please re-run as root: ${C_BOLD}sudo ./uninstall.sh${C_RESET}"
    exit 1
fi

clear 2>/dev/null || true
echo -e "${C_CYAN}"
cat << "EOF"
  ____             _     _____             
 |  _ \  ___   ___| | __|_   _|   _ _ __   
 | | | |/ _ \ / __| |/ /  | | | | | '_ \  
 | |_| | (_) | (__|   <   | | |_| | | | | 
 |____/ \___/ \___|_|\_\  |_| \__,_|_| |_| 
                                           
        Clean Uninstaller & Reset
EOF
echo -e "${C_RESET}"

echo -e "${C_BOLD}This script will reset DockTun for a completely fresh installation:${C_RESET}"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  DockTun containers (backend, frontend, zeroconf, cloudflared)"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  Docker compose volumes and networks"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  Locally built Docker images"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  SQLite database in ./data/ (user accounts, container metadata, audit logs)"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  Local .env file (allows setting fresh admin credentials & secrets)"
echo -e "  ${C_RED}✖ Remove:${C_RESET}  Cloned project repositories in ./projects/"
echo ""
echo -e "  ${C_GREEN}✔ KEEP:${C_RESET}    ${C_BOLD}Cloudflare Tunnel configuration & credentials in ./cloudflared/${C_RESET}"
echo -e "             (config.yml, credentials.json, cert.pem)"
echo -e "  ${C_GREEN}✔ KEEP:${C_RESET}    DockTun source code, install script, and git repository"
echo ""

# Check for non-interactive flag (-y or --yes)
AUTO_CONFIRM=false
for arg in "$@"; do
    case "$arg" in
        -y|--yes|-f|--force)
            AUTO_CONFIRM=true
            ;;
    esac
done

if [ "$AUTO_CONFIRM" = false ]; then
    read -rp "Are you sure you want to proceed with uninstallation? [y/N]: " CONFIRM_INPUT
    if [[ ! "$CONFIRM_INPUT" =~ ^[Yy]$ ]]; then
        info "Uninstallation cancelled by user. Nothing was changed."
        exit 0
    fi
fi

header "Step 1: Safeguarding Cloudflare Configuration"

BACKUP_TS=$(date +%Y%m%d_%H%M%S)
TMP_BACKUP="/tmp/docktun_cloudflared_backup_${BACKUP_TS}"
LOCAL_BACKUP="$SCRIPT_DIR/cloudflared.backup_${BACKUP_TS}"

if [ -d "$SCRIPT_DIR/cloudflared" ]; then
    mkdir -p "$TMP_BACKUP"
    mkdir -p "$LOCAL_BACKUP"
    cp -a "$SCRIPT_DIR/cloudflared/." "$TMP_BACKUP/" 2>/dev/null || true
    cp -a "$SCRIPT_DIR/cloudflared/." "$LOCAL_BACKUP/" 2>/dev/null || true
    success "Cloudflare configuration safely backed up to:"
    echo -e "    • ${C_CYAN}$TMP_BACKUP${C_RESET}"
    echo -e "    • ${C_CYAN}$LOCAL_BACKUP${C_RESET}"
else
    warn "No local ./cloudflared directory found. Creating directory for new setup."
    mkdir -p "$SCRIPT_DIR/cloudflared"
fi

header "Step 2: Stopping & Removing Docker Containers"

# 1. Detect Docker Compose
if docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE_CMD=(docker-compose)
else
    COMPOSE_CMD=()
fi

if [ ${#COMPOSE_CMD[@]} -gt 0 ] && [ -f "$SCRIPT_DIR/docker-compose.yml" ]; then
    info "Stopping compose stack and removing volumes..."
    "${COMPOSE_CMD[@]}" down -v --remove-orphans 2>/dev/null || true
fi

# 2. Force remove any lingering DockTun / Tunlify containers
TARGET_CONTAINERS=(
    "docktun"
    "docktun-backend"
    "docktun-frontend"
    "docktun-zeroconf"
    "docktun-cloudflared"
    "tunlify-backend"
    "tunlify-frontend"
    "tunlify-zeroconf"
    "tunlify-cloudflared"
)

for cname in "${TARGET_CONTAINERS[@]}"; do
    if docker ps -a --format '{{.Names}}' 2>/dev/null | grep -qx "$cname"; then
        info "Stopping and removing container: $cname"
        docker stop "$cname" >/dev/null 2>&1 || true
        docker rm -f "$cname" >/dev/null 2>&1 || true
    fi
done

# 3. Disconnect external containers from cloudflared network if network exists
if docker network inspect cloudflared >/dev/null 2>&1; then
    attached_containers=$(docker network inspect cloudflared --format '{{range $k, $v := .Containers}}{{$v.Name}} {{end}}' 2>/dev/null || true)
    for ac in $attached_containers; do
        if [ -n "$ac" ]; then
            info "Disconnecting container $ac from cloudflared network..."
            docker network disconnect -f cloudflared "$ac" 2>/dev/null || true
        fi
    done
    info "Removing cloudflared Docker network..."
    docker network rm cloudflared 2>/dev/null || true
fi

success "All containers and networks stopped and cleared."

header "Step 3: Removing Locally Built Images"

IMAGES_TO_REMOVE=(
    "docktun:latest"
    "docktun"
    "docktun-backend:latest"
    "docktun-frontend:latest"
    "docktun-zeroconf:latest"
    "docktun-backend"
    "docktun-frontend"
    "docktun-zeroconf"
    "tunlify-backend:latest"
    "tunlify-frontend:latest"
    "tunlify-zeroconf:latest"
    "tunlify-backend"
    "tunlify-frontend"
    "tunlify-zeroconf"
)

for img in "${IMAGES_TO_REMOVE[@]}"; do
    if docker image inspect "$img" >/dev/null 2>&1; then
        info "Removing image: $img"
        docker rmi -f "$img" >/dev/null 2>&1 || true
    fi
done
success "DockTun Docker images removed."

header "Step 4: Wiping Local Data & State"

# Remove SQLite database directory
if [ -d "$SCRIPT_DIR/data" ]; then
    rm -rf "$SCRIPT_DIR/data"
    success "Removed ./data/ (SQLite database & audit trail wiped)."
fi

# Remove projects directory
if [ -d "$SCRIPT_DIR/projects" ]; then
    rm -rf "$SCRIPT_DIR/projects"
    success "Removed ./projects/ (cloned compose projects cleared)."
fi

# Remove environment file to allow fresh admin/secret setup
if [ -f "$SCRIPT_DIR/.env" ]; then
    rm -f "$SCRIPT_DIR/.env"
    success "Removed ./.env (admin credentials and JWT secrets cleared)."
fi

# Clean up stale upgrade snapshots and build caches
rm -rf "$SCRIPT_DIR/.upgrade-backups" 2>/dev/null || true
rm -rf "$SCRIPT_DIR/backend/tmp" 2>/dev/null || true
rm -rf "$SCRIPT_DIR/backend/server" 2>/dev/null || true
rm -rf "$SCRIPT_DIR/frontend/dist" 2>/dev/null || true
rm -f "$SCRIPT_DIR/docker-compose.override.yml" 2>/dev/null || true

header "Step 5: Verifying Preserved Cloudflare Configuration"

mkdir -p "$SCRIPT_DIR/cloudflared"

# Ensure files were preserved; if missing, restore from backup immediately
if [ ! -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && [ -f "$TMP_BACKUP/credentials.json" ]; then
    cp -a "$TMP_BACKUP/credentials.json" "$SCRIPT_DIR/cloudflared/credentials.json"
fi

if [ ! -f "$SCRIPT_DIR/cloudflared/config.yml" ] && [ -f "$TMP_BACKUP/config.yml" ]; then
    cp -a "$TMP_BACKUP/config.yml" "$SCRIPT_DIR/cloudflared/config.yml"
fi

# Verify permissions
chmod 644 "$SCRIPT_DIR/cloudflared"/* 2>/dev/null || true

if [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] || [ -f "$SCRIPT_DIR/cloudflared/config.yml" ]; then
    success "Cloudflare configuration verified and intact:"
    [ -f "$SCRIPT_DIR/cloudflared/config.yml" ] && echo -e "  • ${C_GREEN}Config file:${C_RESET}       $SCRIPT_DIR/cloudflared/config.yml"
    [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && echo -e "  • ${C_GREEN}Credentials file:${C_RESET}  $SCRIPT_DIR/cloudflared/credentials.json"
else
    info "No prior Cloudflare files found. Ready for fresh tunnel creation on next install."
fi

header "Uninstallation Complete!"

echo -e "${C_GREEN}${C_BOLD}✔ DockTun has been successfully uninstalled.${C_RESET}\n"
echo -e "Your ${C_BOLD}Cloudflare Tunnel settings${C_RESET} remain ready in: ${C_CYAN}$SCRIPT_DIR/cloudflared/${C_RESET}"
echo -e "To perform a completely fresh installation now, run:\n"
echo -e "    ${C_BOLD}${C_CYAN}sudo ./install.sh${C_RESET}\n"
echo -e "When prompted with ${C_BOLD}\"Found configuration from previous installation attempt: Would you like to resume?\"${C_RESET},"
echo -e "simply press ${C_BOLD}Enter (Y)${C_RESET} to reuse your existing Cloudflare Tunnel, then enter your fresh admin password."
echo ""
