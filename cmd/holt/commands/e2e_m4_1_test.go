//go:build integration
// +build integration

package commands

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestE2E_M4_1_AgentToHumanQA validates the complete Agent-to-Human Question/Answer flow:
// 1. Agent produces Question artefact about user's GoalDefined
// 2. Orchestrator terminates claim and creates feedback claim for "user" role
// 3. Human runs `holt questions` to see the question
// 4. Human runs `holt answer` to provide clarified goal
// 5. Orchestrator creates new claim for clarified goal
func TestE2E_M4_1_AgentToHumanQA(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M4.1 E2E: Agent-to-Human Question/Answer ===")

	// Step 0: Build question-agent Docker image
	projectRoot := testutil.GetProjectRoot()

	t.Log("Building question-agent Docker image...")
	buildCmd := exec.Command("docker", "build",
		"-t", "question-agent:latest",
		"-f", "agents/question-agent/Dockerfile",
		".")  // Build from project root (needs access to go.mod, cmd/pup, etc.)
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build question-agent image")
	t.Log("✓ question-agent image built")

	// Step 1: Setup environment with question agent
	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000 # 10 minutes for E2E tests
agents:
  QuestionAgent:
    image: "question-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: ro
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		_ = runDown(downCmd, []string{})
		t.Log("✓ Cleanup complete")
	}()

	t.Logf("✓ Environment setup complete: %s", env.TmpDir)
	t.Logf("✓ Instance name: %s", env.InstanceName)

	// Step 2: Start instance
	t.Log("Step 2: Starting Holt instance...")
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err, "Failed to start instance")
	t.Logf("✓ Instance started: %s", env.InstanceName)

	// Wait for containers to be ready
	time.Sleep(2 * time.Second)

	// Step 3: Connect to blackboard
	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Logf("✓ Connected to blackboard (Redis port: %d)", env.RedisPort)

	// Step 4: Create initial GoalDefined artefact (from "user")
	t.Log("Step 4: Creating GoalDefined artefact...")

	goalArtefact := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         1,
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  "user",
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		ClaimID:         "",
	}, "Build a REST API")

	t.Logf("✓ GoalDefined created: %s", goalArtefact.ID)

	// Step 5: Wait for Question artefact to be produced
	t.Log("Step 5: Waiting for agent to produce Question artefact...")
	var questionArtefact *blackboard.Artefact
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

		// Query for Question artefacts
		rdb := bbClient.GetRedisClient()
		pattern := "holt:" + env.InstanceName + ":artefact:*"
		iter := rdb.Scan(ctx, 0, pattern, 0).Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			artefactID := key[len("holt:"+env.InstanceName+":artefact:"):]

			art, err := bbClient.GetArtefact(ctx, artefactID)
			if err != nil {
				continue
			}

			if art.StructuralType == blackboard.StructuralTypeQuestion {
				questionArtefact = art
				break
			}
		}
		if err := iter.Err(); err != nil {
			t.Logf("Scan error: %v", err)
		}

		if questionArtefact != nil {
			break
		}
	}

	if questionArtefact == nil {
		env.DumpInstanceLogs()
	}

	require.NotNil(t, questionArtefact, "Question artefact should have been created")
	t.Logf("✓ Question artefact created: %s", questionArtefact.ID)

	// Verify Question payload
	var questionPayload struct {
		QuestionText     string `json:"question_text"`
		TargetArtefactID string `json:"target_artefact_id"`
	}
	err = json.Unmarshal([]byte(questionArtefact.Payload), &questionPayload)
	require.NoError(t, err)
	require.Equal(t, goalArtefact.ID, questionPayload.TargetArtefactID)
	t.Logf("✓ Question text: \"%s\"", questionPayload.QuestionText)

	// Step 6: Verify original claim was terminated
	t.Log("Step 6: Verifying original claim was terminated...")
	originalClaim, err := bbClient.GetClaimByArtefactID(ctx, goalArtefact.ID)
	require.NoError(t, err)
	require.Equal(t, blackboard.ClaimStatusTerminated, originalClaim.Status)
	require.Contains(t, originalClaim.TerminationReason, "Question")
	t.Logf("✓ Original claim terminated: %s", originalClaim.TerminationReason)

	// Step 7: Run `holt questions` to list the question
	t.Log("Step 7: Running 'holt questions' command...")
	questionsInstanceName = env.InstanceName
	questionsSince = "10m" // Show questions from last 10 minutes

	// We can't easily call runQuestions directly since it writes to stdout,
	// so let's verify we can query for unanswered questions using the internal logic
	unanswered, err := getUnansweredQuestions(ctx, bbClient)
	require.NoError(t, err)
	require.Len(t, unanswered, 1, "Should have exactly 1 unanswered question")
	require.Equal(t, questionArtefact.ID, unanswered[0].ID)
	t.Logf("✓ 'holt questions' would show: %s", questionArtefact.ID[:8])

	// Step 8: Human answers the question via `holt answer`
	t.Log("Step 8: Answering question with clarified goal...")
	clarifiedGoal := "Build a REST API with JWT authentication and null handling"

	answerArtefact := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalArtefact.ID, questionArtefact.ID},
		LogicalThreadID: goalArtefact.LogicalID, // Same logical thread
		Version:         goalArtefact.Version + 1, // Incremented version
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  "user",
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		ClaimID:         "",
	}, clarifiedGoal)

	t.Logf("✓ Clarified goal created: %s v%d", answerArtefact.ID, answerArtefact.Version)

	// Step 9: Verify new claim is created for clarified goal
	t.Log("Step 9: Waiting for new claim on clarified goal...")
	var newClaim *blackboard.Claim
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

		claim, err := bbClient.GetClaimByArtefactID(ctx, answerArtefact.ID)
		if err == nil && claim != nil {
			newClaim = claim
			break
		}
	}

	if newClaim == nil {
		env.DumpInstanceLogs()
	}

	require.NotNil(t, newClaim, "New claim should have been created for clarified goal")
	t.Logf("✓ New claim created: %s (status: %s)", newClaim.ID, newClaim.Status)

	// Step 10: Verify the question is now "answered" (unanswered list should be empty)
	t.Log("Step 10: Verifying question is marked as answered...")
	unanswered, err = getUnansweredQuestions(ctx, bbClient)
	require.NoError(t, err)
	require.Len(t, unanswered, 0, "Question should now be answered")
	t.Log("✓ Question marked as answered")

	t.Log("=== E2E Test Complete ===")
	t.Log("✓ Agent-to-Human Question/Answer flow works end-to-end")
}

