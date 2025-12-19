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

// setupTestPup creates a pup engine with test configuration for unit tests
// M3.7: agentName IS the role, agentRole parameter kept for compatibility but should match agentName
func setupTestPup(t *testing.T, agentName, agentRole string) (*Engine, *blackboard.Client) {
	// Use miniredis for embedded Redis
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	instanceName := "test-" + uuid.New().String()[:8]
	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, instanceName)
	require.NoError(t, err)
	t.Cleanup(func() { bbClient.Close() })

	cfg := &Config{
		InstanceName:    instanceName,
		AgentName:       agentName, // M3.7: This IS the role
		Command:         []string{"test"},
		BiddingStrategy: BiddingStrategy{Type: blackboard.BidTypeExclusive},
		MaxContextDepth: 10,
	}

	engine := New(cfg, bbClient)

	return engine, bbClient
}

func TestCreateReworkArtefact_VersionIncrement(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create target artefact (v1)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "v1-hash",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create review artefact
	reviewArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "Review",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: `{"issue": "needs tests"}`,
		},
	}
	reviewHash, err := blackboard.ComputeArtefactHash(reviewArtefact)
	require.NoError(t, err)
	reviewArtefact.ID = reviewHash
	err = bbClient.CreateArtefact(ctx, reviewArtefact)
	require.NoError(t, err)

	// Create feedback claim
	feedbackClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingAssignment,
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{reviewArtefact.ID},
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}
	err = bbClient.CreateClaim(ctx, feedbackClaim)
	require.NoError(t, err)

	// Create tool output
	toolOutput := &ToolOutput{
		ArtefactType:    "TestType", // Matches target type
		ArtefactPayload: "v2-hash",
		Summary:         "Fixed tests",
	}

	// Call createReworkArtefact
	reworkArtefact, err := engine.createReworkArtefact(ctx, feedbackClaim, toolOutput)
	require.NoError(t, err)

	// Verify version was incremented
	assert.Equal(t, 2, reworkArtefact.Header.Version)

	// Verify logical_id was preserved
	assert.Equal(t, targetArtefact.Header.LogicalThreadID, reworkArtefact.Header.LogicalThreadID)

	// Verify type was preserved
	assert.Equal(t, targetArtefact.Header.Type, reworkArtefact.Header.Type)

	// Verify payload from tool output
	assert.Equal(t, "v2-hash", reworkArtefact.Payload.Content)

	// Verify source_artefacts includes target + review
	assert.Len(t, reworkArtefact.Header.ParentHashes, 2)
	assert.Contains(t, reworkArtefact.Header.ParentHashes, targetArtefact.ID)
	assert.Contains(t, reworkArtefact.Header.ParentHashes, reviewArtefact.ID)

	// Verify produced by correct role
	assert.Equal(t, "Coder", reworkArtefact.Header.ProducedByRole)

	// Verify structural type from tool output
	assert.Equal(t, blackboard.StructuralTypeStandard, reworkArtefact.Header.StructuralType)
}

func TestCreateReworkArtefact_MultipleReviews(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create target artefact (v2)
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         2,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "v2-hash",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create multiple review artefacts
	review1 := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "Review",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: `{"issue": "needs tests"}`,
		},
	}
	review1Hash, err := blackboard.ComputeArtefactHash(review1)
	require.NoError(t, err)
	review1.ID = review1Hash
	err = bbClient.CreateArtefact(ctx, review1)
	require.NoError(t, err)

	review2 := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "Review",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: `{"issue": "add docs"}`,
		},
	}
	review2Hash, err := blackboard.ComputeArtefactHash(review2)
	require.NoError(t, err)
	review2.ID = review2Hash
	err = bbClient.CreateArtefact(ctx, review2)
	require.NoError(t, err)

	// Create feedback claim with multiple reviews
	feedbackClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingAssignment,
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{review1.ID, review2.ID},
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}
	err = bbClient.CreateClaim(ctx, feedbackClaim)
	require.NoError(t, err)

	// Create tool output
	toolOutput := &ToolOutput{
		ArtefactType:    "CodeCommit",
		ArtefactPayload: "v3-hash",
		Summary:         "Fixed tests and added docs",
	}

	// Call createReworkArtefact
	reworkArtefact, err := engine.createReworkArtefact(ctx, feedbackClaim, toolOutput)
	require.NoError(t, err)

	// Verify version was incremented (v2 → v3)
	assert.Equal(t, 3, reworkArtefact.Header.Version)

	// Verify source_artefacts includes target + both reviews
	assert.Len(t, reworkArtefact.Header.ParentHashes, 3)
	assert.Contains(t, reworkArtefact.Header.ParentHashes, targetArtefact.ID)
	assert.Contains(t, reworkArtefact.Header.ParentHashes, review1.ID)
	assert.Contains(t, reworkArtefact.Header.ParentHashes, review2.ID)
}

