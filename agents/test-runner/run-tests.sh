#!/bin/sh
# TestRunnerAgent: Runs automated tests on ChangeSet artefacts (M4.5)
# Contract: Reads JSON from stdin, outputs Review artefact to stdout

set -e

# Read stdin JSON
input=$(cat)

# Extract target artefact (the ChangeSet being reviewed)
target_artefact=$(echo "$input" | jq -r '.target_artefact')

# Extract artefact type - must be ChangeSet
artefact_type=$(echo "$target_artefact" | jq -r '.type')
if [ "$artefact_type" != "ChangeSet" ]; then
    # Not a ChangeSet - ignore
    cat <<EOF
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "TestRunner: Not a ChangeSet, ignoring",
  "structural_type": "Review"
}
EOF
    exit 0
fi

# Parse ChangeSet payload (it's a JSON string)
changeset_payload=$(echo "$target_artefact" | jq -r '.payload')

# Extract commit hash from ChangeSet
commit_hash=$(echo "$changeset_payload" | jq -r '.commit_hash')

if [ -z "$commit_hash" ] || [ "$commit_hash" = "null" ]; then
    # Missing commit hash - create failure Review
    cat <<EOF
{
  "artefact_type": "Review",
  "artefact_payload": "{\"test_failures\": \"ChangeSet payload missing commit_hash field\"}",
  "summary": "TestRunner: Invalid ChangeSet format",
  "structural_type": "Review"
}
EOF
    exit 0
fi

echo "[TestRunner] Checking out commit: $commit_hash" >&2

# Navigate to workspace
cd /workspace

# Attempt git checkout
if ! git checkout "$commit_hash" 2>&1 >&2; then
    # Checkout failed
    cat <<EOF
{
  "artefact_type": "Review",
  "artefact_payload": "{\"test_failures\": \"Failed to checkout commit $commit_hash. Commit not found in repository.\"}",
  "summary": "TestRunner: Git checkout failed",
  "structural_type": "Review"
}
EOF
    exit 0
fi

echo "[TestRunner] Running: make test-failed" >&2

# Run tests and capture exit code
test_output=$(make test-failed 2>&1) || test_exit_code=$?

# Default to success if no exit code was captured
test_exit_code=${test_exit_code:-0}

if [ "$test_exit_code" -eq 0 ]; then
    # Tests passed
    echo "[TestRunner] All tests passed" >&2
    cat <<EOF
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "TestRunner: All tests passed",
  "structural_type": "Review"
}
EOF
else
    # Tests failed - include error output in feedback
    # Escape special characters for JSON
    escaped_output=$(echo "$test_output" | jq -Rs .)
    echo "[TestRunner] Tests failed with exit code $test_exit_code" >&2
    cat <<EOF
{
  "artefact_type": "Review",
  "artefact_payload": "{\"test_failures\": $escaped_output}",
  "summary": "TestRunner: Tests failed",
  "structural_type": "Review"
}
EOF
fi
