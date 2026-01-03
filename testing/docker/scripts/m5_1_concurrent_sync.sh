#!/bin/bash
set -e
cat > /dev/null

# Simple output (deduplication lock prevents multiple executions)
cat <<RESULT >&3
{"artefact_type":"DeployResult","artefact_payload":"deployed","summary":"Deployment complete"}
RESULT
