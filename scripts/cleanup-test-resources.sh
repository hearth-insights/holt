#!/bin/bash
# Cleanup script for orphaned Holt test resources
# Run this to clean up Docker networks and instances left behind by failed E2E tests

set -e

echo "=== Holt Test Resource Cleanup ==="
echo ""

# Count resources before cleanup
instances_before=$(holt list | grep -E "test-e2e-|test-queue-" | wc -l | tr -d ' ')
networks_before=$(docker network ls --filter "name=holt-network-test" --format "{{.Name}}" | wc -l | tr -d ' ')

echo "Found $instances_before test instances"
echo "Found $networks_before test networks"
echo ""

if [ "$instances_before" -eq 0 ] && [ "$networks_before" -eq 0 ]; then
  echo "✓ No test resources to clean up"
  exit 0
fi

echo "Cleaning up test instances..."

# Use JSON output for reliable parsing
instances=$(holt list --json 2>/dev/null | jq -r '.[] | select(.status == "Stopped" and (.name | startswith("test-"))) | .name' 2>/dev/null || echo "")

if [ -z "$instances" ]; then
  echo "  No stopped test instances found"
else
  for instance in $instances; do
    echo "  Removing instance: $instance"
    holt down --name "$instance" 2>&1 | grep -v "no Holt instances found" || true
  done
fi

echo ""
echo "Cleaning up orphaned networks..."

# Remove orphaned test networks (networks without containers)
for network in $(docker network ls --filter "name=holt-network-test" --format "{{.Name}}"); do
  echo "  Removing network: $network"
  docker network rm "$network" 2>/dev/null || true
done

echo ""

# Count resources after cleanup
instances_after=$(holt list | grep -E "test-e2e-|test-queue-" | wc -l | tr -d ' ')
networks_after=$(docker network ls --filter "name=holt-network-test" --format "{{.Name}}" | wc -l | tr -d ' ')

echo "=== Cleanup Summary ==="
echo "Instances removed: $((instances_before - instances_after))"
echo "Networks removed: $((networks_before - networks_after))"

if [ "$instances_after" -eq 0 ] && [ "$networks_after" -eq 0 ]; then
  echo "✓ All test resources cleaned up successfully"
else
  echo "⚠ Some resources remain (may be in use)"
  if [ "$instances_after" -gt 0 ]; then
    echo "  Remaining instances: $instances_after"
  fi
  if [ "$networks_after" -gt 0 ]; then
    echo "  Remaining networks: $networks_after"
  fi
fi
