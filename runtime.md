# Runtime Selection Implementation Plan

## Overview

Add runtime selection capability when adding models, allowing users to filter HuggingFace search results by model format (GGUF, PyTorch, Safetensors, TensorFlow, ONNX). Support "all formats" option to show all results. Additionally, add specific support for Parakeet and Whisper-GGUF models for voice-to-text applications.

---

## Hardware Requirements by Runtime

### GGUF (llama.cpp)
- **Minimum**: 4GB RAM (any CPU), optional GPU
- **Recommended**: 8GB+ RAM, GPU with 4GB+ VRAM
- **GPU Support**: CUDA, Metal (macOS), ROCm (AMD), Vulkan
- **Overhead**: Very lightweight (~50MB runtime)
- **Best for**: LLM text generation, edge devices, Whisper-GGUF models
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ✅ Perfect fit

### PyTorch
- **Minimum**: 8GB RAM (CPU only), 16GB+ for larger models
- **Recommended**: 32GB+ RAM, GPU with 8GB+ VRAM
- **GPU Support**: CUDA, ROCm, Metal
- **Overhead**: Heavy (2-4GB for framework + model)
- **Best for**: Vision, audio, general ML, research, Parakeet models
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ⚠️ Can run small/medium models on GPU, CPU for larger models

### Safetensors
- **Runtime**: Needs PyTorch or TensorFlow to load
- **Requirements**: Same as runtime you use (PyTorch or TensorFlow)
- **Overhead**: Same as runtime
- **Advantage**: Zero-copy loading, memory efficient
- **Best for**: Production deployments, memory-constrained environments
- **Current User Hardware (8GB VRAM, 16GB RAM)**: Same as PyTorch

### TensorFlow
- **Minimum**: 8GB RAM (CPU only), 16GB+ for larger models
- **Recommended**: 32GB+ RAM, GPU with 8GB+ VRAM
- **GPU Support**: CUDA, ROCm
- **Overhead**: Very heavy (3-5GB for framework + model)
- **Best for**: Google ecosystem, TPU deployments, production
- **Note**: Often more memory-hungry than PyTorch
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ⚠️ Tight for large models

### ONNX
- **Minimum**: 4GB RAM (CPU), 8GB+ for GPU
- **Recommended**: 16GB+ RAM, GPU with 4GB+ VRAM
- **GPU Support**: CUDA, TensorRT, OpenVINO, DirectML
- **Overhead**: Moderate (~500MB runtime)
- **Best for**: Cross-platform deployment, mobile, edge
- **Backends**: ONNX Runtime, TensorRT, OpenVINO
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ✅ Good fit

---

## Special Model Support

### Parakeet Models (NVIDIA ASR - Voice-to-Text)

**Parakeet 0.6B Models:**
- **Parameters**: 600 million
- **Format**: PyTorch (via NeMo toolkit)
- **Minimum RAM**: 2GB
- **VRAM usage**: ~2-4GB on GPU
- **Supported Languages**: 25 European languages (v3 multilingual)
- **Use Cases**: Speech-to-text transcription, subtitle generation, voice analytics
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ✅ Perfect fit

**Parakeet 1.1B Models:**
- **Parameters**: 1.1 billion
- **Format**: PyTorch (via NeMo toolkit)
- **VRAM usage**: Likely 4-6GB on GPU
- **Supported Languages**: English only
- **Current User Hardware (8GB VRAM, 16GB RAM)**: ⚠️ Tight but possible

**Requirements for Parakeet:**
```bash
# Install dependencies
pip install -U nemo_toolkit['asr']

# Automatic instantiation (Python)
import nemo.collections.asr as nemo_asr
asr_model = nemo_asr.models.ASRModel.from_pretrained(model_name="nvidia/parakeet-tdt-0.6b-v3")

# Transcribe audio
output = asr_model.transcribe(['audio_file.wav'])
print(output[0].text)
```

### Whisper-GGUF Models (OpenAI ASR - Voice-to-Text)

**Why Whisper-GGUF?**
- Simple runtime (llama.cpp, ~50MB overhead)
- Great CPU performance (much better than PyTorch models)
- Optional GPU acceleration
- No heavy framework dependencies

