# Example Batch Aggregator Agent - Producer-Declared Pattern Synchronizer

**Pattern**: Producer-Declared Pattern (M5.1)
**Use Case**: Data batch processing with dynamic parallelism and aggregation

---

## Overview

This agent demonstrates the **Producer-Declared Pattern** for fan-in synchronization. It includes two components:

1. **Producer** (`producer.sh`): Processes a batch and outputs N `ProcessedRecord` artefacts
2. **Aggregator** (`aggregate.sh`): Waits for N records (N from metadata) and aggregates results

The count N is **unknown at design time** - determined by the producer when processing the batch.

---

## How It Works

### Configuration (holt.yml)

⚠️ **Important:** `synchronize` is **MUTUALLY EXCLUSIVE** with `bidding_strategy` and `bid_script`.
- Producer uses `bidding_strategy` (standard agent)
- Aggregator uses `synchronize` (synchronizer agent)
- **Never use both on the same agent**

```yaml
agents:
  # Producer agent (creates multiple artefacts)
  batch-producer:
    role: "Batch Producer"
    image: "example-batch-producer:latest"
    command: ["/app/producer.sh"]
    bidding_strategy: "eager"  # ← Standard agent
    workspace:
      mode: ro

  # Aggregator agent (waits for all records using Producer-Declared pattern)
  batch-aggregator:
    role: "Batch Aggregator"
    image: "example-batch-aggregator:latest"
    command: ["/app/aggregate.sh"]

    # Synchronize block - Producer-Declared pattern
    # NOTE: Cannot use bidding_strategy here (mutually exclusive)
    synchronize:
      # Wait for descendants of this ancestor type
      ancestor_type: "DataBatch"

      # Wait for N artefacts (N from metadata)
      wait_for:
        - type: "ProcessedRecord"
          count_from_metadata: "batch_size"

    workspace:
      mode: ro
```

### Workflow

```
1. User creates DataBatch artefact (batch_id: "batch-001")
       ↓
2. Producer agent bids and wins
       ↓
3. Producer processes batch, outputs 10 ProcessedRecord JSON objects to FD 3
       ↓
4. Pup buffers all 10 outputs until process exits
       ↓
5. Pup injects {"batch_size": "10"} into each artefact's metadata
       ↓
6. Pup creates all 10 artefacts atomically
       ↓
7. Aggregator synchronizer detects records appearing
       ↓
8. Aggregator reads batch_size from first record's metadata (N = 10)
       ↓
9. Aggregator waits until 10 ProcessedRecord artefacts exist
       ↓
10. Aggregator bids exclusive on the final (10th) record
        ↓
11. Aggregator receives all 10 records in descendant_artefacts
        ↓
12. Aggregator calculates statistics and creates AggregationReport
```

---

## Key Features

### Multi-Artefact Output (Producer)

The producer demonstrates **M5.1 multi-artefact output**:

```bash
# Producer outputs multiple JSON objects to FD 3
for i in $(seq 1 10); do
  cat <<EOF >&3
{
  "artefact_type": "ProcessedRecord",
  "artefact_payload": "record-$i-success",
  "summary": "Processed record $i"
}
EOF
done

# Pup automatically:
# 1. Buffers all outputs
# 2. Counts them (10)
# 3. Injects {"batch_size": "10"} metadata
# 4. Creates all artefacts atomically
```

### Dynamic Count (Aggregator)

The aggregator reads the expected count from metadata:

```yaml
wait_for:
  - type: "ProcessedRecord"
    count_from_metadata: "batch_size"
```

**How it works:**
1. Pup loads first ProcessedRecord artefact
2. Reads `metadata` field: `{"batch_size": "10"}`
3. Waits until 10 ProcessedRecord artefacts exist
4. Bids when count matches

---

## Building and Running

### Build Images

```bash
# Build producer (if separate image needed)
# For simplicity, we'll use a combined image in this example

# Build aggregator
docker build -t example-batch-aggregator:latest \
  -f agents/example-batch-aggregator-agent/Dockerfile .
```

### Configure holt.yml

