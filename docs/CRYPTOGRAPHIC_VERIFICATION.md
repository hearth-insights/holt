# Cryptographic Verification in Holt

**Purpose**: Explains the M4.6 cryptographic verification system for compliance officers, auditors, and security teams.

**Audience**: External auditors, compliance officers, security architects, and forensic investigators.

**Status**: Production (M4.6 - Verifiable Artefact Ledger)

---

## Overview

Holt M4.6 introduces a **content-addressable Merkle DAG** architecture where every artefact's identity is its SHA-256 content hash. This provides cryptographic proof that the audit trail has not been tampered with after creation.

This is not an incremental security enhancement—it is a fundamental re-architecture designed to satisfy banking, defense, and healthcare regulatory requirements.

### Key Benefits

1. **Tamper Detection**: Any modification to artefact content invalidates the hash
2. **Independent Verification**: External auditors can verify the entire chain without trusting Holt
3. **DAG Integrity**: Orphan blocks are cryptographically impossible
4. **Forensic Investigation**: Global lockdown preserves evidence when tampering detected
5. **Audit Trail Immutability**: Every artefact is mathematically bound to its content and lineage

---

## Architecture

### Content-Addressable Storage

In V2 (M4.6+), artefacts are stored using their content hash as the key:

```
Redis Key: holt:{instance}:artefact:{sha256_hash}
Hash Format: 64 hex characters (lowercase)
Example: holt:prod:artefact:a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1
```

**Comparison with V1**:
- V1: `holt:{instance}:artefact:{uuid}` (36 characters, no integrity guarantee)
- V2: `holt:{instance}:artefact:{hash}` (64 characters, cryptographic integrity)

### Merkle DAG Structure

Artefacts form a Directed Acyclic Graph (DAG) where:
- Each artefact references its parent(s) by hash
- Root artefacts (e.g., GoalDefined) have empty `parent_hashes`
- Child artefacts reference parents by SHA-256 hash

```
GoalDefined (root)
  hash: a3f2b9c4...
  parent_hashes: []

CodeCommit (child)
  hash: b8c4e6d1...
  parent_hashes: ["a3f2b9c4..."]

ReviewApproval (child)
  hash: def456ab...
  parent_hashes: ["b8c4e6d1..."]
```

**Orphan Block Prevention**: The orchestrator validates that all parent hashes exist before accepting an artefact. Orphan blocks trigger a global lockdown.

---

## Hash Computation

### RFC 8785 Canonicalization

Holt uses **RFC 8785 (JSON Canonicalization Scheme)** to ensure deterministic serialization:

1. **Lexicographic key sorting**: Object keys sorted alphabetically
2. **No insignificant whitespace**: Compact JSON with no extra spaces
3. **Deterministic number representation**: IEEE 754 double precision
4. **Consistent Unicode escaping**: Standard escape sequences

**Why RFC 8785?**: It guarantees that the same data structure produces the same byte sequence across all implementations, platforms, and programming languages.

### Hash Algorithm

**Algorithm**: SHA-256 (FIPS 180-4)
**Output Format**: 64 hex characters (lowercase)
**Library**: Go stdlib `crypto/sha256`

### What is Hashed

The hash is computed over **Header + Payload** (ID field excluded):

```json
{
  "header": {
    "parent_hashes": ["..."],
    "logical_thread_id": "550e8400-e29b-41d4-a716-446655440000",
    "version": 2,
    "created_at_ms": 1704067200000,
    "produced_by_role": "go-coder-agent",
    "structural_type": "Standard",
    "type": "CodeCommit",
    "context_for_roles": []
  },
  "payload": {
    "content": "e3b0c442..."
  }
}
```

**Critical**: The `created_at_ms` timestamp is part of the hash. Modifying the timestamp invalidates the hash, preventing time-based attacks.

### Payload Size Limit

**Hard Limit**: 1MB (1,048,576 bytes)

**Rationale**:
- Redis performance: Sub-megabyte values recommended
- Hash computation: <5ms for 1MB on modern CPUs
- Audit trail readability: Large files should be referenced by hash, not stored inline

