#!/usr/bin/env bash
# Single-file Proxmox VE 8/9 host prep & hardened unprivileged LXC provisioner
# with shared GPU passthrough (NVIDIA/Intel/AMD), Docker CE + ZFS guard,
# pre-installed DockTun, and AIOStreams readiness.
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"
NON_INTERACTIVE="${NON_INTERACTIVE:-0}"
SKIP_HOST_PREP="${SKIP_HOST_PREP:-0}"
HOST_PREP_ONLY="${HOST_PREP_ONLY:-0}"

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
IP_ADDR="dhcp"
GW_ADDR=""
DNS_ADDR="1.1.1.1"
SSH_PUBKEY_FILE=""
SSH_PUBKEY=""
ADMIN_USER="mediaadmin"
GPU_MODE="auto"
PCI_SLOT=""
DOCKTUN_PORT="8080"
DOCKTUN_PORT_EXPLICIT=0
DOCKTUN_DIR="/opt/docktun"
DOCKTUN_ADMIN_USER=""
DOCKTUN_ADMIN_PASSWORD=""
LAN_SUBNETS="10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
MEDIA_DIR=""
WITH_FUSE=0
WITH_WOLF_INPUT=0
WITH_ZEROCONF=1
ALLOW_UNCONFINED_APPARMOR=0
TEMPLATE_HYGIENE=0
AIO_DOMAIN="aio.example.com"

# Runtime state
DEV_IDX=0
MP_IDX=0
TMP_WORK_DIR=""
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

cleanup() {
  if [[ -n "${TMP_WORK_DIR:-}" && -d "${TMP_WORK_DIR}" ]]; then
    rm -rf "${TMP_WORK_DIR}"
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

is_port_in_use() {
  local port="${1:?is_port_in_use requires a port number}"
  if [[ ! "$port" =~ ^[0-9]+$ ]]; then
    return 2
  fi
  if command -v ss >/dev/null 2>&1; then
    ss -H -tuln 2>/dev/null | grep -qE ":${port}([[:space:]]|$)"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -tuln 2>/dev/null | grep -qE ":${port}([[:space:]]|$)"
  else
    return 1
  fi
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
      --ip <cidr|dhcp>            IPv4 CIDR (e.g. 192.168.1.50/24) or dhcp (default: dhcp)
      --gw <ip>                   IPv4 gateway (used with static --ip)
      --dns <ip>                  DNS nameserver IP (default: 1.1.1.1)
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
                                  Optional pre-seeded DockTun admin password
      --lan-subnets <cidrs>       Comma-separated trusted LAN CIDRs for UFW/DOCKER-USER
                                  (default: 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16)
      --media-dir <host_path>     Optional host media path mounted at /mnt/media
      --with-fuse                 Enable fuse=1 feature on the LXC
      --with-wolf-input           Configure uinput/uhid & input passthrough for Wolf/Purah
      --with-zeroconf             Enable docktun-zeroconf mDNS (*.local, port 80; default: on)
      --no-zeroconf, --without-zeroconf  Disable docktun-zeroconf mDNS (leave host port 80 free)
      --allow-unconfined-apparmor Opt-in escape hatch to set lxc.apparmor.profile: unconfined
      --template-hygiene          Clear SSH host keys & machine-id after provisioning
      --domain <hostname>         Domain placeholder for AIOStreams BASE_URL/Nginx
                                  (default: aio.example.com)
  -h, --help                      Show this help message and exit
EOF
}

parse_args() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      -y|--yes|--non-interactive) NON_INTERACTIVE=1; shift ;;
      --dry-run) DRY_RUN=1; shift ;;
      --skip-host-prep) SKIP_HOST_PREP=1; shift ;;
      --host-prep-only) HOST_PREP_ONLY=1; shift ;;
      --ctid) [[ "$#" -ge 2 ]] || die "--ctid requires a value"; CTID="$2"; shift 2 ;;
      --ctid=*) CTID="${1#*=}"; shift ;;
      --hostname) [[ "$#" -ge 2 ]] || die "--hostname requires a value"; HOSTNAME="$2"; shift 2 ;;
      --hostname=*) HOSTNAME="${1#*=}"; shift ;;
      --os) [[ "$#" -ge 2 ]] || die "--os requires a value"; OS_CHOICE="${2,,}"; shift 2 ;;
      --os=*) OS_CHOICE="${1#*=}"; OS_CHOICE="${OS_CHOICE,,}"; shift ;;
      --storage) [[ "$#" -ge 2 ]] || die "--storage requires a value"; STORAGE="$2"; shift 2 ;;
      --storage=*) STORAGE="${1#*=}"; shift ;;
      --template-storage) [[ "$#" -ge 2 ]] || die "--template-storage requires a value"; TEMPLATE_STORAGE="$2"; shift 2 ;;
      --template-storage=*) TEMPLATE_STORAGE="${1#*=}"; shift ;;
      --cores) [[ "$#" -ge 2 ]] || die "--cores requires a value"; CORES="$2"; shift 2 ;;
      --cores=*) CORES="${1#*=}"; shift ;;
      --memory) [[ "$#" -ge 2 ]] || die "--memory requires a value"; MEMORY="$2"; shift 2 ;;
      --memory=*) MEMORY="${1#*=}"; shift ;;
      --swap) [[ "$#" -ge 2 ]] || die "--swap requires a value"; SWAP="$2"; shift 2 ;;
      --swap=*) SWAP="${1#*=}"; shift ;;
      --disk) [[ "$#" -ge 2 ]] || die "--disk requires a value"; DISK="$2"; shift 2 ;;
      --disk=*) DISK="${1#*=}"; shift ;;
      --bridge) [[ "$#" -ge 2 ]] || die "--bridge requires a value"; BRIDGE="$2"; shift 2 ;;
      --bridge=*) BRIDGE="${1#*=}"; shift ;;
      --ip) [[ "$#" -ge 2 ]] || die "--ip requires a value"; IP_ADDR="$2"; shift 2 ;;
      --ip=*) IP_ADDR="${1#*=}"; shift ;;
      --gw) [[ "$#" -ge 2 ]] || die "--gw requires a value"; GW_ADDR="$2"; shift 2 ;;
      --gw=*) GW_ADDR="${1#*=}"; shift ;;
      --dns) [[ "$#" -ge 2 ]] || die "--dns requires a value"; DNS_ADDR="$2"; shift 2 ;;
      --dns=*) DNS_ADDR="${1#*=}"; shift ;;
      --ssh-pubkey-file) [[ "$#" -ge 2 ]] || die "--ssh-pubkey-file requires a value"; SSH_PUBKEY_FILE="$2"; shift 2 ;;
      --ssh-pubkey-file=*) SSH_PUBKEY_FILE="${1#*=}"; shift ;;
      --ssh-pubkey) [[ "$#" -ge 2 ]] || die "--ssh-pubkey requires a value"; SSH_PUBKEY="$2"; shift 2 ;;
      --ssh-pubkey=*) SSH_PUBKEY="${1#*=}"; shift ;;
      --admin-user) [[ "$#" -ge 2 ]] || die "--admin-user requires a value"; ADMIN_USER="$2"; shift 2 ;;
      --admin-user=*) ADMIN_USER="${1#*=}"; shift ;;
      --gpu) [[ "$#" -ge 2 ]] || die "--gpu requires a value"; GPU_MODE="${2,,}"; shift 2 ;;
      --gpu=*) GPU_MODE="${1#*=}"; GPU_MODE="${GPU_MODE,,}"; shift ;;
      --pci-slot) [[ "$#" -ge 2 ]] || die "--pci-slot requires a value"; PCI_SLOT="$2"; shift 2 ;;
      --pci-slot=*) PCI_SLOT="${1#*=}"; shift ;;
      --docktun-port) [[ "$#" -ge 2 ]] || die "--docktun-port requires a value"; DOCKTUN_PORT="$2"; DOCKTUN_PORT_EXPLICIT=1; shift 2 ;;
      --docktun-port=*) DOCKTUN_PORT="${1#*=}"; DOCKTUN_PORT_EXPLICIT=1; shift ;;
      --docktun-dir) [[ "$#" -ge 2 ]] || die "--docktun-dir requires a value"; DOCKTUN_DIR="$2"; shift 2 ;;
      --docktun-dir=*) DOCKTUN_DIR="${1#*=}"; shift ;;
      --docktun-admin-user) [[ "$#" -ge 2 ]] || die "--docktun-admin-user requires a value"; DOCKTUN_ADMIN_USER="$2"; shift 2 ;;
      --docktun-admin-user=*) DOCKTUN_ADMIN_USER="${1#*=}"; shift ;;
      --docktun-admin-password) [[ "$#" -ge 2 ]] || die "--docktun-admin-password requires a value"; DOCKTUN_ADMIN_PASSWORD="$2"; shift 2 ;;
      --docktun-admin-password=*) DOCKTUN_ADMIN_PASSWORD="${1#*=}"; shift ;;
      --lan-subnets) [[ "$#" -ge 2 ]] || die "--lan-subnets requires a value"; LAN_SUBNETS="$2"; shift 2 ;;
      --lan-subnets=*) LAN_SUBNETS="${1#*=}"; shift ;;
      --media-dir) [[ "$#" -ge 2 ]] || die "--media-dir requires a value"; MEDIA_DIR="$2"; shift 2 ;;
      --media-dir=*) MEDIA_DIR="${1#*=}"; shift ;;
      --with-fuse) WITH_FUSE=1; shift ;;
      --with-wolf-input) WITH_WOLF_INPUT=1; shift ;;
      --with-zeroconf) WITH_ZEROCONF=1; shift ;;
      --no-zeroconf|--without-zeroconf) WITH_ZEROCONF=0; shift ;;
      --allow-unconfined-apparmor) ALLOW_UNCONFINED_APPARMOR=1; shift ;;
      --template-hygiene) TEMPLATE_HYGIENE=1; shift ;;
      --domain) [[ "$#" -ge 2 ]] || die "--domain requires a value"; AIO_DOMAIN="$2"; shift 2 ;;
      --domain=*) AIO_DOMAIN="${1#*=}"; shift ;;
      -h|--help) usage; exit 0 ;;
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
    found="$(pvesm status -content "$content" 2>/dev/null | awk 'NR>1 && $2 != "inactive" {print $1; exit}' || true)"
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
    if [[ "$br" =~ ^vmbr[0-9]+$ ]]; then
      printf '%s\n' "$br"
      return 0
    fi
  fi
  shopt -s nullglob
  local net_dir=""
  for net_dir in /sys/class/net/vmbr*; do
    [[ -d "$net_dir" ]] || continue
    printf '%s\n' "$(basename "$net_dir")"
    shopt -u nullglob
    return 0
  done
  shopt -u nullglob
  printf 'vmbr0\n'
}

