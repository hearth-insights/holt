# GeminiAgent

**Purpose:** Multi-role Gemini-based agent for design and architecture tasks (M4.5)

**Roles:** Designer, Architect, LeadEngineer

## Overview

The GeminiAgent provides three specialized scripts for different workflow roles:
1. **designer.sh** - Creates high-level design proposals (DesignSpecDraft)
2. **answer.sh** - Answers technical questions using project knowledge (Answer)
3. **review-code.sh** - Performs final code review and approval (Terminal/Review)

## Mock Mode (Default)

By default, all scripts run in **MOCK_MODE=true** which returns hardcoded responses for testing without requiring API keys.

## Scripts

### 1. designer.sh (DesignerGeminiAgent)

**Purpose:** Create high-level design proposals

**Output:** DesignSpecDraft artefact

**Example holt.yml:**
```yaml
agents:
  Designer:
    image: holt/gemini-agent:latest
    command: ["/app/designer.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: ro
    environment:
      - MOCK_MODE=true  # For testing without API key
```

### 2. answer.sh (ArchitectGeminiAgent)

**Purpose:** Answer technical questions about architecture

**Output:** Answer artefact

**Example holt.yml:**
```yaml
agents:
  Architect:
    image: holt/gemini-agent:latest
    command: ["/app/answer.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: ro
    environment:
      - MOCK_MODE=true
```

### 3. review-code.sh (LeadEngineerGeminiAgent)

**Purpose:** Final code review and approval

**Output:** Terminal (approval) or Review (rejection)

**Example holt.yml:**
```yaml
agents:
  LeadEngineer:
    image: holt/gemini-agent:latest
    command: ["/app/review-code.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: ro
    environment:
      - MOCK_MODE=true
```

## Real API Integration

To use real Gemini API instead of mocks:

```yaml
agents:
  Designer:
    image: holt/gemini-agent:latest
    command: ["/app/designer.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: ro
    environment:
      - MOCK_MODE=false
      - GEMINI_API_KEY=${GEMINI_API_KEY}
```

## Building

```bash
docker build -t holt/gemini-agent:latest -f agents/gemini-agent/Dockerfile .
```

## Testing Locally

```bash
# Test designer script with mock
echo '{"claim_type":"exclusive","target_artefact":{"type":"GoalDefined","payload":"Build a feature"}}' | \
  docker run -i --rm -e MOCK_MODE=true holt/gemini-agent:latest /app/designer.sh
```

## Mock Responses

Mock JSON files are located in `mocks/`:
- `design_spec_draft.json` - Design proposal output
- `answer.json` - Technical answer output
- `terminal_approval.json` - Final approval output

## Future: Real API Implementation

Real Gemini API integration will use the `google-generativeai` Python package to:
1. Parse input context
2. Build prompts with system instructions
3. Call Gemini API
4. Format response as Holt artefact JSON
