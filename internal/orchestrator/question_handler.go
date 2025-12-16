package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hearth-insights/holt/pkg/blackboard"
)

// QuestionPayload represents the JSON structure stored in Question artefact payloads.
// M4.1: This schema is defined in the design document section 2.1.
type QuestionPayload struct {
	QuestionText     string `json:"question_text"`
	TargetArtefactID string `json:"target_artefact_id"`
	Routing          string `json:"routing,omitempty"` // "auto" (default) or "human"
}

// handleQuestionArtefact processes a Question artefact by terminating the original claim
// and creating a feedback claim for the author of the questioned artefact.
// M4.1: Reuses M3.3 feedback loop machinery.
func (e *Engine) handleQuestionArtefact(ctx context.Context, questionArtefact *blackboard.Artefact) error {
	// Parse Question payload
	var payload QuestionPayload
	if err := json.Unmarshal([]byte(questionArtefact.Payload.Content), &payload); err != nil {
		log.Printf("[Orchestrator] Failed to parse Question payload (artefact: %s): %v", questionArtefact.ID, err)
		e.logEvent("question_parse_error", map[string]interface{}{
			"question_id": questionArtefact.ID,
			"error":       err.Error(),
		})
		// Don't crash - just skip this Question
		return nil
	}

	// Validate required fields
	if payload.TargetArtefactID == "" {
		log.Printf("[Orchestrator] Question payload missing target_artefact_id (artefact: %s)", questionArtefact.ID)
		e.logEvent("question_validation_error", map[string]interface{}{
			"question_id": questionArtefact.ID,
			"error":       "missing target_artefact_id",
		})
		return nil
	}

	// Load the target artefact to determine its producer role
	targetArtefact, err := e.client.GetArtefact(ctx, payload.TargetArtefactID)
	if err != nil {
		log.Printf("[Orchestrator] Failed to load target artefact %s for Question %s: %v",
			payload.TargetArtefactID, questionArtefact.ID, err)

		// Create Failure artefact for missing target
		return e.createTargetNotFoundFailure(ctx, questionArtefact, payload.TargetArtefactID)
	}

	// Find the claim that produced the Question artefact
	originalClaim, err := e.findClaimByProducedArtefact(ctx, questionArtefact.ID)
	if err != nil {
		log.Printf("[Orchestrator] Failed to find original claim for Question %s: %v", questionArtefact.ID, err)
		return fmt.Errorf("failed to find original claim: %w", err)
	}

	if originalClaim == nil {
		log.Printf("[Orchestrator] Warning: No claim found for Question artefact %s", questionArtefact.ID)
		return nil
	}

	// Check iteration limit using target artefact version
	iterationCount := targetArtefact.Header.Version - 1
	maxIterations := *e.config.Orchestrator.MaxReviewIterations

	if maxIterations > 0 && iterationCount >= maxIterations {
		// Create Failure artefact and terminate
		return e.terminateQuestionIterationLimit(ctx, originalClaim, targetArtefact, questionArtefact, iterationCount)
	}

	// Don't terminate the claim here - the Terminal artefact (ClaimComplete) will handle it
	// The orchestrator's handleTerminalArtefact() will detect the Question sibling and terminate
	log.Printf("[Orchestrator] Question artefact %s produced for claim %s (will be terminated via Terminal artefact)",
		questionArtefact.ID, originalClaim.ID)

	// M4.11: Production Gatekeeper - Direct to Human routing
	// If the agent explicitly requests human routing, we bypass the producer feedback loop.
	if payload.Routing == "human" {
		log.Printf("[Orchestrator] Question %s requested direct-to-human routing", questionArtefact.ID)
		if err := e.publishHumanInputRequiredEvent(ctx, questionArtefact.ID, payload.QuestionText, targetArtefact.ID); err != nil {
			log.Printf("[Orchestrator] Failed to publish human_input_required event: %v", err)
			return fmt.Errorf("failed to publish human_input_required event: %w", err)
		}

		// Terminate the original claim as the question interrupts the flow
		originalClaim.Status = blackboard.ClaimStatusTerminated
		originalClaim.TerminationReason = fmt.Sprintf("Agent asked a clarifying question (Question artefact: %s) with direct-to-human routing", questionArtefact.ID)
		if err := e.client.UpdateClaim(ctx, originalClaim); err != nil {
			return fmt.Errorf("failed to terminate original claim (human routing): %w", err)
		}

		// Bypass feedback claim creation
		return nil
	}

	// M4.1: Special handling for questions targeting "user" (legacy implicit routing)
	if targetArtefact.Header.ProducedByRole == "user" {
		if err := e.publishHumanInputRequiredEvent(ctx, questionArtefact.ID, payload.QuestionText, targetArtefact.ID); err != nil {
			log.Printf("[Orchestrator] Failed to publish human_input_required event: %v", err)
		}

		// Terminate the original claim as the question interrupts the flow
		originalClaim.Status = blackboard.ClaimStatusTerminated
		originalClaim.TerminationReason = fmt.Sprintf("Agent asked a clarifying question (Question artefact: %s)", questionArtefact.ID)
		if err := e.client.UpdateClaim(ctx, originalClaim); err != nil {
			return fmt.Errorf("failed to terminate original claim (user target): %w", err)
		}

		// For user targets, we don't create a feedback claim or look up an agent.
		// The event signals external intervention is needed.
		return nil
	}

	// Find the agent that produced the target artefact
	producerAgent, err := e.findAgentByRole(targetArtefact.Header.ProducedByRole)
	if err != nil {
		// Agent no longer exists in config
		return e.terminateMissingAgent(ctx, originalClaim, targetArtefact)
	}

	// Create feedback claim assigned to the original author
	feedbackClaim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            targetArtefact.ID, // Target is the questioned artefact
		Status:                blackboard.ClaimStatusPendingAssignment,
		GrantedExclusiveAgent: producerAgent,
		AdditionalContextIDs:  []string{questionArtefact.ID}, // Inject Question into context
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
	}

	if err := e.client.CreateClaim(ctx, feedbackClaim); err != nil {
		return fmt.Errorf("failed to create feedback claim: %w", err)
	}

	e.logEvent("feedback_claim_created", map[string]interface{}{
		"feedback_claim_id": feedbackClaim.ID,
		"original_claim_id": originalClaim.ID,
		"target_artefact":   targetArtefact.ID,
		"assigned_agent":    producerAgent,
		"question_id":       questionArtefact.ID,
		"iteration":         iterationCount + 1,
	})

	log.Printf("[Orchestrator] Created feedback claim %s for agent %s (question: %s, iteration %d)",
		feedbackClaim.ID, producerAgent, questionArtefact.ID, iterationCount+1)

	// Track feedback claim for completion checking
	e.pendingAssignmentClaims[feedbackClaim.ID] = targetArtefact.ID

	// Terminate the original claim
	originalClaim.Status = blackboard.ClaimStatusTerminated
	originalClaim.TerminationReason = fmt.Sprintf("Agent asked a clarifying question (Question artefact: %s)", questionArtefact.ID)
	if err := e.client.UpdateClaim(ctx, originalClaim); err != nil {
		return fmt.Errorf("failed to terminate original claim: %w", err)
	}

	return nil
}

