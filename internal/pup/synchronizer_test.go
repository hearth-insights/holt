package pup

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M5.1: Unit Tests for Synchronizer (Consumer Side)

// setupTestSynchronizer creates a test synchronizer with miniredis backend
func setupTestSynchronizer(t *testing.T, config *SynchronizeConfig) (*Synchronizer, *blackboard.Client, *miniredis.Miniredis) {
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, "test-instance")
	require.NoError(t, err)
	t.Cleanup(func() { bbClient.Close() })

	sync := NewSynchronizer(config, bbClient, "test-agent")

	return sync, bbClient, mr
}

// TestSynchronizer_IsPotentialTrigger_Matching tests matching wait_for type
func TestSynchronizer_IsPotentialTrigger_Matching(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor: []WaitCondition{
			{Type: "TestResult"},
			{Type: "LintResult"},
		},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	// Matching type
	artefact := &blackboard.Artefact{
		Type: "TestResult",
	}

	assert.True(t, sync.isPotentialTrigger(artefact))
}

// TestSynchronizer_IsPotentialTrigger_NonMatching tests non-matching type
func TestSynchronizer_IsPotentialTrigger_NonMatching(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor: []WaitCondition{
			{Type: "TestResult"},
			{Type: "LintResult"},
		},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	// Non-matching type
	artefact := &blackboard.Artefact{
		Type: "SecurityScan", // Not in wait_for list
	}

	assert.False(t, sync.isPotentialTrigger(artefact))
}

// TestSynchronizer_FindCommonAncestor_DirectParent tests finding direct parent
func TestSynchronizer_FindCommonAncestor_DirectParent(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor:      []WaitCondition{{Type: "TestResult"}},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor
	ancestor := &blackboard.Artefact{
		ID:              "ancestor-id",
		LogicalID:       "ancestor-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "CodeCommit",
		Payload:         "commit-abc",
		SourceArtefacts: []string{},
		ProducedByRole:  "coder",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create child
	child := &blackboard.Artefact{
		ID:              "child-id",
		LogicalID:       "child-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "TestResult",
		Payload:         "test-passed",
		SourceArtefacts: []string{"ancestor-id"}, // Direct parent
		ProducedByRole:  "tester",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, child))

	// Find ancestor from child
	found, err := sync.findCommonAncestor(ctx, child)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "ancestor-id", found.ID)
	assert.Equal(t, "CodeCommit", found.Type)
}

// TestSynchronizer_FindCommonAncestor_Grandparent tests finding grandparent ancestor
func TestSynchronizer_FindCommonAncestor_Grandparent(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor:      []WaitCondition{{Type: "DeployResult"}},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor (CodeCommit)
	ancestor := &blackboard.Artefact{
		ID:              "ancestor-id",
		LogicalID:       "ancestor-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "CodeCommit",
		Payload:         "commit-abc",
		SourceArtefacts: []string{},
		ProducedByRole:  "coder",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create intermediate (BuildResult)
	intermediate := &blackboard.Artefact{
		ID:              "intermediate-id",
		LogicalID:       "intermediate-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "BuildResult",
		Payload:         "build-ok",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "builder",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, intermediate))

	// Create grandchild (DeployResult)
	grandchild := &blackboard.Artefact{
		ID:              "grandchild-id",
		LogicalID:       "grandchild-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DeployResult",
		Payload:         "deployed",
		SourceArtefacts: []string{"intermediate-id"}, // Parent is intermediate
		ProducedByRole:  "deployer",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, grandchild))

	// Find ancestor from grandchild (should traverse upward)
	found, err := sync.findCommonAncestor(ctx, grandchild)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "ancestor-id", found.ID)
	assert.Equal(t, "CodeCommit", found.Type)
}

