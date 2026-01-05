#!/bin/bash
# M5.1 E2E Test: Aggregator (synchronizer)
# Waits for N ProcessedRecords (N from batch_size metadata)
# Creates ONE AggregatedReport and should NOT bid on its own output

set -e
INPUT=$(cat)

# Count accumulated artefacts
ACCUMULATED=$(echo "$INPUT" | jq -r '.accumulated_artefacts | length')

# Extract payloads from all accumulated records
PAYLOADS=$(echo "$INPUT" | jq -r '.accumulated_artefacts[].payload.content' | tr '\n' ', ' | sed 's/,$//')

# Create aggregated report
jq -n \
  --argjson count "$ACCUMULATED" \
  --arg payloads "$PAYLOADS" \
  '{
    artefact_type: "AggregatedReport",
    artefact_payload: ("Aggregated " + ($count|tostring) + " records: " + $payloads),
    summary: ("Aggregation complete: " + ($count|tostring) + " records")
  }' >&3

echo "Created AggregatedReport from $ACCUMULATED records" >&2
