.PHONY: help test test-verbose test-integration test-e2e test-all coverage coverage-html lint build build-orchestrator build-pup docker-orchestrator build-all clean install test-pup cleanup-docker

# Use Go 1.24 if available in /usr/local/go, otherwise use system go
GO := $(shell [ -x /usr/local/go/bin/go ] && echo /usr/local/go/bin/go || echo go)

# Version information from git
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Go build flags
LDFLAGS := -ldflags "-X github.com/hearth-insights/holt/pkg/version.Version=$(VERSION) -X github.com/hearth-insights/holt/pkg/version.Commit=$(COMMIT) -X github.com/hearth-insights/holt/pkg/version.Date=$(BUILD_DATE)"

# Test flags (can be overridden)
TEST_FLAGS ?= -v

# Default target

# Default target
help:
	@echo "Holt Development Makefile"
	@echo ""
	@echo "Targets:"
	@echo ""
	@echo "Common workflows:"
	@echo "  build-all           - Build everything (CLI + orchestrator + pup)"
	@echo "  demos               - Build all demos using their respective Makefiles"
	@echo "  build               - Build the holt CLI binary for current platform"
	@echo "  docker-orchestrator - Build orchestrator Docker image (required for 'holt up')"
	@echo ""
	@echo "Cross-compilation:"
	@echo "  build-darwin-arm64  - Build for macOS ARM64 (M1/M2/M3 Macs)"
	@echo "  build-darwin-amd64  - Build for macOS Intel"
	@echo "  build-linux-arm64   - Build for Linux ARM64"
	@echo "  build-linux-amd64   - Build for Linux AMD64"
	@echo ""
	@echo "Testing:"
	@echo "  test                - Run all unit tests"
	@echo "  test-verbose        - Run all unit tests with verbose output"
	@echo "  test-crypto         - Run cryptographic verification tests (M4.6)"
	@echo "  test-pup            - Run pup unit and integration tests"
	@echo "  test-integration    - Run orchestrator integration tests (requires Docker)"
	@echo "  test-e2e            - Run Phase 2 E2E test suite (requires Docker)"
	@echo "  test-all            - Run ALL tests (unit + pup + integration + e2e)"
	@echo "  test-failed         - Run all tests, print out only the failures"
	@echo "  coverage            - Run tests and show coverage report"
	@echo "  coverage-html       - Generate HTML coverage report"
	@echo "  lint                - Run go vet and staticcheck"
	@echo ""
	@echo "Development:"
	@echo "  build-orchestrator  - Build orchestrator binary (for debugging only)"
	@echo "  build-pup           - Build agent pup binary (Linux, matches host arch)"
	@echo "  cleanup-docker      - Remove all Holt Docker containers and networks"
	@echo "  install             - Install holt binary to user bin directory"
	@echo "                        (tries /usr/local/bin, ~/.local/bin, or GOPATH/bin)"
	@echo "                        Use PREFIX=/custom/path for custom location"
	@echo "  clean               - Remove build artifacts"

# Run all tests (depends on binaries being built)
test:
	@echo "Running tests..."
	@$(GO) test $(TEST_FLAGS) ./...

# Run all tests with verbose output
test-verbose:
	@echo "Running tests (verbose)..."
	@$(MAKE) test TEST_FLAGS="-v"

# Run cryptographic verification tests (M4.6)
test-crypto:
	@echo "Running cryptographic verification tests..."
	@$(GO) test -v ./pkg/blackboard -run 'Test(Hash|Canonical|Validate|Payload)'
	@echo "✓ All cryptographic tests passed"

# Run tests with coverage
coverage: build build-pup
	@echo "Running tests with coverage..."
	@$(GO) test -coverprofile=coverage.out ./...
	@echo ""
	@echo "Coverage by package:"
	@$(GO) tool cover -func=coverage.out
	@echo ""
	@echo "To view HTML coverage report, run: make coverage-html"

# Generate HTML coverage report
coverage-html: coverage
	@echo "Generating HTML coverage report..."
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@echo "Open in browser: open coverage.html (macOS) or xdg-open coverage.html (Linux)"

