package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/git"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/internal/watch"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	forageInstanceName string
	forageWatch        bool
	forageGoal         string
)

var forageCmd = &cobra.Command{
	Use:   "forage",
	Short: "Create a new workflow by submitting a goal",
	Long: `Create a new workflow by submitting a goal description.

The forage command creates a GoalDefined artefact on the blackboard,
which the orchestrator will process to begin coordinating agents.

Prerequisites:
  • Git repository with clean workspace (no uncommitted changes)
  • Running Holt instance (start with 'holt up')

Examples:
  # Create workflow on inferred instance
  holt forage --goal "Build a REST API for user management"

  # Target specific instance
  holt forage --name prod --goal "Refactor authentication module"

  # Validate orchestrator response (Phase 1)
  holt forage --watch --goal "Add logging to all endpoints"`,
	RunE: runForage,
}

func init() {
	forageCmd.Flags().StringVarP(&forageInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	forageCmd.Flags().BoolVarP(&forageWatch, "watch", "w", false, "Wait for orchestrator to create claim (Phase 1 validation)")
	forageCmd.Flags().StringVarP(&forageGoal, "goal", "g", "", "Goal description (required)")
	forageCmd.MarkFlagRequired("goal")
	rootCmd.AddCommand(forageCmd)
}

func runForage(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Phase 1: Validate goal input
	if forageGoal == "" {
		return printer.Error(
			"required flag --goal not provided",
			"Usage:\n  holt forage --goal \"description of what you want to build\"\n\nExample:\n  holt forage --goal \"Create a REST API for user management\"",
			[]string{"For immediate validation:\n  holt forage --watch --goal \"your goal\""},
		)
	}

	// Phase 2: Git workspace validation
	checker := git.NewChecker()

	isRepo, err := checker.IsGitRepository()
	if err != nil {
		return err
	}
	if !isRepo {
		return printer.Error(
			"not a Git repository",
			"Holt requires a Git repository to manage workflows.",
			[]string{"Initialize Git first:\n  git init\n  holt init\n  holt up"},
		)
	}

	isClean, err := checker.IsWorkspaceClean()
	if err != nil {
		return fmt.Errorf("failed to check Git workspace: %w", err)
	}
	if !isClean {
		dirtyFiles, err := checker.GetDirtyFiles()
		if err != nil {
			return fmt.Errorf("failed to get dirty files: %w", err)
		}

		return printer.Error(
			"Git workspace is not clean",
			dirtyFiles,
			[]string{
				"Commit changes:\n  git add .\n  git commit -m \"your message\"",
				"Stash temporarily:\n  git stash",
			},
		)
	}

	// Phase 3: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := forageInstanceName
	if targetInstanceName == "" {
		targetInstanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			if err.Error() == "no Holt instances found for this workspace" {
				return printer.ErrorWithContext(
					"no Holt instances found",
					"No running instances found for workspace:",
					map[string]string{"Workspace": mustGetGitRoot()},
					[]string{
						"Start an instance first:\n  holt up",
						fmt.Sprintf("Then retry:\n  holt forage --goal \"%s\"", forageGoal),
					},
				)
			}
			if err.Error() == "multiple instances found for this workspace, use --name to specify which one" {
				return printer.Error(
					"multiple instances found",
					"Found multiple running instances for this workspace.",
					[]string{
						fmt.Sprintf("Specify which instance to use:\n  holt forage --name <instance-name> --goal \"%s\"", forageGoal),
						"List instances:\n  holt list",
					},
				)
			}
			return fmt.Errorf("failed to infer instance: %w", err)
		}
	}

	// Phase 4: Verify instance is running
	if err := instance.VerifyInstanceRunning(ctx, cli, targetInstanceName); err != nil {
		return printer.Error(
			fmt.Sprintf("instance '%s' is not running", targetInstanceName),
			fmt.Sprintf("Error: %v", err),
			[]string{
				fmt.Sprintf("Start the instance:\n  holt up --name %s", targetInstanceName),
				fmt.Sprintf("Or if stuck, restart:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName),
			},
		)
	}

	// Phase 5: Get Redis port
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return printer.ErrorWithContext(
			"Redis port not found",
			fmt.Sprintf("Instance '%s' exists but Redis port label is missing.", targetInstanceName),
			map[string]string{
				"This may indicate": "Instance was created with older holt version\n  - Manual container manipulation",
			},
			[]string{
				fmt.Sprintf("Restart the instance:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName),
			},
		)
	}

	// Phase 6: Connect to blackboard
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

	// Verify Redis connectivity
	if err := bbClient.Ping(ctx); err != nil {
		return printer.ErrorWithContext(
			"Redis connection failed",
			fmt.Sprintf("Could not connect to Redis at %s", redisURL),
			nil,
			[]string{
				fmt.Sprintf("Check Redis container status:\n  docker logs holt-redis-%s", targetInstanceName),
				fmt.Sprintf("Restart if needed:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName),
			},
		)
	}

	// Phase 7: Fetch active_manifest (M4.7 System Spine)
	activeManifestID, err := fetchActiveManifest(ctx, bbClient, targetInstanceName)
	if err != nil {
		return printer.ErrorWithContext(
			"System Spine not initialized",
			"The orchestrator has not initialized the System Spine yet.",
			map[string]string{"Error": err.Error()},
			[]string{
				"Wait for orchestrator to complete startup and retry",
				fmt.Sprintf("Check orchestrator logs:\n  holt logs orchestrator --name %s", targetInstanceName),
			},
		)
	}

	// Phase 8: If --watch mode, start streaming BEFORE creating artefact to catch all events
	if forageWatch {
		printer.Info("Starting watch mode...\n")

		// Start streaming in a goroutine
		streamDone := make(chan error, 1)
		go func() {
			// No filters, no exit-on-completion for forage command
			streamDone <- watch.StreamActivity(ctx, bbClient, targetInstanceName, watch.OutputFormatDefault, nil, false, os.Stdout)
		}()

		// Give subscription time to set up before publishing artefact
		time.Sleep(100 * time.Millisecond)

		// Create V2-compatible artefact to compute hash
		// M4.7: Anchor to active SystemManifest
		v2Artefact := &blackboard.VerifiableArtefact{
			Header: blackboard.ArtefactHeader{
				ParentHashes:    []string{activeManifestID}, // M4.7: Anchor to SystemManifest
				LogicalThreadID: blackboard.NewID(),         // New thread
				Version:         1,
				CreatedAtMs:     time.Now().UnixMilli(),
				ProducedByRole:  "user",
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ClaimID:         "", // Root artefact has no claim
			},
			Payload: blackboard.ArtefactPayload{
				Content: forageGoal,
			},
		}

		hash, err := blackboard.ComputeArtefactHash(v2Artefact)
		if err != nil {
			return fmt.Errorf("failed to compute hash: %w", err)
		}
		v2Artefact.ID = hash

		// Convert to V1 for client.CreateArtefact
		v1Artefact := &blackboard.Artefact{
			ID:              v2Artefact.ID,
			LogicalID:       v2Artefact.Header.LogicalThreadID,
			Version:         v2Artefact.Header.Version,
			StructuralType:  v2Artefact.Header.StructuralType,
			Type:            v2Artefact.Header.Type,
			Payload:         v2Artefact.Payload.Content,
			SourceArtefacts: v2Artefact.Header.ParentHashes,
			ProducedByRole:  v2Artefact.Header.ProducedByRole,
			CreatedAtMs:     v2Artefact.Header.CreatedAtMs,
			ClaimID:         v2Artefact.Header.ClaimID,
		}

		if err := bbClient.CreateArtefact(ctx, v1Artefact); err != nil {
			return fmt.Errorf("failed to create artefact: %w", err)
		}

		// Wait for streaming to complete (typically on Ctrl+C)
		return <-streamDone
	}

	// Non-watch mode: create artefact and return
	// Create V2-compatible artefact to compute hash
	// M4.7: Anchor to active SystemManifest
	v2Artefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{activeManifestID}, // M4.7: Anchor to SystemManifest
			LogicalThreadID: blackboard.NewID(),         // New thread
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ClaimID:         "", // Root artefact has no claim
		},
		Payload: blackboard.ArtefactPayload{
			Content: forageGoal,
		},
	}

	hash, err := blackboard.ComputeArtefactHash(v2Artefact)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}
	v2Artefact.ID = hash

	// Convert to V1 for client.CreateArtefact
	v1Artefact := &blackboard.Artefact{
		ID:              v2Artefact.ID,
		LogicalID:       v2Artefact.Header.LogicalThreadID,
		Version:         v2Artefact.Header.Version,
		StructuralType:  v2Artefact.Header.StructuralType,
		Type:            v2Artefact.Header.Type,
		Payload:         v2Artefact.Payload.Content,
		SourceArtefacts: v2Artefact.Header.ParentHashes,
		ProducedByRole:  v2Artefact.Header.ProducedByRole,
		CreatedAtMs:     v2Artefact.Header.CreatedAtMs,
		ClaimID:         v2Artefact.Header.ClaimID,
	}

	if err := bbClient.CreateArtefact(ctx, v1Artefact); err != nil {
		return fmt.Errorf("failed to create artefact: %w", err)
	}

	printer.Success("Goal artefact created: %s\n", v1Artefact.ID)

	printer.Info("\nNext steps:\n")
	printer.Info("  • View all artefacts: holt hoard --name %s\n", targetInstanceName)
	printer.Info("  • Monitor workflow: holt watch --name %s\n", targetInstanceName)

	return nil
}

func mustGetGitRoot() string {
	checker := git.NewChecker()
	root, err := checker.GetGitRoot()
	if err != nil {
		return "<unknown>"
	}
	return root
}

// fetchActiveManifest retrieves the active SystemManifest ID from Redis (M4.7).
// This manifest hash is used to anchor root artefacts to the current system configuration state.
func fetchActiveManifest(ctx context.Context, bbClient *blackboard.Client, instanceName string) (string, error) {
	activeManifestKey := fmt.Sprintf("holt:%s:active_manifest", instanceName)
	manifestID, err := bbClient.GetRedisClient().Get(ctx, activeManifestKey).Result()

	if err != nil {
		if err.Error() == "redis: nil" {
			return "", fmt.Errorf("active_manifest not found - orchestrator may not be fully initialized")
		}
		return "", fmt.Errorf("failed to fetch active_manifest: %w", err)
	}

	if manifestID == "" {
		return "", fmt.Errorf("active_manifest is empty")
	}

	return manifestID, nil
}
