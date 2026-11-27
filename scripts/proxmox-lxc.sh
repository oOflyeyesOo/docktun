#!/usr/bin/env bash
# Single-file Proxmox VE 8/9 host prep & hardened unprivileged LXC provisioner
# with shared GPU passthrough (NVIDIA/Intel/AMD), Docker CE + ZFS guard,
# pre-installed DockTun, and AIOStreams readiness.
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"
NON_INTERACTIVE="${NON_INTERACTIVE:-0}"
SKIP_HOST_PREP="${SKIP_HOST_PREP:-0}"
HOST_PREP_ONLY="${HOST_PREP_ONLY:-0}"
PROVISION_ONLY="${PROVISION_ONLY:-0}"
DOCTOR_MODE="${DOCTOR_MODE:-0}"
LIST_MODE="${LIST_MODE:-0}"
DESTROY_MODE="${DESTROY_MODE:-0}"
CLEANUP_ON_FAILURE="${CLEANUP_ON_FAILURE:-0}"
CHOWN_MEDIA_DIR="${CHOWN_MEDIA_DIR:-0}"

# Provisioning defaults (auto-detected values fill empty fields before prompts/execution)
CTID=""
HOSTNAME="media-lxc"
OS_CHOICE="debian12"
STORAGE=""
TEMPLATE_STORAGE=""
CORES="4"
MEMORY="4096"
SWAP="2048"
DISK="32"
BRIDGE=""
VLAN_TAG=""
MTU_BYTES=""
IP_ADDR="dhcp"
GW_ADDR=""
DNS_ADDR="1.1.1.1"
SSH_PUBKEY_FILE=""
SSH_PUBKEY=""
ADMIN_USER="mediaadmin"
GPU_MODE="auto"
PCI_SLOT=""
DOCKTUN_PORT="8080"
DOCKTUN_DIR="/opt/docktun"
DOCKTUN_IMAGE="${DOCKTUN_IMAGE:-ghcr.io/ooflyeyesoo/docktun:latest}"
DOCKTUN_ADMIN_USER=""
DOCKTUN_ADMIN_PASSWORD=""
LAN_SUBNETS="10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
MEDIA_DIR=""
declare -a EXTRA_MOUNTS=()
WITH_FUSE=0
WITH_WOLF_INPUT=0
WITH_ZEROCONF=1
ALLOW_UNCONFINED_APPARMOR=0
TEMPLATE_HYGIENE=0
STARTUP_SPEC=""
AIO_COMPOSE_FILE=""
AIO_REPO_URL=""
AIO_REPO_BRANCH=""
AIO_DOMAIN="aio.example.com"
AIO_TIMEZONE="${AIO_TIMEZONE:-}"
NVIDIA_SMOKE_IMAGE="${NVIDIA_SMOKE_IMAGE:-nvidia/cuda:12.6.0-base-ubuntu24.04}"
DOCKTUN_HEALTH_TIMEOUT="${DOCKTUN_HEALTH_TIMEOUT:-60}"
HOST_NVIDIA_VERSION="${HOST_NVIDIA_VERSION:-}"
HOST_NVIDIA_PROC_VERSION_FILE="${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}"
HOST_APT_DIR="${HOST_APT_DIR:-/etc/apt}"
APT_BACKUP_DIR="${APT_BACKUP_DIR:-/var/backups/proxmox-lxc-apt}"

# Runtime state
DEV_IDX=0
MP_IDX=0
TMP_WORK_DIR=""
CREATED_CTID=""
PROVISION_SUCCEEDED=0
POST_BOOT_DEV_ADDED=0
GPUS_RESOLVED=0
PREFLIGHT_DONE=0
CLI_CTID=""
MODE_CTID=""
EFFECTIVE_ZEROCONF=""
EFFECTIVE_ZEROCONF_FALLBACK=""
declare -a CREATION_FLAGS_USED=()
LXC_OSTYPE="debian"
TPL_PATTERN="debian-12-standard_"
LXC_TEMPLATE=""
DETECTED_NVIDIA=0
DETECTED_INTEL=0
DETECTED_AMD=0
DETECTED_GPU_COUNT=0
DETECTED_INTEL_DRIVER=""
DO_NVIDIA=0
DO_INTEL=0
DO_AMD=0
HOST_PREP_NEEDED=1
RUN_HOST_PREP=1
OS_ID=""
OS_CODENAME=""
OS_VERSION_ID=""
PVE_MAJOR=""
PVE_MINOR=""

# Safe BASH_SOURCE check for bash -c "$(curl ...)" and curl | bash
SCRIPT_SOURCE="${BASH_SOURCE[0]:-}"

if [[ -z "${NO_COLOR:-}" ]] && [[ -t 1 || -t 2 ]]; then
  _CLR_RESET=$'\033[0m'
  _CLR_BOLD=$'\033[1m'
  _CLR_BLUE=$'\033[0;34m'
  _CLR_GREEN=$'\033[0;32m'
  _CLR_YELLOW=$'\033[0;33m'
  _CLR_RED=$'\033[0;31m'
  _CLR_CYAN=$'\033[0;36m'
else
  _CLR_RESET=""
  _CLR_BOLD=""
  _CLR_BLUE=""
  _CLR_GREEN=""
  _CLR_YELLOW=""
  _CLR_RED=""
  _CLR_CYAN=""
fi

log_info()    { printf '%s[INFO]%s %s\n' "${_CLR_BLUE}" "${_CLR_RESET}" "$*"; }
log_warn()    { printf '%s[WARN]%s %s\n' "${_CLR_YELLOW}" "${_CLR_RESET}" "$*" >&2; }
log_error()   { printf '%s[ERROR]%s %s\n' "${_CLR_RED}" "${_CLR_RESET}" "$*" >&2; }
log_success() { printf '%s[OK]%s %s\n' "${_CLR_GREEN}" "${_CLR_RESET}" "$*"; }

die() {
  log_error "$*"
  exit 1
}

destroy_ct_with_cleanup() {
  local ctid="${1:?destroy_ct_with_cleanup requires CTID}"
  pct stop "$ctid" >/dev/null 2>&1 || true
  if ! pct destroy "$ctid" --purge >/dev/null 2>&1; then
    pct unlock "$ctid" >/dev/null 2>&1 || true
    pct stop "$ctid" >/dev/null 2>&1 || true
    pct destroy "$ctid" --purge >/dev/null 2>&1 || return 1
  fi
  rm -f "/usr/local/libexec/nvidia-lxc-prestart-${ctid}.sh"
  return 0
}

cleanup() {
  if [[ -n "${TMP_WORK_DIR:-}" && -d "${TMP_WORK_DIR}" ]]; then
    rm -rf "${TMP_WORK_DIR}"
  fi
  if [[ -n "${CREATED_CTID:-}" && "${PROVISION_SUCCEEDED:-0}" != "1" ]]; then
    if (( CLEANUP_ON_FAILURE )); then
      log_warn "Provisioning failed mid-run; destroying partial LXC ${CREATED_CTID} (--cleanup-on-failure)..."
      if ! destroy_ct_with_cleanup "$CREATED_CTID"; then
        log_error "Failed to destroy partial LXC ${CREATED_CTID}; clean up manually with: pct unlock ${CREATED_CTID}; pct stop ${CREATED_CTID}; pct destroy ${CREATED_CTID} --purge"
      fi
    else
      local script_name="proxmox-lxc.sh"
      if [[ -n "${SCRIPT_SOURCE:-}" && "${SCRIPT_SOURCE}" != "bash" && "${SCRIPT_SOURCE}" != "-bash" ]]; then
        script_name="${SCRIPT_SOURCE##*/}"
      fi
      log_error "LXC ${CREATED_CTID} provisioning did not complete."
      log_error "To remove the partial container: pct stop ${CREATED_CTID} 2>/dev/null; pct destroy ${CREATED_CTID} --purge"
      log_error "Or to re-provision it: ${script_name} --provision-only ${CREATED_CTID}"
    fi
  fi
}

ensure_tmp_dir() {
  if [[ -z "${TMP_WORK_DIR:-}" ]]; then
    TMP_WORK_DIR="$(mktemp -d)"
  fi
}

require_root() {
  if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    die "This script must be run as root (EUID 0)."
  fi
}

has_controlling_tty() {
  [[ -c /dev/tty ]] && (: </dev/tty) 2>/dev/null
}

run_cmd() {
  if [[ "$#" -eq 0 ]]; then
    return 0
  fi
  if [[ "${DRY_RUN:-0}" == "1" ]]; then
    local rendered=""
    printf -v rendered '%q ' "$@"
    printf '%s[DRY-RUN]%s %s\n' "${_CLR_CYAN}" "${_CLR_RESET}" "${rendered% }"
    return 0
  fi
  "$@"
}

wait_for() {
  local timeout="${1:?wait_for requires timeout}"
  local desc="${2:?wait_for requires description}"
  shift 2
  if [[ "$#" -eq 0 ]]; then
    die "wait_for requires a command to poll"
  fi
  local elapsed=0
  log_info "Waiting up to ${timeout}s for ${desc}..."
  while (( elapsed < timeout )); do
    if "$@"; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  log_error "Timed out after ${timeout}s waiting for: ${desc}"
  return 1
}

detect_os() {
  local os_file="${OS_RELEASE_FILE:-/etc/os-release}"
  if [[ ! -r "$os_file" ]]; then
    return 1
  fi
  local id="" version_id="" version_codename="" ubuntu_codename="" key="" val=""
  while IFS='=' read -r key val || [[ -n "$key" ]]; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    val="${val%$'\r'}"
    val="${val#\"}"
    val="${val%\"}"
    val="${val#\'}"
    val="${val%\'}"
    case "$key" in
      ID) id="$val" ;;
      VERSION_ID) version_id="$val" ;;
      VERSION_CODENAME) version_codename="$val" ;;
      UBUNTU_CODENAME) ubuntu_codename="$val" ;;
    esac
  done < "$os_file"

  OS_ID="$id"
  OS_VERSION_ID="$version_id"
  OS_CODENAME="${version_codename:-$ubuntu_codename}"
  if [[ -z "$OS_CODENAME" ]] && command -v lsb_release >/dev/null 2>&1; then
    OS_CODENAME="$(lsb_release -cs 2>/dev/null || true)"
  fi
  [[ -n "$OS_ID" ]]
}

detect_pve_version() {
  PVE_MAJOR=""
  PVE_MINOR=""
  if ! command -v pveversion >/dev/null 2>&1; then
    return 1
  fi
  local out=""
  out="$(pveversion 2>/dev/null || true)"
  if [[ "$out" =~ pve-manager/([0-9]+)\.([0-9]+) ]]; then
    PVE_MAJOR="${BASH_REMATCH[1]}"
    PVE_MINOR="${BASH_REMATCH[2]}"
    return 0
  fi
  return 1
}

get_char_major() {
  local dev="${1:-}"
  if [[ -z "$dev" || ! -c "$dev" ]]; then
    return 1
  fi
  local hex_major=""
  hex_major="$(stat -L -c '%t' "$dev" 2>/dev/null || true)"
  if [[ ! "$hex_major" =~ ^[0-9a-fA-F]+$ ]]; then
    return 1
  fi
  printf '%d\n' "0x${hex_major}"
}

glob_matches() {
  local -n _gm_out="$1"
  shift
  _gm_out=()
  local pat="" m=""
  for pat in "$@"; do
    while IFS= read -r m; do
      if [[ -n "$m" ]]; then
        _gm_out+=("$m")
      fi
    done < <(compgen -G "$pat" || true)
  done
}

strip_pct_snapshots() {
  awk '/^\[/ && $0 != "[PENDING]" {exit} {print}' <<< "${1:-}"
}

usage() {
  cat <<'EOF'
Usage: proxmox-lxc.sh [OPTIONS]

Single-file Proxmox VE 8/9 host preparer and unprivileged LXC provisioner with
shared GPU passthrough (NVIDIA/Intel/AMD), security hardening, Docker CE + ZFS
guard, pre-installed DockTun, and AIOStreams readiness.

Quickstart:
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/oOflyeyesOo/docktun/main/scripts/proxmox-lxc.sh)"

Options:
  -y, --yes, --non-interactive    Skip interactive prompts; use detected defaults & flags
      --dry-run                   Preview planned commands and changes without modifying system
      --skip-host-prep            Skip Proxmox host preparation step
      --host-prep-only            Only prepare the Proxmox host (do not create an LXC)
      --ctid <id>                 LXC container ID (>= 100; default: next free cluster ID)
      --hostname <name>           Container hostname (default: media-lxc)
      --os <debian12|debian13|ubuntu2404>
                                  Container OS template (default: debian12)
      --storage <name>            Proxmox rootfs storage (default: auto rootdir)
      --template-storage <name>   Proxmox template storage (default: auto vztmpl)
      --cores <n>                 CPU cores (default: 4)
      --memory <mb>               Memory in MiB (default: 4096)
      --swap <mb>                 Swap in MiB (default: 2048)
      --disk <gb>                 Root disk size in GiB (default: 32)
      --bridge <vmbrX>            Proxmox network bridge (default: auto or vmbr0)
      --vlan <tag>                Optional VLAN tag (1-4094) for LXC net0
      --mtu <bytes>               Optional MTU (576-65535) for LXC net0
      --ip <cidr|dhcp>            IPv4 CIDR (e.g. 192.168.1.50/24) or dhcp (default: dhcp)
      --gw <ip>                   IPv4 gateway (used with static --ip)
      --dns <ip>                  DNS nameserver IP (default: 1.1.1.1)
      --timezone <tz>             IANA timezone inside LXC (default: host timezone)
      --ssh-pubkey-file <path>    Path to SSH public key file (auto-detected if present)
      --ssh-pubkey <key>          SSH public key string to authorize
      --admin-user <name>         Non-root sudo/docker user (default: mediaadmin)
      --gpu <auto|nvidia|intel|amd|all|none>
                                  Shared GPU mode (default: auto)
      --pci-slot <slot>           Target GPU PCI slot on multi-GPU hosts (e.g. 0000:01:00.0)
      --docktun-port <port>       DockTun dashboard port (default: 8080)
      --docktun-dir <dir>         DockTun directory inside LXC (default: /opt/docktun)
      --docktun-admin-user <user> Optional pre-seeded DockTun admin username
      --docktun-admin-password <pw>
                                  Optional pre-seeded DockTun admin password (12-72 bytes)
      --lan-subnets <cidrs>       Comma-separated trusted LAN CIDRs (IPv4/IPv6) for UFW/DOCKER-USER
                                  (default: 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16)
      --media-dir <host_path>     Optional host media path mounted at /mnt/media
      --chown-media-dir           Chown existing --media-dir to 101000:101000 (ADMIN_USER UID 1000)
      --mount <host_path>:<ct_path>
                                  Repeatable extra bind mount (e.g. /mnt/dl:/mnt/downloads)
      --startup <spec>            Optional Proxmox startup order (e.g. order=2,up=30,down=60)
      --aio-compose <host_file>   Optional host compose YAML pre-staged into AIOStreams dir if absent
      --aio-repo <git_url>        Optional HTTPS/SSH Git URL cloned into AIOStreams dir if absent
      --aio-branch <ref>          Optional Git branch or tag for --aio-repo
      --with-fuse                 Enable fuse=1 feature on the LXC
      --with-wolf-input           Configure uinput/uhid & input passthrough for Wolf/Purah
      --with-zeroconf             Enable docktun-zeroconf mDNS (*.local, port 80; default: on)
      --no-zeroconf, --without-zeroconf  Disable docktun-zeroconf mDNS (leave host port 80 free)
      --allow-unconfined-apparmor Opt-in escape hatch to set lxc.apparmor.profile: unconfined
      --template-hygiene          Install SSH host-key first-boot regen, strip live state & machine-id
      --cleanup-on-failure        Destroy newly created CTID if provisioning fails mid-run
      --provision-only <ctid>     Run/re-run inner LXC provisioning on an existing unprivileged CTID
      --doctor <ctid>             Inspect an existing LXC config, GPU/TUN/UFW/DOCKER-USER, and DockTun health
      --list                      List docktun-tagged Proxmox LXCs with status and IP
      --destroy <ctid>            Stop, unlock, and purge an existing LXC (plus per-CTID prestart hook)
      --domain <hostname>         Domain placeholder for AIOStreams BASE_URL/Nginx
                                  (default: aio.example.com)
  -h, --help                      Show this help message and exit
EOF
}

parse_args() {
  local opt="" val="" has_eq=0
  while [[ "$#" -gt 0 ]]; do
    opt="$1"
    val=""
    has_eq=0
    if [[ "$opt" == --*=* ]]; then
      val="${opt#*=}"
      opt="${opt%%=*}"
      has_eq=1
    fi
    case "$opt" in
      -y|--yes|--non-interactive) (( has_eq )) && die "${opt} does not accept a value"; NON_INTERACTIVE=1; shift ;;
      --dry-run) (( has_eq )) && die "${opt} does not accept a value"; DRY_RUN=1; shift ;;
      --skip-host-prep) (( has_eq )) && die "${opt} does not accept a value"; SKIP_HOST_PREP=1; shift ;;
      --host-prep-only) (( has_eq )) && die "${opt} does not accept a value"; HOST_PREP_ONLY=1; shift ;;
      --cleanup-on-failure) (( has_eq )) && die "${opt} does not accept a value"; CLEANUP_ON_FAILURE=1; shift ;;
      --chown-media-dir) (( has_eq )) && die "${opt} does not accept a value"; CHOWN_MEDIA_DIR=1; CREATION_FLAGS_USED+=("--chown-media-dir"); shift ;;
      --list) (( has_eq )) && die "${opt} does not accept a value"; LIST_MODE=1; shift ;;
      --with-fuse) (( has_eq )) && die "${opt} does not accept a value"; WITH_FUSE=1; CREATION_FLAGS_USED+=("--with-fuse"); shift ;;
      --with-wolf-input) (( has_eq )) && die "${opt} does not accept a value"; WITH_WOLF_INPUT=1; CREATION_FLAGS_USED+=("--with-wolf-input"); shift ;;
      --with-zeroconf) (( has_eq )) && die "${opt} does not accept a value"; WITH_ZEROCONF=1; shift ;;
      --no-zeroconf|--without-zeroconf) (( has_eq )) && die "${opt} does not accept a value"; WITH_ZEROCONF=0; shift ;;
      --allow-unconfined-apparmor) (( has_eq )) && die "${opt} does not accept a value"; ALLOW_UNCONFINED_APPARMOR=1; CREATION_FLAGS_USED+=("--allow-unconfined-apparmor"); shift ;;
      --template-hygiene) (( has_eq )) && die "${opt} does not accept a value"; TEMPLATE_HYGIENE=1; shift ;;
      -h|--help) (( has_eq )) && die "${opt} does not accept a value"; usage; exit 0 ;;
      --provision-only) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--provision-only requires a CTID"; val="$2"; shift; }; [[ -n "$val" ]] || die "--provision-only cannot be empty"; (( PROVISION_ONLY )) && [[ "$MODE_CTID" != "$val" ]] && die "Conflicting --provision-only CTIDs: '${MODE_CTID}' vs '${val}'."; PROVISION_ONLY=1; MODE_CTID="$val"; CTID="$val"; shift ;;
      --doctor) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--doctor requires a CTID"; val="$2"; shift; }; [[ -n "$val" ]] || die "--doctor cannot be empty"; (( DOCTOR_MODE )) && [[ "$MODE_CTID" != "$val" ]] && die "Conflicting --doctor CTIDs: '${MODE_CTID}' vs '${val}'."; DOCTOR_MODE=1; MODE_CTID="$val"; CTID="$val"; shift ;;
      --destroy) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--destroy requires a CTID"; val="$2"; shift; }; [[ -n "$val" ]] || die "--destroy cannot be empty"; (( DESTROY_MODE )) && [[ "$MODE_CTID" != "$val" ]] && die "Conflicting --destroy CTIDs: '${MODE_CTID}' vs '${val}'."; DESTROY_MODE=1; MODE_CTID="$val"; CTID="$val"; shift ;;
      --ctid) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--ctid requires a value"; val="$2"; shift; }; [[ -n "$val" ]] || die "--ctid cannot be empty"; CLI_CTID="$val"; CTID="$val"; shift ;;
      --hostname) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--hostname requires a value"; val="$2"; shift; }; HOSTNAME="$val"; CREATION_FLAGS_USED+=("--hostname"); shift ;;
      --os) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--os requires a value"; val="$2"; shift; }; OS_CHOICE="${val,,}"; CREATION_FLAGS_USED+=("--os"); shift ;;
      --storage) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--storage requires a value"; val="$2"; shift; }; STORAGE="$val"; CREATION_FLAGS_USED+=("--storage"); shift ;;
      --template-storage) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--template-storage requires a value"; val="$2"; shift; }; TEMPLATE_STORAGE="$val"; CREATION_FLAGS_USED+=("--template-storage"); shift ;;
      --cores) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--cores requires a value"; val="$2"; shift; }; CORES="$val"; CREATION_FLAGS_USED+=("--cores"); shift ;;
      --memory) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--memory requires a value"; val="$2"; shift; }; MEMORY="$val"; CREATION_FLAGS_USED+=("--memory"); shift ;;
      --swap) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--swap requires a value"; val="$2"; shift; }; SWAP="$val"; CREATION_FLAGS_USED+=("--swap"); shift ;;
      --disk) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--disk requires a value"; val="$2"; shift; }; DISK="$val"; CREATION_FLAGS_USED+=("--disk"); shift ;;
      --bridge) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--bridge requires a value"; val="$2"; shift; }; BRIDGE="$val"; CREATION_FLAGS_USED+=("--bridge"); shift ;;
      --vlan) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--vlan requires a value"; val="$2"; shift; }; VLAN_TAG="$val"; CREATION_FLAGS_USED+=("--vlan"); shift ;;
      --mtu) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--mtu requires a value"; val="$2"; shift; }; MTU_BYTES="$val"; CREATION_FLAGS_USED+=("--mtu"); shift ;;
      --ip) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--ip requires a value"; val="$2"; shift; }; IP_ADDR="$val"; CREATION_FLAGS_USED+=("--ip"); shift ;;
      --gw) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--gw requires a value"; val="$2"; shift; }; GW_ADDR="$val"; CREATION_FLAGS_USED+=("--gw"); shift ;;
      --dns) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--dns requires a value"; val="$2"; shift; }; DNS_ADDR="$val"; CREATION_FLAGS_USED+=("--dns"); shift ;;
      --timezone) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--timezone requires a value"; val="$2"; shift; }; AIO_TIMEZONE="$val"; shift ;;
      --ssh-pubkey-file) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--ssh-pubkey-file requires a value"; val="$2"; shift; }; SSH_PUBKEY_FILE="$val"; shift ;;
      --ssh-pubkey) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--ssh-pubkey requires a value"; val="$2"; shift; }; SSH_PUBKEY="$val"; shift ;;
      --admin-user) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--admin-user requires a value"; val="$2"; shift; }; ADMIN_USER="$val"; shift ;;
      --gpu) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--gpu requires a value"; val="$2"; shift; }; GPU_MODE="${val,,}"; shift ;;
      --pci-slot) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--pci-slot requires a value"; val="$2"; shift; }; PCI_SLOT="$val"; shift ;;
      --docktun-port) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--docktun-port requires a value"; val="$2"; shift; }; DOCKTUN_PORT="$val"; shift ;;
      --docktun-dir) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--docktun-dir requires a value"; val="$2"; shift; }; DOCKTUN_DIR="$val"; shift ;;
      --docktun-admin-user) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--docktun-admin-user requires a value"; val="$2"; shift; }; DOCKTUN_ADMIN_USER="$val"; shift ;;
      --docktun-admin-password) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--docktun-admin-password requires a value"; val="$2"; shift; }; DOCKTUN_ADMIN_PASSWORD="$val"; shift ;;
      --lan-subnets) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--lan-subnets requires a value"; val="$2"; shift; }; LAN_SUBNETS="$val"; shift ;;
      --media-dir) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--media-dir requires a value"; val="$2"; shift; }; MEDIA_DIR="$val"; CREATION_FLAGS_USED+=("--media-dir"); shift ;;
      --mount) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--mount requires <host_path>:<ct_path>"; val="$2"; shift; }; EXTRA_MOUNTS+=("$val"); CREATION_FLAGS_USED+=("--mount"); shift ;;
      --startup) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--startup requires a value"; val="$2"; shift; }; STARTUP_SPEC="$val"; CREATION_FLAGS_USED+=("--startup"); shift ;;
      --aio-compose) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--aio-compose requires a value"; val="$2"; shift; }; AIO_COMPOSE_FILE="$val"; shift ;;
      --aio-repo) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--aio-repo requires a value"; val="$2"; shift; }; AIO_REPO_URL="$val"; shift ;;
      --aio-branch) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--aio-branch requires a value"; val="$2"; shift; }; AIO_REPO_BRANCH="$val"; shift ;;
      --domain) (( has_eq )) || { [[ "$#" -ge 2 ]] || die "--domain requires a value"; val="$2"; shift; }; AIO_DOMAIN="$val"; shift ;;
      *) die "Unknown option: $1 (use --help for usage)" ;;
    esac
  done
}

# ============================================================================
# Smart Auto-Detection & Interactive Prompts (/dev/tty)
# ============================================================================

detect_default_ctid() {
  if command -v pvesh >/dev/null 2>&1; then
    local next_id=""
    next_id="$(pvesh get /cluster/nextid 2>/dev/null | tr -d '[:space:]' || true)"
    if [[ "$next_id" =~ ^[0-9]+$ ]]; then
      printf '%s\n' "$next_id"
      return 0
    fi
  fi
  printf '100\n'
}

detect_default_storage() {
  local content="$1"
  local fallback="$2"
  if command -v pvesm >/dev/null 2>&1; then
    local found=""
    found="$(pvesm status -content "$content" 2>/dev/null | awk 'NR>1 && $3 == "active" {print $1; exit}' || true)"
    if [[ -n "$found" ]]; then
      printf '%s\n' "$found"
      return 0
    fi
  fi
  printf '%s\n' "$fallback"
}

detect_default_bridge() {
  local br=""
  if command -v ip >/dev/null 2>&1; then
    br="$(ip -4 route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") {print $(i+1); exit}}' || true)"
    if [[ -n "$br" ]]; then
      if [[ -d "/sys/class/net/${br}/bridge" ]] || \
         { command -v ovs-vsctl >/dev/null 2>&1 && ovs-vsctl br-exists "$br" >/dev/null 2>&1; } || \
         [[ "$br" =~ ^(vmbr[0-9a-zA-Z_-]*|vnet[0-9a-zA-Z_-]*|br[0-9a-zA-Z_-]*)$ ]]; then
        printf '%s\n' "$br"
        return 0
      fi
    fi
  fi
  local -a br_dirs=()
  glob_matches br_dirs "/sys/class/net/*/bridge" "/sys/class/net/vmbr*"
  local net_dir="" found_br=""
  for net_dir in ${br_dirs[@]+"${br_dirs[@]}"}; do
    [[ -d "$net_dir" ]] || continue
    if [[ "$net_dir" == */bridge ]]; then
      found_br="$(basename "$(dirname "$net_dir")")"
    elif [[ -d "${net_dir}/bridge" ]]; then
      found_br="$(basename "$net_dir")"
    else
      continue
    fi
    break
  done
  if [[ -z "$found_br" ]] && command -v ovs-vsctl >/dev/null 2>&1; then
    found_br="$(ovs-vsctl list-br 2>/dev/null | head -n1 || true)"
  fi
  if [[ -n "$found_br" ]]; then
    printf '%s\n' "$found_br"
    return 0
  fi
  printf 'vmbr0\n'
}

is_valid_ssh_pubkey_line() {
  local line="${1%$'\r'}"
  [[ -n "$line" && ! "$line" =~ ^[[:space:]]*# ]] || return 1
  [[ "$line" =~ (^|[[:space:]])(ssh-(rsa|dss|ed25519)|ecdsa-sha2-nistp[0-9]+|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com)(-cert-v01@openssh\.com)?[[:space:]]+[^[:space:]]+ ]]
}

file_has_ssh_pubkey() {
  local file="$1"
  [[ -r "$file" && -s "$file" ]] || return 1
  local line=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    if is_valid_ssh_pubkey_line "$line"; then
      return 0
    fi
  done < "$file"
  return 1
}

detect_default_ssh_pubkey_file() {
  local candidate="" found_file=""
  for candidate in /root/.ssh/id_ed25519.pub /root/.ssh/id_rsa.pub /root/.ssh/authorized_keys; do
    if file_has_ssh_pubkey "$candidate"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  local -a pub_files=()
  glob_matches pub_files "/root/.ssh/*.pub"
  for candidate in ${pub_files[@]+"${pub_files[@]}"}; do
    if file_has_ssh_pubkey "$candidate"; then
      found_file="$candidate"
      break
    fi
  done
  printf '%s\n' "$found_file"
}

detect_default_timezone() {
  local tz=""
  if command -v timedatectl >/dev/null 2>&1; then
    tz="$(timedatectl show -p Timezone --value 2>/dev/null | tr -d '[:space:]' || true)"
  fi
  if [[ -z "$tz" && -r /etc/timezone ]]; then
    tz="$(head -n1 /etc/timezone 2>/dev/null | tr -d '[:space:]' || true)"
  fi
  if [[ -z "$tz" && -L /etc/localtime ]]; then
    local target=""
    target="$(readlink -f /etc/localtime 2>/dev/null || true)"
    if [[ "$target" == */zoneinfo/* ]]; then
      tz="${target#*/zoneinfo/}"
    fi
  fi
  printf '%s\n' "${tz:-UTC}"
}

get_pci_kernel_driver() {
  local slot="${1:-}"
  [[ -n "$slot" ]] || return 1
  local drv=""
  if [[ -L "/sys/bus/pci/devices/${slot}/driver" ]]; then
    local drv_target=""
    drv_target="$(readlink -f "/sys/bus/pci/devices/${slot}/driver" 2>/dev/null || true)"
    if [[ -n "$drv_target" ]]; then
      drv="$(basename "$drv_target")"
    fi
  fi
  if [[ -z "$drv" ]] && command -v lspci >/dev/null 2>&1; then
    drv="$(lspci -k -s "$slot" 2>/dev/null | awk -F': ' '/Kernel driver in use:/ {print $2; exit}' || true)"
  fi
  drv="${drv//[[:space:]]/}"
  [[ -n "$drv" ]] || return 1
  printf '%s\n' "$drv"
}

detect_gpus_via_lspci() {
  DETECTED_NVIDIA=0
  DETECTED_INTEL=0
  DETECTED_AMD=0
  DETECTED_GPU_COUNT=0
  DETECTED_INTEL_DRIVER=""

  if ! command -v lspci >/dev/null 2>&1; then
    return 0
  fi

  local all_gpu_lines="" gpu_lines=""
  all_gpu_lines="$(lspci -Dnn 2>/dev/null | grep -iE '\[(0300|0302|0380)\]|VGA compatible controller|3D controller|Display controller' || true)"
  if [[ -n "$all_gpu_lines" ]]; then
    DETECTED_GPU_COUNT="$(wc -l <<< "$all_gpu_lines" | tr -d '[:space:]')"
  fi

  if [[ -n "$PCI_SLOT" ]]; then
    local query_slot="$PCI_SLOT"
    if [[ "$query_slot" =~ ^[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$ ]]; then
      query_slot="0000:${query_slot}"
    fi
    if [[ "$query_slot" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$ ]]; then
      gpu_lines="$(lspci -Dnn -s "$query_slot" 2>/dev/null || true)"
    fi
  else
    gpu_lines="$all_gpu_lines"
  fi

  if [[ -z "$gpu_lines" ]]; then
    return 0
  fi

  if grep -qiE '(^|[^[:alnum:]])NVIDIA([^[:alnum:]]|$)|\[10de:' <<< "$gpu_lines"; then
    DETECTED_NVIDIA=1
  fi
  if grep -qiE '(^|[^[:alnum:]])Intel([^[:alnum:]]|$)|\[8086:' <<< "$gpu_lines"; then
    DETECTED_INTEL=1
  fi
  if grep -qiE '(^|[^[:alnum:]])(AMD|ATI)([^[:alnum:]]|$)|Advanced Micro Devices|\[1002:' <<< "$gpu_lines"; then
    DETECTED_AMD=1
  fi

  if (( DETECTED_INTEL )); then
    local intel_slot="" kdrv=""
    intel_slot="$(awk '/Intel|\[8086:/ {print $1; exit}' <<< "$gpu_lines" || true)"
    kdrv="$(get_pci_kernel_driver "$intel_slot" || true)"
    if [[ -z "$kdrv" ]]; then
      kdrv="$(lspci -k 2>/dev/null | awk '
        /(VGA|3D|Display).*Intel/ { in_intel=1; next }
        /^[^[:space:]]/ { in_intel=0 }
        in_intel && /Kernel driver in use:/ { sub(/^.*Kernel driver in use:[[:space:]]*/, ""); print; exit }
      ' || true)"
    fi
    DETECTED_INTEL_DRIVER="${kdrv//[[:space:]]/}"
  fi
  return 0
}

resolve_effective_gpus() {
  DO_NVIDIA=0
  DO_INTEL=0
  DO_AMD=0

  case "$GPU_MODE" in
    auto)
      DO_NVIDIA="$DETECTED_NVIDIA"
      DO_INTEL="$DETECTED_INTEL"
      DO_AMD="$DETECTED_AMD"
      ;;
    all) DO_NVIDIA=1; DO_INTEL=1; DO_AMD=1 ;;
    nvidia) DO_NVIDIA=1 ;;
    intel) DO_INTEL=1 ;;
    amd) DO_AMD=1 ;;
    none) ;;
  esac
  GPUS_RESOLVED=1
}

