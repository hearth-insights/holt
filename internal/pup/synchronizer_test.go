package pup

import (
	"context"
	"testing"
	"time"

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
		Header: blackboard.ArtefactHeader{
			Type: "TestResult",
		},
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
		Header: blackboard.ArtefactHeader{
			Type: "SecurityScan", // Not in wait_for list
		},
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "coder",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "commit-abc",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create child
	child := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestResult",
			ProducedByRole:  "tester",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID}, // Direct parent
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test-passed",
		},
	}
	childHash, err := blackboard.ComputeArtefactHash(child)
	require.NoError(t, err)
	child.ID = childHash
	require.NoError(t, bbClient.CreateArtefact(ctx, child))

	// Find ancestor from child
	found, err := sync.findCommonAncestor(ctx, child)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, ancestor.ID, found.ID)
	assert.Equal(t, "CodeCommit", found.Header.Type)
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "coder",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "commit-abc",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create intermediate (BuildResult)
	intermediate := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "BuildResult",
			ProducedByRole:  "builder",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "build-ok",
		},
	}
	intHash, err := blackboard.ComputeArtefactHash(intermediate)
	require.NoError(t, err)
	intermediate.ID = intHash
	require.NoError(t, bbClient.CreateArtefact(ctx, intermediate))

	// Create grandchild (DeployResult)
	grandchild := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "DeployResult",
			ProducedByRole:  "deployer",
			Metadata:        "{}",
			ParentHashes:    []string{intermediate.ID}, // Parent is intermediate
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "deployed",
		},
	}
	gcHash, err := blackboard.ComputeArtefactHash(grandchild)
	require.NoError(t, err)
	grandchild.ID = gcHash
	require.NoError(t, bbClient.CreateArtefact(ctx, grandchild))

	// Find ancestor from grandchild (should traverse upward)
	found, err := sync.findCommonAncestor(ctx, grandchild)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, ancestor.ID, found.ID)
	assert.Equal(t, "CodeCommit", found.Header.Type)
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestResult",
			ProducedByRole:  "tester",
			Metadata:        "{}",
			ParentHashes:    []string{}, // No parents
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "test",
		},
	}
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(t, err)
	artefact.ID = hash
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "coder",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "commit-abc",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create all 3 required descendants
	testResult := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestResult",
			ProducedByRole:  "tester",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "passed",
		},
	}
	testHash, err := blackboard.ComputeArtefactHash(testResult)
	require.NoError(t, err)
	testResult.ID = testHash
	require.NoError(t, bbClient.CreateArtefact(ctx, testResult))

	lintResult := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "LintResult",
			ProducedByRole:  "linter",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "clean",
		},
	}
	lintHash, err := blackboard.ComputeArtefactHash(lintResult)
	require.NoError(t, err)
	lintResult.ID = lintHash
	require.NoError(t, bbClient.CreateArtefact(ctx, lintResult))

	securityScan := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "SecurityScan",
			ProducedByRole:  "scanner",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "no-vulns",
		},
	}
	scanHash, err := blackboard.ComputeArtefactHash(securityScan)
	require.NoError(t, err)
	securityScan.ID = scanHash
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "CodeCommit",
			ProducedByRole:  "coder",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "commit-abc",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create only 2 of 3 required descendants
	testResult := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestResult",
			ProducedByRole:  "tester",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "passed",
		},
	}
	testHash, err := blackboard.ComputeArtefactHash(testResult)
	require.NoError(t, err)
	testResult.ID = testHash
	require.NoError(t, bbClient.CreateArtefact(ctx, testResult))

	lintResult := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "LintResult",
			ProducedByRole:  "linter",
			Metadata:        "{}",
			ParentHashes:    []string{ancestor.ID},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "clean",
		},
	}
	lintHash, err := blackboard.ComputeArtefactHash(lintResult)
	require.NoError(t, err)
	lintResult.ID = lintHash
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "DataBatch",
			ProducedByRole:  "producer",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "batch-123",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create 5 ProcessedRecord artefacts with batch_size=5 metadata
	for i := 1; i <= 5; i++ {
		record := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "ProcessedRecord",
				ProducedByRole:  "processor",
				Metadata:        `{"batch_size":"5"}`, // M5.1: Metadata injection
				ParentHashes:    []string{ancestor.ID},
				CreatedAtMs:     time.Now().UnixMilli(),
			},
			Payload: blackboard.ArtefactPayload{
				Content: "record-data",
			},
		}
		hash, err := blackboard.ComputeArtefactHash(record)
		require.NoError(t, err)
		record.ID = hash
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
		Header: blackboard.ArtefactHeader{
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "DataBatch",
			ProducedByRole:  "producer",
			Metadata:        "{}",
			ParentHashes:    []string{},
			CreatedAtMs:     time.Now().UnixMilli(),
		},
		Payload: blackboard.ArtefactPayload{
			Content: "batch-123",
		},
	}
	ancestorHash, err := blackboard.ComputeArtefactHash(ancestor)
	require.NoError(t, err)
	ancestor.ID = ancestorHash
	require.NoError(t, bbClient.CreateArtefact(ctx, ancestor))

	// Create only 3 of 5 expected ProcessedRecord artefacts
	for i := 1; i <= 3; i++ {
		record := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: blackboard.NewID(),
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "ProcessedRecord",
				ProducedByRole:  "processor",
				Metadata:        `{"batch_size":"5"}`, // Expects 5 total
				ParentHashes:    []string{ancestor.ID},
				CreatedAtMs:     time.Now().UnixMilli(),
			},
			Payload: blackboard.ArtefactPayload{
				Content: "record-data",
			},
		}
		hash, err := blackboard.ComputeArtefactHash(record)
		require.NoError(t, err)
		record.ID = hash
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
		Header: blackboard.ArtefactHeader{
			Metadata: `{"batch_size":"10"}`,
		},
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
		Header: blackboard.ArtefactHeader{
			Metadata: `{"other_key":"value"}`, // Missing batch_size
		},
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
		Header: blackboard.ArtefactHeader{
			Metadata: `{"batch_size":"not-a-number"}`,
		},
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
			artefact := &blackboard.Artefact{
				Header: blackboard.ArtefactHeader{
					Metadata: tt.metadata,
				},
			}
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
		Header: blackboard.ArtefactHeader{
			Metadata: `{invalid json}`,
		},
	}

	_, err := sync.getExpectedCountFromMetadata(artefact, "batch_size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid metadata JSON")
}
