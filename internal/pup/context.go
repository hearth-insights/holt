package pup

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/dyluth/holt/pkg/blackboard"
)

const (
	// maxContextDepth is the hard limit for BFS traversal depth to prevent
	// infinite loops in malformed graphs and excessive context size.
	maxContextDepth = 10
)

// assembleContext performs breadth-first traversal of the artefact dependency graph
// to build a rich historical context for agent execution.
//
// Algorithm (from agent-pup.md):
//  1. Start with target artefact's source_artefacts
//  2. M3.3: Also add claim.AdditionalContextIDs for feedback claims
//  3. For each level (max 10):
//     - Fetch each artefact from blackboard
//     - Use thread tracking to get latest version of that logical artefact
//     - Store latest version in context map (de-duplicates by logical_id)
//     - Add source_artefacts to next level queue
//  4. Filter context to Standard and Answer artefacts only
//  5. Sort chronologically (oldest → newest)
//  6. Return filtered, sorted context chain
//
// Returns empty array for root artefacts (no source_artefacts).
func (e *Engine) assembleContext(ctx context.Context, targetArtefact *blackboard.Artefact, claim *blackboard.Claim) ([]*blackboard.Artefact, error) {
	log.Printf("[INFO] Assembling context for artefact: artefact_id=%s type=%s",
		targetArtefact.ID, targetArtefact.Type)

	// Initialize BFS queue with target's source artefacts
	queue := make([]string, len(targetArtefact.SourceArtefacts))
	copy(queue, targetArtefact.SourceArtefacts)

	// M3.3: Add additional context IDs for feedback claims
	if len(claim.AdditionalContextIDs) > 0 {
		queue = append(queue, claim.AdditionalContextIDs...)
		log.Printf("[INFO] Feedback claim detected, adding %d Review artefacts to context",
			len(claim.AdditionalContextIDs))
	}

	// Context map keyed by logical_id for de-duplication
	// Also serves as cache for GetLatestVersion results
	contextMap := make(map[string]*blackboard.Artefact)

	// Track depth to enforce limit
	depth := 0

	// Track seen logical_ids to avoid duplicates
	seenLogicalIDs := make(map[string]bool)

	// BFS traversal
	for len(queue) > 0 && depth < maxContextDepth {
		depth++
		currentLevelSize := len(queue)

		log.Printf("[DEBUG] BFS level %d: processing %d artefacts", depth, currentLevelSize)

		// Process all artefacts at current level
		for i := 0; i < currentLevelSize; i++ {
			artefactID := queue[0]
			queue = queue[1:] // Dequeue (pop from front)

			// Fetch artefact from blackboard
			artefact, err := e.bbClient.GetArtefact(ctx, artefactID)
			if err != nil {
				log.Printf("[WARN] Failed to fetch artefact %s: %v (skipping)", artefactID, err)
				continue // Skip this artefact, continue traversal
			}

			if artefact == nil {
				log.Printf("[WARN] Artefact %s not found (skipping)", artefactID)
				continue
			}

			// Get latest version of this logical artefact via thread tracking
			latestArtefact, err := e.getLatestVersionForContext(ctx, artefact)
			if err != nil {
				log.Printf("[WARN] Failed to get latest version for logical_id=%s: %v (using discovered version)",
					artefact.LogicalID, err)
				latestArtefact = artefact // Fallback to discovered version
			}

			// De-duplicate by logical_id (keep first occurrence in BFS order)
			if seenLogicalIDs[latestArtefact.LogicalID] {
				log.Printf("[DEBUG] De-duplication: logical_id=%s already in context, skipping",
					latestArtefact.LogicalID)
				continue
			}

			seenLogicalIDs[latestArtefact.LogicalID] = true
			contextMap[latestArtefact.LogicalID] = latestArtefact
			log.Printf("[DEBUG] Added to context: logical_id=%s version=%d type=%s",
				latestArtefact.LogicalID, latestArtefact.Version, latestArtefact.Type)

			// Add source artefacts to queue for next level
			queue = append(queue, latestArtefact.SourceArtefacts...)
		}
	}

	if len(queue) > 0 {
		log.Printf("[WARN] Depth limit reached: max_depth=%d artefacts_pending=%d",
			maxContextDepth, len(queue))
	}

	// Filter to Standard and Answer artefacts only
	filtered := filterContextArtefacts(contextMap)
	log.Printf("[DEBUG] Context filtering: total=%d filtered_to=%d",
		len(contextMap), len(filtered))

	// Sort chronologically (oldest → newest)
	sortedContext := sortContextChronologically(filtered)

	log.Printf("[DEBUG] Context assembly complete: total=%d depth=%d",
		len(sortedContext), depth)

	return sortedContext, nil
}