**Whisper Model Sizes:**
| Model | Size | VRAM | CPU Performance | Current Hardware |
|--------|-------|--------|-----------------|------------------|
| whisper-tiny | 40MB | 0.5GB | Excellent | ✅ Perfect |
| whisper-base | 150MB | 1GB | Very Good | ✅ Perfect |
| whisper-small | 500MB | 2GB | Good | ✅ Good |
| whisper-medium | 1.5GB | 4GB | Fair | ✅ Good |
| whisper-large-v3 | 3GB | 6GB | Poor on CPU | ✅ Good (GPU) |

**Advantages over PyTorch Whisper:**
- No PyTorch/NeMo overhead (saves 4-6GB RAM)
- Better CPU inference speed
- Simpler setup (just llama.cpp)
- Quantization support for smaller models

---

## Files to Modify

| File | Changes | Lines Affected |
|------|---------|---------------|
| `lib/search_hf.py` | Add `--format` CLI parameter, update `search_models()`, update `main()` | Lines 14-52, 230-247 |
| `lib/manual_add.sh` | Add runtime selection step, update flow numbering, pass format to search, display format in results | Lines 44-144 |
| `TODO.md` | Add new section 18 with runtime support tasks | End of file |
| `runtime.md` | **NEW FILE** - This implementation plan | - |

---

## Detailed Changes

### 1. lib/search_hf.py

#### 1.1 Add `--format` parameter to argparse (around line 232)
```python
parser.add_argument("--format", help="Filter by model format (gguf, pytorch, safetensors, tensorflow, onnx, or leave empty for all)")
```

#### 1.2 Update `search_models()` function signature (line 14)
```python
def search_models(keyword, mem_gb, format_filter=None):
    """Search HF for models with optional format filtering.

    Args:
        keyword: Search keyword
        mem_gb: Available memory in GB
        format_filter: Optional format to filter by (e.g., 'gguf', 'pytorch')
    """
```

#### 1.3 Modify search query logic (lines 17-31)
```python
def search_models(keyword, mem_gb, format_filter=None):
    """Search HF for models with optional format filtering."""
    # Build base URL with search parameters
    params = {
        "search": keyword,
        "sort": "downloads",
        "direction": "-1",
        "limit": "20",
    }

    # Add format filter if specified (use HuggingFace API filter parameter)
    if format_filter:
        params["filter"] = format_filter

    # Build URL with parameters
    url = HF_API + "?" + "&".join(f"{k}={v}" for k, v in params.items())

    try:
        req = urllib.request.Request(url, headers={"User-Agent": "oi/1.0"})
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode())
    except (urllib.error.URLError, OSError) as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)
```

#### 1.4 Extract format from model tags (around line 40, add before results.append)
```python
    results = []
    for model in data:
        model_id = model.get("modelId", "")
        downloads = model.get("downloads", 0)

        # Extract format from tags
        tags = model.get("tags", [])
        model_format = None
        format_priority = ["gguf", "safetensors", "pytorch", "tensorflow", "onnx", "jax", "coreml", "openvino"]
        for fmt in format_priority:
            if fmt in tags:
                model_format = fmt
                break

        # Try to estimate size from model tags or name
        est_gb = _estimate_size_from_id(model_id)

        # Filter: skip if estimated size exceeds available memory (with margin)
        if est_gb and mem_gb and est_gb > mem_gb * 1.1:
            continue

        results.append({
            "repo": model_id,
            "name": model_id.split("/")[-1] if "/" in model_id else model_id,
            "downloads": downloads,
            "est_size_gb": est_gb,
            "format": model_format or "unknown",
        })

    print(json.dumps(results))
```

#### 1.5 Update `main()` to pass format parameter (around line 237-242)
```python
    if args.search:
        search_models(args.search, args.mem, args.format)
    elif args.files:
        list_repo_files(args.files)
    else:
        parser.print_help()
        sys.exit(1)
```

---

### 2. lib/manual_add.sh

#### 2.1 Add runtime selection function (new function before line 44)
```bash
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
```

