# lib/chat.sh - Launch chat

launch_chat() {
    local model_id="$1"
    local quant="$2"
    local context="${3:-$DEFAULT_CONTEXT}"
    local threads="${4:-$(nproc)}"

    local model_info=$(get_model_info "$model_id")
    if [ -z "$model_info" ]; then
        echo -e "${RED}Error: Unknown model ID: ${model_id}${NC}"
        return 1
    fi

    # Check for model-specific default_quant
    local model_default_quant=$(echo "$model_info" | grep '"default_quant":' | head -1 | cut -d'"' -f4)
    if [ -z "$quant" ]; then
        if [ -n "$model_default_quant" ]; then
            quant="$model_default_quant"
        else
            quant="$DEFAULT_QUANT"
        fi
    fi

    local template=$(echo "$model_info" | grep '"filename_template":' | head -1 | cut -d'"' -f4)
    local filename=$(build_filename "$template" "$quant")
    local model_path="${MODELS_DIR}/${filename}"
    local name=$(echo "$model_info" | grep '"name":' | head -1 | cut -d'"' -f4)

    # Check if model exists, download if not
    if [ ! -f "$model_path" ]; then
        echo -e "${YELLOW}Model not found locally. Downloading...${NC}"
        if ! download_model "$model_id" "$quant"; then
            return 1
        fi
    fi

    # Verify file exists after potential download
    if [ ! -f "$model_path" ]; then
        echo -e "${RED}Error: Model file not found: $model_path${NC}"
        return 1
    fi

    echo -e "${GREEN}Starting chat with: ${name}${NC}"
    echo "  Model: $filename"
    echo "  Context: $context"
    echo "  Threads: $threads"
    echo ""
    echo -e "${CYAN}Type your message and press Enter. Use Ctrl+C to exit.${NC}"
    echo "$(make_divider "$(get_term_width)")"

    # Launch llama-cli in interactive mode
    "$LLAMA_CLI" \
        -m "$model_path" \
        -c "$context" \
        -t "$threads" \
        -n -1
}
