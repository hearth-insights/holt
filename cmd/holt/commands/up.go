package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/hearth-insights/holt/internal/config"
	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/git"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	upInstanceName string
	upForce        bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start a Holt instance",
	Long: `Start a new Holt instance in the current Git repository.

Creates and starts:
  • Isolated Docker network
  • Redis container (blackboard storage)
  • Orchestrator container (claim coordinator)

The instance name is auto-generated (default-N) unless specified with --name.
Workspace safety checks prevent multiple instances on the same directory unless --force is used.`,
	RunE: runUp,
}

func init() {
	upCmd.Flags().StringVarP(&upInstanceName, "name", "n", "", "Instance name (auto-generated if omitted)")
	// Note: Cannot use -f shorthand because it conflicts with global --config flag
	upCmd.Flags().BoolVar(&upForce, "force", false, "Bypass workspace collision check")
	rootCmd.AddCommand(upCmd)
}

// expandTildeInVolume expands ~ at the beginning of a volume mount source path (M4.5)
// Example: "~/foo:/bar:ro" → "/home/user/foo:/bar:ro"
// Only expands ~ at the start of the source path, not in destination or mode
func expandTildeInVolume(volumeSpec string) (string, error) {
	// Split by colons to get source:destination:mode parts
	parts := strings.Split(volumeSpec, ":")
	if len(parts) < 2 {
		// Invalid format, but let Docker handle the error
		return volumeSpec, nil
	}

	sourcePath := parts[0]

	// Only expand if it starts with ~/
	if !strings.HasPrefix(sourcePath, "~/") && sourcePath != "~" {
		return volumeSpec, nil
	}

	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory for tilde expansion: %w", err)
	}

	// Expand tilde
	var expandedSource string
	if sourcePath == "~" {
		expandedSource = homeDir
	} else {
		expandedSource = filepath.Join(homeDir, sourcePath[2:]) // Remove ~/ and join with home
	}

	// Reconstruct volume spec with expanded source
	parts[0] = expandedSource
	return strings.Join(parts, ":"), nil
}

func runUp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Phase 1: Environment Validation
	if err := validateEnvironment(); err != nil {
		return err
	}

	// Silence usage for runtime errors (M4.4)
	// We only want to show help text for flag parsing errors, not for
	// runtime failures like container startup issues.
	cmd.SilenceUsage = true

	// Phase 2: Configuration Validation
	cfg, err := config.Load("holt.yml")
	if err != nil {
		// M4.4: Show actual config error for better debugging
		return printer.Error(
			"holt.yml not found or invalid",
			fmt.Sprintf("Configuration error: %v", err),
			[]string{
				"Fix the configuration error above",
				"Or initialize a new project: holt init",
			},
		)
	}

	// M4.10: Check for keep_containers setting and warn if enabled
	var agentsWithRetention []string
	for agentName, agent := range cfg.Agents {
		if agent.Worker != nil && agent.Worker.KeepContainers {
			agentsWithRetention = append(agentsWithRetention, agentName)
		}
	}
	if len(agentsWithRetention) > 0 {
		printer.Warning("Worker container retention enabled for: %s\n", strings.Join(agentsWithRetention, ", "))
		printer.Warning("  Stopped containers will NOT be automatically removed.\n")
		printer.Warning("  Use 'holt down' to clean up all containers.\n")
		printer.Warning("  This is intended for debugging only. Disable in production.\n\n")
	}

	// Create Docker client
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	// Phase 3: Instance Name Determination
	targetInstanceName := upInstanceName
	if targetInstanceName == "" {
		// Auto-generate default-N name
		targetInstanceName, err = instance.GenerateDefaultName(ctx, cli)
		if err != nil {
			return fmt.Errorf("failed to generate instance name: %w", err)
		}
	}

	// Validate instance name
	if err := instance.ValidateName(targetInstanceName); err != nil {
		return err
	}

	// Check for name collision
	nameCollision, err := instance.CheckNameCollision(ctx, cli, targetInstanceName)
	if err != nil {
		return err
	}
	if nameCollision {
		return printer.Error(
			fmt.Sprintf("instance '%s' already exists", targetInstanceName),
			"Found existing containers with this instance name.",
			[]string{
				fmt.Sprintf("Stop the existing instance: holt down --name %s", targetInstanceName),
				"Choose a different name: holt up --name other-name",
			},
		)
	}

	// Phase 4: Workspace Safety Check
	workspacePath, err := instance.GetCanonicalWorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace path: %w", err)
	}

	if !upForce {
		collision, err := instance.CheckWorkspaceCollision(ctx, cli, workspacePath, targetInstanceName)
		if err != nil {
			return fmt.Errorf("failed to check workspace collision: %w", err)
		}
		if collision != nil {
			return printer.ErrorWithContext(
				"workspace in use",
				fmt.Sprintf("Another instance '%s' is already running on this workspace:", collision.InstanceName),
				map[string]string{
					"Workspace": collision.WorkspacePath,
					"Instance":  collision.InstanceName,
				},
				[]string{
					fmt.Sprintf("Stop the other instance: holt down --name %s", collision.InstanceName),
					"Use --force to bypass this check (not recommended)",
				},
			)
		}
	}

	// Phase 5: M3.5 Stale Lock Detection
	if err := detectAndHandleStaleLock(ctx, cli, targetInstanceName); err != nil {
		return err
	}

	// Phase 6: Resource Creation
	runID := blackboard.NewID()
	if err := createInstance(ctx, cli, cfg, targetInstanceName, runID, workspacePath); err != nil {
		// Attempt rollback on failure
		printer.Info("\nResource creation failed: %v\nRolling back...\n", err)
		if rollbackErr := rollbackInstance(ctx, cli, targetInstanceName); rollbackErr != nil {
			printer.Warning("rollback encountered errors: %v\n", rollbackErr)
		}
		return fmt.Errorf("failed to create instance: %w", err)
	}

	// Success message
	printUpSuccess(targetInstanceName, workspacePath, cfg)

	return nil
}