detect_default_ssh_pubkey_file() {
  local candidate=""
  for candidate in /root/.ssh/id_ed25519.pub /root/.ssh/id_rsa.pub /root/.ssh/authorized_keys; do
    if [[ -r "$candidate" && -s "$candidate" ]] && grep -qE '^[[:space:]]*(ssh-|ecdsa-|sk-)' "$candidate"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  shopt -s nullglob
  for candidate in /root/.ssh/*.pub; do
    if [[ -r "$candidate" && -s "$candidate" ]] && grep -qE '^[[:space:]]*(ssh-|ecdsa-|sk-)' "$candidate"; then
      printf '%s\n' "$candidate"
      shopt -u nullglob
      return 0
    fi
  done
  shopt -u nullglob
  printf '\n'
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
  all_gpu_lines="$(lspci -Dnn 2>/dev/null | grep -iE 'VGA|3D|Display' || true)"
  if [[ -n "$all_gpu_lines" ]]; then
    DETECTED_GPU_COUNT="$(wc -l <<< "$all_gpu_lines" | tr -d '[:space:]')"
  fi

  if [[ -n "$PCI_SLOT" ]]; then
    gpu_lines="$(lspci -Dnn -s "$PCI_SLOT" 2>/dev/null || true)"
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
    local kdrv=""
    kdrv="$(lspci -k 2>/dev/null | grep -A3 -E '(VGA|3D|Display).*Intel' | awk -F': ' '/Kernel driver in use:/ {print $2; exit}' || true)"
    DETECTED_INTEL_DRIVER="${kdrv//[[:space:]]/}"
  fi
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
}

detect_host_prep_status() {
  HOST_PREP_NEEDED=0
  if [[ ! -f /etc/modules-load.d/media-lxc-net.conf || ! -f /etc/sysctl.d/99-media-lxc.conf ]]; then
    HOST_PREP_NEEDED=1
  fi
  if (( DO_NVIDIA )) && [[ ! -f /etc/systemd/system/nvidia-lxc-prestart.service || ! -r /proc/driver/nvidia/version ]]; then
    HOST_PREP_NEEDED=1
  fi
  if (( WITH_WOLF_INPUT )) && [[ ! -f /etc/udev/rules.d/85-wolf-virtual-inputs.rules ]]; then
    HOST_PREP_NEEDED=1
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
  if [[ -z "$SSH_PUBKEY_FILE" && -z "$SSH_PUBKEY" ]]; then
    SSH_PUBKEY_FILE="$(detect_default_ssh_pubkey_file)"
  fi
  if [[ "$DOCKTUN_PORT_EXPLICIT" != "1" && "$DOCKTUN_PORT" == "8080" ]] && is_port_in_use 8080; then
    DOCKTUN_PORT="8081"
    log_info "Host port 8080 is in use; auto-selected DockTun dashboard port ${DOCKTUN_PORT}."
  fi
  detect_host_prep_status

  if [[ "$SKIP_HOST_PREP" == "1" ]]; then
    RUN_HOST_PREP=0
  elif [[ "$HOST_PREP_ONLY" == "1" ]]; then
    RUN_HOST_PREP=1
  elif [[ "$HOST_PREP_NEEDED" == "1" ]]; then
    RUN_HOST_PREP=1
  else
    RUN_HOST_PREP=0
  fi
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
    read -r -s -p "$display_prompt" input </dev/tty
    printf '\n' >&2
  else
    read -r -p "$display_prompt" input </dev/tty
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
    read -r -p "${prompt_text} ${hint}: " ans </dev/tty
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
    printf '  LXC Container : CTID=%s, Hostname=%s, OS=%s\n' "$CTID" "$HOSTNAME" "$OS_CHOICE"
    printf '  Compute/Disk  : %s cores, %s MiB RAM, %s MiB swap, %s GiB (%s / tpl:%s)\n' \
      "$CORES" "$MEMORY" "$SWAP" "$DISK" "$STORAGE" "$TEMPLATE_STORAGE"
    printf '  Network       : bridge=%s, ip=%s%s, dns=%s\n' \
      "$BRIDGE" "$IP_ADDR" "${GW_ADDR:+ gw=${GW_ADDR}}" "$DNS_ADDR"
    printf '  Security      : admin=%s, ssh=%s, AppArmor=%s\n' \
      "$ADMIN_USER" "$ssh_desc" "$( (( ALLOW_UNCONFINED_APPARMOR )) && echo 'unconfined (opt-in)' || echo 'confined (default)' )"
    printf '  GPU & Extras  : mode=%s (%s)%s, wolf=%s, fuse=%s\n' \
      "$GPU_MODE" "$gpu_desc" "${PCI_SLOT:+ slot=${PCI_SLOT}}" "$wolf_desc" "$WITH_FUSE"
    printf '  DockTun / AIO : port=%s, mDNS=%s, dir=%s, domain=%s\n' \
      "$DOCKTUN_PORT" "$mdns_desc" "$DOCKTUN_DIR" "$AIO_DOMAIN"
  fi
  printf '%s===========================================%s\n\n' "${_CLR_BOLD}${_CLR_CYAN}" "${_CLR_RESET}"
}

run_interactive_wizard() {
  if [[ "${NON_INTERACTIVE:-0}" == "1" ]]; then
    return 0
  fi

  if ! has_controlling_tty; then
    die "No controlling TTY available; pass --non-interactive (-y|--yes) with CLI flags."
  fi

  print_summary_banner

  local choice=""
  while true; do
    read -r -p "Proceed with these defaults? [Y]es / [c]ustomize / [n]o [Y]: " choice </dev/tty
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
          fi
          prompt_val GPU_MODE "Shared GPU mode (auto|nvidia|intel|amd|all|none)" "$GPU_MODE"
          GPU_MODE="${GPU_MODE,,}"
          if [[ "$GPU_MODE" != "none" ]] && (( DETECTED_GPU_COUNT > 1 )); then
            prompt_val PCI_SLOT "PCI slot (blank for auto/all)" "$PCI_SLOT"
          fi
          prompt_val DOCKTUN_PORT "DockTun dashboard port" "$DOCKTUN_PORT"
          local wolf_default="n"
          (( WITH_WOLF_INPUT )) && wolf_default="y"
          if prompt_yn "Enable Wolf game-streaming input passthrough?" "$wolf_default"; then
            WITH_WOLF_INPUT=1
          else
            WITH_WOLF_INPUT=0
          fi
          resolve_effective_gpus
          detect_host_prep_status
          if [[ "$SKIP_HOST_PREP" == "1" ]]; then
            RUN_HOST_PREP=0
          elif [[ "$HOST_PREP_ONLY" == "1" || "$HOST_PREP_NEEDED" == "1" ]]; then
            RUN_HOST_PREP=1
          else
            RUN_HOST_PREP=0
          fi
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

validate_inputs() {
  case "$GPU_MODE" in
    auto|nvidia|intel|amd|all|none) ;;
    *) die "Invalid --gpu '${GPU_MODE}'. Expected: auto|nvidia|intel|amd|all|none" ;;
  esac

  if [[ -n "$PCI_SLOT" ]]; then
    PCI_SLOT="$(normalize_pci_slot "$PCI_SLOT")"
  fi

  if [[ "$HOST_PREP_ONLY" == "1" ]]; then
    resolve_effective_gpus
    return 0
  fi

  [[ "$CTID" =~ ^[0-9]+$ ]] && (( CTID >= 100 )) || die "Invalid CTID '${CTID}'; must be an integer >= 100."
  [[ "$CORES" =~ ^[0-9]+$ ]] && (( CORES >= 1 )) || die "Invalid --cores '${CORES}'"
  [[ "$MEMORY" =~ ^[0-9]+$ ]] && (( MEMORY >= 512 )) || die "Invalid --memory '${MEMORY}' (min 512)"
  [[ "$SWAP" =~ ^[0-9]+$ ]] || die "Invalid --swap '${SWAP}'"
  [[ "$DISK" =~ ^[0-9]+$ ]] && (( DISK >= 4 )) || die "Invalid --disk '${DISK}' (min 4 GiB)"
  [[ "$DOCKTUN_PORT" =~ ^[0-9]+$ ]] && (( DOCKTUN_PORT >= 1 && DOCKTUN_PORT <= 65535 )) || die "Invalid --docktun-port '${DOCKTUN_PORT}'"
  [[ -n "$HOSTNAME" ]] || die "--hostname cannot be empty"
  [[ -n "$STORAGE" ]] || die "--storage cannot be empty"
  [[ -n "$TEMPLATE_STORAGE" ]] || die "--template-storage cannot be empty"
  [[ -n "$BRIDGE" ]] || die "--bridge cannot be empty"
  [[ -n "$DOCKTUN_DIR" ]] || die "--docktun-dir cannot be empty"
  [[ "$ADMIN_USER" =~ ^[a-z_][a-z0-9_-]*$ && "$ADMIN_USER" != "root" ]] || die "Invalid --admin-user '${ADMIN_USER}' (must be non-root Linux username)"

  case "$OS_CHOICE" in
    debian12|debian13|ubuntu2404) ;;
    *) die "Unsupported --os '${OS_CHOICE}'. Expected: debian12|debian13|ubuntu2404" ;;
  esac

  if [[ "${IP_ADDR,,}" != "dhcp" ]]; then
    if [[ ! "$IP_ADDR" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$ ]]; then
      die "Invalid --ip '${IP_ADDR}'; expected IPv4 CIDR (e.g. 192.168.1.50/24) or 'dhcp'."
    fi
    if [[ -z "$GW_ADDR" ]]; then
      log_warn "Static IP '${IP_ADDR}' specified without --gw; container may lack a default route."
    fi
  else
    IP_ADDR="dhcp"
  fi

  if [[ -n "$SSH_PUBKEY" ]]; then
    SSH_PUBKEY="$(grep -vE '^[[:space:]]*(#|$)' <<< "$SSH_PUBKEY" | head -n1 || true)"
    [[ -n "$SSH_PUBKEY" ]] || die "--ssh-pubkey did not contain a valid key line"
    if [[ -z "$SSH_PUBKEY_FILE" ]]; then
      ensure_tmp_dir
      SSH_PUBKEY_FILE="${TMP_WORK_DIR}/authorized_key.pub"
      printf '%s\n' "$SSH_PUBKEY" > "$SSH_PUBKEY_FILE"
      chmod 0644 "$SSH_PUBKEY_FILE"
    fi
  elif [[ -n "$SSH_PUBKEY_FILE" ]]; then
    [[ -r "$SSH_PUBKEY_FILE" && -s "$SSH_PUBKEY_FILE" ]] || die "SSH public key file not readable or empty: ${SSH_PUBKEY_FILE}"
    SSH_PUBKEY="$(grep -vE '^[[:space:]]*(#|$)' "$SSH_PUBKEY_FILE" | head -n1 || true)"
    [[ -n "$SSH_PUBKEY" ]] || die "No valid public key found in ${SSH_PUBKEY_FILE}"
  else
    log_warn "No SSH public key configured; container SSH will retain PasswordAuthentication yes to prevent lockout."
  fi

  if [[ -f "/etc/pve/lxc/${CTID}.conf" ]]; then
    die "CTID ${CTID} already exists at /etc/pve/lxc/${CTID}.conf"
  fi
  if command -v pct >/dev/null 2>&1 && pct status "$CTID" >/dev/null 2>&1; then
    die "CTID ${CTID} already exists in Proxmox."
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

  local dir=""
  dir="$(dirname "$target")"
  mkdir -p "$dir"
  printf '%s' "$content" > "${target}.tmp"
  chmod "$mode" "${target}.tmp"
  mv "${target}.tmp" "$target"
}

backup_once() {
  local file="$1"
  if [[ -f "$file" && ! -f "${file}.bak" ]]; then
    run_cmd cp -a "$file" "${file}.bak"
  fi
}

enable_nonfree_in_list_file() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  local updated="" rc=0
  updated="$(awk '
    BEGIN { changed = 0 }
    /^[[:space:]]*(deb|deb-src)[[:space:]]+/ {
      if ($0 ~ /(deb|security|ftp)\.debian\.org/) {
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
    function flush_stanza(    i, has_debian, enabled_ok, has_nf, has_nff) {
      if (n == 0) return
      has_debian = 0
      enabled_ok = 1
      for (i = 1; i <= n; i++) {
        if (lines[i] ~ /^URIs:[[:space:]]*.*(deb|security|ftp)\.debian\.org/) {
          has_debian = 1
        }
        if (lines[i] ~ /^Enabled:[[:space:]]*[Nn][Oo][[:space:]]*$/) {
          enabled_ok = 0
        }
      }
      for (i = 1; i <= n; i++) {
        if (has_debian && enabled_ok && lines[i] ~ /^Components:/) {
          has_nf = (lines[i] ~ /(^|[[:space:]])non-free([[:space:]]|$)/)
          has_nff = (lines[i] ~ /(^|[[:space:]])non-free-firmware([[:space:]]|$)/)
          if (!has_nf) {
            lines[i] = lines[i] " non-free"
            changed = 1
          }
          if (!has_nff) {
            lines[i] = lines[i] " non-free-firmware"
            changed = 1
          }
        }
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
  local f=""
  if [[ -f /etc/apt/sources.list ]]; then
    enable_nonfree_in_list_file /etc/apt/sources.list
  fi
  shopt -s nullglob
  for f in /etc/apt/sources.list.d/*.list; do
    enable_nonfree_in_list_file "$f"
  done
  for f in /etc/apt/sources.list.d/*.sources; do
    enable_nonfree_in_sources_file "$f"
  done
  shopt -u nullglob
}

configure_host_network_modules() {
  local mods=(
    tun
    wireguard
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

  run_cmd apt-get update

  local -a hdr_pkgs=("proxmox-default-headers")
  if [[ ! -d "/lib/modules/${kver}/build" ]]; then
    hdr_pkgs+=("proxmox-headers-${kver}")
  fi
  log_info "Ensuring Proxmox kernel headers (${hdr_pkgs[*]})..."
  run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y "${hdr_pkgs[@]}" curl gnupg ca-certificates

  log_info "Blacklisting nouveau in /etc/modprobe.d/blacklist-nouveau.conf"
  local nouveau_conf=""
  nouveau_conf="$(cat <<'EOF'
blacklist nouveau
options nouveau modeset=0
EOF
)"
  write_file_managed "/etc/modprobe.d/blacklist-nouveau.conf" 0644 "${nouveau_conf}"$'\n'

  shopt -s nullglob
  local f=""
  for f in /etc/apt/sources.list.d/cuda-debian*.list /etc/apt/sources.list.d/cuda-debian*.sources; do
    if [[ "$f" != "/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.list" ]]; then
      run_cmd rm -f "$f"
    fi
  done
  shopt -u nullglob

  local keyring_url="https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64/cuda-keyring_1.1-1_all.deb"
  ensure_tmp_dir
  local keyring_tmp="${TMP_WORK_DIR}/cuda-keyring_1.1-1_all.deb"
  if [[ ! -f /usr/share/keyrings/cuda-archive-keyring.gpg ]]; then
    run_cmd curl -fsSL "$keyring_url" -o "$keyring_tmp"
    run_cmd dpkg -i "$keyring_tmp"
  fi

  local cuda_list="deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] https://developer.download.nvidia.com/compute/cuda/repos/${cuda_repo}/x86_64/ /"
  write_file_managed "/etc/apt/sources.list.d/cuda-${cuda_repo}-x86_64.list" 0644 "${cuda_list}"$'\n'

  run_cmd apt-get update
  run_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y \
    nvidia-driver-cuda nvidia-kernel-dkms nvidia-persistenced

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

  if command -v update-initramfs >/dev/null 2>&1; then
    run_cmd update-initramfs -u
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
ExecStart=/bin/sh -c '/usr/bin/nvidia-modprobe -u -c0 -m || true; /usr/bin/nvidia-smi >/dev/null 2>&1 || true'

[Install]
WantedBy=multi-user.target
EOF
)"
  write_file_managed "/etc/systemd/system/nvidia-lxc-prestart.service" 0644 "${service_unit}"$'\n'

  if command -v systemctl >/dev/null 2>&1; then
    run_cmd systemctl daemon-reload
    run_cmd systemctl enable nvidia-persistenced.service nvidia-lxc-prestart.service
    run_cmd systemctl start nvidia-persistenced.service || log_warn "nvidia-persistenced start deferred until reboot."
    run_cmd systemctl start nvidia-lxc-prestart.service || true
  fi

  if [[ "${DRY_RUN}" != "1" && ! -r /proc/driver/nvidia/version ]]; then
    log_warn "NVIDIA kernel module is not yet active (nouveau may still be loaded). Reboot the Proxmox host before starting NVIDIA LXCs."
  fi
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
  if dpkg-query -W -f='${Status}' pve-firmware 2>/dev/null | grep -q "install ok installed"; then
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
DEVPATH=="*/virtual/input/*", ENV{ID_SEAT}="seat9"
KERNEL=="hidraw*", ATTRS{name}=="*Wolf*", ENV{ID_SEAT}="seat9"
EOF
)"
  write_file_managed "/etc/udev/rules.d/85-wolf-virtual-inputs.rules" 0644 "${udev_rules}"$'\n'

  if command -v udevadm >/dev/null 2>&1; then
    run_cmd udevadm control --reload-rules
    run_cmd udevadm trigger --subsystem-match=misc || true
  fi
}

run_host_preparation() {
  log_info "Preparing Proxmox VE host (shared driver mode, never vfio-pci)..."
  if command -v lspci >/dev/null 2>&1; then
    local vfio_check=""
    vfio_check="$(lspci -k 2>/dev/null | grep -B2 -i 'Kernel driver in use: vfio-pci' || true)"
    if [[ -n "$vfio_check" ]]; then
      log_warn "Detected GPU(s) bound to vfio-pci! Shared LXC passthrough requires the host kernel driver (never vfio-pci):"
      log_warn "$vfio_check"
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
  existing_volid="$(pveam list "$TEMPLATE_STORAGE" 2>/dev/null | awk -v pat="$TPL_PATTERN" '$1 ~ pat {print $1}' | sort -V | tail -n1 || true)"
  if [[ -n "$existing_volid" ]]; then
    LXC_TEMPLATE="$existing_volid"
    log_info "Using cached template: ${LXC_TEMPLATE}"
    return 0
  fi

  log_info "Updating Proxmox appliance index (pveam update)..."
  run_cmd pveam update

  local avail_tpl=""
  avail_tpl="$(pveam available --section system 2>/dev/null | awk -v pat="$TPL_PATTERN" '$2 ~ pat {print $2}' | sort -V | tail -n1 || true)"
  if [[ -z "$avail_tpl" ]]; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      LXC_TEMPLATE="${TEMPLATE_STORAGE}:vztmpl/${TPL_PATTERN}dryrun_amd64.tar.zst"
      log_info "[DRY-RUN] Would download latest ${TPL_PATTERN}* template into ${TEMPLATE_STORAGE}"
      return 0
    fi
    die "No available template matching '${TPL_PATTERN}' from pveam available --section system"
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
    if ! grep -qxF "$line" "$conf_file" 2>/dev/null; then
      printf '%s\n' "$line" >> "$conf_file"
    fi
  done
}

resolve_nvidia_minor_for_slot() {
  local slot="${1:-}"
  local info_file="" minor=""

  if [[ -n "$slot" ]]; then
    for info_file in /proc/driver/nvidia/gpus/*/information; do
      [[ -r "$info_file" ]] || continue
      local dir_slot=""
      dir_slot="$(basename "$(dirname "$info_file")")"
      if [[ "${dir_slot,,}" == "${slot,,}" ]]; then
        minor="$(awk '/Device Minor:/ {print $3; exit}' "$info_file" 2>/dev/null || true)"
        if [[ "$minor" =~ ^[0-9]+$ ]]; then
          printf '%s\n' "$minor"
          return 0
        fi
      fi
    done
  fi

  if [[ -z "$slot" ]]; then
    for info_file in /proc/driver/nvidia/gpus/*/information; do
      [[ -r "$info_file" ]] || continue
      minor="$(awk '/Device Minor:/ {print $3; exit}' "$info_file" 2>/dev/null || true)"
      if [[ "$minor" =~ ^[0-9]+$ ]]; then
        printf '%s\n' "$minor"
        return 0
      fi
    done
  fi

  if command -v nvidia-smi >/dev/null 2>&1; then
    local smi_out="" idx="" bus_id=""
    smi_out="$(nvidia-smi --query-gpu=index,pci.bus_id --format=csv,noheader 2>/dev/null || true)"
    while IFS=',' read -r idx bus_id; do
      idx="$(tr -d '[:space:]' <<< "$idx")"
      bus_id="$(tr -d '[:space:]' <<< "$bus_id")"
      bus_id="${bus_id#0000}"
      if [[ -z "$slot" || "${bus_id,,}" == "${slot,,}" ]]; then
        if [[ "$idx" =~ ^[0-9]+$ ]]; then
          printf '%s\n' "$idx"
          return 0
        fi
      fi
    done <<< "$smi_out"
  fi

  return 1
}

ensure_nvidia_prestart_hook() {
  local hook_path="/usr/local/libexec/nvidia-lxc-prestart.sh"
  local hook_body=""
  hook_body="$(cat <<'EOF'
#!/usr/bin/env bash
/usr/bin/nvidia-modprobe -u -c0 -m >/dev/null 2>&1 || true
/usr/bin/nvidia-smi >/dev/null 2>&1 || true
EOF
)"
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would write executable NVIDIA pre-start hook ${hook_path}" >&2
  else
    mkdir -p /usr/local/libexec
    printf '%s\n' "$hook_body" > "$hook_path"
    chmod 0755 "$hook_path"
  fi
  printf '%s\n' "$hook_path"
}

configure_nvidia_passthrough() {
  log_info "Configuring shared NVIDIA GPU passthrough for LXC ${CTID}..."

  if [[ "${DRY_RUN}" != "1" ]] && command -v nvidia-modprobe >/dev/null 2>&1; then
    nvidia-modprobe -u -c0 -m >/dev/null 2>&1 || true
  fi

  local nv_minor=""
  if ! nv_minor="$(resolve_nvidia_minor_for_slot "$PCI_SLOT")"; then
    if [[ "${DRY_RUN}" == "1" ]]; then
      nv_minor="1"
      log_warn "[DRY-RUN] NVIDIA driver not active on this host; simulating resolved device index ${nv_minor}."
    else
      die "Could not resolve /dev/nvidia<N> for PCI slot '${PCI_SLOT:-auto}'. Run host prep and reboot first."
    fi
  fi

  local nv_dev="/dev/nvidia${nv_minor}"
  local nv_major="195"
  if [[ -c "$nv_dev" ]]; then
    nv_major="$(get_char_major "$nv_dev")"
  fi

  local uvm_major="" caps_major=""
  if [[ -c /dev/nvidia-uvm ]]; then
    uvm_major="$(get_char_major /dev/nvidia-uvm || true)"
  fi
  local cap_node=""
  shopt -s nullglob
  for cap_node in /dev/nvidia-caps/nvidia-cap*; do
    if [[ -c "$cap_node" ]]; then
      caps_major="$(get_char_major "$cap_node" || true)"
      break
    fi
  done
  shopt -u nullglob

  local hook_path=""
  hook_path="$(ensure_nvidia_prestart_hook)"

  local -a lines=(
    "lxc.hook.pre-start: ${hook_path}"
    "lxc.cgroup2.devices.allow: c ${nv_major}:* rwm"
  )
  [[ -n "$uvm_major" ]] && lines+=("lxc.cgroup2.devices.allow: c ${uvm_major}:* rwm")
  [[ -n "$caps_major" ]] && lines+=("lxc.cgroup2.devices.allow: c ${caps_major}:* rwm")
  lines+=(
    "lxc.cgroup2.devices.allow: c 226:* rwm"
    "lxc.mount.entry: ${nv_dev} dev/nvidia${nv_minor} none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidiactl dev/nvidiactl none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-modeset dev/nvidia-modeset none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-uvm dev/nvidia-uvm none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-uvm-tools dev/nvidia-uvm-tools none bind,optional,create=file"
    "lxc.mount.entry: /dev/nvidia-caps dev/nvidia-caps none bind,optional,create=dir"
  )

  if (( ! DO_INTEL && ! DO_AMD )); then
    lines+=("lxc.mount.entry: /dev/dri dev/dri none bind,optional,create=dir")
  fi

  append_lxc_conf_lines "${lines[@]}"
}

configure_tun_device() {
  local pve_ge_82=0
  if [[ -n "${PVE_MAJOR:-}" ]] && { (( PVE_MAJOR > 8 )) || (( PVE_MAJOR == 8 && PVE_MINOR >= 2 )); }; then
    pve_ge_82=1
  fi

  if (( pve_ge_82 )); then
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
  log_info "Configuring Wolf virtual input cgroups and bind mounts for LXC ${CTID}..."
  append_lxc_conf_lines \
    "lxc.cgroup2.devices.allow: c 10:* rwm" \
    "lxc.cgroup2.devices.allow: c 13:* rwm" \
    "lxc.mount.entry: /dev/uinput dev/uinput none bind,optional,create=file" \
    "lxc.mount.entry: /dev/uhid dev/uhid none bind,optional,create=file" \
    "lxc.mount.entry: /dev/input dev/input none bind,optional,create=dir" \
    "lxc.mount.entry: /run/udev run/udev none bind,optional,create=dir"
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

configure_dri_and_kfd_after_boot() {
  if (( ! DO_INTEL && ! DO_AMD )); then
    return 0
  fi

  local pve_ge_82=0
  if [[ -n "${PVE_MAJOR:-}" ]] && { (( PVE_MAJOR > 8 )) || (( PVE_MAJOR == 8 && PVE_MINOR >= 2 )); }; then
    pve_ge_82=1
  fi

  if (( ! pve_ge_82 )) || [[ -n "$PCI_SLOT" && -d /dev/dri/by-path ]]; then
    local -a dri_lines=()
    if (( ! DO_NVIDIA )); then
      dri_lines+=("lxc.cgroup2.devices.allow: c 226:* rwm")
    fi
    if [[ -n "$PCI_SLOT" && -e "/dev/dri/by-path/pci-${PCI_SLOT}-render" ]]; then
      dri_lines+=("lxc.mount.entry: /dev/dri/by-path/pci-${PCI_SLOT}-render dev/dri/renderD128 none bind,optional,create=file")
      if [[ -e "/dev/dri/by-path/pci-${PCI_SLOT}-card" ]]; then
        dri_lines+=("lxc.mount.entry: /dev/dri/by-path/pci-${PCI_SLOT}-card dev/dri/card0 none bind,optional,create=file")
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
    append_lxc_conf_lines "${dri_lines[@]}"
    if [[ "${DRY_RUN}" != "1" ]]; then
      run_cmd pct reboot "$CTID"
      wait_lxc_systemd_ready
    fi
    return 0
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would query dynamic video/render GIDs inside LXC ${CTID} and attach /dev/dri/* (and /dev/kfd for AMD) via dev[n]"
    return 0
  fi

  local gid_pair="" video_gid="44" render_gid="104"
  gid_pair="$(pct exec "$CTID" -- bash -c '
    getent group render >/dev/null 2>&1 || groupadd -r render
    v=$(getent group video | cut -d: -f3)
    r=$(getent group render | cut -d: -f3)
    echo "${v:-44}:${r:-104}"
  ')"
  video_gid="${gid_pair%%:*}"
  render_gid="${gid_pair##*:}"

  local added_dev=0 node=""
  shopt -s nullglob
  for node in /dev/dri/renderD*; do
    [[ -c "$node" ]] || continue
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "${node},gid=${render_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  done
  for node in /dev/dri/card*; do
    [[ -c "$node" ]] || continue
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "${node},gid=${video_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  done
  shopt -u nullglob

  if (( DO_AMD )) && [[ -c /dev/kfd ]]; then
    run_cmd pct set "$CTID" "--dev${DEV_IDX}" "/dev/kfd,gid=${render_gid},mode=0660"
    DEV_IDX=$((DEV_IDX + 1))
    added_dev=1
  fi

  if (( added_dev )); then
    log_info "Restarting LXC ${CTID} to activate Proxmox dev[n] GPU nodes..."
    run_cmd pct reboot "$CTID"
    wait_lxc_systemd_ready
  fi
}

# ============================================================================
# Inner LXC Payload (Streamed via pct exec "$CTID" -- bash -s)
# ============================================================================

emit_lxc_payload() {
  cat <<'EOF_LXC_PAYLOAD'
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
GPU_MODE="${GPU_MODE:-auto}"
AIO_DOMAIN="${AIO_DOMAIN:-aio.example.com}"
AIO_DIR="${AIO_DIR:-${DOCKTUN_DIR}/projects/aiostreams}"
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

lxc_log_info()    { printf '[LXC INFO] %s\n' "$*"; }
lxc_log_warn()    { printf '[LXC WARN] %s\n' "$*" >&2; }
lxc_log_error()   { printf '[LXC ERROR] %s\n' "$*" >&2; }
lxc_log_success() { printf '[LXC OK] %s\n' "$*"; }
lxc_die()         { lxc_log_error "$*"; exit 1; }

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

lxc_detect_os() {
  local os_file="${OS_RELEASE_FILE:-/etc/os-release}"
  [[ -r "$os_file" ]] || return 1
  local id="" version_id="" version_codename="" ubuntu_codename="" key="" val=""
  while IFS='=' read -r key val || [[ -n "$key" ]]; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    val="${val%$'\r'}"; val="${val#\"}"; val="${val%\"}"; val="${val#\'}"; val="${val%\'}"
    case "$key" in
      ID) id="$val" ;;
      VERSION_ID) version_id="$val" ;;
      VERSION_CODENAME) version_codename="$val" ;;
      UBUNTU_CODENAME) ubuntu_codename="$val" ;;
    esac
  done < "$os_file"
  LXC_OS_ID="$id"
  LXC_OS_VERSION_ID="$version_id"
  LXC_OS_CODENAME="${version_codename:-$ubuntu_codename}"
  [[ -n "$LXC_OS_ID" ]]
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
  printf '%s\n' "$content" > "$target"
  chmod "$mode" "$target"
}

lxc_port_in_use() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -H -tuln 2>/dev/null | grep -qE ":${p}([[:space:]]|$)"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -tuln 2>/dev/null | grep -qE ":${p}([[:space:]]|$)"
  else
    return 1
  fi
}

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
}

lxc_append_unique_key() {
  local key="$1" auth_file="$2"
  [[ -z "$key" ]] && return 0
  if ! grep -qxF "$key" "$auth_file" 2>/dev/null; then
    printf '%s\n' "$key" >> "$auth_file"
  fi
}

lxc_has_authorized_ssh_key() {
  local file=""
  for file in "$@"; do
    if [[ -r "$file" ]] && grep -qE '^[[:space:]]*(ssh-|ecdsa-|sk-)' "$file"; then
      return 0
    fi
  done
  return 1
}

lxc_configure_ufw_docker_user() {
  local after_rules="/etc/ufw/after.rules"
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would append DOCKER-USER LAN restriction for port ${DOCKTUN_PORT} in ${after_rules}"
    return 0
  fi
  [[ -f "$after_rules" ]] || return 0

  cp -a "$after_rules" "${after_rules}.bak.pre-docktun"
  local tmp_rules=""
  tmp_rules="$(mktemp)"

  # Strip any previous managed block so updates are idempotent
  awk '
    /^# BEGIN DOCKTUN DOCKER-USER$/ { skip=1; next }
    /^# END DOCKTUN DOCKER-USER$/ { skip=0; next }
    !skip { print }
  ' "$after_rules" > "$tmp_rules"

  local block=""
  block+="# BEGIN DOCKTUN DOCKER-USER"$'\n'
  block+=":DOCKER-USER - [0:0]"$'\n'
  block+="-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN"$'\n'
  block+="-A DOCKER-USER -i docker0 -j RETURN"$'\n'
  block+="-A DOCKER-USER -i br-+ -j RETURN"$'\n'
  local -a subnets=()
  local subnet=""
  IFS=',' read -ra subnets <<< "$LAN_SUBNETS"
  for subnet in "${subnets[@]}"; do
    subnet="${subnet#"${subnet%%[![:space:]]*}"}"
    subnet="${subnet%"${subnet##*[![:space:]]}"}"
    [[ -z "$subnet" ]] && continue
    block+="-A DOCKER-USER -p tcp -m conntrack --ctorigdstport ${DOCKTUN_PORT} -s ${subnet} -j RETURN"$'\n'
  done
  block+="-A DOCKER-USER -p tcp -m conntrack --ctorigdstport ${DOCKTUN_PORT} -j DROP"$'\n'
  block+="-A DOCKER-USER -j RETURN"$'\n'
  block+="# END DOCKTUN DOCKER-USER"$'\n'

  if grep -q '^COMMIT$' "$tmp_rules"; then
    local last_commit=""
    last_commit="$(grep -n '^COMMIT$' "$tmp_rules" | tail -n1 | cut -d: -f1)"
    {
      head -n "$((last_commit - 1))" "$tmp_rules"
      printf '%s' "$block"
      tail -n +"$last_commit" "$tmp_rules"
    } > "$after_rules"
  else
    {
      cat "$tmp_rules"
      printf '*filter\n%sCOMMIT\n' "$block"
    } > "$after_rules"
  fi
  rm -f "$tmp_rules"
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
  lxc_run_cmd apt-get install -y --no-install-recommends \
    sudo ufw fail2ban unattended-upgrades apt-listchanges \
    curl ca-certificates gnupg jq openssh-server openssl

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd apt-get install -y --no-install-recommends python3-systemd
  elif apt-cache show python3-systemd >/dev/null 2>&1; then
    apt-get install -y --no-install-recommends python3-systemd || \
      lxc_log_warn "Failed to install python3-systemd; fail2ban may need rsyslog fallback."
  fi

  lxc_fix_networkd_wait_online

  # Admin user
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd useradd -m -s /bin/bash -G sudo "$ADMIN_USER"
    lxc_log_info "[DRY-RUN] Would write /etc/sudoers.d/90-${ADMIN_USER} (mode 0440)"
  else
    if id "$ADMIN_USER" >/dev/null 2>&1; then
      usermod -aG sudo "$ADMIN_USER"
    else
      useradd -m -s /bin/bash -G sudo "$ADMIN_USER"
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

  if [[ "${DRY_RUN}" == "1" ]]; then
    if [[ -z "$SSH_PUBKEY" ]] && ! lxc_has_authorized_ssh_key "$root_auth" "$admin_auth"; then
      password_auth="yes"
    fi
    lxc_log_info "[DRY-RUN] Would write /etc/ssh/sshd_config.d/00-hardening.conf (PasswordAuthentication ${password_auth})"
    lxc_run_cmd sshd -T
    lxc_run_cmd systemctl reload ssh
  else
    install -d -m 0700 -o "$ADMIN_USER" -g "$ADMIN_USER" "${admin_home}/.ssh"
    touch "$admin_auth"
    chown "${ADMIN_USER}:${ADMIN_USER}" "$admin_auth"
    chmod 0600 "$admin_auth"

    [[ -n "$SSH_PUBKEY" ]] && lxc_append_unique_key "$SSH_PUBKEY" "$admin_auth"
    if [[ -r "$root_auth" ]]; then
      local line=""
      while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "$line" || "$line" == \#* ]] && continue
        lxc_append_unique_key "$line" "$admin_auth"
      done < "$root_auth"
    fi

    if ! lxc_has_authorized_ssh_key "$admin_auth" "$root_auth"; then
      lxc_log_warn "No SSH authorized keys found; keeping 'PasswordAuthentication yes' to avoid lockout."
      password_auth="yes"
    fi

    lxc_write_file "/etc/ssh/sshd_config.d/00-hardening.conf" 0644 <<EOF
PasswordAuthentication ${password_auth}
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
PubkeyAuthentication yes
X11Forwarding no
MaxAuthTries 3
EOF

    install -d -m 0755 /run/sshd
    if command -v sshd >/dev/null 2>&1; then
      sshd -t || lxc_die "sshd configuration validation failed."
      local effective_sshd=""
      effective_sshd="$(sshd -T 2>/dev/null | grep -E '^(permitrootlogin|passwordauthentication|pubkeyauthentication) ' || true)"
      lxc_log_info "Effective sshd settings: ${effective_sshd//$'\n'/; }"
    fi

    if systemctl list-unit-files ssh.service 2>/dev/null | grep -q 'ssh\.service'; then
      systemctl enable --now ssh || true
      systemctl reload ssh || systemctl restart ssh || true
    elif systemctl list-unit-files sshd.service 2>/dev/null | grep -q 'sshd\.service'; then
      systemctl enable --now sshd || true
      systemctl reload sshd || systemctl restart sshd || true
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
        lxc_ufw_exec_safe allow from "$subnet" to any port "$DOCKTUN_PORT" proto tcp comment "DockTun LAN" || break
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
        if [[ -f /etc/ufw/after.rules.bak.pre-docktun ]]; then
          mv -f /etc/ufw/after.rules.bak.pre-docktun /etc/ufw/after.rules
          lxc_ufw_exec_safe --force enable || true
        fi
      fi
    fi
  fi

  # Fail2ban with backend = systemd and banaction = nftables-multiport
  lxc_write_file "/etc/fail2ban/jail.d/99-sshd.local" 0644 <<'EOF'
[DEFAULT]
bantime = 1h
findtime = 10m
maxretry = 5
banaction = nftables-multiport

[sshd]
enabled = true
port = ssh
backend = systemd
banaction = nftables-multiport
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

  # Unattended upgrades with NVIDIA blacklist
  lxc_write_file "/etc/apt/apt.conf.d/20auto-upgrades" 0644 <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
  lxc_write_file "/etc/apt/apt.conf.d/52unattended-upgrades-local" 0644 <<'EOF'
Unattended-Upgrade::Package-Blacklist {
    "^nvidia-";
    "^libnvidia-";
};
EOF
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd dpkg-reconfigure -f noninteractive unattended-upgrades
  else
    DEBIAN_FRONTEND=noninteractive dpkg-reconfigure -f noninteractive unattended-upgrades || true
  fi

  # LXC network-namespace sysctls
  lxc_write_file "/etc/sysctl.d/99-lxc-media.conf" 0644 <<'EOF'
net.ipv4.ip_forward = 1
net.ipv4.conf.all.src_valid_mark = 1
EOF
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd sysctl -p /etc/sysctl.d/99-lxc-media.conf
  else
    local kv=""
    for kv in "net.ipv4.ip_forward=1" "net.ipv4.conf.all.src_valid_mark=1"; do
      sysctl -w "$kv" >/dev/null 2>&1 || lxc_log_warn "Sysctl '${kv}' read-only/unsupported in LXC; continuing."
    done
  fi
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
    printf 'disabled_plugins = %s\n' "$snapshotters_json" > "$CONTAINERD_CONFIG"
    chmod 0644 "$CONTAINERD_CONFIG"
    return 0
  fi

  local tmp_cfg=""
  tmp_cfg="$(mktemp)"
  if grep -qE '^[[:space:]]*disabled_plugins[[:space:]]*=' "$CONTAINERD_CONFIG"; then
    local existing_line="" existing_array="" normalized_array="" merged_array=""
    existing_line="$(grep -E '^[[:space:]]*disabled_plugins[[:space:]]*=' "$CONTAINERD_CONFIG" | head -n 1)"
    existing_array="${existing_line#*=}"
    existing_array="${existing_array%%#*}"
    existing_array="${existing_array#"${existing_array%%[![:space:]]*}"}"
    existing_array="${existing_array%"${existing_array##*[![:space:]]}"}"
    normalized_array="$(sed -E "s/'([^']*)'/\"\\1\"/g; s/,[[:space:]]*\\]/]/g" <<< "$existing_array")"
    if merged_array="$(jq -c --argjson add "$snapshotters_json" '. + $add | unique' <<< "$normalized_array" 2>/dev/null)"; then
      awk -v repl="disabled_plugins = ${merged_array}" '
        !done && /^[[:space:]]*disabled_plugins[[:space:]]*=/ { print repl; done=1; next }
        { print }
      ' "$CONTAINERD_CONFIG" > "$tmp_cfg"
    else
      awk -v repl="disabled_plugins = ${snapshotters_json}" '
        !done && /^[[:space:]]*disabled_plugins[[:space:]]*=/ { print repl; done=1; next }
        { print }
      ' "$CONTAINERD_CONFIG" > "$tmp_cfg"
    fi
  else
    {
      printf 'disabled_plugins = %s\n' "$snapshotters_json"
      cat "$CONTAINERD_CONFIG"
    } > "$tmp_cfg"
  fi
  mv "$tmp_cfg" "$CONTAINERD_CONFIG"
  chmod 0644 "$CONTAINERD_CONFIG"
}

lxc_merge_daemon_json() {
  local extra_jq="${1:-.}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would merge ulimits/log-driver/log-opts/live-restore/storage-driver into ${DOCKER_DAEMON_JSON}"
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
  tmp_json="$(mktemp)"
  jq --argjson base_ulimit '{"Name":"nofile","Hard":65536,"Soft":65536}' \
     --argjson base_log_opts '{"max-size":"10m","max-file":"3"}' \
    '
      .["default-ulimits"] = ((.["default-ulimits"] // {}) * {"nofile": $base_ulimit}) |
      .["log-driver"] = "json-file" |
      .["log-opts"] = ((.["log-opts"] // {}) * $base_log_opts) |
      .["live-restore"] = true |
      .["storage-driver"] = (.["storage-driver"] // "overlay2")
    ' "$DOCKER_DAEMON_JSON" | jq "$extra_jq" > "$tmp_json"
  mv "$tmp_json" "$DOCKER_DAEMON_JSON"
  chmod 0644 "$DOCKER_DAEMON_JSON"
}

lxc_docker_ready() {
  docker info >/dev/null 2>&1
}

lxc_enable_fuse_overlayfs_fallback() {
  lxc_log_warn "Docker overlay2 failed or fell back to vfs; attempting fuse-overlayfs fallback..."
  if [[ ! -e /dev/fuse ]]; then
    lxc_log_error "/dev/fuse is not available in this LXC (enable Proxmox feature fuse=1 via --with-fuse)."
    return 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y --no-install-recommends fuse-overlayfs
  lxc_merge_daemon_json '.["storage-driver"] = "fuse-overlayfs"'
  systemctl restart docker
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
  lxc_run_cmd apt-get install -y --no-install-recommends ca-certificates curl gnupg jq

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
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  fi

  lxc_apply_containerd_zfs_guard

  # Pin overlay2 on PVE 8.1+/9 (or use fuse-overlayfs if older PVE on ZFS with /dev/fuse)
  local root_fs=""
  root_fs="$(stat -f -c '%T' / 2>/dev/null || true)"
  if [[ "$root_fs" == "zfs" && -n "$PVE_MAJOR" ]] && { (( PVE_MAJOR < 8 )) || (( PVE_MAJOR == 8 && PVE_MINOR < 1 )); }; then
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
}

# --- Stage 3: Shared GPU Inside LXC -----------------------------------------

lxc_apt_pkg_exists() {
  local pkg="$1"
  local cand=""
  cand="$(apt-cache policy "$pkg" 2>/dev/null | awk '/Candidate:/ {print $2; exit}')"
  [[ -n "$cand" && "$cand" != "(none)" ]]
}

lxc_apt_exact_version_spec() {
  local pkg="$1" host_ver="$2"
  local exact=""
  exact="$(apt-cache madison "$pkg" 2>/dev/null | awk -v v="$host_ver" '$3 ~ ("(^|:)" v "([-~+]|$)") {print $3; exit}')"
  if [[ -n "$exact" ]]; then
    printf '%s=%s\n' "$pkg" "$exact"
  else
    printf '%s\n' "$pkg"
  fi
}

lxc_setup_nvidia() {
  local host_ver=""
  if [[ -n "$HOST_NVIDIA_VERSION" ]]; then
    host_ver="$HOST_NVIDIA_VERSION"
  elif [[ -r "$NVIDIA_PROC_VERSION_FILE" ]]; then
    local content=""
    content="$(cat "$NVIDIA_PROC_VERSION_FILE" 2>/dev/null || true)"
    if [[ "$content" =~ Kernel[[:space:]]+Module[[:space:]]+([0-9]+\.[0-9]+(\.[0-9]+)?) ]]; then
      host_ver="${BASH_REMATCH[1]}"
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
    lxc_run_cmd apt-get install -y --no-install-recommends \
      "libnvidia-compute-${major}" "nvidia-utils-${major}" "libnvidia-encode-${major}" "libnvidia-decode-${major}"
    lxc_run_cmd systemctl mask nvidia-persistenced.service
    lxc_run_cmd apt-mark hold "nvidia-*" "libnvidia-*"
    lxc_run_cmd nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true
    lxc_run_cmd nvidia-ctk runtime configure --runtime=docker
    lxc_run_cmd nvidia-ctk cdi generate --output="$NVIDIA_CDI_OUTPUT"
    return 0
  fi

  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates gnupg jq

  local tmp_deb=""
  tmp_deb="$(mktemp --suffix=.deb)"
  curl -fsSL "$keyring_url" -o "$tmp_deb"
  dpkg -i "$tmp_deb"
  rm -f "$tmp_deb"
  if [[ ! -f "$list_file" && ! -f "$sources_file" ]]; then
    printf 'deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] %s/ /\n' "$repo_url" > "$list_file"
    chmod 0644 "$list_file"
  fi
  apt-get update

  local -a pkgs=()
  local chosen="" candidate=""
  for candidate in "libnvidia-compute-${major}" "libnvidia-compute" "libcuda1"; do
    if lxc_apt_pkg_exists "$candidate"; then
      chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"
      pkgs+=("$chosen"); break
    fi
  done
  for candidate in "nvidia-utils-${major}" "nvidia-utils" "nvidia-smi"; do
    if lxc_apt_pkg_exists "$candidate"; then
      chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"
      pkgs+=("$chosen"); break
    fi
  done
  for candidate in "libnvidia-encode-${major}" "libnvidia-encode1" "libnvidia-encode"; do
    if lxc_apt_pkg_exists "$candidate"; then
      chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"
      pkgs+=("$chosen"); break
    fi
  done
  for candidate in "libnvidia-decode-${major}" "libnvidia-decode1" "libnvidia-decode" "libnvcuvid1"; do
    if lxc_apt_pkg_exists "$candidate"; then
      chosen="$(lxc_apt_exact_version_spec "$candidate" "$host_ver")"
      pkgs+=("$chosen"); break
    fi
  done
  [[ "${#pkgs[@]}" -gt 0 ]] || lxc_die "No matching NVIDIA userspace packages found for driver ${host_ver}."
  apt-get install -y --no-install-recommends "${pkgs[@]}"

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
    dpkg-query -W -f='${Status}\t${Package}\n' 'nvidia-*' 'libnvidia-*' 'libcuda*' 2>/dev/null \
      | awk -F'\t' '$1 == "install ok installed" {print $2}'
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
  apt-get install -y nvidia-container-toolkit

  nvidia-ctk config --in-place --set nvidia-container-cli.no-cgroups=true
  nvidia-ctk runtime configure --runtime=docker
  install -d -m 0755 "$(dirname "$NVIDIA_CDI_OUTPUT")"
  nvidia-ctk cdi generate --output="$NVIDIA_CDI_OUTPUT" || \
    lxc_log_warn "nvidia-ctk cdi generate failed; runtime=nvidia/--gpus all will still work."
  lxc_merge_daemon_json '.features = ((.features // {}) * {"cdi": true})'

  systemctl daemon-reload
  systemctl restart docker
  nvidia-smi || lxc_die "nvidia-smi failed inside LXC."
  if ! docker run --rm --gpus all "$NVIDIA_SMOKE_IMAGE" nvidia-smi >/dev/null 2>&1; then
    lxc_log_warn "Docker GPU smoke test (${NVIDIA_SMOKE_IMAGE}) failed (offline or image unavailable)."
  else
    lxc_log_success "Docker NVIDIA GPU smoke test passed."
  fi
}

lxc_setup_intel_amd_vaapi() {
  local want_intel="$1" want_amd="$2"
  lxc_log_info "Configuring Intel/AMD VA-API drivers and video/render group access..."
  export DEBIAN_FRONTEND=noninteractive
  lxc_run_cmd apt-get update

  local -a pkgs=("vainfo" "va-driver-all")
  if (( want_intel == 1 )); then
    if [[ "${DRY_RUN}" == "1" ]] || lxc_apt_pkg_exists "intel-media-va-driver-non-free"; then
      pkgs+=("intel-media-va-driver-non-free")
    elif lxc_apt_pkg_exists "intel-media-va-driver"; then
      pkgs+=("intel-media-va-driver")
    fi
  fi
  if (( want_amd == 1 )); then
    if [[ "${DRY_RUN}" == "1" ]] || lxc_apt_pkg_exists "mesa-va-drivers"; then
      pkgs+=("mesa-va-drivers")
    fi
  fi

  lxc_run_cmd apt-get install -y --no-install-recommends "${pkgs[@]}"

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

  local render_dev=""
  for render_dev in /dev/dri/renderD*; do
    [[ -c "$render_dev" ]] || continue
    vainfo --display drm --device "$render_dev" >/dev/null 2>&1 && \
      lxc_log_success "vainfo --display drm passed on ${render_dev}." || \
      lxc_log_warn "vainfo --display drm returned non-zero on ${render_dev}."
  done
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
    auto)
      if [[ -e /dev/nvidiactl ]] || compgen -G "/dev/nvidia[0-9]*" >/dev/null 2>&1; then
        do_nvidia=1
      fi
      if compgen -G "/dev/dri/renderD*" >/dev/null 2>&1 || [[ -e /dev/kfd ]]; then
        [[ -e /dev/kfd ]] && do_amd=1
        local vf="" vid=""
        for vf in /sys/class/drm/renderD*/device/vendor; do
          [[ -r "$vf" ]] || continue
          vid="$(tr -d '[:space:]' < "$vf" 2>/dev/null || true)"
          case "${vid,,}" in
            0x8086) do_intel=1 ;;
            0x1002) do_amd=1 ;;
          esac
        done
        if (( do_intel == 0 && do_amd == 0 )); then
          do_intel=1; do_amd=1
        fi
      fi
      ;;
  esac

  if (( do_nvidia == 1 )); then lxc_setup_nvidia; fi
  if (( do_intel == 1 || do_amd == 1 )); then lxc_setup_intel_amd_vaapi "$do_intel" "$do_amd"; fi
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
    lxc_die "Multiple compose files found in ${dir}: ${found[*]}. Keep only one."
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
  if [[ -n "$DOCKTUN_ADMIN_PASSWORD" ]]; then
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
  if command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [[ -z "$ip" ]] && command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}' || true)"
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

  if [[ "${DRY_RUN}" != "1" && "$WITH_ZEROCONF" == "1" ]] && lxc_port_in_use 80; then
    if lxc_is_existing_zeroconf_running; then
      lxc_log_info "Port 80 is already held by running 'docktun-zeroconf' container (idempotent update)."
    else
      lxc_log_warn "Port 80 is already bound inside the LXC by another non-docktun-zeroconf service; disabling ZeroConf mDNS and falling back to core DockTun only."
      WITH_ZEROCONF=0
    fi
  fi

  local compose_file=""
  if compose_file="$(lxc_find_existing_compose "$DOCKTUN_DIR")"; then
    lxc_log_info "Preserving existing compose file: ${compose_file}"
  else
    compose_file="${DOCKTUN_DIR}/compose.yml"
    if [[ "${DRY_RUN}" == "1" ]]; then
      lxc_log_info "[DRY-RUN] Would write ${compose_file} (port=${DOCKTUN_PORT}, zeroconf=${WITH_ZEROCONF}, SELF_PATH=/opt/projects/docktun, TAILSCALE_NETWORK=docktun)"
    else
      local tmp_file=""
      tmp_file="$(mktemp "${DOCKTUN_DIR}/.compose.yml.XXXXXX")"
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

  if [[ "${DRY_RUN}" != "1" ]]; then
    docker compose -f "$compose_file" config --quiet
  fi

  if [[ "$WITH_ZEROCONF" == "1" ]]; then
    lxc_run_cmd docker compose -f "$compose_file" pull
    lxc_run_cmd docker compose -f "$compose_file" up -d
  else
    lxc_run_cmd docker compose -f "$compose_file" pull docktun
    lxc_run_cmd docker compose -f "$compose_file" up -d --no-deps docktun
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would poll DockTun /healthz and display setup.token"
    return 0
  fi

  if ! lxc_wait_for "$DOCKTUN_HEALTH_TIMEOUT" "DockTun /healthz" lxc_docktun_health_probe; then
    docker logs --tail 50 docktun >&2 || true
    lxc_die "DockTun healthcheck failed."
  fi

  local host_ip=""
  host_ip="$(lxc_detect_primary_ip)"
  if [[ "$WITH_ZEROCONF" == "1" ]]; then
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
  lxc_run_cmd install -d -m 0755 "$AIO_DIR" "${AIO_DIR}/data"
  lxc_run_cmd chmod 0775 "$AIO_DIR" "${AIO_DIR}/data"

  if [[ "${DRY_RUN}" != "1" ]] && id "$ADMIN_USER" >/dev/null 2>&1; then
    local target_group="$ADMIN_USER"
    getent group docker >/dev/null 2>&1 && target_group="docker"
    chown -R "${ADMIN_USER}:${target_group}" "$AIO_DIR"
    chmod 2775 "$AIO_DIR" "${AIO_DIR}/data"
  fi

  local env_template="${AIO_DIR}/.env.template"
  local env_generated="${AIO_DIR}/.env.generated"

  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_log_info "[DRY-RUN] Would write ${env_template}, ${env_generated} (non-clobbering), nginx-proxy-streaming.conf.example, and check-readiness.sh"
    return 0
  fi

  local secret_key=""
  if [[ -f "$env_generated" ]]; then
    lxc_log_info "Preserving existing ${env_generated} (will not overwrite SECRET_KEY)."
    secret_key="$(awk -F= '/^SECRET_KEY=/{print $2; exit}' "$env_generated" 2>/dev/null || true)"
  fi
  if [[ -z "$secret_key" ]] || [[ ! "$secret_key" =~ ^[0-9a-fA-F]{64}$ ]]; then
    secret_key="$(lxc_generate_hex_secret)"
  fi
  if [[ ! -f "$env_generated" ]]; then
    local tmp_gen=""
    tmp_gen="$(mktemp "${AIO_DIR}/.env.generated.XXXXXX")"
    lxc_render_aio_env "$secret_key" > "$tmp_gen"
    chmod 0600 "$tmp_gen"
    mv -f "$tmp_gen" "$env_generated"
  fi
  local tmp_tpl=""
  tmp_tpl="$(mktemp "${AIO_DIR}/.env.template.XXXXXX")"
  lxc_render_aio_env "$secret_key" > "$tmp_tpl"
  chmod 0644 "$tmp_tpl"
  mv -f "$tmp_tpl" "$env_template"

  cat > "${AIO_DIR}/nginx-proxy-streaming.conf.example" <<EOF
