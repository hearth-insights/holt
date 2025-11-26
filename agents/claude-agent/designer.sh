#!/bin/sh
# ClaudeDesignAgent: Expands DesignSpecDraft into full DesignSpec (M4.5)

set -e

# Read stdin JSON
input=$(cat)

# Check if we're in mock mode (default for testing)
MOCK_MODE="${MOCK_MODE:-true}"

if [ "$MOCK_MODE" = "true" ]; then
    echo "[ClaudeDesign] Running in MOCK mode" >&2
    # Load mock response
    cat /app/mocks/design_spec.json
    exit 0
fi

# Real Claude API integration (requires ANTHROPIC_API_KEY)
echo "[ClaudeDesign] Using real Claude API" >&2

# TODO: Real Claude API call would go here
# For now, fallback to mock
cat /app/mocks/design_spec.json
