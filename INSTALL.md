# Installing DockTun

DockTun is deployed as a Docker container with access to the Docker daemon socket (`/var/run/docker.sock`). It provides a unified web dashboard for Docker containers, Compose stacks, and remote access tunnels.

---

## Prerequisites

- **Linux host** (Ubuntu, Debian, Fedora, CentOS, Rocky, AlmaLinux, Arch, Synology DSM, or any Docker-compatible Linux OS)
- **Docker Engine** (20.10+) & **Docker Compose** (v2.0+) *(automatically installed by the script if missing)*
- Port **8080** open on your local firewall (or your chosen custom port), plus **80/tcp** & **5353/udp** when using `docktun-zeroconf` automatic `.local` mDNS (enabled by default in Proxmox LXC and `compose.prebuilt.yml`; pass `--no-zeroconf` in LXC to opt out)

---

## Quick Installation

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

If you prefer an instant one-line installation without saving the script first:

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

To keep a full local git checkout of the repository:

```bash
# 1. Clone repository
git clone https://github.com/oOflyeyesOo/docktun.git /opt/docktun
cd /opt/docktun

# 2. Run installer
sudo bash install.sh
```

---

### Option 4: Docker Compose (Manual Setup)

If you already have Docker and Compose installed and prefer managing stacks manually, create a directory and save the following as `compose.yml`:

```yaml
name: docktun

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

Launch the stack:

```bash
docker compose up -d
```

Open your browser at **`http://<your-server-ip>:8080`**.

---

## First-Time Administrator Setup

On first launch, DockTun generates a unique one-time **Ownership Setup Token** to ensure that only the server owner can claim the administrator account.

Retrieve your token by running:

```bash
docker exec docktun cat /app/data/setup.token
```

Copy the token printed in your terminal and paste it into the initial browser setup screen to create your administrator username and password.

*(Optional: For automated or headless deployments, you can define `ADMIN_USERNAME` and `ADMIN_PASSWORD` in your Compose `environment` section to pre-seed credentials).*

---

## Remote Access: Cloudflare Tunnel (Argo Tunnel)

DockTun includes deep integration with **Cloudflare Tunnel** (historically named and often referred to in Cloudflare menus as **Argo Tunnel**).

### Locating Tunnels in Cloudflare Dashboard

Depending on your Cloudflare dashboard version, tunnels are located under:
- **Cloudflare Zero Trust** → **Networks** → **Tunnels** (modern Zero Trust dashboard)
- **Domain Overview** → **Traffic** → **Argo Tunnel** (classic dashboard)
- **Networking** → **Tunnels**

All Cloudflare Tunnels route traffic to edge CNAME targets ending in `.cfargotunnel.com` (e.g. `<tunnel-id>.cfargotunnel.com`).

---

### Understanding Tokens & Permissions

There are two distinct credentials used with Cloudflare:

| Credential Type | Purpose | Required Permissions |
|---|---|---|
| **Connector Token** (Tunnel Token) | Runs the `cloudflared` daemon container to establish the edge tunnel connection. | **No API permissions required.** Provided directly by Cloudflare when creating a tunnel. |
| **Cloudflare API Token** *(Optional)* | Enables DockTun to automatically create/sync DNS CNAME records and manage remote routes from the UI. | `Account → Cloudflare Tunnel → Edit`<br>`Zone → DNS → Edit` |

---

### Do You Need Zone Permissions If You Provide the Zone ID?

This is a common question when configuring Cloudflare API tokens:

> **1. Zone Read (`Zone → Zone → Read`) is NOT required when you enter the Zone ID.**  
> When you copy your **Zone ID** from Cloudflare (*Domain Overview → API → Zone ID*) and paste it into DockTun's **Zone ID** field, DockTun directly targets that zone. DockTun does not need `Zone: Read` permission because it doesn't need to list or discover your zones. Omitting `Zone: Read` follows the security principle of least privilege.

> **2. DNS Edit (`Zone → DNS → Edit`) IS required if you want automated DNS record management.**  
> Cloudflare's security architecture requires that any API token creating or modifying DNS records (such as creating the CNAME for `subdomain.yourdomain.com` pointing to `<tunnel-id>.cfargotunnel.com`) must hold `Zone → DNS → Edit` permissions on that zone. Even when the Zone ID is explicitly known, Cloudflare will reject DNS record writes without DNS Edit permissions.

> **3. You do NOT need any API token or Zone permissions if you manage DNS manually.**  
> If you prefer not to create an API token or grant Zone permissions, you can simply paste your **Connector Token** into DockTun to run the tunnel, and then manually create a `CNAME` record in your Cloudflare DNS table pointing your desired subdomain to `<tunnel-id>.cfargotunnel.com`. DockTun will route the traffic without ever needing an API token.

---

## Platform Guides

### Synology DSM (Container Manager)
1. Open **Container Manager** on Synology DSM.
2. Navigate to **Project** → **Create**.
3. Name your project (e.g., `docktun`) and select a destination folder (e.g., `/docker/docktun`).
4. Choose **Create docker-compose.yml** and paste the Compose configuration above.
5. Finish the wizard to build and start the project.
6. Retrieve the setup token via Synology SSH (`sudo docker exec docktun cat /app/data/setup.token`) or through the Container Manager terminal tab.

### Unraid & TrueNAS SCALE
Create a custom Docker Compose application using the template above, ensuring `/var/run/docker.sock` is mounted so DockTun can discover and manage host containers.

