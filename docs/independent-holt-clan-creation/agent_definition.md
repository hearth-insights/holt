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

orchestrator:
  max_review_iterations: 3  # Limit rework loops (0 = unlimited, default: 3)

agents:
  # Basic agent with environment variables and prompts
  coder:
    image: "coder-agent:latest"
    command: ["/app/code.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["DesignSpec"]
    workspace:
      mode: rw
    environment:
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}  # API key from host
      - MODEL_NAME=claude-sonnet-4
    prompts:
      execution: |
        You are an expert Go developer. Implement features based on
        approved design specifications. Create git commits but don't push.

  # Agent with volume mounts for credentials
  deployer:
    image: "deployer:latest"
    command: ["/app/deploy.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["DeploymentPlan"]
    workspace:
      mode: ro
    volumes:
      - "~/.aws:/root/.aws:ro"              # AWS credentials (read-only)
      - "~/.config/gcloud:/root/.config/gcloud:ro"  # GCP credentials
    resources:
      limits:
        cpus: "2.0"
        memory: "2GB"

  # Controller-worker pattern for parallel execution
  test-runner:
    mode: "controller"
    image: "test-runner:latest"
    command: ["/app/controller.sh"]
    bidding_strategy:
      type: "review"
      target_types: ["CodeCommit"]
    worker:
      max_concurrent: 5
      image: "test-runner:latest"
      command: ["/app/run-tests.sh"]
      workspace: { mode: ro }

  # Synchronizer agent (waits for multiple prerequisites)
  final-deployer:
    image: "deployer:latest"
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

*   **orchestrator**: Optional configuration for orchestrator behavior.
    *   **max_review_iterations**: Maximum times an artefact can be rejected and reworked (default: `3`, `0` = unlimited). See [Orchestrator Configuration](#2-orchestrator-configuration) below.
*   **agents**: A map of agent names (roles) to their configuration.
    *   **key**: The agent's role (e.g., `coder`). The Orchestrator uses this identifier to route tasks and claims.
    *   **image**: The Docker image to use.
    *   **command**: The command to run inside the container.
    *   **bidding_strategy** or **bid_script** or **synchronize**: How the agent decides which claims to work on (exactly one required).
    *   **workspace**: Configuration for the shared workspace. `mode: rw` means read-write access.
    *   **environment**, **prompts**, **volumes**, **resources**, **mode/worker**, **build**: See [Agent Configuration Reference](#4-agent-configuration-reference) for complete options.
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

## 2. Orchestrator Configuration

The `orchestrator` section in `holt.yaml` controls workflow behavior.

### Review Loop Limits

**Problem**: Review/rework loops can run indefinitely if an agent consistently produces work that fails review.

**Solution**: The orchestrator enforces a retry limit using `max_review_iterations`:

```yaml
orchestrator:
  max_review_iterations: 3  # Default: 3 attempts
```

**Behavior**:
*   **Default (`3`)**: After 3 rejected reviews, the orchestrator:
    1.  Creates a `Failure` artefact with type `MaxIterationsExceeded`
    2.  Terminates the workflow claim
    3.  Logs the termination reason: `"Terminated after reaching max review iterations (3)"`
*   **Unlimited (`0`)**: No limit — workflows can retry indefinitely (use cautiously).
*   **Custom (`5`, `10`, etc.)**: Adjust based on your workflow complexity.

**How iterations are counted**:
*   Each rejection → rework → new artefact version counts as one iteration.
*   The count resets for each new workflow branch.

**When to adjust**:
*   **Increase** for complex tasks requiring multiple refinement passes (e.g., `5` for architectural reviews).
*   **Decrease** for simple tasks where repeated failures indicate a deeper issue (e.g., `1` for linting).
*   **Set to `0`** only for experimental workflows where you want to observe failure patterns.

## 3. Dockerfile
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

## 4. Agent Configuration Reference

This section documents all available configuration options for agents in `holt.yml`.

### 4.1. Environment Variables (`environment`)

Pass environment variables to agent containers for API keys, credentials, and configuration.

**Format**: Array of `"KEY=value"` strings. Supports `${VAR_NAME}` expansion from host environment.

**Example**:
```yaml
agents:
  coder:
    image: "coder:latest"
    command: ["/app/code.sh"]
    bidding_strategy: { type: "exclusive" }
    environment:
      - OPENAI_API_KEY=${OPENAI_API_KEY}  # Expands from host
      - MOCK_MODE=true                     # Literal value
      - TIMEOUT=300
```

**Use cases**:
- **API keys**: LLM provider credentials (OpenAI, Anthropic, Gemini)
- **Feature flags**: `MOCK_MODE`, `DEBUG`, `VERBOSE`
- **Configuration**: Timeouts, retry limits, model names

**Security**:
- **Never commit secrets** to version control
- Use `${VAR_NAME}` expansion for secrets
- Set variables in your shell before running `holt up`

### 4.2. Custom Prompts (`prompts`)

Define custom LLM prompts for agent behavior. Essential for LLM-based agents.

**Fields**:
- `execution`: Main task prompt (sent when agent executes work)
- `claim`: Bidding decision prompt (sent when evaluating claims) - optional

**Example**:
```yaml
agents:
  designer:
    image: "gemini-agent:latest"
    command: ["/app/design.sh"]
    bidding_strategy: { type: "exclusive" }
    prompts:
      execution: |
        You are a senior product designer. Your role is to:
        1. Understand high-level goals and ask clarifying questions
        2. Create concise design proposals (1-2 pages)
        3. Focus on the "what" and "why", not the "how"

        Output a DesignSpecDraft artefact with markdown content.

      claim: "Evaluate if this task requires design expertise"
```

**Tips**:
- Use multi-line YAML (`|`) for readable prompts
- Be specific about output format and artefact types
- Reference Holt concepts (artefacts, blackboard, context_chain)

### 4.3. Volume Mounts (`volumes`)

Mount host directories into agent containers for credentials, configuration, or data.

**Format**: Array of `"source:destination:mode"` strings
**Modes**: `ro` (read-only), `rw` (read-write)

**Example**:
```yaml
agents:
  deployer:
    image: "deployer:latest"
    command: ["/app/deploy.sh"]
    bidding_strategy: { type: "exclusive" }
    volumes:
      - "~/.config/gcloud:/root/.config/gcloud:ro"  # GCP credentials
      - "~/.aws:/root/.aws:ro"                      # AWS credentials
      - "/data:/app/data:rw"                        # Shared data directory
```

**⚠️ Security Warnings**:
- **Use `:ro` mode** for credentials to prevent tampering
- **Only mount what's needed** (principle of least privilege)
- **Review agent images** before mounting sensitive directories
- Holt logs security warnings for `:rw` mounts on credential paths

**Common use cases**:
- Cloud provider credentials (gcloud, aws, azure)
- SSH keys for git operations
- Shared datasets or model files
- Configuration files

### 4.4. Resource Limits (`resources`)

Control CPU and memory allocation for agents in production environments.

**Structure**:
```yaml
resources:
  limits:          # Hard limits (cannot exceed)
    cpus: "2.0"    # Max 2 CPUs
    memory: "4GB"  # Max 4 gigabytes
  reservations:    # Soft reservations (guaranteed minimum)
    cpus: "1.0"
    memory: "2GB"
```

**Example**:
```yaml
agents:
  heavy-processor:
    image: "processor:latest"
    command: ["/app/process.sh"]
    bidding_strategy: { type: "claim" }
    resources:
      limits:
        cpus: "4.0"
        memory: "8GB"
      reservations:
        cpus: "2.0"
        memory: "4GB"
```

**Units**:
- **CPUs**: Decimal (e.g., `"0.5"`, `"2.0"`, `"4.0"`)
- **Memory**: `"512M"`, `"2GB"`, `"4GB"` (case-insensitive)

**When to use**:
- **Production deployments**: Prevent resource exhaustion
- **Multi-tenant systems**: Fair resource allocation
- **Cost control**: Limit expensive LLM agents

### 4.5. Controller-Worker Pattern (`mode` + `worker`)

Enable horizontal scaling by separating bidding (controller) from execution (workers).

**Pattern**:
- Single persistent **controller** handles bidding
- Ephemeral **workers** execute granted claims in parallel
- Eliminates bidding race conditions
- Workers auto-scale to zero when idle

**Example**:
```yaml
agents:
  batch-processor:
    mode: "controller"               # Designates as controller
    image: "processor:latest"
    command: ["/app/bid.sh"]         # Controller only bids
    bidding_strategy: { type: "claim" }

    worker:                          # Worker configuration
      max_concurrent: 5              # Max 5 parallel workers (default: 1)
      image: "processor:latest"      # Can differ from controller
      command: ["/app/process.sh"]   # Worker executes work
      workspace: { mode: ro }
      environment:
        - WORKER_ID=${WORKER_ID}     # Worker-specific vars
      keep_containers: false         # Delete after completion (default)
```

**Worker fields**:
- `max_concurrent`: Maximum parallel workers (default: 1)
- `image`: Worker Docker image (can differ from controller)
- `command`: Worker execution command
- `workspace`: Worker-specific workspace config
- `environment`: Worker-specific environment variables
- `keep_containers`: Retain workers for debugging (default: `false`)

**When to use**:
- High-throughput workflows (tests, linting, batch processing)
- Expensive operations that benefit from parallelism
- Fan-out patterns (one trigger → multiple parallel workers)

**Limitations**:
- Controller must remain running
- Workers are stateless (no inter-worker communication)
- Each worker executes independently

### 4.6. Build from Source (`build`)

Build agent images from Dockerfile instead of using pre-built images.

**Example**:
```yaml
agents:
  custom-agent:
    image: "custom-agent:latest"     # Tag for built image
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
    build:
      context: ./agents/custom-agent # Path to Dockerfile directory
    workspace: { mode: rw }
```

**Behavior**:
- `holt up` builds image before starting agent
- Build happens once per `holt up` invocation
- Image cached for subsequent runs
- Rebuild with `docker build --no-cache`

**When to use**:
- Development workflows (frequent code changes)
- Custom agent logic not available as pre-built image
- Testing local changes before publishing

**Alternative**: Use pre-built images pushed to registry for production.

## 5. Synchronizer Agents (M5.1+)

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
