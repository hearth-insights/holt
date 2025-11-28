package blackboard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalIdentityProvider_ComputeIdentity(t *testing.T) {
	// Create temp directory with git repo
	tmpDir := t.TempDir()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	// Create holt.yml
	configPath := filepath.Join(tmpDir, "holt.yml")
	configContent := "version: \"1.0\"\nagents:\n  test: {}"
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

	// Create provider
	provider := &LocalIdentityProvider{
		ConfigPath:    configPath,
		WorkspaceRoot: tmpDir,
	}

	// Compute identity
	identity, err := provider.ComputeIdentity()
	require.NoError(t, err)

	// Assertions
	assert.Equal(t, "local", identity.Strategy)
	assert.NotEmpty(t, identity.ConfigHash)
	assert.True(t, strings.HasPrefix(identity.ConfigHash, "sha256:"))
	assert.NotEmpty(t, identity.GitCommit)
	assert.Greater(t, identity.ComputedAtMs, int64(0))
	assert.Empty(t, identity.ExternalData)
}

func TestLocalIdentityProvider_Deterministic(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	configPath := filepath.Join(tmpDir, "holt.yml")
	require.NoError(t, os.WriteFile(configPath, []byte("test: config"), 0644))

	provider := &LocalIdentityProvider{
		ConfigPath:    configPath,
		WorkspaceRoot: tmpDir,
	}

	// Compute identity multiple times and check the stable parts
	// Note: ComputedAtMs will vary, so we check ConfigHash and GitCommit directly
	identities := make([]*SystemIdentity, 10)
	for i := 0; i < 10; i++ {
		identity, err := provider.ComputeIdentity()
		require.NoError(t, err)
		identities[i] = identity
	}

	// Assert all identities have same config hash and git commit
	firstConfigHash := identities[0].ConfigHash
	firstGitCommit := identities[0].GitCommit
	for i := 1; i < 10; i++ {
		assert.Equal(t, firstConfigHash, identities[i].ConfigHash, "Config hash should be deterministic")
		assert.Equal(t, firstGitCommit, identities[i].GitCommit, "Git commit should be deterministic")
		assert.Equal(t, "local", identities[i].Strategy)
	}
}

func TestLocalIdentityProvider_FailsOnInvalidGitRepo(t *testing.T) {
	// Create temp directory WITHOUT git repo
	tmpDir := t.TempDir()

	configPath := filepath.Join(tmpDir, "holt.yml")
	require.NoError(t, os.WriteFile(configPath, []byte("test: config"), 0644))

	provider := &LocalIdentityProvider{
		ConfigPath:    configPath,
		WorkspaceRoot: tmpDir,
	}

	// Should fail because no git repo
	_, err := provider.ComputeIdentity()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get git commit")
}

func TestLocalIdentityProvider_FailsOnMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	provider := &LocalIdentityProvider{
		ConfigPath:    filepath.Join(tmpDir, "nonexistent.yml"),
		WorkspaceRoot: tmpDir,
	}

	_, err := provider.ComputeIdentity()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLocalIdentityProvider_DetectsConfigChange(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	configPath := filepath.Join(tmpDir, "holt.yml")

	provider := &LocalIdentityProvider{
		ConfigPath:    configPath,
		WorkspaceRoot: tmpDir,
	}

	// Write v1 config
	require.NoError(t, os.WriteFile(configPath, []byte("version: 1"), 0644))
	hash1, err := provider.IdentityHash()
	require.NoError(t, err)

	// Write v2 config (different content)
	require.NoError(t, os.WriteFile(configPath, []byte("version: 2"), 0644))
	hash2, err := provider.IdentityHash()
	require.NoError(t, err)

	// Hashes should be different (drift detected)
	assert.NotEqual(t, hash1, hash2, "Config change should produce different hash")
}

