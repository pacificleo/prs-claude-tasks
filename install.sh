#!/bin/bash
set -e

# ai-tasks installer
# Usage: curl -fsSL https://raw.githubusercontent.com/kylemclaren/claude-tasks/main/install.sh | bash

REPO="kylemclaren/claude-tasks"
INSTALL_DIR="${AI_TASKS_INSTALL_DIR:-$HOME/.local/bin}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$OS" in
        linux)
            OS="linux"
            ;;
        darwin)
            OS="darwin"
            ;;
        mingw*|msys*|cygwin*)
            OS="windows"
            ;;
        *)
            error "Unsupported operating system: $OS"
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            error "Unsupported architecture: $ARCH"
            ;;
    esac

    PLATFORM="${OS}-${ARCH}"
    info "Detected platform: $PLATFORM"
}

# Get latest release version
get_latest_version() {
    LATEST_VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$LATEST_VERSION" ]; then
        error "Failed to get latest version. No releases found yet?"
    fi
    info "Latest version: $LATEST_VERSION"
}

# Download and install
install() {
    BINARY_NAME="ai-tasks-${PLATFORM}"
    if [ "$OS" = "windows" ]; then
        BINARY_NAME="${BINARY_NAME}.exe"
        TARGET_NAME="ai-tasks.exe"
    else
        TARGET_NAME="ai-tasks"
    fi

    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_VERSION}/${BINARY_NAME}"

    info "Creating install directory: $INSTALL_DIR"
    mkdir -p "$INSTALL_DIR"

    info "Downloading binary..."
    if ! curl -fsSL "$DOWNLOAD_URL" -o "$INSTALL_DIR/$TARGET_NAME"; then
        error "Download failed. Check if release exists for $PLATFORM"
    fi
    chmod +x "$INSTALL_DIR/$TARGET_NAME"

    # On macOS, remove quarantine attribute
    if [ "$OS" = "darwin" ]; then
        info "Removing macOS quarantine..."
        xattr -cr "$INSTALL_DIR/$TARGET_NAME" 2>/dev/null || true
    fi

    info "Binary installed: $INSTALL_DIR/$TARGET_NAME"
}

# Check if install dir is in PATH and add if needed
check_path() {
    if [[ ":$PATH:" == *":$INSTALL_DIR:"* ]]; then
        return 0
    fi

    # Determine shell config file
    local shell_rc=""
    if [ -n "$ZSH_VERSION" ] || [ "$SHELL" = "/bin/zsh" ] || [ "$SHELL" = "/usr/bin/zsh" ]; then
        shell_rc="$HOME/.zshrc"
    else
        shell_rc="$HOME/.bashrc"
    fi

    local path_line="export PATH=\"\$PATH:$INSTALL_DIR\""

    # Check if already in config (maybe PATH just not reloaded)
    if [ -f "$shell_rc" ] && grep -q "$INSTALL_DIR" "$shell_rc" 2>/dev/null; then
        info "$INSTALL_DIR already in $shell_rc (restart shell to apply)"
        return 0
    fi

    # Add to shell config
    echo "" >> "$shell_rc"
    echo "# Added by ai-tasks installer" >> "$shell_rc"
    echo "$path_line" >> "$shell_rc"
    info "Added $INSTALL_DIR to PATH in $shell_rc"

    # Also export for current session
    export PATH="$PATH:$INSTALL_DIR"
}

# Setup Sprite service (if running on Sprite)
setup_sprite_service() {
    if ! command -v sprite-env &> /dev/null; then
        return 0
    fi

    info "Sprite environment detected, setting up daemon service..."

    # Ensure jq is installed (needed by sprite-env)
    if ! command -v jq &> /dev/null; then
        info "Installing jq..."
        sudo apt-get update -qq && sudo apt-get install -y -qq jq
    fi

    # Stop and remove existing service if present (ignore errors)
    sprite-env services stop ai-tasks-daemon >/dev/null 2>&1 || true
    sprite-env services delete ai-tasks-daemon >/dev/null 2>&1 || true

    # Create the daemon service
    if sprite-env services create ai-tasks-daemon \
        --cmd "$INSTALL_DIR/$TARGET_NAME" \
        --args daemon \
        --no-stream >/dev/null 2>&1; then
        info "Daemon service created and started"
    else
        warn "Failed to create daemon service (you can run 'ai-tasks daemon' manually)"
    fi

    echo ""
    echo -e "${CYAN}Sprite service commands:${NC}"
    echo "  sprite-env services list                    # List all services"
    echo "  sprite-env services stop ai-tasks-daemon    # Stop daemon"
    echo "  sprite-env services start ai-tasks-daemon   # Start daemon"
    echo ""
}

# Verify installation
verify() {
    echo ""
    echo -e "${GREEN}✓ Installation successful!${NC}"
    echo ""
    echo "Run the TUI:"
    echo -e "  ${CYAN}ai-tasks${NC}"
    echo ""
    echo "Run scheduler as daemon:"
    echo -e "  ${CYAN}ai-tasks daemon${NC}"
    echo ""
    echo "Data stored in: ~/.ai-tasks/"
    echo ""
}

main() {
    echo ""
    echo "╔═══════════════════════════════════════════╗"
    echo "║       ai-tasks installer                  ║"
    echo "╚═══════════════════════════════════════════╝"
    echo ""

    detect_platform
    get_latest_version
    install
    check_path
    setup_sprite_service
    verify
}

main