#### 2.2 Update `search_hf_interactive()` function (lines 44-144)
```bash
search_hf_interactive() {
    _init_hw_info
    local mem_gb="$_HW_TOTAL_GB"

    while true; do
        echo "" >&2
        echo -e "${CYAN}══ Step 2/4: Search HuggingFace ══${NC}" >&2  # Changed from Step 1/3
        echo "" >&2
        read -p "Search keyword (e.g. llama, qwen, phi, parakeet, whisper): " keyword
        if [ -z "$keyword" ]; then
            echo -e "${RED}No keyword entered${NC}" >&2
            return 1
        fi

        # Get runtime selection
        local format_filter
        format_filter=$(select_runtime)

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
        echo -e "${CYAN}══ Step 3/4: Pick a Repository ══${NC}" >&2  # Changed from Step 2/3
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
            printf >&2 "  ${line_color}%-3s %-40s %8s DL  %6s  %-12s  %b\n" "$((i+1)))" "$(truncate_str "$repo" 40)" "$downloads" "$est" "$fmt_tag" "$hw_tag"
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
```

#### 2.3 Update `pick_repo_file()` function header (line 161-166)
```bash
pick_repo_file() {
    local repo="$1"
    _init_hw_info

    echo "" >&2
    echo -e "${CYAN}══ Step 4/4: Pick a Model File ══${NC}" >&2  # Changed from Step 3/3
```

#### 2.4 Display format in file listing (modify around lines 236-280)
```bash
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
```

---

### 3. TODO.md

Add new section at end of file:

```markdown
## 18. Add support for additional runtimes

**Status:** Runtime selection UI implemented, inference support pending

**Completed:**
- [x] Add runtime selection UI in model addition flow
- [x] Implement format filtering in HuggingFace search
- [x] Support GGUF, PyTorch, Safetensors, TensorFlow, ONNX formats
- [x] Display format information in search results
- [x] Display format in file listing
- [x] Add Parakeet model support documentation
- [x] Add Whisper-GGUF model support documentation

**TODO:**
- [ ] Add PyTorch model loading support (inference backend)
- [ ] Add Safetensors model loading support (via PyTorch/TensorFlow)
- [ ] Add TensorFlow model loading support (inference backend)
- [ ] Add ONNX model loading support (inference backend)
- [ ] Add NVIDIA NeMo integration for Parakeet models
- [ ] Test Parakeet 0.6B model inference
- [ ] Test Parakeet 1.1B model inference
- [ ] Test Whisper-GGUF models (tiny, base, small, medium, large)
- [ ] Add format-specific error messages
- [ ] Add format conversion utilities (e.g., PyTorch → GGUF)
- [ ] Add model size warnings per runtime format
- [ ] Document hardware requirements for each runtime
- [ ] Add audio preprocessing for ASR models (Parakeet, Whisper)
- [ ] Add audio file format validation (.wav, .flac for ASR)
- [ ] Add transcription output formatting (timestamps, segments)
- [ ] Test vision model inference (image recognition)
- [ ] Test OCR model inference (document recognition)
```

---

## Implementation Steps

1. **Update `lib/search_hf.py`**:
   - [ ] Add `--format` CLI parameter to argparse
   - [ ] Modify `search_models()` to accept `format_filter` parameter
   - [ ] Use HuggingFace API's `filter` parameter for runtime filtering
   - [ ] Extract format from model tags
   - [ ] Include format in search results JSON
   - [ ] Update `main()` to pass format parameter

2. **Update `lib/manual_add.sh`**:
   - [ ] Create `select_runtime()` function
   - [ ] Add runtime selection step (Step 1/4)
   - [ ] Update step numbering throughout (`search_hf_interactive`, `pick_repo_file`)
   - [ ] Pass selected format to `search_hf.py`
   - [ ] Display format in search results (add format column)
   - [ ] Display format in file listing (add format column)
   - [ ] Update result display formatting for format tag
   - [ ] Update example keywords (add parakeet, whisper)

3. **Update `TODO.md`**:
   - [ ] Add section 18 for runtime support
   - [ ] Document completed features
   - [ ] List TODO items for inference backend support
   - [ ] Add Parakeet and Whisper-GGUF specific tasks

4. **Create `runtime.md`**:
   - [ ] Create comprehensive runtime selection plan
   - [ ] Document hardware requirements
   - [ ] Add Parakeet model support details
   - [ ] Add Whisper-GGUF model support details
   - [ ] Provide usage examples

