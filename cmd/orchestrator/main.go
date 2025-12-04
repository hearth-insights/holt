package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/docker/docker/client"
	"github.com/hearth-insights/holt/internal/config"
	"github.com/hearth-insights/holt/internal/orchestrator"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/hearth-insights/holt/pkg/version"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(context.Background(), os.Args, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// run is the testable entry point for the orchestrator
func run(ctx context.Context, args []string, getEnv func(string) string) error {
	// Check for version flag
	// We use a custom flag set to avoid polluting the global flag set
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "Show version information")
	
	// Parse flags (skipping program name)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if *showVersion {
		fmt.Printf("holt-orchestrator version %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	// 1. Load environment variables
	instanceName := getEnv("HOLT_INSTANCE_NAME")
	redisURL := getEnv("REDIS_URL")

	if instanceName == "" || redisURL == "" {
		return fmt.Errorf("Error: HOLT_INSTANCE_NAME and REDIS_URL must be set")
	}

	// 2. Parse Redis URL
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("Error: Invalid REDIS_URL: %v", err)
	}

	// 3. Create blackboard client
	bbClient, err := blackboard.NewClient(redisOpts, instanceName)
	if err != nil {
		return fmt.Errorf("Error: Failed to create blackboard client: %v", err)
	}
	defer bbClient.Close()

	// 4. Verify Redis connectivity
	if err := bbClient.Ping(ctx); err != nil {
		return fmt.Errorf("Error: Redis not accessible: %v", err)
	}

	// 5. Load holt.yml configuration from workspace
	cfg, err := config.Load("/workspace/holt.yml")
	if err != nil {
		return fmt.Errorf("Error: Failed to load holt.yml: %v", err)
	}

	fmt.Printf("Orchestrator starting for instance '%s' with %d agents\n", instanceName, len(cfg.Agents))

	// 6. Initialize Docker client for worker management (M3.4)
	// The Docker socket is mounted at /var/run/docker.sock by the CLI

	// M3.4: Diagnostic check for Docker socket permissions
	if stat, err := os.Stat("/var/run/docker.sock"); err == nil {
		fmt.Printf("Docker socket found: mode=%v\n", stat.Mode())
		// Print user/group info to help diagnose permission issues
		if sysstat, ok := stat.Sys().(*syscall.Stat_t); ok {
			fmt.Printf("Docker socket ownership: uid=%d, gid=%d\n", sysstat.Uid, sysstat.Gid)

			// Get process groups (returns []int and error)
			groups, err := syscall.Getgroups()
			if err != nil {
				fmt.Printf("Current process: uid=%d, gid=%d, groups=<error: %v>\n",
					syscall.Getuid(), syscall.Getgid(), err)
			} else {
				fmt.Printf("Current process: uid=%d, gid=%d, groups=%v\n",
					syscall.Getuid(), syscall.Getgid(), groups)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "Docker socket not found at /var/run/docker.sock: %v\n", err)
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create Docker client (worker management disabled): %v\n", err)
		// Continue without worker management - controllers will not be able to launch workers
		dockerClient = nil
	} else {
		// Verify Docker connectivity
		if _, err := dockerClient.Ping(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Docker not accessible (worker management disabled): %v\n", err)
			dockerClient.Close()
			dockerClient = nil
		} else {
			fmt.Println("✓ Docker client initialized for worker management")
		}
	}

	// 7. Create worker manager if Docker is available
	var workerManager *orchestrator.WorkerManager = nil
	if dockerClient != nil {
		// M3.4: Get host workspace path from environment (for worker bind mounts)
		// The orchestrator container has the workspace mounted at /workspace internally,
		// but workers need to mount from the actual host path
		hostWorkspacePath := getEnv("HOST_WORKSPACE_PATH")
		if hostWorkspacePath == "" {
			// Fallback: try to use /workspace if not set (for backward compatibility)
			// This may fail if running in a container environment
			hostWorkspacePath = "/workspace"
			fmt.Println("Warning: HOST_WORKSPACE_PATH not set, using /workspace (may fail in containerized environment)")
		}
		workerManager = orchestrator.NewWorkerManager(dockerClient, instanceName, hostWorkspacePath)
		fmt.Println("Worker manager initialized for controller-worker pattern")
	}

	// 8. Create orchestrator engine with config
	engine := orchestrator.NewEngine(bbClient, instanceName, cfg, workerManager)

	// 9. Setup graceful shutdown
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// 10. Start orchestrator in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(runCtx)
	}()

	// 11. Wait for shutdown signal or error
	select {
	case sig := <-sigCh:
		fmt.Printf("Received signal %v, shutting down gracefully...\n", sig)
		cancel()
		// Wait for engine to finish
		return <-errCh
	case runErr := <-errCh:
		if runErr != nil {
			return fmt.Errorf("Orchestrator error: %v", runErr)
		}
	case <-ctx.Done():
		// Context cancelled externally (e.g. in tests)
		cancel()
		return <-errCh
	}

	// 12. Cleanup Docker client if initialized
	if dockerClient != nil {
		dockerClient.Close()
	}

	fmt.Println("Orchestrator stopped")
	return nil
}
