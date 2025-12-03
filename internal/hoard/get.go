package hoard

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/google/uuid"
)

// GetArtefact retrieves a single artefact by ID and prints it as JSON.
// Resolves short IDs if needed.
func GetArtefact(ctx context.Context, bbClient *blackboard.Client, artefactID string, w io.Writer) error {
	// Validate artefact ID format
	// Allow UUID (36 chars) or SHA-256 Hash (64 chars)
	isValidUUID := false
	if _, err := uuid.Parse(artefactID); err == nil {
		isValidUUID = true
	}

	isValidHash := false
	if len(artefactID) == 64 {
		// Simple length check for hash, could add hex validation if needed
		isValidHash = true
	}

	if !isValidUUID && !isValidHash {
		return fmt.Errorf("invalid artefact ID format: must be a valid UUID or SHA-256 hash")
	}

	// Fetch artefact from blackboard
	artefact, err := bbClient.GetArtefact(ctx, artefactID)
	if err != nil {
		if blackboard.IsNotFound(err) {
			return &ArtefactNotFoundError{ArtefactID: artefactID}
		}
		return fmt.Errorf("failed to fetch artefact: %w", err)
	}

	// Resolve relationships
	relationships, err := ResolveRelationships(ctx, bbClient, artefact)
	if err != nil {
		// Log warning to stderr but continue
		fmt.Fprintf(os.Stderr, "⚠️  Failed to resolve relationships: %v\n", err)
	}

	// Format and write as JSON
	if err := FormatSingleJSON(w, artefact, relationships); err != nil {
		return fmt.Errorf("failed to format artefact: %w", err)
	}

	return nil
}

// ArtefactNotFoundError represents a specific "artefact not found" error.
// This allows callers to distinguish not-found errors from other failures.
type ArtefactNotFoundError struct {
	ArtefactID string
}

func (e *ArtefactNotFoundError) Error() string {
	return fmt.Sprintf("artefact with ID '%s' not found", e.ArtefactID)
}

// IsNotFound returns true if the error is an ArtefactNotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(*ArtefactNotFoundError)
	return ok
}
