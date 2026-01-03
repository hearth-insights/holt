package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"time"

	"github.com/hearth-insights/holt/internal/spine"
	"github.com/hearth-insights/holt/pkg/blackboard"
)

// OutputFormat defines the output format for watch streaming
type OutputFormat string

const (
	OutputFormatDefault OutputFormat = "default"
	OutputFormatJSONL   OutputFormat = "jsonl"
)

// FilterCriteria defines filtering options for watch command.
// All filters are ANDed together.
type FilterCriteria struct {
	SinceTimestampMs int64  // Unix timestamp in milliseconds, 0 = no filter
	UntilTimestampMs int64  // Unix timestamp in milliseconds, 0 = no filter
	TypeGlob         string // Glob pattern for artefact type, empty = no filter
	AgentRole        string // Exact match for produced_by_role, empty = no filter
}

// matchesFilter returns true if the artefact matches all filter criteria.
func (fc *FilterCriteria) matchesFilter(art *blackboard.Artefact) bool {
	// Time filtering
	// Note: If created_at_ms is 0 (old data without timestamps), include it
	// This ensures historical replay works with pre-M3.9 data
	if fc.SinceTimestampMs > 0 && art.Header.CreatedAtMs > 0 && art.Header.CreatedAtMs < fc.SinceTimestampMs {
		return false
	}
	if fc.UntilTimestampMs > 0 && art.Header.CreatedAtMs > 0 && art.Header.CreatedAtMs > fc.UntilTimestampMs {
		return false
	}

	// Type filtering - glob pattern matching
	if fc.TypeGlob != "" {
		matched, err := filepath.Match(fc.TypeGlob, art.Header.Type)
		if err != nil || !matched {
			return false
		}
	}

	// Agent filtering - exact match on produced_by_role
	if fc.AgentRole != "" && art.Header.ProducedByRole != fc.AgentRole {
		return false
	}

	return true
}

// hasFilters returns true if any filters are active.
func (fc *FilterCriteria) hasFilters() bool {
	return fc.SinceTimestampMs > 0 ||
		fc.UntilTimestampMs > 0 ||
		fc.TypeGlob != "" ||
		fc.AgentRole != ""
}

// PollForClaim polls for claim creation for a given artefact ID.
// Returns the created claim or an error if timeout occurs.
// Polls every 200ms for the specified timeout duration.
func PollForClaim(ctx context.Context, client *blackboard.Client, artefactID string, timeout time.Duration) (*blackboard.Claim, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	timeoutCh := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-timeoutCh:
			return nil, fmt.Errorf("timeout waiting for claim after %v", timeout)

		case <-ticker.C:
			claim, err := client.GetClaimByArtefactID(ctx, artefactID)
			if err != nil {
				if blackboard.IsNotFound(err) {
					// Not found yet, continue polling
					continue
				}
				return nil, fmt.Errorf("failed to query for claim: %w", err)
			}

			// Success!
			return claim, nil
		}
	}
}

// StreamActivity streams workflow events to the provided writer with filtering support.
// Displays historical events first (if filters active), then streams live events.
// Subscribes to artefact_events, claim_events, and workflow_events channels.
// Handles reconnection on transient failures with 2s retry interval and 60s timeout.
//
// If exitOnCompletion is true, exits with nil when a Terminal artefact is detected.
// If verbose is true, shows all events including ClaimComplete artefacts. Otherwise hides verbose events.
func StreamActivity(ctx context.Context, client *blackboard.Client, instanceName string, format OutputFormat, filters *FilterCriteria, exitOnCompletion bool, verbose bool, writer io.Writer) error {
	// Create formatter
	var formatter eventFormatter
	switch format {
	case OutputFormatJSONL:
		formatter = &jsonlFormatter{writer: writer, verbose: verbose}
	default:
		formatter = &defaultFormatter{
			writer:  writer,
			client:  client,
			verbose: verbose,
		}
	}

	// Phase 1: Query and display historical events if filters are active
	// Note: For now, we only query historical artefacts. Claims and workflow events
	// are typically short-lived and stored in Redis with TTL, so historical query
	// focuses on artefacts which are the primary persistent data.
	// Live streaming will show all event types (artefacts, claims, workflow events).
	if filters != nil && filters.hasFilters() {
		if err := displayHistoricalArtefacts(ctx, client, instanceName, filters, formatter); err != nil {
			// Log error but continue to live streaming
			log.Printf("⚠️  Failed to query historical artefacts: %v", err)
		}
	}

	// Phase 2: Subscribe to live events with reconnection logic
	for {
		err := streamWithSubscriptions(ctx, client, formatter, filters, exitOnCompletion)
		if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
			// Clean exit (includes Terminal detection if exitOnCompletion)
			return nil
		}

		// Connection error - attempt reconnection
		fmt.Fprintf(writer, "⚠️  Connection to blackboard lost: %v. Reconnecting...\n", err)

		// Try to reconnect with timeout
		reconnectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err = reconnectWithRetry(reconnectCtx, client, 2*time.Second)
		cancel()

		if err != nil {
			return fmt.Errorf("failed to reconnect after 60s: %w", err)
		}

		fmt.Fprintf(writer, "✓ Reconnected to blackboard\n")
	}
}

