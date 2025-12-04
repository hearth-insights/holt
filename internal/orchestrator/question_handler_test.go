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

// TestHandleQuestionArtefact_Success verifies basic Question handling flow
func TestHandleQuestionArtefact_Success(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create target artefact (what's being questioned)
	targetArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DesignSpec",
		Payload:         "Build an API",
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
		GrantedExclusiveAgent: "Reviewer",
	}
	err = client.CreateClaim(ctx, targetClaim)
	require.NoError(t, err)

	// Create Question artefact
	questionPayload := QuestionPayload{
		QuestionText:     "Is null handling in scope?",
		TargetArtefactID: targetArtefact.ID,
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Reviewer",
		SourceArtefacts: []string{targetArtefact.ID},
		CreatedAtMs:     2000,
	}
	err = client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Handle the Question
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Verify: Original claim should be terminated
	claim, err := client.GetClaim(ctx, targetClaim.ID)
	require.NoError(t, err)
	assert.Equal(t, blackboard.ClaimStatusTerminated, claim.Status)
	assert.Contains(t, claim.TerminationReason, "clarifying question")

	// Verify: Feedback claim should be created
	assert.Len(t, engine.pendingAssignmentClaims, 1)
	var feedbackClaimID string
	for claimID := range engine.pendingAssignmentClaims {
		feedbackClaimID = claimID
	}

	feedbackClaim, err := client.GetClaim(ctx, feedbackClaimID)
	require.NoError(t, err)
	assert.Equal(t, blackboard.ClaimStatusPendingAssignment, feedbackClaim.Status)
	assert.Equal(t, "Coder", feedbackClaim.GrantedExclusiveAgent)
	assert.Equal(t, targetArtefact.ID, feedbackClaim.ArtefactID)
	assert.Contains(t, feedbackClaim.AdditionalContextIDs, questionArtefact.ID)
}

// TestHandleQuestionArtefact_InvalidJSON tests malformed Question payload handling
func TestHandleQuestionArtefact_InvalidJSON(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create Question with invalid JSON payload
	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         "not valid json {{{",
		ProducedByRole:  "Reviewer",
		SourceArtefacts: []string{},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Handle should not crash - just skip
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	assert.NoError(t, err) // Should return nil (skip gracefully)

	// Verify no feedback claims created
	assert.Len(t, engine.pendingAssignmentClaims, 0)
}

// TestHandleQuestionArtefact_MissingTargetID tests payload validation
func TestHandleQuestionArtefact_MissingTargetID(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create Question with missing target_artefact_id
	questionPayload := map[string]string{
		"question_text": "Is this valid?",
		// Missing target_artefact_id
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Reviewer",
		SourceArtefacts: []string{},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Handle should skip
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	assert.NoError(t, err)

	// Verify no feedback claims created
	assert.Len(t, engine.pendingAssignmentClaims, 0)
}

// TestHandleQuestionArtefact_TargetNotFound tests missing target artefact
func TestHandleQuestionArtefact_TargetNotFound(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	nonExistentID := uuid.New().String()

	// Create Question referencing non-existent artefact
	questionPayload := QuestionPayload{
		QuestionText:     "Does this exist?",
		TargetArtefactID: nonExistentID,
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Reviewer",
		SourceArtefacts: []string{nonExistentID},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Handle should create Failure artefact
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Verify Failure artefact was created
	// (We can't easily query for it without scanning, but the function should have created it)
	// This is a basic smoke test that the error path doesn't crash
}

// TestHandleQuestionArtefact_IterationLimitExceeded tests max iterations enforcement
func TestHandleQuestionArtefact_IterationLimitExceeded(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 2) // Max 2 iterations
	ctx := context.Background()

	// Create target artefact at version 3 (iteration count = 2, at limit)
	targetArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         3, // version - 1 = 2 iterations
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DesignSpec",
		Payload:         "Build an API v3",
		ProducedByRole:  "Coder",
		SourceArtefacts: []string{},
		CreatedAtMs:     3000,
	}
	err := client.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create claim for target artefact
	targetClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "Reviewer",
	}
	err = client.CreateClaim(ctx, targetClaim)
	require.NoError(t, err)

	// Create Question artefact
	questionPayload := QuestionPayload{
		QuestionText:     "Still not clear?",
		TargetArtefactID: targetArtefact.ID,
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Reviewer",
		SourceArtefacts: []string{targetArtefact.ID},
		CreatedAtMs:     4000,
	}
	err = client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Handle should terminate due to iteration limit
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Verify: Claim should be terminated
	claim, err := client.GetClaim(ctx, targetClaim.ID)
	require.NoError(t, err)
	assert.Equal(t, blackboard.ClaimStatusTerminated, claim.Status)
	assert.Contains(t, claim.TerminationReason, "max review iterations")

	// Verify: No feedback claim created (exceeded limit)
	assert.Len(t, engine.pendingAssignmentClaims, 0)
}

