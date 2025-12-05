#!/bin/sh
# M4.10: Helper library for FD 3 Return protocol
# Provides convenient functions for agents to return JSON results

# holt_return: Write JSON result to FD 3
# Usage: holt_return '{"artefact_type":"Test","artefact_payload":"data","summary":"ok"}'
holt_return() {
    json_result="$1"

    if [ -z "$json_result" ]; then
        echo "ERROR: holt_return requires JSON string argument" >&2
        return 1
    fi

    # Write to FD 3 (>&3)
    cat <<EOF >&3
$json_result
EOF
}

# Example usage (commented out):
# holt_return '{
#   "artefact_type": "CodeCommit",
#   "artefact_payload": "abc123",
#   "summary": "Work complete"
# }'
