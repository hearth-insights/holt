// Package blackboard provides identity computation for System Integrity (M4.7).
// Identity providers compute cryptographic fingerprints of the system configuration
// state (holt.yml + git commit + optional external data) to detect configuration drift.
package blackboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	canonicaljson "github.com/gibson042/canonicaljson-go"
)

// SystemIdentity represents the cryptographic identity of a Holt instance.
// This identity is used to detect configuration drift across orchestrator restarts.
type SystemIdentity struct {
	Strategy     string `json:"strategy"`               // "local" or "external"
	ConfigHash   string `json:"config_hash,omitempty"`  // SHA-256 of holt.yml (local only)
	GitCommit    string `json:"git_commit,omitempty"`   // Git HEAD commit (local only)
	ComputedAtMs int64  `json:"computed_at_ms"`         // Timestamp
	ExternalData string `json:"external_data,omitempty"` // Raw JSON from external file (external only)
}

// IdentityProvider is an interface for computing system identity.
// Implementations: LocalIdentityProvider (default) and ExternalIdentityProvider (enterprise).
type IdentityProvider interface {
	ComputeIdentity() (*SystemIdentity, error)
	IdentityHash() (string, error) // Returns SHA-256 hash of the identity
}

// LocalIdentityProvider computes identity from holt.yml and git.
// This is the default strategy for standard Holt deployments.
type LocalIdentityProvider struct {
	ConfigPath    string // Path to holt.yml
	WorkspaceRoot string // Git repository root
}

// ComputeIdentity computes the system identity using local filesystem state.
// Returns error if config file is unreadable or git repository is invalid.
func (p *LocalIdentityProvider) ComputeIdentity() (*SystemIdentity, error) {
	// Step 1: Read and hash holt.yml
	configBytes, err := os.ReadFile(p.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	configHashBytes := sha256.Sum256(configBytes)
	configHash := "sha256:" + hex.EncodeToString(configHashBytes[:])

	// Step 2: Get git HEAD commit (MUST succeed - fail fast if not a git repo)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = p.WorkspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get git commit (ensure workspace is a valid git repository): %w", err)
	}
	gitCommit := strings.TrimSpace(string(output))

	return &SystemIdentity{
		Strategy:     "local",
		ConfigHash:   configHash,
		GitCommit:    gitCommit,
		ComputedAtMs: time.Now().UnixMilli(),
	}, nil
}

// IdentityHash computes the SHA-256 hash of the canonical JSON representation.
// This hash is used for drift detection.
// IMPORTANT: Only stable fields (Strategy, ConfigHash, GitCommit) are hashed.
// ComputedAtMs is excluded to ensure the same config produces the same hash across restarts.
func (p *LocalIdentityProvider) IdentityHash() (string, error) {
	identity, err := p.ComputeIdentity()
	if err != nil {
		return "", err
	}

	// Create a stable representation excluding the timestamp
	stableIdentity := struct {
		Strategy   string `json:"strategy"`
		ConfigHash string `json:"config_hash"`
		GitCommit  string `json:"git_commit"`
	}{
		Strategy:   identity.Strategy,
		ConfigHash: identity.ConfigHash,
		GitCommit:  identity.GitCommit,
	}

	// Hash the canonical JSON representation using RFC 8785
	canonicalBytes, err := canonicalJSON(stableIdentity)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize identity: %w", err)
	}

	hashBytes := sha256.Sum256(canonicalBytes)
	return hex.EncodeToString(hashBytes[:]), nil
}

// ExternalIdentityProvider reads identity from external file.
// This strategy is for enterprise deployments with custom identity sources.
type ExternalIdentityProvider struct {
	ManifestPath string // Path to external manifest file
}

// ComputeIdentity reads identity from an external JSON file.
// Returns error if file is unreadable or not valid JSON.
func (p *ExternalIdentityProvider) ComputeIdentity() (*SystemIdentity, error) {
	// Read file
	manifestBytes, err := os.ReadFile(p.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read external manifest: %w", err)
	}

	// Validate it's valid JSON (but don't enforce schema - opaque to Holt)
	var rawData interface{}
	if err := json.Unmarshal(manifestBytes, &rawData); err != nil {
		return nil, fmt.Errorf("external manifest is not valid JSON: %w", err)
	}

	return &SystemIdentity{
		Strategy:     "external",
		ExternalData: string(manifestBytes),
		ComputedAtMs: time.Now().UnixMilli(),
	}, nil
}

// IdentityHash computes the SHA-256 hash of the raw external data.
// For external strategy, we hash the opaque JSON payload directly.
func (p *ExternalIdentityProvider) IdentityHash() (string, error) {
	identity, err := p.ComputeIdentity()
	if err != nil {
		return "", err
	}

	// Hash the raw external data (opaque to us)
	hashBytes := sha256.Sum256([]byte(identity.ExternalData))
	return hex.EncodeToString(hashBytes[:]), nil
}

// NewIdentityProvider creates the appropriate provider based on environment variables.
// Reads HOLT_IDENTITY_SOURCE (default: "local") and HOLT_MANIFEST_PATH (for external).
func NewIdentityProvider(configPath, workspaceRoot string) (IdentityProvider, error) {
	source := os.Getenv("HOLT_IDENTITY_SOURCE")
	if source == "" {
		source = "local" // Default
	}

	switch source {
	case "local":
		return &LocalIdentityProvider{
			ConfigPath:    configPath,
			WorkspaceRoot: workspaceRoot,
		}, nil

	case "external":
		manifestPath := os.Getenv("HOLT_MANIFEST_PATH")
		if manifestPath == "" {
			return nil, fmt.Errorf("HOLT_IDENTITY_SOURCE=external requires HOLT_MANIFEST_PATH env var")
		}
		return &ExternalIdentityProvider{
			ManifestPath: manifestPath,
		}, nil

	default:
		return nil, fmt.Errorf("unknown HOLT_IDENTITY_SOURCE: %q (must be 'local' or 'external')", source)
	}
}

// canonicalJSON uses RFC 8785 canonicalization for consistent hashing.
// This matches the approach used in canonical.go for VerifiableArtefacts.
func canonicalJSON(v interface{}) ([]byte, error) {
	// Use canonicaljson.Marshal directly (same as canonical.go)
	return canonicaljson.Marshal(v)
}
