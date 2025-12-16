#!/bin/sh
# Example Batch Producer Agent
#
# This agent demonstrates multi-artefact output (M5.1).
# It processes a batch of records and outputs multiple ProcessedRecord artefacts.
# The pup automatically injects {"batch_size": "N"} metadata into each artefact.
#
# Use case: Data processing pipeline that splits work into parallel tasks.

set -e

# Read input from stdin
input=$(cat)

echo "========================================" >&2
echo "Batch Producer - Multi-Artefact Output" >&2
echo "========================================" >&2

# Extract batch information from target artefact
target=$(echo "$input" | jq -r '.target_artefact')
batch_id=$(echo "$target" | jq -r '.payload')

echo "" >&2
echo "Processing batch: $batch_id" >&2

# Simulate processing multiple records
# In a real scenario, you'd:
# - Read data from storage
# - Split into N shards
# - Process each shard
# - Output one artefact per shard

# For this example, we'll process 10 records
BATCH_SIZE=10

echo "" >&2
echo "Batch contains $BATCH_SIZE records" >&2
echo "Processing each record..." >&2

for i in $(seq 1 $BATCH_SIZE); do
  echo "  Processing record $i..." >&2

  # Simulate processing (random success/failure)
  if [ $((i % 7)) -eq 0 ]; then
    # Occasional failure
    result="record-$i-failure"
    echo "    ❌ Record $i failed" >&2
  else
    # Success
    result="record-$i-success"
    echo "    ✅ Record $i processed successfully" >&2
  fi

  # Output ProcessedRecord artefact to FD 3
  # IMPORTANT: Pup buffers ALL outputs until process exits
  cat <<EOF >&3
{
  "artefact_type": "ProcessedRecord",
  "artefact_payload": "$result",
  "summary": "Processed record $i from batch $batch_id"
}
EOF
done

echo "" >&2
echo "✅ Batch processing complete!" >&2
echo "   Pup will now:" >&2
echo "   1. Buffer all $BATCH_SIZE outputs" >&2
echo "   2. Inject metadata: {\"batch_size\": \"$BATCH_SIZE\"}" >&2
echo "   3. Create all artefacts atomically" >&2
echo "" >&2

# When this script exits, the pup will:
# 1. Parse all $BATCH_SIZE JSON objects from FD 3
# 2. Inject {"batch_size": "10"} into each artefact's metadata
# 3. Create all artefacts atomically via Lua script
# 4. All artefacts will have same source_artefacts (this batch)

# The aggregator agent will then:
# 1. Read batch_size from metadata
# 2. Wait until all 10 ProcessedRecord artefacts exist
# 3. Aggregate the results
