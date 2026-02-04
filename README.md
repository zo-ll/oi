# oi - One-Command LLM Chat Interface

`oi` is a simple bash script that makes running local LLMs with llama.cpp as easy as typing a single command. It handles model selection, downloading, and launching interactive chat sessions.

## Features

- **One-command operation**: Just type `oi` and start chatting
- **Smart model recommendations**: Automatically filters models based on your hardware
- **Curated model catalog**: Pre-configured with high-quality models from HuggingFace
- **Automatic downloads**: Downloads models on-demand using curl
- **Hardware detection**: Detects GPU VRAM and CPU specs for optimal model matching
- **Simple CLI interface**: Support for direct model selection and custom downloads

## Requirements

- Linux system with bash
- [llama.cpp](https://github.com/ggml-org/llama.cpp) built from source
- Python 3 (for HuggingFace catalog fetching)
- curl (for downloading models)
- Optional: NVIDIA GPU with CUDA for faster inference

## Installation

### 1. Install llama.cpp

```bash
# Clone the repository
git clone https://github.com/ggml-org/llama.cpp.git ~/llama.cpp

# Build with CUDA support (if you have an NVIDIA GPU)
cd ~/llama.cpp
cmake -B build -DGGML_CUDA=ON
cmake --build build --config Release

# Or build for CPU only
cmake -B build
cmake --build build --config Release
```

### 2. Install oi

```bash
git clone <repository-url> ~/local_script
cd ~/local_script
bash install.sh
```

This creates a symlink at `~/.local/bin/oi` pointing to the repository. Make sure `~/.local/bin` is in your `PATH`:

```bash
# Add to your ~/.bashrc or ~/.zshrc
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

You can verify the installation with:

```bash
which oi
oi --help
```

## Quick Start

### Interactive Mode

Simply run `oi` to enter the interactive menu:

```bash
oi
```

This will:
1. Detect your hardware (VRAM, RAM, CPU)
2. Show a menu of compatible models
3. Download the model if not present
4. Launch an interactive chat session

### Direct Model Selection

Start chatting with a specific model immediately:

```bash
oi -m qwen2.5-3b
```

### List Available Models

See all models and their compatibility with your system:

```bash
oi -l
```

### Check Hardware

Display your system specifications:

```bash
oi -h
```

## Usage

```
oi [OPTIONS]

OPTIONS:
    -m, --model <id>         Directly select model by ID
    -q, --quant <quant>      Specify quantization (default: Q4_K_M)
    -l, --list              List available models
    -i, --installed         List installed models only
    -d, --download <path>   Download custom model from HuggingFace
                            Format: username/repo-name/filename.gguf
    -r, --refresh           Force refresh model catalog from HuggingFace
    -x, --remove <file>     Remove an installed model
    -h, --hardware          Show system hardware information
    -c, --context <size>    Set context size (default: 4096)
    -t, --threads <num>     Set number of CPU threads
    --help                  Show help message
```

## Examples

```bash
oi                              # Interactive model selection
oi -m qwen2.5-3b               # Chat with specific model
oi -m mistral-7b -q Q5_K_M     # Use different quantization
oi -l                           # List all available models
oi -i                           # Show installed models
oi -h                           # Check system specs
oi -r -l                        # Refresh catalog from HuggingFace
oi -x model.gguf                # Remove an installed model
oi -m qwen2.5-7b -c 8192 -t 8  # Custom context size and threads
oi -d microsoft/Phi-3-mini-4k-instruct-gguf/Phi-3-mini-4k-instruct.Q4_K_M.gguf
```

## Quantization Options

Quantization affects model size, speed, and quality:

| Quantization | Size | Quality | Recommendation |
|-------------|------|---------|----------------|
| Q2_K | Smallest | Lowest | Emergency use only |
| Q3_K_S | Small | Decent | Low VRAM |
| Q3_K_M | Small | Good | Balanced 3-bit |
| Q4_K_M | Medium | Excellent | **Recommended default** |
| Q4_K_L | Medium | Very Good | High quality 4-bit |
| Q5_K_M | Large | Near-lossless | Best quality/size |
| Q6_K | Larger | Excellent | Very high quality |
| Q8_0 | Largest | Best | Maximum quality |

## Project Structure

```
local_script/
├── oi                        Entry point (globals, source modules, main)
├── install.sh                Symlinks oi -> ~/.local/bin/oi
├── README.md
└── lib/
    ├── ui.sh                 Terminal helpers + interactive menu
    ├── models.sh             Catalog loading, filtering, listing
    ├── download.sh           Download and remove models
    ├── chat.sh               Launch llama-cli chat
    ├── hardware.sh           Hardware detection and display
    ├── hardware_detect.sh    Low-level hardware probe
    ├── fetch_hf_models.py    HuggingFace API client
    ├── models.json           Curated model catalog
    └── cache/                HuggingFace response cache
```

Models are stored in `~/llama.cpp/models/`.

## Troubleshooting

### "llama.cpp not found"
Make sure you've cloned llama.cpp to `~/llama.cpp` and built it successfully.

### "llama-cli not found"
The build might have failed or llama-cli is in a different location. Check:
```bash
ls ~/llama.cpp/build/bin/
```

### Download fails
- Check your internet connection
- Some models require HuggingFace authentication. Set a token:
  ```bash
  export HF_TOKEN="your_token_here"
  ```
- Try a different quantization (some may not be available)

### Out of memory
- Use a smaller model
- Try a lower quantization (Q3_K_M instead of Q4_K_M)
- Reduce context size: `oi -m model-name -c 2048`

### Slow performance
- If you have a GPU, make sure llama.cpp was built with CUDA: `cmake -B build -DGGML_CUDA=ON`
- Check GPU is being used: `nvidia-smi` during chat
- Use a smaller model or lower quantization

## Customization

### Adding New Models

Edit `lib/models.json` to add your own models:

```json
{
  "id": "your-model-id",
  "name": "Your Model Name",
  "repo": "username/repo-name",
  "filename_template": "model-name-{quant}.gguf",
  "min_vram_gb": 4.0,
  "description": "Description of your model",
  "tags": ["tag1", "tag2"]
}
```

### Changing Defaults

Edit the top of the `oi` script to change default values:
- `DEFAULT_QUANT="Q4_K_M"` - Default quantization
- `DEFAULT_CONTEXT=4096` - Default context size

## License

This script follows the same license as llama.cpp (MIT License).

## Acknowledgments

- [llama.cpp](https://github.com/ggml-org/llama.cpp) by Georgi Gerganov and contributors
- Model creators on HuggingFace
- The open-source LLM community
