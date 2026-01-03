#!/bin/bash
# M5.2 Test: Reviewer agent
# Creates ReviewResult artefact from HPOMappingResult input

set -e
INPUT=$(cat)

# Extract the payload content (not the entire payload object)
MAPPING=$(echo "$INPUT" | jq -r '.target_artefact.payload.content')

# Use jq to construct JSON safely (handles escaping)
jq -n --arg mapping "$MAPPING" \
  '{
    artefact_type: "ReviewResult",
    artefact_payload: ("Reviewed: " + $mapping + " - APPROVED"),
    summary: "Review completed"
  }' >&3
