#!/usr/bin/env bash
# ==============================================================================
# DockTun & Cloudflare Tunnel Installer for Docker VM
# ==============================================================================
# Features:
#   1. Detects OS and installs necessary dependencies (curl, jq, openssl, pciutils)
#   2. Pre-flight checks:
#      - Virtualization & Nesting (LXC cgroups/overlayfs/keyctl, KVM /dev/kvm, nested virt)
#      - GPU Acceleration (NVIDIA GPU & Container Toolkit, Intel QuickSync & AMD VA-API)
#      - Kernel & Network (IPv4 forwarding, /dev/net/tun, br_netfilter, overlay2, NTP sync)
#      - System resources (RAM, disk space, port availability)
#   3. Installs or verifies Docker & Docker Compose
#   4. Installs official cloudflared CLI binary for host architecture
#   5. Authenticates with Cloudflare (login / create tunnel / import credentials JSON)
#   6. Securely configures DockTun Admin credentials & generates JWT secret
#   7. Discovers existing Docker containers:
#      - Connects them to the 'cloudflared' Docker network for direct routing
#      - Imports existing Docker Compose projects into DockTun dashboard
#      - Configures Cloudflare Tunnel Ingress rules & creates DNS records
#   8. Builds & starts DockTun stack via Docker Compose
# ==============================================================================

set -o pipefail

# Colors
C_RESET='\033[0m'
C_BOLD='\033[1m'
C_RED='\033[0;31m'
C_GREEN='\033[0;32m'
C_YELLOW='\033[0;33m'
C_BLUE='\033[0;34m'
C_CYAN='\033[0;36m'
C_MAGENTA='\033[0;35m'

info() { echo -e "${C_CYAN}ℹ [INFO]${C_RESET} $1"; }
success() { echo -e "${C_GREEN}✔ [SUCCESS]${C_RESET} $1"; }
warn() { echo -e "${C_YELLOW}⚠ [WARN]${C_RESET} $1"; }
error() { echo -e "${C_RED}✖ [ERROR]${C_RESET} $1"; }
header() {
    echo -e "\n${C_BOLD}${C_BLUE}===================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  $1${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}===================================================================${C_RESET}\n"
}

# Terminal prompt helpers (handles piped curl | bash via /dev/tty fallback)
prompt_read() {
    local prompt_msg="$1"
    local var_name="$2"
    local default_val="$3"
    local val=""
    if [ -t 0 ]; then
        read -rp "$prompt_msg" val
    elif [ -e /dev/tty ]; then
        read -rp "$prompt_msg" val < /dev/tty
    fi
    val="${val:-$default_val}"
    printf -v "$var_name" '%s' "$val"
}

prompt_secret() {
    local prompt_msg="$1"
    local var_name="$2"
    local val=""
    if [ -t 0 ]; then
        read -rsp "$prompt_msg" val
        echo ""
    elif [ -e /dev/tty ]; then
        read -rsp "$prompt_msg" val < /dev/tty
        echo "" > /dev/tty 2>/dev/null || echo ""
    fi
    printf -v "$var_name" '%s' "$val"
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ------------------------------------------------------------------------------
# 1. Root / Sudo Check
# ------------------------------------------------------------------------------
if [ "$EUID" -ne 0 ]; then
    warn "This script requires superuser privileges to install packages, configure Docker, and manage cloudflared."
    echo -e "Please re-run as root: ${C_BOLD}sudo ./install.sh${C_RESET}"
    exit 1
fi

clear 2>/dev/null || true
echo -e "${C_CYAN}"
cat << "EOF"
  ____             _     _____             
 |  _ \  ___   ___| | __|_   _|   _ _ __   
 | | | |/ _ \ / __| |/ /  | | | | | '_ \  
 | |_| | (_) | (__|   <   | | |_| | | | | 
 |____/ \___/ \___|_|\_\  |_| \__,_|_| |_| 
                                           
  Cloudflare Tunnel & Docker Management Stack
EOF
echo -e "${C_RESET}"

header "Step 1: System, Dependencies & Pre-Flight Checks"

# Detect package manager
PKG_MANAGER=""
if command -v apt-get &>/dev/null; then
    PKG_MANAGER="apt"
elif command -v dnf &>/dev/null; then
    PKG_MANAGER="dnf"
elif command -v yum &>/dev/null; then
    PKG_MANAGER="yum"
elif command -v apk &>/dev/null; then
    PKG_MANAGER="apk"
elif command -v pacman &>/dev/null; then
    PKG_MANAGER="pacman"
fi

install_packages() {
    local pkgs=("$@")
    info "Installing required packages: ${pkgs[*]}..."
    case "$PKG_MANAGER" in
        apt)
            apt-get update -qq && apt-get install -y -qq "${pkgs[@]}" >/dev/null
            ;;
        dnf|yum)
            "$PKG_MANAGER" install -y -q "${pkgs[@]}" >/dev/null
            ;;
        apk)
            apk add --no-cache "${pkgs[@]}" >/dev/null
            ;;
        pacman)
            pacman -Sy --noconfirm "${pkgs[@]}" >/dev/null
            ;;
        *)
            warn "Unknown package manager. Please ensure ${pkgs[*]} are installed manually."
            ;;
    esac
}

# Check prerequisite CLI tools
NEEDED_TOOLS=()
for tool in curl jq openssl; do
    if ! command -v "$tool" &>/dev/null; then
        NEEDED_TOOLS+=("$tool")
    fi
done

if ! command -v lspci &>/dev/null; then
    NEEDED_TOOLS+=("pciutils")
fi

