// Package blackboard provides cryptographically verifiable artefact types for M4.6.
// This file implements the V2 content-addressable Merkle DAG architecture where
// every artefact's identity is its SHA-256 content hash.
package blackboard

// VerifiableArtefact replaces the v1 Artefact struct for M4.6.
// The ID field is the SHA-256 hash (hex-encoded) of the canonicalized Header and Payload.
// This is a V2 clean break - no backward compatibility with UUID-based artefacts.
type VerifiableArtefact struct {
	// ID is the SHA-256 hash (hex-encoded, 64 characters)
	// This is the artefact's immutable, content-derived address.
	ID string `json:"id"`

	Header  ArtefactHeader  `json:"header"`
	Payload ArtefactPayload `json:"payload"`
}

// ArtefactHeader contains metadata and provenance links.
// All fields in this struct are included in the hash computation.
// CRITICAL: Any modification to field names, types, or tags will change hash computation.
type ArtefactHeader struct {
	// ParentHashes replaces v1's SourceArtefacts - now stores SHA-256 hashes, not UUIDs
	// Empty array for root artefacts (e.g., GoalDefined)
	ParentHashes []string `json:"parent_hashes"`

	// LogicalThreadID groups versions of the same conceptual artefact
	// Retained for O(1) "latest version" lookups via Redis ZSET
	// V1 (new thread): Generate new UUID
	// V2+ (versions): Inherit UUID from parent
	LogicalThreadID string `json:"logical_thread_id"` // UUID format

	// Version counter within the logical thread (starts at 1)
	Version int `json:"version"`

	// Timestamp of creation (part of hashed content - CRITICAL for temporal ordering)
	CreatedAtMs int64 `json:"created_at_ms"` // Unix milliseconds

	// Agent that produced this artefact
	ProducedByRole string `json:"produced_by_role"`

	// Orchestration role (hardcoded enum in StructuralType)
	StructuralType StructuralType `json:"structural_type"`

	// User-defined domain type (opaque to orchestrator)
	Type string `json:"type"`

	// M4.3: Context caching - INCLUDED in hash for security/visibility scope
	// Uses omitempty: empty/nil slice excluded from canonical JSON to save space
	ContextForRoles []string `json:"context_for_roles,omitempty"`
}

// ArtefactPayload is the actual content.
// HARD LIMIT: 1MB (1,048,576 bytes). Larger content must be written to disk/git
// and referenced via hash in the payload.
type ArtefactPayload struct {
	Content string `json:"content"` // Max 1MB
}

// MaxPayloadSize is the hard limit for artefact payload content.
// This prevents Redis memory pressure and ensures fast hash computation.
const MaxPayloadSize = 1 * 1024 * 1024 // 1MB

// HashMismatchError is returned when artefact hash verification fails.
// This indicates potential tampering or data corruption.
type HashMismatchError struct {
	Expected string // Hash computed by verifier (Orchestrator)
	Actual   string // Hash claimed in artefact ID (from Pup)
}

func (e *HashMismatchError) Error() string {
	return "hash mismatch: expected " + e.Expected + ", got " + e.Actual
}
