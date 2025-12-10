#!/bin/bash
set -e

# Read input JSON from stdin
INPUT_JSON=$(cat)
INPUT_TYPE=$(echo "$INPUT_JSON" | jq -r .target_artefact.type)

echo "Received input type: $INPUT_TYPE" >&2

if [ "$INPUT_TYPE" == "GoalDefined" ]; then
    echo "Starting automated checks..." >&2
    sleep 2 # Simulate work
    
    # Output TestResults
    cat <<EOF >&3
{
  "artefact_type": "TestResults",
  "structural_type": "Standard",
  "summary": "Automated checks completed",
  "artefact_payload": "{\"status\": \"passed\", \"checks\": [\"lint\", \"unit\", \"integration\"]}"
}
EOF

elif [ "$INPUT_TYPE" == "TestResults" ]; then
    PAYLOAD=$(echo "$INPUT_JSON" | jq -r .target_artefact.payload)
    
    # Check if this is the initial result (needs approval) or the answer (V2)
    # If payload contains "status": "passed", it's V1 (our own output)
    if [[ "$PAYLOAD" == *"\"status\": \"passed\""* ]]; then
        echo "Checks passed. Requesting human approval..." >&2
        
        # Output Question with Direct Routing
        cat <<EOF >&3
{
  "artefact_type": "DeploymentRequest",
  "structural_type": "Question",
  "summary": "Requesting deployment approval",
  "artefact_payload": "{\"question_text\": \"Tests passed. Authorise deployment to production? (yes/no)\", \"target_artefact_id\": \"$(echo "$INPUT_JSON" | jq -r .target_artefact.id)\", \"routing\": \"human\"}"
}
EOF
    else
        # It's V2 (Answer from human)
        # The payload will be the answer text
        echo "Received human answer: $PAYLOAD" >&2
        
        if [[ "$PAYLOAD" == *"yes"* ]]; then
            echo "Deploying to production..." >&2
            sleep 2
            
            cat <<EOF >&3
{
  "artefact_type": "DeploymentManifest",
  "structural_type": "Standard",
  "summary": "Deployment successful",
  "artefact_payload": "{\"deployed_at\": \"$(date)\", \"version\": \"1.0.0\"}"
}
EOF
        else
            echo "Deployment aborted by user." >&2
            
            cat <<EOF >&3
{
  "artefact_type": "DeploymentAborted",
  "structural_type": "Standard",
  "summary": "Deployment aborted",
  "artefact_payload": "Aborted by user request."
}
EOF
        fi
    fi
else
    echo "Unknown input type: $INPUT_TYPE" >&2
    exit 1
fi
