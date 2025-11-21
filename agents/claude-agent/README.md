# ClaudeAgent

**Purpose:** Multi-role Claude-based agent for detailed specification and implementation (M4.5)

**Roles:** DetailedDesigner, Coder

## Overview

The ClaudeAgent provides two specialized scripts for different workflow roles:
1. **designer.sh** - Expands DesignSpecDraft into complete DesignSpec
2. **code.sh** - Implements code and tests based on DesignSpec

## Mock Mode (Default)

By default, all scripts run in **MOCK_MODE=true** which returns hardcoded responses for testing without requiring API keys.

## Scripts

### 1. designer.sh (ClaudeDesignAgent)

**Purpose:** Expand high-level design draft into complete 10-section specification

**Input:** DesignSpecDraft artefact
**Output:** DesignSpec artefact

**Example holt.yml:**
```yaml
agents:
  DetailedDesigner:
    image: holt/claude-agent:latest
    command: ["/app/designer.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: ro
    environment:
      - MOCK_MODE=true  # For testing without API key
```

### 2. code.sh (CoderClaudeAgent)

**Purpose:** Implement code and tests based on approved design

**Input:** DesignSpec artefact
**Output:** ChangeSet artefact (JSON with commit info)

**Example holt.yml:**
```yaml
agents:
  Coder:
    image: holt/claude-agent:latest
    command: ["/app/code.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: rw  # Needs write access for git commits
    environment:
      - MOCK_MODE=true
```

## Real API Integration

To use real Claude API instead of mocks:

```yaml
agents:
  Coder:
    image: holt/claude-agent:latest
    command: ["/app/code.sh"]
    bidding_strategy: "exclusive"
    workspace:
      mode: rw
    environment:
      - MOCK_MODE=false
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
```

## Building

```bash
docker build -t holt/claude-agent:latest -f agents/claude-agent/Dockerfile .
```

## Testing Locally

```bash
# Test code script with mock
echo '{"claim_type":"exclusive","target_artefact":{"type":"DesignSpec","payload":"Design details..."}}' | \
  docker run -i --rm -v $(pwd):/workspace -e MOCK_MODE=true holt/claude-agent:latest /app/code.sh
```

## ChangeSet Artefact Format

The code.sh script outputs a ChangeSet artefact with JSON payload:

```json
{
  "commit_hash": "abc123def456",
  "commit_message": "feat(M4.5): Implement feature",
  "files_changed": ["file1.go", "file2.go"],
  "test_summary": {
    "tests_added": 12,
    "tests_passed": 12,
    "coverage_percentage": 95.3
  },
  "change_summary": "Implemented X by modifying Y...",
  "notable_decisions": ["Used pattern A instead of B because..."]
}
```

## Mock Responses

Mock JSON files are located in `mocks/`:
- `design_spec.json` - Complete design specification
- `changeset.json` - Implementation with commit details

## Future: Real API Implementation

Real Claude API integration will use the `anthropic` Python package to:
1. Parse input context (DesignSpec, previous code)
2. Build prompts with system instructions
3. Call Claude API for code generation
4. Execute git commands to commit changes
5. Format response as ChangeSet artefact JSON
