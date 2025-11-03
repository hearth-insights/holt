package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/dyluth/holt/internal/orchestrator/debug"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/google/uuid"
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

			// Verify command is for current session
			if debugSess.currentSession == nil || cmd.SessionID != debugSess.currentSession.ID {
				log.Printf("[Orchestrator] Ignoring command for inactive session: %s", cmd.SessionID)
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

	default:
		return fmt.Errorf("unknown command type: %s", cmd.CommandType)
	}
}

// M4.2: handleSetBreakpoints adds new breakpoints
func (e *Engine) handleSetBreakpoints(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	// Expecting payload: {"breakpoints": [{"id": "bp-1", "condition_type": "...", "pattern": "..."}]}
	bpsInterface, ok := cmd.Payload["breakpoints"]
	if !ok {
		return fmt.Errorf("missing breakpoints in payload")
	}

	bpsList, ok := bpsInterface.([]interface{})
	if !ok {
		return fmt.Errorf("breakpoints must be array")
	}

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

		// Validate breakpoint
		if err := debug.ValidateBreakpointConditionType(bp.ConditionType); err != nil {
			return err
		}
		if err := debug.ValidateBreakpointPattern(bp.Pattern); err != nil {
			return err
		}

		// Add breakpoint
		if err := debugSess.sessionMgr.AddBreakpoint(ctx, bp); err != nil {
			return err
		}

		// Publish breakpoint_set event
		debugSess.protocolHandler.PublishBreakpointSetEvent(ctx, cmd.SessionID, bp)
	}

	return nil
}

// M4.2: handleClearBreakpoint removes a breakpoint
func (e *Engine) handleClearBreakpoint(ctx context.Context, debugSess *debugSession, cmd *debug.Command) error {
	breakpointID, ok := cmd.Payload["breakpoint_id"].(string)
	if !ok {
		return fmt.Errorf("missing breakpoint_id in payload")
	}

	return debugSess.sessionMgr.RemoveBreakpoint(ctx, breakpointID)
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
		ID:              uuid.New().String(),
		LogicalID:       uuid.New().String(),
		Version:         1,
		StructuralType:  blackboard.StructuralTypeReview,
		Type:            "ManualReview",
		Payload:         string(payloadJSON),
		SourceArtefacts: []string{claim.ArtefactID},
		ProducedByRole:  "user",
		CreatedAtMs:     time.Now().UnixMilli(),
	}

	if err := e.client.CreateArtefact(ctx, reviewArtefact); err != nil {
		return fmt.Errorf("failed to create review artefact: %w", err)
	}

	// Publish review_complete event
	debugSess.protocolHandler.PublishReviewCompleteEvent(ctx, cmd.SessionID, claimID, "rejected")

	log.Printf("[Orchestrator] Manual review submitted for claim %s: %s", claimID, feedback)

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
				log.Printf("[Orchestrator] Debug session expired: %s", debugSess.currentSession.ID)

				// Clear breakpoints
				debugSess.sessionMgr.ClearAllBreakpoints(ctx)

				// If paused, resume
				if debugSess.currentSession.IsPaused {
					select {
					case debugSess.resumeChan <- debug.ResumeContinue:
					default:
					}
				}

				// Publish expiration event
				debugSess.protocolHandler.PublishSessionExpiredEvent(ctx, debugSess.currentSession.ID)

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
		return
	}

	// Get breakpoints
	breakpoints, err := e.debugSession.sessionMgr.GetBreakpoints(ctx)
	if err != nil {
		log.Printf("[Orchestrator] Failed to get breakpoints: %v", err)
		return
	}

	if len(breakpoints) == 0 {
		return
	}

	// Evaluate breakpoints
	matchedBp := debug.EvaluateBreakpoints(ctx, breakpoints, artefact, claim, eventType)
	if matchedBp == nil {
		// Check step mode
		if e.debugSession.stepMode {
			e.debugSession.stepMode = false
			e.pauseForDebug(ctx, artefact, claim, eventType, nil)
		}
		return
	}

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

	// Wait for resume signal or session expiration
	select {
	case <-ctx.Done():
		return
	case <-e.debugSession.resumeChan:
		// Clear pause context
		e.debugSession.sessionMgr.ClearPauseContext(ctx)
		log.Printf("[Orchestrator] Resumed from debug pause")
	}
}
