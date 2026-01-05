#!/bin/bash
# M5.1 E2E Test: Multi-record producer
# Creates 5 ProcessedRecords with batch_size=5 metadata
# Used to test synchronizer output artefact filtering

set -e
INPUT=$(cat)

# Extract batch ID
BATCH_ID=$(echo "$INPUT" | jq -r '.target_artefact.payload.content // "unknown"')

# Create 5 ProcessedRecord artefacts
# Each has batch_size=5 in metadata to signal the synchronizer
for i in {1..5}; do
  jq -n \
    --arg batch_id "$BATCH_ID" \
    --argjson i "$i" \
    '{
      artefact_type: "ProcessedRecord",
      artefact_payload: ("Record " + ($i|tostring) + " from batch " + $batch_id),
      artefact_metadata: "{\"batch_size\": \"5\"}",
      summary: ("Processed record " + ($i|tostring))
    }' >&3
done

echo "Created 5 ProcessedRecords with batch_size=5 metadata" >&2
