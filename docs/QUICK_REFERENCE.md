# **Holt Quick Reference: Key Concepts & Patterns**

**Purpose**: Essential patterns, structures, and workflows for rapid development  
**Scope**: Reference - quick lookup for common development patterns  
**Read when**: Need quick reference during implementation, lookup patterns

## **Core Data Structures**

### **Artefact Versions**

Holt supports two artefact formats:

#### **V1 Artefact (UUID-based, legacy)**
```
id: UUID (36 characters)
logical_id: UUID (groups versions)
version: int
structural_type: Standard|Review|Question|Answer|Failure|Terminal|Knowledge
type: user-defined string (e.g., "CodeCommit", "DesignSpec")
payload: string (git hash, JSON, text)
source_artefacts: JSON array of UUIDs
produced_by_role: string (agent key from holt.yml, which IS the role, or 'user')
created_at_ms: int64 (Unix milliseconds) # M3.9
context_for_roles: JSON array of role globs # M4.3: For Knowledge artefacts
```

#### **V2 VerifiableArtefact (Hash-based, M4.6+)**

Content-addressable artefacts identified by SHA-256 hash:

```json
{
  "id": "a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1",
  "header": {
    "parent_hashes": ["b8c4e6d1f7a3..."],  // Array of parent SHA-256 hashes
    "logical_thread_id": "550e8400-e29b-41d4-a716-446655440000",  // Groups versions
    "version": 2,
    "created_at_ms": 1704067200000,  // CRITICAL: Part of hash computation
    "produced_by_role": "go-coder-agent",
    "structural_type": "Standard",
    "type": "CodeCommit",
    "context_for_roles": []  // Optional, omitted if empty
  },
  "payload": {
    "content": "e3b0c442..."  // Max 1MB (1,048,576 bytes)
  }
}
```

**Key Differences**:
- **ID Format**: 64-char hex hash (SHA-256) vs 36-char UUID
- **Parent References**: `parent_hashes` (array of hashes) vs `source_artefacts` (array of UUIDs)
- **Hash Computation**: ID = SHA-256(RFC 8785 canonical JSON of header + payload)
- **Payload Limit**: Hard 1MB limit for V2 (unlimited for V1)
- **Integrity**: V2 provides cryptographic tamper-evidence

### **Claim (Redis Hash)**
```
id: UUID
artefact_id: UUID
status: pending_review|pending_parallel|pending_exclusive|pending_assignment|complete|terminated
granted_review_agents: JSON array
granted_parallel_agents: JSON array  
granted_exclusive_agent: string
granted_agent_image_id: string # M3.9: sha256 digest of the agent image
additional_context_ids: JSON array # M3.3: For feedback loops
termination_reason: string # M3.3: Reason for termination
```

### **Bid (On Claim)**
A Redis Hash (`holt:{instance_name}:claim:{uuid}:bids`) where each key-value pair is:
- **Key**: Agent's role (e.g., 'Coder', 'Reviewer')
- **Value**: Bid type (`review`, `claim`, `exclusive`, `ignore`)

## **Redis Key Patterns**

```
# Global keys
holt:instance_counter                          # Atomic counter for instance naming
holt:instances                                 # HASH of active instance metadata

# Instance-specific keys - Artefacts
holt:{instance_name}:artefact:{uuid}           # V1 Artefact data (UUID-based)
holt:{instance_name}:artefact:{hash}           # V2 Artefact data (SHA-256 hash-based, M4.6)

# Instance-specific keys - Claims & Bids
holt:{instance_name}:claim:{uuid}              # Claim data
holt:{instance_name}:claim:{uuid}:bids         # Bid data

# Instance-specific keys - Versioning & Management
holt:{instance_name}:thread:{logical_id}       # Version tracking (ZSET)
holt:{instance_name}:lock                      # Instance lock (TTL-based, heartbeat)
holt:{instance_name}:agent_images              # HASH of role -> image_id mapping (M3.9)
holt:{instance_name}:grant_queue:{role}        # ZSET for paused grants (M3.5)

# Instance-specific keys - Security (M4.6)
holt:{instance_name}:security:alerts:log       # Security alert audit trail (LIST, LPUSH newest first)
holt:{instance_name}:security:lockdown         # Global lockdown circuit breaker (STRING)
```

