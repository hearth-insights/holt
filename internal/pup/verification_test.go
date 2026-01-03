package pup

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateVerifiableResultArtefact_Success verifies V2 artefact creation with hash computation.
func TestCreateVerifiableResultArtefact_Success(t *testing.T) {
	ctx := context.Background()

	// Setup miniredis
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	defer mr.Close()

	// Create blackboard client
	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	// Create pup engine
	config := &Config{
		InstanceName: "test-instance",
		AgentName:    "test-agent",
	}
	engine := &Engine{
		config:   config,
		bbClient: bbClient,
	}

	// Create a V2 target artefact with hash ID (full V2 flow)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{}, // Root artefact
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Create hello.txt",
		},
	}

	// Compute and set hash
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash

	// Write to blackboard
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create claim
	claim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
	}

	// Create tool output (use non-CodeCommit type to avoid git validation in tests)
	output := &ToolOutput{
		ArtefactType:    "TestResult",
		ArtefactPayload: "test payload content",
		Summary:         "Test completed",
	}

	// Call createResultArtefact (was createVerifiableResultArtefact)
	result, err := engine.createResultArtefact(ctx, claim, output, targetArtefact, "{}") // Use "{}" for consistent hashing
	require.NoError(t, err, "Should successfully create verifiable artefact")

	// Verify artefact structure
	assert.NotEmpty(t, result.ID, "ID should be set")
	assert.Len(t, result.ID, 64, "ID should be 64-char SHA-256 hash")
	assert.Equal(t, "TestResult", result.Header.Type)
	assert.Equal(t, "test payload content", result.Payload.Content)
	assert.Equal(t, []string{targetArtefact.ID}, result.Header.ParentHashes)
	assert.Equal(t, "test-agent", result.Header.ProducedByRole)
	assert.Equal(t, blackboard.StructuralTypeStandard, result.Header.StructuralType)

	// Verify hash can be recomputed
	recomputedHash, err := blackboard.ComputeArtefactHash(result)
	require.NoError(t, err)
	// Hash will vary due to timestamp, so we only verify it's a valid hash and recomputable
	assert.Equal(t, result.ID, recomputedHash, "Hash should be deterministic for the same content")

	// Verify artefact was written to Redis
	exists, err := bbClient.ArtefactExists(ctx, result.ID)
	require.NoError(t, err)
	assert.True(t, exists, "Artefact should exist in Redis")
}

// TestCreateVerifiableResultArtefact_PayloadSizeValidation verifies 1MB limit enforcement.
func TestCreateVerifiableResultArtefact_PayloadSizeValidation(t *testing.T) {
	ctx := context.Background()

	// Setup miniredis
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	defer mr.Close()

	// Create blackboard client
	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	// Create pup engine
	config := &Config{
		InstanceName: "test-instance",
		AgentName:    "test-agent",
	}
	engine := &Engine{
		config:   config,
		bbClient: bbClient,
	}

	// Create target artefact (valid hash)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ProducedByRole:  "user",
			CreatedAtMs:     time.Now().UnixMilli(),
			ParentHashes:    []string{},
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Generate large file",
		},
	}
	targetHash, _ := blackboard.ComputeArtefactHash(targetArtefact)
	targetArtefact.ID = targetHash

	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create claim
	claim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
	}

	// Create tool output with >1MB payload
	largePayload := make([]byte, 2*1024*1024) // 2MB
	for i := range largePayload {
		largePayload[i] = 'A'
	}

	output := &ToolOutput{
		ArtefactType:    "LargeFile",
		ArtefactPayload: string(largePayload),
		Summary:         "Created large file",
	}

	// Call createResultArtefact (should fail)
	result, err := engine.createResultArtefact(ctx, claim, output, targetArtefact, "")
	assert.Error(t, err, "Should reject oversized payload")
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "payload validation failed", "Error should mention payload validation")
}

// TestCreateVerifiableResultArtefact_ParentHashExtraction verifies parent hash extraction.
// TODO M4.6: Fix test to use V2 parent artefacts (currently uses V1 UUID parents)
