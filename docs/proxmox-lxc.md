# Proxmox VE 8 & 9 Single-File Hardened LXC Provisioner (`proxmox-lxc.sh`)

[`proxmox-lxc.sh`](../scripts/proxmox-lxc.sh) is a single-file, self-contained Bash script that prepares a Proxmox VE 8/9 host and creates a hardened, unprivileged LXC container with **shared GPU passthrough**, **Docker CE + Compose v2**, **pre-installed [DockTun](https://github.com/oOflyeyesOo/docktun)**, and **[AIOStreams](https://github.com/Viren070/AIOStreams) readiness**.

---

## Quickstart

Run on the Proxmox VE host as `root`:

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/scripts/proxmox-lxc.sh)"
```

Preview all planned host and container actions without modifying the system:

```bash
bash proxmox-lxc.sh --non-interactive --dry-run
```

Fully automated non-interactive run (Debian 12, static IP, auto-detected shared GPU, DockTun on port `8081`):

```bash
bash proxmox-lxc.sh \
  --yes \
  --ctid 120 \
  --hostname media-aio \
  --os debian12 \
  --storage local-lvm \
  --template-storage local \
  --cores 4 \
  --memory 8192 \
  --swap 2048 \
  --disk 48 \
  --bridge vmbr0 \
  --ip 192.168.1.120/24 \
  --gw 192.168.1.1 \
  --dns 1.1.1.1 \
  --ssh-pubkey-file /root/.ssh/id_ed25519.pub \
  --admin-user mediaadmin \
  --gpu auto \
  --docktun-port 8081 \
  --lan-subnets "192.168.1.0/24" \
  --media-dir /mnt/pve/media \
  --domain aio.example.com
