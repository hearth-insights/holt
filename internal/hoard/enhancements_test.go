package hoard

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/internal/spine"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoardEnhancements(t *testing.T) {
	t.Run("spine resolution", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// 1. Create SystemManifest with proper hash ID
		manifestPayload := `{"config_hash": "hash123", "git_commit": "commit123"}`
		manifest := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeSystemManifest,
				Type:            "SystemManifest",
				ProducedByRole:  "orchestrator",
				ParentHashes:    []string{},
				CreatedAtMs:     1234567890,
			},
			Payload: blackboard.ArtefactPayload{
				Content: manifestPayload,
			},
		}
		hash, err := blackboard.ComputeArtefactHash(manifest)
		require.NoError(t, err)
		manifest.ID = hash
		require.NoError(t, bbClient.CreateArtefact(ctx, manifest))

		// 2. Create Anchored Artefact
		artefact := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "user",
				ParentHashes:    []string{manifest.ID},
				CreatedAtMs:     1234567891,
			},
			Payload: blackboard.ArtefactPayload{
				Content: "goal",
			},
		}
		hash, err = blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// 3. List with spine
		var buf bytes.Buffer
		opts := &ListOptions{
			WithSpine: true,
			Format:    OutputFormatDefault,
		}
		err = ListArtefacts(ctx, bbClient, "test-instance", opts, &buf)
		require.NoError(t, err)

		output := buf.String()
		// Check that spine column exists and contains commit hash
		assert.Contains(t, output, "SPINE")
		assert.Contains(t, output, "commit12") // Truncated hash
	})

	t.Run("json output with fields", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// Create artefact with proper hash ID
		artefact := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "user",
				ParentHashes:    []string{},
				CreatedAtMs:     1234567890,
			},
			Payload: blackboard.ArtefactPayload{
				Content: "goal",
			},
		}
		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// List with JSON and fields (using top-level fields)
		var buf bytes.Buffer
		opts := &ListOptions{
			Format: "json",
			Fields: []string{"id", "header"},
		}
		err = ListArtefacts(ctx, bbClient, "test-instance", opts, &buf)
		require.NoError(t, err)

		// Parse output
		var result []map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		require.Len(t, result, 1)

		item := result[0]
		assert.Equal(t, artefact.ID, item["id"])
		assert.Contains(t, item, "header")     // Header should be present
		assert.NotContains(t, item, "payload") // Payload should not be present
	})

	t.Run("json output with spine field", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// Create Manifest with proper hash ID
		manifestPayload := `{"config_hash": "hash123", "git_commit": "commit123"}`
		manifest := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeSystemManifest,
				Type:            "SystemManifest",
				ProducedByRole:  "orchestrator",
				ParentHashes:    []string{},
				CreatedAtMs:     1234567890,
			},
			Payload: blackboard.ArtefactPayload{
				Content: manifestPayload,
			},
		}
		hash, err := blackboard.ComputeArtefactHash(manifest)
		require.NoError(t, err)
		manifest.ID = hash
		require.NoError(t, bbClient.CreateArtefact(ctx, manifest))

		// Create Artefact with proper hash ID
		artefact := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "user",
				ParentHashes:    []string{manifest.ID},
				CreatedAtMs:     1234567891,
			},
			Payload: blackboard.ArtefactPayload{
				Content: "goal",
			},
		}
		hash, err = blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// List with JSON and spine field
		var buf bytes.Buffer
		opts := &ListOptions{
			WithSpine: true, // Must be true to resolve spine
			Format:    "json",
			Fields:    []string{"id", "spine"},
			Filters:   &FilterCriteria{TypeGlob: "GoalDefined"},
		}
		err = ListArtefacts(ctx, bbClient, "test-instance", opts, &buf)
		require.NoError(t, err)

		// Parse output
		var result []struct {
			ID    string           `json:"id"`
			Spine *spine.SpineInfo `json:"spine"`
		}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		require.Len(t, result, 1)

		item := result[0]
		assert.Equal(t, artefact.ID, item.ID)
		require.NotNil(t, item.Spine)
		assert.Equal(t, "commit123", item.Spine.GitCommit)
	})
}