**V2 Artefact ID Format**:
- V1: 36 characters (e.g., `550e8400-e29b-41d4-a716-446655440000`)
- V2: 64 characters (e.g., `a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1`)

## **Pub/Sub Channels**

```
holt:{instance_name}:artefact_events     # Orchestrator watches for new artefacts
holt:{instance_name}:claim_events        # Agents watch for new claims
holt:{instance_name}:workflow_events     # Bids and grants for real-time watch (M2.6)
holt:{instance_name}:agent:{role}:events # Agent-specific grant notifications (M2.2)
holt:{instance_name}:security:alerts     # Real-time security alerts (M4.6)
```

**Security Alerts Channel (M4.6)**:
- Publishes: `hash_mismatch`, `orphan_block`, `timestamp_drift`, `security_override` events
- Used by: `holt security --alerts --watch` for real-time monitoring
- Ephemeral: Not stored (use `security:alerts:log` LIST for persistence)

## **Claim Lifecycle**

```
pending_review → pending_parallel → pending_exclusive → complete
             ↘ terminated (if review feedback or failure)
```

## **Agent Pup Operational Modes**
*(See `design/agent-pup.md` for details)*

### **Standard Mode**
- Both Claim Watcher and Work Executor active.

### **Controller Mode (`mode: controller`)**
- Only Claim Watcher active. Bids on behalf of its role.

### **Worker Mode (`pup --execute-claim <id>`)**
- Only Work Executor active. Executes a single assigned claim and exits.

## **Tool Execution Contract**

### **Input (stdin JSON)**
```json
{
  "claim_type": "review|claim|exclusive",
  "target_artefact": { /* full artefact object */ },
  "context_chain": [ /* array of historical artefact objects */ ]
}
```

### **Output (stdout JSON)**
```json
{
  "artefact_type": "string",
  "artefact_payload": "string",
  "summary": "string",
  "structural_type": "Standard|Review|Question|Answer|Failure|Terminal" // Optional, defaults to "Standard"
}
```

## **Special Artefact Payloads (M4.1+)**

### **Question Artefact Payload**
When an agent produces a Question artefact (`structural_type: "Question"`), the `artefact_payload` field must contain a JSON-encoded string with this schema:

```json
{
  "question_text": "Is null handling in scope for this API?",
  "target_artefact_id": "abc-123-def-456" // UUID of the artefact being questioned
}
```

Example agent output producing a Question:
```json
{
  "structural_type": "Question",
  "artefact_type": "ClarificationNeeded",
  "artefact_payload": "{\"question_text\": \"Should we use REST or GraphQL?\", \"target_artefact_id\": \"xyz-789\"}",
  "summary": "Agent needs clarification on API architecture"
}
```

**Question Flow**:
1. Agent produces Question artefact referencing a target artefact
2. Orchestrator terminates the questioning agent's claim
3. Orchestrator creates `pending_assignment` claim for the original author of the target artefact
4. Original author receives the Question in `additional_context_ids` and produces a new version
5. New version increments `version` and includes Question ID in `source_artefacts`
6. Orchestrator creates new claim for the clarified artefact

**Iteration Limits**: Questions reuse `orchestrator.max_review_iterations` from `holt.yml`. If an artefact is questioned beyond this limit, the orchestrator creates a Failure artefact and terminates the workflow.

## **Common CLI Commands**

### **Global Flags**
```bash
--config, -f <path>   # Path to holt.yml
--debug, -d           # Enable verbose debug output
--quiet, -q           # Suppress all non-essential output
```

### **Instance & Workflow**
```bash
holt init                                # Bootstrap new project
holt up [--name <instance>] [--force]    # Start holt instance
holt down [--name <instance>]            # Stop holt instance
holt list                                # List active instances
holt version                             # Show version, commit, and build time
holt forage --goal "description"         # Start a new workflow
```

### **Observability & Debugging**
*Note: All commands support short IDs (e.g., `abc123de`)*

