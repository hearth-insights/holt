package hoard

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetArtefact(t *testing.T) {
	t.Run("valid artefact ID", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create test artefact with proper hash ID
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
				Content: "test payload",
			},
		}

		// Compute hash ID
		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash

		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Get artefact
		var buf bytes.Buffer
		err = GetArtefact(ctx, bbClient, artefact.ID, &buf)
		require.NoError(t, err)

		// Verify JSON output
		var result blackboard.Artefact
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, artefact.ID, result.ID)
		assert.Equal(t, artefact.Header.Type, result.Header.Type)
		assert.Equal(t, artefact.Payload.Content, result.Payload.Content)
	})

	t.Run("artefact not found", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Try to get non-existent artefact
		var buf bytes.Buffer
		err = GetArtefact(ctx, bbClient, "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9", &buf)

		require.Error(t, err)
		assert.True(t, IsNotFound(err), "error should be ArtefactNotFoundError")

		// Verify error message
		notFoundErr, ok := err.(*ArtefactNotFoundError)
		require.True(t, ok)
		assert.Equal(t, "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9", notFoundErr.ArtefactID)
		assert.Contains(t, err.Error(), "artefact with ID")
	})

	t.Run("invalid artefact ID format", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Try with invalid UUID
		var buf bytes.Buffer
		err = GetArtefact(ctx, bbClient, "not-a-uuid", &buf)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid artefact ID format")
		assert.Contains(t, err.Error(), "must be a valid UUID")

		// IsNotFound should return false for validation errors
		assert.False(t, IsNotFound(err))
	})

	t.Run("empty artefact ID", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Try with empty UUID
		var buf bytes.Buffer
		err = GetArtefact(ctx, bbClient, "", &buf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid artefact ID format")
	})

	t.Run("valid SHA-256 hash ID", func(t *testing.T) {
		// Setup miniredis
		mr := miniredis.RunT(t)
		defer mr.Close()

		redisOpts := &redis.Options{Addr: mr.Addr()}
		bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
		require.NoError(t, err)
		defer bbClient.Close()

		ctx := context.Background()

		// Create test artefact with SHA-256 hash ID (64 chars)
		hashID := "a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1"
		artefact := &blackboard.Artefact{
			ID: hashID,
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9",
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
		}

		err = bbClient.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Get artefact using the hash ID
		var buf bytes.Buffer
		err = GetArtefact(ctx, bbClient, hashID, &buf)
		require.NoError(t, err)

		// Verify output contains the ID
		assert.Contains(t, buf.String(), hashID)
	})
}

func TestArtefactNotFoundError(t *testing.T) {
	t.Run("error message", func(t *testing.T) {
		err := &ArtefactNotFoundError{ArtefactID: "test-id-123"}
		assert.Equal(t, "artefact with ID 'test-id-123' not found", err.Error())
	})
	t.Run("IsNotFound with ArtefactNotFoundError", func(t *testing.T) {
		err := &ArtefactNotFoundError{ArtefactID: "test-id"}
		assert.True(t, IsNotFound(err))
	})

	t.Run("IsNotFound with other error", func(t *testing.T) {
		err := assert.AnError
		assert.False(t, IsNotFound(err))
	})

	t.Run("IsNotFound with nil", func(t *testing.T) {
		assert.False(t, IsNotFound(nil))
	})
}