# Reverse Proxy Example for AIOStreams BuiltinProxy, Web Player & WebSockets
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name ${AIO_DOMAIN};

    # ssl_certificate     /etc/letsencrypt/live/${AIO_DOMAIN}/fullchain.pem;
    # ssl_certificate_key /etc/letsencrypt/live/${AIO_DOMAIN}/privkey.pem;

    client_max_body_size 64m;

    location / {
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
  chmod 0644 "${AIO_DIR}/nginx-proxy-streaming.conf.example"

  cat > "${AIO_DIR}/check-readiness.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

DOCKTUN_PORT="\${DOCKTUN_PORT:-${DOCKTUN_PORT}}"
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
  if command -v ss >/dev/null 2>&1; then
    ss -H -tuln 2>/dev/null | grep -qE ":\${p}([[:space:]]|$)"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -tuln 2>/dev/null | grep -qE ":\${p}([[:space:]]|$)"
  else
    return 1
  fi
}

echo "=== AIOStreams & Media LXC Readiness Check ==="
[[ -c /dev/net/tun ]] && ok "/dev/net/tun present" || warn "/dev/net/tun missing (required for WireGuard/WARP)"
command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && ok "Docker daemon reachable" || fail "Docker daemon unavailable"
command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 && ok "Docker Compose v2 available" || fail "Docker Compose v2 unavailable"

