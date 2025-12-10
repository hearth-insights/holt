#!/bin/bash
set -e

# Input validation
if [ -z "$1" ]; then
    INPUT_JSON=$(cat)
else
    INPUT_JSON="$1"
fi

# Extract artefact type
echo "Input JSON: $INPUT_JSON" >&2
ARTEFACT_TYPE=$(echo "$INPUT_JSON" | jq -r 'if .structural_type == "Question" then .artefact_payload.artefact_type else .type end')
echo "Extracted ARTEFACT_TYPE: '$ARTEFACT_TYPE'" >&2

# Logic for TestRunner: GoalDefined -> TestResults
if [ "$ARTEFACT_TYPE" = "GoalDefined" ]; then
    echo "Running tests..." >&2
    sleep 2 # Simulate tests running
    
    # Output TestResults
    cat <<EOF >&3
{
  "type": "TestResults",
  "payload": {
    "status": "passed",
    "coverage": "98%",
    "unit_tests": "150/150 passed",
    "integration_tests": "24/24 passed"
  }
}
EOF
    exit 0
fi

# Fallback
echo "TestRunner received unexpected type: $ARTEFACT_TYPE" >&2
exit 0
