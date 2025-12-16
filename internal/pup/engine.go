package pup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// Engine represents the core execution logic of the agent pup.
// It manages two concurrent goroutines:
//   - Claim Watcher: Monitors for new claims and evaluates bidding opportunities (M2.2+)
//   - Work Executor: Executes granted work and posts results (M2.3+)
//
// The engine coordinates these goroutines via a work queue channel and
// handles graceful shutdown through context cancellation.
type Engine struct {
	config       *Config
	bbClient     *blackboard.Client
	wg           sync.WaitGroup
	synchronizer *Synchronizer // M5.1: Optional synchronizer for fan-in coordination
}

// New creates a new agent pup engine with the provided configuration and blackboard client.
// The engine is ready to be started but does not begin execution until Start() is called.
//
// Parameters:
//   - config: Agent pup runtime configuration (instance name, agent name, etc.)
//   - bbClient: Blackboard client for Redis operations
//
// Returns a configured Engine ready to start.
func New(config *Config, bbClient *blackboard.Client) *Engine {
	engine := &Engine{
		config:   config,
		bbClient: bbClient,
	}

	// M5.1: Initialize synchronizer if synchronize config is present
	if config.SynchronizeConfig != nil {
		engine.synchronizer = NewSynchronizer(config.SynchronizeConfig, bbClient, config.AgentName)
		log.Printf("[INFO] Synchronizer initialized for agent '%s'", config.AgentName)
	}

	return engine
}

// Start launches the agent pup's concurrent goroutines and blocks until context cancellation.
// Creates a work queue channel and starts both the Claim Watcher and Work Executor goroutines.
//
// The method blocks until:
//   - The provided context is cancelled (normal shutdown)
//   - All goroutines complete their shutdown sequence
//
// Graceful shutdown sequence:
//  1. Context is cancelled (typically via SIGTERM signal)
//  2. Both goroutines detect cancellation via select on ctx.Done()
//  3. Goroutines exit their loops and perform cleanup
//  4. Start() returns once all goroutines complete
//
// Returns nil when shutdown completes successfully.
func (e *Engine) Start(ctx context.Context) error {
	log.Printf("[INFO] Agent pup starting for agent='%s' instance='%s'", e.config.AgentName, e.config.InstanceName)

	// Create work queue with buffer size 1
	// Buffer size 1 allows Claim Watcher to post one claim without blocking
	workQueue := make(chan *blackboard.Claim, 1)

	// Launch Claim Watcher goroutine
	e.wg.Add(1)
	go e.claimWatcher(ctx, workQueue)

	// Launch Work Executor goroutine
	e.wg.Add(1)
	go e.workExecutor(ctx, workQueue)

	// Wait for context cancellation
	<-ctx.Done()
	log.Printf("[INFO] Shutdown signal received, initiating graceful shutdown")

	// Close work queue to signal Work Executor that no more work will arrive
	close(workQueue)

	// Wait for all goroutines to complete
	e.wg.Wait()
	log.Printf("[INFO] All goroutines exited, shutdown complete")

	return nil
}

