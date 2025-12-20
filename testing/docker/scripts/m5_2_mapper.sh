#!/bin/bash
# M5.2 Test: Mapper agent
# Creates HPOMappingResult artefact from SubGoal input

set -e
INPUT=$(cat)

SUBGOAL=$(echo "$INPUT" | jq -r '.target_artefact.payload')
METADATA=$(echo "$INPUT" | jq -r '.target_artefact.header.metadata // "{}"')

cat <<RESULT >&3
{"artefact_type":"HPOMappingResult","artefact_payload":"Mapped: $SUBGOAL","summary":"Mapping created"}
RESULT
