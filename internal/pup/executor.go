package pup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

const (
	// toolExecutionTimeout is the maximum time a tool can run before being killed
	toolExecutionTimeout = 5 * time.Minute

	// maxOutputSize is the maximum number of bytes to read from tool stdout/stderr (10MB)
	maxOutputSize = 10 * 1024 * 1024
)

// executeWork handles the complete workflow of executing an agent tool for a granted claim.
// This is the main entry point called by the work executor goroutine.
//
// Workflow:
//  1. Fetch target artefact from blackboard
//  2. Prepare tool input JSON (stdin)
//  3. Execute tool subprocess with timeout
//  4. Parse tool output JSON (stdout)
//  5. Create result artefact with derivative provenance
//  6. Publish artefact to blackboard
//
// On any failure, creates a Failure artefact and continues (never crashes).
func (e *Engine) executeWork(ctx context.Context, claim *blackboard.Claim) {
	log.Printf("[INFO] Work Executor received claim from queue: claim_id=%s artefact_id=%s",
		claim.ID, claim.ArtefactID)

	// Fetch target artefact
	targetArtefact, err := e.fetchTargetArtefact(ctx, claim)
	if err != nil {
		log.Printf("[ERROR] Failed to fetch target artefact: %v", err)
		e.createFailureArtefact(ctx, claim, -1, "", "", fmt.Sprintf("Failed to fetch target artefact: %v", err))
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	log.Printf("[INFO] Fetched target artefact: artefact_id=%s type=%s",
		targetArtefact.ID, targetArtefact.Type)

	// Prepare tool input with context assembly
	inputJSON, err := e.prepareToolInput(ctx, claim, targetArtefact)
	if err != nil {
		log.Printf("[ERROR] Failed to prepare tool input: %v", err)
		e.createFailureArtefact(ctx, claim, -1, "", "", fmt.Sprintf("Failed to prepare tool input: %v", err))
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	// Execute tool subprocess
	log.Printf("[INFO] Executing tool: command=%v claim_id=%s", e.config.Command, claim.ID)
	startTime := time.Now()

	exitCode, stdout, stderr, err := e.executeToolSubprocess(ctx, inputJSON)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("[ERROR] Tool execution failed: claim_id=%s exit_code=%d duration=%s error=%v",
			claim.ID, exitCode, duration, err)
		e.createFailureArtefact(ctx, claim, exitCode, stdout, stderr, err.Error())
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	log.Printf("[INFO] Tool execution completed: claim_id=%s exit_code=%d duration=%s",
		claim.ID, exitCode, duration)

	// M5.1: Parse all tool outputs from FD 3 (supports multi-artefact)
	outputs, err := e.parseToolOutputs(stdout)
	if err != nil {
		log.Printf("[ERROR] Failed to parse FD 3 output: claim_id=%s error=%v fd3_result=%s",
			claim.ID, err, truncate(stdout, 200))
		e.createFailureArtefact(ctx, claim, exitCode, stdout, stderr,
			fmt.Sprintf("Failed to parse FD 3 output: %v", err))
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	// Validate artefact outputs (type consistency rules)
	if err := e.validateArtefactOutputs(outputs); err != nil {
		log.Printf("[ERROR] Invalid artefact outputs: claim_id=%s error=%v", claim.ID, err)
		e.createFailureArtefact(ctx, claim, 0, stdout, stderr, err.Error())
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	// M5.1: Create all result artefacts with batch metadata
	artefacts, err := e.createVerifiableResultArtefacts(ctx, claim, outputs, targetArtefact)
	if err != nil {
		log.Printf("[ERROR] Failed to create artefacts: claim_id=%s error=%v", claim.ID, err)
		// Try to create a Failure artefact describing the artefact creation failure
		e.createFailureArtefact(ctx, claim, 0, stdout, stderr,
			fmt.Sprintf("Tool succeeded but artefact creation failed: %v", err))
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	// Log all created artefacts
	for i, artefact := range artefacts {
		log.Printf("[INFO] Created artefact %d/%d: artefact_id=%s type=%s logical_id=%s version=%d",
			i+1, len(artefacts), artefact.ID, artefact.Header.Type, artefact.Header.LogicalThreadID, artefact.Header.Version)
	}

	// Confirm all artefacts are in Redis before creating Terminal artefact
	// This prevents race conditions where Terminal is processed before Question/Failure
	if err := e.confirmArtefactsInRedis(ctx, artefacts); err != nil {
		log.Printf("[ERROR] Failed to confirm artefacts in Redis: %v", err)
		// Continue anyway - Terminal will still be created
	}

	// Create Terminal artefact to signal claim completion
	// BUT: Only for final phase or single-phase execution
	// In multi-phase workflows (review → parallel → exclusive), only the exclusive phase should create Terminal
	if e.shouldCreateTerminalForClaim(claim) {
		e.createTerminalArtefact(ctx, claim, len(artefacts))
	} else {
		log.Printf("[INFO] Skipping Terminal artefact creation for claim %s (status=%s, not final phase)",
			claim.ID, claim.Status)
	}
}

// fetchTargetArtefact retrieves the artefact that the claim is for.
// Returns an error if the artefact doesn't exist (defensive check for orchestrator bugs).
func (e *Engine) fetchTargetArtefact(ctx context.Context, claim *blackboard.Claim) (*blackboard.Artefact, error) {
	artefact, err := e.bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artefact %s: %w", claim.ArtefactID, err)
	}

	if artefact == nil {
		return nil, fmt.Errorf("artefact %s not found", claim.ArtefactID)
	}

	return artefact, nil
}

// prepareToolInput creates the JSON structure to pass to the tool via stdin.
// M2.4: Uses context assembly to populate context_chain with historical artefacts.
// M3.3: Passes claim to assembleContext for AdditionalContextIDs support.
// M4.3: Loads Knowledge artefacts and populates context caching fields.
func (e *Engine) prepareToolInput(ctx context.Context, claim *blackboard.Claim, targetArtefact *blackboard.Artefact) (string, error) {
	// Assemble context chain via BFS traversal with thread tracking
	// M3.3: Pass claim for feedback claim context support
	contextChain, err := e.assembleContext(ctx, targetArtefact, claim)
	if err != nil {
		return "", fmt.Errorf("failed to assemble context: %w", err)
	}

	log.Printf("[DEBUG] Prepared context chain: %d artefacts", len(contextChain))

	// M4.3: Load Knowledge artefacts for this agent
	contextIsDeclared, knowledgeBase, loadedKnowledge, err := e.loadKnowledgeForAgent(ctx, contextChain)
	if err != nil {
		return "", fmt.Errorf("failed to load knowledge: %w", err)
	}

	// Convert to []interface{} for JSON marshaling
	contextChainInterface := make([]interface{}, len(contextChain))
	for i, art := range contextChain {
		contextChainInterface[i] = art
	}

	input := &ToolInput{
		ClaimType:         "exclusive", // M2.4: still hardcoded (Phase 3 will support review/parallel)
		TargetArtefact:    targetArtefact,
		ContextChain:      contextChainInterface,
		ContextIsDeclared: contextIsDeclared,
		KnowledgeBase:     knowledgeBase,
		LoadedKnowledge:   loadedKnowledge,
	}

	jsonBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool input: %w", err)
	}

	return string(jsonBytes), nil
}

// executeToolSubprocess runs the agent command as a subprocess with timeout and output limits.
// M4.10: FD 3 Return Model - stdout/stderr stream to container logs, result JSON via FD 3.
//
// The subprocess is:
//   - Given a 5-minute timeout via context
//   - Run in /workspace directory
//   - Fed input JSON via stdin (pipe closed after write)
//   - stdout/stderr pass through to container logs (for `docker logs`)
//   - Result JSON read from FD 3 (10MB limit)
//
// Returns (exitCode, fd3Result, "", error) where:
//   - exitCode is the process exit code (0 = success, non-zero = failure, -1 = couldn't start)
//   - fd3Result is the JSON result from FD 3
//   - third parameter is always "" (stderr is now streamed, not returned)
//   - error is non-nil if the process failed, timed out, or FD 3 validation failed
func (e *Engine) executeToolSubprocess(ctx context.Context, inputJSON string) (int, string, string, error) {
	// Validate /workspace directory exists (fail-fast check)
	if _, err := os.Stat("/workspace"); os.IsNotExist(err) {
		return -1, "", "", fmt.Errorf("/workspace directory does not exist - agent container must mount workspace")
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, toolExecutionTimeout)
	defer cancel()

	// Create command
	if len(e.config.Command) == 0 {
		return -1, "", "", fmt.Errorf("command array is empty")
	}

	var cmd *exec.Cmd
	if len(e.config.Command) == 1 {
		cmd = exec.CommandContext(execCtx, e.config.Command[0])
	} else {
		cmd = exec.CommandContext(execCtx, e.config.Command[0], e.config.Command[1:]...)
	}

	// Set working directory
	cmd.Dir = "/workspace"

	// M4.10: Create pipe for FD 3 (result JSON)
	fd3Reader, fd3Writer, err := os.Pipe()
	if err != nil {
		return -1, "", "", fmt.Errorf("failed to create FD 3 pipe: %w", err)
	}
	defer fd3Reader.Close()

	// Create stdin pipe
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		fd3Writer.Close()
		return -1, "", "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// M4.10: STREAM stdout/stderr to container (for docker logs)
	// This is a major change - we no longer buffer these!
	cmd.Stdout = os.Stdout // Pass through to container stdout
	cmd.Stderr = os.Stderr // Pass through to container stderr

	// M4.10: Attach FD 3 for result JSON
	cmd.ExtraFiles = []*os.File{fd3Writer} // FD 3
	log.Printf("[DEBUG] M4.10: Attached FD 3 for result JSON")

	// Start process
	if err := cmd.Start(); err != nil {
		fd3Writer.Close()
		return -1, "", "", fmt.Errorf("failed to start process: %w", err)
	}

	// Write input JSON to stdin and close
	go func() {
		defer stdinPipe.Close()
		if _, err := io.WriteString(stdinPipe, inputJSON); err != nil {
			log.Printf("[WARN] Failed to write to stdin: %v", err)
		}
	}()

	// M4.10: Read result JSON from FD 3
	// Close write end so we get EOF when agent closes it
	fd3Writer.Close()

	resultBuf := &bytes.Buffer{}
	resultReader := &limitedReader{r: fd3Reader, limit: maxOutputSize}

	// Read FD 3 in background (agent may write incrementally)
	resultDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(resultBuf, resultReader)
		resultDone <- err
	}()

	// Wait for process to complete
	processErr := cmd.Wait()

	// Wait for FD 3 read to complete
	if readErr := <-resultDone; readErr != nil {
		log.Printf("[WARN] Error reading FD 3: %v", readErr)
	}

	resultJSON := resultBuf.String()

	// Get exit code
	exitCode := 0
	if processErr != nil {
		if exitErr, ok := processErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Process couldn't be started or context timeout
			if execCtx.Err() == context.DeadlineExceeded {
				return -1, "", "", fmt.Errorf("tool execution timeout (5 minutes)")
			}
			return -1, "", "", processErr
		}
	}

	// M4.10: Validate FD 3 output
	if len(resultJSON) == 0 {
		return exitCode, "", "", fmt.Errorf("agent did not write result to FD 3. " +
			"HINT: Use 'cat <<EOF >&3' to return JSON. See docs/AGENT_LOGGING_GUIDE.md")
	}

	// Check for output size limit
	if resultBuf.Len() >= maxOutputSize {
		return exitCode, "", "", fmt.Errorf("result JSON exceeded 10MB limit")
	}

	// Non-zero exit code is an error
	if exitCode != 0 {
		return exitCode, resultJSON, "", fmt.Errorf("process exited with code %d", exitCode)
	}

	// Return: exitCode, FD3 result, empty stderr (now unused), error
	// Note: We no longer return stderr separately since it's streamed
	return exitCode, resultJSON, "", nil
}

// parseFD3Output unmarshals and validates the result JSON from FD 3.
// M4.10: Renamed from parseToolOutput to clarify it reads from FD 3, not stdout.
// M5.1: Delegates to parseToolOutputs() for multi-artefact support, returns first artefact for backward compatibility.
// Returns the parsed ToolOutput or an error if the JSON is invalid or missing required fields.
func (e *Engine) parseFD3Output(fd3Result string) (*ToolOutput, error) {
	outputs, err := e.parseToolOutputs(fd3Result)
	if err != nil {
		return nil, err
	}

	// Return first output for backward compatibility
	return &outputs[0], nil
}

// parseToolOutputs unmarshals and validates multiple result JSONs from FD 3.
// M5.1: Supports multi-artefact output for buffer-and-flush pattern.
//
// Expects one or more newline-separated JSON objects:
//
//	{"artefact_type":"TestResult","artefact_payload":"test1.json","summary":"Test 1"}
//	{"artefact_type":"TestResult","artefact_payload":"test2.json","summary":"Test 2"}
//
// Returns array of parsed ToolOutput objects or an error.
func (e *Engine) parseToolOutputs(fd3Result string) ([]ToolOutput, error) {
	if len(fd3Result) == 0 {
		return nil, fmt.Errorf("agent produced no output on FD 3. " +
			"HINT: Write JSON result using 'cat <<EOF >&3'. See docs/AGENT_LOGGING_GUIDE.md")
	}

	trimmed := strings.TrimSpace(fd3Result)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("FD 3 output does not start with JSON. "+
			"Found: %q. HINT: Use 'cat <<EOF >&3' for JSON. See docs/AGENT_LOGGING_GUIDE.md",
			truncate(trimmed, 50))
	}

	// M5.1: Parse multiple JSON objects using json.Decoder
	var outputs []ToolOutput
	decoder := json.NewDecoder(strings.NewReader(trimmed))

	for {
		var output ToolOutput
		if err := decoder.Decode(&output); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid JSON on FD 3: %w. "+
				"HINT: Verify JSON syntax. See docs/AGENT_LOGGING_GUIDE.md", err)
		}

		if err := output.Validate(); err != nil {
			return nil, fmt.Errorf("validation failed for artefact %d: %w", len(outputs)+1, err)
		}

		outputs = append(outputs, output)
	}

	if len(outputs) == 0 {
		return nil, fmt.Errorf("no valid JSON objects found on FD 3")
	}

	return outputs, nil
}

