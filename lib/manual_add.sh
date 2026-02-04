# lib/manual_add.sh - Add model workflow (search HF, direct repo, pick file)

add_model_flow() {
    echo "" >&2
    echo -e "${CYAN}Add Model${NC}" >&2
    echo -e "  ${CYAN}1${NC}) Search HuggingFace" >&2
    echo -e "  ${CYAN}2${NC}) Enter repo directly" >&2
    echo -e "  ${CYAN}Q${NC}) Cancel" >&2
    echo "" >&2
    read -p "Choice: " add_choice

    case "$add_choice" in
        1) search_hf_interactive ;;
        2) direct_repo_entry ;;
        [Qq]) return 1 ;;
        *)
            echo -e "${RED}Invalid choice${NC}" >&2
            return 1
            ;;
    esac
}

search_hf_interactive() {
    read -p "Search keyword (e.g. llama, qwen, phi): " keyword
    if [ -z "$keyword" ]; then
        echo -e "${RED}No keyword entered${NC}" >&2
        return 1
    fi

    # Get available memory for filtering
    local hw mem_gb
    hw=$(detect_hardware)
    mem_gb=$(echo "$hw" | grep -o '"total_memory_gb": [0-9.]*' | cut -d' ' -f2)

    echo -e "${YELLOW}Searching HuggingFace...${NC}" >&2
    local results
    results=$(python3 "${LIB_DIR}/search_hf.py" --search "$keyword" --mem "${mem_gb:-0}")

    # Check for error
    local err
    err=$(echo "$results" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
    if [ -n "$err" ] && [ "$err" != "" ]; then
        echo -e "${RED}Search failed: ${err}${NC}" >&2
        return 1
    fi

    # Parse results into arrays
    local count
    count=$(echo "$results" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)

    if [ "$count" = "0" ] || [ -z "$count" ]; then
        echo -e "${YELLOW}No results found for '${keyword}'${NC}" >&2
        return 1
    fi

    echo "" >&2
    echo -e "${BLUE}Search results for '${keyword}':${NC}" >&2
    echo -e "${BLUE}$(make_divider "$(get_term_width)")${NC}" >&2

    local repos=()
    local i
    for (( i=0; i<count; i++ )); do
        local repo name downloads est
        repo=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['repo'])")
        name=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['name'])")
        downloads=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['downloads'])")
        est=$(echo "$results" | python3 -c "import sys,json; e=json.load(sys.stdin)[$i]['est_size_gb']; print(f'{e}G' if e else '?')")
        repos+=("$repo")
        printf >&2 "  %-3s %-45s %8s DL  ~%s\n" "$((i+1)))" "$(truncate_str "$repo" 45)" "$downloads" "$est"
    done

    echo "" >&2
    read -p "Pick a repo (1-${count}, or Q to cancel): " pick

    if [[ "$pick" =~ ^[Qq]$ ]]; then
        return 1
    fi

    if [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -ge 1 ] && [ "$pick" -le "$count" ]; then
        local chosen_repo="${repos[$((pick - 1))]}"
        pick_repo_file "$chosen_repo"
    else
        echo -e "${RED}Invalid selection${NC}" >&2
        return 1
    fi
}

direct_repo_entry() {
    echo -e "Enter HuggingFace repo (e.g. ${CYAN}bartowski/Llama-3.1-8B-GGUF${NC}):" >&2
    read -p "Repo: " repo

    if [ -z "$repo" ]; then
        echo -e "${RED}No repo entered${NC}" >&2
        return 1
    fi

    pick_repo_file "$repo"
}

pick_repo_file() {
    local repo="$1"

    echo -e "${YELLOW}Fetching files from ${repo}...${NC}" >&2
    local files
    files=$(python3 "${LIB_DIR}/search_hf.py" --files "$repo")

    # Check for error
    local err
    err=$(echo "$files" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
    if [ -n "$err" ] && [ "$err" != "" ]; then
        echo -e "${RED}Failed to fetch files: ${err}${NC}" >&2
        return 1
    fi

    local count
    count=$(echo "$files" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)

    if [ "$count" = "0" ] || [ -z "$count" ]; then
        echo -e "${YELLOW}No GGUF files found in ${repo}${NC}" >&2
        return 1
    fi

    echo "" >&2
    echo -e "${BLUE}GGUF files in ${repo}:${NC}" >&2
    echo -e "${BLUE}$(make_divider "$(get_term_width)")${NC}" >&2

    local filenames=()
    local i
    for (( i=0; i<count; i++ )); do
        local fname quant size_bytes size_display
        fname=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['filename'])")
        quant=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['quant'])")
        size_bytes=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['size_bytes'])")
        filenames+=("$fname")

        # Convert bytes to human readable
        if [ "$size_bytes" -gt 0 ] 2>/dev/null; then
            size_display=$(python3 -c "b=$size_bytes; print(f'{b/1073741824:.1f}G') if b>1073741824 else print(f'{b/1048576:.0f}M')")
        else
            size_display="?"
        fi

        printf >&2 "  %-3s %-50s %-10s %s\n" "$((i+1)))" "$(truncate_str "$fname" 50)" "$quant" "$size_display"
    done

    echo "" >&2
    read -p "Pick a file (1-${count}, or Q to cancel): " pick

    if [[ "$pick" =~ ^[Qq]$ ]]; then
        return 1
    fi

    if [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -ge 1 ] && [ "$pick" -le "$count" ]; then
        local chosen_file="${filenames[$((pick - 1))]}"
        local dl_path="${repo}/${chosen_file}"
        echo "" >&2
        echo -e "${CYAN}Downloading: ${dl_path}${NC}" >&2
        download_custom "$dl_path"
    else
        echo -e "${RED}Invalid selection${NC}" >&2
        return 1
    fi
}