// getLatestVersionForContext retrieves the latest version of a logical artefact
// using thread tracking. Returns the discovered artefact if thread tracking fails
// or returns an older version.
func (e *Engine) getLatestVersionForContext(ctx context.Context, discoveredArtefact *blackboard.Artefact) (*blackboard.Artefact, error) {
	// Query thread tracking for latest version
	latestID, latestVersion, err := e.bbClient.GetLatestVersion(ctx, discoveredArtefact.LogicalID)
	if err != nil {
		return nil, fmt.Errorf("GetLatestVersion failed: %w", err)
	}

	// Thread tracking returned empty (no thread exists)
	if latestID == "" {
		log.Printf("[DEBUG] No thread tracking for logical_id=%s, using discovered version %d",
			discoveredArtefact.LogicalID, discoveredArtefact.Version)
		return discoveredArtefact, nil
	}

	// Thread tracking returned same or older version - use discovered
	if latestVersion <= discoveredArtefact.Version {
		log.Printf("[DEBUG] Thread has version %d, discovered version %d, using discovered",
			latestVersion, discoveredArtefact.Version)
		return discoveredArtefact, nil
	}

	// Thread tracking found a newer version - fetch it
	log.Printf("[DEBUG] Found latest version: logical_id=%s version=%d (discovered was %d)",
		discoveredArtefact.LogicalID, latestVersion, discoveredArtefact.Version)

	latestArtefact, err := e.bbClient.GetArtefact(ctx, latestID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest version: %w", err)
	}

	if latestArtefact == nil {
		return nil, fmt.Errorf("latest version artefact not found: %s", latestID)
	}

	return latestArtefact, nil
}

// filterContextArtefacts filters the context map to include only Standard, Answer, and Review artefacts.
// M3.3: Review artefacts are included for feedback claims to provide review feedback to agents.
// M4.3: Knowledge artefacts are filtered out (they are not part of the work chain, loaded separately).
// This provides agents with a clean, actionable history without failures or terminal artefacts.
func filterContextArtefacts(contextMap map[string]*blackboard.Artefact) []*blackboard.Artefact {
	filtered := make([]*blackboard.Artefact, 0, len(contextMap))

	for _, artefact := range contextMap {
		if artefact.StructuralType == blackboard.StructuralTypeStandard ||
			artefact.StructuralType == blackboard.StructuralTypeAnswer ||
			artefact.StructuralType == blackboard.StructuralTypeReview {
			filtered = append(filtered, artefact)
		} else {
			log.Printf("[DEBUG] Filtered out artefact: logical_id=%s type=%s structural_type=%s",
				artefact.LogicalID, artefact.Type, artefact.StructuralType)
		}
	}

	return filtered
}

