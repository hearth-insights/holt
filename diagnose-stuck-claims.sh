#!/bin/bash
# Diagnose why claims are stuck

echo "=== Stuck Claims Diagnostic ==="
echo ""

# Get instance name
INSTANCE=$(holt list | tail -1 | awk '{print $1}')
echo "Using instance: $INSTANCE"
echo ""

# Get one stuck claim for detailed inspection
STUCK_CLAIM=$(holt claims --incomplete --json | jq -r 'select(.status=="pending_review") | .claim_id' | head -1)

if [ -z "$STUCK_CLAIM" ]; then
    echo "No stuck pending_review claims found"
    exit 0
fi

SHORT_CLAIM="${STUCK_CLAIM:0:8}"
echo "Examining stuck claim: $SHORT_CLAIM..."
echo ""

# Check orchestrator logs for this claim using docker directly
echo "1. Orchestrator activity for this claim:"
docker logs holt-orchestrator-$INSTANCE 2>&1 | grep "$SHORT_CLAIM" | tail -10
echo ""

# Check if the reviewer agent is alive
echo "2. Checking hpo-reviewer logs (last 20 lines):"
docker logs --tail 20 holt-$INSTANCE-hpo-reviewer 2>&1
echo ""

echo "3. Checking if agent received any grants:"
docker logs holt-$INSTANCE-hpo-reviewer 2>&1 | grep -i "grant\|claim" | tail -10
echo ""

echo "4. Checking for errors in reviewer:"
docker logs holt-$INSTANCE-hpo-reviewer 2>&1 | grep -i "error\|fail\|exception" | tail -5
echo ""

echo "5. Summary of incomplete claims by status:"
holt claims --incomplete --json | jq -r '.status' | sort | uniq -c
echo ""

