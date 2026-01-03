# Example Deployer Agent - Named Pattern Synchronizer

**Pattern**: Named Pattern (M5.1)
**Use Case**: CI/CD deployment that waits for tests, linting, and security scan to complete

---

## Overview

This agent demonstrates the **Named Pattern** for fan-in synchronization. It waits for three specific artefact types to exist as descendants of a CodeCommit ancestor before executing:

- `TestResult`
- `LintResult`
- `SecurityScan`

Only when **all three** exist does the deployer bid and execute deployment.

---

## How It Works

### Configuration (holt.yml)

⚠️ **Important:** `synchronize` is **MUTUALLY EXCLUSIVE** with `bidding_strategy` and `bid_script`. You cannot use both.

```yaml
agents:
  deployer:
    role: "Deployer"
    image: "example-deployer:latest"
    command: ["/app/deploy.sh"]

    # Synchronize block - REPLACES bidding_strategy (mutually exclusive)
    synchronize:
      # Wait for descendants of this ancestor type
      ancestor_type: "CodeCommit"

      # Wait for all these specific types (Named Pattern)
      wait_for:
        - type: "TestResult"
        - type: "LintResult"
        - type: "SecurityScan"

    workspace:
      mode: ro
```

### Workflow

```
1. User creates CodeCommit artefact
       ↓
2. TestAgent creates TestResult (descendant of CodeCommit)
       ↓
3. LintAgent creates LintResult (descendant of CodeCommit)
       ↓
4. SecurityAgent creates SecurityScan (descendant of CodeCommit)
       ↓
5. Deployer synchronizer detects all 3 prerequisites met
       ↓
6. Deployer bids exclusive on the final trigger artefact
       ↓
7. Deployer receives:
   - ancestor_artefact (the CodeCommit)
   - descendant_artefacts (TestResult, LintResult, SecurityScan)
       ↓
8. Deployer verifies all checks passed
       ↓
9. Deployer creates DeploymentComplete artefact
```

---

## Building and Running

### Build Image

```bash
# From project root
docker build -t example-deployer:latest -f agents/example-deployer-agent/Dockerfile .
```

### Configure holt.yml

Add the deployer agent and prerequisite agents:

```yaml
version: "1.0"

agents:
  # Prerequisite agents (create the artefacts deployer waits for)
  test-agent:
    role: "Tester"
    image: "example-test-agent:latest"
    command: ["/app/test.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  lint-agent:
    role: "Linter"
    image: "example-lint-agent:latest"  # (You'd create this)
    command: ["/app/lint.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  security-agent:
    role: "Security Scanner"
    image: "example-security-agent:latest"  # (You'd create this)
    command: ["/app/scan.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  # Synchronizer agent (waits for all 3)
  deployer:
    role: "Deployer"
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

### Run Workflow

```bash
# Start Holt
holt up

# Trigger workflow
holt forage --goal "Deploy v2.0"

# Watch execution
holt watch

# Expected output:
# 1. CodeCommit created
# 2. TestResult created (1 of 3 prerequisites)
# 3. LintResult created (2 of 3 prerequisites)
# 4. SecurityScan created (3 of 3 prerequisites)
# 5. Deployer bids and wins
# 6. DeploymentComplete created

# View audit trail
holt hoard
```

---

## Input Format

When the deployer executes, it receives:

```json
{
  "claim_type": "exclusive",
  "target_artefact": {
    "id": "final-trigger-uuid",
    "type": "SecurityScan",
    "payload": "scan-passed",
    ...
  },
  "context_chain": [ /* historical context */ ],

  "ancestor_artefact": {
    "id": "commit-uuid",
    "type": "CodeCommit",
    "payload": "abc123def456...",
    ...
  },

  "descendant_artefacts": [
    {
      "id": "test-uuid",
      "type": "TestResult",
      "payload": "all-tests-passed",
      ...
    },
    {
      "id": "lint-uuid",
      "type": "LintResult",
      "payload": "no-issues",
      ...
    },
    {
      "id": "scan-uuid",
      "type": "SecurityScan",
      "payload": "scan-passed",
      ...
    }
  ]
}
```

---

## Output

### Success

```json
{
  "artefact_type": "DeploymentComplete",
  "artefact_payload": "deploy-1234567890",
  "summary": "Deployed commit abc123 (tests: passed, lint: passed, security: passed)"
}
```

### Failure (if any check fails)

```json
{
  "structural_type": "Failure",
  "artefact_payload": "Deployment aborted: one or more prerequisites failed",
  "summary": "Deployment aborted due to failed prerequisites"
}
```

---

## Testing

### Unit Test (Without Holt)

```bash
# Create test input
cat > test-input.json <<'EOF'
{
  "claim_type": "exclusive",
  "target_artefact": {
    "id": "test-uuid",
    "type": "SecurityScan",
    "payload": "scan-passed"
  },
  "context_chain": [],
  "ancestor_artefact": {
    "id": "commit-uuid",
    "type": "CodeCommit",
    "payload": "abc123def456"
  },
  "descendant_artefacts": [
    {
      "type": "TestResult",
      "payload": "all-tests-passed"
    },
    {
      "type": "LintResult",
      "payload": "no-issues"
    },
    {
      "type": "SecurityScan",
      "payload": "scan-passed"
    }
  ]
}
EOF