// sortContextChronologically sorts artefacts to provide chronological ordering.
// Since Artefact structs don't have timestamps in Phase 2, we use BFS traversal order
// as a proxy for chronological order. The graph structure (source_artefacts relationships)
// implicitly encodes chronological dependencies: if A → B, then A was created before B.
//
// For Phase 2 with single-agent linear chains, BFS order is equivalent to chronological order.
// Future phases may add explicit timestamps if needed for complex multi-agent scenarios.
func sortContextChronologically(artefacts []*blackboard.Artefact) []*blackboard.Artefact {
	// In Phase 2, artefacts are already in BFS traversal order, which is chronologically
	// correct for linear chains. We reverse the order so oldest artefacts come first.
	sorted := make([]*blackboard.Artefact, len(artefacts))

	// BFS discovers newest artefacts first (closest to target), so reverse the array
	// to get oldest-first ordering
	for i := 0; i < len(artefacts); i++ {
		sorted[i] = artefacts[len(artefacts)-1-i]
	}

	return sorted
}

// loadKnowledgeForAgent loads and filters Knowledge artefacts for the agent's role (M4.3).
// This function:
//  1. Collects all unique logical_ids from the work history
//  2. Queries thread_context SETs for each logical_id to find attached Knowledge artefact IDs
//  3. Loads all unique Knowledge artefacts
//  4. Filters by matching agent's role against context_for_roles glob patterns
//  5. Implements "latest version wins" merge strategy for duplicate knowledge_names
//
// Returns:
//   - contextIsDeclared: true if any Knowledge was found for this agent
//   - knowledgeBase: map of knowledge_name → payload
//   - loadedKnowledge: list of knowledge names that were loaded
func (e *Engine) loadKnowledgeForAgent(ctx context.Context, contextChain []*blackboard.Artefact) (bool, map[string]string, []string, error) {
	// Collect all unique logical_ids from work history
	logicalIDSet := make(map[string]bool)
	for _, art := range contextChain {
		logicalIDSet[art.LogicalID] = true
	}

	// M4.3: Always include "global" logical_id for manually provisioned knowledge
	logicalIDSet["global"] = true

	logicalIDs := make([]string, 0, len(logicalIDSet))
	for logicalID := range logicalIDSet {
		logicalIDs = append(logicalIDs, logicalID)
	}

	log.Printf("[INFO] M4.3: Searching for Knowledge artefacts across %d logical threads", len(logicalIDs))

	// Query thread_context SETs for all logical_ids and collect Knowledge artefact IDs
	knowledgeIDs := make(map[string]bool) // Use map for de-duplication
	for _, logicalID := range logicalIDs {
		// Guard against nil bbClient (unit tests)
		if e.bbClient == nil {
			return false, nil, nil, nil
		}

		threadContextKey := blackboard.ThreadContextKey(e.bbClient.GetInstanceName(), logicalID)
		members, err := e.bbClient.GetRedisClient().SMembers(ctx, threadContextKey).Result()
		if err != nil {
			log.Printf("[WARN] Failed to query thread_context for logical_id=%s: %v", logicalID, err)
			continue
		}

		for _, memberID := range members {
			knowledgeIDs[memberID] = true
		}
	}

	if len(knowledgeIDs) == 0 {
		log.Printf("[DEBUG] M4.3: No Knowledge artefacts found in thread_context")
		return false, nil, nil, nil
	}

	log.Printf("[DEBUG] M4.3: Found %d Knowledge artefact IDs", len(knowledgeIDs))

	// Load all Knowledge artefacts
	allKnowledge := make([]*blackboard.Artefact, 0, len(knowledgeIDs))
	for knowledgeID := range knowledgeIDs {
		knowledge, err := e.bbClient.GetArtefact(ctx, knowledgeID)
		if err != nil {
			log.Printf("[WARN] Failed to load Knowledge artefact %s: %v (skipping)", knowledgeID, err)
			continue
		}
		if knowledge == nil {
			log.Printf("[WARN] Knowledge artefact %s not found (skipping)", knowledgeID)
			continue
		}
		allKnowledge = append(allKnowledge, knowledge)
	}

	log.Printf("[DEBUG] M4.3: Loaded %d Knowledge artefacts", len(allKnowledge))

	// Filter by role matching and implement "latest version wins" merge strategy
	filtered, err := e.filterAndMergeKnowledge(allKnowledge)
	if err != nil {
		return false, nil, nil, fmt.Errorf("failed to filter and merge knowledge: %w", err)
	}

	if len(filtered) == 0 {
		log.Printf("[DEBUG] M4.3: No Knowledge artefacts matched agent role %s", e.config.AgentName)
		return false, nil, nil, nil
	}

	// Build knowledge_base map and loaded_knowledge list
	knowledgeBase := make(map[string]string, len(filtered))
	loadedKnowledge := make([]string, 0, len(filtered))

	for _, knowledge := range filtered {
		knowledgeName := knowledge.Type // The knowledge_name is stored in the Type field
		knowledgeBase[knowledgeName] = knowledge.Payload
		loadedKnowledge = append(loadedKnowledge, knowledgeName)
		log.Printf("[INFO] M4.3: Loaded knowledge '%s' (version=%d) for role %s",
			knowledgeName, knowledge.Version, e.config.AgentName)
	}

	return true, knowledgeBase, loadedKnowledge, nil
}

