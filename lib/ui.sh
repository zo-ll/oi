# lib/ui.sh - Terminal helpers + interactive menu

get_term_width() {
    local w
    w=$(tput cols 2>/dev/null) || w=80
    (( w < 40 )) && w=40
    (( w > 80 )) && w=80
    echo "$w"
}

make_divider() {
    local width="$1"
    local char="${2:-─}"
    local out=""
    local i
    for (( i=0; i<width; i++ )); do
        out+="$char"
    done
    printf '%s' "$out"
}

truncate_str() {
    local str="$1"
    local max="$2"
    if [ "${#str}" -gt "$max" ]; then
        echo "${str:0:$((max-3))}..."
    else
        echo "$str"
    fi
}

show_help() {
    cat <<EOF
 oi - One-command LLM chat interface

USAGE:
    oi [OPTIONS]

OPTIONS:
    -m, --model <id>         Directly select model by ID
    -q, --quant <quant>      Specify quantization (default: ${DEFAULT_QUANT})
    -l, --list              List available models
    -i, --installed         List installed models only
    -d, --download <path>   Download custom model from HuggingFace
                            Format: username/repo-name/filename.gguf
    -r, --refresh           Force refresh model catalog from HuggingFace
    -x, --remove <file>     Remove an installed model
    -h, --hardware          Show system hardware information
    -c, --context <size>    Set context size (default: ${DEFAULT_CONTEXT})
    -t, --threads <num>     Set number of CPU threads
    --help                  Show this help message

EXAMPLES:
    oi                      # Interactive model selection
    oi -m qwen2.5-3b        # Start chat with specific model
    oi -m phi-3-mini -q Q5_K_M  # Use specific quantization
    oi -l                   # List all available models
    oi -r -l                # Force refresh catalog from HuggingFace
    oi -x qwen3-8b-q4_k_m.gguf  # Remove an installed model
    oi -d microsoft/Phi-3-mini-4k-instruct-gguf/Phi-3-mini-4k-instruct.Q4_K_M.gguf

QUANTIZATION OPTIONS:
    Q2_K    - Smallest, fastest, lowest quality
    Q3_K_S  - Small and fast, decent quality
    Q3_K_M  - Balanced 3-bit
    Q4_K_M  - Recommended (default)
    Q4_K_L  - High quality 4-bit
    Q5_K_M  - Near-lossless quality
    Q6_K    - Very high quality
    Q8_0    - Best quality, largest

MODEL STORAGE:
    Models are stored in: ${MODELS_DIR}
EOF
}