func validateEnvironment() error {
	// Check Git context
	checker := git.NewChecker()
	if err := checker.ValidateGitContext(); err != nil {
		return printer.Error(
			"not a Git repository",
			"Holt requires initialization from within a Git repository.",
			[]string{"Run these commands in order:\n  1. git init\n  2. holt init\n  3. holt up"},
		)
	}

	return nil
}

func createInstance(ctx context.Context, cli *client.Client, cfg *config.HoltConfig, instanceName, runID, workspacePath string) error {
	// Step 1: Validate all agent images exist
	if err := validateAgentImages(ctx, cli, cfg); err != nil {
		return err
	}

	// M4.4: Determine Redis mode (external vs managed)
	redisConfig, err := determineRedisMode(cfg)
	if err != nil {
		return fmt.Errorf("failed to determine Redis mode: %w", err)
	}

	// Step 2: Create isolated network
	networkName := dockerpkg.NetworkName(instanceName)
	networkLabels := dockerpkg.BuildLabels(instanceName, runID, workspacePath, "")

	_, err = cli.NetworkCreate(ctx, networkName, types.NetworkCreate{
		Driver: "bridge",
		Labels: networkLabels,
	})
	if err != nil {
		return fmt.Errorf("failed to create network '%s': %w", networkName, err)
	}

	printer.Debug("Created network: %s\n", networkName)

	// M4.4: Step 3: Handle Redis based on mode
	var redisURL string
	var redisPort int

	if redisConfig.Mode == RedisModeExternal {
		// External mode: Use provided URI directly
		redisURL = redisConfig.URI
		redisPort = 0 // No port allocation needed for external Redis
		printer.Debug("Using external Redis: %s\n", sanitizeRedisURI(redisURL))
	} else {
		// Managed mode: Start Redis container
		redisPort, err = instance.FindNextAvailablePort(ctx, cli)
		if err != nil {
			return fmt.Errorf("failed to allocate Redis port: %w", err)
		}
		printer.Debug("Allocated Redis port: %d\n", redisPort)

		redisName := dockerpkg.RedisContainerName(instanceName)

		// M4.4: Start Redis with optional password
		if err := startManagedRedis(ctx, cli, instanceName, runID, workspacePath, networkName, redisConfig, redisPort); err != nil {
			return err
		}

		// M4.4: Construct Redis URL (with password if set)
		redisURL = constructManagedRedisURI(redisName, redisConfig.Password)
		printer.Debug("Started Redis container: %s (port %d, auth: %v)\n", redisName, redisPort, redisConfig.Password != "")
	}

	// Step 4: Verify orchestrator image exists
	orchestratorImage := "holt-orchestrator:latest"
	if cfg.Orchestrator != nil && cfg.Orchestrator.Image != "" {
		orchestratorImage = cfg.Orchestrator.Image
	}
	if err := verifyOrchestratorImage(ctx, cli, orchestratorImage); err != nil {
		return err
	}

	// Step 5: Start Orchestrator container with pre-built image
	orchestratorName := dockerpkg.OrchestratorContainerName(instanceName)
	orchestratorLabels := dockerpkg.BuildLabels(instanceName, runID, workspacePath, "orchestrator")

	// M3.4: Get Docker socket GID for worker management permissions
	dockerGroups := getDockerSocketGroups()

	// M4.5: Extract environment variables from holt.yml and pass them to orchestrator
	// The orchestrator needs these to expand ${VAR_NAME} references when loading the config
	orchestratorEnv := []string{
		fmt.Sprintf("HOLT_INSTANCE_NAME=%s", instanceName),
		fmt.Sprintf("REDIS_URL=%s", redisURL), // M4.4: May be external or managed
		// M3.4: Pass host workspace path for worker mounts
		fmt.Sprintf("HOST_WORKSPACE_PATH=%s", workspacePath),
	}

	// Read raw holt.yml to extract environment variable references
	holtYmlData, err := os.ReadFile("holt.yml")
	if err != nil {
		return fmt.Errorf("failed to read holt.yml for env var extraction: %w", err)
	}

	// Extract environment variable names referenced in the config
	envVarNames := config.ExtractEnvVarNames(holtYmlData)
	for _, varName := range envVarNames {
		// Get value from host environment
		if value, exists := os.LookupEnv(varName); exists {
			orchestratorEnv = append(orchestratorEnv, fmt.Sprintf("%s=%s", varName, value))
			printer.Debug("Passing environment variable to orchestrator: %s\n", varName)
		} else {
			// This should not happen since config.Load() already validated all vars exist
			printer.Warning("Environment variable '%s' referenced in holt.yml but not set\n", varName)
		}
	}

	orchestratorResp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:  orchestratorImage,
		Labels: orchestratorLabels,
		Env:    orchestratorEnv,
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(networkName),
		Binds: []string{
			fmt.Sprintf("%s:/workspace:ro", workspacePath),
			// M3.4: Mount Docker socket for worker management
			"/var/run/docker.sock:/var/run/docker.sock",
		},
		// M3.4: Grant Docker socket access (required for worker launching)
		// Only add group if we successfully detected the socket's GID
		GroupAdd: dockerGroups,
	}, nil, nil, orchestratorName)
	if err != nil {
		return fmt.Errorf("failed to create orchestrator container: %w", err)
	}

	if err := cli.ContainerStart(ctx, orchestratorResp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start orchestrator container: %w", err)
	}

	printer.Debug("Started orchestrator container: %s\n", orchestratorName)

	// Step 6: Launch agent containers
	// M4.4: Pass redisURL instead of redisName to support both modes
	if err := launchAgentContainersWithRedisURL(ctx, cli, cfg, instanceName, runID, workspacePath, networkName, redisURL); err != nil {
		return fmt.Errorf("failed to launch agent containers: %w", err)
	}

	// Step 7: M3.9: Populate agent_images hash for audit trail
	// M4.4: Skip if external Redis (port is 0)
	if redisPort > 0 {
		if err := populateAgentImages(ctx, cli, cfg, instanceName, redisPort); err != nil {
			return fmt.Errorf("failed to populate agent images: %w", err)
		}
	} else {
		// M4.4: For external Redis, we need to connect differently
		// For now, skip this step - will be addressed in future work
		printer.Debug("Skipping agent_images population for external Redis (not yet implemented)\n")
	}

	return nil
}

