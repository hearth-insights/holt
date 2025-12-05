# Holt Cryptographic Verification Standard

**Purpose**: Technical specification of the Holt Merkle DAG and verification procedures.
**Audience**: External auditors, security architects, and forensic investigators.
**Classification**: Technical Reference

---

## 1. Architectural Overview

Holt utilizes a **content-addressable Merkle DAG (Directed Acyclic Graph)** architecture for its audit trail. Unlike traditional append-only logs, every record in Holt is cryptographically bound to its content and its history.

### 1.1 The Merkle Guarantee
Every artefact in the system is identified by a **SHA-256** hash of its canonicalised content. This hash includes:
1.  **The Header**: Metadata, timestamps, and—crucially—the hashes of all parent artefacts.
2.  **The Payload**: The actual work content (or a pointer to it).

Because every child artefact includes the hash of its parent(s), it is mathematically impossible to modify a historical record without invalidating the hashes of all subsequent records. This provides a tamper-evident chain of custody.

### 1.2 Prover/Verifier Model
Holt enforces integrity via a strict separation of concerns:
*   **The Prover (Agent)**: Computes the hash of its work locally and submits it.
*   **The Verifier (Orchestrator)**: Independently re-computes the hash upon receipt. If the computed hash differs from the submitted ID by even one bit, the system triggers a **Global Lockdown**.

---

## 2. Technical Specification

### 2.1 Storage Format
Artefacts are stored in Redis using their hash as the key.

```
Key format: holt:{instance}:artefact:{sha256_hash}
```

### 2.2 Canonicalisation (RFC 8785)
To ensure consistent hashing across different systems, Holt mandates **RFC 8785 (JSON Canonicalization Scheme)**. This ensures that JSON data is serialized deterministically (sorted keys, specific whitespace rules) before hashing.

### 2.3 Hash Construction
The ID of an artefact is calculated as:

```go
ID = SHA256( Canonicalise( Header ) + Canonicalise( Payload ) )
```

The **Header** contains:
*   `parent_hashes`: Array of SHA-256 hashes of parent artefacts.
*   `created_at_ms`: Unix timestamp (milliseconds). **Modifying the timestamp invalidates the hash.**
*   `produced_by_role`: The agent identity.
*   `context_for_roles`: Security scoping rules.

### 2.4 Payload Limits
*   **Inline Limit**: 1MB (1,048,576 bytes).
*   **Large Files**: Must be written to the immutable workspace (Git) and referenced by commit hash.

---

## 3. Independent Verification Procedure

Auditors do not need to trust the Holt runtime. Verification can be performed offline or via the CLI using only the raw data.

### 3.1 CLI Verification
The `verify` command fetches an artefact and re-runs the cryptographic proof.

```bash
holt verify <artefact-id>
```

**Output Analysis**:
*   `✓ Hash verification PASSED`: The content matches the ID. The record is mathematically intact.
*   `✗ Hash verification FAILED`: The content has been altered. The record is corrupt or tampered with.

### 3.2 Manual / Offline Verification
An auditor can export the raw data and verify it using standard tools (like `openssl`):

1.  **Export**: `holt hoard <id> --output json > artefact.json`
2.  **Canonicalise**: Use an RFC 8785 compliant tool to canonicalise the header and payload.
3.  **Hash**: Run `sha256sum` on the result.
4.  **Compare**: The output must match the artefact ID.

---

## 4. Security Events & Forensics

### 4.1 Global Lockdown
If the Orchestrator detects a hash mismatch or a missing parent (Orphan Block), it triggers a **Global Lockdown**.
*   **State**: All workflow processing halts immediately.
*   **Preservation**: Agent containers are **kept running** to allow for forensic analysis of memory and disk state.
*   **Alerting**: A high-priority alert is pushed to the `holt:{instance}:security:alerts:log`.

### 4.2 Timestamp Drift
To prevent "pre-dating" attacks, the Orchestrator rejects artefacts with timestamps significantly divergent from the system clock (Default tolerance: 5 minutes).

---

## 5. References
*   **RFC 8785**: https://datatracker.ietf.org/doc/html/rfc8785
*   **NIST FIPS 180-4**: Secure Hash Standard (SHA-256)