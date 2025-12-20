package pup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// Synchronizer implements declarative fan-in coordination (M5.1).
// It evaluates claims to determine when all prerequisite artefacts are ready
// for synchronization, enabling declarative scatter-gather patterns.
type Synchronizer struct {
	config    *SynchronizeConfig
	bbClient  *blackboard.Client
	agentRole string
}

// NewSynchronizer creates a new Synchronizer with the given configuration.
// M5.1: Used by controller-mode agents with synchronize block in holt.yml.
func NewSynchronizer(cfg *SynchronizeConfig, bbClient *blackboard.Client, agentRole string) *Synchronizer {
	return &Synchronizer{
		config:    cfg,
		bbClient:  bbClient,
		agentRole: agentRole,
	}
}

// shouldBidOnClaim checks if synchronization conditions are met for a claim.
// M5.2: Updated to only CHECK lock status during bidding, not acquire it.
// Lock acquisition happens during work execution to prevent stuck locks.
//
// Returns:
//   - DecisionBid if all conditions met and lock not held (ready to bid)
//   - DecisionIgnore if not ready or lock already held (skip bid)
//   - error if traversal/check fails
//
// Algorithm:
//  1. Check if target artefact is a potential trigger
//  2. Find common ancestor of configured type
//  3. Verify all dependencies are met (fan-in check)
//  4. Check (but don't acquire) deduplication lock
//  5. Return DecisionBid to allow bidding
//
// Parameters:
//   - ctx: Context
//   - claim: The claim to evaluate
func (s *Synchronizer) shouldBidOnClaim(ctx context.Context, claim *blackboard.Claim) (Decision, error) {
	log.Printf("[Synchronizer] Evaluating claim %s", claim.ID)

	// Load target artefact
	log.Printf("[Synchronizer] Loading target artefact %s...", claim.ArtefactID)
	targetArtefact, err := s.bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		log.Printf("[Synchronizer] Failed to load target artefact: %v", err)
		return DecisionIgnore, fmt.Errorf("failed to load target artefact: %w", err)
	}
	log.Printf("[Synchronizer] Loaded target artefact %s (Type: %s)", claim.ArtefactID, targetArtefact.Header.Type)

	// M5.1: Synchronizers only bid on trigger artefacts (wait_for types), not ancestors
	// The ancestor claim should be handled by other agents (if any)
	// Step 1: Check if target artefact is a potential trigger
	if !s.isPotentialTrigger(targetArtefact) {
		// Not a trigger type -> Ignore (includes ancestor type)
		if targetArtefact.Header.Type == s.config.AncestorType {
			log.Printf("[Synchronizer] Ignoring ancestor claim for %s (waiting for trigger types: %v)",
				targetArtefact.ID, s.getTriggerTypes())
		} else {
			log.Printf("[Synchronizer] Artefact %s (type=%s) is not a potential trigger, ignoring",
				targetArtefact.ID, targetArtefact.Header.Type)
		}
		return DecisionIgnore, nil
	}

	// This is a trigger type -> find its ancestor
	// Step 2: Find common ancestor
	ancestor, err := s.findCommonAncestor(ctx, targetArtefact)
	if err != nil {
		log.Printf("[Synchronizer] Failed to find ancestor: %v", err)
		return DecisionIgnore, nil // Error finding ancestor -> Ignore (transient error)
	}
	if ancestor == nil {
		log.Printf("[Synchronizer] No ancestor of type '%s' found for artefact %s",
			s.config.AncestorType, targetArtefact.ID)
		return DecisionIgnore, nil // Trigger valid but no ancestor -> Ignore
	}

	log.Printf("[Synchronizer] Found ancestor %s (type=%s)", ancestor.ID, ancestor.Header.Type)

	// Step 3: Verify all dependencies (fan-in check)
	allReady, err := s.checkAllDependenciesMet(ctx, ancestor)
	if err != nil {
		return DecisionIgnore, fmt.Errorf("failed to check dependencies: %w", err)
	}
	if !allReady {
		log.Printf("[Synchronizer] Not all dependencies met for ancestor %s", ancestor.ID)
		return DecisionIgnore, nil // Not ready -> Ignore (will re-evaluate on next artefact event)
	}

	log.Printf("[Synchronizer] All dependencies met for ancestor %s", ancestor.ID)

	// M5.2 Fix: Check lock status but DON'T acquire during bidding
	// Lock will be acquired during work execution to prevent race conditions
	// Step 4: Check deduplication lock (peek only, no acquisition)
	log.Printf("[Synchronizer] Checking deduplication lock for ancestor %s (role=%s)",
		ancestor.ID[:16], s.agentRole)

	// Always use non-destructive check during bidding
	isLocked, err := s.bbClient.CheckSyncLock(ctx, ancestor.ID, s.agentRole)
	if err != nil {
		log.Printf("[Synchronizer] ❌ Error checking lock for ancestor %s: %v", ancestor.ID[:16], err)
		return DecisionIgnore, fmt.Errorf("failed to check sync lock: %w", err)
	}

	if isLocked {
		log.Printf("[Synchronizer] ⚠️  Lock already held for ancestor %s, skipping bid (work in progress)",
			ancestor.ID[:16])
		return DecisionIgnore, nil
	}

	log.Printf("[Synchronizer] ✓ Lock available for ancestor %s, proceeding with bid", ancestor.ID[:16])

	return DecisionBid, nil // Ready -> Bid (lock will be acquired during executeWork)
}

