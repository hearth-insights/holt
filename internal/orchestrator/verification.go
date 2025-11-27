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
			ClaimID:         a.ClaimID, // M4.6 Security Addendum
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

// verifyArtefact performs comprehensive 4-stage validation on a VerifiableArtefact.
// This is the orchestrator's critical security check for M4.6 cryptographic verification.
//
// Validation stages (per design section 2.2 + M4.6 Security Addendum):
// 1. Parent existence check (prevents orphan blocks in the Merkle DAG)
// 2. Timestamp validation (prevents time-based attacks)
// 3. Topology validation (M4.6 Addendum - prevents unauthorized work via Grant Linkage)
// 4. Hash verification (prevents content tampering)
//
// Returns error if validation fails. On security violations (orphan block, topology violation,
// or hash mismatch), triggers global lockdown via the three-step alert mechanism.
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

	// STAGE 3: Topology validation (M4.6 Security Addendum - prevent unauthorized work)
	// This stage validates the Grant Linkage: every artefact must be cryptographically
	// bound to a valid authorization (Claim) that permitted its creation.

	// Declare variables before goto statements (Go requirement)
	var claim *blackboard.Claim
	var err error

	// Rule 1: Root artefacts (CLI/user-generated) validation
	if artefact.Header.ProducedByRole == "user" || artefact.Header.ProducedByRole == "cli" {
		// Root artefacts must have empty parents AND empty claim
		if len(artefact.Header.ParentHashes) == 0 && artefact.Header.ClaimID == "" {
			// Valid root artefact - skip further topology checks
			goto hashVerification
		}

		// SECURITY EVENT: Root artefact with invalid topology
		alert := &blackboard.SecurityAlert{
			Type:                "unauthorized_topology",
			TimestampMs:         time.Now().UnixMilli(),
			ArtefactID:          artefact.ID,
			AgentRole:           artefact.Header.ProducedByRole,
			ViolationType:       "root_artefact_with_claim",
			OrchestratorAction:  "global_lockdown",
		}

		if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
			log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
		}

		return fmt.Errorf("topology violation: root artefact must have empty ParentHashes and ClaimID (artefact %s)", artefact.ID)
	}

	// Rule 2: Orchestrator artefacts (Failure/Terminal) validation
	if artefact.Header.ProducedByRole == "orchestrator" {
		// Orchestrator artefacts MAY have ClaimID (if triggered by specific claim)
		// or empty ClaimID (if global event). No parent linkage enforcement.
		goto hashVerification
	}

	// Rule 3: Agent-produced artefacts MUST have topology binding
	if artefact.Header.ClaimID == "" {
		// SECURITY EVENT: Missing required ClaimID
		alert := &blackboard.SecurityAlert{
			Type:               "unauthorized_topology",
			TimestampMs:        time.Now().UnixMilli(),
			ArtefactID:         artefact.ID,
			AgentRole:          artefact.Header.ProducedByRole,
			ViolationType:      "missing_claim_id",
			OrchestratorAction: "global_lockdown",
		}

		if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
			log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
		}

		return fmt.Errorf("topology violation: agent artefact missing required ClaimID (artefact %s)", artefact.ID)
	}

	// Rule 4: Validate ClaimID references an active/granted claim for this agent
	claim, err = e.client.GetClaim(ctx, artefact.Header.ClaimID)
	if err != nil || claim == nil {
		// SECURITY EVENT: Invalid claim reference
		alert := &blackboard.SecurityAlert{
			Type:               "unauthorized_topology",
			TimestampMs:        time.Now().UnixMilli(),
			ArtefactID:         artefact.ID,
			AgentRole:          artefact.Header.ProducedByRole,
			ClaimIDProvided:    artefact.Header.ClaimID,
			ViolationType:      "invalid_claim_reference",
			OrchestratorAction: "global_lockdown",
		}

		if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
			log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
		}

		return fmt.Errorf("topology violation: ClaimID does not exist: %s (artefact %s)", artefact.Header.ClaimID, artefact.ID)
	}

	// Verify claim status (must be Active or Granted - specific status values depend on claim lifecycle)
	// In Holt's current implementation, we check for terminal states
	if claim.Status == blackboard.ClaimStatusComplete || claim.Status == blackboard.ClaimStatusTerminated {
		// SECURITY EVENT: Claim is not active
		alert := &blackboard.SecurityAlert{
			Type:               "unauthorized_topology",
			TimestampMs:        time.Now().UnixMilli(),
			ArtefactID:         artefact.ID,
			AgentRole:          artefact.Header.ProducedByRole,
			ClaimIDProvided:    artefact.Header.ClaimID,
			ClaimStatus:        string(claim.Status),
			ViolationType:      "invalid_claim_reference",
			OrchestratorAction: "global_lockdown",
		}

		if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
			log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
		}

		return fmt.Errorf("topology violation: ClaimID references non-active claim: %s (status: %s, artefact %s)",
			artefact.Header.ClaimID, claim.Status, artefact.ID)
	}

	// Rule 5: Parent linkage check - at least one parent must match claim's target
	// This ensures the agent is building upon the work it was assigned
	if len(artefact.Header.ParentHashes) > 0 {
		hasValidParent := false
		for _, parentHash := range artefact.Header.ParentHashes {
			if parentHash == claim.ArtefactID {
				hasValidParent = true
				break
			}
		}

		if !hasValidParent {
			// SECURITY EVENT: Parent linkage violation
			alert := &blackboard.SecurityAlert{
				Type:                   "unauthorized_topology",
				TimestampMs:            time.Now().UnixMilli(),
				ArtefactID:             artefact.ID,
				AgentRole:              artefact.Header.ProducedByRole,
				ClaimIDProvided:        artefact.Header.ClaimID,
				ExpectedParentArtefact: claim.ArtefactID,
				ActualParentHashes:     artefact.Header.ParentHashes,
				ViolationType:          "parent_linkage_violation",
				OrchestratorAction:     "global_lockdown",
			}

			if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
				log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
			}

			return fmt.Errorf("topology violation: no parent matches claim target %s (artefact %s)",
				claim.ArtefactID, artefact.ID)
		}
	}

hashVerification:
	// STAGE 4: Hash verification (prevent content tampering)
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
