# Installation

## 1. Obtain Holt Binaries

### Option A: Download Script (Easiest)
We provide a script to automatically detect your OS/Architecture and download the correct binaries.

```bash
# Download the script
curl -O https://raw.githubusercontent.com/hearth-insights/holt/main/scripts/download-holt.sh
chmod +x download-holt.sh

# Run to download binaries to current directory (auto-detects host OS)
./download-holt.sh

# Download specifically for Linux ARM64 (e.g., for Docker build on Mac)
./download-holt.sh --os linux --arch arm64

# Or run with --install to install 'holt' to /usr/local/bin (requires sudo)
sudo ./download-holt.sh --install
```

### Option B: Manual Download
Download the latest release from the **[GitHub Releases Page](https://github.com/hearth-insights/holt/releases/tag/latest)**.

You will need to download the appropriate binaries for your OS/Arch.
**Note**: If you are building Docker images, you **must** download the Linux binary for the container, even if you are on macOS/Windows.

| Component | OS | Arch | Filename |
| :--- | :--- | :--- | :--- |
| **CLI** | macOS | Apple Silicon | `holt-darwin-arm64` |
| **CLI** | macOS | Intel | `holt-darwin-amd64` |
| **CLI** | Linux | ARM64 | `holt-linux-arm64` |
| **CLI** | Linux | AMD64 | `holt-linux-amd64` |
| **Pup** | Linux | ARM64 | `holt-pup-linux-arm64` |
| **Pup** | Linux | AMD64 | `holt-pup-linux-amd64` |

> [!IMPORTANT]
> **Cross-Platform Docker Builds**: If you are building Docker images on macOS (Apple Silicon), you must download the **Linux ARM64** version of `holt-pup` (`holt-pup-linux-arm64`) to copy into your container. Using the macOS binary inside a Linux container will cause an `exec format error`.

**Renaming**:
*   Rename your CLI binary to `holt`.
*   Rename your Pup binary to `holt-pup`.

Make them executable:
```bash
chmod +x holt holt-pup
```

### Option C: Build from Source
Run the following command in the root of the `holt` repository to build the CLI, Orchestrator, and Pup binaries.

```bash
make build-all
```

This will create:
*   `bin/holt`: The CLI tool.
*   `bin/holt-pup`: The agent pup binary.
*   `holt-orchestrator:latest`: The Docker image for the orchestrator.

## 2. Orchestrator Image

### Option A: Pull from Registry
The orchestrator image is available from the GitHub Container Registry.

```bash
docker pull ghcr.io/hearth-insights/holt/holt-orchestrator:latest
```

### Option B: Build Locally
If you built from source using `make build-all`, the image `holt-orchestrator:latest` (and `ghcr.io/hearth-insights/holt/holt-orchestrator:latest`) is already created.
To build just the image:

```bash
make docker-orchestrator
```
