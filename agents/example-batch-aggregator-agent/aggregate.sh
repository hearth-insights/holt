#!/bin/sh
# Example Batch Aggregator Agent - Producer-Declared Pattern Synchronizer
#
# This agent demonstrates the Producer-Declared Pattern for fan-in synchronization.
# It waits for N ProcessedRecord artefacts (where N is determined at runtime from metadata)
# to exist as descendants of a DataBatch ancestor before aggregating.
#
# Use case: Data processing pipeline where a batch is split into N shards,
# processed in parallel, and then aggregated once all shards complete.

set -e

# Read synchronizer input from stdin
input=$(cat)

echo "================================================" >&2
echo "Batch Aggregator - Producer-Declared Pattern Example" >&2
echo "================================================" >&2

# Extract ancestor artefact (the DataBatch)
ancestor=$(echo "$input" | jq -r '.ancestor_artefact')
ancestor_type=$(echo "$ancestor" | jq -r '.type')
batch_id=$(echo "$ancestor" | jq -r '.payload')

echo "" >&2
echo "Ancestor Artefact:" >&2
echo "  Type: $ancestor_type" >&2
echo "  Batch ID: $batch_id" >&2

# Extract all descendant artefacts (the processed records)
descendants=$(echo "$input" | jq -r '.descendant_artefacts')
record_count=$(echo "$descendants" | jq 'length')

echo "" >&2
echo "Descendant Artefacts: $record_count ProcessedRecord(s)" >&2

# Verify count matches metadata expectation
# (The synchronizer already verified this, but we can double-check)
first_record=$(echo "$descendants" | jq -r '.[0]')
expected_count=$(echo "$first_record" | jq -r '.metadata' | jq -r '.batch_size')

echo "" >&2
echo "Batch Size Validation:" >&2
echo "  Expected (from metadata): $expected_count" >&2
echo "  Actual (received): $record_count" >&2

if [ "$record_count" != "$expected_count" ]; then
  echo "" >&2
  echo "⚠️  WARNING: Count mismatch! This should not happen if synchronizer is working correctly." >&2
fi

# Aggregate record data
echo "" >&2
echo "Aggregating records..." >&2

# Extract all record payloads
record_payloads=$(echo "$descendants" | jq -r '.[].payload' | paste -sd,)

# Calculate statistics (example: count successes/failures)
success_count=$(echo "$descendants" | jq '[.[] | select(.payload | contains("success"))] | length')
failure_count=$(echo "$descendants" | jq '[.[] | select(.payload | contains("failure"))] | length')

echo "" >&2
echo "Aggregation Statistics:" >&2
echo "  Total Records: $record_count" >&2
echo "  Successful: $success_count" >&2
echo "  Failed: $failure_count" >&2

# Build aggregation report
report=$(cat <<EOF
{
  "batch_id": "$batch_id",
  "total_records": $record_count,
  "successful": $success_count,
  "failed": $failure_count,
  "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "aggregated_data": "$record_payloads"
}
EOF
)

echo "" >&2
echo "Aggregation Report:" >&2
echo "$report" | jq '.' >&2

echo "" >&2
echo "✅ Aggregation complete!" >&2

# Output AggregationReport artefact to FD 3
cat <<EOF >&3
{
  "artefact_type": "AggregationReport",
  "artefact_payload": $(echo "$report" | jq -c '.'),
  "summary": "Aggregated $record_count records from batch $batch_id (success: $success_count, failures: $failure_count)"
}
EOF
