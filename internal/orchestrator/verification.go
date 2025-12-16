package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// verifyArtefact performs comprehensive 4-stage validation on an Artefact.
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
func (e *Engine) verifyArtefact(ctx context.Context, artefact *blackboard.Artefact) error {
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
			Type:                    "timestamp_drift",
			TimestampMs:             now,
			ArtefactID:              artefact.ID,
			ArtefactTimestampMs:     artefact.Header.CreatedAtMs,
			OrchestratorTimestampMs: now,
			DriftMs:                 drift,
			ThresholdMs:             driftToleranceMs,
			AgentRole:               artefact.Header.ProducedByRole,
			OrchestratorAction:      "rejected",
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
			Type:                    "timestamp_drift",
			TimestampMs:             now,
			ArtefactID:              artefact.ID,
			ArtefactTimestampMs:     artefact.Header.CreatedAtMs,
			OrchestratorTimestampMs: now,
			DriftMs:                 drift,
			ThresholdMs:             driftToleranceMs,
			AgentRole:               artefact.Header.ProducedByRole,
			OrchestratorAction:      "rejected",
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
	var isReviewClaim bool

	// Rule 1: Root/User artefacts (CLI/user-generated) validation
	if artefact.Header.ProducedByRole == "user" || artefact.Header.ProducedByRole == "cli" {
		// User/CLI artefacts must have empty claim (they operate outside grant system)
		if artefact.Header.ClaimID != "" {
			// SECURITY EVENT: Root artefact with invalid topology
			alert := &blackboard.SecurityAlert{
				Type:               "unauthorized_topology",
				TimestampMs:        time.Now().UnixMilli(),
				ArtefactID:         artefact.ID,
				AgentRole:          artefact.Header.ProducedByRole,
				ViolationType:      "root_artefact_with_claim",
				OrchestratorAction: "global_lockdown",
			}

			if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
				log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
			}

			return fmt.Errorf("topology violation: user/cli artefact must have empty ClaimID (artefact %s)", artefact.ID)
		}

		// M4.7: Root Artefact Manifest Anchoring
		// TRUE root artefacts (Version=1, starting new threads) MUST have exactly one parent: the SystemManifest
		// Continuations (Version>1) may have additional semantic parents (e.g., answer to question)
		// Backward compatibility: Pre-M4.7 artefacts may have empty ParentHashes

		if len(artefact.Header.ParentHashes) == 0 {
			// Pre-M4.7 artefact (no manifest anchor) - accept with warning
			log.Printf("[Orchestrator] Warning: Pre-M4.7 root artefact detected (no manifest anchor): %s", artefact.ID[:16]+"...")
			goto hashVerification
		}

		// M4.7+: Apply manifest anchor validation ONLY to true roots (Version=1)
		if artefact.Header.Version == 1 {
			// This is a workflow-initiating artefact - must have exactly ONE parent (the manifest)
			if len(artefact.Header.ParentHashes) != 1 {
				// Invalid: root artefact must have exactly ONE parent (the manifest)
				alert := &blackboard.SecurityAlert{
					Type:               "unauthorized_topology",
					TimestampMs:        time.Now().UnixMilli(),
					ArtefactID:         artefact.ID,
					AgentRole:          artefact.Header.ProducedByRole,
					ViolationType:      "root_missing_manifest_anchor",
					OrchestratorAction: "global_lockdown",
				}

				if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
					log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
				}

				return fmt.Errorf("topology violation: root artefact (Version=1) must have exactly one parent (active manifest), got %d (artefact %s)",
					len(artefact.Header.ParentHashes), artefact.ID)
			}

			// Verify the single parent is a valid SystemManifest
			parentHash := artefact.Header.ParentHashes[0]
			parentManifest, err := e.client.GetArtefact(ctx, parentHash)
			if err != nil {
				// Parent doesn't exist - this is caught by orphan block check (Stage 1)
				// But we provide a more specific error message here
				alert := &blackboard.SecurityAlert{
					Type:               "unauthorized_topology",
					TimestampMs:        time.Now().UnixMilli(),
					ArtefactID:         artefact.ID,
					MissingParentHash:  parentHash,
					AgentRole:          artefact.Header.ProducedByRole,
					ViolationType:      "root_invalid_manifest_reference",
					OrchestratorAction: "global_lockdown",
				}

				if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
					log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
				}

				return fmt.Errorf("topology violation: root artefact references non-existent manifest: %s (artefact %s)", parentHash, artefact.ID)
			}

			// Verify parent is actually a SystemManifest
			if parentManifest.Header.StructuralType != blackboard.StructuralTypeSystemManifest {
				alert := &blackboard.SecurityAlert{
					Type:               "unauthorized_topology",
					TimestampMs:        time.Now().UnixMilli(),
					ArtefactID:         artefact.ID,
					AgentRole:          artefact.Header.ProducedByRole,
					ViolationType:      "root_parent_not_manifest",
					OrchestratorAction: "global_lockdown",
				}

				if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
					log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
				}

				return fmt.Errorf("topology violation: root artefact parent must be SystemManifest, got %s (artefact %s)",
					parentManifest.Header.StructuralType, artefact.ID)
			}

			// SOFT CHECK: Warn if anchored to old manifest (continuity rule - M4.7 design section 3.1)
			// This can happen during config drift race conditions
			if e.activeManifestID != "" && parentHash != e.activeManifestID {
				log.Printf("[Orchestrator] Warning: Root artefact anchored to old manifest (config drift race condition)")
				log.Printf("[Orchestrator]   Artefact: %s", artefact.ID[:16]+"...")
				log.Printf("[Orchestrator]   Expected manifest: %s", e.activeManifestID[:16]+"...")
				log.Printf("[Orchestrator]   Actual manifest: %s", parentHash[:16]+"...")
				// Accept the artefact - valid historical manifest
			}

			// Valid root artefact with manifest anchor
			goto hashVerification
		} else {
			// Version>1: Continuation artefact (e.g., answer to question)
			// Must have at least one parent (verified in Stage 1)
			// No strict manifest anchoring required for continuations
			log.Printf("[Orchestrator] Info: User continuation artefact (Version=%d) with %d parents: %s",
				artefact.Header.Version, len(artefact.Header.ParentHashes), artefact.ID[:16]+"...")

			// Valid user continuation artefact
			goto hashVerification
		}
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
	// EXCEPTION: Terminal artefacts can reference any claim status (they signal completion)
	if artefact.Header.StructuralType != blackboard.StructuralTypeTerminal {
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
	}

	// Rule 4.5: Review Claim Enforcement (M3.3)
	// If the claim was granted as a REVIEW claim, the resulting artefact MUST be StructuralTypeReview.
	// This prevents misconfigured agents from breaking the workflow state machine.
	isReviewClaim = false
	if claim.GrantedReviewAgents != nil {
		for _, agent := range claim.GrantedReviewAgents {
			if agent == artefact.Header.ProducedByRole { // Note: agent name == role in simplified model
				isReviewClaim = true
				break
			}
		}
	}

	if isReviewClaim && artefact.Header.StructuralType != blackboard.StructuralTypeReview {
		// SECURITY EVENT: Review claim violation
		alert := &blackboard.SecurityAlert{
			Type:               "unauthorized_topology",
			TimestampMs:        time.Now().UnixMilli(),
			ArtefactID:         artefact.ID,
			AgentRole:          artefact.Header.ProducedByRole,
			ClaimIDProvided:    artefact.Header.ClaimID,
			ViolationType:      "review_claim_type_mismatch",
			OrchestratorAction: "global_lockdown",
		}

		if lockdownErr := e.client.TriggerGlobalLockdown(ctx, alert); lockdownErr != nil {
			log.Printf("[Orchestrator] CRITICAL: Failed to trigger lockdown for topology violation: %v", lockdownErr)
		}

		return fmt.Errorf("topology violation: agent granted review claim must produce Review artefact, got %s (artefact %s)",
			artefact.Header.StructuralType, artefact.ID)
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
