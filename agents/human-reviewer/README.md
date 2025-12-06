# HumanReviewerAgent

**Purpose:** Console-based human review for artefacts (M4.5)

**Type:** Review agent (requires human input via console)

## Overview

The HumanReviewerAgent is a Go binary that:
1. Receives artefacts for human review
2. Displays artefact content to the console
3. Prompts for approval/rejection via stdin
4. Creates Review artefacts based on human decision

## Contract

### Input (stdin JSON):
```json
{
  "claim_type": "review",
  "target_artefact": {
    "type": "DesignSpecDraft",
    "payload": "# Design Proposal...",
    "version": 1
  },
  "context_chain": [...]
}
```

### Output (FD 3 JSON):

**Approval:**
```json
{
  "structural_type": "Review",
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "Approved by human reviewer"
}
```

**Rejection:**
```json
{
  "structural_type": "Review",
  "artefact_type": "Review",
  "artefact_payload": "{\"feedback\": \"Needs more detail in section 3\"}",
  "summary": "Rejected by human reviewer"
}
```

## Configuration

**holt.yml example:**
```yaml
agents:
  HumanReviewer:
    image: holt/human-reviewer:latest
    command: ["/app/human-reviewer"]
    bidding_strategy: "review"
    workspace:
      mode: ro
    environment:
      - REVIEW_TIMEOUT=300         # 5 minutes (default)
      - AUTO_APPROVE=false         # Set to true for testing
```

## Environment Variables

- **`AUTO_APPROVE`**: If set to `true`, automatically approves all reviews (for testing)
- **`REVIEW_TIMEOUT`**: Timeout in seconds before review fails (default: 300 = 5 minutes)

## User Interaction

When a review is requested, the agent displays:

```
================================================================================
Artefact ID: abc-123-def-456
Type: DesignSpecDraft (version 1)
Produced by: Designer
--------------------------------------------------------------------------------
Payload:
# Design Proposal: Feature X
...
================================================================================

Review this DesignSpecDraft (v1). Approve? (y/n/comment): _
```

### Response Options:

1. **Approve:** Type `y` or `yes`
2. **Reject:** Type `n` or `no`, then provide feedback when prompted
3. **Reject with comment:** Type `comment <your feedback here>`

### Examples:

```bash
# Approve
y

# Reject (will prompt for feedback)
n
Rejection reason: Missing error handling in section 3

# Reject with inline comment
comment Needs more detail on authentication flow
```

## Signal Handling

- **SIGINT/SIGTERM:** Agent exits immediately (claim will be terminated by orchestrator)
- **Timeout:** After `REVIEW_TIMEOUT` seconds, agent creates Failure artefact and exits

## Testing Locally

### With AUTO_APPROVE (automated testing):
```bash
echo '{
  "claim_type": "review",
  "target_artefact": {
    "type": "DesignSpecDraft",
    "payload": "# Test Design",
    "version": 1
  }
}' | docker run -i --rm -e AUTO_APPROVE=true holt/human-reviewer:latest /app/human-reviewer
```

### Interactive mode:
```bash
echo '{
  "claim_type": "review",
  "target_artefact": {
    "type": "DesignSpecDraft",
    "payload": "# Test Design",
    "version": 1,
    "produced_by_role": "Designer"
  }
}' | docker run -i --rm holt/human-reviewer:latest /app/human-reviewer
```

## Building

```bash
docker build -t holt/human-reviewer:latest -f agents/human-reviewer/Dockerfile .
```

## Error Handling

- **Invalid input:** Treats unknown responses as rejection
- **Timeout:** Creates Failure artefact after timeout period
- **Signal interrupt:** Exits immediately (orchestrator handles cleanup)
- **Empty feedback:** Rejection is still created with default message

## Design Notes

- **Stateless:** Each review is independent
- **No pause/resume:** Uses M3.3 feedback loop if rejection occurs
- **Single-session:** No support for extending timeout mid-review
- **Console-only:** No web UI (future enhancement in Phase 5+)
