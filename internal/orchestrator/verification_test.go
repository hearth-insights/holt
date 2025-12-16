package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hearth-insights/holt/internal/config"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupVerificationTestEngine creates a test engine with miniredis for verification tests
func setupVerificationTestEngine(t *testing.T, instanceName string, driftToleranceMs int) (*Engine, *blackboard.Client) {
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, instanceName)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	cfg := &config.HoltConfig{
		Orchestrator: &config.OrchestratorConfig{
			TimestampDriftToleranceMs: intPtr(driftToleranceMs),
		},
	}
	engine := NewEngine(client, instanceName, cfg, nil)

	return engine, client
}

// TestVerifyArtefact_Success verifies that a valid artefact passes all validation stages.
func TestVerifyArtefact_Success(t *testing.T) {
	ctx := context.Background()

	// Setup test engine
	engine, _ := setupVerificationTestEngine(t, "test-verify-success", 300000)

	// Create a valid verifiable artefact
	// Use "user" role to make it a valid root artefact (bypassing claim check)
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{}, // Root artefact (no parents)
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user", // Valid root producer
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			ClaimID:         "", // Root has no claim
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test payload content",
		},
	}

	// Compute correct hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	// Verify artefact (should pass)
	err = engine.verifyArtefact(ctx, artefact)
	assert.NoError(t, err, "Valid artefact should pass verification")
}

// TestVerifyArtefact_OrphanBlock verifies orphan block detection and lockdown.
func TestVerifyArtefact_OrphanBlock(t *testing.T) {
	ctx := context.Background()

	// Setup test engine
	engine, client := setupVerificationTestEngine(t, "test-verify-orphan", 300000)

	// Create artefact with non-existent parent
	// Stage 1 (Orphan Check) runs before Stage 3 (Topology), so this should fail
	// with orphan error regardless of topology validity.
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{"nonexistent-parent-hash-123456789abcdef"},
			LogicalThreadID: "thread-123",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test payload",
		},
	}

	// Compute correct hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	// Verify artefact (should fail with orphan block error)
	err = engine.verifyArtefact(ctx, artefact)
	assert.Error(t, err, "Orphan block should be detected")
	assert.Contains(t, err.Error(), "orphan block", "Error should mention orphan block")

	// Verify lockdown was triggered
	locked, alert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked, "System should be in lockdown after orphan block")
	assert.Equal(t, blackboard.AlertTypeOrphanBlock, alert.Type, "Alert type should be orphan_block")
	assert.Equal(t, "global_lockdown", alert.OrchestratorAction)
}

// TestVerifyArtefact_TimestampDrift_Future verifies timestamp drift detection (future).
func TestVerifyArtefact_TimestampDrift_Future(t *testing.T) {
	ctx := context.Background()

	// Setup test engine with tight drift tolerance (1 minute)
	engine, client := setupVerificationTestEngine(t, "test-verify-timestamp-future", 60000)

	// Create artefact with timestamp 2 minutes in the future
	futureTimestamp := time.Now().UnixMilli() + (2 * 60 * 1000)
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     futureTimestamp,
			ProducedByRole:  "user", // User role to pass topology check (Stage 3) if it reached it
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test payload",
		},
	}

	// Compute correct hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	// Verify artefact (should fail with timestamp drift error)
	// Timestamp check (Stage 2) runs before Topology (Stage 3)
	err = engine.verifyArtefact(ctx, artefact)
	assert.Error(t, err, "Future timestamp should be rejected")
	assert.Contains(t, err.Error(), "timestamp too far in future", "Error should mention timestamp drift")

	// Verify lockdown was NOT triggered (timestamp drift is a warning, not lockdown event)
	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked, "System should NOT be in lockdown for timestamp drift")
}

// TestVerifyArtefact_TimestampDrift_Past verifies timestamp drift detection (past).
func TestVerifyArtefact_TimestampDrift_Past(t *testing.T) {
	ctx := context.Background()

	// Setup test engine with tight drift tolerance (1 minute)
	engine, client := setupVerificationTestEngine(t, "test-verify-timestamp-past", 60000)

	// Create artefact with timestamp 2 minutes in the past
	pastTimestamp := time.Now().UnixMilli() - (2 * 60 * 1000)
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     pastTimestamp,
			ProducedByRole:  "user", // User role to pass topology check
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test payload",
		},
	}

	// Compute correct hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	// Verify artefact (should fail with timestamp drift error)
	err = engine.verifyArtefact(ctx, artefact)
	assert.Error(t, err, "Past timestamp should be rejected")
	assert.Contains(t, err.Error(), "timestamp too far in past", "Error should mention timestamp drift")

	// Verify lockdown was NOT triggered
	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked, "System should NOT be in lockdown for timestamp drift")
}

