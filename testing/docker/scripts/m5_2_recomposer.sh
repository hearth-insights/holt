#!/bin/bash
# M5.2 Test: Recomposer agent (synchronizer)
# Aggregates ReviewResult artefacts after all reviews complete

set -e
INPUT=$(cat)

# Count ReviewResult artefacts in descendant_artefacts
COUNT=$(echo "$INPUT" | jq -r '.descendant_artefacts | map(select(.header.type == "ReviewResult")) | length')

# Log what we received for debugging
echo "Recomposer received $COUNT ReviewResult artefacts" >&2
echo "$INPUT" | jq '.descendant_artefacts[].header.type' >&2

cat <<RESULT >&3
{"artefact_type":"FinalPatientProfile","artefact_payload":"Aggregated $COUNT reviews","summary":"Patient profile complete"}
RESULT