// TestE2E_M4_1_IterationLimit validates iteration limit enforcement for Questions
func TestE2E_M4_1_IterationLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M4.1 E2E: Question Iteration Limit ===")

	// Setup with max_review_iterations = 1 (very low for testing)
	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 1
  timestamp_drift_tolerance_ms: 600000 # 10 minutes
agents:
  QuestionAgent:
    image: "question-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: ro
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		_ = runDown(downCmd, []string{})
	}()

	// Start instance
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err := runUp(upCmd, []string{})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Connect to blackboard
	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Logf("✓ Connected to blackboard (Redis port: %d)", env.RedisPort)

	// Create artefact at version 2 (iteration count = 1, at limit)
	// Must be GoalDefined for question-agent to ask a question
	targetArtefact := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // Already at iteration limit
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  "user",
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		ClaimID:         "",
	}, "Build API v2")

	t.Logf("✓ Created artefact at version 2: %s", targetArtefact.ID)

	// Wait for Question (agent will try to question it)
	time.Sleep(5 * time.Second)

	// Verify Failure artefact was created due to iteration limit
	t.Log("Checking for Failure artefact...")
	var failureFound bool
	rdb := bbClient.GetRedisClient()
	pattern := "holt:" + env.InstanceName + ":artefact:*"
	iter := rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		artefactID := key[len("holt:"+env.InstanceName+":artefact:"):]

		art, err := bbClient.GetArtefact(ctx, artefactID)
		if err != nil {
			continue
		}

		if art.StructuralType == blackboard.StructuralTypeFailure &&
			art.Type == "MaxIterationsExceeded" {
			failureFound = true
			t.Logf("✓ Failure artefact created: %s", art.ID)
			t.Logf("  Payload: %s", art.Payload)
			break
		}
	}

	require.True(t, failureFound, "Failure artefact should have been created for exceeding iteration limit")
	t.Log("✓ Iteration limit enforcement works correctly")
}
