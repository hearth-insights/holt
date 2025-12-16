package blackboard

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateOrVersionKnowledge_FirstCreation tests creating a new Knowledge artefact
func TestCreateOrVersionKnowledge_FirstCreation(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	knowledge, err := client.CreateOrVersionKnowledge(
		ctx,
		"test-docs",
		"This is the documentation content",
		[]string{"coder*", "reviewer"},
		"thread-123",
		"test-agent",
	)

	require.NoError(t, err)
	assert.NotNil(t, knowledge)
	assert.Equal(t, "test-docs", knowledge.Header.Type)
	assert.Equal(t, "This is the documentation content", knowledge.Payload.Content)
	assert.Equal(t, 1, knowledge.Header.Version)
	assert.Equal(t, StructuralTypeKnowledge, knowledge.Header.StructuralType)
	assert.Equal(t, []string{"coder*", "reviewer"}, knowledge.Header.ContextForRoles)
	assert.Equal(t, "test-agent", knowledge.Header.ProducedByRole)

	// Verify it's in the knowledge_index
	indexKey := KnowledgeIndexKey("test-instance")
	logicalID, err := client.rdb.HGet(ctx, indexKey, "test-docs").Result()
	require.NoError(t, err)
	assert.Equal(t, knowledge.Header.LogicalThreadID, logicalID)

	// Verify it's in thread_context
	threadContextKey := ThreadContextKey("test-instance", "thread-123")
	isMember, err := client.rdb.SIsMember(ctx, threadContextKey, knowledge.ID).Result()
	require.NoError(t, err)
	assert.True(t, isMember)
}

// TestCreateOrVersionKnowledge_Versioning tests that subsequent calls create new versions
func TestCreateOrVersionKnowledge_Versioning(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	// Create v1
	v1, err := client.CreateOrVersionKnowledge(
		ctx,
		"sdk-docs",
		"Version 1 content",
		[]string{"*"},
		"thread-abc",
		"agent-1",
	)
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Header.Version)

	// Create v2 (same knowledge_name)
	v2, err := client.CreateOrVersionKnowledge(
		ctx,
		"sdk-docs",
		"Version 2 content - updated",
		[]string{"*"},
		"thread-abc",
		"agent-1",
	)
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Header.Version)
	assert.Equal(t, v1.Header.LogicalThreadID, v2.Header.LogicalThreadID) // Same logical thread
	assert.NotEqual(t, v1.ID, v2.ID)                                      // Different artefact IDs

	// Create v3
	v3, err := client.CreateOrVersionKnowledge(
		ctx,
		"sdk-docs",
		"Version 3 content - final",
		[]string{"backend-*"},
		"thread-xyz",
		"agent-2",
	)
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Header.Version)
	assert.Equal(t, v1.Header.LogicalThreadID, v3.Header.LogicalThreadID)

	// Verify knowledge_index still points to same logical_id
	indexKey := KnowledgeIndexKey("test-instance")
	logicalID, err := client.rdb.HGet(ctx, indexKey, "sdk-docs").Result()
	require.NoError(t, err)
	assert.Equal(t, v1.Header.LogicalThreadID, logicalID)

	// Verify thread tracking
	threadKey := ThreadKey("test-instance", v1.Header.LogicalThreadID)
	members, err := client.rdb.ZRange(ctx, threadKey, 0, -1).Result()
	require.NoError(t, err)
	assert.Len(t, members, 3)
	assert.Contains(t, members, v1.ID)
	assert.Contains(t, members, v2.ID)
	assert.Contains(t, members, v3.ID)
}

// TestCreateOrVersionKnowledge_DefaultTargetRoles tests that empty target_roles defaults to ["*"]
func TestCreateOrVersionKnowledge_DefaultTargetRoles(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	knowledge, err := client.CreateOrVersionKnowledge(
		ctx,
		"global-config",
		"config content",
		[]string{}, // Empty - should default to ["*"]
		"thread-1",
		"cli",
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, knowledge.Header.ContextForRoles)
}

// TestCreateOrVersionKnowledge_GlobalThread tests manually provisioned knowledge (empty threadLogicalID)
func TestCreateOrVersionKnowledge_GlobalThread(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	knowledge, err := client.CreateOrVersionKnowledge(
		ctx,
		"manual-provision",
		"manually provisioned content",
		[]string{"coder*"},
		"", // Empty thread = global knowledge
		"cli",
	)

	require.NoError(t, err)
	assert.NotNil(t, knowledge)

	// Verify it's in the "global" thread_context
	globalThreadKey := ThreadContextKey("test-instance", "global")
	isMember, err := client.rdb.SIsMember(ctx, globalThreadKey, knowledge.ID).Result()
	require.NoError(t, err)
	assert.True(t, isMember)
}

