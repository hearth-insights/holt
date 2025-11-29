package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	dockerpkg "github.com/dyluth/holt/internal/docker"
	"github.com/dyluth/holt/internal/instance"
	"github.com/dyluth/holt/internal/printer"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	spineInstanceName string
)

var spineCmd = &cobra.Command{
	Use:   "spine",
	Short: "Display System Spine history",
	Long: `Display the history of SystemManifest artefacts for an instance.

The System Spine tracks configuration changes over time. Each entry represents
a unique system state (holt.yml + git commit + agent versions) that was active.
Root artefacts are anchored to the active manifest at creation time.

Examples:
  # Show spine history for auto-inferred instance
  holt spine

  # Show spine history for specific instance
  holt spine --name prod`,
	RunE: runSpine,
}

func init() {
	spineCmd.Flags().StringVarP(&spineInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	rootCmd.AddCommand(spineCmd)
}

func runSpine(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := spineInstanceName
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

	// Fetch active manifest ID
	activeManifestKey := fmt.Sprintf("holt:%s:active_manifest", targetInstanceName)
	activeManifestID, err := bbClient.GetRedisClient().Get(ctx, activeManifestKey).Result()
	if err == redis.Nil {
		printer.Info("No active manifest found (orchestrator not initialized or fresh instance)\n")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to fetch active manifest: %w", err)
	}

	// Fetch active manifest artefact to start traversal
	activeManifest, err := bbClient.GetVerifiableArtefact(ctx, activeManifestID)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest artefact %s: %w", activeManifestID, err)
	}

	// Traverse spine backwards via ParentHashes
	// Note: SystemManifests form a linear chain via ParentHashes
	var manifests []*blackboard.VerifiableArtefact
	manifests = append(manifests, activeManifest)
	current := activeManifest

	for len(current.Header.ParentHashes) > 0 {
		parentID := current.Header.ParentHashes[0]
		parent, err := bbClient.GetVerifiableArtefact(ctx, parentID)
		if err != nil {
			printer.Warning("Failed to fetch parent manifest %s: %v\n", parentID, err)
			break
		}
		manifests = append(manifests, parent)
		current = parent
	}

	// Display in chronological order (oldest first)
	printer.Info("\nSystem Spine History\n")
	printer.Info("====================\n\n")

	for i := len(manifests) - 1; i >= 0; i-- {
		m := manifests[i]
		isActive := m.ID == activeManifestID

		// Parse identity payload
		var identity map[string]interface{}
		if err := json.Unmarshal([]byte(m.Payload.Content), &identity); err != nil {
			printer.Warning("Failed to parse payload for manifest %s: %v\n", m.ID, err)
			continue
		}

		printer.Info("Version %d%s\n", m.Header.Version, activeMarker(isActive))
		printer.Info("  Manifest ID: %s\n", m.ID[:16]+"...")
		printer.Info("  Created:     %s\n", formatTimestamp(m.Header.CreatedAtMs))

		if strategy, ok := identity["strategy"].(string); ok && strategy == "local" {
			if hash, ok := identity["config_hash"].(string); ok {
				printer.Info("  Config Hash: %s\n", truncateHash(hash))
			}
			if commit, ok := identity["git_commit"].(string); ok {
				printer.Info("  Git Commit:  %s\n", commit)
			}
		} else if strategy == "external" {
			printer.Info("  Strategy:    External (opaque)\n")
		}

		if i > 0 {
			printer.Info("\n")
		}
	}

	return nil
}

func activeMarker(isActive bool) string {
	if isActive {
		return " (ACTIVE)"
	}
	return ""
}

func truncateHash(hash string) string {
	if len(hash) > 16 {
		return hash[:16] + "..."
	}
	return hash
}

func formatTimestamp(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
