package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/dyluth/holt/pkg/blackboard"
)

// convertToVerifiableArtefact converts a V1 Artefact to V2 VerifiableArtefact for verification.
// This is a transitional helper during M4.6 incremental implementation.
// In the final V2 system, artefacts will be created as VerifiableArtefact from the start.
func convertToVerifiableArtefact(a *blackboard.Artefact) *blackboard.VerifiableArtefact {
	return &blackboard.VerifiableArtefact{
		ID: a.ID, // Will be verified against computed hash
		Header: blackboard.ArtefactHeader{
			ParentHashes:    a.SourceArtefacts, // V1 called it SourceArtefacts, V2 calls it ParentHashes
			LogicalThreadID: a.LogicalID,
			Version:         a.Version,
			CreatedAtMs:     a.CreatedAtMs,
			ProducedByRole:  a.ProducedByRole,
			StructuralType:  a.StructuralType,
			Type:            a.Type,
			ContextForRoles: a.ContextForRoles,
		},
		Payload: blackboard.ArtefactPayload{
			Content: a.Payload,
		},
	}
}

// verifyArtefactV1 performs verification on a V1 Artefact by converting to V2.
// This is the transitional entry point during M4.6 implementation.
func (e *Engine) verifyArtefactV1(ctx context.Context, artefact *blackboard.Artefact) error {
	verifiable := convertToVerifiableArtefact(artefact)
	return e.verifyArtefact(ctx, verifiable)
}

// verifyArtefact performs comprehensive 3-stage validation on a VerifiableArtefact.
// This is the orchestrator's critical security check for M4.6 cryptographic verification.
//
// Validation stages (per design section 2.2):
// 1. Parent existence check (prevents orphan blocks in the Merkle DAG)
// 2. Timestamp validation (prevents time-based attacks)
// 3. Hash verification (prevents content tampering)
//
// Returns error if validation fails. On security violations (orphan block or hash mismatch),
// triggers global lockdown via the three-step alert mechanism.
func (e *Engine) verifyArtefact(ctx context.Context, artefact *blackboard.VerifiableArtefact) error {
	// STAGE 1: Parent existence check (prevent orphan blocks)
	// Exception: Root artefacts with empty ParentHashes are valid (e.g., GoalDefined)
	if len(artefact.Header.ParentHashes) > 0 {
		for _, parentHash := range artefact.Header.ParentHashes {
			exists, err := e.client.ArtefactExists(ctx, parentHash)
			if err != nil {
				return fmt.Errorf("failed to check parent existence for hash %s: %w", parentHash, err)
			}

			if !exists {
				// SECURITY EVENT: Orphan block detected
				alert := &blackboard.SecurityAlert{
					Type:               "orphan_block",
					TimestampMs:        time.Now().UnixMilli(),
					ArtefactID:         artefact.ID,
					MissingParentHash:  parentHash,
					AgentRole:          artefact.Header.ProducedByRole,
					OrchestratorAction: "global_lockdown",
				}

				// Trigger global lockdown (3-step mechanism)
				if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
					log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for orphan block: %v", lockdownErr)
				}

				return fmt.Errorf("orphan block detected: parent hash %s does not exist (artefact %s)", parentHash, artefact.ID)
			}
		}
	}

	// STAGE 2: Timestamp validation (prevent time-based attacks)
	now := time.Now().UnixMilli()

	// Get drift tolerance from config (default 5 minutes = 300000ms)
	driftToleranceMs := int64(300000) // Default
	if e.config != nil && e.config.Orchestrator.TimestampDriftToleranceMs != nil {
		driftToleranceMs = int64(*e.config.Orchestrator.TimestampDriftToleranceMs)
	}

	// Check if timestamp is too far in the future
	if artefact.Header.CreatedAtMs > now+driftToleranceMs {
		drift := artefact.Header.CreatedAtMs - now

		// Create timestamp drift alert (warning, not lockdown)
		alert := &blackboard.SecurityAlert{
			Type:                   "timestamp_drift",
			TimestampMs:            now,
			ArtefactID:             artefact.ID,
			ArtefactTimestampMs:    artefact.Header.CreatedAtMs,
			OrchestratorTimestampMs: now,
			DriftMs:                drift,
			ThresholdMs:            driftToleranceMs,
			AgentRole:              artefact.Header.ProducedByRole,
			OrchestratorAction:     "rejected",
		}

		// Log to security alerts (but don't trigger lockdown)
		if err := e.client.PublishSecurityAlert(ctx, alert); err != nil {
			log.Printf("[Orchestrator] Warning: Failed to publish timestamp drift alert: %v", err)
		}

		return fmt.Errorf("timestamp too far in future: drift=%dms, threshold=%dms (artefact %s)",
			drift, driftToleranceMs, artefact.ID)
	}

	// Check if timestamp is too far in the past
	if artefact.Header.CreatedAtMs < now-driftToleranceMs {
		drift := now - artefact.Header.CreatedAtMs

		// Create timestamp drift alert (warning, not lockdown)
		alert := &blackboard.SecurityAlert{
			Type:                   "timestamp_drift",
			TimestampMs:            now,
			ArtefactID:             artefact.ID,
			ArtefactTimestampMs:    artefact.Header.CreatedAtMs,
			OrchestratorTimestampMs: now,
			DriftMs:                drift,
			ThresholdMs:            driftToleranceMs,
			AgentRole:              artefact.Header.ProducedByRole,
			OrchestratorAction:     "rejected",
		}

		// Log to security alerts (but don't trigger lockdown)
		if err := e.client.PublishSecurityAlert(ctx, alert); err != nil {
			log.Printf("[Orchestrator] Warning: Failed to publish timestamp drift alert: %v", err)
		}

		return fmt.Errorf("timestamp too far in past: drift=%dms, threshold=%dms (artefact %s)",
			drift, driftToleranceMs, artefact.ID)
	}

	// STAGE 3: Hash verification (prevent content tampering)
	if err := blackboard.ValidateArtefactHash(artefact); err != nil {
		// Check if this is a hash mismatch error
		var mismatchErr *blackboard.HashMismatchError
		if errors.As(err, &mismatchErr) {
			// SECURITY EVENT: Hash mismatch detected
			alert := &blackboard.SecurityAlert{
				Type:               "hash_mismatch",
				TimestampMs:        time.Now().UnixMilli(),
				ArtefactIDClaimed:  artefact.ID,
				HashExpected:       mismatchErr.Expected,
				HashActual:         mismatchErr.Actual,
				AgentRole:          artefact.Header.ProducedByRole,
				OrchestratorAction: "global_lockdown",
			}

			// Trigger global lockdown (3-step mechanism)
			if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
				log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for hash mismatch: %v", lockdownErr)
			}

			return fmt.Errorf("hash mismatch detected: expected=%s, actual=%s (artefact %s)",
				mismatchErr.Expected, mismatchErr.Actual, artefact.ID)
		}

		// Other validation error (e.g., canonicalization failure)
		return fmt.Errorf("hash validation failed: %w", err)
	}

	// All validation stages passed
	e.logEvent("artefact_verified", map[string]interface{}{
		"artefact_id":  artefact.ID,
		"parent_count": len(artefact.Header.ParentHashes),
		"timestamp_ms": artefact.Header.CreatedAtMs,
		"producer":     artefact.Header.ProducedByRole,
	})

	return nil
}
