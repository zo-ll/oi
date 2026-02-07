# lib/chat.sh - Launch chat with installed model file

launch_chat_file() {
    local filepath="$1"
    local context="${2:-$DEFAULT_CONTEXT}"
    local threads="${3:-$(nproc)}"

    if [ ! -f "$filepath" ]; then
        echo -e "${RED}Error: Model file not found: $filepath${NC}"
        return 1
    fi

    local filename
    filename=$(basename "$filepath")

    echo -e "${GREEN}Starting chat with: ${filename}${NC}"
    echo "  Model: $filename"
    echo "  Context: $context"
    echo "  Threads: $threads"
    echo ""
    echo -e "${CYAN}Type your message and press Enter. Use Ctrl+C to exit.${NC}"
    echo "$(make_divider "$(get_term_width)")"

    "$LLAMA_CLI" \
        -m "$filepath" \
        -c "$context" \
        -t "$threads" \
        -n -1
}