# Run linters
lint:
	@echo "Running go vet..."
	@$(GO) vet ./...
	@echo "✓ go vet passed"
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "Running staticcheck..."; \
		staticcheck ./...; \
		echo "✓ staticcheck passed"; \
	else \
		echo "⚠️  staticcheck not installed (optional)"; \
		echo "   Install with: $(GO) install honnef.co/go/tools/cmd/staticcheck@latest"; \
	fi

# Run orchestrator integration tests (requires Docker)
test-integration:
	@echo "Running orchestrator integration tests..."
	@$(GO) test $(TEST_FLAGS) -tags=integration ./cmd/orchestrator

# Run Phase 2 E2E test suite (requires Docker)
# M3.4: Now depends on docker-orchestrator to ensure latest orchestrator image is available
test-e2e: build build-pup docker-orchestrator
	@echo "Running Phase 2 E2E test suite..."
	@echo "Building example-git-agent Docker image..."
	@docker build -q -t example-git-agent:latest -f agents/example-git-agent/Dockerfile . > /dev/null
	@docker build -q -t example-agent:latest -f agents/example-agent/Dockerfile . > /dev/null
	@echo "Running E2E tests..."
	@$(GO) test $(TEST_FLAGS) -timeout 15m -tags=integration -run="TestE2E|TestPerformance" ./cmd/holt/commands
	@echo "✓ All E2E tests passed"

# Run all tests (unit + pup + integration + e2e)
test-all: test test-pup test-integration test-e2e
	@echo ""
	@echo "========================================"
	@echo "✓ ALL TESTS PASSED"
	@echo "========================================"
	@echo "  Unit tests:        ✓"
	@echo "  Pup tests:         ✓"
	@echo "  Integration tests: ✓"
	@echo "  E2E tests:         ✓"
	@echo ""

# Run all tests and show only the failures
# Uses `go test -json` and a custom awk script to extract complete failure output
test-failed: build build-pup docker-orchestrator
	@echo "Running all tests with failure filtering..."
	@echo "Building Docker images for E2E tests..."
	@docker build -q -t example-git-agent:latest -f agents/example-git-agent/Dockerfile . > /dev/null 2>&1 || true
	@docker build -q -t example-agent:latest -f agents/example-agent/Dockerfile . > /dev/null 2>&1 || true
	@echo ""
	@echo "Running unit tests..."
	@$(GO) test $(TEST_FLAGS) -json ./pkg/... ./internal/... ./cmd/holt/... 2>&1 | bash scripts/filter-test-failures.sh || true
	@echo ""
	@echo "Running pup tests..."
	@$(GO) test $(TEST_FLAGS) -json ./cmd/pup 2>&1 | bash scripts/filter-test-failures.sh || true
	@echo ""
	@echo "Running integration tests..."
	@$(GO) test $(TEST_FLAGS) -json -tags=integration ./cmd/orchestrator 2>&1 | bash scripts/filter-test-failures.sh || true
	@echo ""
	@echo "Running E2E tests..."
	@$(GO) test $(TEST_FLAGS) -json -timeout 15m -tags=integration -run="TestE2E|TestPerformance" ./cmd/holt/commands 2>&1 | bash scripts/filter-test-failures.sh || true
	@echo ""
	@echo "========================================"
	@echo "Test failure summary complete"
	@echo "========================================"

# Build the holt binary
build:
	@echo "Building holt CLI..."
	@mkdir -p bin
	@$(GO) build $(LDFLAGS) -o bin/holt ./cmd/holt
	@echo "✓ Built: bin/holt (version: $(VERSION), commit: $(COMMIT))"

# Cross-compile for macOS ARM64 (M1/M2/M3 Macs)
build-darwin-arm64:
	@echo "Building holt CLI for macOS ARM64..."
	@mkdir -p bin
	@GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-darwin-arm64 ./cmd/holt
	@echo "✓ Built: bin/holt-darwin-arm64"

# Cross-compile for macOS Intel
build-darwin-amd64:
	@echo "Building holt CLI for macOS Intel..."
	@mkdir -p bin
	@GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-darwin-amd64 ./cmd/holt
	@echo "✓ Built: bin/holt-darwin-amd64"

