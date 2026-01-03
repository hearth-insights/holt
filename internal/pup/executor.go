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

	"github.com/hearth-insights/holt/internal/debug"
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
//  2. M5.2: Acquire synchronization lock (if synchronizer configured)
//  3. Prepare tool input JSON (stdin)
//  4. Execute tool subprocess with timeout
//  5. Parse tool output JSON (stdout)
//  6. Create result artefact with derivative provenance
//  7. Publish artefact to blackboard
//  8. M5.2: Release synchronization lock
//
// On any failure, creates a Failure artefact and continues (never crashes).
func (e *Engine) executeWork(ctx context.Context, claim *blackboard.Claim) {
	// Fetch target artefact
	targetArtefact, err := e.fetchTargetArtefact(ctx, claim)
	if err != nil {
		log.Printf("[ERROR] Failed to fetch target artefact: %v", err)
		e.createFailureArtefact(ctx, claim, -1, "", "", fmt.Sprintf("Failed to fetch target artefact: %v", err))
		e.createTerminalArtefact(ctx, claim, 0)
		return
	}

	log.Printf("[INFO] Fetched target artefact: artefact_id=%s type=%s",
		targetArtefact.ID, targetArtefact.Header.Type)

	// M5.1.1: Work lock acquisition removed
	// Old M5.1 approach: Acquire/release locks to prevent duplicate processing
	// New M5.1.1 approach: Orchestrator manages accumulation, no client-side locking needed

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
		// Check if this is a terminal failure
		if targetArtefact.Header.Type == "Failure" {
			e.createFailureArtefact(ctx, claim, 0, stdout, stderr,
				fmt.Sprintf("Tool succeeded but artefact creation failed: %v", err))
			e.createTerminalArtefact(ctx, claim, 0)
			return
		}
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
	// M5.1.1: Detect Fan-In Claim (merge phase grants)
	// Fan-In Claim IDs have format: fanin:{ancestor_id}:{role}
	isFanInClaim := strings.HasPrefix(claim.ID, "fanin:")

	if isFanInClaim {
		// Handle Fan-In Claim specially (no target artefact, use accumulator data)
		return e.prepareToolInputForMerge(ctx, claim)
	}

	// Assemble context chain via BFS traversal with thread tracking
	// M3.3: Pass claim for feedback claim context support
	contextChain, err := e.assembleContext(ctx, targetArtefact, claim)
	if err != nil {
		return "", fmt.Errorf("failed to assemble context: %w", err)
	}

	debug.Log("Prepared context chain: %d artefacts", len(contextChain))

	// M4.3: Load Knowledge artefacts for this agent
	contextIsDeclared, knowledgeBase, loadedKnowledge, err := e.loadKnowledgeForAgent(ctx, contextChain)
	if err != nil {
		return "", fmt.Errorf("failed to load knowledge: %w", err)
	}

	// M5.1: If using synchronization, fetch ancestor and descendants
	var ancestorArtefact *blackboard.Artefact
	var descendantArtefacts []interface{}
	if e.config.SynchronizeConfig != nil {
		// 1. Find the common ancestor configuration
		ancestorType := e.config.SynchronizeConfig.AncestorType
		maxDepth := e.config.SynchronizeConfig.MaxDepth

		// 2. Find the ancestor artefact
		ancestor, err := e.findSyncAncestor(ctx, targetArtefact, ancestorType)
		if err != nil {
			log.Printf("[WARN] Failed to find sync ancestor (type=%s) for artefact %s: %v",
				ancestorType, targetArtefact.ID, err)
			// Don't fail the job, just pass empty descendants (best effort)
		} else if ancestor != nil {
			ancestorArtefact = ancestor
			log.Printf("[INFO] Found sync ancestor: id=%s type=%s", ancestor.ID, ancestor.Header.Type)

			// 3. Fetch descendants of the ancestor
			descendants, err := e.bbClient.GetDescendants(ctx, ancestor.ID, maxDepth)
			if err != nil {
				return "", fmt.Errorf("failed to fetch descendants for synchronization: %w", err)
			}

			// M5.2: Filter descendants to only include wait_for types
			// The agent should receive the artefacts it was waiting for, not all descendants
			waitForTypes := make(map[string]bool)
			for _, condition := range e.config.SynchronizeConfig.WaitFor {
				waitForTypes[condition.Type] = true
			}

			// Filter by type and deduplicate by LogicalThreadID (keep latest version)
			latestByThread := make(map[string]*blackboard.Artefact)
			for _, desc := range descendants {
				if !waitForTypes[desc.Header.Type] {
					continue // Not a wait_for type, skip
				}

				threadID := desc.Header.LogicalThreadID
				existing, exists := latestByThread[threadID]
				if !exists || desc.Header.Version > existing.Header.Version {
					// First occurrence or newer version - keep it
					latestByThread[threadID] = desc
				}
			}

			// Convert map to slice
			filteredDescendants := make([]*blackboard.Artefact, 0, len(latestByThread))
			for _, desc := range latestByThread {
				filteredDescendants = append(filteredDescendants, desc)
			}

			descendantArtefacts = make([]interface{}, len(filteredDescendants))
			for i, desc := range filteredDescendants {
				descendantArtefacts[i] = desc
			}
			debug.Log("Fetched %d descendants of ancestor %s for synchronization (filtered to %d wait_for types, deduplicated to %d unique threads)",
				len(descendants), ancestor.ID, len(filteredDescendants), len(latestByThread))
		}
	}

	// Convert to []interface{} for JSON marshaling
	contextChainInterface := make([]interface{}, len(contextChain))
	for i, art := range contextChain {
		contextChainInterface[i] = art
	}

	input := &ToolInput{
		ClaimType:           "exclusive", // M2.4: still hardcoded (Phase 3 will support review/parallel)
		TargetArtefact:      targetArtefact,
		ContextChain:        contextChainInterface,
		ContextIsDeclared:   contextIsDeclared,
		KnowledgeBase:       knowledgeBase,
		LoadedKnowledge:     loadedKnowledge,
		AncestorArtefact:    ancestorArtefact,    // M5.1
		DescendantArtefacts: descendantArtefacts, // M5.1
	}

	jsonBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool input: %w", err)
	}

	return string(jsonBytes), nil
}