// displayHistoricalArtefacts queries and displays historical events (artefacts, claims, workflow events) matching filters.
// Reconstructs the complete workflow event sequence from stored data in Redis.
func displayHistoricalArtefacts(ctx context.Context, client *blackboard.Client, instanceName string, filters *FilterCriteria, formatter eventFormatter) error {
	// historicalEvent represents any event (artefact, claim, or workflow event) with a timestamp
	type historicalEvent struct {
		timestampMs   int64
		artefact      *blackboard.Artefact
		claim         *blackboard.Claim
		workflowEvent *blackboard.WorkflowEvent
	}

	var allEvents []historicalEvent
	artefactsByID := make(map[string]*blackboard.Artefact)
	allClaimsByID := make(map[string]*blackboard.Claim)

	// Phase 1: Collect all artefacts
	artefactPattern := fmt.Sprintf("holt:%s:artefact:*", instanceName)
	iter := client.RedisClient().Scan(ctx, 0, artefactPattern, 0).Iterator()

	hasArtefactsWithoutTimestamps := false

	for iter.Next(ctx) {
		key := iter.Val()
		artefactPrefix := fmt.Sprintf("holt:%s:artefact:", instanceName)
		if len(key) <= len(artefactPrefix) {
			continue
		}
		artefactID := key[len(artefactPrefix):]

		artefact, err := client.GetArtefact(ctx, artefactID)
		if err != nil {
			continue // Skip malformed artefacts
		}

		artefactsByID[artefactID] = artefact

		// Track if we have old data without timestamps
		if artefact.Header.CreatedAtMs == 0 {
			hasArtefactsWithoutTimestamps = true
		}

		// Apply filters to artefact
		if !filters.matchesFilter(artefact) {
			continue // Skip artefacts that don't match filters
		}

		// Add artefact event (will be filtered by formatter for Review/reworked artefacts)
		allEvents = append(allEvents, historicalEvent{
			timestampMs: artefact.Header.CreatedAtMs,
			artefact:    artefact,
		})
	}

	// Warn user if we detected old data without timestamps
	if hasArtefactsWithoutTimestamps && filters.hasFilters() {
		log.Printf("⚠️  Warning: Some artefacts lack timestamps (pre-M3.9 data). Time-based filtering may be inaccurate.")
		log.Printf("    To get accurate historical replay, flush Redis and re-run your workflow.")
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan artefacts: %w", err)
	}

	// Phase 2: Collect ALL claims first
	claimPattern := fmt.Sprintf("holt:%s:claim:*", instanceName)
	iter = client.RedisClient().Scan(ctx, 0, claimPattern, 0).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		claimPrefix := fmt.Sprintf("holt:%s:claim:", instanceName)
		if len(key) <= len(claimPrefix) {
			continue
		}

		// Skip bid keys (they have format claim:{uuid}:bids)
		if len(key) > len(claimPrefix)+36 && key[len(claimPrefix)+36] == ':' {
			continue
		}

		claimID := key[len(claimPrefix):]
		claim, err := client.GetClaim(ctx, claimID)
		if err != nil {
			log.Printf("⚠️  Warning: Failed to load claim %s: %v", claimID, err)
			continue // Skip malformed claims
		}

		allClaimsByID[claimID] = claim
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan claims: %w", err)
	}

	// Phase 3: Process each artefact and find ALL claims for it
	// Group claims by artefact ID
	claimsByArtefact := make(map[string][]*blackboard.Claim)
	for _, claim := range allClaimsByID {
		claimsByArtefact[claim.ArtefactID] = append(claimsByArtefact[claim.ArtefactID], claim)
	}

	// Sort claims for each artefact (we'll process them in order)
	for _, claims := range claimsByArtefact {
		sort.Slice(claims, func(i, j int) bool {
			// Terminated or Dormant claims come before complete/pending_assignment claims
			// This ensures we show: original claim (terminated/dormant) → feedback claim (pending_assignment/complete)
			if (claims[i].Status == blackboard.ClaimStatusTerminated || claims[i].Status == blackboard.ClaimStatusDormant) &&
				(claims[j].Status != blackboard.ClaimStatusTerminated && claims[j].Status != blackboard.ClaimStatusDormant) {
				return true
			}
			if (claims[i].Status != blackboard.ClaimStatusTerminated && claims[i].Status != blackboard.ClaimStatusDormant) &&
				(claims[j].Status == blackboard.ClaimStatusTerminated || claims[j].Status == blackboard.ClaimStatusDormant) {
				return false
			}
			// Otherwise maintain order by ID (arbitrary but consistent)
			return claims[i].ID < claims[j].ID
		})
	}

	// Phase 4: Process each artefact and reconstruct its workflow events
	for _, artefact := range artefactsByID {
		// Only process artefacts that match filters
		if !filters.matchesFilter(artefact) {
			continue
		}

		// Find ALL claims for this artefact
		claims := claimsByArtefact[artefact.ID]
		if len(claims) == 0 {
			// No claims (e.g., Terminal artefacts don't get claims)
			continue
		}

		// Process each claim for this artefact
		for _, primaryClaim := range claims {

			// Timestamp for claim events - use artefact creation time
			claimTimestampMs := artefact.Header.CreatedAtMs

			// Add claim created event
			allEvents = append(allEvents, historicalEvent{
				timestampMs: claimTimestampMs,
				claim:       primaryClaim,
			})

			// Reconstruct bid_submitted events from PhaseState.AllBids
			if primaryClaim.PhaseState != nil && len(primaryClaim.PhaseState.AllBids) > 0 {
				// Sort agents to ensure deterministic event ordering (even with timestamps)
				var agents []string
				for agent := range primaryClaim.PhaseState.AllBids {
					agents = append(agents, agent)
				}
				sort.Strings(agents)

				for _, agentName := range agents {
					bidType := primaryClaim.PhaseState.AllBids[agentName]

					// Use stored timestamp if available, otherwise fallback to claim timestamp + 1ms
					bidTimestamp := claimTimestampMs + 1
					if ts, ok := primaryClaim.PhaseState.BidTimestamps[agentName]; ok && ts > 0 {
						bidTimestamp = ts
					}

					workflowEvent := &blackboard.WorkflowEvent{
						Event: "bid_submitted",
						Data: map[string]interface{}{
							"agent_name": agentName,
							"claim_id":   primaryClaim.ID,
							"bid_type":   string(bidType),
						},
					}
					allEvents = append(allEvents, historicalEvent{
						timestampMs:   bidTimestamp,
						workflowEvent: workflowEvent,
					})
				}
			}

			// Reconstruct claim_granted events
			grantOffset := int64(100) // Grants come ~100ms after bids

			// Review phase grants
			for _, agentName := range primaryClaim.GrantedReviewAgents {
				workflowEvent := &blackboard.WorkflowEvent{
					Event: "claim_granted",
					Data: map[string]interface{}{
						"agent_name":     agentName,
						"claim_id":       primaryClaim.ID,
						"grant_type":     "review",
						"agent_image_id": primaryClaim.GrantedAgentImageID,
					},
				}
				allEvents = append(allEvents, historicalEvent{
					timestampMs:   claimTimestampMs + grantOffset,
					workflowEvent: workflowEvent,
				})
				grantOffset += 1
			}

			// Parallel phase grants
			for _, agentName := range primaryClaim.GrantedParallelAgents {
				workflowEvent := &blackboard.WorkflowEvent{
					Event: "claim_granted",
					Data: map[string]interface{}{
						"agent_name":     agentName,
						"claim_id":       primaryClaim.ID,
						"grant_type":     "claim",
						"agent_image_id": primaryClaim.GrantedAgentImageID,
					},
				}
				allEvents = append(allEvents, historicalEvent{
					timestampMs:   claimTimestampMs + grantOffset,
					workflowEvent: workflowEvent,
				})
				grantOffset += 1
			}

			// Exclusive phase grant
			if primaryClaim.GrantedExclusiveAgent != "" {
				// M3.3: Skip synthetic grant events for feedback claims (rework)
				// These are direct assignments, not competitive grants, and shouldn't appear
				// as "granted" at the original artefact creation time.
				if len(primaryClaim.AdditionalContextIDs) > 0 {
					continue
				}
				workflowEvent := &blackboard.WorkflowEvent{
					Event: "claim_granted",
					Data: map[string]interface{}{
						"agent_name":     primaryClaim.GrantedExclusiveAgent,
						"claim_id":       primaryClaim.ID,
						"grant_type":     "exclusive",
						"agent_image_id": primaryClaim.GrantedAgentImageID,
					},
				}
				allEvents = append(allEvents, historicalEvent{
					timestampMs:   claimTimestampMs + grantOffset,
					workflowEvent: workflowEvent,
				})
			}

			// Reconstruct feedback_claim_created events for terminated claims
			// The feedback claim is a separate claim (could be pending_assignment, complete, etc.)
			// that has additional_context_ids populated with review artefact IDs
			// Only do this for terminated claims to avoid showing the feedback_claim_created event multiple times
			if primaryClaim.Status == blackboard.ClaimStatusTerminated {
				// Find the feedback claim (has additional_context_ids and same artefact ID)
				for _, otherClaim := range allClaimsByID {
					if len(otherClaim.AdditionalContextIDs) > 0 &&
						otherClaim.ArtefactID == artefact.ID &&
						otherClaim.ID != primaryClaim.ID {

						// Find the latest review timestamp to place these events after
						latestReviewTs := claimTimestampMs
						for _, reviewArtefact := range artefactsByID {
							if reviewArtefact.Header.StructuralType != blackboard.StructuralTypeReview {
								continue
							}
							for _, sourceID := range reviewArtefact.Header.ParentHashes {
								if sourceID == artefact.ID && reviewArtefact.Header.CreatedAtMs > latestReviewTs {
									latestReviewTs = reviewArtefact.Header.CreatedAtMs
								}
							}
						}

						workflowEvent := &blackboard.WorkflowEvent{
							Event: "feedback_claim_created",
							Data: map[string]interface{}{
								"target_agent_role": otherClaim.GrantedExclusiveAgent,
								"feedback_claim_id": otherClaim.ID,
								"iteration":         artefact.Header.Version,
							},
						}
						// Feedback assignment comes 1ms after the last review
						allEvents = append(allEvents, historicalEvent{
							timestampMs:   latestReviewTs + 1,
							workflowEvent: workflowEvent,
						})

						// NOTE: Don't add the feedback claim here - it will be processed
						// in its own iteration of the claim loop
						break
					}
				}
			}

			// Reconstruct claim_dormant event for dormant claims
			if primaryClaim.Status == blackboard.ClaimStatusDormant {
				// Determine timestamp (slightly after bids)
				dormantTimestamp := claimTimestampMs + 200 // Arbitrary small offset after consensus

				// Try to use phase state bid timestamps to place it after the last bid
				if primaryClaim.PhaseState != nil {
					for _, ts := range primaryClaim.PhaseState.BidTimestamps {
						if ts > dormantTimestamp {
							dormantTimestamp = ts + 1
						}
					}
				}

				workflowEvent := &blackboard.WorkflowEvent{
					Event: "claim_dormant",
					Data: map[string]interface{}{
						"claim_id": primaryClaim.ID,
						"reason":   "no_bids_in_any_phase", // We don't persist reason separately but this is the only path to dormant currently
					},
				}
				allEvents = append(allEvents, historicalEvent{
					timestampMs:   dormantTimestamp,
					workflowEvent: workflowEvent,
				})
			}
		} // End of loop over claims for this artefact

		// Reconstruct review_approved/review_rejected events from Review artefacts
		// Do this ONCE per artefact (outside claim loop to avoid duplicates)
		for _, reviewArtefact := range artefactsByID {
			if reviewArtefact.Header.StructuralType != blackboard.StructuralTypeReview {
				continue
			}

			// Check if this review is for the current artefact
			isForThisArtefact := false
			for _, sourceID := range reviewArtefact.Header.ParentHashes {
				if sourceID == artefact.ID {
					isForThisArtefact = true
					break
				}
			}

			if !isForThisArtefact {
				continue
			}

			// Determine if approved or rejected based on payload
			// Approvals must be empty JSON object {} or empty array []
			// Any other content is treated as feedback/rejection
			eventType := "review_rejected" // Default to rejected unless proven empty

			var jsonData interface{}
			if err := json.Unmarshal([]byte(reviewArtefact.Payload.Content), &jsonData); err == nil {
				switch v := jsonData.(type) {
				case map[string]interface{}:
					if len(v) == 0 {
						eventType = "review_approved"
					}
				case []interface{}:
					if len(v) == 0 {
						eventType = "review_approved"
					}
				}
			}

			workflowEvent := &blackboard.WorkflowEvent{
				Event: eventType,
				Data: map[string]interface{}{
					"reviewer_role":        reviewArtefact.Header.ProducedByRole,
					"original_artefact_id": artefact.ID,
					"review_artefact_id":   reviewArtefact.ID,
				},
			}
			// Use review artefact's actual creation time
			allEvents = append(allEvents, historicalEvent{
				timestampMs:   reviewArtefact.Header.CreatedAtMs,
				workflowEvent: workflowEvent,
			})
		}
	} // End of loop over artefacts

	// Phase 5: Reconstruct artefact_reworked events for reworked artefacts (version > 1)
	// These should appear BEFORE the reworked artefact itself
	for _, artefact := range artefactsByID {
		if artefact.Header.Version > 1 && filters.matchesFilter(artefact) {
			workflowEvent := &blackboard.WorkflowEvent{
				Event: "artefact_reworked",
				Data: map[string]interface{}{
					"produced_by_role": artefact.Header.ProducedByRole,
					"artefact_type":    artefact.Header.Type,
					"new_artefact_id":  artefact.ID,
					"new_version":      artefact.Header.Version,
				},
			}
			// Place rework event 1ms before the artefact creation
			allEvents = append(allEvents, historicalEvent{
				timestampMs:   artefact.Header.CreatedAtMs - 1,
				workflowEvent: workflowEvent,
			})
		}
	}

	// Phase 6: Sort all events chronologically
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].timestampMs < allEvents[j].timestampMs
	})

	// Phase 7: Format and output events
	for _, evt := range allEvents {
		if evt.artefact != nil {
			if err := formatter.FormatArtefact(evt.artefact); err != nil {
				log.Printf("⚠️  Failed to format historical artefact: %v", err)
			}
		} else if evt.claim != nil {
			if err := formatter.FormatClaim(evt.claim, evt.timestampMs); err != nil {
				log.Printf("⚠️  Failed to format historical claim: %v", err)
			}
		} else if evt.workflowEvent != nil {
			if err := formatter.FormatWorkflow(evt.workflowEvent, evt.timestampMs); err != nil {
				log.Printf("⚠️  Failed to format historical workflow event: %v", err)
			}
		}
	}

	return nil
}

