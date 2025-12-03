package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dyluth/holt/pkg/blackboard"
	canonicaljson "github.com/gibson042/canonicaljson-go"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockIdentityProvider implements blackboard.IdentityProvider for testing
type MockIdentityProvider struct {
	Identity *blackboard.SystemIdentity
	Hash     string
	Err      error
}

func (m *MockIdentityProvider) ComputeIdentity() (*blackboard.SystemIdentity, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Identity, nil
}

func (m *MockIdentityProvider) IdentityHash() (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return m.Hash, nil
}

func TestInitializeSpine(t *testing.T) {
	// Setup miniredis
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisOpts := &redis.Options{Addr: mr.Addr()}
	bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	ctx := context.Background()
	spineThreadID := "spine-thread-123"

	// Helper to compute hash matching SpineManager logic
	computeHash := func(identity *blackboard.SystemIdentity) string {
		stable := struct {
			Strategy   string `json:"strategy"`
			ConfigHash string `json:"config_hash"`
			GitCommit  string `json:"git_commit"`
		}{
			Strategy:   identity.Strategy,
			ConfigHash: identity.ConfigHash,
			GitCommit:  identity.GitCommit,
		}
		// We need to use the same canonicaljson package
		// But since we can't easily import the internal vendor one if it's vendored,
		// or if it's just a library.
		// Actually, identity.go uses "github.com/gibson042/canonicaljson-go"
		// We can use that too.
		// But for simplicity in test, we can just assume the manager works if we use the same library.
		// However, we can't access the private sha256Hash helper.
		// Let's just use a real identity provider logic or copy the hashing logic.
		// Since we are mocking the provider, we need to return what the manager EXPECTS.
		// The manager expects IdentityHash() to return X.
		// And it expects extractIdentityHash(manifest) to return X.
		// extractIdentityHash computes it from payload.
		// So we must ensure our mock IdentityHash() returns what extractIdentityHash computes.
		
		// Let's use a real LocalIdentityProvider to compute the hash for our test data?
		// No, LocalIdentityProvider requires files.
		
		// We'll implement the hashing logic here.
		bytes, _ := canonicaljson.Marshal(stable)
		hash := sha256.Sum256(bytes)
		return hex.EncodeToString(hash[:])
	}

	// Scenario 1: First startup (No manifest)
	t.Run("FirstStartup", func(t *testing.T) {
		identity := &blackboard.SystemIdentity{
			Strategy:   "local",
			ConfigHash: "hash-v1",
			GitCommit:  "commit-v1",
		}
		hash := computeHash(identity)
		
		mockProvider := &MockIdentityProvider{
			Identity: identity,
			Hash:     hash,
		}

		manager := NewSpineManager(bbClient, mockProvider, "test-instance", spineThreadID)

		manifestID, err := manager.InitializeSpine(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, manifestID)

		// Verify manifest created
		manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
		require.NoError(t, err)
		assert.Equal(t, 1, manifest.Header.Version)
		assert.Equal(t, spineThreadID, manifest.Header.LogicalThreadID)
		assert.Equal(t, "SystemConfig", manifest.Header.Type)

		// Verify active manifest set
		activeID, err := manager.GetActiveManifest(ctx)
		require.NoError(t, err)
		assert.Equal(t, manifestID, activeID)
	})

	// Scenario 2: No drift (Reuse manifest)
	t.Run("NoDrift", func(t *testing.T) {
		// Setup existing state (v1)
		identity := &blackboard.SystemIdentity{
			Strategy:   "local",
			ConfigHash: "hash-v1",
			GitCommit:  "commit-v1",
		}
		hash := computeHash(identity)

		mockProvider := &MockIdentityProvider{
			Identity: identity,
			Hash:     hash,
		}
		manager := NewSpineManager(bbClient, mockProvider, "test-instance", spineThreadID)
		
		// Run init again
		manifestID, err := manager.InitializeSpine(ctx)
		require.NoError(t, err)

		// Should be same ID as before (we can't easily check exact ID without storing it, 
		// but we can check version is still 1)
		manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
		require.NoError(t, err)
		assert.Equal(t, 1, manifest.Header.Version)
	})

	// Scenario 3: Drift detected (New manifest)
	t.Run("DriftDetected", func(t *testing.T) {
		// Change identity (v2)
		identity := &blackboard.SystemIdentity{
			Strategy:   "local",
			ConfigHash: "hash-v2",
			GitCommit:  "commit-v2",
		}
		hash := computeHash(identity)

		mockProvider := &MockIdentityProvider{
			Identity: identity,
			Hash:     hash,
		}
		manager := NewSpineManager(bbClient, mockProvider, "test-instance", spineThreadID)

		// Run init
		manifestID, err := manager.InitializeSpine(ctx)
		require.NoError(t, err)

		// Verify new manifest created
		manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
		require.NoError(t, err)
		assert.Equal(t, 2, manifest.Header.Version)
		assert.NotEmpty(t, manifest.Header.ParentHashes)
		
		// Verify active manifest updated
		activeID, err := manager.GetActiveManifest(ctx)
		require.NoError(t, err)
		assert.Equal(t, manifestID, activeID)
	})
}

func TestFetchSpineHistory(t *testing.T) {
	// Setup miniredis
	mr := miniredis.RunT(t)
	defer mr.Close()

	redisOpts := &redis.Options{Addr: mr.Addr()}
	bbClient, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer bbClient.Close()

	ctx := context.Background()
	spineThreadID := "spine-thread-history"
	
	mockProvider := &MockIdentityProvider{
		Identity: &blackboard.SystemIdentity{Strategy: "local"},
		Hash:     "hash",
	}
	manager := NewSpineManager(bbClient, mockProvider, "test-instance", spineThreadID)

	// Create a few manifests manually via internal helper or public API
	// Since createManifest is private, we use InitializeSpine with changing hashes
	
	// V1
	mockProvider.Hash = "hash-1-long-enough-for-logging-0000000000000000"
	_, err = manager.InitializeSpine(ctx)
	require.NoError(t, err)

	// V2
	mockProvider.Hash = "hash-2-long-enough-for-logging-0000000000000000"
	_, err = manager.InitializeSpine(ctx)
	require.NoError(t, err)

	// V3
	mockProvider.Hash = "hash-3-long-enough-for-logging-0000000000000000"
	_, err = manager.InitializeSpine(ctx)
	require.NoError(t, err)

	// Fetch history
	history, err := manager.FetchSpineHistory(ctx)
	require.NoError(t, err)
	
	assert.Len(t, history, 3)
	assert.Equal(t, 1, history[0].Header.Version)
	assert.Equal(t, 2, history[1].Header.Version)
	assert.Equal(t, 3, history[2].Header.Version)
}