5. **Testing**:
   - [ ] Test GGUF format selection (default)
   - [ ] Test PyTorch format selection
   - [ ] Test Safetensors format selection
   - [ ] Test TensorFlow format selection
   - [ ] Test ONNX format selection
   - [ ] Test "All formats" option (no filter)
   - [ ] Verify format displayed in search results
   - [ ] Verify format displayed in file listing
   - [ ] Test search for "parakeet" keyword with PyTorch filter
   - [ ] Test search for "whisper" keyword with GGUF filter
   - [ ] Test keyboard shortcuts (S for search again, Q to cancel)
   - [ ] Test invalid runtime selection
   - [ ] Test empty runtime selection (default to GGUF)

---

## Testing Checklist

### Basic Functionality
- [ ] Runtime selection menu displays correctly
- [ ] All 6 options (GGUF, PyTorch, Safetensors, TensorFlow, ONNX, All formats) are available
- [ ] Default selection (Enter key) selects GGUF
- [ ] Invalid input defaults to GGUF
- [ ] Parakeet and Whisper keywords work

### Search Results
- [ ] Format filter is applied to HuggingFace API query
- [ ] Results are filtered by selected runtime
- [ ] "All formats" option shows models of all types
- [ ] Format column displays correctly in search results
- [ ] Format tag is visible for each result (e.g., [PyTorch], [GGUF])
- [ ] Parakeet models show [PyTorch] format
- [ ] Whisper models show [GGUF] format

### File Listing
- [ ] All format files are displayed (not just GGUF)
- [ ] Format column displays in file listing
- [ ] Quantization column shows "-" for non-GGUF formats
- [ ] File size and compatibility tags work for all formats

### User Flow
- [ ] Step numbers are correct (1/4 → 2/4 → 3/4 → 4/4)
- [ ] "S" option allows searching again with new keyword
- [ ] "Q" option cancels and returns to menu
- [ ] After selecting repo, file picker shows correct files
- [ ] Download works for all supported formats

### Edge Cases
- [ ] No results found for keyword + format combination
- [ ] Empty search keyword
- [ ] Invalid runtime choice
- [ ] HuggingFace API errors
- [ ] Network errors during search

---

## User Flow Examples

### Example 1: Adding a Parakeet Model (ASR - Voice-to-Text)

```
$ oi
... installed models menu ...
Press A to add models

══ Add Model ══

1) Search HuggingFace
2) Enter repo directly
Q) Cancel

Choice: 1

══ Step 1/4: Select Runtime ══

Select runtime:
  1) GGUF (for LLM text generation, Whisper models)
  2) PyTorch (vision, audio, Parakeet, general ML)
  3) Safetensors (safe alternative to PyTorch)
  4) TensorFlow (Google's ML framework)
  5) ONNX (cross-platform inference)
  6) All formats (no filter)

Runtime (1-6) [default: 1]: 2

══ Step 2/4: Search HuggingFace ══

Search keyword (e.g. llama, qwen, phi, parakeet, whisper): parakeet
Searching HuggingFace for 'parakeet' (PyTorch)...

══ Step 3/4: Pick a Repository ══

Results for 'parakeet' (15 found):
────────────────────────────────────────────────────────────────────────────────────────
1)  nvidia/parakeet-tdt-0.6b-v3      90.1K DL  ~0.6G  GPU   [PyTorch]
2)  nvidia/parakeet-tdt-1.1b         107K DL   ~1.1G  GPU   [PyTorch]
3)  nvidia/parakeet-ctc-0.6b          3.6K DL   ~0.6G  GPU   [PyTorch]

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a repo (1-15, S to search again, Q to cancel): 1

══ Step 4/4: Pick a Model File ══

Available files in nvidia/parakeet-tdt-0.6b-v3:
VRAM: 7.9G  Total RAM: 16G
────────────────────────────────────────────────────────────────────────────────────────
1)  PyTorch      -         0.6G     GPU   model.nemo
2)  Config       -         0.0M           config.yaml
3)  Tokens       -         0.1M           tokens.txt

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a file (1-3, or Q to cancel): 1

══ Downloading ══

Downloading: model.nemo
  URL: https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3/resolve/main/model.nemo
  Destination: /home/user/.local/share/oi/llama.cpp/models/model.nemo

Download complete!

Note: Parakeet models require NVIDIA NeMo toolkit:
  pip install nemo_toolkit['asr']

Usage example:
  import nemo.collections.asr as nemo_asr
  asr_model = nemo_asr.models.ASRModel.from_pretrained(model_name="nvidia/parakeet-tdt-0.6b-v3")
  output = asr_model.transcribe(['audio.wav'])
  print(output[0].text)
```

