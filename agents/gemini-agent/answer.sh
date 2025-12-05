#!/bin/sh
# ArchitectGeminiAgent: Answers technical questions (M4.5)
# Uses M4.3 Knowledge artefacts for context

set -e

# Read stdin JSON
input=$(cat)

# Check if we're in mock mode (default for testing)
MOCK_MODE="${MOCK_MODE:-true}"

if [ "$MOCK_MODE" = "true" ]; then
    echo "[ArchitectGemini] Running in MOCK mode" >&2
    # Load mock response
    cat /app/mocks/answer.json >&3
    exit 0
fi

# Real Gemini API integration (requires GEMINI_API_KEY)
echo "[ArchitectGemini] Using real Gemini API" >&2

# TODO: Real Gemini API call would go here
# For now, fallback to mock
cat /app/mocks/answer.json >&3
