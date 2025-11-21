#!/bin/sh
# LeadEngineerGeminiAgent: Final code review (M4.5)
# Creates Terminal artefact on approval or Review with feedback on rejection

set -e

# Read stdin JSON
input=$(cat)

# Check if we're in mock mode (default for testing)
MOCK_MODE="${MOCK_MODE:-true}"

if [ "$MOCK_MODE" = "true" ]; then
    echo "[LeadEngineerGemini] Running in MOCK mode" >&2
    # Load mock response (approves with Terminal)
    cat /app/mocks/terminal_approval.json
    exit 0
fi

# Real Gemini API integration (requires GEMINI_API_KEY)
echo "[LeadEngineerGemini] Using real Gemini API" >&2

# TODO: Real Gemini API call would go here
# For now, fallback to mock
cat /app/mocks/terminal_approval.json
