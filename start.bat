@echo off
setlocal
echo ========================================================
echo   Starting Tunlify (Cloudflare Tunnel & Compose Web UI)
echo ========================================================

where docker >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Docker is not found in PATH. Please install or start Docker Desktop.
    pause
    exit /b 1
)

echo.
echo Launching containers with Docker Compose...
docker compose up -d --build

if %ERRORLEVEL% equ 0 (
    echo.
    echo ========================================================
    echo   Tunlify is running!
    echo   URL:      http://localhost:8080
    echo   Username: admin
    echo   Password: adminpassword123
    echo ========================================================
) else (
    echo.
    echo [ERROR] Failed to start Tunlify containers. Make sure Docker Desktop is running.
)

pause
