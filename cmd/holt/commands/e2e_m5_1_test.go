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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_M5_1_NamedPattern validates the Named Pattern synchronization:
// 1. Create CodeCommit ancestor
// 2. Create 3 distinct prerequisite types (TestResult, LintResult, SecurityScan)
// 3. Synchronizer should wait for all 3
// 4. When all present, synchronizer bids and wins
// 5. Synchronizer receives ancestor + all descendants in context
func TestE2E_M5_1_NamedPattern(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.1 E2E: Named Pattern Fan-In Synchronization ===")

	projectRoot := testutil.GetProjectRoot()

	// Build test agents
	t.Log("Building test agent Docker images...")

	// Build producer agent (creates TestResult, LintResult, SecurityScan)
	buildCmd := exec.Command("docker", "build",
		"-t", "m5-1-producer:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getProducerAgentDockerfile()
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Producer build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build producer agent")
	t.Log("✓ Producer agent built")

	// Build synchronizer agent (waits for all 3 types)
	buildCmd = exec.Command("docker", "build",
		"-t", "m5-1-synchronizer:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getSynchronizerAgentDockerfile()
	output, err = buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Synchronizer build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build synchronizer agent")
	t.Log("✓ Synchronizer agent built")

	// Setup environment with synchronizer configuration
	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  Producer:
    image: "m5-1-producer:latest"
    command: ["/app/produce.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["CodeCommit"]
  Synchronizer:
    image: "m5-1-synchronizer:latest"
    command: ["/app/synchronize.sh"]
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"
        - type: "LintResult"
        - type: "SecurityScan"
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

	t.Logf("✓ Environment setup: %s", env.InstanceName)

	// Start instance
	t.Log("Starting Holt instance...")
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err)
	t.Log("✓ Instance started")

	// Wait for orchestrator to be ready
	time.Sleep(1 * time.Second)

	// Connect to blackboard
	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Connected to blackboard")

	// M4.7: Create proper workflow spine (Manifest → Goal)
	_, goalID := env.CreateWorkflowSpine(ctx, "Build and test the application")

	// Step 1: Create CodeCommit ancestor (as continuation of goal)
	t.Log("Step 1: Creating CodeCommit ancestor...")
	codeCommit := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation of workflow
		Type:            "CodeCommit",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "commit-abc123")
	t.Logf("✓ CodeCommit created: %s", codeCommit.ID)

	// Wait for Producer to claim (should bid on CodeCommit)
	time.Sleep(1 * time.Second)

	// Step 2: Create first 2 prerequisites (TestResult, LintResult)
	t.Log("Step 2: Creating partial prerequisites...")

	testResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID}, // Child of CodeCommit
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "TestResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "tests-passed")
	t.Logf("✓ TestResult created: %s", testResult.ID)

	lintResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID}, // Child of CodeCommit
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "LintResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "lint-clean")
	t.Logf("✓ LintResult created: %s", lintResult.ID)

	// Wait and verify Synchronizer does NOT bid yet
	time.Sleep(500 * time.Millisecond)

	// Check no DeployResult exists yet (Synchronizer hasn't run)
	t.Log("Verifying Synchronizer has NOT bid yet (SecurityScan missing)...")
	exists := artefactTypeExists(ctx, bbClient, "DeployResult")
	require.False(t, exists, "Synchronizer should not have run yet (SecurityScan missing)")
	t.Log("✓ Synchronizer correctly waiting")

	// Step 3: Create final prerequisite (SecurityScan)
	t.Log("Step 3: Creating final prerequisite (SecurityScan)...")

	securityScan := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID}, // Child of CodeCommit
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "SecurityScan",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "no-vulnerabilities")
	t.Logf("✓ SecurityScan created: %s", securityScan.ID)

	// Step 4: Wait for Synchronizer to bid and execute
	t.Log("Step 4: Waiting for Synchronizer to bid and execute...")
	time.Sleep(1 * time.Second)

	// Verify DeployResult was created
	deployResult := waitForArtefactType(ctx, t, bbClient, "DeployResult", 45*time.Second)
	if deployResult == nil {
		env.DumpInstanceLogs()
	}
	require.NotNil(t, deployResult, "DeployResult should exist (Synchronizer succeeded)")
	t.Logf("✓ Synchronizer executed successfully: DeployResult %s", deployResult.ID)

	// Verify DeployResult payload contains confirmation
	require.Contains(t, deployResult.Payload.Content, "synchronized", "DeployResult should confirm synchronization")

	t.Log("=== Named Pattern E2E Test PASSED ===")
}

