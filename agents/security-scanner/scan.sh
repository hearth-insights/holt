#!/bin/sh
# SecurityScannerAgent: Runs security scans on ChangeSet artefacts (M4.5)
# Contract: Reads JSON from stdin, outputs Review artefact to FD 3 (not stdout)

set -e

# Read stdin JSON
input=$(cat)

# Extract target artefact (the ChangeSet being reviewed)
target_artefact=$(echo "$input" | jq -r '.target_artefact')

# Extract artefact type - must be ChangeSet
artefact_type=$(echo "$target_artefact" | jq -r '.type')
if [ "$artefact_type" != "ChangeSet" ]; then
    # Not a ChangeSet - ignore
    cat <<EOF >&3
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "SecurityScanner: Not a ChangeSet, ignoring",
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
    cat <<EOF >&3
{
  "artefact_type": "Review",
  "artefact_payload": "{\"security_issues\": \"ChangeSet payload missing commit_hash field\"}",
  "summary": "SecurityScanner: Invalid ChangeSet format",
  "structural_type": "Review"
}
EOF
    exit 0
fi

echo "[SecurityScanner] Checking out commit: $commit_hash" >&2

# Navigate to workspace
cd /workspace

# Attempt git checkout
if ! git checkout "$commit_hash" 2>&1 >&2; then
    # Checkout failed
    cat <<EOF >&3
{
  "artefact_type": "Review",
  "artefact_payload": "{\"security_issues\": \"Failed to checkout commit $commit_hash. Commit not found in repository.\"}",
  "summary": "SecurityScanner: Git checkout failed",
  "structural_type": "Review"
}
EOF
    exit 0
fi

echo "[SecurityScanner] Running: gosec ./..." >&2

# Run gosec and capture exit code
# gosec exits with non-zero if issues are found
scan_output=$(gosec -fmt=json ./... 2>&1) || scan_exit_code=$?

# Default to success if no exit code was captured
scan_exit_code=${scan_exit_code:-0}

if [ "$scan_exit_code" -eq 0 ]; then
    # No security issues found
    echo "[SecurityScanner] No security issues found" >&2
    cat <<EOF >&3
{
  "artefact_type": "Review",
  "artefact_payload": "{}",
  "summary": "SecurityScanner: No security issues found",
  "structural_type": "Review"
}
EOF
else
    # Security issues found - include findings in feedback
    # Parse gosec JSON output to extract issue summaries
    issue_count=$(echo "$scan_output" | jq -r '.Stats.found // 0')

    # If gosec output is valid JSON, extract formatted summary
    if echo "$scan_output" | jq empty 2>/dev/null; then
        # Extract first 5 issues for summary
        issues_summary=$(echo "$scan_output" | jq -r '
            .Issues[:5] | map(
                "[\(.severity)] \(.file):\(.line) - \(.details)"
            ) | join("\n")
        ')
    else
        # Fallback if JSON parsing fails
        issues_summary="$scan_output"
    fi

    # Escape for JSON
    escaped_summary=$(echo "$issues_summary" | jq -Rs .)

    echo "[SecurityScanner] Found $issue_count security issues" >&2
    cat <<EOF >&3
{
  "artefact_type": "Review",
  "artefact_payload": "{\"security_issues\": $escaped_summary}",
  "summary": "SecurityScanner: Found $issue_count security issues",
  "structural_type": "Review"
}
EOF
fi
