// Package identity manages the System Spine - M4.7 System Integrity & Configuration Ledger.
// The SpineManager creates and manages SystemManifest artefacts that anchor workflows to
// specific system configuration states (holt.yml + git commit + optional external identity).
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	canonicaljson "github.com/gibson042/canonicaljson-go"
	"github.com/google/uuid"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
)

// SpineManager manages the System Spine lifecycle for a Holt instance.
// It performs configuration drift detection and creates SystemManifest artefacts.
type SpineManager struct {
	client           *blackboard.Client
	identityProvider blackboard.IdentityProvider
	instanceName     string
	spineThreadID    string // Shared LogicalThreadID for this instance's spine
}

// NewSpineManager creates a SpineManager for the given instance.
// The spineThreadID is the LogicalThreadID for all SystemManifest artefacts in this instance.
func NewSpineManager(
	client *blackboard.Client,
	identityProvider blackboard.IdentityProvider,
	instanceName string,
	spineThreadID string,
) *SpineManager {
	return &SpineManager{
		client:           client,
		identityProvider: identityProvider,
		instanceName:     instanceName,
		spineThreadID:    spineThreadID,
	}
}

// InitializeSpine performs startup drift detection and manifest management.
// Returns the active manifest ID (hash) that should be used to anchor new workflows.
//
// Logic:
// 1. Compute current system identity
// 2. Query for latest SystemManifest in spine
// 3. If no manifest exists, create initial v1 manifest
// 4. If manifest exists, compare identity hashes
// 5. If drift detected, create new manifest (version++, parent=previous)
// 6. Update active_manifest key
// 7. Return active manifest ID
func (sm *SpineManager) InitializeSpine(ctx context.Context) (string, error) {
	// Step 1: Compute current system identity
	currentIdentity, err := sm.identityProvider.ComputeIdentity()
	if err != nil {
		return "", fmt.Errorf("failed to compute system identity: %w", err)
	}

	currentIdentityHash, err := sm.identityProvider.IdentityHash()
	if err != nil {
		return "", fmt.Errorf("failed to hash identity: %w", err)
	}

	log.Printf("[SpineManager] Computed system identity hash: %s...", currentIdentityHash[:16])

	// Step 2: Query for latest SystemManifest in this instance's spine
	latestManifest, err := sm.fetchLatestManifest(ctx)
	if err == ErrNoManifest {
		// First startup - create initial manifest
		log.Printf("[SpineManager] No existing SystemManifest found - creating initial manifest")
		return sm.createManifest(ctx, currentIdentity, currentIdentityHash, nil, 1)
	}
	if err != nil {
		return "", fmt.Errorf("failed to query latest manifest: %w", err)
	}

	// Step 3: Drift detection - compare identity hashes
	latestIdentityHash, err := sm.extractIdentityHash(latestManifest)
	if err != nil {
		return "", fmt.Errorf("failed to extract identity from latest manifest: %w", err)
	}

	if currentIdentityHash == latestIdentityHash {
		// No drift - reuse existing manifest
		log.Printf("[SpineManager] System identity unchanged - reusing manifest %s... (version %d)",
			latestManifest.ID[:16], latestManifest.Header.Version)

		// Update active_manifest pointer (idempotent)
		if err := sm.setActiveManifest(ctx, latestManifest.ID); err != nil {
			return "", err
		}

		return latestManifest.ID, nil
	}

	// Step 4: Drift detected - create new manifest
	log.Printf("[SpineManager] Configuration drift detected - creating new SystemManifest")
	log.Printf("[SpineManager]   Old identity hash: %s...", latestIdentityHash[:16])
	log.Printf("[SpineManager]   New identity hash: %s...", currentIdentityHash[:16])
	log.Printf("[SpineManager]   Old version: %d", latestManifest.Header.Version)

	return sm.createManifest(
		ctx,
		currentIdentity,
		currentIdentityHash,
		[]string{latestManifest.ID}, // Parent = previous manifest
		latestManifest.Header.Version+1,
	)
}

// createManifest creates a new SystemManifest artefact and updates spine tracking.
func (sm *SpineManager) createManifest(
	ctx context.Context,
	identity *blackboard.SystemIdentity,
	identityHash string,
	parentHashes []string,
	version int,
) (string, error) {
	// Marshal identity to JSON payload
	payloadBytes, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("failed to marshal identity: %w", err)
	}

	// Create Artefact
	manifest := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    parentHashes,
			LogicalThreadID: sm.spineThreadID, // Shared thread for this instance
			Version:         version,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "orchestrator",
			StructuralType:  blackboard.StructuralTypeSystemManifest,
			Type:            "SystemConfig",
			ClaimID:         "", // No claim for system artefacts
		},
		Payload: blackboard.ArtefactPayload{
			Content: string(payloadBytes),
		},
	}

	// Compute hash
	manifestID, err := blackboard.ComputeArtefactHash(manifest)
	if err != nil {
		return "", fmt.Errorf("failed to compute manifest hash: %w", err)
	}
	manifest.ID = manifestID

	// Write to blackboard
	if err := sm.client.CreateArtefact(ctx, manifest); err != nil {
		return "", fmt.Errorf("failed to write manifest: %w", err)
	}

	// Update spine ZSET
	if err := sm.addToSpine(ctx, manifestID, version); err != nil {
		return "", fmt.Errorf("failed to update spine: %w", err)
	}

	// Update active_manifest pointer
	if err := sm.setActiveManifest(ctx, manifestID); err != nil {
		return "", err
	}

	log.Printf("[SpineManager] Created SystemManifest %s... (version %d, identity hash %s...)",
		manifestID[:16], version, identityHash[:16])

	return manifestID, nil
}