// TestE2E_M5_1_ProducerDeclared validates the Producer-Declared Pattern:
// 1. Create DataBatch ancestor
// 2. Producer creates N ProcessedRecord artefacts with batch_size=N metadata
// 3. Synchronizer reads metadata and waits for N records
// 4. When all N present, synchronizer aggregates them
func TestE2E_M5_1_ProducerDeclared(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.1 E2E: Producer-Declared Pattern (Dynamic Count) ===")

	projectRoot := testutil.GetProjectRoot()
	t.Logf("DEBUG: Project Root: %s", projectRoot)
	checkLs := exec.Command("ls", "-R", "internal/pup")
	checkLs.Dir = projectRoot
	out, _ := checkLs.CombinedOutput()
	t.Logf("DEBUG: internal/pup content:\n%s", string(out))

	// Build multi-artefact producer (outputs 5 ProcessedRecords)
	t.Log("Building multi-output producer...")
	buildCmd := exec.Command("docker", "build",
		"--no-cache",
		"-t", "m5-1-multi-producer-new:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getMultiProducerDockerfile()
	output, err := buildCmd.CombinedOutput()
	t.Logf("Multi-producer build output:\n%s", string(output))
	require.NoError(t, err)
	t.Log("✓ Multi-producer built")

	// Build aggregator (synchronizes on batch_size)
	buildCmd = exec.Command("docker", "build",
		"--no-cache",
		"-t", "m5-1-aggregator:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getAggregatorDockerfile()
	output, err = buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Aggregator build output:\n%s", string(output))
	}
	require.NoError(t, err)
	t.Log("✓ Aggregator built")

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  MultiProducer:
    image: "m5-1-multi-producer-new:latest"
    command: ["/app/produce-multi.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["DataBatch"]
  Aggregator:
    image: "m5-1-aggregator:latest"
    command: ["/app/aggregate.sh"]
    synchronize:
      ancestor_type: "GoalDefined"
      wait_for:
        - type: "ProcessedRecord"
          count_from_metadata: "batch_size"
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

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Instance ready")

	// M4.7: Create proper workflow spine
	_, goalID := env.CreateWorkflowSpine(ctx, "Process batch data")

	// Create DataBatch (trigger for MultiProducer to execute)
	t.Log("Creating DataBatch...")
	dataBatch := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "DataBatch",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",  // Not used by Aggregator - it reads metadata from ProcessedRecords
	}, "batch-123")
	t.Logf("✓ DataBatch created: %s", dataBatch.ID)

	// Wait for Orchestrator to create claim for DataBatch
	t.Log("Waiting for DataBatch claim to be created...")
	time.Sleep(500 * time.Millisecond)

	// Verify claim exists for DataBatch
	claim, err := bbClient.GetClaimByArtefactID(ctx, dataBatch.ID)
	if err != nil || claim == nil {
		env.DumpInstanceLogs()
		require.NoError(t, err, "DataBatch claim should exist")
		require.NotNil(t, claim, "DataBatch claim should not be nil")
	}
	t.Logf("✓ DataBatch claim created: %s (status=%s)", claim.ID, claim.Status)

	// Wait for MultiProducer to bid, get granted, execute, and create 5 ProcessedRecords
	t.Log("Waiting for MultiProducer to execute and create 5 ProcessedRecords...")
	time.Sleep(2 * time.Second)  // Increased wait time for claim processing + execution

	// Verify 5 ProcessedRecords exist
	records, _ := testutil.FindAllArtefactsOfType(ctx, bbClient, "ProcessedRecord")
	if len(records) != 5 {
		env.DumpInstanceLogs()
		// Debug: check for failures
		failures, _ := testutil.FindAllArtefactsOfType(ctx, bbClient, "Failure")
		for _, f := range failures {
			t.Logf("FAILURE DETECTED: %s", f.Payload.Content)
		}
		assert.Equal(t, 5, len(records), "Should have 5 ProcessedRecords, found failures: %d", len(failures))
	}

	// Verify metadata injection
	for i, record := range records {
		var metadata map[string]string
		if record.Header.Metadata != "" {
			_ = json.Unmarshal([]byte(record.Header.Metadata), &metadata)
		}

		if metadata == nil {
			metadata = make(map[string]string)
		}

		if metadata["batch_size"] != "5" {
			env.DumpInstanceLogs()
		}
		assert.Equal(t, "5", metadata["batch_size"], "Record %d should have batch_size=5", i)
	}
	t.Log("✓ All 5 ProcessedRecords created with correct metadata")

	// Debug: Check claims for ProcessedRecords
	t.Log("Checking claims for ProcessedRecords...")
	for i, record := range records {
		claim, err := bbClient.GetClaimByArtefactID(ctx, record.ID)
		if err != nil {
			t.Logf("  ProcessedRecord %d (%s): No claim found - %v", i+1, record.ID, err)
		} else {
			t.Logf("  ProcessedRecord %d (%s): Claim %s (status=%s)", i+1, record.ID, claim.ID, claim.Status)
		}
	}

	// Wait for Aggregator to synchronize
	t.Log("Waiting for Aggregator to synchronize...")
	time.Sleep(2 * time.Second) // Increased wait time for claim processing

	// Verify AggregatedReport was created
	report := waitForArtefactType(ctx, t, bbClient, "AggregatedReport", 45*time.Second)
	if report == nil {
		t.Log("AggregatedReport not found - dumping logs...")
		env.DumpInstanceLogs()
	}
	require.NotNil(t, report, "AggregatedReport should exist")
	t.Logf("✓ Aggregator synchronized: %s", report.ID)

	require.Contains(t, report.Payload.Content, "5 records", "Report should mention 5 records")

	t.Log("=== Producer-Declared E2E Test PASSED ===")
}