// createResultArtefact builds a new artefact from the tool output and writes it to the blackboard.
// M2.4: Uses the DERIVATIVE relationship model for new work
// M3.3: Automatically manages versioning for feedback claims (rework)
//
// For regular claims:
//   - New logical_id (creates new logical thread)
//   - Version = 1 (first version of this new work)
//   - source_artefacts = [claim.ArtefactID] (links back to input)
//
// For feedback claims (pending_assignment):
//   - Same logical_id as target (continues thread)
//   - Version = target.Version + 1 (increment version)
//   - Same type as target (rework, not new type)
//   - source_artefacts = [target.ID] + claim.AdditionalContextIDs (includes Review artefacts)
//
// Agents remain completely unaware of this versioning logic.
func (e *Engine) createResultArtefact(ctx context.Context, claim *blackboard.Claim, output *ToolOutput) (*blackboard.Artefact, error) {
	// M2.4: Validate git commit for CodeCommit artefacts
	if output.ArtefactType == "CodeCommit" {
		log.Printf("[INFO] Validating git commit: hash=%s", output.ArtefactPayload)
		if err := validateCommitExists(output.ArtefactPayload); err != nil {
			return nil, fmt.Errorf("git commit validation failed for hash %s: %w",
				output.ArtefactPayload, err)
		}
		log.Printf("[DEBUG] Git commit validation passed: hash=%s", output.ArtefactPayload)
	}

	// M3.3: Check if this is a feedback claim (rework scenario)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		// This is a feedback claim - create rework artefact
		return e.createReworkArtefact(ctx, claim, output)
	}

	// Regular claim - create new work artefact
	// Generate new IDs
	artefactID := blackboard.NewID()
	logicalID := artefactID // Derivative: new logical thread

	artefact := &blackboard.Artefact{
		ID:              artefactID,
		LogicalID:       logicalID,
		Version:         1, // First version of new thread
		StructuralType:  output.GetStructuralType(),
		Type:            output.ArtefactType,
		Payload:         output.ArtefactPayload,
		SourceArtefacts: []string{claim.ArtefactID}, // Derivative from target artefact
		ProducedByRole:  e.config.AgentName,         // M3.7: AgentName IS the role
		CreatedAtMs:     time.Now().UnixMilli(),     // M3.9: Millisecond precision timestamp
	}

	// Create artefact in Redis (also publishes event)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to create artefact: %w", err)
	}

	// Add to thread tracking
	if err := e.bbClient.AddVersionToThread(ctx, logicalID, artefactID, 1); err != nil {
		// Log but don't fail - artefact was created successfully
		log.Printf("[WARN] Failed to add version to thread: logical_id=%s error=%v", logicalID, err)
	}

	// M4.3: Process checkpoints if present
	if len(output.Checkpoints) > 0 {
		if err := e.processCheckpoints(ctx, output.Checkpoints, artefact.LogicalID); err != nil {
			// Log but don't fail - main artefact was created successfully
			log.Printf("[WARN] Failed to process checkpoints: %v", err)
		}
	}

	return artefact, nil
}

