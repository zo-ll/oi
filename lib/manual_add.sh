# lib/manual_add.sh - Add model workflow (search HF, direct repo, pick file)

# Detect hardware once per session and cache in globals
_init_hw_info() {
    if [ -n "$_HW_VRAM_GB" ]; then
        return
    fi
    local hw
    hw=$(detect_hardware)
    _HW_VRAM_GB=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    _HW_RAM_GB=$(echo "$hw" | grep -o '"ram_gb": [0-9]*' | cut -d' ' -f2)
    _HW_TOTAL_GB=$(echo "$hw" | grep -o '"total_memory_gb": [0-9.]*' | cut -d' ' -f2)
    : "${_HW_VRAM_GB:=0}"
    : "${_HW_RAM_GB:=0}"
    : "${_HW_TOTAL_GB:=0}"
}

# Compare two floats: returns 0 if $1 >= $2
_float_ge() {
    python3 -c "import sys; sys.exit(0 if float('$1') >= float('$2') else 1)"
}

select_runtime() {
    echo "" >&2
    echo -e "${CYAN}══ Step 1/4: Select Runtime ══${NC}" >&2
    echo "" >&2
    echo -e "Select runtime:" >&2
    echo -e "  ${CYAN}1${NC}) GGUF (for LLM text generation, Whisper models)" >&2
    echo -e "  ${CYAN}2${NC}) PyTorch (vision, audio, Parakeet, general ML)" >&2
    echo -e "  ${CYAN}3${NC}) Safetensors (safe alternative to PyTorch)" >&2
    echo -e "  ${CYAN}4${NC}) TensorFlow (Google's ML framework)" >&2
    echo -e "  ${CYAN}5${NC}) ONNX (cross-platform inference)" >&2
    echo -e "  ${CYAN}6${NC}) All formats (no filter)" >&2
    echo "" >&2
    read -p "Runtime (1-6) [default: 1]: " runtime_choice
    
    case "$runtime_choice" in
        2) echo "pytorch" ;;
        3) echo "safetensors" ;;
        4) echo "tensorflow" ;;
        5) echo "onnx" ;;
        6) echo "" ;;
        ""|1|*) echo "gguf" ;;
    esac
}

add_model_flow() {
    echo "" >&2
    echo -e "${CYAN}══ Add Model ══${NC}" >&2
    echo "" >&2
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
    _init_hw_info
    local mem_gb="$_HW_TOTAL_GB"
    
    # Get runtime selection
    local format_filter
    format_filter=$(select_runtime)
    
    while true; do
        echo "" >&2
        echo -e "${CYAN}══ Step 2/4: Search HuggingFace ══${NC}" >&2
        echo "" >&2
        read -p "Search keyword (e.g. llama, qwen, phi, parakeet, whisper): " keyword
        if [ -z "$keyword" ]; then
            echo -e "${RED}No keyword entered${NC}" >&2
            return 1
        fi
        
        echo -e "${YELLOW}Searching HuggingFace for '${keyword}'${format_filter:+ ($format_filter)}...${NC}" >&2
        local results
        # Pass format filter to search
        if [ -n "$format_filter" ]; then
            results=$(python3 "${LIB_DIR}/search_hf.py" --search "$keyword" --mem "${mem_gb:-0}" --format "$format_filter")
        else
            results=$(python3 "${LIB_DIR}/search_hf.py" --search "$keyword" --mem "${mem_gb:-0}")
        fi

        # Check for error
        local err
        err=$(echo "$results" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
        if [ -n "$err" ] && [ "$err" != "" ]; then
            echo -e "${RED}Search failed: ${err}${NC}" >&2
            return 1
        fi
        
        # Parse results
        local count
        count=$(echo "$results" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
        
        if [ "$count" = "0" ] || [ -z "$count" ]; then
            echo -e "${YELLOW}No results found for '${keyword}'${format_filter:+ ($format_filter)}${NC}" >&2
            echo "" >&2
            read -p "Try another search? [Y/n]: " retry
            if [[ "$retry" =~ ^[Nn]$ ]]; then
                return 1
            fi
            continue
        fi
        
        echo "" >&2
        echo -e "${CYAN}══ Step 3/4: Pick a Repository ══${NC}" >&2
        echo "" >&2
        echo -e "${BLUE}Results for '${keyword}' (${count} found):${NC}" >&2
        echo -e "${BLUE}$(make_divider "$(get_term_width)")${NC}" >&2
        
        local repos=()
        local i
        for (( i=0; i<count; i++ )); do
            local repo name downloads est est_raw format
            repo=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['repo'])")
            name=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['name'])")
            downloads=$(echo "$results" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['downloads'])")
            est_raw=$(echo "$results" | python3 -c "import sys,json; e=json.load(sys.stdin)[$i]['est_size_gb']; print(e if e else '')")
            format=$(echo "$results" | python3 -c "import sys,json; f=json.load(sys.stdin)[$i]['format']; print(f if f else 'unknown')" 2>/dev/null || echo "unknown")
            repos+=("$repo")

            # Color-code by hardware fit
            local hw_tag="" line_color=""
            if [ -n "$est_raw" ] && [ "$est_raw" != "None" ]; then
                est="~${est_raw}G"
                if _float_ge "$_HW_VRAM_GB" "$est_raw"; then
                    hw_tag="${GREEN}GPU${NC}"
                    line_color="${GREEN}"
                elif _float_ge "$_HW_TOTAL_GB" "$est_raw"; then
                    hw_tag="${YELLOW}CPU${NC}"
                    line_color="${YELLOW}"
                else
                    hw_tag="${RED}too large${NC}"
                    line_color="${RED}"
                fi
            else
                est="?"
                hw_tag=""
                line_color=""
            fi
            
            # Display format in brackets
            local fmt_tag="[${format}]"
            printf >&2 "  ${line_color}%-3s${NC} %-40s %8s DL  %6s  %-12s  %b\n" "$((i+1)))" "$(truncate_str "$repo" 40)" "$downloads" "$est" "$fmt_tag" "$hw_tag"
        done

        echo -e "  ${GREEN}GPU${NC}=fits VRAM  ${YELLOW}CPU${NC}=fits RAM  ${RED}too large${NC}=won't fit" >&2

        echo "" >&2
        read -p "Pick a repo (1-${count}, S to search again, Q to cancel): " pick

        if [[ "$pick" =~ ^[Qq]$ ]]; then
            return 1
        fi

        if [[ "$pick" =~ ^[Ss]$ ]]; then
            continue
        fi

        if [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -ge 1 ] && [ "$pick" -le "$count" ]; then
            local chosen_repo="${repos[$((pick - 1))]}"
            pick_repo_file "$chosen_repo"
            return $?
        else
            echo -e "${RED}Invalid selection${NC}" >&2
        fi
    done
}

