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

	// Execute script for first claim (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, claimID, "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate, "No duplicate in COUNT mode")
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

	// Check mode
	storedMode, err := client.rdb.HGet(ctx, key, "mode").Result()
	require.NoError(t, err)
	assert.Equal(t, "count", storedMode)

	// Check expected_count
	storedCount, err := client.rdb.HGet(ctx, key, "expected_count").Result()
	require.NoError(t, err)
	assert.Equal(t, batchSize, storedCount)

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

	// Add first claim (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Verify count is 1
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)

	// Add second claim
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Add first claim (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add second claim
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add third claim (completes batch)
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-003", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Add claim-001 first time (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Verify count is 1
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)

	// Add claim-001 again (duplicate claim ID, but SADD is idempotent in COUNT mode)
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate, "Duplicate claim IDs are allowed in COUNT mode (SADD is idempotent)")
	assert.False(t, complete, "Duplicate claim should not complete batch")

	// Verify count is still 1 (SADD is idempotent)
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1, "Count should not increase for duplicate claim")

	// Add claim-002 (should complete batch)
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-002", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Add claims for Patient A (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A1", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add claims for Patient B
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B1", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A2", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "Patient A batch should complete")

	// Patient B should still be incomplete
	claimsB, err = client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1, "Patient B should still have only 1 claim")

	// Complete Patient B batch
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B2", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Add claims for Aggregator (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, roleA, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add claims for Reporter
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleB, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Verify separate accumulators
	claimsA, err := client.GetAccumulatedClaims(ctx, ancestorID, roleA)
	require.NoError(t, err)
	assert.Len(t, claimsA, 1)

	claimsB, err := client.GetAccumulatedClaims(ctx, ancestorID, roleB)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1)

	// Complete both batches independently
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleA, "claim-002", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete)

	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, roleB, "claim-003", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Create accumulator (COUNT mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-001", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
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

	// Add 99 claims (should not complete) (COUNT mode)
	for i := 1; i <= 99; i++ {
		claimID := fmt.Sprintf("claim-%03d", i)
		complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, claimID, "count", batchSize, targetType, "")
		require.NoError(t, err)
		assert.False(t, duplicate)
		assert.False(t, complete, "Batch should not complete until 100th claim")
	}

	// Verify count is 99
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 99)

	// Add 100th claim (should complete)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-100", "count", batchSize, targetType, "")
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "100th claim should complete batch")

	// Verify final count is 100
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 100)
}

// ====================
// TYPES MODE TESTS
// ====================

// TestExecuteAccumulatorScript_TypesMode_FirstType verifies that the first type initializes the accumulator correctly.
func TestExecuteAccumulatorScript_TypesMode_FirstType(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-types-001"
	role := "TypedRecomposer"
	claimID := "claim-test-001"
	expectedTypesJSON := `["LintResult","ScanResult","TestResult"]` // 3 types expected (alphabetically sorted)
	currentArtefactType := "TestResult"

	// Execute script for first type (TYPES mode)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, claimID, "types", "3", currentArtefactType, expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate, "First type should not be a duplicate")
	assert.False(t, complete, "First type should not complete set of 3")

	// Verify accumulator was created with correct metadata
	key := ClaimAccumulatorKey("test-instance", ancestorID, role)

	// Check mode
	storedMode, err := client.rdb.HGet(ctx, key, "mode").Result()
	require.NoError(t, err)
	assert.Equal(t, "types", storedMode)

	// Check expected_count
	storedCount, err := client.rdb.HGet(ctx, key, "expected_count").Result()
	require.NoError(t, err)
	assert.Equal(t, "3", storedCount)

	// Check expected_types
	storedTypes, err := client.rdb.HGet(ctx, key, "expected_types").Result()
	require.NoError(t, err)
	assert.Equal(t, expectedTypesJSON, storedTypes)

	// Verify type was added to HASH
	typesKey := ClaimAccumulatorTypesKey("test-instance", ancestorID, role)
	claimIDForType, err := client.rdb.HGet(ctx, typesKey, currentArtefactType).Result()
	require.NoError(t, err)
	assert.Equal(t, claimID, claimIDForType, "Type should map to claim ID")

	// Verify accumulated claims (should return 1 claim ID from HASH)
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)
	assert.Contains(t, accumulatedClaims, claimID)
}

// TestExecuteAccumulatorScript_TypesMode_AllTypesComplete verifies set completion.
func TestExecuteAccumulatorScript_TypesMode_AllTypesComplete(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-types-complete"
	role := "CompleteRecomposer"
	expectedTypesJSON := `["LintResult","ScanResult","TestResult"]`

	// Add first type: TestResult
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-test-001", "types", "3", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add second type: LintResult
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-lint-001", "types", "3", "LintResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete, "Second type should not complete set of 3")

	// Verify 2 types accumulated
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 2)

	// Add third type: ScanResult (completes set)
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-scan-001", "types", "3", "ScanResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "Third type should complete set")

	// Verify all 3 types accumulated
	accumulatedClaims, err = client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 3)
	assert.Contains(t, accumulatedClaims, "claim-test-001")
	assert.Contains(t, accumulatedClaims, "claim-lint-001")
	assert.Contains(t, accumulatedClaims, "claim-scan-001")
}