// fetchLatestManifest queries the spine ZSET for the highest version manifest.
// Returns ErrNoManifest if spine doesn't exist yet.
func (sm *SpineManager) fetchLatestManifest(ctx context.Context) (*blackboard.Artefact, error) {
	// Query spine ZSET for highest version
	key := fmt.Sprintf("holt:%s:system_spine:%s", sm.instanceName, sm.spineThreadID)

	// ZREVRANGE returns members in descending order by score (version)
	results, err := sm.client.GetRedisClient().ZRevRangeWithScores(ctx, key, 0, 0).Result()
	if err == redis.Nil || len(results) == 0 {
		return nil, ErrNoManifest
	}
	if err != nil {
		return nil, err
	}

	manifestID := results[0].Member.(string)
	return sm.client.GetArtefact(ctx, manifestID)
}

// addToSpine adds a manifest to the spine ZSET (score = version).
func (sm *SpineManager) addToSpine(ctx context.Context, manifestID string, version int) error {
	key := fmt.Sprintf("holt:%s:system_spine:%s", sm.instanceName, sm.spineThreadID)

	// Add to ZSET with version as score
	return sm.client.GetRedisClient().ZAdd(ctx, key, redis.Z{
		Score:  float64(version),
		Member: manifestID,
	}).Err()
}

// setActiveManifest updates the active_manifest key (no TTL - permanent).
func (sm *SpineManager) setActiveManifest(ctx context.Context, manifestID string) error {
	key := fmt.Sprintf("holt:%s:active_manifest", sm.instanceName)
	return sm.client.GetRedisClient().Set(ctx, key, manifestID, 0).Err() // No TTL
}

// GetActiveManifest fetches the current active manifest ID.
// Returns ErrNoManifest if not set.
func (sm *SpineManager) GetActiveManifest(ctx context.Context) (string, error) {
	key := fmt.Sprintf("holt:%s:active_manifest", sm.instanceName)
	manifestID, err := sm.client.GetRedisClient().Get(ctx, key).Result()
	if err == redis.Nil {
		return "", ErrNoManifest
	}
	if err != nil {
		return "", err
	}
	return manifestID, nil
}

// extractIdentityHash extracts the identity hash from a SystemManifest payload.
// For comparison during drift detection.
func (sm *SpineManager) extractIdentityHash(manifest *blackboard.Artefact) (string, error) {
	// Parse the payload as SystemIdentity
	var identity blackboard.SystemIdentity
	if err := json.Unmarshal([]byte(manifest.Payload.Content), &identity); err != nil {
		return "", fmt.Errorf("failed to parse manifest payload: %w", err)
	}

	// Reconstruct the identity provider (temporary) to compute hash
	// This is a bit awkward, but matches the design - we need to re-hash the stored identity
	switch identity.Strategy {
	case "local":
		// For local strategy, we need to compute hash from the stored identity
		// We can't use LocalIdentityProvider since we don't have filesystem access
		// Instead, hash the canonical JSON of the stored identity (matching IdentityHash logic)
		// IMPORTANT: Must exclude ComputedAtMs to match LocalIdentityProvider.IdentityHash behavior
		stableIdentity := struct {
			Strategy   string `json:"strategy"`
			ConfigHash string `json:"config_hash"`
			GitCommit  string `json:"git_commit"`
		}{
			Strategy:   identity.Strategy,
			ConfigHash: identity.ConfigHash,
			GitCommit:  identity.GitCommit,
		}
		canonicalBytes, err := canonicalJSON(stableIdentity)
		if err != nil {
			return "", fmt.Errorf("failed to canonicalize stored identity: %w", err)
		}
		hash := sha256Hash(canonicalBytes)
		return hash, nil

	case "external":
		// For external strategy, hash the opaque external data
		hash := sha256Hash([]byte(identity.ExternalData))
		return hash, nil

	default:
		return "", fmt.Errorf("unknown identity strategy: %q", identity.Strategy)
	}
}

// FetchSpineHistory returns all SystemManifest artefacts in chronological order (oldest first).
func (sm *SpineManager) FetchSpineHistory(ctx context.Context) ([]*blackboard.Artefact, error) {
	key := fmt.Sprintf("holt:%s:system_spine:%s", sm.instanceName, sm.spineThreadID)

	// Get all manifests (ordered by version ascending)
	results, err := sm.client.GetRedisClient().ZRangeWithScores(ctx, key, 0, -1).Result()
	if err == redis.Nil || len(results) == 0 {
		return []*blackboard.Artefact{}, nil
	}
	if err != nil {
		return nil, err
	}

	manifests := make([]*blackboard.Artefact, 0, len(results))
	for _, result := range results {
		manifestID := result.Member.(string)
		manifest, err := sm.client.GetArtefact(ctx, manifestID)
		if err != nil {
			// Log warning but continue - spine may have gaps
			log.Printf("[SpineManager] Warning: failed to fetch manifest %s: %v", manifestID, err)
			continue
		}
		manifests = append(manifests, manifest)
	}

	return manifests, nil
}

// GenerateSpineThreadID generates a new UUID for the spine thread.
// Should be called once during instance creation and persisted.
func GenerateSpineThreadID() string {
	return uuid.New().String()
}

// ErrNoManifest is returned when no SystemManifest exists yet.
var ErrNoManifest = fmt.Errorf("no SystemManifest found")

// Helper functions for identity hashing (match blackboard/identity.go logic)

func canonicalJSON(v interface{}) ([]byte, error) {
	return canonicaljson.Marshal(v)
}

func sha256Hash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
