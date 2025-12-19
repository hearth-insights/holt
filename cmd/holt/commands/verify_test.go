package commands

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveV2ShortHash(t *testing.T) {
	s := miniredis.RunT(t)

	client, err := blackboard.NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	instanceName := "test-instance"

	// Setup: Create some dummy keys simulating V2 artefacts
	// Key format: holt:{instance}:artefact:{hash}
	fullHash1 := "a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1"
	fullHash2 := "b4f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a2"

	err = s.Set("holt:"+instanceName+":artefact:"+fullHash1, "data1")
	require.NoError(t, err)
	err = s.Set("holt:"+instanceName+":artefact:"+fullHash2, "data2")
	require.NoError(t, err)

	tests := []struct {
		name        string
		shortHash   string
		expected    string
		expectError bool
	}{
		{
			name:        "Exact match full hash",
			shortHash:   fullHash1,
			expected:    fullHash1,
			expectError: false,
		},
		{
			name:        "Unique prefix match",
			shortHash:   "a3f2b9c4",
			expected:    fullHash1,
			expectError: false,
		},
		{
			name:        "No match",
			shortHash:   "c5f2b9c4",
			expected:    "",
			expectError: true,
		},
		// Miniredis SCAN might not support pattern matching perfectly or order is not guaranteed,
		// but for simple cases it should work.
		// Ambiguous match case:
		// We need two keys sharing a prefix.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolveV2ShortHash(ctx, client, instanceName, tt.shortHash)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}

	// Test Ambiguous Match separately to ensure setup
	t.Run("Ambiguous match", func(t *testing.T) {
		// Add another key sharing prefix with fullHash1
		ambiguousHash := "a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a3"
		err = s.Set("holt:"+instanceName+":artefact:"+ambiguousHash, "data3")
		require.NoError(t, err)

		_, err := resolveV2ShortHash(ctx, client, instanceName, "a3f2b9c4")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ambiguous short hash")
	})
}