// findClaimByProducedArtefact finds the claim that resulted in the production of a specific artefact.
// This is used to determine which claim to terminate when a Question is produced.
func (e *Engine) findClaimByProducedArtefact(ctx context.Context, artefactID string) (*blackboard.Claim, error) {
	// Load the artefact to get its source artefacts
	artefact, err := e.client.GetArtefact(ctx, artefactID)
	if err != nil {
		return nil, fmt.Errorf("failed to load artefact: %w", err)
	}

	// The artefact's source should contain the target artefact of the claim
	// Find a claim whose ArtefactID matches any of the source artefacts
	for _, sourceID := range artefact.Header.ParentHashes {
		claim, err := e.client.GetClaimByArtefactID(ctx, sourceID)
		if err != nil {
			if blackboard.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed to get claim for source %s: %w", sourceID, err)
		}

		// Return the first matching claim (should only be one in typical workflow)
		if claim != nil {
			return claim, nil
		}
	}

	return nil, nil
}

// terminateQuestionIterationLimit creates a Failure artefact when iteration limit is exceeded for Questions.
// M4.1: Similar to M3.3 review iteration limit logic.
func (e *Engine) terminateQuestionIterationLimit(ctx context.Context, claim *blackboard.Claim, artefact *blackboard.Artefact, questionArtefact *blackboard.Artefact, iterations int) error {
	maxIterations := *e.config.Orchestrator.MaxReviewIterations

	// Parse question text for better error message
	var payload QuestionPayload
	_ = json.Unmarshal([]byte(questionArtefact.Payload.Content), &payload)

	failurePayload := fmt.Sprintf("Max review iterations (%d) exceeded for artefact %s (version %d). Latest question: %s",
		maxIterations, artefact.ID, artefact.Header.Version, payload.QuestionText)

	failure := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeFailure,
			Type:            "MaxIterationsExceeded",
			ProducedByRole:  "orchestrator",
			ParentHashes:    []string{artefact.ID, questionArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: failurePayload,
		},
	}
	failureHash, err := blackboard.ComputeArtefactHash(failure)
	if err != nil {
		return fmt.Errorf("failed to compute hash for Failure artefact: %w", err)
	}
	failure.ID = failureHash

	if err := e.client.CreateArtefact(ctx, failure); err != nil {
		return fmt.Errorf("failed to create Failure artefact: %w", err)
	}

	claim.Status = blackboard.ClaimStatusTerminated
	claim.TerminationReason = fmt.Sprintf("Terminated after reaching max review iterations (%d) via questions.", maxIterations)

	e.logEvent("claim_terminated_max_iterations", map[string]interface{}{
		"claim_id":    claim.ID,
		"artefact_id": artefact.ID,
		"iterations":  iterations + 1,
		"failure_id":  failure.ID,
		"question_id": questionArtefact.ID,
	})

	log.Printf("[Orchestrator] Claim %s terminated: max iterations (%d) reached via question loop",
		claim.ID, maxIterations)

	return e.client.UpdateClaim(ctx, claim)
}

