#!/bin/sh
set -e

# Read input JSON
input=$(cat)

# Parse artefact type
type=$(echo "$input" | jq -r '.type')

# Only bid on GoalDefined artefacts
if [ "$type" = "GoalDefined" ]; then
    echo "exclusive"
else
    echo "ignore"
fi
