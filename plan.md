# Feature Plan: Installed Models Only

## Overview
Restructure main `oi` interface to show **only installed models** in the main menu. Remove "Available Models" catalog section. Allow manual model addition through 'A' option that fetches from HuggingFace, lets user select and download.

---

## Current Behavior (To Be Removed)

### Main Menu Shows:
- "Available Models:" section
- List of 7 static models from `models.json`
- List of user-added models from `user_models.json`
- List of cached HF models
- Compatibility indicators (GPU/CPU)
- Installed markers (✓)
- Suggested/recommended models

### Problems with Current Approach:
- Overwhelming - too many models shown at once
- Users only need to see what they actually have installed
- "Available" models clutter the UI
- Confusion about what's available vs installed
- No clear call to action

---

## Desired Behavior (To Implement)

### 1. Main Menu - Installed Models Only

**When user runs `oi`:**

```bash
# Check for installed models
installed_models = list files in MODELS_DIR
count = number of .gguf files

if count == 0:
    # NO catalog loading
    # NO static models shown
    # NO user models shown
    # NO cached models shown
    # ONLY show empty state
    
    show "═══════════════════════════"
    show "  No models installed"
    show ""
    show "  Add models with [A]dd to get started"
    show ""
    show "Actions: [A]dd  [Q]uit"
    show "═════════════════════════"
else:
    # Show installed models only
    # Proceed to main menu
    show "═════════════════════════"
    show "  Installed Models"
    show "═════════════════════════"
    
    show numbered list of installed models with:
        - Model name
        - File size
        - Quick actions: [C]hat, [D]elete
    
    show ""
    show "Actions: [A]dd  [D]elete  [Q]uit"
    show "═══════════════════════════"
fi
```

**Visual Layout:**
```
═══════════════════════════
  Installed Models
═══════════════════════════
  
  1) Llama-3.1-8B-Q4_K_M.gguf     4.7GB  ●
  2) Qwen3-8B-Q4_K_M.gguf          4.2GB  ●
  3) Anima-Phi-Neptune-GGUF.gguf      6.2GB  ●
  
Actions: [A]dd  [D]elete  [Q]uit
═══════════════════════════
```

**Key Points:**
- **No model catalog display on first run**
- **No suggestions or recommendations**
- **Clear call to action**: Add models to get started
- **Minimal, clean interface**
- Installed models sorted alphabetically or by size

---

### 2. First Run/Installation Behavior

**When `oi` is first run or on fresh installation:**

```bash
# Check for installed models
installed_count = count files in MODELS_DIR

if installed_count == 0:
    # NO catalog loading
    # NO static models shown
    # NO user models shown
    # NO cached models shown
    # ONLY show empty state
    
    show "═════════════════════════"
    show "  No models installed"
    show ""
    show "  Add models with [A]dd to get started"
    show ""
    show "Actions: [A]dd  [Q]uit"
    show "═══════════════════════════"
else:
    # Show installed models only
    # Proceed to main menu
fi
```

**Key Points:**
- **No model catalog display on first run**
- **No suggestions or recommendations**
- **Clear call to action**: Add models to get started
- **Minimal, clean interface**
- User can immediately start using 'A' to add first model

---

### 3. Option A - Manual Model Addition Workflow

**When user presses 'A':**

```
═══════════════════════════════
  Add Custom Model
═════════════════════════════════
  
How would you like to add a model?
  
  1) Search HuggingFace by name
     Search for models (e.g., 'llama', 'qwen')
     Browse catalog results with hardware compatibility
     Select from results
  
  2) Enter repository path directly
     Type full repo (e.g., 'Bedovyy/Anima-GGUF')
     Skip search, go straight to selection
  
  Q) Cancel and return to installed models
  
Your choice (1, 2, or Q):
```

#### 3a. Option 1 - Search Flow

