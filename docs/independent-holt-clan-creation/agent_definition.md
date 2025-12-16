# 3. Agent Definition

To run agents in Holt, you need two things:
1.  A definition in `holt.yaml`.
2.  A Dockerfile for the agent image.

## Do's and Don'ts
*   **DO** keep agents small and focused.
*   **DON'T** implement internal retry loops. If your agent fails or produces bad output, just output the error or the bad result. The **Reviewer** agent will reject it, and the Orchestrator will loop it back to you. This "fail fast" approach is critical for the system to work.
*   **DO** use the `holt.yaml` to define clear roles.

## Review Protocol (Strict)
The Orchestrator enforces a strict rule for Reviewer agents:
*   **Approval**: The review payload **MUST** be an empty JSON object `{}` or an empty JSON array `[]`.
*   **Rejection/Feedback**: **ANY** other content (e.g., `{"issue": "..."}`, `{"status": "ok"}`, or even `true`) is treated as **negative feedback** and will trigger a rework loop.
*   **Implication**: You must explicitly program your Reviewer agents to return strictly empty JSON when they intend to approve.

## 1. holt.yaml
This file resides in the root of your project (or the directory where you run `holt`). It defines the agents and services.

### Example `holt.yaml`
```yaml
version: "1.0"
agents:
  git-agent:
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["GoalDefined"]
    workspace:
      mode: rw

  # M5.1: Synchronizer agent example (waits for multiple prerequisites)
  deployer:
    image: "example-deployer:latest"
    command: ["/app/deploy.sh"]
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"
        - type: "LintResult"
        - type: "SecurityScan"
    workspace:
      mode: ro

services:
  redis:
    image: redis:7-alpine
```

