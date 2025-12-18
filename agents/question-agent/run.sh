#!/bin/sh
# M4.1: Test agent that produces Question artefacts
# This agent asks a clarifying question about GoalDefined artefacts only

# Read JSON input from stdin
input=$(cat)

# Extract target artefact info using jq (robust for V2)
target_id=$(echo "$input" | jq -r '.target_artefact.id // .target_artefact.header.id')
target_type=$(echo "$input" | jq -r '.target_artefact.header.type // .target_artefact.type')
target_structural_type=$(echo "$input" | jq -r '.target_artefact.header.structural_type // .target_artefact.structural_type')

# Check for parent hashes (V2) or source artefacts (V1)
# API returns parent_hashes in header for V2
has_parents=$(echo "$input" | jq -r 'if (.target_artefact.header.parent_hashes | length) > 0 then "yes" else "no" end')

# Log to stderr
echo "Question agent received claim" >&2
echo "Target artefact: $target_id" >&2
echo "Target type: $target_type" >&2
echo "Target structural_type: $target_structural_type" >&2
echo "Has parents: $has_parents" >&2

# Only ask questions about ROOT GoalDefined artefacts (no parents)
# If it has parents, it's a clarification (v2+), so we accept it.
if [ "$target_type" = "GoalDefined" ] && [ "$target_structural_type" = "Standard" ] && [ "$has_parents" = "no" ]; then
  echo "Asking question about original GoalDefined artefact (no parents)..." >&2

  # M4.10: Output Question artefact to FD 3
  cat <<EOF >&3
{
  "structural_type": "Question",
  "artefact_type": "ClarificationNeeded",
  "artefact_payload": "{\"question_text\": \"Is null handling in scope for this API?\", \"target_artefact_id\": \"$target_id\"}",
  "summary": "Agent needs clarification on requirements"
}
EOF
else
  echo "Not a root GoalDefined artefact (type=$target_type parents=$has_parents), producing standard work artefact..." >&2

  # M4.10: For non-GoalDefined or clarified artefacts, output standard acknowledgement to FD 3
  cat <<EOF >&3
{
  "artefact_type": "Acknowledged",
  "artefact_payload": "Processed artefact $target_id",
  "summary": "Agent acknowledged the artefact"
}
EOF
fi
