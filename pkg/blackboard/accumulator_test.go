package blackboard

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteAccumulatorScript_FirstClaim verifies that the first claim initializes the accumulator correctly.
func TestExecuteAccumulatorScript_FirstClaim(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-123"
	role := "Recomposer"
	claimID := "claim-001"
	batchSize := "5"
	targetType := "HPOMappingResult"

	// Execute script for first claim
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, claimID, batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete, "First claim should not complete batch")

	// Verify accumulator was created with correct metadata
	key := ClaimAccumulatorKey("test-instance", ancestorID, role)

	// Check Fan-In Claim ID is deterministic
	fanInClaimID, err := client.GetAccumulatorFanInClaimID(ctx, ancestorID, role)
	require.NoError(t, err)
	expectedFanInID := "fanin:" + ancestorID + ":" + role
	assert.Equal(t, expectedFanInID, fanInClaimID, "Fan-In Claim ID should be deterministic")

	// Check status
	status, err := client.rdb.HGet(ctx, key, "status").Result()
	require.NoError(t, err)
	assert.Equal(t, "accumulating", status)

	// Check claimer
	claimer, err := client.rdb.HGet(ctx, key, "claimer").Result()
	require.NoError(t, err)
	assert.Equal(t, role, claimer)

	// Check batch_size
	storedBatchSize, err := client.rdb.HGet(ctx, key, "batch_size").Result()
	require.NoError(t, err)
	assert.Equal(t, batchSize, storedBatchSize)

	// Check target_type
	storedTargetType, err := client.rdb.HGet(ctx, key, "target_type").Result()
	require.NoError(t, err)
	assert.Equal(t, targetType, storedTargetType)

	// Check merge_ancestor
	storedAncestor, err := client.rdb.HGet(ctx, key, "merge_ancestor").Result()
	require.NoError(t, err)
	assert.Equal(t, ancestorID, storedAncestor)

	// Check created_at_ms exists
	createdAtMs, err := client.rdb.HGet(ctx, key, "created_at_ms").Result()
	require.NoError(t, err)
	assert.NotEmpty(t, createdAtMs)

	// Verify claim was added to set
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Equal(t, []string{claimID}, accumulatedClaims)
}

// TestExecuteAccumulatorScript_SubsequentClaims verifies that subsequent claims increment count correctly.
func TestExecuteAccumulatorScript_SubsequentClaims(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-456"
	role := "Aggregator"
	batchSize := "3"
	targetType := "ProcessedRecord"

	// Add first claim
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Verify count is 1
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)

	// Add second claim
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete, "Second claim should not complete batch of 3")

	// Verify count is 2
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 2)
	assert.Contains(t, accumulatedClaims, "claim-001")
	assert.Contains(t, accumulatedClaims, "claim-002")
}

// TestExecuteAccumulatorScript_BatchComplete verifies that the script returns true when batch is complete.
func TestExecuteAccumulatorScript_BatchComplete(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-789"
	role := "TestAggregator"
	batchSize := "3"
	targetType := "TestResult"

	// Add first claim
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Add second claim
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Add third claim (completes batch)
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-003", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "Third claim should complete batch of 3")

	// Verify all claims are in set
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 3)
	assert.Contains(t, accumulatedClaims, "claim-001")
	assert.Contains(t, accumulatedClaims, "claim-002")
	assert.Contains(t, accumulatedClaims, "claim-003")
}

// TestExecuteAccumulatorScript_Idempotency verifies that re-adding the same claim doesn't increment count.
func TestExecuteAccumulatorScript_Idempotency(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-idempotent"
	role := "IdempotentAgent"
	batchSize := "2"
	targetType := "IdempotentType"

	// Add claim-001 first time
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Verify count is 1
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)

	// Add claim-001 again (duplicate)
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete, "Duplicate claim should not complete batch")

	// Verify count is still 1 (SADD is idempotent)
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1, "Count should not increase for duplicate claim")

	// Add claim-002 (should complete batch)
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "Second unique claim should complete batch of 2")

	// Verify count is 2
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 2)
}

