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
// M5.1: This is the main entry point for synchronizer bidding logic.
//
// Returns:
//   - true if all conditions met and lock acquired (ready to bid)
//   - false if not ready or lock already held (skip bid)
//   - error if traversal/check fails
//
// Algorithm:
//  1. Check if target artefact is a potential trigger
//  2. Find common ancestor of configured type
//  3. Verify all dependencies are met (fan-in check)
//  4. Acquire deduplication lock
//  5. Return true to bid
func (s *Synchronizer) shouldBidOnClaim(ctx context.Context, claim *blackboard.Claim) (bool, error) {
	log.Printf("[Synchronizer] Evaluating claim %s for synchronization", claim.ID)

	// Load target artefact
	targetArtefact, err := s.bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		return false, fmt.Errorf("failed to load target artefact: %w", err)
	}

	// Step 1: Check if target artefact is a potential trigger
	if !s.isPotentialTrigger(targetArtefact) {
		log.Printf("[Synchronizer] Artefact %s (type=%s) is not a potential trigger, ignoring",
			targetArtefact.ID, targetArtefact.Header.Type)
		return false, nil
	}

	// Step 2: Find common ancestor
	ancestor, err := s.findCommonAncestor(ctx, targetArtefact)
	if err != nil {
		log.Printf("[Synchronizer] Failed to find ancestor: %v", err)
		return false, nil // Not an error, just not ready
	}
	if ancestor == nil {
		log.Printf("[Synchronizer] No ancestor of type '%s' found for artefact %s",
			s.config.AncestorType, targetArtefact.ID)
		return false, nil
	}

	log.Printf("[Synchronizer] Found ancestor %s (type=%s)", ancestor.ID, ancestor.Header.Type)

	// Step 3: Verify all dependencies (fan-in check)
	allReady, err := s.checkAllDependenciesMet(ctx, ancestor)
	if err != nil {
		return false, fmt.Errorf("failed to check dependencies: %w", err)
	}
	if !allReady {
		log.Printf("[Synchronizer] Not all dependencies met for ancestor %s", ancestor.ID)
		return false, nil
	}

	log.Printf("[Synchronizer] All dependencies met for ancestor %s", ancestor.ID)

	// Step 4: Acquire deduplication lock
	lockAcquired, err := s.bbClient.AcquireSyncLock(ctx, ancestor.ID, s.agentRole)
	if err != nil {
		return false, fmt.Errorf("failed to acquire sync lock: %w", err)
	}
	if !lockAcquired {
		log.Printf("[Synchronizer] Lock already held for ancestor %s, skipping bid (deduplication)", ancestor.ID)
		return false, nil
	}

	log.Printf("[Synchronizer] Lock acquired for ancestor %s, ready to bid", ancestor.ID)
	return true, nil
}

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

	log.Printf("[Synchronizer] Found %d descendants of ancestor %s", len(descendants), ancestor.ID)

	// Group descendants by type
	descendantsByType := make(map[string][]*blackboard.Artefact)
	for _, desc := range descendants {
		descendantsByType[desc.Header.Type] = append(descendantsByType[desc.Header.Type], desc)
	}

	// Check each wait condition
	for _, condition := range s.config.WaitFor {
		artefactsOfType := descendantsByType[condition.Type]

		if condition.CountFromMetadata != "" {
			// Producer-Declared pattern: Read expected count from metadata
			if len(artefactsOfType) == 0 {
				log.Printf("[Synchronizer] No artefacts of type '%s' found yet", condition.Type)
				return false, nil
			}

			// Read batch_size from any artefact's metadata (all siblings have same value)
			expectedCount, err := s.getExpectedCountFromMetadata(artefactsOfType[0], condition.CountFromMetadata)
			if err != nil {
				log.Printf("[Synchronizer] Failed to read metadata '%s': %v", condition.CountFromMetadata, err)
				return false, nil // Not ready (metadata missing or invalid)
			}

			if len(artefactsOfType) < expectedCount {
				log.Printf("[Synchronizer] Type '%s': found %d of %d expected",
					condition.Type, len(artefactsOfType), expectedCount)
				return false, nil
			}

			log.Printf("[Synchronizer] Type '%s': all %d artefacts present", condition.Type, expectedCount)
		} else {
			// Named pattern: Exactly 1 required
			if len(artefactsOfType) == 0 {
				log.Printf("[Synchronizer] Type '%s' not found", condition.Type)
				return false, nil
			}

			log.Printf("[Synchronizer] Type '%s': present", condition.Type)
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