// streamWithSubscriptions creates subscriptions and streams events until error or cancellation
func streamWithSubscriptions(ctx context.Context, client *blackboard.Client, formatter eventFormatter, filters *FilterCriteria, exitOnCompletion bool) error {
	// Subscribe to all three channels
	artefactSub, err := client.SubscribeArtefactEvents(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to artefact events: %w", err)
	}
	defer artefactSub.Close()

	claimSub, err := client.SubscribeClaimEvents(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to claim events: %w", err)
	}
	defer claimSub.Close()

	workflowSub, err := client.SubscribeWorkflowEvents(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to workflow events: %w", err)
	}
	defer workflowSub.Close()

	securitySub := client.SubscribeSecurityAlerts(ctx)
	defer securitySub.Close()

	// Stream events from all channels
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case artefact, ok := <-artefactSub.Events():
			if !ok {
				return fmt.Errorf("artefact events channel closed")
			}

			// Apply filters
			if filters != nil && !filters.matchesFilter(artefact) {
				continue
			}

			// Format and output artefact
			if err := formatter.FormatArtefact(artefact); err != nil {
				log.Printf("⚠️  Failed to format artefact event: %v", err)
			}

			// Check for Terminal artefact if exitOnCompletion is enabled
			if exitOnCompletion && artefact.Header.StructuralType == blackboard.StructuralTypeTerminal {
				return nil // Clean exit
			}

		case claim, ok := <-claimSub.Events():
			if !ok {
				return fmt.Errorf("claim events channel closed")
			}
			// For live claims, use current time (0 will trigger time.Now() in formatter)
			if err := formatter.FormatClaim(claim, 0); err != nil {
				log.Printf("⚠️  Failed to format claim event: %v", err)
			}

		case workflow, ok := <-workflowSub.Events():
			if !ok {
				return fmt.Errorf("workflow events channel closed")
			}
			// For live workflow events, use current time (0 will trigger time.Now() in formatter)
			if err := formatter.FormatWorkflow(workflow, 0); err != nil {
				log.Printf("⚠️  Failed to format workflow event: %v", err)
			}

		case alertMessage, ok := <-securitySub.Channel():
			if !ok {
				return fmt.Errorf("security alerts channel closed")
			}

			var alert blackboard.SecurityAlert
			if err := json.Unmarshal([]byte(alertMessage.Payload), &alert); err != nil {
				log.Printf("⚠️  Failed to parse security alert: %v", err)
				continue
			}

			if err := formatter.FormatSecurityAlert(&alert); err != nil {
				log.Printf("⚠️  Failed to format security alert: %v", err)
			}

		case err := <-artefactSub.Errors():
			log.Printf("⚠️  Failed to parse artefact event: %v", err)

		case err := <-claimSub.Errors():
			log.Printf("⚠️  Failed to parse claim event: %v", err)

		case err := <-workflowSub.Errors():
			log.Printf("⚠️  Failed to parse workflow event: %v", err)
		}
	}
}

