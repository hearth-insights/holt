package spine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// SpineInfo contains the essential spine details for display.
type SpineInfo struct {
	ManifestID  string `json:"manifest_id"`
	ConfigHash  string `json:"config_hash"`
	GitCommit   string `json:"git_commit"`
	IsDetached  bool   `json:"is_detached"` // True if no spine found
}

// ResolveSpine attempts to find the SystemManifest anchored to the given artefact.
// It checks immediate parents for a StructuralTypeSystemManifest.
// Uses a cache to avoid repeated lookups for the same manifest ID.
func ResolveSpine(ctx context.Context, bbClient *blackboard.Client, artefact *blackboard.Artefact, cache map[string]*SpineInfo) (*SpineInfo, error) {
	// If the artefact itself is a SystemManifest, parse it directly
	if artefact.StructuralType == blackboard.StructuralTypeSystemManifest {
		return parseSpinePayload(artefact)
	}

	// Check source artefacts for a SystemManifest
	for _, sourceID := range artefact.SourceArtefacts {
		// Check cache first if we knew which sourceID was the manifest, but we don't know types of sources without fetching.
		// However, we can check if we've already resolved this sourceID as a manifest.
		if info, ok := cache[sourceID]; ok {
			return info, nil
		}

		// Fetch source artefact
		source, err := bbClient.GetArtefact(ctx, sourceID)
		if err != nil {
			// If source not found, skip it (might be a race or cleanup)
			continue
		}

		// Check if it is a SystemManifest
		if source.StructuralType == blackboard.StructuralTypeSystemManifest {
			info, err := parseSpinePayload(source)
			if err != nil {
				return nil, err
			}
			// Cache it
			cache[sourceID] = info
			return info, nil
		}
	}

	// If not found in immediate parents, return detached info
	return &SpineInfo{IsDetached: true}, nil
}

// parseSpinePayload extracts config hash and git commit from SystemManifest payload.
func parseSpinePayload(manifest *blackboard.Artefact) (*SpineInfo, error) {
	var payload struct {
		ConfigHash string `json:"config_hash"`
		GitCommit  string `json:"git_commit"`
	}

	if err := json.Unmarshal([]byte(manifest.Payload), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse system manifest payload: %w", err)
	}

	return &SpineInfo{
		ManifestID: manifest.ID,
		ConfigHash: payload.ConfigHash,
		GitCommit:  payload.GitCommit,
		IsDetached: false,
	}, nil
}