```
Enter search term (e.g., 'llama', 'qwen', 'anima'):
> llama
Searching HuggingFace...

═══════════════════════════════
  Search Results (sorted by compatibility)
═══════════════════════════════════
  
  1) Llama-3.1-8B               [✓ Compatible] 42.5K downloads
  2) Llama-3.1-70B              [⚠ Needs 42GB VRAM]
  3) Anima-7B                     [✓ Compatible] 12K downloads
  
  0) Enter repository directly (go back to search)
  
Select model number (1-15) or press Q to cancel:
> 1

Selected: meta-llama/Llama-3.1-8B-GGUF

═══════════════════════════════════════
  Available Quantizations
═══════════════════════════════════════
  
  1) Q2_K    - 2.8GB (Low quality, smallest)
  2) Q4_K_M  - 4.7GB (Recommended balance ⭐)
  3) Q5_K_M  - 5.6GB (Near-lossless)
  4) Q8_0    - 9.4GB (Best quality)
  
  0) Enter repository path directly (go back to search)
  
Select quantization (1-4) or Q to cancel:
> 2

═════════════════════════════════════
  Hardware Compatibility Check
═══════════════════════════════════════════
  
Model: Llama-3.1-8B-Q4_K_M.gguf
File size: 4.7GB

Requirements:
  VRAM needed: 5.3GB
  Your VRAM: 7.9GB
  
  Status: ✓ Compatible with your GPU
  
✓ This model can run on your system!

Download and add this model? [Y/n]:
> Y

Downloading Llama-3.1-8B-Q4_K_M.gguf...
████████████████████████████████████ 100%
✓ Download complete
✓ Successfully added model: llama-3.1-8b

The model is now available in your main list.

[Press Enter to return to installed models]
```

#### 3b. Option 2 - Direct Repo Entry

```
Enter repository path (format: username/model-name):
Examples: Bedovyy/Anima-GGUF, bartowski/Llama-3.1-8B-GGUF
> Bedovyy/Anima-GGUF

Verifying repository...
✓ Repository verified

═════════════════════════════════════════
  Available Quantizations
═══════════════════════════════════════════
  [Same display as search flow]
  
Select quantization:
> 1
[Same hardware check and download]
```

---

### 4. Model Lifecycle

**After adding a model:**

1. **Download completes** → File saved to `~/.local/share/oi/llama.cpp/models/`
2. **Added to catalog** → Entry in `~/.local/share/oi/lib/user_models.json`
3. **Available immediately** → Model appears in main menu on next run
4. **No separation** → No distinction between "installed" and "available"

**Key principle:** Once a model is added, it's just like any other model - available for use.

---

### 5. Model Deletion Workflow

**When user presses 'D' from installed models menu:**

```
Installed Models (3)
═══════════════════════════════════
  
  1) Llama-3.1-8B-Q4_K_M.gguf     4.7GB
  2) Qwen3-8B-Q4_K_M.gguf          4.2GB
  3) Anima-Phi-Neptune-GGUF.gguf    6.2GB
  
Actions: [A]dd  [D]elete  [Q]uit
  
Delete model number (1-3) or press Q to cancel:
> 2

Are you sure you want to delete Anima-Phi-Neptune-GGUF.gguf? [y/N]:
> y

✓ Model deleted
```

**After deletion:**
- List redisplays with remaining models
- No additional prompt needed
- Immediate action taken

---

## Files to Modify

### 1. `oi` (main script)

**Changes needed:**