// reconnectWithRetry attempts to reconnect to Redis with retries
func reconnectWithRetry(ctx context.Context, client *blackboard.Client, retryInterval time.Duration) error {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			if err := client.Ping(ctx); err == nil {
				return nil
			}
			// Continue retrying
		}
	}
}

// shortID returns the first 8 characters of a UUID for readability
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// eventFormatter formats events for output
type eventFormatter interface {
	FormatArtefact(artefact *blackboard.Artefact) error
	FormatClaim(claim *blackboard.Claim, timestampMs int64) error
	FormatWorkflow(event *blackboard.WorkflowEvent, timestampMs int64) error
	FormatSecurityAlert(alert *blackboard.SecurityAlert) error
}

// defaultFormatter produces human-readable output with emojis
type defaultFormatter struct {
	writer  io.Writer
	client  *blackboard.Client
	verbose bool // Show verbose events (ClaimComplete artefacts, etc.)
}

func (f *defaultFormatter) FormatArtefact(artefact *blackboard.Artefact) error {
	// Filter out Review artefacts - they're shown via review_approved/review_rejected events
	if artefact.Header.StructuralType == blackboard.StructuralTypeReview {
		return nil
	}

	// Filter out reworked artefacts (version > 1) - they're shown via artefact_reworked events
	if artefact.Header.Version > 1 {
		return nil
	}

	// Use artefact's creation timestamp, fallback to current time for live events
	timestamp := formatTimestampMs(artefact.Header.CreatedAtMs)

	// Hide Terminal artefacts (ClaimComplete, etc.) unless verbose mode
	if artefact.Header.StructuralType == blackboard.StructuralTypeTerminal {
		if !f.verbose {
			return nil // Skip Terminal artefacts in non-verbose mode
		}
		// In verbose mode, show with clearer messaging
		_, err := fmt.Fprintf(f.writer, "[%s] 🏁 Claim completed: agent=%s, type=%s, claim_complete_id=%s\n",
			timestamp, artefact.Header.ProducedByRole, artefact.Header.Type, shortID(artefact.ID))
		return err
	}

	_, err := fmt.Fprintf(f.writer, "[%s] ✨ Artefact created: by=%s, type=%s, id=%s",
		timestamp, artefact.Header.ProducedByRole, artefact.Header.Type, shortID(artefact.ID))
	if err != nil {
		return err
	}

	// M4.7: For GoalDefined artefacts, resolve and display spine info
	if artefact.Header.Type == "GoalDefined" && f.client != nil {
		// Create a temporary context for the lookup
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Resolve spine (no cache needed for single lookup)
		spineInfo, err := spine.ResolveSpine(ctx, f.client, artefact, make(map[string]*spine.SpineInfo))
		if err == nil && !spineInfo.IsDetached {
			_, _ = fmt.Fprintf(f.writer, " (anchored to spine=%s)", shortID(spineInfo.ManifestID))
		}
	}

	_, err = fmt.Fprintf(f.writer, "\n")
	return err
}

