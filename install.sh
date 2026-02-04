#!/bin/bash
set -e

VERSION="1.0.0"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

OI_SHARE_DIR="${HOME}/.local/share/oi"
OI_BIN_DIR="${HOME}/.local/bin"
LLAMA_CPP_DIR="${OI_SHARE_DIR}/llama.cpp"
OI_WRAPPER="${OI_BIN_DIR}/oi"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

log_info() {
    echo -e "${CYAN}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_dependencies() {
    log_info "Checking dependencies..."
    
    local missing=()
    
    for cmd in cmake gcc git python3 curl; do
        if ! command -v "$cmd" &> /dev/null; then
            missing+=("$cmd")
        fi
    done
    
    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Missing required dependencies: ${missing[*]}"
        echo "Please install them using your package manager:"
        echo "  Ubuntu/Debian: sudo apt-get install cmake build-essential git python3 curl"
        echo "  Fedora/RHEL: sudo dnf install cmake gcc git python3 curl"
        echo "  Arch: sudo pacman -S cmake base-devel git python curl"
        exit 1
    fi
    
    log_success "All dependencies found"
}

detect_cuda() {
    log_info "Detecting CUDA support..."
    
    local has_nvidia_smi="no"
    local has_nvcc="no"
    
    if command -v nvidia-smi &> /dev/null; then
        local vram=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 | tr -d ' ')
        if [ -n "$vram"" ] && [ "$vram" != "[Insufficientpermissions]" ]; then
            has_nvidia_smi="yes"
            log_info "Found NVIDIA GPU with ${vram}MB VRAM"
        fi
    fi
    
    if command -v nvcc &> /dev/null; then
        has_nvcc="yes"
        local nvcc_version=$(nvcc --version | grep "release" | head -n1)
        log_info "Found nvcc: ${nvcc_version}"
    fi
    
    if [ "$has_nvidia_smi" = "yes" ] && [ "$has_nvcc" = "yes" ]; then
        log_success "CUDA detected - will build with GPU support"
        return 0
    else
        log_warning "CUDA not fully detected - will build CPU-only version"
        if [ "$has_nvidia_smi" = "yes" ]; then
            log_warning "  NVIDIA GPU found but CUDA toolkit (nvcc) not in PATH"
            echo "  Install CUDA toolkit to enable GPU acceleration"
        fi
        return 1
    fi
}

check_existing_install() {
    if [ -f "$OI_WRAPPER" ]; then
        log_warning "Existing installation found at $OI_WRAPPER"
        read -p "Overwrite existing installation? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log_info "Installation cancelled"
            exit 0
        fi
        log_info "Proceeding with reinstallation..."
    fi
}

create_directories() {
    log_info "Creating directories..."
    mkdir -p "$OI_SHARE_DIR"
    mkdir -p "$OI_BIN_DIR"
    mkdir -p "${OI_SHARE_DIR}/lib/cache"
    log_success "Directories created"
}

clone_llama_cpp() {
    log_info "Cloning llama.cpp..."
    
    if [ -d "$LLAMA_CPP_DIR/.git" ]; then
        log_info "llama.cpp already exists, updating..."
        cd "$LLAMA_CPP_DIR"
        git fetch origin
        git reset --hard origin/master
    else
        git clone https://github.com/ggml-org/llama.cpp.git "$LLAMA_CPP_DIR"
    fi
    
    log_success "llama.cpp cloned/updated"
}

build_llama_cpp() {
    log_info "Building llama.cpp..."
    cd "$LLAMA_CPP_DIR"
    
    if detect_cuda; then
        log_info "Configuring with CUDA support..."
        cmake -B build -DGGML_CUDA=ON
    else
        log_info "Configuring for CPU-only..."
        cmake -B build
    fi
    
    log_info "Compiling... (this may take a few minutes)"
    cmake --build build --config Release -j$(nproc)
    
    log_success "llama.cpp built successfully"
}

