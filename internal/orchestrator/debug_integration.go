package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hearth-insights/holt/internal/orchestrator/debug"
	"github.com/hearth-insights/holt/pkg/blackboard"
)

// M4.2: Debug session management
type debugSession struct {
	sessionMgr      *debug.SessionManager
	protocolHandler *debug.ProtocolHandler
	currentSession  *debug.Session
	resumeChan      chan debug.ResumeSignal
	stepMode        bool
}

// M4.2: initializeDebugMonitoring starts the debug session monitoring goroutine
func (e *Engine) initializeDebugMonitoring(ctx context.Context) error {
	sessionMgr := debug.NewSessionManager(e.client, e.instanceName)
	protocolHandler := debug.NewProtocolHandler(e.client, e.instanceName)

	// Check for existing session on startup
	session, err := sessionMgr.GetActiveSession(ctx)
	if err != nil {
		log.Printf("[Orchestrator] Warning: Failed to check for debug session: %v", err)
		return nil // Non-fatal
	}

	debugSess := &debugSession{
		sessionMgr:      sessionMgr,
		protocolHandler: protocolHandler,
		currentSession:  session,
		resumeChan:      make(chan debug.ResumeSignal, 1),
		stepMode:        false,
	}

	// Start debug command processor goroutine
	go e.processDebugCommands(ctx, debugSess)

	// Start session expiration monitor
	go e.monitorSessionExpiration(ctx, debugSess)

	// Store debug session for use in main loop
	e.debugSession = debugSess

	if session != nil {
		log.Printf("[Orchestrator] Detected active debug session: %s", session.ID)
		e.logEvent("debug_session_detected", map[string]interface{}{
			"session_id":      session.ID,
			"connected_at_ms": session.ConnectedAtMs,
		})
	}

	return nil
}

// M4.2: processDebugCommands handles debug commands from CLI
func (e *Engine) processDebugCommands(ctx context.Context, debugSess *debugSession) {
	// Subscribe to debug commands
	pubsub, cmdChan, err := debugSess.protocolHandler.SubscribeToCommands(ctx)
	if err != nil {
		log.Printf("[Orchestrator] Failed to subscribe to debug commands: %v", err)
		return
	}
	defer pubsub.Close()

	log.Printf("[Orchestrator] Debug command processor started")

	for {
		select {
		case <-ctx.Done():
			return

		case cmd, ok := <-cmdChan:
			if !ok {
				return
			}

			// Special handling for set_breakpoints during session initialization
			if debugSess.currentSession == nil {
				// Check if this is for a valid session that's being initialized
				session, err := debugSess.sessionMgr.GetActiveSession(ctx)
				if err == nil && session != nil && cmd.SessionID == session.ID {
					// Session exists but not yet activated - activate it now
					debugSess.currentSession = session
					log.Printf("[Orchestrator] Debug session activated early due to command: %s", session.ID)
					e.logEvent("debug_session_activated", map[string]interface{}{
						"session_id":      session.ID,
						"connected_at_ms": session.ConnectedAtMs,
					})
				} else {
					log.Printf("[Orchestrator] Ignoring command for inactive session: %s", cmd.SessionID)
					continue
				}
			} else if cmd.SessionID != debugSess.currentSession.ID {
				log.Printf("[Orchestrator] Ignoring command for wrong session: %s (expected %s)", cmd.SessionID, debugSess.currentSession.ID)
				continue
			}

			if err := e.handleDebugCommand(ctx, debugSess, cmd); err != nil {
				log.Printf("[Orchestrator] Error handling debug command: %v", err)
			}
		}
	}
}

// M4.2: handleDebugCommand processes a single debug command
func (e *Engine) handleDebugCommand(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	switch debug.CommandType(cmd.CommandType) {
	case debug.CommandContinue:
		select {
		case debugSess.resumeChan <- debug.ResumeContinue:
		default:
			// Channel full or not waiting - ignore
		}
		return nil

	case debug.CommandStepNext:
		debugSess.stepMode = true
		select {
		case debugSess.resumeChan <- debug.ResumeStep:
		default:
		}
		return nil

	case debug.CommandSetBreakpoints:
		return e.handleSetBreakpoints(ctx, debugSess, cmd)

	case debug.CommandClearBreakpoint:
		return e.handleClearBreakpoint(ctx, debugSess, cmd)

	case debug.CommandClearAll:
		return debugSess.sessionMgr.ClearAllBreakpoints(ctx)

	case debug.CommandInspectArtefact:
		return e.handleInspectArtefact(ctx, debugSess, cmd)

	case debug.CommandManualReview:
		return e.handleManualReview(ctx, debugSess, cmd)

	case debug.CommandTerminateClaim:
		return e.handleTerminateClaim(ctx, debugSess, cmd)

	default:
		return fmt.Errorf("unknown command type: %s", cmd.CommandType)
	}
}

