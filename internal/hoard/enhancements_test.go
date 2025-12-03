package hoard

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/internal/spine"
	"github.com/dyluth/holt/pkg/blackboard"
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

		// 1. Create SystemManifest
		manifestID := "manifest-123"
		manifestPayload := `{"config_hash": "hash123", "git_commit": "commit123"}`
		manifest := &blackboard.Artefact{
			ID:             manifestID,
			LogicalID:      "log-man-1",
			Version:        1,
			StructuralType: blackboard.StructuralTypeSystemManifest,
			Type:           "SystemManifest",
			ProducedByRole: "orchestrator",
			Payload:        manifestPayload,
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, manifest))

		// 2. Create Anchored Artefact
		artefact := &blackboard.Artefact{
			ID:              "art-123",
			LogicalID:       "log-art-1",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "GoalDefined",
			ProducedByRole: "user",
			Payload:        "goal",
			SourceArtefacts: []string{manifestID},
		}
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

		// Create artefact
		artefact := &blackboard.Artefact{
			ID:              "art-123",
			LogicalID:       "log-art-1",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "GoalDefined",
			ProducedByRole: "user",
			Payload:        "goal",
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

		// List with JSON and fields
		var buf bytes.Buffer
		opts := &ListOptions{
			Format: "json",
			Fields: []string{"id", "type", "produced_by_role"},
		}
		err = ListArtefacts(ctx, bbClient, "test-instance", opts, &buf)
		require.NoError(t, err)

		// Parse output
		var result []map[string]interface{}
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		require.Len(t, result, 1)

		item := result[0]
		assert.Equal(t, "art-123", item["id"])
		assert.Equal(t, "GoalDefined", item["type"])
		assert.Equal(t, "user", item["produced_by_role"])
		assert.NotContains(t, item, "payload") // Should not be present
	})

	t.Run("json output with spine field", func(t *testing.T) {
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()
		ctx := context.Background()

		// Create Manifest and Artefact
		manifestID := "manifest-123"
		manifestPayload := `{"config_hash": "hash123", "git_commit": "commit123"}`
		manifest := &blackboard.Artefact{
			ID:             manifestID,
			LogicalID:      "log-man-1",
			Version:        1,
			StructuralType: blackboard.StructuralTypeSystemManifest,
			Type:           "SystemManifest",
			ProducedByRole: "orchestrator",
			Payload:        manifestPayload,
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, manifest))

		artefact := &blackboard.Artefact{
			ID:              "art-123",
			LogicalID:       "log-art-1",
			Version:        1,
			StructuralType: blackboard.StructuralTypeStandard,
			Type:           "GoalDefined",
			ProducedByRole: "user",
			Payload:        "goal",
			SourceArtefacts: []string{manifestID},
		}
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
		assert.Equal(t, "art-123", item.ID)
		require.NotNil(t, item.Spine)
		assert.Equal(t, "commit123", item.Spine.GitCommit)
	})
}