func (f *defaultFormatter) FormatClaim(claim *blackboard.Claim, timestampMs int64) error {
	timestamp := formatTimestampMs(timestampMs)
	_, err := fmt.Fprintf(f.writer, "[%s] ⏳ Claim created: claim=%s, artefact=%s, status=%s\n",
		timestamp, shortID(claim.ID), shortID(claim.ArtefactID), claim.Status)
	return err
}

func (f *defaultFormatter) FormatWorkflow(event *blackboard.WorkflowEvent, timestampMs int64) error {
	timestamp := formatTimestampMs(timestampMs)

	switch event.Event {
	case "bid_submitted":
		agentName, _ := event.Data["agent_name"].(string)
		claimID, _ := event.Data["claim_id"].(string)
		bidType, _ := event.Data["bid_type"].(string)
		_, err := fmt.Fprintf(f.writer, "[%s] 🙋 Bid submitted: agent=%s, claim=%s, type=%s\n",
			timestamp, agentName, shortID(claimID), bidType)
		return err

	case "claim_dormant":
		claimID, _ := event.Data["claim_id"].(string)
		reason, _ := event.Data["reason"].(string)
		_, err := fmt.Fprintf(f.writer, "[%s] 💤 Claim dormant: claim=%s, reason=%s\n",
			timestamp, shortID(claimID), reason)
		return err

	case "claim_granted":
		agentName, _ := event.Data["agent_name"].(string)
		claimID, _ := event.Data["claim_id"].(string)
		grantType, _ := event.Data["grant_type"].(string)
		agentImageID, _ := event.Data["agent_image_id"].(string) // M3.9

		// M3.9: Display agent@imageID format
		agentDisplay := agentName
		if agentImageID != "" {
			agentDisplay = fmt.Sprintf("%s@%s", agentName, truncateImageID(agentImageID))
		}

		_, err := fmt.Fprintf(f.writer, "[%s] 🏆 Claim granted: agent=%s, claim=%s, type=%s\n",
			timestamp, agentDisplay, shortID(claimID), grantType)
		return err

	case "review_approved":
		reviewerRole, _ := event.Data["reviewer_role"].(string)
		originalArtefactID, _ := event.Data["original_artefact_id"].(string)
		reviewArtefactID, _ := event.Data["review_artefact_id"].(string)

		_, err := fmt.Fprintf(f.writer, "[%s] ✅ Review Approved: by=%s for artefact %s (review: %s)\n",
			timestamp, reviewerRole, shortID(originalArtefactID), shortID(reviewArtefactID))
		return err

	case "review_rejected":
		reviewerRole, _ := event.Data["reviewer_role"].(string)
		originalArtefactID, _ := event.Data["original_artefact_id"].(string)
		reviewArtefactID, _ := event.Data["review_artefact_id"].(string)

		_, err := fmt.Fprintf(f.writer, "[%s] ❌ Review Rejected: by=%s for artefact %s (review: %s)\n",
			timestamp, reviewerRole, shortID(originalArtefactID), shortID(reviewArtefactID))
		return err

	case "feedback_claim_created":
		targetAgentRole, _ := event.Data["target_agent_role"].(string)
		feedbackClaimID, _ := event.Data["feedback_claim_id"].(string)
		iteration := 1 // default
		if iter, ok := event.Data["iteration"].(int); ok {
			iteration = iter
		} else if iterFloat, ok := event.Data["iteration"].(float64); ok {
			iteration = int(iterFloat)
		}

		_, err := fmt.Fprintf(f.writer, "[%s] 🔄 Rework Assigned: to=%s for claim %s (iteration %d)\n",
			timestamp, targetAgentRole, shortID(feedbackClaimID), iteration)
		return err

	case "artefact_reworked":
		producedByRole, _ := event.Data["produced_by_role"].(string)
		artefactType, _ := event.Data["artefact_type"].(string)
		newArtefactID, _ := event.Data["new_artefact_id"].(string)
		newVersion := 1 // default
		if ver, ok := event.Data["new_version"].(int); ok {
			newVersion = ver
		} else if verFloat, ok := event.Data["new_version"].(float64); ok {
			newVersion = int(verFloat)
		}

		_, err := fmt.Fprintf(f.writer, "[%s] 🔄 Artefact Reworked (v%d): by=%s, type=%s, id=%s\n",
			timestamp, newVersion, producedByRole, artefactType, shortID(newArtefactID))
		return err

	case "human_input_required":
		// M4.11: Display human input required event as compact single line
		questionID, _ := event.Data["question_id"].(string)
		questionText, _ := event.Data["question_text"].(string)
		// targetArtefactID unused in compact view

		_, err := fmt.Fprintf(f.writer, "[%s] ⚠️  HUMAN_INPUT_REQUIRED: %s (id=%s)\n",
			timestamp, questionText, shortID(questionID))
		return err

	default:
		// Unknown events are only shown in verbose mode
		if !f.verbose {
			return nil // Silently skip unknown events
		}
		_, err := fmt.Fprintf(f.writer, "[%s] ❓ Unknown event: %s\n", timestamp, event.Event)
		return err
	}
}

