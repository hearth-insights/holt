//go:build integration
// +build integration

package commands

import (
	"context"
	"testing"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_M4_7_ForageAnchoring tests that holt forage correctly anchors GoalDefined artefacts to the active SystemManifest.
// This validates the complete M4.7 integration: orchestrator creates manifest → forage fetches it → artefact anchored.
func TestE2E_M4_7_ForageAnchoring(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()

	// Setup: Create test environment
	holtYML := `version: "1.0"
agents:
  TestAgent:
    role: "Test Agent"
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
services:
  redis:
    image: redis:7-alpine
`
	env := testutil.SetupE2EEnvironment(t, holtYML)

	// Clean up at the end
	defer func() {
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		runDown(downCmd, []string{})
	}()

	// Start Holt instance (this will initialize the System Spine)
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	err := runUp(upCmd, []string{})
	require.NoError(t, err, "holt up failed")

	// Wait for orchestrator to be ready
	env.WaitForContainer("orchestrator")

	// Initialize blackboard client
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// Test 1: Verify SystemManifest was created by orchestrator startup
	t.Run("OrchestratorCreatesSystemManifest", func(t *testing.T) {
		// Fetch active_manifest key
		activeManifestKey := "holt:" + env.InstanceName + ":active_manifest"
		manifestID, err := bbClient.GetRedisClient().Get(ctx, activeManifestKey).Result()
		require.NoError(t, err, "active_manifest key should exist")
		assert.NotEmpty(t, manifestID, "Manifest ID should not be empty")
		assert.Len(t, manifestID, 64, "Manifest ID should be 64-char SHA-256 hash")

		// Fetch the manifest artefact
		manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
		require.NoError(t, err, "Should be able to fetch SystemManifest")
		assert.Equal(t, blackboard.StructuralTypeSystemManifest, manifest.Header.StructuralType)
		assert.Equal(t, "orchestrator", manifest.Header.ProducedByRole)
		assert.Equal(t, "SystemConfig", manifest.Header.Type)
		assert.Equal(t, 1, manifest.Header.Version, "First manifest should be version 1")
		assert.Empty(t, manifest.Header.ParentHashes, "First manifest should have no parents")
	})

	// Test 2: Run holt forage and verify artefact is anchored to SystemManifest
	t.Run("ForageAnchorsToSystemManifest", func(t *testing.T) {
		// Fetch active manifest ID first
		activeManifestKey := "holt:" + env.InstanceName + ":active_manifest"
		expectedManifestID, err := bbClient.GetRedisClient().Get(ctx, activeManifestKey).Result()
		require.NoError(t, err)

		// Run holt forage
		forageCmd := &cobra.Command{}
		forageInstanceName = env.InstanceName
		forageGoal = "Test goal for M4.7 anchoring validation"
		forageWatch = false

		err = runForage(forageCmd, []string{})
		require.NoError(t, err, "holt forage should succeed")

		// Find the GoalDefined artefact (scan recent artefacts)
		// Note: In real implementation, we'd track the artefact ID returned by forage
		// For this test, we scan for artefacts with Type="GoalDefined"
		keys, err := bbClient.ScanKeys(ctx, "holt:"+env.InstanceName+":artefact:*")
		require.NoError(t, err)

		var goalArtefact *blackboard.VerifiableArtefact
		for _, key := range keys {
			// Extract hash from key (last part after final :)
			hashID := key[len("holt:"+env.InstanceName+":artefact:"):]

			artefact, err := bbClient.GetVerifiableArtefact(ctx, hashID)
			if err != nil {
				continue // Skip if can't fetch
			}

			if artefact.Header.Type == "GoalDefined" && artefact.Header.ProducedByRole == "user" {
				goalArtefact = artefact
				break
			}
		}

		require.NotNil(t, goalArtefact, "Should find GoalDefined artefact")

		// CRITICAL ASSERTION: Verify artefact is anchored to SystemManifest
		assert.Len(t, goalArtefact.Header.ParentHashes, 1, "GoalDefined should have exactly one parent (the SystemManifest)")
		assert.Equal(t, expectedManifestID, goalArtefact.Header.ParentHashes[0], "Parent should be the active SystemManifest")

		// Verify the parent is actually a SystemManifest
		parentManifest, err := bbClient.GetVerifiableArtefact(ctx, goalArtefact.Header.ParentHashes[0])
		require.NoError(t, err, "Should be able to fetch parent manifest")
		assert.Equal(t, blackboard.StructuralTypeSystemManifest, parentManifest.Header.StructuralType)
	})

	// Test 3: Verify orchestrator accepts properly anchored artefacts
	t.Run("OrchestratorAcceptsAnchoredArtefacts", func(t *testing.T) {
		// The fact that the orchestrator created a claim for the GoalDefined artefact
		// proves it passed topology validation. Let's verify this explicitly.

		// Find GoalDefined artefact ID
		keys, err := bbClient.ScanKeys(ctx, "holt:"+env.InstanceName+":artefact:*")
		require.NoError(t, err)

		var goalArtefactID string
		for _, key := range keys {
			hashID := key[len("holt:"+env.InstanceName+":artefact:"):]
			artefact, err := bbClient.GetVerifiableArtefact(ctx, hashID)
			if err != nil {
				continue
			}
			if artefact.Header.Type == "GoalDefined" {
				goalArtefactID = artefact.ID
				break
			}
		}

		require.NotEmpty(t, goalArtefactID, "Should find GoalDefined artefact")

		// Check if orchestrator created a claim for it
		claim, err := bbClient.GetClaimByArtefactID(ctx, goalArtefactID)
		// Note: claim might not exist yet if orchestrator is slow, but artefact should be in blackboard
		// The key test is that no topology violation occurred (no lockdown triggered)

		// Verify no global lockdown was triggered
		isLockedDown, _, err := bbClient.IsInLockdown(ctx)
		require.NoError(t, err)
		assert.False(t, isLockedDown, "Orchestrator should NOT trigger lockdown for properly anchored artefacts")

		// If claim exists, verify it's valid
		if claim != nil {
			assert.Equal(t, goalArtefactID, claim.ArtefactID)
			t.Logf("Orchestrator successfully created claim %s for anchored GoalDefined artefact", claim.ID)
		}
	})
}