// M4.2: handleSetBreakpoints adds new breakpoints
func (e *Engine) handleSetBreakpoints(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	log.Printf("[Orchestrator] Handling set_breakpoints command for session %s", cmd.SessionID)

	// Expecting payload: {"breakpoints": [{"id": "bp-1", "condition_type": "...", "pattern": "..."}]}
	bpsInterface, ok := cmd.Payload["breakpoints"]
	if !ok {
		log.Printf("[Orchestrator] ERROR: missing breakpoints in payload")
		return fmt.Errorf("missing breakpoints in payload")
	}

	bpsList, ok := bpsInterface.([]interface{})
	if !ok {
		log.Printf("[Orchestrator] ERROR: breakpoints must be array")
		return fmt.Errorf("breakpoints must be array")
	}

	log.Printf("[Orchestrator] Setting %d breakpoints", len(bpsList))

	for _, bpInterface := range bpsList {
		bpMap, ok := bpInterface.(map[string]interface{})
		if !ok {
			continue
		}

		bp := &debug.Breakpoint{
			ID:            bpMap["id"].(string),
			ConditionType: bpMap["condition_type"].(string),
			Pattern:       bpMap["pattern"].(string),
		}

		log.Printf("[Orchestrator] Setting breakpoint %s: %s=%s", bp.ID, bp.ConditionType, bp.Pattern)

		// Validate breakpoint
		if err := debug.ValidateBreakpointConditionType(bp.ConditionType); err != nil {
			log.Printf("[Orchestrator] ERROR: Invalid condition type: %v", err)
			return err
		}
		if err := debug.ValidateBreakpointPattern(bp.Pattern); err != nil {
			log.Printf("[Orchestrator] ERROR: Invalid pattern: %v", err)
			return err
		}

		// Add breakpoint
		if err := debugSess.sessionMgr.AddBreakpoint(ctx, bp); err != nil {
			log.Printf("[Orchestrator] ERROR: Failed to add breakpoint: %v", err)
			return err
		}

		log.Printf("[Orchestrator] Breakpoint %s added successfully", bp.ID)

		// Publish breakpoint_set event
		debugSess.protocolHandler.PublishBreakpointSetEvent(ctx, cmd.SessionID, bp)

		// Log breakpoint creation
		e.logEvent("debug_breakpoint_set", map[string]interface{}{
			"session_id":     cmd.SessionID,
			"breakpoint_id":  bp.ID,
			"condition_type": bp.ConditionType,
			"pattern":        bp.Pattern,
		})
	}

	return nil
}

// M4.2: handleClearBreakpoint removes a breakpoint
func (e *Engine) handleClearBreakpoint(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	breakpointID, ok := cmd.Payload["breakpoint_id"].(string)
	if !ok {
		return fmt.Errorf("missing breakpoint_id in payload")
	}

	if err := debugSess.sessionMgr.RemoveBreakpoint(ctx, breakpointID); err != nil {
		return err
	}

	e.logEvent("debug_breakpoint_cleared", map[string]interface{}{
		"session_id":    cmd.SessionID,
		"breakpoint_id": breakpointID,
	})

	return nil
}

// M4.2: handleInspectArtefact returns artefact details
func (e *Engine) handleInspectArtefact(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	artefactID, ok := cmd.Payload["artefact_id"].(string)
	if !ok {
		return fmt.Errorf("missing artefact_id in payload")
	}

	artefact, err := e.client.GetArtefact(ctx, artefactID)
	if err != nil {
		return fmt.Errorf("failed to get artefact: %w", err)
	}

	// Publish artefact details as event
	payload := map[string]interface{}{
		"artefact": artefact,
	}

	return debugSess.protocolHandler.PublishEvent(ctx, "artefact_details", cmd.SessionID, payload)
}