func TestAssembleContext_WithAdditionalContextIDs(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create source artefact (GoalDefined)
	goalArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "feature.txt",
		},
	}
	goalHash, err := blackboard.ComputeArtefactHash(goalArtefact)
	require.NoError(t, err)
	goalArtefact.ID = goalHash
	err = bbClient.CreateArtefact(ctx, goalArtefact)
	require.NoError(t, err)

	// Create target artefact (v1) that references goal
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{goalArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "v1-hash",
		},
	}
	targetHash, _ := blackboard.ComputeArtefactHash(targetArtefact)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create review artefact
	reviewArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "Review",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: `{"issue": "needs tests"}`,
		},
	}
	reviewHash, err := blackboard.ComputeArtefactHash(reviewArtefact)
	require.NoError(t, err)
	reviewArtefact.ID = reviewHash
	err = bbClient.CreateArtefact(ctx, reviewArtefact)
	require.NoError(t, err)

	// Create feedback claim with additional context
	feedbackClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingAssignment,
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{reviewArtefact.ID},
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}

	// Call assembleContext with feedback claim
	contextChain, err := engine.assembleContext(ctx, targetArtefact, feedbackClaim)
	require.NoError(t, err)

	// Verify context includes both source artefacts AND additional context
	// Context should have:
	// - goalArtefact (from target's source_artefacts)
	// - reviewArtefact (from claim's additional_context_ids)
	// - targetArtefact (from review's source_artefacts during BFS traversal)
	require.Len(t, contextChain, 3)

	// Find the artefacts in context (order may vary)
	var foundGoal, foundReview, foundTarget bool
	for _, art := range contextChain {
		if art.Header.Type == "GoalDefined" {
			foundGoal = true
		}
		if art.Header.Type == "Review" {
			foundReview = true
		}
		if art.Header.Type == "CodeCommit" {
			foundTarget = true
		}
	}

	assert.True(t, foundGoal, "Context should include GoalDefined from source_artefacts")
	assert.True(t, foundReview, "Context should include Review from AdditionalContextIDs")
	assert.True(t, foundTarget, "Context should include CodeCommit (target) from Review's source_artefacts via BFS")
}

func TestAssembleContext_RegularClaim_NoAdditionalContext(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create source artefact
	goalArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "feature.txt",
		},
	}
	goalHash, err := blackboard.ComputeArtefactHash(goalArtefact)
	require.NoError(t, err)
	goalArtefact.ID = goalHash
	err = bbClient.CreateArtefact(ctx, goalArtefact)
	require.NoError(t, err)

	// Create target artefact
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{goalArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "commit-hash",
		},
	}
	targetHash, _ := blackboard.ComputeArtefactHash(targetArtefact)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create regular claim (no additional context)
	regularClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{}, // Empty
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}

	// Call assembleContext with regular claim
	contextChain, err := engine.assembleContext(ctx, targetArtefact, regularClaim)
	require.NoError(t, err)

	// Verify context only includes source artefacts
	require.Len(t, contextChain, 1)
	assert.Equal(t, "GoalDefined", contextChain[0].Header.Type)
}

func TestCreateResultArtefact_DetectsFeedbackClaim(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create target artefact
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType", // Updated to match output type and avoid git validation
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
		},
		Payload: blackboard.ArtefactPayload{
			Content: "v1-hash",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create review artefact for additional context
	reviewArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "Review",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{targetArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: `{"issue": "needs fixes"}`,
		},
	}
	reviewHash, err := blackboard.ComputeArtefactHash(reviewArtefact)
	require.NoError(t, err)
	reviewArtefact.ID = reviewHash
	err = bbClient.CreateArtefact(ctx, reviewArtefact)
	require.NoError(t, err)

	// Create feedback claim (pending_assignment)
	feedbackClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingAssignment, // Key: pending_assignment
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{reviewArtefact.ID},
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}
	err = bbClient.CreateClaim(ctx, feedbackClaim)
	require.NoError(t, err)

	// Create tool output
	toolOutput := &ToolOutput{
		ArtefactType:    "TestType", // Matches target type
		ArtefactPayload: "v2-payload",
		Summary:         "Fixed",
	}

	// Call createResultArtefact (should call createReworkArtefact internally)
	resultArtefact, err := engine.createResultArtefact(ctx, feedbackClaim, toolOutput, targetArtefact, "")
	require.NoError(t, err)

	// Verify it created a rework artefact (version incremented, logical_id preserved)
	assert.Equal(t, 2, resultArtefact.Header.Version, "Should increment version for feedback claim")
	assert.Equal(t, targetArtefact.Header.LogicalThreadID, resultArtefact.Header.LogicalThreadID, "Should preserve logical_id for feedback claim")
	assert.Equal(t, targetArtefact.Header.Type, resultArtefact.Header.Type, "Should preserve type for feedback claim")
}

func TestCreateResultArtefact_RegularClaim_NewThread(t *testing.T) {
	ctx := context.Background()
	engine, bbClient := setupTestPup(t, "Coder", "Coder") // M3.7: Agent name IS the role

	// Create target artefact
	targetArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ProducedByRole:  "test-agent",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: "goal.txt",
		},
	}
	targetHash, err := blackboard.ComputeArtefactHash(targetArtefact)
	require.NoError(t, err)
	targetArtefact.ID = targetHash
	err = bbClient.CreateArtefact(ctx, targetArtefact)
	require.NoError(t, err)

	// Create regular claim (NOT pending_assignment)
	regularClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID,
		Status:                blackboard.ClaimStatusPendingExclusive, // Regular status
		GrantedExclusiveAgent: "test-agent",
		AdditionalContextIDs:  []string{},
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}
	err = bbClient.CreateClaim(ctx, regularClaim)
	require.NoError(t, err)

	// Create tool output
	toolOutput := &ToolOutput{
		ArtefactType:    "TestArtefact",
		ArtefactPayload: "new-payload",
		Summary:         "Created",
	}

	// Call createResultArtefact (should create new work)
	resultArtefact, err := engine.createResultArtefact(ctx, regularClaim, toolOutput, targetArtefact, "")
	require.NoError(t, err)

	// Verify it created new work (new logical_id, version 1)
	assert.Equal(t, 1, resultArtefact.Header.Version, "Should be version 1 for new work")
	assert.NotEqual(t, targetArtefact.Header.LogicalThreadID, resultArtefact.Header.LogicalThreadID, "Should have new logical_id for new work")
	assert.Equal(t, "TestArtefact", resultArtefact.Header.Type, "Should use type from tool output")
}