**For >1MB Content**:
1. Write content to workspace filesystem
2. Commit to Git (if persistence required)
3. Compute file SHA-256 hash
4. Store hash in payload: `{"file": "report.pdf", "hash": "e3b0c442..."}`

---

## Prover/Verifier Protocol

Holt implements a **challenge-response protocol** where:
- **Prover (Pup)**: Computes hash and submits as artefact ID
- **Verifier (Orchestrator)**: Independently recomputes and validates

### Pup (Prover) Side

1. Assemble artefact Header + Payload
2. Validate payload size ≤ 1MB
3. Canonicalize using RFC 8785
4. Compute SHA-256 hash
5. Set `artefact.ID = hash`
6. Submit to blackboard

```go
// Pup hash computation
hash, err := blackboard.ComputeArtefactHash(artefact)
artefact.ID = hash
client.WriteVerifiableArtefact(ctx, artefact)
```

### Orchestrator (Verifier) Side

When the orchestrator receives an artefact event, it performs **three-stage validation**:

#### Stage 1: Parent Existence Check (Orphan Block Prevention)

```go
for _, parentHash := range artefact.Header.ParentHashes {
    exists, _ := blackboard.ArtefactExists(ctx, parentHash)
    if !exists {
        // SECURITY EVENT: Orphan block detected
        triggerGlobalLockdown("orphan_block")
        return errors.New("parent hash not found")
    }
}
```

**Exception**: Root artefacts with `parent_hashes: []` are valid.

#### Stage 2: Timestamp Drift Validation (Clock Skew Detection)

```go
now := time.Now().UnixMilli()
tolerance := 5 * 60 * 1000 // 5 minutes

if artefact.Header.CreatedAtMs > now + tolerance {
    publishSecurityAlert("timestamp_drift")
    return errors.New("timestamp too far in future")
}
```

**Configurable Tolerance**: Set `orchestrator.timestamp_drift_tolerance_ms` in `holt.yml` (default: 300000ms = 5 minutes).

#### Stage 3: Hash Verification (Tampering Detection)

```go
computed, _ := blackboard.ComputeArtefactHash(artefact)
if computed != artefact.ID {
    // SECURITY EVENT: Hash mismatch (tampering)
    triggerGlobalLockdown("hash_mismatch")
    return &HashMismatchError{
        Expected: computed,
        Actual:   artefact.ID,
    }
}
```

**Performance**: Hash verification completes in <10ms including Redis fetch.

---

## Security Events

### Alert Types

Holt publishes four types of security alerts:

#### 1. `hash_mismatch` - Tampering Detected

**Trigger**: Artefact ID doesn't match computed hash
**Action**: Global lockdown
**Cause**: Malicious pup, memory corruption, or network MITM attack

**Example**:
```json
{
  "type": "hash_mismatch",
  "timestamp_ms": 1704067200000,
  "artefact_id_claimed": "a3f2b9c4...",
  "hash_expected": "def456ab...",
  "agent_role": "go-coder-agent",
  "orchestrator_action": "global_lockdown"
}
```

#### 2. `orphan_block` - DAG Corruption Detected

**Trigger**: Artefact references non-existent parent
**Action**: Global lockdown
**Cause**: Agent bug, race condition, or malicious agent

**Example**:
```json
{
  "type": "orphan_block",
  "timestamp_ms": 1704067200000,
  "artefact_id": "xyz789ab...",
  "missing_parent_hash": "abc123de...",
  "agent_role": "buggy-agent",
  "orchestrator_action": "global_lockdown"
}
```

#### 3. `timestamp_drift` - Clock Skew Detected

**Trigger**: Timestamp >5 minutes from orchestrator clock
**Action**: Rejected (no lockdown)
**Cause**: NTP failure, misconfigured container clock