### Example 2: Adding a Whisper-GGUF Model (ASR - Voice-to-Text)

```
$ oi
... installed models menu ...
Press A to add models

══ Add Model ══

1) Search HuggingFace
2) Enter repo directly
Q) Cancel

Choice: 1

══ Step 1/4: Select Runtime ══

Select runtime:
  1) GGUF (for LLM text generation, Whisper models)
  2) PyTorch (vision, audio, Parakeet, general ML)
  3) Safetensors (safe alternative to PyTorch)
  4) TensorFlow (Google's ML framework)
  5) ONNX (cross-platform inference)
  6) All formats (no filter)

Runtime (1-6) [default: 1]: 1

══ Step 2/4: Search HuggingFace ══

Search keyword (e.g. llama, qwen, phi, parakeet, whisper): whisper
Searching HuggingFace for 'whisper' (GGUF)...

══ Step 3/4: Pick a Repository ══

Results for 'whisper' (12 found):
────────────────────────────────────────────────────────────────────────────────────────
1)  openai/whisper-tiny          150K DL   ~0.04G  GPU   [GGUF]
2)  openai/whisper-base          200K DL   ~0.15G  GPU   [GGUF]
3)  openai/whisper-small         180K DL   ~0.5G   GPU   [GGUF]
4)  openai/whisper-medium        160K DL   ~1.5G   GPU   [GGUF]
5)  openai/whisper-large-v3      140K DL   ~3.0G   GPU   [GGUF]

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a repo (1-12, S to search again, Q to cancel): 2

══ Step 4/4: Pick a Model File ══

Available files in openai/whisper-base:
VRAM: 7.9G  Total RAM: 16G
────────────────────────────────────────────────────────────────────────────────────────
1)  GGUF         Q5_0      0.15G    GPU   whisper-base.Q5_0.gguf
2)  GGUF         Q4_K_M     0.15G    GPU   whisper-base.Q4_K_M.gguf
3)  GGUF         Q8_0       0.15G    GPU   whisper-base.Q8_0.gguf

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a file (1-3, or Q to cancel): 2

══ Downloading ══

Downloading: whisper-base.Q4_K_M.gguf
  URL: https://huggingface.co/openai/whisper-base/resolve/main/whisper-base.Q4_K_M.gguf
  Destination: /home/user/.local/share/oi/llama.cpp/models/whisper-base.Q4_K_M.gguf

Download complete!

Note: Whisper-GGUF models can run with llama.cpp:
  oi -m whisper-base.Q4_K_M.gguf --asr-mode
```

### Example 3: Adding a Vision Model (Image Recognition)

