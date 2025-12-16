package resolver

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
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

	// Create test artefact with proper SHA-256 hash ID
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     1234567890,
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test-content",
		},
	}
	hashID, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hashID

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

	t.Run("resolve full hash ID for second artefact", func(t *testing.T) {
		// Create another artefact with proper hash ID
		artefact2 := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
				CreatedAtMs:     1234567891,
			},
			Payload: blackboard.ArtefactPayload{
				Content: "second-content",
			},
		}
		hashID2, err := blackboard.ComputeArtefactHash(artefact2)
		require.NoError(t, err)
		artefact2.ID = hashID2
		err = bbClient.CreateArtefact(ctx, artefact2)
		require.NoError(t, err)

		resolved, err := ResolveArtefactID(ctx, bbClient, hashID2)
		require.NoError(t, err)
		assert.Equal(t, hashID2, resolved)
	})
}

func TestErrorHelpers(t *testing.T) {
	// Test NotFoundError
	notFoundErr := &NotFoundError{ShortID: "abc"}
	assert.Equal(t, "no artefacts found matching 'abc'", notFoundErr.Error())
	assert.True(t, IsNotFoundError(notFoundErr))
	assert.False(t, IsAmbiguousError(notFoundErr))

	// Test AmbiguousError
	ambiguousErr := &AmbiguousError{
		ShortID: "abc",
		Matches: []string{"abc1", "abc2"},
	}
	assert.Equal(t, "ambiguous short ID 'abc' matches 2 artefacts", ambiguousErr.Error())
	assert.True(t, IsAmbiguousError(ambiguousErr))
	assert.False(t, IsNotFoundError(ambiguousErr))

	// Test FormatAmbiguousError
	formatted := FormatAmbiguousError(ambiguousErr)
	assert.Contains(t, formatted, "Error: ambiguous short ID 'abc' matches 2 artefacts:")
	assert.Contains(t, formatted, "  abc1")
	assert.Contains(t, formatted, "  abc2")

	// Test FormatAmbiguousError with many matches
	manyMatches := make([]string, 15)
	for i := 0; i < 15; i++ {
		manyMatches[i] = "match"
	}
	largeErr := &AmbiguousError{ShortID: "large", Matches: manyMatches}
	formattedLarge := FormatAmbiguousError(largeErr)
	assert.Contains(t, formattedLarge, "...and 5 more")
}
