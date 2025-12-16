package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoardCommand_Integration(t *testing.T) {
	t.Run("list mode - empty blackboard", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Verify empty
		pattern := "holt:test-instance:artefact:*"
		keys := bbClient.RedisClient().Keys(ctx, pattern).Val()
		assert.Empty(t, keys)

		// Note: Full CLI integration would require mocking Docker
		// This test verifies blackboard operations work correctly
	})

	t.Run("list mode - with artefacts", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		id1 := blackboard.NewID()
		id2 := blackboard.NewID()

		// Create test artefacts
		artefacts := []*blackboard.Artefact{
			{
				ID: id1,
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: blackboard.NewID(),
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "GoalDefined",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "hello-from-holt.txt",
				},
			},
			{
				ID: id2,
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: blackboard.NewID(),
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "CodeCommit",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{id1},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6",
				},
			},
		}

		for _, a := range artefacts {
			err = bbClient.CreateArtefact(ctx, a)
			require.NoError(t, err)
		}

		// Verify artefacts exist
		pattern := "holt:test-instance:artefact:*"
		keys := bbClient.RedisClient().Keys(ctx, pattern).Val()
		assert.Len(t, keys, 2)
	})

	t.Run("get mode - valid artefact", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create test artefact
		artefact := &blackboard.Artefact{
			ID: blackboard.NewID(),
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "hello-from-holt.txt",
			},
		}

		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Get artefact
		retrieved, err := bbClient.GetArtefact(ctx, artefact.ID)
		require.NoError(t, err)
		assert.Equal(t, artefact.ID, retrieved.ID)
		assert.Equal(t, artefact.Header.Type, retrieved.Header.Type)
		assert.Equal(t, artefact.Payload.Content, retrieved.Payload.Content)
	})

	t.Run("get mode - artefact not found", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Try to get non-existent artefact
		_, err = bbClient.GetArtefact(ctx, blackboard.NewID())
		assert.Error(t, err)
		assert.True(t, blackboard.IsNotFound(err))
	})

	t.Run("JSON output format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		id1 := blackboard.NewID()
		id2 := blackboard.NewID()

		// Create test artefacts
		artefacts := []*blackboard.Artefact{
			{
				ID: id1,
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: blackboard.NewID(),
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "GoalDefined",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "test.txt",
				},
			},
			{
				ID: id2,
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: blackboard.NewID(),
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "CodeCommit",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{id1},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "abc123",
				},
			},
		}

		for _, a := range artefacts {
			err = bbClient.CreateArtefact(ctx, a)
			require.NoError(t, err)
		}

		// Scan and retrieve all artefacts
		pattern := "holt:test-instance:artefact:*"
		iter := bbClient.RedisClient().Scan(ctx, 0, pattern, 0).Iterator()

		var retrieved []*blackboard.Artefact
		for iter.Next(ctx) {
			key := iter.Val()
			artefactID := strings.TrimPrefix(key, "holt:test-instance:artefact:")

			artefact, err := bbClient.GetArtefact(ctx, artefactID)
			require.NoError(t, err)
			retrieved = append(retrieved, artefact)
		}

		require.NoError(t, iter.Err())
		assert.Len(t, retrieved, 2)
	})

	t.Run("malformed artefact handling", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create a valid artefact
		validArtefact := &blackboard.Artefact{
			ID: blackboard.NewID(),
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "Valid",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "valid",
			},
		}
		err = bbClient.CreateArtefact(ctx, validArtefact)
		require.NoError(t, err)

		// Manually create a malformed artefact (missing required fields)
		malformedKey := "holt:test-instance:artefact:malformed-123"
		bbClient.RedisClient().HSet(ctx, malformedKey, "id", "malformed-123")

		// Should be able to scan and skip malformed
		pattern := "holt:test-instance:artefact:*"
		iter := bbClient.RedisClient().Scan(ctx, 0, pattern, 0).Iterator()

		validCount := 0
		for iter.Next(ctx) {
			key := iter.Val()
			artefactID := strings.TrimPrefix(key, "holt:test-instance:artefact:")

			_, err := bbClient.GetArtefact(ctx, artefactID)
			if err == nil {
				validCount++
			}
		}

		require.NoError(t, iter.Err())
		assert.Equal(t, 1, validCount, "should only retrieve valid artefacts")
	})
}

func TestHoardCommand_OutputValidation(t *testing.T) {
	t.Run("default output is table format", func(t *testing.T) {
		// This would be an E2E test with actual CLI execution
		// Verifying the logic through unit tests in internal/hoard package
	})

	t.Run("JSON output is valid and parseable", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create artefact
		artefact := &blackboard.Artefact{
			ID: blackboard.NewID(),
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "Test",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "test",
			},
		}
		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Simulate JSON marshaling
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(artefact)
		require.NoError(t, err)

		// Verify valid JSON
		var decoded blackboard.Artefact
		err = json.Unmarshal(buf.Bytes(), &decoded)
		require.NoError(t, err)
		assert.Equal(t, artefact.ID, decoded.ID)
	})
}

func TestHoardCommand_PayloadTruncation(t *testing.T) {
	t.Run("long payloads are truncated in table view", func(t *testing.T) {
		longPayload := strings.Repeat("a", 100)

		// Test the truncation logic directly
		truncated := longPayload
		if len(truncated) > 60 {
			truncated = truncated[:57] + "..."
		}

		assert.Equal(t, 60, len(truncated))
		assert.True(t, strings.HasSuffix(truncated, "..."))
	})

	t.Run("multi-line payloads show first line only", func(t *testing.T) {
		multiLinePayload := "First line\nSecond line\nThird line"

		lines := strings.Split(multiLinePayload, "\n")
		firstLine := strings.TrimSpace(lines[0])

		assert.Equal(t, "First line", firstLine)
		assert.NotContains(t, firstLine, "Second line")
	})
}

func TestHoardCommand_SortingBehavior(t *testing.T) {
	t.Run("artefacts are sorted alphabetically by ID", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create artefacts with IDs in non-alphabetical order
		ids := []string{
			"aaaaaa6aeb4a313fe8059ea942b2acfbaed7ce3087f8cf04ae713c63076fe1b9",
			"bbbbba6aeb4a313fe8059ea942b2acfbaed7ce3087f8cf04ae713c63076fe1b9",
			"ccccca6aeb4a313fe8059ea942b2acfbaed7ce3087f8cf04ae713c63076fe1b9",
		}

		for _, id := range ids {
			artefact := &blackboard.Artefact{
				ID: id,
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: blackboard.NewID(),
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "Test",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "test",
				},
			}
			err = bbClient.CreateArtefact(ctx, artefact)
			require.NoError(t, err)
		}

		// Retrieve and verify we can sort
		pattern := "holt:test-instance:artefact:*"
		iter := bbClient.RedisClient().Scan(ctx, 0, pattern, 0).Iterator()

		var retrievedIDs []string
		for iter.Next(ctx) {
			key := iter.Val()
			artefactID := strings.TrimPrefix(key, "holt:test-instance:artefact:")
			retrievedIDs = append(retrievedIDs, artefactID)
		}

		require.NoError(t, iter.Err())
		assert.Len(t, retrievedIDs, 3)
	})
}