// claimWatcher monitors for new claims and grant notifications.
// Implements dual-subscription pattern:
//  1. Subscribes to claim_events - receives all new claims, submits bids
//  2. Subscribes to agent:{name}:events - receives grant notifications from orchestrator
//
// When a claim event is received, the pup always bids "exclusive" (M2.2 hardcoded strategy).
// When a grant notification is received, the pup validates it and pushes the claim to the work queue.
//
// The goroutine runs until the context is cancelled, then exits cleanly.
func (e *Engine) claimWatcher(ctx context.Context, workQueue chan *blackboard.Claim) {
	defer e.wg.Done()
	defer log.Printf("[DEBUG] Claim Watcher exited cleanly")

	log.Printf("[DEBUG] Claim Watcher starting")

	// Subscribe to claim events
	claimSub, err := e.bbClient.SubscribeClaimEvents(ctx)
	if err != nil {
		log.Printf("[ERROR] Failed to subscribe to claim events: %v", err)
		return
	}
	defer claimSub.Close()

	// Subscribe to agent-specific grant notifications
	agentChannel := blackboard.AgentEventsChannel(e.config.InstanceName, e.config.AgentName)
	grantSub, err := e.bbClient.SubscribeRawChannel(ctx, agentChannel)
	if err != nil {
		log.Printf("[ERROR] Failed to subscribe to agent events channel: %v", err)
		return
	}
	defer grantSub.Close()

	log.Printf("[INFO] Claim Watcher subscribed to claim_events and %s", agentChannel)

	// Dual-subscription select loop
	for {
		select {
		case <-ctx.Done():
			// Context cancelled - shutdown requested
			log.Printf("[DEBUG] Claim Watcher received shutdown signal")
			return

		case claim, ok := <-claimSub.Events():
			if !ok {
				// Claim events channel closed
				log.Printf("[WARN] Claim events channel closed")
				return
			}
			// Handle claim event - submit bid or handle pending_assignment
			e.handleClaimEvent(ctx, claim, workQueue)

		case grantMsg, ok := <-grantSub.Messages():
			if !ok {
				// Grant events channel closed
				log.Printf("[WARN] Grant events channel closed")
				return
			}
			// Handle grant notification - validate and push to work queue
			e.handleGrantNotification(ctx, grantMsg, workQueue)

		case err, ok := <-claimSub.Errors():
			if !ok {
				log.Printf("[WARN] Claim subscription error channel closed")
				return
			}
			log.Printf("[ERROR] Claim subscription error: %v", err)
			// Continue processing - errors are non-fatal
		}
	}
}

// handleClaimEvent processes a claim event by submitting a bid or handling pre-assigned work.
// M3.3: Detects pending_assignment claims (feedback claims) and pushes directly to work queue.
// M3.4: Uses dynamic bidding script if available, otherwise falls back to static strategy.
func (e *Engine) handleClaimEvent(ctx context.Context, claim *blackboard.Claim, workQueue chan *blackboard.Claim) {
	log.Printf("[INFO] Received claim event: claim_id=%s artefact_id=%s status=%s",
		claim.ID, claim.ArtefactID, claim.Status)

	// M3.3: Handle pending_assignment claims (feedback claims)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		if claim.GrantedExclusiveAgent == e.config.AgentName {
			log.Printf("[INFO] Feedback claim %s is assigned to this agent, pushing to work queue", claim.ID)
			select {
			case workQueue <- claim:
				log.Printf("[DEBUG] Feedback claim %s successfully queued for execution", claim.ID)
			case <-ctx.Done():
				log.Printf("[DEBUG] Context cancelled while queuing feedback claim %s", claim.ID)
			}
		} else {
			log.Printf("[DEBUG] Feedback claim %s assigned to %s, ignoring (we are %s)",
				claim.ID, claim.GrantedExclusiveAgent, e.config.AgentName)
		}
		return // No bidding for pending_assignment claims
	}

	// Regular claim - proceed with bidding logic
	targetArtefact, err := e.bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		log.Printf("[ERROR] Failed to fetch target artefact %s for bid decision: %v", claim.ArtefactID, err)
		return
	}
	if targetArtefact == nil {
		log.Printf("[ERROR] Target artefact %s not found for bid decision", claim.ArtefactID)
		return
	}

	// Determine bid type dynamically or from static config
	bidType, err := e.determineBidType(ctx, claim, targetArtefact)
	if err != nil {
		log.Printf("[ERROR] Failed to determine bid type for claim %s: %v", claim.ID, err)
		// M5.1: For synchronizer errors, create failure artefact (fatal configuration error)
		if e.synchronizer != nil && strings.Contains(err.Error(), "synchronizer") {
			e.createBiddingFailureArtefact(ctx, claim, fmt.Sprintf("Synchronizer configuration error: %v", err))
			return
		}
		// For other errors, submit an "ignore" bid as a safe default
		bidType = blackboard.BidTypeIgnore
	}

	log.Printf("[DEBUG] Submitting bid for claim_id=%s: agent=%s type=%s", claim.ID, e.config.AgentName, bidType)

	err = e.bbClient.SetBid(ctx, claim.ID, e.config.AgentName, bidType)
	if err != nil {
		log.Printf("[ERROR] Failed to submit bid for claim_id=%s: %v", claim.ID, err)

		// Check if this is a fatal configuration error (invalid bid type)
		// These indicate misconfiguration and will never succeed
		if strings.Contains(err.Error(), "invalid bid type") || strings.Contains(err.Error(), "unknown bid type") {
			log.Printf("[ERROR] FATAL: Bid submission failed due to configuration error - creating Failure artefact")
			e.createBiddingFailureArtefact(ctx, claim, fmt.Sprintf("Fatal bidding error (configuration issue): %v", err))
		}
		// For transient errors (Redis connection, etc.), just log and continue
		// The agent will get another chance on the next claim
		return
	}

	log.Printf("[INFO] Submitted %s bid for claim_id=%s", bidType, claim.ID)
}

