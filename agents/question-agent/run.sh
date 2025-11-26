#!/bin/sh
# M4.1: Test agent that produces Question artefacts
# This agent asks a clarifying question about GoalDefined artefacts only

# Read JSON input from stdin
input=$(cat)

# Extract target artefact info
target_id=$(echo "$input" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
target_type=$(echo "$input" | grep -o '"type":"[^"]*"' | head -1 | cut -d'"' -f4)
target_structural_type=$(echo "$input" | grep -o '"structural_type":"[^"]*"' | head -1 | cut -d'"' -f4)
# Check if source_artefacts array is non-empty (contains at least one ID)
has_sources=$(echo "$input" | grep -o '"source_artefacts":\[[^]]*[a-f0-9-][^]]*\]')

# Log to stderr
echo "Question agent received claim" >&2
echo "Target artefact: $target_id" >&2
echo "Target type: $target_type" >&2
echo "Target structural_type: $target_structural_type" >&2

# Only ask questions about GoalDefined artefacts (not Questions, not other types)
# This prevents infinite Question loops
# Also skip if this artefact is already an answer to a question (has non-empty source_artefacts)
if [ "$target_type" = "GoalDefined" ] && [ "$target_structural_type" = "Standard" ] && [ -z "$has_sources" ]; then
  echo "Asking question about original GoalDefined artefact (no sources)..." >&2

  # Output Question artefact
  cat <<EOF
{
  "structural_type": "Question",
  "artefact_type": "ClarificationNeeded",
  "artefact_payload": "{\"question_text\": \"Is null handling in scope for this API?\", \"target_artefact_id\": \"$target_id\"}",
  "summary": "Agent needs clarification on requirements"
}
EOF
else
  echo "Not a GoalDefined artefact, producing standard work artefact instead..." >&2

  # For non-GoalDefined artefacts, just produce a standard acknowledgement
  cat <<EOF
{
  "artefact_type": "Acknowledged",
  "artefact_payload": "Processed artefact $target_id",
  "summary": "Agent acknowledged the artefact"
}
EOF
fi
