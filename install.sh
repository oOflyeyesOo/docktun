#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE="${SCRIPT_DIR}/.env.example"
CLOUDFLARED_DIR="${SCRIPT_DIR}/cloudflared"

C_RESET='\033[0m'
C_RED='\033[31m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_CYAN='\033[36m'

info() { echo -e "${C_CYAN}ℹ [INFO]${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔ [SUCCESS]${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠ [WARN]${C_RESET} $1"; }
error() { echo -e "${C_RED}✖ [ERROR]${C_RESET} $1"; }

if ! command -v docker >/dev/null 2>&1; then
  error "Docker is not installed or not in PATH."
  exit 1
fi

if [ -f "${ENV_FILE}" ] || [ -f "${CLOUDFLARED_DIR}/config.yml" ] || [ -f "${CLOUDFLARED_DIR}/credentials.json" ]; then
  info "Existing install detected. Running upgrade flow with config preservation..."
  exec "${SCRIPT_DIR}/upgrade.sh"
fi

if [ ! -f "${ENV_FILE}" ] && [ -f "${ENV_EXAMPLE}" ]; then
  info "Creating .env from example..."
  cp "${ENV_EXAMPLE}" "${ENV_FILE}"
fi

info "Starting DockTun for the first time..."
docker compose up -d --build

success "DockTun is running!"
info "Open: http://localhost:8080"
info "Default login: admin / change this in .env"