```bash
# New function to get installed models list
get_installed_models_list() {
    local models_dir="${MODELS_DIR}"
    local installed=()
    
    if [ ! -d "$models_dir" ]; then
        echo "[]"
        return
    fi
    
    for f in "$models_dir"/*.gguf; do
        if [ -f "$f" ]; then
            installed+=("$(basename "$f")")
        fi
    done
    
    # Output as JSON array
    echo "[\"$(IFS=','; echo "${installed[*]}" | sed 's/","/\", "/g')\"]"
}

# New function to show only installed models
show_installed_models_only() {
    local w=$(get_term_width)
    local models=$(get_installed_models_list)
    local count=$(echo "$models" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null)
    
    echo ""
    echo -e "${CYAN}═══════════════════════════════${NC}"
    echo -e "${CYAN}  Installed Models ($count)${NC}"
    echo -e "${CYAN}═══════════════════════════════════${NC}"
    echo ""
    
    if [ "$count" -eq 0 ]; then
        echo -e "${YELLOW}No models installed${NC}"
        echo ""
        echo -e "${YELLOW}Add models with ${CYAN}[A]${YELLOW}]${NC}dd to get started${NC}"
        echo ""
        echo -e "${CYAN}Actions: ${CYAN}[${YELLOW}A${CYAN}]${NC}dd ${CYAN}[${YELLOW}Q${CYAN}]${NC}uit${NC}"
        echo -e "${CYAN}═════════════════════════════════════${NC}"
    else
        local i=1
        local max_name_len=$((w - 16))  # Account for "N) M" - 3
        
        # Display each installed model
        for model_file in "${installed[@]}"; do
            local filename=$(basename "$model_file")
            local size_bytes=$(stat -c%s "$model_file" 2>/dev/null || stat -f%z "$model_file" 2>/dev/null)
            local size_str
            
            if [ "$size_bytes" -lt 1073741824 ]; then
                size_str="$(echo "$size_bytes / 1048576" | bc -l)GB"
            elif [ "$size_bytes" -lt 1073741824 ]; then
                size_str="$(echo "$size_bytes / 1048576" | bc -l)MB"
            else
                size_str="$((size_bytes / 1024 / 1024))GB"
            fi
            
            # Truncate name if needed
            local display_name="$filename"
            if [ ${#filename} -gt $max_name_len ]; then
                display_name="${filename:0:$((max_name_len - 3))}..."
            fi
            
            printf "  %2d) %-35s %6s\n" "$i" "$display_name" "$size_str"
            ((i++))
        done
        
        echo ""
        echo -e "${CYAN}Actions:${NC}"
        echo -e "  ${CYAN}[${YELLOW}A${CYAN}]${NC}dd  ${CYAN}[${YELLOW}D${CYAN}]${NC}elete  ${CYAN}[${YELLOW}Q${CYAN}]${NC}uit${NC}"
        echo ""
    fi
}

# New function to delete a specific model
delete_installed_model() {
    local models_dir="${MODELS_DIR}"
    local models=()
    
    if [ ! -d "$models_dir" ]; then
        return
    fi
    
    for f in "$models_dir"/*.gguf; do
        if [ -f "$f" ]; then
            models+=("$(basename "$f")")
        fi
    done
    
    if [ ${#models[@]} -eq 0 ]; then
        echo -e "${YELLOW}No models to delete${NC}"
        return 1
    fi
    
    echo ""
    echo -e "${CYAN}Installed Models:${NC}"
    echo ""
    
    local i=1
    for model in "${models[@]}"; do
        echo "  $i) $model"
        ((i++))
    done
    
    echo ""
    read -p "Delete model number (1-${#models[@]}) or press Q to cancel: " del_choice
    
    if [[ "$del_choice" =~ ^[Qq]$ ]]; then
        return 1
    fi
    
    if ! [[ "$del_choice" =~ ^[0-9]+$ ]] || [ "$del_choice" -lt 1 ] || [ "$del_choice" -gt ${#models[@]} ]; then
        echo -e "${RED}Invalid selection${NC}"
        return 1
    fi
    
    local model_to_delete="${models[$((del_choice - 1))]}"
    
    rm -f "${MODELS_DIR}/${model_to_delete}"
    echo -e "${GREEN}✓ Model deleted${NC}"
    
    # Also remove from user_models.json if present
    local user_models_file="${LIB_DIR}/user_models.json"
    if [ -f "$user_models_file" ]; then
        python3 -c "
import json, sys
try:
    with open('$user_models_file', 'r') as f:
        data = json.load(f)
    data['models'] = [m for m in data['models'] if m.get('source') != 'manual']
    with open('$user_models_file', 'w') as f:
        json.dump(data, f, indent=2)
except:
    pass
" 2>/dev/null
    fi
    
    return 0
}

# Modify main function to use installed-only view
main() {
    # ... existing argument parsing ...
    
    # Check llama.cpp setup
    check_llama_cpp
    
    # Handle list modes
    if [ "$list_mode" = "all" ]; then
        list_models
        exit 0
    elif [ "$list_mode" = "installed" ]; then
        show_installed_models_only
        exit 0
    fi
    
    # Main flow - ONLY installed models
    if [ -z "$model_id" ]; then
        show_installed_models_only
        
        # Get user selection
        while true; do
            echo ""
            read -p "Enter choice or [A]dd, [D]elete, [Q]uit: " choice
            
            case "$choice" in
                [Aa])
                    # Call manual_add_model_flow
                    # After adding, redisplay installed models
                    show_installed_models_only
                    ;;
                [Dd])
                    # Delete a specific model
                    delete_installed_model
                    # After deleting, redisplay list
                    show_installed_models_only
                    ;;
                [Qq])
                    return 0
                    ;;
                *)
                    # Try to use as model ID
                    if get_model_info "$choice" > /dev/null; then
                        launch_chat "$choice" "$quant" "$context" "$threads"
                    else
                        echo -e "${RED}Unknown model: $choice${NC}"
                    ;;
            esac
        done
    else
        # Direct model launch
        launch_chat "$model_id" "$quant" "$context" "$threads"
    fi
}
```

**Remove:**
- `draw_full_menu()` function
- `interactive_select()` function (not needed with new flow)

