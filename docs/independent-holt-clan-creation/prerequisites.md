# 2. Prerequisites

Before you begin, ensure the following tools are installed and available in your environment.

## Required Tools

### 1. Docker
Used to run agent containers and the Redis service.
*   **Check**: `docker --version`
*   **Requirement**: Version 20.10 or higher.

### 2. Go (Golang)
Used to build the Holt binaries (CLI, Orchestrator, Pup).
*   **Check**: `go version`
*   **Requirement**: Version 1.21 or higher.
*   **Note**: Optional if you plan to use pre-built binaries.

### 3. Git
Used for workspace management and version control.
*   **Check**: `git --version`
*   **Requirement**: Version 2.x or higher.

### 4. Make
Used to run build commands.
*   **Check**: `make --version`

## Verification
Run the following command to verify all prerequisites:

```bash
docker --version && go version && git --version && make --version
```

If any command fails, please install the missing tool before proceeding.

## Next Step
## Next Step
*   **[Agent Definition](./agent_definition.md)**: Start defining your agents.
