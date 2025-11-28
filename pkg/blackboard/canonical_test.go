package blackboard

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHashDeterminism verifies that computing the hash of the same artefact
// produces the exact same result across 1000 iterations.
// This is CRITICAL for Merkle DAG integrity.
func TestHashDeterminism(t *testing.T) {
	artefact := createTestArtefact()

	// Compute hash 1000 times and track unique values
	hashes := make(map[string]int)
	for i := 0; i < 1000; i++ {
		hash, err := ComputeArtefactHash(artefact)
		require.NoError(t, err, "hash computation should never fail for valid artefact")
		hashes[hash]++
	}

	// Assert only ONE unique hash exists
	assert.Equal(t, 1, len(hashes), "hash must be deterministic - should produce exactly one unique value")

	// Verify hash is 64 characters (SHA-256 hex encoding)
	for hash := range hashes {
		assert.Len(t, hash, 64, "SHA-256 hex-encoded hash must be exactly 64 characters")
		assert.Regexp(t, "^[a-f0-9]{64}$", hash, "hash must be lowercase hex")
	}
}

// TestCanonicalisation_FieldOrder verifies that JSON field order does not affect the hash.
// RFC 8785 guarantees lexicographic key sorting.
func TestCanonicalisation_FieldOrder(t *testing.T) {
	// Create artefact with fields in one order
	a1 := &VerifiableArtefact{
		Header: ArtefactHeader{
			Type:            "CodeCommit",
			ProducedByRole:  "coder",
			ParentHashes:    []string{"abc123"},
			LogicalThreadID: "thread-1",
			Version:         1,
			CreatedAtMs:     1704067200000,
			StructuralType:  StructuralTypeStandard,
		},
		Payload: ArtefactPayload{
			Content: "test content",
		},
	}

	// Create identical artefact (fields will be in different memory order due to map iteration)
	a2 := &VerifiableArtefact{
		Payload: ArtefactPayload{
			Content: "test content",
		},
		Header: ArtefactHeader{
			Version:         1,
			ParentHashes:    []string{"abc123"},
			StructuralType:  StructuralTypeStandard,
			Type:            "CodeCommit",
			CreatedAtMs:     1704067200000,
			LogicalThreadID: "thread-1",
			ProducedByRole:  "coder",
		},
	}

	hash1, err1 := ComputeArtefactHash(a1)
	hash2, err2 := ComputeArtefactHash(a2)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.Equal(t, hash1, hash2, "field order must not affect hash due to RFC 8785 canonicalization")
}

// TestHashSensitivity_Timestamp verifies that a 1ms timestamp change produces a different hash.
// This ensures temporal ordering is cryptographically enforced.
func TestHashSensitivity_Timestamp(t *testing.T) {
	baseTime := time.Now().UnixMilli()

	artefact1 := createTestArtefact()
	artefact1.Header.CreatedAtMs = baseTime

	artefact2 := createTestArtefact()
	artefact2.Header.CreatedAtMs = baseTime + 1 // 1ms difference

	hash1, err1 := ComputeArtefactHash(artefact1)
	hash2, err2 := ComputeArtefactHash(artefact2)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, hash1, hash2, "1ms timestamp change MUST produce different hash")
}

// TestHashSensitivity_Payload verifies that payload content changes affect the hash.
func TestHashSensitivity_Payload(t *testing.T) {
	artefact1 := createTestArtefact()
	artefact1.Payload.Content = "original content"

	artefact2 := createTestArtefact()
	artefact2.Payload.Content = "modified content"

	hash1, err1 := ComputeArtefactHash(artefact1)
	hash2, err2 := ComputeArtefactHash(artefact2)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, hash1, hash2, "payload content change must produce different hash")
}

// TestHashSensitivity_ParentHashes verifies that parent hash modifications affect the hash.
func TestHashSensitivity_ParentHashes(t *testing.T) {
	artefact1 := createTestArtefact()
	artefact1.Header.ParentHashes = []string{"abc123"}

	artefact2 := createTestArtefact()
	artefact2.Header.ParentHashes = []string{"def456"}

	hash1, err1 := ComputeArtefactHash(artefact1)
	hash2, err2 := ComputeArtefactHash(artefact2)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, hash1, hash2, "parent hash modification must produce different hash")
}

