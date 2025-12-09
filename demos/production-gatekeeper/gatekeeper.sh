#!/bin/bash
set -e

# Input validation
if [ -z "$1" ]; then
    INPUT_JSON=$(cat)
else
    INPUT_JSON="$1"
fi

# Extract artefact type and version
ARTEFACT_TYPE=$(echo "$INPUT_JSON" | jq -r 'if .structural_type == "Question" then .artefact_payload.artefact_type else .type end')
VERSION=$(echo "$INPUT_JSON" | jq -r '.version // 1')

echo "Gatekeeper received: $ARTEFACT_TYPE (v$VERSION)" >&2

# State 1: Fresh TestResults (V1) -> Validate and Ask Question
if [ "$ARTEFACT_TYPE" = "TestResults" ] && [ "$VERSION" -eq 1 ]; then
    echo "Validating test results..." >&2
    sleep 1
    
    echo "Requesting deployment approval..." >&2
    
    # Output Question with Direct Routing
    cat <<EOF >&3
{
  "artefact_type": "DeploymentRequest",
  "structural_type": "Question",
  "artefact_payload": {
     "question_text": "Tests passed. Authorise deployment to production? (yes/no)",
     "target_artefact_id": "$(echo "$INPUT_JSON" | jq -r '.id')",
     "routing": "human"
  }
}
EOF
    exit 0
fi

# State 2: Answered TestResults (V>1) -> Interpret Answer and Produce Manifest
if [ "$ARTEFACT_TYPE" = "TestResults" ] && [ "$VERSION" -gt 1 ]; then
    echo "Checking approval..." >&2
    
    # The ANSWER is in the payload of the V2 artefact
    ANSWER=$(echo "$INPUT_JSON" | jq -r '.payload // ""')
    
    if [ "$ANSWER" = "yes" ]; then
        echo "Deployment approved! Creating manifest..." >&2
        
        cat <<EOF >&3
{
  "type": "DeploymentManifest",
  "payload": {
    "manifest": "k8s-manifest-v1.yaml",
    "replicas": 3,
    "strategy": "rolling",
    "approved_by": "human"
  }
}
EOF
    else
        echo "Deployment rejected." >&2
        cat <<EOF >&3
{
  "type": "DeploymentAborted",
  "payload": {
    "reason": "User rejected deployment"
  }
}
EOF
    fi
    exit 0
fi

# Fallback
echo "Gatekeeper received unexpected input" >&2
exit 0