**`holt watch [--since <duration>] [--type <glob>] [--agent <role>] [--output jsonl]`**

The primary tool for observing a Holt instance. It has two modes:

*   **Live Mode (default):** Streams all events on the Blackboard in real-time.
*   **Historical Replay Mode (`--since`):** Use a duration (e.g., `1h`, `30m`) to get a complete, chronological replay of a past workflow. This replay reconstructs the entire sequence of events, including:
    *   Artefacts (with original creation timestamps)
    *   Claims (including terminated claims)
    *   Bids, grants, and review results
    *   Rework assignments from feedback loops

**`holt hoard [--since <duration>] [--type <glob>] [--agent <role>] [--output jsonl]`**

Inspects historical artefacts. Use the filtering flags to find specific artefacts created in the past. To see the full history of a workflow, use `holt watch --since`.

**`holt hoard <artefact-id>`**

Retrieves and displays the full details for a single artefact.

**`holt logs <agent-role|orchestrator>`**

Views the logs for a specific running or stopped container (e.g., `holt logs Coder`).

### **Cryptographic Verification & Security (M4.6+)**

**`holt verify <artefact-id>`**

Independently verify a V2 artefact's cryptographic hash. Recomputes SHA-256 hash using RFC 8785 canonicalization and compares with stored ID.

Supports short hash IDs (minimum 8 characters if unique).

Examples:
```bash
# Verify with full hash
holt verify a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1

# Verify with short hash (resolves to full hash)
holt verify a3f2b9c4

# Verify in specific instance
holt verify --name prod a3f2b9c4
```

**`holt security --alerts [flags]`**

Monitor security events from permanent audit log and/or real-time stream.

Flags:
- `--since <duration>` - Show historical alerts (e.g., `1h`, `24h`)
- `--watch` - Stream live alerts in real-time
- Both flags: Historical replay then live stream

Alert Types:
- `hash_mismatch` - Tampering detected (triggers global lockdown)
- `orphan_block` - DAG corruption (triggers global lockdown)
- `timestamp_drift` - Clock skew >5min (rejected, no lockdown)
- `security_override` - Lockdown cleared by operator

Examples:
```bash
# View all historical alerts
holt security --alerts

# View alerts from last hour
holt security --alerts --since=1h

# Stream live alerts
holt security --alerts --watch

# Historical + live
holt security --alerts --since=24h --watch
```

**`holt security --unlock --reason "<text>"`**

Clear global lockdown after forensic investigation. Creates audited `security_override` event.

**Required**: Explicit reason for compliance audit trail.

Examples:
```bash
# Clear lockdown with investigation summary
holt security --unlock --reason "Investigation complete: memory corruption detected, container replaced"

# Unlock after false positive
holt security --unlock --reason "Alert was triggered by test data, no actual tampering"
```

**Global Lockdown Behavior**:
- Triggered by: `hash_mismatch` or `orphan_block` alerts
- Effect: All claim creation and grant operations halted
- Containers: Remain running for forensic investigation
- Recovery: Manual unlock required with `--reason`

### **Human-in-the-Loop (M4.1+)**

**`holt questions [flags]`**

Display unanswered Question artefacts from agents. Questions are a form of "late review feedback" that trigger the M3.3 automated feedback loop.

Flags:
- `--watch` - Continuously stream Questions as they appear
- `--exit-on-complete` - Exit when Terminal artefact is created (used with --watch)
- `--since <duration>` - Show all unanswered Questions from time range (e.g., `1h`, `30m`)
- `--output jsonl` - Output as line-delimited JSON for scripting

Examples:
```bash
# Show oldest unanswered question or wait for new one (default)
holt questions

# Watch for questions continuously
holt questions --watch

# List all unanswered questions from last hour
holt questions --since 1h

# Stream questions as JSONL until workflow completes
holt questions --watch --exit-on-complete --output jsonl
```

**`holt answer <question-id> "clarified-text" [flags]`**

Respond to a Question by creating a new version of the questioned artefact with clarified content. The new version automatically links to both the original artefact and the Question artefact.

Flags:
- `--then-questions` - After answering, immediately run `holt questions` (default behavior)