---

### 2. `lib/ui.sh`

**Changes:**

**Remove:**
- `draw_full_menu()` function
- `interactive_select()` function

**Keep:**
- `get_term_width()`
- `truncate_str()`
- `show_help()`
- Colors
- All other helper functions

---

### 3. `lib/models.sh`

**No changes** - Keep all functions as-is (still needed for `manual_add.sh`)

---

### 4. `lib/manual_add.sh`

**No changes** - Keep all functions as-is

---

### 5. New Files

**None** - All changes are modifications to existing files

---

## Implementation Order

1. [ ] Create `show_installed_models_only()` function in `oi`
2. [ ] Create `get_installed_models_list()` function in `oi`
3. [ ] Create `delete_installed_model()` function in `oi`
4. [ ] Modify `main()` function to use installed-only view
5. [ ] Remove `draw_full_menu()` function from `lib/ui.sh`
6. [ ] Remove `interactive_select()` function from `lib/ui.sh`
7. [ ] Test: No models installed → show correct message
8. [ ] Test: With models → show numbered list
9. [ ] Test: Press 'A' → call manual_add_model_flow
10. [ ] Test: Press 'D' → select and delete → redisplay
11. [ ] Test: Press 'Q' → exit
12. [ ] Test: Multiple add/delete cycles work correctly
13. [ ] Test: Existing models from user_models.json appear in list
14. [ ] Test: Fresh installation (no models) shows correct message
15. [ ] Test: User can type model ID to launch (if in user catalog)
16. [ ] Test: Delete removes from user_models.json
17. [ ] Test: Refresh not needed (removed from options)

---

## User Flow Examples

### First Run (No Models)
```
$ oi
╔═══════════════════════════════╗
║                           oi - LLM Chat Interface                    ║
╚════════════════════════════════╝
VRAM: 7.9G  RAM: 7G

═══════════════════════════════════
  No models installed
═══════════════════════════════════

  Add models with [A]dd to get started

Actions: [A]dd  [Q]uit
═══════════════════════════════════

Enter choice or [A]dd, [D]elete, [Q]uit: A
```

### After Adding Model
```
$ oi
╔═════════════════════════════════╗
║                           oi - LLM Chat Interface                    ║
╚════════════════════════════════════╝
VRAM: 7.9G  RAM: 7G

═════════════════════════════════════
  Installed Models (1)
═════════════════════════════════════

  1) Llama-3.1-8B-Q4_K_M.gguf     4.7GB

Actions: [A]dd  [D]elete  [Q]uit
═══════════════════════════════════

Enter choice or [A]dd, [D]elete, [Q]uit: Q
[Exit back to shell]
```

### Delete Model
```
$ oi
...
Installed Models (1)
═══════════════════════════════════

  1) Llama-3.1-8B-Q4_K_M.gguf     4.7GB

Actions: [A]dd  [D]elete  [Q]uit

Delete model number (1-1) or press Q to cancel: 1
Are you sure you want to delete Llama-3.1-8B-Q4_K_M.gguf? [y/N]: y
✓ Model deleted

Installed Models (0)
═══════════════════════════════════

  No models installed

Actions: [A]dd  [Q]uit
═══════════════════════════════════
```

---

## Key Decisions

### Decision 1: Model Identification
**Decision**: When user types anything other than 'A', 'D', or 'Q', try to find matching model in installed list by filename/ID match
**Rationale**: User might want to launch a model they already have installed
**Implementation**: Check if input matches any installed model name

### Decision 2: Delete Confirmation
**Decision**: No explicit confirmation prompt, just delete immediately
**Rationale**: Keeps UI simple, user can re-add if they made a mistake
**Implementation**: Show "Are you sure? [y/N]" prompt

### Decision 3: Quick Actions
**Decision**: No keyboard shortcuts (like 'r' for refresh)
**Rationale**: Keep interface simple, use clear letters
**Implementation**: Only 'A', 'D', 'Q' options

### Decision 4: Back Navigation
**Decision**: After any action (add/delete), automatically redisplay updated list
**No user input required**: List refreshes immediately after action completes
**Implementation**: Show installed list again without requiring user to press a key

---

## Questions Before Implementation

All decisions documented above. Ready to implement once confirmed.

---

## Notes

- No refresh functionality needed (removed from options)
- Focus on simplicity and clarity
- Installed models are sorted alphabetically or by download time
- Clean separation between installed models and adding new ones
- Manual model addition flow (from `manual_add.sh`) remains unchanged
