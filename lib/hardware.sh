# lib/hardware.sh - Hardware detection and display

check_llama_cpp() {
    if [ ! -d "$LLAMA_CPP_DIR" ]; then
        echo -e "${RED}Error: llama.cpp not found at ${LLAMA_CPP_DIR}${NC}"
        echo "Please run the install script:"
        echo "  cd ~/oi && bash install.sh"
        exit 1
    fi

    if [ ! -f "$LLAMA_CLI" ]; then
        echo -e "${RED}Error: llama-cli not found at ${LLAMA_CLI}${NC}"
        echo "Please run the install script:"
        echo "  cd ~/oi && bash install.sh"
        exit 1
    fi

    mkdir -p "$MODELS_DIR"
}

detect_hardware() {
    if [ -f "${LIB_DIR}/hardware_detect.sh" ]; then
        bash "${LIB_DIR}/hardware_detect.sh"
    else
        echo '{"vram_gb": 0, "ram_gb": 8, "total_memory_gb": 8, "gpu_name": "Unknown", "cuda_available": "no", "cpu_cores": 4}'
    fi
}

show_hardware() {
    local hw=$(detect_hardware)
    local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    local ram=$(echo "$hw" | grep -o '"ram_gb": [0-9]*' | cut -d' ' -f2)
    local gpu=$(echo "$hw" | grep -o '"gpu_name": "[^"]*"' | cut -d'"' -f4)
    local cuda=$(echo "$hw" | grep -o '"cuda_available": "[^"]*"' | cut -d'"' -f4)
    local cores=$(echo "$hw" | grep -o '"cpu_cores": [0-9]*' | cut -d' ' -f2)

    local w=$(get_term_width)
    local div=$(make_divider "$w")
    local gpu_max=$((w - 14))
    local gpu_display=$(truncate_str "$gpu" "$gpu_max")

    echo -e "${CYAN}Hardware Profile${NC}"
    echo -e "${CYAN}${div}${NC}"
    printf "  ${BLUE}GPU:${NC}         %s\n" "${gpu_display}"
    printf "  ${BLUE}VRAM:${NC}        %.1f GB\n" "${vram}"
    printf "  ${BLUE}CUDA:${NC}        %s\n" "${cuda}"
    printf "  ${BLUE}RAM:${NC}         %s GB\n" "${ram}"
    printf "  ${BLUE}CPU Cores:${NC}   %s\n" "${cores}"
    echo -e "${CYAN}${div}${NC}"

    # Recommendation
    local rec_text="" rec_color=""
    if [ "${cuda}" = "yes" ] && [ "${vram%.*}" -ge 7 ]; then
        rec_color="$GREEN"; rec_text="✓ You can run models up to 7B parameters comfortably"
    elif [ "${cuda}" = "yes" ] && [ "${vram%.*}" -ge 4 ]; then
        rec_color="$YELLOW"; rec_text="● You can run models up to 4B parameters comfortably"
    elif [ "${vram%.*}" -ge 2 ] && [ "$ram" -ge 8 ]; then
        # Integrated GPU with decent shared memory
        rec_color="$YELLOW"; rec_text="● Integrated GPU mode. Models up to 4-7B will work (slower on CPU)"
    elif [ "$ram" -ge 16 ]; then
        rec_color="$YELLOW"; rec_text="● CPU-only mode. Models up to 7B will work but slower"
    else
        rec_color="$RED"; rec_text="● Small models only (2-3B parameters)"
    fi
    echo -e "${rec_color}$(truncate_str "$rec_text" "$w")${NC}"
}

update_llama_cpp() {
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    echo -e "${CYAN}  Updating llama.cpp${NC}"
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    echo ""

    # Check if llama.cpp exists
    if [ ! -d "$LLAMA_CPP_DIR" ]; then
        echo -e "${RED}Error: llama.cpp not found at ${LLAMA_CPP_DIR}${NC}"
        echo "Please run the install script first:"
        echo "  cd ~/oi && bash install.sh"
        return 1
    fi

    # Backup models directory
    local models_backup="${HOME}/.oi_models_backup_$(date +%s)"
    if [ -d "$MODELS_DIR" ] && [ "$(ls -A "$MODELS_DIR" 2>/dev/null)" ]; then
        echo -e "${YELLOW}Backing up models to ${models_backup}...${NC}"
        cp -r "$MODELS_DIR" "$models_backup"
        echo -e "${GREEN}Models backed up${NC}"
    fi

    # Detect CUDA for rebuild configuration
    local use_cuda="no"
    if command -v nvidia-smi &> /dev/null && command -v nvcc &> /dev/null; then
        local vram=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 | tr -d ' ')
        if [ -n "$vram" ] && [ "$vram" != "[Insufficientpermissions]" ]; then
            use_cuda="yes"
            echo -e "${GREEN}CUDA detected - will build with GPU support${NC}"
        fi
    fi

    if [ "$use_cuda" = "no" ]; then
        echo -e "${YELLOW}No CUDA detected - will build CPU-only version${NC}"
    fi

    echo ""
    echo -e "${CYAN}Removing old llama.cpp installation...${NC}"
    rm -rf "$LLAMA_CPP_DIR"

    echo -e "${CYAN}Cloning latest llama.cpp from GitHub...${NC}"
    if ! git clone https://github.com/ggml-org/llama.cpp.git "$LLAMA_CPP_DIR"; then
        echo -e "${RED}Error: Failed to clone llama.cpp${NC}"
        # Restore models if backup exists
        if [ -d "$models_backup" ]; then
            echo -e "${YELLOW}Restoring models from backup...${NC}"
            mkdir -p "$MODELS_DIR"
            cp -r "$models_backup/"* "$MODELS_DIR/" 2>/dev/null || true
            rm -rf "$models_backup"
        fi
        return 1
    fi

    echo ""
    echo -e "${CYAN}Building llama.cpp...${NC}"
    cd "$LLAMA_CPP_DIR"

    if [ "$use_cuda" = "yes" ]; then
        echo -e "${CYAN}Configuring with CUDA support...${NC}"
        cmake -B build -DGGML_CUDA=ON
    else
        echo -e "${CYAN}Configuring for CPU-only...${NC}"
        cmake -B build
    fi

    echo -e "${CYAN}Compiling... (this may take a few minutes)${NC}"
    if ! cmake --build build --config Release -j$(nproc); then
        echo -e "${RED}Error: Build failed${NC}"
        # Restore models if backup exists
        if [ -d "$models_backup" ]; then
            echo -e "${YELLOW}Restoring models from backup...${NC}"
            mkdir -p "$MODELS_DIR"
            cp -r "$models_backup/"* "$MODELS_DIR/" 2>/dev/null || true
        fi
        return 1
    fi

    # Verify build
    if [ ! -f "$LLAMA_CLI" ]; then
        echo -e "${RED}Error: llama-cli not found after build${NC}"
        return 1
    fi

    echo -e "${GREEN}llama.cpp built successfully!${NC}"

    # Restore models
    if [ -d "$models_backup" ]; then
        echo ""
        echo -e "${CYAN}Restoring models...${NC}"
        mkdir -p "$MODELS_DIR"
        cp -r "$models_backup/"* "$MODELS_DIR/" 2>/dev/null || true
        rm -rf "$models_backup"
        echo -e "${GREEN}Models restored${NC}"
    fi

    echo ""
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo -e "${GREEN}  llama.cpp update complete!${NC}"
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo ""
    echo "You can now run: oi"
    echo ""

    return 0
}