```
$ oi
... installed models menu ...
Press A to add models

══ Add Model ══

1) Search HuggingFace
2) Enter repo directly
Q) Cancel

Choice: 1

══ Step 1/4: Select Runtime ══

Select runtime:
  1) GGUF (for LLM text generation, Whisper models)
  2) PyTorch (vision, audio, Parakeet, general ML)
  3) Safetensors (safe alternative to PyTorch)
  4) TensorFlow (Google's ML framework)
  5) ONNX (cross-platform inference)
  6) All formats (no filter)

Runtime (1-6) [default: 1]: 2

══ Step 2/4: Search HuggingFace ══

Search keyword (e.g. llama, qwen, phi, parakeet, whisper): vit
Searching HuggingFace for 'vit' (PyTorch)...

══ Step 3/4: Pick a Repository ══

Results for 'vit' (18 found):
────────────────────────────────────────────────────────────────────────────────────────
1)  google/vit-base-patch16-224   85K DL   ~0.3G   GPU   [PyTorch]
2)  facebook/deit-base-distilled   42K DL   ~0.3G   GPU   [PyTorch]
3)  microsoft/beit-base-patch16-224  38K DL   ~0.4G   GPU   [PyTorch]

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a repo (1-18, S to search again, Q to cancel): 1

══ Step 4/4: Pick a Model File ══

Available files in google/vit-base-patch16-224:
VRAM: 7.9G  Total RAM: 16G
────────────────────────────────────────────────────────────────────────────────────────
1)  PyTorch      -         0.3G     GPU   pytorch_model.bin
2)  Safetensors  -         0.3G     GPU   model.safetensors
3)  Config       -         0.0M           config.json

  GPU=fits VRAM  CPU=fits RAM  too large=won't fit

Pick a file (1-3, or Q to cancel): 2

══ Downloading ══

Downloading: model.safetensors
  URL: https://huggingface.co/google/vit-base-patch16-224/resolve/main/model.safetensors
  Destination: /home/user/.local/share/oi/llama.cpp/models/model.safetensors

Download complete!

Note: Vision models require PyTorch or TensorFlow:
  pip install torch torchvision

Usage example:
  import torch
  from transformers import ViTImageProcessor, ViTForImageClassification
  processor = ViTImageProcessor.from_pretrained('google/vit-base-patch16-224')
  model = ViTForImageClassification.from_pretrained('google/vit-base-patch16-224')
```

---

## Notes

- **GGUF remains default** for backward compatibility
- **Format filtering uses HuggingFace API** - more reliable than client-side filtering
- **Format extraction** uses model tags (reliable method)
- **Hardware compatibility checks** remain same for all formats
- **Inference support** for non-GGUF formats is a separate task (see TODO.md section 18)
- **Current user hardware (8GB VRAM, 16GB RAM)**:
  - ✅ **GGUF models**: Perfect fit (LLMs, Whisper)
  - ✅ **Parakeet 0.6B**: Good fit (VRAM: 2-4GB, RAM: 2GB + framework overhead)
  - ⚠️ **Parakeet 1.1B**: Tight (VRAM: 4-6GB, RAM: higher + framework overhead)
  - ⚠️ **PyTorch vision models**: Small/medium models on GPU, larger on CPU
  - ⚠️ **Large PyTorch models**: May be slow on CPU, needs GPU for real-time

### Parakeet vs Whisper Comparison

| Feature | Parakeet (0.6B) | Whisper (base) |
|----------|---------------------|----------------|
| Parameters | 600M | 74M |
| VRAM Required | 2-4GB | 1GB |
| RAM Required | 2GB + 4GB (NeMo) | 150MB + 50MB (llama.cpp) |
| Runtime | PyTorch + NeMo | GGUF + llama.cpp |
| Setup Complexity | High (pip install nemo_toolkit) | Low (already have llama.cpp) |
| CPU Performance | Slow (10-50x slower) | Good |
| GPU Required | Strongly recommended | Optional |
| Languages | 25 European (v3) | 99 (multilingual) |
| Best For | Production ASR | Simple setup, CPU inference |

**Recommendation**: Use Whisper-GGUF for simpler setup and better CPU performance. Use Parakeet if you need production-grade ASR with NVIDIA hardware optimization.

---

## Decision Points

1. **Default runtime**: GGUF (for backward compatibility and current LLM use case)
2. **Format display**: Show in brackets after repo name (e.g., [PyTorch])
3. **Step numbering**: Updated from 3 steps to 4 steps (1: Runtime → 2: Search → 3: Repo → 4: File)
4. **File listing**: Show all formats, not just GGUF - allows downloading any format
5. **Hardware warnings**: Not added yet - can be added as enhancement (show warning if runtime has high requirements)
6. **Special model documentation**: Added for Parakeet and Whisper-GGUF models

---

This plan is ready for implementation. The changes are minimal, focused, and don't break existing functionality.

---

**Next Steps**:
1. ✅ Plan created in `runtime.md`
2. ⏭ Implement changes to `lib/search_hf.py`
3. ⏭ Implement changes to `lib/manual_add.sh`
4. ⏭ Update `TODO.md`
5. ⏭ Test all runtime filters
6. ⏭ Test Parakeet model search/download
7. ⏭ Test Whisper-GGUF model search/download
8. ⏭ Commit changes to `feature/installed-models-only` branch
