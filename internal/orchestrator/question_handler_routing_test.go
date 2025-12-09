package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/google/uuid"
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
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "CodeArtefact",
		Payload:         "Some code",
		ProducedByRole:  "Coder",
		SourceArtefacts: []string{},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

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
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "GatekeeperQuestion",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Gatekeeper",
		SourceArtefacts: []string{targetArtefact.ID},
		CreatedAtMs:     2000,
	}
	err = client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

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
	assert.Equal(t, blackboard.ClaimStatusTerminated, claim.Status)

	// Verify: NO feedback claim created (because we routed to human)
	assert.Len(t, engine.pendingAssignmentClaims, 0, "Should not create feedback claim when routing=human")

	// Verify: human_input_required event published
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.Contains(t, msg.Payload, "human_input_required")
	assert.Contains(t, msg.Payload, "Deploy to prod?")
}
