#!/bin/sh
# Example Deployer Agent - Named Pattern Synchronizer
#
# This agent demonstrates the Named Pattern for fan-in synchronization.
# It waits for three specific artefact types (TestResult, LintResult, SecurityScan)
# to exist as descendants of a CodeCommit ancestor before deploying.
#
# Use case: CI/CD pipeline where deployment only proceeds after all checks pass.

set -e

# Read synchronizer input from stdin
input=$(cat)

echo "========================================" >&2
echo "Deployer Agent - Named Pattern Example" >&2
echo "========================================" >&2

# Extract ancestor artefact (the CodeCommit being deployed)
ancestor=$(echo "$input" | jq -r '.ancestor_artefact')
ancestor_type=$(echo "$ancestor" | jq -r '.type')
commit_hash=$(echo "$ancestor" | jq -r '.payload')

echo "" >&2
echo "Ancestor Artefact:" >&2
echo "  Type: $ancestor_type" >&2
echo "  Commit Hash: $commit_hash" >&2

# Extract all descendant artefacts (the prerequisites)
descendants=$(echo "$input" | jq -r '.descendant_artefacts')
descendant_count=$(echo "$descendants" | jq 'length')

echo "" >&2
echo "Descendant Artefacts: $descendant_count" >&2

# Extract each prerequisite artefact
test_result=$(echo "$descendants" | jq -r '.[] | select(.type=="TestResult") | .payload')
lint_result=$(echo "$descendants" | jq -r '.[] | select(.type=="LintResult") | .payload')
scan_result=$(echo "$descendants" | jq -r '.[] | select(.type=="SecurityScan") | .payload')

echo "" >&2
echo "Prerequisites:" >&2
echo "  TestResult:   $test_result" >&2
echo "  LintResult:   $lint_result" >&2
echo "  SecurityScan: $scan_result" >&2

# Verify all prerequisites passed
# (In a real deployment, you'd check actual status/content)
all_passed=true

if echo "$test_result" | grep -iq "fail"; then
  echo "" >&2
  echo "❌ Tests FAILED - aborting deployment" >&2
  all_passed=false
fi

if echo "$lint_result" | grep -iq "fail"; then
  echo "" >&2
  echo "❌ Linting FAILED - aborting deployment" >&2
  all_passed=false
fi

if echo "$scan_result" | grep -iq "fail"; then
  echo "" >&2
  echo "❌ Security scan FAILED - aborting deployment" >&2
  all_passed=false
fi

if [ "$all_passed" = "false" ]; then
  # Deployment aborted due to failures
  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Deployment aborted: one or more prerequisites failed (tests=$test_result, lint=$lint_result, scan=$scan_result)",
  "summary": "Deployment aborted due to failed prerequisites"
}
EOF
  exit 0
fi

# All prerequisites passed - proceed with deployment
echo "" >&2
echo "✅ All prerequisites passed - proceeding with deployment" >&2

# Simulate deployment process
deployment_id="deploy-$(date +%s)"
deployment_timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "" >&2
echo "Deployment Details:" >&2
echo "  Deployment ID: $deployment_id" >&2
echo "  Timestamp: $deployment_timestamp" >&2
echo "  Commit: $commit_hash" >&2

# In a real deployment, you would:
# - Check out the commit
# - Build artifacts
# - Deploy to staging/production
# - Run smoke tests
# - Update deployment records

echo "" >&2
echo "✅ Deployment successful!" >&2

# Output DeploymentComplete artefact to FD 3
cat <<EOF >&3
{
  "artefact_type": "DeploymentComplete",
  "artefact_payload": "$deployment_id",
  "summary": "Deployed commit $commit_hash (tests: passed, lint: passed, security: passed)"
}
EOF
