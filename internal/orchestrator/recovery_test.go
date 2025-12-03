package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverState(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupTestEngine(t)

	// Setup: Create claims in various states to be recovered

	// 1. Pending Assignment (Feedback Loop)
	claim1 := &blackboard.Claim{
		ID:         "claim-1",
		ArtefactID: "art-1",
		Status:     blackboard.ClaimStatusPendingAssignment,
	}
	require.NoError(t, e.client.CreateClaim(ctx, claim1))

	// 2. Pending Review (Phased)
	claim2 := &blackboard.Claim{
		ID:         "claim-2",
		ArtefactID: "art-2",
		Status:     blackboard.ClaimStatusPendingReview,
		PhaseState: &blackboard.PhaseState{
			Current:       "review",
			GrantedAgents: []string{"Reviewer"},
			StartTimeMs:   time.Now().UnixMilli(),
		},
	}
	require.NoError(t, e.client.CreateClaim(ctx, claim2))

	// 3. Pending Parallel (Phased, with some received)
	claim3 := &blackboard.Claim{
		ID:         "claim-3",
		ArtefactID: "art-3",
		Status:     blackboard.ClaimStatusPendingParallel,
		PhaseState: &blackboard.PhaseState{
			Current:       "parallel",
			GrantedAgents: []string{"Coder"},
			Received:      map[string]string{"Coder": "result-1"},
			StartTimeMs:   time.Now().UnixMilli(),
		},
	}
	require.NoError(t, e.client.CreateClaim(ctx, claim3))

	// Run Recovery
	err := e.RecoverState(ctx)
	require.NoError(t, err)

	// Verify State Reconstruction

	// Claim 1: Should be in pendingAssignmentClaims
	assert.Contains(t, e.pendingAssignmentClaims, "claim-1")
	assert.Equal(t, "art-1", e.pendingAssignmentClaims["claim-1"])

	// Claim 2: Should have PhaseState reconstructed
	ps2, exists := e.phaseStates["claim-2"]
	require.True(t, exists, "claim-2 should be recovered")
	assert.Equal(t, "review", ps2.Phase)
	assert.ElementsMatch(t, []string{"Reviewer"}, ps2.GrantedAgents)

	// Claim 3: Should have PhaseState reconstructed with received artefacts
	ps3, exists := e.phaseStates["claim-3"]
	require.True(t, exists, "claim-3 should be recovered")
	assert.Equal(t, "parallel", ps3.Phase)
	assert.Equal(t, "result-1", ps3.ReceivedArtefacts["Coder"])
}

func TestRetriggerGrant(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupTestEngine(t)

	// Setup a claim that expects artefacts but hasn't received them
	claim := &blackboard.Claim{
		ID:               "claim-retrigger",
		ArtefactID:       "art-retrigger",
		Status:           blackboard.ClaimStatusPendingParallel,
		ArtefactExpected: true,
		PhaseState: &blackboard.PhaseState{
			Current:       "parallel",
			GrantedAgents: []string{"Coder"},
			StartTimeMs:   time.Now().UnixMilli(),
		},
	}
	require.NoError(t, e.client.CreateClaim(ctx, claim))

	// Subscribe BEFORE action to catch the event
	streamKey := fmt.Sprintf("holt:%s:agent:Coder:events", e.instanceName)
	pubsub := e.client.GetRedisClient().Subscribe(ctx, streamKey)
	defer pubsub.Close()
	// Wait for subscription to be established
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	// Retrigger grant
	err = e.retriggerGrant(ctx, claim, []string{"Coder"})
	require.NoError(t, err)

	// Verify that grant notification was re-published
	// We check the agent's event channel
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	
	assert.NotEmpty(t, msg.Payload)
	assert.Equal(t, streamKey, msg.Channel)
	
	// Check payload contains "claim_type": "claim" (for parallel)
	assert.Contains(t, msg.Payload, `"claim_type":"claim"`)
	assert.Contains(t, msg.Payload, `"claim_id":"claim-retrigger"`)
}

func TestRecoverGrantQueues(t *testing.T) {
	ctx := context.Background()
	e, _, _ := setupTestEngine(t)

	// Manually add items to a grant queue in Redis
	queueKey := fmt.Sprintf("holt:%s:grant_queue:Coder", e.instanceName)
	err := e.client.GetRedisClient().ZAdd(ctx, queueKey, redis.Z{
		Score:  float64(time.Now().UnixMilli()),
		Member: "claim-queued-1",
	}).Err()
	require.NoError(t, err)

	// Run recoverGrantQueues (via RecoverState or directly)
	// RecoverState calls recoverGrantQueues internally
	err = e.RecoverState(ctx)
	require.NoError(t, err)

	// Verify log output? 
	// Hard to verify log output directly without capturing stdout/log hook.
	// But we can verify that it didn't error and the queue is still there.
	count, err := e.client.GetRedisClient().ZCard(ctx, queueKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
