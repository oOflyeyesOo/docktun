#!/usr/bin/env bash
set -e

echo "========================================================"
echo "  Starting Tunlify (Cloudflare Tunnel & Compose Web UI)"
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
echo "  Tunlify is running!"
echo "  URL:      http://localhost:8080"
echo "  Username: admin"
echo "  Password: adminpassword123"
echo "========================================================"