// determineBidType determines the bid type for a claim using one of three methods (priority order):
// 1. M5.1: If synchronizer is configured, evaluate synchronization conditions
// 2. M3.6: If bid_script is configured, execute it with target artefact as JSON on stdin
// 3. M2.2: Fall back to static bidding_strategy from config
func (e *Engine) determineBidType(ctx context.Context, claim *blackboard.Claim, targetArtefact *blackboard.Artefact) (blackboard.BidType, error) {
	// M5.1: Check synchronizer first (highest priority)
	if e.synchronizer != nil {
		shouldBid, err := e.synchronizer.shouldBidOnClaim(ctx, claim)
		if err != nil {
			log.Printf("[ERROR] Synchronizer evaluation failed for claim %s: %v", claim.ID, err)
			// Don't fall through - synchronizer errors should not silently ignore
			return "", fmt.Errorf("synchronizer evaluation failed: %w", err)
		}
		if shouldBid {
			log.Printf("[Synchronizer] Conditions met, bidding 'exclusive' on claim %s", claim.ID)
			return blackboard.BidTypeExclusive, nil
		}
		log.Printf("[Synchronizer] Conditions not met, ignoring claim %s", claim.ID)
		return blackboard.BidTypeIgnore, nil
	}

	// M3.6: Check bid script second
	if len(e.config.BidScript) == 0 {
		// M4.8: Check target types filtering
		if len(e.config.BiddingStrategy.TargetTypes) > 0 {
			match := false
			for _, t := range e.config.BiddingStrategy.TargetTypes {
				if t == targetArtefact.Type {
					match = true
					break
				}
			}
			if !match {
				log.Printf("[DEBUG] Target artefact type '%s' not in target_types %v, ignoring",
					targetArtefact.Type, e.config.BiddingStrategy.TargetTypes)
				return blackboard.BidTypeIgnore, nil
			}
		}
		return e.config.BiddingStrategy.Type, nil
	}

	// A bid script is defined, execute it dynamically.
	log.Printf("[DEBUG] Executing bid script: %v", e.config.BidScript)

	// Prepare the command
	cmd := exec.CommandContext(ctx, e.config.BidScript[0], e.config.BidScript[1:]...)
	// Set working directory to /workspace if it exists (production), otherwise use current directory (tests)
	if _, err := os.Stat("/workspace"); err == nil {
		cmd.Dir = "/workspace"
	}

	// Prepare stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return e.handleBidScriptFailure("failed to create stdin pipe for bid script", err)
	}

	// Write artefact to stdin in a separate goroutine to avoid deadlocks
	go func() {
		defer stdin.Close()
		json.NewEncoder(stdin).Encode(targetArtefact)
	}()

	// Execute command and capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return e.handleBidScriptFailure("bid script execution failed",
			fmt.Errorf("%w\nOutput:\n%s", err, string(output)))
	}

	// Read bid type from stdout
	bidTypeStr := strings.TrimSpace(string(output))
	bidType := blackboard.BidType(bidTypeStr)

	// Validate the bid type returned by the script
	if err := bidType.Validate(); err != nil {
		return e.handleBidScriptFailure(
			fmt.Sprintf("bid script returned invalid bid type '%s'", bidTypeStr), err)
	}

	log.Printf("[DEBUG] Bid script returned: %s", bidType)
	return bidType, nil
}