// TestCreateOrVersionKnowledge_RaceCondition tests atomic behavior with concurrent creation
// Note: miniredis doesn't perfectly emulate Redis concurrency, so this test verifies
// that the Lua script logic is correct rather than testing true race conditions
func TestCreateOrVersionKnowledge_RaceCondition(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	const goroutineCount = 10
	const knowledgeName = "concurrent-docs"

	var wg sync.WaitGroup
	results := make([]*Artefact, goroutineCount)
	errors := make([]error, goroutineCount)

	// Launch concurrent goroutines trying to create the same knowledge
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			knowledge, err := client.CreateOrVersionKnowledge(
				ctx,
				knowledgeName,
				"concurrent content",
				[]string{"*"},
				uuid.New().String(), // Different threads
				"concurrent-agent",
			)
			results[idx] = knowledge
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errors {
		require.NoError(t, err, "goroutine %d failed", i)
	}

	// All should have same logical_id (from knowledge_index)
	firstLogicalID := results[0].Header.LogicalThreadID
	for i, k := range results {
		assert.Equal(t, firstLogicalID, k.Header.LogicalThreadID, "goroutine %d has different logical_id", i)
	}

	// Collect all versions
	versions := make(map[int]int) // version -> count
	for _, k := range results {
		versions[k.Header.Version]++
	}

	// Total versions should sum to goroutineCount
	totalVersions := 0
	for _, count := range versions {
		totalVersions += count
	}
	assert.Equal(t, goroutineCount, totalVersions, "total version count should equal goroutine count")

	// Versions should be between 1 and goroutineCount
	for v := range versions {
		assert.True(t, v >= 1 && v <= goroutineCount, "version %d out of range", v)
	}

	// knowledge_index should have exactly one entry
	indexKey := KnowledgeIndexKey("test-instance")
	logicalID, err := client.rdb.HGet(ctx, indexKey, knowledgeName).Result()
	require.NoError(t, err)
	assert.Equal(t, firstLogicalID, logicalID)
}

// TestCreateOrVersionKnowledge_MultipleKnowledgeNames tests different knowledge names are independent
func TestCreateOrVersionKnowledge_MultipleKnowledgeNames(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	k1, err := client.CreateOrVersionKnowledge(ctx, "docs-1", "content 1", []string{"*"}, "thread-1", "agent")
	require.NoError(t, err)

	k2, err := client.CreateOrVersionKnowledge(ctx, "docs-2", "content 2", []string{"*"}, "thread-1", "agent")
	require.NoError(t, err)

	k3, err := client.CreateOrVersionKnowledge(ctx, "docs-3", "content 3", []string{"*"}, "thread-1", "agent")
	require.NoError(t, err)

	// All should have different logical_ids
	assert.NotEqual(t, k1.Header.LogicalThreadID, k2.Header.LogicalThreadID)
	assert.NotEqual(t, k1.Header.LogicalThreadID, k3.Header.LogicalThreadID)
	assert.NotEqual(t, k2.Header.LogicalThreadID, k3.Header.LogicalThreadID)

	// All should be v1
	assert.Equal(t, 1, k1.Header.Version)
	assert.Equal(t, 1, k2.Header.Version)
	assert.Equal(t, 1, k3.Header.Version)

	// Verify knowledge_index has all three
	indexKey := KnowledgeIndexKey("test-instance")
	entries, err := client.rdb.HGetAll(ctx, indexKey).Result()
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	assert.Equal(t, k1.Header.LogicalThreadID, entries["docs-1"])
	assert.Equal(t, k2.Header.LogicalThreadID, entries["docs-2"])
	assert.Equal(t, k3.Header.LogicalThreadID, entries["docs-3"])
}

// TestCreateOrVersionKnowledge_PreservesSourceArtefacts tests Knowledge artefacts have empty sources
func TestCreateOrVersionKnowledge_PreservesSourceArtefacts(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	knowledge, err := client.CreateOrVersionKnowledge(
		ctx,
		"isolated-knowledge",
		"content",
		[]string{"*"},
		"thread-1",
		"agent",
	)

	require.NoError(t, err)
	assert.Empty(t, knowledge.Header.ParentHashes, "Knowledge artefacts should have empty source_artefacts")
}

// TestCreateOrVersionKnowledge_CreatedAtMsPopulated tests timestamp is set
func TestCreateOrVersionKnowledge_CreatedAtMsPopulated(t *testing.T) {
	client, _ := setupTestClient(t)
	ctx := context.Background()

	knowledge, err := client.CreateOrVersionKnowledge(
		ctx,
		"timestamped",
		"content",
		[]string{"*"},
		"thread-1",
		"agent",
	)

	require.NoError(t, err)
	assert.Greater(t, knowledge.Header.CreatedAtMs, int64(0), "CreatedAtMs should be populated")
}
