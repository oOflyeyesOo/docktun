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
   - Default network bridge (`vmbr*`, SDN `vnet*`, or OVS bridge)
   - Host GPUs via `lspci` with PCI class & word-boundary matching (so `NVIDIA Corporation` / `Intel Corporation` never false-match `ATI`), plus Intel `i915` vs `xe` kernel driver detection
   - Existing host SSH public keys (`/root/.ssh/id_ed25519.pub`, `/root/.ssh/id_rsa.pub`, `/root/.ssh/authorized_keys`, `/root/.ssh/*.pub`)
   - Whether host preparation is already configured or still needed (runs automatically for new LXC creation when needed unless `--skip-host-prep` is set; skipped by default in `--provision-only` and `--doctor`)
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
| `--vlan <tag>` | empty | Optional VLAN tag (`1`–`4094`) appended to `net0` |
| `--mtu <bytes>` | empty | Optional MTU (`576`–`65535`) appended to `net0` |
| `--ip <cidr\|dhcp>` | `dhcp` | IPv4 CIDR (e.g. `192.168.1.50/24`) or `dhcp` |
| `--gw <ip>` | empty | IPv4 gateway (used with static `--ip`) |
| `--dns <ip>` | `1.1.1.1` | Container DNS nameserver(s) (space-separated) |
| `--timezone <tz>` | host timezone | IANA timezone configured inside LXC (e.g. `America/New_York`) |
| `--ssh-pubkey-file <path>` | auto (`/root/.ssh/*.pub`) | Path to SSH public key file to authorize |
| `--ssh-pubkey <key>` | empty | Inline SSH public key string to authorize |
| `--admin-user <name>` | `mediaadmin` | Non-root `sudo`/`docker`/`video`/`render` admin username |
| `--gpu <mode>` | `auto` | Shared GPU passthrough mode: `auto`, `nvidia`, `intel`, `amd`, `all`, `none` |
| `--pci-slot <slot>` | empty | Target PCI slot on multi-GPU hosts (e.g. `0000:01:00.0` or `01:00.0`) |
| `--docktun-port <port>` | `8080` | Host port mapped to DockTun `:3001` |
| `--docktun-dir <dir>` | `/opt/docktun` | DockTun installation directory inside LXC |
| `--docktun-admin-user <user>` | empty | Optional pre-seeded DockTun admin username (first DB init only) |
| `--docktun-admin-password <pw>` | empty | Optional pre-seeded DockTun admin password (12–72 bytes; first DB init only; triggers `0600` compose mode) |
| `--lan-subnets <cidrs>` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` | Comma-separated trusted IPv4/IPv6 LAN CIDRs allowed in UFW and `DOCKER-USER` |
| `--media-dir <host_path>` | empty | Optional host directory mounted at `/mnt/media` (newly created dirs `chown 101000:101000` for `ADMIN_USER` UID `1000`; existing dirs preserved unless `--chown-media-dir`) |
| `--chown-media-dir` | off | `chown 101000:101000` (`ADMIN_USER` UID `1000`; vs `100000:100000` LXC `root`) on an existing `--media-dir` (warns gracefully on `root_squash` NFS; rejects protected system paths) |
| `--mount <host:ct>` | empty | Repeatable extra bind mount (`<host_path>:<ct_path>`, e.g. `/mnt/dl:/mnt/downloads`; normalized and rejects protected OS/`--docktun-dir` paths) |
| `--startup <spec>` | empty | Optional Proxmox startup order passed to `pct create --startup` (comma-separated `order=<uint>`, `up=<uint>`, `down=<uint>` in any order) |
| `--aio-compose <host_file>` | empty | Optional host `.yml`/`.yaml` file pre-staged as `${DOCKTUN_DIR}/projects/aiostreams/compose.yml` if absent (mutually exclusive with `--aio-repo`) |
| `--aio-repo <git_url>` | empty | Optional safe HTTPS/SSH Git repository cloned non-clobberingly into `${DOCKTUN_DIR}/projects/aiostreams` if absent (mutually exclusive with `--aio-compose`) |
| `--aio-branch <ref>` | empty | Optional Git branch or tag to clone with `--aio-repo` |
| `--with-fuse` | off | Enable `fuse=1` LXC feature (for `rclone`/`mergerfs`/AppImages) |
| `--with-wolf-input` | off | Configure `uinput`/`uhid` host modules, udev rules (`MODE="0666"` virtual inputs), and LXC input mounts (`dev[n]` on PVE 8.2+, `/run/udev` `ro`) for Wolf/Purah |
| `--with-zeroconf` | on | Enable `docktun-zeroconf` service (mDNS `*.local`, uses host port `80`; enabled by default) |
| `--no-zeroconf`, `--without-zeroconf` | off | Disable `docktun-zeroconf` mDNS service (leaves host port `80` free for reverse proxies or secondary LXCs) |
| `--allow-unconfined-apparmor` | off | Opt-in escape hatch setting `lxc.apparmor.profile: unconfined` (keep off unless host `lxc-pve` cannot be updated for CVE-2025-52881) |
| `--template-hygiene` | off | Install `regenerate-ssh-host-keys.service` + `docktun-firstboot.sh`/`.service` (regenerates `.env.generated` on clone boot), strip live state & `machine-id` (forbids `--docktun-admin-password`) |
| `--cleanup-on-failure` | off | Automatically `pct stop`/`pct unlock` + `pct destroy --purge` a newly created LXC (and its prestart hook) if provisioning fails mid-run |
| `--provision-only <ctid>` | empty | Run/re-run inner LXC provisioning on an existing unprivileged CTID (derives GPU from `pct config` when `--gpu auto`, skips host prep) |
| `--doctor <ctid>` | empty | Inspect an existing LXC (`pct config`, `/dev/net/tun`, GPU/input permissions, `check-readiness.sh`, UFW/`DOCKER-USER`, actual DockTun port & `/healthz`) |
| `--list` | off | List `docktun`-tagged Proxmox LXCs with status, name, IP, and tags |
| `--destroy <ctid>` | empty | Stop, unlock, and purge an existing LXC plus its per-CTID NVIDIA prestart hook |
| `--domain <hostname>` | `aio.example.com` | Domain placeholder for AIOStreams `BASE_URL` and Nginx example |
| `-h`, `--help` | — | Show help message and exit |

---

## Architecture & Hardening Details

### 1. Proxmox Host Preparation
- **APT Components**: Idempotently enables `non-free` and `non-free-firmware` in both deb822 `.sources` (PVE 9 / Debian 13, including folded multi-line fields and inline `#` comments) and legacy `.list` (PVE 8 / Debian 12) across `*.debian.org` mirrors (excluding `proxmox.com`), storing backups under `/var/backups/proxmox-lxc-apt/` so `apt` never warns about `.bak` files in `/etc/apt/sources.list.d/`.
- **Kernel Modules**: Persists `tun`, `wireguard`, `br_netfilter`, `ip_tables`, `iptable_filter`, `iptable_nat`, `xt_conntrack`, `xt_MASQUERADE`, and `nf_nat` in `/etc/modules-load.d/media-lxc-net.conf`.
- **Host Global Sysctls**: Writes `/etc/sysctl.d/99-media-lxc.conf` (`vm.overcommit_memory=1`, `fs.inotify.max_user_watches=524288`, `fs.inotify.max_user_instances=1024`, `net.core.somaxconn=4096`) where global kernel parameters actually work outside unprivileged user namespaces.