Examples:
```bash
# Basic usage (supports ID prefix matching, minimum 6 chars if ambiguous)
holt answer abc-123 "Build REST API with JWT auth (not OAuth)"

# Multi-line answer (quotes preserve newlines)
holt answer def-456 "Requirements:
1. Support null values
2. Return 400 for invalid input
3. Document edge cases"

# Answer and watch for next question (workflow chaining)
holt answer abc-123 "Clarified requirements here" --then-questions
```

### **Interactive Debugging & Control (M4.2+)**

**`holt debug [flags]`**

Interactive debugger with breakpoint-based control. For comprehensive workflows and examples, see **[docs/DEBUGGING_GUIDE.md](./docs/DEBUGGING_GUIDE.md)**.

**Flags:**
- `--name <instance>` - Target instance (auto-inferred if omitted)
- `--break <condition>` (alias `-b`) - Set breakpoint on startup (repeatable)
- `--pause-on-start` - Pause orchestrator immediately

**Breakpoint Conditions:**
- `artefact.type=<glob>` - Match artefact type (e.g., `Code*`, `*Spec`)
- `artefact.structural_type=<type>` - Match structural type (`Question`, `Review`, `Terminal`)
- `claim.status=<status>` - Match claim status (`pending_review`, `pending_exclusive`)
- `agent.role=<glob>` - Match agent role on grant (e.g., `coder-*`)
- `event.type=<event>` - Match orchestrator event type

**Interactive Commands:**
- `continue` (alias: `c`) - Resume workflow execution
- `next` (alias: `n`) - Single-step one event
- `break <condition>` (alias: `b`) - Set new breakpoint
- `breakpoints` (alias: `bp`) - List active breakpoints
- `clear <id>` - Clear breakpoint by ID
- `print [id]` (alias: `p`) - Inspect artefact/claim (current or by ID)
- `reviews` - List pending reviews
- `review <claim-id> [--approve | --reject "text"]` - Manual review
- `terminate` (alias: `kill`) - Terminate current claim (permanent)
- `forage --goal "text"` - Start new workflow
- `help` (alias: `h`, `?`) - Show command reference
- `exit` - End debug session

## **Redis Debug Protocol (M4.2+)**

The debug system uses Redis Pub/Sub for CLI ↔ Orchestrator communication:

**Pub/Sub Channels:**
```
holt:{instance}:debug:command    # CLI → Orchestrator commands
holt:{instance}:debug:event      # Orchestrator → CLI events
```

**Redis Keys:**
```
holt:{instance}:debug:session          # Active session metadata (Hash, TTL: 30s)
holt:{instance}:debug:breakpoints      # Breakpoint list (List)
holt:{instance}:debug:pause_context    # Context when paused (Hash)
```

**Session Fields:**
- `session_id` - UUID of active session
- `connected_at_ms` - Timestamp when session connected
- `last_heartbeat_ms` - Timestamp of last heartbeat (refreshed every 5s)
- `is_paused` - Boolean (true if orchestrator currently paused)

**Command Types:**
- `set_breakpoints` - Add new breakpoints
- `clear_breakpoint` - Remove specific breakpoint
- `clear_all` - Remove all breakpoints
- `continue` - Resume from pause
- `step_next` - Single-step one event
- `inspect_artefact` - Request artefact details
- `manual_review` - Submit manual review decision

**Event Types:**
- `session_active` - Session successfully created
- `paused_on_breakpoint` - Orchestrator paused (includes pause context)
- `resumed` - Orchestrator resumed execution
- `breakpoint_set` - Breakpoint successfully added
- `breakpoint_cleared` - Breakpoint removed
- `step_complete` - Single step executed
- `session_expired` - Session TTL expired (auto-cleanup)
- `review_complete` - Manual review processed

## **Health Check Endpoints**

### **Default (`/healthz`)**
```
GET /healthz
200 OK           # Connected to Redis
503 Unavailable  # Redis connection failed
```

### **Configurable (M3.9+)**
Agents can define a custom `health_check` command in `holt.yml`. The `/healthz` endpoint will reflect the success or failure of that custom command.
