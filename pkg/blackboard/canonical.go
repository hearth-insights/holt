package blackboard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	canonicaljson "github.com/gibson042/canonicaljson-go"
)

// ComputeArtefactHash computes the SHA-256 hash of a Artefact.
// Uses RFC 8785 JSON Canonicalization Scheme to ensure deterministic serialization
// across different implementations, field orders, and machine architectures.
//
// The hash is computed over the canonical representation of Header + Payload.
// The ID field is NOT included in the hash computation (it IS the hash).
//
// Returns the hash as a lowercase hex-encoded string (64 characters).
func ComputeArtefactHash(a *Artefact) (string, error) {
	// Panic recovery for malformed data that might cause canonicaljson to panic
	defer func() {
		if r := recover(); r != nil {
			// Convert panic to error for graceful handling
			err := fmt.Errorf("canonicalization panic: %v", r)
			panic(err) // Re-panic with formatted error
		}
	}()

	// Step 1: Create canonical representation (Header + Payload only, NOT ID)
	// The struct field order here doesn't matter - RFC 8785 will sort keys
	canonical := struct {
		Header  ArtefactHeader  `json:"header"`
		Payload ArtefactPayload `json:"payload"`
	}{
		Header:  a.Header,
		Payload: a.Payload,
	}

	// Step 2: Canonicalize using RFC 8785
	// This guarantees:
	// - Lexicographic key sorting
	// - No insignificant whitespace
	// - Deterministic number representation
	// - Consistent Unicode escaping
	canonicalBytes, err := canonicaljson.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalization failed: %w", err)
	}

	// fmt.Printf("DEBUG CANONICAL JSON (%s): %s\n", a.ID, string(canonicalBytes))

	// Step 3: Hash with SHA-256
	hash := sha256.Sum256(canonicalBytes)

	// Step 4: Return lowercase hex-encoded hash (64 characters)
	return hex.EncodeToString(hash[:]), nil
}

// ValidateArtefactHash verifies that the provided ID matches the computed hash.
// This is the Orchestrator's verification step in the Prover/Verifier contract.
//
// Returns nil if validation passes.
// Returns *HashMismatchError if the hash doesn't match (potential tampering).
func ValidateArtefactHash(a *Artefact) error {
	computed, err := ComputeArtefactHash(a)
	if err != nil {
		return fmt.Errorf("hash computation failed during validation: %w", err)
	}

	if computed != a.ID {
		return &HashMismatchError{
			Expected: computed,
			Actual:   a.ID,
		}
	}

	return nil
}
