# lib/download.sh - Download, remove, fetch_hf_models wrapper

download_model() {
    local model_id="$1"
    local quant="$2"

    local model_info=$(get_model_info "$model_id")
    if [ -z "$model_info" ]; then
        echo -e "${RED}Error: Unknown model ID: ${model_id}${NC}"
        return 1
    fi

    local repo=$(echo "$model_info" | grep '"repo":' | head -1 | cut -d'"' -f4)
    local template=$(echo "$model_info" | grep '"filename_template":' | head -1 | cut -d'"' -f4)
    local name=$(echo "$model_info" | grep '"name":' | head -1 | cut -d'"' -f4)

    # Check if model has default_quant specified
    local model_default_quant=$(echo "$model_info" | grep '"default_quant":' | head -1 | cut -d'"' -f4)
    if [ -z "$quant" ]; then
        if [ -n "$model_default_quant" ]; then
            quant="$model_default_quant"
        else
            quant="$DEFAULT_QUANT"
        fi
    fi

    local filename=$(build_filename "$template" "$quant")
    local url=$(build_hf_url "$repo" "$filename")
    local output_path="${MODELS_DIR}/${filename}"

    echo -e "${CYAN}Downloading: ${name}${NC}"
    echo "  Quantization: $quant"
    echo "  URL: $url"
    echo "  Destination: $output_path"
    echo ""

    # Check if already exists
    if [ -f "$output_path" ]; then
        echo -e "${YELLOW}Model already exists. Skipping download.${NC}"
        return 0
    fi

    # Download with curl
    # Using -C - for resume support, -# for progress bar
    if ! curl -C - -# -L "$url" -o "$output_path"; then
        echo -e "${RED}Error: Download failed${NC}"
        # Clean up partial download
        rm -f "$output_path"
        return 1
    fi

    # Verify download (basic size check)
    local size=$(stat -c%s "$output_path" 2>/dev/null || stat -f%z "$output_path" 2>/dev/null)
    if [ "$size" -lt 1000000 ]; then
        echo -e "${RED}Error: Downloaded file is too small (${size} bytes). Download may have failed.${NC}"
        rm -f "$output_path"
        return 1
    fi

    echo -e "${GREEN}Download complete: ${filename}${NC}"
    return 0
}

download_custom() {
    local hf_path="$1"
    # Parse username/repo-name/filename.gguf
    local parts=$(echo "$hf_path" | tr '/' ' ')
    local repo=$(echo "$parts" | awk '{print $1"/"$2}')
    local filename=$(echo "$parts" | awk '{for(i=3;i<=NF;i++) printf "%s", $i; if(i<=NF) printf "/"}')

    if [ -z "$repo" ] || [ -z "$filename" ]; then
        echo -e "${RED}Error: Invalid HuggingFace path${NC}"
        echo "Format: username/repo-name/filename.gguf"
        return 1
    fi

    local url=$(build_hf_url "$repo" "$filename")
    local output_path="${MODELS_DIR}/${filename}"

    echo -e "${CYAN}Downloading custom model${NC}"
    echo "  URL: $url"
    echo ""

    if [ -f "$output_path" ]; then
        echo -e "${YELLOW}Model already exists.${NC}"
        return 0
    fi

    if ! curl -C - -# -L "$url" -o "$output_path"; then
        echo -e "${RED}Error: Download failed${NC}"
        rm -f "$output_path"
        return 1
    fi

    echo -e "${GREEN}Download complete: ${filename}${NC}"
}

remove_model() {
    local filename="$1"
    local filepath="${MODELS_DIR}/${filename}"

    if [ ! -f "$filepath" ]; then
        echo -e "${RED}Error: Model not found: ${filename}${NC}" >&2
        echo "Use 'oi -i' to list installed models." >&2
        return 1
    fi

    local size=$(du -h "$filepath" 2>/dev/null | cut -f1)
    echo -e "${YELLOW}Model: ${filename}${NC}" >&2
    echo -e "${YELLOW}Size:  ${size}${NC}" >&2
    read -p "Are you sure you want to delete this model? [y/N] " confirm
    case "$confirm" in
        [Yy]|[Yy][Ee][Ss])
            if rm "$filepath"; then
                echo -e "${GREEN}Deleted: ${filename}${NC}" >&2
            else
                echo -e "${RED}Error: Failed to delete ${filename}${NC}" >&2
                return 1
            fi
            ;;
        *)
            echo "Cancelled." >&2
            ;;
    esac
}
