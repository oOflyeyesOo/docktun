# ☁️ DockTun

Self-hosted web dashboard to manage and monitor Cloudflare Tunnel (cloudflared) and Docker Compose projects.

![Go](https://img.shields.io/badge/backend-Go-00ADD8?logo=go)
![Vue](https://img.shields.io/badge/frontend-Vue_3-4FC08D?logo=vue.js)
![Docker](https://img.shields.io/badge/deploy-Docker-2496ED?logo=docker)
![License](https://img.shields.io/badge/license-MIT-green)

## Features

- **Dashboard** — tunnel status, uptime, active hostnames, container PID
- **Control** — start, stop, restart, reload config via Docker API
- **Logs** — realtime streaming via WebSocket with filter & search
- **Config Editor** — edit `config.yml` with YAML validation and auto-backup
- **Health Check** — monitor ingress endpoint availability
- **Metrics** — cloudflared metrics visualization
- **TCP Access** — generate `cloudflared access tcp` commands
- **Notifications** — webhook alerts (Discord, Telegram, Slack)
- **Audit Log** — full history of all actions
- **Auth** — JWT-based login with session management
- **Projects** — manage Docker Compose projects (up, down, restart, deploy with git checkout), streaming build output, per-project action history

## Tech Stack

| Layer | Stack |
|-------|-------|
| Backend | Go, Chi, gorilla/websocket, SQLite |
| Frontend | Vue 3, Vite, Tailwind CSS |
| Deploy | Docker Compose |

## Quick Start

### Option 1: Prebuilt image — no source checkout or build (recommended)

**For Synology, NAS, and Linux hosts with Docker Compose**, download just [`compose.prebuilt.yml`](compose.prebuilt.yml), save it as `compose.yml` in an empty folder, then run:

```sh
docker compose up -d
docker compose ps
```

DockTun pulls `ghcr.io/ooflyeyesoo/docktun:latest` instead of compiling the frontend and backend. GitHub Actions publishes multi-platform `amd64` and `arm64` images after successful `main` validation, with commit-specific `sha-...` tags available for pinning. The **GHCR package must be made public separately** before other people can pull anonymously; the GitHub repository itself can remain private.

Open `http://<server-ip>:8080`. If the setup wizard requests an ownership code, read it locally from the DockTun container:

```sh
docker exec docktun cat /app/data/setup.token
```

The setup token is sensitive: do not share it or publish it in logs or screenshots.

The Compose file creates persistent volumes for the application data and tunnel configuration. To enable the optional Cloudflare companion service **after configuring its tunnel credentials and ingress config**, run:

```sh
docker compose --profile cloudflare up -d
```

DockTun needs the Docker socket to manage containers, granting the app extensive host privileges. Keep the dashboard on a trusted network or behind authenticated remote access; **do not forward port 8080 directly to the public internet.** For Synology, import the single `compose.prebuilt.yml` file into Container Manager, rather than uploading a Dockerfile.

### Option 2: Build from source (full repository required)

Start DockTun with zero initial configuration. The interactive **First-Run Setup Wizard** will guide you through setting up admin credentials, activating tunnels, and importing your existing containers:

```bash
git clone https://github.com/oOflyeyesOo/docktun.git /opt/docktun
cd /opt/docktun

# Start containers (creates network and persistent data directory automatically)
docker network create cloudflared 2>/dev/null || true
docker compose up -d

# Docker Compose must be run from the full repository checkout.
# If the build says "COPY frontend/package.json ... not found",
# verify you have backend/, frontend/, Dockerfile, and docker-compose.yml
# together under /opt/docktun (not just the Dockerfile).

# Open in your browser to launch the Onboarding Wizard:
# http://localhost:8080 or http://<your-server-ip>:8080
```

### Source build troubleshooting for Synology and older Docker Compose installations

**Use the complete repository source tree as the Docker build context.** The Dockerfile is not standalone. In particular, `frontend/package.json`, `frontend/pnpm-lock.yaml`, `frontend/pnpm-workspace.yaml`, `backend/go.mod`, and `backend/go.sum` must all exist beneath the directory that contains `Dockerfile` and `docker-compose.yml`. A build log that says "Sending build context to Docker daemon 1.865kB" indicates the source files were not included. Upload/extract or clone the **entire** repo into the Synology Container Manager project folder, and select that folder as the build context.

For a shell-based installation:

```sh
git clone https://github.com/oOflyeyesOo/docktun.git
cd docktun
test -f frontend/package.json && test -f frontend/pnpm-lock.yaml && test -f frontend/pnpm-workspace.yaml
test -f backend/go.mod && test -f Dockerfile && test -f docker-compose.yml
docker compose config
docker compose up -d --build
```

If the repository is private, cloning requires authorized GitHub access. For DSM Container Manager's graphical project installer, copy the **entire** source tree to the project directory before starting the build. Do not copy only the Compose file or Dockerfile.

The Compose file avoids newer `env_file: [{path, required}]` syntax. A local `.env` file is **optional** for first-run setup; Compose will read it automatically for variable substitution when present. You can copy `.env.example` to `.env` to customize settings without supplying an initial administrator password. Configure credentials using the first-run onboarding flow.

If `docker compose` is unavailable or the project still fails validation, check `docker compose version` (or `docker-compose version`). Upgrade Synology Container Manager / the Compose plugin where possible: obsolete Compose v1 installations may not support other required features, such as service profiles.

Docker running as a non-root account is not the reason the Dockerfile `COPY` instruction cannot find build context files. The account will still need permissions to use the Docker daemon socket for DockTun's management features.

### Option 3: Single Docker Run Command

```bash
docker network create cloudflared 2>/dev/null || true

docker run -d --name docktun \
  -p 8080:3001 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v docktun_data:/app/data \
  --network cloudflared \
  --restart unless-stopped \
  ghcr.io/ooflyeyesoo/docktun:latest
```

### Option 4: Guided CLI Terminal Installer

For dedicated Linux servers or VMs, the automated script installs Docker, cloudflared, sets up systemd/LXC nesting, and configures everything:

```bash
sudo ./install.sh
```

### First-Run Setup Wizard

When you open DockTun in your browser for the first time, the guided onboarding wizard will:
1. **Admin Credentials**: Create your secure administrator account.
2. **Tunnel Activation**: Select and activate your remote access tunnel:
   - **Cloudflare Tunnel (Zero Trust)**: Enter your tunnel connector token or API token.
   - **WireGuard**: Configure a high-speed point-to-point VPN tunnel.
   - **Tailscale**: Connect via Tailscale auth key into your secure mesh tailnet.
   - **Local Only (ZeroConf)**: Access services locally via `<container>.local` mDNS.
3. **Auto-Discovery & Import**: Discover all existing containers running on your Docker daemon, attach them to the tunnel network, and automatically import Compose projects.
4. **Incident Alerts (Optional)**: Configure Discord, Telegram, Slack, or Ntfy push notifications.

### Automated Installation, Updates & Reset

```bash
# Automated install (sets up cloudflared, containers, and web UI)
sudo ./install.sh

# Update DockTun (creates backup of .env/db/config and rebuilds containers)
sudo ./upgrade.sh

# Reset / Uninstall (wipes containers, DB, and credentials while keeping Cloudflare tunnel config)
sudo ./uninstall.sh
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `JWT_SECRET` | Secret for JWT signing (min 32 chars) | *required* |
| `ADMIN_USERNAME` | Login username | `admin` |
| `ADMIN_PASSWORD` | Login password | *required* |
| `TUNNEL_NAME` | Cloudflare tunnel name | — |
| `TUNNEL_ID` | Cloudflare tunnel UUID | — |
| `CLOUDFLARED_CONFIG_PATH` | Config path inside container | `/etc/cloudflared/config.yml` |
| `CLOUDFLARED_CONTAINER` | Cloudflared container name | `docktun-cloudflared` |
| `DB_PATH` | SQLite database path | `/app/data/app.db` |
| `LISTEN_ADDR` | Backend listen address | `0.0.0.0:3001` |

## Architecture

```
┌─────────────────────────────────────────────┐
│  Docker Compose                             │
│                                             │
│  ┌───────────┐  ┌─────────┐  ┌──────────┐  │
│  │ frontend  │→ │ backend │→ │cloudflared│  │
│  │  (nginx)  │  │  (Go)   │  │ (tunnel) │  │
│  └───────────┘  └─────────┘  └──────────┘  │
│                      ↓                      │
│                  ┌────────┐                 │
│                  │ SQLite │                 │
│                  └────────┘                 │
└─────────────────────────────────────────────┘
```

## Project Structure

```
DockTun/
├── docker-compose.yml
├── .env.example
├── cloudflared/         # Tunnel config & credentials
├── backend/             # Go API server
│   ├── cmd/server/
│   └── internal/
│       ├── handler/     # HTTP handlers
│       ├── service/     # Business logic
│       ├── middleware/  # Auth middleware
│       ├── db/          # SQLite
│       └── ws/          # WebSocket hub
└── frontend/            # Vue 3 SPA
    ├── src/
    │   ├── views/
    │   ├── components/
    │   ├── composables/
    │   ├── stores/
    │   └── router/
    └── public/
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/login` | Authenticate |
| POST | `/auth/logout` | End session |
| POST | `/auth/refresh` | Refresh token |
| GET | `/api/status` | Tunnel status |
| POST | `/api/control/{action}` | start / stop / restart |
| GET | `/api/logs/ws` | Realtime log stream (WebSocket) |
| GET | `/api/logs/history` | Log history |
| GET | `/api/config` | Get tunnel config |
| PUT | `/api/config` | Update tunnel config |
| POST | `/api/config/validate` | Validate YAML |
| GET | `/api/config/backups` | List config backups |
| POST | `/api/tcp-access/generate` | Generate TCP access command |
| GET | `/api/health` | Health check |
| GET | `/api/metrics` | Cloudflared metrics |
| GET | `/api/notifications` | Get notification config |
| PUT | `/api/notifications` | Update notification config |
| GET | `/api/audit` | Audit log |
| POST | `/api/settings/password` | Change password |
| GET | `/api/projects` | List projects |
| POST | `/api/projects` | Create project |
| PUT | `/api/projects/{id}` | Update project |
| DELETE | `/api/projects/{id}` | Delete project |
| GET | `/api/projects/{id}/history` | Project action history |
| POST | `/api/projects/{id}/{action}` | up / down / restart / deploy |

## Development

### Backend

```bash
cd backend
go mod tidy
JWT_SECRET=dev ADMIN_PASSWORD=dev go run ./cmd/server
```

### Frontend

```bash
cd frontend
npm install
npm run dev
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Commit using [conventional commits](https://www.conventionalcommits.org/)
4. Push and open a Pull Request

See [RELEASE.md](RELEASE.md) for versioning and release details.

## License

MIT
