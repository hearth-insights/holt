package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTopologyValidation_ValidRootArtefact tests that root artefacts with empty ClaimID pass validation.
func TestTopologyValidation_ValidRootArtefact(t *testing.T) {
	ctx := context.Background()
	engine, _, _ := setupTestEngine(t)

	// Create root artefact (CLI/user-generated)
	rootArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{}, // Empty - root artefact
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user", // Root artefact
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ClaimID:         "", // Empty - no claim for root
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Create hello.txt",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(rootArtefact)
	require.NoError(t, err)
	rootArtefact.ID = hash

	// Verification should pass
	err = engine.verifyArtefact(ctx, rootArtefact)
	assert.NoError(t, err, "Valid root artefact should pass topology validation")
}

// TestTopologyValidation_ValidOrchestratorArtefact tests that orchestrator artefacts pass validation.
func TestTopologyValidation_ValidOrchestratorArtefact(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create parent artefact so it's not an orphan
	parentHash := "parent-hash-123"
	parentArtefact := &blackboard.Artefact{
		ID:              parentHash,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "Test",
		Payload:         "test",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Create orchestrator artefact (Failure/Terminal)
	orchArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentHash},
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "orchestrator", // Trusted authority
			StructuralType:  blackboard.StructuralTypeFailure,
			Type:            "SystemFailure",
			ClaimID:         "", // May be empty for global events
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Agent timeout",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(orchArtefact)
	require.NoError(t, err)
	orchArtefact.ID = hash

	// Verification should pass (orchestrator is exempt from parent linkage checks, but orphan check still applies)
	err = engine.verifyArtefact(ctx, orchArtefact)
	assert.NoError(t, err, "Orchestrator artefact should pass topology validation")
}

// TestTopologyValidation_ValidAgentArtefact tests that agent artefacts with valid claim pass validation.
func TestTopologyValidation_ValidAgentArtefact(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create parent artefact
	parentID := blackboard.NewID()
	parentArtefact := &blackboard.Artefact{
		ID:              parentID,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Create feature",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Create active claim for the parent artefact
	claimID := blackboard.NewID()
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            parentID,
		Status:                blackboard.ClaimStatusPendingExclusive, // Active status
		GrantedExclusiveAgent: "test-agent",
	}
	err = bbClient.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Create agent artefact with valid topology
	agentArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentID}, // References parent
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         claimID, // Valid claim
		},
		Payload: blackboard.ArtefactPayload{
			Content: "git-hash-123",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(agentArtefact)
	require.NoError(t, err)
	agentArtefact.ID = hash

	// Verification should pass
	err = engine.verifyArtefact(ctx, agentArtefact)
	assert.NoError(t, err, "Valid agent artefact should pass topology validation")
}

// TestTopologyValidation_RejectRootArtefactWithClaim tests rejection of root artefacts with ClaimID.
func TestTopologyValidation_RejectRootArtefactWithClaim(t *testing.T) {
	ctx := context.Background()
	engine, _, _ := setupTestEngine(t)

	// Create malicious root artefact with ClaimID
	maliciousRoot := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{}, // Root
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ClaimID:         "invalid-claim-123", // INVALID - root should not have claim
		},
		Payload: blackboard.ArtefactPayload{
			Content: "Malicious goal",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(maliciousRoot)
	require.NoError(t, err)
	maliciousRoot.ID = hash

	// Verification should fail
	err = engine.verifyArtefact(ctx, maliciousRoot)
	assert.Error(t, err, "Root artefact with ClaimID should be rejected")
	assert.Contains(t, err.Error(), "topology violation", "Error should mention topology violation")
	assert.Contains(t, err.Error(), "user/cli artefact", "Error should mention user/cli artefact")
}

// TestTopologyValidation_RejectMissingClaimID tests rejection of agent artefacts without ClaimID.
func TestTopologyValidation_RejectMissingClaimID(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create parent artefact
	parentID := blackboard.NewID()
	parentArtefact := &blackboard.Artefact{
		ID:              parentID,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Create feature",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Create agent artefact WITHOUT ClaimID
	unboundArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentID},
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "malicious-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         "", // MISSING - agent artefacts must have ClaimID
		},
		Payload: blackboard.ArtefactPayload{
			Content: "unauthorized-work",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(unboundArtefact)
	require.NoError(t, err)
	unboundArtefact.ID = hash

	// Verification should fail
	err = engine.verifyArtefact(ctx, unboundArtefact)
	assert.Error(t, err, "Agent artefact without ClaimID should be rejected")
	assert.Contains(t, err.Error(), "missing required ClaimID", "Error should mention missing ClaimID")
}