// M4.2: handleManualReview processes manual review command
func (e *Engine) handleManualReview(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	// Manual review only allowed when paused on review claim
	pauseCtx, err := debugSess.sessionMgr.GetPauseContext(ctx)
	if err != nil {
		return err
	}

	if pauseCtx == nil {
		return fmt.Errorf("not paused on any claim")
	}

	claimID, ok := cmd.Payload["claim_id"].(string)
	if !ok {
		return fmt.Errorf("missing claim_id in payload")
	}

	if pauseCtx.ClaimID != claimID {
		return fmt.Errorf("claim %s is not the currently paused claim (paused on: %s)", claimID, pauseCtx.ClaimID)
	}

	// Get claim
	claim, err := e.client.GetClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("failed to get claim: %w", err)
	}

	if claim.Status != blackboard.ClaimStatusPendingReview {
		return fmt.Errorf("claim %s is not in pending_review status", claimID)
	}

	// Get feedback text
	feedback, ok := cmd.Payload["feedback"].(string)
	if !ok || feedback == "" {
		return fmt.Errorf("missing or empty feedback text")
	}

	// Create Review artefact with manual_debug metadata
	reviewPayload := map[string]string{
		"feedback":      feedback,
		"review_method": "manual_debug",
	}
	payloadJSON, _ := json.Marshal(reviewPayload)

	reviewArtefact := &blackboard.Artefact{
		ID: uuid.New().String(),
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeReview,
			Type:            "ManualReview",
			ProducedByRole:  "user",
			ParentHashes:    []string{claim.ArtefactID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: string(payloadJSON),
		},
	}

	if err := e.client.CreateArtefact(ctx, reviewArtefact); err != nil {
		return fmt.Errorf("failed to create review artefact: %w", err)
	}

	// Publish review_complete event
	debugSess.protocolHandler.PublishReviewCompleteEvent(ctx, cmd.SessionID, claimID, "rejected")

	log.Printf("[Orchestrator] Manual review submitted for claim %s: %s", claimID, feedback)

	// Structured log for manual review
	e.logEvent("debug_manual_review_submitted", map[string]interface{}{
		"session_id": cmd.SessionID,
		"claim_id":   claimID,
		"decision":   "reject",
		"feedback":   feedback,
		"level":      "warn", // Manual intervention is noteworthy
	})

	return nil
}

// M4.2: handleTerminateClaim manually terminates the currently paused claim
func (e *Engine) handleTerminateClaim(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	// Only allowed when paused on a claim
	pauseCtx, err := debugSess.sessionMgr.GetPauseContext(ctx)
	if err != nil {
		return err
	}

	if pauseCtx == nil {
		return fmt.Errorf("not paused on any claim")
	}

	if pauseCtx.ClaimID == "" {
		return fmt.Errorf("no claim in current pause context")
	}

	claimID := pauseCtx.ClaimID

	// Get claim
	claim, err := e.client.GetClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("failed to get claim: %w", err)
	}

	// Verify claim is not already terminated
	if claim.Status == blackboard.ClaimStatusTerminated || claim.Status == blackboard.ClaimStatusComplete {
		return fmt.Errorf("claim %s is already %s", claimID, claim.Status)
	}

	// Terminate the claim with detailed audit trail
	timestamp := time.Now().UTC().Format(time.RFC3339)
	claim.Status = blackboard.ClaimStatusTerminated
	claim.TerminationReason = fmt.Sprintf("MANUAL TERMINATION via debugger operator (session: %s) at %s. This claim was explicitly killed during interactive debugging.",
		cmd.SessionID, timestamp)

	if err := e.client.UpdateClaim(ctx, claim); err != nil {
		return fmt.Errorf("failed to terminate claim: %w", err)
	}

	// Clean up phase state if it exists
	delete(e.phaseStates, claimID)

	// Delete from pending assignment tracking if present
	delete(e.pendingAssignmentClaims, claimID)

	// Structured log with HIGH visibility
	log.Printf("[Orchestrator] ⚠️  MANUAL TERMINATION: Claim %s terminated by debugger operator (session: %s)",
		claimID, cmd.SessionID)

	e.logEvent("debug_claim_terminated", map[string]interface{}{
		"session_id":          cmd.SessionID,
		"claim_id":            claimID,
		"artefact_id":         claim.ArtefactID,
		"paused_event_type":   pauseCtx.EventType,
		"termination_reason":  claim.TerminationReason,
		"level":               "error", // Use error level for high visibility
		"manual_intervention": true,
	})

	// Resume the orchestrator - work is done on this claim
	select {
	case debugSess.resumeChan <- debug.ResumeContinue:
		log.Printf("[Orchestrator] Resuming after claim termination")
	default:
		// Not waiting for resume - that's ok
	}

	return nil
}

// M4.2: monitorSessionExpiration checks for session expiration
func (e *Engine) monitorSessionExpiration(ctx context.Context, debugSess *debugSession) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if debugSess.currentSession == nil {
				// Check if new session started
				session, err := debugSess.sessionMgr.GetActiveSession(ctx)
				if err != nil {
					continue
				}
				if session != nil {
					debugSess.currentSession = session
					log.Printf("[Orchestrator] Debug session activated: %s", session.ID)
					e.logEvent("debug_session_activated", map[string]interface{}{
						"session_id":      session.ID,
						"connected_at_ms": session.ConnectedAtMs,
					})
				}
				continue
			}

			// Check if current session expired
			session, err := debugSess.sessionMgr.GetActiveSession(ctx)
			if err != nil {
				continue
			}

			if session == nil {
				// Session expired
				sessionID := debugSess.currentSession.ID
				wasPaused := debugSess.currentSession.IsPaused
				log.Printf("[Orchestrator] Debug session expired: %s", sessionID)

				// Clear breakpoints
				debugSess.sessionMgr.ClearAllBreakpoints(ctx)

				// If paused, resume
				if wasPaused {
					select {
					case debugSess.resumeChan <- debug.ResumeContinue:
					default:
					}
				}

				// Publish expiration event
				debugSess.protocolHandler.PublishSessionExpiredEvent(ctx, sessionID)

				// Structured log for session expiration
				e.logEvent("debug_session_expired", map[string]interface{}{
					"session_id":  sessionID,
					"was_paused":  wasPaused,
					"auto_resume": true,
					"level":       "warn",
				})

				debugSess.currentSession = nil
				debugSess.stepMode = false
			}
		}
	}
}