### 2. Unprivileged LXC + Secret-Safe Payload Streaming
- Creates `--unprivileged 1` with `--features keyctl=1,nesting=1` (plus `,fuse=1` if `--with-fuse`), `firewall=1`, `--onboot 1`, optional `--startup` / `--vlan` / `--mtu`, `--timezone` (traversal-checked), and Linux or Open vSwitch (`OVSBridge`) bridges.
- Runs fast pre-flight host checks (DNS/HTTPS reachability with APT proxy detection via `apt-config dump`, active `pve-enterprise` repo without subscription including `Enabled: false`/`0` & multi-line `URIs:`, storage capacity vs `--disk`, early `/dev/shm < 512 MiB` warning when `--memory < 1024`, and missing `--media-dir`/`--mount` host directory warnings; offline-tolerant for `--host-prep-only --gpu none`).
- Never uses insecure default passwords (`testing`), validates `--docktun-admin-password` to 12–72 UTF-8 bytes (matching DockTun `auth.go`), rejects path traversal (`..`) and control characters in paths/usernames, and keeps AppArmor confined by default.
- Waits for systemd and non-loopback IPv4 readiness before streaming the inner provisioning payload via `pct exec "$CTID" -- bash -s` using a `printf '%q'` stdin preamble so secrets (`DOCKTUN_ADMIN_PASSWORD`, `SSH_PUBKEY`) never appear on host `ps aux` / `/proc/<pid>/cmdline` or in temporary container files.
- Writes non-secret Proxmox Web UI summary notes (`pct set "$CTID" --description ...` with live detected DockTun port, accurate mDNS ZeroConf status/fallback, `ADMIN_USER`, GPU mode, AIOStreams directory, and readiness/setup-token commands—omitting `setup.token` when `--docktun-admin-password` pre-seeds auth and emitting clone-generic notes without temporary CTID/IP when `--template-hygiene` is active) and prints a **Post-Install Cheat Sheet** at completion. Supports `--cleanup-on-failure` (with `INT`/`TERM` traps & `pct unlock` fallback), `--provision-only <ctid>`, `--doctor <ctid>`, `--list`, and `--destroy <ctid>`.