// handleBidScriptFailure logs the error and returns fallback bidding strategy.
// M3.6: Implements graceful degradation when bid scripts fail.
func (e *Engine) handleBidScriptFailure(msg string, err error) (blackboard.BidType, error) {
	log.Printf("[ERROR] %s: %v", msg, err)

	// If we have a fallback strategy, use it
	if e.config.BiddingStrategy.Type != "" {
		log.Printf("[WARN] Falling back to static bidding_strategy: %s", e.config.BiddingStrategy.Type)
		return e.config.BiddingStrategy.Type, nil
	}

	// No fallback available, return ignore as safe default
	log.Printf("[WARN] No fallback bidding_strategy available, returning 'ignore'")
	return blackboard.BidTypeIgnore, nil
}

// GrantNotification represents the JSON structure of grant notifications.
type GrantNotification struct {
	EventType string `json:"event_type"`
	ClaimID   string `json:"claim_id"`
	ClaimType string `json:"claim_type,omitempty"` // M3.2: "review", "claim", or "exclusive"
}

// handleGrantNotification processes a grant notification from the orchestrator.
// Validates that the claim is actually granted to this agent, then pushes to work queue.
func (e *Engine) handleGrantNotification(ctx context.Context, msgPayload string, workQueue chan *blackboard.Claim) {
	// Parse grant notification JSON
	var grant GrantNotification
	if err := json.Unmarshal([]byte(msgPayload), &grant); err != nil {
		log.Printf("[WARN] Failed to parse grant notification: %v", err)
		return
	}

	if grant.EventType != "grant" {
		log.Printf("[WARN] Unexpected event_type in grant notification: %s", grant.EventType)
		return
	}

	log.Printf("[INFO] Received grant notification: claim_id=%s", grant.ClaimID)

	// Fetch full claim from blackboard
	claim, err := e.bbClient.GetClaim(ctx, grant.ClaimID)
	if err != nil {
		log.Printf("[ERROR] Failed to fetch claim %s: %v", grant.ClaimID, err)
		return
	}

	// Security check: Verify claim is actually granted to this agent
	// M3.2: Check review, parallel, and exclusive grant fields
	isGranted := false

	// Check review grants
	for _, grantedAgent := range claim.GrantedReviewAgents {
		if grantedAgent == e.config.AgentName {
			isGranted = true
			break
		}
	}

	// Check parallel grants
	if !isGranted {
		for _, grantedAgent := range claim.GrantedParallelAgents {
			if grantedAgent == e.config.AgentName {
				isGranted = true
				break
			}
		}
	}

	// Check exclusive grant
	if !isGranted && claim.GrantedExclusiveAgent == e.config.AgentName {
		isGranted = true
	}

	if !isGranted {
		log.Printf("[WARN] Grant notification for claim %s not granted to this agent (name: %s)",
			grant.ClaimID, e.config.AgentName)
		log.Printf("[DEBUG] Claim grants - review: %v, parallel: %v, exclusive: %s",
			claim.GrantedReviewAgents, claim.GrantedParallelAgents, claim.GrantedExclusiveAgent)
		return
	}

	log.Printf("[INFO] Grant validated for claim_id=%s, pushing to work queue", grant.ClaimID)

	// Push claim to work queue (buffered channel, may block briefly if queue full)
	select {
	case workQueue <- claim:
		log.Printf("[DEBUG] Claim %s successfully queued for execution", claim.ID)
	case <-ctx.Done():
		log.Printf("[DEBUG] Context cancelled while queuing claim %s", claim.ID)
		return
	}
}