**Example**:
```json
{
  "type": "timestamp_drift",
  "timestamp_ms": 1704067200000,
  "artefact_id": "drift123...",
  "drift_ms": 600000,
  "threshold_ms": 300000,
  "orchestrator_action": "rejected"
}
```

#### 4. `security_override` - Lockdown Cleared

**Trigger**: Manual unlock via `holt security --unlock`
**Action**: Lockdown cleared
**Cause**: Operator decision after forensic investigation

**Example**:
```json
{
  "type": "security_override",
  "timestamp_ms": 1704067800000,
  "action": "lockdown_cleared",
  "reason": "Investigation complete: memory corruption in agent container",
  "operator": "admin"
}
```

### Three-Step Alert Process

When a security event occurs, Holt executes three operations:

1. **LPUSH** to `holt:{instance}:security:alerts:log` (Redis LIST)
   - Permanent audit trail
   - Newest alerts first (LPUSH prepends)
   - No expiration

2. **SET** `holt:{instance}:security:lockdown` with alert payload
   - Circuit breaker state
   - Checked before every orchestrator operation
   - No expiration (manual clear required)

3. **PUBLISH** to `holt:{instance}:security:alerts` (Pub/Sub)
   - Real-time notification
   - Received by `holt security --alerts --watch`
   - Ephemeral (not stored)

---

## Global Lockdown

### When Lockdown Occurs

**Triggers**:
- Hash mismatch (tampering detected)
- Orphan block (DAG corruption detected)

**NOT Triggered By**:
- Timestamp drift (rejected but no lockdown)
- Security override (recovery operation)

### Lockdown Behavior

When locked down:
- ✅ Agent containers **remain running** (forensic evidence preserved)
- ❌ Orchestrator **halts all claim creation**
- ❌ Orchestrator **halts all grant operations**
- ✅ Redis **remains accessible** (audit log intact)
- ✅ CLI commands **remain functional** (investigation tools available)

**Rationale**: Preserving running containers allows forensic investigation of memory state, process state, and log files.

### Recovery Procedure

**Step 1: Investigation**
```bash
# View security alerts
holt security --alerts

# Inspect agent logs
holt logs <agent-role>

# Verify artefact hashes
holt verify <artefact-id>

# Export evidence
holt hoard <artefact-id> > evidence.json
```

**Step 2: Unlock (Audited)**
```bash
holt security --unlock --reason "Investigation complete: memory corruption detected in agent container, container replaced"
```

**Step 3: Verification**
```bash
# Check orchestrator resumes
holt watch

# Verify lockdown cleared
holt security --alerts
```

**Audit Trail**: Every unlock creates a `security_override` event in the permanent log with:
- Timestamp
- Operator identity
- Reason (required, plain text)
- Action taken

---

## Independent Verification

### CLI Verification Tool

External auditors can independently verify artefacts using:

```bash
holt verify <artefact-id>
```

**What it Does**:
1. Fetches artefact from Redis
2. Recomputes SHA-256 hash using RFC 8785
3. Compares with stored ID
4. Displays result

**Example Output (Success)**:
```
Verifying artefact a3f2b9c4e8d6...

✓ Hash verification PASSED

  Stored ID:    a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1
  Computed:     a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1

Artefact details:
  Type:         CodeCommit
  Producer:     go-coder-agent
  Created:      1704067200000 (Unix ms)
  Version:      2
  Parents:      [b8c4e6d1f7a3...]
  Payload size: 234 bytes
```

**Example Output (Failure)**:
```
Verifying artefact a3f2b9c4e8d6...

✗ Hash verification FAILED

  Stored ID:    a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1
  Computed:     def456ab1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8

CRITICAL: This artefact has been tampered with or corrupted!

Immediate actions:
  1. Check security alerts: holt security --alerts
  2. Inspect orchestrator logs: holt logs orchestrator
  3. Contact security team immediately
```

### Short Hash Resolution

For convenience, partial hashes can be used:

```bash
# Full hash (64 chars)
holt verify a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1

# Short hash (8+ chars)
holt verify a3f2b9c4
```