// prepareToolInputForMerge creates tool input for Fan-In Claims (M5.1.1 merge phase).
// Fan-In Claims have deterministic IDs: fanin:{ancestor_id}:{role}
//
// Workflow:
//  1. Parse ancestor_id from claim ID
//  2. Fetch accumulator to get accumulated claim IDs
//  3. Fetch ancestor artefact
//  4. Fetch all artefacts from accumulated claims
//  5. Filter by wait_for types and deduplicate by LogicalThreadID
//  6. Build tool input with ancestor + descendants
//
// Returns:
//   - Tool input JSON string
//   - error if any step fails
func (e *Engine) prepareToolInputForMerge(ctx context.Context, claim *blackboard.Claim) (string, error) {
	// Parse Fan-In Claim ID: fanin:{ancestor_id}:{role}
	parts := strings.Split(claim.ID, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid Fan-In Claim ID format: %s", claim.ID)
	}
	ancestorID := parts[1]
	role := parts[2]

	log.Printf("[Pup/Merge] Preparing Fan-In tool input for ancestor %.16s, role %s", ancestorID, role)

	// Fetch accumulator to get accumulated claim IDs
	accumulatedClaimIDs, err := e.bbClient.GetAccumulatedClaims(ctx, ancestorID, role)
	if err != nil {
		return "", fmt.Errorf("failed to get accumulated claims: %w", err)
	}

	log.Printf("[Pup/Merge] Found %d accumulated claims", len(accumulatedClaimIDs))

	// Fetch ancestor artefact
	ancestorArtefact, err := e.bbClient.GetArtefact(ctx, ancestorID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch ancestor artefact: %w", err)
	}

	// Fetch all artefacts from accumulated claims
	var allArtefacts []*blackboard.Artefact
	for _, claimID := range accumulatedClaimIDs {
		accClaim, err := e.bbClient.GetClaim(ctx, claimID)
		if err != nil {
			log.Printf("[WARN] Failed to fetch accumulated claim %s: %v", claimID, err)
			continue
		}

		artefact, err := e.bbClient.GetArtefact(ctx, accClaim.ArtefactID)
		if err != nil {
			log.Printf("[WARN] Failed to fetch artefact %s for claim %s: %v", accClaim.ArtefactID, claimID, err)
			continue
		}

		allArtefacts = append(allArtefacts, artefact)
	}

	log.Printf("[Pup/Merge] Fetched %d artefacts from accumulated claims", len(allArtefacts))

	// M5.2: Filter descendants to only include wait_for types
	// Build waitForTypes map
	waitForTypes := make(map[string]bool)
	if e.config.SynchronizeConfig != nil {
		for _, condition := range e.config.SynchronizeConfig.WaitFor {
			waitForTypes[condition.Type] = true
		}
	}

	// Filter by type and deduplicate by LogicalThreadID (keep latest version)
	latestByThread := make(map[string]*blackboard.Artefact)
	for _, artefact := range allArtefacts {
		if len(waitForTypes) > 0 && !waitForTypes[artefact.Header.Type] {
			continue // Not a wait_for type, skip
		}

		threadID := artefact.Header.LogicalThreadID
		existing, exists := latestByThread[threadID]
		if !exists || artefact.Header.Version > existing.Header.Version {
			// First occurrence or newer version - keep it
			latestByThread[threadID] = artefact
		}
	}

	// Convert map to slice
	filteredArtefacts := make([]*blackboard.Artefact, 0, len(latestByThread))
	for _, artefact := range latestByThread {
		filteredArtefacts = append(filteredArtefacts, artefact)
	}

	// Convert to []interface{} for JSON marshaling
	descendantArtefacts := make([]interface{}, len(filteredArtefacts))
	for i, artefact := range filteredArtefacts {
		descendantArtefacts[i] = artefact
	}

	log.Printf("[Pup/Merge] Filtered to %d descendants (wait_for types, deduplicated by thread)", len(descendantArtefacts))

	// Build tool input (no target artefact, no context chain for Fan-In Claims)
	input := &ToolInput{
		ClaimType:           "merge", // M5.1.1: New claim type for Fan-In Claims
		TargetArtefact:      nil,     // No target for Fan-In Claims
		ContextChain:        []interface{}{}, // Empty context chain
		ContextIsDeclared:   false,
		KnowledgeBase:       map[string]string{},
		LoadedKnowledge:     []string{}, // No knowledge for merge claims
		AncestorArtefact:    ancestorArtefact,
		DescendantArtefacts: descendantArtefacts,
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
	debug.Log("M4.10: Attached FD 3 for result JSON")

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

// createResultArtefact creates a V2 Artefact with cryptographic hash ID.
// M4.6: This is the hash-based artefact creation path.
// Handles both new work (derivative) and rework (feedback loop) scenarios.
func (e *Engine) createResultArtefact(ctx context.Context, claim *blackboard.Claim, output *ToolOutput, targetArtefact *blackboard.Artefact, metadata string) (*blackboard.Artefact, error) {
	// M2.4: Validate git commit for CodeCommit artefacts
	if output.ArtefactType == "CodeCommit" {
		log.Printf("[INFO] Validating git commit: hash=%s", output.ArtefactPayload)
		if err := validateCommitExists(output.ArtefactPayload); err != nil {
			return nil, fmt.Errorf("git commit validation failed for hash %s: %w",
				output.ArtefactPayload, err)
		}
		debug.Log("Git commit validation passed: hash=%s", output.ArtefactPayload)
	}

	// Determine relationship (Derivative vs Rework)
	var logicalThreadID string
	var version int
	var parentHashes []string

	// M3.3: Check if this is a feedback claim (rework scenario)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		// Rework: Continue existing thread
		logicalThreadID = targetArtefact.Header.LogicalThreadID
		version = targetArtefact.Header.Version + 1

		// Parent hashes = Target + Reviews
		parentHashes = []string{targetArtefact.ID}
		parentHashes = append(parentHashes, claim.AdditionalContextIDs...)

		log.Printf("[INFO] Creating V2 rework artefact: logical_id=%s version=%d->%d",
			logicalThreadID, targetArtefact.Header.Version, version)
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
		ContextForRoles: nil,      // Not used for standard work artefacts
		ClaimID:         claimID,  // M4.6 Security Addendum: Grant Linkage
		Metadata:        metadata, // M5.1: Inject metadata
	}

	// Create V2 artefact (ID will be set after hash computation)
	artefact := &blackboard.Artefact{
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

	// Create artefact in Redis (uses Lua script for atomic thread updates and events)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to create artefact in Redis: %w", err)
	}

	// If rework, publish artefact_reworked event (CreateArtefact only publishes artefact_events)
	if claim.Status == blackboard.ClaimStatusPendingAssignment {
		if err := e.publishArtefactReworkedEvent(ctx, artefact, targetArtefact.ID); err != nil {
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
//   - Share the same parent (claim.ArtefactID)
//   - Get metadata: {"batch_size": "N"} where N = len(outputs)
//   - Are created atomically via Lua script
//
// Returns all created artefacts or error on first failure.
func (e *Engine) createVerifiableResultArtefacts(ctx context.Context, claim *blackboard.Claim, outputs []ToolOutput, targetArtefact *blackboard.Artefact) ([]*blackboard.Artefact, error) {
	batchSize := len(outputs)

	log.Printf("[INFO] Creating batch of %d artefacts with metadata injection", batchSize)

	var artefacts []*blackboard.Artefact

	for i, output := range outputs {
		// M5.1: Inject batch_size metadata
		metadata := map[string]string{
			"batch_size": fmt.Sprintf("%d", batchSize),
		}
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal metadata for artefact %d: %w", i+1, err)
		}

		// Create artefact with metadata
		artefact, err := e.createResultArtefact(ctx, claim, &output, targetArtefact, string(metadataJSON))
		if err != nil {
			return nil, fmt.Errorf("failed to create artefact %d/%d: %w", i+1, batchSize, err)
		}

		artefacts = append(artefacts, artefact)
	}

	return artefacts, nil
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
		debug.Log("M4.3: Processing checkpoint %d: knowledge_name=%s target_roles=%v",
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
			checkpoint.KnowledgeName, knowledge.Header.Version, knowledge.ID)
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

	// M4.6: Use V2 Artefact structure
	logicalThreadID := blackboard.NewID()

	// M4.6 Security Addendum: Inject HOLT_CLAIM_ID into header
	// For failures during claim processing, we know the claim ID
	claimID := claim.ID

	artefact := &blackboard.Artefact{
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
	hash, err := blackboard.ComputeArtefactHash(artefact)
	if err != nil {
		log.Printf("[ERROR] Failed to compute hash for Failure artefact: %v", err)
		return
	}
	artefact.ID = hash

	// Write to blackboard using atomic CreateArtefact (V2)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		log.Printf("[ERROR] Failed to create Failure artefact: %v", err)
		return
	}

	log.Printf("[INFO] Created Failure artefact: artefact_id=%s claim_id=%s", artefact.ID, claim.ID)
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
		targetArtefact.Header.LogicalThreadID, targetArtefact.Header.Version, targetArtefact.Header.Version+1, targetArtefact.Header.Type)

	// Build source_artefacts: target + Review artefacts
	sourceArtefacts := []string{targetArtefact.ID}
	sourceArtefacts = append(sourceArtefacts, claim.AdditionalContextIDs...)

	// Generate new artefact ID
	artefactID := blackboard.NewID()

	artefact := &blackboard.Artefact{
		ID: artefactID,
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: targetArtefact.Header.LogicalThreadID, // Same thread
			Version:         targetArtefact.Header.Version + 1,     // Increment version
			StructuralType:  output.GetStructuralType(),
			Type:            targetArtefact.Header.Type, // Same type (rework)
			ParentHashes:    sourceArtefacts,            // Target + Reviews
			ProducedByRole:  e.config.AgentName,         // M3.7: AgentName IS the role
			CreatedAtMs:     time.Now().UnixMilli(),     // M3.9: Millisecond precision timestamp
		},
		Payload: blackboard.ArtefactPayload{
			Content: output.ArtefactPayload,
		},
	}

	// Create artefact in Redis (also publishes event)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		return nil, fmt.Errorf("failed to create rework artefact: %w", err)
	}

	// Add to thread tracking
	if err := e.bbClient.AddVersionToThread(ctx, artefact.Header.LogicalThreadID, artefact.ID, artefact.Header.Version); err != nil {
		// Log but don't fail - artefact was created successfully
		log.Printf("[WARN] Failed to add rework artefact to thread: logical_id=%s error=%v",
			artefact.Header.LogicalThreadID, err)
	}

	// Publish artefact_reworked workflow event
	if err := e.publishArtefactReworkedEvent(ctx, artefact, targetArtefact.ID); err != nil {
		log.Printf("[WARN] Failed to publish artefact_reworked event: %v", err)
	}

	log.Printf("[INFO] Rework artefact created: id=%s logical_id=%s version=%d (agent unaware of versioning)",
		artefact.ID, artefact.Header.LogicalThreadID, artefact.Header.Version)

	// M4.3: Process checkpoints if present
	if len(output.Checkpoints) > 0 {
		if err := e.processCheckpoints(ctx, output.Checkpoints, artefact.Header.LogicalThreadID); err != nil {
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
		"logical_id":          newArtefact.Header.LogicalThreadID,
		"new_version":         newArtefact.Header.Version,
		"previous_version_id": previousVersionID,
		"artefact_type":       newArtefact.Header.Type,
		"produced_by_role":    newArtefact.Header.ProducedByRole,
	}

	if err := e.bbClient.PublishWorkflowEvent(ctx, "artefact_reworked", eventData); err != nil {
		return fmt.Errorf("failed to publish artefact_reworked event: %w", err)
	}

	log.Printf("[INFO] Published artefact_reworked event: new_id=%s logical_id=%s version=%d",
		newArtefact.ID, newArtefact.Header.LogicalThreadID, newArtefact.Header.Version)

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
func (e *Engine) confirmArtefactsInRedis(ctx context.Context, artefacts []*blackboard.Artefact) error {
	log.Printf("[INFO] Confirming %d artefacts are in Redis before creating Terminal", len(artefacts))

	for i, artefact := range artefacts {
		// Try to read back the artefact from Redis
		_, err := e.bbClient.GetArtefact(ctx, artefact.ID)
		if err != nil {
			return fmt.Errorf("artefact %d/%d (id=%s) not found in Redis: %w", i+1, len(artefacts), artefact.ID[:16], err)
		}
		debug.Log("Confirmed artefact %d/%d in Redis: %s (type=%s, structural=%s)",
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

	artefact := &blackboard.Artefact{
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
	hash, err := blackboard.ComputeArtefactHash(artefact)
	if err != nil {
		log.Printf("[ERROR] Failed to compute hash for Terminal artefact: %v", err)
		return
	}
	artefact.ID = hash

	// Write to blackboard using atomic CreateArtefact (V2)
	if err := e.bbClient.CreateArtefact(ctx, artefact); err != nil {
		log.Printf("[ERROR] Failed to create Terminal artefact: %v", err)
		return
	}

	log.Printf("[INFO] Terminal artefact created: artefact_id=%s type=%s", artefact.ID, artefact.Header.Type)
}

// findSyncAncestor traverses upward to find the first ancestor matching ancestorType.
// M5.1: Helper for synchronization logic in Executor.
//
// Returns:
//   - Ancestor artefact if found
//   - nil if not found
//   - error if traversal fails
func (e *Engine) findSyncAncestor(ctx context.Context, startArtefact *blackboard.Artefact, ancestorType string) (*blackboard.Artefact, error) {
	// Check if start artefact itself is the ancestor
	if startArtefact.Header.Type == ancestorType {
		return startArtefact, nil
	}

	visited := make(map[string]bool)
	queue := []string{startArtefact.ID}

	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]

		if visited[currentID] {
			continue
		}
		visited[currentID] = true

		// Optimisation: If currentID is startArtefact.ID, we already have it
		var current *blackboard.Artefact
		var err error

		if currentID == startArtefact.ID {
			current = startArtefact
		} else {
			current, err = e.bbClient.GetArtefact(ctx, currentID)
			if err != nil {
				return nil, err
			}
		}

		// Check if this is the ancestor we're looking for
		if current.Header.Type == ancestorType {
			return current, nil
		}

		// Add parents to queue (traverse upward)
		queue = append(queue, current.Header.ParentHashes...)

		// Safety check: Don't traverse too deep/wide (prevent infinite loops in malformed graphs)
		if len(visited) > 1000 {
			return nil, fmt.Errorf("traversal limit exceeded looking for ancestor type %s", ancestorType)
		}
	}

	return nil, nil // Not found
}