// createVerifiableResultArtefact creates a V2 VerifiableArtefact with cryptographic hash ID.
// M4.6: This is the hash-based artefact creation path for Phase 3.
// Handles both new work (derivative) and rework (feedback loop) scenarios.
func (e *Engine) createVerifiableResultArtefact(ctx context.Context, claim *blackboard.Claim, output *ToolOutput, targetArtefact *blackboard.Artefact) (*blackboard.VerifiableArtefact, error) {
	// M2.4: Validate git commit for CodeCommit artefacts
	if output.ArtefactType == "CodeCommit" {
		log.Printf("[INFO] Validating git commit: hash=%s", output.ArtefactPayload)
		if err := validateCommitExists(output.ArtefactPayload); err != nil {
			return nil, fmt.Errorf("git commit validation failed for hash %s: %w",
				output.ArtefactPayload, err)
		}
		log.Printf("[DEBUG] Git commit validation passed: hash=%s", output.ArtefactPayload)
	}

	// Determine relationship (Derivative vs Rework)
	var logicalThreadID string
	var version int
	var parentHashes []string

	// M3.3: Check if this is a feedback claim (rework scenario)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		// Rework: Continue existing thread
		logicalThreadID = targetArtefact.LogicalID
		version = targetArtefact.Version + 1
		
		// Parent hashes = Target + Reviews
		parentHashes = []string{targetArtefact.ID}
		parentHashes = append(parentHashes, claim.AdditionalContextIDs...)
		
		log.Printf("[INFO] Creating V2 rework artefact: logical_id=%s version=%d→%d",
			logicalThreadID, targetArtefact.Version, version)
	} else {
		// New Work: Start new thread
		logicalThreadID = blackboard.NewID()
		version = 1
		parentHashes = []string{targetArtefact.ID}
	}

	// Assemble payload
	payload := blackboard.ArtefactPayload{
		Content: output.ArtefactPayload,
	}

	// M4.6: Validate payload size BEFORE hashing (1MB hard limit)
	if err := payload.Validate(); err != nil {
		return nil, fmt.Errorf("payload validation failed: %w", err)
	}

	// M4.6 Security Addendum: Inject HOLT_CLAIM_ID into header for topology validation
	// Use the claim ID we are working on.
	claimID := claim.ID

	// Assemble header
	header := blackboard.ArtefactHeader{
		ParentHashes:    parentHashes,
		LogicalThreadID: logicalThreadID,
		Version:         version,
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  e.config.AgentName, // M3.7: AgentName IS the role
		StructuralType:  output.GetStructuralType(),
		Type:            output.ArtefactType,
		ContextForRoles: nil, // Not used for standard work artefacts
		ClaimID:         claimID, // M4.6 Security Addendum: Grant Linkage
	}

	// Create verifiable artefact (ID will be set after hash computation)
	artefact := &blackboard.VerifiableArtefact{
		Header:  header,
		Payload: payload,
	}

	// M4.6: Compute SHA-256 hash using RFC 8785 canonicalization
	hash, err := blackboard.ComputeArtefactHash(artefact)
	if err != nil {
		return nil, fmt.Errorf("failed to compute artefact hash: %w", err)
	}

	// Set hash as artefact ID (this is the Prover step)
	artefact.ID = hash

	log.Printf("[INFO] M4.6: Computed artefact hash: %s (Prover step)", hash)

	// Create artefact in Redis (Orchestrator will verify hash - Verifier step)
	if err := e.bbClient.WriteVerifiableArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to create verifiable artefact: %w", err)
	}

	// Add to thread tracking
	if err := e.bbClient.AddVersionToThread(ctx, logicalThreadID, artefact.ID, version); err != nil {
		log.Printf("[WARN] Failed to add version to thread: logical_id=%s error=%v", logicalThreadID, err)
	}

	// Publish artefact event (so orchestrator picks it up)
	// Note: In full V2, this would be integrated into WriteVerifiableArtefact
	// For now, we manually publish to maintain compatibility
	v1Wrapper := &blackboard.Artefact{
		ID:              artefact.ID,
		LogicalID:       artefact.Header.LogicalThreadID,
		Version:         artefact.Header.Version,
		StructuralType:  artefact.Header.StructuralType,
		Type:            artefact.Header.Type,
		Payload:         artefact.Payload.Content,
		SourceArtefacts: artefact.Header.ParentHashes,
		ProducedByRole:  artefact.Header.ProducedByRole,
		CreatedAtMs:     artefact.Header.CreatedAtMs,
		ClaimID:         artefact.Header.ClaimID,
	}
	
	artefactJSON, err := json.Marshal(v1Wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal artefact for event: %w", err)
	}

	channel := fmt.Sprintf("holt:%s:artefact_events", e.config.InstanceName)
	if err := e.bbClient.PublishRaw(ctx, channel, string(artefactJSON)); err != nil {
		return nil, fmt.Errorf("failed to publish artefact event: %w", err)
	}

	// If rework, publish artefact_reworked event
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		if err := e.publishArtefactReworkedEvent(ctx, v1Wrapper, targetArtefact.ID); err != nil {
			log.Printf("[WARN] Failed to publish artefact_reworked event: %v", err)
		}
	}

	log.Printf("[INFO] Created verifiable artefact: id=%s type=%s logical_id=%s version=%d",
		artefact.ID, artefact.Header.Type, artefact.Header.LogicalThreadID, artefact.Header.Version)

	// M4.3: Process checkpoints if present
	if len(output.Checkpoints) > 0 {
		if err := e.processCheckpoints(ctx, output.Checkpoints, artefact.Header.LogicalThreadID); err != nil {
			// Log but don't fail - main artefact was created successfully
			log.Printf("[WARN] Failed to process checkpoints: %v", err)
		}
	}

	return artefact, nil
}

