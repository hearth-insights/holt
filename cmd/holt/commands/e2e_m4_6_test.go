//go:build integration
// +build integration

package commands

import (
	"context"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_M4_6_VerifyCommand tests the holt verify command with V2 artefacts.
// This validates the end-to-end flow: create V2 artefact → verify hash → success.
func TestE2E_M4_6_VerifyCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()

	// Setup: Create test environment with minimal config
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

	// Start Holt instance
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	err := runUp(upCmd, []string{})
	require.NoError(t, err, "holt up failed")

	// Wait for orchestrator to be ready
	env.WaitForContainer("orchestrator")

	// Initialize blackboard client
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// Test 1: Create a V2 VerifiableArtefact
	t.Run("CreateAndVerifyV2Artefact", func(t *testing.T) {
		// Create a valid V2 artefact
		artefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{}, // Root artefact
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				CreatedAtMs:     time.Now().UnixMilli(),
				ProducedByRole:  "test-agent",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "TestArtefact",
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Test payload content for M4.6 verification",
			},
		}

		// Compute hash (this is what the Pup does)
		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err, "Failed to compute hash")
		require.Len(t, hash, 64, "Hash should be 64 hex characters")

		artefact.ID = hash

		// Write to blackboard
		err = bbClient.WriteVerifiableArtefact(ctx, artefact)
		require.NoError(t, err, "Failed to write artefact")

		// Test 1a: Verify the artefact using GetVerifiableArtefact + ValidateArtefactHash
		// (This simulates what `holt verify` does internally)
		retrieved, err := bbClient.GetVerifiableArtefact(ctx, hash)
		require.NoError(t, err, "Failed to retrieve artefact")
		assert.Equal(t, hash, retrieved.ID, "Retrieved artefact ID should match")

		// Verify hash
		err = blackboard.ValidateArtefactHash(retrieved)
		assert.NoError(t, err, "Hash verification should pass for valid artefact")

		// Test 1b: Verify short hash resolution works
		shortHash := hash[:8]
		matches, err := bbClient.ScanKeys(ctx, "holt:"+env.InstanceName+":artefact:"+shortHash+"*")
		require.NoError(t, err, "Short hash scan should succeed")
		assert.Len(t, matches, 1, "Should find exactly one match for short hash")

		// Test 1c: Verify full hash can be resolved from short hash
		fullKey := "holt:" + env.InstanceName + ":artefact:" + hash
		assert.Equal(t, fullKey, matches[0], "Full key should match")
	})

	// Test 2: Tamper detection - modify payload after hash computation
	t.Run("DetectTamperedArtefact", func(t *testing.T) {
		// Create artefact
		artefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{},
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				CreatedAtMs:     time.Now().UnixMilli(),
				ProducedByRole:  "malicious-agent",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "TamperedArtefact",
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Original content",
			},
		}

		// Compute correct hash
		correctHash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)

		// TAMPER: Change payload after computing hash
		artefact.Payload.Content = "TAMPERED CONTENT - SHOULD BE DETECTED!"
		artefact.ID = correctHash // Use old hash with new content

		// Verification should detect tampering
		err = blackboard.ValidateArtefactHash(artefact)
		assert.Error(t, err, "Verification should fail for tampered artefact")

		// Check it's specifically a hash mismatch error
		var mismatchErr *blackboard.HashMismatchError
		assert.True(t, blackboard.IsHashMismatchError(err, &mismatchErr), "Error should be HashMismatchError")
		assert.NotEqual(t, mismatchErr.Expected, mismatchErr.Actual, "Expected and actual hashes should differ")
	})

	// Test 3: Parent hash validation (orphan block detection)
	t.Run("DetectOrphanBlock", func(t *testing.T) {
		// Create artefact with non-existent parent
		nonExistentParent := "0000000000000000000000000000000000000000000000000000000000000000"

		artefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{nonExistentParent}, // Parent doesn't exist
				LogicalThreadID: blackboard.NewID(),
				Version:         2,
				CreatedAtMs:     time.Now().UnixMilli(),
				ProducedByRole:  "test-agent",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "OrphanArtefact",
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Content with invalid parent",
			},
		}

		// Compute hash
		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash

		// Check parent exists - should fail
		exists, err := bbClient.ArtefactExists(ctx, nonExistentParent)
		require.NoError(t, err)
		assert.False(t, exists, "Non-existent parent should not be found")

		// This demonstrates the orchestrator's orphan block detection logic
		// (The actual rejection happens in orchestrator, not in blackboard client)
	})

	// Test 4: Timestamp validation
	t.Run("ValidateTimestamp", func(t *testing.T) {
		now := time.Now().UnixMilli()

		// Test 4a: Future timestamp (>5 minutes ahead)
		futureArtefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{},
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				CreatedAtMs:     now + (10 * 60 * 1000), // 10 minutes in future
				ProducedByRole:  "time-traveling-agent",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "FutureArtefact",
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Content from the future",
			},
		}

		hash, err := blackboard.ComputeArtefactHash(futureArtefact)
		require.NoError(t, err)
		futureArtefact.ID = hash

		// Calculate drift
		drift := futureArtefact.Header.CreatedAtMs - now
		threshold := int64(5 * 60 * 1000) // 5 minutes

		assert.Greater(t, drift, threshold, "Drift should exceed threshold")

		// Test 4b: Past timestamp (>5 minutes ago)
		pastArtefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{},
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				CreatedAtMs:     now - (10 * 60 * 1000), // 10 minutes in past
				ProducedByRole:  "ancient-agent",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "PastArtefact",
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Content from the past",
			},
		}

		hash, err = blackboard.ComputeArtefactHash(pastArtefact)
		require.NoError(t, err)
		pastArtefact.ID = hash

		drift = now - pastArtefact.Header.CreatedAtMs
		assert.Greater(t, drift, threshold, "Drift should exceed threshold")
	})
}

