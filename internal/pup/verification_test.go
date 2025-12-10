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
	targetArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{}, // Root artefact
			LogicalThreadID: uuid.New().String(),
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
	err = bbClient.WriteVerifiableArtefact(ctx, targetArtefact)
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

	// Convert V2 artefact to V1 for function call (transitional compatibility)
	targetArtefactV1 := &blackboard.Artefact{
		ID:              targetArtefact.ID,
		LogicalID:       targetArtefact.Header.LogicalThreadID,
		Version:         targetArtefact.Header.Version,
		StructuralType:  targetArtefact.Header.StructuralType,
		Type:            targetArtefact.Header.Type,
		Payload:         targetArtefact.Payload.Content,
		SourceArtefacts: targetArtefact.Header.ParentHashes,
		ProducedByRole:  targetArtefact.Header.ProducedByRole,
		CreatedAtMs:     targetArtefact.Header.CreatedAtMs,
	}

	// Call createVerifiableResultArtefact
	result, err := engine.createVerifiableResultArtefact(ctx, claim, output, targetArtefactV1)
	require.NoError(t, err, "Should successfully create verifiable artefact")

	// Verify artefact structure
	assert.NotEmpty(t, result.ID, "ID should be set")
	assert.Len(t, result.ID, 64, "ID should be 64-char SHA-256 hash")
	assert.Equal(t, "TestResult", result.Header.Type)
	assert.Equal(t, "test payload content", result.Payload.Content)
	assert.Equal(t, []string{targetArtefactV1.ID}, result.Header.ParentHashes)
	assert.Equal(t, "test-agent", result.Header.ProducedByRole)
	assert.Equal(t, blackboard.StructuralTypeStandard, result.Header.StructuralType)

	// Verify hash can be recomputed
	recomputedHash, err := blackboard.ComputeArtefactHash(result)
	require.NoError(t, err)
	assert.Equal(t, result.ID, recomputedHash, "Hash should be deterministic")

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

	// Create target artefact
	targetArtefact := &blackboard.Artefact{
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Generate large file",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}

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

	// Call createVerifiableResultArtefact (should fail)
	result, err := engine.createVerifiableResultArtefact(ctx, claim, output, targetArtefact)
	assert.Error(t, err, "Should reject oversized payload")
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "payload validation failed", "Error should mention payload validation")
}

// TestCreateVerifiableResultArtefact_ParentHashExtraction verifies parent hash extraction.
// TODO M4.6: Fix test to use V2 parent artefacts (currently uses V1 UUID parents)
