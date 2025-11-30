#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

echo "Verifying version metadata..."

# Build all binaries
make build-all

# Get version output
HOLT_VERSION=$(./bin/holt --version)
PUP_VERSION=$(./bin/holt-pup --version)
ORCH_VERSION=$(docker run --rm holt-orchestrator:latest --version)

echo "Holt CLI: $HOLT_VERSION"
echo "Holt Pup: $PUP_VERSION"
echo "Holt Orch: $ORCH_VERSION"

# Extract timestamp (last field)
HOLT_TS=$(echo "$HOLT_VERSION" | awk '{print $NF}')
PUP_TS=$(echo "$PUP_VERSION" | awk '{print $NF}')
ORCH_TS=$(echo "$ORCH_VERSION" | awk '{print $NF}')

# Verify timestamps match
if [ "$HOLT_TS" == "$PUP_TS" ] && [ "$HOLT_TS" == "$ORCH_TS" ]; then
    echo -e "${GREEN}✓ All timestamps match: $HOLT_TS${NC}"
else
    echo -e "${RED}✗ Timestamp mismatch!${NC}"
    echo "Holt: $HOLT_TS"
    echo "Pup:  $PUP_TS"
    echo "Orch: $ORCH_TS"
    exit 1
fi

# Verify commit hash (second to last field, stripped of parens/commas)
# Format: ... (commit: <hash>, built: <date>)
HOLT_COMMIT=$(echo "$HOLT_VERSION" | awk '{print $(NF-2)}' | tr -d ',')
PUP_COMMIT=$(echo "$PUP_VERSION" | awk '{print $(NF-2)}' | tr -d ',')
ORCH_COMMIT=$(echo "$ORCH_VERSION" | awk '{print $(NF-2)}' | tr -d ',')

if [ "$HOLT_COMMIT" == "$PUP_COMMIT" ] && [ "$HOLT_COMMIT" == "$ORCH_COMMIT" ]; then
    echo -e "${GREEN}✓ All commits match: $HOLT_COMMIT${NC}"
else
    echo -e "${RED}✗ Commit mismatch!${NC}"
    echo "Holt: $HOLT_COMMIT"
    echo "Pup:  $PUP_COMMIT"
    echo "Orch: $ORCH_COMMIT"
    exit 1
fi

echo -e "${GREEN}✓ Version verification passed${NC}"
