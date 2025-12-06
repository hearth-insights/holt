#!/bin/sh
# Example agent tool script for M2.3
# This is a simple echo agent that demonstrates the stdin/stdout JSON contract

# Read JSON input from stdin
input=$(cat)

# Log to stderr (visible in agent logs, not sent to pup)
echo "Echo agent received claim, processing..." >&2
echo "Input: $input" >&2

# Generate timestamp for unique payload
timestamp=$(date +%s)

# M4.10: Output success JSON to FD 3 (not stdout)
# stdout/stderr are now for logs - result goes to FD 3
cat <<EOF >&3
{
  "artefact_type": "EchoSuccess",
  "artefact_payload": "echo-$timestamp",
  "summary": "Echo agent successfully processed the claim"
}
EOF