// workExecutor receives granted claims from the work queue and executes them.
// M2.3: Executes agent tools via subprocess, creates result artefacts.
//
// The goroutine runs until:
//   - The context is cancelled (shutdown signal)
//   - The work queue channel is closed (no more work will arrive)
//
// Work execution never crashes - all errors create Failure artefacts and continue processing.
func (e *Engine) workExecutor(ctx context.Context, workQueue chan *blackboard.Claim) {
	defer e.wg.Done()
	defer log.Printf("[DEBUG] Work Executor exited cleanly")

	log.Printf("[DEBUG] Work Executor starting")

	for {
		select {
		case <-ctx.Done():
			// Context cancelled - shutdown requested
			log.Printf("[DEBUG] Work Executor received shutdown signal")
			return

		case claim, ok := <-workQueue:
			if !ok {
				// Work queue closed - no more work will arrive
				log.Printf("[DEBUG] Work queue closed, Work Executor shutting down")
				return
			}

			// Execute work for this claim
			// Note: executeWork handles all errors internally and never panics
			e.executeWork(ctx, claim)
		}
	}
}

// createBiddingFailureArtefact creates a Failure artefact for fatal bidding errors.
// These are configuration errors (invalid bid type, synchronizer misconfiguration) that
// prevent the agent from ever successfully bidding on claims.
//
// Unlike execution failures (which have stdout/stderr/exitCode), bidding failures only
// have a reason/error message since no tool was executed.
//
// M5.1: This ensures workflow failures are visible and don't leave claims stuck in limbo.
func (e *Engine) createBiddingFailureArtefact(ctx context.Context, claim *blackboard.Claim, reason string) {
	log.Printf("[INFO] Creating Failure artefact for bidding error: claim_id=%s reason=%s", claim.ID, reason)

	// Prepare failure data (no stdout/stderr/exitCode for bidding failures)
	failureData := &FailureData{
		Reason:   reason,
		ExitCode: -1, // -1 indicates non-execution failure
		Stdout:   "",
		Stderr:   "",
		Error:    reason,
	}

	payloadContent, err := MarshalFailurePayload(failureData)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal failure payload: %v", err)
		payloadContent = fmt.Sprintf(`{"reason": "Failed to marshal failure data: %v"}`, err)
	}

	// Create V2 VerifiableArtefact
	logicalThreadID := blackboard.NewID()

	v2Artefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{claim.ArtefactID},
			LogicalThreadID: logicalThreadID,
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  e.config.AgentName,
			StructuralType:  blackboard.StructuralTypeFailure,
			Type:            "BiddingConfigurationFailure",
			ClaimID:         claim.ID,
		},
		Payload: blackboard.ArtefactPayload{
			Content: payloadContent,
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(v2Artefact)
	if err != nil {
		log.Printf("[ERROR] Failed to compute hash for bidding Failure artefact: %v", err)
		return
	}
	v2Artefact.ID = hash

	// Write to blackboard
	if err := e.bbClient.WriteVerifiableArtefact(ctx, v2Artefact); err != nil {
		log.Printf("[ERROR] Failed to create bidding Failure artefact: %v", err)
		return
	}

	// Create V1 wrapper for event publishing/thread tracking
	artefact := &blackboard.Artefact{
		ID:              v2Artefact.ID,
		LogicalID:       v2Artefact.Header.LogicalThreadID,
		Version:         v2Artefact.Header.Version,
		StructuralType:  v2Artefact.Header.StructuralType,
		Type:            v2Artefact.Header.Type,
		Payload:         v2Artefact.Payload.Content,
		SourceArtefacts: v2Artefact.Header.ParentHashes,
		ProducedByRole:  v2Artefact.Header.ProducedByRole,
		CreatedAtMs:     v2Artefact.Header.CreatedAtMs,
		ClaimID:         v2Artefact.Header.ClaimID,
	}

	// Add to thread tracking
	if err := e.bbClient.AddVersionToThread(ctx, logicalThreadID, v2Artefact.ID, 1); err != nil {
		log.Printf("[WARN] Failed to add bidding Failure artefact to thread: %v", err)
	}

	// Publish event
	artefactJSON, _ := json.Marshal(artefact)
	channel := fmt.Sprintf("holt:%s:artefact_events", e.config.InstanceName)
	if err := e.bbClient.PublishRaw(ctx, channel, string(artefactJSON)); err != nil {
		log.Printf("[ERROR] Failed to publish bidding failure artefact event: %v", err)
		return
	}

	log.Printf("[INFO] Bidding Failure artefact created: artefact_id=%s type=%s", artefact.ID, artefact.Type)
}