func TestExternalIdentityProvider_ComputeIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")

	// Write valid JSON manifest
	manifestData := map[string]interface{}{
		"cluster_id": "prod-us-east-1",
		"version":    "v2.3.1",
	}
	manifestBytes, err := json.Marshal(manifestData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0644))

	provider := &ExternalIdentityProvider{
		ManifestPath: manifestPath,
	}

	identity, err := provider.ComputeIdentity()
	require.NoError(t, err)

	assert.Equal(t, "external", identity.Strategy)
	assert.NotEmpty(t, identity.ExternalData)
	assert.Greater(t, identity.ComputedAtMs, int64(0))
	assert.Empty(t, identity.ConfigHash)
	assert.Empty(t, identity.GitCommit)

	// Verify external data is valid JSON
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(identity.ExternalData), &parsed))
	assert.Equal(t, "prod-us-east-1", parsed["cluster_id"])
}

func TestExternalIdentityProvider_ValidatesJSON(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "invalid.json")

	// Write invalid JSON
	require.NoError(t, os.WriteFile(manifestPath, []byte("invalid json {"), 0644))

	provider := &ExternalIdentityProvider{
		ManifestPath: manifestPath,
	}

	_, err := provider.ComputeIdentity()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestExternalIdentityProvider_FailsOnMissingFile(t *testing.T) {
	provider := &ExternalIdentityProvider{
		ManifestPath: "/nonexistent/path/manifest.json",
	}

	_, err := provider.ComputeIdentity()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read external manifest")
}

func TestExternalIdentityProvider_Deterministic(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")

	manifestData := map[string]string{"test": "data"}
	manifestBytes, _ := json.Marshal(manifestData)
	require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0644))

	provider := &ExternalIdentityProvider{
		ManifestPath: manifestPath,
	}

	// Compute hash 10 times
	hashes := make(map[string]int)
	for i := 0; i < 10; i++ {
		hash, err := provider.IdentityHash()
		require.NoError(t, err)
		hashes[hash]++
	}

	// Assert deterministic
	assert.Equal(t, 1, len(hashes))
}

func TestNewIdentityProvider_DefaultsToLocal(t *testing.T) {
	// Clear env vars
	os.Unsetenv("HOLT_IDENTITY_SOURCE")
	os.Unsetenv("HOLT_MANIFEST_PATH")

	provider, err := NewIdentityProvider("/tmp/holt.yml", "/tmp")
	require.NoError(t, err)

	_, ok := provider.(*LocalIdentityProvider)
	assert.True(t, ok, "Should default to LocalIdentityProvider")
}

func TestNewIdentityProvider_ExternalRequiresManifestPath(t *testing.T) {
	os.Setenv("HOLT_IDENTITY_SOURCE", "external")
	os.Unsetenv("HOLT_MANIFEST_PATH")
	defer os.Unsetenv("HOLT_IDENTITY_SOURCE")

	_, err := NewIdentityProvider("/tmp/holt.yml", "/tmp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires HOLT_MANIFEST_PATH")
}

func TestNewIdentityProvider_ExternalSuccess(t *testing.T) {
	os.Setenv("HOLT_IDENTITY_SOURCE", "external")
	os.Setenv("HOLT_MANIFEST_PATH", "/tmp/manifest.json")
	defer func() {
		os.Unsetenv("HOLT_IDENTITY_SOURCE")
		os.Unsetenv("HOLT_MANIFEST_PATH")
	}()

	provider, err := NewIdentityProvider("/tmp/holt.yml", "/tmp")
	require.NoError(t, err)

	external, ok := provider.(*ExternalIdentityProvider)
	assert.True(t, ok)
	assert.Equal(t, "/tmp/manifest.json", external.ManifestPath)
}

func TestNewIdentityProvider_UnknownSource(t *testing.T) {
	os.Setenv("HOLT_IDENTITY_SOURCE", "unknown")
	defer os.Unsetenv("HOLT_IDENTITY_SOURCE")

	_, err := NewIdentityProvider("/tmp/holt.yml", "/tmp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown HOLT_IDENTITY_SOURCE")
}

// Helper: Initialize a git repository in the given directory
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// git init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	// Configure git user (required for commits)
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
}
