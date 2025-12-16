package spine

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSpine(t *testing.T) {
	// Setup miniredis
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisOpts := &redis.Options{Addr: mr.Addr()}
	bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	ctx := context.Background()

	// Helper to create artefact with proper hash ID
	createArtefact := func(structType, payload string, sources []string) *blackboard.Artefact {
		art := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralType(structType),
				Type:            "TestType",
				ProducedByRole:  "test",
				ParentHashes:    sources,
				CreatedAtMs:     1234567890,
			},
			Payload: blackboard.ArtefactPayload{
				Content: payload,
			},
		}
		hash, err := blackboard.ComputeArtefactHash(art)
		require.NoError(t, err)
		art.ID = hash
		err = bbClient.CreateArtefact(ctx, art)
		require.NoError(t, err)
		return art
	}

	// Scenario 1: Artefact IS a SystemManifest
	t.Run("ArtefactIsManifest", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		manifestPayload := `{"config_hash": "hash1", "git_commit": "commit1"}`
		manifest := createArtefact(string(blackboard.StructuralTypeSystemManifest), manifestPayload, nil)

		info, err := ResolveSpine(ctx, bbClient, manifest, cache)
		require.NoError(t, err)
		assert.False(t, info.IsDetached)
		assert.Equal(t, manifest.ID, info.ManifestID)
		assert.Equal(t, "hash1", info.ConfigHash)
		assert.Equal(t, "commit1", info.GitCommit)
	})

	// Scenario 2: Artefact has SystemManifest as parent
	t.Run("ParentIsManifest", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		manifestPayload := `{"config_hash": "hash2", "git_commit": "commit2"}`
		manifest := createArtefact(string(blackboard.StructuralTypeSystemManifest), manifestPayload, nil)

		child := createArtefact(string(blackboard.StructuralTypeStandard), "data", []string{manifest.ID})

		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.False(t, info.IsDetached)
		assert.Equal(t, manifest.ID, info.ManifestID)
		assert.Equal(t, "hash2", info.ConfigHash)

		// Verify caching
		assert.Contains(t, cache, manifest.ID)
	})

	// Scenario 3: Detached (no manifest parent)
	t.Run("Detached", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		parent := createArtefact(string(blackboard.StructuralTypeStandard), "data", nil)
		child := createArtefact(string(blackboard.StructuralTypeStandard), "data", []string{parent.ID})

		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.True(t, info.IsDetached)
	})

	// Scenario 4: Cache hit
	t.Run("CacheHit", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)

		// Create a real manifest first to get its hash ID
		manifestPayload := `{"config_hash": "cached-hash", "git_commit": "cached-commit"}`
		cachedManifest := createArtefact(string(blackboard.StructuralTypeSystemManifest), manifestPayload, nil)

		// Pre-populate cache with the manifest's hash ID
		cache[cachedManifest.ID] = &SpineInfo{
			ManifestID: cachedManifest.ID,
			ConfigHash: "cached-hash",
			GitCommit:  "cached-commit",
			IsDetached: false,
		}

		child := createArtefact(string(blackboard.StructuralTypeStandard), "data", []string{cachedManifest.ID})

		// Should resolve from cache
		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.Equal(t, "cached-hash", info.ConfigHash)
	})
}
