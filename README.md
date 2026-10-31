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
  - **Cloudflare Tunnels (Argo Tunnel)**: Public ingress without open ports, visual YAML config editor with auto-backups, and Cloudflare DNS sync.
  - **Tailscale**: Connect each container as its own isolated Tailscale device (userspace mode, no host TUN or root privileges required).
  - **WireGuard**: Mesh connectivity via companion container or existing host VPN.
  - **Local mDNS**: Automatic local `.local` domain routing on your LAN.
- **Optional local reverse proxy**: Choose Caddy, Traefik, or Nginx under **Settings → Local Reverse Proxy**, add manual hostname-to-container routes, and apply. DockTun creates and uses the dedicated `docktun-proxy` network, connects selected containers, and manages one active proxy companion. Routes use container names and internal ports; no public DNS or Cloudflare credentials are required.
- **Project backup and restore**: Export registered projects as portable `.tar.gz` archives from Settings and restore them into a new project name and folder. Archives include Compose, `.env`, and supporting files; protect them because `.env` may contain credentials.
- **Web security protections**: The API applies browser hardening headers, limits login and token-refresh attempts by client address, and rejects oversized authentication request bodies.
- **Session revocation**: Logout revokes the presented access or refresh token, and refresh-token rotation invalidates the previous token so copied refresh credentials cannot be reused.
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

### Option 1: Download & Run with Custom Port (Recommended)

Download the installer script, choose your desired web dashboard port, and launch:

```bash
# 1. Download installer
curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/install.sh -o install.sh
chmod +x install.sh

# 2. Run with your custom port (default is 8080)
sudo ./install.sh --port 8080
```

> [!TIP]
> Need a different port or directory? Pass `-p / --port` and `-d / --dir`:
> ```bash
> sudo ./install.sh --port 9000 --dir /opt/docktun
> ```

---

### Option 2: Instant One-Liner from GitHub

```bash
# Default port (8080)
curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/install.sh | sudo bash

# Custom port (e.g. 9000)
curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/install.sh | sudo bash -s -- --port 9000
```

*Or with `wget`:*

```bash
wget -qO- https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/install.sh | sudo bash -s -- --port 9000
```

---

### Option 3: Clone from GitHub

```bash
git clone https://github.com/oOflyeyesOo/docktun.git /opt/docktun
cd /opt/docktun
sudo bash install.sh
```

---

### Option 4: Docker Compose (If Docker is already installed)

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

### Option 5: Docker Run

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

### Option 6: Build from Source

```bash
git clone https://github.com/oOflyeyesOo/docktun.git /opt/docktun
cd /opt/docktun

docker compose up -d --build
```

---

## Remote Access & Exposure Providers

DockTun does not enforce a single global networking mode. Every container starts internal and isolated; choose remote access per app in **Containers → Access**:

- **Cloudflare Tunnel (Argo Tunnel)**: Enter your Cloudflare Tunnel connector token (or mount your existing `config.yml` and credentials) under **Remote Access → Cloudflare**, then click **Enable**.
  - **Terminology in Cloudflare menus**: Cloudflare Tunnel was originally named **Argo Tunnel** and routes edge traffic to `<tunnel-id>.cfargotunnel.com`. In Cloudflare dashboards, look for it under **Zero Trust → Networks → Tunnels** (or **Traffic → Argo Tunnel** in classic views).
  - **Connector Token vs API Token**: The connector token runs the `cloudflared` daemon without needing API permissions. An API token is only required if you want DockTun to automatically create CNAME records in Cloudflare DNS or manage remote routes.
  - **Zone Permissions vs Zone ID**:
    - **Zone Read (`Zone → Zone → Read`) is NOT needed** when you enter your **Zone ID** (*Domain Overview → API → Zone ID*) into DockTun. Providing the Zone ID allows you to safely omit Zone Read for least-privilege security.
    - **DNS Edit (`Zone → DNS → Edit`) IS required** if you want automated CNAME record creation, because Cloudflare requires DNS write permissions to modify records in a zone even when the Zone ID is known.
    - **Manual DNS**: If you prefer not to grant DockTun any Zone permissions, simply create the CNAME record manually in Cloudflare pointing to `<tunnel-id>.cfargotunnel.com`. DockTun will route the traffic without ever needing an API token.