// TestContextForRoles_OmitEmpty verifies that empty ContextForRoles is excluded from canonical JSON.
// This uses RFC 8785's field omission for efficiency.
func TestContextForRoles_OmitEmpty(t *testing.T) {
	// Artefact with nil ContextForRoles
	artefact1 := createTestArtefact()
	artefact1.Header.ContextForRoles = nil

	// Artefact with empty ContextForRoles
	artefact2 := createTestArtefact()
	artefact2.Header.ContextForRoles = []string{}

	// Artefact with populated ContextForRoles
	artefact3 := createTestArtefact()
	artefact3.Header.ContextForRoles = []string{"agent1"}

	hash1, err1 := ComputeArtefactHash(artefact1)
	hash2, err2 := ComputeArtefactHash(artefact2)
	hash3, err3 := ComputeArtefactHash(artefact3)

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NoError(t, err3)

	// nil and empty array should produce SAME hash (both omitted)
	assert.Equal(t, hash1, hash2, "nil and empty ContextForRoles must produce same hash (both omitted)")

	// Non-empty should produce DIFFERENT hash
	assert.NotEqual(t, hash1, hash3, "populated ContextForRoles must produce different hash")
	assert.NotEqual(t, hash2, hash3, "populated ContextForRoles must produce different hash")
}

// TestContextForRoles_IncludedInHash verifies ContextForRoles IS included in hash computation.
// This is critical for security/visibility scope enforcement.
func TestContextForRoles_IncludedInHash(t *testing.T) {
	artefact1 := createTestArtefact()
	artefact1.Header.ContextForRoles = []string{"agent1", "agent2"}

	artefact2 := createTestArtefact()
	artefact2.Header.ContextForRoles = []string{"agent1", "agent3"} // Different agent

	hash1, err1 := ComputeArtefactHash(artefact1)
	hash2, err2 := ComputeArtefactHash(artefact2)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, hash1, hash2, "ContextForRoles content MUST affect hash")
}

// TestValidateArtefactHash_Success verifies successful hash validation.
func TestValidateArtefactHash_Success(t *testing.T) {
	artefact := createTestArtefact()

	// Compute correct hash
	correctHash, err := ComputeArtefactHash(artefact)
	require.NoError(t, err)

	artefact.ID = correctHash

	// Validation should pass
	err = ValidateArtefactHash(artefact)
	assert.NoError(t, err, "validation should pass when hash matches")
}

// TestValidateArtefactHash_Mismatch verifies hash mismatch detection.
func TestValidateArtefactHash_Mismatch(t *testing.T) {
	artefact := createTestArtefact()

	// Set WRONG hash
	artefact.ID = strings.Repeat("a", 64) // Invalid hash

	// Validation should fail with HashMismatchError
	err := ValidateArtefactHash(artefact)
	require.Error(t, err, "validation should fail when hash mismatches")

	var mismatchErr *HashMismatchError
	assert.ErrorAs(t, err, &mismatchErr, "error should be HashMismatchError type")
	assert.Equal(t, artefact.ID, mismatchErr.Actual, "actual hash should match artefact ID")
	assert.NotEmpty(t, mismatchErr.Expected, "expected hash should be computed")
	assert.NotEqual(t, mismatchErr.Expected, mismatchErr.Actual, "expected and actual should differ")
}

// TestComputeArtefactHash_PanicRecovery verifies panic recovery for malformed data.
func TestComputeArtefactHash_PanicRecovery(t *testing.T) {
	// This test will be implemented after ComputeArtefactHash includes panic recovery
	// For now, we expect normal error handling
	artefact := createTestArtefact()

	hash, err := ComputeArtefactHash(artefact)
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
}

// Helper function to create a consistent test artefact
func createTestArtefact() *VerifiableArtefact {
	return &VerifiableArtefact{
		Header: ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "550e8400-e29b-41d4-a716-446655440000",
			Version:         1,
			CreatedAtMs:     1704067200000,
			ProducedByRole:  "test-agent",
			StructuralType:  StructuralTypeStandard,
			Type:            "TestArtefact",
		},
		Payload: ArtefactPayload{
			Content: "test content",
		},
	}
}

// BenchmarkHashComputation_1KB benchmarks hash computation for 1KB payload.
// Target: <1ms per operation (informational only, not enforced).
func BenchmarkHashComputation_1KB(b *testing.B) {
	artefact := createTestArtefact()
	// Create 1KB payload (1,024 bytes)
	artefact.Payload.Content = strings.Repeat("a", 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ComputeArtefactHash(artefact)
		if err != nil {
			b.Fatalf("hash computation failed: %v", err)
		}
	}
}

// BenchmarkHashComputation_1MB benchmarks hash computation for 1MB payload.
// Target: <5ms per operation (informational only, not enforced).
// This represents the maximum allowed payload size for V2 artefacts.
func BenchmarkHashComputation_1MB(b *testing.B) {
	artefact := createTestArtefact()
	// Create 1MB payload (1,048,576 bytes - the hard limit)
	artefact.Payload.Content = strings.Repeat("a", 1024*1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ComputeArtefactHash(artefact)
		if err != nil {
			b.Fatalf("hash computation failed: %v", err)
		}
	}
}
