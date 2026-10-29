# Installing DockTun

DockTun is deployed as a Docker container with access to the Docker daemon socket (`/var/run/docker.sock`).

---

## Prerequisites

- **Docker Engine** (20.10+)
- **Docker Compose** (v2.0+)

---

## Quick Installation Options

### Method 1: Automated installer (Docker installation included)

Save the supplied `install.sh` locally, then run it on your Linux host:

```bash
sudo bash install.sh
```

The repository is private; share the installer file directly with testers. No source checkout or GitHub token is needed to pull the published image. On Synology, install Container Manager from Package Center first. Rerunning the installer keeps the existing Compose file, pulls the current image, and starts only DockTun; optional providers retain their current state.

The script automatically:
1. Detects your OS and installs the official Docker Engine and Compose plugin if missing.
2. Creates an optimized `/opt/docktun/compose.yml`.
3. Starts the container stack and outputs your dashboard URL and initial ownership setup token.

---

### Method 2: Docker Compose (If Docker is already installed)

Create a directory for DockTun, download or save the following as `compose.yml`, and launch:

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
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:3001/healthz"]
      interval: 30s
      timeout: 5s
      start_period: 30s
      retries: 3
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

Start the container:

```bash
docker compose up -d
```

Open your browser at **`http://<server-ip>:8080`**.

---

## First-Time Administrator Setup

When opening the dashboard for the first time, you will be prompted for an **Ownership Setup Code** to create the initial admin account:

```bash
docker exec docktun cat /app/data/setup.token
```

Copy the token printed in your terminal and paste it into the setup screen.

*(Optional: If you prefer automated headless setups, you can define `ADMIN_USERNAME` and `ADMIN_PASSWORD` in your Compose `environment` section to pre-create credentials).*

---

## Platform Guides

### Synology DSM (Container Manager)
1. Open **Container Manager** on your Synology NAS.
2. Go to **Project** → **Create**.
3. Name your project (e.g. `docktun`) and select a destination folder (e.g. `/docker/docktun`).
4. Select **Create docker-compose.yml** and paste the Compose configuration above.
5. Finish the wizard to build and start the project.
6. Retrieve the setup token via Synology SSH (`sudo docker exec docktun cat /app/data/setup.token`) or container terminal.

### Unraid & TrueNAS SCALE
Create a custom Docker Compose application using the template above, ensuring `/var/run/docker.sock` is passed as a bind mount so DockTun can manage host containers.

---

## Persistent Volumes

| Volume / Mount | Purpose |
|----------------|---------|
| `/var/run/docker.sock` | Communicates with the Docker daemon to monitor and control containers. |
| `docktun-data` (`/app/data`) | Stores the SQLite database (`app.db`), user sessions, audit logs, and encryption keys. |
| `docktun-cloudflared` (`/etc/cloudflared`) | Stores Cloudflare Tunnel YAML ingress configuration and tunnel credentials. |
| `docktun-wireguard` (`/app/data/wireguard`) | Stores WireGuard VPN configuration and profile keys. |
| `./projects` (`/opt/projects`) | Stores cloned Compose repositories and project working directories. |

---

## Updating DockTun

### Via Dashboard (1-Click)
Navigate to **Settings → Update DockTun → Check for updates → Install update**. DockTun pulls the latest published image, verifies health, and seamlessly replaces the running container without losing volumes or network attachments.

### Via Docker Compose
```bash
docker compose pull
docker compose up -d
```