# Cross-compile for Linux ARM64
build-linux-arm64:
	@echo "Building holt CLI for Linux ARM64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-linux-arm64 ./cmd/holt
	@echo "✓ Built: bin/holt-linux-arm64"

# Cross-compile for Linux AMD64
build-linux-amd64:
	@echo "Building holt CLI for Linux AMD64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-linux-amd64 ./cmd/holt
	@echo "✓ Built: bin/holt-linux-amd64"

# Build the orchestrator binary
build-orchestrator:
	@echo "Building orchestrator..."
	@mkdir -p bin
	@$(GO) build $(LDFLAGS) -o bin/holt-orchestrator ./cmd/orchestrator
	@echo "✓ Built: bin/holt-orchestrator"

# Build the agent pup binary
# Note: Builds for Linux by default since pups run inside Docker containers
# Detects host architecture to match Docker's default platform
# Use build-pup-darwin-* targets if you need a native macOS binary for testing
build-pup:
	@echo "Building agent pup for Linux..."
	@mkdir -p bin
	@if [ "$$(uname -m)" = "aarch64" ] || [ "$$(uname -m)" = "arm64" ]; then \
		GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-pup ./cmd/pup; \
		echo "✓ Built: bin/holt-pup (Linux ARM64)"; \
	else \
		GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-pup ./cmd/pup; \
		echo "✓ Built: bin/holt-pup (Linux AMD64)"; \
	fi

# Cross-compile pup for macOS ARM64
build-pup-darwin-arm64:
	@echo "Building pup for macOS ARM64..."
	@mkdir -p bin
	@GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-pup-darwin-arm64 ./cmd/pup
	@echo "✓ Built: bin/holt-pup-darwin-arm64"

# Cross-compile pup for macOS Intel
build-pup-darwin-amd64:
	@echo "Building pup for macOS Intel..."
	@mkdir -p bin
	@GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-pup-darwin-amd64 ./cmd/pup
	@echo "✓ Built: bin/holt-pup-darwin-amd64"

# Cross-compile pup for Linux ARM64
build-pup-linux-arm64:
	@echo "Building pup for Linux ARM64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-pup-linux-arm64 ./cmd/pup
	@echo "✓ Built: bin/holt-pup-linux-arm64"

# Cross-compile pup for Linux AMD64
build-pup-linux-amd64:
	@echo "Building pup for Linux AMD64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-pup-linux-amd64 ./cmd/pup
	@echo "✓ Built: bin/holt-pup-linux-amd64"

# Cross-compile orchestrator for Linux ARM64
build-orchestrator-linux-arm64:
	@echo "Building orchestrator for Linux ARM64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o bin/holt-orchestrator-linux-arm64 ./cmd/orchestrator
	@echo "✓ Built: bin/holt-orchestrator-linux-arm64"

# Cross-compile orchestrator for Linux AMD64
build-orchestrator-linux-amd64:
	@echo "Building orchestrator for Linux AMD64..."
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o bin/holt-orchestrator-linux-amd64 ./cmd/orchestrator
	@echo "✓ Built: bin/holt-orchestrator-linux-amd64"

# Run pup unit and integration tests
test-pup:
	@echo "Running pup tests..."
	@$(GO) test $(TEST_FLAGS) -race ./internal/pup
	@$(GO) test $(TEST_FLAGS) -timeout 60s ./cmd/pup
	@echo "✓ All pup tests passed"

# Build orchestrator Docker image
docker-orchestrator:
	@echo "Building orchestrator Docker image..."
	@docker build -f cmd/orchestrator/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(BUILD_DATE) \
		-t holt-orchestrator:latest .
	@docker tag holt-orchestrator:latest ghcr.io/hearth-insights/holt/holt-orchestrator:latest
	@echo "✓ Built: holt-orchestrator:latest"
	@echo "✓ Tagged: ghcr.io/hearth-insights/holt/holt-orchestrator:latest"

# Build everything (CLI + orchestrator Docker image + pup)
build-all: build build-pup docker-orchestrator
	@echo ""
	@echo "✓ Build complete!"
	@echo "  - CLI binary: bin/holt"
	@echo "  - Pup binary: bin/holt-pup (Linux, for Docker)"
	@echo "  - Orchestrator image: holt-orchestrator:latest"
	@echo ""
	@echo "Ready to use: ./bin/holt up"

