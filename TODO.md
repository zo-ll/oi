# TODO — oi script improvements

Each task targets `~/.local/share/oi/oi` and is self-contained.

---

## ~~1. Fix model list number alignment~~ ✅

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

## ~~2. Compact the interactive menu layout~~ ✅ (2a, 2b done; 2c skipped)

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

## ~~3. Add a spinner during catalog fetch~~ ✅

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

## ~~4. Add visible feedback after interactive model deletion~~ ✅

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

## ~~5. Extract inline Python into its own file~~ ✅

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

---

## 7. Add chat memory / conversation persistence across sessions

**Current behavior:** Each chat session starts fresh with no history.
**Desired behavior:** Conversation history is saved and can be resumed across sessions.

**Steps:**
1. Create `lib/session.sh` with session management functions:
   - `save_session()`: Export conversation history to JSON format
   - `load_session()`: Import JSON and restore conversation context
   - Session format:
     ```json
     {
       "model": "qwen3-8b",
       "created": "2025-02-04T18:00:00Z",
       "messages": [
         {"role": "user", "content": "...", "timestamp": "..."},
         {"role": "assistant", "content": "...", "timestamp": "..."}
       ]
     }
     ```
2. Add CLI flags to main():
   - `--save-session <path>`: Save conversation to file
   - `--load-session <path>`: Load and resume conversation
3. Modify `launch_chat()` to:
   - If `--load-session` is set, prepend message history to initial prompt
   - Maintain session state in memory (array of messages with timestamps)
   - On `--save-session` or exit, write session to file
4. Auto-save last N sessions to `~/.local_oi_sessions/` directory
5. Add session listing command: `oi --list-sessions`

---

## 8. Add terminal shortcuts and slash commands

**File:** `launch_chat()` in `oi`, new signal handlers

**Current behavior:** Only standard readline behavior (arrow keys for history). No special commands or shortcuts during chat.

**Desired behavior:** Add keyboard shortcuts and slash commands for common actions (clear, help, regenerate, parameter adjustment).

**Steps:**
1. Define slash commands in `launch_chat()` input loop:
   - `/help`: Show available commands
   - `/clear`: Clear conversation history (but keep model loaded)
   - `/system <prompt>`: Set or update system prompt
   - `/model <name>`: Switch model mid-chat
   - `/temp <0.0-1.0>`: Adjust temperature
   - `/exit`: Exit chat
2. Implement signal handlers in `launch_chat()`:
   - Trap `SIGINT` (Ctrl+C):
     - First press: Regenerate last assistant response
     - Second press (within 2s): Exit chat
   - Trap `SIGWINCH`: Handle terminal resize
3. Add inline help display on `/help` command:
   ```
   Available commands:
   /help       Show this help
   /clear      Clear conversation
   /system     Set system prompt
   /model      Switch model
   /temp       Adjust temperature
   /exit       Exit chat
   ```
4. Use `read -e` with custom key bindings if terminal supports it
5. Store conversation history in memory array for `/clear` functionality

---

## 9. Add session export formats

**File:** New `lib/export.sh`, modify session save logic in task 7

**Current behavior:** Only internal JSON format for sessions. Cannot easily share or document conversations.

**Desired behavior:** Export conversations in multiple formats (Markdown, plain text, JSON).

**Steps:**
1. Create `lib/export.sh` with `export_session()` function:
   - Accept session file path and format type flag
   - `export_session session.json --md`: Markdown with code blocks and headers
   - `export_session session.json --txt`: Plain text transcript
   - `export_session session.json --json`: Pretty-printed JSON (same as save)
2. Add CLI flags to main():
   - `--export-md <path>`: Export session as Markdown
   - `--export-txt <path>`: Export session as plain text
3. Markdown export format:
   ```markdown
   # Chat Session - qwen3-8b
   **Date:** 2025-02-04 18:00:00
   **Model:** qwen3-8b (Q4_K_M, context: 4096)

   ## User
   Can you explain recursion?

   ## Assistant
   Recursion is a programming technique where...
   ```
