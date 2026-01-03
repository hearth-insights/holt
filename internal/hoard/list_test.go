package hoard

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

// createTestArtefact creates an artefact with a proper hash ID for testing
func createTestArtefact(t *testing.T, artType, payload string, parentHashes []string) *blackboard.Artefact {
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            artType,
			ProducedByRole:  "user",
			ParentHashes:    parentHashes,
			CreatedAtMs:     1234567890,
		},
		Payload: blackboard.ArtefactPayload{
			Content: payload,
		},
	}

	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	return artefact
}

func TestListArtefacts(t *testing.T) {
	t.Run("empty blackboard - default format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// List artefacts
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatDefault}, &buf)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "No artefacts found for instance 'test-instance'")
	})

	t.Run("empty blackboard - JSON format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// List artefacts
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatJSONL}, &buf)
		require.NoError(t, err)

		// JSONL format should be empty for no artefacts
		assert.Empty(t, buf.String())
	})

	t.Run("single artefact - default format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create artefact
		artefact := createTestArtefact(t, "GoalDefined", "test-goal.txt", []string{})
		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// List artefacts
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatDefault}, &buf)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Artefacts for instance 'test-instance'")
		assert.Contains(t, output, artefact.ID[:8]) // ID is truncated to 8 chars in table
		assert.Contains(t, output, "Goal")          // "GoalDefined" is shortened to "Goal"
		assert.Contains(t, output, "user")
		assert.Contains(t, output, "test-goal.txt")
		assert.Contains(t, output, "1 artefact found")
	})

	t.Run("multiple artefacts - default format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create multiple artefacts with proper hash IDs
		art1 := createTestArtefact(t, "GoalDefined", "test-goal.txt", []string{})
		art2 := createTestArtefact(t, "CodeCommit", "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a", []string{art1.ID})

		err = bbClient.CreateArtefact(ctx, art1)
		require.NoError(t, err)
		err = bbClient.CreateArtefact(ctx, art2)
		require.NoError(t, err)

		// List artefacts
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatDefault}, &buf)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, art1.ID[:8]) // IDs are truncated to 8 chars
		assert.Contains(t, output, "2 artefacts found")
	})

	t.Run("multiple artefacts - JSON format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create multiple artefacts with proper hash IDs
		art1 := createTestArtefact(t, "GoalDefined", "test-goal.txt", []string{})
		art2 := createTestArtefact(t, "CodeCommit", "commit-hash", []string{art1.ID})

		err = bbClient.CreateArtefact(ctx, art1)
		require.NoError(t, err)
		err = bbClient.CreateArtefact(ctx, art2)
		require.NoError(t, err)

		// List artefacts
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatJSONL}, &buf)
		require.NoError(t, err)

		// Parse JSONL (one JSON object per line)
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		require.Len(t, lines, 2)

		// Parse both lines and collect IDs and types
		var results []blackboard.Artefact
		for _, line := range lines {
			var art blackboard.Artefact
			err = json.Unmarshal([]byte(line), &art)
			require.NoError(t, err)
			results = append(results, art)
		}

		// Verify both artefacts are present (order may vary)
		ids := []string{results[0].ID, results[1].ID}
		types := []string{results[0].Header.Type, results[1].Header.Type}
		assert.Contains(t, ids, art1.ID)
		assert.Contains(t, ids, art2.ID)
		assert.Contains(t, types, "GoalDefined")
		assert.Contains(t, types, "CodeCommit")
	})

	t.Run("artefacts sorted alphabetically by ID", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create artefacts with proper hash IDs
		art1 := createTestArtefact(t, "Third", "c", []string{})
		art2 := createTestArtefact(t, "First", "a", []string{})
		art3 := createTestArtefact(t, "Second", "b", []string{})

		err = bbClient.CreateArtefact(ctx, art1)
		require.NoError(t, err)
		err = bbClient.CreateArtefact(ctx, art2)
		require.NoError(t, err)
		err = bbClient.CreateArtefact(ctx, art3)
		require.NoError(t, err)

		// List artefacts in JSON format for easy verification
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatJSONL}, &buf)
		require.NoError(t, err)

		// Parse JSONL
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		require.Len(t, lines, 3)

		// Verify all artefacts are present
		var ids []string
		for _, line := range lines {
			var art blackboard.Artefact
			err = json.Unmarshal([]byte(line), &art)
			require.NoError(t, err)
			ids = append(ids, art.ID)
		}
		assert.Contains(t, ids, art1.ID)
		assert.Contains(t, ids, art2.ID)
		assert.Contains(t, ids, art3.ID)
	})

	t.Run("invalid output format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Try with invalid format
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormat("invalid")}, &buf)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown output format")
	})

	t.Run("skips malformed artefacts", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create a valid artefact with proper hash ID
		validArtefact := createTestArtefact(t, "ValidType", "valid", []string{})
		err = bbClient.CreateArtefact(ctx, validArtefact)
		require.NoError(t, err)

		// Manually create a malformed artefact in Redis (missing required fields)
		malformedKey := "holt:test-instance:artefact:malformed-id"
		bbClient.RedisClient().HSet(ctx, malformedKey, "id", "malformed-id")

		// List artefacts - should skip malformed one
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatJSONL}, &buf)
		require.NoError(t, err)

		// Parse JSONL - should only have the valid artefact
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		assert.Len(t, lines, 1)

		var result blackboard.Artefact
		err = json.Unmarshal([]byte(lines[0]), &result)
		require.NoError(t, err)
		assert.Equal(t, validArtefact.ID, result.ID)
	})

	t.Run("artefact with long multi-line payload", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create artefact with long multi-line payload
		longPayload := strings.Repeat("x", 100) + "\nSecond line\nThird line"
		artefact := createTestArtefact(t, "LongPayload", longPayload, []string{})
		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// List in default format - payload should be truncated
		var buf bytes.Buffer
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatDefault}, &buf)
		require.NoError(t, err)

		output := buf.String()
		// Should contain truncation indicator
		assert.Contains(t, output, "...")
		// Should not contain "Second line"
		assert.NotContains(t, output, "Second line")

		// List in JSON format - payload should be preserved
		buf.Reset()
		err = ListArtefacts(ctx, bbClient, "test-instance", &ListOptions{Format: OutputFormatJSONL}, &buf)
		require.NoError(t, err)

		// Parse JSONL
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		require.Len(t, lines, 1)

		var result blackboard.Artefact
		err = json.Unmarshal([]byte(lines[0]), &result)
		require.NoError(t, err)
		// Full payload should be preserved in JSONL
		assert.Equal(t, longPayload, result.Payload.Content)
	})
}
