# Feature: GitHub Actions Release Workflow

## Goal
Automate the building and releasing of `holt`, `orchestrator`, and `pup` binaries, as well as the `orchestrator` Docker image, to allow independent systems to deploy Holt without building from source.

## Components

### 1. Binaries
We need to build the following binaries for multiple platforms:

*   **holt** (CLI)
    *   Platforms: Linux (amd64, arm64), macOS (amd64, arm64)
*   **pup** (Agent)
    *   Platforms: Linux (amd64, arm64), macOS (amd64, arm64)
*   **orchestrator** (Server)
    *   Platforms: Linux (amd64, arm64) - Primarily runs in Docker, but a binary is useful.

### 2. Docker Image
*   **orchestrator**
    *   Registry: GitHub Container Registry (ghcr.io)
    *   Tags: `latest`, `<version-tag>`

## Workflow Design

### Trigger
*   Push to tags matching `v*` (e.g., `v1.0.0`) - Creates a stable release.
*   Push to `main` branch - Updates the `latest` rolling release and Docker image.
*   Manual dispatch (`workflow_dispatch`) for testing.

### Jobs

#### `build-binaries`
*   Strategy: Matrix build for OS/Arch.
*   Steps:
    *   Checkout code.
    *   Setup Go.
    *   Build binaries using `make` or direct `go build` commands.
    *   Upload artifacts.

#### `build-push-image`
*   Steps:
    *   Checkout code.
    *   Login to GHCR.
    *   Build and push Docker image using `docker/build-push-action`.

#### `create-release`
*   Needs: `build-binaries`
*   Steps:
    *   Download artifacts.
    *   Create GitHub Release.
    *   **[Build & Run](./build_and_run.md)**: How to use the released binaries.

## Gaps & Requirements
*   Need to ensure `Makefile` or build scripts support all target platforms cleanly, or handle it within the workflow.
*   Need to configure permissions for GITHUB_TOKEN to allow package publishing and release creation.