4. Text export format:
   - Remove markdown formatting
   - Simple `User:` and `Assistant:` prefixes
   - Timestamps in parentheses
5. Add metadata to exports (model, quantization, parameters) for documentation

---

## 10. Add custom model registry

**File:** `lib/models.json`, new function `register_local_model()` in `oi`

**Current behavior:** Only pre-configured HuggingFace models are available in `models.json`. Users with local GGUF files cannot easily add them to the catalog.

**Desired behavior:** Users can register custom local GGUF models and use them like catalog models (with auto-selection and metadata).

**Steps:**
1. Extend `lib/models.json` structure to include `custom_models` array:
   ```json
   {
     "models": [...],
     "custom_models": [
       {
         "id": "my-custom",
         "name": "My Custom Model",
         "path": "/absolute/path/to/model.gguf",
         "min_vram_gb": 4.0,
         "description": "Custom fine-tuned model for X",
         "tags": ["custom", "finetuned"]
       }
     ]
   }
   ```
2. Create `register_local_model()` function in `oi`:
   - Accept path to GGUF file as argument
   - Detect file size for VRAM estimation (1GB ≈ 1GB VRAM)
   - Prompt user for name, description, and tags
   - Validate model file exists and is readable
   - Add entry to `models.json` under `custom_models`
3. Update `load_models()` to merge `custom_models` with catalog models
   - Filter custom models by hardware compatibility
   - Display with different marker (e.g., `*`) in menu
4. Add CLI flag: `oi --add-local /path/to/model.gguf --name mymodel --desc "My model"`
5. Add CLI flag: `oi --remove-custom mymodel` to remove from registry

---

## 11. Add model benchmarking

**File:** New function `benchmark_model()` in `oi`

**Current behavior:** No way to measure model performance (token speed, latency, resource usage) before using for actual work.

**Desired behavior:** Run performance benchmarks on any installed model to evaluate speed and resource usage objectively.

**Steps:**
1. Create `benchmark_model()` function accepting model path and options:
   - Generate N sample prompts of varying lengths (short: 50 tokens, medium: 200, long: 500)
   - Measure time-to-first-token (TTFT) for each prompt
   - Measure tokens-per-second (TPS) for streaming generation
   - Record peak memory usage (via `nvidia-smi` or `/proc/meminfo`)
2. Add CLI flag: `oi --benchmark <model-id> [--prompts N]`
3. Benchmark process:
   - Load model once (warm-up prompt)
   - Run 5 iterations per prompt length
   - Calculate averages and standard deviations
4. Output format:
   ```
   Model: qwen3-8b (Q4_K_M)
   ============================================
   Time to First Token (TTFT):
     Short (50t):    620ms ± 45ms
     Medium (200t):   1240ms ± 78ms
     Long (500t):     2850ms ± 156ms
   
   Tokens per Second (TPS):
     Short:  52.3 ± 3.2
     Medium: 48.7 ± 4.1
     Long:   44.1 ± 5.3
   
   Peak Memory: 5.2GB VRAM
   ```
5. Save benchmark results to `lib/cache/benchmarks.json` for historical comparison
6. Add `--compare-benchmarks` flag to show side-by-side comparison of all benchmarked models

---

## 12. Add model comparison mode

**File:** New function `compare_models()` in `oi`

**Current behavior:** Can only chat with one model at a time. Cannot easily compare responses across models for quality analysis.

**Desired behavior:** Run identical prompt through multiple models and display results side-by-side for easy comparison.

**Steps:**
1. Create `compare_models()` function accepting comma-separated model list:
   ```bash
   oi --compare qwen2.5-7b,mistral-7b,phi-3-mini "explain recursion"
   ```
2. For each model:
   - Run prompt with identical parameters (temperature, top-p, top-k, context)
   - Capture full output to temporary buffer
   - Record TTFT and TPS metrics
