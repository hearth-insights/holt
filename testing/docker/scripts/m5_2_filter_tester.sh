#!/bin/bash
# M5.2 Test: Filter Tester agent (synchronizer)
# Verifies that descendant_artefacts contains ONLY wait_for types

set -e
INPUT=$(cat)

# Extract all artefact types received
TYPES=$(echo "$INPUT" | jq -r '.descendant_artefacts[].header.type' | sort | uniq | tr '\n' ',' | sed 's/,$//')

# Log for debugging
echo "FilterTester received types: $TYPES" >&2

cat <<RESULT >&3
{"artefact_type":"FilterTestResult","artefact_payload":"received_types:$TYPES","summary":"Filter test complete"}
RESULT
