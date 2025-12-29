package pup

import (
	"context"
	"log"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// RunControllerMode runs a controller that only bids, never executes.
// M3.4: Controllers eliminate race conditions by being the single bidder per role.
// M5.1: Supports synchronizer mode for declarative fan-in coordination.
// When a controller wins a grant, the orchestrator launches ephemeral workers to execute.
func RunControllerMode(ctx context.Context, config *Config, bbClient *blackboard.Client) error {
	// M3.7: AgentName IS the role
	log.Printf("[Controller] Controller %s ready - bidder-only mode", config.AgentName)

	// M5.1: Initialize synchronizer if configured
	var synchronizer *Synchronizer
	if config.SynchronizeConfig != nil {
		synchronizer = NewSynchronizer(config.SynchronizeConfig, bbClient, config.AgentName)
		log.Printf("[Controller] Running in synchronizer mode (ancestor_type=%s)", config.SynchronizeConfig.AncestorType)
	}

	// Subscribe to claim events
	subscription, err := bbClient.SubscribeClaimEvents(ctx)
	if err != nil {
		return err
	}
	defer subscription.Close()

	log.Printf("[Controller] Subscribed to claim events")

	// M5.1: Claim-only bidding (artefact subscriptions removed for simplification)
	// The orchestrator creates claims for all artefacts

	// M5.1: Scan for existing claims (Cold Start Recovery)
	// MUST be done AFTER subscription to ensure no events are missed in the gap.
	// Duplicates (scanning a claim we also get an event for) are fine as processClaim is idempotent.
	existingClaims, err := bbClient.ScanClaims(ctx)
	if err != nil {
		log.Printf("[Controller] Failed to scan existing claims: %v", err)
	} else {
		log.Printf("[Controller] Found %d existing claims, processing...", len(existingClaims))
		for _, claimID := range existingClaims {
			claim, err := bbClient.GetClaim(ctx, claimID)
			if err != nil {
				log.Printf("[Controller] Failed to load existing claim %s: %v", claimID, err)
				continue
			}
			// Process claim (fire-and-forget to avoid blocking loop entry)
			go processClaim(ctx, config, bbClient, synchronizer, claim)
		}
	}

	// Bidding loop (never executes work)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[Controller] Shutting down...")
			return nil

		case claim, ok := <-subscription.Events():
			if !ok {
				log.Printf("[Controller] Claim events channel closed")
				return nil
			}
			processClaim(ctx, config, bbClient, synchronizer, claim)

		// M5.1: Artefact event handling removed - using claim-only bidding
		// The orchestrator creates claims for all artefacts, eliminating race conditions

		case err, ok := <-subscription.Errors():
			if !ok {
				log.Printf("[Controller] Error channel closed")
				return nil
			}
			log.Printf("[Controller] Subscription error: %v", err)
		}
	}
}

// processClaim handles a single claim for bidding
func processClaim(ctx context.Context, config *Config, bbClient *blackboard.Client, synchronizer *Synchronizer, claim *blackboard.Claim) {
	// M5.1.1: Synchronizer mode now ONLY uses merge bidding, not exclusive bidding
	// The old synchronizer exclusive bidding is deprecated
	var shouldBid bool
	var bid blackboard.BidType

	if synchronizer != nil {
		// M5.1.1: Check if this is a merge pattern or dependency wait pattern
		// Merge patterns (COUNT/TYPES) use merge bidding
		// Dependency wait patterns (single wait_for) use old synchronizer logic

		// Try merge bid first to detect pattern type
		testMergeBid, _ := shouldBidMerge(ctx, bbClient, config.AgentName, claim, config.SynchronizeConfig)

		if testMergeBid != nil {
			// Merge pattern detected - bid IGNORE, merge bids handled separately
			shouldBid = true
			bid = blackboard.BidTypeIgnore
			log.Printf("[Controller] Merge pattern detected: bidding IGNORE (merge bids handled separately)")
		} else {
			// Dependency wait pattern - use old synchronizer logic
			decision, err := synchronizer.shouldBidOnClaim(ctx, claim)
			if err != nil {
				log.Printf("[Controller] Synchronizer error: %v", err)
				return // Skip bid on error
			}

			switch decision {
			case DecisionBid:
				shouldBid = true
				bid = blackboard.BidTypeExclusive
			case DecisionIgnore:
				shouldBid = true
				bid = blackboard.BidTypeIgnore
				log.Printf("[Controller] Synchronizer decided IGNORE for claim %s", claim.ID)
			default:
				log.Printf("[Controller] ERROR: Unexpected synchronizer decision: %v", decision)
				shouldBid = true
				bid = blackboard.BidTypeIgnore
			}
		}
	} else {
		// Traditional bidding logic (M4.8)
		shouldBid = true // Always bid (unless filtered)
		bid = config.BiddingStrategy.Type

		// Fetch target artefact for filtering
		targetArtefact, err := bbClient.GetArtefact(ctx, claim.ArtefactID)
		if err != nil {
			log.Printf("[Controller] Failed to fetch artefact %s: %v", claim.ArtefactID, err)
			// Fallback ignore
			if err := bbClient.SetBid(ctx, claim.ID, config.AgentName, blackboard.BidTypeIgnore); err != nil {
				log.Printf("[Controller] Failed to submit fallback ignore bid: %v", err)
			}
			return
		}

		// M4.8: Check target types filtering
		if len(config.BiddingStrategy.TargetTypes) > 0 {
			match := false
			for _, t := range config.BiddingStrategy.TargetTypes {
				if t == targetArtefact.Header.Type {
					match = true
					break
				}
			}
			if !match {
				bid = blackboard.BidTypeIgnore
			}
		}
	}

	// Submit bid if ready
	if shouldBid {
		if err := bbClient.SetBid(ctx, claim.ID, config.AgentName, bid); err != nil {
			log.Printf("[Controller] Failed to submit bid for claim %s: %v", claim.ID, err)
			return
		}

		log.Printf("[Controller] Submitted bid: claim=%s type=%s status=%s", claim.ID, bid, claim.Status)
	}

	// M5.1.1: NEW - Evaluate merge bid for Fan-In Accumulator pattern
	// Merge bids are submitted IN ADDITION to regular bids (not mutually exclusive)
	// The Orchestrator will process merge bids in the 4th phase (after exclusive)
	if config.SynchronizeConfig != nil {
		mergeBid, err := shouldBidMerge(ctx, bbClient, config.AgentName, claim, config.SynchronizeConfig)
		if err != nil {
			log.Printf("[Controller] Merge bid evaluation failed: %v", err)
			// Don't return - merge bid failure shouldn't block regular bidding
		} else if mergeBid != nil {
			// Submit merge bid using the new Bid type with metadata
			if err := bbClient.SubmitBid(ctx, claim.ID, mergeBid); err != nil {
				log.Printf("[Controller] Failed to submit merge bid for claim %s: %v", claim.ID, err)
			}
		}
	}
}
