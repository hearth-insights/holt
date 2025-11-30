# 4. Build and Run

See the [Release Strategy](./release_strategy.md) for details on how these are built.

## 1. Obtain Holt Binaries

### Option A: Download Script (Easiest)
We provide a script to automatically detect your OS/Architecture and download the correct binaries.

```bash
# Download the script
curl -O https://raw.githubusercontent.com/hearth-insights/holt/main/scripts/download-holt.sh
chmod +x download-holt.sh

# Run to download binaries to current directory
./download-holt.sh

# Or run with --install to install 'holt' to /usr/local/bin (requires sudo)
sudo ./download-holt.sh --install
```

### Option B: Manual Download
Download the latest release from the **[GitHub Releases Page](https://github.com/hearth-insights/holt/releases/tag/latest)**.

You will need to download the appropriate binaries for your OS/Arch:
*   **CLI**: `holt-<os>-<arch>` (e.g., `holt-darwin-arm64`) -> Rename to `holt`
*   **Pup**: `holt-pup-<os>-<arch>` (e.g., `holt-pup-linux-amd64`) -> Rename to `holt-pup` (Required for building agent images)

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
If you built from source using `make build-all`, the image `holt-orchestrator:latest` is already created.
To build just the image:

```bash
make docker-orchestrator
```

## 3. Build Agent Images
You need to build the Docker images for your agents.
Assuming you have your `Dockerfile` and `holt-pup` binary in the current directory:

```bash
docker build -t my-agent:latest .
```

Ensure the tag (`my-agent:latest`) matches the `image` field in your `holt.yaml`.

## 4. Initialize Holt
If you haven't already, initialize a new Holt project.

```bash
./holt init
```

## 5. Start Holt
Start the Holt orchestrator and services.

```bash
./holt up
```

## 6. Run a Workflow
Submit a goal to the system.

```bash
./holt forage --goal "Create a file named hello.txt"
```

## 7. Monitor
Watch the progress.

```bash
./holt watch
```

## Summary
You have now successfully configured, built, and run a Holt instance!
