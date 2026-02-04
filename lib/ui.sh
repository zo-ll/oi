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

draw_full_menu() {
    local w=$(get_term_width)
    local inner=$((w - 2))  # inside the box border chars

    # Output all UI to stderr so only the model ID goes to stdout
    # Dynamic banner
    local title="oi - LLM Chat Interface"
    local pad_total=$((inner - ${#title}))
    local pad_left=$((pad_total / 2))
    local pad_right=$((pad_total - pad_left))
    echo -e "${CYAN}╔$(make_divider "$inner" "═")╗${NC}" >&2
    printf >&2 "${CYAN}║${NC}%*s%s%*s${CYAN}║${NC}\n" "$pad_left" "" "$title" "$pad_right" ""
    echo -e "${CYAN}╚$(make_divider "$inner" "═")╝${NC}" >&2

    # Hardware section — compact if terminal is short
    local term_h
    term_h=$(tput lines 2>/dev/null) || term_h=40
    local hw=$(detect_hardware)
    local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    local ram=$(echo "$hw" | grep -o '"ram_gb": [0-9]*' | cut -d' ' -f2)
    echo -e "${CYAN}VRAM: ${vram}G  RAM: ${ram}G${NC}" >&2

    # Get all models and check which are installed
    local models=$(load_models)
    local ids=$(echo "$models" | grep '"id":' | cut -d'"' -f4)
    local installed=()

    for id in $ids; do
        local model_info=$(echo "$models" | grep -A 10 "\"id\": \"${id}\"" | head -10)
        local template=$(echo "$model_info" | grep '"filename_template":' | head -1 | cut -d'"' -f4)
        local filename=$(build_filename "$template" "$DEFAULT_QUANT")
        if is_model_installed "$filename"; then
            installed+=("$id")
        fi
    done

    # Build single list of all compatible models
    local compatible=$(get_compatible_models)
    local all_ids=()
    local all_names=()
    local all_status=()

    while IFS='|' read -r id name status; do
        [ -z "$id" ] && continue
        all_ids+=("$id")
        all_names+=("$name")
        all_status+=("$status")
    done <<< "$compatible"

    # Show single unified menu
    echo -e "${BLUE}Available Models:${NC}" >&2
    echo -e "${BLUE}$(make_divider "$w")${NC}" >&2

    # Compute how many models we can show
    local total_models=${#all_ids[@]}
    local max_models=$total_models
    # Reserve lines: 3 banner + hw(~8 or 1) + 2 header/divider + 3 footer + 2 prompt = ~18 or ~11
    local reserved_lines=10
    if (( term_h < 20 )); then reserved_lines=7; fi
    local avail_lines=$((term_h - reserved_lines))
    if (( avail_lines < 3 )); then avail_lines=3; fi
    if (( max_models > avail_lines )); then
        max_models=$((avail_lines - 1))  # leave room for overflow indicator
    fi

    # Figure out name column width: w - "  N) " (5) - " ● GPU" (6) - " ✓" (2) - padding (2)
    local name_max=$((w - 16))
    (( name_max < 10 )) && name_max=10

    local i=1
    for idx in "${!all_ids[@]}"; do
        if (( i > max_models )) && (( total_models > max_models )); then
            local remaining=$((total_models - max_models))
            echo -e "  ${YELLOW}... ${remaining} more (press L)${NC}" >&2
            break
        fi

        local id="${all_ids[$idx]}"
        local name="${all_names[$idx]}"
        local status="${all_status[$idx]}"

        local status_str=""
        if [ "$status" = "gpu" ]; then
            status_str="${GREEN}●${NC} GPU"
        else
            status_str="${YELLOW}●${NC} CPU"
        fi

        # Truncate name and use short installed marker
        local display_name=$(truncate_str "$name" "$name_max")
        local installed_marker=""
        if is_model_in_array "$id" "${installed[@]}"; then
            installed_marker=" ${GREEN}✓${NC}"
        fi

        printf >&2 "  %-3s %-${name_max}s %s%s\n" "$i)" "$display_name" "$status_str" "$installed_marker"
        ((i++))
    done

    echo -e "${BLUE}$(make_divider "$w")${NC}" >&2
    echo -e "  ${CYAN}L${NC})ist  ${CYAN}D${NC})elete  ${CYAN}H${NC})ardware  ${CYAN}Q${NC})uit" >&2
}

interactive_select() {
    # Draw the full menu once at startup
    draw_full_menu

    # Get all models and check which are installed (needed for the choice handling)
    local models=$(load_models)
    local ids=$(echo "$models" | grep '"id":' | cut -d'"' -f4)
    local installed=()

    for id in $ids; do
        local model_info=$(echo "$models" | grep -A 10 "\"id\": \"${id}\"" | head -10)
        local template=$(echo "$model_info" | grep '"filename_template":' | head -1 | cut -d'"' -f4)
        local filename=$(build_filename "$template" "$DEFAULT_QUANT")
        if is_model_installed "$filename"; then
            installed+=("$id")
        fi
    done

    # Build single list of all compatible models
    local compatible=$(get_compatible_models)
    local all_ids=()
    local all_names=()
    local all_status=()

    while IFS='|' read -r id name status; do
        [ -z "$id" ] && continue
        all_ids+=("$id")
        all_names+=("$name")
        all_status+=("$status")
    done <<< "$compatible"

    # Main interaction loop - only redraws the prompt after L/H commands
    while true; do
        # Read selection - only redraw this prompt line
        local w=$(get_term_width)
        local box_inner=$((w - 2))
        echo -e "${CYAN}┌$(make_divider "$box_inner")┐${NC}" >&2
        read -p "│ Enter choice (1-${#all_ids[@]}, L, D, H, Q): " choice
        # Clear the input line and draw bottom border on same line
        echo -e "\r${CYAN}└$(make_divider "$box_inner")┘${NC}" >&2

        case "$choice" in
            [Ll])
                list_models >&2
                echo "" >&2
                sleep 2
                # Continue loop to redraw only the prompt
                continue
                ;;
            [Hh])
                show_hardware >&2
                echo "" >&2
                sleep 1
                # Continue loop to redraw only the prompt
                continue
                ;;
            [Dd])
                # List installed models for selection (skip small vocab/test files)
                local del_files=()
                local del_i=1
                for f in "$MODELS_DIR"/*.gguf; do
                    [ -f "$f" ] || continue
                    local fsize=$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f" 2>/dev/null)
                    [ "$fsize" -lt 100000000 ] && continue  # skip files < 100MB
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
                    continue
                fi
                if [[ "$del_choice" =~ ^[0-9]+$ ]] && [ "$del_choice" -ge 1 ] && [ "$del_choice" -le ${#del_files[@]} ]; then
                    remove_model "${del_files[$((del_choice - 1))]}"
                    sleep 1
                else
                    echo -e "${RED}Invalid selection${NC}" >&2
                fi
                continue
                ;;
            [Qq])
                echo -e "${GREEN}Goodbye!${NC}" >&2
                return 2
                ;;
            *)
                if [[ "$choice" =~ ^[0-9]+$ ]]; then
                    local idx=$((choice - 1))

                    # Simple index lookup in unified list
                    if [ $idx -ge 0 ] && [ $idx -lt ${#all_ids[@]} ]; then
                        local selected_id="${all_ids[$idx]}"
                        # This goes to stdout - the only thing captured by command substitution
                        echo "$selected_id"
                        return 0
                    else
                        echo -e "${RED}Invalid selection${NC}" >&2
                        # Continue loop to redraw only the prompt
                        continue
                    fi
                else
                    # Try direct ID
                    if get_model_info "$choice" > /dev/null; then
                        echo "$choice"
                        return 0
                    else
                        echo -e "${RED}Unknown model ID: $choice${NC}" >&2
                        # Continue loop to redraw only the prompt
                        continue
                    fi
                fi
                ;;
        esac
    done
}
