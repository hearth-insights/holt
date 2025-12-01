package hoard

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRelationships(t *testing.T) {
	t.Run("resolves upstream and downstream claims", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// 1. Create Upstream Claim (Produced By)
		upstreamClaim := &blackboard.Claim{
			ID:         "claim-upstream",
			ArtefactID: "art-parent",
			Status:     blackboard.ClaimStatusComplete,
			GrantedExclusiveAgent: "upstream-agent",
		}
		require.NoError(t, bbClient.CreateClaim(ctx, upstreamClaim))

		// 2. Create Artefact (linked to upstream claim)
		artefact := &blackboard.Artefact{
			ID:             "art-target",
			LogicalID:      "log-target",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "GoalDefined",
			ProducedByRole: "upstream-agent",
			Payload:        "goal",
			ClaimID:        upstreamClaim.ID, // Link to upstream
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// 3. Create Downstream Claim (Consumed By)
		downstreamClaim := &blackboard.Claim{
			ID:                    "claim-downstream",
			ArtefactID:            artefact.ID, // Link to target artefact
			Status:                blackboard.ClaimStatusPendingParallel,
			GrantedParallelAgents: []string{"downstream-agent-1", "downstream-agent-2"},
		}
		require.NoError(t, bbClient.CreateClaim(ctx, downstreamClaim))

		// 4. Resolve Relationships
		info, err := ResolveRelationships(ctx, bbClient, artefact)
		require.NoError(t, err)
		require.NotNil(t, info)

		// Verify Upstream
		require.NotNil(t, info.ProducedBy)
		assert.Equal(t, upstreamClaim.ID, info.ProducedBy.ClaimID)
		assert.Equal(t, "upstream-agent", info.ProducedBy.ExclusiveAgent)
		assert.Equal(t, blackboard.ClaimStatusComplete, info.ProducedBy.Status)

		// Verify Downstream
		require.NotNil(t, info.ConsumedBy)
		assert.Equal(t, downstreamClaim.ID, info.ConsumedBy.ClaimID)
		assert.Contains(t, info.ConsumedBy.ParallelAgents, "downstream-agent-1")
		assert.Contains(t, info.ConsumedBy.ParallelAgents, "downstream-agent-2")
		assert.Equal(t, blackboard.ClaimStatusPendingParallel, info.ConsumedBy.Status)
	})

	t.Run("handles missing claims gracefully", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// Create Artefact with no claims
		artefact := &blackboard.Artefact{
			ID:             "art-orphan",
			LogicalID:      "log-orphan",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "GoalDefined",
			ProducedByRole: "user",
			Payload:        "goal",
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// Resolve
		info, err := ResolveRelationships(ctx, bbClient, artefact)
		require.NoError(t, err)
		require.NotNil(t, info)

		assert.Nil(t, info.ProducedBy)
		assert.Nil(t, info.ConsumedBy)
	})
}
