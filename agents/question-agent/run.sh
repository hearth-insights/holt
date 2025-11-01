#!/bin/sh
# M4.1: Test agent that produces Question artefacts
# This agent always asks a clarifying question about its target artefact

# Read JSON input from stdin
input=$(cat)

# Extract target artefact ID from stdin using jq (if available) or simple grep
target_id=$(echo "$input" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

# Log to stderr
echo "Question agent received claim, asking question..." >&2
echo "Target artefact: $target_id" >&2

# Output Question artefact to stdout
# structural_type: "Question" tells the orchestrator this is a question
cat <<EOF
{
  "structural_type": "Question",
  "artefact_type": "ClarificationNeeded",
  "artefact_payload": "{\"question_text\": \"Is null handling in scope for this API?\", \"target_artefact_id\": \"$target_id\"}",
  "summary": "Agent needs clarification on requirements"
}
EOF
