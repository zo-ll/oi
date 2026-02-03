# TODO — oi script improvements

Each task targets `/home/az/local_script/oi` and is self-contained.

---

## 1. Fix model list number alignment

**File:** `oi`, function `draw_full_menu()`, line 794
**Current behavior:** The `printf` uses `%-2s` for the number column (`"$i)"`). Numbers 1–9 produce 2-char strings like `1)`, but `10)` is 3 chars, causing that row and all subsequent rows to shift right by one space.
**Desired behavior:** All rows align identically regardless of digit count.

**Current code (line 794):**
```bash
printf >&2 "  %-2s %-${name_max}s %s%s\n" "$i)" "$display_name" "$status_str" "$installed_marker"
```

**Steps:**
1. Change `%-2s` to `%-3s` on line 794.
2. On line 765, change `name_max` from `w - 15` to `w - 16` to compensate for the extra column character.

**After:**
```bash
# line 765
local name_max=$((w - 16))

# line 794
printf >&2 "  %-3s %-${name_max}s %s%s\n" "$i)" "$display_name" "$status_str" "$installed_marker"
```

---

## 2. Compact the interactive menu layout

**File:** `oi`, functions `draw_full_menu()` (line 694) and `show_hardware()` (line 136)
**Current behavior:** The menu uses 3 banner lines + full hardware block (~8 lines) + divider + models + divider + 4 footer lines. On a typical 24-line terminal, this leaves room for only ~6 models before truncation.
**Desired behavior:** Reduce vertical chrome so more models are visible without scrolling.

**Steps:**

### 2a. Always use single-line hardware summary in the menu
In `draw_full_menu()`, lines 708–719, remove the `if (( term_h < 20 ))` conditional and always show the compact one-line hardware summary. The full `show_hardware` display remains available via the `H` key.

**Current (lines 708–719):**
```bash
local term_h
term_h=$(tput lines 2>/dev/null) || term_h=40
if (( term_h < 20 )); then
    local hw=$(detect_hardware)
    local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
    local ram=$(echo "$hw" | grep -o '"ram_gb": [0-9]*' | cut -d' ' -f2)
    echo -e "${CYAN}VRAM: ${vram}G  RAM: ${ram}G${NC}" >&2
else
    show_hardware >&2
fi
```

**After:**
```bash
local term_h
term_h=$(tput lines 2>/dev/null) || term_h=40
local hw=$(detect_hardware)
local vram=$(echo "$hw" | grep -o '"vram_gb": [0-9.]*' | cut -d' ' -f2)
local ram=$(echo "$hw" | grep -o '"ram_gb": [0-9]*' | cut -d' ' -f2)
echo -e "${CYAN}VRAM: ${vram}G  RAM: ${ram}G${NC}" >&2
```

### 2b. Collapse footer options onto one line
Replace the four separate footer `echo` statements (lines 799–802) with a single line.

**Current (lines 799–802):**
```bash
echo -e "  ${CYAN}L${NC}) List all available models" >&2
echo -e "  ${CYAN}D${NC}) Delete an installed model" >&2
echo -e "  ${CYAN}H${NC}) Show hardware info" >&2
echo -e "  ${CYAN}Q${NC}) Quit" >&2
```

**After:**
```bash
echo -e "  ${CYAN}L${NC})ist  ${CYAN}D${NC})elete  ${CYAN}H${NC})ardware  ${CYAN}Q${NC})uit" >&2
```

### 2c. (Optional) Slim the banner to one line
Replace the 3-line box banner (lines 700–706) with a single styled line.

**Current (lines 700–706):**
```bash
local title="oi - LLM Chat Interface"
local pad_total=$((inner - ${#title}))
local pad_left=$((pad_total / 2))
local pad_right=$((pad_total - pad_left))
echo -e "${CYAN}╔$(make_divider "$inner" "═")╗${NC}" >&2
printf >&2 "${CYAN}║${NC}%*s%s%*s${CYAN}║${NC}\n" "$pad_left" "" "$title" "$pad_right" ""
echo -e "${CYAN}╚$(make_divider "$inner" "═")╝${NC}" >&2
```

**After:**
```bash
echo -e "${CYAN}── oi - LLM Chat Interface ──${NC}" >&2
```

---

## 3. Add a spinner during catalog fetch

**File:** `oi`, function `fetch_hf_models()`, line 187
**Current behavior:** Prints `"Updating model catalog from HuggingFace..."` then blocks silently while the Python subprocess runs (can take 10–30 s).
**Desired behavior:** A rotating spinner runs alongside the status message so the user knows the process is alive.

**Steps:**
1. After the status message on line 187, launch the `python3` heredoc in a background subshell and capture its PID.
2. Run a spinner loop that prints and erases braille spinner chars (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) while the PID is alive.
3. On completion, kill the spinner, clear the spinner character, and return the exit status of the python process.