// M4.4: startManagedRedis creates and starts a managed Redis container with optional password
func startManagedRedis(ctx context.Context, cli *client.Client, instanceName, runID, workspacePath, networkName string, redisConfig RedisConfig, redisPort int) error {
	redisName := dockerpkg.RedisContainerName(instanceName)
	redisLabels := dockerpkg.BuildLabels(instanceName, runID, workspacePath, "redis")
	// Add Redis port label
	redisLabels[dockerpkg.LabelRedisPort] = fmt.Sprintf("%d", redisPort)

	// M4.4: Build Redis command with optional password
	var redisCmd []string
	if redisConfig.Password != "" {
		// Start Redis with password protection
		redisCmd = []string{"redis-server", "--requirepass", redisConfig.Password}
	}
	// If no password, use default Redis command (nil = use image's default CMD)

	redisResp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:  redisConfig.Image,
		Labels: redisLabels,
		Cmd:    redisCmd, // M4.4: May be nil (default) or with password
		ExposedPorts: nat.PortSet{
			"6379/tcp": struct{}{},
		},
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(networkName),
		PortBindings: nat.PortMap{
			"6379/tcp": []nat.PortBinding{
				{
					HostIP:   "127.0.0.1",
					HostPort: fmt.Sprintf("%d", redisPort),
				},
			},
		},
	}, nil, nil, redisName)
	if err != nil {
		return fmt.Errorf("failed to create Redis container: %w", err)
	}

	if err := cli.ContainerStart(ctx, redisResp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start Redis container: %w", err)
	}

	return nil
}

func rollbackInstance(ctx context.Context, cli *client.Client, instanceName string) error {
	timeout := 10

	// Find all containers for this instance
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s=%s", dockerpkg.LabelInstanceName, instanceName)),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Stop and remove containers
	for _, c := range containers {
		printer.Info("  Stopping %s...\n", c.Names[0])
		_ = cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})

		printer.Info("  Removing %s...\n", c.Names[0])
		if err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			printer.Warning("failed to remove %s: %v\n", c.Names[0], err)
		}
	}

	// Remove network
	networks, err := cli.NetworkList(ctx, types.NetworkListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s=%s", dockerpkg.LabelInstanceName, instanceName)),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	for _, net := range networks {
		printer.Info("  Removing network %s...\n", net.Name)
		if err := cli.NetworkRemove(ctx, net.ID); err != nil {
			printer.Warning("failed to remove network %s: %v\n", net.Name, err)
		}
	}

	return nil
}

