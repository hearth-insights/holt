package blackboard

import (
	"context"
	"fmt"
	"log"
	"sort"
)

// ScanClaims retrieves all claim IDs that match the instance.
// M5.1: Enables agents to discover existing claims on startup (cold start recovery).
// Returns array of claim IDs.
func (c *Client) ScanClaims(ctx context.Context) ([]string, error) {
	// Build scan pattern: holt:{instance}:claim:*
	// Note: We need to avoid matching :bids suffix
	pattern := fmt.Sprintf("holt:%s:claim:*", c.instanceName)
	log.Printf("[Blackboard] DEBUG: ScanClaims using pattern: %s", pattern)

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