// TestVerifyArtefact_HashMismatch verifies hash mismatch detection and lockdown.
func TestVerifyArtefact_HashMismatch(t *testing.T) {
	ctx := context.Background()

	// Setup test engine
	engine, client := setupVerificationTestEngine(t, "test-verify-hash-mismatch", 300000)

	// Create artefact with INCORRECT hash (tampered)
	// Use "user" role to pass topology check (Stage 3) and reach Hash check (Stage 4)
	artefact := &blackboard.Artefact{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // Wrong hash
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user", // User role avoids need for ClaimID
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test payload content",
		},
	}

	// Verify artefact (should fail with hash mismatch)
	err := engine.verifyArtefact(ctx, artefact)
	assert.Error(t, err, "Hash mismatch should be detected")
	assert.Contains(t, err.Error(), "hash mismatch", "Error should mention hash mismatch")

	// Verify lockdown was triggered
	locked, alert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked, "System should be in lockdown after hash mismatch")
	assert.Equal(t, blackboard.AlertTypeHashMismatch, alert.Type, "Alert type should be hash_mismatch")
	assert.Equal(t, "global_lockdown", alert.OrchestratorAction)
}

// TestVerifyArtefact_ValidParentChain verifies that parent existence check works for valid chains.
func TestVerifyArtefact_ValidParentChain(t *testing.T) {
	ctx := context.Background()

	// Setup test engine
	engine, client := setupVerificationTestEngine(t, "test-verify-parent-chain", 300000)

	// Create parent artefact (root, user)
	parentArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "parent payload",
		},
	}

	// Compute parent hash and write to Redis
	parentHash, err := blackboard.ComputeArtefactHash(parentArtefact)
	require.NoError(t, err)
	parentArtefact.ID = parentHash

	// Write parent to Redis (simulating it exists in the blackboard)
	err = client.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Setup active claim for child to link to
	claimID := "claim-123"
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            parentHash, // Claim is for parent
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
	}
	err = client.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Create child artefact referencing parent, with valid ClaimID
	childArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentHash}, // References parent
			LogicalThreadID: "thread-123",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			ClaimID:         claimID, // Valid claim reference
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "child payload",
		},
	}

	// Compute child hash
	childHash, err := blackboard.ComputeArtefactHash(childArtefact)
	require.NoError(t, err)
	childArtefact.ID = childHash

	// Verify child artefact (should pass - parent exists, topology valid)
	err = engine.verifyArtefact(ctx, childArtefact)
	assert.NoError(t, err, "Valid parent chain should pass verification")

	// Verify no lockdown
	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked, "No lockdown should occur for valid chain")
}

// TestLockdownCheck_EventLoop verifies that the orchestrator event loop checks for lockdown.
func TestLockdownCheck_EventLoop(t *testing.T) {
	// This test verifies that the lockdown check in engine.go:106-116 works.
	// We can't easily test the full event loop in a unit test, but we can verify
	// the lockdown mechanism itself works.

	ctx := context.Background()

	// Setup test engine
	_, client := setupVerificationTestEngine(t, "test-lockdown-check", 300000)

	// Trigger lockdown manually
	alert := &blackboard.SecurityAlert{
		Type:               blackboard.AlertTypeHashMismatch,
		TimestampMs:        time.Now().UnixMilli(),
		ArtefactIDClaimed:  "test-artefact-123",
		HashExpected:       "expected-hash",
		HashActual:         "actual-hash",
		AgentRole:          "test-agent",
		OrchestratorAction: "global_lockdown",
	}

	err := client.TriggerGlobalLockdown(ctx, alert)
	require.NoError(t, err)

	// Verify lockdown is active
	locked, retrievedAlert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.Equal(t, blackboard.AlertTypeHashMismatch, retrievedAlert.Type)

	// Clear lockdown
	err = client.ClearLockdown(ctx, "Test completed", "test-operator")
	require.NoError(t, err)

	// Verify lockdown is cleared
	locked, _, err = client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked)
}