detect_host_prep_status() {
  HOST_PREP_NEEDED=0
  if [[ ! -f /etc/modules-load.d/media-lxc-net.conf || ! -f /etc/sysctl.d/99-media-lxc.conf ]]; then
    HOST_PREP_NEEDED=1
  fi
  if (( DO_NVIDIA )) && [[ ! -f /etc/systemd/system/nvidia-lxc-prestart.service || ! -f /etc/udev/rules.d/70-nvidia-lxc.rules || ! -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    HOST_PREP_NEEDED=1
  fi
  if (( DO_INTEL )); then
    if command -v dpkg-query >/dev/null 2>&1; then
      if ! dpkg-query -W -f='${Status}\n' intel-gpu-tools 2>/dev/null | grep -qE '^(install|hold) ok installed$'; then
        HOST_PREP_NEEDED=1
      fi
    elif ! command -v intel_gpu_top >/dev/null 2>&1; then
      HOST_PREP_NEEDED=1
    fi
  fi
  if (( WITH_WOLF_INPUT )) && [[ ! -f /etc/udev/rules.d/85-wolf-virtual-inputs.rules ]]; then
    HOST_PREP_NEEDED=1
  fi
  return 0
}

check_zeroconf_collision() {
  if (( ! WITH_ZEROCONF || HOST_PREP_ONLY || DOCTOR_MODE || PROVISION_ONLY || LIST_MODE || DESTROY_MODE )); then
    return 0
  fi
  local existing_ip=""
  if command -v avahi-resolve >/dev/null 2>&1; then
    if command -v timeout >/dev/null 2>&1; then
      existing_ip="$(timeout 2 avahi-resolve -n -4 docktun.local 2>/dev/null | awk '{print $2; exit}' || true)"
    else
      existing_ip="$(avahi-resolve -n -4 docktun.local 2>/dev/null | awk '{print $2; exit}' || true)"
    fi
  elif command -v getent >/dev/null 2>&1; then
    existing_ip="$(getent hosts docktun.local 2>/dev/null | awk '{print $1; exit}' || true)"
  fi
  if [[ -n "$existing_ip" ]]; then
    log_warn "docktun.local already responds on this LAN (${existing_ip}). For a secondary LXC, pass --no-zeroconf to avoid mDNS collisions."
  fi
  return 0
}

resolve_run_host_prep() {
  if [[ "$SKIP_HOST_PREP" == "1" || "$PROVISION_ONLY" == "1" || "$DOCTOR_MODE" == "1" || "$LIST_MODE" == "1" || "$DESTROY_MODE" == "1" ]]; then
    RUN_HOST_PREP=0
  elif [[ "$HOST_PREP_ONLY" == "1" || "$HOST_PREP_NEEDED" == "1" ]]; then
    RUN_HOST_PREP=1
  else
    RUN_HOST_PREP=0
  fi
}

auto_detect_defaults() {
  detect_os || true
  detect_pve_version || true
  detect_gpus_via_lspci
  resolve_effective_gpus

  if [[ -z "$CTID" ]]; then
    CTID="$(detect_default_ctid)"
  fi
  if [[ -z "$STORAGE" ]]; then
    STORAGE="$(detect_default_storage rootdir local-lvm)"
  fi
  if [[ -z "$TEMPLATE_STORAGE" ]]; then
    TEMPLATE_STORAGE="$(detect_default_storage vztmpl local)"
  fi
  if [[ -z "$BRIDGE" ]]; then
    BRIDGE="$(detect_default_bridge)"
  fi
  if [[ -z "$AIO_TIMEZONE" ]]; then
    AIO_TIMEZONE="$(detect_default_timezone)"
  fi
  if [[ -z "$SSH_PUBKEY_FILE" && -z "$SSH_PUBKEY" ]]; then
    SSH_PUBKEY_FILE="$(detect_default_ssh_pubkey_file)"
  fi
  check_zeroconf_collision
  detect_host_prep_status
  resolve_run_host_prep
}

prompt_val() {
  local var_name="${1:?prompt_val requires variable name}"
  local prompt_text="${2:?prompt_val requires prompt text}"
  local default_val="${3:-}"
  local is_secret="${4:-0}"

  local current_val="${!var_name:-}"
  local effective_default="${current_val:-$default_val}"

  if [[ "${NON_INTERACTIVE:-0}" == "1" ]]; then
    printf -v "$var_name" '%s' "$effective_default"
    return 0
  fi

  local display_prompt="$prompt_text"
  if [[ -n "$effective_default" ]]; then
    if [[ "$is_secret" == "1" ]]; then
      display_prompt="${prompt_text} [****]: "
    else
      display_prompt="${prompt_text} [${effective_default}]: "
    fi
  else
    display_prompt="${prompt_text}: "
  fi

  local input=""
  if [[ "$is_secret" == "1" ]]; then
    read -r -s -p "$display_prompt" input </dev/tty || die "Input aborted (EOF)."
    printf '\n' >&2
  else
    read -r -p "$display_prompt" input </dev/tty || die "Input aborted (EOF)."
  fi

  if [[ -z "$input" ]]; then
    input="$effective_default"
  fi
  printf -v "$var_name" '%s' "$input"
}

prompt_yn() {
  local prompt_text="${1:?prompt_yn requires prompt text}"
  local default_yn="${2:-n}"
  default_yn="${default_yn,,}"

  if [[ "${NON_INTERACTIVE:-0}" == "1" ]]; then
    case "$default_yn" in
      y|yes|1|true) return 0 ;;
      *) return 1 ;;
    esac
  fi

  local hint="[y/N]"
  if [[ "$default_yn" == "y" || "$default_yn" == "yes" ]]; then
    hint="[Y/n]"
  fi

  local ans=""
  while true; do
    read -r -p "${prompt_text} ${hint}: " ans </dev/tty || die "Input aborted (EOF)."
    ans="${ans,,}"
    if [[ -z "$ans" ]]; then
      ans="$default_yn"
    fi
    case "$ans" in
      y|yes) return 0 ;;
      n|no) return 1 ;;
      *) log_warn "Please answer 'y' or 'n'." ;;
    esac
  done
}

print_summary_banner() {
  local pve_str="${PVE_MAJOR:-?}.${PVE_MINOR:-?}"
  local host_prep_str="skip (already configured)"
  if [[ "$SKIP_HOST_PREP" == "1" ]]; then
    host_prep_str="skipped (--skip-host-prep)"
  elif [[ "$RUN_HOST_PREP" == "1" ]]; then
    host_prep_str="will run (needed)"
  fi

  local gpu_desc="none"
  local -a gpu_parts=()
  (( DO_NVIDIA )) && gpu_parts+=("NVIDIA")
  (( DO_INTEL )) && gpu_parts+=("Intel${DETECTED_INTEL_DRIVER:+ (${DETECTED_INTEL_DRIVER})}")
  (( DO_AMD )) && gpu_parts+=("AMD")
  if [[ "${#gpu_parts[@]}" -gt 0 ]]; then
    gpu_desc="${gpu_parts[*]}"
  fi

  local ssh_desc="NONE (PasswordAuthentication fallback)"
  if [[ -n "$SSH_PUBKEY" ]]; then
    ssh_desc="inline key"
  elif [[ -n "$SSH_PUBKEY_FILE" ]]; then
    ssh_desc="${SSH_PUBKEY_FILE}"
  fi

  local mdns_desc="disabled (port 80 free)"
  if (( WITH_ZEROCONF )); then
    mdns_desc="enabled (http://docktun.local)"
  fi
  local wolf_desc="off"
  if (( WITH_WOLF_INPUT )); then
    wolf_desc="on (uinput/uhid)"
  fi

  printf '\n%s=== Auto-Detected Configuration Summary ===%s\n' "${_CLR_BOLD}${_CLR_CYAN}" "${_CLR_RESET}"
  printf '  Host          : PVE %s (%s %s), Host Prep: %s\n' \
    "$pve_str" "${OS_ID:-unknown}" "${OS_CODENAME:-unknown}" "$host_prep_str"
  if [[ "$HOST_PREP_ONLY" != "1" ]]; then
    printf '  LXC Container : CTID=%s, Hostname=%s, OS=%s, TZ=%s\n' "$CTID" "$HOSTNAME" "$OS_CHOICE" "${AIO_TIMEZONE:-UTC}"
    local shm_note=""
    if [[ "$MEMORY" =~ ^[0-9]+$ ]] && (( 10#$MEMORY < 1024 )); then
      shm_note=" (WARN: /dev/shm < 512 MiB)"
    fi
    printf '  Compute/Disk  : %s cores, %s MiB RAM%s, %s MiB swap, %s GiB (%s / tpl:%s)%s\n' \
      "$CORES" "$MEMORY" "$shm_note" "$SWAP" "$DISK" "$STORAGE" "$TEMPLATE_STORAGE" "${STARTUP_SPEC:+ startup=${STARTUP_SPEC}}"
    local gw_display=""
    if [[ "${IP_ADDR,,}" != "dhcp" && -n "$GW_ADDR" ]]; then
      gw_display=" gw=${GW_ADDR}"
    fi
    printf '  Network       : bridge=%s%s%s, ip=%s%s, dns=%s\n' \
      "$BRIDGE" "${VLAN_TAG:+ vlan=${VLAN_TAG}}" "${MTU_BYTES:+ mtu=${MTU_BYTES}}" "$IP_ADDR" "$gw_display" "$DNS_ADDR"
    printf '  Security      : admin=%s, ssh=%s, AppArmor=%s\n' \
      "$ADMIN_USER" "$ssh_desc" "$( (( ALLOW_UNCONFINED_APPARMOR )) && echo 'unconfined (opt-in)' || echo 'confined (default)' )"
    printf '  GPU & Extras  : mode=%s (%s)%s, wolf=%s, fuse=%s\n' \
      "$GPU_MODE" "$gpu_desc" "${PCI_SLOT:+ slot=${PCI_SLOT}}" "$wolf_desc" "$WITH_FUSE"
    printf '  DockTun / AIO : port=%s, mDNS=%s, dir=%s, domain=%s%s%s\n' \
      "$DOCKTUN_PORT" "$mdns_desc" "$DOCKTUN_DIR" "$AIO_DOMAIN" "${AIO_COMPOSE_FILE:+ compose=${AIO_COMPOSE_FILE}}" "${AIO_REPO_URL:+ repo=${AIO_REPO_URL}${AIO_REPO_BRANCH:+ (${AIO_REPO_BRANCH})}}"
  fi
  printf '%s===========================================%s\n\n' "${_CLR_BOLD}${_CLR_CYAN}" "${_CLR_RESET}"
}

run_interactive_wizard() {
  if [[ "${NON_INTERACTIVE:-0}" == "1" || "$DOCTOR_MODE" == "1" || "$PROVISION_ONLY" == "1" || "$LIST_MODE" == "1" || "$DESTROY_MODE" == "1" ]]; then
    return 0
  fi

  if ! has_controlling_tty; then
    die "No controlling TTY available; pass --non-interactive (-y|--yes) with CLI flags."
  fi

  print_summary_banner

  local choice=""
  while true; do
    read -r -p "Proceed with these defaults? [Y]es / [c]ustomize / [n]o [Y]: " choice </dev/tty || die "Input aborted (EOF)."
    choice="${choice,,}"
    case "$choice" in
      ""|y|yes|e|express)
        return 0
        ;;
      n|no|q)
        die "Aborted by user."
        ;;
      c|customize)
        if [[ "$HOST_PREP_ONLY" != "1" ]]; then
          prompt_val CTID "CTID" "$CTID"
          prompt_val HOSTNAME "Hostname" "$HOSTNAME"
          prompt_val OS_CHOICE "OS (debian12|debian13|ubuntu2404)" "$OS_CHOICE"
          OS_CHOICE="${OS_CHOICE,,}"
          prompt_val CORES "CPU cores" "$CORES"
          prompt_val MEMORY "Memory (MiB)" "$MEMORY"
          prompt_val DISK "Disk size (GiB)" "$DISK"
          prompt_val IP_ADDR "IPv4 CIDR or 'dhcp'" "$IP_ADDR"
          if [[ "${IP_ADDR,,}" != "dhcp" ]]; then
            prompt_val GW_ADDR "IPv4 gateway" "$GW_ADDR"
          else
            IP_ADDR="dhcp"
            GW_ADDR=""
          fi
          prompt_val GPU_MODE "Shared GPU mode (auto|nvidia|intel|amd|all|none)" "$GPU_MODE"
          GPU_MODE="${GPU_MODE,,}"
          if [[ "$GPU_MODE" != "none" ]] && (( DETECTED_GPU_COUNT > 1 )); then
            prompt_val PCI_SLOT "PCI slot (blank for auto/all)" "$PCI_SLOT"
          fi
          if [[ -n "$PCI_SLOT" ]]; then
            PCI_SLOT="$(normalize_pci_slot "$PCI_SLOT")"
          fi
          prompt_val DOCKTUN_PORT "DockTun dashboard port" "$DOCKTUN_PORT"
          local wolf_default="n"
          (( WITH_WOLF_INPUT )) && wolf_default="y"
          if prompt_yn "Enable Wolf game-streaming input passthrough?" "$wolf_default"; then
            WITH_WOLF_INPUT=1
          else
            WITH_WOLF_INPUT=0
          fi
          detect_gpus_via_lspci
          resolve_effective_gpus
          detect_host_prep_status
          resolve_run_host_prep
        fi
        print_summary_banner
        if ! prompt_yn "Proceed?" "y"; then
          die "Aborted by user."
        fi
        return 0
        ;;
      *)
        log_warn "Please enter 'Y' (yes), 'c' (customize), or 'n' (no)."
        ;;
    esac
  done
}

# ============================================================================
# Validation
# ============================================================================