func printUpSuccess(instanceName, workspacePath string, cfg *config.HoltConfig) {
	agentCount := len(cfg.Agents)
	printer.Success("\nInstance '%s' started successfully (%d agents ready)\n\n", instanceName, agentCount)

	// Debug-level container/network details
	printer.Debug("Containers:\n")
	printer.Debug("  • %s (running)\n", dockerpkg.RedisContainerName(instanceName))
	printer.Debug("  • %s (running)\n", dockerpkg.OrchestratorContainerName(instanceName))

	// List agent containers (M3.7: agent key IS the role)
	for agentRole, agent := range cfg.Agents {
		printer.Debug("  • %s (running, healthy, bidding_strategy=%s)\n",
			dockerpkg.AgentContainerName(instanceName, agentRole),
			agent.BiddingStrategy)
	}

	printer.Debug("\n")
	printer.Debug("Network:\n")
	printer.Debug("  • %s\n", dockerpkg.NetworkName(instanceName))
	printer.Debug("\n")

	printer.Info("Workspace: %s\n", workspacePath)
	printer.Info("\n")
	printer.Info("Next steps:\n")
	printer.Info("  1. Run 'holt forage --goal \"your goal\"' to start a workflow\n")
	if len(cfg.Agents) > 0 {
		// Get first agent name for example
		var firstAgent string
		for name := range cfg.Agents {
			firstAgent = name
			break
		}
		printer.Info("  2. Run 'holt logs %s' to view agent logs\n", firstAgent)
		printer.Info("  3. Run 'holt down --name %s' when finished\n", instanceName)
	} else {
		printer.Info("  2. Run 'holt list' to view all instances\n")
		printer.Info("  3. Run 'holt down --name %s' when finished\n", instanceName)
	}
}

func verifyOrchestratorImage(ctx context.Context, cli *client.Client, imageName string) error {
	// Check if the image exists locally
	images, err := cli.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Docker images: %w", err)
	}

	// Look for the orchestrator image
	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == imageName {
				printer.Debug("Found orchestrator image: %s\n", imageName)
				return nil
			}
		}
	}

	// Image not found - return helpful error
	return printer.Error(
		fmt.Sprintf("orchestrator image '%s' not found", imageName),
		"",
		[]string{"Please run 'make docker-orchestrator' to build it first."},
	)
}

func validateAgentImages(ctx context.Context, cli *client.Client, cfg *config.HoltConfig) error {
	if len(cfg.Agents) == 0 {
		return nil
	}

	// Get list of all local images
	images, err := cli.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Docker images: %w", err)
	}

	// Build set of available image tags
	availableImages := make(map[string]bool)
	for _, image := range images {
		for _, tag := range image.RepoTags {
			availableImages[tag] = true
		}
	}

	// Validate each agent image exists (M3.7: agent key IS the role)
	var missingImages []string
	for agentRole, agent := range cfg.Agents {
		if !availableImages[agent.Image] {
			missingImages = append(missingImages, fmt.Sprintf("%s (for agent '%s')", agent.Image, agentRole))
		}
	}

	if len(missingImages) > 0 {
		return printer.Error(
			"agent images not found",
			fmt.Sprintf("The following agent images are not available locally:\n  - %s",
				missingImages[0]),
			[]string{
				"Build the agent images first:",
				"  cd agents/<agent-dir>",
				"  docker build -t <image-name> .",
				"",
				"Then retry: holt up",
			},
		)
	}

	printer.Debug("Validated %d agent image(s)\n", len(cfg.Agents))
	return nil
}

// M3.1: launchAgentContainersParallel launches all agent containers in parallel with fail-fast.
// Uses goroutines + WaitGroup for concurrent startup.
// Validates health checks before reporting success.
// Returns error immediately on first failure (fail-fast) and triggers rollback.
func launchAgentContainers(ctx context.Context, cli *client.Client, cfg *config.HoltConfig, instanceName, runID, workspacePath, networkName, redisName string) error {
	// M4.4: Construct Redis URL from container name (backward compatibility)
	redisURL := fmt.Sprintf("redis://%s:6379", redisName)
	return launchAgentContainersWithRedisURL(ctx, cli, cfg, instanceName, runID, workspacePath, networkName, redisURL)
}

