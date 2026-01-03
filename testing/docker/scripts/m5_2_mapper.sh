#!/bin/bash
# M5.2 Test: Mapper agent
# Creates HPOMappingResult artefact from SubGoal input

set -e
INPUT=$(cat)

# Extract payload content
SUBGOAL=$(echo "$INPUT" | jq -r '.target_artefact.payload.content')
METADATA=$(echo "$INPUT" | jq -r '.target_artefact.header.metadata // "{}"')

# Use jq to construct JSON safely (handles escaping)
jq -n --arg subgoal "$SUBGOAL" \
  '{
    artefact_type: "HPOMappingResult",
    artefact_payload: ("Mapped: " + $subgoal),
    summary: "Mapping created"
  }' >&3