3. Display results:
   - If terminal width ≥ 120 chars: side-by-side columns
   - If terminal width < 120 chars: sequential display with separators
4. Add color coding to highlights:
   - Green: fastest TTFT
   - Blue: highest TPS
   - Yellow: longest output
5. Handle errors gracefully:
   - If model fails to load or generate, show error message
   - Continue with remaining models
6. Add `--compare-export <format>` flag to save comparison as JSON/Markdown

---

## 13. Add file/code context support

**File:** `launch_chat()` in `oi`, new function `prepare_context()`

**Current behavior:** Chat only uses the user's prompt text. Cannot include file contents as additional context for the conversation.

**Desired behavior:** Attach files or pipe content to provide code/docs as context to the LLM.

**Steps:**
1. Add CLI flag to main():
   - `oi --file <path>`: Attach file (can be used multiple times)
   - `oi --file main.py --file README.md "review this code"`
2. Create `prepare_context()` function:
   - Accept list of file paths
   - Read file contents
   - Detect file type (extension: .py, .js, .ts, .md, etc.)
   - Format each file as system context:
     ```
     Context from {filename} ({language}):
     {file contents}
     ---
     ```
   - Combine multiple files with separators
3. Support stdin input:
   - Check if stdin has data: `read -t 0`
   - If stdin present, read all input
   - Prepend to user prompt with label "Context from stdin:"
4. Add file type detection for helpful hints:
   - Map extensions to languages (py→Python, js→JavaScript, ts→TypeScript)
   - Include language name in context header for better LLM understanding
5. Examples:
   ```bash
   # Attach single file
   oi --file main.py "optimize this function"
   
   # Attach multiple files
   oi --file api.py --file schema.sql "review these"
   
   # Pipe code
   cat main.py | oi "refactor this"
   ```

---

## 14. Add multi-model simultaneous chat

**File:** New function `multi_chat()` in `oi`

**Current behavior:** Only one model is active at a time during chat. Cannot compare live streaming responses.

**Desired behavior:** Run 2-4 models in parallel, display streaming outputs side-by-side in real-time for direct comparison.

**Steps:**
1. Create `multi_chat()` function accepting model list:
   ```bash
   oi --multi qwen2.5-7b,mistral-7b "explain quantum computing"
   ```
2. Implementation approach:
   - For each model, spawn background `llama-cli` process
   - Capture each process's stdout to separate buffer
   - Use terminal width to calculate column layout:
     - 2 models: 50% width each
     - 3 models: 33% width each
     - 4 models: 25% width each
   - Stream responses character-by-character to respective columns
3. Display format with box-drawing characters:
   ```
   ╔═════════════════════════╦═════════════════════════╗
   ║ qwen2.5-7b                ║ mistral-7b                ║
   ╠═════════════════════════╬═════════════════════════╣
   ║ Quantum computing harnesses   ║ Quantum mechanics describes  ║
   ║ the principles of quantum...    ║ the behavior of matter...   ║
   ```
4. Handle terminal resizing (SIGWINCH signal):
   - Recalculate column widths
   - Redraw frames with new dimensions
5. Add slash commands during multi-chat:
   - `/focus <1|2|3|4>`: Expand one model to full screen
   - `/all`: Return to split view
   - `/quit`: Exit multi-chat
6. Gracefully handle model failures:
   - If model crashes, show error in its column
   - Continue running other models
7. Add color coding: Each model gets unique color for its stream

---

## 15. Add hardware-aware model defaults

**File:** `launch_chat()` and `get_compatible_models()` in `oi`

**Current behavior:** Default model is either the first in catalog or user-selected. No automatic recommendations based on detected hardware.

**Desired behavior:** Suggest optimal model based on detected VRAM/RAM when no model is explicitly specified.

**Steps:**
1. Update `detect_hardware()` to capture detailed capabilities:
   ```bash
   {
     "vram_gb": 8.0,
     "ram_gb": 32,
     "cpu_cores": 8,
     "has_cuda": true,
     "cuda_version": "12.2"
   }
   ```