if [[ -d /dev/shm ]]; then
  shm_mb="\$(df -Pm /dev/shm 2>/dev/null | awk 'NR==2 {for (i=2; i<=NF; i++) if (\$i ~ /^[0-9]+\$/) {print \$i; exit}}' || echo 0)"
  (( shm_mb >= 512 )) && ok "/dev/shm size is \${shm_mb} MiB" || warn "/dev/shm size is \${shm_mb:-unknown} MiB (< 512 MiB)"
fi

if [[ -r /etc/docker/daemon.json ]] && grep -q '65536' /etc/docker/daemon.json 2>/dev/null; then
  ok "Docker daemon.json configures nofile=65536"
else
  warn "Docker daemon.json missing nofile=65536"
fi

if command -v getent >/dev/null 2>&1 && getent ahosts github.com >/dev/null 2>&1; then
  ok "Outbound DNS resolution succeeded"
else
  warn "Outbound DNS resolution failed"
fi

if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
  ok "NVIDIA GPU accessible via nvidia-smi"
fi
if compgen -G "/dev/dri/renderD*" >/dev/null 2>&1; then
  vaapi_ok=0
  if command -v vainfo >/dev/null 2>&1; then
    for node in /dev/dri/renderD*; do
      if [[ -c "\$node" ]] && vainfo --display drm --device "\$node" >/dev/null 2>&1; then
        vaapi_ok=1
        break
      fi
    done
  fi
  (( vaapi_ok == 1 )) && ok "DRM/VAAPI vainfo --display drm succeeded" || ok "DRM render node(s) present"
