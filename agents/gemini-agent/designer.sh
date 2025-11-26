#!/bin/sh
# DesignerGeminiAgent: Creates high-level design drafts (M4.5)
# Uses M4.1 Q&A to ask clarifying questions before drafting

set -e

# Read stdin JSON
input=$(cat)

# Check if we're in mock mode (default for testing)
MOCK_MODE="${MOCK_MODE:-true}"

if [ "$MOCK_MODE" = "true" ]; then
    echo "[DesignerGemini] Running in MOCK mode" >&2
    # Load mock response
    cat /app/mocks/design_spec_draft.json
    exit 0
fi

# Real Gemini API integration (requires GEMINI_API_KEY)
echo "[DesignerGemini] Using real Gemini API" >&2

# TODO: Real Gemini API call would go here
# For now, fallback to mock
cat /app/mocks/design_spec_draft.json
