# lib/models.sh - Catalog loading, filtering, listing

is_cache_valid() {
    [ -f "$CACHE_FILE" ] || return 1
    local now file_mod age
    now=$(date +%s)
    file_mod=$(stat -c %Y "$CACHE_FILE" 2>/dev/null || stat -f %m "$CACHE_FILE" 2>/dev/null) || return 1
    age=$((now - file_mod))
    [ "$age" -lt "$CACHE_TTL" ]
}

fetch_hf_models() {
    mkdir -p "$CACHE_DIR"
    local hw=$(detect_hardware)
    local total_mem=$(echo "$hw" | grep -o '"total_memory_gb": [0-9.]*' | cut -d' ' -f2)
    echo -ne "${YELLOW}Updating model catalog from HuggingFace... ${NC}" >&2

    python3 "${LIB_DIR}/fetch_hf_models.py" "$CACHE_FILE" "$HF_ORGS" "$total_mem" &
    local py_pid=$!

    # Spinner while fetch runs
    local spin_chars='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
    local i=0
    while kill -0 "$py_pid" 2>/dev/null; do
        printf '\b%s' "${spin_chars:i%${#spin_chars}:1}" >&2
        ((i++))
        sleep 0.1
    done
    printf '\b \b' >&2

    wait "$py_pid"
    local py_exit=$?
    if [ $py_exit -eq 0 ]; then
        echo -e "${GREEN}done${NC}" >&2
    else
        echo -e "${RED}failed${NC}" >&2
    fi
    return $py_exit
}

load_models() {
    # If user has previously fetched from HF with -r, use that cache
    if is_cache_valid; then
        cat "$CACHE_FILE"
        return
    fi

    # Default: use the curated static model list
    if [ -f "${LIB_DIR}/models.json" ]; then
        cat "${LIB_DIR}/models.json"
        return
    fi

    echo '{"models": []}'
}

get_model_info() {
    local model_id="$1"
    local models=$(load_models)

    # Extract model entry from JSON - escape model_id for safety
    local escaped_id=$(echo "$model_id" | sed 's/[[\.*^$()+?{|]/\\&/g')
    echo "$models" | grep -A 10 "\"id\": \"${escaped_id}\"" | head -10
}

is_model_installed() {
    local filename="$1"
    [ -f "${MODELS_DIR}/${filename}" ]
}

build_filename() {
    local template="$1"
    local quant="$2"

    # If template doesn't contain {quant}, return it as-is (model-specific filename)
    if [[ "$template" != *"{quant}"* ]]; then
        echo "$template"
        return
    fi

    echo "${template//\{quant\}/$quant}"
}

build_hf_url() {
    local repo="$1"
    local filename="$2"
    echo "https://huggingface.co/${repo}/resolve/main/${filename}"
}

get_compatible_models() {
    local hw=$(detect_hardware)
    local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    local total_mem=$(echo "$hw" | grep -o '"total_memory_gb": [0-9.]*' | cut -d' ' -f2)

    local models=$(load_models)
    local compatible=""

    # Build list of compatible models
    local ids=$(echo "$models" | grep '"id":' | cut -d'"' -f4)
    for id in $ids; do
        local model_info=$(echo "$models" | grep -A 10 "\"id\": \"${id}\"" | head -10)
        local min_vram=$(echo "$model_info" | grep '"min_vram_gb":' | grep -o '[0-9.]*' | head -1)
        local name=$(echo "$model_info" | grep '"name":' | head -1 | cut -d'"' -f4)

        if [ -n "$min_vram" ]; then
            local status=""
            # Use bc for floating point comparison if available
            if command -v bc >/dev/null 2>&1; then
                if (( $(echo "$vram >= $min_vram" | bc -l) )); then
                    status="gpu"
                elif (( $(echo "$total_mem >= $min_vram" | bc -l) )); then
                    status="cpu"
                fi
            else
                # Fallback: compare integer parts
                local vram_int=$(echo "$vram" | cut -d. -f1)
                local total_mem_int=$(echo "$total_mem" | cut -d. -f1)
                local min_vram_int=$(echo "$min_vram" | cut -d. -f1)
                if [ "$vram_int" -ge "$min_vram_int" ]; then
                    status="gpu"
                elif [ "$total_mem_int" -ge "$min_vram_int" ]; then
                    status="cpu"
                fi
            fi

            if [ -n "$status" ]; then
                compatible="${compatible}${id}|${name}|${status}
"
            fi
        fi
    done

    echo -e "$compatible"
}

is_model_in_array() {
    local target="$1"
    shift
    local arr=("$@")
    for item in "${arr[@]}"; do
        if [ "$item" = "$target" ]; then
            return 0
        fi
    done
    return 1
}

list_models() {
    local show_all="$1"
    local hw=$(detect_hardware)
    local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    local total_mem=$(echo "$hw" | grep -o '"total_memory_gb": [0-9.]*' | cut -d' ' -f2)
    local w=$(get_term_width)

    echo -e "${CYAN}Available Models:${NC}"
    echo ""

    local models=$(load_models)

    # Parse and display each model
    echo "$models" | grep -E '"id"|"name"|"min_vram_gb"|"description"|"tags"' | while read -r line; do
        if echo "$line" | grep -q '"id":'; then
            id=$(echo "$line" | cut -d'"' -f4)
        elif echo "$line" | grep -q '"name":'; then
            name=$(echo "$line" | cut -d'"' -f4)
        elif echo "$line" | grep -q '"min_vram_gb":'; then
            min_vram=$(echo "$line" | grep -o '"min_vram_gb": [0-9.]*' | grep -o '[0-9.]*' | head -1)

            # Check if model is suitable - convert to integers for comparison
            suitable=""
            if [ "$show_all" != "all" ]; then
                # Use bc for floating point comparison
                if command -v bc >/dev/null 2>&1; then
                    if (( $(echo "$vram >= $min_vram" | bc -l) )); then
                        suitable="${GREEN}[Compatible]${NC}"
                    else
                        suitable="${YELLOW}[CPU Only]${NC}"
                    fi
                else
                    # Fallback: compare integer parts
                    local vram_int=$(echo "$vram" | cut -d. -f1)
                    local min_vram_int=$(echo "$min_vram" | cut -d. -f1)
                    if [ "$vram_int" -ge "$min_vram_int" ]; then
                        suitable="${GREEN}[Compatible]${NC}"
                    else
                        suitable="${YELLOW}[CPU Only]${NC}"
                    fi
                fi
            fi

            # Check if installed
            local template=$(echo "$models" | grep -A 10 "\"id\": \"${id}\"" | grep '"filename_template":' | cut -d'"' -f4)
            local filename=$(build_filename "$template" "$DEFAULT_QUANT")
            local installed=""
            if is_model_installed "$filename"; then
                installed=" ${GREEN}[Installed]${NC}"
            fi

            # Truncate the header line: "id: name [status][installed]"
            local header_plain="${id}: ${name}"
            local header_max=$((w - 15))  # room for status markers
            header_plain=$(truncate_str "$header_plain" "$header_max")
            echo -e "${BLUE}${header_plain}${NC} ${suitable}${installed}"
            echo "    Min VRAM: ${min_vram} GB"
        elif echo "$line" | grep -q '"description":'; then
            desc=$(echo "$line" | cut -d'"' -f4)
            # Only print if we have a valid model context (id is set)
            if [ -n "$id" ] && [ -n "$name" ]; then
                echo "    $(truncate_str "$desc" $((w - 4)))"
                echo ""
                # Reset variables for next model
                id=""
                name=""
                min_vram=""
            fi
        fi
    done
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
