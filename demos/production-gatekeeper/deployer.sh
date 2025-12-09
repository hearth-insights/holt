#!/bin/bash
set -e

# Input validation
if [ -z "$1" ]; then
    INPUT_JSON=$(cat)
else
    INPUT_JSON="$1"
fi

ARTEFACT_TYPE=$(echo "$INPUT_JSON" | jq -r 'if .structural_type == "Question" then .artefact_payload.artefact_type else .type end')
echo "Deployer received: $ARTEFACT_TYPE" >&2

# Logic: DeploymentManifest -> DeploymentComplete
if [ "$ARTEFACT_TYPE" = "DeploymentManifest" ]; then
    echo "Deploying manifest..." >&2
    sleep 2
    
    cat <<EOF >&3
{
  "type": "DeploymentComplete",
  "payload": {
    "status": "success",
    "url": "https://production.example.com"
  }
}
EOF
    exit 0
fi

# Fallback
echo "Deployer received unexpected input: $ARTEFACT_TYPE" >&2
exit 0
