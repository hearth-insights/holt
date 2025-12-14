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

			// M5.1: Use synchronizer logic if configured
			var shouldBid bool
			var bid blackboard.BidType

			if synchronizer != nil {
				// Synchronizer mode: Evaluate fan-in conditions
				ready, err := synchronizer.shouldBidOnClaim(ctx, claim)
				if err != nil {
					log.Printf("[Controller] Synchronizer error: %v", err)
					// On error, skip bid (don't submit 'ignore' to avoid blocking)
					continue
				}
				shouldBid = ready
				bid = blackboard.BidTypeExclusive // Synchronizers always bid exclusive
			} else {
				// Traditional bidding logic (M4.8)
				shouldBid = true // Always bid (unless filtered)
				bid = config.BiddingStrategy.Type

				// Fetch target artefact for filtering
				targetArtefact, err := bbClient.GetArtefact(ctx, claim.ArtefactID)
				if err != nil {
					log.Printf("[Controller] Failed to fetch artefact %s: %v", claim.ArtefactID, err)
					// If we can't check type, we can't safely bid our strategy.
					// Submit 'ignore' to avoid blocking consensus.
					if err := bbClient.SetBid(ctx, claim.ID, config.AgentName, blackboard.BidTypeIgnore); err != nil {
						log.Printf("[Controller] Failed to submit fallback ignore bid: %v", err)
					}
					continue
				}

				// M4.8: Check target types filtering
				if len(config.BiddingStrategy.TargetTypes) > 0 {
					match := false
					for _, t := range config.BiddingStrategy.TargetTypes {
						if t == targetArtefact.Type {
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
					continue
				}

				log.Printf("[Controller] Submitted bid: claim=%s type=%s status=%s", claim.ID, bid, claim.Status)
			}

		case err, ok := <-subscription.Errors():
			if !ok {
				log.Printf("[Controller] Error channel closed")
				return nil
			}
			log.Printf("[Controller] Subscription error: %v", err)
		}
	}
}
