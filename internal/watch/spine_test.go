package watch

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSpineVisibility(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisOpts := &redis.Options{Addr: mr.Addr()}
	client, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer client.Close()
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
	require.NoError(t, client.CreateArtefact(ctx, manifest))

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
	require.NoError(t, client.CreateArtefact(ctx, artefact))

	// 3. Test Formatter
	var buf []byte
	writer := &testWriter{buf: &buf}
	formatter := &defaultFormatter{
		writer: writer,
		client: client, // Pass client for spine resolution
	}

	err = formatter.FormatArtefact(artefact)
	require.NoError(t, err)

	output := string(buf)
	require.Contains(t, output, "✨ Artefact created")
	require.Contains(t, output, "type=GoalDefined")
	require.Contains(t, output, "anchored to spine=")
	require.Contains(t, output, "manifest") // Truncated ID (manifest-123 -> manifest)
}
