# Fan-In and Fan-Out Synchronization Guide

**Target Audience:** Developers building agents that merge multiple parallel workflow branches

**Prerequisites:**
- Understanding of Holt agent development ([agent-development.md](./agent-development.md))
- Familiarity with DAG (Directed Acyclic Graph) workflows
- Basic knowledge of `holt.yml` configuration

---

## ⚠️ Critical Configuration Rule

**The `synchronize` block is MUTUALLY EXCLUSIVE with `bidding_strategy` and `bid_script`.**

```yaml
# ❌ INVALID - Will cause validation error
agents:
  my-agent:
    bidding_strategy: "eager"  # Cannot use both!
    synchronize:
      ancestor_type: "CodeCommit"

# ✅ VALID - Use one or the other
agents:
  standard-agent:
    bidding_strategy: "eager"  # Standard agent

  synchronizer-agent:
    synchronize:               # Synchronizer agent
      ancestor_type: "CodeCommit"
```

---

## Table of Contents

1. [What is Fan-In Synchronization?](#what-is-fan-in-synchronization)
2. [When to Use Synchronizers](#when-to-use-synchronizers)
3. [Fan-Out Patterns](#fan-out-patterns)
4. [Synchronization Patterns](#synchronization-patterns)
5. [Configuration Reference](#configuration-reference)
6. [How It Works: Under the Hood](#how-it-works-under-the-hood)
7. [Building Your First Synchronizer](#building-your-first-synchronizer)
7. [Named Pattern Examples](#named-pattern-examples)
8. [Producer-Declared Pattern Examples](#producer-declared-pattern-examples)
9. [Multi-Artefact Output](#multi-artefact-output)
10. [Advanced Topics](#advanced-topics)
11. [Known Limitations](#known-limitations)
12. [Troubleshooting](#troubleshooting)

---

## What is Fan-In Synchronization?

**Fan-in** is a coordination pattern where an agent waits for multiple parallel branches of a workflow to complete before executing. Think of it as a "merge point" or "synchronization barrier" in a DAG.

### Visual Example: CI/CD Pipeline

```
CodeCommit (ancestor)
    ├── TestResult (branch 1)
    ├── LintResult (branch 2)
    └── SecurityScan (branch 3)
         ↓
    [Synchronizer waits for ALL THREE]
         ↓
    DeploymentComplete (fan-in point)
```

**Without synchronization**, you'd need complex bidding logic with race conditions and brittle queries.

**With M5.1 synchronization**, you declare your requirements in `holt.yml`, and Holt handles the coordination automatically.

---

## When to Use Synchronizers

### Use Synchronizers When:

✅ **Multiple parallel tasks must complete before next step**
- CI/CD: Deploy only after tests + linting + security scan pass
- Data processing: Aggregate results only after all shards complete
- Multi-stage approval: Proceed only after all reviewers approve

✅ **You need deterministic, race-free coordination**
- Traditional bidding scripts are prone to timing bugs
- Synchronizers use atomic Redis operations and deduplication locks

✅ **Dynamic parallelism (count unknown at design time)**
- Batch processing: Wait for N records (N determined at runtime)
- Sharded workflows: Wait for all shards (shard count from metadata)

### Don't Use Synchronizers When:

❌ **Single-branch workflows** - Use standard bidding
❌ **Optional dependencies** - Synchronizers require ALL conditions met
❌ **Timeout-based coordination** - V1 waits indefinitely (no timeouts)

---

## Fan-Out Patterns

**Fan-out** is the creation of multiple parallel units of work from a single agent. This is the counterpart to fan-in synchronization.

### How to Fan-Out

To create multiple artefacts (fan-out), an agent simply outputs multiple JSON objects to **FD 3**, separated by newlines (NDJSON format).

**Example: Splitter Agent**

```bash
#!/bin/sh
# Generate 3 parallel sub-tasks

# Output 1
cat <<EOF >&3
{"artefact_type": "SubTask", "artefact_payload": "task-1", "summary": "Subtask 1"}
EOF

# Output 2
cat <<EOF >&3
{"artefact_type": "SubTask", "artefact_payload": "task-2", "summary": "Subtask 2"}
EOF

# Output 3
cat <<EOF >&3
{"artefact_type": "SubTask", "artefact_payload": "task-3", "summary": "Subtask 3"}
EOF

# Pup automatically:
# 1. Counts the artefacts (3)
# 2. Injects metadata {"batch_size": "3"} into ALL of them
# 3. Publishes them atomically
```

This automatic metadata injection is what enables the **Producer-Declared** fan-in pattern downstream.

---

## Synchronization Patterns

M5.1 supports two patterns:

### 1. Named Pattern

**Use case:** Wait for specific, known artefact types.

**Example:** Deployer waits for `TestResult` + `LintResult` + `SecurityScan`.

**Configuration:**
```yaml
synchronize:
  ancestor_type: "CodeCommit"
  wait_for:
    - type: "TestResult"
    - type: "LintResult"
    - type: "SecurityScan"
```

**Behavior:** Synchronizer bids when **exactly one** of each type exists as a descendant of the ancestor.

---

### 2. Producer-Declared Pattern

**Use case:** Wait for N artefacts of same type, where N is unknown until runtime.

**Example:** Aggregator waits for N `ProcessedRecord` artefacts (N from batch metadata).

**Configuration:**
```yaml
synchronize:
  ancestor_type: "DataBatch"
  wait_for:
    - type: "ProcessedRecord"
      count_from_metadata: "batch_size"
```

**Behavior:** Synchronizer reads `batch_size` from the first `ProcessedRecord`'s metadata, then waits until N records exist.

---

### Pattern Comparison

| Aspect | Named Pattern | Producer-Declared Pattern |
|--------|--------------|--------------------------|
| **Use Case** | Known, distinct types | Dynamic count of same type |
| **Configuration** | List of types | Type + metadata key |
| **Example** | Tests + Lint + Scan | N sharded records |
| **Count** | Fixed (1 per type) | Dynamic (from metadata) |
| **Metadata Required** | No | Yes (producer injects) |

---

## Configuration Reference

### Full Synchronizer Configuration

```yaml
agents:
  my-synchronizer:
    role: "My Synchronizer"
    image: "my-sync:latest"
    command: ["/app/run.sh"]

    # Synchronize block (MUTUALLY EXCLUSIVE with bidding_strategy and bid_script)
    synchronize:
      # Required: Common ancestor artefact type
      ancestor_type: "CodeCommit"

      # Required: List of conditions (at least one)
      wait_for:
        - type: "TestResult"               # Named pattern
        - type: "LintResult"               # Named pattern
        - type: "ProcessedRecord"          # Producer-Declared pattern
          count_from_metadata: "batch_size"

      # Optional: Limit descendant traversal depth (0 = unlimited)
      max_depth: 10

    workspace:
      mode: ro  # Read-only unless writing files
```

### Configuration Rules

**Critical: Mutual Exclusivity**

⚠️ **`synchronize` is MUTUALLY EXCLUSIVE with `bidding_strategy` and `bid_script`**

You MUST use **one or the other**, never both:
- **Standard agents**: Use `bidding_strategy` OR `bid_script`
- **Synchronizer agents**: Use `synchronize` block
- **Invalid**: Using both will cause a validation error

**Other Validation Rules:**
- ✅ `ancestor_type` is required
- ✅ `wait_for` must have at least one condition
- ✅ Each condition must have a `type`
- ✅ `max_depth` must be >= 0 (0 = unlimited)

**Common Errors:**

```yaml
# ❌ WRONG: Can't use both synchronize and bidding_strategy
agents:
  bad-agent:
    bidding_strategy: "eager"  # ← ERROR! Remove this
    synchronize:
      ancestor_type: "Goal"
      wait_for:
        - type: "Result"

# ❌ WRONG: Can't use both synchronize and bid_script
agents:
  bad-agent:
    bid_script: "/app/bid.sh"  # ← ERROR! Remove this
    synchronize:
      ancestor_type: "Goal"
      wait_for:
        - type: "Result"

# ✅ CORRECT: Use ONLY synchronize
agents:
  good-agent:
    synchronize:
      ancestor_type: "Goal"
      wait_for:
        - type: "Result"

# ✅ CORRECT: Use ONLY bidding_strategy
agents:
  good-agent:
    bidding_strategy: "eager"
```

```yaml
# ❌ WRONG: Empty wait_for
agents:
  bad-agent:
    synchronize:
      ancestor_type: "Goal"
      wait_for: []  # Error!
```

---

## How It Works: Under the Hood

### 1. Artefact Creation (Automatic)

When ANY agent creates an artefact, Holt automatically:

1. **Creates artefact Hash** in Redis
2. **Updates reverse index** (`holt:{inst}:index:children:{parent_id}`)
3. **Publishes event** to trigger synchronizers

This happens via a Lua script (atomic operation).

### 2. Synchronizer Bidding Logic

When a claim event arrives, the synchronizer:

1. **Checks if trigger**: Is the artefact type in `wait_for`?
2. **Finds ancestor**: Traverse UP via `source_artefacts` to find `ancestor_type`
3. **Gets descendants**: Traverse DOWN via reverse index from ancestor
4. **Verifies conditions**: Check if ALL `wait_for` conditions met
5. **Acquires lock**: Prevent duplicate bids (race protection)
6. **Bids exclusive**: Submit bid on the triggering artefact

### 3. Context Assembly

When granted, the synchronizer receives:

```json
{
  "claim_type": "exclusive",
  "target_artefact": { /* the final trigger */ },
  "context_chain": [ /* historical context */ ],
  "ancestor_artefact": { /* the common ancestor */ },
  "descendant_artefacts": [ /* ALL matched descendants */ ]
}
```

Your agent script sees the full picture for aggregation.

### 4. Deduplication Lock

**Problem:** Two workers complete simultaneously, both trigger synchronizer evaluation.

**Solution:** First to acquire lock (`SET NX`) wins, second skips bidding.

**Lock key:** `holt:{inst}:sync_dedup:{ancestor_id}:{agent_role_hash}`

**TTL:** 10 minutes (auto-cleanup if pup crashes)

---

## Building Your First Synchronizer

### Step 1: Define Your Workflow

**Goal:** Deploy code only after tests, linting, and security scan pass.

**Artefacts:**
- `CodeCommit` (ancestor)
- `TestResult`, `LintResult`, `SecurityScan` (prerequisites)
- `DeploymentComplete` (output)

### Step 2: Configure holt.yml

```yaml
version: "1.0"

agents:
  # Agents that create prerequisites
  test-agent:
    role: "Tester"
    image: "test-agent:latest"
    command: ["/app/test.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  lint-agent:
    role: "Linter"
    image: "lint-agent:latest"
    command: ["/app/lint.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  security-agent:
    role: "Security"
    image: "security-agent:latest"
    command: ["/app/scan.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  # Synchronizer agent
  deployer:
    role: "Deployer"
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

### Step 3: Write Deployer Script

**File:** `agents/deployer/deploy.sh`

```bash
#!/bin/sh
set -e

# Read synchronizer input
input=$(cat)

# Extract ancestor artefact (the CodeCommit)
ancestor=$(echo "$input" | jq -r '.ancestor_artefact')
commit_hash=$(echo "$ancestor" | jq -r '.payload')

echo "Deploying commit: $commit_hash" >&2

# Extract descendant artefacts (prerequisites)
descendants=$(echo "$input" | jq -r '.descendant_artefacts')

# Check each prerequisite
test_status=$(echo "$descendants" | jq -r '.[] | select(.type=="TestResult") | .payload')
lint_status=$(echo "$descendants" | jq -r '.[] | select(.type=="LintResult") | .payload')
scan_status=$(echo "$descendants" | jq -r '.[] | select(.type=="SecurityScan") | .payload')

echo "Prerequisites:" >&2
echo "  Tests: $test_status" >&2
echo "  Lint: $lint_status" >&2
echo "  Security: $scan_status" >&2

# Verify all passed (example logic)
if echo "$test_status" | grep -q "passed" && \
   echo "$lint_status" | grep -q "passed" && \
   echo "$scan_status" | grep -q "passed"; then

  echo "All checks passed - deploying!" >&2

  # Simulate deployment
  deployment_id="deploy-$(date +%s)"

  # Output success artefact to FD 3
  cat <<EOF >&3
{
  "artefact_type": "DeploymentComplete",
  "artefact_payload": "$deployment_id",
  "summary": "Deployed commit $commit_hash (all checks passed)"
}
EOF

else
  echo "Prerequisites failed - aborting deployment" >&2

  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Deployment aborted: prerequisites not met",
  "summary": "Deployment failed"
}
EOF
fi
```

### Step 4: Build and Test

```bash
# Build deployer image
docker build -t deployer:latest -f agents/deployer/Dockerfile .

# Build prerequisite agents (test, lint, security)
# ... (same pattern as deployer)

# Start Holt
holt up

# Trigger workflow
holt forage --goal "Deploy v2.0"

# Watch execution
holt watch

# View results
holt hoard
```

---

## Named Pattern Examples

### Example 1: Multi-Approval Gate

**Use case:** Document requires approval from Legal + Finance + Security.

**Configuration:**
```yaml
agents:
  final-approver:
    role: "Final Approver"
    image: "approver:latest"
    command: ["/app/approve.sh"]

    synchronize:
      ancestor_type: "DocumentDraft"
      wait_for:
        - type: "LegalApproval"
        - type: "FinanceApproval"
        - type: "SecurityApproval"
```

**Agent Script:**
```bash
#!/bin/sh
input=$(cat)

# Extract all approvals
approvals=$(echo "$input" | jq -r '.descendant_artefacts')

legal=$(echo "$approvals" | jq -r '.[] | select(.type=="LegalApproval") | .payload')
finance=$(echo "$approvals" | jq -r '.[] | select(.type=="FinanceApproval") | .payload')
security=$(echo "$approvals" | jq -r '.[] | select(.type=="SecurityApproval") | .payload')

echo "Approvals received:" >&2
echo "  Legal: $legal" >&2
echo "  Finance: $finance" >&2
echo "  Security: $security" >&2

# All approvals must be "approved"
if [ "$legal" = "approved" ] && [ "$finance" = "approved" ] && [ "$security" = "approved" ]; then
  cat <<EOF >&3
{
  "artefact_type": "DocumentApproved",
  "artefact_payload": "all-departments-approved",
  "summary": "Document approved by all departments"
}
EOF
else
  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Not all departments approved",
  "summary": "Approval failed"
}
EOF
fi
```

---

### Example 2: Build Matrix (Multiple Platforms)

**Use case:** Wait for builds on Linux + macOS + Windows before releasing.

**Configuration:**
```yaml
agents:
  release-publisher:
    role: "Release Publisher"
    image: "publisher:latest"
    command: ["/app/publish.sh"]

    synchronize:
      ancestor_type: "VersionTag"
      wait_for:
        - type: "LinuxBuild"
        - type: "MacOSBuild"
        - type: "WindowsBuild"
```

**Agent Script:**
```bash
#!/bin/sh
input=$(cat)

# Extract build artefacts
builds=$(echo "$input" | jq -r '.descendant_artefacts')

linux_artifact=$(echo "$builds" | jq -r '.[] | select(.type=="LinuxBuild") | .payload')
macos_artifact=$(echo "$builds" | jq -r '.[] | select(.type=="MacOSBuild") | .payload')
windows_artifact=$(echo "$builds" | jq -r '.[] | select(.type=="WindowsBuild") | .payload')

echo "Creating release with artifacts:" >&2
echo "  Linux: $linux_artifact" >&2
echo "  macOS: $macos_artifact" >&2
echo "  Windows: $windows_artifact" >&2

# Package all builds
release_package="release-$(date +%Y%m%d-%H%M%S).tar.gz"

cat <<EOF >&3
{
  "artefact_type": "ReleasePublished",
  "artefact_payload": "$release_package",
  "summary": "Published release with Linux, macOS, and Windows builds"
}
EOF
```

---

## Producer-Declared Pattern Examples

### Example 1: Batch Data Processing

**Use case:** Process 1000 records in parallel, aggregate when all complete.

**Producer Agent (creates records):**
```bash
#!/bin/sh
# This agent creates multiple ProcessedRecord artefacts
# The pup automatically injects metadata: {"batch_size": "1000"}

for i in $(seq 1 1000); do
  # Process record
  result="record-$i-processed"

  # Output to FD 3 (pup buffers until process exits)
  cat <<EOF >&3
{
  "artefact_type": "ProcessedRecord",
  "artefact_payload": "$result",
  "summary": "Processed record $i"
}
EOF
done

# Pup automatically:
# 1. Buffers all 1000 outputs
# 2. Injects {"batch_size": "1000"} into each artefact's metadata
# 3. Creates all artefacts atomically via Lua script
```

**Synchronizer Configuration:**
```yaml
agents:
  aggregator:
    role: "Aggregator"
    image: "aggregator:latest"
    command: ["/app/aggregate.sh"]

    synchronize:
      ancestor_type: "DataBatch"
      wait_for:
        - type: "ProcessedRecord"
          count_from_metadata: "batch_size"
```

**Synchronizer Script:**
```bash
#!/bin/sh
input=$(cat)

# Extract all processed records
records=$(echo "$input" | jq -r '.descendant_artefacts')

# Count records
record_count=$(echo "$records" | jq 'length')

echo "Aggregating $record_count records..." >&2

# Aggregate data (example: concatenate)
aggregated_data=$(echo "$records" | jq -r '.[].payload' | paste -sd,)

cat <<EOF >&3
{
  "artefact_type": "AggregationReport",
  "artefact_payload": "{\"total_records\": $record_count, \"data\": \"$aggregated_data\"}",
  "summary": "Aggregated $record_count records"
}
EOF
```

---

### Example 2: Distributed Testing (Dynamic Shards)

**Use case:** Run tests on N shards (N determined by test suite size at runtime).

**Test Shard Producer:**
```bash
#!/bin/sh
# Discover tests and shard them dynamically
test_count=$(find /workspace/tests -name "*.test" | wc -l)
shard_size=10
shard_count=$((test_count / shard_size))

echo "Running $test_count tests across $shard_count shards..." >&2

for shard in $(seq 1 $shard_count); do
  # Run shard
  shard_result="shard-$shard-passed"

  cat <<EOF >&3
{
  "artefact_type": "TestShardResult",
  "artefact_payload": "$shard_result",
  "summary": "Test shard $shard completed"
}
EOF
done

# Pup injects: {"batch_size": "$shard_count"}
```

**Synchronizer Configuration:**
```yaml
agents:
  test-aggregator:
    role: "Test Aggregator"
    image: "test-agg:latest"
    command: ["/app/aggregate-tests.sh"]

    synchronize:
      ancestor_type: "TestRun"
      wait_for:
        - type: "TestShardResult"
          count_from_metadata: "batch_size"
```

**Synchronizer Script:**
```bash
#!/bin/sh
input=$(cat)

shards=$(echo "$input" | jq -r '.descendant_artefacts')
total_shards=$(echo "$shards" | jq 'length')
passed_shards=$(echo "$shards" | jq '[.[] | select(.payload | contains("passed"))] | length')

echo "Test Results: $passed_shards / $total_shards shards passed" >&2

if [ "$passed_shards" -eq "$total_shards" ]; then
  cat <<EOF >&3
{
  "artefact_type": "TestSuiteComplete",
  "artefact_payload": "all-tests-passed",
  "summary": "All $total_shards test shards passed"
}
EOF
else
  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Only $passed_shards / $total_shards shards passed",
  "summary": "Test suite failed"
}
EOF
fi
```

---

## Multi-Artefact Output

### How It Works

**Traditional (pre-M5.1):** Agent outputs ONE JSON object to FD 3.

**M5.1 (multi-artefact):** Agent outputs MULTIPLE JSON objects to FD 3 (buffer-and-flush).

**Pup behavior:**
1. **Buffers** all FD 3 output until process exits
2. **Parses** multiple JSON objects from buffer
3. **Injects** `{"batch_size": "N"}` metadata into each artefact
4. **Creates** all artefacts atomically via Lua script

### Example: Multi-File Generator

```bash
#!/bin/sh
# Agent that generates 3 files

cd /workspace

# Generate file 1
cat > file1.txt <<EOF
Content 1
EOF

git add file1.txt
git commit -m "Generated file1"
commit1=$(git rev-parse HEAD)

# Output artefact 1 to FD 3 (buffered)
cat <<EOF >&3
{
  "artefact_type": "CodeCommit",
  "artefact_payload": "$commit1",
  "summary": "Generated file1.txt"
}
EOF

# Generate file 2
cat > file2.txt <<EOF
Content 2
EOF

git add file2.txt
git commit -m "Generated file2"
commit2=$(git rev-parse HEAD)

# Output artefact 2 to FD 3 (buffered)
cat <<EOF >&3
{
  "artefact_type": "CodeCommit",
  "artefact_payload": "$commit2",
  "summary": "Generated file2.txt"
}
EOF

# Generate file 3
cat > file3.txt <<EOF
Content 3
EOF

git add file3.txt
git commit -m "Generated file3"
commit3=$(git rev-parse HEAD)

# Output artefact 3 to FD 3 (buffered)
cat <<EOF >&3
{
  "artefact_type": "CodeCommit",
  "artefact_payload": "$commit3",
  "summary": "Generated file3.txt"
}
EOF

# Pup processes all 3 outputs after this script exits
# Each artefact gets metadata: {"batch_size": "3"}
```

**Result:** 3 `CodeCommit` artefacts created, all with:
- Same `source_artefacts` (parent)
- Metadata: `{"batch_size": "3"}`
- Different payloads (commit1, commit2, commit3)

### Downstream Synchronizer

```yaml
agents:
  file-verifier:
    role: "File Verifier"
    image: "verifier:latest"
    command: ["/app/verify.sh"]

    synchronize:
      ancestor_type: "Goal"
      wait_for:
        - type: "CodeCommit"
          count_from_metadata: "batch_size"
```

**Synchronizer will wait until all 3 commits exist before bidding.**

---

## Advanced Topics

### Mixed Patterns (Named + Producer-Declared)

You can mix patterns in one synchronizer:

```yaml
synchronize:
  ancestor_type: "Project"
  wait_for:
    - type: "DesignDoc"           # Named: exactly 1
    - type: "TestResult"          # Producer-Declared: N from metadata
      count_from_metadata: "test_count"
    - type: "SecurityScan"        # Named: exactly 1
```

**Behavior:** Wait for 1 DesignDoc + N TestResults + 1 SecurityScan.

---

### Recursive Descendant Traversal

By default, synchronizers find descendants at **any depth** below the ancestor.

**Example:**
```
CodeCommit (ancestor)
  └── BuildResult
        └── TestResult (grandchild)
```

Synchronizer configured to wait for `TestResult` will find it at depth 2.

**Limiting depth:**
```yaml
synchronize:
  ancestor_type: "CodeCommit"
  wait_for:
    - type: "TestResult"
  max_depth: 1  # Only search direct children
```

**Use `max_depth` when:**
- Deep artefact graphs (>10 levels)
- Performance optimization needed
- You know prerequisites are direct children

---

### Multiple Synchronizers on Same Ancestor

**Valid scenario:**
```yaml
agents:
  quick-deployer:
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"

  full-deployer:
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"
        - type: "SecurityScan"
```

**Behavior:**
- `quick-deployer` bids when TestResult exists
- `full-deployer` bids when TestResult + SecurityScan exist
- Both can execute (different claims, different triggers)

---

### Synchronizer Context Structure

**Standard agent input:**
```json
{
  "claim_type": "exclusive",
  "target_artefact": { /* artefact being processed */ },
  "context_chain": [ /* historical context */ ]
}
```

**Synchronizer input (additional fields):**
```json
{
  "claim_type": "exclusive",
  "target_artefact": { /* the final trigger artefact */ },
  "context_chain": [ /* full historical context */ ],
  "ancestor_artefact": { /* the common ancestor */ },
  "descendant_artefacts": [ /* ALL matched descendants */ ]
}
```

**Accessing in shell script:**
```bash
#!/bin/sh
input=$(cat)

# Standard fields
claim_type=$(echo "$input" | jq -r '.claim_type')
target=$(echo "$input" | jq -r '.target_artefact')

# Synchronizer-specific fields
ancestor=$(echo "$input" | jq -r '.ancestor_artefact')
descendants=$(echo "$input" | jq -r '.descendant_artefacts')

# Process synchronization data
ancestor_id=$(echo "$ancestor" | jq -r '.id')
descendant_count=$(echo "$descendants" | jq 'length')

echo "Synchronizing $descendant_count descendants of $ancestor_id" >&2
```

---

## Known Limitations

### 1. Deadlock on Pup Crash (10-minute TTL)

**Scenario:** Pup acquires deduplication lock, then crashes before bidding.

**Impact:** Workflow stalls for 10 minutes (lock TTL).

**Mitigation:**
- Monitor for orphaned locks: `redis-cli KEYS "holt:*:sync_dedup:*"`
- Manual cleanup: `redis-cli DEL holt:inst:sync_dedup:{ancestor_id}:{role_hash}`
- Wait for TTL expiry (automatic recovery)

**Example:**
```bash
# Check for orphaned locks
docker exec holt-default-1-redis redis-cli KEYS "holt:default-1:sync_dedup:*"

# Delete specific lock (if you're sure it's orphaned)
docker exec holt-default-1-redis redis-cli DEL "holt:default-1:sync_dedup:abc-123:def456"
```

---

### 2. Partial Fan-In Hang (No Timeout)

**Scenario:** 4 of 5 shards complete, 1 crashes. Synchronizer waits forever.

**Impact:** Workflow never completes (no automatic failure detection).

**Mitigation:**
- Monitor workflows: `holt watch`
- Manual intervention: Create Terminal artefact or restart instance
- Design for retry: Upstream agents should handle failures

**Example:**
```bash
# Identify stuck workflow
holt hoard | grep -A 5 "ProcessedRecord"
# Shows 4 records when expecting 5

# Option 1: Manually create Terminal artefact
holt forage --goal "terminate-workflow"

# Option 2: Restart instance (clears state)
holt down && holt up
```

---

### 3. Reverse Index Unbounded Growth

**Scenario:** Long-running instance (weeks) creates millions of artefacts.

**Impact:** Redis memory usage increases (~50 bytes per parent-child relationship).

**Mitigation:**
- Periodic instance recycling (recommended: daily/weekly)
- Monitor Redis memory: `docker exec holt-{inst}-redis redis-cli INFO memory`
- Long-term: Use `holt gc` (future feature)

**Example:**
```bash
# Check Redis memory usage
docker exec holt-default-1-redis redis-cli INFO memory | grep used_memory_human

# Count reverse index keys
docker exec holt-default-1-redis redis-cli KEYS "holt:default-1:index:children:*" | wc -l

# Recycle instance (clears all state)
holt down --name default-1
holt up --name default-2
```

---

### 4. No Cross-Ancestor Synchronization

**Limitation:** Synchronizers wait for descendants of **one** ancestor, not multiple.

**Not supported:**
```
CodeCommit-A          CodeCommit-B
    ↓                     ↓
TestResult-A          TestResult-B
         \                /
          [Can't synchronize on BOTH]
```

**Workaround:** Use intermediate merge artefact.

---

### 5. Metadata Missing or Invalid

**Scenario:** Producer Pup crashes before injecting metadata, or metadata corrupted.

**Impact:** Synchronizer cannot determine expected count, hangs.

**Mitigation:**
- Pup injects metadata atomically (unlikely to fail)
- Validate metadata in producer agent (defensive)
- Monitor for missing metadata: Check logs for "metadata key not found"

---

## Troubleshooting

### Synchronizer Never Bids

**Symptoms:** Prerequisites complete, but synchronizer doesn't execute.

**Causes:**
1. Ancestor not found in provenance chain
2. Not all dependencies met
3. Lock already held (race condition)
4. Configuration error

**Debug steps:**
```bash
# Check synchronizer logs
holt logs {synchronizer-agent}

# Look for:
#   "No ancestor of type 'X' found"
#   "Not all dependencies met"
#   "Lock already held"

# Verify ancestor exists
holt hoard | grep -A 5 "{ancestor_type}"

# Verify descendants exist
holt hoard | grep -A 5 "{descendant_type}"

# Check reverse index (Redis)
docker exec holt-{inst}-redis redis-cli SMEMBERS "holt:{inst}:index:children:{ancestor_id}"
```

---

### Synchronizer Bids Too Early

**Symptoms:** Synchronizer executes before all dependencies complete.

**Causes:**
1. Wrong `wait_for` configuration
2. Metadata count incorrect
3. Duplicate artefacts (same type created twice)

**Debug steps:**
```bash
# Check synchronizer configuration
cat holt.yml | grep -A 10 synchronize

# Verify dependency count
holt hoard | grep -c "{descendant_type}"

# Check metadata (Producer-Declared pattern)
docker exec holt-{inst}-redis redis-cli HGET "holt:{inst}:artefact:{id}" metadata
# Should show: {"batch_size": "N"}
```

---

### Duplicate Bids (Race Condition)

**Symptoms:** Two synchronizers bid on same ancestor simultaneously.

**Expected:** Deduplication lock prevents this.

**Debug steps:**
```bash
# Check orchestrator logs for duplicate bids
holt logs orchestrator | grep "Received bid"

# Verify lock acquisition
holt logs {synchronizer} | grep "Lock acquired"
holt logs {synchronizer} | grep "Lock already held"

# Check lock in Redis
docker exec holt-{inst}-redis redis-cli GET "holt:{inst}:sync_dedup:{ancestor_id}:{role_hash}"
# Returns "1" if lock held
```

**If deduplication fails:** Report as bug (lock mechanism failure).

---

### Metadata Not Found Error

**Symptoms:**
```
[Synchronizer] Failed to read metadata 'batch_size': key not found
```

**Causes:**
1. Producer agent didn't output multiple artefacts (no metadata injection)
2. Metadata key mismatch in configuration
3. Producer Pup version too old (pre-M5.1)

**Debug steps:**
```bash
# Check artefact metadata
docker exec holt-{inst}-redis redis-cli HGET "holt:{inst}:artefact:{id}" metadata

# Should return: {"batch_size": "N"}
# If returns: {}, producer didn't create multiple artefacts

# Verify configuration
cat holt.yml | grep count_from_metadata
# Key must match metadata field name

# Rebuild producer agent with M5.1 Pup
docker build -t producer:latest -f agents/producer/Dockerfile .
holt down && holt up
```

---

### Descendant Not Found (max_depth)

**Symptoms:** Synchronizer never bids, but descendant exists deep in graph.

**Cause:** `max_depth` limit stops traversal too early.

**Debug steps:**
```bash
# Check synchronizer configuration
cat holt.yml | grep max_depth

# Verify descendant depth
holt hoard | grep -B 10 -A 5 "{descendant_type}"
# Count how many "source_artefacts" hops from ancestor

# Increase max_depth or remove limit
# holt.yml:
synchronize:
  max_depth: 0  # Unlimited
```

---

## Next Steps

1. **Read the design document:** `../reference/architecture.md`
2. **Study examples:** `agents/example-deployer-agent/` and `agents/example-batch-aggregator-agent/`
3. **Build your first synchronizer:** Start with Named pattern (simpler)
4. **Test with `holt watch`:** Observe synchronization in real-time
5. **Read troubleshooting:** `docs/guides/troubleshooting.md` (M5.1 section)

---

## Reference

- **Agent Development Guide:** [agent-development.md](./agent-development.md)
- **Troubleshooting:** [troubleshooting.md](./troubleshooting.md)
- **M5.1 Design Document:** `../reference/architecture.md`
- **Example Agents:** `agents/example-deployer-agent/`, `agents/example-batch-aggregator-agent/`
