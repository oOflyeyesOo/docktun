Write-Host "========================================================" -ForegroundColor Cyan
Write-Host "  Updating DockTun (Rebuilding Containers)" -ForegroundColor Cyan
Write-Host "========================================================" -ForegroundColor Cyan

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "[ERROR] Docker is not installed or not in PATH." -ForegroundColor Red
    exit 1
}

# Optional git pull if in a git repository
if (Test-Path ".git") {
    if (Get-Command git -ErrorAction SilentlyContinue) {
        Write-Host "Pulling latest code changes from git..." -ForegroundColor Cyan
        git pull
    }
}

Write-Host "`nRebuilding and restarting DockTun containers..." -ForegroundColor Yellow
docker compose up -d --build --remove-orphans

if ($LASTEXITCODE -eq 0) {
    Write-Host "`n========================================================" -ForegroundColor Green
    Write-Host "  ✔ DockTun has been successfully updated!" -ForegroundColor Green
    Write-Host "  URL: http://localhost:8080 (or your configured port)" -ForegroundColor Green
    Write-Host "========================================================" -ForegroundColor Green
} else {
    Write-Host "`n[ERROR] Update failed. Ensure Docker Desktop is running." -ForegroundColor Red
}
