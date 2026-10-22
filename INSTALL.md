# 🚀 DockTun Docker VM Installation Guide

This guide explains how to deploy **DockTun** and **Cloudflare Tunnel** onto your Docker VM or container using the automated [`install.sh`](file:///c:/Users/oofly/Downloads/tun/install.sh) script.

---

## 📋 What the Installer Does

1. **Pre-Flight Hardware & Feature Checks**:
   - **Virtualization & Nesting**: Detects your environment (Proxmox LXC, KVM, QEMU, VMware, Hyper-V, WSL, bare-metal). Verifies LXC nesting permissions (cgroups writable, overlayfs support), nested CPU virtualization extensions (Intel VMX / AMD SVM), and `/dev/kvm`.
   - **GPU Acceleration**: Scans for NVIDIA GPUs (and automatically checks/configures the **NVIDIA Container Toolkit** for Docker), plus Intel QuickSync and AMD VA-API hardware video acceleration (`/dev/dri`).
   - **Kernel & Networking**: Checks and automatically enables IPv4 Forwarding (`net.ipv4.ip_forward = 1`), loads/creates TUN/TAP devices (`/dev/net/tun`), enables bridge netfilter (`br_netfilter`), verifies `overlay2` storage, and checks NTP system time synchronization.
   - **Resources & Ports**: Audits RAM, free disk space, and tests for port 8080 conflicts (auto-suggesting an alternative port like 8081 if occupied).
2. **Docker & Docker Compose**: Verifies or offers one-click official installation of Docker and Docker Compose plugin.
3. **Cloudflared Installation**: Automatically detects your CPU architecture (`x86_64`, `arm64`, `arm`, etc.) and installs the official `cloudflared` CLI binary into `/usr/local/bin/cloudflared`.
4. **Cloudflare Authentication**:
   - Launches interactive `cloudflared tunnel login` flow (or allows importing existing credentials JSON).
   - Prompts for your tunnel name and creates the tunnel in Cloudflare.
   - Automatically locates the generated credentials JSON (`<tunnel-id>.json`) and moves it to `./cloudflared/credentials.json`.
   - Automatically registers DNS CNAME routes directly in Cloudflare.
5. **Admin Security**:
   - Prompts you to set an Admin username and strong password (with masked input and auto-generate fallback).
   - Generates a random 64-character `JWT_SECRET`.
   - Writes everything into `.env`.
6. **Existing Container Discovery & Import**:
   - Connects all detected running containers to the `cloudflared` Docker network for internal DNS and routing.
   - Inspects containers for Docker Compose metadata (`com.docker.compose.project.working_dir`), registers them in DockTun's SQLite database (`app.db`), and mounts their paths in `docker-compose.override.yml`.
   - Prompts you to assign public domain names to any of your containers, generating `cloudflared/config.yml` ingress rules and Cloudflare DNS records automatically.
7. **Launch & Verification**:
   - Launches the stack using `docker compose up -d --build`.
   - Displays your local dashboard URL, public tunnel URL, and admin login credentials.

---

## ⚡ Quick Start on Your Linux VM

### 1. Copy or Clone DockTun to your VM

If copying from your local machine to your VM:
```bash
# Example using rsync or scp:
rsync -avz --exclude 'node_modules' --exclude '.git' ./ user@your-vm-ip:/opt/docktun
```

Or clone directly on your VM:
```bash
cd /opt
# Ensure you are inside the DockTun directory
cd docktun
```

### 2. Make the script executable and run it

```bash
chmod +x install.sh
sudo ./install.sh
```

---

## 🛠️ Pre-Flight Verification Highlights

### Virtualization & Nesting Check
- **Proxmox LXC Containers**: Verifies that LXC nesting is functional (`cgroups` writable, `overlayfs` supported). If not enabled, the installer guides you to enable `nesting=1,keyctl=1` in your container options.
- **KVM / QEMU / VMware / Hyper-V VMs**: Checks CPU flags for nested hardware virtualization (`vmx`/`svm`) and `/dev/kvm` presence.
- **Docker Socket**: Confirms `/var/run/docker.sock` is ready for DockTun container management.

### GPU Acceleration Check
- **NVIDIA GPU Detection**: If an NVIDIA GPU is detected (via PCI or `nvidia-smi`), the script checks whether the **NVIDIA Container Toolkit** (`nvidia-ctk`) is configured in `/etc/docker/daemon.json`.
  - If not configured, the script offers a **one-click automatic installation & configuration** of the toolkit so your media (Plex, Jellyfin, Tdarr) and AI containers (Ollama, LocalAI) can access the GPU.
- **Intel & AMD Hardware Acceleration**: Inspects `/dev/dri/renderD128` and `/dev/dri/card*` for QuickSync and VA-API hardware transcoding capabilities.

### Kernel & Networking Fixes
- **IPv4 Forwarding**: If `net.ipv4.ip_forward` is disabled (`0`), the script automatically activates it (`1`) and writes `/etc/sysctl.d/99-docker-ip-forward.conf` to persist across reboots, preventing container network isolation issues.
- **TUN/TAP Support**: Verifies `/dev/net/tun` exists and automatically loads the kernel module or creates the node if missing.
- **Time Synchronization (NTP)**: Checks that system time is synced. Accurate time is strictly required for Cloudflare TLS handshakes and JWT authentication.

---

## 🌐 Post-Installation Access

- **Local Access**: `http://<VM-IP>:<PORT>` (e.g. `http://192.168.1.100:8080`)
- **Public Tunnel Access**: `https://docktun.yourdomain.com` (if configured)
- **Default Username**: `admin` (or the username you selected)
- **Password**: Your chosen admin password

---

## 🔧 Useful Commands on the VM

```bash
# View realtime logs of all DockTun services
docker compose logs -f

# View cloudflared tunnel logs specifically
docker compose logs -f cloudflared

# Restart DockTun
docker compose restart

# Stop DockTun
docker compose down

# Check Cloudflare tunnels via CLI
cloudflared tunnel list

# Reset / Uninstall DockTun (preserving Cloudflare config & credentials)
sudo ./uninstall.sh
```

---

## ⬆️ How to Update / Upgrade DockTun

DockTun preserves all your data (`data/app.db`), configuration (`cloudflared/config.yml`), and credentials across updates.

### On Linux Server or VM:
```bash
# 1. Pull latest code (if using Git)
git pull

# 2. Run the automated upgrade script (creates automatic backups and rebuilds)
sudo ./upgrade.sh
```
*Or simply rebuild with Docker Compose:*
```bash
docker compose up -d --build
```

### On Windows (PowerShell or Command Prompt):
```powershell
.\upgrade.ps1
# or double-click upgrade.bat
```
*Or run Docker Compose directly:*
```powershell
docker compose up -d --build
```

---

## 🔄 Fresh Reinstall / Reset

If you wish to wipe the database, containers, and credentials for a clean slate while **keeping your Cloudflare Tunnel configuration**:

```bash
sudo ./uninstall.sh
sudo ./install.sh
```
When running `install.sh`, press `Enter` to resume with your preserved tunnel, and enter fresh admin credentials!

