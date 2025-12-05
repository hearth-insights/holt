# SecurityScannerAgent

**Purpose:** Automated security scanning for ChangeSet artefacts (M4.5)

**Type:** Review agent (validates ChangeSet artefacts by running security scans)

## Overview

The SecurityScannerAgent is a simple shell script that:
1. Receives ChangeSet artefacts containing git commit hashes
2. Checks out the specified commit
3. Runs `gosec ./...` to scan for security vulnerabilities
4. Creates a Review artefact with scan results

## Contract

### Input (stdin JSON):
```json
{
  "claim_type": "review",
  "target_artefact": {
    "type": "ChangeSet",
    "payload": "{\"commit_hash\": \"abc123def456\", ...}"
  },
  "context_chain": [...]
}
```

### Output (FD 3 JSON):

**Success (no issues found):**
```json
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "SecurityScanner: No security issues found",
  "structural_type": "Review"
}
```

**Failure (issues found):**
```json
{
  "artefact_type": "Review",
  "artefact_payload": "{\"security_issues\": \"[HIGH] file.go:42 - SQL injection risk...\"}",
  "summary": "SecurityScanner: Found 3 security issues",
  "structural_type": "Review"
}
```

## Configuration

**holt.yml example:**
```yaml
agents:
  SecurityScanner:
    image: holt/security-scanner:latest
    command: ["/app/scan.sh"]
    bidding_strategy: "review"
    workspace:
      mode: ro
```

## Building

```bash
docker build -t holt/security-scanner:latest -f agents/security-scanner/Dockerfile .
```

## Testing Locally

```bash
# Create test input
echo '{
  "claim_type": "review",
  "target_artefact": {
    "type": "ChangeSet",
    "payload": "{\"commit_hash\": \"HEAD\"}"
  }
}' | docker run -i --rm -v $(pwd):/workspace holt/security-scanner:latest /app/scan.sh
```

## Requirements

- Git repository with a valid commit hash
- Go code to scan (gosec is Go-specific)
- Workspace mounted as `/workspace`

## Security Tool

This agent uses [gosec](https://github.com/securego/gosec) to scan for common security issues in Go code, including:
- SQL injection risks
- Hardcoded credentials
- Weak cryptography
- Path traversal vulnerabilities
- And more...

## Error Handling

- **Missing commit_hash:** Creates Review with feedback explaining invalid format
- **Git checkout fails:** Creates Review with feedback about missing commit
- **Scan fails to execute:** Creates Review with failure output
- **Non-ChangeSet artefact:** Ignores with approval Review
