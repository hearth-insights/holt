package blackboard

import (
	"fmt"
	"regexp"
)

// Validate checks if the ArtefactPayload has valid field values.
// Primary check: enforces 1MB hard limit on payload content.
//
// Returns an error if validation fails.
func (p *ArtefactPayload) Validate() error {
	if len(p.Content) > MaxPayloadSize {
		return fmt.Errorf(
			"payload exceeds 1MB limit: %d bytes (write large content to disk/git and store hash)",
			len(p.Content),
		)
	}
	return nil
}

// Validate checks if the Artefact has valid field values.
// Does NOT verify the cryptographic hash - use ValidateArtefactHash for that.
//
// Returns an error if any validation fails.
func (a *Artefact) Validate() error {
	// Validate hash ID format (64 lowercase hex characters)
	if !isValidSHA256Hash(a.ID) {
		return fmt.Errorf("invalid hash ID: must be 64 lowercase hex characters, got %q", a.ID)
	}

	// Validate logical thread ID (UUID format)
	if a.Header.LogicalThreadID == "" {
		return fmt.Errorf("logical_thread_id cannot be empty")
	}

	// Validate version (must be >= 1)
	if a.Header.Version < 1 {
		return fmt.Errorf("invalid version: must be >= 1, got %d", a.Header.Version)
	}

	// Validate structural type
	if err := a.Header.StructuralType.Validate(); err != nil {
		return fmt.Errorf("invalid structural type: %w", err)
	}

	// Validate user type (cannot be empty)
	if a.Header.Type == "" {
		return fmt.Errorf("artefact type cannot be empty")
	}

	// Validate producer role (cannot be empty)
	if a.Header.ProducedByRole == "" {
		return fmt.Errorf("produced_by_role cannot be empty")
	}

	// Validate timestamp (must be positive)
	if a.Header.CreatedAtMs <= 0 {
		return fmt.Errorf("invalid timestamp: must be positive Unix milliseconds, got %d", a.Header.CreatedAtMs)
	}

	// Validate parent hashes (all must be valid SHA-256 hashes)
	for i, parentHash := range a.Header.ParentHashes {
		if !isValidSHA256Hash(parentHash) {
			return fmt.Errorf("invalid parent hash at index %d: must be 64 lowercase hex characters", i)
		}
	}

	// Validate payload
	if err := a.Payload.Validate(); err != nil {
		return fmt.Errorf("payload validation failed: %w", err)
	}

	return nil
}

// isValidSHA256Hash checks if a string is a valid SHA-256 hash (64 lowercase hex characters).
func isValidSHA256Hash(s string) bool {
	if len(s) != 64 {
		return false
	}
	matched, _ := regexp.MatchString("^[a-f0-9]{64}$", s)
	return matched
}
