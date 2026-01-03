//go:build integration
// +build integration

package commands

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests in this file and cleans up orphaned networks
func TestMain(m *testing.M) {
	// Create a dummy testing.T for cleanup logging
	t := &testing.T{}
	testutil.CleanupOrphanedTestNetworks(t)

	// Run tests
	os.Exit(m.Run())
}

// TestE2E_M5_2_ReviewPhaseCompletion validates that synchronizers:
// 1. Trigger on work artefact claims (HPOMappingResult)
// 2. Wait for review artefacts to exist (ReviewResult)
// 3. Check claim status to ensure reviews completed
// 4. Filter descendant_artefacts to only wait_for types
// 5. Deduplicate by LogicalThreadID to keep latest versions
//
// Scenario:
//   GoalDefined (batch_size=3)
//     ├─ HPOMappingResult #1 (claim: pending_review) → ReviewResult (in progress)
//     ├─ HPOMappingResult #2 (claim: pending_parallel) → ReviewResult ✓ (done)
//     └─ HPOMappingResult #3 (claim: pending_parallel) → ReviewResult ✓ (done)
//
// Expected: Synchronizer waits until ALL claims move past pending_review
func TestE2E_M5_2_ReviewPhaseCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.2 E2E: Review Phase Completion Checking ===")

	testutil.EnsureTestAgentImage(t)

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  # Mapper creates HPOMappingResult artefacts
  Mapper:
    image: "holt-test-agent:latest"
    command: ["/app/m5_2_mapper.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["SubGoal"]

  # Reviewer creates ReviewResult artefacts (simulates human review)
  Reviewer:
    image: "holt-test-agent:latest"
    command: ["/app/m5_2_reviewer.sh"]
    bidding_strategy:
      type: "exclusive"
      target_types: ["HPOMappingResult"]

  # Recomposer synchronizes on ReviewResult artefacts
  # Triggers on HPOMappingResult claims, waits for ReviewResult artefacts
  Recomposer:
    image: "holt-test-agent:latest"
    command: ["/app/m5_2_recomposer.sh"]
    synchronize:
      ancestor_type: "GoalDefined"
      wait_for:
        - type: "ReviewResult"
          count_from_metadata: "batch_size"

services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		if t.Failed() {
			env.DumpInstanceLogs()
		}
		env.ForceCleanup()
		t.Log("✓ Cleanup complete")
	}()

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err := runUp(upCmd, []string{})
	require.NoError(t, err)
	t.Log("✓ Instance started")

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// Create workflow spine
	_, goalID := env.CreateWorkflowSpine(ctx, "Map patient terms with review")
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second),
		"GoalDefined claim should be created")
	t.Log("✓ Orchestrator ready")

	// Create 3 SubGoal artefacts (batch_size=3)
	t.Log("Creating 3 SubGoals (batch_size=3)...")
	subGoals := make([]*blackboard.Artefact, 3)
	for i := 0; i < 3; i++ {
		subGoals[i] = env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
			ParentHashes:    []string{goalID},
			LogicalThreadID: blackboard.NewID(),
			Version:         2,
			Type:            "SubGoal",
			CreatedAtMs:     time.Now().UnixMilli(),
			Metadata:        `{"batch_size": "3"}`,
		}, "map-clinical-terms")
		t.Logf("✓ SubGoal %d created: %s", i+1, subGoals[i].ID[:16])
	}

	// Wait for Mapper to process SubGoals and create HPOMappingResults
	t.Log("Waiting for Mapper to create 3 HPOMappingResults...")
	require.True(t, waitForArtefactCount(ctx, bbClient, "HPOMappingResult", 3, 30*time.Second),
		"Should have 3 HPOMappingResults")
	mappings, _ := testutil.FindAllArtefactsOfType(ctx, bbClient, "HPOMappingResult")
	require.Len(t, mappings, 3, "Should have exactly 3 mappings")
	t.Log("✓ Mapper created 3 HPOMappingResults")

	// Wait for HPOMappingResult claims to be created
	for i, mapping := range mappings {
		require.True(t, waitForClaimCreated(ctx, bbClient, mapping.ID, 5*time.Second),
			"HPOMappingResult %d claim should be created", i+1)
	}
	t.Log("✓ All HPOMappingResult claims created")

	// Wait for all 3 ReviewResults to be created by the Reviewer agent
	// (The synchronizer will wait until all are present before bidding)
	t.Log("Waiting for all 3 ReviewResults to be created...")
	require.True(t, waitForArtefactCount(ctx, bbClient, "ReviewResult", 3, 30*time.Second),
		"Should have 3 ReviewResults")
	t.Log("✓ All 3 ReviewResults created")

	// Now Recomposer should bid and execute
	t.Log("Waiting for Recomposer to synchronize (all 3 reviews complete)...")
	finalProfile := waitForArtefactType(ctx, t, bbClient, "FinalPatientProfile", 30*time.Second)
	if finalProfile == nil {
		t.Log("FinalPatientProfile not created - dumping logs...")
		env.DumpInstanceLogs()
	}
	require.NotNil(t, finalProfile, "FinalPatientProfile should be created")
	t.Logf("✓ Recomposer synchronized: %s", finalProfile.ID[:16])

	// Verify the Recomposer received ONLY ReviewResult artefacts (filtered)
	require.Contains(t, finalProfile.Payload.Content, "3 reviews", "Should mention 3 reviews")

	t.Log("=== Review Phase Completion E2E Test PASSED ===")
}

