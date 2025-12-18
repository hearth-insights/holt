#!/bin/bash
set -e

# Read input from stdin (not used for this simple producer)
cat > /dev/null

# Produce a single artefact (CodeCommit trigger will create prerequisites)
cat <<RESULT >&3
{"artefact_type":"TriggerComplete","artefact_payload":"triggered","summary":"Producer triggered"}
RESULT
