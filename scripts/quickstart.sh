#!/bin/bash
# scripts/quickstart.sh
# Holt: Instant Audit Demo
# Usage: curl -sL https://raw.githubusercontent.com/hearth-insights/holt/main/scripts/quickstart.sh | bash

set -e

GREEN='\033[0;32m'
TEAL='\033[0;36m' # Brand alignment
RED='\033[0;31m'
NC='\033[0m'

echo -e "${TEAL}::: HOLT SOVEREIGN ORCHESTRATION :::${NC}"

# Safety Check: Ensure we aren't already in a Git repository
if [ -d ".git" ]; then
    echo -e "${RED}Error: Current directory is already a Git repository.${NC}"
    echo "The quickstart script initializes a new git repository in the current directory."
    echo "Please run this script in a new, empty directory."
    exit 1
fi

echo "Initializing demonstration environment in current directory..."

# Check prerequisites
if ! command -v docker &> /dev/null; then
    echo "Error: docker is required."
    exit 1
fi

# Store CWD to return to later
TARGET_DIR=$(pwd)

# Create a temporary workspace for building Holt (keeps user source clean)
BUILD_DIR=$(mktemp -d -t holt-build-XXXXXX)
echo "Build Workspace: $BUILD_DIR"

# Clone (shallow)
echo -e "${GREEN}1. Retrieving Holt...${NC}"
git clone --depth 1 https://github.com/hearth-insights/holt.git "$BUILD_DIR" > /dev/null 2>&1

# Build (Simulated for speed in demo, or actual build if Go is present)
# For the sake of a quick demo, we will check if the user has Go.
# If not, we would pull a Docker image.
# Assuming Go for now as per README prerequisites.
if command -v go &> /dev/null; then
    echo -e "${GREEN}2. Compiling binaries...${NC}"
    # Build in temp dir
    (cd "$BUILD_DIR" && make build > /dev/null 2>&1)
    
    # Install binaries to local bin/
    mkdir -p "$TARGET_DIR/bin"
    cp "$BUILD_DIR/bin/holt" "$TARGET_DIR/bin/"
else
    echo "Error: go 1.21+ is required for this source build demo."
    rm -rf "$BUILD_DIR"
    exit 1
fi

# Setup Agent
echo -e "${GREEN}3. Provisioning 'Git-Agent' container...${NC}"
# Use the Dockerfile from the build dir context
(cd "$BUILD_DIR" && docker build -t example-git-agent:latest -f agents/example-git-agent/Dockerfile . > /dev/null 2>&1)

# Initialize Project in TARGET_DIR
echo -e "${GREEN}4. Initializing forensic environment...${NC}"
git init > /dev/null 2>&1
git commit --allow-empty -m "Genesis" > /dev/null 2>&1

# Run holt init in current dir using the newly built binary
"$TARGET_DIR/bin/holt" init > /dev/null

# Clean up build artifacts
rm -rf "$BUILD_DIR"

# Clean up scaffolded agents (quickstart uses its own container)
rm -rf agents/

# Config
cat > holt.yml <<EOF
version: "1.0"
orchestrator:
  image: "ghcr.io/hearth-insights/holt/holt-orchestrator:latest"
agents:
  GitAgent:
    role: "Git Agent"
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    workspace:
      mode: rw
    bidding_strategy:
      type: "exclusive"
services:
  redis:
    image: redis:7-alpine
EOF

# Setup Git Ignore
cat > .gitignore <<EOF
bin/
holt-demo-*
*.log
EOF

# Commit initialization
git add holt.yml .gitignore
git commit -m "Initialize Holt demonstration" > /dev/null 2>&1

# Run Workflow
echo -e "${TEAL}::: SYSTEM ONLINE. EXECUTING WORKFLOW :::${NC}"
# Capture output to log file but stream errors if it fails
if ! "$TARGET_DIR/bin/holt" up -d > holt_up.log 2>&1; then
    echo -e "${RED}Error starting Holt instance:${NC}"
    cat holt_up.log
    exit 1
fi

"$TARGET_DIR/bin/holt" forage --goal "audit_proof.txt" > /dev/null

# Show Proof
echo -e "${TEAL}::: AUDIT TRAIL GENERATED :::${NC}"
"$TARGET_DIR/bin/holt" hoard
echo -e "${GREEN}Proof of Work:${NC}"
ls -la audit_proof.txt

# Cleanup (optional - for a persistent demo we might want to keep it running? 
# But conventionally quickstarts clean up. Let's keep it running so user can play per request)
# "$TARGET_DIR/bin/holt" down > /dev/null 2>&1
# echo -e "${TEAL}Demo complete. Infrastructure torn down.${NC}"

echo ""
echo -e "${GREEN}Demo environment ready!${NC}"
echo "You can now run holt commands directly:"
echo "  ./bin/holt watch"
echo "  ./bin/holt hoard"
echo "  ./bin/holt down  # to stop the system"
