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
- Build tools: cmake, gcc
- Git, Python 3, curl
- Optional: NVIDIA GPU with CUDA toolkit for GPU acceleration

Note: llama.cpp is downloaded and compiled automatically during installation.

## Installation

### Quick Install

1. Clone the repository:
   ```bash
   git clone <repository-url> ~/oi
   ```

2. Run the install script:
   ```bash
   cd ~/oi
   bash install.sh
   ```

   The install script will automatically:
   - Check for required dependencies (cmake, gcc, git, python3, curl)
   - Detect CUDA support and build llama.cpp with GPU acceleration if available
   - Download and compile llama.cpp in `~/.local/share/oi/llama.cpp`
   - Install oi to `~/.local/share/oi/`
   - Create the `oi` command at `~/.local/bin/oi`

3. Ensure `~/.local/bin` is in your PATH:
   ```bash
   echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
   source ~/.bashrc
   ```

4. Verify installation:
   ```bash
   which oi
   oi --help
   ```

### Uninstall

To remove oi completely:
```bash
cd ~/oi
bash uninstall.sh
```

You will be prompted whether to preserve your downloaded models in `~/.local/share/oi/llama.cpp/models/`.

### Update Installation

To update oi to the latest version:
```bash
cd ~/oi
git pull
bash install.sh
```

Your downloaded models will be preserved during the update.

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
    --version               Show version information
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
oi --version                   # Show oi version
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
~/.local/share/oi/
├── oi                        Main entry point
├── install.sh                Installation script
├── uninstall.sh              Uninstallation script
├── README.md                 This file
├── TODO.md                   Development tasks
├── lib/
│   ├── ui.sh                 Terminal helpers + interactive menu
│   ├── models.sh             Catalog loading, filtering, listing
│   ├── download.sh           Download and remove models
│   ├── chat.sh               Launch llama-cli chat
│   ├── hardware.sh           Hardware detection and display
│   ├── hardware_detect.sh    Low-level hardware probe
│   ├── fetch_hf_models.py    HuggingFace API client
│   ├── models.json           Curated model catalog
│   └── cache/                HuggingFace response cache
└── llama.cpp/
    ├── build/                Compiled llama.cpp
    └── models/               Downloaded GGUF models (auto-created)
```

The `oi` command is a wrapper at `~/.local/bin/oi` that calls the main script in `~/.local/share/oi/oi`.

Models are stored in `~/.local/share/oi/llama.cpp/models/`.

## Troubleshooting

### "oi command not found"
Make sure `~/.local/bin` is in your PATH:
```bash
echo $PATH | grep "$HOME/.local/bin"
```
If not present, add it to your shell config:
```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### "llama.cpp not found" or "llama-cli not found"
The installation may have failed. Re-run the install script:
```bash
cd ~/oi
bash install.sh
```

Check the build log for errors. If llama.cpp failed to compile, ensure:
- cmake and gcc are installed: `cmake --version`, `gcc --version`
- Sufficient disk space: `df -h`

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
- If you have a GPU, ensure the install script detected CUDA. Check:
  ```bash
  oi -h  # Look for CUDA: yes
  ```
- Verify GPU is being used during chat: `nvidia-smi`
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
