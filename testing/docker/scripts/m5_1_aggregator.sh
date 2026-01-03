#!/bin/bash
set -e
INPUT=$(cat)
COUNT=$(echo "$INPUT" | jq -r '.descendant_artefacts | map(select(.header.type == "ProcessedRecord")) | length')

cat <<RESULT >&3
{"artefact_type":"AggregatedReport","artefact_payload":"Aggregated $COUNT records","summary":"Aggregation complete"}
RESULT
