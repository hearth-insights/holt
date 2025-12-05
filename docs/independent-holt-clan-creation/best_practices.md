# 7. Best Practices

Designing effective autonomous agent systems requires a different mindset than traditional software engineering. This guide outlines key principles and patterns observed in successful Holt deployments.

## Core Philosophy: Small & Focused

**Agents should do the smallest possible task that the LLM is needed for.**

Avoid creating "God Agents" that try to do everything (plan, code, test, deploy). Instead, break down workflows into discrete steps handled by specialized agents.

*   **Why?** LLMs perform better with narrow context and specific instructions.
*   **Benefit**: Easier to debug, test, and swap out components.
*   **Safety**: Smaller steps are essential for future auditing.

## Context Management
Holt agents receive the full history of the workflow (`context_chain`). This allows them to "remember" previous attempts and fix mistakes. However, this can grow large.

*   **Context Window**: Ensure your LLM provider (e.g., Ollama, vLLM) is configured with a sufficient context window (recommend **8k** or **16k** minimum). The default 2k is often too small for complex rework loops.
*   **Trimming**: If the context is too large, your agent script should **trim** the `context_chain`. Prioritize the **Target Artefact** (the goal) and the **most recent** items (the latest feedback).
    *   See [examples/trim_context.py](./examples/trim_context.py) for a reference implementation.

## Auditability & Compliance
Holt is designed for regulated environments. Your agent design should leverage these built-in features:

### 1. The Merkle Ledger (Blackboard)
Holt's Blackboard is not just a database; it's a **cryptographically verifiable Merkle DAG**.
*   Every artefact is content-addressable (SHA-256 hash).
*   Every child record includes the hash of its parent.
*   **Implication**: You cannot alter history. Design agents to produce meaningful, granular artefacts that tell a clear story for auditors.

### 2. Configuration as Policy
Treat `holt.yml` as your primary compliance document.
*   It explicitly declares the "clan" of trusted agents.
*   It enforces **Least Privilege** via `workspace: ro`.
*   It is version-controlled, providing a history of your security posture.

## Patterns

### 1. The Doer/Reviewer Pattern
For critical tasks, pair a "Doer" agent with multiple specialized "Reviewer" agents.

*   **Doer**: Generates content (code, text, plans).
*   **Reviewer**: Validates the content against specific criteria.
    *   **Idempotent**: Reviewers should be idempotent (safe to run multiple times).
    *   **Gatekeeper**: The Orchestrator enforces a strict protocol:
        *   **Empty JSON (`{}` or `[]`)**: **Approval**. The workflow proceeds.
        *   **Non-Empty**: **Rejection**. The workflow loops back for rework.

**Example**:
*   `Coder` agent writes a function.
*   Multiple specialized reviewers check the work:
    *   `Code Reviewer`: Checks logic and design.
    *   `Linter`: Checks syntax and formatting.
    *   `Security Tester`: Scans for vulnerabilities.
    *   `Test Runner`: Executes unit tests.

### 2. Least Privilege Workspaces
Configure workspace access modes in `holt.yaml` based on the agent's role.

*   **Read-Write (`rw`)**: Only for agents that *must* modify files (e.g., Coder, Formatter).
*   **Read-Only (`ro`)**: For agents that only need to read context (e.g., Designer, Reviewer, Architect).

```yaml
agents:
  Coder:
    workspace: { mode: rw }
  Reviewer:
    workspace: { mode: ro }
```

### 3. Explicit Health Checks
Define health checks in `holt.yaml` to ensure agents are ready before the orchestrator assigns tasks.

```yaml
health_check:
  command: ["python", "check_dependencies.py"]
  interval: "30s"
```

## Agent Design Principles

### Single Responsibility
Each agent should have one clear role.
*   **Bad**: `DevOpsAgent` (does coding, testing, and deployment).
*   **Good**: `TerraformWriter`, `TfLint`, `Deployer`.

### Deterministic Tools
Where possible, wrap non-deterministic LLM calls with deterministic tools.
*   Use `gofmt` or `prettier` agents to fix formatting instead of asking an LLM to "fix the style".
*   Use static analysis tools (`lint`, `security-scan`) as reviewers.

### Clear Bidding Strategies
Use `bidding_strategy` to control the workflow flow and concurrency.

*   **`review`**:
    *   **Runs First**: All reviewers run before any other agents.
    *   **Parallel**: Multiple reviewers run in parallel.
    *   **Gate**: Any output from a reviewer cancels the claim and forces rework.
*   **`exclusive`**:
    *   **Concurrency Limit**: Only **one** claim is ever granted, even if multiple agents bid exclusively.
    *   **Use Case**: Enforcing limits on external systems (e.g., a shared filesystem or API) to prevent race conditions.
*   **`claim`**:
    *   **Standard Worker**: Runs after reviews pass.
    *   **Parallel**: Can have as many as needed running in parallel.
*   **`ignore`**:
    *   **Opt-out**: The agent explicitly does not want to work on this bid.

## Logging Best Practices (M4.10)

Since M4.10, Holt uses the **FD 3 Return** architecture for agent logging:

### The Three-Channel Model
*   **FD 0 (stdin)**: JSON input from pup
*   **FD 1 (stdout)**: Logs, tool output, debug prints (anything!)
*   **FD 2 (stderr)**: Errors, warnings, stack traces
*   **FD 3**: **Final JSON result ONLY** (`>&3`)

### When to Use Each Channel

**✅ Use stdout/stderr for**:
- Progress updates: `echo "Processing 50%..."`
- Tool output: `npm install` (full output visible)
- Debug messages: `echo "DEBUG: Variable x = $x"`
- Status updates: `echo "[INFO] Starting phase 2..."`

**✅ Use FD 3 for**:
- Final JSON result only: `cat result.json >&3`

**❌ Common Mistakes**:
- ❌ Writing logs to FD 3 (corrupts result)
- ❌ Writing result to stdout (pup won't see it)
- ❌ Silencing tools (`npm install > /dev/null`) - not needed anymore!

### Viewing Logs
```bash
holt logs <agent-role>          # View agent logs
holt logs -f <agent-role>       # Follow in real-time
holt logs --since=1h orchestrator  # Historical logs
```

### Example (M4.10)
```bash
#!/bin/sh
# Read input
input=$(cat)

# Be noisy! All this goes to docker logs
echo "Starting work..."
npm install  # Full output visible
echo "Work complete!"

# Write result to FD 3
cat <<EOF >&3
{
  "artefact_type": "Result",
  "artefact_payload": "data",
  "summary": "Done"
}
EOF
```

**See Also**: [Agent Logging Guide](../AGENT_LOGGING_GUIDE.md) for comprehensive examples.

## Summary
*   **Decompose** complex tasks.
*   **Specialize** your agents.
*   **Restrict** permissions.
*   **Verify** outputs with reviewers.
*   **Use FD 3** for results, stdout/stderr for logs (M4.10).