// filterAndMergeKnowledge filters Knowledge artefacts by role matching and implements "latest version wins" (M4.3).
// This function:
//  1. Filters artefacts by matching agent's role against context_for_roles glob patterns
//  2. Groups artefacts by knowledge_name (Type field)
//  3. For each group, selects the artefact with the highest version number
func (e *Engine) filterAndMergeKnowledge(allKnowledge []*blackboard.Artefact) ([]*blackboard.Artefact, error) {
	// First, filter by role matching
	roleMatched := make([]*blackboard.Artefact, 0)

	for _, knowledge := range allKnowledge {
		if e.matchesRole(knowledge.ContextForRoles) {
			roleMatched = append(roleMatched, knowledge)
			log.Printf("[DEBUG] M4.3: Knowledge '%s' (v%d) matched role %s",
				knowledge.Type, knowledge.Version, e.config.AgentName)
		} else {
			log.Printf("[DEBUG] M4.3: Knowledge '%s' (v%d) did not match role %s (target_roles=%v)",
				knowledge.Type, knowledge.Version, e.config.AgentName, knowledge.ContextForRoles)
		}
	}

	if len(roleMatched) == 0 {
		return nil, nil
	}

	// Group by knowledge_name (Type field) and select latest version
	knowledgeMap := make(map[string]*blackboard.Artefact)

	for _, knowledge := range roleMatched {
		knowledgeName := knowledge.Type

		existing, exists := knowledgeMap[knowledgeName]
		if !exists || knowledge.Version > existing.Version {
			// Either first time seeing this knowledge, or this is a newer version
			knowledgeMap[knowledgeName] = knowledge
			if exists {
				log.Printf("[DEBUG] M4.3: Latest version wins: '%s' v%d replaced v%d",
					knowledgeName, knowledge.Version, existing.Version)
			}
		}
	}

	// Convert map to slice
	result := make([]*blackboard.Artefact, 0, len(knowledgeMap))
	for _, knowledge := range knowledgeMap {
		result = append(result, knowledge)
	}

	return result, nil
}

// matchesRole checks if the agent's role matches any of the glob patterns in context_for_roles.
// Returns true if the agent's role matches at least one pattern, or if patterns is empty/contains "*".
func (e *Engine) matchesRole(contextForRoles []string) bool {
	if len(contextForRoles) == 0 {
		return true // Empty means all roles
	}

	for _, roleGlob := range contextForRoles {
		if roleGlob == "*" {
			return true // Wildcard matches all
		}

		// Use path/filepath.Match for glob pattern matching
		matched, err := filepath.Match(roleGlob, e.config.AgentName)
		if err != nil {
			log.Printf("[WARN] Invalid glob pattern '%s': %v", roleGlob, err)
			continue
		}

		if matched {
			return true
		}
	}

	return false
}