normalize_pci_slot() {
  local slot="$1"
  if [[ -z "$slot" ]]; then
    return 0
  fi
  if [[ "$slot" =~ ^[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$ ]]; then
    slot="0000:${slot}"
  fi
  if [[ ! "$slot" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$ ]]; then
    die "Invalid --pci-slot format '${slot}' (expected e.g. 0000:01:00.0 or 01:00.0)"
  fi
  printf '%s\n' "${slot,,}"
}

is_valid_ipv4() {
  local ip="$1"
  [[ "$ip" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
  local o=""
  for o in "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[4]}"; do
    [[ "$o" =~ ^(0|[1-9][0-9]*)$ ]] || return 1
    (( 10#$o <= 255 )) || return 1
  done
  return 0
}

is_valid_ipv4_cidr() {
  local cidr="$1"
  [[ "$cidr" == */* ]] || return 1
  local addr="${cidr%/*}"
  local prefix="${cidr#*/}"
  is_valid_ipv4 "$addr" || return 1
  [[ "$prefix" =~ ^(0|[1-9][0-9]*)$ ]] || return 1
  (( 10#$prefix <= 32 ))
}

is_valid_ipv6() {
  local ip="$1"
  [[ "$ip" == *:* && "$ip" =~ ^[0-9a-fA-F:]+$ ]] || return 1
  [[ "$ip" != *:::* ]] || return 1
  local dbl="${ip#*::}"
  if [[ "$dbl" != "$ip" && "$dbl" == *::* ]]; then
    return 1
  fi
  if [[ "$ip" == *::* ]]; then
    local left="${ip%%::*}" right="${ip#*::}"
    local -a lg=() rg=()
    local non_empty=0 g=""
    if [[ -n "$left" ]]; then
      [[ "$left" != :* && "$left" != *: ]] || return 1
      IFS=':' read -ra lg <<< "$left"
      for g in "${lg[@]}"; do
        [[ "$g" =~ ^[0-9a-fA-F]{1,4}$ ]] || return 1
        non_empty=$((non_empty + 1))
      done
    fi
    if [[ -n "$right" ]]; then
      [[ "$right" != :* && "$right" != *: ]] || return 1
      IFS=':' read -ra rg <<< "$right"
      for g in "${rg[@]}"; do
        [[ "$g" =~ ^[0-9a-fA-F]{1,4}$ ]] || return 1
        non_empty=$((non_empty + 1))
      done
    fi
    (( non_empty <= 7 )) || return 1
  else
    [[ "$ip" != :* && "$ip" != *: ]] || return 1
    local -a groups=()
    IFS=':' read -ra groups <<< "$ip"
    (( ${#groups[@]} == 8 )) || return 1
    local g=""
    for g in "${groups[@]}"; do
      [[ "$g" =~ ^[0-9a-fA-F]{1,4}$ ]] || return 1
    done
  fi
  return 0
}

is_valid_ipv6_cidr() {
  local cidr="$1"
  [[ "$cidr" == */* ]] || return 1
  local addr="${cidr%/*}"
  local prefix="${cidr#*/}"
  is_valid_ipv6 "$addr" || return 1
  [[ "$prefix" =~ ^(0|[1-9][0-9]*)$ ]] || return 1
  (( 10#$prefix <= 128 ))
}

is_valid_cidr() {
  local cidr="$1"
  is_valid_ipv4_cidr "$cidr" || is_valid_ipv6_cidr "$cidr"
}

check_ctid_exists_cluster() {
  local id="$1"
  if [[ -f "/etc/pve/lxc/${id}.conf" ]]; then
    return 0
  fi
  if [[ -f "/etc/pve/qemu-server/${id}.conf" ]]; then
    return 0
  fi
  local -a cluster_confs=()
  local f=""
  glob_matches cluster_confs "/etc/pve/nodes/*/lxc/${id}.conf" "/etc/pve/nodes/*/qemu-server/${id}.conf"
  for f in ${cluster_confs[@]+"${cluster_confs[@]}"}; do
    if [[ -f "$f" ]]; then
      return 0
    fi
  done
  if command -v pct >/dev/null 2>&1 && pct status "$id" >/dev/null 2>&1; then
    return 0
  fi
  if command -v qm >/dev/null 2>&1 && qm status "$id" >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

normalize_mount_target() {
  local raw="${1:-}"
  if [[ "$raw" =~ [[:cntrl:]=] ]]; then
    die "Invalid mount path '${raw}' (control characters and '=' are not allowed)"
  fi
  if [[ "$raw" =~ (^|/)\.\.?(/|$) ]]; then
    die "Invalid mount path '${raw}' (path traversal '.' or '..' segments are not allowed)"
  fi
  local norm=""
  norm="$(printf '%s' "$raw" | sed -E 's#/+#/#g; s#/$##')"
  [[ -z "$norm" ]] && norm="/"
  printf '%s\n' "$norm"
}

is_protected_system_path() {
  local check_path="${1:-}"
  local include_root_mnt="${2:-0}"
  case "$check_path" in
    /|/bin|/bin/*|/boot|/boot/*|/dev|/dev/*|/etc|/etc/*|/lib|/lib/*|/lib32|/lib32/*|/lib64|/lib64/*|/libx32|/libx32/*|/proc|/proc/*|/run|/run/*|/sbin|/sbin/*|/sys|/sys/*|/usr|/usr/*|/var)
      return 0
      ;;
    /mnt|/root|/root/*)
      (( include_root_mnt )) && return 0
      return 1
      ;;
  esac
  return 1
}

validate_docktun_dir() {
  [[ -n "$DOCKTUN_DIR" && "$DOCKTUN_DIR" == /* ]] || \
    die "Invalid --docktun-dir '${DOCKTUN_DIR}' (must be an absolute path)"
  if [[ "$DOCKTUN_DIR" =~ [[:space:][:cntrl:]:,%] || "$DOCKTUN_DIR" == *'$'* ]]; then
    die "Invalid --docktun-dir '${DOCKTUN_DIR}' (whitespace, control chars, ':', ',', '\$', and '%' are not allowed)"
  fi
  if [[ "$DOCKTUN_DIR" =~ (^|/)\.\.?(/|$) ]]; then
    die "Invalid --docktun-dir '${DOCKTUN_DIR}' (path traversal '.' or '..' segments are not allowed)"
  fi
}

validate_mode_flags() {
  if (( HOST_PREP_ONLY && SKIP_HOST_PREP )); then
    die "--host-prep-only and --skip-host-prep cannot be used together."
  fi
  if [[ -n "${MODE_CTID:-}" && -n "${CLI_CTID:-}" && "$MODE_CTID" != "$CLI_CTID" ]]; then
    die "Conflicting CTID values: mode CTID '${MODE_CTID}' vs --ctid '${CLI_CTID}'."
  fi
  local mode_count=0
  (( HOST_PREP_ONLY )) && mode_count=$((mode_count + 1))
  (( PROVISION_ONLY )) && mode_count=$((mode_count + 1))
  (( DOCTOR_MODE )) && mode_count=$((mode_count + 1))
  (( LIST_MODE )) && mode_count=$((mode_count + 1))
  (( DESTROY_MODE )) && mode_count=$((mode_count + 1))
  if (( mode_count > 1 )); then
    die "--host-prep-only, --provision-only, --doctor, --list, and --destroy cannot be combined."
  fi
}

validate_gpu_and_pci_slot() {
  case "$GPU_MODE" in
    auto|nvidia|intel|amd|all|none) ;;
    *) die "Invalid --gpu '${GPU_MODE}'. Expected: auto|nvidia|intel|amd|all|none" ;;
  esac
  if [[ -n "$PCI_SLOT" ]]; then
    PCI_SLOT="$(normalize_pci_slot "$PCI_SLOT")"
  fi
}

validate_startup_spec() {
  local spec="${1:-}"
  [[ -n "$spec" ]] || return 0
  if [[ "$spec" == *$'\n'* || "$spec" == *$'\r'* || "$spec" =~ [[:cntrl:]] ]]; then
    die "Invalid --startup '${spec}' (newlines and control characters are not allowed)"
  fi
  if [[ "$spec" == ,* || "$spec" == *, || "$spec" == *,,* ]]; then
    die "Invalid --startup '${spec}' (empty token; expected comma-separated order=<uint>, up=<uint>, down=<uint>)"
  fi
  local -a tokens=()
  local tok="" key="" count=0
  local -A seen=()
  IFS=',' read -ra tokens <<< "$spec"
  for tok in "${tokens[@]}"; do
    [[ "$tok" =~ ^(order|up|down)=([0-9]+)$ ]] || \
      die "Invalid --startup '${spec}': unexpected token '${tok}' (expected order=<uint>, up=<uint>, or down=<uint>)"
    key="${BASH_REMATCH[1]}"
    if [[ -n "${seen[$key]+x}" ]]; then
      die "Invalid --startup '${spec}': duplicate key '${key}'"
    fi
    seen["$key"]=1
    count=$((count + 1))
  done
  (( count >= 1 )) || die "Invalid --startup '${spec}' (must contain at least one of order=<uint>, up=<uint>, down=<uint>)"
}

validate_aio_compose_file() {
  [[ -n "$AIO_COMPOSE_FILE" ]] || return 0
  [[ -r "$AIO_COMPOSE_FILE" && -s "$AIO_COMPOSE_FILE" ]] || \
    die "AIOStreams compose file not readable or empty: ${AIO_COMPOSE_FILE}"
  case "${AIO_COMPOSE_FILE,,}" in
    *.yml|*.yaml) ;;
    *) die "Invalid --aio-compose '${AIO_COMPOSE_FILE}' (expected a .yml or .yaml file)" ;;
  esac
  local raw_bytes="" clean_bytes=""
  raw_bytes="$(LC_ALL=C wc -c < "$AIO_COMPOSE_FILE" | tr -d '[:space:]')"
  clean_bytes="$(LC_ALL=C tr -d '\0' < "$AIO_COMPOSE_FILE" | wc -c | tr -d '[:space:]')"
  if [[ "$raw_bytes" != "$clean_bytes" ]]; then
    die "Invalid --aio-compose '${AIO_COMPOSE_FILE}': file contains NUL bytes"
  fi
  if ! grep -qE '[^[:space:]]' "$AIO_COMPOSE_FILE"; then
    die "Invalid --aio-compose '${AIO_COMPOSE_FILE}': file contains only whitespace"
  fi
}

validate_aio_repo_opts() {
  if [[ -n "$AIO_REPO_URL" && -n "$AIO_COMPOSE_FILE" ]]; then
    die "--aio-repo and --aio-compose are mutually exclusive; choose one."
  fi
  if [[ -n "$AIO_REPO_BRANCH" && -z "$AIO_REPO_URL" ]]; then
    die "--aio-branch requires --aio-repo."
  fi
  if [[ -n "$AIO_REPO_URL" ]]; then
    if [[ "$AIO_REPO_URL" == -* || "$AIO_REPO_URL" =~ [[:space:][:cntrl:]] ]]; then
      die "Invalid --aio-repo '${AIO_REPO_URL}' (must be a safe HTTPS or SSH Git URL)"
    fi
    if [[ ! "$AIO_REPO_URL" =~ ^(https://[A-Za-z0-9._-]+(:[0-9]+)?/[A-Za-z0-9._~/%+-]+|ssh://([A-Za-z0-9._-]+@)?[A-Za-z0-9._-]+(:[0-9]+)?/[A-Za-z0-9._~/%+-]+|[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:[A-Za-z0-9._~/%+][A-Za-z0-9._~/%+-]*)$ ]]; then
      die "Invalid --aio-repo '${AIO_REPO_URL}' (expected safe https://, ssh://, or user@host:path Git URL)"
    fi
  fi
  if [[ -n "$AIO_REPO_BRANCH" ]]; then
    if [[ ! "$AIO_REPO_BRANCH" =~ ^[A-Za-z0-9._/-]+$ ]] || \
       [[ "$AIO_REPO_BRANCH" == -* || "$AIO_REPO_BRANCH" == .* || "$AIO_REPO_BRANCH" == /* || "$AIO_REPO_BRANCH" == */ || "$AIO_REPO_BRANCH" == *..* || "$AIO_REPO_BRANCH" == *//* || "$AIO_REPO_BRANCH" == *.lock ]]; then
      die "Invalid --aio-branch '${AIO_REPO_BRANCH}' (unsafe Git branch/tag ref)"
    fi
  fi
}

validate_media_dir() {
  [[ -n "$MEDIA_DIR" ]] || return 0
  [[ "$MEDIA_DIR" == /* && "$MEDIA_DIR" != *[,:]* ]] || \
    die "Invalid --media-dir '${MEDIA_DIR}' (must be an absolute path without commas or colons)"
  if [[ "$MEDIA_DIR" =~ [[:cntrl:]=] ]]; then
    die "Invalid --media-dir '${MEDIA_DIR}' (control characters and '=' are not allowed)"
  fi
  if [[ "$MEDIA_DIR" =~ (^|/)\.\.?(/|$) ]]; then
    die "Invalid --media-dir '${MEDIA_DIR}' (path traversal '.' or '..' segments are not allowed)"
  fi
  if (( CHOWN_MEDIA_DIR )); then
    local norm_media=""
    norm_media="$(normalize_mount_target "$MEDIA_DIR")"
    if is_protected_system_path "$norm_media" 1; then
      die "Refusing --chown-media-dir on protected system path '${MEDIA_DIR}'."
    fi
    if [[ "${DRY_RUN}" != "1" ]]; then
      if [[ -L "$MEDIA_DIR" ]]; then
        die "Refusing --chown-media-dir on symlink '${MEDIA_DIR}'."
      fi
      if command -v realpath >/dev/null 2>&1; then
        local resolved_media=""
        resolved_media="$(realpath -m -- "$MEDIA_DIR" 2>/dev/null || true)"
        if [[ -n "$resolved_media" ]] && is_protected_system_path "$resolved_media" 1; then
          die "Refusing --chown-media-dir on protected system path '${MEDIA_DIR}' (resolves to '${resolved_media}')."
        fi
      fi
    fi
  fi
}

validate_timezone_value() {
  if [[ -n "$AIO_TIMEZONE" ]]; then
    if [[ "$AIO_TIMEZONE" =~ (^|/)\.\.?(/|$) ]]; then
      die "Invalid --timezone '${AIO_TIMEZONE}' (path traversal segments are not allowed)"
    fi
    [[ "$AIO_TIMEZONE" =~ ^[A-Za-z0-9_+.-]+(/[A-Za-z0-9_+.-]+)*$ ]] || die "Invalid --timezone '${AIO_TIMEZONE}'"
    if [[ "${DRY_RUN}" != "1" && -d /usr/share/zoneinfo && ! -f "/usr/share/zoneinfo/${AIO_TIMEZONE}" ]]; then
      die "Unknown timezone '${AIO_TIMEZONE}' (not found under /usr/share/zoneinfo)"
    fi
  fi
}

validate_docktun_admin_password() {
  if [[ -n "$DOCKTUN_ADMIN_USER" ]]; then
    if [[ "$DOCKTUN_ADMIN_USER" == *$'\n'* || "$DOCKTUN_ADMIN_USER" == *$'\r'* || "$DOCKTUN_ADMIN_USER" =~ [[:cntrl:]] ]]; then
      die "--docktun-admin-user cannot contain newlines or control characters."
    fi
  fi
  if [[ -n "$DOCKTUN_ADMIN_PASSWORD" ]]; then
    if [[ "$DOCKTUN_ADMIN_PASSWORD" == *$'\n'* || "$DOCKTUN_ADMIN_PASSWORD" == *$'\r'* ]]; then
      die "--docktun-admin-password cannot contain newline characters."
    fi
    local pw_bytes=0
    pw_bytes="$(LC_ALL=C printf '%s' "$DOCKTUN_ADMIN_PASSWORD" | wc -c | tr -d '[:space:]')"
    if (( pw_bytes < 12 || pw_bytes > 72 )); then
      die "--docktun-admin-password must be 12-72 bytes (got ${pw_bytes} bytes; DockTun rejects out-of-range bootstrap passwords)."
    fi
  fi
  if (( TEMPLATE_HYGIENE )) && [[ -n "$DOCKTUN_ADMIN_PASSWORD" ]]; then
    die "--template-hygiene cannot be combined with --docktun-admin-password (avoids baking plaintext passwords into template compose.yml)."
  fi
}

validate_extra_mounts() {
  if (( ${#EXTRA_MOUNTS[@]} == 0 )); then
    return 0
  fi
  local mnt="" host_p="" ct_p="" norm_ct="" docktun_norm=""
  local -A seen_ct_paths=()
  [[ -n "$MEDIA_DIR" ]] && seen_ct_paths["/mnt/media"]=1
  docktun_norm="$(normalize_mount_target "$DOCKTUN_DIR")"
  for mnt in "${EXTRA_MOUNTS[@]}"; do
    [[ "$mnt" == *:* ]] || \
      die "Invalid --mount '${mnt}' (expected <host_path>:<ct_path>)"
    if [[ "$mnt" =~ [[:cntrl:]=] ]]; then
      die "Invalid --mount '${mnt}' (control characters and '=' are not allowed)"
    fi
    host_p="${mnt%%:*}"
    ct_p="${mnt#*:}"
    [[ "$host_p" == /* && "$ct_p" == /* && "$host_p" != *:* && "$ct_p" != *:* && "$host_p" != *,* && "$ct_p" != *,* ]] || \
      die "Invalid --mount '${mnt}' (both paths must be absolute without commas or extra colons)"
    if [[ "$host_p" =~ (^|/)\.\.?(/|$) || "$ct_p" =~ (^|/)\.\.?(/|$) ]]; then
      die "Invalid --mount '${mnt}': path traversal ('.' or '..') segments are not allowed"
    fi
    norm_ct="$(normalize_mount_target "$ct_p")"
    if is_protected_system_path "$norm_ct" 0; then
      die "Invalid --mount '${mnt}': refusing protected container target '${norm_ct}'"
    fi
    if [[ "$norm_ct" == "$docktun_norm" || "$norm_ct" == "${docktun_norm}/"* || "$docktun_norm" == "${norm_ct}/"* ]]; then
      die "Invalid --mount '${mnt}': container target collides with --docktun-dir (${docktun_norm})"
    fi
    if [[ -n "${seen_ct_paths[$norm_ct]+x}" ]]; then
      die "Duplicate container mount target '${norm_ct}' in --mount / --media-dir"
    fi
    seen_ct_paths["$norm_ct"]=1
  done
}

validate_early_cli_inputs() {
  validate_mode_flags
  if (( LIST_MODE )); then
    return 0
  fi
  validate_gpu_and_pci_slot
  if [[ -n "$CTID" ]]; then
    [[ "$CTID" =~ ^[1-9][0-9]*$ ]] && (( 10#$CTID >= 100 )) || die "Invalid CTID '${CTID}'; must be an integer >= 100 without leading zeros."
  elif (( PROVISION_ONLY || DOCTOR_MODE || DESTROY_MODE )); then
    die "A CTID (>= 100) is required."
  fi
  if (( HOST_PREP_ONLY || DESTROY_MODE )); then
    return 0
  fi
  validate_docktun_dir
  if (( DOCTOR_MODE )); then
    return 0
  fi
  validate_timezone_value
  validate_docktun_admin_password
  validate_startup_spec "$STARTUP_SPEC"
  validate_aio_compose_file
  validate_aio_repo_opts
  validate_media_dir
  validate_extra_mounts
}

validate_inputs() {
  validate_mode_flags

  if (( LIST_MODE )); then
    return 0
  fi

  validate_gpu_and_pci_slot

  if [[ "$HOST_PREP_ONLY" == "1" ]]; then
    resolve_effective_gpus
    return 0
  fi

  [[ "$CTID" =~ ^[1-9][0-9]*$ ]] && (( 10#$CTID >= 100 )) || die "Invalid CTID '${CTID}'; must be an integer >= 100 without leading zeros."

  if (( DESTROY_MODE )); then
    return 0
  fi

  [[ "$DOCKTUN_PORT" =~ ^[1-9][0-9]*$ ]] && (( 10#$DOCKTUN_PORT >= 1 && 10#$DOCKTUN_PORT <= 65535 )) || die "Invalid --docktun-port '${DOCKTUN_PORT}'"
  validate_docktun_dir
  [[ "$ADMIN_USER" =~ ^[a-z_][a-z0-9_-]*$ ]] || die "Invalid --admin-user '${ADMIN_USER}' (must be a valid Linux username)"
  case "$ADMIN_USER" in
    root|daemon|bin|sys|sync|games|man|lp|mail|news|uucp|proxy|www-data|backup|list|irc|_apt|nobody|systemd-network|systemd-resolve|messagebus|sshd|docker|ubuntu|debian)
      die "Invalid --admin-user '${ADMIN_USER}': system account names cannot be used."
      ;;
  esac

  if (( DOCTOR_MODE )); then
    resolve_effective_gpus
    return 0
  fi

  if (( PROVISION_ONLY )) && (( ${#CREATION_FLAGS_USED[@]} > 0 )); then
    log_warn "--provision-only ignores container-creation flags (${CREATION_FLAGS_USED[*]})."
  fi

  validate_timezone_value
  if (( 10#$DOCKTUN_PORT == 80 && WITH_ZEROCONF == 1 )); then
    die "--docktun-port 80 conflicts with docktun-zeroconf (port 80). Use a different --docktun-port or pass --no-zeroconf."
  fi
  validate_docktun_admin_password
  validate_aio_compose_file
  validate_aio_repo_opts
  [[ "$AIO_DOMAIN" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$ ]] || \
    die "Invalid --domain '${AIO_DOMAIN}' (expected valid hostname)"

  if [[ "$LAN_SUBNETS" == *$'\n'* || "$LAN_SUBNETS" == *$'\r'* || "$LAN_SUBNETS" =~ [[:cntrl:]] ]]; then
    die "Invalid --lan-subnets (newlines and control characters are not allowed)"
  fi
  local -a lan_tokens=()
  local tok="" valid_lan_count=0
  IFS=',' read -ra lan_tokens <<< "$LAN_SUBNETS"
  for tok in "${lan_tokens[@]}"; do
    tok="${tok#"${tok%%[![:space:]]*}"}"
    tok="${tok%"${tok##*[![:space:]]}"}"
    [[ -z "$tok" ]] && continue
    is_valid_cidr "$tok" || die "Invalid CIDR '${tok}' in --lan-subnets"
    valid_lan_count=$((valid_lan_count + 1))
  done
  (( valid_lan_count > 0 )) || die "--lan-subnets must contain at least one valid IPv4 or IPv6 CIDR."

  local inline_key="" file_keys="" combined_keys=""
  if [[ -n "$SSH_PUBKEY" ]]; then
    SSH_PUBKEY="${SSH_PUBKEY//$'\r'/}"
    inline_key="$(grep -vE '^[[:space:]]*(#|$)' <<< "$SSH_PUBKEY" | head -n1 || true)"
    is_valid_ssh_pubkey_line "$inline_key" || die "--ssh-pubkey did not contain a valid SSH public key"
    SSH_PUBKEY="$inline_key"
  fi
  if [[ -n "$SSH_PUBKEY_FILE" ]]; then
    [[ -r "$SSH_PUBKEY_FILE" && -s "$SSH_PUBKEY_FILE" ]] || die "SSH public key file not readable or empty: ${SSH_PUBKEY_FILE}"
    local line=""
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%$'\r'}"
      if is_valid_ssh_pubkey_line "$line"; then
        file_keys+="${line}"$'\n'
      fi
    done < "$SSH_PUBKEY_FILE"
    [[ -n "$file_keys" ]] || die "No valid SSH public key found in ${SSH_PUBKEY_FILE}"
  fi

  if [[ -n "$inline_key" && -n "$file_keys" ]]; then
    combined_keys="${inline_key}"$'\n'"${file_keys}"
    SSH_PUBKEY="${combined_keys%$'\n'}"
    if [[ "${DRY_RUN}" != "1" ]]; then
      ensure_tmp_dir
      SSH_PUBKEY_FILE="${TMP_WORK_DIR}/authorized_key.pub"
      printf '%s\n' "$SSH_PUBKEY" > "$SSH_PUBKEY_FILE"
      chmod 0644 "$SSH_PUBKEY_FILE"
    fi
  elif [[ -n "$inline_key" ]]; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      SSH_PUBKEY_FILE="/tmp/dry-run-authorized_key.pub"
    else
      ensure_tmp_dir
      SSH_PUBKEY_FILE="${TMP_WORK_DIR}/authorized_key.pub"
      printf '%s\n' "$SSH_PUBKEY" > "$SSH_PUBKEY_FILE"
      chmod 0644 "$SSH_PUBKEY_FILE"
    fi
  elif [[ -n "$file_keys" ]]; then
    SSH_PUBKEY="${file_keys%$'\n'}"
  else
    log_warn "No SSH public key configured; container SSH will retain PasswordAuthentication yes, but '${ADMIN_USER}' has no password set yet (PermitRootLogin is prohibit-password). Enter via 'pct enter ${CTID}' and run 'passwd ${ADMIN_USER}' to enable SSH login."
  fi

  if (( ! PROVISION_ONLY )); then
    [[ "$CORES" =~ ^[1-9][0-9]*$ ]] && (( 10#$CORES >= 1 )) || die "Invalid --cores '${CORES}'"
    [[ "$MEMORY" =~ ^[1-9][0-9]*$ ]] && (( 10#$MEMORY >= 512 )) || die "Invalid --memory '${MEMORY}' (min 512)"
    if (( 10#$MEMORY < 1024 )); then
      log_warn "Memory ${MEMORY} MiB yields /dev/shm < 512 MiB (Proxmox LXC defaults /dev/shm to 50% of RAM); consider >= 1024 MiB for Docker/FFmpeg/Postgres workloads."
    fi
    [[ "$SWAP" =~ ^(0|[1-9][0-9]*)$ ]] || die "Invalid --swap '${SWAP}'"
    [[ "$DISK" =~ ^[1-9][0-9]*$ ]] && (( 10#$DISK >= 4 )) || die "Invalid --disk '${DISK}' (min 4 GiB)"
    [[ -n "$HOSTNAME" && "$HOSTNAME" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$ ]] || \
      die "Invalid --hostname '${HOSTNAME}'"
    [[ -n "$STORAGE" ]] || die "--storage cannot be empty"
    [[ -n "$TEMPLATE_STORAGE" ]] || die "--template-storage cannot be empty"
    [[ -n "$BRIDGE" ]] || die "--bridge cannot be empty"

    if [[ -n "$VLAN_TAG" ]]; then
      [[ "$VLAN_TAG" =~ ^[1-9][0-9]*$ ]] && (( 10#$VLAN_TAG >= 1 && 10#$VLAN_TAG <= 4094 )) || \
        die "Invalid --vlan '${VLAN_TAG}' (expected integer 1-4094)"
    fi
    if [[ -n "$MTU_BYTES" ]]; then
      [[ "$MTU_BYTES" =~ ^[1-9][0-9]*$ ]] && (( 10#$MTU_BYTES >= 576 && 10#$MTU_BYTES <= 65535 )) || \
        die "Invalid --mtu '${MTU_BYTES}' (expected integer 576-65535)"
    fi
    validate_startup_spec "$STARTUP_SPEC"
    validate_media_dir
    validate_extra_mounts

    case "$OS_CHOICE" in
      debian12|debian13|ubuntu2404) ;;
      *) die "Unsupported --os '${OS_CHOICE}'. Expected: debian12|debian13|ubuntu2404" ;;
    esac

    if [[ "${IP_ADDR,,}" != "dhcp" ]]; then
      is_valid_ipv4_cidr "$IP_ADDR" || die "Invalid --ip '${IP_ADDR}'; expected IPv4 CIDR (e.g. 192.168.1.50/24) or 'dhcp'."
      local ip_prefix="${IP_ADDR#*/}"
      if (( 10#$ip_prefix < 1 || 10#$ip_prefix > 32 )); then
        die "Invalid --ip '${IP_ADDR}'; CIDR prefix must be between 1 and 32."
      fi
      if [[ -z "$GW_ADDR" ]]; then
        log_warn "Static IP '${IP_ADDR}' specified without --gw; container may lack a default route."
      fi
    else
      IP_ADDR="dhcp"
    fi
    if [[ -n "$GW_ADDR" ]]; then
      is_valid_ipv4 "$GW_ADDR" || die "Invalid --gw '${GW_ADDR}'; expected valid IPv4 address."
    fi
    if [[ -n "$DNS_ADDR" ]]; then
      [[ "$DNS_ADDR" != *,* ]] || die "Invalid --dns '${DNS_ADDR}'; separate multiple nameservers with spaces, not commas."
      local dns_tok=""
      for dns_tok in $DNS_ADDR; do
        is_valid_ipv4 "$dns_tok" || is_valid_ipv6 "$dns_tok" || die "Invalid --dns entry '${dns_tok}'"
      done
    fi

    if check_ctid_exists_cluster "$CTID"; then
      die "CTID ${CTID} already exists in Proxmox cluster."
    fi
  fi

  resolve_effective_gpus
}

# ============================================================================
# Proxmox Host Preparation
# ============================================================================

write_file_managed() {
  local target="$1"
  local mode="$2"
  local content="$3"

  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would write ${target} (mode ${mode})"
    return 0
  fi

  local dir="" tmp_file=""
  dir="$(dirname "$target")"
  mkdir -p "$dir"
  tmp_file="$(mktemp "${target}.XXXXXX")"
  printf '%s' "$content" > "$tmp_file"
  chmod "$mode" "$tmp_file"
  mv "$tmp_file" "$target"
}

backup_once() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  local backup_dir="${APT_BACKUP_DIR:-/var/backups/proxmox-lxc-apt}"
  local flat="${file#/}"
  flat="${flat//\//_}"
  local backup_path="${backup_dir}/${flat}.bak"
  if [[ ! -f "$backup_path" ]]; then
    run_cmd mkdir -p "$backup_dir"
    run_cmd cp -a "$file" "$backup_path"
  fi
  return 0
}

enable_nonfree_in_list_file() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  local updated="" rc=0
  updated="$(awk '
    BEGIN { changed = 0 }
    /^[[:space:]]*(deb|deb-src)[[:space:]]+/ {
      if (($0 ~ /([a-z0-9-]+\.)*debian\.org/ || $0 ~ /https?:\/\/[^[:space:]]+\/debian(-security)?([[:space:]]|\/|$)/) && $0 !~ /proxmox\.com/) {
        line = $0
        comment = ""
        hash_idx = index(line, "#")
        if (hash_idx > 0) {
          comment = substr(line, hash_idx)
          line = substr(line, 1, hash_idx - 1)
          sub(/[[:space:]]+$/, "", line)
        }
        has_nf = (line ~ /(^|[[:space:]])non-free([[:space:]]|$)/)
        has_nff = (line ~ /(^|[[:space:]])non-free-firmware([[:space:]]|$)/)
        if (!has_nf) {
          line = line " non-free"
          changed = 1
        }
        if (!has_nff) {
          line = line " non-free-firmware"
          changed = 1
        }
        if (comment != "") {
          line = line " " comment
        }
        print line
        next
      }
    }
    { print }
    END { if (changed == 0) exit 10 }
  ' "$file")" && rc=0 || rc=$?

  if [[ "$rc" -eq 0 ]]; then
    log_info "Updating legacy APT list for non-free/non-free-firmware: ${file}"
    backup_once "$file"
    write_file_managed "$file" 0644 "${updated}"$'\n'
  elif [[ "$rc" -eq 10 ]]; then
    log_info "Legacy APT list already configured or non-Debian: ${file}"
  else
    die "Failed processing ${file}"
  fi
}

enable_nonfree_in_sources_file() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  local updated="" rc=0
  updated="$(awk '
    function strip_comment(s,    idx) {
      idx = index(s, "#")
      if (idx > 0) s = substr(s, 1, idx - 1)
      return s
    }
    function flush_stanza(    i, cur_key, code, key, rest, uris_val, enabled_val, comp_val, comp_last_idx, has_debian, enabled_ok, has_nf, has_nff, cl, cidx, ccomment) {
      if (n == 0) return
      cur_key = ""
      uris_val = ""
      enabled_val = ""
      comp_val = ""
      comp_last_idx = 0
      for (i = 1; i <= n; i++) {
        if (lines[i] ~ /^[[:space:]]*#/) {
          continue
        }
        if (lines[i] ~ /^[A-Za-z0-9_-]+:/) {
          code = strip_comment(lines[i])
          key = code
          sub(/:.*$/, "", key)
          rest = code
          sub(/^[A-Za-z0-9_-]+:[[:space:]]*/, "", rest)
          cur_key = tolower(key)
          if (cur_key == "uris") uris_val = uris_val " " rest
          else if (cur_key == "enabled") enabled_val = enabled_val " " rest
          else if (cur_key == "components") {
            comp_val = comp_val " " rest
            comp_last_idx = i
          }
        } else if (lines[i] ~ /^[[:space:]]+/ && cur_key != "") {
          rest = strip_comment(lines[i])
          if (cur_key == "uris") uris_val = uris_val " " rest
          else if (cur_key == "enabled") enabled_val = enabled_val " " rest
          else if (cur_key == "components") {
            comp_val = comp_val " " rest
            comp_last_idx = i
          }
        } else {
          cur_key = ""
        }
      }
      has_debian = ((uris_val ~ /([a-z0-9-]+\.)*debian\.org/ || uris_val ~ /https?:\/\/[^[:space:]]+\/debian(-security)?([[:space:]]|\/|$)/) && uris_val !~ /proxmox\.com/)
      enabled_ok = (tolower(enabled_val) !~ /(^|[[:space:]])(no|false|0)([[:space:]]|$)/)
      if (has_debian && enabled_ok && comp_last_idx > 0) {
        has_nf = (comp_val ~ /(^|[[:space:]])non-free([[:space:]]|$)/)
        has_nff = (comp_val ~ /(^|[[:space:]])non-free-firmware([[:space:]]|$)/)
        if (!has_nf || !has_nff) {
          cl = lines[comp_last_idx]
          ccomment = ""
          cidx = index(cl, "#")
          if (cidx > 0) {
            ccomment = substr(cl, cidx)
            cl = substr(cl, 1, cidx - 1)
          }
          sub(/[[:space:]]+$/, "", cl)
          if (!has_nf) {
            cl = cl " non-free"
            changed = 1
          }
          if (!has_nff) {
            cl = cl " non-free-firmware"
            changed = 1
          }
          if (ccomment != "") {
            cl = cl " " ccomment
          }
          lines[comp_last_idx] = cl
        }
      }
      for (i = 1; i <= n; i++) {
        out[++out_n] = lines[i]
      }
      n = 0
    }
    BEGIN { n = 0; out_n = 0; changed = 0 }
    /^[[:space:]]*$/ {
      flush_stanza()
      out[++out_n] = $0
      next
    }
    {
      lines[++n] = $0
    }
    END {
      flush_stanza()
      if (changed == 0) exit 10
      for (i = 1; i <= out_n; i++) {
        print out[i]
      }
    }
  ' "$file")" && rc=0 || rc=$?

  if [[ "$rc" -eq 0 ]]; then
    log_info "Updating deb822 APT source for non-free/non-free-firmware: ${file}"
    backup_once "$file"
    write_file_managed "$file" 0644 "${updated}"$'\n'
  elif [[ "$rc" -eq 10 ]]; then
    log_info "deb822 APT source already configured or non-Debian: ${file}"
  else
    die "Failed processing ${file}"
  fi
}

configure_host_apt_nonfree() {
  log_info "Ensuring host Debian non-free and non-free-firmware components are enabled..."
  local apt_dir="${HOST_APT_DIR:-/etc/apt}"
  local f=""
  if [[ -f "${apt_dir}/sources.list" ]]; then
    enable_nonfree_in_list_file "${apt_dir}/sources.list"
  fi
  local -a list_files=() source_files=()
  glob_matches list_files "${apt_dir}/sources.list.d/*.list"
  glob_matches source_files "${apt_dir}/sources.list.d/*.sources"
  for f in ${list_files[@]+"${list_files[@]}"}; do
    enable_nonfree_in_list_file "$f"
  done
  for f in ${source_files[@]+"${source_files[@]}"}; do
    enable_nonfree_in_sources_file "$f"
  done
}

configure_host_network_modules() {
  local mods=(
    tun
    wireguard
    br_netfilter
    ip_tables
    iptable_filter
    iptable_nat
    xt_conntrack
    xt_MASQUERADE
    nf_nat
  )
  local content="# Managed by proxmox-lxc.sh for LXC Docker / DockTun / WireGuard networking"$'\n'
  local m=""
  for m in "${mods[@]}"; do
    content+="${m}"$'\n'
  done

  log_info "Persisting networking kernel modules in /etc/modules-load.d/media-lxc-net.conf"
  write_file_managed "/etc/modules-load.d/media-lxc-net.conf" 0644 "$content"

  for m in "${mods[@]}"; do
    if ! run_cmd modprobe "$m"; then
      log_warn "Could not modprobe '${m}' (may be built-in or unavailable on this kernel)."
    fi
  done
}

configure_host_sysctl() {
  log_info "Configuring host global sysctls in /etc/sysctl.d/99-media-lxc.conf"
  local content=""
  content="$(cat <<'EOF'
# Managed by proxmox-lxc.sh for media/Docker/Redis/AIOStreams LXC workloads
vm.overcommit_memory = 1
fs.inotify.max_user_watches = 524288
fs.inotify.max_user_instances = 1024
net.core.somaxconn = 4096
EOF
)"
  write_file_managed "/etc/sysctl.d/99-media-lxc.conf" 0644 "${content}"$'\n'
  if command -v sysctl >/dev/null 2>&1; then
    run_cmd sysctl -p /etc/sysctl.d/99-media-lxc.conf || run_cmd sysctl --system
  fi
}

setup_nvidia_host() {
  log_info "Configuring NVIDIA host driver & pre-start device initialization..."

  local kver=""
  kver="$(uname -r)"
  local cuda_repo="debian12"
  case "${OS_CODENAME:-}" in
    trixie) cuda_repo="debian13" ;;
    bookworm) cuda_repo="debian12" ;;
    *)
      log_warn "Unrecognized host Debian codename '${OS_CODENAME:-unknown}'; defaulting CUDA repo to debian12."
      cuda_repo="debian12"
      ;;
  esac

  local nv_driver_present=0
  if [[ -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    nv_driver_present=1
  elif command -v dpkg-query >/dev/null 2>&1; then
    local nv_pkg=""
    for nv_pkg in nvidia-kernel-dkms nvidia-kernel-open-dkms nvidia-open-kernel-dkms nvidia-open; do
      if dpkg-query -W -f='${Status}\n' "$nv_pkg" 2>/dev/null | grep -qE '^(install|hold) ok installed$'; then
        nv_driver_present=1
        break
      fi
    done
  fi

  local need_headers=0
  [[ ! -d "/lib/modules/${kver}/build" ]] && need_headers=1

  if (( need_headers || ! nv_driver_present )); then
    run_cmd apt-get update
  fi

  if (( need_headers )); then
    local -a hdr_pkgs=()
    local hcand=""
    for hcand in "proxmox-headers-${kver}" "proxmox-default-headers" "pve-headers-${kver}" "linux-headers-${kver}"; do
      if [[ "${DRY_RUN}" == "1" ]] || apt-cache show "$hcand" >/dev/null 2>&1; then
        hdr_pkgs+=("$hcand")
        break
      fi
    done
    if (( ${#hdr_pkgs[@]} > 0 )); then
      log_info "Ensuring kernel headers (${hdr_pkgs[*]})..."
      run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y "${hdr_pkgs[@]}" curl gnupg ca-certificates
    else
      log_warn "No kernel headers package matched for '${kver}'; installing base tools only."
      run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y curl gnupg ca-certificates
    fi
  else
    log_info "Kernel headers already present at /lib/modules/${kver}/build."
  fi

  log_info "Blacklisting nouveau in /etc/modprobe.d/blacklist-nouveau.conf"
  local nouveau_conf=""
  nouveau_conf="$(cat <<'EOF'
blacklist nouveau
options nouveau modeset=0
EOF
)"
  write_file_managed "/etc/modprobe.d/blacklist-nouveau.conf" 0644 "${nouveau_conf}"$'\n'

  if (( nv_driver_present )); then
    log_info "Existing NVIDIA host driver detected (active module or DKMS/open package); skipping CUDA driver install while configuring udev/modprobe/prestart."
  else
    if (( ! need_headers )); then
      run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y curl gnupg ca-certificates
    fi
    local list_file="/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.list"
    local sources_file="/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.sources"
    local -a old_cuda_files=()
    glob_matches old_cuda_files "/etc/apt/sources.list.d/cuda-debian*.list" "/etc/apt/sources.list.d/cuda-debian*.sources"
    local f=""
    for f in ${old_cuda_files[@]+"${old_cuda_files[@]}"}; do
      if [[ "$f" != "$list_file" && "$f" != "$sources_file" ]]; then
        run_cmd rm -f "$f"
      fi
    done

    local keyring_url="https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64/cuda-keyring_1.1-1_all.deb"
    ensure_tmp_dir
    local keyring_tmp="${TMP_WORK_DIR}/cuda-keyring_1.1-1_all.deb"
    if [[ ! -s /usr/share/keyrings/cuda-archive-keyring.gpg ]]; then
      run_cmd curl -fsSL "$keyring_url" -o "$keyring_tmp"
      run_cmd dpkg -i "$keyring_tmp"
    fi

    if [[ ! -f "$list_file" && ! -f "$sources_file" ]]; then
      local cuda_list="deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64/ /"
      write_file_managed "$list_file" 0644 "${cuda_list}"$'\n'
    fi

    run_cmd apt-get update
    run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y \
      nvidia-driver-cuda nvidia-kernel-dkms nvidia-persistenced

    if [[ "${DRY_RUN}" != "1" ]] && command -v dkms >/dev/null 2>&1; then
      local dkms_out=""
      dkms_out="$(dkms status 2>/dev/null || true)"
      [[ -n "$dkms_out" ]] && log_info "DKMS status: ${dkms_out//$'\n'/; }"
      if grep -qiE 'nvidia.*(broken|error)' <<< "$dkms_out"; then
        die "NVIDIA DKMS module build failed for kernel ${kver}; inspect 'dkms status' and /var/lib/dkms/nvidia/*/build/make.log."
      fi
    fi
  fi

  log_info "Enabling nvidia-drm modeset=1 and persisting NVIDIA modules at boot..."
  write_file_managed "/etc/modprobe.d/nvidia-drm.conf" 0644 $'options nvidia-drm modeset=1\n'

  local nv_mods=""
  nv_mods="$(cat <<'EOF'
nvidia
nvidia_modeset
nvidia_uvm
nvidia_drm
EOF
)"
  write_file_managed "/etc/modules-load.d/nvidia.conf" 0644 "${nv_mods}"$'\n'

  log_info "Installing /etc/udev/rules.d/70-nvidia-lxc.rules for unprivileged nvidia-caps permissions..."
  local nv_udev_rules=""
  nv_udev_rules="$(cat <<'EOF'
# Allow unprivileged LXC containers to access NVIDIA capability device nodes
SUBSYSTEM=="nvidia-caps", KERNEL=="nvidia-cap*", MODE="0666"
EOF
)"
  write_file_managed "/etc/udev/rules.d/70-nvidia-lxc.rules" 0644 "${nv_udev_rules}"$'\n'
  if command -v udevadm >/dev/null 2>&1; then
    run_cmd udevadm control --reload-rules || log_warn "udevadm control --reload-rules failed (udev daemon may not be running)."
    run_cmd udevadm trigger --subsystem-match=nvidia-caps || true
  fi

  if (( ! nv_driver_present )) && command -v update-initramfs >/dev/null 2>&1; then
    run_cmd update-initramfs -u -k all
  fi

  if [[ "${DRY_RUN}" != "1" && ! -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    if [[ ! -d /sys/module/nouveau ]]; then
      log_info "nouveau is not loaded; attempting to modprobe NVIDIA kernel modules..."
      local nvm=""
      for nvm in nvidia nvidia_modeset nvidia_uvm nvidia_drm; do
        modprobe "$nvm" >/dev/null 2>&1 || true
      done
    fi
  fi

  log_info "Installing nvidia-lxc-prestart.service (Before=pve-guests.service)..."
  local service_unit=""
  service_unit="$(cat <<'EOF'
[Unit]
Description=Pre-initialize NVIDIA device nodes for Proxmox LXCs before guest startup
After=systemd-modules-load.service nvidia-persistenced.service
Before=pve-guests.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'found=0; for f in /proc/driver/nvidia/gpus/*/information; do [ -r "$f" ] || continue; m=$(awk "/Device Minor:/ {print \$3; exit}" "$f" 2>/dev/null); [ -n "$m" ] && /usr/bin/nvidia-modprobe -u -c"$m" -m >/dev/null 2>&1 && found=1 || true; done; [ "$found" -eq 0 ] && /usr/bin/nvidia-modprobe -u -c0 -m >/dev/null 2>&1 || true; /usr/bin/nvidia-smi >/dev/null 2>&1 || true; udevadm settle --timeout=5 >/dev/null 2>&1 || true'

[Install]
WantedBy=multi-user.target
EOF
)"
  write_file_managed "/etc/systemd/system/nvidia-lxc-prestart.service" 0644 "${service_unit}"$'\n'

  if command -v systemctl >/dev/null 2>&1; then
    run_cmd systemctl daemon-reload
    if [[ "${DRY_RUN}" == "1" ]] || systemctl list-unit-files nvidia-persistenced.service 2>/dev/null | grep -q 'nvidia-persistenced\.service'; then
      run_cmd systemctl enable nvidia-persistenced.service || true
      run_cmd systemctl start nvidia-persistenced.service || log_warn "nvidia-persistenced start deferred until reboot."
    fi
    run_cmd systemctl enable nvidia-lxc-prestart.service
    run_cmd systemctl start nvidia-lxc-prestart.service || true
  fi

  if [[ "${DRY_RUN}" != "1" && ! -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    log_warn "NVIDIA kernel module is not yet active (nouveau may still be loaded). Reboot the Proxmox host before creating/starting NVIDIA LXCs."
  fi
  return 0
}

setup_intel_host() {
  log_info "Configuring Intel iGPU/Arc host tools..."
  if [[ "$DETECTED_INTEL_DRIVER" == "xe" ]]; then
    log_info "Intel 'xe' kernel driver detected (GuC/HuC enabled natively; skipping i915 modprobe options)."
  elif [[ "$DETECTED_INTEL_DRIVER" == "i915" ]]; then
    log_info "Intel 'i915' kernel driver detected (retaining safe kernel defaults without forcing enable_guc=3)."
  fi
  run_cmd apt-get update
  run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y intel-gpu-tools
}

setup_amd_host() {
  log_info "Configuring AMD GPU/APU host support..."
  if dpkg-query -W -f='${Status}\n' pve-firmware 2>/dev/null | grep -qE '^(install|hold) ok installed$'; then
    log_info "pve-firmware is installed (already includes AMD GPU firmware; skipping firmware-amd-graphics)."
  elif command -v pveversion >/dev/null 2>&1; then
    log_info "Proxmox VE host detected; skipping firmware-amd-graphics to avoid conflicting with pve-firmware."
  else
    run_cmd apt-get update
    run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y firmware-amd-graphics
  fi
}

setup_wolf_input_host() {
  log_info "Configuring Wolf virtual input modules (uinput, uhid) and udev rules..."
  local wolf_mods=""
  wolf_mods="$(cat <<'EOF'
uinput
uhid
EOF
)"
  write_file_managed "/etc/modules-load.d/wolf-input.conf" 0644 "${wolf_mods}"$'\n'
  run_cmd modprobe uinput || log_warn "Could not load uinput module."
  run_cmd modprobe uhid || log_warn "Could not load uhid module."

  local udev_rules=""
  udev_rules="$(cat <<'EOF'
# Virtual input permissions and seat isolation for Wolf/Moonlight streaming containers
KERNEL=="uinput", SUBSYSTEM=="misc", MODE="0660", GROUP="input", OPTIONS+="static_node=uinput"
KERNEL=="uhid", SUBSYSTEM=="misc", MODE="0660", GROUP="input", OPTIONS+="static_node=uhid"
SUBSYSTEM=="input", DEVPATH=="*/virtual/input/*", MODE="0666", ENV{ID_SEAT}="seat9"
KERNEL=="hidraw*", ATTRS{name}=="*Wolf*", MODE="0666", ENV{ID_SEAT}="seat9"
EOF
)"
  write_file_managed "/etc/udev/rules.d/85-wolf-virtual-inputs.rules" 0644 "${udev_rules}"$'\n'

  if command -v udevadm >/dev/null 2>&1; then
    run_cmd udevadm control --reload-rules || log_warn "udevadm control --reload-rules failed (udev daemon may not be running)."
    run_cmd udevadm trigger --subsystem-match=misc || true
    run_cmd udevadm trigger --subsystem-match=input || true
  fi
  return 0
}

preflight_host_checks() {
  if (( PREFLIGHT_DONE )); then
    return 0
  fi
  PREFLIGHT_DONE=1

  local apt_dir="${HOST_APT_DIR:-/etc/apt}"
  local -a apt_files=() extra_lists=() extra_sources=()
  [[ -f "${apt_dir}/sources.list" ]] && apt_files+=("${apt_dir}/sources.list")
  glob_matches extra_lists "${apt_dir}/sources.list.d/*.list"
  glob_matches extra_sources "${apt_dir}/sources.list.d/*.sources"
  apt_files+=(${extra_lists[@]+"${extra_lists[@]}"} ${extra_sources[@]+"${extra_sources[@]}"})

  local af="" has_active_enterprise=0
  for af in ${apt_files[@]+"${apt_files[@]}"}; do
    [[ -r "$af" ]] || continue
    if [[ "$af" == *.sources ]]; then
      if awk '
        function strip_comment(s, idx) {
          idx = index(s, "#")
          if (idx > 0) s = substr(s, 1, idx - 1)
          return s
        }
        BEGIN { ent=0; dis=0; found=0; cur_key="" }
        /^[[:space:]]*$/ { if (ent && !dis) found=1; ent=0; dis=0; cur_key=""; next }
        /^[[:space:]]*#/ { next }
        /^[A-Za-z0-9_-]+:/ {
          line = strip_comment($0)
          key = line; sub(/:.*$/, "", key); cur_key = tolower(key)
          rest = line; sub(/^[A-Za-z0-9_-]+:[[:space:]]*/, "", rest)
          if (cur_key == "uris" && rest ~ /enterprise\.proxmox\.com/) ent = 1
          if (cur_key == "enabled" && tolower(rest) ~ /(^|[[:space:]])(no|false|0)([[:space:]]|$)/) dis = 1
          next
        }
        /^[[:space:]]+/ && cur_key != "" {
          rest = strip_comment($0)
          if (cur_key == "uris" && rest ~ /enterprise\.proxmox\.com/) ent = 1
          if (cur_key == "enabled" && tolower(rest) ~ /(^|[[:space:]])(no|false|0)([[:space:]]|$)/) dis = 1
          next
        }
        { cur_key = "" }
        END { if (ent && !dis) found=1; exit !found }
      ' "$af" 2>/dev/null; then
        has_active_enterprise=1
        break
      fi
    else
      if grep -qE '^[[:space:]]*(deb|deb-src)[[:space:]].*enterprise\.proxmox\.com' "$af" 2>/dev/null; then
        has_active_enterprise=1
        break
      fi
    fi
  done

  if (( has_active_enterprise )); then
    local sub_active=0
    if command -v pvesubscription >/dev/null 2>&1; then
      if pvesubscription get 2>/dev/null | grep -qiE 'status:[[:space:]]*active'; then
        sub_active=1
      fi
    fi
    if (( ! sub_active )); then
      log_warn "Active 'enterprise.proxmox.com' APT repository detected without an active PVE subscription; APT updates may fail with 401 Unauthorized unless switched to pve-no-subscription."
    fi
  fi

  local amd_needs_apt=0
  if (( ${DO_AMD:-0} )); then
    if ! { command -v dpkg-query >/dev/null 2>&1 && dpkg-query -W -f='${Status}\n' pve-firmware 2>/dev/null | grep -qE '^(install|hold) ok installed$'; } && \
       ! command -v pveversion >/dev/null 2>&1; then
      amd_needs_apt=1
    fi
  fi
  local skip_net_probe=0
  if (( HOST_PREP_ONLY )) && [[ "$GPU_MODE" == "none" || ( "${GPUS_RESOLVED:-0}" == "1" && "${DO_NVIDIA:-0}" == "0" && "${DO_INTEL:-0}" == "0" && "$amd_needs_apt" == "0" ) ]]; then
    skip_net_probe=1
  fi
  if (( skip_net_probe )); then
    log_info "Skipping remote deb.debian.org network probe (--host-prep-only with no GPU APT installs required)."
    return 0
  fi

  local probe_proxy="" env_proxy=""
  if command -v apt-config >/dev/null 2>&1; then
    local apt_cfg_out="" apt_https_proxy="" apt_http_proxy="" cand_proxy=""
    apt_cfg_out="$(apt-config dump Acquire::http::Proxy Acquire::https::Proxy 2>/dev/null || true)"
    apt_https_proxy="$(awk -F'"' '/^Acquire::https::Proxy "/ {print $2; exit}' <<< "$apt_cfg_out" || true)"
    apt_http_proxy="$(awk -F'"' '/^Acquire::http::Proxy "/ {print $2; exit}' <<< "$apt_cfg_out" || true)"
    for cand_proxy in "$apt_https_proxy" "$apt_http_proxy"; do
      if [[ -n "$cand_proxy" && "${cand_proxy^^}" != "DIRECT" && "${cand_proxy,,}" != "false" ]]; then
        probe_proxy="$cand_proxy"
        break
      fi
    done
  fi
  if [[ -z "$probe_proxy" ]]; then
    env_proxy="${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}"
  fi

  local net_fail=0
  if [[ -n "$probe_proxy" ]]; then
    if command -v curl >/dev/null 2>&1; then
      if ! curl -fsSIL --max-time 5 --proxy "$probe_proxy" https://deb.debian.org >/dev/null 2>&1 && \
         ! curl -fsSIL --max-time 5 --proxy "$probe_proxy" http://deb.debian.org >/dev/null 2>&1; then
        net_fail=1
      fi
    fi
  elif [[ -n "$env_proxy" ]]; then
    if command -v curl >/dev/null 2>&1; then
      if ! curl -fsSIL --max-time 5 https://deb.debian.org >/dev/null 2>&1 && \
         ! curl -fsSIL --max-time 5 http://deb.debian.org >/dev/null 2>&1; then
        net_fail=1
      fi
    fi
  else
    if command -v getent >/dev/null 2>&1 && ! getent ahosts deb.debian.org >/dev/null 2>&1; then
      net_fail=1
    fi
    if (( ! net_fail )) && command -v curl >/dev/null 2>&1; then
      if ! curl -fsSIL --max-time 5 https://deb.debian.org >/dev/null 2>&1; then
        net_fail=1
      fi
    fi
  fi
  if (( net_fail )); then
    if [[ "${DRY_RUN}" == "1" ]]; then
      log_warn "[DRY-RUN] Host pre-flight DNS/HTTPS check to deb.debian.org failed; verify host DNS/proxy/firewall before live APT/pveam operations."
    else
      die "Host pre-flight network check failed (DNS/HTTPS to deb.debian.org unreachable). Verify /etc/resolv.conf, gateway, proxy, or firewall before running APT/pveam."
    fi
  fi
}

run_host_preparation() {
  preflight_host_checks
  log_info "Preparing Proxmox VE host (shared driver mode, never vfio-pci)..."
  if command -v lspci >/dev/null 2>&1; then
    if [[ -n "$PCI_SLOT" ]]; then
      local slot_drv=""
      slot_drv="$(get_pci_kernel_driver "$PCI_SLOT" || true)"
      if [[ "$slot_drv" == "vfio-pci" ]]; then
        die "Target GPU ${PCI_SLOT} is bound to vfio-pci! Shared LXC passthrough requires the host kernel driver (never vfio-pci)."
      fi
    else
      local vfio_check=""
      vfio_check="$(lspci -k 2>/dev/null | awk '
        /(VGA|3D|Display)/ { blk=$0; in_gpu=1; next }
        /^[^[:space:]]/ { in_gpu=0 }
        in_gpu { blk=blk "\n" $0; if (/Kernel driver in use:[[:space:]]*vfio-pci/) print blk }
      ' || true)"
      if [[ -n "$vfio_check" ]]; then
        log_warn "Detected GPU(s) bound to vfio-pci! Shared LXC passthrough requires the host kernel driver (never vfio-pci):"
        log_warn "$vfio_check"
      fi
    fi
  fi

  configure_host_apt_nonfree
  configure_host_network_modules
  configure_host_sysctl

  if (( DO_NVIDIA )); then setup_nvidia_host; fi
  if (( DO_INTEL )); then setup_intel_host; fi
  if (( DO_AMD )); then setup_amd_host; fi
  if (( WITH_WOLF_INPUT )); then setup_wolf_input_host; fi

  log_success "Proxmox host preparation complete."
}

# ============================================================================
# Proxmox LXC Creation & Shared GPU Passthrough
# ============================================================================

check_host_storage_and_bridge() {
  if command -v pvesm >/dev/null 2>&1; then
    local root_line="" root_status="" root_avail_kb="" tpl_status=""
    root_line="$(pvesm status -content rootdir 2>/dev/null | awk -v s="$STORAGE" 'NR>1 && $1 == s {print $3, $6; exit}' || true)"
    read -r root_status root_avail_kb <<< "$root_line"
    tpl_status="$(pvesm status -content vztmpl 2>/dev/null | awk -v s="$TEMPLATE_STORAGE" 'NR>1 && $1 == s {print $3; exit}' || true)"
    if [[ "$root_status" != "active" ]]; then
      if [[ "${DRY_RUN}" == "1" ]]; then
        log_warn "[DRY-RUN] Storage '${STORAGE}' is not active/rootdir-capable (status='${root_status:-missing}')."
      else
        die "Storage '${STORAGE}' is missing, inactive, or does not support 'rootdir' content (status='${root_status:-missing}')."
      fi
    elif [[ "$root_avail_kb" =~ ^[0-9]+$ && "$DISK" =~ ^[0-9]+$ ]]; then
      local req_kb=$(( 10#$DISK * 1024 * 1024 ))
      if (( root_avail_kb > 0 && root_avail_kb < req_kb )); then
        local avail_gb=$(( root_avail_kb / 1024 / 1024 ))
        log_warn "Storage '${STORAGE}' reports ${avail_gb} GiB (${root_avail_kb} KiB) available (< requested --disk ${DISK} GiB); verify capacity or thin-provisioning headroom."
      fi
    fi
    if [[ "$tpl_status" != "active" ]]; then
      if [[ "${DRY_RUN}" == "1" ]]; then
        log_warn "[DRY-RUN] Template storage '${TEMPLATE_STORAGE}' is not active/vztmpl-capable (status='${tpl_status:-missing}')."
      else
        die "Template storage '${TEMPLATE_STORAGE}' is missing, inactive, or does not support 'vztmpl' content (status='${tpl_status:-missing}')."
      fi
    fi
  fi

  if [[ "${DRY_RUN}" != "1" && -d /sys/class/net ]]; then
    if [[ ! -d "/sys/class/net/${BRIDGE}/bridge" ]] && \
       ! { command -v ovs-vsctl >/dev/null 2>&1 && ovs-vsctl br-exists "$BRIDGE" >/dev/null 2>&1; } && \
       [[ ! -d "/sys/class/net/${BRIDGE}" ]]; then
      die "Proxmox network bridge '${BRIDGE}' does not exist (checked /sys/class/net/${BRIDGE} and ovs-vsctl)."
    fi
  fi
}

resolve_template() {
  case "$OS_CHOICE" in
    debian12)
      LXC_OSTYPE="debian"
      TPL_PATTERN="debian-12-standard_"
      ;;
    debian13)
      LXC_OSTYPE="debian"
      TPL_PATTERN="debian-13-standard_"
      ;;
    ubuntu2404)
      LXC_OSTYPE="ubuntu"
      TPL_PATTERN="ubuntu-24.04-standard_"
      ;;
  esac

  if ! command -v pveam >/dev/null 2>&1; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      LXC_TEMPLATE="${TEMPLATE_STORAGE}:vztmpl/${TPL_PATTERN}dryrun_amd64.tar.zst"
      log_info "[DRY-RUN] pveam not present; using placeholder template ${LXC_TEMPLATE}"
      return 0
    fi
    die "'pveam' command not found; must be run on a Proxmox VE host."
  fi

  local existing_volid=""
  existing_volid="$(pveam list "$TEMPLATE_STORAGE" 2>/dev/null | awk -v pat="$TPL_PATTERN" '
    {
      name = $1
      sub(/^.*\//, "", name)
      if (index(name, pat) == 1 && name ~ /amd64/) print $1
    }
  ' | sort -V | tail -n1 || true)"
  if [[ -n "$existing_volid" ]]; then
    LXC_TEMPLATE="$existing_volid"
    log_info "Using cached template: ${LXC_TEMPLATE}"
    return 0
  fi

  log_info "Updating Proxmox appliance index (pveam update)..."
  run_cmd pveam update

  local avail_tpl=""
  avail_tpl="$(pveam available --section system 2>/dev/null | awk -v pat="$TPL_PATTERN" 'index($2, pat) == 1 && $2 ~ /amd64/ {print $2}' | sort -V | tail -n1 || true)"
  if [[ -z "$avail_tpl" ]]; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      LXC_TEMPLATE="${TEMPLATE_STORAGE}:vztmpl/${TPL_PATTERN}dryrun_amd64.tar.zst"
      log_info "[DRY-RUN] Would download latest ${TPL_PATTERN}* template into ${TEMPLATE_STORAGE}"
      return 0
    fi
    die "No available amd64 template matching '${TPL_PATTERN}' from pveam available --section system"
  fi

  log_info "Downloading template ${avail_tpl} to ${TEMPLATE_STORAGE}..."
  run_cmd pveam download "$TEMPLATE_STORAGE" "$avail_tpl"
  LXC_TEMPLATE="${TEMPLATE_STORAGE}:vztmpl/${avail_tpl}"
}

append_lxc_conf_lines() {
  local conf_file="/etc/pve/lxc/${CTID}.conf"
  local line=""
  for line in "$@"; do
    if [[ "${DRY_RUN}" == "1" ]]; then
      log_info "[DRY-RUN] Would append to ${conf_file}: ${line}"
      continue
    fi
    local live_cfg=""
    if [[ -f "$conf_file" ]]; then
      live_cfg="$(strip_pct_snapshots "$(cat "$conf_file" 2>/dev/null || true)")"
    fi
    if ! grep -qxF -- "$line" <<< "$live_cfg" 2>/dev/null; then
      if [[ -f "$conf_file" ]] && grep -qE '^\[' "$conf_file" 2>/dev/null; then
        ensure_tmp_dir
        local tmp_conf="${TMP_WORK_DIR}/lxc_${CTID}.conf"
        AWK_INS="$line" awk '
          BEGIN { ins = ENVIRON["AWK_INS"]; inserted = 0 }
          !inserted && /^\[/ { print ins; inserted = 1 }
          { print }
          END { if (!inserted) print ins }
        ' "$conf_file" > "$tmp_conf"
        [[ -s "$tmp_conf" ]] || die "Failed updating ${conf_file}: generated temp config is empty."
        cat "$tmp_conf" > "$conf_file"
      else
        printf '%s\n' "$line" >> "$conf_file"
      fi
    fi
  done
}

is_pve_ge_82() {
  [[ -n "${PVE_MAJOR:-}" && -n "${PVE_MINOR:-}" ]] || return 1
  (( PVE_MAJOR > 8 )) || (( PVE_MAJOR == 8 && PVE_MINOR >= 2 ))
}

resolve_nvidia_minors_for_slot() {
  local slot="${1:-}"
  local info_file="" minor=""
  local -a found=() gpu_info_files=()

  glob_matches gpu_info_files "/proc/driver/nvidia/gpus/*/information"

  for info_file in ${gpu_info_files[@]+"${gpu_info_files[@]}"}; do
    [[ -r "$info_file" ]] || continue
    local dir_slot=""
    dir_slot="$(basename "$(dirname "$info_file")")"
    if [[ -z "$slot" || "${dir_slot,,}" == "${slot,,}" ]]; then
      minor="$(awk '/Device Minor:/ {print $3; exit}' "$info_file" 2>/dev/null || true)"
      if [[ "$minor" =~ ^[0-9]+$ ]]; then
        found+=("$minor")
      fi
    fi
  done

  if (( ${#found[@]} == 0 )) && command -v nvidia-smi >/dev/null 2>&1; then
    local smi_out="" idx="" bus_id=""
    smi_out="$(nvidia-smi --query-gpu=index,pci.bus_id --format=csv,noheader 2>/dev/null || true)"
    while IFS=',' read -r idx bus_id; do
      idx="$(tr -d '[:space:]' <<< "$idx")"
      bus_id="$(tr -d '[:space:]' <<< "$bus_id")"
      if [[ "$bus_id" =~ ^0000([0-9a-fA-F]{4}:.*)$ ]]; then
        bus_id="${BASH_REMATCH[1]}"
      fi
      if [[ -z "$slot" || "${bus_id,,}" == "${slot,,}" ]]; then
        if [[ "$idx" =~ ^[0-9]+$ ]]; then
          found+=("$idx")
        fi
      fi
    done <<< "$smi_out"
  fi

  if (( ${#found[@]} == 0 )); then
    return 1
  fi
  printf '%s\n' "${found[@]}" | awk '!seen[$0]++' | sort -n
}

resolve_nvidia_pci_slots() {
  local -a slots=() gpu_dirs=()
  if [[ -n "$PCI_SLOT" ]]; then
    printf '%s\n' "$PCI_SLOT"
    return 0
  fi
  glob_matches gpu_dirs "/proc/driver/nvidia/gpus/*"
  local d="" slot_base=""
  for d in ${gpu_dirs[@]+"${gpu_dirs[@]}"}; do
    [[ -d "$d" ]] || continue
    slot_base="$(basename "$d")"
    slots+=("${slot_base,,}")
  done
  if (( ${#slots[@]} == 0 )) && command -v nvidia-smi >/dev/null 2>&1; then
    local smi_out="" bus_id=""
    smi_out="$(nvidia-smi --query-gpu=pci.bus_id --format=csv,noheader 2>/dev/null || true)"
    while IFS= read -r bus_id; do
      bus_id="$(tr -d '[:space:]' <<< "$bus_id")"
      if [[ "$bus_id" =~ ^0000([0-9a-fA-F]{4}:.*)$ ]]; then
        bus_id="${BASH_REMATCH[1]}"
      fi
      if [[ -n "$bus_id" ]]; then
        slots+=("${bus_id,,}")
      fi
    done <<< "$smi_out"
  fi
  if (( ${#slots[@]} == 0 )) && command -v lspci >/dev/null 2>&1; then
    local line=""
    while IFS= read -r line; do
      if [[ -n "$line" ]]; then
        slots+=("${line,,}")
      fi
    done < <(lspci -Dnn 2>/dev/null | awk '/VGA|3D|Display/ && (/NVIDIA/ || /\[10de:/) {print $1}')
  fi
  if (( ${#slots[@]} > 0 )); then
    printf '%s\n' "${slots[@]}" | awk '!seen[$0]++'
  fi
}

resolve_dri_nodes_for_slot() {
  local slot="${1:-}"
  local -n _out_r="$2"
  local -n _out_c="$3"
  [[ -n "$slot" ]] || return 0

  local r_node="" c_node=""
  if [[ -e "/dev/dri/by-path/pci-${slot}-render" ]]; then
    r_node="$(readlink -f "/dev/dri/by-path/pci-${slot}-render" 2>/dev/null || true)"
    [[ -n "$r_node" ]] && _out_r+=("$r_node")
  fi
  if [[ -z "$r_node" ]]; then
    local -a sys_r=()
    local sr=""
    glob_matches sys_r "/sys/bus/pci/devices/${slot}/drm/renderD*"
    for sr in ${sys_r[@]+"${sys_r[@]}"}; do
      _out_r+=("/dev/dri/${sr##*/}")
    done
  fi

  if [[ -e "/dev/dri/by-path/pci-${slot}-card" ]]; then
    c_node="$(readlink -f "/dev/dri/by-path/pci-${slot}-card" 2>/dev/null || true)"
    [[ -n "$c_node" ]] && _out_c+=("$c_node")
  fi
  if [[ -z "$c_node" ]]; then
    local -a sys_c=()
    local sc=""
    glob_matches sys_c "/sys/bus/pci/devices/${slot}/drm/card*"
    for sc in ${sys_c[@]+"${sys_c[@]}"}; do
      _out_c+=("/dev/dri/${sc##*/}")
    done
  fi
}

ensure_nvidia_prestart_hook() {
  local -a minors=("$@")
  if (( ${#minors[@]} == 0 )); then
    minors=("0")
  fi
  local hook_path="/usr/local/libexec/nvidia-lxc-prestart-${CTID}.sh"
  local modprobe_cmds="" m=""
  for m in "${minors[@]}"; do
    modprobe_cmds+="/usr/bin/nvidia-modprobe -u -c${m} -m >/dev/null 2>&1 || true"$'\n'
  done
  local hook_body=""
  hook_body="#!/usr/bin/env bash"$'\n'"${modprobe_cmds}/usr/bin/nvidia-smi >/dev/null 2>&1 || true"$'\n'"command -v udevadm >/dev/null 2>&1 && udevadm settle --timeout=5 >/dev/null 2>&1 || true"
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would write executable NVIDIA pre-start hook ${hook_path} (minors: ${minors[*]})" >&2
  else
    mkdir -p /usr/local/libexec
    printf '%s\n' "$hook_body" > "$hook_path"
    chmod 0755 "$hook_path"
  fi
  printf '%s\n' "$hook_path"
}

configure_nvidia_passthrough() {
  log_info "Configuring shared NVIDIA GPU passthrough for LXC ${CTID}..."

  local -a nv_minors=()
  local m=""
  if mapfile -t nv_minors < <(resolve_nvidia_minors_for_slot "$PCI_SLOT") && (( ${#nv_minors[@]} > 0 )); then
    :
  else
    if [[ "${DRY_RUN}" == "1" ]]; then
      nv_minors=("0")
      log_warn "[DRY-RUN] NVIDIA driver not active on this host; simulating resolved device index ${nv_minors[0]}."
    else
      die "Could not resolve /dev/nvidia<N> for PCI slot '${PCI_SLOT:-auto}'. Run host prep and reboot first."
    fi
  fi

  if [[ "${DRY_RUN}" != "1" ]]; then
    if command -v nvidia-modprobe >/dev/null 2>&1; then
      for m in "${nv_minors[@]}"; do
        nvidia-modprobe -u -c"${m}" -m >/dev/null 2>&1 || true
      done
    fi
    if command -v nvidia-smi >/dev/null 2>&1; then
      nvidia-smi >/dev/null 2>&1 || true
    fi
  fi

  local -a nv_majors=()
  local nv_dev="" nv_major=""
  for m in "${nv_minors[@]}"; do
    nv_dev="/dev/nvidia${m}"
    nv_major="195"
    if [[ -c "$nv_dev" ]]; then
      nv_major="$(get_char_major "$nv_dev")" || die "Failed to determine major device number for ${nv_dev}"
    elif [[ "${DRY_RUN}" != "1" ]]; then
      die "NVIDIA device node ${nv_dev} is missing after nvidia-modprobe; verify host NVIDIA driver and reboot state."
    fi
    nv_majors+=("$nv_major")
  done

  local uvm_major="" caps_major=""
  if [[ -c /dev/nvidia-uvm ]]; then
    uvm_major="$(get_char_major /dev/nvidia-uvm || true)"
  fi
  if [[ -z "$uvm_major" && -r /proc/devices ]]; then
    uvm_major="$(awk '$2 == "nvidia-uvm" {print $1; exit}' /proc/devices 2>/dev/null || true)"
  fi
  local -a cap_nodes=()
  glob_matches cap_nodes "/dev/nvidia-caps/nvidia-cap*"
  local cap_node=""
  for cap_node in ${cap_nodes[@]+"${cap_nodes[@]}"}; do
    if [[ -c "$cap_node" ]]; then
      caps_major="$(get_char_major "$cap_node" || true)"
      break
    fi
  done
  if [[ -z "$caps_major" && -r /proc/devices ]]; then
    caps_major="$(awk '$2 == "nvidia-caps" {print $1; exit}' /proc/devices 2>/dev/null || true)"
  fi

  local hook_path=""
  hook_path="$(ensure_nvidia_prestart_hook "${nv_minors[@]}")"

  local -a lines=("lxc.hook.pre-start: ${hook_path}")
  local seen_majors=""
  for nv_major in "${nv_majors[@]}"; do
    if [[ " ${seen_majors} " != *" ${nv_major} "* ]]; then
      lines+=("lxc.cgroup2.devices.allow: c ${nv_major}:* rwm")
      seen_majors+=" ${nv_major}"
    fi
  done
  [[ -n "$uvm_major" ]] && lines+=("lxc.cgroup2.devices.allow: c ${uvm_major}:* rwm")
  [[ -n "$caps_major" ]] && lines+=("lxc.cgroup2.devices.allow: c ${caps_major}:* rwm")
  if ! is_pve_ge_82; then
    lines+=("lxc.cgroup2.devices.allow: c 226:* rwm")
  fi
  for m in "${nv_minors[@]}"; do
    lines+=("lxc.mount.entry: /dev/nvidia${m} dev/nvidia${m} none bind,optional,create=file")
  done
  lines+=(
    "lxc.mount.entry: /dev/nvidiactl dev/nvidiactl none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-modeset dev/nvidia-modeset none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-uvm dev/nvidia-uvm none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-uvm-tools dev/nvidia-uvm-tools none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-caps dev/nvidia-caps none bind,optional,create=dir"
  )

  if (( ! DO_INTEL && ! DO_AMD )) && ! is_pve_ge_82; then
    if [[ -n "$PCI_SLOT" ]]; then
      local -a nv_r_nodes=() nv_c_nodes=()
      local node=""
      resolve_dri_nodes_for_slot "$PCI_SLOT" nv_r_nodes nv_c_nodes
      for node in ${nv_r_nodes[@]+"${nv_r_nodes[@]}"} ${nv_c_nodes[@]+"${nv_c_nodes[@]}"}; do
        lines+=("lxc.mount.entry: ${node} dev/dri/${node##*/} none bind,optional,create=file")
      done
    else
      lines+=("lxc.mount.entry: /dev/dri dev/dri none bind,optional,create=dir")
    fi
  fi

  append_lxc_conf_lines "${lines[@]}"
}

configure_tun_device() {
  if is_pve_ge_82; then
    log_info "Passing /dev/net/tun via Proxmox native dev${DEV_IDX}..."
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "/dev/net/tun,uid=0,gid=0,mode=0666"
    DEV_IDX=$((DEV_IDX + 1))
  else
    log_info "Passing /dev/net/tun via cgroup2 + lxc.mount.entry..."
    append_lxc_conf_lines \
      "lxc.cgroup2.devices.allow: c 10:200 rwm" \
      "lxc.mount.entry: /dev/net/tun dev/net/tun none bind,optional,create=file"
  fi
}

configure_wolf_input_passthrough() {
  if is_pve_ge_82; then
    log_info "Configuring Wolf virtual input cgroups and /dev/input mounts for LXC ${CTID} (uinput/uhid will use dev[n] after boot)..."
    append_lxc_conf_lines \
      "lxc.cgroup2.devices.allow: c 13:* rwm" \
      "lxc.mount.entry: /dev/input dev/input none bind,optional,create=dir" \
      "lxc.mount.entry: /run/udev run/udev none bind,ro,optional,create=dir"
  else
    log_info "Configuring Wolf virtual input cgroups and bind mounts for LXC ${CTID} (PVE < 8.2)..."
    log_warn "On PVE < 8.2, /dev/uinput and /dev/uhid bind-mounts appear as nobody:nogroup in unprivileged LXCs unless lxc.idmap is configured."
    append_lxc_conf_lines \
      "lxc.cgroup2.devices.allow: c 10:* rwm" \
      "lxc.cgroup2.devices.allow: c 13:* rwm" \
      "lxc.mount.entry: /dev/uinput dev/uinput none bind,optional,create=file" \
      "lxc.mount.entry: /dev/uhid dev/uhid none bind,optional,create=file" \
      "lxc.mount.entry: /dev/input dev/input none bind,optional,create=dir" \
      "lxc.mount.entry: /run/udev run/udev none bind,ro,optional,create=dir"
  fi
}

check_lxc_systemd_running() {
  local state=""
  state="$(pct exec "$CTID" -- systemctl is-system-running 2>/dev/null || true)"
  [[ "$state" == "running" || "$state" == "degraded" ]]
}

wait_lxc_systemd_ready() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would wait for LXC ${CTID} systemd readiness"
    return 0
  fi
  wait_for 90 "LXC ${CTID} systemd readiness" check_lxc_systemd_running
}

check_lxc_net_ready() {
  pct exec "$CTID" -- bash -c '
    if command -v ip >/dev/null 2>&1; then
      ip -4 -o addr show scope global 2>/dev/null | grep -q "inet "
    elif command -v hostname >/dev/null 2>&1; then
      hostname -I 2>/dev/null | grep -qE "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+"
    else
      exit 0
    fi
  ' >/dev/null 2>&1
}

wait_lxc_net_ready() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would wait for LXC ${CTID} non-loopback IPv4 readiness"
    return 0
  fi
  if ! wait_for 30 "LXC ${CTID} IPv4 network readiness" check_lxc_net_ready; then
    log_warn "Timed out waiting for non-loopback IPv4 address inside LXC ${CTID}; continuing."
  fi
}

sync_pct_indices_from_config() {
  local cfg="${1:-}"
  if [[ -z "$cfg" ]] && command -v pct >/dev/null 2>&1; then
    cfg="$(pct config "$CTID" 2>/dev/null || true)"
  fi
  cfg="$(strip_pct_snapshots "$cfg")"
  local max_dev=-1 max_mp=-1 idx=""
  while IFS= read -r idx; do
    [[ "$idx" =~ ^[0-9]+$ ]] || continue
    if (( idx > max_dev )); then
      max_dev="$idx"
    fi
  done < <(awk -F: '/^dev[0-9]+:/ {sub(/^dev/, "", $1); print $1}' <<< "$cfg")
  while IFS= read -r idx; do
    [[ "$idx" =~ ^[0-9]+$ ]] || continue
    if (( idx > max_mp )); then
      max_mp="$idx"
    fi
  done < <(awk -F: '/^mp[0-9]+:/ {sub(/^mp/, "", $1); print $1}' <<< "$cfg")
  DEV_IDX=$((max_dev + 1))
  MP_IDX=$((max_mp + 1))
}

configure_dri_and_kfd_after_boot() {
  local do_reboot="${1:-1}"
  if (( ! DO_INTEL && ! DO_AMD && ! DO_NVIDIA )); then
    return 0
  fi
  if (( DO_NVIDIA && ! DO_INTEL && ! DO_AMD )) && ! is_pve_ge_82; then
    return 0
  fi

  local pve_ge_82=0
  if is_pve_ge_82; then
    pve_ge_82=1
  fi

  if (( ! pve_ge_82 )); then
    local -a dri_lines=()
    local node=""
    if (( ! DO_NVIDIA )); then
      dri_lines+=("lxc.cgroup2.devices.allow: c 226:* rwm")
    fi
    if [[ -n "$PCI_SLOT" ]]; then
      local -a pve81_render=() pve81_card=()
      resolve_dri_nodes_for_slot "$PCI_SLOT" pve81_render pve81_card
      for node in ${pve81_render[@]+"${pve81_render[@]}"} ${pve81_card[@]+"${pve81_card[@]}"}; do
        dri_lines+=("lxc.mount.entry: ${node} dev/dri/${node##*/} none bind,optional,create=file")
      done
      if (( ${#pve81_render[@]} == 0 && ${#pve81_card[@]} == 0 )); then
        dri_lines+=("lxc.mount.entry: /dev/dri dev/dri none bind,optional,create=dir")
      fi
    else
      dri_lines+=("lxc.mount.entry: /dev/dri dev/dri none bind,optional,create=dir")
    fi
    if (( DO_AMD )) && [[ -c /dev/kfd ]]; then
      local kfd_major=""
      kfd_major="$(get_char_major /dev/kfd || true)"
      if [[ -n "$kfd_major" ]]; then
        dri_lines+=("lxc.cgroup2.devices.allow: c ${kfd_major}:* rwm")
        dri_lines+=("lxc.mount.entry: /dev/kfd dev/kfd none bind,optional,create=file")
      fi
    fi
    log_warn "PVE < 8.2 uses lxc.mount.entry for DRI/KFD; unprivileged LXCs may require host udev or lxc.idmap for render/video permissions."
    if (( ${#dri_lines[@]} > 0 )); then
      append_lxc_conf_lines "${dri_lines[@]}"
      POST_BOOT_DEV_ADDED=1
      if [[ "${DRY_RUN}" != "1" && "$do_reboot" == "1" ]]; then
        run_cmd pct reboot "$CTID"
        wait_lxc_systemd_ready
      fi
    fi
    return 0
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would query dynamic video/render GIDs inside LXC ${CTID} and attach /dev/dri/* (and /dev/kfd for AMD) via dev[n]"
    return 0
  fi

  local gid_pair="" video_gid="44" render_gid="104" parsed_v="" parsed_r=""
  gid_pair="$(pct exec "$CTID" -- bash -c '
    getent group render >/dev/null 2>&1 || groupadd -r render
    v=$(getent group video | cut -d: -f3)
    r=$(getent group render | cut -d: -f3)
    echo "${v:-44}:${r:-104}"
  ' 2>/dev/null || true)"
  if [[ "$gid_pair" == *:* ]]; then
    parsed_v="${gid_pair%%:*}"
    parsed_r="${gid_pair##*:}"
    [[ "$parsed_v" =~ ^[0-9]+$ ]] && video_gid="$parsed_v"
    [[ "$parsed_r" =~ ^[0-9]+$ ]] && render_gid="$parsed_r"
  fi

  local added_dev=0 node=""
  local -a render_nodes=()
  local -a card_nodes=()

  if [[ -n "$PCI_SLOT" ]]; then
    resolve_dri_nodes_for_slot "$PCI_SLOT" render_nodes card_nodes
  elif (( DO_NVIDIA && ! DO_INTEL && ! DO_AMD )); then
    local nv_slot=""
    while IFS= read -r nv_slot; do
      [[ -n "$nv_slot" ]] || continue
      resolve_dri_nodes_for_slot "$nv_slot" render_nodes card_nodes
    done < <(resolve_nvidia_pci_slots)
  else
    glob_matches render_nodes "/dev/dri/renderD*"
    glob_matches card_nodes "/dev/dri/card*"
  fi

  for node in ${render_nodes[@]+"${render_nodes[@]}"}; do
    [[ -c "$node" ]] || continue
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "${node},gid=${render_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  done
  for node in ${card_nodes[@]+"${card_nodes[@]}"}; do
    [[ -c "$node" ]] || continue
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "${node},gid=${video_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  done

  if (( DO_AMD )) && [[ -c /dev/kfd ]]; then
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "/dev/kfd,gid=${render_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  fi

  if (( added_dev )); then
    POST_BOOT_DEV_ADDED=1
    if [[ "$do_reboot" == "1" ]]; then
      log_info "Restarting LXC ${CTID} to activate Proxmox dev[n] GPU nodes..."
      run_cmd pct reboot "$CTID"
      wait_lxc_systemd_ready
    fi
  fi
  return 0
}

configure_wolf_input_after_boot() {
  local do_reboot="${1:-1}"
  if (( ! WITH_WOLF_INPUT )) || ! is_pve_ge_82; then
    return 0
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would query input GID inside LXC ${CTID} and attach /dev/uinput and /dev/uhid via dev[n] (mode=0660)"
    return 0
  fi

  local input_gid="105" queried_gid=""
  queried_gid="$(pct exec "$CTID" -- bash -c '
    getent group input >/dev/null 2>&1 || groupadd -r input
    getent group input | cut -d: -f3
  ' 2>/dev/null || true)"
  [[ "$queried_gid" =~ ^[0-9]+$ ]] && input_gid="$queried_gid"

  local added_wolf=0 node=""
  for node in /dev/uinput /dev/uhid; do
    if [[ -c "$node" ]]; then
      run_cmd pct set "$CTID" "--dev${DEV_IDX}" "${node},gid=${input_gid},mode=0660"
      DEV_IDX=$((DEV_IDX + 1))
      added_wolf=1
    fi
  done

  if (( added_wolf )); then
    POST_BOOT_DEV_ADDED=1
    if [[ "$do_reboot" == "1" ]]; then
      log_info "Restarting LXC ${CTID} to activate Proxmox dev[n] Wolf input nodes..."
      run_cmd pct reboot "$CTID"
      wait_lxc_systemd_ready
    fi
  fi
  return 0
}

# ============================================================================
# Inner LXC Payload (Streamed via pct exec "$CTID" -- bash -s)
# ============================================================================

emit_lxc_payload() {
  cat <<'EOF_LXC_PAYLOAD_HEAD'
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"
ADMIN_USER="${ADMIN_USER:-mediaadmin}"
SSH_PUBKEY="${SSH_PUBKEY:-}"
DOCKTUN_PORT="${DOCKTUN_PORT:-8080}"
DOCKTUN_DIR="${DOCKTUN_DIR:-/opt/docktun}"
DOCKTUN_IMAGE="${DOCKTUN_IMAGE:-ghcr.io/ooflyeyesoo/docktun:latest}"
DOCKTUN_ADMIN_USER="${DOCKTUN_ADMIN_USER:-}"
DOCKTUN_ADMIN_PASSWORD="${DOCKTUN_ADMIN_PASSWORD:-}"
LAN_SUBNETS="${LAN_SUBNETS:-10.0.0.0/8,172.16.0.0/12,192.168.0.0/16}"
ENABLE_UFW="${ENABLE_UFW:-1}"
ENABLE_WIREGUARD_PORT="${ENABLE_WIREGUARD_PORT:-1}"
WITH_ZEROCONF="${WITH_ZEROCONF:-1}"
WITH_WOLF_INPUT="${WITH_WOLF_INPUT:-0}"
GPU_MODE="${GPU_MODE:-auto}"
AIO_DOMAIN="${AIO_DOMAIN:-aio.example.com}"
AIO_TIMEZONE="${AIO_TIMEZONE:-}"
AIO_DIR="${AIO_DIR:-${DOCKTUN_DIR}/projects/aiostreams}"
AIO_COMPOSE_B64="${AIO_COMPOSE_B64:-}"
AIO_REPO_URL="${AIO_REPO_URL:-}"
AIO_REPO_BRANCH="${AIO_REPO_BRANCH:-}"
OS_ID=""
OS_VERSION_ID=""
OS_CODENAME=""
LXC_OS_ID=""
LXC_OS_VERSION_ID=""
LXC_OS_CODENAME=""
PVE_MAJOR="${PVE_MAJOR:-}"
PVE_MINOR="${PVE_MINOR:-}"
TEMPLATE_HYGIENE="${TEMPLATE_HYGIENE:-0}"
DOCKER_DAEMON_JSON="${DOCKER_DAEMON_JSON:-/etc/docker/daemon.json}"
CONTAINERD_CONFIG="${CONTAINERD_CONFIG:-/etc/containerd/config.toml}"
NVIDIA_PROC_VERSION_FILE="${NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}"
HOST_NVIDIA_VERSION="${HOST_NVIDIA_VERSION:-}"
NVIDIA_CDI_OUTPUT="${NVIDIA_CDI_OUTPUT:-/etc/cdi/nvidia.yaml}"
NVIDIA_SMOKE_IMAGE="${NVIDIA_SMOKE_IMAGE:-nvidia/cuda:12.6.0-base-ubuntu24.04}"
DOCKTUN_HEALTH_TIMEOUT="${DOCKTUN_HEALTH_TIMEOUT:-60}"
LXC_APT_BACKUP_DIR="${LXC_APT_BACKUP_DIR:-/var/backups/proxmox-lxc-apt}"
APT_BACKUP_DIR="${LXC_APT_BACKUP_DIR}"
SYS_CLASS_DRM="${SYS_CLASS_DRM:-/sys/class/drm}"
DOCKTUN_FIRSTBOOT_SCRIPT="${DOCKTUN_FIRSTBOOT_SCRIPT:-/usr/local/libexec/docktun-firstboot.sh}"

declare -a TMP_LXC_FILES=()

lxc_cleanup() {
  local f=""
  for f in ${TMP_LXC_FILES[@]+"${TMP_LXC_FILES[@]}"}; do
    if [[ -n "$f" ]] && [[ -e "$f" || -L "$f" ]]; then
      rm -rf "$f"
    fi
  done
  return 0
}
trap lxc_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

lxc_mktemp() {
  [[ "${1:-}" != __lxc_mk_* ]] || lxc_die "lxc_mktemp: invalid output variable name '${1:-}'"
  local -n __lxc_mk_out="$1"
  shift
  local __lxc_mk_f=""
  __lxc_mk_f="$(mktemp "$@")"
  TMP_LXC_FILES+=("$__lxc_mk_f")
  __lxc_mk_out="$__lxc_mk_f"
}

lxc_log_info()    { printf '[LXC INFO] %s\n' "$*"; }
lxc_log_warn()    { printf '[LXC WARN] %s\n' "$*" >&2; }
lxc_log_error()   { printf '[LXC ERROR] %s\n' "$*" >&2; }
lxc_log_success() { printf '[LXC OK] %s\n' "$*"; }
lxc_die()         { lxc_log_error "$*"; exit 1; }

log_info()  { lxc_log_info "$@"; }
log_warn()  { lxc_log_warn "$@"; }
log_error() { lxc_log_error "$@"; }
die()       { lxc_die "$@"; }

lxc_run_cmd() {
  if [[ "$#" -eq 0 ]]; then return 0; fi
  if [[ "${DRY_RUN}" == "1" ]]; then
    local rendered=""
    printf -v rendered '%q ' "$@"
    printf '[DRY-RUN] %s\n' "${rendered% }"
    return 0
  fi
  "$@"
}
run_cmd() { lxc_run_cmd "$@"; }

lxc_apt_install() {
  DEBIAN_FRONTEND=noninteractive lxc_run_cmd apt-get install -y \
    -o Dpkg::Options::="--force-confdef" \
    -o Dpkg::Options::="--force-confold" \
    "$@"
}

lxc_wait_for() {
  local timeout="$1" desc="$2"; shift 2
  local elapsed=0
  lxc_log_info "Waiting up to ${timeout}s for ${desc}..."
  while (( elapsed < timeout )); do
    if "$@"; then return 0; fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  lxc_log_error "Timed out after ${timeout}s waiting for: ${desc}"
  return 1
}
EOF_LXC_PAYLOAD_HEAD

  declare -f is_valid_ipv4 is_valid_ipv4_cidr is_valid_ipv6 is_valid_ipv6_cidr \
    is_valid_ssh_pubkey_line detect_os glob_matches write_file_managed \
    backup_once enable_nonfree_in_list_file enable_nonfree_in_sources_file

  cat <<'EOF_LXC_PAYLOAD'
lxc_detect_os() {
  detect_os || return 1
  LXC_OS_ID="$OS_ID"
  LXC_OS_VERSION_ID="$OS_VERSION_ID"
  LXC_OS_CODENAME="$OS_CODENAME"
}

lxc_write_file() {
  local target="$1"
  local mode="${2:-0644}"
  local content=""
  content="$(cat)"
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would write ${target} (mode ${mode})"
    return 0
  fi
  install -d -m 0755 "$(dirname "$target")"
  local tmp_f=""
  lxc_mktemp tmp_f "${target}.XXXXXX"
  printf '%s\n' "$content" > "$tmp_f"
  chmod "$mode" "$tmp_f"
  mv -f "$tmp_f" "$target"
}

lxc_port_in_use() {
  local p="$1"
  local ss_out="" ns_out="" dk_ports="" nat_out=""
  if command -v ss >/dev/null 2>&1; then
    ss_out="$(ss -H -tuln 2>/dev/null || true)"
    if grep -E ":${p}([[:space:]]|$)" >/dev/null <<< "$ss_out"; then
      return 0
    fi
  elif command -v netstat >/dev/null 2>&1; then
    ns_out="$(netstat -tuln 2>/dev/null || true)"
    if grep -E ":${p}([[:space:]]|$)" >/dev/null <<< "$ns_out"; then
      return 0
    fi
  fi
  if command -v docker >/dev/null 2>&1; then
    dk_ports="$(docker ps --format '{{.Ports}}' 2>/dev/null || true)"
    if grep -E "(^|[,[:space:]])([^[:space:],]+:)?${p}->" >/dev/null <<< "$dk_ports"; then
      return 0
    fi
  fi
  if [[ "${EUID:-$(id -u)}" -eq 0 ]] && command -v iptables >/dev/null 2>&1; then
    nat_out="$(iptables -t nat -S DOCKER 2>/dev/null || true)"
    if grep -E -- "--dport ${p}([[:space:]]|$)" >/dev/null <<< "$nat_out"; then
      return 0
    fi
  fi
  return 1
}

lxc_is_valid_ipv4() { is_valid_ipv4 "$@"; }
lxc_is_valid_ipv4_cidr() { is_valid_ipv4_cidr "$@"; }
lxc_is_valid_ipv6() { is_valid_ipv6 "$@"; }
lxc_is_valid_ipv6_cidr() { is_valid_ipv6_cidr "$@"; }
lxc_is_valid_ssh_pubkey_line() { is_valid_ssh_pubkey_line "$@"; }

# --- Stage 1: LXC Hardening -------------------------------------------------

lxc_fix_networkd_wait_online() {
  lxc_log_info "Configuring systemd-networkd-wait-online timeout (--timeout=5 --any)..."
  local wait_bin="/lib/systemd/systemd-networkd-wait-online"
  if [[ -x /usr/lib/systemd/systemd-networkd-wait-online ]]; then
    wait_bin="/usr/lib/systemd/systemd-networkd-wait-online"
  fi
  lxc_write_file "/etc/systemd/system/systemd-networkd-wait-online.service.d/override.conf" 0644 <<EOF
[Service]
ExecStart=
ExecStart=${wait_bin} --timeout=5 --any
EOF
  if [[ "${DRY_RUN}" != "1" ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
  fi
  return 0
}

lxc_append_unique_key() {
  local key="${1%$'\r'}" auth_file="$2"
  [[ -z "$key" ]] && return 0
  if ! grep -qxF "$key" "$auth_file" 2>/dev/null; then
    printf '%s\n' "$key" >> "$auth_file"
  fi
}

lxc_has_authorized_ssh_key() {
  local file="" line=""
  for file in "$@"; do
    [[ -r "$file" ]] || continue
    while IFS= read -r line || [[ -n "$line" ]]; do
      if lxc_is_valid_ssh_pubkey_line "$line"; then
        return 0
      fi
    done < "$file"
  done
  return 1
}

lxc_inject_docker_user_block() {
  local rules_file="$1"
  local ufw_forward_chain="$2"
  shift 2
  local -a subnets=("$@")
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would inject DOCKER-USER block into ${rules_file}"
    return 0
  fi
  [[ -f "$rules_file" ]] || return 0

  if [[ ! -f "${rules_file}.bak.pre-docktun" ]]; then
    cp -a "$rules_file" "${rules_file}.bak.pre-docktun"
  fi
  local tmp_rules="" tmp_out=""
  lxc_mktemp tmp_rules

  awk '
    BEGIN { in_blk=0; buf="" }
    /^# BEGIN DOCKTUN DOCKER-USER$/ {
      if (in_blk) { printf "%s", buf; buf="" }
      in_blk=1; buf=$0 "\n"; next
    }
    in_blk && /^# END DOCKTUN DOCKER-USER$/ {
      in_blk=0; buf=""; next
    }
    in_blk { buf = buf $0 "\n"; next }
    { print }
    END { if (in_blk) printf "%s", buf }
  ' "$rules_file" > "$tmp_rules"

  local def_if="" route_flag="-4"
  if [[ "$ufw_forward_chain" == "ufw6-user-forward" || "$rules_file" == *after6.rules* ]]; then
    route_flag="-6"
  fi
  if command -v ip >/dev/null 2>&1; then
    def_if="$(ip "$route_flag" route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") {print $(i+1); exit}}' || true)"
    def_if="${def_if%%@*}"
  fi

  local block="" subnet=""
  block+="# BEGIN DOCKTUN DOCKER-USER"$'\n'
  block+="*filter"$'\n'
  block+=":DOCKER-USER - [0:0]"$'\n'
  block+="-F DOCKER-USER"$'\n'
  block+="-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN"$'\n'
  block+="-A DOCKER-USER -i docker0 -j RETURN"$'\n'
  block+="-A DOCKER-USER -i br-+ -j RETURN"$'\n'
  for subnet in ${subnets[@]+"${subnets[@]}"}; do
    [[ -z "$subnet" ]] && continue
    block+="-A DOCKER-USER -s ${subnet} -j RETURN"$'\n'
  done
  if [[ "$ENABLE_WIREGUARD_PORT" == "1" ]]; then
    block+="-A DOCKER-USER -p udp -m conntrack --ctorigdstport 51820 -j RETURN"$'\n'
  fi
  block+="-A DOCKER-USER -j ${ufw_forward_chain}"$'\n'
  block+="-A DOCKER-USER -i eth+ -m conntrack --ctstate NEW -j DROP"$'\n'
  if [[ -n "$def_if" && "$def_if" =~ ^[a-zA-Z0-9._-]+$ && ! "$def_if" =~ ^(eth|docker0|br-|veth|wg|tailscale|tun|zt|tap|lo) ]]; then
    block+="-A DOCKER-USER -i ${def_if} -m conntrack --ctstate NEW -j DROP"$'\n'
  fi
  block+="-A DOCKER-USER -j RETURN"$'\n'
  block+="COMMIT"$'\n'
  block+="# END DOCKTUN DOCKER-USER"$'\n'

  lxc_mktemp tmp_out "${rules_file}.XXXXXX"
  {
    cat "$tmp_rules"
    printf '%s' "$block"
  } > "$tmp_out"
  chmod 0640 "$tmp_out"
  chmod --reference="$rules_file" "$tmp_out" 2>/dev/null || true
  mv -f "$tmp_out" "$rules_file"
}

lxc_configure_ufw_docker_user() {
  local after_rules="/etc/ufw/after.rules"
  local after6_rules="/etc/ufw/after6.rules"
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would append standalone DOCKER-USER filter block in ${after_rules} (and ${after6_rules} when IPv6/ip6tables active)"
    return 0
  fi

  local -a subnets=() ipv4_subnets=() ipv6_subnets=()
  local subnet=""
  IFS=',' read -ra subnets <<< "$LAN_SUBNETS"
  for subnet in "${subnets[@]}"; do
    subnet="${subnet#"${subnet%%[![:space:]]*}"}"
    subnet="${subnet%"${subnet##*[![:space:]]}"}"
    [[ -z "$subnet" ]] && continue
    if lxc_is_valid_ipv4_cidr "$subnet"; then
      ipv4_subnets+=("$subnet")
    elif lxc_is_valid_ipv6_cidr "$subnet"; then
      ipv6_subnets+=("$subnet")
    else
      lxc_log_warn "Ignoring invalid CIDR '${subnet}' in LAN_SUBNETS."
    fi
  done

  if [[ -f "$after_rules" ]]; then
    lxc_inject_docker_user_block "$after_rules" "ufw-user-forward" ${ipv4_subnets[@]+"${ipv4_subnets[@]}"}
  fi

  if [[ -f "$after6_rules" ]]; then
    local ufw_ipv6_disabled=0
    if [[ -r /etc/default/ufw ]] && grep -qE '^[[:space:]]*IPV6[[:space:]]*=[[:space:]]*["'\'']?[Nn][Oo]["'\'']?' /etc/default/ufw; then
      ufw_ipv6_disabled=1
    fi
    if (( ! ufw_ipv6_disabled )) && command -v ip6tables >/dev/null 2>&1 && ip6tables -L -n >/dev/null 2>&1; then
      local ip6_test_ok=1
      if command -v ip6tables-restore >/dev/null 2>&1; then
        local test6=""
        test6="$(printf '*filter\n:ufw6-user-forward - [0:0]\n:DOCKER-USER - [0:0]\n-F DOCKER-USER\n-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN\n-A DOCKER-USER -j ufw6-user-forward\n-A DOCKER-USER -i eth+ -m conntrack --ctstate NEW -j DROP\n-A DOCKER-USER -j RETURN\nCOMMIT\n')"
        if ! ip6tables-restore --test <<< "$test6" >/dev/null 2>&1; then
          ip6_test_ok=0
        fi
      fi
      if (( ip6_test_ok )); then
        lxc_inject_docker_user_block "$after6_rules" "ufw6-user-forward" ${ipv6_subnets[@]+"${ipv6_subnets[@]}"}
      else
        lxc_log_warn "ip6tables-restore --test failed; skipping IPv6 DOCKER-USER injection in ${after6_rules}."
      fi
    else
      lxc_log_info "UFW IPv6 disabled or ip6tables unavailable; skipping ${after6_rules} DOCKER-USER injection."
    fi
  fi
  return 0
}

lxc_ufw_exec_safe() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd ufw "$@"
    return 0
  fi
  local err_out=""
  if ! err_out="$(ufw "$@" 2>&1)"; then
    lxc_log_warn "UFW command failed ('ufw $*'): ${err_out}"
    return 1
  fi
  return 0
}

lxc_harden() {
  lxc_log_info "Hardening LXC container..."
  export DEBIAN_FRONTEND=noninteractive
  lxc_run_cmd apt-get update
  lxc_apt_install --no-install-recommends \
    sudo ufw fail2ban nftables unattended-upgrades apt-listchanges \
    curl ca-certificates gnupg jq openssh-server openssl

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_apt_install --no-install-recommends python3-systemd
  elif apt-cache show python3-systemd >/dev/null 2>&1; then
    lxc_apt_install --no-install-recommends python3-systemd || \
      lxc_log_warn "Failed to install python3-systemd; fail2ban will check rsyslog fallback."
  fi

  lxc_fix_networkd_wait_online

  # Journald disk cap (prevent logs exhausting rootfs)
  lxc_write_file "/etc/systemd/journald.conf.d/99-lxc.conf" 0644 <<'EOF'
[Journal]
SystemMaxUse=200M
EOF
  if [[ "${DRY_RUN}" != "1" ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl restart systemd-journald || true
  fi

  # Timezone alignment
  if [[ -n "$AIO_TIMEZONE" ]]; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      lxc_run_cmd timedatectl set-timezone "$AIO_TIMEZONE"
    elif command -v timedatectl >/dev/null 2>&1 && timedatectl set-timezone "$AIO_TIMEZONE" >/dev/null 2>&1; then
      lxc_log_info "Container timezone set to ${AIO_TIMEZONE}."
    elif [[ -f "/usr/share/zoneinfo/${AIO_TIMEZONE}" ]]; then
      ln -snf "/usr/share/zoneinfo/${AIO_TIMEZONE}" /etc/localtime
      printf '%s\n' "$AIO_TIMEZONE" > /etc/timezone
      lxc_log_info "Container timezone linked to ${AIO_TIMEZONE}."
    fi
  fi

  # Admin user
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd useradd -m -s /bin/bash -G sudo "$ADMIN_USER"
    lxc_log_info "[DRY-RUN] Would write /etc/sudoers.d/90-${ADMIN_USER} (mode 0440)"
  else
    if id "$ADMIN_USER" >/dev/null 2>&1; then
      local existing_uid=""
      existing_uid="$(id -u "$ADMIN_USER" 2>/dev/null || echo 1000)"
      if (( existing_uid < 1000 )) && [[ "$existing_uid" -ne 0 ]]; then
        lxc_die "ADMIN_USER '${ADMIN_USER}' is an existing system account (UID ${existing_uid} < 1000)."
      fi
      usermod -aG sudo "$ADMIN_USER"
    else
      useradd -m -s /bin/bash -G sudo "$ADMIN_USER"
    fi
    if [[ "$WITH_WOLF_INPUT" == "1" ]]; then
      getent group input >/dev/null 2>&1 || groupadd -r input
      usermod -aG input "$ADMIN_USER"
    fi
    local sudoers_file="/etc/sudoers.d/90-${ADMIN_USER}"
    lxc_write_file "$sudoers_file" 0440 <<EOF
${ADMIN_USER} ALL=(ALL) NOPASSWD:ALL
EOF
    if command -v visudo >/dev/null 2>&1; then
      if ! visudo -cf "$sudoers_file" >/dev/null 2>&1; then
        rm -f "$sudoers_file"
        lxc_die "Generated sudoers file failed visudo validation."
      fi
    fi
  fi

  # OpenSSH hardening (00-hardening.conf for OpenSSH first-match-wins!)
  local admin_home="/home/${ADMIN_USER}"
  if [[ "${DRY_RUN}" != "1" ]] && getent passwd "$ADMIN_USER" >/dev/null 2>&1; then
    admin_home="$(getent passwd "$ADMIN_USER" | cut -d: -f6)"
  fi
  local admin_auth="${admin_home}/.ssh/authorized_keys"
  local root_auth="/root/.ssh/authorized_keys"
  local password_auth="no"
  local permit_root="no"
  local allow_users_line=""

  if [[ "${DRY_RUN}" == "1" ]]; then
    if [[ -z "$SSH_PUBKEY" ]] && ! lxc_has_authorized_ssh_key "$root_auth" "$admin_auth"; then
      password_auth="yes"
      permit_root="prohibit-password"
    fi
    lxc_log_info "[DRY-RUN] Would write /etc/ssh/sshd_config.d/00-hardening.conf (PasswordAuthentication ${password_auth}, PermitRootLogin ${permit_root})"
    lxc_run_cmd sshd -T
    lxc_run_cmd systemctl reload ssh
  else
    install -d -m 0700 -o "$ADMIN_USER" -g "$ADMIN_USER" "${admin_home}/.ssh"
    touch "$admin_auth"
    chown "${ADMIN_USER}:${ADMIN_USER}" "$admin_auth"
    chmod 0600 "$admin_auth"

    if [[ -n "$SSH_PUBKEY" ]]; then
      local pk_line=""
      while IFS= read -r pk_line || [[ -n "$pk_line" ]]; do
        pk_line="${pk_line%$'\r'}"
        if lxc_is_valid_ssh_pubkey_line "$pk_line"; then
          lxc_append_unique_key "$pk_line" "$admin_auth"
        fi
      done <<< "$SSH_PUBKEY"
    fi
    if [[ -r "$root_auth" ]]; then
      local line=""
      while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line%$'\r'}"
        if lxc_is_valid_ssh_pubkey_line "$line"; then
          lxc_append_unique_key "$line" "$admin_auth"
        fi
      done < "$root_auth"
    fi

    if ! lxc_has_authorized_ssh_key "$admin_auth"; then
      lxc_log_warn "No SSH authorized keys found; keeping 'PasswordAuthentication yes' (PermitRootLogin prohibit-password). Enter via 'pct enter <ctid>' and run 'passwd ${ADMIN_USER}' to enable SSH login."
      password_auth="yes"
      permit_root="prohibit-password"
    else
      permit_root="no"
      allow_users_line="AllowUsers ${ADMIN_USER}"$'\n'
    fi

    lxc_write_file "/etc/ssh/sshd_config.d/00-hardening.conf" 0644 <<EOF
PasswordAuthentication ${password_auth}
KbdInteractiveAuthentication no
PermitRootLogin ${permit_root}
${allow_users_line}PubkeyAuthentication yes
X11Forwarding no
MaxAuthTries 3
EOF

    install -d -m 0755 /run/sshd
    if command -v sshd >/dev/null 2>&1; then
      sshd -t || lxc_die "sshd configuration validation failed."
      local effective_sshd=""
      effective_sshd="$(sshd -T 2>/dev/null | grep -E '^(permitrootlogin|passwordauthentication|pubkeyauthentication|allowusers) ' || true)"
      lxc_log_info "Effective sshd settings: ${effective_sshd//$'\n'/; }"
      if ! grep -qiE "^passwordauthentication[[:space:]]+${password_auth}$" <<< "$effective_sshd"; then
        lxc_die "sshd -T mismatch: expected passwordauthentication ${password_auth} (check /etc/ssh/sshd_config Include order)."
      fi
      if ! grep -qiE "^permitrootlogin[[:space:]]+${permit_root}$" <<< "$effective_sshd"; then
        lxc_die "sshd -T mismatch: expected permitrootlogin ${permit_root} (check /etc/ssh/sshd_config Include order)."
      fi
    fi

    if command -v systemctl >/dev/null 2>&1; then
      if systemctl is-enabled ssh.socket >/dev/null 2>&1 || systemctl is-active ssh.socket >/dev/null 2>&1; then
        systemctl restart ssh.socket || true
        systemctl try-reload-or-restart ssh || true
      elif systemctl list-unit-files ssh.service 2>/dev/null | grep -q 'ssh\.service'; then
        systemctl enable --now ssh || true
        systemctl reload ssh || systemctl restart ssh || true
      elif systemctl list-unit-files sshd.service 2>/dev/null | grep -q 'sshd\.service'; then
        systemctl enable --now sshd || true
        systemctl reload sshd || systemctl restart sshd || true
      fi
    fi
  fi

  # UFW + DOCKER-USER chain restriction
  if [[ "$ENABLE_UFW" == "1" ]] && { [[ "${DRY_RUN}" == "1" ]] || command -v ufw >/dev/null 2>&1; }; then
    lxc_log_info "Configuring UFW + DOCKER-USER LAN rules..."
    if lxc_ufw_exec_safe default deny incoming && \
       lxc_ufw_exec_safe default allow outgoing && \
       lxc_ufw_exec_safe allow 22/tcp comment "SSH"; then
      local -a subnets=()
      local subnet=""
      IFS=',' read -ra subnets <<< "$LAN_SUBNETS"
      for subnet in "${subnets[@]}"; do
        subnet="${subnet#"${subnet%%[![:space:]]*}"}"
        subnet="${subnet%"${subnet##*[![:space:]]}"}"
        [[ -z "$subnet" ]] && continue
        if ! lxc_is_valid_ipv4_cidr "$subnet" && ! lxc_is_valid_ipv6_cidr "$subnet"; then
          lxc_log_warn "Skipping invalid UFW LAN subnet '${subnet}'."
          continue
        fi
        lxc_ufw_exec_safe allow from "$subnet" to any port "$DOCKTUN_PORT" proto tcp comment "DockTun LAN" || \
          lxc_log_warn "Failed adding UFW rule for ${subnet}; continuing."
        if [[ "$WITH_ZEROCONF" == "1" ]]; then
          lxc_ufw_exec_safe allow from "$subnet" to any port 80 proto tcp comment "DockTun ZeroConf HTTP" || true
          lxc_ufw_exec_safe allow from "$subnet" to any port 5353 proto udp comment "DockTun ZeroConf mDNS" || true
        fi
      done
      if [[ "$ENABLE_WIREGUARD_PORT" == "1" ]]; then
        lxc_ufw_exec_safe allow 51820/udp comment "DockTun WireGuard" || true
      fi
      lxc_configure_ufw_docker_user
      if ! lxc_ufw_exec_safe --force enable; then
        if [[ -f /etc/ufw/after6.rules.bak.pre-docktun ]]; then
          mv -f /etc/ufw/after6.rules.bak.pre-docktun /etc/ufw/after6.rules
          if lxc_ufw_exec_safe --force enable; then
            lxc_log_warn "IPv6 DOCKER-USER rules were rejected by ip6tables/ufw; reverted after6.rules and kept IPv4 DOCKER-USER protection."
          else
            [[ -f /etc/ufw/after.rules.bak.pre-docktun ]] && mv -f /etc/ufw/after.rules.bak.pre-docktun /etc/ufw/after.rules
            lxc_ufw_exec_safe --force enable || true
            lxc_die "UFW failed to enable with DOCKER-USER rules; firewall protection could not be verified."
          fi
        else
          [[ -f /etc/ufw/after.rules.bak.pre-docktun ]] && mv -f /etc/ufw/after.rules.bak.pre-docktun /etc/ufw/after.rules
          lxc_ufw_exec_safe --force enable || true
          lxc_die "UFW failed to enable with DOCKER-USER rules; firewall protection could not be verified."
        fi
      fi
    else
      lxc_die "Failed configuring base UFW policy."
    fi
  fi

  # Fail2ban with backend = systemd (or rsyslog fallback) and banaction = nftables-multiport (or iptables fallback)
  local f2b_backend="systemd"
  local f2b_banaction="nftables-multiport"
  if [[ "${DRY_RUN}" != "1" ]]; then
    if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import systemd.journal' >/dev/null 2>&1; then
      lxc_log_warn "python3-systemd unavailable; installing rsyslog and falling back fail2ban backend to 'auto'."
      lxc_apt_install --no-install-recommends rsyslog || true
      command -v systemctl >/dev/null 2>&1 && systemctl enable --now rsyslog >/dev/null 2>&1 || true
      f2b_backend="auto"
    fi
    if ! command -v nft >/dev/null 2>&1; then
      lxc_log_warn "nft binary unavailable; falling back fail2ban banaction to 'iptables-multiport'."
      f2b_banaction="iptables-multiport"
    fi
  fi

  local f2b_backend_line="backend = systemd"
  if [[ "$f2b_backend" != "systemd" ]]; then
    f2b_backend_line="backend = auto"
  fi
  local f2b_banaction_line="banaction = nftables-multiport"
  if [[ "$f2b_banaction" != "nftables-multiport" ]]; then
    f2b_banaction_line="banaction = iptables-multiport"
  fi
  local f2b_ignoreip="127.0.0.1/8 ::1"
  local -a f2b_subnets=()
  local f2b_sub=""
  IFS=',' read -ra f2b_subnets <<< "$LAN_SUBNETS"
  for f2b_sub in "${f2b_subnets[@]}"; do
    f2b_sub="${f2b_sub#"${f2b_sub%%[![:space:]]*}"}"
    f2b_sub="${f2b_sub%"${f2b_sub##*[![:space:]]}"}"
    [[ -n "$f2b_sub" ]] && f2b_ignoreip+=" ${f2b_sub}"
  done

  lxc_write_file "/etc/fail2ban/jail.d/99-sshd.local" 0644 <<EOF
[DEFAULT]
ignoreip = ${f2b_ignoreip}
bantime = 1h
findtime = 10m
maxretry = 5
${f2b_banaction_line}

[sshd]
enabled = true
port = ssh
${f2b_backend_line}
${f2b_banaction_line}
EOF
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd systemctl enable --now fail2ban
  else
    if ! systemctl enable --now fail2ban; then
      lxc_log_warn "fail2ban failed to start inside LXC; continuing."
    else
      systemctl reload fail2ban 2>/dev/null || systemctl restart fail2ban 2>/dev/null || true
    fi
  fi

  # Unattended upgrades: pre-seed & dpkg-reconfigure BEFORE writing 20auto-upgrades
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd dpkg-reconfigure -f noninteractive unattended-upgrades
  else
    if command -v debconf-set-selections >/dev/null 2>&1; then
      printf 'unattended-upgrades unattended-upgrades/enable_auto_updates boolean true\n' | debconf-set-selections || true
    fi
    DEBIAN_FRONTEND=noninteractive dpkg-reconfigure -f noninteractive unattended-upgrades </dev/null || true
  fi
  lxc_write_file "/etc/apt/apt.conf.d/20auto-upgrades" 0644 <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
  lxc_write_file "/etc/apt/apt.conf.d/52unattended-upgrades-local" 0644 <<'EOF'
Unattended-Upgrade::Package-Blacklist {
    "^nvidia-";
    "^libnvidia-";
    "^libcuda";
    "^libnvcuvid";
    "^cuda-";
};
EOF

  # PAM shell limits (ssh/su/sudo) and systemd DefaultLimitNOFILE for LXC services (e.g. Nginx)
  lxc_write_file "/etc/security/limits.d/99-lxc-media.conf" 0644 <<'EOF'
* soft nofile 65536
* hard nofile 65536
root soft nofile 65536
root hard nofile 65536
EOF
  lxc_write_file "/etc/systemd/system.conf.d/99-lxc-media.conf" 0644 <<'EOF'
[Manager]
DefaultLimitNOFILE=65536:524288
EOF
  if [[ "${DRY_RUN}" != "1" ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reexec >/dev/null 2>&1 || systemctl daemon-reload >/dev/null 2>&1 || true
  fi

  # LXC network-namespace sysctls
  lxc_write_file "/etc/sysctl.d/99-lxc-media.conf" 0644 <<'EOF'
net.ipv4.ip_forward = 1
net.ipv4.conf.all.src_valid_mark = 1
net.ipv4.conf.all.rp_filter = 2
net.ipv4.conf.default.rp_filter = 2
EOF
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd sysctl -p /etc/sysctl.d/99-lxc-media.conf
  else
    sysctl -p /etc/sysctl.d/99-lxc-media.conf >/dev/null 2>&1 || \
      lxc_log_warn "One or more sysctls in /etc/sysctl.d/99-lxc-media.conf are read-only/unsupported in LXC; continuing."
  fi
  return 0
}

# --- Stage 2: Docker CE + ZFS Guard + CVE-2025-52881 Smoke Test -------------

lxc_is_zfs_or_lxc_quirk_present() {
  local root_fs=""
  root_fs="$(stat -f -c '%T' / 2>/dev/null || true)"
  if [[ "$root_fs" == "zfs" || -e /dev/zfs ]]; then
    return 0
  fi
  if [[ -r /run/systemd/container ]] && grep -qi 'lxc' /run/systemd/container 2>/dev/null; then
    return 0
  fi
  if command -v systemd-detect-virt >/dev/null 2>&1 && [[ "$(systemd-detect-virt -c 2>/dev/null || true)" == "lxc" ]]; then
    return 0
  fi
  return 1
}

lxc_apply_containerd_zfs_guard() {
  if ! lxc_is_zfs_or_lxc_quirk_present; then
    lxc_log_info "No LXC/ZFS containerd quirk detected; skipping snapshotter disable."
    return 0
  fi

  lxc_log_info "Applying Proxmox ZFS-in-LXC containerd hang guard in ${CONTAINERD_CONFIG}..."
  local snapshotters_json='["io.containerd.snapshotter.v1.zfs","io.containerd.snapshotter.v1.btrfs","io.containerd.snapshotter.v1.aufs","io.containerd.snapshotter.v1.devmapper"]'

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would ensure disabled_plugins includes zfs/btrfs/aufs/devmapper snapshotters in ${CONTAINERD_CONFIG}"
    return 0
  fi

  install -d -m 0755 "$(dirname "$CONTAINERD_CONFIG")"
  if [[ ! -f "$CONTAINERD_CONFIG" ]]; then
    local ct_ver_line="" cfg_ver=2
    if command -v containerd >/dev/null 2>&1; then
      ct_ver_line="$(containerd --version 2>/dev/null || true)"
      if [[ "$ct_ver_line" =~ [[:space:]]v?2\.[0-9]+ ]]; then
        cfg_ver=3
      fi
    fi
    printf 'version = %d\ndisabled_plugins = %s\n' "$cfg_ver" "$snapshotters_json" > "$CONTAINERD_CONFIG"
    chmod 0644 "$CONTAINERD_CONFIG"
    return 0
  fi

  if [[ ! -f "${CONTAINERD_CONFIG}.bak.pre-docktun" ]]; then
    cp -a "$CONTAINERD_CONFIG" "${CONTAINERD_CONFIG}.bak.pre-docktun"
  fi
  local tmp_cfg=""
  lxc_mktemp tmp_cfg "${CONTAINERD_CONFIG}.XXXXXX"

  local has_top_dp=0
  if awk '
    /^[[:space:]]*\[/ { exit 1 }
    /^[[:space:]]*disabled_plugins[[:space:]]*=/ { found=1; exit 0 }
    END { exit !found }
  ' "$CONTAINERD_CONFIG"; then
    has_top_dp=1
  fi

  if (( has_top_dp )); then
    local existing_array="" normalized_array="" merged_array=""
    existing_array="$(awk '
      function strip_toml_comment(s,    i, c, in_dq, in_sq, esc) {
        in_dq = 0; in_sq = 0; esc = 0
        for (i = 1; i <= length(s); i++) {
          c = substr(s, i, 1)
          if (esc) { esc = 0; continue }
          if (in_dq) {
            if (c == "\\") esc = 1
            else if (c == "\"") in_dq = 0
          } else if (in_sq) {
            if (c == "'"'"'") in_sq = 0
          } else {
            if (c == "\"") in_dq = 1
            else if (c == "'"'"'") in_sq = 1
            else if (c == "#") return substr(s, 1, i - 1)
          }
        }
        return s
      }
      BEGIN { in_dp=0; in_sec=0; buf="" }
      !in_dp && !in_sec && /^[[:space:]]*\[/ { in_sec=1; next }
      !in_dp && !in_sec && /^[[:space:]]*disabled_plugins[[:space:]]*=/ {
        line = $0
        sub(/^[[:space:]]*disabled_plugins[[:space:]]*=[[:space:]]*/, "", line)
        line = strip_toml_comment(line)
        buf = line
        if (index(line, "]") > 0) { print buf; exit }
        in_dp = 1
        next
      }
      in_dp {
        line = strip_toml_comment($0)
        buf = buf " " line
        if (index(line, "]") > 0) { print buf; exit }
      }
    ' "$CONTAINERD_CONFIG")"
    normalized_array="$(sed -E "s/'([^']*)'/\"\\1\"/g; s/,[[:space:]]*\\]/]/g" <<< "$existing_array")"
    if ! merged_array="$(jq -c --argjson add "$snapshotters_json" '. + $add | unique' <<< "$normalized_array" 2>/dev/null)"; then
      merged_array="$snapshotters_json"
    fi

    awk -v repl="disabled_plugins = ${merged_array}" '
      function strip_toml_comment(s,    i, c, in_dq, in_sq, esc) {
        in_dq = 0; in_sq = 0; esc = 0
        for (i = 1; i <= length(s); i++) {
          c = substr(s, i, 1)
          if (esc) { esc = 0; continue }
          if (in_dq) {
            if (c == "\\") esc = 1
            else if (c == "\"") in_dq = 0
          } else if (in_sq) {
            if (c == "'"'"'") in_sq = 0
          } else {
            if (c == "\"") in_dq = 1
            else if (c == "'"'"'") in_sq = 1
            else if (c == "#") return substr(s, 1, i - 1)
          }
        }
        return s
      }
      BEGIN { in_dp=0; in_sec=0; done=0 }
      !in_dp && !in_sec && /^[[:space:]]*\[/ { in_sec=1 }
      !done && !in_dp && !in_sec && /^[[:space:]]*disabled_plugins[[:space:]]*=/ {
        print repl
        done = 1
        code = strip_toml_comment($0)
        if (index(code, "]") == 0) in_dp = 1
        next
      }
      in_dp {
        code = strip_toml_comment($0)
        if (index(code, "]") > 0) in_dp = 0
        next
      }
      { print }
    ' "$CONTAINERD_CONFIG" > "$tmp_cfg"
  else
    {
      printf 'disabled_plugins = %s\n' "$snapshotters_json"
      cat "$CONTAINERD_CONFIG"
    } > "$tmp_cfg"
  fi
  chmod 0644 "$tmp_cfg"
  mv -f "$tmp_cfg" "$CONTAINERD_CONFIG"

  if command -v containerd >/dev/null 2>&1; then
    if ! containerd --config "$CONTAINERD_CONFIG" config dump >/dev/null 2>&1; then
      cp -a "${CONTAINERD_CONFIG}.bak.pre-docktun" "$CONTAINERD_CONFIG"
      lxc_die "Generated containerd config failed 'containerd --config ${CONTAINERD_CONFIG} config dump'; restored backup."
    fi
  fi
  return 0
}

lxc_merge_daemon_json() {
  local extra_jq="${1:-.}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would merge ulimits/log-driver/log-opts/live-restore/userland-proxy/icc/storage-driver into ${DOCKER_DAEMON_JSON}"
    return 0
  fi

  install -d -m 0755 "$(dirname "$DOCKER_DAEMON_JSON")"
  if [[ ! -s "$DOCKER_DAEMON_JSON" ]]; then
    printf '{}\n' > "$DOCKER_DAEMON_JSON"
  fi
  if ! jq empty "$DOCKER_DAEMON_JSON" >/dev/null 2>&1; then
    cp "$DOCKER_DAEMON_JSON" "${DOCKER_DAEMON_JSON}.bak.$(date +%s)"
    printf '{}\n' > "$DOCKER_DAEMON_JSON"
  fi

  local tmp_json=""
  lxc_mktemp tmp_json "${DOCKER_DAEMON_JSON}.XXXXXX"
  jq --argjson base_ulimit '{"Name":"nofile","Hard":65536,"Soft":65536}' \
     --argjson base_log_opts '{"max-size":"10m","max-file":"3"}' \
     --argjson base_pools '[{"base":"172.24.0.0/14","size":24}]' \
    '
      .["default-ulimits"] = ((.["default-ulimits"] // {}) * {"nofile": $base_ulimit}) |
      .["log-driver"] = "json-file" |
      .["log-opts"] = ((.["log-opts"] // {}) * $base_log_opts) |
      .["live-restore"] = true |
      .["userland-proxy"] = false |
      .["icc"] = false |
      .["storage-driver"] = (.["storage-driver"] // "overlay2") |
      .["default-address-pools"] = (.["default-address-pools"] // $base_pools)
    ' "$DOCKER_DAEMON_JSON" | jq "$extra_jq" > "$tmp_json"
  chmod 0644 "$tmp_json"
  mv -f "$tmp_json" "$DOCKER_DAEMON_JSON"
}

lxc_docker_ready() {
  docker info >/dev/null 2>&1
}

lxc_enable_fuse_overlayfs_fallback() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would install fuse-overlayfs, update ${DOCKER_DAEMON_JSON}, and restart docker"
    return 0
  fi
  lxc_log_warn "Docker overlay2 failed or fell back to vfs; attempting fuse-overlayfs fallback..."
  if [[ ! -e /dev/fuse ]]; then
    lxc_log_error "/dev/fuse is not available in this LXC (enable Proxmox feature fuse=1 via --with-fuse)."
    return 1
  fi
  lxc_apt_install --no-install-recommends fuse-overlayfs || return 1
  lxc_merge_daemon_json '.["storage-driver"] = "fuse-overlayfs"' || return 1
  systemctl restart docker || return 1
  lxc_wait_for 30 "Docker daemon readiness" lxc_docker_ready || return 1
}

lxc_install_docker_prune_timer() {
  lxc_log_info "Installing weekly Docker image/build-cache prune timer (never prunes volumes)..."
  lxc_write_file "/etc/systemd/system/docktun-docker-prune.service" 0644 <<'EOF'
[Unit]
Description=Prune unused Docker images and build cache older than 7 days (never volumes)
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/bin/docker image prune -af --filter "until=168h"
ExecStart=/usr/bin/docker builder prune -af --filter "until=168h"
EOF

  lxc_write_file "/etc/systemd/system/docktun-docker-prune.timer" 0644 <<'EOF'
[Unit]
Description=Weekly Docker image and build-cache prune

[Timer]
OnCalendar=weekly
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
EOF

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd systemctl enable docktun-docker-prune.timer
  elif command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    systemctl enable --now docktun-docker-prune.timer || systemctl enable docktun-docker-prune.timer || true
  fi
}

lxc_install_docker() {
  lxc_detect_os || lxc_die "Failed to detect OS from /etc/os-release."
  local docker_distro=""
  case "$LXC_OS_ID" in
    debian|ubuntu) docker_distro="$LXC_OS_ID" ;;
    *) lxc_die "Unsupported OS '${LXC_OS_ID}'." ;;
  esac
  [[ -n "${LXC_OS_CODENAME:-}" ]] || lxc_die "Could not determine OS codename for ${LXC_OS_ID}."

  local arch=""
  arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
  [[ "$arch" == "x86_64" ]] && arch="amd64"
  [[ "$arch" == "aarch64" ]] && arch="arm64"

  lxc_log_info "Installing official Docker CE for ${docker_distro} (${LXC_OS_CODENAME}, ${arch})..."
  export DEBIAN_FRONTEND=noninteractive
  lxc_run_cmd apt-get update
  lxc_apt_install --no-install-recommends ca-certificates curl gnupg jq

  local keyring="/etc/apt/keyrings/docker.asc"
  local gpg_url="https://download.docker.com/linux/${docker_distro}/gpg"
  local repo_url="https://download.docker.com/linux/${docker_distro}"
  local repo_file="/etc/apt/sources.list.d/docker.list"

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd install -m 0755 -d /etc/apt/keyrings
    lxc_run_cmd curl -fsSL "$gpg_url" -o "$keyring"
    lxc_log_info "[DRY-RUN] Would write ${repo_file} and install docker-ce + docker-compose-plugin"
  else
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL "$gpg_url" -o "$keyring"
    chmod a+r "$keyring"
    printf 'deb [arch=%s signed-by=%s] %s %s stable\n' \
      "$arch" "$keyring" "$repo_url" "$LXC_OS_CODENAME" > "$repo_file"
    apt-get update
    lxc_apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  fi

  lxc_apply_containerd_zfs_guard

  # Pin overlay2 on PVE 8.1+/9 (or install/use fuse-overlayfs BEFORE starting Docker on older PVE + ZFS)
  local root_fs=""
  root_fs="$(stat -f -c '%T' / 2>/dev/null || true)"
  if [[ "$root_fs" == "zfs" && -n "$PVE_MAJOR" && -n "$PVE_MINOR" ]] && { (( PVE_MAJOR < 8 )) || (( PVE_MAJOR == 8 && PVE_MINOR < 1 )); }; then
    if [[ "${DRY_RUN}" != "1" ]]; then
      [[ -e /dev/fuse ]] || lxc_die "ZFS on PVE < 8.1 requires /dev/fuse for fuse-overlayfs (pass --with-fuse)."
      lxc_apt_install --no-install-recommends fuse-overlayfs
    fi
    lxc_merge_daemon_json '.["storage-driver"] = "fuse-overlayfs"'
  else
    lxc_merge_daemon_json '.["storage-driver"] = "overlay2"'
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd usermod -aG docker "$ADMIN_USER"
    lxc_run_cmd systemctl daemon-reload
    lxc_run_cmd systemctl enable --now containerd
    lxc_run_cmd systemctl enable --now docker
    lxc_run_cmd docker run --rm hello-world
    lxc_install_docker_prune_timer
    return 0
  fi

  getent group docker >/dev/null 2>&1 || groupadd -r docker
  if id "$ADMIN_USER" >/dev/null 2>&1; then
    usermod -aG docker "$ADMIN_USER"
  fi

  systemctl daemon-reload
  systemctl enable --now containerd
  systemctl restart containerd
  systemctl enable docker

  if ! systemctl start docker; then
    lxc_enable_fuse_overlayfs_fallback || lxc_die "Docker failed to start with both overlay2 and fuse-overlayfs."
  fi
  lxc_wait_for 30 "Docker daemon readiness" lxc_docker_ready || lxc_die "Docker daemon did not become ready."

  local driver=""
  driver="$(docker info --format '{{.Driver}}' 2>/dev/null || true)"
  if [[ "$driver" == "vfs" ]]; then
    lxc_enable_fuse_overlayfs_fallback || true
    driver="$(docker info --format '{{.Driver}}' 2>/dev/null || true)"
    if [[ "$driver" == "vfs" ]]; then
      lxc_die "Docker remained on 'vfs' storage driver (enable --with-fuse or upgrade Proxmox VE to 8.1+ for overlay2)."
    fi
  fi
  lxc_log_info "Docker storage driver: ${driver:-unknown}"

  # Post-install smoke test detecting CVE-2025-52881 ip_unprivileged_port_start AppArmor error
  local smoke_out=""
  if ! smoke_out="$(docker run --rm hello-world 2>&1)"; then
    if grep -qiE 'ip_unprivileged_port_start|reopen fd .*permission denied' <<< "$smoke_out"; then
      lxc_log_error "Docker container start blocked by Proxmox host AppArmor profile (CVE-2025-52881):"
      lxc_log_error "  ${smoke_out}"
      lxc_log_error "Action required on Proxmox host: run 'apt update && apt full-upgrade' to install patched 'lxc-pve' (PVE 8.4/9.1+)."
      lxc_log_error "Temporary opt-in workaround if host cannot be updated immediately: recreate with --allow-unconfined-apparmor."
      lxc_die "Docker smoke test failed due to unpatched host lxc-pve AppArmor profile."
    fi
    lxc_log_warn "Docker hello-world smoke test could not complete (offline or registry unreachable): ${smoke_out}"
  else
    lxc_log_success "Docker CE smoke test passed (CVE-2025-52881 check OK)."
  fi
  lxc_install_docker_prune_timer
}

# --- Stage 3: Shared GPU Inside LXC -----------------------------------------

lxc_apt_pkg_exists() {
  local pkg="$1"
  local cand=""
  cand="$(apt-cache policy "$pkg" 2>/dev/null | awk '/Candidate:/ {print $2; exit}' || true)"
  [[ -n "$cand" && "$cand" != "(none)" ]]
}

lxc_apt_exact_version_spec() {
  local pkg="$1" host_ver="$2"
  local exact=""
  exact="$(apt-cache madison "$pkg" 2>/dev/null | awk -v v="$host_ver" '
    BEGIN { gsub(/\./, "\\.", v) }
    $3 ~ ("(^|:)" v "([-~+]|$)") { print $3; exit }
  ' || true)"
  [[ -n "$exact" ]] || return 1
  printf '%s=%s\n' "$pkg" "$exact"
}

lxc_enable_debian_nonfree() {
  local f=""
  local -a list_files=() extra_list_files=() source_files=()
  [[ -f /etc/apt/sources.list ]] && list_files+=(/etc/apt/sources.list)
  glob_matches extra_list_files "/etc/apt/sources.list.d/*.list"
  list_files+=(${extra_list_files[@]+"${extra_list_files[@]}"})
  glob_matches source_files "/etc/apt/sources.list.d/*.sources"
  for f in ${list_files[@]+"${list_files[@]}"}; do
    enable_nonfree_in_list_file "$f"
  done
  for f in ${source_files[@]+"${source_files[@]}"}; do
    enable_nonfree_in_sources_file "$f"
  done
}

lxc_setup_nvidia() {
  local host_ver=""
  if [[ -n "$HOST_NVIDIA_VERSION" ]]; then
    host_ver="$HOST_NVIDIA_VERSION"
  elif [[ -r "$NVIDIA_PROC_VERSION_FILE" ]]; then
    local content=""
    content="$(cat "$NVIDIA_PROC_VERSION_FILE" 2>/dev/null || true)"
    if [[ "$content" =~ Kernel[[:space:]]+Module([[:space:]]+for[[:space:]]+[^[:space:]]+)?[[:space:]]+([0-9]+(\.[0-9]+)+) ]]; then
      host_ver="${BASH_REMATCH[2]}"
    fi
  fi
  if [[ -z "$host_ver" ]]; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      host_ver="000.00.00"
    else
      lxc_die "Cannot read host NVIDIA driver version from ${NVIDIA_PROC_VERSION_FILE}."
    fi
  fi

  lxc_detect_os || lxc_die "Failed to detect OS from /etc/os-release."
  local cuda_repo="debian12"
  case "${LXC_OS_ID,,}" in
    debian)
      case "${LXC_OS_CODENAME,,}" in
        trixie|13) cuda_repo="debian13" ;;
        *) cuda_repo="debian12" ;;
      esac
      ;;
    ubuntu)
      case "${LXC_OS_CODENAME,,}" in
        noble) cuda_repo="ubuntu2404" ;;
        jammy) cuda_repo="ubuntu2204" ;;
        *) cuda_repo="ubuntu2404" ;;
      esac
      ;;
  esac

  local keyring_url="https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64/cuda-keyring_1.1-1_all.deb"
  local repo_url="https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64"
  local list_file="/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.list"
  local sources_file="/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.sources"
  local major="${host_ver%%.*}"

  lxc_log_info "Configuring NVIDIA userspace + container-toolkit (host driver ${host_ver}, repo ${cuda_repo})..."

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd curl -fsSL "$keyring_url" -o /tmp/cuda-keyring.deb
    lxc_run_cmd dpkg -i /tmp/cuda-keyring.deb
    lxc_apt_install --no-install-recommends --allow-downgrades --allow-change-held-packages \
      "libnvidia-compute-${major}" "nvidia-utils-${major}" "libnvidia-encode-${major}" "libnvidia-decode-${major}"
    lxc_run_cmd systemctl mask nvidia-persistenced.service
    lxc_run_cmd apt-mark hold "nvidia-*" "libnvidia-*" "libcuda*" "libnvcuvid*"
    lxc_run_cmd nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true
    lxc_run_cmd nvidia-ctk runtime configure --runtime=docker
    lxc_run_cmd nvidia-ctk cdi generate --output="$NVIDIA_CDI_OUTPUT"
    return 0
  fi

  if [[ "${LXC_OS_ID,,}" == "debian" ]]; then
    lxc_enable_debian_nonfree
  fi

  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  lxc_apt_install --no-install-recommends curl ca-certificates gnupg jq

  local tmp_deb=""
  lxc_mktemp tmp_deb --suffix=.deb
  curl -fsSL "$keyring_url" -o "$tmp_deb"
  dpkg -i "$tmp_deb"
  rm -f "$tmp_deb"
  local -a old_cuda_files=()
  glob_matches old_cuda_files "/etc/apt/sources.list.d/cuda-debian*.list" "/etc/apt/sources.list.d/cuda-debian*.sources" "/etc/apt/sources.list.d/cuda-ubuntu*.list" "/etc/apt/sources.list.d/cuda-ubuntu*.sources"
  local f=""
  for f in ${old_cuda_files[@]+"${old_cuda_files[@]}"}; do
    if [[ "$f" != "$list_file" && "$f" != "$sources_file" ]]; then
      rm -f "$f"
    fi
  done
  if [[ ! -f "$list_file" && ! -f "$sources_file" ]]; then
    printf 'deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] %s/ /\n' "$repo_url" > "$list_file"
    chmod 0644 "$list_file"
  fi
  apt-get update

  local -a pkgs=()
  local chosen="" candidate="" have_compute=0 have_utils=0
  for candidate in "libnvidia-compute-${major}" "libnvidia-compute" "libcuda1"; do
    if lxc_apt_pkg_exists "$candidate" && chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"; then
      pkgs+=("$chosen"); have_compute=1; break
    fi
  done
  for candidate in "nvidia-utils-${major}" "nvidia-utils" "nvidia-smi"; do
    if lxc_apt_pkg_exists "$candidate" && chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"; then
      pkgs+=("$chosen"); have_utils=1; break
    fi
  done
  for candidate in "libnvidia-encode-${major}" "libnvidia-encode1" "libnvidia-encode"; do
    if lxc_apt_pkg_exists "$candidate" && chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"; then
      pkgs+=("$chosen"); break
    fi
  done
  for candidate in "libnvidia-decode-${major}" "libnvidia-decode1" "libnvidia-decode" "libnvcuvid1"; do
    if lxc_apt_pkg_exists "$candidate" && chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"; then
      pkgs+=("$chosen"); break
    fi
  done
  if (( ! have_compute || ! have_utils || ${#pkgs[@]} == 0 )); then
    lxc_die "Exact NVIDIA userspace packages matching host driver '${host_ver}' were not found in APT repos (${LXC_OS_ID} ${LXC_OS_CODENAME:-unknown}). Ensure matching CUDA or Debian non-free packages exist for ${host_ver}, or match guest OS to host driver repo."
  fi
  lxc_apt_install --no-install-recommends --allow-downgrades --allow-change-held-packages "${pkgs[@]}"

  systemctl mask nvidia-persistenced.service || true
  local modconf=""
  for modconf in /etc/modprobe.d/nvidia*.conf; do
    [[ -e "$modconf" ]] && : > "$modconf"
  done
  lxc_write_file "/etc/modprobe.d/blacklist-nvidia.conf" 0644 <<'EOF'
blacklist nvidia
blacklist nvidia_drm
blacklist nvidia_modeset
blacklist nvidia_uvm
blacklist nouveau
EOF

  local -a installed_nv_pkgs=()
  mapfile -t installed_nv_pkgs < <(
    dpkg-query -W -f='${Status}\t${Package}\n' 'nvidia-*' 'libnvidia-*' 'libcuda*' 'libnvcuvid*' 2>/dev/null \
      | awk -F'\t' '$1 == "install ok installed" && $2 !~ /^(nvidia-container-toolkit|libnvidia-container)/ {print $2}'
  )
  if [[ "${#installed_nv_pkgs[@]}" -gt 0 ]]; then
    apt-mark hold "${installed_nv_pkgs[@]}"
  fi

  local nv_keyring="/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg"
  local nv_list="/etc/apt/sources.list.d/nvidia-container-toolkit.list"
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor --yes -o "$nv_keyring"
  chmod a+r "$nv_keyring"
  curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed "s#deb https://#deb [signed-by=${nv_keyring}] https://#g" > "$nv_list"
  apt-get update
  lxc_apt_install --allow-change-held-packages nvidia-container-toolkit

  nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true
  nvidia-ctk runtime configure --runtime=docker
  install -d -m 0755 "$(dirname "$NVIDIA_CDI_OUTPUT")"
  if nvidia-ctk cdi generate --output="$NVIDIA_CDI_OUTPUT" && [[ -s "$NVIDIA_CDI_OUTPUT" ]]; then
    lxc_merge_daemon_json '.features = ((.features // {}) * {"cdi": true})'
  else
    lxc_log_warn "nvidia-ctk cdi generate failed or produced empty ${NVIDIA_CDI_OUTPUT}; skipping features.cdi=true (runtime=nvidia/--gpus all will still work)."
  fi

  systemctl daemon-reload
  systemctl restart docker
  nvidia-smi || lxc_die "nvidia-smi failed inside LXC."
  local nv_smoke_err=""
  if ! nv_smoke_err="$(docker run --rm --gpus all -e NVIDIA_DISABLE_REQUIRE=1 "$NVIDIA_SMOKE_IMAGE" nvidia-smi 2>&1)"; then
    lxc_log_warn "Docker GPU smoke test (${NVIDIA_SMOKE_IMAGE}) failed (offline or image unavailable): ${nv_smoke_err}"
  else
    lxc_log_success "Docker NVIDIA GPU smoke test passed."
  fi
  return 0
}

lxc_setup_intel_amd_vaapi() {
  local want_intel="$1" want_amd="$2"
  lxc_log_info "Configuring Intel/AMD VA-API drivers and video/render group access..."
  lxc_detect_os || true
  if [[ "${DRY_RUN}" != "1" && "${LXC_OS_ID,,}" == "debian" ]] && (( want_intel == 1 )); then
    lxc_enable_debian_nonfree
  fi

  export DEBIAN_FRONTEND=noninteractive
  lxc_run_cmd apt-get update

  local -a pkgs=("vainfo" "va-driver-all")
  if (( want_intel == 1 )); then
    if [[ "${DRY_RUN}" == "1" ]] || lxc_apt_pkg_exists "intel-media-va-driver-non-free"; then
      pkgs+=("intel-media-va-driver-non-free")
    elif lxc_apt_pkg_exists "intel-media-va-driver"; then
      if [[ "${LXC_OS_ID,,}" == "ubuntu" ]]; then
        lxc_log_warn "intel-media-va-driver-non-free unavailable (Ubuntu 'multiverse' may be disabled); falling back to free intel-media-va-driver (low-power HuC/GuC encoders for Intel Arc/Gen11+ require multiverse)."
      else
        lxc_log_warn "intel-media-va-driver-non-free unavailable; falling back to intel-media-va-driver."
      fi
      pkgs+=("intel-media-va-driver")
    fi
  fi
  if (( want_amd == 1 )); then
    if [[ "${DRY_RUN}" == "1" ]] || lxc_apt_pkg_exists "mesa-va-drivers"; then
      pkgs+=("mesa-va-drivers")
    fi
  fi

  lxc_apt_install --no-install-recommends "${pkgs[@]}"

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd groupadd -r render
    lxc_run_cmd usermod -aG video,render "$ADMIN_USER"
    lxc_run_cmd vainfo --display drm --device /dev/dri/renderD128
    return 0
  fi

  getent group video >/dev/null 2>&1 || groupadd -r video
  getent group render >/dev/null 2>&1 || groupadd -r render
  if id "$ADMIN_USER" >/dev/null 2>&1; then
    usermod -aG video,render "$ADMIN_USER"
  fi

  local render_gid="" node_gid="" render_dev="" render_name="" render_vendor=""
  render_gid="$(getent group render 2>/dev/null | cut -d: -f3 || true)"
  for render_dev in /dev/dri/renderD*; do
    [[ -c "$render_dev" ]] || continue
    render_name="${render_dev##*/}"
    render_vendor="$(tr -d '[:space:]' < "${SYS_CLASS_DRM}/${render_name}/device/vendor" 2>/dev/null || true)"
    if [[ "${render_vendor,,}" == "0x10de" ]]; then
      lxc_log_info "Skipping VA-API vainfo check on NVIDIA DRM node ${render_dev}."
      continue
    fi
    node_gid="$(stat -c '%g' "$render_dev" 2>/dev/null || true)"
    if [[ -n "$render_gid" && -n "$node_gid" && "$node_gid" != "$render_gid" ]]; then
      lxc_log_warn "GID mismatch on ${render_dev} (node GID=${node_gid}, container render GID=${render_gid}); non-root VA-API access may fail."
    fi
    if id "$ADMIN_USER" >/dev/null 2>&1 && command -v runuser >/dev/null 2>&1; then
      runuser -u "$ADMIN_USER" -- vainfo --display drm --device "$render_dev" </dev/null >/dev/null 2>&1 && \
        lxc_log_success "vainfo --display drm passed as ${ADMIN_USER} on ${render_dev}." || \
        lxc_log_warn "vainfo --display drm failed as ${ADMIN_USER} on ${render_dev} (check video/render group GID mapping)."
    else
      vainfo --display drm --device "$render_dev" >/dev/null 2>&1 && \
        lxc_log_success "vainfo --display drm passed on ${render_dev}." || \
        lxc_log_warn "vainfo --display drm returned non-zero on ${render_dev}."
    fi
  done
  return 0
}

lxc_setup_gpu() {
  if [[ "$GPU_MODE" == "none" ]]; then
    lxc_log_info "GPU_MODE=none; skipping LXC GPU setup."
    return 0
  fi

  local do_nvidia=0 do_intel=0 do_amd=0
  case "$GPU_MODE" in
    nvidia) do_nvidia=1 ;;
    intel) do_intel=1 ;;
    amd) do_amd=1 ;;
    all) do_nvidia=1; do_intel=1; do_amd=1 ;;
    *,*)
      local -a mode_parts=()
      local mp=""
      IFS=',' read -ra mode_parts <<< "$GPU_MODE"
      for mp in "${mode_parts[@]}"; do
        case "$mp" in
          nvidia) do_nvidia=1 ;;
          intel) do_intel=1 ;;
          amd) do_amd=1 ;;
        esac
      done
      ;;
    auto)
      if [[ -e /dev/nvidiactl ]] || compgen -G "/dev/nvidia[0-9]*" >/dev/null 2>&1; then
        do_nvidia=1
      fi
      if compgen -G "/dev/dri/renderD*" >/dev/null 2>&1 || [[ -e /dev/kfd ]]; then
        [[ -e /dev/kfd ]] && do_amd=1
        local vf="" vid="" saw_nvidia_dri=0 saw_unknown_dri=0
        for vf in /sys/class/drm/renderD*/device/vendor; do
          [[ -r "$vf" ]] || continue
          vid="$(tr -d '[:space:]' < "$vf" 2>/dev/null || true)"
          case "${vid,,}" in
            0x8086) do_intel=1 ;;
            0x1002) do_amd=1 ;;
            0x10de) saw_nvidia_dri=1 ;;
            *) saw_unknown_dri=1 ;;
          esac
        done
        if (( do_nvidia == 0 && do_intel == 0 && do_amd == 0 && saw_nvidia_dri == 0 )); then
          do_intel=1; do_amd=1
        elif (( saw_unknown_dri == 1 && do_nvidia == 0 && do_intel == 0 && do_amd == 0 )); then
          do_intel=1; do_amd=1
        fi
      fi
      ;;
  esac

  if (( do_nvidia == 1 )); then lxc_setup_nvidia; fi
  if (( do_intel == 1 || do_amd == 1 )); then lxc_setup_intel_amd_vaapi "$do_intel" "$do_amd"; fi
  return 0
}

# --- Stage 4: Pre-installed DockTun -----------------------------------------

lxc_yaml_compose_scalar() {
  local val="$1"
  val="${val//\$/\$\$}"
  val="${val//\'/\'\'}"
  printf "'%s'" "$val"
}

lxc_find_existing_compose() {
  local dir="$1"
  local -a found=()
  local f=""
  for f in compose.yml compose.yaml docker-compose.yml docker-compose.yaml; do
    if [[ -f "${dir}/${f}" ]]; then
      found+=("${dir}/${f}")
    fi
  done
  if (( ${#found[@]} > 1 )); then
    lxc_log_warn "Multiple compose files found in ${dir}: ${found[*]}. Keep only one."
    return 2
  fi
  if (( ${#found[@]} == 1 )); then
    printf '%s\n' "${found[0]}"
    return 0
  fi
  return 1
}

lxc_render_docktun_compose() {
  cat <<EOF
name: docktun
services:
  docktun:
    image: ${DOCKTUN_IMAGE}
    container_name: docktun
    restart: unless-stopped
    ports:
      - "${DOCKTUN_PORT}:3001"
    environment:
      PORT: ${DOCKTUN_PORT}
      DOCKER_NETWORK: docktun
      TAILSCALE_STATE_VOLUME: docktun-tailscale-state
      TAILSCALE_NETWORK: docktun
      WIREGUARD_PORT: 51820
      DB_PATH: /app/data/app.db
      CLOUDFLARED_CONFIG_PATH: /etc/cloudflared/config.yml
      CLOUDFLARED_CONTAINER: docktun-cloudflared
      CLOUDFLARED_METRICS: http://docktun-cloudflared:2000/metrics
      SELF_PATH: /opt/projects/docktun
EOF
  if [[ -n "$DOCKTUN_ADMIN_USER" ]]; then
    printf '      ADMIN_USERNAME: %s\n' "$(lxc_yaml_compose_scalar "$DOCKTUN_ADMIN_USER")"
  fi
  if [[ -n "$DOCKTUN_ADMIN_PASSWORD" && "${TEMPLATE_HYGIENE:-0}" != "1" ]]; then
    printf '      ADMIN_PASSWORD: %s\n' "$(lxc_yaml_compose_scalar "$DOCKTUN_ADMIN_PASSWORD")"
  fi
  cat <<EOF
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - docktun-data:/app/data
      - docktun-cloudflared:/etc/cloudflared
      - docktun-wireguard:/app/data/wireguard
      - ${DOCKTUN_DIR}/projects:/opt/projects
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:3001/healthz"]
      interval: 30s
      timeout: 5s
      start_period: 30s
      retries: 3
    networks:
      docktun:
        aliases:
          - docktun
          - docktun-backend
          - docktun-frontend
EOF
  if [[ "$WITH_ZEROCONF" == "1" ]]; then
    cat <<EOF

  zeroconf:
    image: ${DOCKTUN_IMAGE}
    container_name: docktun-zeroconf
    restart: unless-stopped
    network_mode: host
    entrypoint: ["/app/zeroconf"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      DOMAIN: local
      HTTP_PORT: 80
      DOCKTUN_PORT: ${DOCKTUN_PORT}
EOF
  fi
  cat <<'EOF'

volumes:
  docktun-data:
  docktun-cloudflared:
  docktun-wireguard:

networks:
  docktun:
    name: docktun
    driver: bridge
EOF
}

lxc_docktun_health_probe() {
  docker exec docktun curl -fsS http://127.0.0.1:3001/healthz >/dev/null 2>&1
}

lxc_detect_primary_ip() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 -o addr show dev eth0 scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)"
    if [[ -z "$ip" ]]; then
      ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}' || true)"
    fi
  fi
  if [[ -z "$ip" ]] && command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  printf '%s' "${ip:-127.0.0.1}"
}

lxc_is_existing_docktun_on_port() {
  local port="$1"
  command -v docker >/dev/null 2>&1 || return 1
  local running=""
  running="$(docker inspect -f '{{.State.Running}}' docktun 2>/dev/null || true)"
  [[ "$running" == "true" ]] || return 1
  local mapped=""
  mapped="$(docker port docktun 3001/tcp 2>/dev/null || true)"
  grep -qE ":${port}([[:space:]]|$)" <<< "$mapped"
}

lxc_is_existing_zeroconf_running() {
  command -v docker >/dev/null 2>&1 || return 1
  local running=""
  running="$(docker inspect -f '{{.State.Running}}' docktun-zeroconf 2>/dev/null || true)"
  [[ "$running" == "true" ]]
}

lxc_install_docktun() {
  local projects_dir="${DOCKTUN_DIR}/projects"
  lxc_run_cmd install -d -m 0755 "$DOCKTUN_DIR" "$projects_dir"
  lxc_run_cmd chmod 0775 "$projects_dir"

  if [[ "${DRY_RUN}" != "1" ]] && id "$ADMIN_USER" >/dev/null 2>&1; then
    local target_group="$ADMIN_USER"
    getent group docker >/dev/null 2>&1 && target_group="docker"
    chown "${ADMIN_USER}:${target_group}" "$projects_dir"
    chmod 2775 "$projects_dir"
  fi

  if [[ "$DOCKTUN_PORT" == "8080" ]]; then
    lxc_log_warn "DockTun port is 8080. Keep INTERNAL_BIND_IP=127.0.0.1 in AIOStreams (or pass --docktun-port 8081) so stremthru:8080 does not collide."
  fi

  if [[ "${DRY_RUN}" != "1" ]] && lxc_port_in_use "$DOCKTUN_PORT"; then
    if lxc_is_existing_docktun_on_port "$DOCKTUN_PORT"; then
      lxc_log_info "Port ${DOCKTUN_PORT} is already held by running 'docktun' container (idempotent update)."
    else
      lxc_die "Port ${DOCKTUN_PORT} is already bound by another service inside the LXC. Re-run with --docktun-port 8081."
    fi
  fi

  local zeroconf_fallback=0
  if [[ "${DRY_RUN}" != "1" && "$WITH_ZEROCONF" == "1" ]] && lxc_port_in_use 80; then
    if lxc_is_existing_zeroconf_running; then
      lxc_log_info "Port 80 is already held by running 'docktun-zeroconf' container (idempotent update)."
    else
      lxc_log_warn "Port 80 is already bound inside the LXC by another non-docktun-zeroconf service; disabling ZeroConf mDNS and falling back to core DockTun only."
      WITH_ZEROCONF=0
      zeroconf_fallback=1
    fi
  fi

  local compose_file="" rc=0 preserved_compose=0
  if compose_file="$(lxc_find_existing_compose "$DOCKTUN_DIR")"; then
    preserved_compose=1
    lxc_log_info "Preserving existing compose file: ${compose_file}"
  else
    rc=$?
    if (( rc == 2 )); then
      lxc_die "Multiple compose files found in ${DOCKTUN_DIR}; keep only one."
    fi
    compose_file="${DOCKTUN_DIR}/compose.yml"
    if [[ "${DRY_RUN}" == "1" ]]; then
      lxc_log_info "[DRY-RUN] Would write ${compose_file} (port=${DOCKTUN_PORT}, zeroconf=${WITH_ZEROCONF}, SELF_PATH=/opt/projects/docktun, TAILSCALE_NETWORK=docktun)"
    else
      local tmp_file=""
      lxc_mktemp tmp_file "${DOCKTUN_DIR}/.compose.yml.XXXXXX"
      lxc_render_docktun_compose > "$tmp_file"
      if [[ -n "$DOCKTUN_ADMIN_PASSWORD" ]]; then
        chmod 0600 "$tmp_file"
      else
        chmod 0644 "$tmp_file"
      fi
      mv -f "$tmp_file" "$compose_file"
      lxc_log_success "Wrote ${compose_file}"
    fi
  fi

  local has_zeroconf_svc="$WITH_ZEROCONF"
  if [[ "${DRY_RUN}" != "1" ]]; then
    docker compose -f "$compose_file" config --quiet
    local services_out=""
    services_out="$(docker compose -f "$compose_file" config --services 2>/dev/null || true)"
    if [[ -n "$services_out" ]]; then
      if (( preserved_compose )) && ! grep -qx 'docktun' <<< "$services_out"; then
        lxc_die "Existing compose file ${compose_file} does not define a 'docktun' service."
      fi
      if grep -qx 'zeroconf' <<< "$services_out"; then
        has_zeroconf_svc=1
      else
        has_zeroconf_svc=0
      fi
    elif (( preserved_compose )); then
      if ! grep -qE '^[[:space:]]*docktun:' "$compose_file"; then
        lxc_die "Existing compose file ${compose_file} does not define a 'docktun' service."
      fi
      if grep -qE '^[[:space:]]*(zeroconf:|container_name:[[:space:]]*docktun-zeroconf)' "$compose_file"; then
        has_zeroconf_svc=1
      else
        has_zeroconf_svc=0
      fi
    fi
  fi

  local zc_active=0
  if [[ "$WITH_ZEROCONF" == "1" && "$has_zeroconf_svc" == "1" ]]; then
    zc_active=1
    lxc_run_cmd docker compose -f "$compose_file" pull
    lxc_run_cmd docker compose -f "$compose_file" up -d
  else
    if [[ "$WITH_ZEROCONF" == "1" && "$has_zeroconf_svc" == "0" && "$preserved_compose" == "1" ]]; then
      lxc_log_info "Preserved compose file ${compose_file} does not include 'zeroconf'; ZeroConf mDNS will remain inactive."
    fi
    if [[ "$WITH_ZEROCONF" == "0" && "${DRY_RUN}" != "1" ]] && lxc_is_existing_zeroconf_running; then
      lxc_log_info "Stopping running 'docktun-zeroconf' container (--no-zeroconf)..."
      docker stop docktun-zeroconf >/dev/null 2>&1 || true
      docker rm -f docktun-zeroconf >/dev/null 2>&1 || true
    fi
    lxc_run_cmd docker compose -f "$compose_file" pull docktun
    lxc_run_cmd docker compose -f "$compose_file" up -d --no-deps docktun
    zc_active=0
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would poll DockTun /healthz and display setup.token"
    return 0
  fi

  WITH_ZEROCONF="$zc_active"
  if ! lxc_wait_for "$DOCKTUN_HEALTH_TIMEOUT" "DockTun /healthz" lxc_docktun_health_probe; then
    docker logs --tail 50 docktun >&2 || true
    lxc_die "DockTun healthcheck failed."
  fi

  local host_ip=""
  host_ip="$(lxc_detect_primary_ip)"
  printf '[LXC STATE] DOCKTUN_ZEROCONF_ACTIVE=%s\n' "$zc_active"
  if (( zeroconf_fallback )); then
    printf '[LXC STATE] DOCKTUN_ZEROCONF_FALLBACK=port80\n'
  fi
  if [[ "$zc_active" == "1" ]]; then
    lxc_log_success "DockTun is healthy and reachable at http://${host_ip}:${DOCKTUN_PORT} (or http://docktun.local via mDNS)"
  else
    lxc_log_success "DockTun is healthy and reachable at http://${host_ip}:${DOCKTUN_PORT}"
  fi
  if [[ -z "$DOCKTUN_ADMIN_PASSWORD" ]]; then
    local setup_token=""
    setup_token="$(docker exec docktun sh -c 'if [ -f /app/data/setup.token ]; then cat /app/data/setup.token; fi' 2>/dev/null || true)"
    if [[ -n "$setup_token" ]]; then
      lxc_log_info "DockTun first-run setup token (/app/data/setup.token): ${setup_token}"
    fi
  fi
  return 0
}

# --- Stage 5: AIOStreams Readiness ------------------------------------------

lxc_generate_hex_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
    return 0
  fi
  if [[ -r /dev/urandom ]] && command -v od >/dev/null 2>&1; then
    od -An -tx1 -N32 /dev/urandom | tr -d ' \n'
    printf '\n'
    return 0
  fi
  lxc_die "Neither openssl nor od is available to generate SECRET_KEY."
}

lxc_render_aio_env() {
  local secret_key="$1"
  cat <<EOF
# AIOStreams / AIOStreams-All-Local Environment Template
# Copy or rename to .env before starting via DockTun or 'docker compose up -d'.
# Note: Changing SECRET_KEY later invalidates all encrypted user configurations.

SECRET_KEY=${secret_key}
AIO_DOMAIN=${AIO_DOMAIN}
BASE_URL=https://${AIO_DOMAIN}

# Keep internal companion services (including stremthru:8080) bound to 127.0.0.1
INTERNAL_BIND_IP=127.0.0.1
NODE_OPTIONS=--dns-result-order=ipv4first

PORT=3001
AIOMETADATA_PORT=4005
# WARNING: aio-portal (8088) mounts docker.sock; keep on trusted LAN/VPN only.
AIO_PORTAL_PORT=8088
EOF
}

lxc_prepare_aiostreams() {
  lxc_run_cmd install -d -m 0755 "$AIO_DIR"
  lxc_run_cmd chmod 0775 "$AIO_DIR"

  local env_template="${AIO_DIR}/.env.template"
  local env_generated="${AIO_DIR}/.env.generated"
  local aio_readme="${AIO_DIR}/README.md"
  local staged_aio_compose=""

  if [[ "${DRY_RUN}" == "1" ]]; then
    if [[ -n "$AIO_COMPOSE_B64" ]]; then
      lxc_log_info "[DRY-RUN] Would stage ${AIO_DIR}/compose.yml from --aio-compose if absent"
    elif [[ -n "$AIO_REPO_URL" ]]; then
      lxc_log_info "[DRY-RUN] Would install git (if needed) and clone ${AIO_REPO_URL}${AIO_REPO_BRANCH:+ (${AIO_REPO_BRANCH})} into ${AIO_DIR} if absent"
    fi
    lxc_log_info "[DRY-RUN] Would ensure ${AIO_DIR}/data, ${env_template}, ${env_generated} (non-clobbering), nginx-proxy-streaming.conf.example, check-readiness.sh, and README.md (non-clobbering)"
    return 0
  fi

  local existing_aio_compose="" aio_compose_rc=0
  if existing_aio_compose="$(lxc_find_existing_compose "$AIO_DIR")"; then
    aio_compose_rc=0
  else
    aio_compose_rc=$?
  fi
  if (( aio_compose_rc == 2 )); then
    lxc_die "Multiple compose files found in ${AIO_DIR}; keep only one."
  fi

  if [[ -n "$AIO_COMPOSE_B64" ]]; then
    if (( aio_compose_rc == 0 )); then
      lxc_log_info "Preserving existing AIOStreams compose file: ${existing_aio_compose}"
    else
      local tmp_aio_compose=""
      lxc_mktemp tmp_aio_compose "${AIO_DIR}/.compose.yml.XXXXXX"
      if ! base64 -d <<< "$AIO_COMPOSE_B64" > "$tmp_aio_compose" 2>/dev/null; then
        base64 --decode <<< "$AIO_COMPOSE_B64" > "$tmp_aio_compose"
      fi
      [[ -s "$tmp_aio_compose" ]] || lxc_die "Decoded --aio-compose file is empty."
      local raw_bytes="" clean_bytes=""
      raw_bytes="$(LC_ALL=C wc -c < "$tmp_aio_compose" | tr -d '[:space:]')"
      clean_bytes="$(LC_ALL=C tr -d '\0' < "$tmp_aio_compose" | wc -c | tr -d '[:space:]')"
      [[ "$raw_bytes" == "$clean_bytes" ]] || lxc_die "Decoded --aio-compose file contains NUL bytes."
      if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
        local tmp_val_dir="" val_compose=""
        lxc_mktemp tmp_val_dir -d
        val_compose="${tmp_val_dir}/compose.yml"
        cp "$tmp_aio_compose" "$val_compose"
        if [[ -f "${AIO_DIR}/.env" ]]; then
          cp "${AIO_DIR}/.env" "${tmp_val_dir}/.env"
        else
          : > "${tmp_val_dir}/.env"
        fi
        docker compose -f "$val_compose" --no-interpolate config --quiet || \
          lxc_die "Invalid Compose YAML in --aio-compose (failed 'docker compose config --quiet')."
      fi
      chmod 0644 "$tmp_aio_compose"
      mv -f "$tmp_aio_compose" "${AIO_DIR}/compose.yml"
      staged_aio_compose="${AIO_DIR}/compose.yml"
      lxc_log_success "Staged ${staged_aio_compose} from --aio-compose."
    fi
  elif [[ -n "$AIO_REPO_URL" ]]; then
    if [[ -d "${AIO_DIR}/.git" ]] || (( aio_compose_rc == 0 )); then
      lxc_log_info "Preserving existing AIOStreams project in ${AIO_DIR}; skipping --aio-repo clone."
    else
      if ! command -v git >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        lxc_run_cmd apt-get update
        lxc_apt_install --no-install-recommends git
      fi
      local tmp_clone=""
      lxc_mktemp tmp_clone -d "$(dirname "$AIO_DIR")/.aio-clone.XXXXXX"
      local -a clone_cmd=(git clone --depth 1)
      [[ -n "$AIO_REPO_BRANCH" ]] && clone_cmd+=(--branch "$AIO_REPO_BRANCH")
      clone_cmd+=(-- "$AIO_REPO_URL" "$tmp_clone")
      GIT_TERMINAL_PROMPT=0 GIT_SSH_COMMAND="ssh -o BatchMode=yes" "${clone_cmd[@]}" </dev/null || \
        lxc_die "Failed to clone --aio-repo '${AIO_REPO_URL}'."
      local clone_rc=0
      lxc_find_existing_compose "$tmp_clone" >/dev/null || clone_rc=$?
      if (( clone_rc == 2 )); then
        lxc_die "Multiple compose files found in --aio-repo '${AIO_REPO_URL}'; keep only one."
      fi
      if id "$ADMIN_USER" >/dev/null 2>&1; then
        local clone_grp="$ADMIN_USER"
        getent group docker >/dev/null 2>&1 && clone_grp="docker"
        find "$tmp_clone" -exec chown -h "${ADMIN_USER}:${clone_grp}" {} + 2>/dev/null || true
      fi
      shopt -s dotglob nullglob
      local item="" base=""
      for item in "${tmp_clone}"/*; do
        base="${item##*/}"
        if [[ "$base" == "data" && -d "${AIO_DIR}/data" && -z "$(ls -A "${AIO_DIR}/data" 2>/dev/null)" ]]; then
          rmdir "${AIO_DIR}/data" 2>/dev/null || true
        fi
        if [[ -e "${AIO_DIR}/${base}" ]]; then
          lxc_log_info "Preserving existing ${AIO_DIR}/${base} during --aio-repo clone."
          continue
        fi
        mv "$item" "${AIO_DIR}/${base}"
      done
      shopt -u dotglob nullglob
      rm -rf "$tmp_clone"
      if ! git config --system --get-all safe.directory 2>/dev/null | grep -qxF "$AIO_DIR"; then
        git config --system --add safe.directory "$AIO_DIR" || true
      fi
      if staged_aio_compose="$(lxc_find_existing_compose "$AIO_DIR")"; then
        lxc_log_success "Cloned --aio-repo into ${AIO_DIR} (compose: ${staged_aio_compose})."
      else
        lxc_log_success "Cloned --aio-repo into ${AIO_DIR}."
      fi
    fi
  fi

  install -d -m 0755 "${AIO_DIR}/data"
  chmod 0775 "${AIO_DIR}/data"

  if [[ -f "$env_generated" ]]; then
    lxc_log_info "Preserving existing ${env_generated} (will not overwrite SECRET_KEY)."
  else
    local secret_key=""
    secret_key="$(lxc_generate_hex_secret)"
    local tmp_gen=""
    lxc_mktemp tmp_gen "${AIO_DIR}/.env.generated.XXXXXX"
    lxc_render_aio_env "$secret_key" > "$tmp_gen"
    chmod 0600 "$tmp_gen"
    mv -f "$tmp_gen" "$env_generated"
  fi
  local tmp_tpl=""
  lxc_mktemp tmp_tpl "${AIO_DIR}/.env.template.XXXXXX"
  lxc_render_aio_env "REPLACE_WITH_64_HEX_CHARS" > "$tmp_tpl"
  chmod 0644 "$tmp_tpl"
  mv -f "$tmp_tpl" "$env_template"

  lxc_write_file "${AIO_DIR}/nginx-proxy-streaming.conf.example" 0644 <<EOF
# Reverse Proxy Example for AIOStreams BuiltinProxy, Web Player & WebSockets
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    # Note: 'http2 on;' requires Nginx >= 1.25.1 (Debian 13 / Ubuntu 24.04).
    # On Debian 12 (Nginx 1.22), replace these two lines with: listen 443 ssl http2;
    listen 443 ssl;
    http2 on;
    server_name ${AIO_DOMAIN};

    # ssl_certificate     /etc/letsencrypt/live/${AIO_DOMAIN}/fullchain.pem;
    # ssl_certificate_key /etc/letsencrypt/live/${AIO_DOMAIN}/privkey.pem;

    client_max_body_size 64m;

    location / {
        # Use http://127.0.0.1:3001 (never 'localhost', which can resolve to ::1 and fail
        # under Docker userland-proxy: false) when Nginx runs on the LXC host, or
        # http://aiostreams:3001 when Nginx runs inside Docker on the 'docktun' network.
        proxy_pass http://127.0.0.1:3001;
        proxy_http_version 1.1;

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Preserve HTTP 206 Partial Content byte-range seeking/scrubbing
        proxy_set_header Range \$http_range;
        proxy_set_header If-Range \$http_if_range;
        proxy_force_ranges on;
        proxy_pass_header Accept-Ranges;
        proxy_pass_header Content-Range;

        proxy_buffering off;
        proxy_request_buffering off;

        proxy_connect_timeout 60s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;

        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
    }
}
EOF

  lxc_write_file "${AIO_DIR}/check-readiness.sh" 0755 <<EOF
#!/usr/bin/env bash
set -euo pipefail

DOCKTUN_PORT="\${DOCKTUN_PORT:-${DOCKTUN_PORT}}"
WITH_ZEROCONF="\${WITH_ZEROCONF:-${WITH_ZEROCONF}}"
ADMIN_USER="\${ADMIN_USER:-${ADMIN_USER}}"
SYS_CLASS_DRM="\${SYS_CLASS_DRM:-${SYS_CLASS_DRM}}"
STRICT="\${STRICT:-0}"
[[ "\${1:-}" == "--strict" ]] && STRICT=1

PASS_COUNT=0
WARN_COUNT=0
FAIL_COUNT=0

ok()   { PASS_COUNT=\$((PASS_COUNT + 1)); printf '[PASS] %s\n' "\$*"; }
warn() { WARN_COUNT=\$((WARN_COUNT + 1)); printf '[WARN] %s\n' "\$*"; }
fail() { FAIL_COUNT=\$((FAIL_COUNT + 1)); printf '[FAIL] %s\n' "\$*"; }

port_in_use() {
  local p="\$1"
  local ss_out="" ns_out="" dk_ports="" nat_out=""
  if command -v ss >/dev/null 2>&1; then
    ss_out="\$(ss -H -tuln 2>/dev/null || true)"
    if grep -E ":\${p}([[:space:]]|\$)" >/dev/null <<< "\$ss_out"; then
      return 0
    fi
  elif command -v netstat >/dev/null 2>&1; then
    ns_out="\$(netstat -tuln 2>/dev/null || true)"
    if grep -E ":\${p}([[:space:]]|\$)" >/dev/null <<< "\$ns_out"; then
      return 0
    fi
  fi
  if command -v docker >/dev/null 2>&1; then
    dk_ports="\$(docker ps --format '{{.Ports}}' 2>/dev/null || true)"
    if grep -E "(^|[,[:space:]])([^[:space:],]+:)?\${p}->" >/dev/null <<< "\$dk_ports"; then
      return 0
    fi
  fi
  if [[ "\${EUID:-\$(id -u)}" -eq 0 ]] && command -v iptables >/dev/null 2>&1; then
    nat_out="\$(iptables -t nat -S DOCKER 2>/dev/null || true)"
    if grep -E -- "--dport \${p}([[:space:]]|\$)" >/dev/null <<< "\$nat_out"; then
      return 0
    fi
  fi
  return 1
}

echo "=== AIOStreams & Media LXC Readiness Check ==="
[[ -c /dev/net/tun ]] && ok "/dev/net/tun present" || warn "/dev/net/tun missing (required for WireGuard/WARP)"
command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && ok "Docker daemon reachable" || fail "Docker daemon unavailable"
command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 && ok "Docker Compose v2 available" || fail "Docker Compose v2 unavailable"

if [[ -d /dev/shm ]]; then
  shm_mb="\$(df -Pm /dev/shm 2>/dev/null | awk 'NR==2 {for (i=2; i<=NF; i++) if (\$i ~ /^[0-9]+\$/) {print \$i; exit}}' || echo 0)"
  (( \${shm_mb:-0} >= 512 )) && ok "/dev/shm size is \${shm_mb} MiB" || warn "/dev/shm size is \${shm_mb:-0} MiB (< 512 MiB)"
fi

if [[ -r /etc/docker/daemon.json ]]; then
  if command -v jq >/dev/null 2>&1; then
    if jq -e '.["default-ulimits"].nofile.Soft == 65536 and .["default-ulimits"].nofile.Hard == 65536' /etc/docker/daemon.json >/dev/null 2>&1; then
      ok "Docker daemon.json configures nofile=65536"
    else
      warn "Docker daemon.json missing nofile=65536"
    fi
  elif grep -q '65536' /etc/docker/daemon.json 2>/dev/null; then
    ok "Docker daemon.json configures nofile=65536"
  else
    warn "Docker daemon.json missing nofile=65536"
  fi
else
  warn "Docker daemon.json missing nofile=65536"
fi

if command -v getent >/dev/null 2>&1 && getent ahosts github.com >/dev/null 2>&1; then
  ok "Outbound DNS resolution succeeded"
else
  warn "Outbound DNS resolution failed"
fi

if [[ -e /dev/nvidiactl ]] || compgen -G "/dev/nvidia[0-9]*" >/dev/null 2>&1; then
  if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
    ok "NVIDIA GPU accessible via nvidia-smi"
  else
    fail "NVIDIA device node(s) present but nvidia-smi failed"
  fi
elif command -v nvidia-smi >/dev/null 2>&1; then
  if nvidia-smi >/dev/null 2>&1; then
    ok "NVIDIA GPU accessible via nvidia-smi"
  else
    warn "nvidia-smi installed but no NVIDIA device accessible"
  fi
fi

if compgen -G "/dev/dri/renderD*" >/dev/null 2>&1; then
  vaapi_ok=0
  non_nv_drm=0
  for node in /dev/dri/renderD*; do
    [[ -c "\$node" ]] || continue
    node_name="\${node##*/}"
    vendor="\$(tr -d '[:space:]' < "\${SYS_CLASS_DRM}/\${node_name}/device/vendor" 2>/dev/null || true)"
    [[ "\${vendor,,}" == "0x10de" ]] && continue
    non_nv_drm=1
    if command -v vainfo >/dev/null 2>&1; then
      if [[ "\${EUID:-\$(id -u)}" -eq 0 ]] && id "\$ADMIN_USER" >/dev/null 2>&1 && command -v runuser >/dev/null 2>&1; then
        if runuser -u "\$ADMIN_USER" -- vainfo --display drm --device "\$node" </dev/null >/dev/null 2>&1; then
          vaapi_ok=1
          break
        fi
      elif vainfo --display drm --device "\$node" >/dev/null 2>&1; then
        vaapi_ok=1
        break
      fi
    fi
  done
  if (( non_nv_drm == 0 )); then
    ok "DRM render node(s) belong to NVIDIA (VA-API check skipped)"
  elif (( vaapi_ok == 1 )); then
    ok "DRM/VAAPI vainfo --display drm succeeded"
  else
    warn "DRM render node(s) present, but vainfo --display drm failed (check video/render GIDs)"
  fi
fi

if [[ "\${EUID:-\$(id -u)}" -eq 0 ]]; then
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi 'Status: active'; then
    ok "UFW firewall active"
  else
    warn "UFW firewall is not active"
  fi
  if command -v iptables >/dev/null 2>&1 && iptables -S DOCKER-USER 2>/dev/null | grep -q -- "-j DROP"; then
    ok "DOCKER-USER ingress restriction active"
  else
    warn "DOCKER-USER ingress restriction missing or inactive"
  fi
  if [[ -f /etc/ufw/after6.rules ]] && grep -q '^# BEGIN DOCKTUN DOCKER-USER$' /etc/ufw/after6.rules 2>/dev/null \
     && ! grep -qE '^[[:space:]]*IPV6[[:space:]]*=[[:space:]]*["'\'']?[Nn][Oo]["'\'']?' /etc/default/ufw 2>/dev/null \
     && command -v ip6tables >/dev/null 2>&1 && ip6tables -L -n >/dev/null 2>&1; then
    if ip6tables -S DOCKER-USER 2>/dev/null | grep -q -- "-j DROP"; then
      ok "IPv6 DOCKER-USER ingress restriction active"
    else
      warn "IPv6 DOCKER-USER ingress restriction missing or inactive"
    fi
  fi
else
  printf '[SKIP] UFW / DOCKER-USER check requires root\n'
fi

if command -v docker >/dev/null 2>&1 && docker exec docktun curl -fsS http://127.0.0.1:3001/healthz >/dev/null 2>&1; then
  ok "DockTun container /healthz passing"
else
  warn "DockTun /healthz did not respond"
fi

check_ports=(3001 4005 8088 51820 "\${DOCKTUN_PORT}")
if [[ "\${DOCKTUN_PORT}" != "8080" ]]; then
  check_ports=(8080 "\${check_ports[@]}")
fi
if [[ "\${WITH_ZEROCONF}" == "1" ]]; then
  check_ports+=(80 5353)
fi

for check_port in "\${check_ports[@]}"; do
  if port_in_use "\$check_port"; then
    if [[ "\$check_port" == "\$DOCKTUN_PORT" ]]; then
      ok "Port \${check_port} bound (DockTun)"
    elif [[ "\$WITH_ZEROCONF" == "1" && ( "\$check_port" == "80" || "\$check_port" == "5353" ) ]]; then
      ok "Port \${check_port} bound (DockTun ZeroConf)"
    else
      warn "Port \${check_port} is already bound"
    fi
  else
    if [[ "\${WITH_ZEROCONF}" == "1" && "\$check_port" == "80" ]]; then
      warn "Port 80 is free (WITH_ZEROCONF=1 expects docktun-zeroconf on :80)"
    else
      ok "Port \${check_port} is free"
    fi
  fi
done

echo "=== Summary: \${PASS_COUNT} passed, \${WARN_COUNT} warnings, \${FAIL_COUNT} failed ==="
(( FAIL_COUNT > 0 )) && exit 1
(( STRICT == 1 && WARN_COUNT > 0 )) && exit 1
exit 0
EOF

  if [[ -f "$aio_readme" ]]; then
    lxc_log_info "Preserving existing ${aio_readme}."
  else
    lxc_write_file "$aio_readme" 0644 <<EOF
# AIOStreams & Media Stack Directory (${AIO_DIR})

This directory is mounted inside the DockTun container as \`/opt/projects/aiostreams\`.

## What You Need to Place in This Directory

| File / Folder | Required? | Purpose |
|---|---|---|
| \`compose.yml\` *(or \`compose.yaml\`)* | **Yes** | Your AIOStreams / media Docker Compose stack (keep **only one** compose file here; pre-staged automatically if you passed \`--aio-compose\` or \`--aio-repo\`). |
| \`.env\` | **Yes** | Runtime environment secrets. Copy from \`.env.generated\` (\`cp -n .env.generated .env && chmod 0600 .env\`), then add any upstream/provider tokens. **Never commit \`.env\` or \`.env.generated\`.** |
| Extra app config files *(optional)* | Optional | Place stack config files under \`./\` or \`./data/\`. Host-mapped media from Proxmox is mounted at \`/mnt/media\` (and any custom \`--mount\` targets). |

## Files Already Created Here by \`proxmox-lxc.sh\`

| File / Folder | Purpose |
|---|---|
| \`data/\` | Persistent application data directory (\`chmod 2775\`, owned by \`${ADMIN_USER}:docker\` when initially empty). |
| \`.env.generated\` | Pre-generated \`0600\` env starter containing a fresh 64-char hex \`SECRET_KEY\`, \`INTERNAL_BIND_IP=127.0.0.1\`, \`BASE_URL=https://${AIO_DOMAIN}\`, and \`NODE_OPTIONS=--dns-result-order=ipv4first\`. *(Under \`--template-hygiene\`, this is regenerated on each clone's first boot.)* |
| \`.env.template\` | Non-secret reference template (\`SECRET_KEY=REPLACE_WITH_64_HEX_CHARS\`). Safe to keep in Git. |
| \`nginx-proxy-streaming.conf.example\` | TLS reverse proxy config with \`proxy_buffering off\`, HTTP \`206\` byte-range headers, and WebSocket upgrades (includes Debian 12 Nginx 1.22 \`http2\` note). |
| \`check-readiness.sh\` | Diagnostic check for \`/dev/net/tun\`, Docker, \`/dev/shm\`, \`nofile=65536\`, DNS, GPU (\`nvidia-smi\`/\`vainfo\`), UFW + \`DOCKER-USER\`, DockTun \`/healthz\`, and port conflicts. |
| \`README.md\` | This directory guide (preserved on re-runs or \`--aio-repo\` clones). |

## Important LXC Networking & Port Notes

- **\`userland-proxy: false\` & Loopback**: Host-LXC reverse proxies (Nginx/Caddy) must target \`http://127.0.0.1:3001\` — **never \`localhost\`**, which can resolve to IPv6 \`::1\` and fail with \`Connection refused\`. If your reverse proxy runs in Docker on the \`docktun\` network, target \`http://aiostreams:3001\`.
- **DockTun External Network**: Join containers to DockTun's bridge network when proxying internally:
  \`\`\`yaml
  networks:
    docktun:
      external: true
  \`\`\`
- **Port \`8080\` & \`80\` Collisions**: DockTun runs on host port \`${DOCKTUN_PORT}\` (and ZeroConf \`http://docktun.local\` binds \`:80\` + \`5353/udp\` when enabled). Services like \`stremthru\` or Wolf Den also default to \`8080\` — keep \`INTERNAL_BIND_IP=127.0.0.1\` or run DockTun on \`8081\` (\`--docktun-port 8081\`).

## Shared GPU Compose Snippets (Host-Driven, Current LXC Mode: \`${GPU_MODE}\`)

- **Intel iGPU / Arc or AMD Radeon / APU**:
  \`\`\`yaml
  devices:
    - /dev/dri:/dev/dri
    # - /dev/kfd:/dev/kfd  # Uncomment for AMD ROCm / compute if present
  \`\`\`
- **NVIDIA GPU** *(uses \`nvidia-container-toolkit\` + host shared nodes)*:
  \`\`\`yaml
  environment:
    - NVIDIA_VISIBLE_DEVICES=all
    - NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: all
            capabilities: [gpu]
  \`\`\`
- **Optional Wolf / Purah Cloud Gaming**: Mount \`HOST_APPS_STATE_FOLDER\` at the **exact same path** inside and outside Wolf, expose Moonlight host-network ports (\`47984/tcp\`, \`47989/tcp\`, \`48010/tcp\`, \`47998-48200/udp\`, \`5353/udp\`), provision \`--with-wolf-input\`, and restrict access to trusted LAN/WireGuard/Tailscale (\`docker.sock\` is root-equivalent).

## Quick Launch

\`\`\`bash
cd "${AIO_DIR}"
cp -n .env.generated .env && chmod 0600 .env
sudo ./check-readiness.sh
docker compose up -d   # or deploy directly in the DockTun Web UI -> Projects
\`\`\`
EOF
  fi

  if id "$ADMIN_USER" >/dev/null 2>&1; then
    local target_group="$ADMIN_USER"
    getent group docker >/dev/null 2>&1 && target_group="docker"
    local -a chown_targets=(
      "$AIO_DIR"
      "$env_generated"
      "$env_template"
      "${AIO_DIR}/nginx-proxy-streaming.conf.example"
      "${AIO_DIR}/check-readiness.sh"
      "$aio_readme"
    )
    [[ -n "$staged_aio_compose" && -f "$staged_aio_compose" ]] && chown_targets+=("$staged_aio_compose")
    chown "${ADMIN_USER}:${target_group}" "${chown_targets[@]}"
    if [[ -z "$(ls -A "${AIO_DIR}/data" 2>/dev/null)" ]]; then
      chown "${ADMIN_USER}:${target_group}" "${AIO_DIR}/data"
    fi
    chmod 2775 "$AIO_DIR" "${AIO_DIR}/data"
    chmod 0600 "$env_generated"
    chmod 0644 "$env_template" "${AIO_DIR}/nginx-proxy-streaming.conf.example" "$aio_readme"
    chmod 0755 "${AIO_DIR}/check-readiness.sh"
  fi

  DOCKTUN_PORT="$DOCKTUN_PORT" WITH_ZEROCONF="$WITH_ZEROCONF" "${AIO_DIR}/check-readiness.sh"
}

lxc_apply_template_hygiene() {
  if [[ "$TEMPLATE_HYGIENE" != "1" ]]; then
    return 0
  fi
  lxc_log_info "Applying template hygiene (first-boot SSH host key regen, docktun-firstboot.sh/.service, stripping live DockTun volumes/.env.generated/random-seed, machine-id)..."

  lxc_write_file "/etc/systemd/system/regenerate-ssh-host-keys.service" 0644 <<'EOF'
[Unit]
Description=Regenerate SSH host keys on first boot if missing
DefaultDependencies=no
ConditionPathExistsGlob=!/etc/ssh/ssh_host_*_key
After=local-fs.target
Before=ssh.service ssh.socket sshd.service sysinit.target shutdown.target
Conflicts=shutdown.target

[Service]
Type=oneshot
ExecStart=/usr/bin/ssh-keygen -A
ExecStartPost=-/bin/systemctl try-reload-or-restart ssh.service sshd.service

[Install]
WantedBy=multi-user.target
EOF

  local compose_file="" rc=0
  if compose_file="$(lxc_find_existing_compose "$DOCKTUN_DIR")"; then
    :
  else
    rc=$?
    if (( rc == 2 )); then
      lxc_die "Multiple compose files found in ${DOCKTUN_DIR}; keep only one."
    fi
    compose_file="${DOCKTUN_DIR}/compose.yml"
  fi
  local sentinel_file="${DOCKTUN_DIR}/.firstboot-pending"
  local firstboot_script="${DOCKTUN_FIRSTBOOT_SCRIPT:-/usr/local/libexec/docktun-firstboot.sh}"
  local unit_exec_start="${firstboot_script}" unit_exec_post="/bin/rm -f ${sentinel_file}"
  if [[ "$firstboot_script" =~ [[:space:]\"\\] ]]; then
    local esc_fb="${firstboot_script//\\/\\\\}"
    esc_fb="${esc_fb//\"/\\\"}"
    unit_exec_start="\"${esc_fb}\""
  fi
  if [[ "$sentinel_file" =~ [[:space:]\"\\] ]]; then
    local esc_sf="${sentinel_file//\\/\\\\}"
    esc_sf="${esc_sf//\"/\\\"}"
    unit_exec_post="/bin/rm -f \"${esc_sf}\""
  fi
  local q_compose="" q_aio_dir="" q_admin_user=""
  printf -v q_compose '%q' "$compose_file"
  printf -v q_aio_dir '%q' "$AIO_DIR"
  printf -v q_admin_user '%q' "$ADMIN_USER"

  lxc_write_file "$firstboot_script" 0755 <<EOF
#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE=${q_compose}
AIO_DIR=${q_aio_dir}
ADMIN_USER=${q_admin_user}

tmp_env=""
trap '[[ -n "\${tmp_env:-}" ]] && rm -f "\$tmp_env"' EXIT

if [[ -f "\$COMPOSE_FILE" ]]; then
  /usr/bin/docker compose -f "\$COMPOSE_FILE" up -d
fi

if [[ -f "\${AIO_DIR}/.env.template" && ! -f "\${AIO_DIR}/.env.generated" ]]; then
  secret_key=""
  if command -v openssl >/dev/null 2>&1; then
    secret_key="\$(openssl rand -hex 32)"
  elif [[ -r /dev/urandom ]] && command -v od >/dev/null 2>&1; then
    secret_key="\$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
  fi
  if [[ -n "\$secret_key" ]]; then
    tmp_env="\$(mktemp "\${AIO_DIR}/.env.generated.XXXXXX")"
    sed "s/REPLACE_WITH_64_HEX_CHARS/\${secret_key}/g" "\${AIO_DIR}/.env.template" > "\$tmp_env"
    chmod 0600 "\$tmp_env"
    if id "\$ADMIN_USER" >/dev/null 2>&1; then
      target_group="\$ADMIN_USER"
      getent group docker >/dev/null 2>&1 && target_group="docker"
      chown "\${ADMIN_USER}:\${target_group}" "\$tmp_env"
    fi
    mv -f "\$tmp_env" "\${AIO_DIR}/.env.generated"
    tmp_env=""
  fi
fi
EOF

  lxc_write_file "/etc/systemd/system/docktun-firstboot.service" 0644 <<EOF
[Unit]
Description=Start DockTun stack and regenerate clone secrets on first boot
After=docker.service network-online.target
Wants=docker.service network-online.target
ConditionPathExists=${sentinel_file}

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=${unit_exec_start}
ExecStartPost=${unit_exec_post}

[Install]
WantedBy=multi-user.target
EOF

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd systemctl enable regenerate-ssh-host-keys.service
    lxc_run_cmd systemctl enable docktun-firstboot.service
    lxc_run_cmd touch "$sentinel_file"
    lxc_run_cmd docker compose -f "$compose_file" down -v
    lxc_run_cmd apt-get clean
    lxc_run_cmd rm -rf /var/lib/apt/lists/* /var/lib/docker/engine-id
    lxc_run_cmd rm -f "${AIO_DIR}/.env.generated" /var/lib/systemd/random-seed /etc/ssh/ssh_host_*
    lxc_run_cmd truncate -s 0 /etc/machine-id
    return 0
  fi

  if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    systemctl enable regenerate-ssh-host-keys.service || true
    systemctl enable docktun-firstboot.service || true
  fi

  if [[ -f "$compose_file" ]] && command -v docker >/dev/null 2>&1; then
    docker compose -f "$compose_file" down -v >/dev/null 2>&1 || true
  fi
  install -d -m 0755 "$DOCKTUN_DIR"
  : > "$sentinel_file"
  chmod 0644 "$sentinel_file"

  if command -v apt-get >/dev/null 2>&1; then
    apt-get clean >/dev/null 2>&1 || true
  fi
  rm -rf /var/lib/apt/lists/* /var/lib/docker/engine-id
  rm -f "${AIO_DIR}/.env.generated" /var/lib/systemd/random-seed /etc/ssh/ssh_host_*
  truncate -s 0 /etc/machine-id
  if [[ -L /var/lib/dbus/machine-id ]]; then
    if [[ "$(readlink /var/lib/dbus/machine-id 2>/dev/null || true)" != "/etc/machine-id" ]]; then
      ln -sf /etc/machine-id /var/lib/dbus/machine-id
    fi
  elif [[ -e /var/lib/dbus/machine-id ]]; then
    ln -sf /etc/machine-id /var/lib/dbus/machine-id
  fi
  return 0
}

lxc_payload_main() {
  if [[ "${DRY_RUN}" != "1" && "${EUID:-$(id -u)}" -ne 0 ]]; then
    lxc_die "Inner LXC payload must run as root."
  fi
  lxc_harden
  lxc_install_docker
  lxc_setup_gpu
  lxc_install_docktun
  lxc_prepare_aiostreams
  lxc_apply_template_hygiene
  lxc_log_success "Inner LXC provisioning complete."
}

lxc_payload_main "$@"
EOF_LXC_PAYLOAD
}

resolve_stream_gpu_mode() {
  if (( GPUS_RESOLVED )) || (( DO_NVIDIA || DO_INTEL || DO_AMD )); then
    if (( DO_NVIDIA && DO_INTEL && DO_AMD )); then
      printf 'all\n'
      return 0
    fi
    local -a parts=()
    (( DO_NVIDIA )) && parts+=("nvidia")
    (( DO_INTEL )) && parts+=("intel")
    (( DO_AMD )) && parts+=("amd")
    if (( ${#parts[@]} == 0 )); then
      printf 'none\n'
    else
      local IFS=','
      printf '%s\n' "${parts[*]}"
    fi
    return 0
  fi
  printf '%s\n' "$GPU_MODE"
}

detect_host_nvidia_version() {
  if [[ -n "${HOST_NVIDIA_VERSION:-}" ]]; then
    printf '%s\n' "$HOST_NVIDIA_VERSION"
    return 0
  fi
  if [[ -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    local content=""
    content="$(cat "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" 2>/dev/null || true)"
    if [[ "$content" =~ Kernel[[:space:]]+Module([[:space:]]+for[[:space:]]+[^[:space:]]+)?[[:space:]]+([0-9]+(\.[0-9]+)+) ]]; then
      printf '%s\n' "${BASH_REMATCH[2]}"
      return 0
    fi
  fi
  if command -v nvidia-smi >/dev/null 2>&1; then
    local smi_ver=""
    smi_ver="$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d '[:space:]' || true)"
    if [[ "$smi_ver" =~ ^[0-9]+(\.[0-9]+)+$ ]]; then
      printf '%s\n' "$smi_ver"
      return 0
    fi
  fi
  printf '\n'
}

stream_lxc_payload() {
  local resolved_gpu="" host_nv_ver="" aio_compose_b64=""
  resolved_gpu="$(resolve_stream_gpu_mode)"
  if (( DO_NVIDIA )) || [[ "$resolved_gpu" == *nvidia* ]]; then
    host_nv_ver="$(detect_host_nvidia_version)"
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would stream inner LXC provisioning payload to CT ${CTID} via 'pct exec ${CTID} -- bash -s' (gpu=${resolved_gpu}, secrets quoted via printf '%q', never on argv)."
    return 0
  fi

  if [[ -n "$AIO_COMPOSE_FILE" && -r "$AIO_COMPOSE_FILE" ]]; then
    aio_compose_b64="$(base64 < "$AIO_COMPOSE_FILE" | tr -d '\r\n')"
  fi

  EFFECTIVE_ZEROCONF=""
  EFFECTIVE_ZEROCONF_FALLBACK=""
  ensure_tmp_dir
  local payload_out="${TMP_WORK_DIR}/lxc_payload.out"
  log_info "Streaming inner LXC provisioning payload to CT ${CTID} (GPU mode: ${resolved_gpu})..."
  {
    printf 'DRY_RUN=%q\n' "$DRY_RUN"
    printf 'ADMIN_USER=%q\n' "$ADMIN_USER"
    printf 'SSH_PUBKEY=%q\n' "$SSH_PUBKEY"
    printf 'DOCKTUN_PORT=%q\n' "$DOCKTUN_PORT"
    printf 'DOCKTUN_DIR=%q\n' "$DOCKTUN_DIR"
    printf 'DOCKTUN_ADMIN_USER=%q\n' "$DOCKTUN_ADMIN_USER"
    printf 'DOCKTUN_ADMIN_PASSWORD=%q\n' "$DOCKTUN_ADMIN_PASSWORD"
    printf 'LAN_SUBNETS=%q\n' "$LAN_SUBNETS"
    printf 'ENABLE_UFW=%q\n' "1"
    printf 'ENABLE_WIREGUARD_PORT=%q\n' "1"
    printf 'WITH_ZEROCONF=%q\n' "$WITH_ZEROCONF"
    printf 'WITH_WOLF_INPUT=%q\n' "$WITH_WOLF_INPUT"
    printf 'GPU_MODE=%q\n' "$resolved_gpu"
    printf 'AIO_DOMAIN=%q\n' "$AIO_DOMAIN"
    printf 'AIO_TIMEZONE=%q\n' "${AIO_TIMEZONE:-}"
    printf 'PVE_MAJOR=%q\n' "${PVE_MAJOR:-}"
    printf 'PVE_MINOR=%q\n' "${PVE_MINOR:-}"
    printf 'TEMPLATE_HYGIENE=%q\n' "$TEMPLATE_HYGIENE"
    [[ -n "$aio_compose_b64" ]] && printf 'AIO_COMPOSE_B64=%q\n' "$aio_compose_b64"
    [[ -n "$AIO_REPO_URL" ]] && printf 'AIO_REPO_URL=%q\n' "$AIO_REPO_URL"
    [[ -n "$AIO_REPO_BRANCH" ]] && printf 'AIO_REPO_BRANCH=%q\n' "$AIO_REPO_BRANCH"
    [[ -n "$host_nv_ver" ]] && printf 'HOST_NVIDIA_VERSION=%q\n' "$host_nv_ver"
    [[ -n "${DOCKTUN_IMAGE:-}" ]] && printf 'DOCKTUN_IMAGE=%q\n' "$DOCKTUN_IMAGE"
    [[ -n "${NVIDIA_SMOKE_IMAGE:-}" ]] && printf 'NVIDIA_SMOKE_IMAGE=%q\n' "$NVIDIA_SMOKE_IMAGE"
    [[ -n "${DOCKTUN_HEALTH_TIMEOUT:-}" ]] && printf 'DOCKTUN_HEALTH_TIMEOUT=%q\n' "$DOCKTUN_HEALTH_TIMEOUT"
    emit_lxc_payload
  } | pct exec "$CTID" -- bash -s | tee "$payload_out"
  if grep -q 'DOCKTUN_ZEROCONF_ACTIVE=0' "$payload_out" 2>/dev/null; then
    EFFECTIVE_ZEROCONF=0
  elif grep -q 'DOCKTUN_ZEROCONF_ACTIVE=1' "$payload_out" 2>/dev/null; then
    EFFECTIVE_ZEROCONF=1
  fi
  if grep -q 'DOCKTUN_ZEROCONF_FALLBACK=port80' "$payload_out" 2>/dev/null; then
    EFFECTIVE_ZEROCONF_FALLBACK="port80"
  fi
}

detect_ct_eth0_ip() {
  local ctid="${1:-$CTID}"
  local raw="" ip=""
  raw="$(pct exec "$ctid" -- bash -c '
    if command -v ip >/dev/null 2>&1; then
      out="$(ip -4 -o addr show dev eth0 scope global 2>/dev/null | awk "{print \$4}" | cut -d/ -f1 | head -n1)"
      [[ -z "$out" ]] && out="$(ip -4 route get 1.1.1.1 2>/dev/null | awk "{for(i=1;i<=NF;i++) if(\$i==\"src\") {print \$(i+1); exit}}")"
      [[ -z "$out" ]] && out="$(ip -4 -o addr show scope global 2>/dev/null | awk "{print \$4}" | cut -d/ -f1 | head -n1)"
      printf "%s\n" "$out"
    elif command -v hostname >/dev/null 2>&1; then
      hostname -I 2>/dev/null | awk "{print \$1}"
    fi
  ' 2>/dev/null || true)"
  if [[ "$raw" =~ ([0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}) ]]; then
    ip="${BASH_REMATCH[1]}"
  fi
  printf '%s' "$ip"
}

detect_ct_docktun_port() {
  local ctid="${1:-$CTID}"
  if [[ "${DRY_RUN}" == "1" ]] || ! command -v pct >/dev/null 2>&1; then
    printf '%s\n' "$DOCKTUN_PORT"
    return 0
  fi
  local port=""
  port="$(pct exec "$ctid" -- bash -c '
    dt_dir="${1:-/opt/docktun}"
    if command -v docker >/dev/null 2>&1; then
      p="$(docker port docktun 3001/tcp 2>/dev/null | head -n1 | awk -F: "{print \$NF}" | tr -d "[:space:]")"
      if [[ "$p" =~ ^[0-9]+$ ]]; then
        printf "%s\n" "$p"
        exit 0
      fi
    fi
    for cf in "${dt_dir}/compose.yml" "${dt_dir}/compose.yaml" "${dt_dir}/docker-compose.yml" "${dt_dir}/docker-compose.yaml"; do
      if [[ -r "$cf" ]]; then
        p="$(awk "!/^[[:space:]]*#/ && /[0-9]+:3001(\\/(tcp|udp))?([\"'\'']|[[:space:]]|#|\$)/ { if (match(\$0, /[0-9]+:3001/)) { s=substr(\$0, RSTART, RLENGTH); sub(/:3001\$/, \"\", s); print s; exit } }" "$cf" 2>/dev/null)"
        if [[ "$p" =~ ^[0-9]+$ ]]; then
          printf "%s\n" "$p"
          exit 0
        fi
        p="$(awk -F: "!/^[[:space:]]*#/ && /^[[:space:]]*PORT:[[:space:]]*[0-9]+/ {sub(/#.*/, \"\", \$2); gsub(/[^0-9]/, \"\", \$2); print \$2; exit}" "$cf" 2>/dev/null)"
        if [[ "$p" =~ ^[0-9]+$ ]]; then
          printf "%s\n" "$p"
          exit 0
        fi
      fi
    done
  ' _ "$DOCKTUN_DIR" 2>/dev/null || true)"
  port="${port//[^0-9]/}"
  if [[ "$port" =~ ^[1-9][0-9]*$ ]] && (( 10#$port >= 1 && 10#$port <= 65535 )); then
    printf '%s\n' "$port"
  else
    printf '%s\n' "$DOCKTUN_PORT"
  fi
}

resolve_ct_display_ip() {
  local ctid="${1:-$CTID}"
  local lxc_ip=""
  if [[ "${DRY_RUN}" != "1" ]] && command -v pct >/dev/null 2>&1; then
    lxc_ip="$(detect_ct_eth0_ip "$ctid")"
  fi
  if [[ -z "$lxc_ip" && "${IP_ADDR,,}" != "dhcp" ]]; then
    lxc_ip="${IP_ADDR%/*}"
  fi
  printf '%s' "${lxc_ip:-<lxc-ip>}"
}

detect_ct_zeroconf_active() {
  local ctid="${1:-$CTID}"
  if [[ -n "${EFFECTIVE_ZEROCONF:-}" ]]; then
    printf '%s\n' "$EFFECTIVE_ZEROCONF"
    return 0
  fi
  if [[ "${WITH_ZEROCONF}" == "0" ]]; then
    printf '0\n'
    return 0
  fi
  if [[ "${DRY_RUN}" == "1" ]] || ! command -v pct >/dev/null 2>&1; then
    printf '%s\n' "$WITH_ZEROCONF"
    return 0
  fi
  local zc_state=""
  zc_state="$(pct exec "$ctid" -- bash -c '
    dt_dir="${1:-/opt/docktun}"
    if command -v docker >/dev/null 2>&1; then
      z_run="$(docker inspect -f "{{.State.Running}}" docktun-zeroconf 2>/dev/null || true)"
      if [[ "$z_run" == "true" ]]; then
        printf "1\n"
        exit 0
      fi
      d_run="$(docker inspect -f "{{.State.Running}}" docktun 2>/dev/null || true)"
      if [[ "$d_run" == "true" ]]; then
        printf "0\n"
        exit 0
      fi
    fi
    for cf in "${dt_dir}/compose.yml" "${dt_dir}/compose.yaml" "${dt_dir}/docker-compose.yml" "${dt_dir}/docker-compose.yaml"; do
      if [[ -r "$cf" ]]; then
        if grep -qE "^[[:space:]]*(zeroconf:|container_name:[[:space:]]*docktun-zeroconf)" "$cf" 2>/dev/null; then
          printf "1\n"
        else
          printf "0\n"
        fi
        exit 0
      fi
    done
  ' _ "$DOCKTUN_DIR" 2>/dev/null || true)"
  zc_state="${zc_state//[^01]/}"
  if [[ "$zc_state" == "0" || "$zc_state" == "1" ]]; then
    printf '%s\n' "$zc_state"
  else
    printf '%s\n' "$WITH_ZEROCONF"
  fi
}

update_lxc_notes() {
  if ! command -v pct >/dev/null 2>&1 && [[ "${DRY_RUN}" != "1" ]]; then
    return 0
  fi
  local lxc_ip="" gpu_summary="" docktun_port="" zc_active="" aio_dir="${DOCKTUN_DIR}/projects/aiostreams"
  gpu_summary="$(resolve_stream_gpu_mode)"
  docktun_port="$(detect_ct_docktun_port "$CTID")"
  zc_active="$(detect_ct_zeroconf_active "$CTID")"

  local mdns_line="Disabled (--no-zeroconf)"
  if [[ "$zc_active" == "1" ]]; then
    mdns_line="http://docktun.local"
  elif [[ "${EFFECTIVE_ZEROCONF_FALLBACK:-}" == "port80" ]]; then
    mdns_line="Disabled (port 80 conflict fallback)"
  elif (( WITH_ZEROCONF )); then
    mdns_line="Disabled (not configured in compose)"
  fi

  local notes=""
  if (( TEMPLATE_HYGIENE )); then
    notes="$(cat <<EOF
## DockTun + AIOStreams Media LXC (Template)

- **Template Mode**: Stripped for cloning (SSH host keys & \`.env.generated\` regenerate on first boot via \`regenerate-ssh-host-keys.service\` & \`docktun-firstboot.service\`)
- **DockTun Dashboard**: http://<clone-ip>:${docktun_port}
- **mDNS URL**: ${mdns_line}
- **Admin User**: \`${ADMIN_USER}\`
- **GPU Mode**: \`${gpu_summary}\`
- **AIOStreams Dir**: \`${aio_dir}\`

### Useful Commands (on booted clone)
- Readiness check: \`${aio_dir}/check-readiness.sh\`
- First-run setup token: \`docker exec docktun cat /app/data/setup.token\`
EOF
)"
  else
    lxc_ip="$(resolve_ct_display_ip "$CTID")"
    local auth_line=""
    if [[ -z "$DOCKTUN_ADMIN_PASSWORD" ]]; then
      auth_line="- First-run setup token: \`pct exec ${CTID} -- docker exec docktun cat /app/data/setup.token\`"
    else
      auth_line="- Pre-seeded DockTun admin user: \`${DOCKTUN_ADMIN_USER:-admin}\`"
    fi
    notes="$(cat <<EOF
## DockTun + AIOStreams Media LXC (CT ${CTID})

- **DockTun Dashboard**: http://${lxc_ip}:${docktun_port}
- **mDNS URL**: ${mdns_line}
- **Admin User**: \`${ADMIN_USER}\`
- **GPU Mode**: \`${gpu_summary}\`
- **AIOStreams Dir**: \`${aio_dir}\`

### Useful Commands
- Readiness check: \`pct exec ${CTID} -- ${aio_dir}/check-readiness.sh\`
${auth_line}
EOF
)"
  fi
  run_cmd pct set "$CTID" --description "$notes" || \
    log_warn "Could not set Proxmox container notes (--description) for CT ${CTID}."
}

print_post_install_cheat_sheet() {
  local lxc_ip="" docktun_port="" zc_active="" setup_token=""
  lxc_ip="$(resolve_ct_display_ip "$CTID")"
  docktun_port="$(detect_ct_docktun_port "$CTID")"
  zc_active="$(detect_ct_zeroconf_active "$CTID")"
  if [[ "${DRY_RUN}" != "1" ]] && command -v pct >/dev/null 2>&1; then
    if [[ -z "$DOCKTUN_ADMIN_PASSWORD" && "$TEMPLATE_HYGIENE" != "1" ]]; then
      setup_token="$(pct exec "$CTID" -- docker exec docktun sh -c 'cat /app/data/setup.token 2>/dev/null' 2>/dev/null || true)"
    fi
  fi

  local mdns_suffix=""
  if [[ "$zc_active" == "1" ]]; then
    mdns_suffix="  (mDNS: http://docktun.local)"
  elif [[ "${EFFECTIVE_ZEROCONF_FALLBACK:-}" == "port80" ]]; then
    mdns_suffix="  (mDNS skipped: port 80 in use)"
  elif (( WITH_ZEROCONF )); then
    mdns_suffix="  (mDNS disabled in compose)"
  fi

  local aio_dir="${DOCKTUN_DIR}/projects/aiostreams"
  printf '\n%s=== Post-Install Cheat Sheet (CT %s / %s) ===%s\n' "${_CLR_BOLD}${_CLR_GREEN}" "$CTID" "$HOSTNAME" "${_CLR_RESET}"
  printf '  SSH Access        : ssh %s@%s\n' "$ADMIN_USER" "$lxc_ip"
  printf '  Proxmox Console   : pct enter %s\n' "$CTID"
  if (( TEMPLATE_HYGIENE )); then
    printf '  DockTun (Template): Stopped & stripped for cloning (auto-starts on clone first boot via docktun-firstboot.service)\n'
    printf '  Clone Dashboard   : http://<clone-ip>:%s%s\n' "$docktun_port" "$mdns_suffix"
    printf '  Clone Setup Token : Run on booted clone: docker exec docktun cat /app/data/setup.token\n'
    printf '  Security Note     : "%s" is in the docker group (root-equivalent on this LXC).\n' "$ADMIN_USER"
    printf '  AIOStreams Setup  :\n'
    printf '    1. On booted clone: .env.generated is auto-created with a fresh SECRET_KEY; run: cd %s && cp .env.generated .env\n' "$aio_dir"
    printf '    2. Deploy via DockTun UI (/opt/projects/aiostreams) or docker compose up -d\n'
    printf '    3. Verify on booted clone: %s/check-readiness.sh\n' "$aio_dir"
    printf '    4. Reverse proxy & directory guide: %s/nginx-proxy-streaming.conf.example (https://%s) | %s/README.md\n' "$aio_dir" "$AIO_DOMAIN" "$aio_dir"
    printf '  Template Convert  : pct stop %s && pct template %s\n' "$CTID" "$CTID"
  else
    printf '  DockTun Dashboard : http://%s:%s%s\n' "$lxc_ip" "$docktun_port" "$mdns_suffix"
    if [[ -n "$DOCKTUN_ADMIN_PASSWORD" ]]; then
      printf '  DockTun Auth      : pre-seeded username "%s"\n' "${DOCKTUN_ADMIN_USER:-admin}"
    elif [[ -n "$setup_token" ]]; then
      printf '  DockTun Token     : %s\n' "$setup_token"
    else
      printf '  DockTun Token     : pct exec %s -- docker exec docktun cat /app/data/setup.token\n' "$CTID"
    fi
    printf '  Security Note     : "%s" is in the docker group (root-equivalent on this LXC).\n' "$ADMIN_USER"
    printf '  AIOStreams Setup  :\n'
    printf '    1. pct exec %s -- bash -lc '\''cd %s && cp .env.generated .env'\''\n' "$CTID" "$aio_dir"
    printf '    2. Deploy via DockTun UI (/opt/projects/aiostreams) or docker compose up -d\n'
    printf '    3. Re-verify anytime: pct exec %s -- %s/check-readiness.sh\n' "$CTID" "$aio_dir"
    printf '    4. Reverse proxy & directory guide: %s/nginx-proxy-streaming.conf.example (https://%s) | %s/README.md\n' "$aio_dir" "$AIO_DOMAIN" "$aio_dir"
  fi
  printf '%s=====================================================%s\n\n' "${_CLR_BOLD}${_CLR_GREEN}" "${_CLR_RESET}"
}

create_and_provision_lxc() {
  preflight_host_checks
  check_host_storage_and_bridge
  resolve_template

  local features="keyctl=1,nesting=1"
  (( WITH_FUSE )) && features="${features},fuse=1"

  local net0="name=eth0,bridge=${BRIDGE},firewall=1,ip=${IP_ADDR},type=veth"
  if [[ "${IP_ADDR,,}" != "dhcp" && -n "$GW_ADDR" ]]; then
    net0="${net0},gw=${GW_ADDR}"
  fi
  [[ -n "$VLAN_TAG" ]] && net0="${net0},tag=${VLAN_TAG}"
  [[ -n "$MTU_BYTES" ]] && net0="${net0},mtu=${MTU_BYTES}"

  local tags="media;docker;docktun"
  if (( DO_NVIDIA || DO_INTEL || DO_AMD )); then
    tags="${tags};gpu"
  fi

  local -a create_cmd=(
    pct create "$CTID" "$LXC_TEMPLATE"
    --unprivileged 1
    --arch amd64
    --ostype "$LXC_OSTYPE"
    --hostname "$HOSTNAME"
    --cores "$CORES"
    --memory "$MEMORY"
    --swap "$SWAP"
    --rootfs "${STORAGE}:${DISK}"
    --features "$features"
    --onboot 1
    --net0 "$net0"
    --tags "$tags"
  )
  [[ -n "$STARTUP_SPEC" ]] && create_cmd+=(--startup "$STARTUP_SPEC")
  [[ -n "$DNS_ADDR" ]] && create_cmd+=(--nameserver "$DNS_ADDR")
  [[ -n "$SSH_PUBKEY_FILE" ]] && create_cmd+=(--ssh-public-keys "$SSH_PUBKEY_FILE")

  log_info "Creating unprivileged LXC ${CTID} (${HOSTNAME}, ${OS_CHOICE})..."
  run_cmd "${create_cmd[@]}"
  if [[ "${DRY_RUN}" != "1" ]]; then
    CREATED_CTID="$CTID"
    PROVISION_SUCCEEDED=0
  fi

  if (( ALLOW_UNCONFINED_APPARMOR )); then
    log_warn "Applying opt-in --allow-unconfined-apparmor (lxc.apparmor.profile: unconfined) to CT ${CTID}."
    append_lxc_conf_lines "lxc.apparmor.profile: unconfined"
  fi

  if [[ -n "$MEDIA_DIR" ]]; then
    local media_existed=0
    [[ -d "$MEDIA_DIR" ]] && media_existed=1
    if (( ! media_existed )); then
      log_warn "Host --media-dir '${MEDIA_DIR}' does not exist yet (if this is an external/NAS mount, verify it is mounted so mkdir does not write onto the Proxmox rootfs)."
    fi
    if (( ! media_existed )) || (( CHOWN_MEDIA_DIR )); then
      log_info "Preparing host media directory ${MEDIA_DIR} (owner 101000:101000 -> LXC ${ADMIN_USER} UID 1000)..."
      run_cmd mkdir -p "$MEDIA_DIR"
      if ! run_cmd chown 101000:101000 "$MEDIA_DIR"; then
        log_warn "Could not chown 101000:101000 ${MEDIA_DIR} (e.g. root_squash NFS/CIFS); continuing with existing ownership."
      fi
      if ! run_cmd chmod 0775 "$MEDIA_DIR"; then
        log_warn "Could not chmod 0775 ${MEDIA_DIR}; continuing with existing permissions."
      fi
    else
      log_info "Using existing host media directory ${MEDIA_DIR} (preserving existing ownership/permissions; pass --chown-media-dir to chown 101000:101000)."
    fi
    run_cmd pct set "$CTID" "--mp${MP_IDX}" "${MEDIA_DIR},mp=/mnt/media"
    MP_IDX=$((MP_IDX + 1))
  fi

  if (( ${#EXTRA_MOUNTS[@]} > 0 )); then
    local mnt="" host_p="" ct_p="" norm_ct=""
    for mnt in "${EXTRA_MOUNTS[@]}"; do
      host_p="${mnt%%:*}"
      ct_p="${mnt#*:}"
      norm_ct="$(normalize_mount_target "$ct_p")"
      if [[ ! -d "$host_p" ]]; then
        log_warn "Host --mount path '${host_p}' does not exist yet (if this is an external/NAS mount, verify it is mounted before creating directories)."
        run_cmd mkdir -p "$host_p"
      fi
      run_cmd pct set "$CTID" "--mp${MP_IDX}" "${host_p},mp=${norm_ct}"
      MP_IDX=$((MP_IDX + 1))
    done
  fi

  configure_tun_device

  if (( DO_NVIDIA )); then
    configure_nvidia_passthrough
  fi
  if (( WITH_WOLF_INPUT )); then
    configure_wolf_input_passthrough
  fi

  log_info "Starting LXC ${CTID}..."
  run_cmd pct start "$CTID"
  wait_lxc_systemd_ready

  POST_BOOT_DEV_ADDED=0
  configure_dri_and_kfd_after_boot 0
  configure_wolf_input_after_boot 0
  if (( POST_BOOT_DEV_ADDED )) && [[ "${DRY_RUN}" != "1" ]]; then
    log_info "Restarting LXC ${CTID} to activate Proxmox dev[n] device nodes..."
    run_cmd pct reboot "$CTID"
    wait_lxc_systemd_ready
  fi

  wait_lxc_net_ready
  stream_lxc_payload

  if [[ "${DRY_RUN}" != "1" ]]; then
    PROVISION_SUCCEEDED=1
  fi
  update_lxc_notes

  if (( TEMPLATE_HYGIENE )); then
    log_warn "Template hygiene applied. To convert CT ${CTID} into a template: pct stop ${CTID} && pct template ${CTID}"
  fi

  print_post_install_cheat_sheet
  log_success "LXC ${CTID} (${HOSTNAME}) created and provisioned successfully."
}

derive_gpu_from_pct_config() {
  local cfg=""
  cfg="$(strip_pct_snapshots "$1")"
  DO_NVIDIA=0
  DO_INTEL=0
  DO_AMD=0
  GPUS_RESOLVED=1

  if grep -qE '/dev/nvidia|nvidia-lxc-prestart' <<< "$cfg"; then
    DO_NVIDIA=1
  fi
  if grep -qE '/dev/kfd' <<< "$cfg"; then
    DO_AMD=1
  fi
  if grep -qE '/dev/dri' <<< "$cfg"; then
    local dri_node="" vendor="" found_dri_vendor=0 pci_bypath=""
    while IFS= read -r dri_node; do
      dri_node="$(basename "$dri_node")"
      if [[ -r "/sys/class/drm/${dri_node}/device/vendor" ]]; then
        vendor="$(tr -d '[:space:]' < "/sys/class/drm/${dri_node}/device/vendor" 2>/dev/null || true)"
        case "${vendor,,}" in
          0x8086) DO_INTEL=1; found_dri_vendor=1 ;;
          0x1002) DO_AMD=1; found_dri_vendor=1 ;;
          0x10de) found_dri_vendor=1 ;;
        esac
      fi
    done < <(grep -oE '/dev/dri/(renderD[0-9]+|card[0-9]+)' <<< "$cfg" || true)
    while IFS= read -r pci_bypath; do
      [[ "$pci_bypath" =~ pci-([0-9a-fA-F:.]+)-(render|card) ]] || continue
      local slot="${BASH_REMATCH[1],,}"
      if [[ -r "/sys/bus/pci/devices/${slot}/vendor" ]]; then
        vendor="$(tr -d '[:space:]' < "/sys/bus/pci/devices/${slot}/vendor" 2>/dev/null || true)"
        case "${vendor,,}" in
          0x8086) DO_INTEL=1; found_dri_vendor=1 ;;
          0x1002) DO_AMD=1; found_dri_vendor=1 ;;
          0x10de) found_dri_vendor=1 ;;
        esac
      fi
    done < <(grep -oE 'pci-[0-9a-fA-F:.]+-(render|card)' <<< "$cfg" || true)
    if (( ! found_dri_vendor && ! DO_NVIDIA )); then
      DO_INTEL="$DETECTED_INTEL"
      DO_AMD="$DETECTED_AMD"
      if (( ! DO_INTEL && ! DO_AMD )); then
        DO_INTEL=1
        DO_AMD=1
      fi
    fi
  fi
}

run_provision_only() {
  log_info "Running --provision-only for existing LXC ${CTID}..."
  if [[ "${DRY_RUN}" != "1" ]]; then
    if ! command -v pct >/dev/null 2>&1; then
      die "'pct' command not found; --provision-only must be run on a Proxmox VE host."
    fi
    local status_out=""
    status_out="$(pct status "$CTID" 2>/dev/null || true)"
    [[ -n "$status_out" ]] || die "CTID ${CTID} does not exist in Proxmox."
    local cfg=""
    cfg="$(strip_pct_snapshots "$(pct config "$CTID" 2>/dev/null || true)")"
    if ! grep -qE '^unprivileged:[[:space:]]*1$' <<< "$cfg"; then
      die "CTID ${CTID} is not an unprivileged LXC (unprivileged: 1 required); refusing to provision."
    fi
    sync_pct_indices_from_config "$cfg"
    if [[ "$GPU_MODE" == "auto" ]]; then
      derive_gpu_from_pct_config "$cfg"
      log_info "Derived GPU mode from CT ${CTID} config: $(resolve_stream_gpu_mode)"
    fi
    if (( DO_NVIDIA )) && [[ ! -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
      die "NVIDIA host kernel driver is not active (/proc/driver/nvidia/version is unreadable); reboot the Proxmox host before provisioning an NVIDIA LXC."
    fi
    local cfg_hostname=""
    cfg_hostname="$(awk -F': ' '/^hostname:/ {print $2; exit}' <<< "$cfg" || true)"
    [[ -n "$cfg_hostname" ]] && HOSTNAME="$cfg_hostname"
    if [[ "$status_out" != *"running"* ]]; then
      log_info "Starting LXC ${CTID}..."
      run_cmd pct start "$CTID"
      wait_lxc_systemd_ready
    fi
  else
    log_info "[DRY-RUN] Would verify CTID ${CTID} exists and is unprivileged, derive GPU mode from pct config, ensure running, and stream payload"
  fi

  wait_lxc_net_ready
  stream_lxc_payload
  update_lxc_notes
  print_post_install_cheat_sheet
  log_success "LXC ${CTID} re-provisioned successfully."
}

run_doctor_mode() {
  log_info "Running --doctor health inspection on LXC ${CTID}..."
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would inspect 'pct config ${CTID}' (unprivileged, keyctl, nesting, TUN, GPU), check inner /dev/net/tun & GPU/input permissions, run ${DOCKTUN_DIR}/projects/aiostreams/check-readiness.sh, and detect actual DockTun port"
    return 0
  fi

  command -v pct >/dev/null 2>&1 || die "'pct' command not found; --doctor must be run on a Proxmox VE host."
  local status_out="" cfg=""
  status_out="$(pct status "$CTID" 2>/dev/null || true)"
  [[ -n "$status_out" ]] || die "CTID ${CTID} does not exist in Proxmox."
  cfg="$(strip_pct_snapshots "$(pct config "$CTID" 2>/dev/null || true)")"

  local doc_fail=0
  if grep -qE '^unprivileged:[[:space:]]*1$' <<< "$cfg"; then
    log_success "CT ${CTID} is unprivileged (unprivileged: 1)"
  else
    log_error "CT ${CTID} is NOT unprivileged!"
    doc_fail=1
  fi
  if grep -qE '^features:.*nesting=1' <<< "$cfg" && grep -qE '^features:.*keyctl=1' <<< "$cfg"; then
    log_success "CT ${CTID} features include nesting=1 and keyctl=1"
  else
    log_warn "CT ${CTID} features may be missing nesting=1 or keyctl=1"
  fi
  if grep -qE '/dev/net/tun' <<< "$cfg"; then
    log_success "CT ${CTID} configures /dev/net/tun"
  else
    log_warn "CT ${CTID} config has no /dev/net/tun entry"
  fi
  if grep -qE '(/dev/dri|/dev/nvidia|/dev/kfd)' <<< "$cfg"; then
    log_success "CT ${CTID} config includes GPU device entries"
  else
    log_info "CT ${CTID} config has no GPU device entries"
  fi

  if [[ "$status_out" != *"running"* ]]; then
    log_warn "CT ${CTID} is not running (${status_out}); skipping live inner checks."
    (( doc_fail == 0 )) || exit 1
    return 0
  fi

  if pct exec "$CTID" -- test -c /dev/net/tun 2>/dev/null; then
    log_success "Inner /dev/net/tun character device is present"
  else
    log_warn "Inner /dev/net/tun character device is missing or inaccessible"
  fi

  local dev_perm_out=""
  dev_perm_out="$(pct exec "$CTID" -- bash -c '
    for p in /dev/dri/renderD* /dev/dri/card* /dev/nvidia* /dev/nvidia-caps/nvidia-cap* /dev/kfd /dev/uinput /dev/uhid /dev/input/event* /dev/input/js*; do
      [[ -e "$p" && ! -d "$p" ]] || continue
      stat -c "%n mode=%a owner=%u:%g (%U:%G)" "$p" 2>/dev/null || true
    done
  ' 2>/dev/null || true)"
  if [[ -n "$dev_perm_out" ]]; then
    if grep -qE 'mode=[0-7]*[0-5][[:space:]]+(owner=(65534:[0-9]+|[0-9]+:65534)|\(nobody:nogroup\))' <<< "$dev_perm_out"; then
      log_warn "Some inner GPU/input device nodes appear as nobody:nogroup (65534:65534) or unmapped (65534) without world-rw (0666):"
      while IFS= read -r dline; do
        if grep -qE 'mode=[0-7]*[0-5][[:space:]]+(owner=(65534:[0-9]+|[0-9]+:65534)|\(nobody:nogroup\))' <<< "$dline"; then
          log_warn "  ${dline}"
        fi
      done <<< "$dev_perm_out"
    elif grep -qE 'owner=(65534:[0-9]+|[0-9]+:65534)|\(nobody:nogroup\)' <<< "$dev_perm_out"; then
      log_success "Inner GPU/input device node ownership OK (unmapped nodes have world-rw 0666/0777 access)"
    else
      log_success "Inner GPU/input device node ownership OK (no nobody:nogroup mapping errors)"
    fi
  fi

  local readiness_script="${DOCKTUN_DIR}/projects/aiostreams/check-readiness.sh"
  if pct exec "$CTID" -- test -x "$readiness_script" 2>/dev/null; then
    pct exec "$CTID" -- "$readiness_script" || doc_fail=1
  else
    log_warn "${readiness_script} not found inside CT ${CTID}; running basic checks..."
    local ufw_out=""
    ufw_out="$(pct exec "$CTID" -- ufw status 2>/dev/null || true)"
    if grep -qi 'Status:[[:space:]]*active' <<< "$ufw_out"; then
      log_success "UFW firewall is active inside CT ${CTID}"
    else
      log_warn "UFW firewall is not active inside CT ${CTID}"
      doc_fail=1
    fi
    pct exec "$CTID" -- iptables -S DOCKER-USER || doc_fail=1
    if pct exec "$CTID" -- grep -q '^# BEGIN DOCKTUN DOCKER-USER$' /etc/ufw/after6.rules 2>/dev/null && \
       pct exec "$CTID" -- ip6tables -L -n >/dev/null 2>&1; then
      pct exec "$CTID" -- ip6tables -S DOCKER-USER || doc_fail=1
    fi
    pct exec "$CTID" -- docker exec docktun curl -fsS http://127.0.0.1:3001/healthz >/dev/null || doc_fail=1
  fi

  local lxc_ip="" actual_port="" zc_active=""
  lxc_ip="$(detect_ct_eth0_ip "$CTID")"
  actual_port="$(detect_ct_docktun_port "$CTID")"
  zc_active="$(detect_ct_zeroconf_active "$CTID")"
  if [[ -n "$lxc_ip" ]]; then
    log_info "Reachable DockTun URL: http://${lxc_ip}:${actual_port}"
  fi
  if [[ "$zc_active" == "1" ]]; then
    log_info "DockTun ZeroConf mDNS: active (http://docktun.local)"
  else
    log_info "DockTun ZeroConf mDNS: inactive"
  fi
  (( doc_fail == 0 )) || exit 1
  log_success "Doctor check completed cleanly for CT ${CTID}."
}

run_list_mode() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would list docktun-tagged Proxmox LXCs via 'pct list' and 'pct config'"
    return 0
  fi
  command -v pct >/dev/null 2>&1 || die "'pct' command not found; --list must be run on a Proxmox VE host."

  local list_out=""
  list_out="$(pct list 2>/dev/null || true)"
  if [[ -z "$list_out" ]]; then
    log_info "No Proxmox LXCs found."
    return 0
  fi

  printf '%-8s %-10s %-20s %-18s %s\n' "CTID" "STATUS" "NAME" "IP" "TAGS"
  local vmid="" status="" name="" cfg="" tags="" ip="" found=0
  while read -r vmid status _; do
    [[ "$vmid" =~ ^[0-9]+$ ]] || continue
    cfg="$(strip_pct_snapshots "$(pct config "$vmid" 2>/dev/null || true)")"
    tags="$(awk -F': ' '/^tags:/ {print $2; exit}' <<< "$cfg" || true)"
    if ! grep -qE '(^|[;,[:space:]])docktun([;,[:space:]]|$)' <<< "$tags"; then
      continue
    fi
    found=1
    name="$(awk -F': ' '/^hostname:/ {print $2; exit}' <<< "$cfg" || true)"
    if [[ "$status" == "running" ]]; then
      ip="$(detect_ct_eth0_ip "$vmid")"
    else
      ip=""
    fi
    if [[ -z "$ip" ]]; then
      ip="$(sed -nE 's/^net0:.*ip=([^,]+).*/\1/p' <<< "$cfg" | head -n1 || true)"
    fi
    printf '%-8s %-10s %-20s %-18s %s\n' "$vmid" "$status" "${name:-unknown}" "${ip:-dhcp}" "$tags"
  done < <(awk 'NR>1 {print $1, $2}' <<< "$list_out")

  if (( ! found )); then
    log_info "No docktun-tagged LXCs found."
  fi
  return 0
}

run_destroy_mode() {
  log_info "Destroying LXC ${CTID}..."
  if [[ "${DRY_RUN}" == "1" ]]; then
    run_cmd pct stop "$CTID"
    run_cmd pct unlock "$CTID"
    run_cmd pct destroy "$CTID" --purge
    run_cmd rm -f "/usr/local/libexec/nvidia-lxc-prestart-${CTID}.sh"
    return 0
  fi

  command -v pct >/dev/null 2>&1 || die "'pct' command not found; --destroy must be run on a Proxmox VE host."
  local status_out=""
  status_out="$(pct status "$CTID" 2>/dev/null || true)"
  [[ -n "$status_out" ]] || die "CTID ${CTID} does not exist in Proxmox."

  if [[ "${NON_INTERACTIVE:-0}" != "1" ]]; then
    if ! has_controlling_tty; then
      die "No controlling TTY available for --destroy confirmation; pass -y/--yes."
    fi
    if ! prompt_yn "Stop and permanently purge LXC ${CTID} and all its data?" "n"; then
      die "Aborted by user."
    fi
  fi

  if ! destroy_ct_with_cleanup "$CTID"; then
    die "Failed to destroy LXC ${CTID}."
  fi
  log_success "Destroyed LXC ${CTID} and removed per-CTID NVIDIA prestart hook (if present)."
}

main() {
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  parse_args "$@"

  if [[ "${DRY_RUN}" != "1" ]]; then
    require_root
  elif [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    log_warn "Running in --dry-run mode as non-root user."
  fi

  validate_early_cli_inputs

  if (( LIST_MODE )); then
    run_list_mode
    return 0
  fi

  if (( DESTROY_MODE )); then
    validate_inputs
    run_destroy_mode
    return 0
  fi

  if (( DOCTOR_MODE )); then
    validate_inputs
    run_doctor_mode
    return 0
  fi

  auto_detect_defaults
  run_interactive_wizard
  validate_inputs

  if (( RUN_HOST_PREP )); then
    run_host_preparation
  else
    log_info "Skipping Proxmox host preparation (already configured or --skip-host-prep)."
  fi

  if (( HOST_PREP_ONLY )); then
    log_success "Host preparation complete (--host-prep-only)."
    return 0
  fi

  if (( PROVISION_ONLY )); then
    run_provision_only
    return 0
  fi

  if (( DO_NVIDIA )) && [[ "${DRY_RUN}" != "1" && ! -r "${HOST_NVIDIA_PROC_VERSION_FILE:-/proc/driver/nvidia/version}" ]]; then
    log_error "NVIDIA host kernel driver is not active (/proc/driver/nvidia/version is unreadable)."
    log_error "Aborting BEFORE LXC creation/provisioning to avoid leaving an incomplete container."
    log_error "Reboot the Proxmox host ('systemctl reboot') to load the NVIDIA module (and unload nouveau), then re-run this script."
    exit 1
  fi

  create_and_provision_lxc
}

main "$@"