// TestExecuteAccumulatorScript_TypesMode_DuplicateTypeError verifies duplicate type detection.
func TestExecuteAccumulatorScript_TypesMode_DuplicateTypeError(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-types-duplicate"
	role := "DuplicateRecomposer"
	expectedTypesJSON := `["LintResult","TestResult"]`

	// Add first TestResult (v1)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-test-v1", "types", "2", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Attempt to add second TestResult (v2) - SHOULD ERROR
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-test-v2", "types", "2", "TestResult", expectedTypesJSON)
	require.NoError(t, err, "Script should not return execution error")
	assert.True(t, duplicate, "Duplicate type should be detected")
	assert.False(t, complete, "Duplicate should not complete set")

	// Verify only 1 claim accumulated (first one)
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1, "Only first type should be accumulated")
	assert.Contains(t, accumulatedClaims, "claim-test-v1", "First claim should remain")

	// Verify HASH still has only TestResult -> claim-test-v1
	typesKey := ClaimAccumulatorTypesKey("test-instance", ancestorID, role)
	claimIDForType, err := client.rdb.HGet(ctx, typesKey, "TestResult").Result()
	require.NoError(t, err)
	assert.Equal(t, "claim-test-v1", claimIDForType, "Original claim should remain")
}

// TestExecuteAccumulatorScript_TypesMode_ConcurrentBatches verifies separate accumulators for different ancestors.
func TestExecuteAccumulatorScript_TypesMode_ConcurrentBatches(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorA := "ancestor-workflow-A"
	ancestorB := "ancestor-workflow-B"
	role := "TypedAggregator"
	expectedTypesJSON := `["LintResult","TestResult"]`

	// Add TestResult for Workflow A
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A-test", "types", "2", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Add TestResult for Workflow B
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B-test", "types", "2", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	// Verify separate accumulators
	claimsA, err := client.GetAccumulatedClaims(ctx, ancestorA, role)
	require.NoError(t, err)
	assert.Len(t, claimsA, 1)
	assert.Contains(t, claimsA, "claim-A-test")

	claimsB, err := client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1)
	assert.Contains(t, claimsB, "claim-B-test")

	// Complete Workflow A set
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorA, role, "claim-A-lint", "types", "2", "LintResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "Workflow A should complete")

	// Workflow B should still be incomplete
	claimsB, err = client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 1, "Workflow B should still have 1 type")

	// Complete Workflow B set
	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorB, role, "claim-B-lint", "types", "2", "LintResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "Workflow B should complete")

	// Verify both sets complete independently
	claimsA, err = client.GetAccumulatedClaims(ctx, ancestorA, role)
	require.NoError(t, err)
	assert.Len(t, claimsA, 2)

	claimsB, err = client.GetAccumulatedClaims(ctx, ancestorB, role)
	require.NoError(t, err)
	assert.Len(t, claimsB, 2)
}

// TestExecuteAccumulatorScript_TypesMode_PartialSet verifies behavior when not all types arrive.
func TestExecuteAccumulatorScript_TypesMode_PartialSet(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-types-partial"
	role := "PartialRecomposer"
	expectedTypesJSON := `["LintResult","ScanResult","TestResult"]`

	// Add only 2 out of 3 types
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-test-001", "types", "3", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete)

	complete, duplicate, err = client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-lint-001", "types", "3", "LintResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.False(t, complete, "Partial set should not complete")

	// Verify accumulator remains in 'accumulating' state
	key := ClaimAccumulatorKey("test-instance", ancestorID, role)
	status, err := client.rdb.HGet(ctx, key, "status").Result()
	require.NoError(t, err)
	assert.Equal(t, "accumulating", status, "Status should remain 'accumulating'")

	// Verify only 2 types accumulated
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 2, "Only 2 types should be present")
}

// TestExecuteAccumulatorScript_TypesMode_SingleType verifies handling of single-type sets.
func TestExecuteAccumulatorScript_TypesMode_SingleType(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	ancestorID := "ancestor-types-single"
	role := "SingleTypeRecomposer"
	expectedTypesJSON := `["TestResult"]`

	// Add single type (should complete immediately)
	complete, duplicate, err := client.ExecuteAccumulatorScript(ctx, ancestorID, role, "claim-test-001", "types", "1", "TestResult", expectedTypesJSON)
	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.True(t, complete, "Single type should complete set immediately")

	// Verify 1 type accumulated
	accumulatedClaims, err := client.GetAccumulatedClaims(ctx, ancestorID, role)
	require.NoError(t, err)
	assert.Len(t, accumulatedClaims, 1)
}
