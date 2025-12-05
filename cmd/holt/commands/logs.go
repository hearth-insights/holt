package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/spf13/cobra"
)

var (
	logsInstanceName string
	logsFollow       bool
	logsSince        string
	logsTail         string
	logsTimestamps   bool
)

// M4.10: holt logs command - streams Docker container logs
var logsCmd = &cobra.Command{
	Use:   "logs <role-or-orchestrator>",
	Short: "View logs for an agent or orchestrator container",
	Long: `View logs from a Holt agent or orchestrator container.

M4.10: With FD 3 Return architecture, agent stdout/stderr now contain:
  - Tool output (npm install, git fetch, etc.)
  - Debug prints and progress messages
  - Error messages and stack traces

Result JSON is returned via FD 3 and does not appear in logs.

Examples:
  # View agent logs
  holt logs coder-agent

  # Follow logs in real-time
  holt logs -f coder-agent

  # Show last 100 lines
  holt logs --tail=100 coder-agent

  # Show logs from last hour
  holt logs --since=1h orchestrator

  # Combine flags
  holt logs -f --since=30m --timestamps coder-agent`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().StringVarP(&logsInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (like tail -f)")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "Show logs since timestamp (e.g., 1h, 30m)")
	logsCmd.Flags().StringVar(&logsTail, "tail", "all", "Number of lines to show from the end of the logs")
	logsCmd.Flags().BoolVar(&logsTimestamps, "timestamps", false, "Show timestamps")

	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	roleOrOrchestrator := args[0]

	// Create Docker client first
	dockerClient, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Resolve instance
	instanceName := logsInstanceName
	if instanceName == "" {
		resolvedName, err := instance.InferInstanceFromWorkspace(ctx, dockerClient)
		if err != nil {
			return fmt.Errorf("failed to resolve instance: %w (hint: use --name flag)", err)
		}
		instanceName = resolvedName
		printer.Info("Using instance: %s", instanceName)
	}

	// Translate role to container name
	var containerName string
	if roleOrOrchestrator == "orchestrator" {
		containerName = dockerpkg.OrchestratorContainerName(instanceName)
	} else {
		// Agent role → controller/primary container
		containerName = dockerpkg.AgentContainerName(instanceName, roleOrOrchestrator)
	}

	// Check if container exists
	_, err = dockerClient.ContainerInspect(ctx, containerName)
	if err != nil {
		return fmt.Errorf("container not found: %s (hint: check 'holt list')", containerName)
	}

	// Build log options
	logOptions := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     logsFollow,
		Timestamps: logsTimestamps,
		Tail:       logsTail,
	}

	if logsSince != "" {
		logOptions.Since = logsSince
	}

	// Stream logs
	reader, err := dockerClient.ContainerLogs(ctx, containerName, logOptions)
	if err != nil {
		return fmt.Errorf("failed to retrieve logs: %w", err)
	}
	defer reader.Close()

	// Copy logs to stdout
	// Docker logs use a multiplexed stream format - we use io.Copy which handles it
	_, err = io.Copy(cmd.OutOrStdout(), reader)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error streaming logs: %w", err)
	}

	return nil
}
