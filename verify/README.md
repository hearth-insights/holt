# Holt Release Verification

All Holt releases are cryptographically signed using [Cosign](https://docs.sigstore.dev/cosign/overview/).

## Quick Verification

Run the automated verification script:

```bash
# For latest stable release
curl -sSfL https://raw.githubusercontent.com/hearth-insights/holt/main/verify/verify.sh | bash -s -- v1.0.0

# For rolling latest
curl -sSfL https://raw.githubusercontent.com/hearth-insights/holt/main/verify/verify.sh | bash -s -- latest
```

## Manual Verification (Air-Gapped)

For air-gapped environments, follow these steps:

### Prerequisites

- `cosign` CLI: https://docs.sigstore.dev/cosign/installation/
- `sha256sum` (typically pre-installed)

### Step 1: Download Public Key

Download once and store securely:

```bash
curl -sSfL https://raw.githubusercontent.com/hearth-insights/holt/main/verify/cosign.pub -o cosign.pub
```

### Step 2: Download Transparency Log Files

From `https://github.com/hearth-insights/holt/tree/main/releases/<version>/`:

- `checksums.txt` - SHA256 checksums of all release artifacts
- `checksums.txt.sig` - Cosign signature of checksums.txt

### Step 3: Verify Checksums Signature

```bash
cosign verify-blob --key cosign.pub --signature checksums.txt.sig checksums.txt
```

**Expected output:**
```
Verified OK
```

### Step 4: Download Binary

From GitHub release: `https://github.com/hearth-insights/holt-engine/releases/tag/<version>`

Example:
```bash
curl -sSfL https://github.com/hearth-insights/holt-engine/releases/download/v1.0.0/holt-linux-amd64 -o holt
curl -sSfL https://github.com/hearth-insights/holt-engine/releases/download/v1.0.0/holt-linux-amd64.sig -o holt.sig
```

### Step 5: Verify Binary Signature

```bash
cosign verify-blob --key cosign.pub --signature holt.sig holt
```

### Step 6: Verify Checksum

```bash
# Get expected checksum from transparency log
grep "holt-linux-amd64" checksums.txt

# Compute actual checksum
sha256sum holt

# Compare manually - they must match exactly
```

### Step 7: Verify Docker Image

```bash
# For specific version
cosign verify --key cosign.pub ghcr.io/hearth-insights/holt/holt-orchestrator:v1.0.0

# For latest
cosign verify --key cosign.pub ghcr.io/hearth-insights/holt/holt-orchestrator:latest
```

**Expected output:**
```
Verification for ghcr.io/hearth-insights/holt/holt-orchestrator:v1.0.0 --
The following checks were performed on each of these signatures:
  - The cosign claims were validated
  - The signatures were verified against the specified public key
```

## SBOM Verification

Software Bill of Materials (SBOM) files are also signed:

```bash
# Download SBOM and signature
curl -sSfL https://github.com/hearth-insights/holt/raw/main/releases/v1.0.0/holt.spdx.json -o holt.spdx.json
curl -sSfL https://github.com/hearth-insights/holt/raw/main/releases/v1.0.0/holt.spdx.json.sig -o holt.spdx.json.sig

# Verify
cosign verify-blob --key cosign.pub --signature holt.spdx.json.sig holt.spdx.json
```

## Compliance Notes

This verification process supports:

- **HIPAA §164.312(c)** - Integrity Controls: Cryptographic signatures provide tamper-evidence
- **SOC 2** - Non-repudiation: Signatures prove artifact origin
- **ISO 27001 A.8.16** - Audit trails: Transparency log provides complete history
- **Air-gapped deployment**: All verification can be performed offline

## Key Rotation

In the event of key compromise, a new public key will be published here with a signed transition statement from the old key.

Current key fingerprint:
```
[Run: cosign public-key --key cosign.pub]
```

## Questions?

See main documentation: https://github.com/hearth-insights/holt-engine/tree/main/docs