// TestSynchronizer_FindCommonAncestor_NotFound tests ancestor not found
func TestSynchronizer_FindCommonAncestor_NotFound(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit", // Looking for this type
		WaitFor:      []WaitCondition{{Type: "TestResult"}},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create artefact with no CodeCommit ancestor
	artefact := &blackboard.Artefact{
		ID:              "orphan-id",
		LogicalID:       "orphan-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "TestResult",
		Payload:         "test",
		SourceArtefacts: []string{}, // No parents
		ProducedByRole:  "tester",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, artefact))

	// Find ancestor (should return nil, not error)
	found, err := sync.findCommonAncestor(ctx, artefact)
	require.NoError(t, err)
	assert.Nil(t, found)
}

// TestSynchronizer_CheckDependencies_Named_AllPresent tests Named pattern with all types present
func TestSynchronizer_CheckDependencies_Named_AllPresent(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor: []WaitCondition{
			{Type: "TestResult"},   // Named pattern
			{Type: "LintResult"},   // Named pattern
			{Type: "SecurityScan"}, // Named pattern
		},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor
	ancestor := &blackboard.Artefact{
		ID:              "ancestor-id",
		LogicalID:       "ancestor-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "CodeCommit",
		Payload:         "commit-abc",
		SourceArtefacts: []string{},
		ProducedByRole:  "coder",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create all 3 required descendants
	testResult := &blackboard.Artefact{
		ID:              "test-id",
		LogicalID:       "test-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "TestResult",
		Payload:         "passed",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "tester",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, testResult))

	lintResult := &blackboard.Artefact{
		ID:              "lint-id",
		LogicalID:       "lint-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "LintResult",
		Payload:         "clean",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "linter",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, lintResult))

	securityScan := &blackboard.Artefact{
		ID:              "scan-id",
		LogicalID:       "scan-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "SecurityScan",
		Payload:         "no-vulns",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "scanner",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, securityScan))

	// Check dependencies (should be met)
	allReady, err := sync.checkAllDependenciesMet(ctx, ancestor)
	require.NoError(t, err)
	assert.True(t, allReady)
}

// TestSynchronizer_CheckDependencies_Named_Partial tests Named pattern with missing type
func TestSynchronizer_CheckDependencies_Named_Partial(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "CodeCommit",
		WaitFor: []WaitCondition{
			{Type: "TestResult"},
			{Type: "LintResult"},
			{Type: "SecurityScan"}, // This one is missing
		},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor
	ancestor := &blackboard.Artefact{
		ID:              "ancestor-id",
		LogicalID:       "ancestor-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "CodeCommit",
		Payload:         "commit-abc",
		SourceArtefacts: []string{},
		ProducedByRole:  "coder",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create only 2 of 3 required descendants
	testResult := &blackboard.Artefact{
		ID:              "test-id",
		LogicalID:       "test-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "TestResult",
		Payload:         "passed",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "tester",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, testResult))

	lintResult := &blackboard.Artefact{
		ID:              "lint-id",
		LogicalID:       "lint-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "LintResult",
		Payload:         "clean",
		SourceArtefacts: []string{"ancestor-id"},
		ProducedByRole:  "linter",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, lintResult))

	// SecurityScan is missing

	// Check dependencies (should NOT be met)
	allReady, err := sync.checkAllDependenciesMet(ctx, ancestor)
	require.NoError(t, err)
	assert.False(t, allReady)
}

// TestSynchronizer_CheckDependencies_ProducerDeclared_CorrectCount tests Producer-Declared with correct count
func TestSynchronizer_CheckDependencies_ProducerDeclared_CorrectCount(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor: []WaitCondition{
			{Type: "ProcessedRecord", CountFromMetadata: "batch_size"},
		},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor
	ancestor := &blackboard.Artefact{
		ID:              "batch-id",
		LogicalID:       "batch-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DataBatch",
		Payload:         "batch-123",
		SourceArtefacts: []string{},
		ProducedByRole:  "producer",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create 5 ProcessedRecord artefacts with batch_size=5 metadata
	for i := 1; i <= 5; i++ {
		record := &blackboard.Artefact{
			ID:              blackboard.NewID(),
			LogicalID:       blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "ProcessedRecord",
			Payload:         "record-data",
			SourceArtefacts: []string{"batch-id"},
			ProducedByRole:  "processor",
			Metadata:        `{"batch_size":"5"}`, // M5.1: Metadata injection
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, record))
	}

	// Check dependencies (should be met - 5 of 5 present)
	allReady, err := sync.checkAllDependenciesMet(ctx, ancestor)
	require.NoError(t, err)
	assert.True(t, allReady)
}

