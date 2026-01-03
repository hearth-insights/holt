package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleQuestionArtefact_RoutingHuman verifies that routing="human"
// bypasses the feedback loop and requests human input, even for agent-produced artifacts.
func TestHandleQuestionArtefact_RoutingHuman(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create target artefact produced by "Coder" (an agent)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeArtefact",
			ProducedByRole:  "Coder",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Some code",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	require.NoError(t, client.CreateArtefact(ctx, targetArtefact))

	// Create claim for target artefact
	targetClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "Gatekeeper",
	}
	err = client.CreateClaim(ctx, targetClaim)
	require.NoError(t, err)

	// Create Question artefact with Routing="human"
	questionPayload := QuestionPayload{
		QuestionText:     "Deploy to prod?",
		TargetArtefactID: targetArtefact.ID,
		Routing:          "human",
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeQuestion,
			Type:            "GatekeeperQuestion",
			ProducedByRole:  "Gatekeeper",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: string(payloadJSON),
		},
	}
	hash, err := blackboard.ComputeArtefactHash(questionArtefact)
	require.NoError(t, err)
	questionArtefact.ID = hash
	require.NoError(t, client.CreateArtefact(ctx, questionArtefact))

	// Subscribe to workflow events to verify human_input_required is emitted
	streamKey := fmt.Sprintf("holt:%s:workflow_events", engine.instanceName)
	pubsub := client.GetRedisClient().Subscribe(ctx, streamKey)
	defer pubsub.Close()
	// Wait for subscription
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)

	// Handle the Question
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Verify: Target claim is terminated
	claim, err := client.GetClaim(ctx, targetClaim.ID)
	require.NoError(t, err)
	t.Logf("Debug: Claim %s status is %s (expected terminated)", claim.ID, claim.Status)
	assert.Equal(t, blackboard.ClaimStatusTerminated, claim.Status)

	// Verify: NO feedback claim created (because we routed to human)
	assert.Len(t, engine.pendingAssignmentClaims, 0, "Should not create feedback claim when routing=human")

	// Verify: human_input_required event published
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.Contains(t, msg.Payload, "human_input_required")
	assert.Contains(t, msg.Payload, "Deploy to prod?")
}
