# System Spine: Configuration Drift Detection

**Status**: M4.7 (System Integrity & Configuration Ledger)

The **System Spine** is a cryptographic ledger that anchors every workflow artefact to the specific system configuration (holt.yml + git commit + agent versions) that produced it. This provides compliance officers and auditors with proof of exactly *which version of the system* was responsible for any given decision.

---

## Core Concepts

### SystemManifest Artefact

The `SystemManifest` is a special artefact created by the Orchestrator at startup. It acts as a "checkpoint" representing the system's configuration state.

**Payload Schema (Local Strategy):**
```json
{
  "strategy": "local",
  "config_hash": "sha256:a3f2b9c4...",  // Hash of holt.yml
  "git_commit": "def456ab...",          // Git HEAD commit hash
  "computed_at_ms": 1704067200000
}
```

### The "Spine" Thread

All SystemManifest artefacts belong to a single logical thread per instance, creating a linear history of configuration changes over time.

```
SystemManifest v1  -->  SystemManifest v2  -->  SystemManifest v3 (Active)
(Initial State)         (Config Changed)        (Git Commit Changed)
```

### Artefact Anchoring

Every root artefact (e.g., `GoalDefined` created by `holt forage`) is cryptographically anchored to the **active** SystemManifest by including its hash in the `parent_hashes` field.

```
SystemManifest v2  <--  GoalDefined (parent_hashes=[v2_hash])
```

This anchor is immutable. Even if the system configuration changes later (creating v3), the historical workflow remains permanently linked to v2.

---

## CLI Commands

### Viewing System History

Use `holt spine` to view the history of configuration changes for an instance:

```bash
$ holt spine

System Spine History
====================

Version 1
  Manifest ID: b8c4e6d1...
  Created:     2025-01-15 10:30:00 UTC
  Config Hash: sha256:abc123...
  Git Commit:  def456ab...

Version 2 (ACTIVE)
  Manifest ID: a3f2b9c4...
  Created:     2025-01-15 14:22:15 UTC
  Config Hash: sha256:xyz789...
  Git Commit:  789abc0d...
```

### Verifying Configuration State

Use `holt verify-config` to check if the current filesystem state matches a stored manifest. This is critical for forensic auditing.

```bash
# Verify against the active manifest
$ holt verify-config --manifest $(holt spine | grep ACTIVE | awk '{print $4}')

Verifying SystemManifest a3f2b9c4...

Config Hash Verification:
  Stored:  sha256:xyz789...
  Current: sha256:xyz789...
  Status:  ✓ MATCH

Git Commit Verification:
  Stored:  789abc0d...
  Current: 789abc0d...
  Status:  ✓ MATCH
```

If you modify `holt.yml` without restarting the orchestrator, this command will report a mismatch, alerting you to the drift.

---

## Identity Strategies

Holt supports two strategies for computing system identity, configured via the `HOLT_IDENTITY_SOURCE` environment variable.

### 1. Local Strategy (Default)

Computes identity from local files:
- **Config Hash**: SHA-256 hash of the `holt.yml` file.
- **Git Commit**: Output of `git rev-parse HEAD` in the workspace.

**Requirements**:
- Workspace must be a valid Git repository.
- `git` command must be available.

### 2. External Strategy (Enterprise)

Reads identity from an external JSON file provided by the deployment environment (e.g., Kubernetes ConfigMap, CI/CD pipeline).

**Configuration**:
- `HOLT_IDENTITY_SOURCE=external`
- `HOLT_MANIFEST_PATH=/path/to/manifest.json`

**Payload**: The content of the external file is stored opaquely in the SystemManifest. This allows organizations to inject arbitrary metadata (e.g., CI build ID, cluster region, deployment version).

---

## Drift Detection Logic

When the Orchestrator starts:

1.  Computes current system identity (hash of config + git commit).
2.  Fetches the latest SystemManifest from the spine.
3.  **Compares Hashes**:
    *   **Match**: Reuses the existing manifest (no drift).
    *   **Mismatch**: Creates a new SystemManifest (version++) and sets it as active.

This ensures that every configuration change results in a new, verifiable version in the spine.

---

## Troubleshooting

**Error: "failed to get git commit"**
*   **Cause**: The workspace is not a git repository or has no commits.
*   **Fix**: Run `git init` and `git commit --allow-empty -m "Initial commit"`.

**Error: "active_manifest not found"**
*   **Cause**: `holt forage` was run before the orchestrator finished initializing.
*   **Fix**: Wait a few seconds for `holt up` to complete, or check orchestrator logs.

**Warning: "Root artefact anchored to old manifest"**
*   **Cause**: A workflow was started just as the orchestrator was restarting with a new config.
*   **Impact**: The artefact is accepted but flagged. It is anchored to the old (valid) configuration state.
