#!/usr/bin/env bash
# setup-hwaccel-ubuntu.sh — Detect GPU/TPU and install hardware decoding dependencies
# Supports: Ubuntu 24.04 (Noble) and Ubuntu 26.04 (planned)
# Usage: sudo ./scripts/setup-hwaccel-ubuntu.sh
set -euo pipefail

# --- Colors and logging ---
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

# --- Pre-flight checks ---
if [[ $EUID -ne 0 ]]; then
    error "This script must be run as root (use sudo)"
    exit 1
fi

if ! grep -qE 'Ubuntu (24\.04|26\.04)' /etc/os-release 2>/dev/null; then
    warn "This script targets Ubuntu 24.04/26.04. Detected OS:"
    grep PRETTY_NAME /etc/os-release 2>/dev/null || echo "Unknown"
fi

# --- Detection state ---
HAS_NVIDIA=false
HAS_INTEL=false
HAS_AMD=false
HAS_CORAL=false
INSTALLED=()

# --- Hardware detection ---
info "Detecting hardware accelerators..."

if lspci 2>/dev/null | grep -qi nvidia; then
    HAS_NVIDIA=true
    ok "NVIDIA GPU detected"
fi

if lspci 2>/dev/null | grep -qi 'intel.*graphics\|intel.*display'; then
    HAS_INTEL=true
    ok "Intel iGPU detected"
fi

if lspci 2>/dev/null | grep -qi 'amd.*radeon\|amd.*vga\|amd.*display'; then
    HAS_AMD=true
    ok "AMD GPU detected"
fi

if ls /dev/accel* &>/dev/null || lsusb 2>/dev/null | grep -qi "google\|coral"; then
    HAS_CORAL=true
    ok "Google TPU/Coral device detected"
fi

if ls /dev/dri/renderD* &>/dev/null; then
    info "DRI render nodes available: $(ls /dev/dri/renderD* 2>/dev/null | tr '\n' ' ')"
fi

if ! $HAS_NVIDIA && ! $HAS_INTEL && ! $HAS_AMD && ! $HAS_CORAL; then
    warn "No GPU/TPU hardware detected. Installing common packages only."
fi

# --- Helper ---
apt_install() {
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
}

# --- Update package lists ---
info "Updating package lists..."
apt-get update -qq

# --- Common packages (always installed) ---
info "Installing common packages (ffmpeg, libav*, pkg-config)..."
apt_install ffmpeg libavcodec-dev libavutil-dev pkg-config
INSTALLED+=(ffmpeg libavcodec-dev libavutil-dev pkg-config)
ok "Common packages installed"

# --- NVIDIA ---
if $HAS_NVIDIA; then
    info "Installing NVIDIA hardware decoding packages..."
    # Determine driver version available
    NVIDIA_VER=$(apt-cache search '^nvidia-driver-[0-9]+$' 2>/dev/null | sort -t- -k3 -n | tail -1 | grep -oP '\d+$' || echo "550")
    apt_install "nvidia-driver-${NVIDIA_VER}" "libnvidia-decode-${NVIDIA_VER}" ffmpeg libavcodec-dev libavutil-dev
    # cuda-toolkit is large; install only if not already present
    if ! dpkg -l cuda-toolkit-12-* &>/dev/null; then
        apt_install cuda-toolkit-12-6 2>/dev/null || warn "cuda-toolkit-12-6 not available, skipping"
    fi
    INSTALLED+=("nvidia-driver-${NVIDIA_VER}" "libnvidia-decode-${NVIDIA_VER}")

    # Verify
    info "Verifying NVIDIA installation..."
    if nvidia-smi &>/dev/null; then
        ok "nvidia-smi works"
    else
        warn "nvidia-smi not responding (reboot may be required)"
    fi
    if ls /dev/nvidia* &>/dev/null; then
        ok "NVIDIA device nodes present"
    else
        warn "/dev/nvidia* not found (driver may need reboot to load)"
    fi
fi

# --- Intel ---
if $HAS_INTEL; then
    info "Installing Intel VA-API packages..."
    apt_install intel-media-va-driver libva-dev libva-drm2 vainfo
    INSTALLED+=(intel-media-va-driver libva-dev libva-drm2 vainfo)

    # Verify
    info "Verifying Intel VA-API..."
    if vainfo 2>&1 | grep -qi "H264\|h264"; then
        ok "VA-API H.264 decode profile available"
    else
        warn "VA-API H.264 profile not detected (check vainfo output)"
    fi
fi

# --- AMD ---
if $HAS_AMD; then
    info "Installing AMD VA-API packages..."
    apt_install mesa-va-drivers libva-dev libva-drm2 vainfo
    INSTALLED+=(mesa-va-drivers libva-dev libva-drm2 vainfo)

    # Verify
    info "Verifying AMD VA-API..."
    if vainfo 2>&1 | grep -qi "H264\|h264"; then
        ok "VA-API H.264 decode profile available"
    else
        warn "VA-API H.264 profile not detected (check vainfo output)"
    fi
fi

# --- Google TPU/Coral ---
if $HAS_CORAL; then
    info "TPU/Coral detected — note: TPUs accelerate ML inference, not video decode."
    info "Installing Coral Edge TPU runtime..."
    if ! dpkg -l libedgetpu1-std &>/dev/null; then
        # Add Coral repo if not present
        if [[ ! -f /etc/apt/sources.list.d/coral-edgetpu.list ]]; then
            curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor -o /usr/share/keyrings/coral-edgetpu-archive-keyring.gpg 2>/dev/null || true
            echo "deb [signed-by=/usr/share/keyrings/coral-edgetpu-archive-keyring.gpg] https://packages.cloud.google.com/apt coral-edgetpu-stable main" \
                > /etc/apt/sources.list.d/coral-edgetpu.list
            apt-get update -qq
        fi
        apt_install libedgetpu1-std 2>/dev/null && INSTALLED+=(libedgetpu1-std) || warn "Could not install Coral runtime"
    else
        ok "Coral Edge TPU runtime already installed"
    fi
fi

# --- Summary ---
echo ""
echo -e "${BLUE}═══════════════════════════════════════${NC}"
echo -e "${BLUE}         SETUP SUMMARY${NC}"
echo -e "${BLUE}═══════════════════════════════════════${NC}"
echo -e "  NVIDIA:  $( $HAS_NVIDIA && echo -e "${GREEN}detected${NC}" || echo -e "${YELLOW}not found${NC}" )"
echo -e "  Intel:   $( $HAS_INTEL  && echo -e "${GREEN}detected${NC}" || echo -e "${YELLOW}not found${NC}" )"
echo -e "  AMD:     $( $HAS_AMD    && echo -e "${GREEN}detected${NC}" || echo -e "${YELLOW}not found${NC}" )"
echo -e "  Coral:   $( $HAS_CORAL  && echo -e "${GREEN}detected${NC}" || echo -e "${YELLOW}not found${NC}" )"
echo -e "  Packages installed: ${INSTALLED[*]}"
echo -e "${BLUE}═══════════════════════════════════════${NC}"
ok "Hardware acceleration setup complete."