// EvaluateArtefact REMOVED - M5.1 simplified to claim-only bidding.
// Synchronizers now evaluate all claims via shouldBidOnClaim() when claim events arrive.
// This eliminates race conditions and maintains clean orchestrator→agent boundaries.
// The Orchestrator is responsible for creating claims when artefacts appear.

// isPotentialTrigger checks if target artefact type is in wait_for list.
// M5.1: Early filter to avoid traversal for irrelevant artefacts.
func (s *Synchronizer) isPotentialTrigger(artefact *blackboard.Artefact) bool {
	for _, condition := range s.config.WaitFor {
		if artefact.Header.Type == condition.Type {
			return true
		}
	}
	return false
}

// getTriggerTypes returns a list of trigger types from wait_for conditions.
// Used for logging purposes.
func (s *Synchronizer) getTriggerTypes() []string {
	types := make([]string, len(s.config.WaitFor))
	for i, condition := range s.config.WaitFor {
		types[i] = condition.Type
	}
	return types
}

// findCommonAncestor traverses upward to find first ancestor matching ancestor_type.
// M5.1: Uses BFS upward traversal via source_artefacts.
//
// Returns:
//   - Ancestor artefact if found
//   - nil if not found (not an error, workflow may not be ready yet)
//   - error if traversal fails
func (s *Synchronizer) findCommonAncestor(ctx context.Context, artefact *blackboard.Artefact) (*blackboard.Artefact, error) {
	visited := make(map[string]bool)
	queue := []string{artefact.ID}

	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]

		if visited[currentID] {
			continue
		}
		visited[currentID] = true

		current, err := s.bbClient.GetArtefact(ctx, currentID)
		if err != nil {
			return nil, err
		}

		// Check if this is the ancestor we're looking for
		if current.Header.Type == s.config.AncestorType {
			return current, nil
		}
		
		log.Printf("[Synchronizer] Visited node %s (Type: %s), looking for %s", current.ID, current.Header.Type, s.config.AncestorType)

		// Add parents to queue (traverse upward)
		queue = append(queue, current.Header.ParentHashes...)
	}

	return nil, nil // Not found
}