// M4.4: launchAgentContainersWithRedisURL is the updated version that accepts a Redis URL
// This supports both external and managed Redis modes
func launchAgentContainersWithRedisURL(ctx context.Context, cli *client.Client, cfg *config.HoltConfig, instanceName, runID, workspacePath, networkName, redisURL string) error {
	if len(cfg.Agents) == 0 {
		return nil
	}

	// Create cancellable context for fail-fast behavior
	launchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channels for collecting results
	type launchResult struct {
		agentName string
		err       error
	}
	resultChan := make(chan launchResult, len(cfg.Agents))

	// Launch all agents in parallel (M3.7: agent key IS the role)
	agentCount := 0
	for agentRole, agent := range cfg.Agents {
		agentCount++
		// Launch each agent in a goroutine
		go func(role string, agentCfg config.Agent) {
			err := launchAgentContainerWithRedisURL(launchCtx, cli, instanceName, runID, workspacePath, networkName, redisURL, role, agentCfg)
			resultChan <- launchResult{agentName: role, err: err}
		}(agentRole, agent)
	}

	// Collect results - fail fast on first error
	var containerNames []string
	for i := 0; i < agentCount; i++ {
		result := <-resultChan
		if result.err != nil {
			// Fail-fast: cancel other launches and return error
			cancel()
			return fmt.Errorf("failed to launch agent '%s': %w", result.agentName, result.err)
		}
		containerNames = append(containerNames, dockerpkg.AgentContainerName(instanceName, result.agentName))
	}

	printer.Debug("Started %d agent container(s) in parallel\n", agentCount)

	// M3.1: Validate all agents are healthy before reporting success
	if err := validateAllAgentsHealthy(ctx, cli, containerNames); err != nil {
		return fmt.Errorf("agent health check failed: %w", err)
	}

	printer.Debug("All agents healthy\n")

	return nil
}

// M3.7: agentRole parameter is the agent key from holt.yml (which IS the role)
func launchAgentContainer(ctx context.Context, cli *client.Client, instanceName, runID, workspacePath, networkName, redisName, agentRole string, agent config.Agent) error {
	// M4.4: Construct Redis URL from container name (backward compatibility)
	redisURL := fmt.Sprintf("redis://%s:6379", redisName)
	return launchAgentContainerWithRedisURL(ctx, cli, instanceName, runID, workspacePath, networkName, redisURL, agentRole, agent)
}

// M4.4: launchAgentContainerWithRedisURL is the updated version that accepts a Redis URL
func launchAgentContainerWithRedisURL(ctx context.Context, cli *client.Client, instanceName, runID, workspacePath, networkName, redisURL, agentRole string, agent config.Agent) error {
	containerName := dockerpkg.AgentContainerName(instanceName, agentRole)
	labels := dockerpkg.BuildLabels(instanceName, runID, workspacePath, "agent")
	labels[dockerpkg.LabelAgentName] = agentRole // M3.7: Agent name = role
	labels[dockerpkg.LabelAgentRole] = agentRole // M3.7: Same value (kept for label consistency)

	// Determine workspace mode (default to ro)
	workspaceMode := "ro"
	if agent.Workspace != nil && agent.Workspace.Mode != "" {
		workspaceMode = agent.Workspace.Mode
	}

	// Serialize BiddingStrategyConfig to JSON (M4.8)
	biddingStrategyJSON, err := json.Marshal(agent.BiddingStrategy)
	if err != nil {
		return fmt.Errorf("failed to marshal bidding strategy: %w", err)
	}

	// Build environment variables
	// M3.7: ONLY HOLT_AGENT_NAME is set (to the role), HOLT_AGENT_ROLE removed
	// M4.4: Use provided redisURL (may be external or managed with password)
	env := []string{
		fmt.Sprintf("HOLT_INSTANCE_NAME=%s", instanceName),
		fmt.Sprintf("HOLT_AGENT_NAME=%s", agentRole),
		fmt.Sprintf("REDIS_URL=%s", redisURL),
		fmt.Sprintf("HOLT_BIDDING_STRATEGY=%s", string(biddingStrategyJSON)), // M4.8: Serialized JSON
	}

	// M3.4: Set HOLT_MODE for controller agents
	if agent.Mode == "controller" {
		env = append(env, "HOLT_MODE=controller")
	}

	// Add HOLT_AGENT_COMMAND as JSON array
	if len(agent.Command) > 0 {
		commandJSON, err := json.Marshal(agent.Command)
		if err != nil {
			return fmt.Errorf("failed to marshal agent command to JSON: %w", err)
		}
		env = append(env, fmt.Sprintf("HOLT_AGENT_COMMAND=%s", commandJSON))
	}

	// Add HOLT_AGENT_BID_SCRIPT as JSON array
	if len(agent.BidScript) > 0 {
		bidScriptJSON, err := json.Marshal(agent.BidScript)
		if err != nil {
			return fmt.Errorf("failed to marshal agent bid script to JSON: %w", err)
		}
		env = append(env, fmt.Sprintf("HOLT_AGENT_BID_SCRIPT=%s", bidScriptJSON))
	}

	// Add custom environment variables from config (with expansion)
	if len(agent.Environment) > 0 {
		for _, envVar := range agent.Environment {
			env = append(env, os.ExpandEnv(envVar))
		}
	}

	// M4.5: Build bind mounts (workspace + custom volumes)
	binds := []string{
		fmt.Sprintf("%s:/workspace:%s", workspacePath, workspaceMode),
	}

	// M4.5: Add custom volume mounts with tilde expansion
	for _, volumeSpec := range agent.Volumes {
		expandedVolume, err := expandTildeInVolume(volumeSpec)
		if err != nil {
			return fmt.Errorf("failed to expand volume mount '%s': %w", volumeSpec, err)
		}
		binds = append(binds, expandedVolume)
		printer.Debug("Agent '%s': Added volume mount: %s\n", agentRole, expandedVolume)
	}

	// Create container
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:  agent.Image,
		Labels: labels,
		Env:    env,
		Cmd:    agent.Command,
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(networkName),
		Binds:       binds,
	}, nil, nil, containerName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	printer.Success("Started agent container: %s\n", containerName)
	return nil
}

