package pup

import (
	"context"
	"testing"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssembleContext_VersionShadowing_DirectV1 reproduces the version shadowing issue
// and verifies the "Direct V1 Link" fix.
// Scenario:
// - Grandparent -> ParentV1
// - ParentV2 (no link to Grandparent)
// - ParentV3 (no link to Grandparent)
// - Target -> ParentV2 (which resolves to ParentV3 as latest)
//
// Without fix: Context contains ParentV3 but MISSES Grandparent (shadowed).
// With fix: Context contains ParentV3 AND Grandparent (merged from V1).
func TestAssembleContext_VersionShadowing_DirectV1(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder")

	// 1. Create Grandparent (The original input we must not lose)
	grandparent := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "ClinicalTerms",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "terms-v1",
		},
	}
	hash, err := blackboard.ComputeArtefactHash(grandparent)
	require.NoError(t, err)
	grandparent.ID = hash
	require.NoError(t, bbClient.CreateArtefact(ctx, grandparent))

	// 2. Create Parent V1 (Links to Grandparent)
	parentLogicalID := blackboard.NewID()
	parentV1 := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: parentLogicalID,
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "HPOMappingResult",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{grandparent.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "mapping-v1",
		},
	}
	v1Hash, err := blackboard.ComputeArtefactHash(parentV1)
	require.NoError(t, err)
	parentV1.ID = v1Hash
	require.NoError(t, bbClient.CreateArtefact(ctx, parentV1))
	require.NoError(t, bbClient.AddVersionToThread(ctx, parentLogicalID, parentV1.ID, 1))

	// 3. Create Parent V2 (Rework - NO link to Grandparent)
	parentV2 := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: parentLogicalID,
			Version:         2,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "HPOMappingResult",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{}, // Missing link!
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "mapping-v2",
		},
	}
	v2Hash, err := blackboard.ComputeArtefactHash(parentV2)
	require.NoError(t, err)
	parentV2.ID = v2Hash
	require.NoError(t, bbClient.CreateArtefact(ctx, parentV2))
	require.NoError(t, bbClient.AddVersionToThread(ctx, parentLogicalID, parentV2.ID, 2))

	// 4. Create Parent V3 (Latest - NO link to Grandparent)
	parentV3 := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: parentLogicalID,
			Version:         3,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "HPOMappingResult",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{}, // Missing link!
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "mapping-v3",
		},
	}
	v3Hash, err := blackboard.ComputeArtefactHash(parentV3)
	require.NoError(t, err)
	parentV3.ID = v3Hash
	require.NoError(t, bbClient.CreateArtefact(ctx, parentV3))
	require.NoError(t, bbClient.AddVersionToThread(ctx, parentLogicalID, parentV3.ID, 3))

	// 5. Create Target (Links to ParentV2 - simulating working from an intermediate state)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "ReviewResult",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{parentV2.ID}, // Links to V2
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "review-v1",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	require.NoError(t, bbClient.CreateArtefact(ctx, targetArtefact))

	// 6. Assemble Context
	// We pass a dummy claim as it's not used for this logic
	dummyClaim := &blackboard.Claim{AdditionalContextIDs: []string{}}
	contextChain, err := engine.assembleContext(ctx, targetArtefact, dummyClaim)
	require.NoError(t, err)

	// 7. Verify Context
	// Should contain:
	// - ParentV3 (Latest version of Parent)
	// - Grandparent (Merged from V1)

	var foundParentV3, foundGrandparent bool
	for _, art := range contextChain {
		if art.ID == parentV3.ID {
			foundParentV3 = true
		}
		if art.ID == grandparent.ID {
			foundGrandparent = true
		}
	}

	assert.True(t, foundParentV3, "Context should contain ParentV3 (latest version)")
	assert.True(t, foundGrandparent, "Context should contain Grandparent (merged from V1 links)")
}