func (f *defaultFormatter) FormatSecurityAlert(alert *blackboard.SecurityAlert) error {
	timestamp := formatTimestampMs(alert.TimestampMs)

	// Make it REALLY OBVIOUS with spacing and big headers
	fmt.Fprintf(f.writer, "\n")
	fmt.Fprintf(f.writer, "🚨🚨🚨 SECURITY ALERT: %s 🚨🚨🚨\n", alert.Type)
	fmt.Fprintf(f.writer, "[%s] ACTION: %s\n", timestamp, alert.OrchestratorAction)

	// Add specific details based on alert type
	switch alert.Type {
	case "hash_mismatch":
		fmt.Fprintf(f.writer, "❌  HASH MISMATCH DETECTED (M5.1 Tamper Protection)\n")
		fmt.Fprintf(f.writer, "    Artefact Claimed: %s\n", alert.ArtefactIDClaimed)
		fmt.Fprintf(f.writer, "    Claimed Hash:     %s\n", alert.HashActual)
		fmt.Fprintf(f.writer, "    Computed Hash:    %s\n", alert.HashExpected)
		fmt.Fprintf(f.writer, "    Agent Role:       %s\n", alert.AgentRole)
		if alert.ClaimID != "" {
			fmt.Fprintf(f.writer, "    Claim ID:         %s\n", alert.ClaimID)
		}

	case "orphan_block":
		fmt.Fprintf(f.writer, "❌  ORPHAN BLOCK DETECTED (Missing Parent)\n")
		fmt.Fprintf(f.writer, "    Artefact ID:      %s\n", alert.ArtefactID)
		fmt.Fprintf(f.writer, "    Missing Parent:   %s\n", alert.MissingParentHash)
		fmt.Fprintf(f.writer, "    Agent Role:       %s\n", alert.AgentRole)

	default:
		// Generic dump for other types
		details, _ := json.MarshalIndent(alert, "    ", "  ")
		fmt.Fprintf(f.writer, "    Details:\n%s\n", string(details))
	}

	fmt.Fprintf(f.writer, "\n")
	return nil
}