fi

if command -v docker >/dev/null 2>&1 && docker exec docktun curl -fsS http://127.0.0.1:3001/healthz >/dev/null 2>&1; then
  ok "DockTun container /healthz passing"
else
  warn "DockTun /healthz did not respond"
fi

for check_port in 8080 3001 4005 8088 51820 "\${DOCKTUN_PORT}"; do
  if port_in_use "\$check_port"; then
    if [[ "\$check_port" == "\$DOCKTUN_PORT" ]]; then
      ok "Port \${check_port} bound (DockTun)"
    else
      warn "Port \${check_port} is already bound"
    fi
  else
    ok "Port \${check_port} is free"
  fi
done

echo "=== Summary: \${PASS_COUNT} passed, \${WARN_COUNT} warnings, \${FAIL_COUNT} failed ==="
(( FAIL_COUNT > 0 )) && exit 1
(( STRICT == 1 && WARN_COUNT > 0 )) && exit 1
exit 0
EOF
  chmod 0755 "${AIO_DIR}/check-readiness.sh"
  DOCKTUN_PORT="$DOCKTUN_PORT" "${AIO_DIR}/check-readiness.sh"
}

lxc_apply_template_hygiene() {
  if [[ "$TEMPLATE_HYGIENE" != "1" ]]; then
    return 0
  fi
  lxc_log_info "Applying template hygiene (clearing SSH host keys and machine-id)..."
  if [[ "${DRY_RUN}" == "1" ]]; then
    lxc_run_cmd rm -f /etc/ssh/ssh_host_*
    lxc_run_cmd truncate -s 0 /etc/machine-id
    return 0
  fi
  rm -f /etc/ssh/ssh_host_*
  truncate -s 0 /etc/machine-id
  [[ -f /var/lib/dbus/machine-id ]] && truncate -s 0 /var/lib/dbus/machine-id
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

stream_lxc_payload() {
  if [[ "${DRY_RUN}" == "1" ]]; then
    log_info "[DRY-RUN] Would stream inner LXC provisioning payload to CT ${CTID} via 'pct exec ${CTID} -- bash -s' (secrets quoted via printf '%q', never on argv)."
    return 0
  fi

  log_info "Streaming inner LXC provisioning payload to CT ${CTID}..."
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
    printf 'GPU_MODE=%q\n' "$GPU_MODE"
    printf 'AIO_DOMAIN=%q\n' "$AIO_DOMAIN"
    printf 'PVE_MAJOR=%q\n' "${PVE_MAJOR:-}"
    printf 'PVE_MINOR=%q\n' "${PVE_MINOR:-}"
    printf 'TEMPLATE_HYGIENE=%q\n' "$TEMPLATE_HYGIENE"
    emit_lxc_payload
  } | pct exec "$CTID" -- bash -s
}