// createVerifiableResultArtefacts creates multiple V2 VerifiableArtefacts with batch metadata.
// M5.1: Supports multi-artefact output pattern with automatic batch_size injection.
//
// All artefacts in the batch:
//  - Share the same parent (claim.ArtefactID)
//  - Get metadata: {"batch_size": "N"} where N = len(outputs)
//  - Are created atomically via Lua script
//
// Returns all created artefacts or error on first failure.
func (e *Engine) createVerifiableResultArtefacts(ctx context.Context, claim *blackboard.Claim, outputs []ToolOutput, targetArtefact *blackboard.Artefact) ([]*blackboard.VerifiableArtefact, error) {
	batchSize := len(outputs)

	log.Printf("[INFO] Creating batch of %d artefacts with metadata injection", batchSize)

	var artefacts []*blackboard.VerifiableArtefact

	for i, output := range outputs {
		// M5.1: Inject batch_size metadata
		metadata := map[string]string{
			"batch_size": fmt.Sprintf("%d", batchSize),
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal metadata for artefact %d: %w", i+1, err)
		}

		// Create artefact with metadata (reuse existing logic)
		artefact, err := e.createVerifiableResultArtefactWithMetadata(ctx, claim, &output, targetArtefact, string(metadataJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to create artefact %d/%d: %w", i+1, batchSize, err)
		}

		artefacts = append(artefacts, artefact)
	}

	return artefacts, nil
}

// createVerifiableResultArtefactWithMetadata creates a V2 VerifiableArtefact with custom metadata.
// M5.1: Helper function that wraps createVerifiableResultArtefact with metadata injection.
//
// This is a temporary bridge until we refactor the artefact creation to support metadata natively.
// For now, we delegate to the existing function and manually inject metadata.
func (e *Engine) createVerifiableResultArtefactWithMetadata(ctx context.Context, claim *blackboard.Claim, output *ToolOutput, targetArtefact *blackboard.Artefact, metadata string) (*blackboard.VerifiableArtefact, error) {
	// M2.4: Validate git commit for CodeCommit artefacts
	if output.ArtefactType == "CodeCommit" {
		log.Printf("[INFO] Validating git commit: hash=%s", output.ArtefactPayload)
		if err := validateCommitExists(output.ArtefactPayload); err != nil {
			return nil, fmt.Errorf("git commit validation failed for hash %s: %w",
				output.ArtefactPayload, err)
		}
		log.Printf("[DEBUG] Git commit validation passed: hash=%s", output.ArtefactPayload)
	}

	// Determine relationship (Derivative vs Rework)
	var logicalThreadID string
	var version int
	var parentHashes []string

	// M3.3: Check if this is a feedback claim (rework scenario)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		// Rework: Continue existing thread
		logicalThreadID = targetArtefact.LogicalID
		version = targetArtefact.Version + 1

		// Parent hashes = Target + Reviews
		parentHashes = []string{targetArtefact.ID}
		parentHashes = append(parentHashes, claim.AdditionalContextIDs...)

		log.Printf("[INFO] Creating V2 rework artefact: logical_id=%s version=%d→%d",
			logicalThreadID, targetArtefact.Version, version)
	} else {
		// New Work: Start new thread
		logicalThreadID = blackboard.NewID()
		version = 1
		parentHashes = []string{targetArtefact.ID}
	}

	// Assemble payload
	payload := blackboard.ArtefactPayload{
		Content: output.ArtefactPayload,
	}

	// M4.6: Validate payload size BEFORE hashing (1MB hard limit)
	if err := payload.Validate(); err != nil {
		return nil, fmt.Errorf("payload validation failed: %w", err)
	}

	// M4.6 Security Addendum: Inject HOLT_CLAIM_ID into header for topology validation
	claimID := claim.ID

	// Assemble header
	header := blackboard.ArtefactHeader{
		ParentHashes:    parentHashes,
		LogicalThreadID: logicalThreadID,
		Version:         version,
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  e.config.AgentName,
		StructuralType:  output.GetStructuralType(),
		Type:            output.ArtefactType,
		ContextForRoles: nil,
		ClaimID:         claimID,
	}

	// Create verifiable artefact
	artefact := &blackboard.VerifiableArtefact{
		Header:  header,
		Payload: payload,
	}

	// M4.6: Compute SHA-256 hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	if err != nil {
		return nil, fmt.Errorf("failed to compute artefact hash: %w", err)
	}

	artefact.ID = hash

	// M5.1: Create V1 wrapper with metadata for Lua script
	v1Wrapper := &blackboard.Artefact{
		ID:              artefact.ID,
		LogicalID:       artefact.Header.LogicalThreadID,
		Version:         artefact.Header.Version,
		StructuralType:  artefact.Header.StructuralType,
		Type:            artefact.Header.Type,
		Payload:         artefact.Payload.Content,
		SourceArtefacts: artefact.Header.ParentHashes,
		ProducedByRole:  artefact.Header.ProducedByRole,
		CreatedAtMs:     artefact.Header.CreatedAtMs,
		ClaimID:         artefact.Header.ClaimID,
		Metadata:        metadata, // M5.1: Inject metadata
	}

	// Write V1 wrapper (uses Lua script from Phase 1)
	if err := e.bbClient.CreateArtefact(ctx, v1Wrapper); err != nil {
		return nil, fmt.Errorf("failed to create artefact with metadata: %w", err)
	}

	// Also write V2 verifiable artefact (for verification)
	if err := e.bbClient.WriteVerifiableArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to write verifiable artefact: %w", err)
	}

	// If rework, publish artefact_reworked event
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		if err := e.publishArtefactReworkedEvent(ctx, v1Wrapper, targetArtefact.ID); err != nil {
			log.Printf("[WARN] Failed to publish artefact_reworked event: %v", err)
		}
	}

	// M4.3: Process checkpoints if present
	if len(output.Checkpoints) > 0 {
		if err := e.processCheckpoints(ctx, output.Checkpoints, artefact.Header.LogicalThreadID); err != nil {
			log.Printf("[WARN] Failed to process checkpoints: %v", err)
		}
	}

	return artefact, nil
}