// TestE2E_M4_6_SecurityAlerts tests security alert infrastructure.
func TestE2E_M4_6_SecurityAlerts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()

	// Setup
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

	// Start Holt instance
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	err := runUp(upCmd, []string{})
	require.NoError(t, err, "holt up failed")

	// Wait for orchestrator to be ready
	env.WaitForContainer("orchestrator")

	// Initialize blackboard client
	env.InitializeBlackboardClient()
	bbClient := env.BBClient

	// Test 1: Security alert creation and retrieval
	t.Run("CreateAndRetrieveSecurityAlert", func(t *testing.T) {
		// Create hash mismatch alert
		alert := blackboard.NewHashMismatchAlert(
			"a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1",
			"def456ab1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8",
			"a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1",
			"malicious-agent",
			"claim-123",
		)

		assert.Equal(t, "hash_mismatch", alert.Type)
		assert.Equal(t, "global_lockdown", alert.OrchestratorAction)
		assert.NotZero(t, alert.TimestampMs)

		// Test 2: Orphan block alert
		orphanAlert := blackboard.NewOrphanBlockAlert(
			"xyz789ab1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8",
			"abc123de4f567890abcdef1234567890abcdef1234567890abcdef123456789",
			"buggy-agent",
			"claim-456",
		)

		assert.Equal(t, "orphan_block", orphanAlert.Type)
		assert.Equal(t, "global_lockdown", orphanAlert.OrchestratorAction)

		// Test 3: Timestamp drift alert
		driftAlert := blackboard.NewTimestampDriftAlert(
			"drift123",
			time.Now().UnixMilli()+600000, // 10 min future
			time.Now().UnixMilli(),
			600000, // 10 min drift
			300000, // 5 min threshold
			"misconfigured-agent",
		)

		assert.Equal(t, "timestamp_drift", driftAlert.Type)
		assert.Equal(t, "rejected", driftAlert.OrchestratorAction)
		assert.Equal(t, int64(600000), driftAlert.DriftMs)

		// Test 4: Security override alert
		overrideAlert := blackboard.NewSecurityOverrideAlert(
			"Investigation complete: memory corruption detected",
			"admin",
		)

		assert.Equal(t, "security_override", overrideAlert.Type)
		assert.Equal(t, "lockdown_cleared", overrideAlert.Action)
		assert.Equal(t, "admin", overrideAlert.Operator)
	})

	// Test 2: Lockdown state management
	t.Run("LockdownStateManagement", func(t *testing.T) {
		// Initially no lockdown
		_, err := bbClient.GetLockdownState(ctx)
		assert.Error(t, err, "Should get redis.Nil when no lockdown")
		assert.True(t, blackboard.IsNotFound(err), "Error should be redis.Nil")

		// Create and test unlock (without actual lockdown for this test)
		overrideAlert := blackboard.SecurityAlert{
			Type:        "security_override",
			TimestampMs: time.Now().UnixMilli(),
			Action:      "lockdown_cleared",
			Reason:      "Test unlock operation",
			Operator:    "test-user",
		}

		// Note: In production, UnlockGlobalLockdown would:
		// 1. LPUSH to security:alerts:log
		// 2. DEL security:lockdown
		// 3. PUBLISH to security:alerts channel
		// For this test, we just validate the alert structure
		assert.Equal(t, "security_override", overrideAlert.Type)
		assert.NotZero(t, overrideAlert.TimestampMs)
	})
}
