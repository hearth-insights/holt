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
services:
  redis:
    image: redis:7-alpine
```

*   **agents**: A map of agent names (roles) to their configuration.
    *   **key**: The agent's role (e.g., `git-agent`). The Orchestrator uses this identifier to route tasks and claims.
    *   **image**: The Docker image to use.
    *   **command**: The command to run inside the container.
    *   **workspace**: Configuration for the shared workspace. `mode: rw` means read-write access.
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

## Next Step
*   **[Agent Interface](./agent_interface.md)**: Learn how to write the `bid.sh` and `run.sh` scripts.
*   **[Build & Run](./build_and_run.md)**: Learn how to build your agent image and run the clan.