// processCheckpoints handles declarative context caching (M4.3).
// For each checkpoint in the tool output, this function:
//  1. Calls the blackboard's CreateOrVersionKnowledge method (uses Lua script for atomicity)
//  2. Creates/versions Knowledge artefacts and attaches them to the work thread
//
// Parameters:
//   - checkpoints: Array of checkpoint declarations from tool output
//   - threadLogicalID: The logical_id of the work thread to attach knowledge to
func (e *Engine) processCheckpoints(ctx context.Context, checkpoints []Checkpoint, threadLogicalID string) error {
	log.Printf("[INFO] M4.3: Processing %d checkpoints for thread %s", len(checkpoints), threadLogicalID)

	for i, checkpoint := range checkpoints {
		log.Printf("[DEBUG] M4.3: Processing checkpoint %d: knowledge_name=%s target_roles=%v",
			i+1, checkpoint.KnowledgeName, checkpoint.TargetRoles)

		// Validate checkpoint
		if checkpoint.KnowledgeName == "" {
			log.Printf("[WARN] Skipping checkpoint %d: knowledge_name is empty", i+1)
			continue
		}

		// Use blackboard's atomic create-or-version method
		knowledge, err := e.bbClient.CreateOrVersionKnowledge(
			ctx,
			checkpoint.KnowledgeName,
			checkpoint.KnowledgePayload,
			checkpoint.TargetRoles,
			threadLogicalID,
			e.config.AgentName, // M3.7: AgentName IS the role
		)
		if err != nil {
			return fmt.Errorf("failed to create/version knowledge '%s': %w", checkpoint.KnowledgeName, err)
		}

		log.Printf("[INFO] M4.3: Created Knowledge artefact: name=%s version=%d id=%s",
			checkpoint.KnowledgeName, knowledge.Version, knowledge.ID)
	}

	return nil
}

