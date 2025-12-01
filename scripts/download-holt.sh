#!/bin/bash
set -e

# Configuration
REPO="hearth-insights/holt"
VERSION="latest" # or specific tag like "v0.1.0"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
    echo "Error: Unsupported OS: $OS"
    exit 1
fi

# Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        echo "Error: Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

# Parse arguments
INSTALL=false
INSTALL_DIR="/usr/local/bin"
TARGET_OS=""
TARGET_ARCH=""

for arg in "$@"; do
    case $arg in
        -i|--install)
            INSTALL=true
            shift
            ;;
        --os=*)
            TARGET_OS="${arg#*=}"
            shift
            ;;
        --arch=*)
            TARGET_ARCH="${arg#*=}"
            shift
            ;;
        *)
            ;;
    esac
done

# Override detection if flags provided
if [ -n "$TARGET_OS" ]; then
    OS="$TARGET_OS"
fi
if [ -n "$TARGET_ARCH" ]; then
    ARCH="$TARGET_ARCH"
fi

echo "Detected system: $OS/$ARCH"

# Determine download URLs
if [ "$VERSION" = "latest" ]; then
    BASE_URL="https://github.com/$REPO/releases/latest/download"
else
    BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
fi

HOLT_BINARY="holt-$OS-$ARCH"
PUP_BINARY="holt-pup-$OS-$ARCH"

echo "Downloading binaries from $REPO ($VERSION)..."

# Download holt CLI
echo "Downloading holt CLI..."
curl -L -o holt "$BASE_URL/$HOLT_BINARY"
chmod +x holt
echo "✓ holt downloaded"

# Download holt-pup
echo "Downloading holt-pup..."
curl -L -o holt-pup "$BASE_URL/$PUP_BINARY"
chmod +x holt-pup
echo "✓ holt-pup downloaded"

# Install if requested
if [ "$INSTALL" = true ]; then
    echo "Installing to $INSTALL_DIR..."
    
    # Check if we have write access to INSTALL_DIR
    if [ ! -w "$INSTALL_DIR" ]; then
        echo "Error: No write permission for $INSTALL_DIR. Try running with sudo."
        echo "Binaries are available in the current directory."
        exit 1
    fi

    mv holt "$INSTALL_DIR/holt"
    # We generally don't install pup to bin as it's used by Dockerfiles, but we can if requested
    # For now, let's keep pup local as it's usually copied into containers
    echo "✓ holt installed to $INSTALL_DIR/holt"
    echo "Note: holt-pup kept in current directory for Dockerfile usage."
else
    echo ""
    echo "Download complete!"
    echo "Run ./holt --help to get started."
    echo "To install, run with --install (may require sudo)."
fi
