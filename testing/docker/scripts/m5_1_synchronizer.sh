#!/bin/bash
set -e

# Read synchronizer context from stdin
INPUT=$(cat)

# Extract ancestor ID and descendant count (avoid embedding complex JSON)
ANCESTOR_ID=$(echo "$INPUT" | jq -r '.ancestor_artefact.id // "unknown"')
DESCENDANTS=$(echo "$INPUT" | jq -r '.descendant_artefacts | length')

# Create synchronized result with safe payload
cat <<RESULT >&3
{"artefact_type":"DeployResult","artefact_payload":"synchronized-ancestor-$ANCESTOR_ID-descendants-$DESCENDANTS","summary":"Deployment synchronized"}
RESULT