// createFailureArtefact creates a Failure artefact describing a tool execution failure.
// Uses the same derivative provenance model as success artefacts.
// The failure payload contains diagnostic information (exit code, stdout, stderr, error message).
func (e *Engine) createFailureArtefact(ctx context.Context, claim *blackboard.Claim, exitCode int, stdout, stderr, reason string) {
	log.Printf("[INFO] Creating Failure artefact: claim_id=%s reason=%s", claim.ID, reason)

	// Prepare failure data
	failureData := &FailureData{
		Reason:   reason,
		ExitCode: exitCode,
		Stdout:   truncate(stdout, 5000), // Limit stored output
		Stderr:   truncate(stderr, 5000),
		Error:    reason,
	}

	payloadContent, err := MarshalFailurePayload(failureData)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal failure payload: %v", err)
		payloadContent = fmt.Sprintf(`{"reason": "Failed to marshal failure data: %v"}`, err)
	}

	// M4.6: Use V2 VerifiableArtefact structure
	logicalThreadID := blackboard.NewID()
	
	// M4.6 Security Addendum: Inject HOLT_CLAIM_ID into header
	// For failures during claim processing, we know the claim ID
	claimID := claim.ID

	v2Artefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{claim.ArtefactID},
			LogicalThreadID: logicalThreadID,
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  e.config.AgentName,
			StructuralType:  blackboard.StructuralTypeFailure,
			Type:            "ToolExecutionFailure",
			ClaimID:         claimID,
		},
		Payload: blackboard.ArtefactPayload{
			Content: payloadContent,
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(v2Artefact)
	if err != nil {
		log.Printf("[ERROR] Failed to compute hash for Failure artefact: %v", err)
		return
	}
	v2Artefact.ID = hash

	// Write to blackboard
	if err := e.bbClient.WriteVerifiableArtefact(ctx, v2Artefact); err != nil {
		log.Printf("[ERROR] Failed to create Failure artefact: %v", err)
		return
	}

	// Publish event (manual until V2 integration complete)
	// We need to convert to V1 struct for event publishing if subscribers expect V1
	// But wait, WriteVerifiableArtefact might not publish event? 
	// The client.WriteVerifiableArtefact does NOT publish event by default in current impl?
	// Let's check client code... actually WriteVerifiableArtefact just writes hash.
	// We need to publish.
	
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
		log.Printf("[WARN] Failed to add Failure artefact to thread: %v", err)
	}

	// Publish event
	artefactJSON, _ := json.Marshal(artefact)
	channel := fmt.Sprintf("holt:%s:artefact_events", e.config.InstanceName)
	e.bbClient.PublishRaw(ctx, channel, string(artefactJSON))

	log.Printf("[INFO] Created Failure artefact: artefact_id=%s claim_id=%s", v2Artefact.ID, claim.ID)
}

// limitedWriter wraps a writer and enforces a size limit.
// Once the limit is reached, further writes are discarded.
type limitedWriter struct {
	w       io.Writer
	limit   int
	written int
}

func (lw *limitedWriter) Write(p []byte) (n int, err error) {
	remaining := lw.limit - lw.written
	if remaining <= 0 {
		// Already hit limit, discard this write
		return len(p), nil
	}

	// Write up to the limit
	toWrite := p
	if len(p) > remaining {
		toWrite = p[:remaining]
	}

	n, err = lw.w.Write(toWrite)
	lw.written += n
	return len(p), err // Return len(p) to satisfy the writer interface
}