// checkAllDependenciesMet verifies all wait_for conditions are satisfied.
// M5.1: Supports both Named and Producer-Declared patterns.
//
// Returns:
//   - true if all conditions satisfied
//   - false if any condition not met (workflow not ready)
//   - error if descendant traversal fails
func (s *Synchronizer) checkAllDependenciesMet(ctx context.Context, ancestor *blackboard.Artefact) (bool, error) {
	// Get all descendants of ancestor
	descendants, err := s.bbClient.GetDescendants(ctx, ancestor.ID, s.config.MaxDepth)
	if err != nil {
		return false, fmt.Errorf("failed to get descendants: %w", err)
	}

	log.Printf("[Synchronizer] checkAllDependenciesMet: Ancestor %s has %d descendants", ancestor.ID, len(descendants))

	// Group descendants by type
	descendantsByType := make(map[string][]*blackboard.Artefact)
	for _, desc := range descendants {
		log.Printf("[Synchronizer] - Descendant: %s (type=%s)", desc.ID, desc.Header.Type)
		descendantsByType[desc.Header.Type] = append(descendantsByType[desc.Header.Type], desc)
	}

	// Check each wait condition
	for _, condition := range s.config.WaitFor {
		artefactsOfType := descendantsByType[condition.Type]
		log.Printf("[Synchronizer] Checking condition: Type=%s, CountFromMetadata=%s. Found %d candidates.",
			condition.Type, condition.CountFromMetadata, len(artefactsOfType))

		if condition.CountFromMetadata != "" {
			// Producer-Declared pattern: Read expected count from metadata
			// M5.2 Fix: The batch_size is defined at the fan-out point (direct children of ancestor),
			// but we might be waiting for leaf nodes (batch_size=1).
			// We must find the metadata on the DIRECT CHILDREN of the ancestor.

			log.Printf("[Synchronizer] Looking for metadata '%s' in direct children of ancestor %s",
				condition.CountFromMetadata, ancestor.ID[:16])

			expectedCount := 0
			foundMetadata := false
			var sourceArtefactID string
			var sourceArtefactType string

			// First pass: Identify and log all direct children
			var directChildren []*blackboard.Artefact
			for _, desc := range descendants {
				isDirectChild := false
				for _, parentID := range desc.Header.ParentHashes {
					if parentID == ancestor.ID {
						isDirectChild = true
						log.Printf("[Synchronizer]   Direct child found: %s (type=%s, parent=%s)",
							desc.ID[:16], desc.Header.Type, ancestor.ID[:16])
						break
					}
				}

				if isDirectChild {
					directChildren = append(directChildren, desc)
				}
			}

			log.Printf("[Synchronizer] Found %d direct children of ancestor %s", len(directChildren), ancestor.ID[:16])

			// Second pass: Search direct children for metadata
			for _, child := range directChildren {
				count, err := s.getExpectedCountFromMetadata(child, condition.CountFromMetadata)
				if err == nil {
					expectedCount = count
					foundMetadata = true
					sourceArtefactID = child.ID
					sourceArtefactType = child.Header.Type
					log.Printf("[Synchronizer]   ✓ Found metadata '%s'=%d on direct child %s (type=%s)",
						condition.CountFromMetadata, expectedCount, sourceArtefactID[:16], sourceArtefactType)
					break // Found it on a direct child (all siblings share same batch_size)
				} else {
					log.Printf("[Synchronizer]   ✗ Direct child %s (type=%s) has no metadata '%s': %v",
						child.ID[:16], child.Header.Type, condition.CountFromMetadata, err)
				}
			}

			// Fallback: If no direct child has it, check the target artefacts themselves
			if !foundMetadata && len(artefactsOfType) > 0 {
				log.Printf("[Synchronizer] No metadata found on direct children, checking target artefacts of type '%s'", condition.Type)
				count, err := s.getExpectedCountFromMetadata(artefactsOfType[0], condition.CountFromMetadata)
				if err == nil {
					expectedCount = count
					foundMetadata = true
					sourceArtefactID = artefactsOfType[0].ID
					sourceArtefactType = artefactsOfType[0].Header.Type
					log.Printf("[Synchronizer]   ✓ Found metadata '%s'=%d on target artefact %s (type=%s) [FALLBACK]",
						condition.CountFromMetadata, expectedCount, sourceArtefactID[:16], sourceArtefactType)
				} else {
					log.Printf("[Synchronizer]   ✗ Target artefact %s (type=%s) has no metadata '%s': %v",
						artefactsOfType[0].ID[:16], artefactsOfType[0].Header.Type, condition.CountFromMetadata, err)
				}
			}

			if !foundMetadata {
				log.Printf("[Synchronizer] ❌ Failed to read metadata '%s' from any direct child (%d checked) or target artefacts",
					condition.CountFromMetadata, len(directChildren))
				return false, nil // Not ready
			}

			log.Printf("[Synchronizer] Expected count determined: %d (source: %s, type=%s)",
				expectedCount, sourceArtefactID[:16], sourceArtefactType)

			if len(artefactsOfType) < expectedCount {
				log.Printf("[Synchronizer] Status: Type '%s': found %d of %d expected (WAITING)",
					condition.Type, len(artefactsOfType), expectedCount)
				return false, nil
			}

			log.Printf("[Synchronizer] Status: Type '%s': all %d artefacts present (READY)", condition.Type, expectedCount)
		} else {
			// Named pattern: Exactly 1 required
			if len(artefactsOfType) == 0 {
				log.Printf("[Synchronizer] Status: Type '%s' NOT FOUND", condition.Type)
				return false, nil
			}

			log.Printf("[Synchronizer] Status: Type '%s': present (READY)", condition.Type)
		}
	}

	return true, nil
}

