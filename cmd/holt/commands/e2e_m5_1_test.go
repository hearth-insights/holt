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

	// Connect to blackboard
	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient
	t.Log("✓ Connected to blackboard")

	// M4.7: Create proper workflow spine (Manifest → Goal)
	_, goalID := env.CreateWorkflowSpine(ctx, "Build and test the application")

	// Wait for GoalDefined claim to be created (ensures orchestrator is ready)
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second),
		"Orchestrator should create claim for GoalDefined")
	t.Log("✓ Orchestrator is ready (GoalDefined claim exists)")

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

	// Wait for CodeCommit claim to be created and completed (Producer should process it)
	require.True(t, waitForClaimCreated(ctx, bbClient, codeCommit.ID, 5*time.Second),
		"Orchestrator should create claim for CodeCommit")
	t.Log("✓ CodeCommit claim created")

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

	// Wait for claims to be created for prerequisites
	require.True(t, waitForClaimCreated(ctx, bbClient, testResult.ID, 5*time.Second),
		"TestResult claim should be created")
	require.True(t, waitForClaimCreated(ctx, bbClient, lintResult.ID, 5*time.Second),
		"LintResult claim should be created")

	// Verify Synchronizer has NOT created DeployResult yet (SecurityScan missing)
	// Give it a brief moment to ensure it's not racing ahead
	t.Log("Verifying Synchronizer has NOT bid yet (SecurityScan missing)...")
	time.Sleep(1 * time.Second) // Brief stabilization period
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

	// Wait for SecurityScan claim to be created
	require.True(t, waitForClaimCreated(ctx, bbClient, securityScan.ID, 5*time.Second),
		"SecurityScan claim should be created")
	t.Log("✓ All prerequisite claims created")

	// Step 4: Wait for Synchronizer to bid, win, and execute
	t.Log("Step 4: Waiting for Synchronizer to bid and execute...")
	// Synchronizer should now detect all conditions met and bid
	deployResult := waitForArtefactType(ctx, t, bbClient, "DeployResult", 30*time.Second)
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

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// M4.7: Create proper workflow spine
	_, goalID := env.CreateWorkflowSpine(ctx, "Process batch data")

	// Wait for orchestrator to be ready
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second),
		"Orchestrator should create claim for GoalDefined")
	t.Log("✓ Orchestrator ready")

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

	// Wait for DataBatch claim to be created
	require.True(t, waitForClaimCreated(ctx, bbClient, dataBatch.ID, 5*time.Second),
		"DataBatch claim should be created")
	t.Log("✓ DataBatch claim created")

	// Wait for DataBatch claim to complete (MultiProducer executes)
	require.True(t, waitForClaimStatus(ctx, bbClient, dataBatch.ID, blackboard.ClaimStatusComplete, 15*time.Second),
		"DataBatch claim should be completed by MultiProducer")
	t.Log("✓ MultiProducer executed")

	// Wait for 5 ProcessedRecords to be created
	t.Log("Waiting for 5 ProcessedRecords to be created...")
	require.True(t, waitForArtefactCount(ctx, bbClient, "ProcessedRecord", 5, 10*time.Second),
		"Should have 5 ProcessedRecords")

	// Get the records
	records, _ := testutil.FindAllArtefactsOfType(ctx, bbClient, "ProcessedRecord")
	if len(records) != 5 {
		env.DumpInstanceLogs()
		failures, _ := testutil.FindAllArtefactsOfType(ctx, bbClient, "Failure")
		for _, f := range failures {
			t.Logf("FAILURE DETECTED: %s", f.Payload.Content)
		}
	}
	require.Equal(t, 5, len(records), "Should have exactly 5 ProcessedRecords")

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

	// Wait for all ProcessedRecord claims to be created (Aggregator waits for claims)
	t.Log("Waiting for all ProcessedRecord claims to be created...")
	for i, record := range records {
		require.True(t, waitForClaimCreated(ctx, bbClient, record.ID, 5*time.Second),
			"ProcessedRecord %d claim should be created", i+1)
	}
	t.Log("✓ All ProcessedRecord claims created")

	// Wait for Aggregator to synchronize and create report
	t.Log("Waiting for Aggregator to synchronize...")
	report := waitForArtefactType(ctx, t, bbClient, "AggregatedReport", 30*time.Second)
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

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

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

	// Wait for orchestrator to be ready
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second),
		"Orchestrator should create claim for GoalDefined")
	t.Log("✓ Orchestrator ready with 2 concurrent workers")

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

	// Wait for CodeCommit claim to be created
	require.True(t, waitForClaimCreated(ctx, bbClient, codeCommit.ID, 5*time.Second),
		"CodeCommit claim should be created")
	t.Log("✓ CodeCommit claim confirmed")

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

	// Wait for claims to be created
	require.True(t, waitForClaimCreated(ctx, bbClient, testResult.ID, 5*time.Second),
		"TestResult claim should be created")
	require.True(t, waitForClaimCreated(ctx, bbClient, lintResult.ID, 5*time.Second),
		"LintResult claim should be created")

	// Wait for synchronizer to execute and create exactly ONE DeployResult
	require.True(t, waitForArtefactCount(ctx, bbClient, "DeployResult", 1, 30*time.Second),
		"Should have exactly 1 DeployResult")

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
	// Wait removed - using deterministic polling

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
	// Wait removed - using deterministic polling
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
	// Wait removed - using deterministic polling

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

// Helper functions for deterministic test conditions

// waitForClaimCreated waits for the orchestrator to create a claim for an artefact.
// Returns true if claim exists, false if timeout.
func waitForClaimCreated(ctx context.Context, bbClient *blackboard.Client, artefactID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		claim, err := bbClient.GetClaimByArtefactID(ctx, artefactID)
		if err == nil && claim != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitForClaimStatus waits for a claim to reach a specific status.
func waitForClaimStatus(ctx context.Context, bbClient *blackboard.Client, artefactID string, status blackboard.ClaimStatus, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		claim, err := bbClient.GetClaimByArtefactID(ctx, artefactID)
		if err == nil && claim != nil && claim.Status == status {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

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
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// waitForArtefactCount waits for a specific number of artefacts of a given type.
func waitForArtefactCount(ctx context.Context, bbClient *blackboard.Client, artefactType string, count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		artefacts := getArtefactsByType(ctx, bbClient, artefactType)
		if len(artefacts) >= count {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
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

# Extract ancestor ID and descendant count (avoid embedding complex JSON)
ANCESTOR_ID=$(echo "$INPUT" | jq -r '.ancestor_artefact.id // "unknown"')
DESCENDANTS=$(echo "$INPUT" | jq -r '.descendant_artefacts | length')

# Create synchronized result with safe payload
cat <<RESULT >&3
{"artefact_type":"DeployResult","artefact_payload":"synchronized-ancestor-$ANCESTOR_ID-descendants-$DESCENDANTS","summary":"Deployment synchronized"}
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