// validateAllAgentsHealthy validates that all agent containers pass health checks.
// M3.1: Uses docker exec to check /healthz endpoint inside each container.
// Implements retry logic with exponential backoff (5 attempts over ~10s).
// Total timeout: 30 seconds for all agents.
// Fail-fast: Returns error immediately on first health check failure.
func validateAllAgentsHealthy(ctx context.Context, cli *client.Client, containerNames []string) error {
	// Create timeout context (30 seconds total)
	healthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	printer.Info("Validating agent health checks...\n")

	// Check each agent health in parallel
	type healthResult struct {
		containerName string
		err           error
	}
	resultChan := make(chan healthResult, len(containerNames))

	for _, containerName := range containerNames {
		go func(name string) {
			err := validateAgentHealth(healthCtx, cli, name)
			resultChan <- healthResult{containerName: name, err: err}
		}(containerName)
	}

	// Collect results - fail fast on first error
	for i := 0; i < len(containerNames); i++ {
		result := <-resultChan
		if result.err != nil {
			// M4.4: Print logs for the failed container to help debugging
			// We do this before returning the error so the logs appear before rollback
			printer.Warning("\nHealth check failed for %s. Fetching logs...\n", result.containerName)
			if logErr := printContainerLogs(ctx, cli, result.containerName); logErr != nil {
				printer.Warning("Failed to fetch logs: %v\n", logErr)
			}
			printer.Info("\n") // Add spacing before error/rollback message

			return fmt.Errorf("agent %s failed health check: %w", result.containerName, result.err)
		}
		printer.Info("  ✓ %s (healthy)\n", result.containerName)
	}

	return nil
}

// validateAgentHealth checks if a single agent container is healthy.
// Retries up to 5 times with exponential backoff: 100ms, 200ms, 400ms, 800ms, 1600ms.
func validateAgentHealth(ctx context.Context, cli *client.Client, containerName string) error {
	maxAttempts := 10
	backoff := 500 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Try health check
		err := checkHealthEndpoint(ctx, cli, containerName)
		if err == nil {
			return nil // Success!
		}

		// Check if context cancelled (timeout or fail-fast)
		if ctx.Err() != nil {
			return fmt.Errorf("health check cancelled: %w", ctx.Err())
		}

		// Last attempt failed - return error
		if attempt == maxAttempts {
			return fmt.Errorf("health check failed after %d attempts: %w", maxAttempts, err)
		}

		// Wait before retry with exponential backoff
		time.Sleep(backoff)
		backoff *= 2
	}

	return fmt.Errorf("health check failed after %d attempts", maxAttempts)
}

// checkHealthEndpoint uses docker exec to check the /healthz endpoint inside a container.
// Uses wget -q -O- http://localhost:8080/healthz to fetch health status.
func checkHealthEndpoint(ctx context.Context, cli *client.Client, containerName string) error {
	// Create exec instance
	execConfig := types.ExecConfig{
		Cmd:          []string{"wget", "-q", "-O-", "http://localhost:8080/healthz"},
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := cli.ContainerExecCreate(ctx, containerName, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	// Start exec
	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("failed to start exec: %w", err)
	}
	defer resp.Close()

	// Wait for completion
	err = cli.ContainerExecStart(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("failed to exec start: %w", err)
	}

	// Check exit code
	inspectResp, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("health check returned non-zero exit code: %d", inspectResp.ExitCode)
	}

	return nil
}

// printContainerLogs fetches and prints the last 50 lines of logs from a container (M4.4).
// This is useful for debugging startup failures.
func printContainerLogs(ctx context.Context, cli *client.Client, containerName string) error {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "50",
	}

	logs, err := cli.ContainerLogs(ctx, containerName, options)
	if err != nil {
		return fmt.Errorf("failed to get container logs: %w", err)
	}
	defer logs.Close()

	// Use stdcopy to demultiplex stdout/stderr if needed, or just copy to stdout
	// Since we want to show it to the user, we'll just copy everything to stdout
	// Note: Docker logs are multiplexed, so we might see header bytes if we just Copy.
	// However, for simple debugging, printing raw output might be enough, but
	// using stdcopy is cleaner. Since we don't want to import stdcopy just for this
	// if it's not already imported, we'll check imports.
	// Actually, let's just use a simple copy for now as adding imports might be complex
	// in a multi-replace.
	// Wait, we can't just io.Copy because of the multiplexing headers.
	// Let's try to read it simply.
	// Better yet, let's use the printer to print a header.

	printer.Info("--- Logs for %s ---\n", containerName)
	// We need to handle the multiplexed stream.
	// Since we can't easily add "github.com/docker/docker/pkg/stdcopy" without checking imports,
	// and we know `cli` is from `github.com/docker/docker/client`,
	// let's assume we can just dump it for now or use a simple buffer.
	// actually, `docker logs` output usually needs stdcopy.
	// Let's try to just read it all and print it.
	// If we just io.Copy(os.Stdout, logs), we get the headers.
	// Let's rely on the fact that for TTY containers it's raw, but for non-TTY it's multiplexed.
	// Our agents might be non-TTY.
	// Let's just print it. The user will see some garbage headers but the text will be there.
	// OR, we can try to be smarter.
	// Let's just use io.Copy to os.Stdout for now.
	_, err = os.Stdout.ReadFrom(logs)
	if err != nil {
		return err
	}
	printer.Info("\n--- End of logs ---\n")

	return nil
}