2. Create `recommend_model()` function:
   - Define model tiers based on VRAM availability:
     - < 2GB: gemma-2-2b, phi-3-mini
     - 2-4GB: qwen2.5-3b, llama-3.2-3b
     - 4-8GB: qwen2.5-7b, mistral-7b, deepseek-coder-6.7b
     - 8-16GB: qwen3-8b, deepseek-r1-8b
     - 16-24GB: qwen3-14b, mixtral-8x7b
     - > 24GB: qwen3-32b, llama-3-70b
   - Check which tier models are installed
   - Return best-fit model with highest parameter count
3. Modify `launch_chat()` interactive mode:
   - If no model specified and user enters menu
   - Show "Recommended: {model}" at top of menu
   - Highlight with green color and star (*) in menu
   - Add description: "Based on your {VRAM}GB VRAM"
4. Add CLI flag: `oi --auto` (skip menu, use recommended model immediately)
5. Fallback logic:
   - If no compatible model installed, recommend download
   - Suggest quantization level (Q3_K_M for low VRAM, Q4_K_M for mid-range)

---

## 16. Add streaming display enhancements

**File:** `launch_chat()` in `oi`, new display functions

**Current behavior:** Raw text output from llama-cli. No formatting, highlighting, or visual improvements during streaming.

**Desired behavior:** Enhanced streaming display with syntax highlighting, progress indicators, and better readability.

**Steps:**
1. Create `display_stream()` function to process llama-cli output:
   - Detect and format code blocks (```)
   - Apply syntax highlighting for detected languages
   - Colorize: bold (**), italic (_), code (`), lists (-, *)
2. Add thinking indicator:
   - Show spinner or progress bar while model is generating
   - Display "Thinking..." before first token arrives
   - Clear when streaming starts
3. Add token counter:
   - Estimate tokens based on character count (≈4 chars/token for English)
   - Display in footer: "Tokens: 342 | Time: 12s | TPS: 28.5"
4. Code highlighting approach:
   - Use ANSI colors for keywords, strings, comments
   - Simple regex-based highlighting (no external dependencies)
   - Support: Python, JavaScript, TypeScript, Bash, SQL, JSON, Markdown
5. Add markdown rendering:
   - Headers (#) → bold, larger
   - Code blocks → dim background
   - Inline code → cyan
   - Bold → bold ANSI
   - Links → blue, underlined
6. Respect `NO_COLOR` environment variable to disable colors

---

## 17. Add conversation branching

**File:** `lib/session.sh`, extend session save/load from task 7

**Current behavior:** Linear conversation history. Cannot explore alternative responses or "what if" scenarios.

**Desired behavior:** Fork conversations at any point to explore different response paths, maintain branching history.

**Steps:**
1. Extend session format to support branching:
   ```json
   {
     "messages": [
       {
         "id": "msg-1",
         "role": "user",
         "content": "...",
         "children": ["msg-2a", "msg-2b"]
       },
       {
         "id": "msg-2a",
         "role": "assistant",
         "content": "...",
         "parent": "msg-1",
         "branch": "A"
       },
       {
         "id": "msg-2b",
         "role": "assistant",
         "content": "...",
         "parent": "msg-1",
         "branch": "B"
       }
     ]
   }
   ```
2. Add slash commands:
   - `/branch <name>`: Create new branch from current point
   - `/switch <branch>`: Switch to different branch
   - `/merge <branch>`: Merge branch back into main
   - `/branches`: List all branches with previews
3. Add CLI flags:
   - `--load-session <file>:<branch>`: Load specific branch
   - `--export-branch <branch>:<format>`: Export single branch
4. Visual indicator in chat:
   - Show current branch name in prompt: `[branch-A] User:`
   - Use tree-style when viewing history
5. Save branches as separate files or single file with metadata