// TestE2E_M5_1_DeduplicationLock validates the deduplication lock prevents double-bidding:
// 1. Create ancestor
// 2. Create final two prerequisites simultaneously (simulates race condition)
// 3. Both trigger synchronizer evaluation
// 4. Only ONE synchronizer bid should be submitted (deduplication lock)
// 5. Only ONE DeployResult should be created
func TestE2E_M5_1_DeduplicationLock(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.1 E2E: Deduplication Lock (Safety Check) ===")

	projectRoot := testutil.GetProjectRoot()

	// Build synchronizer with controller/worker (2 max_concurrent workers)
	t.Log("Building concurrent synchronizer...")
	buildCmd := exec.Command("docker", "build",
		"-t", "m5-1-concurrent-sync-debug:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getConcurrentSynchronizerDockerfile()
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Concurrent sync build output:\n%s", string(output))
	}
	require.NoError(t, err)
	t.Log("✓ Concurrent synchronizer built")

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  ConcurrentSync:
    image: m5-1-concurrent-sync-debug:latest
    command: ["/app/sync-concurrent.sh"]
    worker:
      workspace:
        mode: copy
    inputs:
      - type: "CodeCommit"
    environment:
      # HOLT_MODE default (traditional) is required for execution
      - "HOLT_LOG_LEVEL=debug"
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"
        - type: "LintResult"
  DummyConsumer:
    image: "m5-1-concurrent-sync-debug:latest" # Use same image as ConcurrentSync
    command: ["/app/pup", "controller"]
    bidding_strategy:
      type: "ignore"
      target_types: ["CodeCommit"]
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		if t.Failed() {
			env.DumpInstanceLogs()
		}
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		_ = runDown(downCmd, []string{})
		t.Log("✓ Cleanup complete")
	}()

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{"--build"})
	require.NoError(t, err)
	t.Log("Waiting for Orchestrator to subscribe...")
	time.Sleep(2 * time.Second) // Wait for Orchestrator to be fully ready

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Instance ready with 2 concurrent workers")

	// Debug Pub/Sub: Subscribe in test to verify messages
	sub, err := bbClient.SubscribeArtefactEvents(ctx)
	require.NoError(t, err)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Ignore panics during test cleanup (subscription closed)
				t.Logf("DEBUG: Subscription goroutine exiting (expected during cleanup)")
			}
		}()
		for start := time.Now(); time.Since(start) < 60*time.Second; {
			select {
			case evt := <-sub.Events():
				if evt != nil {
					t.Logf("DEBUG: Test received artefact event: %s (%s)", evt.ID, evt.Header.Type)
				}
			case <-time.After(1 * time.Second):
			}
		}
	}()

	// M4.7: Create proper workflow spine
	_, goalID := env.CreateWorkflowSpine(ctx, "Test concurrent synchronization")

	// Create ancestor
	t.Log("Creating CodeCommit...")
	codeCommit := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "CodeCommit",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "commit-xyz")
	t.Logf("✓ CodeCommit created: %s", codeCommit.ID)

	// Wait for Orchestrator to create the claim for CodeCommit (Async)
	// This ensures the Synchronizer can find it when TestResult arrives.
	t.Log("Waiting for CodeCommit claim creation...")
	require.Eventually(t, func() bool {
		claim, err := bbClient.GetClaimByArtefactID(ctx, codeCommit.ID)
		return err == nil && claim != nil
	}, 10*time.Second, 100*time.Millisecond, "Timed out waiting for CodeCommit claim")
	t.Log("✓ CodeCommit claim confirmed")
	// Manual claim workaround removed to avoid conflict with Orchestrator-created claim

	// Create both final prerequisites nearly simultaneously (race condition)
	t.Log("Creating TestResult and LintResult simultaneously...")

	testResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "TestResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "passed")

	// Create second artefact immediately (race condition)
	lintResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // V2+ continuation
		Type:            "LintResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "clean")

	t.Logf("✓ Both prerequisites created (race): TestResult=%s, LintResult=%s", testResult.ID, lintResult.ID)

	// Wait for synchronizer to execute
	time.Sleep(1 * time.Second)

	// Verify ONLY ONE DeployResult was created (deduplication worked)
	deploys := getArtefactsByType(ctx, bbClient, "DeployResult")
	require.Len(t, deploys, 1, "Deduplication lock should prevent double execution (only 1 DeployResult)")
	t.Logf("✓ Deduplication successful: Only 1 DeployResult created")

	t.Log("=== Deduplication Lock E2E Test PASSED ===")
}

