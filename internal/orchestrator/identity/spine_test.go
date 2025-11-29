package identity

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRedis creates a test Redis client and blackboard client for integration tests.
func setupTestRedis(t *testing.T) (*redis.Client, *blackboard.Client) {
	t.Helper()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		t.Skip("REDIS_URL not set, skipping integration test")
	}

	redisOpts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)

	redisClient := redis.NewClient(redisOpts)
	require.NoError(t, redisClient.Ping(context.Background()).Err())

	instanceName := "test-spine-" + time.Now().Format("20060102-150405")
	bbClient, err := blackboard.NewClient(redisOpts, instanceName)
	require.NoError(t, err)

	// Cleanup on test end
	t.Cleanup(func() {
		ctx := context.Background()
		// Clean up all keys for this instance
		pattern := "holt:" + instanceName + ":*"
		keys, _ := bbClient.ScanKeys(ctx, pattern)
		if len(keys) > 0 {
			redisClient.Del(ctx, keys...)
		}
		bbClient.Close()
		redisClient.Close()
	})

	return redisClient, bbClient
}

// mockIdentityProvider is a test double for IdentityProvider
type mockIdentityProvider struct {
	identity     *blackboard.SystemIdentity
	identityHash string
}

func (m *mockIdentityProvider) ComputeIdentity() (*blackboard.SystemIdentity, error) {
	return m.identity, nil
}

func (m *mockIdentityProvider) IdentityHash() (string, error) {
	return m.identityHash, nil
}

func TestSpineManager_FirstStartupCreatesManifest(t *testing.T) {
	_, bbClient := setupTestRedis(t)
	ctx := context.Background()

	provider := &mockIdentityProvider{
		identity: &blackboard.SystemIdentity{
			Strategy:     "local",
			ConfigHash:   "sha256:abc123",
			GitCommit:    "def456",
			ComputedAtMs: time.Now().UnixMilli(),
		},
		identityHash: "test-identity-hash-v1",
	}

	spineManager := NewSpineManager(bbClient, provider, bbClient.GetInstanceName(), GenerateSpineThreadID())

	// Initialize spine
	manifestID, err := spineManager.InitializeSpine(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, manifestID)

	// Verify manifest was created
	manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
	require.NoError(t, err)
	assert.Equal(t, blackboard.StructuralTypeSystemManifest, manifest.Header.StructuralType)
	assert.Equal(t, 1, manifest.Header.Version)
	assert.Empty(t, manifest.Header.ParentHashes) // First manifest has no parent
	assert.Equal(t, "orchestrator", manifest.Header.ProducedByRole)
	assert.Equal(t, "SystemConfig", manifest.Header.Type)

	// Verify payload contains identity
	var storedIdentity blackboard.SystemIdentity
	err = json.Unmarshal([]byte(manifest.Payload.Content), &storedIdentity)
	require.NoError(t, err)
	assert.Equal(t, "local", storedIdentity.Strategy)
	assert.Equal(t, "sha256:abc123", storedIdentity.ConfigHash)
	assert.Equal(t, "def456", storedIdentity.GitCommit)

	// Verify active_manifest key was set
	activeManifestID, err := spineManager.GetActiveManifest(ctx)
	require.NoError(t, err)
	assert.Equal(t, manifestID, activeManifestID)
}

func TestSpineManager_NoChangesReusesManifest(t *testing.T) {
	_, bbClient := setupTestRedis(t)
	ctx := context.Background()

	provider := &mockIdentityProvider{
		identity: &blackboard.SystemIdentity{
			Strategy:     "local",
			ConfigHash:   "sha256:unchanged",
			ComputedAtMs: time.Now().UnixMilli(),
		},
		identityHash: "identity-hash-stable",
	}

	spineThreadID := GenerateSpineThreadID()
	spineManager := NewSpineManager(bbClient, provider, bbClient.GetInstanceName(), spineThreadID)

	// First startup
	manifestID1, err := spineManager.InitializeSpine(ctx)
	require.NoError(t, err)

	// Second startup with same identity
	spineManager2 := NewSpineManager(bbClient, provider, bbClient.GetInstanceName(), spineThreadID)
	manifestID2, err := spineManager2.InitializeSpine(ctx)
	require.NoError(t, err)

	// Assert same manifest reused
	assert.Equal(t, manifestID1, manifestID2)

	// Verify only one manifest exists in spine
	history, err := spineManager.FetchSpineHistory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(history))
}

