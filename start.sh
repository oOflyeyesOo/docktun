#!/usr/bin/env bash
set -e

echo "========================================================"
echo "  Starting DockTun (Cloudflare Tunnel & Docker Manager)"
echo "========================================================"

if ! command -v docker &> /dev/null; then
    echo "[ERROR] Docker is not installed or not in PATH."
    exit 1
fi

echo ""
echo "Launching containers with Docker Compose..."
docker compose up -d --build

echo ""
echo "========================================================"
echo "  DockTun is running!"
echo "  URL:      http://localhost:8080"
echo "  Username: admin"
echo "  Password: [Configured in .env]"
echo "========================================================"