// TestE2E_M5_1_RecursiveTraversal validates recursive descendant traversal:
// 1. Create CodeCommit ancestor
// 2. Create BuildResult (child of CodeCommit)
// 3. Create TestResult (grandchild - child of BuildResult)
// 4. Create LintResult (direct child of CodeCommit)
// 5. Synchronizer should find TestResult at depth=2 (recursive traversal)
func TestE2E_M5_1_RecursiveTraversal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.1 E2E: Recursive Descendant Traversal (Depth=2) ===")

	projectRoot := testutil.GetProjectRoot()

	// Build synchronizer that waits for TestResult + LintResult (no max_depth limit)
	t.Log("Building recursive synchronizer...")
	buildCmd := exec.Command("docker", "build",
		"-t", "m5-1-recursive-sync:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getSynchronizerAgentDockerfile()
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err)
	t.Log("✓ Recursive synchronizer built")

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  RecursiveSync:
    image: "m5-1-recursive-sync:latest"
    command: ["/app/synchronize.sh"]
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"   # At depth=2 (grandchild)
        - type: "LintResult"   # At depth=1 (direct child)
      max_depth: 0  # Unlimited - should find all depths
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

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Instance ready")

	// Create tree structure:
	//   CodeCommit
	//   ├── BuildResult
	//   │   └── TestResult (depth=2)
	//   └── LintResult (depth=1)

	t.Log("Creating CodeCommit ancestor...")
	codeCommit := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         1,
		Type:            "CodeCommit",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "commit-recursive")
	t.Logf("✓ CodeCommit: %s", codeCommit.ID)

	t.Log("Creating BuildResult (depth=1)...")
	buildResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "BuildResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "build-ok")
	t.Logf("✓ BuildResult: %s", buildResult.ID)

	t.Log("Creating TestResult (depth=2 - grandchild)...")
	testResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{buildResult.ID}, // Child of BuildResult, not CodeCommit!
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "TestResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "tests-passed")
	t.Logf("✓ TestResult (grandchild): %s", testResult.ID)

	// Verify synchronizer does NOT bid yet (LintResult missing)
	time.Sleep(500 * time.Millisecond)
	exists := artefactTypeExists(ctx, bbClient, "DeployResult")
	require.False(t, exists, "Synchronizer should wait for LintResult")
	t.Log("✓ Synchronizer waiting (LintResult missing)")

	t.Log("Creating LintResult (depth=1)...")
	lintResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "LintResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "lint-clean")
	t.Logf("✓ LintResult: %s", lintResult.ID)

	// Now both dependencies met (TestResult at depth=2, LintResult at depth=1)
	t.Log("Waiting for synchronizer (should find TestResult at depth=2)...")
	time.Sleep(1 * time.Second)

	deployResult := waitForArtefactType(ctx, t, bbClient, "DeployResult", 10*time.Second)
	require.NotNil(t, deployResult, "Synchronizer should find TestResult at depth=2")
	t.Logf("✓ Synchronizer found grandchild: %s", deployResult.ID)

	t.Log("=== Recursive Traversal E2E Test PASSED ===")
}