// limitedReader wraps a reader and enforces a size limit (M4.10).
// Used to limit bytes read from FD 3 to prevent unbounded memory growth.
type limitedReader struct {
	r     io.Reader
	limit int
	read  int
}

func (lr *limitedReader) Read(p []byte) (n int, err error) {
	if lr.read >= lr.limit {
		return 0, fmt.Errorf("read limit exceeded (%d bytes)", lr.limit)
	}

	maxRead := lr.limit - lr.read
	if len(p) > maxRead {
		p = p[:maxRead]
	}

	n, err = lr.r.Read(p)
	lr.read += n
	return n, err
}

// truncate limits a string to maxLen characters, appending "..." if truncated
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// createReworkArtefact builds a new version of the target artefact using agent output.
// M3.3: Automatically manages versioning for feedback claims - agents remain unaware.
//
// The pup:
//   - Fetches the target artefact to get its logical_id, version, and type
//   - Creates a new artefact with:
//   - Same logical_id (continues the thread)
//   - Version = target.version + 1 (automatic increment)
//   - Same type as target (rework, not new type)
//   - source_artefacts = [target.ID] + claim.AdditionalContextIDs (target + Review artefacts)
//   - Agent tool is completely unaware of versioning
func (e *Engine) createReworkArtefact(ctx context.Context, claim *blackboard.Claim, output *ToolOutput) (*blackboard.Artefact, error) {
	// Fetch the target artefact to get logical_id, version, and type
	targetArtefact, err := e.bbClient.GetArtefact(ctx, claim.ArtefactID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch target artefact: %w", err)
	}
	if targetArtefact == nil {
		return nil, fmt.Errorf("target artefact %s not found", claim.ArtefactID)
	}

	log.Printf("[INFO] Creating rework artefact: logical_id=%s version=%d→%d type=%s",
		targetArtefact.LogicalID, targetArtefact.Version, targetArtefact.Version+1, targetArtefact.Type)

	// Build source_artefacts: target + Review artefacts
	sourceArtefacts := []string{targetArtefact.ID}
	sourceArtefacts = append(sourceArtefacts, claim.AdditionalContextIDs...)

	// Generate new artefact ID
	artefactID := blackboard.NewID()

	artefact := &blackboard.Artefact{
		ID:              artefactID,
		LogicalID:       targetArtefact.LogicalID,   // Same thread
		Version:         targetArtefact.Version + 1, // Increment version
		StructuralType:  output.GetStructuralType(),
		Type:            targetArtefact.Type, // Same type (rework)
		Payload:         output.ArtefactPayload,
		SourceArtefacts: sourceArtefacts,        // Target + Reviews
		ProducedByRole:  e.config.AgentName,    // M3.7: AgentName IS the role
		CreatedAtMs:     time.Now().UnixMilli(), // M3.9: Millisecond precision timestamp
	}

	// Create artefact in Redis (also publishes event)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to create rework artefact: %w", err)
	}

	// Add to thread tracking
	if err := e.bbClient.AddVersionToThread(ctx, artefact.LogicalID, artefact.ID, artefact.Version); err != nil {
		// Log but don't fail - artefact was created successfully
		log.Printf("[WARN] Failed to add rework artefact to thread: logical_id=%s error=%v",
			artefact.LogicalID, err)
	}

	// Publish artefact_reworked workflow event
	if err := e.publishArtefactReworkedEvent(ctx, artefact, targetArtefact.ID); err != nil {
		log.Printf("[WARN] Failed to publish artefact_reworked event: %v", err)
	}

	log.Printf("[INFO] Rework artefact created: id=%s logical_id=%s version=%d (agent unaware of versioning)",
		artefact.ID, artefact.LogicalID, artefact.Version)

	// M4.3: Process checkpoints if present
	if len(output.Checkpoints) > 0 {
		if err := e.processCheckpoints(ctx, output.Checkpoints, artefact.LogicalID); err != nil {
			// Log but don't fail - main artefact was created successfully
			log.Printf("[WARN] Failed to process checkpoints: %v", err)
		}
	}

	return artefact, nil
}

// publishArtefactReworkedEvent publishes an artefact_reworked workflow event.
// Called when creating a new version of an existing artefact (feedback claim rework).
func (e *Engine) publishArtefactReworkedEvent(ctx context.Context, newArtefact *blackboard.Artefact, previousVersionID string) error {
	eventData := map[string]interface{}{
		"new_artefact_id":     newArtefact.ID,
		"logical_id":          newArtefact.LogicalID,
		"new_version":         newArtefact.Version,
		"previous_version_id": previousVersionID,
		"artefact_type":       newArtefact.Type,
		"produced_by_role":    newArtefact.ProducedByRole,
	}

	if err := e.bbClient.PublishWorkflowEvent(ctx, "artefact_reworked", eventData); err != nil {
		return fmt.Errorf("failed to publish artefact_reworked event: %w", err)
	}

	log.Printf("[INFO] Published artefact_reworked event: new_id=%s logical_id=%s version=%d",
		newArtefact.ID, newArtefact.LogicalID, newArtefact.Version)

	return nil
}