// getExpectedCountFromMetadata parses metadata to extract expected count.
// M5.1: Used by Producer-Declared pattern to read batch_size.
//
// Returns:
//   - Expected count (positive integer)
//   - error if metadata missing, invalid JSON, or not a positive integer
func (s *Synchronizer) getExpectedCountFromMetadata(artefact *blackboard.Artefact, metadataKey string) (int, error) {
	var metadata map[string]string
	if err := json.Unmarshal([]byte(artefact.Header.Metadata), &metadata); err != nil {
		return 0, fmt.Errorf("invalid metadata JSON: %w", err)
	}

	countStr, exists := metadata[metadataKey]
	if !exists {
		return 0, fmt.Errorf("metadata key '%s' not found", metadataKey)
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return 0, fmt.Errorf("metadata value '%s' is not a valid integer: %w", countStr, err)
	}

	if count <= 0 {
		return 0, fmt.Errorf("metadata value '%s' must be positive", countStr)
	}

	return count, nil
}

// AcquireWorkLock attempts to acquire the synchronization lock for work execution.
// M5.2: Called at the start of executeWork() to prevent duplicate processing.
//
// Returns:
//   - ancestorID: The ID of the ancestor that was locked (for later release)
//   - locked: true if lock was acquired, false if already held
//   - error: if lock acquisition failed
func (s *Synchronizer) AcquireWorkLock(ctx context.Context, targetArtefact *blackboard.Artefact) (string, bool, error) {
	// Find the ancestor for this target artefact
	ancestor, err := s.findCommonAncestor(ctx, targetArtefact)
	if err != nil {
		return "", false, fmt.Errorf("failed to find ancestor for lock: %w", err)
	}
	if ancestor == nil {
		// No ancestor found - this shouldn't happen if bidding logic worked correctly
		log.Printf("[Synchronizer] Warning: No ancestor found during work lock acquisition for artefact %s", targetArtefact.ID)
		return "", true, nil // Allow work to proceed without lock
	}

	log.Printf("[Synchronizer] Acquiring work lock for ancestor %s", ancestor.ID[:16])

	// Acquire the lock
	acquired, err := s.bbClient.AcquireSyncLock(ctx, ancestor.ID, s.agentRole)
	if err != nil {
		return "", false, fmt.Errorf("failed to acquire work lock: %w", err)
	}

	if !acquired {
		log.Printf("[Synchronizer] ❌ Work lock already held for ancestor %s - another worker is processing", ancestor.ID[:16])
		return ancestor.ID, false, nil
	}

	log.Printf("[Synchronizer] ✓ Work lock acquired for ancestor %s", ancestor.ID[:16])
	return ancestor.ID, true, nil
}

// ReleaseWorkLock releases the synchronization lock after work completion.
// M5.2: Called at the end of executeWork() to allow other claims to proceed.
func (s *Synchronizer) ReleaseWorkLock(ctx context.Context, ancestorID string) error {
	if ancestorID == "" {
		return nil // No lock to release
	}

	log.Printf("[Synchronizer] Releasing work lock for ancestor %s", ancestorID[:16])

	// Release the lock by deleting the Redis key
	err := s.bbClient.ReleaseSyncLock(ctx, ancestorID, s.agentRole)
	if err != nil {
		log.Printf("[Synchronizer] ⚠️  Failed to release work lock for ancestor %s: %v", ancestorID[:16], err)
		return fmt.Errorf("failed to release work lock: %w", err)
	}

	log.Printf("[Synchronizer] ✓ Work lock released for ancestor %s", ancestorID[:16])
	return nil
}
