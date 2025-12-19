package blackboard

import (
	"context"
	"fmt"
	"sort"

	"github.com/hearth-insights/holt/internal/debug"
)

// ScanClaims retrieves all claim IDs for the current instance.
// M5.1: Enables agents to discover existing claims on startup (cold start recovery).
//
// This is used by synchronizers and controller-mode agents to process claims that were
// created before the agent started (e.g., after a restart). The scan happens AFTER
// subscription to ensure no claims are missed in the gap.
//
// Returns:
//   - Array of claim IDs (sorted alphabetically)
//   - error if scan fails
func (c *Client) ScanClaims(ctx context.Context) ([]string, error) {
	// Build scan pattern: holt:{instance}:claim:*
	// Note: We need to avoid matching :bids suffix
	pattern := fmt.Sprintf("holt:%s:claim:*", c.instanceName)
	debug.Log("ScanClaims using pattern: %s", pattern)

	var claimIDs []string
	iter := c.rdb.Scan(ctx, 0, pattern, 0).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()

		// Skip if it's a bids key or other sub-key
		// Our key structure is: holt:{instance}:claim:{id}
		// Sub-keys: holt:{instance}:claim:{id}:bids

		// Simple filter: if it ends with :bids, skip it
		if len(key) > 5 && key[len(key)-5:] == ":bids" {
			continue
		}

		// Extract ID
		prefix := fmt.Sprintf("holt:%s:claim:", c.instanceName)
		if len(key) > len(prefix) {
			id := key[len(prefix):]
			claimIDs = append(claimIDs, id)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan claim keys: %w", err)
	}

	sort.Strings(claimIDs)
	return claimIDs, nil
}