// TestTopologyValidation_RejectInvalidClaimReference tests rejection when ClaimID doesn't exist.
func TestTopologyValidation_RejectInvalidClaimReference(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create parent artefact
	parentID := blackboard.NewID()
	parentArtefact := &blackboard.Artefact{
		ID:              parentID,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Create feature",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Create agent artefact with non-existent ClaimID
	invalidClaim := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentID},
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         "nonexistent-claim-uuid", // INVALID - claim doesn't exist
		},
		Payload: blackboard.ArtefactPayload{
			Content: "work",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(invalidClaim)
	require.NoError(t, err)
	invalidClaim.ID = hash

	// Verification should fail
	err = engine.verifyArtefact(ctx, invalidClaim)
	assert.Error(t, err, "Artefact with invalid ClaimID should be rejected")
	assert.Contains(t, err.Error(), "ClaimID does not exist", "Error should mention ClaimID doesn't exist")
}

// TestTopologyValidation_RejectTerminatedClaim tests rejection when claim is terminated.
func TestTopologyValidation_RejectTerminatedClaim(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create parent artefact
	parentID := blackboard.NewID()
	parentArtefact := &blackboard.Artefact{
		ID:              parentID,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Create feature",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, parentArtefact)
	require.NoError(t, err)

	// Create TERMINATED claim
	claimID := blackboard.NewID()
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            parentID,
		Status:                blackboard.ClaimStatusTerminated, // TERMINATED - should be rejected
		GrantedExclusiveAgent: "test-agent",
	}
	err = bbClient.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Agent tries to use terminated claim
	reusedClaim := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentID},
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         claimID, // INVALID - claim is terminated
		},
		Payload: blackboard.ArtefactPayload{
			Content: "work",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(reusedClaim)
	require.NoError(t, err)
	reusedClaim.ID = hash

	// Verification should fail
	err = engine.verifyArtefact(ctx, reusedClaim)
	assert.Error(t, err, "Artefact with terminated claim should be rejected")
	assert.Contains(t, err.Error(), "non-active claim", "Error should mention non-active claim")
}

// TestTopologyValidation_RejectParentLinkageViolation tests rejection when parent doesn't match claim target.
func TestTopologyValidation_RejectParentLinkageViolation(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create artefact A
	artefactA := blackboard.NewID()
	artA := &blackboard.Artefact{
		ID:              artefactA,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Feature A",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, artA)
	require.NoError(t, err)

	// Create artefact B (unrelated)
	artefactB := blackboard.NewID()
	artB := &blackboard.Artefact{
		ID:              artefactB,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Feature B",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err = bbClient.CreateArtefact(ctx, artB)
	require.NoError(t, err)

	// Create claim for artefact A
	claimID := blackboard.NewID()
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            artefactA, // Claim is for A
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
	}
	err = bbClient.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Agent submits work referencing artefact B (not A)
	wrongParent := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{artefactB}, // WRONG - claim is for A, not B
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         claimID,
		},
		Payload: blackboard.ArtefactPayload{
			Content: "work-on-wrong-task",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(wrongParent)
	require.NoError(t, err)
	wrongParent.ID = hash

	// Verification should fail
	err = engine.verifyArtefact(ctx, wrongParent)
	assert.Error(t, err, "Artefact with wrong parent should be rejected")
	assert.Contains(t, err.Error(), "topology violation: no parent matches claim target", "Error should mention parent linkage violation")

	// Security alert published
	locked, alert, err := bbClient.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked, "System should be in lockdown")
	assert.Equal(t, "unauthorized_topology", alert.Type)
	assert.Equal(t, "parent_linkage_violation", alert.ViolationType)
	assert.Equal(t, artefactA, alert.ExpectedParentArtefact) // artefactA is the expected parent from claim
}

// TestTopologyValidation_AllowMultipleParents tests that artefacts with multiple parents pass if one matches.
func TestTopologyValidation_AllowMultipleParents(t *testing.T) {
	ctx := context.Background()
	engine, bbClient, _ := setupTestEngine(t)

	// Create artefact A (target)
	artefactA := blackboard.NewID()
	artA := &blackboard.Artefact{
		ID:              artefactA,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Feature A",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err := bbClient.CreateArtefact(ctx, artA)
	require.NoError(t, err)

	// Create artefact B (context)
	artefactB := blackboard.NewID()
	artB := &blackboard.Artefact{
		ID:              artefactB,
		LogicalID:       blackboard.NewID(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "Requirements",
		Payload:         "Requirements doc",
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}
	err = bbClient.CreateArtefact(ctx, artB)
	require.NoError(t, err)

	// Create claim for artefact A
	claimID := blackboard.NewID()
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            artefactA,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
	}
	err = bbClient.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Agent submits work with multiple parents (A + B for context)
	multiParent := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{artefactA, artefactB}, // Both A (assigned) and B (context)
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "test-agent",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ClaimID:         claimID,
		},
		Payload: blackboard.ArtefactPayload{
			Content: "implementation-with-context",
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(multiParent)
	require.NoError(t, err)
	multiParent.ID = hash

	// Verification should PASS (at least one parent matches)
	err = engine.verifyArtefact(ctx, multiParent)
	assert.NoError(t, err, "Artefact with multiple parents should pass if one matches claim target")
}
