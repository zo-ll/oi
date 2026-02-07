# lib/models.sh - Model file utilities

is_model_installed() {
    local filename="$1"
    [ -f "${MODELS_DIR}/${filename}" ]
}

build_hf_url() {
    local repo="$1"
    local filename="$2"
    echo "https://huggingface.co/${repo}/resolve/main/${filename}"
}

list_installed_models() {
    local w=$(get_term_width)
    echo -e "${CYAN}Installed Models:${NC}"
    echo ""

    local found=0
    for file in "$MODELS_DIR"/*.gguf; do
        if [ -f "$file" ]; then
            found=1
            local basename=$(basename "$file")
            local size=$(du -h "$file" 2>/dev/null | cut -f1)
            local suffix=" (${size})"
            local name_max=$((w - ${#suffix} - 2))  # 2 for "✓ "
            local display_name=$(truncate_str "$basename" "$name_max")
            echo -e "${GREEN}✓${NC} ${display_name}${suffix}"
        fi
    done

    if [ "$found" -eq 0 ]; then
        echo "No models installed yet."
        echo "Run 'oi' to download and install models."
    fi
}