```yaml
version: "1.0"

agents:
  batch-producer:
    role: "Batch Producer"
    image: "example-batch-aggregator:latest"  # Can use same image
    command: ["/app/producer.sh"]
    bidding_strategy: "eager"
    workspace:
      mode: ro

  batch-aggregator:
    role: "Batch Aggregator"
    image: "example-batch-aggregator:latest"
    command: ["/app/aggregate.sh"]

    synchronize:
      ancestor_type: "DataBatch"
      wait_for:
        - type: "ProcessedRecord"
          count_from_metadata: "batch_size"

    workspace:
      mode: ro

services:
  redis:
    image: redis:7-alpine
```

### Run Workflow

```bash
# Start Holt
holt up

# Trigger batch processing
holt forage --goal "batch-001"

# Watch execution
holt watch

# Expected output:
# 1. DataBatch artefact created
# 2. Producer processes batch
# 3. 10 ProcessedRecord artefacts created (1, 2, 3, ..., 10)
# 4. Aggregator bids when 10th record appears
# 5. AggregationReport created

# View logs
holt logs batch-producer
holt logs batch-aggregator

# View results
holt hoard | grep AggregationReport
```

---

## Input Format

### Producer Input

Standard agent input:

```json
{
  "claim_type": "exclusive",
  "target_artefact": {
    "type": "DataBatch",
    "payload": "batch-001",
    ...
  },
  "context_chain": []
}
```

### Aggregator Input

Synchronizer input (includes metadata):

```json
{
  "claim_type": "exclusive",
  "target_artefact": {
    "type": "ProcessedRecord",
    "payload": "record-10-success",
    ...
  },
  "context_chain": [ /* ... */ ],

  "ancestor_artefact": {
    "type": "DataBatch",
    "payload": "batch-001",
    ...
  },

  "descendant_artefacts": [
    {
      "type": "ProcessedRecord",
      "payload": "record-1-success",
      "metadata": "{\"batch_size\": \"10\"}",
      ...
    },
    {
      "type": "ProcessedRecord",
      "payload": "record-2-success",
      "metadata": "{\"batch_size\": \"10\"}",
      ...
    },
    // ... (all 10 records)
  ]
}
```

---

## Output

### Producer Output (10 artefacts)

Each `ProcessedRecord` artefact:

```json
{
  "artefact_type": "ProcessedRecord",
  "artefact_payload": "record-1-success",
  "summary": "Processed record 1 from batch batch-001"
}
```

Pup adds to Redis:
```
metadata: {"batch_size": "10"}
```

### Aggregator Output

```json
{
  "artefact_type": "AggregationReport",
  "artefact_payload": {
    "batch_id": "batch-001",
    "total_records": 10,
    "successful": 9,
    "failed": 1,
    "timestamp": "2025-01-15T10:30:00Z",
    "aggregated_data": "record-1-success,record-2-success,..."
  },
  "summary": "Aggregated 10 records from batch batch-001 (success: 9, failures: 1)"
}
```

---

## Testing

### Unit Test Producer (Without Holt)

```bash
# Create test input
cat > test-producer-input.json <<'EOF'
{
  "claim_type": "exclusive",
  "target_artefact": {
    "type": "DataBatch",
    "payload": "test-batch"
  },
  "context_chain": []
}
EOF

# Test producer script
cat test-producer-input.json | \
  agents/example-batch-aggregator-agent/producer.sh 3>&1 | \
  jq -s '.'

# Should output array of 10 ProcessedRecord JSON objects
```

### Unit Test Aggregator (Without Holt)

```bash
# Create test input with all 10 records
cat > test-aggregator-input.json <<'EOF'
{
  "claim_type": "exclusive",
  "target_artefact": {
    "type": "ProcessedRecord",
    "payload": "record-10-success"
  },
  "context_chain": [],
  "ancestor_artefact": {
    "type": "DataBatch",
    "payload": "test-batch"
  },
  "descendant_artefacts": [
    {"type": "ProcessedRecord", "payload": "record-1-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-2-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-3-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-4-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-5-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-6-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-7-failure", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-8-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-9-success", "metadata": "{\"batch_size\": \"10\"}"},
    {"type": "ProcessedRecord", "payload": "record-10-success", "metadata": "{\"batch_size\": \"10\"}"}
  ]
}
EOF

# Test aggregator script
cat test-aggregator-input.json | \
  agents/example-batch-aggregator-agent/aggregate.sh 3>&1

# Should output AggregationReport with total_records: 10, successful: 9, failed: 1
```

