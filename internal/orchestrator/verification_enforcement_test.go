package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockClient is a partial mock for blackboard.Client
type MockClient struct {
	mock.Mock
}

func (m *MockClient) TriggerGlobalLockdown(ctx context.Context, alert *blackboard.SecurityAlert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *MockClient) GetClaim(ctx context.Context, id string) (*blackboard.Claim, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*blackboard.Claim), args.Error(1)
}

func (m *MockClient) ArtefactExists(ctx context.Context, id string) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

// TestVerifyArtefact_ReviewClaimEnforcement verifies that review claims must produce review artefacts
func TestVerifyArtefact_ReviewClaimEnforcement(t *testing.T) {
	// Setup
	// mockClient := new(MockClient) // Unused
	engine := &Engine{
		client: &blackboard.Client{}, // We can't easily mock the struct, so we'll use a different approach
	}
	
	// Since Engine uses a concrete *blackboard.Client, we can't inject our MockClient.
	// Instead, we should use the existing setupVerificationTestEngine from verification_test.go
	// which uses miniredis.
	
	ctx := context.Background()
	engine, client := setupVerificationTestEngine(t, "test-verify-review-enforcement", 300000)

	// 1. Create a parent artefact
	parentArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "thread-123",
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: blackboard.ArtefactPayload{Content: "parent"},
	}
	parentHash, _ := blackboard.ComputeArtefactHash(parentArtefact)
	parentArtefact.ID = parentHash
	client.WriteVerifiableArtefact(ctx, parentArtefact)

	// 2. Create a REVIEW claim
	claimID := "claim-review-123"
	claim := &blackboard.Claim{
		ID:                  claimID,
		ArtefactID:          parentHash,
		Status:              blackboard.ClaimStatusPendingReview,
		GrantedReviewAgents: []string{"reviewer-agent"},
	}
	err := client.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// 3. Create a STANDARD artefact (Violation!)
	violationArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentHash},
			LogicalThreadID: "thread-123",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "reviewer-agent",
			StructuralType:  blackboard.StructuralTypeStandard, // WRONG TYPE
			Type:            "ReviewResult",
			ClaimID:         claimID,
		},
		Payload: blackboard.ArtefactPayload{Content: "review content"},
	}
	violationHash, _ := blackboard.ComputeArtefactHash(violationArtefact)
	violationArtefact.ID = violationHash

	// Verify - should fail
	err = engine.verifyArtefact(ctx, violationArtefact)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "topology violation: agent granted review claim must produce Review artefact")

	// 4. Create a REVIEW artefact (Success)
	validArtefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{parentHash},
			LogicalThreadID: "thread-123",
			Version:         2,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "reviewer-agent",
			StructuralType:  blackboard.StructuralTypeReview, // CORRECT TYPE
			Type:            "ReviewResult",
			ClaimID:         claimID,
		},
		Payload: blackboard.ArtefactPayload{Content: "review content"},
	}
	validHash, _ := blackboard.ComputeArtefactHash(validArtefact)
	validArtefact.ID = validHash

	// Verify - should pass
	err = engine.verifyArtefact(ctx, validArtefact)
	assert.NoError(t, err)
}