- **Tailscale**: Connect any container with 1-click in the container's **Access** tab. DockTun spawns a dedicated userspace Tailscale helper for that app, creating a separate device identity and MagicDNS hostname on your tailnet without touching the host's networking stack.
- **WireGuard**: Configure an existing WireGuard VPN address or use the companion container.
- **Internal / mDNS**: Keeps containers internal, accessible via LAN or local `.local` addresses.

### Local Reverse Proxy

The local proxy is disabled by default. It is intended for simple LAN-friendly hostnames such as `jellyfin.home`, not as a replacement for Cloudflare Tunnel or public DNS.

#### Engine choices

- **Caddy** is the easiest starting point. It uses a compact generated configuration and can manage HTTPS certificates when the hostname and DNS setup support it.
- **Traefik** is useful when you have many Docker services or expect to move toward label- or provider-driven routing. DockTun currently generates explicit file-based routes for it.
- **Nginx** is the traditional option for users who prefer familiar, explicit server configuration and broad Nginx documentation.

Only one engine runs at a time. Switching engines preserves the route list and recreates the managed proxy companion with the selected image.

#### How a route works

Each enabled route has three important values:

1. **Hostname** — the name a browser requests, such as `jellyfin.home`. Your LAN DNS server or each client's hosts file must resolve it to the Docker host.
2. **Container/service** — the Docker container name on the proxy network, such as `jellyfin`. Do not enter `localhost`, a public URL, or the host's published port.
3. **Internal port** — the port the application listens on inside its container, such as `8096` for Jellyfin. This is not necessarily the host port shown in a Compose `ports:` mapping.

Example:

```text
jellyfin.home  ->  jellyfin  ->  8096
```

With HTTP port `80`, clients open `http://jellyfin.home`. If you choose HTTP port `8080`, they open `http://jellyfin.home:8080`.

DockTun creates the dedicated `docktun-proxy` bridge network, connects each enabled target container to it, and generates the selected engine's configuration. The proxy container and targets communicate through Docker's internal network; target application ports do not need to be published to the host. DockTun validates that each target container exists before applying changes.

#### Common setup checklist

1. Make sure the application is running and note its internal listening port.
2. Add a hostname that resolves to the Docker host.
3. Add the container name and internal port to a route.
4. Choose an available host HTTP/HTTPS port.
5. Enable the proxy and click **Apply Local Proxy**.
6. Test the hostname from a client on the same LAN.

Port conflicts, missing containers, invalid hostnames, and containers that cannot be attached to the dedicated network are reported during apply. Disable the feature to stop and remove the managed proxy companion.

---

## Updating DockTun

- **From the Web UI**: Open **Settings → DockTun services**, check for updates, select the installed services, and choose **Update selected**. This covers the core, local proxy, managed Cloudflare/Tailscale/WireGuard companions and per-app Tailscale helpers. Companions update one at a time and the core updates last using an independent worker. Stopped services stay stopped; missing providers are configured separately under Exposure providers. Existing mounts, settings, network aliases and device state volumes are retained. A failed replacement restores that service’s previous container and stops the batch; earlier successful updates remain installed. Container rollback does not reverse application-data migrations.
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

### Local `.local` links

Portless `http://app.local` links require the `zeroconf` service in the current Compose file. For an existing prebuilt installation, download the updated `compose.prebuilt.yml`, retain your existing volume/project settings, then run `docker compose -f compose.prebuilt.yml up -d --pull always docktun zeroconf`. Updating only the DockTun container does not add or recreate this separate service.

The proxy uses Linux host networking and host TCP port 80. Check `docker logs docktun-zeroconf` if names resolve but pages fail. A Synology web server or another proxy may already occupy port 80; arrange the existing LAN reverse proxy to serve these hostnames or free that port before using portless links. Keep direct LAN IP and published-port URLs available as a fallback. mDNS clients must be on a network that passes local multicast. On hosts with multiple interfaces, set `HOST_IP` in the zeroconf service to the Docker host's LAN address. These links do not enable a remote tunnel.
