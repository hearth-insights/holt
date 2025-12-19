#!/bin/sh
set -e

# Read input JSON
input=$(cat)

# Parse artefact type (handle V1 and V2)
type=$(echo "$input" | jq -r '.header.type // .type')

# Only bid on GoalDefined artefacts
if [ "$type" = "GoalDefined" ]; then
    echo "exclusive"
else
    echo "ignore"
fi
