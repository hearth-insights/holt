package pup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// M5.1.1: Merge bidding logic for Fan-In Accumulator pattern
// Supports BOTH COUNT mode (Producer-Declared) and TYPES mode (Named pattern)

// shouldBidMerge evaluates whether to submit a merge bid for a claim.
// Returns a merge bid if conditions are met, nil otherwise.
//
// MODE DETECTION:
//   - COUNT mode: len(wait_for)==1 AND count_from_metadata present
//   - TYPES mode: len(wait_for)>1 AND no count_from_metadata
//
// COUNT Mode Logic:
//   1. Find ancestor of configured type
//   2. Get direct children of ancestor
//   3. Read batch_size from first child's metadata
//   4. Submit merge bid with batch_size
//
// TYPES Mode Logic:
//   1. Find ancestor of configured type
//   2. Build expected_types JSON array (sorted alphabetically)
//   3. Submit merge bid with expected_count = len(wait_for)
//
// Returns:
//   - (*Bid, nil) if merge bid should be submitted
//   - (nil, nil) if agent doesn't use synchronize or conditions not met
//   - (nil, error) if evaluation fails
func shouldBidMerge(ctx context.Context, bbClient *blackboard.Client, agentRole string, claim *blackboard.Claim, syncConfig *SynchronizeConfig) (*blackboard.Bid, error) {
	// Only bid merge if agent has synchronize config
	if syncConfig == nil {
		return nil, nil
	}

	// Get claim's artefact
	artefact, err := bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artefact: %w", err)
	}

	// MODE DETECTION
	hasCountFromMetadata := false
	var countFromMetadata string
	for _, condition := range syncConfig.WaitFor {
		if condition.CountFromMetadata != "" {
			hasCountFromMetadata = true
			countFromMetadata = condition.CountFromMetadata
			break
		}
	}

	// Determine mode
	var mode string
	if hasCountFromMetadata && len(syncConfig.WaitFor) == 1 {
		mode = "count"
	} else if !hasCountFromMetadata && len(syncConfig.WaitFor) > 1 {
		mode = "types"
	} else if len(syncConfig.WaitFor) == 1 && !hasCountFromMetadata {
		// Single wait_for without count = dependency wait pattern, not a merge
		// Use old synchronizer logic (bid exclusive when ready)
		log.Printf("[Pup/Merge] Single wait_for detected - dependency wait pattern, not merge")
		return nil, nil
	} else {
		// Invalid config (should have been caught by validation)
		return nil, fmt.Errorf("invalid synchronize config: cannot determine mode")
	}

	// Skip merge bidding if current artefact IS the ancestor
	// We only want to bid merge for DESCENDANTS of the ancestor
	if artefact.Header.Type == syncConfig.AncestorType {
		log.Printf("[Pup/Merge] Current artefact is the ancestor type %s - skipping merge bid", syncConfig.AncestorType)
		return nil, nil
	}

	// Find ancestor
	ancestor, err := findAncestor(ctx, bbClient, artefact, syncConfig.AncestorType, syncConfig.MaxDepth)
	if err != nil {
		log.Printf("[Pup/Merge] Cannot find ancestor type %s: %v", syncConfig.AncestorType, err)
		return nil, nil // Cannot bid if ancestor not found
	}
	if ancestor == nil {
		log.Printf("[Pup/Merge] No ancestor of type %s found", syncConfig.AncestorType)
		return nil, nil
	}

	// Build merge bid based on mode
	var expectedCount string
	var expectedTypesJSON string
	var targetType string

	if mode == "count" {
		// COUNT MODE: Read batch_size from first child's metadata
		children, err := getDirectChildren(ctx, bbClient, ancestor.ID)
		if err != nil || len(children) == 0 {
			return nil, fmt.Errorf("ancestor has no children or failed to fetch: %w", err)
		}

		// Read batch_size from first child's metadata
		firstChild := children[0]
		var metadata map[string]interface{}
		if err := json.Unmarshal([]byte(firstChild.Header.Metadata), &metadata); err != nil {
			return nil, fmt.Errorf("failed to parse child metadata: %w", err)
		}

		batchSizeRaw, ok := metadata[countFromMetadata]
		if !ok {
			return nil, fmt.Errorf("metadata field '%s' not found in child artefact", countFromMetadata)
		}

		expectedCount = fmt.Sprintf("%v", batchSizeRaw) // Convert to string
		targetType = artefact.Header.Type
		expectedTypesJSON = "" // Not used in COUNT mode

	} else {
		// TYPES MODE: Build expected_types JSON array
		types := make([]string, len(syncConfig.WaitFor))
		for i, condition := range syncConfig.WaitFor {
			types[i] = condition.Type
		}
		// CRITICAL: Alphabetically sort before storing (per design doc Section 11.10)
		sort.Strings(types)

		typesJSON, err := json.Marshal(types)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal expected_types: %w", err)
		}

		expectedCount = fmt.Sprintf("%d", len(types))
		expectedTypesJSON = string(typesJSON)
		targetType = artefact.Header.Type
	}

	// Construct merge bid
	bid := &blackboard.Bid{
		AgentName:   agentRole,
		BidType:     blackboard.BidTypeMerge,
		TimestampMs: time.Now().UnixMilli(),
		Metadata: map[string]string{
			"ancestor_id":         ancestor.ID,
			"mode":                mode,
			"expected_count":      expectedCount,
			"current_artefact_type": targetType,
			"expected_types_json": expectedTypesJSON,
		},
	}

	log.Printf("[Pup/Merge] Bidding MERGE (%s mode) for %s (ancestor: %.16s, expected: %s)",
		mode, targetType, ancestor.ID, expectedCount)

	return bid, nil
}