// TestSynchronizer_CheckDependencies_ProducerDeclared_PartialCount tests Producer-Declared with partial count
func TestSynchronizer_CheckDependencies_ProducerDeclared_PartialCount(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor: []WaitCondition{
			{Type: "ProcessedRecord", CountFromMetadata: "batch_size"},
		},
	}

	sync, bbClient, _ := setupTestSynchronizer(t, config)
	ctx := context.Background()

	// Create ancestor
	ancestor := &blackboard.Artefact{
		ID:              "batch-id",
		LogicalID:       "batch-logical",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "DataBatch",
		Payload:         "batch-123",
		SourceArtefacts: []string{},
		ProducedByRole:  "producer",
		Metadata:        "{}",
	}
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create only 3 of 5 expected ProcessedRecord artefacts
	for i := 1; i <= 3; i++ {
		record := &blackboard.Artefact{
			ID:              blackboard.NewID(),
			LogicalID:       blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "ProcessedRecord",
			Payload:         "record-data",
			SourceArtefacts: []string{"batch-id"},
			ProducedByRole:  "processor",
			Metadata:        `{"batch_size":"5"}`, // Expects 5 total
		}
		require.NoError(t, bbClient.CreateArtefact(ctx, record))
	}

	// Check dependencies (should NOT be met - only 3 of 5 present)
	allReady, err := sync.checkAllDependenciesMet(ctx, ancestor)
	require.NoError(t, err)
	assert.False(t, allReady)
}

// TestSynchronizer_GetExpectedCount_Valid tests valid metadata parsing
func TestSynchronizer_GetExpectedCount_Valid(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor:      []WaitCondition{{Type: "Record", CountFromMetadata: "batch_size"}},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	artefact := &blackboard.Artefact{
		Metadata: `{"batch_size":"10"}`,
	}

	count, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
	require.NoError(t, err)
	assert.Equal(t, 10, count)
}

// TestSynchronizer_GetExpectedCount_MissingKey tests error for missing metadata key
func TestSynchronizer_GetExpectedCount_MissingKey(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor:      []WaitCondition{{Type: "Record", CountFromMetadata: "batch_size"}},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	artefact := &blackboard.Artefact{
		Metadata: `{"other_key":"value"}`, // Missing batch_size
	}

	_, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestSynchronizer_GetExpectedCount_InvalidInteger tests error for non-integer value
func TestSynchronizer_GetExpectedCount_InvalidInteger(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor:      []WaitCondition{{Type: "Record", CountFromMetadata: "batch_size"}},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	artefact := &blackboard.Artefact{
		Metadata: `{"batch_size":"not-a-number"}`,
	}

	_, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid integer")
}

// TestSynchronizer_GetExpectedCount_NegativeValue tests error for negative/zero count
func TestSynchronizer_GetExpectedCount_NegativeValue(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor:      []WaitCondition{{Type: "Record", CountFromMetadata: "batch_size"}},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	tests := []struct {
		name     string
		metadata string
	}{
		{"zero", `{"batch_size":"0"}`},
		{"negative", `{"batch_size":"-5"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artefact := &blackboard.Artefact{Metadata: tt.metadata}
			_, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be positive")
		})
	}
}

// TestSynchronizer_GetExpectedCount_MalformedJSON tests error for invalid JSON
func TestSynchronizer_GetExpectedCount_MalformedJSON(t *testing.T) {
	config := &SynchronizeConfig{
		AncestorType: "DataBatch",
		WaitFor:      []WaitCondition{{Type: "Record", CountFromMetadata: "batch_size"}},
	}

	sync, _, _ := setupTestSynchronizer(t, config)

	artefact := &blackboard.Artefact{
		Metadata: `{invalid json}`,
	}

	_, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid metadata JSON")
}
