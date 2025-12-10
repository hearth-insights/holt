#!/bin/bash
# scripts/quickstart.sh
# Holt: Instant Audit Demo
# Usage: curl -sL https://raw.githubusercontent.com/hearth-insights/holt/main/scripts/quickstart.sh | bash

set -e

GREEN='\033[0;32m'
TEAL='\033[0;36m' # Brand alignment
NC='\033[0m'

echo -e "${TEAL}::: HOLT SOVEREIGN ORCHESTRATION :::${NC}"
echo "Initializing demonstration environment..."

# Check prerequisites
if ! command -v docker &> /dev/null; then
    echo "Error: docker is required."
    exit 1
fi

# Create a temporary workspace
DEMO_DIR=$(mktemp -d -t holt-demo-XXXXXX)
cd "$DEMO_DIR"
echo "Workspace: $DEMO_DIR"

# Clone (shallow)
echo -e "${GREEN}1. Retrieving Holt...${NC}"
git clone --depth 1 https://github.com/hearth-insights/holt.git . > /dev/null 2>&1

# Build (Simulated for speed in demo, or actual build if Go is present)
# For the sake of a quick demo, we will check if the user has Go.
# If not, we would pull a Docker image.
# Assuming Go for now as per README prerequisites.
if command -v go &> /dev/null; then
    echo -e "${GREEN}2. Compiling binaries...${NC}"
    make build > /dev/null 2>&1
else
    echo "Error: go 1.21+ is required for this source build demo."
    exit 1
fi

# Setup Agent
echo -e "${GREEN}3. Provisioning 'Git-Agent' container...${NC}"
docker build -t example-git-agent:latest -f agents/example-git-agent/Dockerfile . > /dev/null 2>&1

# Initialize Project
echo -e "${GREEN}4. Initializing forensic environment...${NC}"
mkdir demo-project && cd demo-project
git init > /dev/null 2>&1
git commit --allow-empty -m "Genesis" > /dev/null 2>&1
../bin/holt init > /dev/null

# Config
cat > holt.yml <<EOF
version: "1.0"
agents:
  git-agent:
    role: "Git Agent"
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    workspace:
      mode: rw
services:
  redis:
    image: redis:7-alpine
EOF

# Run Workflow
echo -e "${TEAL}::: SYSTEM ONLINE. EXECUTING WORKFLOW :::${NC}"
../bin/holt up -d > /dev/null 2>&1
../bin/holt forage --goal "audit_proof.txt" > /dev/null

# Show Proof
echo -e "${TEAL}::: AUDIT TRAIL GENERATED :::${NC}"
../bin/holt hoard
echo -e "${GREEN}Proof of Work:${NC}"
ls -la audit_proof.txt

# Cleanup
../bin/holt down > /dev/null 2>&1
echo -e "${TEAL}Demo complete. Infrastructure torn down.${NC}"