create_and_provision_lxc() {
  resolve_template

  local features="keyctl=1,nesting=1"
  (( WITH_FUSE )) && features="${features},fuse=1"

  local net0="name=eth0,bridge=${BRIDGE},firewall=1,ip=${IP_ADDR},type=veth"
  if [[ "${IP_ADDR}" != "dhcp" && -n "$GW_ADDR" ]]; then
    net0="${net0},gw=${GW_ADDR}"
  fi

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
  [[ -n "$DNS_ADDR" ]] && create_cmd+=(--nameserver "$DNS_ADDR")
  [[ -n "$SSH_PUBKEY_FILE" ]] && create_cmd+=(--ssh-public-keys "$SSH_PUBKEY_FILE")

  log_info "Creating unprivileged LXC ${CTID} (${HOSTNAME}, ${OS_CHOICE})..."
  run_cmd "${create_cmd[@]}"

  if (( ALLOW_UNCONFINED_APPARMOR )); then
    log_warn "Applying opt-in --allow-unconfined-apparmor (lxc.apparmor.profile: unconfined) to CT ${CTID}."
    append_lxc_conf_lines "lxc.apparmor.profile: unconfined"
  fi

  if [[ -n "$MEDIA_DIR" ]]; then
    log_info "Preparing host media directory ${MEDIA_DIR} (owner 100000:100000)..."
    run_cmd mkdir -p "$MEDIA_DIR"
    run_cmd chown 100000:100000 "$MEDIA_DIR"
    run_cmd chmod 0775 "$MEDIA_DIR"
    run_cmd pct set "$CTID" "--mp${MP_IDX}" "${MEDIA_DIR},mp=/mnt/media"
    MP_IDX=$((MP_IDX + 1))
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

  configure_dri_and_kfd_after_boot
  stream_lxc_payload

  if (( TEMPLATE_HYGIENE )); then
    log_warn "Template hygiene applied. To convert CT ${CTID} into a template: pct stop ${CTID} && pct template ${CTID}"
  fi

  log_success "LXC ${CTID} (${HOSTNAME}) created and provisioned successfully."
}

main() {
  trap cleanup EXIT
  parse_args "$@"

  if [[ "${DRY_RUN}" != "1" ]]; then
    require_root
  elif [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    log_warn "Running in --dry-run mode as non-root user."
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

  create_and_provision_lxc
}

main "$@"