draw_installed_menu() {
    _MENU_FILES=()
    _MENU_COUNT=0

    local w=$(get_term_width)
    local inner=$((w - 2))

    # Banner
    local title="oi - LLM Chat Interface"
    local pad_total=$((inner - ${#title}))
    local pad_left=$((pad_total / 2))
    local pad_right=$((pad_total - pad_left))
    echo -e "${CYAN}╔$(make_divider "$inner" "═")╗${NC}" >&2
    printf >&2 "${CYAN}║${NC}%*s%s%*s${CYAN}║${NC}\n" "$pad_left" "" "$title" "$pad_right" ""
    echo -e "${CYAN}╚$(make_divider "$inner" "═")╝${NC}" >&2

    # Scan installed models
    local files=()
    local sizes=()
    for f in "$MODELS_DIR"/*.gguf; do
        [ -f "$f" ] || continue
        local fsize
        fsize=$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f" 2>/dev/null)
        [ "$fsize" -lt 100000000 ] && continue  # skip files < 100MB
        files+=("$f")
        sizes+=("$(du -h "$f" 2>/dev/null | cut -f1)")
    done

    _MENU_FILES=("${files[@]}")
    _MENU_COUNT=${#files[@]}

    echo -e "${BLUE}Installed Models:${NC}" >&2
    echo -e "${BLUE}$(make_divider "$w")${NC}" >&2

    if [ $_MENU_COUNT -eq 0 ]; then
        echo "" >&2
        echo -e "  ${YELLOW}No models installed.${NC}" >&2
        echo -e "  Press ${CYAN}A${NC} to add models from HuggingFace." >&2
        echo "" >&2
    else
        local name_max=$((w - 16))
        (( name_max < 10 )) && name_max=10

        local i
        for (( i=0; i<_MENU_COUNT; i++ )); do
            local basename
            basename=$(basename "${files[$i]}")
            local display_name=$(truncate_str "$basename" "$name_max")
            printf >&2 "  %-3s %-${name_max}s %s\n" "$((i+1)))" "$display_name" "${sizes[$i]}"
        done
    fi

    echo -e "${BLUE}$(make_divider "$w")${NC}" >&2
    echo -e "  ${CYAN}A${NC})dd  ${CYAN}D${NC})elete  ${CYAN}Q${NC})uit" >&2
}

interactive_select_installed() {
    draw_installed_menu

    while true; do
        local w=$(get_term_width)
        local box_inner=$((w - 2))
        echo -e "${CYAN}┌$(make_divider "$box_inner")┐${NC}" >&2

        local prompt_text
        if [ $_MENU_COUNT -gt 0 ]; then
            prompt_text="│ Enter choice (1-${_MENU_COUNT}, A, D, Q): "
        else
            prompt_text="│ Enter choice (A, Q): "
        fi
        read -p "$prompt_text" choice
        echo -e "\r${CYAN}└$(make_divider "$box_inner")┘${NC}" >&2

        case "$choice" in
            [Aa])
                add_model_flow
                # Redraw menu after add
                draw_installed_menu
                continue
                ;;
            [Dd])
                if [ $_MENU_COUNT -eq 0 ]; then
                    echo -e "${YELLOW}No installed models to delete.${NC}" >&2
                    continue
                fi
                local del_files=()
                local del_i=1
                for f in "$MODELS_DIR"/*.gguf; do
                    [ -f "$f" ] || continue
                    local fsize
                    fsize=$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f" 2>/dev/null)
                    [ "$fsize" -lt 100000000 ] && continue
                    del_files+=("$(basename "$f")")
                    local del_size=$(du -h "$f" 2>/dev/null | cut -f1)
                    echo -e "  ${del_i}) $(basename "$f") (${del_size})" >&2
                    ((del_i++))
                done
                if [ ${#del_files[@]} -eq 0 ]; then
                    echo -e "${YELLOW}No installed models to delete.${NC}" >&2
                    continue
                fi
                read -p "Select model to delete (1-${#del_files[@]}, or C to cancel): " del_choice
                if [[ "$del_choice" =~ ^[Cc]$ ]]; then
                    draw_installed_menu
                    continue
                fi
                if [[ "$del_choice" =~ ^[0-9]+$ ]] && [ "$del_choice" -ge 1 ] && [ "$del_choice" -le ${#del_files[@]} ]; then
                    remove_model "${del_files[$((del_choice - 1))]}"
                    sleep 1
                else
                    echo -e "${RED}Invalid selection${NC}" >&2
                fi
                # Redraw menu after delete
                draw_installed_menu
                continue
                ;;
            [Qq])
                echo -e "${GREEN}Goodbye!${NC}" >&2
                return 2
                ;;
            *)
                if [[ "$choice" =~ ^[0-9]+$ ]]; then
                    local idx=$((choice - 1))
                    if [ $idx -ge 0 ] && [ $idx -lt $_MENU_COUNT ]; then
                        # Output filepath on stdout
                        echo "${_MENU_FILES[$idx]}"
                        return 0
                    else
                        echo -e "${RED}Invalid selection${NC}" >&2
                        continue
                    fi
                else
                    echo -e "${RED}Invalid input: $choice${NC}" >&2
                    continue
                fi
                ;;
        esac
    done
}