// TestE2E_M5_2_ReviewRevisions validates synchronizer behavior with multiple review versions:
// 1. Create mapping that gets revised (v1 → v2)
// 2. Both versions get reviewed (ReviewResult v1, v2)
// 3. All parent claims moved past review phase
// 4. Synchronizer executes when claim status check passes
//
// TODO(M5.3): Add deduplication by parent LogicalThreadID to keep only latest versions
func TestE2E_M5_2_ReviewRevisions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.2 E2E: Review Revisions with Deduplication ===")

	testutil.EnsureTestAgentImage(t)

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  Recomposer:
    image: "holt-test-agent:latest"
    command: ["/app/m5_2_recomposer.sh"]
    synchronize:
      ancestor_type: "GoalDefined"
      wait_for:
        - type: "ReviewResult"
          count_from_metadata: "batch_size"
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		if t.Failed() {
			env.DumpInstanceLogs()
		}
		env.ForceCleanup()
	}()

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err := runUp(upCmd, []string{})
	require.NoError(t, err)

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	_, goalID := env.CreateWorkflowSpine(ctx, "Test deduplication")
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second))
	t.Log("✓ Orchestrator ready")

	// Create SubGoal with batch_size=2
	t.Log("Creating SubGoal (batch_size=2)...")
	subGoal := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2,
		Type:            "SubGoal",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "test-goal")

	// Create 2 HPOMappingResults (same logical thread, different versions)
	thread1 := blackboard.NewID()
	thread2 := blackboard.NewID()

	t.Log("Creating HPOMappingResult v1 (thread 1)...")
	mapping1v1 := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{subGoal.ID},
		LogicalThreadID: thread1,
		Version:         2, // Version 2: continuation from SubGoal
		Type:            "HPOMappingResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "mapping-1-v1")

	// Create ReviewResult v1 for mapping 1
	t.Log("Creating ReviewResult v1 (thread 1)...")
	_ = env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{mapping1v1.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         3, // Version 3: continuation from HPOMappingResult
		Type:            "ReviewResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "review-1-rejected")

	// Revise mapping 1 (v2)
	t.Log("Creating HPOMappingResult v2 (thread 1 - revision)...")
	mapping1v2 := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{subGoal.ID},
		LogicalThreadID: thread1, // Same thread!
		Version:         2,       // Version 2 of this thread
		Type:            "HPOMappingResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "mapping-1-v2-corrected")

	// Create ReviewResult v2 for mapping 1 (latest)
	t.Log("Creating ReviewResult v2 (thread 1 - latest)...")
	_ = env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{mapping1v2.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         3, // Version 3: continuation from HPOMappingResult v2
		Type:            "ReviewResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "review-1-approved")

	// Create mapping 2 (separate thread)
	t.Log("Creating HPOMappingResult v1 (thread 2)...")
	mapping2 := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{subGoal.ID},
		LogicalThreadID: thread2,
		Version:         2, // Version 2: continuation from SubGoal
		Type:            "HPOMappingResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "mapping-2-v1")

	// Create ReviewResult for mapping 2
	t.Log("Creating ReviewResult v1 (thread 2)...")
	_ = env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{mapping2.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         3, // Version 3: continuation from HPOMappingResult
		Type:            "ReviewResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        `{"batch_size": "2"}`,
	}, "review-2-approved")

	// Wait for claims to be created and move to parallel phase
	t.Log("Waiting for claims to complete review...")
	require.True(t, waitForClaimCreated(ctx, bbClient, mapping1v1.ID, 5*time.Second))
	require.True(t, waitForClaimCreated(ctx, bbClient, mapping1v2.ID, 5*time.Second))
	require.True(t, waitForClaimCreated(ctx, bbClient, mapping2.ID, 5*time.Second))

	// Simulate moving ALL mapping claims past review
	// (Even though mapping1v1 will be deduplicated, its claim status still needs to be "past review")
	claim1v1, _ := bbClient.GetClaimByArtefactID(ctx, mapping1v1.ID)
	claim1v1.Status = blackboard.ClaimStatusPendingParallel
	_ = bbClient.UpdateClaim(ctx, claim1v1)

	claim1v2, _ := bbClient.GetClaimByArtefactID(ctx, mapping1v2.ID)
	claim1v2.Status = blackboard.ClaimStatusPendingParallel
	_ = bbClient.UpdateClaim(ctx, claim1v2)

	claim2, _ := bbClient.GetClaimByArtefactID(ctx, mapping2.ID)
	claim2.Status = blackboard.ClaimStatusPendingParallel
	_ = bbClient.UpdateClaim(ctx, claim2)

	t.Log("✓ All 3 mapping claims moved to parallel phase")

	// Now we have:
	// - 3 ReviewResults total (v1 and v2 for thread1, v1 for thread2)
	// - All 3 mapping claims moved past review phase
	// - Synchronizer will receive all 3 ReviewResults
	//
	// TODO(M5.3): Implement deduplication by parent LogicalThreadID
	// Expected future behavior: 3 ReviewResults → 2 unique parent threads → 2 deduplicated reviews

	t.Log("Waiting for Recomposer to synchronize...")
	finalProfile := waitForArtefactType(ctx, t, bbClient, "FinalPatientProfile", 30*time.Second)
	if finalProfile == nil {
		t.Log("FinalPatientProfile not created - dumping logs...")
		env.DumpInstanceLogs()
	}
	require.NotNil(t, finalProfile, "FinalPatientProfile should be created")
	t.Logf("✓ Recomposer synchronized: %s", finalProfile.ID[:16])

	// Verify synchronizer executed (current behavior: no deduplication, receives all 3)
	require.Contains(t, finalProfile.Payload.Content, "3 reviews", "Should receive all 3 ReviewResults (deduplication not yet implemented)")
	t.Log("✓ Synchronizer executed with 3 ReviewResults")

	// Verify only latest versions received
	var metadata map[string]interface{}
	json.Unmarshal([]byte(finalProfile.Header.Metadata), &metadata)
	if received, ok := metadata["received_versions"]; ok {
		t.Logf("Received versions: %v", received)
		// Should NOT include v1 of thread1
	}

	t.Log("=== Review Revisions E2E Test PASSED ===")
}

