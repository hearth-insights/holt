package hoard

import (
	"context"
	"fmt"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
)

// RelationshipInfo holds information about an artefact's upstream and downstream claims.
type RelationshipInfo struct {
	ProducedBy *ClaimSummary `json:"produced_by,omitempty"` // Upstream claim (authorized creation)
	ConsumedBy *ClaimSummary `json:"consumed_by,omitempty"` // Downstream claim (processing this artefact)
}

// ClaimSummary provides a brief overview of a claim.
type ClaimSummary struct {
	ClaimID        string                 `json:"claim_id"`
	Status         blackboard.ClaimStatus `json:"status"`
	Phase          string                 `json:"phase,omitempty"`           // Current phase
	Reviewers      []string               `json:"reviewers,omitempty"`       // Agents in review phase
	ParallelAgents []string               `json:"parallel_agents,omitempty"` // Agents in parallel phase
	ExclusiveAgent string                 `json:"exclusive_agent,omitempty"` // Agent in exclusive phase
}

// ResolveRelationships finds the upstream and downstream claims for an artefact.
func ResolveRelationships(ctx context.Context, bbClient *blackboard.Client, artefact *blackboard.Artefact) (*RelationshipInfo, error) {
	info := &RelationshipInfo{}

	// 1. Resolve Upstream Claim (Produced By)
	if artefact.Header.ClaimID != "" {
		claim, err := bbClient.GetClaim(ctx, artefact.Header.ClaimID)
		if err != nil && !isRedisNotFound(err) {
			return nil, fmt.Errorf("failed to get upstream claim %s: %w", artefact.Header.ClaimID, err)
		}
		if claim != nil {
			info.ProducedBy = summarizeClaim(claim)
		}
	}

	// 2. Resolve Downstream Claim (Consumed By)
	// Check if there is a claim for this artefact ID
	claim, err := bbClient.GetClaimByArtefactID(ctx, artefact.ID)
	if err != nil && !isRedisNotFound(err) {
		return nil, fmt.Errorf("failed to get downstream claim for artefact %s: %w", artefact.ID, err)
	}
	if claim != nil {
		info.ConsumedBy = summarizeClaim(claim)
	}

	return info, nil
}

// summarizeClaim converts a full Claim to a ClaimSummary.
func summarizeClaim(claim *blackboard.Claim) *ClaimSummary {
	summary := &ClaimSummary{
		ClaimID:        claim.ID,
		Status:         claim.Status,
		Reviewers:      claim.GrantedReviewAgents,
		ParallelAgents: claim.GrantedParallelAgents,
		ExclusiveAgent: claim.GrantedExclusiveAgent,
	}

	// Infer current phase
	if claim.PhaseState != nil {
		summary.Phase = claim.PhaseState.Current
	}

	return summary
}

// isRedisNotFound checks if an error is a Redis nil error
func isRedisNotFound(err error) bool {
	return err == redis.Nil
}
