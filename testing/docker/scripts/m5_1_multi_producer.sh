#!/bin/bash
set -e
cat > /dev/null

# Produce 5 ProcessedRecord artefacts (Pup will inject batch_size=5 metadata automatically)
for i in {1..5}; do
  cat <<RECORD >&3
{"artefact_type":"ProcessedRecord","artefact_payload":"record-$i","summary":"Processed record $i"}
RECORD
done