**Ambiguity Detection**: If multiple artefacts match the prefix, the CLI lists all matches and prompts for more characters.

### Verification Logic Location

**Critical**: The CLI uses the **same code** as the orchestrator:

```go
// Both orchestrator and CLI call this function
func ValidateArtefactHash(a *VerifiableArtefact) error {
    computed, _ := ComputeArtefactHash(a)
    if computed != a.ID {
        return &HashMismatchError{Expected: computed, Actual: a.ID}
    }
    return nil
}
```

**Location**: `pkg/blackboard/canonical.go`

This ensures auditors verify using identical logic to production.

---

## Monitoring Security Alerts

### View Historical Alerts

```bash
# All alerts in audit log
holt security --alerts

# Alerts from last hour
holt security --alerts --since=1h

# Alerts since specific time
holt security --alerts --since="2025-11-27T00:00:00Z"
```

### Stream Live Alerts

```bash
# Watch for new alerts (real-time)
holt security --alerts --watch

# Historical + live
holt security --alerts --since=24h --watch
```

**Pub/Sub Channel**: `holt:{instance}:security:alerts`

### Alert Log Storage

**Redis Key**: `holt:{instance}:security:alerts:log` (LIST)
**Order**: Newest first (LPUSH prepends)
**Retention**: Permanent (no TTL)
**Backup**: Recommended to export to SIEM or log aggregation system

---

## Compliance Audit Workflow

### Scenario: Quarterly Compliance Audit

**Step 1: Verify Recent Artefacts**
```bash
# List all artefacts from last quarter
holt hoard --since="2025-08-01T00:00:00Z" --output=jsonl > audit_q3.jsonl

# Verify each artefact hash
cat audit_q3.jsonl | jq -r '.id' | while read hash; do
  holt verify $hash || echo "FAIL: $hash" >> failed_hashes.txt
done
```

**Step 2: Check Security Alerts**
```bash
# Review all security events
holt security --alerts --since="2025-08-01T00:00:00Z" > security_audit.log

# Count alert types
grep '"type":' security_audit.log | sort | uniq -c
```

**Step 3: Verify DAG Integrity**
```bash
# Export artefact graph
holt hoard --output=jsonl | jq '{id, parent_hashes}' > dag_structure.json

# Verify all parents exist
cat dag_structure.json | jq -r '.parent_hashes[]?' | sort -u | while read parent; do
  holt verify $parent 2>&1 | grep -q PASSED || echo "Missing parent: $parent"
done
```

**Step 4: Generate Compliance Report**

Required evidence:
- ✅ All artefact hashes verified
- ✅ No hash_mismatch or orphan_block alerts
- ✅ All security_override events justified
- ✅ DAG structure complete (no missing parents)
- ✅ Timestamp drift within tolerance

---

## Troubleshooting

### Hash Verification Failures

**Symptom**: `holt verify` returns "Hash verification FAILED"

**Possible Causes**:
1. **Actual tampering**: Artefact content modified after creation
2. **Redis data corruption**: Rare but possible with disk failures
3. **Software bug**: Hash computation logic error (report to Holt team)

**Investigation Steps**:
1. Check security alerts: `holt security --alerts`
2. Verify Redis integrity: `redis-cli --rdb /path/to/dump.rdb`
3. Compare with Git-backed copy (if available)
4. Contact security team if tampering suspected

### Timestamp Drift Errors

**Symptom**: Artefacts rejected with "timestamp too far in future/past"

**Cause**: Clock skew between pup container and orchestrator >5 minutes

**Solution**:
```bash
# Check NTP sync status
timedatectl status

# Restart NTP service
sudo systemctl restart systemd-timesyncd

# Verify drift
holt logs orchestrator | grep timestamp_drift
```

**Configuration**: Adjust tolerance in `holt.yml`:
```yaml
orchestrator:
  timestamp_drift_tolerance_ms: 600000  # 10 minutes (looser for multi-region)
```

### Orphan Block Alerts

**Symptom**: Orchestrator triggers lockdown with "orphan_block" alert