# Install holt binary to user bin directory
# Default: /usr/local/bin (recommended for macOS, may require sudo)
# Fallback: ~/.local/bin or GOPATH/bin
# Override with: make install PREFIX=/custom/path
install: build
	@echo "Installing holt..."
	@if [ -n "$(PREFIX)" ]; then \
		INSTALL_DIR="$(PREFIX)/bin"; \
		mkdir -p "$$INSTALL_DIR" 2>/dev/null || true; \
		cp bin/holt "$$INSTALL_DIR/holt"; \
		chmod +x "$$INSTALL_DIR/holt"; \
	elif [ -d "/usr/local/bin" ]; then \
		INSTALL_DIR="/usr/local/bin"; \
		echo "Installing to: $$INSTALL_DIR (may require sudo)"; \
		if [ -w "$$INSTALL_DIR" ]; then \
			cp bin/holt "$$INSTALL_DIR/holt"; \
			chmod +x "$$INSTALL_DIR/holt"; \
		else \
			sudo cp bin/holt "$$INSTALL_DIR/holt"; \
			sudo chmod +x "$$INSTALL_DIR/holt"; \
		fi; \
	elif [ -d "$$HOME/.local/bin" ]; then \
		INSTALL_DIR="$$HOME/.local/bin"; \
		cp bin/holt "$$INSTALL_DIR/holt"; \
		chmod +x "$$INSTALL_DIR/holt"; \
	else \
		INSTALL_DIR="$$($(GO) env GOPATH)/bin"; \
		mkdir -p "$$INSTALL_DIR"; \
		cp bin/holt "$$INSTALL_DIR/holt"; \
		chmod +x "$$INSTALL_DIR/holt"; \
	fi; \
	echo "✓ Installed: $$INSTALL_DIR/holt"; \
	if [ "$$(uname)" = "Darwin" ]; then \
		sudo xattr -cr "$$INSTALL_DIR/holt" 2>/dev/null || true; \
		sudo codesign --force --deep --sign - "$$INSTALL_DIR/holt" 2>/dev/null && \
			echo "✓ Applied macOS ad-hoc code signature" || \
			echo "⚠️  Warning: Could not sign binary (may be killed by macOS). Run: sudo codesign --force --deep --sign - $$INSTALL_DIR/holt"; \
	fi; \
	echo ""; \
	if ! echo "$$PATH" | grep -q "$${INSTALL_DIR}"; then \
		echo "⚠️  Note: $$INSTALL_DIR is not in your PATH"; \
		echo "   Add to PATH: export PATH=\"$$INSTALL_DIR:\$$PATH\""; \
	fi

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@echo "✓ Clean complete"

# Cleanup all Holt Docker resources
cleanup-docker:
	@echo "Cleaning up Holt Docker containers..."
	@docker rm -f $$(docker ps -a -q -f "label=holt.project=true") 2>/dev/null || true
	@echo "Cleaning up Holt Docker networks..."
	@docker network rm $$(docker network ls -q -f "label=holt.project=true") 2>/dev/null || true
	@echo "✓ Cleanup complete"

# Build all demos dynamically
demos:
	@echo "Building all demos found in demos/..."
	@for dir in demos/*; do \
		if [ -f "$$dir/Makefile" ]; then \
			echo "--------------------------------------------------"; \
			echo "Building demo in $$dir"; \
			echo "--------------------------------------------------"; \
			$(MAKE) -f $$dir/Makefile; \
		fi \
	done
	@echo "✓ All demos built successfully."

# Alias for backward compatibility (or if I just added it, renaming is fine)
build-all-demos: demos

# General phony targets
.PHONY: all help build clean install test lint format vet verify build-all-demos clean-all-demos demos

# Clean all demo images
clean-all-demos:
	@echo "Cleaning all demo images..."
	@$(MAKE) -f demos/terraform-generator/Makefile clean-demo-terraform
	@# Add other demos here as they are updated
	@echo "✓ All demo images cleaned."
