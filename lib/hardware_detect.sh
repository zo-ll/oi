#!/bin/bash
#
# hardware_detect.sh - System hardware detection for oi
# Detects VRAM, RAM, and CPU cores to recommend suitable models
#

get_nvidia_gpu_info() {
    local vram_gb=0
    local gpu_name="None"
    local cuda_available="no"
    
    if command -v nvidia-smi &> /dev/null; then
        local vram_mb=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 | tr -d ' ')
        if [ -n "$vram_mb" ] && [ "$vram_mb" != "[Insufficientpermissions]" ]; then
            vram_gb=$(echo "scale=1; $vram_mb / 1024" | bc 2>/dev/null || echo "0")
            gpu_name=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n1 | sed 's/^ *//;s/ *$//')
            cuda_available="yes"
        fi
    fi
    
    echo "nvidia|${vram_gb}|${gpu_name}|${cuda_available}"
}

get_integrated_gpu_info() {
    local gpu_type="none"
    local gpu_name="None"
    local vram_gb=0
    
    # Check for Intel integrated graphics
    if command -v lspci &> /dev/null; then
        local intel_gpu=$(lspci 2>/dev/null | grep -i vga | grep -i intel | head -n1)
        if [ -n "$intel_gpu" ]; then
            gpu_type="intel"
            gpu_name=$(echo "$intel_gpu" | sed 's/.*: //' | sed 's/^ *//;s/ *$//')
        fi
        
        # Check for AMD integrated graphics
        local amd_gpu=$(lspci 2>/dev/null | grep -i vga | grep -iE 'amd|ati' | head -n1)
        if [ -n "$amd_gpu" ]; then
            gpu_type="amd"
            gpu_name=$(echo "$amd_gpu" | sed 's/.*: //' | sed 's/^ *//;s/ *$//')
        fi
    fi
    
    # Also check via /sys/class/drm for render devices
    if [ "$gpu_type" = "none" ]; then
        for dev in /sys/class/drm/renderD*/device/vendor 2>/dev/null; do
            if [ -f "$dev" ]; then
                local vendor=$(cat "$dev" 2>/dev/null)
                case "$vendor" in
                    0x8086)  # Intel
                        gpu_type="intel"
                        if [ -f "${dev%/*}/product" ]; then
                            local product=$(cat "${dev%/*}/product" 2>/dev/null)
                            gpu_name="Intel Graphics (0x${product})"
                        else
                            gpu_name="Intel Integrated Graphics"
                        fi
                        break
                        ;;
                    0x1002|0x1022)  # AMD
                        gpu_type="amd"
                        if [ -f "${dev%/*}/product" ]; then
                            local product=$(cat "${dev%/*}/product" 2>/dev/null)
                            gpu_name="AMD Graphics (0x${product})"
                        else
                            gpu_name="AMD Integrated Graphics"
                        fi
                        break
                        ;;
                esac
            fi
        done
    fi
    
    # For integrated GPUs, use a portion of system RAM as VRAM estimate
    if [ "$gpu_type" != "none" ]; then
        local ram_gb=$(get_ram_info)
        # Use up to 50% of system RAM or max 4GB as shared VRAM estimate
        if [ "$ram_gb" -gt 8 ]; then
            vram_gb=4
        else
            vram_gb=$(echo "scale=1; $ram_gb * 0.5" | bc 2>/dev/null || echo "2")
        fi
    fi
    
    echo "${gpu_type}|${vram_gb}|${gpu_name}"
}

get_ram_info() {
    local ram_gb=$(free -g 2>/dev/null | awk '/^Mem:/{print $2}')
    if [ -z "$ram_gb" ] || [ "$ram_gb" = "0" ]; then
        ram_gb=$(free -m 2>/dev/null | awk '/^Mem:/{print int($2/1024)}')
    fi
    echo "${ram_gb:-0}"
}

get_cpu_info() {
    local cores=$(nproc 2>/dev/null || echo "1")
    local model=$(lscpu 2>/dev/null | grep "Model name" | cut -d':' -f2 | sed 's/^ *//' | head -n1)
    echo "${cores}|${model:-Unknown}"
}

detect_hardware() {
    # Try NVIDIA first
    local nvidia_data=$(get_nvidia_gpu_info)
    local gpu_type=$(echo "$nvidia_data" | cut -d'|' -f1)
    local vram_gb=$(echo "$nvidia_data" | cut -d'|' -f2)
    local gpu_name=$(echo "$nvidia_data" | cut -d'|' -f3)
    local cuda_available=$(echo "$nvidia_data" | cut -d'|' -f4)
    
    # If no NVIDIA, check for integrated GPUs
    if [ "$gpu_type" = "nvidia" ] && [ -z "$gpu_name" ] || [ "$gpu_name" = "None" ]; then
        local integrated_data=$(get_integrated_gpu_info)
        gpu_type=$(echo "$integrated_data" | cut -d'|' -f1)
        vram_gb=$(echo "$integrated_data" | cut -d'|' -f2)
        gpu_name=$(echo "$integrated_data" | cut -d'|' -f3)
        # Integrated GPUs don't support CUDA
        cuda_available="no"
    fi
    
    local ram_gb=$(get_ram_info)
    local cpu_data=$(get_cpu_info)
    local cpu_cores=$(echo "$cpu_data" | cut -d'|' -f1)
    local cpu_model=$(echo "$cpu_data" | cut -d'|' -f2)
    
    # Calculate total available memory for models
    local total_memory_gb=$(echo "$vram_gb + $ram_gb" | bc 2>/dev/null || echo "$ram_gb")
    
    cat <<EOF
{
  "vram_gb": ${vram_gb:-0},
  "ram_gb": ${ram_gb:-0},
  "total_memory_gb": ${total_memory_gb:-0},
  "gpu_name": "${gpu_name}",
  "cuda_available": "${cuda_available}",
  "cpu_cores": ${cpu_cores:-1},
  "cpu_model": "${cpu_model}"
}
EOF
}

# If script is run directly, output hardware info
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    detect_hardware
fi