// findAncestor traverses the artefact graph upward to find an ancestor of the specified type.
// Uses BFS traversal through parent hashes.
//
// Parameters:
//   - ctx: Context
//   - bbClient: Blackboard client
//   - startArtefact: Artefact to start traversal from
//   - ancestorType: Type of ancestor to find
//   - maxDepth: Maximum depth to traverse (0 = unlimited)
//
// Returns:
//   - Ancestor artefact if found
//   - nil if not found (not an error - just not present in graph)
//   - error if traversal fails
func findAncestor(ctx context.Context, bbClient *blackboard.Client, startArtefact *blackboard.Artefact, ancestorType string, maxDepth int) (*blackboard.Artefact, error) {
	visited := make(map[string]bool)
	queue := []string{startArtefact.ID}
	depth := 0

	for len(queue) > 0 {
		// Check depth limit
		if maxDepth > 0 && depth > maxDepth {
			return nil, nil // Depth limit reached
		}

		// Process current level
		levelSize := len(queue)
		for i := 0; i < levelSize; i++ {
			currentID := queue[0]
			queue = queue[1:]

			if visited[currentID] {
				continue
			}
			visited[currentID] = true

			current, err := bbClient.GetArtefact(ctx, currentID)
			if err != nil {
				return nil, fmt.Errorf("failed to get artefact %s: %w", currentID, err)
			}

			// Check if this is the ancestor we're looking for
			if current.Header.Type == ancestorType {
				return current, nil
			}

			// Add parents to queue (traverse upward)
			queue = append(queue, current.Header.ParentHashes...)
		}

		depth++
	}

	return nil, nil // Not found (not an error)
}

// getDirectChildren retrieves the direct children of an artefact.
// Uses the reverse index maintained by the blackboard.
//
// Parameters:
//   - ctx: Context
//   - bbClient: Blackboard client
//   - parentID: Artefact ID to get children for
//
// Returns:
//   - Slice of child artefacts
//   - error if fetch fails
func getDirectChildren(ctx context.Context, bbClient *blackboard.Client, parentID string) ([]*blackboard.Artefact, error) {
	// Use GetDescendants with maxDepth=1 to get only direct children
	descendants, err := bbClient.GetDescendants(ctx, parentID, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to get descendants: %w", err)
	}

	// Filter to only direct children (those with parentID in their ParentHashes)
	var children []*blackboard.Artefact
	for _, desc := range descendants {
		for _, parent := range desc.Header.ParentHashes {
			if parent == parentID {
				children = append(children, desc)
				break
			}
		}
	}

	return children, nil
}