**Possible Causes**:
1. **Agent bug**: Fabricated parent hash instead of reading from blackboard
2. **Race condition**: Parent deleted/expired before child submission (rare)
3. **Malicious agent**: Deliberately attempting to bypass DAG integrity

**Investigation Steps**:
1. Inspect alert: `holt security --alerts | jq 'select(.type=="orphan_block")'`
2. Check missing parent: `holt verify <missing_parent_hash>`
3. Review agent logs: `holt logs <agent_role>`
4. Verify agent context assembly logic

---

## Security Considerations

### Attack Scenarios

#### Scenario 1: Compromised Pup Attempts Tampering

**Attack**: Malicious pup submits artefact with hash ID `abc123...` but payload that hashes to `def456...`

**Detection**: Orchestrator Stage 3 validation detects mismatch within <10ms

**Response**:
- Hash mismatch alert published
- Global lockdown triggered
- Container preserved for forensics
- Malicious artefact never written to Redis

**Residual Risk**: None (if orchestrator verification implemented correctly)

#### Scenario 2: Compromised Orchestrator Accepts Invalid Hash

**Mitigation**: External auditors re-verify using `holt verify` CLI

**Detection**: Independent verification will detect mismatch

**Residual Risk**: If blackboard library itself is compromised, entire system compromised (standard software supply chain risk)

#### Scenario 3: MITM Attack on Redis Connection

**Risk**: High in untrusted network environments

**Mitigation** (Future Work):
- TLS for Redis connections
- Redis AUTH password
- Network isolation (orchestrator and Redis on same host)

**Current Status**: Out of scope for M4.6

#### Scenario 4: Timestamp Manipulation

**Attack**: Modify timestamp to evade drift detection

**Protection**: Timestamps are **part of the hash**. Modifying timestamp invalidates the hash.

**Residual Risk**: None

### Cryptographic Assumptions

**Hash Collision Resistance**: SHA-256 collision probability is 2^-256 (effectively impossible with current computing)

**If SHA-256 Broken**: Future work will add `hash_algorithm` field to ArtefactHeader for algorithm agility

---

## Performance Characteristics

### Hash Computation

**Pup Side**:
- 1KB payload: <1ms
- 1MB payload: <5ms
- Bottleneck: CPU-bound (SHA-256)

**Orchestrator Side**:
- Verification: <10ms (including Redis fetch)
- Throughput: >1000 artefacts/second on 8-core machine

### Storage Overhead

**Key Size**:
- V1 UUID: 36 bytes
- V2 Hash: 64 bytes
- Increase: +28 bytes per artefact (~78% larger)

**Redis Memory**:
- Marginal increase
- Hash IDs compress well in Redis (string interning)

### Network Overhead

**No Change**: Artefacts still transmitted as JSON over Redis Pub/Sub

---

## Glossary

- **Content-Addressable Storage**: Storage where data is retrieved by its content hash, not an arbitrary identifier
- **Merkle DAG**: Directed Acyclic Graph where each node is identified by the hash of its content and references to parent hashes
- **Prover/Verifier Protocol**: Challenge-response system where one party (prover) computes a proof and another (verifier) independently validates it
- **RFC 8785**: IETF standard for deterministic JSON canonicalization
- **Orphan Block**: Artefact that references a non-existent parent, creating a disconnected node in the DAG
- **Global Lockdown**: Emergency halt of orchestrator operations triggered by security events

---

## References

- **M4.6 Design Spec**: `/design/features/phase-4-human-loop/M4.6-verifiable-artefact-ledger.md`
- **RFC 8785**: https://datatracker.ietf.org/doc/html/rfc8785
- **FIPS 180-4 (SHA-256)**: https://csrc.nist.gov/publications/detail/fips/180/4/final
- **Merkle DAG**: https://docs.ipfs.tech/concepts/merkle-dag/

---

**Document Version**: 1.0
**Last Updated**: 2025-11-27
**Applies To**: Holt v2 (M4.6+)
**Status**: Production
