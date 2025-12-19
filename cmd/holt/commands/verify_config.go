package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/git"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	verifyConfigInstanceName string
	verifyConfigManifestID   string
)

var verifyConfigCmd = &cobra.Command{
	Use:   "verify-config",
	Short: "Verify stored configuration state",
	Long: `Verify that the current system configuration matches a stored SystemManifest.

This command compares the current holt.yml hash and git commit against what is
recorded in the specified SystemManifest artefact. This allows auditors to detect
configuration drift or verify that an artefact was produced by the current config.

Examples:
  # Verify active manifest
  holt verify-config --manifest $(holt spine | grep ACTIVE | awk '{print $4}')

  # Verify historical manifest
  holt verify-config --manifest a3f2b9c4...`,
	RunE: runVerifyConfig,
}

func init() {
	verifyConfigCmd.Flags().StringVarP(&verifyConfigInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	verifyConfigCmd.Flags().StringVarP(&verifyConfigManifestID, "manifest", "m", "", "Manifest ID to verify (required)")
	verifyConfigCmd.MarkFlagRequired("manifest")
	rootCmd.AddCommand(verifyConfigCmd)
}

func runVerifyConfig(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := verifyConfigInstanceName
	if targetInstanceName == "" {
		targetInstanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			return err
		}
	}

	// Get Redis port
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return err
	}

	// Connect to blackboard
	redisURL := instance.GetRedisURL(redisPort)
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	bbClient, err := blackboard.NewClient(redisOpts, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to create blackboard client: %w", err)
	}
	defer bbClient.Close()

	// Fetch specified manifest
	// Support short IDs via M3.10 logic (if we had access to resolver package, but we'll just use ScanArtefacts directly for simplicity here if needed, or assume full ID for now as per design doc example)
	// The design doc implies full ID or at least something fetchable.
	// Let's use ResolveArtefactID logic if we can, or just FetchArtefact.
	// Given dependencies, let's stick to FetchArtefact. If short ID support is needed, we'd need to import resolver.
	// But wait, ScanArtefacts is available on client. Let's do simple short ID resolution here.

	manifestID := verifyConfigManifestID
	if len(manifestID) < 64 {
		matches, err := bbClient.ScanArtefacts(ctx, manifestID)
		if err != nil {
			return fmt.Errorf("failed to search for manifest: %w", err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("manifest %s not found", manifestID)
		}
		if len(matches) > 1 {
			return fmt.Errorf("ambiguous manifest ID %s matches %d artefacts", manifestID, len(matches))
		}
		manifestID = matches[0]
	}

	manifest, err := bbClient.GetArtefact(ctx, manifestID)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest %s: %w", manifestID, err)
	}

	// Verify structural type
	if manifest.Header.StructuralType != blackboard.StructuralTypeSystemManifest {
		return fmt.Errorf("artefact is not a SystemManifest (type: %s)", manifest.Header.StructuralType)
	}

	// Parse stored identity
	var storedIdentity blackboard.SystemIdentity
	if err := json.Unmarshal([]byte(manifest.Payload.Content), &storedIdentity); err != nil {
		return fmt.Errorf("failed to parse manifest payload: %w", err)
	}

	printer.Info("Verifying SystemManifest %s...\n\n", manifestID[:16]+"...")

	// Strategy-specific verification
	if storedIdentity.Strategy == "local" {
		// Get workspace root
		checker := git.NewChecker()
		workspaceRoot, err := checker.GetGitRoot()
		if err != nil {
			return fmt.Errorf("failed to find git root: %w", err)
		}
		configPath := filepath.Join(workspaceRoot, "holt.yml")

		// Recompute current config hash
		configBytes, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("failed to read current config: %w", err)
		}

		currentConfigHashBytes := sha256.Sum256(configBytes)
		currentConfigHash := "sha256:" + hex.EncodeToString(currentConfigHashBytes[:])

		// Compare config hashes
		printer.Info("Config Hash Verification:\n")
		printer.Info("  Stored:  %s\n", storedIdentity.ConfigHash)
		printer.Info("  Current: %s\n", currentConfigHash)

		if storedIdentity.ConfigHash == currentConfigHash {
			printer.Success("  Status:  ✓ MATCH\n")
		} else {
			printer.Printf("  Status:  ✗ MISMATCH (config has changed since manifest creation)\n")
		}
		printer.Info("\n")

		// Verify git commit
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = workspaceRoot
		output, err := cmd.Output()

		printer.Info("Git Commit Verification:\n")
		printer.Info("  Stored:  %s\n", storedIdentity.GitCommit)

		if err != nil {
			printer.Warning("  Current: (git command failed: %v)\n", err)
		} else {
			currentCommit := strings.TrimSpace(string(output))
			printer.Info("  Current: %s\n", currentCommit)
			if storedIdentity.GitCommit == currentCommit {
				printer.Success("  Status:  ✓ MATCH\n")
			} else {
				printer.Printf("  Status:  ✗ MISMATCH (git HEAD has moved)\n")
			}
		}

	} else if storedIdentity.Strategy == "external" {
		printer.Info("Strategy: External (opaque)\n")
		printer.Info("  Stored identity is opaque JSON - manual verification required\n")
		printer.Info("  Payload: %s\n", storedIdentity.ExternalData)
	}

	printer.Info("\n")
	printer.Info("Manifest Created: %s\n", formatTimestamp(manifest.Header.CreatedAtMs))
	printer.Info("Manifest Version: %d\n", manifest.Header.Version)

	return nil
}