### Proxmox VE (LXC)
Provision a hardened, unprivileged Proxmox VE 8/9 LXC container with Docker CE, **shared GPU passthrough** (NVIDIA, Intel `i915`/`xe`, AMD), **pre-installed DockTun**, and **AIOStreams readiness** directly from your Proxmox host (`root`) via a 1-keystroke express wizard or `--yes`:

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/scripts/proxmox-lxc.sh)"
```

- **Hardening**: OpenSSH key-first drop-in (`00-hardening.conf`), UFW + standalone reload-safe (`-F DOCKER-USER`) `DOCKER-USER` LAN scoping (`after.rules` & IPv6-gated `after6.rules`), Fail2ban (`systemd` + `nftables`), `br_netfilter` + `"userland-proxy": false` (`127.0.0.1` loopback), `"default-address-pools": [{"base": "172.24.0.0/14", "size": 24}]`, weekly `docktun-docker-prune.timer` (never prunes volumes), `/etc/security/limits.d/99-lxc-media.conf` (`nofile 65536` for PAM shells) + `/etc/systemd/system.conf.d/99-lxc-media.conf` (`DefaultLimitNOFILE=65536:524288` for systemd services), and unattended-upgrades with NVIDIA package hold/blacklist (`libnvcuvid*` included, excluding `nvidia-container-toolkit*` from hold).
- **Automatic mDNS & Wolf Support**: Enables `docktun-zeroconf` (`http://docktun.local`, ports `80/tcp` + `5353/udp`) by default (`--no-zeroconf` / `--without-zeroconf` opt-out, with port-80 conflict fallback reflected in notes/cheat sheet), and supports `--with-wolf-input` for unprivileged Wolf/Purah virtual gamepad passthrough.
- **Shared GPU & Lifecycle Operations**: Concurrent host-driven GPU sharing across multiple LXCs without `vfio-pci` (NVIDIA CDI + per-CTID pre-start hook, Intel VA-API, AMD `/dev/dri` + `/dev/kfd`), plus `--doctor <ctid>`, `--provision-only <ctid>`, `--list`, `--destroy <ctid>`, `--cleanup-on-failure`, `--startup <spec>`, `--aio-compose <host_file>`, `--aio-repo <git_url>`, `--aio-branch <ref>`, `--vlan <tag>`, `--mtu <bytes>`, `--timezone <tz>`, `--mount <host:ct>`, template-aware Proxmox Web UI `--description` summary notes, and `--template-hygiene` (`docktun-firstboot.sh`/`.service`).
- **Pre-installed DockTun & AIOStreams readiness**: Deploys DockTun with `compose.yml` alignment and prepares `/opt/docktun/projects/aiostreams` (`.env.template`, HTTP `206` streaming Nginx example, and `check-readiness.sh`).
- **Full guide & CLI flags**: See [docs/proxmox-lxc.md](docs/proxmox-lxc.md) (script: [scripts/proxmox-lxc.sh](scripts/proxmox-lxc.sh)).

---

## Persistent Volumes

| Volume / Mount | Purpose |
|----------------|---------|
| `/var/run/docker.sock` | Communicates with the host Docker daemon to inspect and manage containers. |
| `docktun-data` (`/app/data`) | Stores the SQLite database (`app.db`), user sessions, audit logs, and encryption keys. |
| `docktun-cloudflared` (`/etc/cloudflared`) | Stores Cloudflare Tunnel YAML ingress configuration and tunnel credentials. |
| `docktun-wireguard` (`/app/data/wireguard`) | Stores WireGuard VPN configuration and profile keys. |
| `./projects` (`/opt/projects`) | Stores cloned Compose repositories and project working directories. |

---

## Updating DockTun

### 1-Click Update via Web UI
Navigate to **Settings → Update DockTun → Check for updates → Install update**. DockTun pulls the latest published image, verifies container health, and seamlessly replaces the running container without losing volumes or network attachments.

### Via Docker Compose
```bash
docker compose pull
docker compose up -d
```


## Update, session and restore integrity

Container image updates now preserve the actual running Docker definition:
mounts (including anonymous volumes), environment, entrypoint, restart policy,
ports and every network/alias. They do not synthesize a one-service Compose
stack or guess a database host. Mount/network mismatches and failed startup
checks restore the old container. Stopped containers stay stopped. Source builds
continue through stack build/deploy. A running container without a Docker
healthcheck is checked only for stable running state; configure an application
readiness healthcheck to verify database access. Pin database major versions;
image replacement is not a database schema migration or database backup.

Core updates refuse an unmounted database directory even when update progress
has a persistent mount. Keep `DB_PATH` and its sibling signing secret on the
same persistent writable data directory. The bootstrap environment password is
used only when no administrator exists: Settings password changes survive
restarts and updates. No database credentials are guessed or overwritten.

The session-integrity upgrade requires one fresh sign-in for previously issued
sessions. It preserves accounts, passwords and stored data. New sessions survive
restarts and core updates; logout revokes the complete session, refresh tokens
cannot call ordinary APIs, and password changes end all previous sessions.

Project restore requires a new, unused absolute destination. It stages and
validates the archive before registering the project; it never overwrites an
existing project folder. Exports preserve executable files and saved Compose
source lists but omit Git credentials and `.git` history. Re-enter a private
repository token before Git deployment. Named-volume/database contents remain a
separate backup responsibility. Existing file-only archives remain importable.

Automatic alerts check Docker state every 30 seconds while DockTun runs. They
report observed running-to-stopped transitions, recovery, Docker workload CPU
and memory thresholds, and explicit stack deployment completion. Outages
shorter than the sampling interval can be missed. Queued outage notifications
retry while DockTun runs; delivery is not durable across core restarts. The
manual test remains available. Movie Night is temporarily unavailable in the
library until its browser image is published and its architecture requirements
are satisfied; existing deployments are unaffected.