// jsonlFormatter produces line-delimited JSON output (JSONL format)
type jsonlFormatter struct {
	writer  io.Writer
	verbose bool // Show verbose events (ClaimComplete artefacts, etc.)
}

func (f *jsonlFormatter) FormatArtefact(artefact *blackboard.Artefact) error {
	// Use artefact's creation timestamp, fallback to current time for live events
	timestampMs := artefact.Header.CreatedAtMs
	if timestampMs == 0 {
		timestampMs = time.Now().UnixMilli()
	}

	output := map[string]interface{}{
		"timestamp": time.UnixMilli(timestampMs).UTC().Format(time.RFC3339),
		"event":     "artefact_created",
		"data":      artefact,
	}
	if err := f.writeJSON(output); err != nil {
		return err
	}

	// Add workflow_completed event for Terminal artefacts
	if artefact.Header.StructuralType == blackboard.StructuralTypeTerminal {
		completionOutput := map[string]interface{}{
			"timestamp": time.UnixMilli(timestampMs).UTC().Format(time.RFC3339),
			"event":     "workflow_completed",
			"data": map[string]interface{}{
				"artefact_id":   artefact.ID,
				"artefact_type": artefact.Header.Type,
				"produced_by":   artefact.Header.ProducedByRole,
			},
		}
		return f.writeJSON(completionOutput)
	}

	return nil
}

