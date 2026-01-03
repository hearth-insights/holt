package orchestrator

import (
	"context"
	"fmt"
	"log"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// M5.1.1: Merge Phase - 4th phase in claim lifecycle for Fan-In Accumulator pattern
// Supports BOTH COUNT mode (Producer-Declared) and TYPES mode (Named pattern)

// GrantMergePhase processes merge bids for Fan-In accumulation.
// This is the 4th phase in the claim lifecycle, executed AFTER exclusive phase.
//
// Algorithm:
//  1. For each merge bid on the claim
//  2. Extract metadata (mode, ancestor_id, expected_count, etc.)
//  3. Call Lua script to atomically add claim to accumulator
//  4. Handle result:
//     - COMPLETE (1): Batch/set complete → Grant Fan-In Claim to agent
//     - ACCUMULATING (0): Still waiting → No action
//     - DUPLICATE_TYPE (-1): TYPES mode error → Terminate claim
//  5. Mark current claim as complete (it's been added to accumulator)
//
// Parameters:
//   - ctx: Context
//   - claim: The claim being processed
//   - bids: All bids on this claim (we filter for merge bids)
//
// Returns error if processing fails.
func (e *Engine) GrantMergePhase(ctx context.Context, claim *blackboard.Claim, bids map[string]blackboard.Bid) error {
	// M5.1.1: Merge bids have metadata, but PhaseState doesn't store it
	// Re-fetch full bids from Redis to get metadata
	fullBids, err := e.client.GetAllBids(ctx, claim.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch full bids: %w", err)
	}

	// Collect all merge bids (with metadata)
	var mergeBids []blackboard.Bid
	for _, bid := range fullBids {
		if bid.BidType == blackboard.BidTypeMerge {
			mergeBids = append(mergeBids, bid)
		}
	}

	if len(mergeBids) == 0 {
		return fmt.Errorf("GrantMergePhase called with no merge bidders")
	}

	// Process each merge bid (usually only one agent bids merge for a given claim)
	for _, bid := range mergeBids {
		// Extract metadata from merge bid
		ancestorID := bid.Metadata["ancestor_id"]
		mode := bid.Metadata["mode"]
		expectedCount := bid.Metadata["expected_count"]
		currentArtefactType := bid.Metadata["current_artefact_type"]
		expectedTypesJSON := bid.Metadata["expected_types_json"]
		role := bid.AgentName

		// Validate required metadata
		if ancestorID == "" || mode == "" || expectedCount == "" || role == "" {
			log.Printf("[Orchestrator/Merge] ERROR: Merge bid missing required metadata (ancestor_id, mode, expected_count, or role)")
			continue
		}

		log.Printf("[Orchestrator/Merge] Processing merge bid from %s (mode=%s, ancestor=%.16s, expected=%s)",
			role, mode, ancestorID, expectedCount)

		// Atomic operation via Lua script:
		// 1. Add claim ID to accumulator (mode-specific: :count SET or :types HASH)
		// 2. Check if batch/set complete
		// 3. Return status (1=complete, 0=accumulating, -1=duplicate type error)
		complete, duplicate, err := e.client.ExecuteAccumulatorScript(
			ctx,
			ancestorID,
			role,
			claim.ID,
			mode,
			expectedCount,
			currentArtefactType,
			expectedTypesJSON,
		)

		if err != nil {
			log.Printf("[Orchestrator/Merge] ERROR: Accumulator script failed: %v", err)
			continue
		}

		// Handle duplicate type error (TYPES mode only)
		if duplicate {
			log.Printf("[Orchestrator/Merge] ERROR: Duplicate type '%s' detected for ancestor %s (TYPES mode)",
				currentArtefactType, ancestorID)

			// Terminate the claim (duplicate type is a fatal error)
			claim.Status = blackboard.ClaimStatusTerminated
			if err := e.client.UpdateClaim(ctx, claim); err != nil {
				log.Printf("[Orchestrator/Merge] Failed to terminate claim: %v", err)
			}

			e.logEvent("merge_duplicate_type_error", map[string]interface{}{
				"claim_id":     claim.ID,
				"ancestor_id":  ancestorID,
				"role":         role,
				"type":         currentArtefactType,
				"mode":         mode,
			})

			return fmt.Errorf("duplicate type %s in TYPES mode accumulator", currentArtefactType)
		}

		// Handle batch/set completion
		if complete {
			log.Printf("[Orchestrator/Merge] ✓ %s mode complete for ancestor %.16s: Granting Fan-In Claim to %s",
				mode, ancestorID, role)

			// Get all accumulated claims
			accumulatedClaimIDs, err := e.client.GetAccumulatedClaims(ctx, ancestorID, role)
			if err != nil {
				log.Printf("[Orchestrator/Merge] Failed to get accumulated claims: %v", err)
				continue
			}

			log.Printf("[Orchestrator/Merge] Marking %d accumulated claims as complete", len(accumulatedClaimIDs))

			// Mark all accumulated claims as complete
			for _, accClaimID := range accumulatedClaimIDs {
				accClaim, err := e.client.GetClaim(ctx, accClaimID)
				if err != nil {
					log.Printf("[Orchestrator/Merge] Failed to get claim %s: %v", accClaimID, err)
					continue
				}

				accClaim.Status = blackboard.ClaimStatusComplete
				if err := e.client.UpdateClaim(ctx, accClaim); err != nil {
					log.Printf("[Orchestrator/Merge] Failed to mark claim %s complete: %v", accClaimID, err)
				}
			}

			// Update accumulator status to "granted"
			if err := e.client.UpdateAccumulatorStatus(ctx, ancestorID, role, "granted"); err != nil {
				log.Printf("[Orchestrator/Merge] Failed to update accumulator status: %v", err)
			}

			// Get Fan-In Claim ID (deterministic)
			fanInClaimID, err := e.client.GetAccumulatorFanInClaimID(ctx, ancestorID, role)
			if err != nil || fanInClaimID == "" {
				log.Printf("[Orchestrator/Merge] Failed to get Fan-In Claim ID: %v", err)
				continue
			}

			// Create a pseudo-claim for the Fan-In work
			// The Fan-In claim ID is deterministic, but we need a Claim object for worker launch
			fanInClaim := &blackboard.Claim{
				ID:         fanInClaimID,
				ArtefactID: ancestorID, // Fan-In claim targets the ancestor artefact
				Status:     blackboard.ClaimStatusPendingMerge,
			}

			// M3.4: Check if agent is a controller and launch worker if needed
			agent, agentExists := e.config.Agents[role]
			if agentExists && agent.Mode == "controller" {
				// Controller-worker pattern - launch worker instead of publishing notification
				if e.workerManager != nil {
					// Check worker limit
					if e.workerManager.IsAtWorkerLimit(role, agent.Worker.MaxConcurrent) {
						log.Printf("[Orchestrator/Merge] Role '%s' at max_concurrent worker limit (%d), pausing merge grant",
							role, agent.Worker.MaxConcurrent)
						// TODO: Add to grant queue for merge claims (future enhancement)
						// For now, log warning and skip
						continue
					}

					// Launch worker for Fan-In execution
					log.Printf("[Orchestrator/Merge] Launching worker for merge controller %s (Fan-In claim %s)", role, fanInClaimID)
					if err := e.workerManager.LaunchWorker(ctx, fanInClaim, role, agent, e.client); err != nil {
						log.Printf("[Orchestrator/Merge] Failed to launch worker for merge controller %s: %v", role, err)
					} else {
						// Publish workflow event for audit trail
						if err := e.publishClaimGrantedEvent(ctx, fanInClaimID, role, "merge", ""); err != nil {
							log.Printf("[Orchestrator/Merge] Failed to publish workflow event for merge grant to %s: %v", role, err)
						}
					}
				} else {
					log.Printf("[Orchestrator/Merge] WARN: Controller %s granted merge but workerManager is nil", role)
				}
			} else {
				// Traditional agent - publish grant notification
				if err := e.publishGrantNotificationWithType(ctx, role, fanInClaimID, "merge"); err != nil {
					log.Printf("[Orchestrator/Merge] Failed to publish grant notification: %v", err)
				}
			}

			// Log event for audit trail
			e.logEvent("merge_phase_granted", map[string]interface{}{
				"ancestor_id":         ancestorID,
				"role":                role,
				"mode":                mode,
				"expected_count":      expectedCount,
				"accumulated_claims":  len(accumulatedClaimIDs),
				"fan_in_claim_id":     fanInClaimID,
			})

		} else {
			// Still accumulating, no action needed
			log.Printf("[Orchestrator/Merge] Accumulating for %s (mode=%s, ancestor=%.16s)",
				role, mode, ancestorID)

			e.logEvent("merge_phase_accumulating", map[string]interface{}{
				"ancestor_id":    ancestorID,
				"role":           role,
				"mode":           mode,
				"expected_count": expectedCount,
				"claim_id":       claim.ID,
			})
		}
	}

	// Mark the current claim as complete (it's been added to accumulator)
	// This happens whether the batch is complete or still accumulating
	claim.Status = blackboard.ClaimStatusComplete
	if err := e.client.UpdateClaim(ctx, claim); err != nil {
		return fmt.Errorf("failed to mark claim complete: %w", err)
	}

	return nil
}
