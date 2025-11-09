# Holt Interactive Debugging Guide

**Purpose**: Comprehensive guide to using Holt's interactive debugger for workflow inspection, control, and manual intervention.

**Status**: M4.2 - Interactive Debugging & Control

---

## Table of Contents

1. [Introduction](#introduction)
2. [Quick Start](#quick-start)
3. [Breakpoint Patterns](#breakpoint-patterns)
4. [Interactive Commands](#interactive-commands)
5. [Common Debugging Workflows](#common-debugging-workflows)
6. [Manual Review Intervention](#manual-review-intervention)
7. [Troubleshooting](#troubleshooting)
8. [Best Practices](#best-practices)
9. [Safety & Security](#safety--security)

---

## Introduction

The Holt debugger provides traditional breakpoint-based debugging for AI agent workflows. Unlike log inspection or post-mortem analysis, the debugger allows you to:

- **Pause workflows** at specific conditions (artefact types, claim states, agent roles)
- **Inspect state** in real-time (artefacts, claims, context)
- **Single-step** through orchestrator events
- **Manually intervene** (approve/reject reviews, provide feedback)
- **Control execution** (continue, step, exit)

The debugger is designed to be **safe** and **ephemeral**:
- Only one active session per instance (no conflicting commands)
- Session heartbeat with 30-second TTL (auto-resume on crash)
- All manual interventions create audit trail artefacts
- Breakpoints cleared on disconnect

---

## Quick Start

### Basic Debug Session

The standard workflow is to start your instance, attach the debugger, set your breakpoints, and then start the workflow directly from the debugger.

1.  **Start Holt instance** (in one terminal):
    ```bash
    holt up
    ```

2.  **Attach the debugger** (in a second terminal):
    ```bash
    holt debug
    ```
    You are now in the interactive prompt.

3.  **Set breakpoints and start the workflow**:
    ```
    (holt-debug) break artefact.type=CodeCommit
    (holt-debug) continue
    (holt-debug) forage --goal "Create a new test file"
    ✓ GoalDefined artefact created: fedcba98...
    ```

The workflow will now begin, and the debugger will pause it as soon as it hits the `CodeCommit` breakpoint you set.

### Debug with Pre-Set Breakpoints

```bash
# Set breakpoints on startup
holt debug \
  -b artefact.type=CodeCommit \
  -b claim.status=pending_review

# Debugger will pause when:
# 1. Any CodeCommit artefact is created
# 2. Any claim enters pending_review status
```

### Target Specific Instance

```bash
# List running instances
holt list

# Attach to specific instance
holt debug --name production-workflow
```

---

## Breakpoint Patterns

Breakpoints use condition-based matching with glob pattern support.

### Artefact Type Matching

Pause when specific artefact types are created:

```bash
(holt-debug) break artefact.type=CodeCommit
# Matches exactly "CodeCommit"

(holt-debug) break artefact.type=*Spec
# Matches "DesignSpec", "APISpec", "TestSpec", etc.

(holt-debug) break artefact.type=Code*
# Matches "CodeCommit", "CodeReview", etc.
```

### Structural Type Matching

Pause on artefact structural types:

```bash
(holt-debug) break artefact.structural_type=Question
# Pause when agents ask questions

(holt-debug) break artefact.structural_type=Review
# Pause when review artefacts created

(holt-debug) break artefact.structural_type=Terminal
# Pause before workflow completes
```

### Claim Status Matching

Pause at specific claim phase transitions:

```bash
(holt-debug) break claim.status=pending_review
# Pause when claim enters review phase

(holt-debug) break claim.status=pending_exclusive
# Pause before exclusive agent grant

(holt-debug) break claim.status=complete
# Pause when claim completes
```

### Agent Role Matching

Pause when specific agents are granted claims:

```bash
(holt-debug) break agent.role=coder-agent
# Pause on grants to coder-agent

(holt-debug) break agent.role=coder-*
# Pause on grants to any agent with "coder-" prefix

(holt-debug) break agent.role=*-reviewer
# Pause on grants to any reviewer agent
```

### Event Type Matching

Pause on specific orchestrator internal events:

```bash
(holt-debug) break event.type=artefact_received
# Pause when orchestrator receives new artefact

(holt-debug) break event.type=review_consensus_reached
# Pause after all reviews collected (before decision)

(holt-debug) break event.type=claim_created
# Pause immediately after claim creation
```

**Common event types:**
- `artefact_received` - New artefact published to blackboard
- `claim_created` - Orchestrator created claim for artefact
- `bid_received` - Agent submitted bid
- `review_consensus_reached` - All reviews collected
- `claim_granted` - Claim granted to agent(s)
- `claim_completed` - Agent completed work

---

## Interactive Commands

Once attached, the debugger provides a rich command set.

### Execution Control

#### `continue` (alias: `c`)

Resume workflow execution until next breakpoint hit.

```bash
(holt-debug) continue
Continuing...

# Workflow runs normally until:
# - Breakpoint condition matches
# - You manually pause
```

#### `next` (alias: `n`)

Single-step: process exactly one orchestrator event, then pause again.

```bash
(holt-debug) next
Stepping to next event...

# Orchestrator processes one event
Stepped: artefact_received

(holt-debug) next
Stepping to next event...
Stepped: claim_created

(holt-debug) next
# ... continue stepping through events
```

**Use case**: Precise event-by-event inspection to understand orchestrator behavior.

#### `exit`

End debug session, clear all breakpoints, and resume workflow.

```bash
(holt-debug) exit
Exiting debug session...
Debug session ended. Breakpoints cleared.

# Session cleaned up automatically
# Orchestrator resumes normal operation
```

### Breakpoint Management

#### `break <condition>` (alias: `b`)

Set new breakpoint during active session.

```bash
(holt-debug) break artefact.type=*Test*
Breakpoint bp-2 set: artefact.type=*Test*

(holt-debug) break claim.status=pending_review
Breakpoint bp-3 set: claim.status=pending_review
```

**Pattern validation**: The debugger validates patterns before setting. Invalid patterns are rejected immediately:

```bash
(holt-debug) break invalid
Invalid breakpoint condition: invalid (expected format: condition_type=pattern)

(holt-debug) break unknown.field=test
Invalid condition type: unknown.field (valid: artefact.type, artefact.structural_type, claim.status, agent.role, event.type)
```

#### `breakpoints` (alias: `bp`)

List all active breakpoints.

```bash
(holt-debug) breakpoints

Active Breakpoints:
  bp-1: artefact.type=CodeCommit
  bp-2: artefact.type=*Test*
  bp-3: claim.status=pending_review
```

#### `clear <id>`

Remove specific breakpoint by ID.

```bash
(holt-debug) clear bp-2
Breakpoint bp-2 cleared

(holt-debug) breakpoints

Active Breakpoints:
  bp-1: artefact.type=CodeCommit
  bp-3: claim.status=pending_review
```

### Inspection Commands

#### `print [artefact-id]` (alias: `p`)

Inspect artefact details.

**Without ID** - Display artefact that triggered current breakpoint:

```bash
# Paused on breakpoint
🛑 Paused on breakpoint bp-1 (event: artefact_received)

(holt-debug) print

────────────────────────────────────────────────────────────
Artefact abc-123-def-456
────────────────────────────────────────────────────────────
  Type:             CodeCommit
  Structural Type:  Standard
  Produced By:      coder-agent
  Version:          1
  Payload:          git:a1b2c3d4e5f6789...
  Source Artefacts: [xyz-789-abc-123]
  Created:          1704123456789 ms
────────────────────────────────────────────────────────────
```

**With ID** - Display specific artefact by ID:

```bash
(holt-debug) print xyz-789-abc-123

────────────────────────────────────────────────────────────
Artefact xyz-789-abc-123
────────────────────────────────────────────────────────────
  Type:             DesignSpec
  Structural Type:  Standard
  Produced By:      architect-agent
  Version:          1
  Payload:          {...specification JSON...}
  Source Artefacts: []
  Created:          1704123450000 ms
────────────────────────────────────────────────────────────
```

**Use cases:**
- Inspect artefact that triggered pause
- Verify artefact payload contents
- Check source artefact chain (provenance)
- Validate artefact metadata (timestamps, roles)

#### `reviews`

List all claims currently in `pending_review` status.

```bash
(holt-debug) reviews

Pending Reviews:
  1. claim-abc123 (artefact: DesignSpec v1)
     Granted to: [architect-reviewer, security-reviewer]
     Waiting for: 2 reviews

  2. claim-def456 (artefact: CodeCommit v1)
     Granted to: [code-reviewer]
     Waiting for: 1 review
```

**Use case**: Understand current review state before manual intervention.

### Manual Intervention

#### `review <claim-id> [--approve | --reject "text"]`

Manually review a claim, bypassing agent review process.

**Requirements:**
- Must be paused on `claim.status=pending_review` breakpoint
- Claim ID must match currently paused claim
- Must provide exactly one of `--approve` or `--reject`

**Approve Example:**

```bash
# Paused at review breakpoint
🛑 Paused on breakpoint bp-1 (event: review_consensus_reached)

(holt-debug) reviews

Pending Reviews:
  1. claim-abc123 (artefact: DesignSpec v1)
     Granted to: [architect-reviewer]
     Waiting for: 1 review

(holt-debug) review claim-abc123 --approve
Manual review submitted for claim claim-abc123: approved

# Review artefact created with "user" role
# Claim transitions to next phase
```

**Reject Example:**

```bash
(holt-debug) review claim-abc123 --reject "Missing error handling for edge cases"
Manual review submitted for claim claim-abc123: rejected

# Review artefact created with rejection payload
# M3.3 feedback loop triggered
# Original agent receives rework claim with feedback
```

**Audit Trail**: Manual reviews create proper Review artefacts:

```json
{
  "id": "review-xyz-789",
  "structural_type": "Review",
  "type": "ManualReview",
  "payload": "{\"feedback\": \"Missing error handling\", \"review_method\": \"manual_debug\"}",
  "produced_by_role": "user",
  "source_artefacts": ["original-artefact-id"]
}
```

**Error Handling:**

```bash
# Not paused on review
(holt-debug) review claim-123 --approve
Error: Not paused on review claim

# Wrong claim ID
(holt-debug) review claim-999 --approve
Error: Claim claim-999 is not the currently paused claim (paused on: claim-123)

# Missing decision
(holt-debug) review claim-123
Error: Must provide exactly one of --approve or --reject
```

### Help Commands

#### `help` (alias: `h`, `?`)

Display command reference with examples.

```bash
(holt-debug) help

Holt Debugger Commands:

  Execution Control:
    continue (c)              Resume workflow execution until next breakpoint
    next (n)                  Single-step: process one event, then pause again
    exit                      End debug session and clear all breakpoints

  Breakpoints:
    break <condition> (b)     Set new breakpoint
    breakpoints (bp)          List all active breakpoints
    clear <breakpoint-id>     Clear specific breakpoint by ID

  Inspection:
    print [artefact-id] (p)   Inspect artefact (current or by ID)
    reviews                   List all claims in pending_review status

  Manual Intervention:
- `review <claim-id>`         Manually review claim
      --approve               Approve the claim
      --reject "reason"       Reject with feedback
    - `forage --goal "text"`    Start a new workflow with the given goal

  Help:
    help (h, ?)               Show this help message
```

---

## Common Debugging Workflows

### Workflow 1: Inspect Code Commits

Debug a workflow that produces code, pausing at each commit to verify content.

```bash
# Start debugger with breakpoint
holt debug -b artefact.type=CodeCommit

# When paused:
🛑 Paused on breakpoint bp-1 (event: artefact_received)

(holt-debug) print
# Verify commit hash in payload

(holt-debug) continue
# Resume until next commit
```

### Workflow 2: Step Through Event Sequence

Understand exactly how orchestrator processes a specific artefact.

```bash
# Set breakpoint on artefact creation
holt debug -b artefact.type=DesignSpec

# When paused:
🛑 Paused on breakpoint bp-1 (event: artefact_received)

(holt-debug) next
Stepped: claim_created

(holt-debug) next
Stepped: bid_received

(holt-debug) next
Stepped: bid_received

(holt-debug) next
Stepped: review_consensus_reached

(holt-debug) continue
# Resume normal execution
```

### Workflow 3: Manual Review Gate

Implement mandatory human approval for critical artefacts.

```bash
# Set breakpoint on review phase
holt debug -b claim.status=pending_review

# When paused:
🛑 Paused on breakpoint bp-1 (event: review_consensus_reached)

(holt-debug) print
# Inspect artefact requiring review

(holt-debug) reviews
# See all pending reviews

# Decision point:
# Option 1: Approve
(holt-debug) review claim-abc123 --approve

# Option 2: Reject with feedback
(holt-debug) review claim-abc123 --reject "Security concerns: API endpoints lack authentication"

(holt-debug) continue
# Workflow proceeds based on decision
```

### Workflow 4: Monitor Multi-Agent Coordination

Watch how multiple agents interact with same artefact.

```bash
# Set breakpoints on grants
holt debug \
  -b agent.role=reviewer-* \
  -b agent.role=coder-*

# When paused on reviewer grant:
🛑 Paused on breakpoint bp-1 (event: claim_granted)

(holt-debug) print
# Inspect artefact being reviewed

(holt-debug) continue

# When paused on coder grant:
🛑 Paused on breakpoint bp-2 (event: claim_granted)

(holt-debug) print
# Inspect same artefact after review

(holt-debug) continue
```

### Workflow 5: Catch Questions Before Escalation

Intercept agent questions to provide immediate answers.

```bash
# Set breakpoint on questions
holt debug -b artefact.structural_type=Question

# When paused:
🛑 Paused on breakpoint bp-1 (event: artefact_received)

(holt-debug) print
# See question details and target artefact

# In another terminal, provide answer:
holt answer abc-123 "Clarified requirements: Use JWT with RS256"

# In debugger:
(holt-debug) continue
# Workflow resumes with clarified version
```

### Workflow 6: Debug Review Consensus Logic

Understand how orchestrator evaluates multiple reviews.

```bash
# Set breakpoint before consensus decision
holt debug -b event.type=review_consensus_reached

# When paused:
🛑 Paused on breakpoint bp-1 (event: review_consensus_reached)

(holt-debug) print
# Inspect artefact being reviewed

# Can manually override review decision if needed
(holt-debug) reviews
(holt-debug) review claim-xyz --reject "Override: Requirements changed"

(holt-debug) continue
```

---

## Manual Review Intervention

Manual review is the most powerful debugging feature, allowing human operators to override agent review processes.

### When to Use Manual Review

1. **Compliance Checkpoints**: Regulated workflows requiring mandatory human approval
2. **Critical Decisions**: High-stakes artefacts (production deployments, security policies)
3. **Agent Review Failures**: When review agents provide unclear or conflicting feedback
4. **Emergency Override**: Urgent workflows requiring immediate human decision

### Manual Review Process

**Step 1: Set Review Breakpoint**

```bash
holt debug -b claim.status=pending_review
```

**Step 2: Wait for Pause**

```bash
🛑 Paused on breakpoint bp-1 (event: review_consensus_reached)
Type 'continue' to resume, 'print' to inspect, or 'help' for commands
```

**Step 3: Inspect Artefact**

```bash
(holt-debug) print

────────────────────────────────────────────────────────────
Artefact abc-123-def-456
────────────────────────────────────────────────────────────
  Type:             DeploymentSpec
  Structural Type:  Standard
  Produced By:      devops-agent
  Payload:          {...deployment configuration...}
────────────────────────────────────────────────────────────
```

**Step 4: Check Pending Reviews**

```bash
(holt-debug) reviews

Pending Reviews:
  1. claim-abc123 (artefact: DeploymentSpec v1)
     Granted to: [security-reviewer, ops-reviewer]
     Waiting for: 2 reviews
```

**Step 5: Make Decision**

```bash
# Approve if safe
(holt-debug) review claim-abc123 --approve

# OR reject with detailed feedback
(holt-debug) review claim-abc123 --reject "Deployment targets wrong environment. Update production config before proceeding."
```

**Step 6: Resume Workflow**

```bash
(holt-debug) continue
▶️  Resumed

# If approved: Workflow proceeds to next phase
# If rejected: M3.3 feedback loop triggers, original agent receives rework claim
```

### Manual Review Audit Trail

Every manual review creates immutable artefacts for full audit trail:

**Approval:**
```json
{
  "id": "review-manual-123",
  "structural_type": "Review",
  "type": "ManualReview",
  "payload": "{\"feedback\": \"\", \"review_method\": \"manual_debug\"}",
  "produced_by_role": "user",
  "source_artefacts": ["deployment-spec-abc"]
}
```

**Rejection:**
```json
{
  "id": "review-manual-456",
  "structural_type": "Review",
  "type": "ManualReview",
  "payload": "{\"feedback\": \"Deployment targets wrong environment...\", \"review_method\": \"manual_debug\"}",
  "produced_by_role": "user",
  "source_artefacts": ["deployment-spec-abc"]
}
```

These artefacts are queryable via `holt hoard` for compliance reporting.

---

## Troubleshooting

### Debug Session Won't Connect

**Problem:**
```bash
holt debug
Error: Debug session already active (started at 1704123456789 ms)
```

**Cause**: Another debugger is attached, or previous session didn't clean up.

**Solutions:**
1. **Check for active sessions**:
   ```bash
   holt list
   # Look for debug sessions in instance details
   ```

2. **Wait for TTL expiration** (30 seconds max)

3. **Manually clear stuck session**:
   ```bash
   redis-cli DEL holt:<instance>:debug:session
   ```

### Breakpoint Not Triggering

**Problem**: Set breakpoint but workflow never pauses.

**Causes & Solutions:**

1. **Pattern Mismatch**:
   ```bash
   # Wrong: Case-sensitive patterns
   (holt-debug) break artefact.type=codeCommit
   # Correct:
   (holt-debug) break artefact.type=CodeCommit
   ```

2. **Wrong Condition Type**:
   ```bash
   # Wrong: Event already passed
   (holt-debug) break event.type=artefact_received
   # If artefact already received, breakpoint won't trigger

   # Correct: Set breakpoint before triggering event
   holt debug -b event.type=artefact_received
   # Then create artefact
   ```

3. **Verify Breakpoint Active**:
   ```bash
   (holt-debug) breakpoints
   # Ensure breakpoint is in list
   ```

### Workflow Auto-Resumed

**Problem**: Workflow resumed on its own while paused.

**Cause**: Session key expired (heartbeat failed).

**Reasons:**
- Network connectivity issue to Redis
- Debugger crashed without cleanup
- TTL expired (30 seconds without heartbeat)

**Prevention:**
- Keep debugger terminal active
- Monitor heartbeat warnings
- Use stable network connection

**Recovery:**
- Restart debug session
- Set breakpoints again

### Manual Review Command Fails

**Problem:**
```bash
(holt-debug) review claim-123 --approve
Error: Not paused on review claim
```

**Causes:**

1. **Not Paused on Review Breakpoint**:
   ```bash
   # Must set review breakpoint first
   (holt-debug) break claim.status=pending_review
   ```

2. **Wrong Claim ID**:
   ```bash
   (holt-debug) reviews
   # Use actual claim ID from list
   ```

3. **Claim Not in pending_review**:
   ```bash
   # Claim may have already transitioned
   # Check current claim status via holt watch in another terminal
   ```

### go-prompt Errors

**Problem:**
```
Error: not a terminal
```

**Cause**: Trying to run `holt debug` in non-interactive environment (CI pipeline, background script).

**Solution**: `holt debug` requires interactive TTY. Use only in terminal sessions.

---

## Best Practices

### 1. Use Specific Breakpoints

**❌ Avoid:**
```bash
# Too broad - pauses on every event
(holt-debug) break event.type=*
```

**✅ Prefer:**
```bash
# Specific - pauses only when needed
(holt-debug) break artefact.type=CriticalDeployment
(holt-debug) break claim.status=pending_review
```

### 2. Exit Sessions Cleanly

**❌ Avoid:**
```bash
# Killing terminal without cleanup
Ctrl+C (forced kill)
# Session may linger until TTL expires
```

**✅ Prefer:**
```bash
# Clean exit
(holt-debug) exit
# Session cleaned up immediately
```

### 3. Monitor Orchestrator Logs

```bash
# In separate terminal
holt logs orchestrator

# Watch for debug events:
# - debug_session_started
# - paused_on_breakpoint
# - manual_review_submitted
# - debug_session_expired
```

### 4. Document Manual Interventions

When using manual review for compliance:

```bash
# Provide detailed rejection feedback
(holt-debug) review claim-123 --reject "COMPLIANCE: Missing HIPAA BAA requirement. Add business associate agreement clause to section 4.2 before proceeding."

# This creates auditable record
```

### 5. Test Breakpoints on Non-Production

```bash
# Test breakpoint patterns first
holt up --name test-debug
holt debug --name test-debug -b artefact.type=Test*

# Verify behavior before using on production
```

### 6. Use Multiple Terminals

```
Terminal 1: holt debug          # Interactive debugger
Terminal 2: holt watch          # Live event stream
Terminal 3: holt logs orchestrator  # Orchestrator logs
```

This provides comprehensive visibility during debugging.

### 7. Avoid Long Pauses

```bash
# Don't leave workflow paused indefinitely
# Workers may timeout
# Agents may become unresponsive

# If you need to pause for extended period:
(holt-debug) exit
# Restart debug session when ready
```

---

## Safety & Security

### Session Security Model

**Current Model**: Anyone with Redis access can debug.

**Implications:**
- Redis should be network-isolated (not exposed to public internet)
- Debug access = full workflow control
- Manual review can approve/reject any artefact

**Best Practices:**
1. Network-isolate Redis (firewall rules, VPN, private network)
2. Restrict Redis access to authorized operators only
3. Audit debug session logs regularly
4. Consider adding authentication layer in future (out of scope for M4.2)

### Data Exposure

Debug commands can inspect any artefact payload, including:
- Sensitive configuration data
- API keys (if accidentally included in artefacts)
- Personal information
- Business logic details

**Mitigation:**
- Don't log artefact payloads in orchestrator debug logs (only IDs)
- Train operators on handling sensitive data
- Use `holt hoard` to audit what data was inspected

### Denial of Service Risk

**Risk**: Malicious or accidental workflow stalling via debugging.

**Mitigations Implemented:**
- Session TTL with heartbeat (30-second max stall if heartbeat stops)
- Single session limit (prevents multiple operators from conflicting)
- Breakpoint limit (100 max per session)
- Auto-resume on session expiration

**Operational Best Practices:**
- Monitor orchestrator logs for unexpected debug sessions
- Alert on long-running paused states (>5 minutes)
- Document authorized debug users

### Audit Trail Integrity

**All manual interventions are logged:**

1. **Orchestrator Logs** (JSON events):
   ```json
   {
     "level": "warn",
     "component": "orchestrator",
     "event": "manual_review_submitted",
     "session_id": "abc-123",
     "claim_id": "claim-456",
     "decision": "reject",
     "rejection_text": "Needs error handling"
   }
   ```

2. **Review Artefacts** (immutable on blackboard):
   ```json
   {
     "id": "review-xyz",
     "structural_type": "Review",
     "produced_by_role": "user",
     "payload": "{\"feedback\": \"...\", \"review_method\": \"manual_debug\"}"
   }
   ```

3. **Session Logs** (start/stop):
   ```json
   {
     "event": "debug_session_started",
     "session_id": "abc-123",
     "breakpoint_count": 3,
     "timestamp_ms": 1704123456789
   }
   ```

**Query Audit Trail:**
```bash
# View all manual reviews
holt hoard --type ManualReview --output jsonl

# Watch for debug activity
holt watch | grep debug_session

# Orchestrator logs
holt logs orchestrator | grep manual_review
```

### Privilege Boundaries

Manual review as "user" role is a **privileged operation**:
- Bypasses agent logic
- Can approve work that should fail review
- Overrides consensus mechanisms

**Operator Responsibilities:**
1. Understand artefact content before approving
2. Provide detailed feedback on rejection
3. Document rationale for compliance workflows
4. Follow organizational approval policies

**Example Compliance Documentation:**
```bash
(holt-debug) review claim-123 --approve
# Add comment in separate audit log:
# "2024-01-01 10:00:00 UTC - Operator: jane.doe@example.com"
# "Reason: Emergency deployment approved by CTO via email"
# "Ticket: JIRA-1234"
```

---

## Next Steps

- **Learn More**: See [QUICK_REFERENCE.md](../QUICK_REFERENCE.md) for command syntax reference
- **Understand Architecture**: See [M4.2 Design Document](../design/features/phase-4-human-loop/M4.2-interactive-debugging-control.md)
- **Report Issues**: https://github.com/dyluth/holt/issues
- **Advanced Topics**: See [holt-system-specification.md](../design/holt-system-specification.md)

---

**Version**: M4.2 - Interactive Debugging & Control
**Last Updated**: November 2024
**Status**: Production Ready