// TestProcessArtefact_RejectsTamperedArtefact is a CRITICAL integration test that verifies
// the orchestrator rejects tampered artefacts BEFORE creating claims.
// This test ensures the verification hook is properly integrated into the main execution path.
func TestProcessArtefact_RejectsTamperedArtefact(t *testing.T) {
	ctx := context.Background()

	// Setup test engine with real orchestrator flow
	engine, client := setupVerificationTestEngine(t, "test-tamper-integration", 300000)

	// Create parent for topology validity
	parentArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{Content: "parent"},
	}
	parentHash, _ := blackboard.ComputeArtefactHash(parentArtefact)
	parentArtefact.ID = parentHash
	client.CreateArtefact(ctx, parentArtefact)

	// Create valid claim
	claimID := uuid.New().String() // Use valid UUID
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            parentHash,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "malicious-agent",
	}
	err := client.CreateClaim(ctx, claim)
	require.NoError(t, err, "Setup: Create valid claim")

	// Create a tampered V2 artefact (hash doesn't match content)
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentHash},
			LogicalThreadID: "thread-123",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "malicious-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "MaliciousCode",
			ClaimID:         claim.ID, // Valid ClaimID to pass topology check
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "legitimate content",
		},
	}

	// Compute correct hash
	correctHash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)

	// TAMPER: Set incorrect hash ID (simulating malicious agent)
	artefact.ID = strings.Repeat("f", 64) // Wrong hash!

	// Write tampered artefact to Redis (bypassing normal checks)
	err = client.CreateArtefact(ctx, artefact)
	require.NoError(t, err, "Setup: Write tampered artefact to Redis")

	// Recompute correct hash in case CreateArtefact modified the struct (e.g. nil slices)
	correctHash, err = blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)

	// CRITICAL TEST: Call processArtefact (main orchestrator flow)
	// This MUST reject the tampered artefact
	err = engine.processArtefact(ctx, artefact)
	assert.Error(t, err, "processArtefact MUST reject tampered artefact")
	assert.Contains(t, err.Error(), "hash mismatch", "Error should indicate hash mismatch")

	// Verify NO claim was created (artefact rejected before claim creation)
	claim, err = client.GetClaimByArtefactID(ctx, artefact.ID)
	// Expect "not found" error or nil claim
	if err != nil && !blackboard.IsNotFound(err) {
		t.Fatalf("Unexpected error checking for claim: %v", err)
	}
	assert.Nil(t, claim, "CRITICAL: No claim should exist for tampered artefact")

	// Verify global lockdown was triggered
	locked, alert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked, "System should be in global lockdown")
	assert.Equal(t, blackboard.AlertTypeHashMismatch, alert.Type)
	assert.Equal(t, artefact.ID, alert.ArtefactIDClaimed)
	assert.Equal(t, correctHash, alert.HashExpected, "Alert should contain correct hash")
	assert.Equal(t, artefact.ID, alert.HashActual, "Alert should contain tampered hash")
}

// TestProcessArtefact_RejectsOrphanBlock verifies orphan block detection in main flow.
func TestProcessArtefact_RejectsOrphanBlock(t *testing.T) {
	ctx := context.Background()

	engine, client := setupVerificationTestEngine(t, "test-orphan-integration", 300000)

	// Create artefact with non-existent parent (must be valid 64-char hex hash)
	artefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{strings.Repeat("a", 64)}, // Valid format but doesn't exist
			LogicalThreadID: "thread-456",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestArtefact",
			// Note: Technically this should fail topology because claim check comes after parent check?
			// Actually: Parent check (Stage 1) comes FIRST.
			// So missing ClaimID won't matter yet. It should fail on parent check.
			Metadata: "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test content",
		},
	}

	// Compute correct hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash

	// Write to Redis
	err = client.CreateArtefact(ctx, artefact)
	require.NoError(t, err)

	// Process artefact - should reject orphan block
	err = engine.processArtefact(ctx, artefact)
	assert.Error(t, err, "processArtefact MUST reject orphan block")
	assert.Contains(t, err.Error(), "orphan block", "Error should indicate orphan block")

	// Verify NO claim created
	claim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
	if err != nil && !blackboard.IsNotFound(err) {
		t.Fatalf("Unexpected error checking for claim: %v", err)
	}
	assert.Nil(t, claim, "No claim should exist for orphan artefact")

	// Verify lockdown triggered
	locked, alert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked, "System should be in lockdown")
	assert.Equal(t, blackboard.AlertTypeOrphanBlock, alert.Type)
}