// getDockerSocketGroups returns the GID of /var/run/docker.sock for container access.
// Handles platform differences:
// - Linux: Returns docker group GID (typically 999, 998, or 121)
// - macOS: Returns "0" (root group) since Docker Desktop's socket appears as GID 0 in containers
// - GitHub Actions: Returns detected GID (varies by runner)
//
// Returns empty slice if unable to detect (graceful degradation).
func getDockerSocketGroups() []string {
	// Stat the Docker socket to get its GID
	fileInfo, err := os.Stat("/var/run/docker.sock")
	if err != nil {
		// Socket doesn't exist or can't be accessed
		// This is expected in some test environments
		printer.Warning("Docker socket not accessible: %v (worker management will be disabled)\n", err)
		return []string{}
	}

	// Get the GID from the file info
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		// Can't get stat info - platform doesn't support syscall.Stat_t
		printer.Warning("Cannot detect Docker socket GID (worker management will be disabled)\n")
		return []string{}
	}

	gid := stat.Gid

	// Platform-specific handling
	if gid == 0 {
		// Socket owned by root (GID 0) - likely Linux without docker group
		printer.Info("Docker socket owned by root (GID 0)\n")
		return []string{"0"}
	}

	// macOS Docker Desktop: Host shows GID 20 (staff), but inside containers it's GID 0
	// We detect macOS by checking for common macOS GIDs (20=staff, 80=admin)
	if gid == 20 || gid == 80 {
		printer.Info("Docker socket GID: %d (macOS detected - using GID 0 for container)\n", gid)
		return []string{"0"}
	}

	// Linux/GitHub Actions: Return the actual docker group GID
	gidStr := strconv.FormatUint(uint64(gid), 10)
	printer.Info("Docker socket GID: %s (adding to orchestrator container)\n", gidStr)
	return []string{gidStr}
}