### 3. Security Hardening Inside the LXC
- **OpenSSH First-Match-Wins**: Writes `/etc/ssh/sshd_config.d/00-hardening.conf` (evaluated before cloud-init `50-*`/`60-*` drop-ins), supports OpenSSH key options (`from="..." ssh-ed25519 ...`) and Ubuntu 24.04 `ssh.socket`, sets `PermitRootLogin no` + `AllowUsers <admin_user>` when an authorized SSH key is present (or warns to run `pct enter <ctid>` + `passwd <admin_user>` when no SSH key is present), and verifies `sshd -t` / `sshd -T` assertions.
- **UFW + `DOCKER-USER` Chain**: Validates IPv4/IPv6 CIDRs in `--lan-subnets` (with strict IPv6 group counting), enables UFW (`default deny incoming`, `22/tcp`, LAN-scoped `DOCKTUN_PORT/tcp`, `51820/udp` WireGuard, plus LAN-scoped `5353/udp` mDNS and `80/tcp` when `WITH_ZEROCONF=1`), and appends a standalone `*filter ... COMMIT` `DOCKER-USER` block at the end of `/etc/ufw/after.rules` (and `/etc/ufw/after6.rules` when `IPV6 != no` and `ip6tables` / `ip6tables-restore --test` succeed). The `DOCKER-USER` chain allows `RELATED,ESTABLISHED`, `docker0`/`br-+`, trusted `LAN_SUBNETS` (so AIOStreams `3001`/`4005`/`8088`, Reverse Proxy `80`/`443`, DockTun, and Wolf work on LAN), `51820/udp`, and jumps to `ufw-user-forward` (`ufw6-user-forward`) before dropping `NEW` external `eth+` ingress (plus non-`eth` default-route interfaces like `ens18` via IPv4/IPv6 default route detection, excluding `docker0`/`br-`/`veth`/`wg`/`tailscale`/`lo`).
- **Fail2ban**: Configures `/etc/fail2ban/jail.d/99-sshd.local` with `ignoreip`, `backend = systemd` (falling back to `rsyslog` + `backend = auto` if `python3-systemd` is absent), and `banaction = nftables-multiport` (`iptables-multiport` fallback if `nft` is absent).
- **Unattended Upgrades, Journald & Boot Speed**: Pre-seeds `unattended-upgrades` debconf before configuring `/etc/apt/apt.conf.d/20auto-upgrades` and `/etc/apt/apt.conf.d/52unattended-upgrades-local` (`^nvidia-`, `^libnvidia-`, `^libcuda`, `^libnvcuvid`, `^cuda-`), caps journald at `SystemMaxUse=200M`, and adds a `systemd-networkd-wait-online.service` drop-in (`--timeout=5 --any`).
- **LXC Limits & Net-NS Sysctls**: Writes `/etc/security/limits.d/99-lxc-media.conf` (`* soft/hard nofile 65536` and `root soft/hard nofile 65536`) for host-LXC services, and writes `net.ipv4.ip_forward=1`, `net.ipv4.conf.all.src_valid_mark=1`, and `net.ipv4.conf.all.rp_filter=2` to `/etc/sysctl.d/99-lxc-media.conf` (applied non-fatally).

### 4. Docker CE, ZFS Guard & CVE-2025-52881 Smoke Test
- Installs official Docker CE + `docker-compose-plugin` plus a weekly `docktun-docker-prune.timer` / `docktun-docker-prune.service` (`docker image prune -af --filter "until=168h"` and `docker builder prune -af --filter "until=168h"` — never pruning volumes).
- Merges `/etc/docker/daemon.json` via `jq` (`nofile=65536`, `json-file` log rotation, `live-restore: true`, `userland-proxy: false`, `icc: false`, `"default-address-pools": [{"base": "172.24.0.0/14", "size": 24}]` when unset to avoid `172.16.0.0/12` / `192.168.0.0/16` subnet collisions, and `"storage-driver": "overlay2"` on PVE 8.1+/9 with `fuse-overlayfs` installed before start on older ZFS PVE).
  - **`userland-proxy: false` & `127.0.0.1` vs `localhost`**: With `userland-proxy: false`, Docker uses pure `iptables` DNAT (no `docker-proxy` host sockets) and IPv4-only `route_localnet=1` for loopback-bound ports. Always use `127.0.0.1` (never `localhost`, which can resolve to IPv6 `::1` and fail with `Connection refused`) in `INTERNAL_BIND_IP` and local Nginx `proxy_pass` configs. Port checks in `proxmox-lxc.sh` and `check-readiness.sh` inspect `docker ps` and `iptables -t nat -S DOCKER` alongside `ss` so bridge-published ports are accurately detected.
