# TestRunnerAgent

**Purpose:** Automated test execution for ChangeSet artefacts (M4.5)

**Type:** Review agent (validates ChangeSet artefacts by running tests)

## Overview

The TestRunnerAgent is a simple shell script that:
1. Receives ChangeSet artefacts containing git commit hashes
2. Checks out the specified commit
3. Runs `make test-failed` to execute tests
4. Creates a Review artefact with test results

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

**Success (tests pass):**
```json
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "TestRunner: All tests passed",
  "structural_type": "Review"
}
```

**Failure (tests fail):**
```json
{
  "artefact_type": "Review",
  "artefact_payload": "{\"test_failures\": \"<test output>\"}",
  "summary": "TestRunner: Tests failed",
  "structural_type": "Review"
}
```

## Configuration

**holt.yml example:**
```yaml
agents:
  TestRunner:
    image: holt/test-runner:latest
    command: ["/app/run-tests.sh"]
    bidding_strategy: "review"
    workspace:
      mode: ro
```

## Building

```bash
docker build -t holt/test-runner:latest -f agents/test-runner/Dockerfile .
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
}' | docker run -i --rm -v $(pwd):/workspace holt/test-runner:latest /app/run-tests.sh
```

## Requirements

- Git repository with a valid commit hash
- `make test-failed` target must exist in the repository
- Workspace mounted as `/workspace`

## Error Handling

- **Missing commit_hash:** Creates Review with feedback explaining invalid format
- **Git checkout fails:** Creates Review with feedback about missing commit
- **Tests fail to execute:** Creates Review with failure output
- **Non-ChangeSet artefact:** Ignores with approval Review