### Integration Test (With Holt)

```bash
# Build image
docker build -t example-batch-aggregator:latest \
  -f agents/example-batch-aggregator-agent/Dockerfile .

# Start instance
holt up

# Submit batch
holt forage --goal "batch-001"

# Monitor producer
holt logs batch-producer

# Expected:
# Processing batch: batch-001
# Batch contains 10 records
# Processing each record...
#   ✅ Record 1 processed successfully
#   ✅ Record 2 processed successfully
#   ...
#   ❌ Record 7 failed
#   ...
# ✅ Batch processing complete!

# Monitor aggregator
holt logs batch-aggregator

# Expected:
# Batch Aggregator - Producer-Declared Pattern Example
# Ancestor Artefact: DataBatch, Batch ID: batch-001
# Descendant Artefacts: 10 ProcessedRecord(s)
# Expected (from metadata): 10
# Actual (received): 10
# Aggregation Statistics:
#   Total Records: 10
#   Successful: 9
#   Failed: 1
# ✅ Aggregation complete!

# Verify result
holt hoard | jq '.artefacts[] | select(.type=="AggregationReport")'
```

---

## Troubleshooting

### Aggregator Never Executes

**Issue:** Producer creates records but aggregator doesn't bid.

**Debug:**
```bash
# Check aggregator logs
holt logs batch-aggregator

# Look for:
# - "No ancestor of type 'DataBatch' found"
# - "Type 'ProcessedRecord': found X of Y expected"
# - "Failed to read metadata 'batch_size'"

# Verify metadata exists
docker exec holt-default-1-redis redis-cli \
  HGET "holt:default-1:artefact:{record-id}" metadata

# Should return: {"batch_size":"10"}
```

### Metadata Not Injected

**Issue:** Producer creates records but no metadata field.

**Cause:** Producer only output 1 artefact (not multi-artefact).

**Solution:**
```bash
# Verify producer outputs multiple records
holt logs batch-producer | grep "Created artefact"

# Should see 10 lines, not 1

# Check producer script outputs to FD 3 (>&3), not stdout
```

### Wrong Count in Metadata

**Issue:** Aggregator waits for wrong number of records.

**Debug:**
```bash
# Check what pup counted
holt logs batch-producer | grep "batch_size"

# Check what aggregator expects
holt logs batch-aggregator | grep "Expected (from metadata)"

# These should match
```

---

## Customization

### Change Batch Size

Edit `producer.sh`:

```bash
# Instead of fixed BATCH_SIZE=10
# Read from input or environment
BATCH_SIZE=$(echo "$input" | jq -r '.target_artefact.payload' | cut -d'-' -f2)
# For batch-050, BATCH_SIZE=50
```

### Add Validation Logic

Edit `aggregate.sh` to validate records:

```bash
# Check for required fields
invalid_count=$(echo "$descendants" | jq '[.[] | select(.payload | contains("invalid"))] | length')

if [ "$invalid_count" -gt 0 ]; then
  cat <<EOF >&3
{
  "structural_type": "Failure",
  "artefact_payload": "Aggregation failed: $invalid_count invalid records",
  "summary": "Aggregation aborted"
}
EOF
  exit 0
fi
```

### Use Different Metadata Key

```yaml
synchronize:
  wait_for:
    - type: "ProcessedRecord"
      count_from_metadata: "shard_count"  # Different key
```

Pup will inject metadata as: `{"batch_size": "10"}` (key is always `batch_size`)

To use custom key, producer must manually add metadata (not supported in M5.1 V1).

---

## Learn More

- **Fan-In Synchronization Guide**: `docs/guides/fan-in-synchronization.md`
- **Agent Development Guide**: `docs/guides/agent-development.md`
- **M5.1 Design Document**: `design/features/phase-5-complex-coordination/M5.1-fan-in.md`
- **Example Deployer**: `agents/example-deployer-agent/` (Named pattern)