**Implementation sketch (replace lines 187–336):**
```bash
echo -ne "${YELLOW}Updating model catalog from HuggingFace... ${NC}" >&2

python3 - "$CACHE_FILE" "$HF_ORGS" "$total_mem" <<'PYEOF' &
# ... existing python code unchanged ...
PYEOF
local py_pid=$!

# Spinner
local spin_chars='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
local i=0
while kill -0 "$py_pid" 2>/dev/null; do
    printf '\b%s' "${spin_chars:i%${#spin_chars}:1}" >&2
    ((i++))
    sleep 0.1
done
printf '\b \b' >&2  # clear spinner char

wait "$py_pid"
local py_exit=$?
if [ $py_exit -eq 0 ]; then
    echo -e "${GREEN}done${NC}" >&2
else
    echo -e "${RED}failed${NC}" >&2
fi
return $py_exit
```

Note: The heredoc with `&` backgrounding requires careful quoting. Test that `$CACHE_FILE`, `$HF_ORGS`, and `$total_mem` are expanded before backgrounding (they will be, since heredoc delimiter is quoted with `'PYEOF'` but the variables are shell-level arguments to `python3`).

---

## 4. Add visible feedback after interactive model deletion

**File:** `oi`, the `[Dd]` case in `interactive_select()`, line 884
**Current behavior:** `remove_model()` prints `"Deleted: file"` in green, but the interactive menu immediately reprints the prompt line — the confirmation scrolls away or is missed.
**Desired behavior:** After a successful deletion, pause briefly so the user can see the confirmation message.

**Current code (lines 883–887):**
```bash
if [[ "$del_choice" =~ ^[0-9]+$ ]] && [ "$del_choice" -ge 1 ] && [ "$del_choice" -le ${#del_files[@]} ]; then
    remove_model "${del_files[$((del_choice - 1))]}"
else
    echo -e "${RED}Invalid selection${NC}" >&2
fi
```

**Steps:**
Add a `sleep 1` (or a "Press any key" prompt) after the `remove_model` call so the deletion message is visible before the prompt redraws.

**After:**
```bash
if [[ "$del_choice" =~ ^[0-9]+$ ]] && [ "$del_choice" -ge 1 ] && [ "$del_choice" -le ${#del_files[@]} ]; then
    remove_model "${del_files[$((del_choice - 1))]}"
    sleep 1
else
    echo -e "${RED}Invalid selection${NC}" >&2
fi
```

Alternatively, for a more explicit UX:
```bash
    remove_model "${del_files[$((del_choice - 1))]}"
    read -n 1 -s -r -p "Press any key to continue..." >&2
    echo >&2
```

---

## 5. Extract inline Python into its own file

**File:** `oi`, function `fetch_hf_models()`, lines 188–335
**Current behavior:** A ~150-line Python script is embedded inline via heredoc inside the bash function. This makes the bash file harder to read, harder to lint/test the Python independently, and prevents editor tooling (syntax highlighting, LSP) from working on the Python code.
**Desired behavior:** The Python code lives in its own file at `lib/fetch_hf_models.py` and the bash function calls it directly.

**Steps:**
1. Create `lib/fetch_hf_models.py` containing the existing Python code from the heredoc (lines 189–335), adapted to be a standalone script.
2. Keep the same CLI interface: `python3 lib/fetch_hf_models.py "$CACHE_FILE" "$HF_ORGS" "$total_mem"` — the script already reads `sys.argv[1..3]` and writes to `cache_path`, so no argument changes are needed.
3. Replace the heredoc block in `fetch_hf_models()` (lines 188–335) with:
   ```bash
   python3 "${LIB_DIR}/fetch_hf_models.py" "$CACHE_FILE" "$HF_ORGS" "$total_mem"
   ```
4. Ensure the `.py` file is executable or invoked via `python3` explicitly (the latter is safer).
5. If task 3 (spinner) is also implemented, the backgrounding (`&`) works the same way — just background the `python3` command instead of the heredoc.

---

## 6. Consider rewriting the HF fetcher in Go instead of Python

**File:** `lib/fetch_hf_models.py` (after task 5) or the inline heredoc (lines 188–335)
**Current behavior:** The HuggingFace model catalog fetch uses Python with `urllib`. It requires a Python 3 installation on the host and takes 10–30 s due to sequential HTTP requests.
**Desired behavior:** Evaluate whether a Go implementation would be a better fit.

**Reasons to consider Go:**
- Compiles to a single static binary — no runtime dependency (Python 3 may not be installed everywhere).
- Native concurrency (`goroutines`) makes it trivial to parallelize the per-org/per-size API calls, which could cut fetch time significantly.
- Smaller deployment footprint (one binary vs. requiring `python3` on `$PATH`).

**Reasons to keep Python:**
- Faster to iterate on and modify.
- The script is simple enough that Python's startup overhead is negligible vs. network time.
- Already works; rewriting is effort with no new functionality.

**Steps (if proceeding):**
1. Create `lib/fetch_hf_models.go` implementing the same logic: accept `cache_path`, `hf_orgs`, and `total_mem_gb` as CLI args, write JSON to `cache_path`.
2. Use `net/http` for API calls and `sync.WaitGroup` / goroutines to fetch all org+size combinations concurrently.
3. Build with `go build -o lib/fetch_hf_models lib/fetch_hf_models.go`.
4. Update `fetch_hf_models()` in `oi` to call `"${LIB_DIR}/fetch_hf_models" "$CACHE_FILE" "$HF_ORGS" "$total_mem"`.
5. Keep the Python version as a fallback or remove it once the Go binary is validated.