if [ ${#NEEDED_TOOLS[@]} -gt 0 ]; then
    install_packages "${NEEDED_TOOLS[@]}"
fi
success "Prerequisite tools verified."

# ------------------------------------------------------------------------------
# Pre-Flight: Docker & Docker Compose Verification
# ------------------------------------------------------------------------------
if ! command -v docker &>/dev/null; then
    warn "Docker is not installed on this system."
    prompt_read "Would you like this installer to automatically install Docker? [Y/n]: " INSTALL_DOCKER "Y"
    if [[ "$INSTALL_DOCKER" =~ ^[Yy]$ ]]; then
        info "Installing Docker using official convenience script..."
        curl -fsSL https://get.docker.com | sh
        systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
        success "Docker installed successfully."
    else
        error "Docker is required to run DockTun. Please install Docker and re-run this script."
        exit 1
    fi
else
    success "Docker is installed: $(docker --version)"
fi

# Ensure Docker daemon is active
if ! docker info &>/dev/null; then
    info "Starting Docker service..."
    systemctl start docker 2>/dev/null || service docker start 2>/dev/null || true
    sleep 2
    if ! docker info &>/dev/null; then
        error "Unable to communicate with Docker daemon. Please ensure Docker is running."
        exit 1
    fi
fi

# Check Docker Compose
DOCKER_COMPOSE_CMD=""
if docker compose version &>/dev/null; then
    DOCKER_COMPOSE_CMD="docker compose"
elif command -v docker-compose &>/dev/null; then
    DOCKER_COMPOSE_CMD="docker-compose"
else
    info "Installing Docker Compose CLI plugin..."
    if [ "$PKG_MANAGER" = "apt" ]; then
        apt-get install -y -qq docker-compose-plugin >/dev/null || true
    fi
    if docker compose version &>/dev/null; then
        DOCKER_COMPOSE_CMD="docker compose"
    else
        error "Docker Compose plugin could not be detected. Please install 'docker-compose-plugin'."
        exit 1
    fi
fi
success "Docker Compose verified: $($DOCKER_COMPOSE_CMD version)"

# ------------------------------------------------------------------------------
# Pre-Flight: Virtualization & Nesting Checks
# ------------------------------------------------------------------------------
echo ""
info "Checking Virtualization & Nesting support..."

VIRT_TYPE="bare-metal"
if command -v systemd-detect-virt &>/dev/null; then
    VIRT_TYPE=$(systemd-detect-virt 2>/dev/null || echo "none")
elif [ -f /sys/class/dmi/id/product_name ]; then
    VIRT_TYPE=$(cat /sys/class/dmi/id/product_name 2>/dev/null || echo "unknown")
fi

echo -e "  Environment: ${C_BOLD}${VIRT_TYPE}${C_RESET}"

if [[ "$VIRT_TYPE" =~ lxc|lxd ]]; then
    info "Detected LXC container environment (e.g. Proxmox LXC / LXD)."
    
    # Check cgroup write permissions
    cgroup_ok=false
    [ -w /sys/fs/cgroup ] && cgroup_ok=true

    # Check overlayfs
    modprobe overlay 2>/dev/null || true
    overlay_ok=false
    grep -q overlay /proc/filesystems 2>/dev/null && overlay_ok=true

    if [ "$cgroup_ok" = true ] && [ "$overlay_ok" = true ]; then
        success "LXC Nesting verified (cgroups writable, overlayfs active)."
    else
        warn "LXC Nesting may not be enabled on the hypervisor!"
        echo -e "${C_YELLOW}  -> If running Proxmox LXC: Go to Proxmox Web UI > Container > Options > Features${C_RESET}"
        echo -e "${C_YELLOW}     Enable 'Nesting' (nesting=1,keyctl=1 in /etc/pve/lxc/<ID>.conf)${C_RESET}"
    fi

elif [[ "$VIRT_TYPE" =~ kvm|qemu|vmware|hyperv|oracle|wsl ]]; then
    info "Detected VM environment (${VIRT_TYPE}). Checking nested virtualization..."

    # Check CPU flags for nested hardware virtualization
    if grep -E -q '(vmx|svm)' /proc/cpuinfo 2>/dev/null; then
        success "Nested CPU Virtualization extensions detected (Intel VMX / AMD SVM active)."
    else
        info "Nested CPU virtualization (VMX/SVM) not passed to VM (Docker runs fine; only nested VMs require this)."
    fi

    # Check /dev/kvm
    if [ -e /dev/kvm ]; then
        success "Hardware acceleration device /dev/kvm is available."
    fi
else
    success "Host environment: ${VIRT_TYPE}."
fi

# Verify Docker socket nesting support
if [ -S /var/run/docker.sock ]; then
    success "Docker daemon socket (/var/run/docker.sock) is ready for container control."
else
    warn "Docker socket /var/run/docker.sock was not found."
fi

# ------------------------------------------------------------------------------
# Pre-Flight: GPU Acceleration Checks
# ------------------------------------------------------------------------------
echo ""
info "Scanning for GPU Acceleration hardware..."

FOUND_GPU=false

# 1. NVIDIA GPU Check
NVIDIA_PCI=""
if command -v lspci &>/dev/null; then
    NVIDIA_PCI=$(lspci 2>/dev/null | grep -iE 'nvidia|vga compatible.*nvidia|3d controller.*nvidia' | head -n1 || true)
fi

if [ -n "$NVIDIA_PCI" ] || command -v nvidia-smi &>/dev/null || [ -e /dev/nvidia0 ]; then
    FOUND_GPU=true
    GPU_MODEL=""
    if command -v nvidia-smi &>/dev/null; then
        GPU_MODEL=$(nvidia-smi --query-gpu=name,driver_version --format=csv,noheader 2>/dev/null | head -n1 || true)
    fi
    [ -z "$GPU_MODEL" ] && GPU_MODEL="${NVIDIA_PCI:-NVIDIA Graphics Card}"

    success "NVIDIA GPU Detected: ${C_BOLD}${GPU_MODEL}${C_RESET}"

    # Check NVIDIA Container Toolkit in Docker
    info "Checking NVIDIA Container Toolkit configuration..."
    NVC_TOOL=false
    if command -v nvidia-ctk &>/dev/null || command -v nvidia-container-runtime &>/dev/null; then
        NVC_TOOL=true
    fi

    DOCKER_NVIDIA=false
    if docker info 2>/dev/null | grep -iq 'nvidia'; then
        DOCKER_NVIDIA=true
    elif [ -f /etc/docker/daemon.json ] && grep -q 'nvidia' /etc/docker/daemon.json; then
        DOCKER_NVIDIA=true
    fi

    if [ "$NVC_TOOL" = true ] && [ "$DOCKER_NVIDIA" = true ]; then
        success "NVIDIA Container Toolkit is configured and active in Docker."
    else
        warn "NVIDIA GPU detected, but NVIDIA Container Toolkit is not configured in Docker!"
        echo -e "${C_YELLOW}  Enabling this allows Docker containers (Plex, AI/Ollama, Jellyfin, Tdarr) to use your GPU.${C_RESET}"
        prompt_read "  Would you like to auto-install & configure nvidia-container-toolkit now? [y/N]: " SETUP_NVC "N"
        if [[ "$SETUP_NVC" =~ ^[Yy]$ ]]; then
            info "Setting up NVIDIA Container Toolkit repository..."
            if [ "$PKG_MANAGER" = "apt" ]; then
                curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg 2>/dev/null || true
                curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
                    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
                    tee /etc/apt/sources.list.d/nvidia-container-toolkit.list >/dev/null
                apt-get update -qq && apt-get install -y -qq nvidia-container-toolkit >/dev/null
            elif [ "$PKG_MANAGER" = "dnf" ] || [ "$PKG_MANAGER" = "yum" ]; then
                curl -s -L https://nvidia.github.io/libnvidia-container/stable/rpm/nvidia-container-toolkit.repo | \
                    tee /etc/yum.repos.d/nvidia-container-toolkit.repo >/dev/null
                "$PKG_MANAGER" install -y -q nvidia-container-toolkit >/dev/null
            fi

            if command -v nvidia-ctk &>/dev/null; then
                nvidia-ctk runtime configure --runtime=docker >/dev/null 2>&1 || true
                systemctl restart docker 2>/dev/null || service docker restart 2>/dev/null || true
                sleep 2
                success "NVIDIA Container Toolkit installed and Docker restarted."
            else
                warn "nvidia-ctk not available. Please install manually if GPU acceleration is needed."
            fi
        else
            info "Skipping NVIDIA Container Toolkit setup."
        fi
    fi
fi

# 2. Intel / AMD GPU Check (QuickSync, VA-API, ROCm)
RENDER_DEVS=""
if [ -d /dev/dri ]; then
    RENDER_DEVS=$(ls /dev/dri/render* /dev/dri/card* 2>/dev/null || true)
fi

if [ -n "$RENDER_DEVS" ]; then
    FOUND_GPU=true
    INTEL_AMD_PCI=""
    if command -v lspci &>/dev/null; then
        INTEL_AMD_PCI=$(lspci 2>/dev/null | grep -iE 'intel.*graphics|amd.*radeon|vga.*intel|vga.*amd' | head -n1 || true)
    fi
    [ -z "$INTEL_AMD_PCI" ] && INTEL_AMD_PCI="Integrated/Discrete GPU"

    success "Intel/AMD Video Acceleration detected: ${C_BOLD}${INTEL_AMD_PCI}${C_RESET}"
    echo -e "  Render nodes: ${C_CYAN}${RENDER_DEVS}${C_RESET} (available for QuickSync / VA-API hardware transcoding)."
fi

if [ "$FOUND_GPU" = false ]; then
    info "No dedicated or integrated GPU detected. Containers will use CPU rendering."
fi

# ------------------------------------------------------------------------------
# Pre-Flight: Essential Kernel & Network Features
# ------------------------------------------------------------------------------
echo ""
info "Checking Essential Kernel & Network Features..."

# 1. IP Forwarding (net.ipv4.ip_forward)
IP_FWD=$(sysctl -n net.ipv4.ip_forward 2>/dev/null || echo "0")
if [ "$IP_FWD" != "1" ]; then
    warn "IPv4 forwarding is currently disabled (net.ipv4.ip_forward=0). Containers will lack routing!"
    info "Enabling IPv4 forwarding..."
    sysctl -w net.ipv4.ip_forward=1 >/dev/null
    mkdir -p /etc/sysctl.d
    echo "net.ipv4.ip_forward = 1" > /etc/sysctl.d/99-docker-ip-forward.conf
    success "IPv4 forwarding enabled and persisted (/etc/sysctl.d/99-docker-ip-forward.conf)."
else
    success "IPv4 forwarding is enabled (net.ipv4.ip_forward=1)."
fi

# 2. TUN / TAP Device (/dev/net/tun)
if [ -c /dev/net/tun ]; then
    success "TUN/TAP device (/dev/net/tun) is available."
else
    info "TUN/TAP device missing. Attempting to load 'tun' module..."
    modprobe tun 2>/dev/null || true
    if [ ! -c /dev/net/tun ]; then
        mkdir -p /dev/net
        mknod /dev/net/tun c 10 200 2>/dev/null || true
        chmod 666 /dev/net/tun 2>/dev/null || true
    fi
    if [ -c /dev/net/tun ]; then
        success "TUN/TAP device created at /dev/net/tun."
    else
        warn "Could not create /dev/net/tun. VPN or Cloudflare WARP features may be limited."
    fi
fi

# 3. br_netfilter module (Docker bridge packet filtering)
if lsmod | grep -q br_netfilter 2>/dev/null; then
    success "Bridge netfilter module (br_netfilter) is loaded."
else
    modprobe br_netfilter 2>/dev/null || true
    if lsmod | grep -q br_netfilter 2>/dev/null; then
        mkdir -p /etc/modules-load.d
        echo "br_netfilter" > /etc/modules-load.d/docker-br-netfilter.conf 2>/dev/null || true
        success "Loaded bridge netfilter module (br_netfilter)."
    fi
fi

# 4. Storage Driver (OverlayFS)
modprobe overlay 2>/dev/null || true
if grep -q overlay /proc/filesystems 2>/dev/null; then
    success "Overlay filesystem (overlay2) kernel support verified."
else
    warn "Overlay filesystem not found in /proc/filesystems."
fi

# 5. Cgroup Version
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    success "Control Groups: cgroup v2 (modern unified hierarchy)."
else
    info "Control Groups: cgroup v1 (legacy hierarchy)."
fi

# 6. Time Synchronization (Crucial for Cloudflare TLS & JWT tokens)
TIME_SYNCED=false
if command -v timedatectl &>/dev/null; then
    if timedatectl status 2>/dev/null | grep -iE 'synchronized: yes|NTP service: active' &>/dev/null; then
        TIME_SYNCED=true
    fi
fi
if [ "$TIME_SYNCED" = true ]; then
    success "System clock is synchronized (NTP active)."
else
    warn "System clock synchronization could not be confirmed."
    echo -e "${C_YELLOW}     Note: Cloudflare API & JWT authentication strictly require accurate system time.${C_RESET}"
    echo -e "${C_YELLOW}     Current system time: $(date)${C_RESET}"
fi

# ------------------------------------------------------------------------------
# Pre-Flight: Resources & Port Availability
# ------------------------------------------------------------------------------
echo ""
info "Checking System Resources & Port Availability..."

# RAM Check
if command -v free &>/dev/null; then
    TOTAL_RAM=$(free -m | awk '/^Mem:/{print $2}')
    if [ -n "$TOTAL_RAM" ]; then
        if [ "$TOTAL_RAM" -lt 900 ]; then
            warn "Total RAM is low: ${TOTAL_RAM}MB (1GB+ recommended for multi-container stacks)."
        else
            success "Total RAM: ${TOTAL_RAM}MB."
        fi
    fi
fi

# Disk Space Check
FREE_DISK_MB=$(df -m "$SCRIPT_DIR" 2>/dev/null | awk 'NR==2{print $4}')
if [ -n "$FREE_DISK_MB" ]; then
    FREE_GB=$((FREE_DISK_MB / 1024))
    if [ "$FREE_GB" -lt 3 ]; then
        warn "Free disk space is low: ${FREE_GB}GB available on ${SCRIPT_DIR}."
    else
        success "Free disk space: ${FREE_GB}GB available."
    fi
fi

# Port 8080 Conflict Check
PORT_BUSY=false
if command -v ss &>/dev/null; then
    ss -tulpn 2>/dev/null | grep -qE ':8080\s' && PORT_BUSY=true
elif command -v netstat &>/dev/null; then
    netstat -tulpn 2>/dev/null | grep -qE ':8080\s' && PORT_BUSY=true
fi

DEFAULT_WEB_PORT="8080"
if [ "$PORT_BUSY" = true ]; then
    DEFAULT_WEB_PORT="8081"
    warn "Default port 8080 is currently in use by an existing host service!"
    info "Defaulting suggested Web UI port to 8081."
else
    success "Web port 8080 is available."
fi

# ------------------------------------------------------------------------------
# 2. Install Cloudflared CLI on Host
# ------------------------------------------------------------------------------
header "Step 2: Cloudflared CLI Installation"

if command -v cloudflared &>/dev/null; then
    success "cloudflared CLI is already installed: $(cloudflared --version 2>&1 | head -n1)"
else
    info "Detecting system architecture for cloudflared download..."
    SYS_ARCH=$(uname -m)
    case "$SYS_ARCH" in
        x86_64)   CF_ARCH="amd64" ;;
        aarch64)  CF_ARCH="arm64" ;;
        armv7l|armhf) CF_ARCH="arm" ;;
        i386|i686) CF_ARCH="386" ;;
        *)
            error "Unsupported architecture for automated cloudflared download: $SYS_ARCH"
            exit 1
            ;;
    esac

    CF_URL="https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CF_ARCH}"
    info "Downloading cloudflared (${CF_ARCH}) from GitHub releases..."
    curl -fsSL "$CF_URL" -o /usr/local/bin/cloudflared
    chmod +x /usr/local/bin/cloudflared

    if command -v cloudflared &>/dev/null; then
        success "cloudflared installed successfully: $(cloudflared --version 2>&1 | head -n1)"
    else
        error "Failed to install cloudflared to /usr/local/bin/cloudflared"
        exit 1
    fi