// TestE2E_M5_1_MaxDepthLimiting validates max_depth limiting:
// 1. Create same tree as RecursiveTraversal test
// 2. Synchronizer has max_depth=1 (only direct children)
// 3. TestResult is at depth=2 (grandchild) - should NOT be found
// 4. Synchronizer should NOT bid (TestResult is too deep)
func TestE2E_M5_1_MaxDepthLimiting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.1 E2E: Max Depth Limiting (max_depth=1) ===")

	projectRoot := testutil.GetProjectRoot()

	// Build synchronizer with max_depth=1
	t.Log("Building depth-limited synchronizer...")
	buildCmd := exec.Command("docker", "build",
		"-t", "m5-1-depth-limited:latest",
		"-f", "-",
		".")
	buildCmd.Dir = projectRoot
	buildCmd.Stdin = getSynchronizerAgentDockerfile()
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err)
	t.Log("✓ Depth-limited synchronizer built")

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  DepthLimitedSync:
    image: "m5-1-depth-limited:latest"
    command: ["/app/synchronize.sh"]
    synchronize:
      ancestor_type: "CodeCommit"
      wait_for:
        - type: "TestResult"   # At depth=2 (too deep!)
        - type: "LintResult"   # At depth=1 (OK)
      max_depth: 1  # Only direct children
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

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Instance ready")

	// Create same tree structure as RecursiveTraversal
	t.Log("Creating CodeCommit ancestor...")
	codeCommit := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{},
		LogicalThreadID: blackboard.NewID(),
		Version:         1,
		Type:            "CodeCommit",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "commit-depth-test")
	t.Logf("✓ CodeCommit: %s", codeCommit.ID)

	t.Log("Creating BuildResult (depth=1)...")
	buildResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "BuildResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "build-ok")
	t.Logf("✓ BuildResult: %s", buildResult.ID)

	t.Log("Creating TestResult (depth=2 - TOO DEEP)...")
	testResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{buildResult.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "TestResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "tests-passed")
	t.Logf("✓ TestResult (grandchild): %s", testResult.ID)

	t.Log("Creating LintResult (depth=1 - OK)...")
	lintResult := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{codeCommit.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2, // M4.7: Version>1 to bypass root manifest validation
		Type:            "LintResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "lint-clean")
	t.Logf("✓ LintResult: %s", lintResult.ID)

	// Wait and verify synchronizer does NOT bid
	// TestResult is at depth=2, but max_depth=1, so it won't be found
	t.Log("Waiting to verify synchronizer does NOT bid (TestResult too deep)...")
	time.Sleep(1 * time.Second)

	exists := artefactTypeExists(ctx, bbClient, "DeployResult")
	require.False(t, exists, "Synchronizer should NOT bid (TestResult at depth=2, max_depth=1)")
	t.Log("✓ Max depth limiting working correctly (TestResult not found)")

	t.Log("=== Max Depth Limiting E2E Test PASSED ===")
}

// Helper functions

func artefactTypeExists(ctx context.Context, bbClient *blackboard.Client, artefactType string) bool {
	artefacts := getArtefactsByType(ctx, bbClient, artefactType)
	return len(artefacts) > 0
}

func waitForArtefactType(ctx context.Context, t *testing.T, bbClient *blackboard.Client, artefactType string, timeout time.Duration) *blackboard.Artefact {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		artefacts := getArtefactsByType(ctx, bbClient, artefactType)
		if len(artefacts) > 0 {
			return artefacts[0]
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func getArtefactsByType(ctx context.Context, bbClient *blackboard.Client, artefactType string) []*blackboard.Artefact {
	// Scan all artefacts and filter by type
	var results []*blackboard.Artefact

	// Use ScanArtefacts to find all artefact IDs
	artefactIDs, err := bbClient.ScanArtefacts(ctx, "")
	if err != nil {
		return results
	}

	for _, artefactID := range artefactIDs {
		artefact, err := bbClient.GetArtefact(ctx, artefactID)
		if err == nil && artefact != nil && artefact.Header.Type == artefactType {
			results = append(results, artefact)
		}
	}

	return results
}

// Dockerfile generators (inline for simplicity)

func getProducerAgentDockerfile() *testutil.StringReader {
	dockerfile := `# Build stage - compile pup
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache bash ca-certificates
WORKDIR /app
COPY --from=builder /build/pup /app/pup
COPY <<'EOF' /app/produce.sh
#!/bin/bash
set -e

# Read input from stdin (not used for this simple producer)
cat > /dev/null

# Produce a single artefact (CodeCommit trigger will create prerequisites)
cat <<RESULT >&3
{"artefact_type":"TriggerComplete","artefact_payload":"triggered","summary":"Producer triggered"}
RESULT
EOF
RUN chmod +x /app/produce.sh
RUN adduser -D -u 1000 agent
USER agent
ENTRYPOINT ["/app/pup"]
`
	return testutil.NewStringReader(dockerfile)
}

func getSynchronizerAgentDockerfile() *testutil.StringReader {
	dockerfile := `# Build stage - compile pup
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache bash jq ca-certificates
WORKDIR /app
COPY --from=builder /build/pup /app/pup
COPY <<'EOF' /app/synchronize.sh
#!/bin/bash
set -e

# Read synchronizer context from stdin
INPUT=$(cat)

# Extract ancestor and descendants
ANCESTOR=$(echo "$INPUT" | jq -r '.ancestor_artefact.payload // ""')
DESCENDANTS=$(echo "$INPUT" | jq -r '.descendant_artefacts | length')

# Create synchronized result
cat <<RESULT >&3
{"artefact_type":"DeployResult","artefact_payload":"synchronized from $ANCESTOR with $DESCENDANTS descendants","summary":"Deployment synchronized"}
RESULT
EOF
RUN chmod +x /app/synchronize.sh
RUN adduser -D -u 1000 agent
USER agent
ENTRYPOINT ["/app/pup"]
`
	return testutil.NewStringReader(dockerfile)
}

func getMultiProducerDockerfile() *testutil.StringReader {
	dockerfile := `# Build stage - compile pup
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache bash ca-certificates
WORKDIR /app
COPY --from=builder /build/pup /app/pup
COPY <<'EOF' /app/produce-multi.sh
#!/bin/bash
set -e
cat > /dev/null

# Produce 5 ProcessedRecord artefacts (Pup will inject batch_size=5 metadata automatically)
for i in {1..5}; do
  cat <<RECORD >&3
{"artefact_type":"ProcessedRecord","artefact_payload":"record-$i","summary":"Processed record $i"}
RECORD
done
EOF
RUN chmod +x /app/produce-multi.sh
RUN adduser -D -u 1000 agent
USER agent
ENTRYPOINT ["/app/pup"]
`
	return testutil.NewStringReader(dockerfile)
}

func getAggregatorDockerfile() *testutil.StringReader {
	dockerfile := `# Build stage - compile pup
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache bash jq ca-certificates
WORKDIR /app
COPY --from=builder /build/pup /app/pup
COPY <<'EOF' /app/aggregate.sh
#!/bin/bash
set -e
INPUT=$(cat)
COUNT=$(echo "$INPUT" | jq -r '.descendant_artefacts | map(select(.header.type == "ProcessedRecord")) | length')

cat <<RESULT >&3
{"artefact_type":"AggregatedReport","artefact_payload":"Aggregated $COUNT records","summary":"Aggregation complete"}
RESULT
EOF
RUN chmod +x /app/aggregate.sh
RUN adduser -D -u 1000 agent
USER agent
ENTRYPOINT ["/app/pup"]
`
	return testutil.NewStringReader(dockerfile)
}

func getConcurrentSynchronizerDockerfile() *testutil.StringReader {
	dockerfile := `# Build stage - compile pup
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache bash ca-certificates
WORKDIR /app
COPY --from=builder /build/pup /app/pup
COPY <<'EOF' /app/sync-concurrent.sh
#!/bin/bash
set -e
cat > /dev/null

# Simple output (deduplication lock prevents multiple executions)
cat <<RESULT >&3
{"artefact_type":"DeployResult","artefact_payload":"deployed","summary":"Deployment complete"}
RESULT
EOF
RUN chmod +x /app/sync-concurrent.sh
RUN adduser -D -u 1000 agent
USER agent
ENTRYPOINT ["/app/pup"]
`
	return testutil.NewStringReader(dockerfile)
}
