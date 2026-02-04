#!/bin/bash
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

OI_SHARE_DIR="${HOME}/.local/share/oi"
OI_BIN_DIR="${HOME}/.local/bin"
OI_WRAPPER="${OI_BIN_DIR}/oi"
LLAMA_MODELS_DIR="${OI_SHARE_DIR}/llama.cpp/models"

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

confirm_uninstall() {
    echo ""
    echo -e "${YELLOW}This will remove:${NC}"
    echo "  - $OI_WRAPPER (oi command)"
    echo "  - $OI_SHARE_DIR (oi installation directory)"
    echo ""
    
    if [ -d "$LLAMA_MODELS_DIR" ] && [ -n "$(ls -A $LLAMA_MODELS_DIR 2>/dev/null)" ]; then
        local model_count=$(ls -1 "$LLAMA_MODELS_DIR" 2>/dev/null | wc -l)
        log_warning "Found $model_count downloaded model(s) in $LLAMA_MODELS_DIR"
        echo ""
    fi
    
    echo -e "${RED}This action cannot be undone!${NC}"
    echo ""
    read -p "Are you sure you want to uninstall oi? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Uninstall cancelled"
        exit 0
    fi
    
    echo ""
    return 0
}

ask_preserve_models() {
    if [ -d "$LLAMA_MODELS_DIR" ] && [ -n "$(ls -A $LLAMA_MODELS_DIR 2>/dev/null)" ]; then
        echo ""
        read -p "Preserve downloaded models? [Y/n] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Nn]$ ]]; then
            return 0
        fi
    fi
    return 1
}

remove_wrapper() {
    log_info "Removing wrapper script..."
    
    if [ -L "$OI_WRAPPER" ]; then
        rm -f "$OI_WRAPPER"
        log_success "Wrapper symlink removed"
    elif [ -f "$OI_WRAPPER" ]; then
        rm -f "$OI_WRAPPER"
        log_success "Wrapper script removed"
    else
        log_warning "Wrapper not found at $OI_WRAPPER"
    fi
}

remove_installation() {
    local preserve_models="$1"
    
    if [ ! -d "$OI_SHARE_DIR" ]; then
        log_warning "Installation directory not found at $OI_SHARE_DIR"
        return 0
    fi
    
    if [ "$preserve_models" = "yes" ]; then
        log_info "Removing oi files (preserving models)..."
        
        if [ -d "$LLAMA_MODELS_DIR" ]; then
            local models_backup="${HOME}/.local/share/oi_models_backup"
            log_info "Backing up models to $models_backup..."
            mkdir -p "$models_backup"
            mv "$LLAMA_MODELS_DIR"/* "$models_backup/" 2>/dev/null || true
            log_success "Models preserved in $models_backup"
        fi
        
        rm -rf "$OI_SHARE_DIR/oi"
        rm -rf "$OI_SHARE_DIR/lib"
        rm -rf "$OI_SHARE_DIR/llama.cpp"
        rm -f "$OI_SHARE_DIR/README.md"
        rm -f "$OI_SHARE_DIR/TODO.md"
        
        rmdir "$OI_SHARE_DIR" 2>/dev/null || log_warning "Directory not empty, not removing $OI_SHARE_DIR"
        
        log_success "oi files removed (models preserved)"
    else
        log_info "Removing entire installation directory..."
        rm -rf "$OI_SHARE_DIR"
        log_success "Installation directory removed"
    fi
}

show_completion() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo -e "${GREEN}  oi uninstalled successfully!${NC}"
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo ""
    echo "To reinstall oi:"
    echo "  1. Navigate to the oi source directory"
    echo "  2. Run: bash install.sh"
    echo ""
    echo "Thank you for using oi!"
    echo ""
}

main() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    echo -e "${CYAN}  oi Uninstaller${NC}"
    echo -e "${CYAN}═══════════════════════════════════════${NC}"
    
    if [ ! -d "$OI_SHARE_DIR" ] && [ ! -f "$OI_WRAPPER" ]; then
        log_warning "oi is not installed"
        echo "Nothing to remove."
        exit 0
    fi
    
    confirm_uninstall
    
    local preserve_models="no"
    if ask_preserve_models; then
        preserve_models="yes"
    fi
    
    remove_wrapper
    remove_installation "$preserve_models"
    show_completion
}

main "$@"