func (f *jsonlFormatter) FormatClaim(claim *blackboard.Claim, timestampMs int64) error {
	if timestampMs == 0 {
		timestampMs = time.Now().UnixMilli()
	}

	output := map[string]interface{}{
		"timestamp": time.UnixMilli(timestampMs).UTC().Format(time.RFC3339),
		"event":     "claim_created",
		"data":      claim,
	}
	return f.writeJSON(output)
}

func (f *jsonlFormatter) FormatWorkflow(event *blackboard.WorkflowEvent, timestampMs int64) error {
	// All workflow events are shown in JSONL mode
	// (filtering is done by downstream tools like jq)

	if timestampMs == 0 {
		timestampMs = time.Now().UnixMilli()
	}

	output := map[string]interface{}{
		"timestamp": time.UnixMilli(timestampMs).UTC().Format(time.RFC3339),
		"event":     event.Event,
		"data":      event.Data,
	}
	return f.writeJSON(output)
}

func (f *jsonlFormatter) FormatSecurityAlert(alert *blackboard.SecurityAlert) error {
	output := map[string]interface{}{
		"timestamp": time.UnixMilli(alert.TimestampMs).UTC().Format(time.RFC3339),
		"event":     "security_alert",
		"data":      alert,
	}
	return f.writeJSON(output)
}

func (f *jsonlFormatter) writeJSON(data interface{}) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f.writer, "%s\n", string(bytes))
	return err
}

// truncateImageID shortens an image ID/digest for display (M3.9).
// Extracts first 12 characters of sha256 hash.
func truncateImageID(imageID string) string {
	// Handle "sha256:..." format
	if len(imageID) > 7 && imageID[:7] == "sha256:" {
		hash := imageID[7:]
		if len(hash) >= 12 {
			return hash[:12]
		}
		return hash
	}

	// Handle other formats
	if len(imageID) >= 12 {
		return imageID[:12]
	}

	return imageID
}

// formatTimestampMs formats a Unix millisecond timestamp as HH:MM:SS.mmm.
// If timestampMs is 0, uses current time (for live events without stored timestamps).
func formatTimestampMs(timestampMs int64) string {
	if timestampMs == 0 {
		return time.Now().Format("15:04:05.000")
	}
	t := time.UnixMilli(timestampMs)
	return t.Format("15:04:05.000")
}
