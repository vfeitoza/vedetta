#!/usr/bin/env bash
# detect-hwaccel.sh — Detect GPU/TPU hardware and output JSON (no install)
# Usage: ./scripts/detect-hwaccel.sh
set -euo pipefail

gpus_json="[]"
vaapi_available=false
vaapi_device=""
nvdec_available=false
coral_available=false
recommended=""

# --- NVIDIA detection ---
if lspci 2>/dev/null | grep -qi nvidia; then
    model=$(lspci 2>/dev/null | grep -i nvidia | head -1 | sed 's/.*: //' | sed 's/ \[.*//;s/NVIDIA Corporation //')
    driver=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1 || echo "unknown")
    gpus_json=$(echo "$gpus_json" | python3 -c "
import sys, json
g = json.load(sys.stdin)
g.append({'vendor': 'nvidia', 'model': '${model//\'/\\\'}', 'driver': '${driver}'})
print(json.dumps(g))" 2>/dev/null || echo '[{"vendor":"nvidia","model":"'"$model"'","driver":"'"$driver"'"}]')
    nvdec_available=true
    recommended="nvdec"
fi

# --- Intel detection ---
if lspci 2>/dev/null | grep -qi 'intel.*graphics\|intel.*display'; then
    model=$(lspci 2>/dev/null | grep -i 'intel.*graphics\|intel.*display' | head -1 | sed 's/.*: //' | sed 's/ \[.*//')
    gpus_json=$(echo "$gpus_json" | python3 -c "
import sys, json
g = json.load(sys.stdin)
g.append({'vendor': 'intel', 'model': '${model//\'/\\\'}', 'driver': 'i915'})
print(json.dumps(g))" 2>/dev/null || echo "$gpus_json")
    if [[ -z "$recommended" ]]; then recommended="vaapi"; fi
fi

# --- AMD detection ---
if lspci 2>/dev/null | grep -qi 'amd.*radeon\|amd.*vga\|amd.*display'; then
    model=$(lspci 2>/dev/null | grep -i 'amd.*radeon\|amd.*vga\|amd.*display' | head -1 | sed 's/.*: //' | sed 's/ \[.*//')
    gpus_json=$(echo "$gpus_json" | python3 -c "
import sys, json
g = json.load(sys.stdin)
g.append({'vendor': 'amd', 'model': '${model//\'/\\\'}', 'driver': 'amdgpu'})
print(json.dumps(g))" 2>/dev/null || echo "$gpus_json")
    if [[ -z "$recommended" ]]; then recommended="vaapi"; fi
fi

# --- VA-API detection ---
if ls /dev/dri/renderD* &>/dev/null; then
    vaapi_available=true
    vaapi_device=$(ls /dev/dri/renderD* 2>/dev/null | head -1)
fi

# --- Coral/TPU detection ---
if ls /dev/accel* &>/dev/null || lsusb 2>/dev/null | grep -qi "google\|coral"; then
    coral_available=true
fi

# --- Default recommendation ---
if [[ -z "$recommended" ]]; then
    if $vaapi_available; then recommended="vaapi"; else recommended="none"; fi
fi

# --- Output JSON ---
cat <<EOF
{
  "gpus": ${gpus_json},
  "vaapi": {"available": ${vaapi_available}, "device": "${vaapi_device}"},
  "nvdec": {"available": ${nvdec_available}},
  "coral": {"available": ${coral_available}},
  "recommended_hwaccel": "${recommended}"
}
EOF