direct_repo_entry() {
    echo "" >&2
    echo -e "${CYAN}══ Enter Repository ══${NC}" >&2
    echo "" >&2
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
    _init_hw_info
    
    echo "" >&2
    echo -e "${CYAN}══ Step 4/4: Pick a Model File ══${NC}" >&2
    echo "" >&2
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

    # Pre-compute sizes in GB and find the best option:
    # Largest quant that fits in VRAM (GPU), or failing that, largest that fits in total mem
    local sizes_gb=()
    local best_gpu_idx=-1
    local best_cpu_idx=-1
    local best_gpu_size=0
    local best_cpu_size=0

    local i
    for (( i=0; i<count; i++ )); do
        local size_bytes
        size_bytes=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['size_bytes'])")
        local size_gb
        if [ "$size_bytes" -gt 0 ] 2>/dev/null; then
            size_gb=$(python3 -c "print(f'{$size_bytes/1073741824:.2f}')")
        else
            size_gb="0"
        fi
        sizes_gb+=("$size_gb")

        # Track best GPU fit (largest that fits in VRAM)
        if _float_ge "$_HW_VRAM_GB" "$size_gb" 2>/dev/null; then
            if _float_ge "$size_gb" "$best_gpu_size" 2>/dev/null; then
                best_gpu_idx=$i
                best_gpu_size="$size_gb"
            fi
        # Track best CPU fit (largest that fits in total mem)
        elif _float_ge "$_HW_TOTAL_GB" "$size_gb" 2>/dev/null; then
            if _float_ge "$size_gb" "$best_cpu_size" 2>/dev/null; then
                best_cpu_idx=$i
                best_cpu_size="$size_gb"
            fi
        fi
    done

    # The recommended index: prefer GPU, fall back to CPU
    local rec_idx=-1
    if [ $best_gpu_idx -ge 0 ]; then
        rec_idx=$best_gpu_idx
    elif [ $best_cpu_idx -ge 0 ]; then
        rec_idx=$best_cpu_idx
    fi

    echo "" >&2
    echo -e "${BLUE}Available files in ${repo}:${NC}" >&2
    echo -e "  VRAM: ${_HW_VRAM_GB}G  Total RAM: ${_HW_TOTAL_GB}G" >&2
    echo -e "${BLUE}$(make_divider "$(get_term_width)")${NC}" >&2

    for (( i=0; i<count; i++ )); do
        local fname quant fmt size_bytes shard_count size_display shard_info
        fname=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['filename'])")
        quant=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['quant'])")
        fmt=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['format'])")
        size_bytes=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['size_bytes'])")
        shard_count=$(echo "$files" | python3 -c "import sys,json; print(json.load(sys.stdin)[$i]['shard_count'])")

        # Convert bytes to human readable
        if [ "$size_bytes" -gt 0 ] 2>/dev/null; then
            size_display=$(python3 -c "b=$size_bytes; print(f'{b/1073741824:.1f}G') if b>1073741824 else print(f'{b/1048576:.0f}M')")
        else
            size_display="?"
        fi

        shard_info=""
        if [ "$shard_count" -gt 1 ] 2>/dev/null; then
            shard_info=" (${shard_count} parts)"
        fi

        # Color and tag based on hardware fit
        local color="" tag=""
        local sg="${sizes_gb[$i]}"
        if [ "$sg" != "0" ]; then
            if _float_ge "$_HW_VRAM_GB" "$sg" 2>/dev/null; then
                color="${GREEN}"
                tag="GPU"
            elif _float_ge "$_HW_TOTAL_GB" "$sg" 2>/dev/null; then
                color="${YELLOW}"
                tag="CPU"
            else
                color="${RED}"
                tag="too large"
            fi
        fi

        local rec_mark=""
        if [ $i -eq $rec_idx ]; then
            rec_mark=" << recommended"
        fi

        # Show format in output
        printf >&2 "  ${color}%-3s %-12s %-12s %8s%s  %-12s %s${NC}%s\n" \
            "$((i+1)))" "$fmt" "${quant:--}" "$size_display" "$shard_info" "$tag" \
            "$(truncate_str "$fname" 20)" "$rec_mark"
    done

    echo -e "  ${GREEN}GPU${NC}=fits VRAM  ${YELLOW}CPU${NC}=fits RAM  ${RED}too large${NC}=won't fit" >&2

    echo "" >&2
    read -p "Pick a file (1-${count}, or Q to cancel): " pick

    if [[ "$pick" =~ ^[Qq]$ ]]; then
        return 1
    fi

    if [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -ge 1 ] && [ "$pick" -le "$count" ]; then
        local idx=$((pick - 1))
        # Get all_files list for the selected entry
        local all_files_json
        all_files_json=$(echo "$files" | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin)[$idx]['all_files']))")
        local file_count
        file_count=$(echo "$all_files_json" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))")

        echo "" >&2
        echo -e "${CYAN}══ Downloading ══${NC}" >&2

        local fi
        for (( fi=0; fi<file_count; fi++ )); do
            local dl_file
            dl_file=$(echo "$all_files_json" | python3 -c "import sys,json; print(json.load(sys.stdin)[$fi])")
            local dl_basename
            dl_basename=$(basename "$dl_file")
            local url
            url=$(build_hf_url "$repo" "$dl_file")
            local output_path="${MODELS_DIR}/${dl_basename}"

            if [ "$file_count" -gt 1 ]; then
                echo -e "${CYAN}Downloading part $((fi+1))/${file_count}: ${dl_basename}${NC}" >&2
            else
                echo -e "${CYAN}Downloading: ${dl_basename}${NC}" >&2
            fi
            echo "  URL: $url" >&2
            echo "  Destination: $output_path" >&2

            if [ -f "$output_path" ]; then
                # Check if it's a complete file (>100MB) or a partial download
                local existing_size
                existing_size=$(stat -c%s "$output_path" 2>/dev/null || stat -f%z "$output_path" 2>/dev/null)
                if [ "$existing_size" -gt 100000000 ] 2>/dev/null; then
                    echo -e "${YELLOW}Already exists ($(python3 -c "print(f'{$existing_size/1073741824:.1f}G')")), skipping.${NC}" >&2
                    continue
                else
                    echo -e "${YELLOW}Resuming partial download...${NC}" >&2
                fi
            fi

            # Retry loop: up to 3 attempts with resume
            local max_retries=3
            local attempt=1
            local dl_ok=false
            while [ $attempt -le $max_retries ]; do
                if [ $attempt -gt 1 ]; then
                    echo -e "${YELLOW}Retry $attempt/$max_retries: resuming download...${NC}" >&2
                    sleep 2
                fi

                if curl -C - -# -L "$url" -o "$output_path"; then
                    dl_ok=true
                    break
                fi

                echo -e "${RED}Download interrupted (attempt $attempt/$max_retries)${NC}" >&2
                ((attempt++))
            done

            if [ "$dl_ok" = false ]; then
                echo -e "${RED}Error: Download failed after $max_retries attempts for ${dl_basename}${NC}" >&2
                echo -e "${YELLOW}Partial file kept at: $output_path${NC}" >&2
                echo -e "${YELLOW}Run the same download again to resume.${NC}" >&2
                return 1
            fi

            # Verify download
            local size
            size=$(stat -c%s "$output_path" 2>/dev/null || stat -f%z "$output_path" 2>/dev/null)
            if [ "$size" -lt 1000000 ]; then
                echo -e "${RED}Error: Downloaded file too small (${size} bytes). May have failed.${NC}" >&2
                rm -f "$output_path"
                return 1
            fi
        done

        echo "" >&2
        echo -e "${GREEN}Download complete!${NC}" >&2
        return 0
    else
        echo -e "${RED}Invalid selection${NC}" >&2
        return 1
    fi
}