verify_llama_build() {
    log_info "Verifying llama.cpp build..."
    
    if [ ! -f "$LLAMA_CPP_DIR/build/bin/llama-cli" ]; then
        log_error "llama-cli not found after build"
        log_error "Build may have failed. Check the output above for errors."
        exit 1
    fi
    
    log_success "llama-cli found at $LLAMA_CPP_DIR/build/bin/llama-cli"
}

copy_oi_files() {
    log_info "Copying oi files..."
    
    cp "$SCRIPT_DIR/oi" "$OI_SHARE_DIR/"
    chmod +x "$OI_SHARE_DIR/oi"
    
    cp -r "$SCRIPT_DIR/lib" "$OI_SHARE_DIR/"
    cp "$SCRIPT_DIR/models.json" "$OI_SHARE_DIR/lib/" 2>/dev/null || true
    cp "$SCRIPT_DIR/README.md" "$OI_SHARE_DIR/" 2>/dev/null || true
    cp "$SCRIPT_DIR/TODO.md" "$OI_SHARE_DIR/" 2>/dev/null || true
    
    log_success "oi files copied"
}

create_wrapper() {
    log_info "Creating wrapper script at $OI_WRAPPER..."
    
    # Ensure bin directory exists
    mkdir -p "$OI_BIN_DIR"
    
    cat > "$OI_WRAPPER" << 'WRAPPER_EOF'
#!/bin/bash
OI_SHARE_DIR="${HOME}/.local/share/oi"
OI_MAIN="${OI_SHARE_DIR}/oi"

if [ ! -d "$OI_SHARE_DIR" ]; then
    echo "Error: oi not installed. Run install.sh from the oi repository."
    exit 1
fi

if [ ! -f "$OI_MAIN" ]; then
    echo "Error: oi installation corrupted at $OI_MAIN"
    echo "Run install.sh to reinstall."
    exit 1
fi

if [[ ":$PATH:" != *":${HOME}/.local/bin:"* ]]; then
    echo "Warning: ~/.local/bin is not in your PATH"
    echo "Add this to your ~/.bashrc or ~/.zshrc:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

export OI_SHARE_DIR
exec "$OI_MAIN" "$@"
WRAPPER_EOF
    
    chmod +x "$OI_WRAPPER"
    log_success "Wrapper created"
}

ensure_path() {
    log_info "Checking PATH configuration..."
    
    local shell_config=""
    if [ -n "$ZSH_VERSION" ]; then
        shell_config="${HOME}/.zshrc"
    else
        shell_config="${HOME}/.bashrc"
    fi
    
    if [[ ":$PATH:" != *":${OI_BIN_DIR}:"* ]]; then
        log_warning "$OI_BIN_DIR is not in your PATH"
        echo ""
        echo "Add the following line to $shell_config:"
        echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
        echo ""
        echo "Then reload your shell config:"
        echo "  source $shell_config"
        echo ""
    else
        log_success "PATH is correctly configured"
    fi
}

show_completion() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo -e "${GREEN}  oi installation complete!${NC}"
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo ""
    echo "Version: $VERSION"
    echo "Installation directory: $OI_SHARE_DIR"
    echo "Wrapper: $OI_WRAPPER"
    echo ""
    echo "Next steps:"
    echo "  1. Make sure ~/.local/bin is in your PATH (see warning above if needed)"
    echo "  2. Verify installation: which oi"
    echo "  3. Show help: oi --help"
    echo "  4. Start chatting: oi"
    echo ""
    echo "To uninstall: cd ~/oi && bash uninstall.sh"
    echo ""
}

main() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    echo -e "${CYAN}  oi Installer v${VERSION}${NC}"
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    echo ""
    
    check_dependencies
    detect_cuda
    check_existing_install
    create_directories
    clone_llama_cpp
    build_llama_cpp
    verify_llama_build
    copy_oi_files
    create_wrapper
    ensure_path
    show_completion
}

main "$@"
