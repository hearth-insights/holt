# 3. Agent Definition

To run agents in Holt, you need two things:
1.  A definition in `holt.yaml`.
2.  A Dockerfile for the agent image.

## 1. holt.yaml
This file resides in the root of your project (or the directory where you run `holt`). It defines the agents and services.

### Example `holt.yaml`
```yaml
version: "1.0"
agents:
  git-agent:
    role: "Git Agent"
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    workspace:
      mode: rw
services:
  redis:
    image: redis:7-alpine
```

*   **agents**: A map of agent names to their configuration.
    *   **role**: A human-readable role name.
    *   **image**: The Docker image to use.
    *   **command**: The command to run inside the container.
    *   **workspace**: Configuration for the shared workspace. `mode: rw` means read-write access.
*   **services**: A map of service names (like Redis) to their configuration.

## 2. Dockerfile
Each agent needs a Dockerfile. The key requirement is that it must include the `pup` binary.

### Example Dockerfile
This example assumes you have downloaded the `holt-pup` binary and placed it in the same directory as your Dockerfile.

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
