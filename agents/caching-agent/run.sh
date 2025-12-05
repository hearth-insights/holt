#!/bin/bash
set -e

# Read stdin
INPUT=$(cat)

# Parse JSON using jq
CLAIM_TYPE=$(echo "$INPUT" | jq -r '.claim_type')
CONTEXT_IS_DECLARED=$(echo "$INPUT" | jq -r '.context_is_declared // false')
TARGET_TYPE=$(echo "$INPUT" | jq -r '.target_artefact.type')

# Log for debugging
echo "[caching-agent] claim_type=$CLAIM_TYPE context_is_declared=$CONTEXT_IS_DECLARED target=$TARGET_TYPE" >&2

# Always produce valid output regardless of context
if [ "$CONTEXT_IS_DECLARED" = "false" ]; then
	# First run - no cached context, produce checkpoint
	echo "[caching-agent] First run - producing checkpoint with SDK docs" >&2

	# M4.10: Output to FD 3
	cat <<'EOF' >&3
{
	"artefact_type": "DesignSpec",
	"artefact_payload": "Design based on first-time context discovery",
	"summary": "Created design spec after expensive SDK discovery",
	"checkpoints": [
		{
			"knowledge_name": "go-sdk-docs",
			"knowledge_payload": "GO SDK VERSION 1.21: Key APIs include context, http, database/sql",
			"target_roles": ["coder*"]
		}
	]
}
EOF
else
	# Subsequent run - cached context available, use it
	echo "[caching-agent] Cached run - using knowledge" >&2

	# M4.10: Output to FD 3
	cat <<'EOF' >&3
{
	"artefact_type": "DesignSpec",
	"artefact_payload": "Design using cached SDK docs v2",
	"summary": "Updated design using cached knowledge (no expensive discovery)"
}
EOF
fi
