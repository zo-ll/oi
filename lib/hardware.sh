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
    elif [ "$ram" -ge 16 ]; then
        rec_color="$YELLOW"; rec_text="● CPU-only mode. Models up to 7B will work but slower"
    else
        rec_color="$RED"; rec_text="● Small models only (2-3B parameters)"
    fi
    echo -e "${rec_color}$(truncate_str "$rec_text" "$w")${NC}"
}
