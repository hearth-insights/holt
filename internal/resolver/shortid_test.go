package resolver

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveArtefactID(t *testing.T) {
	// Setup miniredis
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisOpts := &redis.Options{Addr: mr.Addr()}
	bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	ctx := context.Background()

	// Create test artefact with SHA-256 hash ID
	hashID := "a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1"
	artefact := &blackboard.Artefact{
		ID:              hashID,
		LogicalID:       "550e8400-e29b-41d4-a716-446655440000",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		ProducedByRole:  "test-agent",
		SourceArtefacts: []string{},
	}

	err = bbClient.CreateArtefact(ctx, artefact)
	require.NoError(t, err)

	t.Run("resolve full hash ID", func(t *testing.T) {
		resolved, err := ResolveArtefactID(ctx, bbClient, hashID)
		require.NoError(t, err)
		assert.Equal(t, hashID, resolved)
	})

	t.Run("resolve short hash ID", func(t *testing.T) {
		shortID := hashID[:8]
		resolved, err := ResolveArtefactID(ctx, bbClient, shortID)
		require.NoError(t, err)
		assert.Equal(t, hashID, resolved)
	})

    t.Run("resolve full UUID", func(t *testing.T) {
        uuidID := "550e8400-e29b-41d4-a716-446655440000"
        // Create artefact with UUID
        uuidArtefact := &blackboard.Artefact{
            ID:              uuidID,
            LogicalID:       "650e8400-e29b-41d4-a716-446655440000",
            Version:         1,
            StructuralType:  blackboard.StructuralTypeStandard,
            Type:            "GoalDefined",
            ProducedByRole:  "test-agent",
            SourceArtefacts: []string{},
        }
        err = bbClient.CreateArtefact(ctx, uuidArtefact)
        require.NoError(t, err)

        resolved, err := ResolveArtefactID(ctx, bbClient, uuidID)
        require.NoError(t, err)
        assert.Equal(t, uuidID, resolved)
    })
}
