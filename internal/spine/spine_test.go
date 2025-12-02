package spine

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
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

	// Helper to create artefact
	createArtefact := func(id, structType, payload string, sources []string) *blackboard.Artefact {
		art := &blackboard.Artefact{
			ID:              id,
			LogicalID:       id,
			Version:         1,
			StructuralType:  blackboard.StructuralType(structType),
			Type:            "TestType",
			ProducedByRole:  "test",
			Payload:         payload,
			SourceArtefacts: sources,
		}
		err := bbClient.CreateArtefact(ctx, art)
		require.NoError(t, err)
		return art
	}

	// Scenario 1: Artefact IS a SystemManifest
	t.Run("ArtefactIsManifest", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		manifestPayload := `{"config_hash": "hash1", "git_commit": "commit1"}`
		manifest := createArtefact("manifest-1", string(blackboard.StructuralTypeSystemManifest), manifestPayload, nil)

		info, err := ResolveSpine(ctx, bbClient, manifest, cache)
		require.NoError(t, err)
		assert.False(t, info.IsDetached)
		assert.Equal(t, "manifest-1", info.ManifestID)
		assert.Equal(t, "hash1", info.ConfigHash)
		assert.Equal(t, "commit1", info.GitCommit)
	})

	// Scenario 2: Artefact has SystemManifest as parent
	t.Run("ParentIsManifest", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		manifestPayload := `{"config_hash": "hash2", "git_commit": "commit2"}`
		manifest := createArtefact("manifest-2", string(blackboard.StructuralTypeSystemManifest), manifestPayload, nil)
		
		child := createArtefact("child-1", string(blackboard.StructuralTypeStandard), "data", []string{manifest.ID})

		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.False(t, info.IsDetached)
		assert.Equal(t, "manifest-2", info.ManifestID)
		assert.Equal(t, "hash2", info.ConfigHash)
		
		// Verify caching
		assert.Contains(t, cache, manifest.ID)
	})

	// Scenario 3: Detached (no manifest parent)
	t.Run("Detached", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		parent := createArtefact("parent-1", string(blackboard.StructuralTypeStandard), "data", nil)
		child := createArtefact("child-detached", string(blackboard.StructuralTypeStandard), "data", []string{parent.ID})

		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.True(t, info.IsDetached)
	})

	// Scenario 4: Cache hit
	t.Run("CacheHit", func(t *testing.T) {
		cache := make(map[string]*SpineInfo)
		// Pre-populate cache
		cache["cached-manifest"] = &SpineInfo{
			ManifestID: "cached-manifest",
			ConfigHash: "cached-hash",
			GitCommit:  "cached-commit",
			IsDetached: false,
		}

		child := createArtefact("child-cached", string(blackboard.StructuralTypeStandard), "data", []string{"cached-manifest"})

		// Should resolve from cache without fetching artefact (which doesn't exist in DB)
		info, err := ResolveSpine(ctx, bbClient, child, cache)
		require.NoError(t, err)
		assert.Equal(t, "cached-hash", info.ConfigHash)
	})
}
