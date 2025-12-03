package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForConsensus(t *testing.T) {
	// Setup miniredis
	s := miniredis.RunT(t)
	
	// Setup blackboard client
	client, err := blackboard.NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	// Setup engine with known agents
	agentRegistry := map[string]string{
		"agent-a": "role-a",
		"agent-b": "role-b",
	}
	
	engine := &Engine{
		client:        client,
		instanceName:  "test-instance",
		agentRegistry: agentRegistry,
	}

	claimID := "claim-123"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Scenario 1: Consensus achieved immediately
	t.Run("ConsensusAchieved", func(t *testing.T) {
		// Pre-populate bids
		err := client.SetBid(ctx, claimID, "agent-a", blackboard.BidTypeExclusive)
		require.NoError(t, err)
		err = client.SetBid(ctx, claimID, "agent-b", blackboard.BidTypeIgnore)
		require.NoError(t, err)

		bids, err := engine.WaitForConsensus(ctx, claimID)
		assert.NoError(t, err)
		assert.Len(t, bids, 2)
		assert.Equal(t, blackboard.BidTypeExclusive, bids["agent-a"].BidType)
		assert.Equal(t, blackboard.BidTypeIgnore, bids["agent-b"].BidType)
	})

	// Scenario 2: Consensus achieved after delay
	t.Run("ConsensusDelayed", func(t *testing.T) {
		claimIDDelayed := "claim-delayed"
		
		// Start waiting in a goroutine
		resultChan := make(chan map[string]blackboard.Bid)
		errChan := make(chan error)
		
		go func() {
			bids, err := engine.WaitForConsensus(ctx, claimIDDelayed)
			if err != nil {
				errChan <- err
			} else {
				resultChan <- bids
			}
		}()

		// Simulate delay then add bids
		time.Sleep(200 * time.Millisecond)
		client.SetBid(ctx, claimIDDelayed, "agent-a", blackboard.BidTypeExclusive)
		time.Sleep(100 * time.Millisecond)
		client.SetBid(ctx, claimIDDelayed, "agent-b", blackboard.BidTypeExclusive)

		select {
		case bids := <-resultChan:
			assert.Len(t, bids, 2)
		case err := <-errChan:
			t.Fatalf("WaitForConsensus failed: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("WaitForConsensus timed out")
		}
	})
	
	// Scenario 3: Context cancellation (timeout)
	t.Run("ContextCancelled", func(t *testing.T) {
		claimIDTimeout := "claim-timeout"
		
		// Only one bid submitted
		client.SetBid(ctx, claimIDTimeout, "agent-a", blackboard.BidTypeExclusive)
		
		// Create a short context
		shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer shortCancel()

		_, err := engine.WaitForConsensus(shortCtx, claimIDTimeout)
		assert.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestValidateAndSanitizeBids(t *testing.T) {
	engine := &Engine{}
	claimID := "test-claim"

	tests := []struct {
		name     string
		input    map[string]blackboard.Bid
		expected map[string]blackboard.Bid
	}{
		{
			name: "AllValid",
			input: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: blackboard.BidTypeExclusive},
				"agent-b": {AgentName: "agent-b", BidType: blackboard.BidTypeIgnore},
			},
			expected: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: blackboard.BidTypeExclusive},
				"agent-b": {AgentName: "agent-b", BidType: blackboard.BidTypeIgnore},
			},
		},
		{
			name: "MixedValidAndInvalid",
			input: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: blackboard.BidTypeExclusive},
				"agent-b": {AgentName: "agent-b", BidType: "invalid_bid_type"},
			},
			expected: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: blackboard.BidTypeExclusive},
				"agent-b": {AgentName: "agent-b", BidType: blackboard.BidTypeIgnore},
			},
		},
		{
			name: "AllInvalid",
			input: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: "garbage"},
				"agent-b": {AgentName: "agent-b", BidType: ""},
			},
			expected: map[string]blackboard.Bid{
				"agent-a": {AgentName: "agent-a", BidType: blackboard.BidTypeIgnore},
				"agent-b": {AgentName: "agent-b", BidType: blackboard.BidTypeIgnore},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.validateAndSanitizeBids(claimID, tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
