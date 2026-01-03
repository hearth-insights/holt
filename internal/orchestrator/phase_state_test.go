package orchestrator

import (
	"testing"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
)

func TestNewPhaseState(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"agent1": {AgentName: "agent1", BidType: blackboard.BidTypeReview, TimestampMs: 100},
		"agent2": {AgentName: "agent2", BidType: blackboard.BidTypeExclusive, TimestampMs: 200},
	}

	ps := NewPhaseState("claim-123", "review", []string{"agent1"}, bids)

	assert.Equal(t, "claim-123", ps.ClaimID)
	assert.Equal(t, "review", ps.Phase)
	assert.Equal(t, []string{"agent1"}, ps.GrantedAgents)
	assert.Empty(t, ps.ReceivedArtefacts)
	assert.Equal(t, blackboard.BidTypeReview, ps.AllBids["agent1"])
	assert.Equal(t, blackboard.BidTypeExclusive, ps.AllBids["agent2"])
	assert.Equal(t, int64(100), ps.BidTimestamps["agent1"])
	assert.Equal(t, int64(200), ps.BidTimestamps["agent2"])
	assert.False(t, ps.StartTime.IsZero())
}

func TestPhaseState_IsComplete(t *testing.T) {
	ps := &PhaseState{
		GrantedAgents:     []string{"agent1", "agent2", "agent3"},
		ReceivedArtefacts: make(map[string]string),
	}

	// Not complete initially
	assert.False(t, ps.IsComplete())

	// Add one artefact
	ps.ReceivedArtefacts["agent1"] = "artefact-1"
	assert.False(t, ps.IsComplete())

	// Add second artefact
	ps.ReceivedArtefacts["agent2"] = "artefact-2"
	assert.False(t, ps.IsComplete())

	// Add third artefact - now complete
	ps.ReceivedArtefacts["agent3"] = "artefact-3"
	assert.True(t, ps.IsComplete())

	// Still complete with extra artefacts
	ps.ReceivedArtefacts["agent4"] = "artefact-4"
	assert.True(t, ps.IsComplete())
}

func TestHasBidsForPhase_Review(t *testing.T) {
	bids := map[string]blackboard.BidType{
		"agent1": blackboard.BidTypeReview,
		"agent2": blackboard.BidTypeExclusive,
	}

	assert.True(t, HasBidsForPhase(bids, "review"))
	assert.False(t, HasBidsForPhase(bids, "parallel"))
	assert.True(t, HasBidsForPhase(bids, "exclusive"))
}

func TestHasBidsForPhase_Parallel(t *testing.T) {
	bids := map[string]blackboard.BidType{
		"agent1": blackboard.BidTypeParallel,
		"agent2": blackboard.BidTypeParallel,
	}

	assert.False(t, HasBidsForPhase(bids, "review"))
	assert.True(t, HasBidsForPhase(bids, "parallel"))
	assert.False(t, HasBidsForPhase(bids, "exclusive"))
}

func TestHasBidsForPhase_Exclusive(t *testing.T) {
	bids := map[string]blackboard.BidType{
		"agent1": blackboard.BidTypeExclusive,
	}

	assert.False(t, HasBidsForPhase(bids, "review"))
	assert.False(t, HasBidsForPhase(bids, "parallel"))
	assert.True(t, HasBidsForPhase(bids, "exclusive"))
}

func TestHasBidsForPhase_NoBids(t *testing.T) {
	bids := map[string]blackboard.BidType{
		"agent1": blackboard.BidTypeIgnore,
		"agent2": blackboard.BidTypeIgnore,
	}

	assert.False(t, HasBidsForPhase(bids, "review"))
	assert.False(t, HasBidsForPhase(bids, "parallel"))
	assert.False(t, HasBidsForPhase(bids, "exclusive"))
}

func TestDetermineInitialPhase_ReviewFirst(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"reviewer": {AgentName: "reviewer", BidType: blackboard.BidTypeReview},
		"worker":   {AgentName: "worker", BidType: blackboard.BidTypeParallel},
		"coder":    {AgentName: "coder", BidType: blackboard.BidTypeExclusive},
	}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingReview, status)
	assert.Equal(t, "review", phase)
}

func TestDetermineInitialPhase_SkipToParallel(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"worker": {AgentName: "worker", BidType: blackboard.BidTypeParallel},
		"coder":  {AgentName: "coder", BidType: blackboard.BidTypeExclusive},
	}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingParallel, status)
	assert.Equal(t, "parallel", phase)
}

func TestDetermineInitialPhase_SkipToExclusive(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"coder": {AgentName: "coder", BidType: blackboard.BidTypeExclusive},
	}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingExclusive, status)
	assert.Equal(t, "exclusive", phase)
}

func TestDetermineInitialPhase_NoBids(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"agent1": {AgentName: "agent1", BidType: blackboard.BidTypeIgnore},
		"agent2": {AgentName: "agent2", BidType: blackboard.BidTypeIgnore},
	}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingReview, status)
	assert.Equal(t, "", phase) // Empty phase indicates dormant
}

func TestDetermineInitialPhase_EmptyBids(t *testing.T) {
	bids := map[string]blackboard.Bid{}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingReview, status)
	assert.Equal(t, "", phase) // Empty phase indicates dormant
}

// M5.1.1: Test merge phase detection
func TestHasBidsForPhase_Merge(t *testing.T) {
	bids := map[string]blackboard.BidType{
		"aggregator": blackboard.BidTypeMerge,
	}

	assert.False(t, HasBidsForPhase(bids, "review"))
	assert.False(t, HasBidsForPhase(bids, "parallel"))
	assert.False(t, HasBidsForPhase(bids, "exclusive"))
	assert.True(t, HasBidsForPhase(bids, "merge"))
}

// M5.1.1: Test merge-only workflows (skip directly to merge phase)
func TestDetermineInitialPhase_SkipToMerge(t *testing.T) {
	bids := map[string]blackboard.Bid{
		"aggregator": {AgentName: "aggregator", BidType: blackboard.BidTypeMerge},
	}

	status, phase := DetermineInitialPhase(bids)
	assert.Equal(t, blackboard.ClaimStatusPendingMerge, status)
	assert.Equal(t, "merge", phase)
}