// shouldCreateTerminalForClaim determines if a Terminal artefact should be created for this claim.
// Terminal artefacts signal claim completion, but in multi-phase workflows, only the final phase should create one.
//
// Returns true for:
//   - Exclusive phase claims (pending_exclusive) - this is the final phase
//   - Feedback claims (pending_assignment) - single-phase rework
//
// Returns false for:
//   - Review phase claims (pending_review) - workflow continues to parallel phase
//   - Parallel phase claims (pending_parallel) - workflow continues to exclusive phase
func (e *Engine) shouldCreateTerminalForClaim(claim *blackboard.Claim) bool {
	switch claim.Status {
	case blackboard.ClaimStatusPendingExclusive:
		// Final phase in multi-phase workflow - create Terminal
		return true
	case blackboard.ClaimStatusPendingAssignment:
		// Feedback claim - single-phase rework - create Terminal
		return true
	case blackboard.ClaimStatusPendingReview, blackboard.ClaimStatusPendingParallel:
		// Intermediate phases - don't create Terminal, let workflow continue
		return false
	default:
		// Defensive: for unknown statuses, create Terminal
		log.Printf("[WARN] Unknown claim status %s for claim %s - creating Terminal by default",
			claim.Status, claim.ID)
		return true
	}
}

// confirmArtefactsInRedis verifies that all artefacts are written to Redis before proceeding.
// This ensures the orchestrator will see Question/Failure artefacts before processing the Terminal artefact.
// Prevents race conditions where Terminal is processed before siblings are available.
func (e *Engine) confirmArtefactsInRedis(ctx context.Context, artefacts []*blackboard.VerifiableArtefact) error {
	log.Printf("[INFO] Confirming %d artefacts are in Redis before creating Terminal", len(artefacts))

	for i, artefact := range artefacts {
		// Try to read back the artefact from Redis
		_, err := e.bbClient.GetArtefact(ctx, artefact.ID)
		if err != nil {
			return fmt.Errorf("artefact %d/%d (id=%s) not found in Redis: %w", i+1, len(artefacts), artefact.ID[:16], err)
		}
		log.Printf("[DEBUG] Confirmed artefact %d/%d in Redis: %s (type=%s, structural=%s)",
			i+1, len(artefacts), artefact.ID[:16], artefact.Header.Type, artefact.Header.StructuralType)
	}

	log.Printf("[INFO] All %d artefacts confirmed in Redis", len(artefacts))
	return nil
}

// validateArtefactOutputs enforces artefact output consistency rules:
// 1. Cannot mix different structural types (Standard vs Question vs Failure vs Terminal)
// 2. Question, Failure, Terminal artefacts limited to max 1
// 3. Must produce at least one artefact
//
// This prevents agents from creating confusing output combinations like
// "1 CodeCommit + 1 Question" which would be semantically unclear.
func (e *Engine) validateArtefactOutputs(outputs []ToolOutput) error {
	if len(outputs) == 0 {
		return fmt.Errorf("no artefacts produced (agent must produce at least one artefact)")
	}

	structuralTypes := make(map[blackboard.StructuralType]int)

	for _, output := range outputs {
		st := output.GetStructuralType()
		structuralTypes[st]++
	}

	// Rule 1: Cannot mix structural types
	if len(structuralTypes) > 1 {
		types := make([]string, 0, len(structuralTypes))
		for st := range structuralTypes {
			types = append(types, string(st))
		}
		return fmt.Errorf("cannot produce mixed structural types in single execution: %v. Agent must produce artefacts of a single structural type per claim", types)
	}

	// Rule 2: Special types (Question, Failure, Terminal) limited to 1
	for st, count := range structuralTypes {
		switch st {
		case blackboard.StructuralTypeQuestion,
			blackboard.StructuralTypeFailure,
			blackboard.StructuralTypeTerminal:
			if count > 1 {
				return fmt.Errorf("cannot produce multiple %s artefacts (got %d). Question, Failure, and Terminal artefacts are limited to 1 per claim", st, count)
			}
		}
	}

	return nil
}

// createTerminalArtefact creates a Terminal artefact to signal claim completion.
// This allows the orchestrator to know when an agent has finished producing all artefacts,
// preventing premature claim completion when agents produce multiple artefacts.
// This is ALWAYS created, even after Failure artefacts, to signal the claim is done.
func (e *Engine) createTerminalArtefact(ctx context.Context, claim *blackboard.Claim, artefactCount int) {
	log.Printf("[INFO] Creating Terminal artefact: claim_id=%s artefact_count=%d", claim.ID, artefactCount)

	// Create Terminal artefact payload
	terminalData := map[string]interface{}{
		"reason":         "Work execution complete",
		"artefact_count": artefactCount,
		"claim_id":       claim.ID,
	}

	payloadJSON, err := json.Marshal(terminalData)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal Terminal payload: %v", err)
		payloadJSON = []byte(`{"reason": "Work execution complete"}`)
	}

	// Create unique logical thread for Terminal artefact
	logicalThreadID := blackboard.NewID()

	v2Artefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{claim.ArtefactID},
			LogicalThreadID: logicalThreadID,
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  e.config.AgentName,
			StructuralType:  blackboard.StructuralTypeTerminal,
			Type:            "ClaimComplete",
			ClaimID:         claim.ID,
		},
		Payload: blackboard.ArtefactPayload{
			Content: string(payloadJSON),
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(v2Artefact)
	if err != nil {
		log.Printf("[ERROR] Failed to compute hash for Terminal artefact: %v", err)
		return
	}
	v2Artefact.ID = hash

	// Write to blackboard
	if err := e.bbClient.WriteVerifiableArtefact(ctx, v2Artefact); err != nil {
		log.Printf("[ERROR] Failed to create Terminal artefact: %v", err)
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
		log.Printf("[WARN] Failed to add Terminal artefact to thread: %v", err)
	}

	// Publish event
	artefactJSON, _ := json.Marshal(artefact)
	channel := fmt.Sprintf("holt:%s:artefact_events", e.config.InstanceName)
	e.bbClient.PublishRaw(ctx, channel, string(artefactJSON))

	log.Printf("[INFO] Terminal artefact created: artefact_id=%s type=%s", artefact.ID, artefact.Type)
}
