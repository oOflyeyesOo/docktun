@echo off
setlocal
echo ========================================================
echo   Updating DockTun (Rebuilding Containers)
echo ========================================================

where docker >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Docker is not found in PATH. Please install or start Docker Desktop.
    pause
    exit /b 1
)

where git >nul 2>&1
if %ERRORLEVEL% equ 0 (
    if exist ".git" (
        echo Pulling latest code changes from git...
        git pull
    )
)

echo.
echo Rebuilding and restarting DockTun containers...
docker compose up -d --build --remove-orphans

if %ERRORLEVEL% equ 0 (
    echo.
    echo ========================================================
    echo   DockTun has been successfully updated!
    echo   URL: http://localhost:8080 (or your configured port)
    echo ========================================================
) else (
    echo.
    echo [ERROR] Failed to update DockTun containers. Make sure Docker is running.
)

pause