// TestExecuteAccumulatorScript_ConcurrentBatches verifies that different ancestors/roles maintain separate accumulators.
func TestExecuteAccumulatorScript_ConcurrentBatches(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	// Two different ancestors, same role
	ancestorA := "ancestor-patient-A"
	ancestorB := "ancestor-patient-B"
	role := "Aggregator"
	batchSize := "2"
	targetType := "ProcessedRecord"

	// Add claims for Patient A
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A1", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Add claims for Patient B
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B1", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Verify separate accumulators
	claimsA, err := client.GetAccumulatedClaims(ctx, ancestorA, role)
	require.NoError(t, err)
	assert.Len(t, claimsA, 1)
	assert.Contains(t, claimsA, "claim-A1")

	claimsB, err := client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1)
	assert.Contains(t, claimsB, "claim-B1")

	// Complete Patient A batch
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A2", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "Patient A batch should complete")

	// Patient B should still be incomplete
	claimsB, err = client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1, "Patient B should still have only 1 claim")

	// Complete Patient B batch
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B2", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "Patient B batch should complete")

	// Verify both batches are complete and independent
	claimsA, err = client.GetAccumulatedClaims(ctx, ancestorA, role)
	require.NoError(t, err)
	assert.Len(t, claimsA, 2)

	claimsB, err = client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 2)

	// Verify deterministic Fan-In Claim IDs
	fanInClaimIDA, err := client.GetAccumulatorFanInClaimID(ctx, ancestorA, role)
	require.NoError(t, err)
	assert.Equal(t, "fanin:"+ancestorA+":"+role, fanInClaimIDA)

	fanInClaimIDB, err := client.GetAccumulatorFanInClaimID(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Equal(t, "fanin:"+ancestorB+":"+role, fanInClaimIDB)
}

// TestExecuteAccumulatorScript_DifferentRoles verifies that different roles have separate accumulators.
func TestExecuteAccumulatorScript_DifferentRoles(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-shared"
	roleA := "Aggregator"
	roleB := "Reporter"
	batchSize := "2"
	targetType := "SharedType"

	// Add claims for Aggregator
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, roleA, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Add claims for Reporter
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleB, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.False(t, complete)

	// Verify separate accumulators
	claimsA, err := client.GetAccumulatedClaims(ctx, ancestorID, roleA)
	require.NoError(t, err)
	assert.Len(t, claimsA, 1)

	claimsB, err := client.GetAccumulatedClaims(ctx, ancestorID, roleB)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1)

	// Complete both batches independently
	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleA, "claim-002", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete)

	complete, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleB, "claim-003", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete)
}

// TestUpdateAccumulatorStatus verifies status transitions and timestamp updates.
func TestUpdateAccumulatorStatus(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-status-test"
	role := "StatusTester"
	batchSize := "1"
	targetType := "StatusType"

	// Create accumulator
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "Batch of 1 should complete immediately")

	// Update to granted status
	err = client.UpdateAccumulatorStatus(ctx, ancestorID, role, "granted")
	require.NoError(t, err)

	// Verify status and granted_at_ms
	key := ClaimAccumulatorKey("test-instance", ancestorID, role)
	status, err := client.rdb.HGet(ctx, key, "status").Result()
	require.NoError(t, err)
	assert.Equal(t, "granted", status)

	grantedAtMs, err := client.rdb.HGet(ctx, key, "granted_at_ms").Result()
	require.NoError(t, err)
	assert.NotEmpty(t, grantedAtMs)

	// Update to complete status
	err = client.UpdateAccumulatorStatus(ctx, ancestorID, role, "complete")
	require.NoError(t, err)

	// Verify status and completed_at_ms
	status, err = client.rdb.HGet(ctx, key, "status").Result()
	require.NoError(t, err)
	assert.Equal(t, "complete", status)

	completedAtMs, err := client.rdb.HGet(ctx, key, "completed_at_ms").Result()
	require.NoError(t, err)
	assert.NotEmpty(t, completedAtMs)
}

// TestGetAccumulatorFanInClaimID_NonExistent verifies behavior when accumulator doesn't exist.
func TestGetAccumulatorFanInClaimID_NonExistent(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	fanInClaimID, err := client.GetAccumulatorFanInClaimID(ctx, "nonexistent-ancestor", "nonexistent-role")
	require.NoError(t, err)
	assert.Empty(t, fanInClaimID, "Non-existent accumulator should return empty string")
}

// TestGetAccumulatedClaims_NonExistent verifies behavior when accumulator doesn't exist.
func TestGetAccumulatedClaims_NonExistent(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	claims, err := client.GetAccumulatedClaims(ctx, "nonexistent-ancestor", "nonexistent-role")
	require.NoError(t, err)
	assert.Empty(t, claims, "Non-existent accumulator should return empty slice")
}

// TestExecuteAccumulatorScript_LargeBatch verifies handling of larger batch sizes.
func TestExecuteAccumulatorScript_LargeBatch(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-large-batch"
	role := "LargeBatchAgent"
	batchSize := "100"
	targetType := "LargeType"

	// Add 99 claims (should not complete)
	for i := 1; i <= 99; i++ {
		claimID := fmt.Sprintf("claim-%03d", i)
		complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, claimID, batchSize, targetType)
		require.NoError(t, err)
		assert.False(t, complete, "Batch should not complete until 100th claim")
	}

	// Verify count is 99
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 99)

	// Add 100th claim (should complete)
	complete, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-100", batchSize, targetType)
	require.NoError(t, err)
	assert.True(t, complete, "100th claim should complete batch")

	// Verify final count is 100
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 100)
}
