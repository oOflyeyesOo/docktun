Write-Host "========================================================" -ForegroundColor Cyan
Write-Host "  Starting Tunlify (Cloudflare Tunnel & Compose Web UI)" -ForegroundColor Cyan
Write-Host "========================================================" -ForegroundColor Cyan

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "[ERROR] Docker is not installed or not in PATH." -ForegroundColor Red
    exit 1
}

Write-Host "`nLaunching containers with Docker Compose..." -ForegroundColor Yellow
docker compose up -d --build

if ($LASTEXITCODE -eq 0) {
    Write-Host "`n========================================================" -ForegroundColor Green
    Write-Host "  Tunlify is running!" -ForegroundColor Green
    Write-Host "  URL:      http://localhost:8080" -ForegroundColor Green
    Write-Host "  Username: admin" -ForegroundColor Green
    Write-Host "  Password: adminpassword123" -ForegroundColor Green
    Write-Host "========================================================" -ForegroundColor Green
} else {
    Write-Host "`n[ERROR] Failed to start containers. Make sure Docker Desktop is running." -ForegroundColor Red
}