- Configures `/etc/containerd/config.toml` `disabled_plugins` (handling single-line, multi-line, and single-quoted TOML arrays) to disable unused `zfs`/`btrfs`/`aufs`/`devmapper` snapshotters and validates via `containerd --config ... config dump`.
- Runs a `docker run --rm hello-world` smoke test and specifically detects the CVE-2025-52881 `ip_unprivileged_port_start` AppArmor error, instructing you to update `lxc-pve` on the Proxmox host (or use `--allow-unconfined-apparmor` as a temporary opt-in fallback).

---

## Shared GPU Guide

> **Golden Rule:** Never bind a GPU to `vfio-pci` when sharing across LXCs. The Proxmox host owns the kernel driver and exposes device nodes concurrently to multiple unprivileged LXCs.

### NVIDIA
- **Host**: Blacklists `nouveau`, installs `proxmox-default-headers` (+ exact kernel headers if needed), installs `nvidia-driver-cuda` + `nvidia-kernel-dkms` + `nvidia-persistenced` (with `dkms status` check), installs `/etc/udev/rules.d/70-nvidia-lxc.rules` (`MODE="0666"` for `nvidia-caps*`), updates initramfs across all kernels (`-k all`), sets `options nvidia-drm modeset=1`, and installs `nvidia-lxc-prestart.service` (`Before=pve-guests.service`). If `/proc/driver/nvidia/version` is not yet active after host prep, the script aborts cleanly before `pct create` with reboot instructions so no orphan LXC is left behind.
- **LXC Config**: Resolves `/dev/nvidia<N>` from `--pci-slot` (or all NVIDIA GPUs when unset), dynamically detects major numbers (with `/proc/devices` fallback for both `nvidia-uvm` and `nvidia-caps`), isolates DRI nodes to `--pci-slot` via `dev[n]` on PVE 8.2+ (guarding `c 226:*` to PVE `< 8.2`), and attaches a per-CTID `/usr/local/libexec/nvidia-lxc-prestart-<ctid>.sh` as `lxc.hook.pre-start`.
- **Inside LXC**: Supports both proprietary and Open Kernel Module `/proc/driver/nvidia/version` strings, pins exact host-matched userspace packages (`--allow-downgrades`), masks `nvidia-persistenced`, blacklists kernel modules inside LXC, holds packages (`nvidia-*`, `libnvidia-*`, `libcuda*`, `libnvcuvid*`) with `apt-mark hold`, sets `nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true`, runs `nvidia-ctk runtime configure --runtime=docker`, gates `features.cdi=true` on successful `/etc/cdi/nvidia.yaml` generation, and runs the GPU smoke test with `NVIDIA_DISABLE_REQUIRE=1`.

### Intel iGPU / Arc
- Detects `i915` vs `xe` kernel driver (`xe` handles GuC/HuC natively; does not blindly force `i915 enable_guc=3`), installs `intel-gpu-tools` on host.
- On PVE 8.2+, resolves `/dev/dri/by-path/pci-<slot>-{render,card}` via `readlink -f` and attaches them via Proxmox `dev[n]` with dynamic container `video`/`render` GIDs (avoiding `nobody:nogroup` bind mounts). Inside Debian LXCs, enables `non-free`/`non-free-firmware`, installs `intel-media-va-driver-non-free` + `vainfo`, and verifies `vainfo --display drm` as `$ADMIN_USER`.

### AMD Radeon / APU
- Skips `firmware-amd-graphics` when `pve-firmware` / Proxmox VE is detected (since `pve-firmware` already bundles AMD GPU firmware and conflicts with `firmware-amd-graphics`, which would attempt to remove `proxmox-ve`); passes `/dev/dri/*` and `/dev/kfd` (when present) via Proxmox `dev[n]` with `render`/`video` group permissions and `mesa-va-drivers`.

