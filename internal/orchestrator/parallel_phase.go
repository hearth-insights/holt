package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// GrantParallelPhase grants the claim to all agents that bid "claim" (parallel).
// Updates the claim's GrantedParallelAgents field and sets status to pending_parallel.
func (e *Engine) GrantParallelPhase(ctx context.Context, claim *blackboard.Claim, bids map[string]blackboard.Bid) error {
	// Collect all agents with parallel bids
	var parallelBidders []string
	for agentName, bid := range bids {
		if bid.BidType == blackboard.BidTypeParallel {
			parallelBidders = append(parallelBidders, agentName)
		}
	}

	if len(parallelBidders) == 0 {
		return fmt.Errorf("GrantParallelPhase called with no parallel bidders")
	}

	log.Printf("[Orchestrator] Granting parallel phase to %d agents: %v for claim %s",
		len(parallelBidders), parallelBidders, claim.ID)

	// Update claim with granted parallel agents
	claim.GrantedParallelAgents = parallelBidders
	claim.Status = blackboard.ClaimStatusPendingParallel

	if err := e.client.UpdateClaim(ctx, claim); err != nil {
		return fmt.Errorf("failed to update claim with parallel grants: %w", err)
	}

	e.logEvent("parallel_phase_granted", map[string]interface{}{
		"claim_id":        claim.ID,
		"parallel_agents": parallelBidders,
		"agent_count":     len(parallelBidders),
	})

	// Publish grant notifications to all parallel agents
	for _, agentName := range parallelBidders {
		// Check if agent is a controller
		agent, agentExists := e.config.Agents[agentName]
		if agentExists && agent.Mode == "controller" {
			// Controller-worker pattern
			if e.workerManager != nil {
				// Check worker limit
				if e.workerManager.IsAtWorkerLimit(agentName, agent.Worker.MaxConcurrent) {
					log.Printf("[Orchestrator] Role '%s' at max_concurrent worker limit (%d), pausing claim %s in grant queue",
						agentName, agent.Worker.MaxConcurrent, claim.ID)
					
					// Add to persistent grant queue
					if err := e.pauseGrantForQueue(ctx, claim, agentName, agentName); err != nil {
						log.Printf("[Orchestrator] Failed to pause claim in grant queue: %v", err)
					}
					continue
				}

				// Launch worker
				log.Printf("[Orchestrator] Launching worker for parallel controller %s (claim %s)", agentName, claim.ID)
				if err := e.workerManager.LaunchWorker(ctx, claim, agentName, agent, e.client); err != nil {
					log.Printf("[Orchestrator] Failed to launch worker for parallel controller %s: %v", agentName, err)
				}
				
				// M3.9: Get worker image ID (resolved dynamically) - pass empty string for now
				if err := e.publishClaimGrantedEvent(ctx, claim.ID, agentName, "claim", ""); err != nil {
					log.Printf("[Orchestrator] Failed to publish workflow event for parallel grant to %s: %v", agentName, err)
				}
			} else {
				log.Printf("[Orchestrator] WARN: Controller %s granted parallel claim but workerManager is nil", agentName)
			}
			continue
		}

		// Standard agent logic
		if err := e.publishGrantNotificationWithType(ctx, agentName, claim.ID, "claim"); err != nil {
			log.Printf("[Orchestrator] Failed to publish parallel grant notification to %s: %v", agentName, err)
		}
		// M3.9: Get agent image ID for audit trail
		agentImageID := e.getAgentImageID(ctx, agentName)
		// Publish event for watching
		if err := e.publishClaimGrantedEvent(ctx, claim.ID, agentName, "claim", agentImageID); err != nil {
			log.Printf("[Orchestrator] Failed to publish workflow event for parallel grant to %s: %v", agentName, err)
		}
	}

	// M3.5: Create new phase state for parallel phase and persist
	newPhaseState := NewPhaseState(claim.ID, "parallel", parallelBidders, bids)
	e.phaseStates[claim.ID] = newPhaseState

	// M3.5: Persist phase state to claim for restart resilience
	if err := e.persistPhaseState(ctx, claim, newPhaseState); err != nil {
		log.Printf("[Orchestrator] Warning: Failed to persist phase state for claim %s: %v", claim.ID, err)
		// Non-fatal - continue execution
	}

	return nil
}

// CheckParallelPhaseCompletion checks if all parallel agents have produced their artefacts,
// and if so, transitions to the next phase.
func (e *Engine) CheckParallelPhaseCompletion(ctx context.Context, claim *blackboard.Claim, phaseState *PhaseState) error {
	// Check if all granted parallel agents have produced artefacts
	if !phaseState.IsComplete() {
		log.Printf("[Orchestrator] Parallel phase incomplete for claim %s: %d/%d artefacts received",
			claim.ID, len(phaseState.ReceivedArtefacts), len(phaseState.GrantedAgents))
		return nil // Still waiting
	}

	// All parallel agents completed
	duration := time.Since(phaseState.StartTime)

	e.logEvent("parallel_phase_complete", map[string]interface{}{
		"claim_id":    claim.ID,
		"duration_ms": duration.Milliseconds(),
		"agents":      phaseState.GrantedAgents,
	})

	log.Printf("[Orchestrator] Parallel phase complete for claim %s (duration: %v), transitioning to next phase",
		claim.ID, duration.Round(time.Millisecond))

	return e.TransitionToNextPhase(ctx, claim, phaseState)
}