```

---

## Pipe-Safe & Minimal-Prompt UX

1. **Network truncation & pipe safety**: All logic is wrapped in functions with `main "$@"` on the final line. `${BASH_SOURCE[0]:-}` is guarded safely, temporary files live in a `mktemp -d` directory cleaned by `trap cleanup EXIT`, and interactive prompts explicitly read from `/dev/tty`. If no controlling TTY exists and `-y`/`--yes`/`--non-interactive` is not passed, the script fails early with an actionable error.
2. **Smart Auto-Detection**: Before prompting, the script detects:
   - Proxmox VE version (`pveversion`) and host Debian codename (`bookworm`/`trixie`)
   - Next free cluster container ID (`pvesh get /cluster/nextid`)
   - Active `rootdir` and `vztmpl` storage pools (`pvesm status`)
   - Default network bridge (`vmbr*`)
   - Host GPUs via `lspci` with word-boundary matching (so `NVIDIA Corporation` / `Intel Corporation` never false-match `ATI`), plus Intel `i915` vs `xe` kernel driver detection
   - Existing host SSH public keys (`/root/.ssh/id_ed25519.pub`, `/root/.ssh/id_rsa.pub`, `/root/.ssh/authorized_keys`, `/root/.ssh/*.pub`)
   - Host port `8080` collisions (automatically bumps `DOCKTUN_PORT` to `8081` unless `--docktun-port` was explicitly specified)
   - Whether host preparation is already configured or still needed (runs automatically when needed unless `--skip-host-prep` is set)
3. **1-Keystroke Default vs. Streamlined Customize**:
   - Displays a concise **Auto-Detected Configuration Summary** banner (showing mDNS `*.local` status, detected GPU, SSH key status, and DockTun port).
   - Prompts once: `Proceed with these defaults? [Y]es / [c]ustomize / [n]o [Y]:`
     - **`Enter` / `y` / `e`**: Proceeds immediately with all auto-detected defaults (1 keystroke total).
     - **`c`**: Asks only the 8 essential settings (`CTID`, `Hostname`, `OS`, `CPU/RAM/Disk`, `IPv4/Gateway`, `Shared GPU mode` + `PCI slot` if multiple GPUs exist, `DockTun port`, and `Wolf input passthrough`), with `Enter` keeping each auto-detected default. All other options remain configurable via CLI flags.

---

## Complete CLI Reference

| Flag | Default | Description |
|---|---|---|
| `-y`, `--yes`, `--non-interactive` | off | Skip interactive prompts; use auto-detected defaults and CLI flags |
| `--dry-run` | off | Print planned commands and file changes without modifying Proxmox or creating an LXC |
| `--skip-host-prep` | off | Skip Proxmox host preparation step |
| `--host-prep-only` | off | Only run Proxmox host preparation (do not create an LXC) |
| `--ctid <id>` | next cluster ID | Proxmox LXC container ID (`>= 100`) |
| `--hostname <name>` | `media-lxc` | Container hostname |
| `--os <distro>` | `debian12` | LXC template: `debian12`, `debian13`, or `ubuntu2404` |
| `--storage <name>` | auto (`rootdir`) | Proxmox rootfs storage pool (fallback: `local-lvm`) |
| `--template-storage <name>` | auto (`vztmpl`) | Proxmox template storage pool (fallback: `local`) |
| `--cores <n>` | `4` | CPU cores |
| `--memory <mb>` | `4096` | RAM in MiB (`>= 512`) |
| `--swap <mb>` | `2048` | Swap in MiB |
| `--disk <gb>` | `32` | Root disk size in GiB (`>= 4`) |
| `--bridge <vmbrX>` | auto (`vmbr0`) | Proxmox network bridge |
| `--ip <cidr\|dhcp>` | `dhcp` | IPv4 CIDR (e.g. `192.168.1.50/24`) or `dhcp` |
| `--gw <ip>` | empty | IPv4 gateway (used with static `--ip`) |
| `--dns <ip>` | `1.1.1.1` | Container DNS nameserver |
| `--ssh-pubkey-file <path>` | auto (`/root/.ssh/*.pub`) | Path to SSH public key file to authorize |
| `--ssh-pubkey <key>` | empty | Inline SSH public key string to authorize |
| `--admin-user <name>` | `mediaadmin` | Non-root `sudo`/`docker`/`video`/`render` admin username |
| `--gpu <mode>` | `auto` | Shared GPU passthrough mode: `auto`, `nvidia`, `intel`, `amd`, `all`, `none` |
| `--pci-slot <slot>` | empty | Target PCI slot on multi-GPU hosts (e.g. `0000:01:00.0` or `01:00.0`) |
| `--docktun-port <port>` | `8080` | Host port mapped to DockTun `:3001` |
| `--docktun-dir <dir>` | `/opt/docktun` | DockTun installation directory inside LXC |
| `--docktun-admin-user <user>` | empty | Optional pre-seeded DockTun admin username (first DB init only) |
| `--docktun-admin-password <pw>` | empty | Optional pre-seeded DockTun admin password (first DB init only; triggers `0600` compose mode) |
| `--lan-subnets <cidrs>` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` | Comma-separated trusted LAN CIDRs allowed in UFW and `DOCKER-USER` |
| `--media-dir <host_path>` | empty | Optional host directory (`chown 100000:100000`, `chmod 0775`) mounted at `/mnt/media` |
| `--with-fuse` | off | Enable `fuse=1` LXC feature (for `rclone`/`mergerfs`/AppImages) |
| `--with-wolf-input` | off | Configure `uinput`/`uhid` host modules, udev rules, and LXC input mounts for Wolf/Purah |
| `--with-zeroconf` | on | Enable `docktun-zeroconf` service (mDNS `*.local`, uses host port `80`; enabled by default) |
| `--no-zeroconf`, `--without-zeroconf` | off | Disable `docktun-zeroconf` mDNS service (leaves host port `80` free for reverse proxies) |
| `--allow-unconfined-apparmor` | off | Opt-in escape hatch setting `lxc.apparmor.profile: unconfined` (keep off unless host `lxc-pve` cannot be updated for CVE-2025-52881) |
| `--template-hygiene` | off | Remove `/etc/ssh/ssh_host_*` and truncate `/etc/machine-id` after provisioning |
| `--domain <hostname>` | `aio.example.com` | Domain placeholder for AIOStreams `BASE_URL` and Nginx example |
| `-h`, `--help` | — | Show help message and exit |

---

## Architecture & Hardening Details

### 1. Proxmox Host Preparation
- **APT Components**: Idempotently enables `non-free` and `non-free-firmware` in both deb822 `.sources` (PVE 9 / Debian 13) and legacy `.list` (PVE 8 / Debian 12) files (backing up `.bak` once; leaving Proxmox/third-party repos untouched).
- **Kernel Modules**: Persists `tun`, `wireguard`, `ip_tables`, `iptable_filter`, `iptable_nat`, `xt_conntrack`, `xt_MASQUERADE`, and `nf_nat` in `/etc/modules-load.d/media-lxc-net.conf`.
- **Host Global Sysctls**: Writes `/etc/sysctl.d/99-media-lxc.conf` (`vm.overcommit_memory=1`, `fs.inotify.max_user_watches=524288`, `fs.inotify.max_user_instances=1024`, `net.core.somaxconn=4096`) where global kernel parameters actually work outside unprivileged user namespaces.

### 2. Unprivileged LXC + Secret-Safe Payload Streaming
- Creates `--unprivileged 1` with `--features keyctl=1,nesting=1` (plus `,fuse=1` if `--with-fuse`), `firewall=1`, `--onboot 1`.
- Never uses insecure default passwords (`testing`) and keeps AppArmor confined by default.
- Streams the inner provisioning payload via `pct exec "$CTID" -- bash -s` using a `printf '%q'` stdin preamble so secrets (`DOCKTUN_ADMIN_PASSWORD`, `SSH_PUBKEY`) never appear on host `ps aux` / `/proc/<pid>/cmdline` or in temporary container files.

### 3. Security Hardening Inside the LXC
- **OpenSSH First-Match-Wins**: Writes `/etc/ssh/sshd_config.d/00-hardening.conf` (evaluated before cloud-init `50-*`/`60-*` drop-ins), runs `sshd -t` and logs `sshd -T`, and only sets `PasswordAuthentication no` when an authorized SSH key exists.
- **UFW + `DOCKER-USER` Chain**: Enables UFW (`default deny incoming`, `22/tcp`, LAN-scoped `DOCKTUN_PORT/tcp`, `51820/udp` WireGuard, plus LAN-scoped `5353/udp` mDNS and `80/tcp` when `WITH_ZEROCONF=1`) and injects a managed `DOCKER-USER` block into `/etc/ufw/after.rules` so Docker's NAT bypass cannot expose `DOCKTUN_PORT` outside `--lan-subnets`.
- **Fail2ban**: Configures `/etc/fail2ban/jail.d/99-sshd.local` with `backend = systemd` and `banaction = nftables-multiport` for modern Debian/Ubuntu `journald` + `nftables` hosts.
- **Unattended Upgrades & Boot Speed**: Installs `/etc/apt/apt.conf.d/52unattended-upgrades-local` blacklisting `^nvidia-` and `^libnvidia-`, and adds a `systemd-networkd-wait-online.service` drop-in (`--timeout=5 --any`) to prevent 120s boot hangs.
- **LXC Net-NS Sysctls**: Writes `net.ipv4.ip_forward=1` and `net.ipv4.conf.all.src_valid_mark=1` to `/etc/sysctl.d/99-lxc-media.conf`.

### 4. Docker CE, ZFS Guard & CVE-2025-52881 Smoke Test
- Installs official Docker CE + `docker-compose-plugin`.
- Merges `/etc/docker/daemon.json` via `jq` (`nofile=65536`, `json-file` log rotation, `live-restore: true`, `"storage-driver": "overlay2"` on PVE 8.1+/9 with `fuse-overlayfs` fallback).
- Configures `/etc/containerd/config.toml` `disabled_plugins` (single-quote aware) to disable unused `zfs`/`btrfs`/`aufs`/`devmapper` snapshotters that stall inside ZFS-backed LXCs.
- Runs a `docker run --rm hello-world` smoke test and specifically detects the CVE-2025-52881 `ip_unprivileged_port_start` AppArmor error, instructing you to update `lxc-pve` on the Proxmox host (or use `--allow-unconfined-apparmor` as a temporary opt-in fallback).

---

## Shared GPU Guide

> **Golden Rule:** Never bind a GPU to `vfio-pci` when sharing across LXCs. The Proxmox host owns the kernel driver and exposes device nodes concurrently to multiple unprivileged LXCs.

### NVIDIA
- **Host**: Blacklists `nouveau`, installs `proxmox-default-headers` (+ exact kernel headers if needed), installs `nvidia-driver-cuda` + `nvidia-kernel-dkms` + `nvidia-persistenced`, sets `options nvidia-drm modeset=1`, and installs `nvidia-lxc-prestart.service` (`Before=pve-guests.service`) so `/dev/nvidia-uvm*` and `/dev/nvidia-caps/*` exist before `onboot: 1` LXCs start. *(Reboot the Proxmox host after initial driver installation.)*
- **LXC Config**: Resolves `/dev/nvidia<N>` from `--pci-slot` (never hardcodes `/dev/nvidia0`), dynamically detects major numbers via `stat -L -c '%t'`, binds `/dev/nvidia*` + `/dev/dri`, and attaches `/usr/local/libexec/nvidia-lxc-prestart.sh` as `lxc.hook.pre-start`.
- **Inside LXC**: Matches userspace libraries (`compute`, `utils`, `encode`, `decode`) to host `/proc/driver/nvidia/version`, masks `nvidia-persistenced`, blacklists kernel modules inside LXC, pins packages with `apt-mark hold`, sets `nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true`, runs `nvidia-ctk runtime configure --runtime=docker` (leaving `runc` default), and generates `/etc/cdi/nvidia.yaml` with `features.cdi=true`.

### Intel iGPU / Arc
- Detects `i915` vs `xe` kernel driver (`xe` handles GuC/HuC natively; does not blindly force `i915 enable_guc=3`), installs `intel-gpu-tools` on host.
- Passes `/dev/dri/*` via Proxmox `dev[n]` with dynamic container `video`/`render` GIDs (or preserves `/dev/dri/by-path/pci-<slot>-render` & `-card` when `--pci-slot` is specified), installs VA-API drivers (`intel-media-va-driver-non-free` + `vainfo`), and verifies `vainfo --display drm`.

### AMD Radeon / APU
- Skips `firmware-amd-graphics` when `pve-firmware` / Proxmox VE is detected (since `pve-firmware` already bundles AMD GPU firmware and conflicts with `firmware-amd-graphics`, which would attempt to remove `proxmox-ve`); passes `/dev/dri/*` and `/dev/kfd` (when present) into the LXC with `render`/`video` group permissions and `mesa-va-drivers`.

### Wolf / Purah (Optional)
- Pass `--with-wolf-input` (or enable it during `[c]ustomize`) to configure `uinput`/`uhid`, `/etc/udev/rules.d/85-wolf-virtual-inputs.rules` (`seat9`), and `/dev/uinput`, `/dev/uhid`, `/dev/input`, `/run/udev` passthrough.

---

## What Wolf Can Do (Cloud Gaming & Virtual Desktop Streaming)

[Wolf](https://github.com/games-on-whales/wolf) (and setups built on it like [Purah](https://github.com/redtronguy/purah)) turns this shared-GPU LXC into a self-hosted **Moonlight-compatible cloud gaming and virtual desktop server** inside Docker:

- **Headless Virtual Displays (No Monitor or HDMI Dummy Plug)**: Wolf uses an embedded **Wayland / Sway / Gamescope** compositor inside Docker to create virtual displays on demand at whatever resolution, refresh rate, and HDR profile your Moonlight client requests—no physical display or HDMI/DP dummy plug required.
- **Multi-User Isolated Sibling Containers**: Each paired Moonlight client can launch isolated sibling containers managed via `/var/run/docker.sock`, including **Steam Big Picture**, **RetroArch**, **Lutris / Heroic**, full **desktop/browser** sessions, and the **Wolf Den** web management UI—all running concurrently with per-user state.
- **Hardware Encoding & 3D Acceleration Over Shared GPU**:
  - **NVIDIA**: NVENC hardware video encoding + Vulkan/OpenGL 3D acceleration via `nvidia-container-toolkit` (`--gpus all` / CDI).
  - **Intel iGPU / Arc**: QSV and VA-API acceleration (`iHD` driver; on Intel Arc Alchemist/Battlemage, prefer `vah264lpenc` / `vah265lpenc` low-power VA-API encoders).
  - **AMD Radeon / APU**: VA-API (`radeonsi`) hardware encoding and Mesa Vulkan/OpenGL 3D acceleration over `/dev/dri/*` (and `/dev/kfd` where applicable).
- **Virtual Gamepads & Input Passthrough (`--with-wolf-input`)**: Creates virtual **Xbox, DualSense, and Nintendo Switch** controllers plus virtual mouse and keyboard devices via host `uinput` and `uhid` kernel modules, `/dev/input`, `/run/udev`, and `/etc/udev/rules.d/85-wolf-virtual-inputs.rules` (which assigns Wolf virtual inputs to `seat9` so they never hijack host TTYs).
- **Key Operational Caveats**:
  - **`HOST_APPS_STATE_FOLDER` Path Parity**: Mount `HOST_APPS_STATE_FOLDER` at the **exact same path inside and outside** the Wolf container (e.g. `/opt/wolf:/opt/wolf`), because Wolf passes host paths directly to the Docker daemon when spawning sibling app containers.
  - **Port `8080` Collision (Wolf Den vs. DockTun)**: **Wolf Den** defaults to port `8080`. Deploy DockTun with `--docktun-port 8081` (or let `proxmox-lxc.sh` auto-bump when `8080` is busy) so both dashboards coexist cleanly.
  - **Moonlight Host-Network Ports**: Wolf runs with `network_mode: host` and requires **`47984/tcp`**, **`47989/tcp`**, **`48010/tcp`**, **`47998-48200/udp`**, plus mDNS **`5353/udp`** for local Moonlight discovery.
  - **Trusted Network Only (`docker.sock`)**: Because Wolf mounts `/var/run/docker.sock` to spawn sibling containers, keep Wolf and Wolf Den strictly on your trusted LAN, **Tailscale**, or **WireGuard** VPN—never expose them directly to the public internet.

---

## DockTun + AIOStreams Port & Reverse Proxy Notes

- **Compose Alignment**: Preserves any existing `compose.yml` / `compose.yaml` / `docker-compose.yml` / `docker-compose.yaml` in `DOCKTUN_DIR` (just like `docktun/install.sh`). When creating `/opt/docktun/compose.yml`, sets `SELF_PATH: /opt/projects/docktun`, `TAILSCALE_NETWORK: docktun`, `PORT: ${DOCKTUN_PORT}`, `DOCKER_NETWORK: docktun`, `TAILSCALE_STATE_VOLUME: docktun-tailscale-state`, `WIREGUARD_PORT: 51820`, `DB_PATH: /app/data/app.db`, `CLOUDFLARED_*`, and `networks.docktun.aliases`. `docktun-zeroconf` (`http://docktun.local`, host port `80` + mDNS `5353/udp`) is **enabled by default** (`WITH_ZEROCONF=1`); if port `80` is already bound by another non-`docktun-zeroconf` service inside the LXC, `proxmox-lxc.sh` warns and automatically falls back to core `docktun` only (`--no-zeroconf` / `--without-zeroconf` opts out explicitly so port `80` stays free for reverse proxies).
- **Port `8080` Collision Note**: DockTun defaults to `8080/tcp`. AIOStreams' `stremthru` also defaults to `8080` if bound to `0.0.0.0`. Keep `INTERNAL_BIND_IP=127.0.0.1` (default in `.env.generated`/`.env.template`) or set `--docktun-port 8081`.
- **AIOStreams Readiness (`/opt/docktun/projects/aiostreams`)**:
  - Generates a non-clobbering 64-char hex `SECRET_KEY` in `.env.generated` (`0600`) and `.env.template` (`0644`), with `NODE_OPTIONS=--dns-result-order=ipv4first` and `INTERNAL_BIND_IP=127.0.0.1`.
  - Writes `nginx-proxy-streaming.conf.example` preserving **HTTP `206 Partial Content`** byte-ranges, `proxy_buffering off;`, `proxy_read_timeout 3600s;`, and WebSockets.
  - Installs executable `check-readiness.sh` to verify `/dev/net/tun`, Docker/Compose, `/dev/shm`, `nofile=65536`, DNS, GPU (`nvidia-smi` / `vainfo --display drm`), DockTun `/healthz`, and port collisions.
