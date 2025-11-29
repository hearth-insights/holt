# 6. CLI Reference

This document provides a reference for the `holt` command-line interface.

## Global Flags
These flags are available for all commands:
*   `-f, --config string`: Path to `holt.yml` configuration file.
*   `-d, --debug`: Enable verbose debug output.
*   `-q, --quiet`: Suppress all non-essential output.
*   `-v, --version`: Display version information.

## Commands

### `init`
Initialize a new Holt project with default configuration and example agent.

**Usage**: `holt init [flags]`

**Flags**:
*   `--force`: Force reinitialization (removes existing `holt.yml` and `agents/`).

**Creates**:
*   `holt.yml`: Project configuration file.
*   `agents/example-agent/`: Example agent demonstrating the Holt agent contract.

---

### `up`
Start a new Holt instance in the current Git repository.

**Usage**: `holt up [flags]`

**Flags**:
*   `-n, --name string`: Instance name (auto-generated if omitted).
*   `--force`: Bypass workspace collision check.

**Description**:
Creates and starts an isolated Docker network, Redis container (blackboard), and Orchestrator container.

---

### `forage`
Create a new workflow by submitting a goal description.

**Usage**: `holt forage [flags]`

**Flags**:
*   `-g, --goal string`: Goal description (required).
*   `-n, --name string`: Target instance name (auto-inferred if omitted).
*   `-w, --watch`: Wait for orchestrator to create claim (Phase 1 validation).

**Examples**:
```bash
holt forage --goal "Build a REST API"
holt forage --watch --goal "Refactor auth module"
```

---

### `watch`
Monitor real-time workflow progress and agent activity with powerful filtering.

**Usage**: `holt watch [flags]`

**Flags**:
*   `--agent string`: Filter by agent role (exact match).
*   `--type string`: Filter by artefact type (glob pattern).
*   `--since string`: Show events after time (duration like `1h` or RFC3339).
*   `--until string`: Show events before time.
*   `-o, --output string`: Output format (`default`, `jsonl`, `json`).
*   `--exit-on-completion`: Exit with code 0 when Terminal artefact detected.

**Examples**:
```bash
# Watch all activity
holt watch

# Watch specific agent
holt watch --agent=coder

# Export to JSONL
holt watch --output=jsonl > events.jsonl
```

---

### `down`
Stop a Holt instance.

**Usage**: `holt down [flags]`

**Flags**:
*   `-n, --name string`: Instance name (auto-inferred if omitted).

---

### `list`
List all running Holt instances.

**Usage**: `holt list`

---

### `clean`
Remove build artifacts and temporary files.

**Usage**: `holt clean`