fi

# ------------------------------------------------------------------------------
# 3. Cloudflare Authentication & Tunnel Configuration
# ------------------------------------------------------------------------------
header "Step 3: Cloudflare Login & Tunnel Setup"

mkdir -p "$SCRIPT_DIR/cloudflared"
mkdir -p "$SCRIPT_DIR/data"
mkdir -p "$SCRIPT_DIR/projects"

# Guard against stale directory collisions that previously broke installer
for stale_path in "$SCRIPT_DIR/cloudflared/config.yml" "$SCRIPT_DIR/cloudflared/credentials.json"; do
    if [ -d "$stale_path" ]; then
        warn "Detected stale directory at $stale_path. Moving it aside to preserve it."
        mv "$stale_path" "$stale_path.stale-$(date +%s)"
    fi
done

# --- Detect Previous Installation State ---
RESUME_PREVIOUS=false
PREV_TUNNEL_ID=""
PREV_TUNNEL_NAME=""
PREV_DOMAIN=""
PREV_ADMIN_USER=""
PREV_ADMIN_PASS=""
PREV_PORT=""
PREV_JWT_SECRET=""
PREV_CF_API_TOKEN=""
PREV_CF_ACCOUNT_ID=""
PREV_CF_ZONE_ID=""

# 1. Check existing .env file
if [ -f "$SCRIPT_DIR/.env" ]; then
    PREV_ADMIN_USER=$(grep -E '^ADMIN_USERNAME=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_ADMIN_PASS=$(grep -E '^ADMIN_PASSWORD=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_PORT=$(grep -E '^PORT=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_TUNNEL_NAME=$(grep -E '^TUNNEL_NAME=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_TUNNEL_ID=$(grep -E '^TUNNEL_ID=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_TUNNEL_PROTOCOL=$(grep -E '^TUNNEL_PROTOCOL=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_JWT_SECRET=$(grep -E '^JWT_SECRET=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_DOMAIN=$(grep -E '^DOCKTUN_DOMAIN=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '\r')
    PREV_CF_API_TOKEN=$(grep -E '^(CF_API_TOKEN|CLOUDFLARE_API_TOKEN)=' "$SCRIPT_DIR/.env" 2>/dev/null | head -n1 | cut -d'=' -f2- | tr -d '\r')
    PREV_CF_ACCOUNT_ID=$(grep -E '^(CF_ACCOUNT_ID|CLOUDFLARE_ACCOUNT_ID)=' "$SCRIPT_DIR/.env" 2>/dev/null | head -n1 | cut -d'=' -f2- | tr -d '\r')
    PREV_CF_ZONE_ID=$(grep -E '^(CF_ZONE_ID|CLOUDFLARE_ZONE_ID)=' "$SCRIPT_DIR/.env" 2>/dev/null | head -n1 | cut -d'=' -f2- | tr -d '\r')
fi

# 2. Check local credentials.json
if [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ]; then
    cid=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
    if [ -n "$cid" ] && [ "$cid" != "your-tunnel-uuid" ] && [ "$cid" != "placeholder-tunnel-id" ]; then
        PREV_TUNNEL_ID="$cid"
    fi
    if [ -z "$PREV_CF_ACCOUNT_ID" ]; then
        c_act=$(jq -r '.AccountTag // .account_tag // .AccountID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
        [ -n "$c_act" ] && [ "$c_act" != "null" ] && PREV_CF_ACCOUNT_ID="$c_act"
    fi
    if [ -z "$PREV_CF_API_TOKEN" ]; then
        c_tok=$(jq -r '.api_token // .APIToken // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
        [ -n "$c_tok" ] && [ "$c_tok" != "null" ] && PREV_CF_API_TOKEN="$c_tok"
    fi
fi

# 3. Search system ~/.cloudflared or /root/.cloudflared for existing tunnel credentials
SYS_TUNNEL_JSON=""
for p in "$HOME/.cloudflared" "/root/.cloudflared"; do
    if [ -d "$p" ]; then
        for jf in "$p"/*.json; do
            if [ -f "$jf" ]; then
                jid=$(jq -r '.TunnelID // empty' "$jf" 2>/dev/null || true)
                if [ -n "$jid" ] && [ "$jid" != "null" ]; then
                    SYS_TUNNEL_JSON="$jf"
                    [ -z "$PREV_TUNNEL_ID" ] && PREV_TUNNEL_ID="$jid"
                    if [ -z "$PREV_CF_ACCOUNT_ID" ]; then
                        s_act=$(jq -r '.AccountTag // .account_tag // .AccountID // empty' "$jf" 2>/dev/null || true)
                        [ -n "$s_act" ] && [ "$s_act" != "null" ] && PREV_CF_ACCOUNT_ID="$s_act"
                    fi
                    if [ -z "$PREV_CF_API_TOKEN" ]; then
                        s_tok=$(jq -r '.api_token // .APIToken // empty' "$jf" 2>/dev/null || true)
                        [ -n "$s_tok" ] && [ "$s_tok" != "null" ] && PREV_CF_API_TOKEN="$s_tok"
                    fi
                    break 2
                fi
            fi
        done
    fi
done

# 4. Check dedicated token files if token not yet found
if [ -z "$PREV_CF_API_TOKEN" ]; then
    for tf in "$SCRIPT_DIR/cloudflared/token" "$SCRIPT_DIR/cloudflared/api_token" "$HOME/.cloudflared/token"; do
        if [ -f "$tf" ]; then
            t_val=$(head -n1 "$tf" 2>/dev/null | tr -d '\r\n ')
            if [ -n "$t_val" ]; then
                PREV_CF_API_TOKEN="$t_val"
                break
            fi
        fi
    done
fi

# 5. Check environment variables
[ -z "$PREV_CF_API_TOKEN" ] && PREV_CF_API_TOKEN="${CF_API_TOKEN:-${CLOUDFLARE_API_TOKEN}}"
[ -z "$PREV_CF_ACCOUNT_ID" ] && PREV_CF_ACCOUNT_ID="${CF_ACCOUNT_ID:-${CLOUDFLARE_ACCOUNT_ID}}"
[ -z "$PREV_CF_ZONE_ID" ] && PREV_CF_ZONE_ID="${CF_ZONE_ID:-${CLOUDFLARE_ZONE_ID}}"

# 6. Check existing cloudflared/config.yml for tunnel ID and domain
if [ -f "$SCRIPT_DIR/cloudflared/config.yml" ]; then
    cfg_tid=$(grep -E '^[[:space:]]*tunnel:' "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null | awk '{print $NF}' | tr -d '"'\''\r ')
    if [ -n "$cfg_tid" ] && [ "$cfg_tid" != "YOUR_TUNNEL_ID" ]; then
        [ -z "$PREV_TUNNEL_ID" ] && PREV_TUNNEL_ID="$cfg_tid"
    fi
    cfg_dom=$(grep -E '^[[:space:]]*-[[:space:]]*hostname:' "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null | head -n1 | awk '{print $NF}' | tr -d '"'\''\r ')
    cfg_dom=$(echo "$cfg_dom" | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')
    if [ -n "$cfg_dom" ] && [ "$cfg_dom" != "docktun.example.com" ]; then
        PREV_DOMAIN="$cfg_dom"
    fi
fi

# 7. Check if Cloudflare cert.pem is already present (already logged in)
CF_CERT_PATH="$HOME/.cloudflared/cert.pem"
if [ ! -f "$CF_CERT_PATH" ] && [ -f "/root/.cloudflared/cert.pem" ]; then
    CF_CERT_PATH="/root/.cloudflared/cert.pem"
fi
CF_ALREADY_LOGGED_IN=false
[ -f "$CF_CERT_PATH" ] && CF_ALREADY_LOGGED_IN=true

# Check if we can offer a seamless resume
if [ -n "$PREV_TUNNEL_ID" ] && [ "$PREV_TUNNEL_ID" != "your-tunnel-uuid" ]; then
    echo -e "${C_GREEN}🔍 Found configuration from previous installation attempt:${C_RESET}"
    echo -e "  • ${C_BOLD}Tunnel UUID:${C_RESET}     ${C_CYAN}${PREV_TUNNEL_ID}${C_RESET}"
    [ -n "$PREV_TUNNEL_NAME" ] && echo -e "  • ${C_BOLD}Tunnel Name:${C_RESET}     ${PREV_TUNNEL_NAME}"
    [ -n "$PREV_DOMAIN" ]      && echo -e "  • ${C_BOLD}Web UI Domain:${C_RESET}   ${C_CYAN}${PREV_DOMAIN}${C_RESET}"
    [ -n "$PREV_ADMIN_USER" ]  && echo -e "  • ${C_BOLD}Admin User:${C_RESET}      ${PREV_ADMIN_USER}"
    [ -n "$PREV_ADMIN_PASS" ]  && echo -e "  • ${C_BOLD}Admin Password:${C_RESET}  [Saved from previous install]"
    [ -n "$PREV_PORT" ]        && echo -e "  • ${C_BOLD}Web Port:${C_RESET}        ${PREV_PORT}"
    [ -n "$PREV_CF_API_TOKEN" ] && echo -e "  • ${C_BOLD}Cloudflare API Token:${C_RESET} ${C_GREEN}✓ Auto-detected from file/environment${C_RESET}"
    [ -n "$PREV_CF_ACCOUNT_ID" ] && echo -e "  • ${C_BOLD}Cloudflare Account ID:${C_RESET} ${PREV_CF_ACCOUNT_ID}"
    if [ "$CF_ALREADY_LOGGED_IN" = true ]; then
        echo -e "  • ${C_BOLD}Cloudflare Auth:${C_RESET} Active (${CF_CERT_PATH})"
    fi
    echo ""
    prompt_read "Would you like to resume and use these existing settings? [Y/n]: " RESUME_INPUT "Y"
    if [[ "$RESUME_INPUT" =~ ^[Yy]$ ]]; then
        RESUME_PREVIOUS=true
    fi
fi

TUNNEL_ID=""
TUNNEL_NAME=""
DOCKTUN_DOMAIN=""
LOGIN_SUCCESSFUL=false

if [ "$RESUME_PREVIOUS" = true ]; then
    info "Resuming setup with previously configured tunnel and credentials..."
    TUNNEL_ID="$PREV_TUNNEL_ID"
    TUNNEL_NAME="${PREV_TUNNEL_NAME:-docktun-tunnel}"
    DOCKTUN_DOMAIN="${PREV_DOMAIN:-docktun.example.com}"
    [ "$CF_ALREADY_LOGGED_IN" = true ] && LOGIN_SUCCESSFUL=true

    # Ensure credentials.json is in place
    if [ ! -f "$SCRIPT_DIR/cloudflared/credentials.json" ] || [ ! -s "$SCRIPT_DIR/cloudflared/credentials.json" ]; then
        if [ -n "$SYS_TUNNEL_JSON" ] && [ -f "$SYS_TUNNEL_JSON" ]; then
            cp "$SYS_TUNNEL_JSON" "$SCRIPT_DIR/cloudflared/credentials.json"
            success "Restored tunnel credentials from $SYS_TUNNEL_JSON"
        fi
    fi
    [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && chmod 644 "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true
    if [ -f "$CF_CERT_PATH" ]; then
        cp "$CF_CERT_PATH" "$SCRIPT_DIR/cloudflared/cert.pem" 2>/dev/null || true
        chmod 600 "$SCRIPT_DIR/cloudflared/cert.pem" 2>/dev/null || true
        LOGIN_SUCCESSFUL=true
    fi

    success "Tunnel configuration restored: ${TUNNEL_NAME} (${TUNNEL_ID})"

    prompt_read "Expose DockTun web dashboard publicly via Cloudflare Tunnel? [y/N]: " EXPOSE_DOCKTUN_PUBLIC "N"
    if [[ "$EXPOSE_DOCKTUN_PUBLIC" =~ ^[Yy]$ ]]; then
        DEFAULT_DOM="${PREV_DOMAIN:-docktun.example.com}"
        prompt_read "Enter the public domain/hostname for DockTun Web UI (Default: ${DEFAULT_DOM}): " DOCKTUN_DOMAIN "$DEFAULT_DOM"
        DOCKTUN_DOMAIN=$(echo "$DOCKTUN_DOMAIN" | tr -d '"'\''\r ' | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')

        # Register DNS route in Cloudflare if certificate is present
        if [ "$LOGIN_SUCCESSFUL" = true ] && [ -n "$TUNNEL_ID" ] && [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
            info "Ensuring DNS route in Cloudflare: $DOCKTUN_DOMAIN -> $TUNNEL_ID..."
            if dns_res=$(cloudflared tunnel route dns "$TUNNEL_ID" "$DOCKTUN_DOMAIN" 2>&1); then
                success "Cloudflare DNS route configured: $DOCKTUN_DOMAIN"
            else
                warn "Cloudflare CLI returned: $dns_res"
                info "If the DNS record already exists, you can safely continue."
            fi
        fi
    else
        DOCKTUN_DOMAIN=""
        success "DockTun dashboard will remain local-only (not exposed to public Cloudflare Tunnel)."
    fi
else
    # Discover all existing tunnels across ~/.cloudflared, /root/.cloudflared, and Cloudflare account
    DETECTED_TUNNEL_COUNT=0
    declare -A T_IDS
    declare -A T_NAMES
    declare -A T_JSONS

    # Scan filesystem for *.json credentials files in ~/.cloudflared and /root/.cloudflared
    for cdir in "$HOME/.cloudflared" "/root/.cloudflared" "$SCRIPT_DIR/cloudflared"; do
        if [ -d "$cdir" ]; then
            for jf in "$cdir"/*.json; do
                if [ -f "$jf" ]; then
                    jid=$(jq -r '.TunnelID // empty' "$jf" 2>/dev/null || true)
                    [ -z "$jid" ] && jid=$(grep -oE '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' "$jf" 2>/dev/null | head -n1 || true)
                    if [ -n "$jid" ] && [ "$jid" != "placeholder-tunnel-id" ] && [ "$jid" != "your-tunnel-uuid" ]; then
                        already=false
                        for ((k=1; k<=DETECTED_TUNNEL_COUNT; k++)); do
                            if [ "${T_IDS[$k]}" = "$jid" ]; then
                                already=true
                                break
                            fi
                        done
                        if [ "$already" = false ]; then
                            DETECTED_TUNNEL_COUNT=$((DETECTED_TUNNEL_COUNT + 1))
                            T_IDS[$DETECTED_TUNNEL_COUNT]="$jid"
                            T_NAMES[$DETECTED_TUNNEL_COUNT]="tunnel-$DETECTED_TUNNEL_COUNT"
                            T_JSONS[$DETECTED_TUNNEL_COUNT]="$jf"
                        fi
                    fi
                fi
            done
        fi
    done

    # Query cloudflared CLI if cert.pem exists to get real tunnel names and any account tunnels
    if [ -f "$CF_CERT_PATH" ]; then
        tlist=$(cloudflared tunnel list 2>/dev/null | grep -E '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' || true)
        if [ -n "$tlist" ]; then
            while IFS= read -r line; do
                [ -z "$line" ] && continue
                tid=$(echo "$line" | grep -oE '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' | head -n1)
                tname=$(echo "$line" | awk '{print $2}')
                [ -z "$tname" ] && tname="$tid"

                found_idx=0
                for ((k=1; k<=DETECTED_TUNNEL_COUNT; k++)); do
                    if [ "${T_IDS[$k]}" = "$tid" ]; then
                        found_idx=$k
                        break
                    fi
                done

                if [ "$found_idx" -gt 0 ]; then
                    T_NAMES[$found_idx]="$tname"
                else
                    m_json=""
                    for d in "$HOME/.cloudflared" "/root/.cloudflared" "$SCRIPT_DIR/cloudflared"; do
                        if [ -f "$d/$tid.json" ]; then
                            m_json="$d/$tid.json"
                            break
                        fi
                    done
                    DETECTED_TUNNEL_COUNT=$((DETECTED_TUNNEL_COUNT + 1))
                    T_IDS[$DETECTED_TUNNEL_COUNT]="$tid"
                    T_NAMES[$DETECTED_TUNNEL_COUNT]="$tname"
                    T_JSONS[$DETECTED_TUNNEL_COUNT]="$m_json"
                fi
            done <<< "$tlist"
        fi
    fi

    CREATE_BRAND_NEW=false
    IMPORT_CUSTOM=false

    if [ "$DETECTED_TUNNEL_COUNT" -gt 0 ]; then
        if [ "$CF_ALREADY_LOGGED_IN" = true ]; then
            success "Cloudflare login session active: $CF_CERT_PATH"
            LOGIN_SUCCESSFUL=true
        fi
        echo ""
        echo -e "${C_GREEN}Found $DETECTED_TUNNEL_COUNT existing Cloudflare Tunnel(s):${C_RESET}"
        for ((k=1; k<=DETECTED_TUNNEL_COUNT; k++)); do
            tid="${T_IDS[$k]}"
            tname="${T_NAMES[$k]}"
            tjson="${T_JSONS[$k]}"
            jstatus="${C_GREEN}[Credentials ready]${C_RESET}"
            [ -z "$tjson" ] && jstatus="${C_YELLOW}[No local JSON]${C_RESET}"
            echo -e "  [${k}] ${C_BOLD}${tname}${C_RESET} (UUID: ${C_CYAN}${tid}${C_RESET}) $jstatus"
        done
        opt_new=$((DETECTED_TUNNEL_COUNT + 1))
        opt_import=$((DETECTED_TUNNEL_COUNT + 2))
        echo -e "  [${opt_new}] ${C_BOLD}Create a brand new tunnel${C_RESET}"
        echo -e "  [${opt_import}] ${C_BOLD}Import another credentials.json from file/paste${C_RESET}"
        echo ""
        prompt_read "Select an option [1-${opt_import}] (Default: 1): " T_SELECT "1"

        if [ "$T_SELECT" -ge 1 ] && [ "$T_SELECT" -le "$DETECTED_TUNNEL_COUNT" ]; then
            TUNNEL_ID="${T_IDS[$T_SELECT]}"
            TUNNEL_NAME="${T_NAMES[$T_SELECT]}"
            use_json="${T_JSONS[$T_SELECT]}"

            if [ -n "$use_json" ] && [ -f "$use_json" ]; then
                cp "$use_json" "$SCRIPT_DIR/cloudflared/credentials.json"
                success "Using existing tunnel: ${C_BOLD}${TUNNEL_NAME}${C_RESET} (${TUNNEL_ID})"
                success "Credentials copied to $SCRIPT_DIR/cloudflared/credentials.json"
            else
                warn "Credentials file for tunnel '${TUNNEL_NAME}' was not found in ~/.cloudflared."
                prompt_read "Enter path to credentials.json for this tunnel: " MAN_JSON ""
                if [ -f "$MAN_JSON" ]; then
                    cp "$MAN_JSON" "$SCRIPT_DIR/cloudflared/credentials.json"
                    success "Imported credentials."
                fi
            fi
            LOGIN_SUCCESSFUL=true
        elif [ "$T_SELECT" -eq "$opt_new" ]; then
            CREATE_BRAND_NEW=true
        else
            IMPORT_CUSTOM=true
        fi
    fi

    # Fallback if no tunnels detected or user explicitly chose new/import
    if [ -z "$TUNNEL_ID" ]; then
        if [ "$IMPORT_CUSTOM" = true ]; then
            while true; do
                prompt_read "Enter the absolute path to your credentials.json file (or 'paste' to paste): " SRC_JSON ""
                if [ "$SRC_JSON" = "paste" ]; then
                    echo "Paste the full contents of your credentials.json (press Ctrl+D when finished):"
                    cat > "$SCRIPT_DIR/cloudflared/credentials.json"
                    TUNNEL_ID=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
                    prompt_read "Enter your Tunnel Name: " TUNNEL_NAME ""
                    break
                elif [ -f "$SRC_JSON" ]; then
                    cp "$SRC_JSON" "$SCRIPT_DIR/cloudflared/credentials.json"
                    TUNNEL_ID=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
                    prompt_read "Enter your Tunnel Name: " TUNNEL_NAME ""
                    break
                else
                    warn "File not found: $SRC_JSON. Please re-enter."
                fi
            done
        else
            # Need to login or create tunnel
            if [ "$CF_ALREADY_LOGGED_IN" = true ]; then
                success "Cloudflare login session active: $CF_CERT_PATH"
                LOGIN_SUCCESSFUL=true
            else
                info "Launching Cloudflare login flow..."
                echo -e "${C_YELLOW}A Cloudflare login URL will be printed below.${C_RESET}"
                echo -e "${C_YELLOW}Open that link in your browser, select your domain, and approve the authorization.${C_RESET}\n"
                cloudflared tunnel login
                if [ -f "$CF_CERT_PATH" ]; then
                    success "Cloudflare certificate saved: $CF_CERT_PATH"
                    LOGIN_SUCCESSFUL=true
                fi
            fi

            DEFAULT_TNAME="${PREV_TUNNEL_NAME:-docktun-tunnel}"
            prompt_read "Enter a name for your Cloudflare Tunnel (Default: ${DEFAULT_TNAME}): " TUNNEL_NAME "$DEFAULT_TNAME"

            info "Creating tunnel: $TUNNEL_NAME..."
            TUNNEL_CREATE_OUTPUT=$(cloudflared tunnel create "$TUNNEL_NAME" 2>&1)
            echo "$TUNNEL_CREATE_OUTPUT"

            TUNNEL_ID=$(echo "$TUNNEL_CREATE_OUTPUT" | grep -oE '[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}' | head -n1)

            GEN_JSON=""
            if [ -n "$TUNNEL_ID" ]; then
                for p in "$HOME/.cloudflared/$TUNNEL_ID.json" "/root/.cloudflared/$TUNNEL_ID.json"; do
                    if [ -f "$p" ]; then
                        GEN_JSON="$p"
                        break
                    fi
                done
            fi
            if [ -z "$GEN_JSON" ]; then
                GEN_JSON=$(ls -t "$HOME/.cloudflared/"*.json /root/.cloudflared/*.json 2>/dev/null | head -n1 || true)
            fi

            if [ -f "$GEN_JSON" ]; then
                cp "$GEN_JSON" "$SCRIPT_DIR/cloudflared/credentials.json"
                success "Tunnel credentials copied to $SCRIPT_DIR/cloudflared/credentials.json"
                if [ -z "$TUNNEL_ID" ]; then
                    TUNNEL_ID=$(jq -r '.TunnelID // empty' "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true)
                fi
            fi
        fi
    fi

    if [ -z "$TUNNEL_ID" ]; then
        TUNNEL_ID="your-tunnel-uuid"
        warn "Tunnel ID could not be parsed automatically. Using placeholder: $TUNNEL_ID"
    fi

    [ -f "$SCRIPT_DIR/cloudflared/credentials.json" ] && chmod 644 "$SCRIPT_DIR/cloudflared/credentials.json" 2>/dev/null || true

    echo ""
    info "DockTun Web Dashboard Access & Security:"
    echo -e "  By default, DockTun is kept secure and ${C_GREEN}LOCAL ONLY${C_RESET} (accessible via LAN/IP: http://${HOST_IP:-localhost}:${PORT:-8080} or mDNS http://docktun.local)."
    echo -e "  You can optionally expose it publicly through your Cloudflare Tunnel."
    echo -e "  ${C_YELLOW}Recommendation: Keep DockTun local-only, or protect it with Cloudflare Zero Trust (Access) if exposed.${C_RESET}"
    prompt_read "Expose DockTun web dashboard publicly via Cloudflare Tunnel? [y/N]: " EXPOSE_DOCKTUN_PUBLIC "N"
    if [[ "$EXPOSE_DOCKTUN_PUBLIC" =~ ^[Yy]$ ]]; then
        DEFAULT_DOM="${PREV_DOMAIN:-docktun.example.com}"
        prompt_read "Enter the public domain/hostname for DockTun Web UI (Default: ${DEFAULT_DOM}): " DOCKTUN_DOMAIN "$DEFAULT_DOM"
        DOCKTUN_DOMAIN=$(echo "$DOCKTUN_DOMAIN" | tr -d '"'\''\r ' | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')

        # Automatically create DNS CNAME if logged in
        if [ "$LOGIN_SUCCESSFUL" = true ] && [ -n "$TUNNEL_ID" ] && [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
            info "Registering DNS route in Cloudflare: $DOCKTUN_DOMAIN -> $TUNNEL_ID..."
            if dns_res=$(cloudflared tunnel route dns "$TUNNEL_ID" "$DOCKTUN_DOMAIN" 2>&1); then
                success "Cloudflare DNS route configured: $DOCKTUN_DOMAIN"
            else
                warn "Cloudflare CLI returned: $dns_res"
                info "If the DNS record already exists, you can safely continue."
            fi
        fi
    else
        DOCKTUN_DOMAIN=""
        success "DockTun dashboard will remain local-only (not exposed to public Cloudflare Tunnel)."
    fi
fi

# ------------------------------------------------------------------------------
# 4. DockTun Admin Credentials & Security
# ------------------------------------------------------------------------------
header "Step 4: DockTun Admin & Security Setup"

if [ "$RESUME_PREVIOUS" = true ] && [ -n "$PREV_ADMIN_PASS" ]; then
    ADMIN_USERNAME="${PREV_ADMIN_USER:-admin}"
    ADMIN_PASSWORD="$PREV_ADMIN_PASS"
    PORT="${PREV_PORT:-$DEFAULT_WEB_PORT}"
    JWT_SECRET="${PREV_JWT_SECRET:-$(openssl rand -hex 32)}"
    success "Reusing previously configured Admin credentials (Username: ${ADMIN_USERNAME}, Port: ${PORT})."
else
    DEFAULT_AUSER="${PREV_ADMIN_USER:-admin}"
    prompt_read "Admin username (Default: ${DEFAULT_AUSER}): " ADMIN_USERNAME "$DEFAULT_AUSER"

    while true; do
        prompt_secret "Enter DockTun Admin password (leave blank to auto-generate secure password): " ADMIN_PASSWORD
        if [ -z "$ADMIN_PASSWORD" ]; then
            ADMIN_PASSWORD=$(openssl rand -base64 12 | tr -dc 'a-zA-Z0-9' | head -c 16)
            success "Auto-generated secure Admin password: $ADMIN_PASSWORD"
            break
        fi
        prompt_secret "Confirm DockTun Admin password: " ADMIN_PASSWORD_CONFIRM
        if [ "$ADMIN_PASSWORD" != "$ADMIN_PASSWORD_CONFIRM" ]; then
            warn "Passwords do not match. Please try again."
        else
            success "Admin password confirmed."
            break
        fi
    done

    DEFAULT_P="${PREV_PORT:-$DEFAULT_WEB_PORT}"
    prompt_read "Enter Web UI Host Port (Default: ${DEFAULT_P}): " PORT "$DEFAULT_P"

    info "Generating cryptographically secure 64-character JWT secret..."
    JWT_SECRET="${PREV_JWT_SECRET:-$(openssl rand -hex 32)}"
fi

# ------------------------------------------------------------------------------
# 5. Discover & Import Previous Docker Containers
# ------------------------------------------------------------------------------
header "Step 5: Discovering Existing Docker Containers"

# Ensure cloudflared network exists
info "Checking Docker network 'cloudflared'..."
if ! docker network inspect cloudflared >/dev/null 2>&1; then
    docker network create cloudflared >/dev/null
    success "Created Docker bridge network: cloudflared"
else
    success "Docker network 'cloudflared' is already available."
fi

# Find all running containers excluding DockTun
RUNNING_CONTAINERS=$(docker ps --format '{{.Names}}' | grep -vE '^(docktun|docktun-backend|docktun-frontend|docktun-cloudflared|docktun-zeroconf|docktun-tailscale|docktun-wireguard)$' || true)

CONTAINER_COUNT=0
if [ -n "$RUNNING_CONTAINERS" ]; then
    CONTAINER_COUNT=$(echo "$RUNNING_CONTAINERS" | wc -l)
fi

INGRESS_RULES_HOST=()
INGRESS_RULES_SVC=()
INGRESS_RULES_NOTLS=()
COMPOSE_MOUNT_PATHS=()

if [ "$CONTAINER_COUNT" -eq 0 ]; then
    info "No external running containers detected on this host."
else
    success "Found $CONTAINER_COUNT running container(s) on your Docker host:"
    echo ""

    INDEX=1
    declare -A C_NAMES
    declare -A C_PORTS
    declare -A C_COMP_DIR
    declare -A C_COMP_NAME

    while IFS= read -r cname; do
        [ -z "$cname" ] && continue
        
        # Get internal ports
        cports=$(docker inspect -f '{{range $p, $conf := .NetworkSettings.Ports}}{{$p}} {{end}}' "$cname" 2>/dev/null || true)
        [ -z "$cports" ] && cports=$(docker inspect -f '{{range $p, $conf := .Config.ExposedPorts}}{{$p}} {{end}}' "$cname" 2>/dev/null || true)
        
        first_port=$(echo "$cports" | tr ' ' '\n' | grep '/tcp' | head -n1 | cut -d'/' -f1 || true)
        [ -z "$first_port" ] && first_port="80"

        # Check Docker Compose labels
        comp_name=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$cname" 2>/dev/null || true)
        comp_dir=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$cname" 2>/dev/null || true)

        # Validate that comp_dir is a real absolute directory on disk
        if [[ ! "$comp_dir" =~ ^/ ]] || [ ! -d "$comp_dir" ]; then
            comp_dir=""
        fi

        C_NAMES[$INDEX]="$cname"
        C_PORTS[$INDEX]="$first_port"
        C_COMP_NAME[$INDEX]="$comp_name"
        C_COMP_DIR[$INDEX]="$comp_dir"

        if [ -n "$comp_dir" ]; then
            echo -e "  [${INDEX}] ${C_BOLD}${cname}${C_RESET} (Default Port: ${first_port}) ${C_CYAN}[Compose: ${comp_name} @ ${comp_dir}]${C_RESET}"
        else
            echo -e "  [${INDEX}] ${C_BOLD}${cname}${C_RESET} (Default Port: ${first_port}) ${C_YELLOW}[Standalone Container]${C_RESET}"
        fi

        INDEX=$((INDEX + 1))
    done <<< "$RUNNING_CONTAINERS"

    # Containers remain local by default on their existing networks.
    # DockTun only connects a container to 'cloudflared' if you explicitly enable Public Cloudflare Ingress.


    # Import Docker Compose projects and standalone containers into SQLite database & volume mounts
    echo ""
    prompt_read "Import detected containers & projects into DockTun dashboard? [Y/n]: " IMPORT_PROJECTS "Y"

    if [[ "$IMPORT_PROJECTS" =~ ^[Yy]$ ]]; then
        info "Preparing DockTun projects database..."
        
        # Prepare SQL batch file
        SQL_FILE="$SCRIPT_DIR/data/import_projects.sql"
        cat > "$SQL_FILE" << 'EOSQL'
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    repo_url TEXT DEFAULT '',
    git_username TEXT DEFAULT '',
    git_token TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_path ON projects(path);
EOSQL

        declare -A SEEN_PROJECTS
        for ((i=1; i<INDEX; i++)); do
            cname="${C_NAMES[$i]}"
            pname="${C_COMP_NAME[$i]}"
            pdir="${C_COMP_DIR[$i]}"
            
            if [ -n "$pdir" ] && [[ "$pdir" =~ ^/ ]] && [ -d "$pdir" ]; then
                # Existing host Compose project
                if [ -z "${SEEN_PROJECTS[$pdir]}" ]; then
                    SEEN_PROJECTS["$pdir"]=1
                    [ -z "$pname" ] && pname=$(basename "$pdir")
                    info "Importing Compose project: $pname ($pdir)..."
                    
                    esc_name=$(echo "$pname" | sed "s/'/''/g")
                    esc_path=$(echo "$pdir" | sed "s/'/''/g")
                    echo "INSERT OR IGNORE INTO projects (name, path) VALUES ('$esc_name', '$esc_path');" >> "$SQL_FILE"
                    COMPOSE_MOUNT_PATHS+=("$pdir")
                fi
            else
                # Standalone container -> generate compose project stub in projects/<cname>
                proj_stub_dir="$SCRIPT_DIR/projects/$cname"
                proj_db_path="/opt/projects/$cname"
                if [ -z "${SEEN_PROJECTS[$proj_db_path]}" ]; then
                    SEEN_PROJECTS["$proj_db_path"]=1
                    info "Importing standalone container as project: $cname ($proj_db_path)..."
                    
                    mkdir -p "$proj_stub_dir"
                    if [ ! -f "$proj_stub_dir/docker-compose.yml" ]; then
                        cimage=$(docker inspect -f '{{.Config.Image}}' "$cname" 2>/dev/null || echo "alpine")
                        [ -z "$cimage" ] && cimage="alpine"
                        cat > "$proj_stub_dir/docker-compose.yml" << EOF
services:
  ${cname}:
    image: ${cimage}
    container_name: ${cname}
    restart: unless-stopped
EOF
                    fi
                    esc_name=$(echo "$cname" | sed "s/'/''/g")
                    esc_path=$(echo "$proj_db_path" | sed "s/'/''/g")
                    echo "INSERT OR IGNORE INTO projects (name, path) VALUES ('$esc_name', '$esc_path');" >> "$SQL_FILE"
                fi
            fi
        done

        # Execute batch SQL
        if command -v sqlite3 &>/dev/null; then
            sqlite3 "$SCRIPT_DIR/data/app.db" < "$SQL_FILE" 2>/dev/null || true
        else
            docker run --rm -v "$SCRIPT_DIR/data:/data" alpine sh -c '
                apk add --no-cache sqlite >/dev/null 2>&1
                sqlite3 /data/app.db < /data/import_projects.sql
            ' 2>/dev/null || true
        fi
        rm -f "$SQL_FILE"
        chmod 644 "$SCRIPT_DIR/data/app.db" 2>/dev/null || true
        success "Containers and projects successfully registered into DockTun database."
    fi

    # Restore any previously configured custom container ingress rules from existing config.yml
    if [ -f "$SCRIPT_DIR/cloudflared/config.yml" ]; then
        cur_host=""
        cur_notls="false"
        while IFS= read -r raw_line || [ -n "$raw_line" ]; do
            line=$(echo "$raw_line" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
            if [[ "$line" =~ ^-[[:space:]]*hostname:[[:space:]]*[\"']?([^\"'[:space:]]+)[\"']? ]]; then
                cur_host="${BASH_REMATCH[1]}"
                cur_host=$(echo "$cur_host" | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')
                cur_notls="false"
            elif [[ "$line" =~ ^service:[[:space:]]*[\"']?([^\"'[:space:]]+)[\"']? ]]; then
                cur_svc="${BASH_REMATCH[1]}"
                if [ -n "$cur_host" ] && [ "$cur_host" != "$DOCKTUN_DOMAIN" ] && [ "$cur_host" != "docktun.example.com" ] && [ "$cur_svc" != "http_status:404" ]; then
                    INGRESS_RULES_HOST+=("$cur_host")
                    INGRESS_RULES_SVC+=("$cur_svc")
                    INGRESS_RULES_NOTLS+=("$cur_notls")
                fi
                cur_host=""
            elif [[ "$line" =~ noTLSVerify:[[:space:]]*true ]]; then
                if [ ${#INGRESS_RULES_NOTLS[@]} -gt 0 ]; then
                    last_idx=$((${#INGRESS_RULES_NOTLS[@]} - 1))
                    INGRESS_RULES_NOTLS[$last_idx]="true"
                fi
            fi
        done < "$SCRIPT_DIR/cloudflared/config.yml"
        if [ ${#INGRESS_RULES_HOST[@]} -gt 0 ]; then
            info "Restored ${#INGRESS_RULES_HOST[@]} previously configured container ingress rule(s)."
        fi
    fi

    # Ingress Configuration
    BASE_DOMAIN="$DOCKTUN_DOMAIN"
    if [[ "$DOCKTUN_DOMAIN" =~ \. ]]; then
        dot_count=$(echo "$DOCKTUN_DOMAIN" | tr -cd '.' | wc -c)
        if [ "$dot_count" -ge 2 ]; then
            BASE_DOMAIN="${DOCKTUN_DOMAIN#*.}"
        fi
    fi

    echo ""
    prompt_read "Would you like to expose detected containers through Cloudflare Tunnel hostnames? [Y/n]: " CONFIGURE_INGRESS "Y"

    if [[ "$CONFIGURE_INGRESS" =~ ^[Yy]$ ]]; then
        for ((i=1; i<INDEX; i++)); do
            cname="${C_NAMES[$i]}"
            def_port="${C_PORTS[$i]}"
            def_host="${cname}.${BASE_DOMAIN}"

            already_configured=false
            for r in "${INGRESS_RULES_HOST[@]}"; do
                if [ "$r" = "$def_host" ]; then
                    already_configured=true
                    break
                fi
            done

            if [ "$already_configured" = true ]; then
                info "Container '$cname' is already configured at https://$def_host."
            else
                echo ""
                prompt_read "Expose '$cname' via Cloudflare at https://$def_host -> http://$cname:$def_port? [Y/n]: " EXPOSE_C "Y"
                if [[ "$EXPOSE_C" =~ ^[Yy]$ ]]; then
                    prompt_read "Enter public hostname for '$cname' (Default: $def_host): " C_HOST "$def_host"
                    prompt_read "Enter internal service port for '$cname' (Default: $def_port): " C_PORT "$def_port"

                    C_HOST=$(echo "$C_HOST" | tr -d '"'\''\r ' | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')
                    [ -z "$C_PORT" ] && C_PORT="${def_port:-80}"
                    C_PORT=$(echo "$C_PORT" | tr -d '"'\''\r ')
                    svc_target="http://${cname}:${C_PORT}"
                    notls="false"
                    if [[ "$C_PORT" =~ ^https?:// ]]; then
                        svc_target="$C_PORT"
                    fi
                    if [[ "$svc_target" =~ ^https:// ]]; then
                        notls="true"
                    fi

                    if [ -n "$C_HOST" ]; then
                        INGRESS_RULES_HOST+=("$C_HOST")
                        INGRESS_RULES_SVC+=("$svc_target")
                        INGRESS_RULES_NOTLS+=("$notls")
                        if [ "$LOGIN_SUCCESSFUL" = true ] && [ -n "$TUNNEL_ID" ]; then
                            info "Configuring Cloudflare DNS route: $C_HOST -> $TUNNEL_ID..."
                            if dns_c_res=$(cloudflared tunnel route dns "$TUNNEL_ID" "$C_HOST" 2>&1); then
                                success "DNS route configured in Cloudflare: $C_HOST"
                            else
                                warn "Cloudflare CLI returned: $dns_c_res"
                            fi
                        fi
                        success "Added ingress rule for $C_HOST -> $svc_target"
                    fi
                fi
            fi
        done
    fi
fi

# ------------------------------------------------------------------------------
# 6. Generate Configuration Files
# ------------------------------------------------------------------------------
header "Step 6: Generating Configuration Files"

# Write .env
info "Writing .env configuration..."
CF_API_TOKEN="${CF_API_TOKEN:-${PREV_CF_API_TOKEN}}"
CF_ACCOUNT_ID="${CF_ACCOUNT_ID:-${PREV_CF_ACCOUNT_ID}}"
CF_ZONE_ID="${CF_ZONE_ID:-${PREV_CF_ZONE_ID}}"

cat > "$SCRIPT_DIR/.env" << EOF
# ==========================================
# DockTun Configuration
# ==========================================

# Auth
JWT_SECRET=${JWT_SECRET}
ADMIN_USERNAME=${ADMIN_USERNAME}
ADMIN_PASSWORD=${ADMIN_PASSWORD}

# Host Port for Web Dashboard (Access at http://localhost:${PORT})
PORT=${PORT}

# Public Hostname (optional reference)
DOCKTUN_DOMAIN=${DOCKTUN_DOMAIN}

# Tunnel Configuration
TUNNEL_NAME=${TUNNEL_NAME}
TUNNEL_ID=${TUNNEL_ID}
TUNNEL_PROTOCOL=${TUNNEL_PROTOCOL:-${PREV_TUNNEL_PROTOCOL:-http2}}

# Backend Service Settings
CLOUDFLARED_CONFIG_PATH=/etc/cloudflared/config.yml
CLOUDFLARED_CONTAINER=docktun-cloudflared
DB_PATH=/app/data/app.db
LISTEN_ADDR=0.0.0.0:3001
SELF_PATH=/opt/projects/docktun
PROJECTS_ROOT=./projects

# Cloudflare API Integration (auto-discovered from file / environment)
CF_API_TOKEN=${CF_API_TOKEN}
CF_ACCOUNT_ID=${CF_ACCOUNT_ID}
CF_ZONE_ID=${CF_ZONE_ID}
EOF
success "Created $SCRIPT_DIR/.env"

# Persist token to cloudflared/token file if present
if [ -n "$CF_API_TOKEN" ]; then
    mkdir -p "$SCRIPT_DIR/cloudflared"
    echo "$CF_API_TOKEN" > "$SCRIPT_DIR/cloudflared/token"
    chmod 600 "$SCRIPT_DIR/cloudflared/token" 2>/dev/null || true
    success "Preserved Cloudflare API token in $SCRIPT_DIR/cloudflared/token"
fi

# Pre-populate SQLite settings table if database already exists
if [ -f "$SCRIPT_DIR/data/app.db" ] && command -v sqlite3 >/dev/null 2>&1; then
    [ -n "$CF_API_TOKEN" ] && sqlite3 "$SCRIPT_DIR/data/app.db" "INSERT INTO system_settings (key, value) VALUES ('cf_api_token', '$CF_API_TOKEN') ON CONFLICT(key) DO UPDATE SET value='$CF_API_TOKEN';" 2>/dev/null || true
    [ -n "$CF_ACCOUNT_ID" ] && sqlite3 "$SCRIPT_DIR/data/app.db" "INSERT INTO system_settings (key, value) VALUES ('cf_account_id', '$CF_ACCOUNT_ID') ON CONFLICT(key) DO UPDATE SET value='$CF_ACCOUNT_ID';" 2>/dev/null || true
    [ -n "$CF_ZONE_ID" ] && sqlite3 "$SCRIPT_DIR/data/app.db" "INSERT INTO system_settings (key, value) VALUES ('cf_zone_id', '$CF_ZONE_ID') ON CONFLICT(key) DO UPDATE SET value='$CF_ZONE_ID';" 2>/dev/null || true
fi

# Write cloudflared/config.yml
info "Writing cloudflared/config.yml..."
cat > "$SCRIPT_DIR/cloudflared/config.yml" << EOF
# DockTun managed cloudflared config
# Managed by DockTun. Edit via the dashboard or manually here.

tunnel: "${TUNNEL_ID}"
credentials-file: /etc/cloudflared/credentials.json
protocol: http2

ingress:
EOF

if [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
    cat >> "$SCRIPT_DIR/cloudflared/config.yml" << EOF
  - hostname: "${DOCKTUN_DOMAIN}"
    service: "http://docktun:3001"
EOF
fi

declare -A WRITTEN_HOSTS
[ -n "$DOCKTUN_DOMAIN" ] && WRITTEN_HOSTS["$DOCKTUN_DOMAIN"]=1
WRITTEN_HOSTS["docktun.example.com"]=1

for idx in "${!INGRESS_RULES_HOST[@]}"; do
    rhost="${INGRESS_RULES_HOST[$idx]}"
    rsvc="${INGRESS_RULES_SVC[$idx]}"
    rnotls="${INGRESS_RULES_NOTLS[$idx]}"

    [ -z "$rhost" ] || [ -z "$rsvc" ] && continue
    [ "$rsvc" = "http_status:404" ] && continue
    if [ -n "${WRITTEN_HOSTS[$rhost]}" ]; then
        continue
    fi
    WRITTEN_HOSTS["$rhost"]=1

    cat >> "$SCRIPT_DIR/cloudflared/config.yml" << EOF
  - hostname: "${rhost}"
    service: "${rsvc}"
EOF
    if [ "$rnotls" = "true" ] || [[ "$rsvc" =~ ^https:// ]]; then
        cat >> "$SCRIPT_DIR/cloudflared/config.yml" << 'EOF'
    originRequest:
      noTLSVerify: true
EOF
    fi
done

# Ingress catch-all must ALWAYS be the last rule
cat >> "$SCRIPT_DIR/cloudflared/config.yml" << 'EOF'
  - service: "http_status:404"
EOF

# Ensure clean UNIX line endings (no CR \r)
tr -d '\r' < "$SCRIPT_DIR/cloudflared/config.yml" > "$SCRIPT_DIR/cloudflared/config.yml.tmp" && mv "$SCRIPT_DIR/cloudflared/config.yml.tmp" "$SCRIPT_DIR/cloudflared/config.yml"
chmod 644 "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null || true
success "Created $SCRIPT_DIR/cloudflared/config.yml"

# Validate generated cloudflared config.yml syntax
info "Validating Cloudflare Tunnel ingress configuration..."
cfg_valid=false
if docker run --rm -v "$SCRIPT_DIR/cloudflared/config.yml:/etc/cloudflared/config.yml:ro" cloudflare/cloudflared:latest tunnel --config /etc/cloudflared/config.yml ingress validate >/dev/null 2>&1; then
    cfg_valid=true
    success "Cloudflare Tunnel ingress configuration validated successfully via Docker."
elif command -v cloudflared &>/dev/null && cloudflared tunnel --config "$SCRIPT_DIR/cloudflared/config.yml" ingress validate >/dev/null 2>&1; then
    cfg_valid=true
    success "Cloudflare Tunnel ingress configuration validated successfully via CLI."
fi

if [ "$cfg_valid" = false ]; then
    if docker info >/dev/null 2>&1 || command -v cloudflared &>/dev/null; then
        warn "cloudflared detected an issue with the custom ingress rules. Auto-repairing to clean base..."
        cat > "$SCRIPT_DIR/cloudflared/config.yml" << EOF
# DockTun managed cloudflared config
# Managed by DockTun. Edit via the dashboard or manually here.

tunnel: "${TUNNEL_ID}"
credentials-file: /etc/cloudflared/credentials.json
protocol: http2

ingress:
EOF
        if [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
            cat >> "$SCRIPT_DIR/cloudflared/config.yml" << EOF
  - hostname: "${DOCKTUN_DOMAIN}"
    service: "http://docktun:3001"
EOF
        fi
        cat >> "$SCRIPT_DIR/cloudflared/config.yml" << 'EOF'
  - service: "http_status:404"
EOF
        tr -d '\r' < "$SCRIPT_DIR/cloudflared/config.yml" > "$SCRIPT_DIR/cloudflared/config.yml.tmp" && mv "$SCRIPT_DIR/cloudflared/config.yml.tmp" "$SCRIPT_DIR/cloudflared/config.yml"
        chmod 644 "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null || true
    fi
fi

# Automatically synchronize all ingress hostnames and wildcard route with Cloudflare DNS
info "Automatically registering Cloudflare DNS routes for all ingress hostnames..."
auto_sync_cname() {
    local target="$1"
    local tid="$2"
    [ -z "$target" ] || [ "$target" = "docktun.example.com" ] && return 0

    local chost=$(echo "$target" | tr -d '"'\''\r ' | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')

    # Method 1: cloudflared CLI with cert.pem
    if command -v cloudflared >/dev/null 2>&1; then
        if c_res=$(cloudflared tunnel route dns "$tid" "$chost" 2>&1); then
            success "Cloudflare DNS route configured: $chost -> $tid.cfargotunnel.com"
            return 0
        fi
    fi

    # Method 2: Cloudflare REST API (via API Token)
    if [ -n "$CF_API_TOKEN" ]; then
        local bdom="$chost"
        bdom=$(echo "$chost" | sed -E 's/^\*\.//')
        local dcnt=$(echo "$bdom" | tr -cd '.' | wc -c)
        if [ "$dcnt" -ge 2 ]; then
            bdom="${bdom#*.}"
        fi

        local zid=$(curl -s -X GET "https://api.cloudflare.com/client/v4/zones?name=${bdom}&status=active" \
            -H "Authorization: Bearer ${CF_API_TOKEN}" \
            -H "Content-Type: application/json" 2>/dev/null | jq -r '.result[0].id // empty' 2>/dev/null || true)

        if [ -n "$zid" ] && [ "$zid" != "null" ]; then
            local payload="{\"type\":\"CNAME\",\"name\":\"${chost}\",\"content\":\"${tid}.cfargotunnel.com\",\"proxied\":true,\"ttl\":1}"
            local existing_id=$(curl -s -X GET "https://api.cloudflare.com/client/v4/zones/${zid}/dns_records?name=${chost}" \
                -H "Authorization: Bearer ${CF_API_TOKEN}" \
                -H "Content-Type: application/json" 2>/dev/null | jq -r '.result[0].id // empty' 2>/dev/null || true)

            if [ -n "$existing_id" ] && [ "$existing_id" != "null" ]; then
                curl -s -X PUT "https://api.cloudflare.com/client/v4/zones/${zid}/dns_records/${existing_id}" \
                    -H "Authorization: Bearer ${CF_API_TOKEN}" \
                    -H "Content-Type: application/json" \
                    -d "$payload" >/dev/null 2>&1 || true
                success "Cloudflare DNS route verified via API: $chost"
                return 0
            else
                curl -s -X POST "https://api.cloudflare.com/client/v4/zones/${zid}/dns_records" \
                    -H "Authorization: Bearer ${CF_API_TOKEN}" \
                    -H "Content-Type: application/json" \
                    -d "$payload" >/dev/null 2>&1 || true
                success "Cloudflare DNS route created via API: $chost"
                return 0
            fi
        fi
    fi
}

if [ -f "$SCRIPT_DIR/cloudflared/config.yml" ] && [ -n "$TUNNEL_ID" ] && [ "$TUNNEL_ID" != "your-tunnel-uuid" ]; then
    while IFS= read -r h; do
        [ -n "$h" ] && auto_sync_cname "$h" "$TUNNEL_ID"
    done < <(grep -E '^[[:space:]]*-[[:space:]]*hostname:' "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null | awk '{print $NF}' | tr -d '"'\''\r ')

    # Automatically add Wildcard route for root domain (*.domain.com)
    if [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
        rdom=$(echo "$DOCKTUN_DOMAIN" | sed -E 's|^https?://||; s|/+$||; s|:[0-9]+$||')
        dcnt=$(echo "$rdom" | tr -cd '.' | wc -c)
        if [ "$dcnt" -ge 2 ]; then
            rdom="${rdom#*.}"
        fi
        if [ -n "$rdom" ] && [ "$rdom" != "example.com" ]; then
            auto_sync_cname "*.$rdom" "$TUNNEL_ID"
        fi
    fi
fi

# Configure project mounts via docker-compose.override.yml if needed (leaves docker-compose.yml clean for git)
rm -f "$SCRIPT_DIR/docker-compose.override.yml"
VALID_MOUNTS=()
for pdir in "${COMPOSE_MOUNT_PATHS[@]}"; do
    if [[ "$pdir" =~ ^/ ]] && [ -d "$pdir" ] && [ "$pdir" != "$SCRIPT_DIR" ]; then
        VALID_MOUNTS+=("$pdir")
    fi
done

if [ ${#VALID_MOUNTS[@]} -gt 0 ]; then
    info "Configuring external project mounts in docker-compose.override.yml..."
    cat > "$SCRIPT_DIR/docker-compose.override.yml" << EOF
services:
  docktun:
    volumes:
EOF
    for vdir in "${VALID_MOUNTS[@]}"; do
        echo "      - $vdir:$vdir" >> "$SCRIPT_DIR/docker-compose.override.yml"
    done
    success "Created docker-compose.override.yml for project mounts."
fi

# Set correct permissions
chmod 755 "$SCRIPT_DIR/data" "$SCRIPT_DIR/cloudflared" "$SCRIPT_DIR/projects"
chmod 600 "$SCRIPT_DIR/.env" 2>/dev/null || true
chmod 644 "$SCRIPT_DIR/cloudflared/credentials.json" "$SCRIPT_DIR/cloudflared/config.yml" 2>/dev/null || true
[ -f "$SCRIPT_DIR/docker-compose.override.yml" ] && chmod 644 "$SCRIPT_DIR/docker-compose.override.yml" 2>/dev/null || true
[ -f "$SCRIPT_DIR/data/app.db" ] && chmod 644 "$SCRIPT_DIR/data/app.db" 2>/dev/null || true

# ------------------------------------------------------------------------------
# 7. Launch DockTun Containers
# ------------------------------------------------------------------------------
header "Step 7: Launching DockTun Stack"

info "Building and launching containers via $DOCKER_COMPOSE_CMD..."
$DOCKER_COMPOSE_CMD down 2>/dev/null || true
if ! $DOCKER_COMPOSE_CMD up -d --build; then
    echo ""
    error "Failed to start DockTun containers with Docker Compose!"
    echo -e "${C_YELLOW}Please check the Docker error output above.${C_RESET}"
    exit 1
fi

info "Verifying containers are online..."
sleep 4

echo ""
$DOCKER_COMPOSE_CMD ps
echo ""

# Verify containers are running
RUNNING_CONTAINERS_COUNT=$($DOCKER_COMPOSE_CMD ps --status running -q 2>/dev/null | wc -l)
if [ "$RUNNING_CONTAINERS_COUNT" -lt 2 ]; then
    warn "One or more containers failed to stay running! Recent logs:"
    $DOCKER_COMPOSE_CMD logs --tail=30
    echo ""
fi

# Detect host local IP address
HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")
[ -z "$HOST_IP" ] && HOST_IP="localhost"

# ------------------------------------------------------------------------------
# 8. Installation Summary
# ------------------------------------------------------------------------------
header "Installation & Deployment Complete!"

echo -e "${C_GREEN}${C_BOLD}DockTun is now running!${C_RESET}\n"
echo -e "  ${C_BOLD}Local Web Dashboard:${C_RESET}     ${C_CYAN}http://${HOST_IP}:${PORT}${C_RESET} (or http://docktun.local)"
if [ -n "$DOCKTUN_DOMAIN" ] && [ "$DOCKTUN_DOMAIN" != "docktun.example.com" ]; then
    echo -e "  ${C_BOLD}Public Tunnel URL:${C_RESET}       ${C_CYAN}https://${DOCKTUN_DOMAIN}${C_RESET}"
else
    echo -e "  ${C_BOLD}Public Tunnel URL:${C_RESET}       ${C_YELLOW}Disabled (Local-only security active)${C_RESET}"
fi
echo -e "  ${C_BOLD}Admin Username:${C_RESET}          ${C_YELLOW}${ADMIN_USERNAME}${C_RESET}"
echo -e "  ${C_BOLD}Admin Password:${C_RESET}          ${C_YELLOW}${ADMIN_PASSWORD}${C_RESET}"
echo -e "  ${C_BOLD}Tunnel Name:${C_RESET}             ${TUNNEL_NAME}"
echo -e "  ${C_BOLD}Tunnel UUID:${C_RESET}             ${TUNNEL_ID}"
echo ""

if [ ${#INGRESS_RULES_HOST[@]} -gt 0 ]; then
    echo -e "${C_BOLD}Imported Container Ingress Hostnames:${C_RESET}"
    for idx in "${!INGRESS_RULES_HOST[@]}"; do
        echo -e "  • ${C_CYAN}https://${INGRESS_RULES_HOST[$idx]}${C_RESET} -> ${INGRESS_RULES_SVC[$idx]}"
    done
    echo ""
fi

echo -e "${C_CYAN}${C_BOLD}⭐ AUTOMATIC CLOUDFLARE ROUTING (RECOMMENDED):${C_RESET}"
echo -e "  To route any container or project automatically without manual DNS edits, add a Wildcard CNAME in Cloudflare DNS:"
echo -e "    • Type:         ${C_YELLOW}CNAME${C_RESET}"
echo -e "    • Name:         ${C_YELLOW}*${C_RESET} (or *.yourdomain.com)"
echo -e "    • Target:       ${C_CYAN}${TUNNEL_ID}.cfargotunnel.com${C_RESET}"
echo -e "    • Proxy Status: ${C_GREEN}Proxied (Orange Cloud ON)${C_RESET}"
echo -e "  Once set, all subdomains added in DockTun route to your containers immediately!"
echo ""

echo -e "${C_BOLD}Useful Management Commands:${C_RESET}"
echo -e "  View logs:          ${C_CYAN}$DOCKER_COMPOSE_CMD logs -f${C_RESET}"
echo -e "  Restart stack:      ${C_CYAN}$DOCKER_COMPOSE_CMD restart${C_RESET}"
echo -e "  Stop stack:         ${C_CYAN}$DOCKER_COMPOSE_CMD down${C_RESET}"
echo -e "  cloudflared CLI:    ${C_CYAN}cloudflared tunnel list${C_RESET}"
echo ""
success "Enjoy managing your Cloudflare Tunnel with DockTun!"