// M4.2: evaluateBreakpointsAndPause checks breakpoints and pauses if matched
func (e *Engine) evaluateBreakpointsAndPause(
	ctx context.Context,
	artefact *blackboard.Artefact,
	claim *blackboard.Claim,
	eventType debug.EventType,
) {
	if e.debugSession == nil || e.debugSession.currentSession == nil {
		// Only log on the first few events to avoid spam
		if eventType == debug.EventReviewConsensusReached {
			log.Printf("[Orchestrator] Skipping breakpoint evaluation: debugSession=%v, currentSession=%v, eventType=%s",
				e.debugSession != nil, e.debugSession != nil && e.debugSession.currentSession != nil, eventType)
		}
		return
	}

	// Get breakpoints
	breakpoints, err := e.debugSession.sessionMgr.GetBreakpoints(ctx)
	if err != nil {
		log.Printf("[Orchestrator] Failed to get breakpoints: %v", err)
		return
	}

	log.Printf("[Orchestrator] Evaluating %d breakpoints for event %s", len(breakpoints), eventType)

	if len(breakpoints) == 0 {
		return
	}

	// Evaluate breakpoints
	matchedBp := debug.EvaluateBreakpoints(ctx, breakpoints, artefact, claim, eventType)
	if matchedBp == nil {
		log.Printf("[Orchestrator] No breakpoints matched for event %s", eventType)
		// Check step mode
		if e.debugSession.stepMode {
			e.debugSession.stepMode = false
			e.pauseForDebug(ctx, artefact, claim, eventType, nil)
		}
		return
	}

	log.Printf("[Orchestrator] Breakpoint %s matched! Pausing execution", matchedBp.ID)

	// Breakpoint matched - pause
	e.pauseForDebug(ctx, artefact, claim, eventType, matchedBp)
}

// M4.2: pauseForDebug pauses the orchestrator and waits for resume signal
func (e *Engine) pauseForDebug(
	ctx context.Context,
	artefact *blackboard.Artefact,
	claim *blackboard.Claim,
	eventType debug.EventType,
	matchedBp *debug.Breakpoint,
) {
	artefactID := ""
	if artefact != nil {
		artefactID = artefact.ID
	}

	claimID := ""
	if claim != nil {
		claimID = claim.ID
	}

	breakpointID := ""
	if matchedBp != nil {
		breakpointID = matchedBp.ID
	}

	pauseCtx := &debug.PauseContext{
		ArtefactID:   artefactID,
		ClaimID:      claimID,
		BreakpointID: breakpointID,
		EventType:    string(eventType),
		PausedAtMs:   time.Now().UnixMilli(),
	}

	// Update session
	e.debugSession.sessionMgr.SetPauseContext(ctx, pauseCtx)

	// Publish paused event
	e.debugSession.protocolHandler.PublishPausedEvent(ctx, e.debugSession.currentSession.ID, pauseCtx, matchedBp)

	log.Printf("[Orchestrator] Paused on breakpoint: event=%s, artefact=%s, claim=%s, bp=%s",
		eventType, artefactID, claimID, breakpointID)

	// Structured log for pause
	e.logEvent("debug_paused_on_breakpoint", map[string]interface{}{
		"session_id":    e.debugSession.currentSession.ID,
		"event_type":    string(eventType),
		"artefact_id":   artefactID,
		"claim_id":      claimID,
		"breakpoint_id": breakpointID,
		"paused_at_ms":  pauseCtx.PausedAtMs,
		"level":         "warn",
	})

	// Wait for resume signal or session expiration
	select {
	case <-ctx.Done():
		return
	case signal := <-e.debugSession.resumeChan:
		// Clear pause context
		e.debugSession.sessionMgr.ClearPauseContext(ctx)
		log.Printf("[Orchestrator] Resumed from debug pause")

		// Structured log for resume
		resumeType := "continue"
		if signal == debug.ResumeStep {
			resumeType = "step"
		}
		e.logEvent("debug_resumed", map[string]interface{}{
			"session_id":         e.debugSession.currentSession.ID,
			"resume_type":        resumeType,
			"paused_duration_ms": time.Now().UnixMilli() - pauseCtx.PausedAtMs,
		})
	}
}