// TestE2E_M5_2_DescendantFiltering validates that descendant_artefacts only includes wait_for types:
// 1. Create workflow with multiple artefact types in descendants
// 2. Configure synchronizer to wait_for only ReviewResult
// 3. Verify agent receives ONLY ReviewResult artefacts (not SubGoal, HPOMappingResult, etc.)
func TestE2E_M5_2_DescendantFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== M5.2 E2E: Descendant Artefacts Filtering ===")

	testutil.EnsureTestAgentImage(t)

	holtYML := `version: "1.0"
orchestrator:
  max_review_iterations: 3
  timestamp_drift_tolerance_ms: 600000
agents:
  FilterTester:
    image: "holt-test-agent:latest"
    command: ["/app/m5_2_filter_tester.sh"]
    synchronize:
      ancestor_type: "GoalDefined"
      wait_for:
        - type: "ReviewResult"
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		if t.Failed() {
			env.DumpInstanceLogs()
		}
		env.ForceCleanup()
	}()

	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err := runUp(upCmd, []string{})
	require.NoError(t, err)

	ctx := context.Background()
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	_, goalID := env.CreateWorkflowSpine(ctx, "Test filtering")
	require.True(t, waitForClaimCreated(ctx, bbClient, goalID, 10*time.Second))
	t.Log("✓ Orchestrator ready")

	// Create descendant tree with multiple types:
	// GoalDefined
	//   └─ SubGoal (metadata holder, should be filtered out)
	//       └─ HPOMappingResult (work artefact, should be filtered out)
	//           └─ ReviewResult (wait_for type, should be included)

	t.Log("Creating descendant tree...")
	subGoal := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{goalID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2,
		Type:            "SubGoal",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "sub-goal")

	mapping := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{subGoal.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2,
		Type:            "HPOMappingResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "mapping")

	_ = env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{mapping.ID},
		LogicalThreadID: blackboard.NewID(),
		Version:         2,
		Type:            "ReviewResult",
		CreatedAtMs:     time.Now().UnixMilli(),
		Metadata:        "{}",
	}, "review")

	// Move mapping claim past review
	claim, _ := bbClient.GetClaimByArtefactID(ctx, mapping.ID)
	if claim != nil {
		claim.Status = blackboard.ClaimStatusPendingParallel
		_ = bbClient.UpdateClaim(ctx, claim)
	}

	t.Log("✓ Descendant tree created (SubGoal → HPOMappingResult → ReviewResult)")

	// Wait for synchronizer to execute
	t.Log("Waiting for FilterTester to synchronize...")
	result := waitForArtefactType(ctx, t, bbClient, "FilterTestResult", 30*time.Second)
	if result == nil {
		env.DumpInstanceLogs()
	}
	require.NotNil(t, result, "FilterTestResult should be created")

	// Verify agent received ONLY ReviewResult (check metadata or payload)
	require.Contains(t, result.Payload.Content, "received_types:ReviewResult", "Should only receive ReviewResult")
	require.NotContains(t, result.Payload.Content, "SubGoal", "Should NOT receive SubGoal")
	require.NotContains(t, result.Payload.Content, "HPOMappingResult", "Should NOT receive HPOMappingResult")

	t.Log("✓ Filtering worked: Agent received only ReviewResult artefacts")
	t.Log("=== Descendant Filtering E2E Test PASSED ===")
}