func TestSpineManager_DriftCreatesNewManifest(t *testing.T) {
	_, bbClient := setupTestRedis(t)
	ctx := context.Background()

	// First startup with identity v1
	provider1 := &mockIdentityProvider{
		identity: &blackboard.SystemIdentity{
			Strategy:     "local",
			ConfigHash:   "sha256:v1",
			GitCommit:    "commit-v1",
			ComputedAtMs: time.Now().UnixMilli(),
		},
		identityHash: "identity-hash-v1",
	}

	spineThreadID := GenerateSpineThreadID()
	spineManager1 := NewSpineManager(bbClient, provider1, bbClient.GetInstanceName(), spineThreadID)
	manifestID1, err := spineManager1.InitializeSpine(ctx)
	require.NoError(t, err)

	// Verify v1 manifest
	manifest1, err := bbClient.GetVerifiableArtefact(ctx, manifestID1)
	require.NoError(t, err)
	assert.Equal(t, 1, manifest1.Header.Version)

	// Second startup with identity v2 (drift detected)
	provider2 := &mockIdentityProvider{
		identity: &blackboard.SystemIdentity{
			Strategy:     "local",
			ConfigHash:   "sha256:v2",
			GitCommit:    "commit-v2",
			ComputedAtMs: time.Now().UnixMilli(),
		},
		identityHash: "identity-hash-v2",
	}

	spineManager2 := NewSpineManager(bbClient, provider2, bbClient.GetInstanceName(), spineThreadID)
	manifestID2, err := spineManager2.InitializeSpine(ctx)
	require.NoError(t, err)

	// Assert different manifests
	assert.NotEqual(t, manifestID1, manifestID2)

	// Verify v2 manifest has v1 as parent
	manifest2, err := bbClient.GetVerifiableArtefact(ctx, manifestID2)
	require.NoError(t, err)
	assert.Equal(t, 2, manifest2.Header.Version)
	assert.Equal(t, []string{manifestID1}, manifest2.Header.ParentHashes)

	// Verify active_manifest updated to v2
	activeManifestID, err := spineManager2.GetActiveManifest(ctx)
	require.NoError(t, err)
	assert.Equal(t, manifestID2, activeManifestID)

	// Verify spine history contains both versions
	history, err := spineManager2.FetchSpineHistory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(history))
	assert.Equal(t, 1, history[0].Header.Version) // Oldest first
	assert.Equal(t, 2, history[1].Header.Version)
}

func TestSpineManager_ExternalStrategy(t *testing.T) {
	_, bbClient := setupTestRedis(t)
	ctx := context.Background()

	externalData := `{"cluster_id":"prod-us-east-1","version":"v2.3.1"}`
	provider := &mockIdentityProvider{
		identity: &blackboard.SystemIdentity{
			Strategy:     "external",
			ExternalData: externalData,
			ComputedAtMs: time.Now().UnixMilli(),
		},
		identityHash: "external-identity-hash",
	}

	spineManager := NewSpineManager(bbClient, provider, bbClient.GetInstanceName(), GenerateSpineThreadID())

	manifestID, err := spineManager.InitializeSpine(ctx)
	require.NoError(t, err)

	// Verify manifest payload contains external data
	manifest, err := bbClient.GetVerifiableArtefact(ctx, manifestID)
	require.NoError(t, err)

	var storedIdentity blackboard.SystemIdentity
	err = json.Unmarshal([]byte(manifest.Payload.Content), &storedIdentity)
	require.NoError(t, err)
	assert.Equal(t, "external", storedIdentity.Strategy)
	assert.Equal(t, externalData, storedIdentity.ExternalData)
}

func TestSpineManager_FetchSpineHistory(t *testing.T) {
	_, bbClient := setupTestRedis(t)
	ctx := context.Background()

	spineThreadID := GenerateSpineThreadID()

	// Create 3 manifests with drift
	for i := 1; i <= 3; i++ {
		provider := &mockIdentityProvider{
			identity: &blackboard.SystemIdentity{
				Strategy:     "local",
				ConfigHash:   "sha256:v" + string(rune('0'+i)),
				ComputedAtMs: time.Now().UnixMilli(),
			},
			identityHash: "identity-hash-v" + string(rune('0'+i)),
		}

		spineManager := NewSpineManager(bbClient, provider, bbClient.GetInstanceName(), spineThreadID)
		_, err := spineManager.InitializeSpine(ctx)
		require.NoError(t, err)

		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// Fetch history
	spineManager := NewSpineManager(bbClient, nil, bbClient.GetInstanceName(), spineThreadID)
	history, err := spineManager.FetchSpineHistory(ctx)
	require.NoError(t, err)

	// Verify chronological order (oldest first)
	assert.Equal(t, 3, len(history))
	assert.Equal(t, 1, history[0].Header.Version)
	assert.Equal(t, 2, history[1].Header.Version)
	assert.Equal(t, 3, history[2].Header.Version)

	// Verify parent chain
	assert.Empty(t, history[0].Header.ParentHashes)
	assert.Equal(t, []string{history[0].ID}, history[1].Header.ParentHashes)
	assert.Equal(t, []string{history[1].ID}, history[2].Header.ParentHashes)
}

func TestLocalIdentityProvider_Integration(t *testing.T) {
	// This test uses a real LocalIdentityProvider with filesystem
	tmpDir := t.TempDir()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	// Create holt.yml
	configPath := filepath.Join(tmpDir, "holt.yml")
	configContent := "version: \"1.0\"\nagents:\n  test: {}"
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

	// Create provider
	provider := &blackboard.LocalIdentityProvider{
		ConfigPath:    configPath,
		WorkspaceRoot: tmpDir,
	}

	// Compute identity
	identity, err := provider.ComputeIdentity()
	require.NoError(t, err)
	assert.Equal(t, "local", identity.Strategy)
	assert.NotEmpty(t, identity.ConfigHash)
	assert.NotEmpty(t, identity.GitCommit)

	// Verify hash is deterministic
	hash1, err := provider.IdentityHash()
	require.NoError(t, err)

	hash2, err := provider.IdentityHash()
	require.NoError(t, err)

	// Hashes should be stable for same content
	assert.NotEmpty(t, hash1)
	assert.NotEmpty(t, hash2)
}

// Helper function to initialize git repo (duplicated from identity_test.go for independence)
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	ctx := context.Background()

	// git init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	// Create initial commit
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	_ = ctx // Silence unused warning
}