*   **agents**: A map of agent names (roles) to their configuration.
    *   **key**: The agent's role (e.g., `git-agent`). The Orchestrator uses this identifier to route tasks and claims.
    *   **image**: The Docker image to use.
    *   **command**: The command to run inside the container.
    *   **workspace**: Configuration for the shared workspace. `mode: rw` means read-write access.
    *   **synchronize** (M5.1+): Optional synchronizer configuration for fan-in coordination. Replaces `bidding_strategy` and `bid_script`. See [Synchronizer Agents](#4-synchronizer-agents-m51) below.
*   **services**: A map of service names (like Redis) to their configuration.

### Health Checks
By default, Holt performs a **Redis PING** check to verify that the agent can connect to the Blackboard. If the agent can talk to Redis, it is considered healthy.

You can override this with a custom `health_check` in `holt.yaml`. This is useful if your agent has other dependencies or needs to warm up.

```yaml
agents:
  my-agent:
    # ... other config ...
    health_check:
      command: ["python", "check_health.py"]
      interval: "30s"
      timeout: "5s"
```

*   **command**: The command to run inside the container. Exit code 0 means healthy.
*   **interval**: How often to run the check (default: 30s).
*   **timeout**: How long to wait for the command to finish (default: 5s).

## 2. Dockerfile
Each agent needs a Dockerfile. The key requirement is that it must include the `pup` binary.

### Example Dockerfile
This example assumes you have downloaded the `holt-pup` binary (see [Build & Run](./build_and_run.md#option-b-manual-download)) and placed it in the same directory as your Dockerfile.

```dockerfile
FROM alpine:latest

# Install dependencies
RUN apk --no-cache add ca-certificates git

WORKDIR /app

# Copy the pre-built pup binary
# Ensure 'holt-pup' is in your build context (same directory as Dockerfile)
COPY holt-pup /usr/local/bin/pup
RUN chmod +x /usr/local/bin/pup

# Copy your agent script
COPY run.sh /app/run.sh
RUN chmod +x /app/run.sh

# Create a non-root user
RUN adduser -D -u 1000 agent
USER agent

# Set pup as the entrypoint
ENTRYPOINT ["pup"]
```

## 4. Synchronizer Agents (M5.1+)

**New in Phase 5:** Synchronizer agents use declarative fan-in coordination to wait for multiple parallel workflow branches before executing.

### When to Use Synchronizers

Use synchronizers when you need to:
- **Merge parallel branches**: Deploy only after tests + linting + security scans complete
- **Aggregate dynamic results**: Collect N sharded outputs (N unknown at design time)
- **Coordinate without race conditions**: Atomic coordination with deduplication locks

### Configuration

⚠️ **CRITICAL: Mutual Exclusivity**

The `synchronize` block is **MUTUALLY EXCLUSIVE** with `bidding_strategy` and `bid_script`.

**You cannot use both.** Choose one:
- **Standard agent**: Use `bidding_strategy` OR `bid_script`
- **Synchronizer agent**: Use `synchronize` block

**Example configuration:**

```yaml
agents:
  deployer:
    image: "deployer:latest"
    command: ["/app/deploy.sh"]

    # Synchronize block (replaces bidding_strategy)
    synchronize:
      # Required: Common ancestor artefact type
      ancestor_type: "CodeCommit"

      # Required: Conditions to wait for
      wait_for:
        - type: "TestResult"      # Named pattern: exactly 1 required
        - type: "LintResult"      # Named pattern: exactly 1 required
        - type: "SecurityScan"    # Named pattern: exactly 1 required

      # Optional: Limit descendant search depth (0 = unlimited)
      max_depth: 10

    workspace:
      mode: ro
```

### Synchronization Patterns

**Named Pattern** - Wait for specific, known types:
```yaml
wait_for:
  - type: "TestResult"
  - type: "LintResult"
  - type: "SecurityScan"
```

**Producer-Declared Pattern** - Wait for N artefacts (N from metadata):
> **Tip**: See [Agent Interface](./agent_interface.md#multiple-artefacts-fan-out) for how to produce multiple artefacts (Fan-Out) from a single agent.

```yaml
wait_for:
  - type: "ProcessedRecord"
    count_from_metadata: "batch_size"
```

### Input Format

Synchronizers receive additional fields in stdin:

```json
{
  "claim_type": "exclusive",
  "target_artefact": { /* the final trigger */ },
  "context_chain": [ /* historical context */ ],

  "ancestor_artefact": { /* the common ancestor */ },
  "descendant_artefacts": [ /* ALL matched descendants */ ]
}
```

### Example: CI/CD Deployer

**holt.yaml:**
```yaml
agents:
  deployer:
    image: "deployer:latest"
    command: ["/app/deploy.sh"]
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"
        - type: "LintResult"
        - type: "SecurityScan"
```

**deploy.sh:**
```bash
#!/bin/sh
set -e

input=$(cat)

# Extract ancestor (CodeCommit)
commit_hash=$(echo "$input" | jq -r '.ancestor_artefact.payload')

# Extract descendants (prerequisites)
descendants=$(echo "$input" | jq -r '.descendant_artefacts')

test_result=$(echo "$descendants" | jq -r '.[] | select(.type=="TestResult") | .payload')
lint_result=$(echo "$descendants" | jq -r '.[] | select(.type=="LintResult") | .payload')
scan_result=$(echo "$descendants" | jq -r '.[] | select(.type=="SecurityScan") | .payload')

echo "Deploying commit: $commit_hash" >&2
echo "  Tests: $test_result" >&2
echo "  Lint: $lint_result" >&2
echo "  Security: $scan_result" >&2

# Verify all passed
if echo "$test_result $lint_result $scan_result" | grep -q "failed"; then
  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Prerequisites failed",
  "summary": "Deployment aborted"
}
EOF
  exit 0
fi

# Output success
deployment_id="deploy-$(date +%s)"
cat <<EOF >&3
{
  "artefact_type": "DeploymentComplete",
  "artefact_payload": "$deployment_id",
  "summary": "Deployed commit $commit_hash"
}
EOF
```

### Learn More

For comprehensive documentation on fan-in synchronization:
- **Full Guide**: See the main Holt repository's `docs/guides/fan-in-synchronization.md`
- **Example Agents**: `agents/example-deployer-agent/` and `agents/example-batch-aggregator-agent/`
- **Design Document**: `design/features/phase-5-complex-coordination/M5.1-fan-in.md` (Note: internal reference, may not be available in independent deployments)

---

## Next Step
*   **[Agent Interface](./agent_interface.md)**: Learn how to write the `bid.sh` and `run.sh` scripts.
*   **[Build & Run](./build_and_run.md)**: Learn how to build your agent image and run the clan.
