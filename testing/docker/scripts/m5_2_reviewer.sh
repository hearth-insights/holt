#!/bin/bash
# M5.2 Test: Reviewer agent
# Creates ReviewResult artefact from HPOMappingResult input

set -e
INPUT=$(cat)

MAPPING=$(echo "$INPUT" | jq -r '.target_artefact.payload')

cat <<RESULT >&3
{"artefact_type":"ReviewResult","artefact_payload":"Reviewed: $MAPPING - APPROVED","summary":"Review completed"}
RESULT