# Test script directly
cat test-input.json | agents/example-deployer-agent/deploy.sh 3>&1

# Should output DeploymentComplete JSON
```

### Integration Test (With Holt)

```bash
# Build all required images
docker build -t example-deployer:latest -f agents/example-deployer-agent/Dockerfile .
# (Build test-agent, lint-agent, security-agent similarly)

# Start instance
holt up

# Submit goal
holt forage --goal "Deploy v2.0"

# Monitor logs
holt logs deployer

# Expected log output:
# ========================================
# Deployer Agent - Named Pattern Example
# ========================================
#
# Ancestor Artefact:
#   Type: CodeCommit
#   Commit Hash: abc123...
#
# Descendant Artefacts: 3
#
# Prerequisites:
#   TestResult:   all-tests-passed
#   LintResult:   no-issues
#   SecurityScan: scan-passed
#
# ✅ All prerequisites passed - proceeding with deployment
#
# Deployment Details:
#   Deployment ID: deploy-1234567890
#   ...
#
# ✅ Deployment successful!

# Verify result
holt hoard | grep DeploymentComplete
```

---

## Troubleshooting

### Deployer Never Executes

**Issue:** Prerequisites complete but deployer doesn't bid.

**Debug:**
```bash
# Check deployer logs
holt logs deployer

# Look for:
# - "No ancestor of type 'CodeCommit' found" → Check artefact graph
# - "Not all dependencies met" → One of the 3 types missing
# - "Lock already held" → Race condition (expected)

# Verify all artefacts exist
holt hoard | grep -E "TestResult|LintResult|SecurityScan"
# Should see all 3 types
```

### Deployer Bids Too Early

**Issue:** Deployer executes before all 3 prerequisites complete.

**Debug:**
```bash
# Verify configuration
cat holt.yml | grep -A 5 wait_for

# Check artefact count
holt hoard | grep -c "TestResult"
holt hoard | grep -c "LintResult"
holt hoard | grep -c "SecurityScan"
# Each should be >= 1
```

---

## Customization

### Adding More Prerequisites

```yaml
synchronize:
  ancestor_type: "CodeCommit"
  wait_for:
    - type: "TestResult"
    - type: "LintResult"
    - type: "SecurityScan"
    - type: "PerformanceTest"  # Add new prerequisite
    - type: "IntegrationTest"  # Add another
```

Update `deploy.sh` to extract and validate new types:

```bash
perf_result=$(echo "$descendants" | jq -r '.[] | select(.type=="PerformanceTest") | .payload')
integration_result=$(echo "$descendants" | jq -r '.[] | select(.type=="IntegrationTest") | .payload')
```

### Changing Ancestor Type

Wait for descendants of a different ancestor (e.g., "ReleaseTag"):

```yaml
synchronize:
  ancestor_type: "ReleaseTag"
  wait_for:
    - type: "BuildArtifact"
    - type: "ChangelogUpdated"
    - type: "DocumentationBuilt"
```

---

## Learn More

- **Fan-In Synchronization Guide**: `docs/guides/fan-in-synchronization.md`
- **Agent Development Guide**: `docs/guides/agent-development.md`
- **M5.1 Design Document**: `design/features/phase-5-complex-coordination/M5.1-fan-in.md`
- **Example Batch Aggregator**: `agents/example-batch-aggregator-agent/` (Producer-Declared pattern)
