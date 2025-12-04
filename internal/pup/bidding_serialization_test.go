package pup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStaticBiddingSerialization verifies that static bidding strategy
// results in a valid JSON payload sent to the Orchestrator.
// This is a regression test for a reported bug where raw strings were sent.
func TestStaticBiddingSerialization(t *testing.T) {
	// Setup miniredis
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	// Setup blackboard client
	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, "test-instance")
	require.NoError(t, err)
	t.Cleanup(func() { bbClient.Close() })

	// Setup environment variables for LoadConfig
	t.Setenv("HOLT_INSTANCE_NAME", "test-instance")
	t.Setenv("HOLT_AGENT_NAME", "test-agent")
	t.Setenv("REDIS_URL", "redis://"+mr.Addr())
	t.Setenv("HOLT_AGENT_COMMAND", "[\"/bin/true\"]")
	// Configure static bidding strategy
	t.Setenv("HOLT_BIDDING_STRATEGY", `{"type": "exclusive", "target_types": ["GoalDefined"]}`)

	// Load config
	config, err := LoadConfig()
	require.NoError(t, err)

	engine := New(config, bbClient)

	// Create a claim and target artefact
	ctx := context.Background()
	artefactID := blackboard.NewID()
	claimID := blackboard.NewID()

	artefact := &blackboard.Artefact{
		ID:             artefactID,
		LogicalID:      blackboard.NewID(),
		Version:        1,
		StructuralType: blackboard.StructuralTypeStandard,
		Type:           "GoalDefined",
		ProducedByRole: "user",
		Payload:        "test",
	}
	err = bbClient.CreateArtefact(ctx, artefact)
	require.NoError(t, err)

	claim := &blackboard.Claim{
		ID:         claimID,
		ArtefactID: artefactID,
		Status:     blackboard.ClaimStatusPendingReview,
	}
	err = bbClient.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Create a work queue (buffered so we don't block)
	workQueue := make(chan *blackboard.Claim, 1)

	// Trigger handleClaimEvent directly (simulating the watcher)
	engine.handleClaimEvent(ctx, claim, workQueue)

	// Verify what was written to Redis
	// The key is holt:{instance}:claim:{claim_id}:bids
	// Field is agent name
	key := blackboard.ClaimBidsKey("test-instance", claimID)
	
	// Read raw value from Redis
	val := mr.HGet(key, "test-agent")
	require.NotEmpty(t, val, "Bid should be in Redis")

	t.Logf("Raw Redis value: %s", val)

	// Attempt to unmarshal as Orchestrator would
	var bid blackboard.Bid
	err = json.Unmarshal([]byte(val), &bid)
	
	// Assert it is valid JSON and has correct values
	assert.NoError(t, err, "Should be valid JSON")
	assert.Equal(t, blackboard.BidTypeExclusive, bid.BidType)
	assert.Equal(t, "test-agent", bid.AgentName)
}