// createTargetNotFoundFailure creates a Failure artefact when Question references a non-existent target.
// M4.1: Edge case handling for orphaned Questions.
func (e *Engine) createTargetNotFoundFailure(ctx context.Context, questionArtefact *blackboard.Artefact, targetArtefactID string) error {
	failurePayload := fmt.Sprintf("Cannot process Question: target artefact '%s' not found in Redis.", targetArtefactID)

	failure := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(), // Use NewID for thread
			Version:         1,
			StructuralType:  blackboard.StructuralTypeFailure,
			Type:            "TargetArtefactNotFound",
			ProducedByRole:  "orchestrator",
			ParentHashes:    []string{questionArtefact.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: failurePayload,
		},
	}
	failureHash, err := blackboard.ComputeArtefactHash(failure)
	if err != nil {
		return fmt.Errorf("failed to compute hash for Failure artefact: %w", err)
	}
	failure.ID = failureHash

	if err := e.client.CreateArtefact(ctx, failure); err != nil {
		return fmt.Errorf("failed to create Failure artefact: %w", err)
	}

	e.logEvent("question_target_not_found", map[string]interface{}{
		"question_id":        questionArtefact.ID,
		"target_artefact_id": targetArtefactID,
		"failure_id":         failure.ID,
	})

	log.Printf("[Orchestrator] Question %s references missing target artefact %s, created Failure %s",
		questionArtefact.ID, targetArtefactID, failure.ID)

	return nil
}

// publishHumanInputRequiredEvent publishes a workflow event when a Question targets the "user" role.
// M4.1: Makes questions visible in `holt watch` output.
func (e *Engine) publishHumanInputRequiredEvent(ctx context.Context, questionID, questionText, targetArtefactID string) error {
	eventData := map[string]interface{}{
		"question_id":        questionID,
		"question_text":      questionText,
		"target_artefact_id": targetArtefactID,
	}

	if err := e.client.PublishWorkflowEvent(ctx, "human_input_required", eventData); err != nil {
		return fmt.Errorf("failed to publish human_input_required event: %w", err)
	}

	log.Printf("[Orchestrator] Published human_input_required event: question_id=%s", questionID)

	return nil
}