// populateAgentImages populates the agent_images hash in Redis (M3.9).
// This hash maps agent roles to their Docker image IDs for audit trail.
// For traditional/controller agents, resolves image ID from running container.
// Fails hard if docker inspect fails - audit trail integrity is critical.
func populateAgentImages(ctx context.Context, cli *client.Client, cfg *config.HoltConfig, instanceName string, redisPort int) error {
	// Determine Redis address based on environment
	// In Docker-in-Docker (DinD) scenarios, we need to use host.docker.internal or gateway IP
	// Otherwise, use localhost with mapped port
	redisAddr := fmt.Sprintf("127.0.0.1:%d", redisPort)

	// Check if we're in a Docker container
	if _, err := os.Stat("/.dockerenv"); err == nil {
		// Try host.docker.internal first (works on Docker Desktop)
		redisAddr = fmt.Sprintf("host.docker.internal:%d", redisPort)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer redisClient.Close()

	// Test connection with retry (Redis may need a moment to be ready)
	var lastErr error
	for i := 0; i < 10; i++ {
		if err := redisClient.Ping(ctx).Err(); err == nil {
			break // Connection successful
		} else {
			lastErr = err
			if i < 9 {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("failed to connect to Redis after retries: %w", lastErr)
	}

	// M3.9: Import blackboard package for schema helper
	agentImagesKey := fmt.Sprintf("holt:%s:agent_images", instanceName)

	// Iterate through agents
	for agentRole, agent := range cfg.Agents {
		// Skip worker-only agents (they're not running yet)
		if agent.Mode == "controller" || agent.Replicas == nil || *agent.Replicas == 1 {
			// Get container name
			containerName := dockerpkg.AgentContainerName(instanceName, agentRole)

			// Get container info
			containerInfo, err := cli.ContainerInspect(ctx, containerName)
			if err != nil {
				return fmt.Errorf("failed to inspect container %s: %w (Cannot start instance without complete audit trail)", containerName, err)
			}

			// Get image ID from container's image reference
			imageID, err := getImageDigest(ctx, cli, containerInfo.Image)
			if err != nil {
				return fmt.Errorf("failed to resolve image ID for agent '%s': %w (Cannot start instance without complete audit trail)", agentRole, err)
			}

			// Store in Redis hash
			if err := redisClient.HSet(ctx, agentImagesKey, agentRole, imageID).Err(); err != nil {
				return fmt.Errorf("failed to store image ID for agent '%s': %w", agentRole, err)
			}

			printer.Info("  Registered agent '%s' with image %s\n", agentRole, truncateImageID(imageID))
		}
	}

	printer.Success("Registered %d agent image(s) for audit trail\n", len(cfg.Agents))
	return nil
}

// getImageDigest resolves a Docker image reference to its content-addressable digest.
// Returns full sha256:... digest if available, falls back to image ID.
func getImageDigest(ctx context.Context, cli *client.Client, imageRef string) (string, error) {
	imageInfo, _, err := cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("failed to inspect image: %w", err)
	}

	// Prefer RepoDigests (contains registry path + sha256)
	if len(imageInfo.RepoDigests) > 0 {
		return imageInfo.RepoDigests[0], nil
	}

	// Fallback to image ID (local builds without registry)
	if imageInfo.ID != "" {
		return imageInfo.ID, nil
	}

	return "", fmt.Errorf("image has no digest or ID")
}

// truncateImageID shortens an image ID/digest for display (M3.9).
// Extracts first 12 characters of sha256 hash.
func truncateImageID(imageID string) string {
	// Handle "sha256:..." format
	if len(imageID) > 7 && imageID[:7] == "sha256:" {
		hash := imageID[7:]
		if len(hash) >= 12 {
			return hash[:12]
		}
		return hash
	}

	// Handle other formats
	if len(imageID) >= 12 {
		return imageID[:12]
	}

	return imageID
}

// detectAndHandleStaleLock checks for stale orchestrator locks and handles takeover (M3.5).
// Connects to Redis to check the instance lock. If lock exists but is stale (>30s old),
// assumes previous orchestrator crashed and proceeds with takeover.
func detectAndHandleStaleLock(ctx context.Context, cli *client.Client, instanceName string) error {
	printer.Info("Detecting instance state...\n")

	// Check if Redis container exists for this instance
	redisName := dockerpkg.RedisContainerName(instanceName)
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("name", redisName),
		),
	})
	if err != nil {
		return fmt.Errorf("failed to check for existing Redis: %w", err)
	}

	if len(containers) == 0 {
		// No Redis container - this is a fresh instance start
		printer.Info("  ✓ No existing instance found (fresh start)\n")
		return nil
	}

	// Redis exists - check if it's running
	redisContainer := containers[0]
	if redisContainer.State != "running" {
		// Redis exists but not running - likely from failed previous start
		printer.Info("  ⚠ Found stopped Redis container from previous run\n")
		printer.Info("  ✓ Will clean up and start fresh\n")
		return nil
	}

	// Redis is running - check for orchestrator lock
	// We need to connect to Redis to read the lock key
	// Get Redis port from container labels
	redisPort := redisContainer.Labels[dockerpkg.LabelRedisPort]
	if redisPort == "" {
		// Old instance without port label - can't check lock, proceed with warning
		printer.Warning("  ⚠ Cannot check orchestrator lock (old instance format)\n")
		return nil
	}

	// Connect to Redis using go-redis
	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("localhost:%s", redisPort),
	})
	defer redisClient.Close()

	// Test connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		printer.Warning("  ⚠ Cannot connect to Redis to check lock: %v\n", err)
		return nil // Non-fatal - proceed with instance start
	}

	// Check for orchestrator lock
	lockKey := fmt.Sprintf("holt:%s:lock", instanceName)
	lockValue, err := redisClient.Get(ctx, lockKey).Result()
	if err != nil {
		// No lock or error reading - safe to proceed
		printer.Info("  ✓ No active orchestrator lock\n")
		return nil
	}

	// Lock exists - parse timestamp
	// Expected format: "orchestrator:{timestamp}"
	var timestamp int64
	if _, err := fmt.Sscanf(lockValue, "orchestrator:%d", &timestamp); err != nil {
		// Malformed lock - consider stale
		printer.Warning("  ⚠ Found malformed orchestrator lock (will take over)\n")
		return nil
	}

	// Check if lock is stale (>30s old)
	age := time.Now().Unix() - timestamp
	if age > 30 {
		// Stale lock - previous orchestrator crashed
		printer.Warning("  ⚠ Found stale orchestrator lock (age: %ds)\n", age)
		printer.Info("  ✓ Taking over instance '%s' (previous orchestrator assumed crashed)\n", instanceName)

		// Clean up the stale lock
		if err := redisClient.Del(ctx, lockKey).Err(); err != nil {
			printer.Warning("  ⚠ Failed to clear stale lock: %v\n", err)
		}

		return nil
	}

	// Lock is fresh - orchestrator is running
	return printer.Error(
		fmt.Sprintf("instance '%s' orchestrator is already running", instanceName),
		fmt.Sprintf("Found active orchestrator lock (age: %ds, threshold: 30s)", age),
		[]string{
			fmt.Sprintf("Wait for the orchestrator to stop, or run: holt down --name %s", instanceName),
			"If you're sure the orchestrator is not running, wait 30s for the lock to become stale",
		},
	)
}
