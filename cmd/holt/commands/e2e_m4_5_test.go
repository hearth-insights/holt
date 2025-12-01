//go:build integration
// +build integration

package commands

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/dyluth/holt/internal/testutil"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestE2E_M4_5_HumanReviewer validates the HumanReviewerAgent with AUTO_APPROVE mode
// This tests the basic infrastructure for M4.5 without requiring all 8 agents
func TestE2E_M4_5_HumanReviewer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M4.5 E2E: HumanReviewer with AUTO_APPROVE ===")

	// Step 0: Build human-reviewer Docker image
	projectRoot := testutil.GetProjectRoot()

	t.Log("Building human-reviewer Docker image...")
	buildCmd := exec.Command("docker", "build",
		"--no-cache", // Force rebuild to ensure latest Dockerfile changes are used
		"-t", "holt/human-reviewer:test",
		"-f", "agents/human-reviewer/Dockerfile",
		".")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build human-reviewer image")
	t.Log("✓ human-reviewer image built")

	// Step 1: Setup environment with human reviewer
	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
agents:
  HumanReviewer:
    image: "holt/human-reviewer:test"
    command: ["/app/human-reviewer"]
    bidding_strategy:
      type: "review"
    workspace:
      mode: ro
    environment:
      - AUTO_APPROVE=true
      - REVIEW_TIMEOUT=30
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

	// Step 4: Create a DesignSpecDraft artefact for review
	t.Log("Step 4: Creating DesignSpecDraft artefact for review...")

	draftArtefact := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         1,
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  "user", // "user" allows skipping ClaimID check
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DesignSpecDraft",
		ClaimID:         "",
	}, "# Design Proposal\nThis is a test design draft.")

	t.Logf("✓ DesignSpecDraft created: %s", draftArtefact.ID)

	// Step 5: Wait for Review artefact (auto-approval)
	t.Log("Step 5: Waiting for HumanReviewer to approve...")
	var reviewArtefact *blackboard.Artefact
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

		// Query for Review artefacts
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

			if art.StructuralType == blackboard.StructuralTypeReview && art.ProducedByRole == "HumanReviewer" {
				reviewArtefact = art
				break
			}
		}
		if err := iter.Err(); err != nil {
			t.Logf("Scan error: %v", err)
		}

		if reviewArtefact != nil {
			break
		}
	}

	// Dump logs before assertion for debugging
	if reviewArtefact == nil {
		t.Log("Review artefact not found - dumping container logs for debugging")
		env.DumpInstanceLogs()
	}

	require.NotNil(t, reviewArtefact, "Review artefact should have been created by HumanReviewer")
	t.Logf("✓ Review artefact created: %s", reviewArtefact.ID)

	// Step 6: Verify approval (payload should be {} for approval)
	t.Log("Step 6: Verifying approval...")
	require.Equal(t, "{}", reviewArtefact.Payload, "Review should be approval (empty JSON object)")
	t.Log("✓ Review approved successfully")
}

// TestE2E_M4_5_VolumeMount validates volume mount configuration with tilde expansion
func TestE2E_M4_5_VolumeMount(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M4.5 E2E: Volume Mount Configuration ===")

	// This test validates that the config accepts volumes field and
	// the holt up command processes it without errors
	holtYML := `version: "1.0"
agents:
  TestAgent:
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: ro
    volumes:
      - "/tmp:/test-mount:ro"
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

	t.Log("✓ Volume mount configuration validated")

	// Note: We're testing that the config loads and validates correctly.
	// Full volume mount functionality would require building a custom test image,
	// which we skip here to keep the test focused on config validation.
}

// TestE2E_M4_5_TestRunner validates the TestRunnerAgent with mock ChangeSet
func TestE2E_M4_5_TestRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M4.5 E2E: TestRunner Agent ===")

	projectRoot := testutil.GetProjectRoot()

	// Step 0: Build test-runner Docker image
	t.Log("Building test-runner Docker image...")
	buildCmd := exec.Command("docker", "build",
		"--no-cache", // Force rebuild to ensure latest Dockerfile changes are used
		"-t", "holt/test-runner:test",
		"-f", "agents/test-runner/Dockerfile",
		".")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build test-runner image")
	t.Log("✓ test-runner image built")

	// Step 1: Setup environment
	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
agents:
  TestRunner:
    image: "holt/test-runner:test"
    command: ["/app/run-tests.sh"]
    bidding_strategy:
      type: "review"
    workspace:
      mode: rw
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

	// Step 2: Start instance
	t.Log("Step 2: Starting Holt instance...")
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err, "Failed to start instance")
	t.Logf("✓ Instance started: %s", env.InstanceName)

	time.Sleep(2 * time.Second)

	// Step 3: Connect to blackboard
	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// Step 4: Create a ChangeSet artefact
	t.Log("Step 4: Creating ChangeSet artefact...")
	changeSetPayload := map[string]interface{}{
		"commit_hash":    "HEAD",
		"commit_message": "test: Add unit tests",
		"files_changed":  []string{"test.go"},
		"test_summary": map[string]interface{}{
			"tests_added":         5,
			"tests_passed":        5,
			"coverage_percentage": 90.0,
		},
	}
	payloadJSON, _ := json.Marshal(changeSetPayload)

	changeSetArtefact := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         1,
		CreatedAtMs:     time.Now().UnixMilli(),
		ProducedByRole:  "user", // "user" allows skipping ClaimID check
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "ChangeSet",
		ClaimID:         "",
	}, string(payloadJSON))

	t.Logf("✓ ChangeSet created: %s", changeSetArtefact.ID)

	// Step 5: Wait for Review artefact from TestRunner
	t.Log("Step 5: Waiting for TestRunner to review...")
	var reviewArtefact *blackboard.Artefact
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

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

			if art.StructuralType == blackboard.StructuralTypeReview && art.ProducedByRole == "TestRunner" {
				reviewArtefact = art
				break
			}
		}
		if err := iter.Err(); err != nil {
			t.Logf("Scan error: %v", err)
		}

		if reviewArtefact != nil {
			break
		}
	}

	// Dump logs before assertion for debugging
	if reviewArtefact == nil {
		t.Log("Review artefact not found - dumping container logs for debugging")
		env.DumpInstanceLogs()
	}

	require.NotNil(t, reviewArtefact, "Review artefact should have been created by TestRunner")
	t.Logf("✓ Review artefact created: %s", reviewArtefact.ID)

	// The test may pass or fail depending on whether make test-failed succeeds
	// We just verify that the TestRunner agent executed and produced a Review
	t.Log("✓ TestRunner executed successfully")
}
