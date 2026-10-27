# ☁️ DockTun

Modern, self-hosted web dashboard for managing Docker containers and Compose stacks, with optional, independent per-container remote access (Cloudflare Tunnels, Tailscale, WireGuard, and local mDNS).

![Go](https://img.shields.io/badge/backend-Go-00ADD8?logo=go)
![Vue](https://img.shields.io/badge/frontend-Vue_3-4FC08D?logo=vue.js)
![Docker](https://img.shields.io/badge/deploy-Docker-2496ED?logo=docker)
![License](https://img.shields.io/badge/license-MIT-green)

---

## Highlights

- 🐳 **Docker Container Management** — Start, stop, restart, pause, inspect, and delete containers. Live CPU, memory, and network I/O stats.
- 📦 **Docker Compose Projects** — Deploy and manage multi-container stacks with Git clone, branch checkout, pull, and streaming build logs.
- 🌐 **Independent Per-App Remote Access** — Expose containers individually without exposing your entire network:
  - **Cloudflare Tunnels**: Public ingress without open ports, visual YAML config editor with auto-backups, and Cloudflare DNS sync.
  - **Tailscale**: Connect each container as its own isolated Tailscale device (userspace mode, no host TUN or root privileges required).
  - **WireGuard**: Mesh connectivity via companion container or existing host VPN.
  - **Local mDNS**: Automatic local `.local` domain routing on your LAN.
- 💻 **Web Terminal & Logs** — In-browser interactive `docker exec` terminal and real-time streaming WebSocket logs with filtering.
- 📁 **File & Volume Explorer** — Browse container filesystems, view and edit configuration files in-place, and upload or download files.
- 🔄 **Self-Healing & 1-Click Updates** — Automatic database companion network relinking, continuous health reconciliation, and 1-click self-updating with automated health-check rollback.
- 🔔 **Audit Trail & Alerts** — Full SQLite action history and webhook notifications for Discord, Telegram, and Slack.

---

## 📸 Interface Preview

<p align="center">
  <img src="assets/screenshots/dashboard.png" alt="DockTun Dashboard" width="100%" />
</p>

<details open>
<summary><b>Additional Views (Containers, Ingress Rules, Remote Access, File Explorer)</b></summary>
<br>

### 🐳 Container & Compose Stack Orchestration
> Real-time CPU, RAM, and network meters, stack grouping, in-browser terminal, and 1-click updates.

<p align="center">
  <img src="assets/screenshots/containers.png" alt="Container Fleet" width="100%" />
</p>

### ☁️ Cloudflare Ingress Routing
> Visual ingress rule management, public domain mapping to internal container ports, and direct Cloudflare DNS synchronization.

<p align="center">
  <img src="assets/screenshots/ingress-rules.png" alt="Cloudflare Ingress Rules" width="100%" />
</p>

### 🌐 Multi-Provider Remote Access Hub
> Unified exposure manager supporting Cloudflare edge tunnels, Tailscale userspace devices, WireGuard VPN, and local mDNS.

<p align="center">
  <img src="assets/screenshots/remote-access.png" alt="Remote Access Hub" width="100%" />
</p>

### 📁 Storage & Docker Volume Explorer
> Full container filesystem and Docker volume browsing with in-place file editing and uploads.

<p align="center">
  <img src="assets/screenshots/storage-explorer.png" alt="Storage and File Explorer" width="100%" />
</p>

</details>

---

## Architecture

DockTun compiles the Vue 3 frontend directly into a single, high-performance Go binary. There is no separate Nginx container or reverse-proxy dependency.

```
┌──────────────────────────────────────────────────────────────┐
│  DockTun (Unified Container)                                 │
│                                                              │
│  ┌────────────────────────┐      ┌────────────────────────┐  │
│  │  Vue 3 SPA (Embedded)  │ ───> │  Go Backend API (Chi)  │  │
│  └────────────────────────┘      └───────────┬────────────┘  │
│                                              │               │
│               ┌──────────────────────────────┼──────────┐    │
│               │                              │          │    │
│               ▼                              ▼          ▼    │
│       /var/run/docker.sock                SQLite    Dynamic  │
│      (Docker Daemon Control)             (app.db)  Companions│
└──────────────────────────────────────────────────────────────┘
```

---

## Quick Start

### Option 1: Docker Compose (Recommended)

Save the following as `compose.yml` in an empty directory:

```yaml
services:
  docktun:
    image: ghcr.io/ooflyeyesoo/docktun:latest
    container_name: docktun
    restart: unless-stopped
    ports:
      - "8080:3001"
    environment:
      DOCKER_NETWORK: docktun
      TAILSCALE_STATE_VOLUME: docktun-tailscale-state
      WIREGUARD_PORT: 51820
      DB_PATH: /app/data/app.db
      CLOUDFLARED_CONFIG_PATH: /etc/cloudflared/config.yml
      CLOUDFLARED_CONTAINER: docktun-cloudflared
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - docktun-data:/app/data
      - docktun-cloudflared:/etc/cloudflared
      - docktun-wireguard:/app/data/wireguard
      - ./projects:/opt/projects
    networks:
      - docktun

volumes:
  docktun-data:
  docktun-cloudflared:
  docktun-wireguard:

networks:
  docktun:
    name: docktun
    driver: bridge
```

Start DockTun:

```bash
docker compose up -d
```

Open **`http://<your-server-ip>:8080`** in your browser.

#### First-Time Admin Account Setup

To create your administrator account on first launch, copy the ownership setup token from the container:

```bash
docker exec docktun cat /app/data/setup.token
```

Enter this token on the initial screen to set your administrator credentials. Alternatively, set `ADMIN_USERNAME` and `ADMIN_PASSWORD` in your Compose `environment` to configure credentials ahead of time.

> [!WARNING]
> DockTun mounts the Docker daemon socket (`/var/run/docker.sock`) to control containers, which grants broad host access. Always keep DockTun behind authenticated remote access or on a trusted local network. **Never expose port 8080 directly to the public internet without authentication.**

---

### Option 2: Docker Run

```bash
docker network create docktun 2>/dev/null || true

docker run -d \
  --name docktun \
  -p 8080:3001 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v docktun-data:/app/data \
  -v docktun-cloudflared:/etc/cloudflared \
  -v docktun-wireguard:/app/data/wireguard \
  -v $(pwd)/projects:/opt/projects \
  --network docktun \
  --restart unless-stopped \
  ghcr.io/ooflyeyesoo/docktun:latest
```

---

### Option 3: Build from Source

```bash
git clone https://github.com/oOflyeyesOo/docktun.git /opt/docktun
cd /opt/docktun

docker compose up -d --build
```

---

## Remote Access & Exposure Providers

DockTun does not enforce a single global networking mode. Every container starts internal and isolated; choose remote access per app in **Containers → Access**:

- **Cloudflare Tunnel**: Enter your Cloudflare Tunnel connector token (or mount your existing `config.yml` and credentials) under **Remote Access → Cloudflare**, then click **Enable**. Map individual apps to public subdomains directly from the dashboard.
- **Tailscale**: Connect any container with 1-click in the container's **Access** tab. DockTun spawns a dedicated userspace Tailscale helper for that app, creating a separate device identity and MagicDNS hostname on your tailnet without touching the host's networking stack.
- **WireGuard**: Configure an existing WireGuard VPN address or use the companion container.
- **Internal / mDNS**: Keeps containers internal, accessible via LAN or local `.local` addresses.

---

## Updating DockTun

- **From the Web UI**: Go to **Settings → Update DockTun → Check for updates → Install update**. DockTun pulls the latest image, spins up the new container while preserving all mounts and networks, verifies health, and automatically rolls back if health checks fail.
- **Via Docker Compose**:
  ```bash
  docker compose pull
  docker compose up -d
  ```

---

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Host port to publish | `8080` |
| `LISTEN_ADDR` | Internal listen address | `0.0.0.0:3001` |
| `JWT_SECRET` | Secret key for JWT session signing | Auto-generated in `app.db` |
| `ADMIN_USERNAME` | Default admin username (pre-seeding) | `admin` |
| `ADMIN_PASSWORD` | Default admin password (pre-seeding) | Generated via `setup.token` |
| `DOCKER_NETWORK` | Bridge network name | `docktun` |
| `DB_PATH` | Path to persistent SQLite database | `/app/data/app.db` |
| `CLOUDFLARED_CONFIG_PATH` | Cloudflare config file path | `/etc/cloudflared/config.yml` |
| `CLOUDFLARED_CONTAINER` | Cloudflared container name | `docktun-cloudflared` |
| `TAILSCALE_STATE_VOLUME` | Named Docker volume for Tailscale state | `docktun-tailscale-state` |
| `SELF_PATH` | Host path for self-updates | `/opt/projects/docktun` |

---

## Development

```bash
# Backend (Go 1.25+)
cd backend
go mod tidy
go run ./cmd/server

# Frontend (Node 20+ / pnpm)
cd frontend
pnpm install
pnpm run dev
```

---

## License

MIT License. See [LICENSE](LICENSE) for details.