// TestHandleQuestionArtefact_UserRole tests that Questions targeting "user" role
// are handled as missing agent (since "user" is not in the agent registry).
// In real workflows, CLI will answer these Questions directly.
func TestHandleQuestionArtefact_UserRole(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create target artefact produced by "user"
	targetArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Build an API",
		ProducedByRole:  "user", // User role
		SourceArtefacts: []string{},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create claim for an agent working on this goal
	targetClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "Coder",
	}
	err = client.CreateClaim(ctx, targetClaim)
	require.NoError(t, err)

	// Create Question artefact about the user's goal
	questionPayload := QuestionPayload{
		QuestionText:     "Should we use REST or GraphQL?",
		TargetArtefactID: targetArtefact.ID,
	}
	payloadJSON, _ := json.Marshal(questionPayload)

	questionArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         string(payloadJSON),
		ProducedByRole:  "Coder",
		SourceArtefacts: []string{targetArtefact.ID},
		CreatedAtMs:     2000,
	}
	err = client.CreateArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Subscribe BEFORE action to catch the event
	streamKey := fmt.Sprintf("holt:%s:workflow_events", engine.instanceName)
	pubsub := client.GetRedisClient().Subscribe(ctx, streamKey)
	defer pubsub.Close()
	// Wait for subscription to be established
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)

	// Handle the Question - should publish event and return nil (no feedback claim)
	err = engine.handleQuestionArtefact(ctx, questionArtefact)
	require.NoError(t, err)

	// Verify: Claim should be terminated (question asked)
	claim, err := client.GetClaim(ctx, targetClaim.ID)
	require.NoError(t, err)
	assert.Equal(t, blackboard.ClaimStatusTerminated, claim.Status)
	assert.Contains(t, claim.TerminationReason, "clarifying question")

	// Verify: No feedback claim created
	assert.Len(t, engine.pendingAssignmentClaims, 0)

	// Verify: human_input_required event published
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	
	// The payload is the JSON of WorkflowEvent struct
	assert.Contains(t, msg.Payload, "human_input_required")
}

// TestFindClaimByProducedArtefact tests claim lookup by artefact
func TestFindClaimByProducedArtefact(t *testing.T) {
	engine, client := setupTestEngineWithMaxIterations(t, 3)
	ctx := context.Background()

	// Create root artefact
	rootArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Build feature",
		ProducedByRole:  "user",
		SourceArtefacts: []string{},
		CreatedAtMs:     1000,
	}
	err := client.CreateArtefact(ctx, rootArtefact)
	require.NoError(t, err)

	// Create claim for root
	rootClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            rootArtefact.ID,
		Status:                blackboard.ClaimStatusComplete,
		GrantedExclusiveAgent: "Coder",
	}
	err = client.CreateClaim(ctx, rootClaim)
	require.NoError(t, err)

	// Create derived artefact (Question)
	derivedArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeQuestion,
		Type:            "ClarificationNeeded",
		Payload:         `{"question_text": "test", "target_artefact_id": "x"}`,
		ProducedByRole:  "Coder",
		SourceArtefacts: []string{rootArtefact.ID}, // Derived from root
		CreatedAtMs:     2000,
	}
	err = client.CreateArtefact(ctx, derivedArtefact)
	require.NoError(t, err)

	// Find claim by derived artefact
	claim, err := engine.findClaimByProducedArtefact(ctx, derivedArtefact.ID)
	require.NoError(t, err)
	require.NotNil(t, claim)
	assert.Equal(t, rootClaim.ID, claim.ID)
}

// TestQuestionPayloadParsing tests the QuestionPayload struct
func TestQuestionPayloadParsing(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		expectError bool
		expectedQ   string
		expectedT   string
	}{
		{
			name:        "valid payload",
			payload:     `{"question_text": "Is this valid?", "target_artefact_id": "abc-123"}`,
			expectError: false,
			expectedQ:   "Is this valid?",
			expectedT:   "abc-123",
		},
		{
			name:        "missing question_text",
			payload:     `{"target_artefact_id": "abc-123"}`,
			expectError: false,
			expectedQ:   "",
			expectedT:   "abc-123",
		},
		{
			name:        "invalid json",
			payload:     `not json`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload QuestionPayload
			err := json.Unmarshal([]byte(tt.payload), &payload)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedQ, payload.QuestionText)
				assert.Equal(t, tt.expectedT, payload.TargetArtefactID)
			}
		})
	}
}
