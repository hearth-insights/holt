package pup

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestEngine(t *testing.T) (*Engine, *blackboard.Client, *miniredis.Miniredis) {
	s := miniredis.RunT(t)
	
	client, err := blackboard.NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	config := &Config{
		InstanceName: "test-instance",
		AgentName:    "test-agent",
		RedisURL:     "redis://" + s.Addr(),
		Command:      []string{"echo", "hello"},
		BiddingStrategy: BiddingStrategy{
			Type: blackboard.BidTypeExclusive,
		},
	}

	engine := New(config, client)
	return engine, client, s
}

func TestHandleClaimEvent(t *testing.T) {
	engine, client, _ := setupTestEngine(t)
	ctx := context.Background()
	workQueue := make(chan *blackboard.Claim, 10)

	// Scenario 1: Feedback claim assigned to this agent
	t.Run("FeedbackClaim_Assigned", func(t *testing.T) {
		claim := &blackboard.Claim{
			ID:                    "claim-feedback-assigned",
			ArtefactID:            "art-1",
			Status:                blackboard.ClaimStatusPendingAssignment,
			GrantedExclusiveAgent: "test-agent",
		}
		
		engine.handleClaimEvent(ctx, claim, workQueue)
		
		select {
		case queuedClaim := <-workQueue:
			assert.Equal(t, claim.ID, queuedClaim.ID)
		default:
			t.Fatal("Expected claim to be queued")
		}
	})

	// Scenario 2: Feedback claim assigned to other agent
	t.Run("FeedbackClaim_OtherAgent", func(t *testing.T) {
		claim := &blackboard.Claim{
			ID:                    "claim-feedback-other",
			ArtefactID:            "art-1",
			Status:                blackboard.ClaimStatusPendingAssignment,
			GrantedExclusiveAgent: "other-agent",
		}
		
		engine.handleClaimEvent(ctx, claim, workQueue)
		
		select {
		case <-workQueue:
			t.Fatal("Expected claim NOT to be queued")
		default:
			// Success
		}
	})

	// Scenario 3: Regular claim - Bidding
	t.Run("RegularClaim_Bidding", func(t *testing.T) {
		// Create target artefact
		artefact := &blackboard.Artefact{
			ID:             "art-target",
			LogicalID:      "logical-target",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "Code",
			ProducedByRole: "user",
			CreatedAtMs:    time.Now().UnixMilli(),
		}
		// We don't verify hash here as we are just testing bidding logic which doesn't verify hash
		// But GetArtefact reads from Redis, so we need to write it.
		// CreateArtefact validates, so we need valid hash if validation is strict.
		// Let's see if CreateArtefact enforces hash. Yes, it calls Validate().
		// Validate() checks fields but doesn't re-compute hash.
		// So we can just set ID.
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		claim := &blackboard.Claim{
			ID:         "claim-regular",
			ArtefactID: artefact.ID,
			Status:     blackboard.ClaimStatusPendingReview,
		}

		engine.handleClaimEvent(ctx, claim, workQueue)

		// Verify bid was submitted
		bids, err := client.GetAllBids(ctx, claim.ID)
		require.NoError(t, err)
		assert.Len(t, bids, 1)
		assert.Equal(t, blackboard.BidTypeExclusive, bids["test-agent"].BidType)
		
		// Verify NOT queued (only queued on grant)
		select {
		case <-workQueue:
			t.Fatal("Expected claim NOT to be queued (only bid submitted)")
		default:
			// Success
		}
	})
}

func TestHandleGrantNotification(t *testing.T) {
	engine, client, _ := setupTestEngine(t)
	ctx := context.Background()
	workQueue := make(chan *blackboard.Claim, 10)

	// Scenario 1: Valid grant
	t.Run("ValidGrant", func(t *testing.T) {
		claim := &blackboard.Claim{
			ID:                    "claim-granted",
			ArtefactID:            "art-1",
			Status:                blackboard.ClaimStatusPendingExclusive,
			GrantedExclusiveAgent: "test-agent",
		}
		err := client.CreateClaim(ctx, claim)
		require.NoError(t, err)

		notification := GrantNotification{
			EventType: "grant",
			ClaimID:   claim.ID,
		}
		jsonBytes, _ := json.Marshal(notification)
		
		engine.handleGrantNotification(ctx, string(jsonBytes), workQueue)
		
		select {
		case queuedClaim := <-workQueue:
			assert.Equal(t, claim.ID, queuedClaim.ID)
		default:
			t.Fatal("Expected claim to be queued")
		}
	})

	// Scenario 2: Invalid grant (not for this agent)
	t.Run("InvalidGrant_OtherAgent", func(t *testing.T) {
		claim := &blackboard.Claim{
			ID:                    "claim-granted-other",
			ArtefactID:            "art-1",
			Status:                blackboard.ClaimStatusPendingExclusive,
			GrantedExclusiveAgent: "other-agent",
		}
		err := client.CreateClaim(ctx, claim)
		require.NoError(t, err)

		notification := GrantNotification{
			EventType: "grant",
			ClaimID:   claim.ID,
		}
		jsonBytes, _ := json.Marshal(notification)
		
		engine.handleGrantNotification(ctx, string(jsonBytes), workQueue)
		
		select {
		case <-workQueue:
			t.Fatal("Expected claim NOT to be queued")
		default:
			// Success
		}
	})
}