### Wolf / Purah (Optional)
- Pass `--with-wolf-input` (or enable it during `[c]ustomize`) to configure `uinput`/`uhid`, `/etc/udev/rules.d/85-wolf-virtual-inputs.rules` (`MODE="0666"` for virtual inputs & `Wolf` hidraw + `seat9`), and `/dev/uinput`, `/dev/uhid` (via Proxmox `dev[n]` with container `input` GID on PVE 8.2+), `/dev/input`, `/run/udev` (mounted read-only `ro`) passthrough.

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
  - **Port `8080` Collision (Wolf Den vs. DockTun)**: **Wolf Den** defaults to port `8080`. Deploy DockTun with `--docktun-port 8081` inside a shared LXC so both dashboards coexist cleanly.
  - **Moonlight Host-Network Ports**: Wolf runs with `network_mode: host` and requires **`47984/tcp`**, **`47989/tcp`**, **`48010/tcp`**, **`47998-48200/udp`**, plus mDNS **`5353/udp`** for local Moonlight discovery.
  - **Trusted Network Only (`docker.sock`)**: Because Wolf mounts `/var/run/docker.sock` to spawn sibling containers, keep Wolf and Wolf Den strictly on your trusted LAN, **Tailscale**, or **WireGuard** VPN—never expose them directly to the public internet.

---

## DockTun + AIOStreams Port & Reverse Proxy Notes

- **Compose Alignment**: Preserves any existing `compose.yml` / `compose.yaml` / `docker-compose.yml` / `docker-compose.yaml` in `DOCKTUN_DIR` (just like `docktun/install.sh`). When creating `/opt/docktun/compose.yml`, sets `SELF_PATH: /opt/projects/docktun`, `TAILSCALE_NETWORK: docktun`, `PORT: ${DOCKTUN_PORT}`, `DOCKER_NETWORK: docktun`, `TAILSCALE_STATE_VOLUME: docktun-tailscale-state`, `WIREGUARD_PORT: 51820`, `DB_PATH: /app/data/app.db`, `CLOUDFLARED_*`, and `networks.docktun.aliases`. `docktun-zeroconf` (`http://docktun.local`, host port `80` + mDNS `5353/udp`) is **enabled by default** (`WITH_ZEROCONF=1`); the host script warns if `docktun.local` already responds on the LAN (pass `--no-zeroconf` on secondary LXCs), and if port `80` is already bound inside the LXC, `proxmox-lxc.sh` warns and automatically falls back to core `docktun` only (`--no-zeroconf` / `--without-zeroconf` opts out explicitly so port `80` stays free for reverse proxies).
- **Port `8080` Collision Note**: DockTun defaults to `8080/tcp`. AIOStreams' `stremthru` also defaults to `8080` if bound to `0.0.0.0`. Keep `INTERNAL_BIND_IP=127.0.0.1` (default in `.env.generated`/`.env.template`) or set `--docktun-port 8081`.
- **AIOStreams Readiness (`/opt/docktun/projects/aiostreams`)**:
  - Supports pre-staging `--aio-compose <host_file>` (validated with `docker compose config --quiet` inside the LXC before moving into `compose.yml`) or cloning `--aio-repo <git_url>` (with optional `--aio-branch <ref>` and `git config --system --add safe.directory`) non-clobberingly when no existing Git repo or compose file is present.
  - Generates a non-clobbering 64-char hex `SECRET_KEY` in `.env.generated` (`0600`, owned by `$ADMIN_USER`) and keeps `SECRET_KEY=REPLACE_WITH_64_HEX_CHARS` in `.env.template` (`0644`), with `NODE_OPTIONS=--dns-result-order=ipv4first` and `INTERNAL_BIND_IP=127.0.0.1`.
  - Writes `nginx-proxy-streaming.conf.example` (`listen 443 ssl; http2 on;` for Nginx >= 1.25.1 / Debian 13 / Ubuntu 24.04, with a comment noting `listen 443 ssl http2;` for Debian 12 Nginx 1.22, plus `http://aiostreams:3001` Docker network note) preserving **HTTP `206 Partial Content`** byte-ranges, `proxy_buffering off;`, `proxy_read_timeout 3600s;`, and WebSockets.
  - Installs executable `check-readiness.sh` to verify `/dev/net/tun`, Docker/Compose, `/dev/shm`, `nofile=65536`, DNS, GPU (`nvidia-smi` / `vainfo --display drm`), UFW + `DOCKER-USER` (printing `[SKIP]` when run non-root), DockTun `/healthz`, and port collisions.
  - Under `--template-hygiene`, also runs `apt-get clean` and clears `/var/lib/apt/lists/*` and `/var/lib/docker/engine-id` alongside `machine-id`, `random-seed`, `ssh_host_*`, and live DockTun volumes.
