//go:build integration
// +build integration

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// ArtefactResult is a simplified artefact for test assertions
type ArtefactResult struct {
	ID      string
	Type    string
	Payload string
}

// E2EEnvironment represents an isolated E2E test environment
type E2EEnvironment struct {
	T            *testing.T
	TmpDir       string // Container path for file operations
	TmpDirHost   string // Host path for Docker bind mounts (DinD only)
	OriginalDir  string
	InstanceName string
	DockerClient *client.Client
	BBClient     *blackboard.Client
	RedisPort    int
	Ctx          context.Context
}

// detectHostPathForApp tries to detect the host filesystem path that maps to /app
// in the current container (for Docker-in-Docker scenarios)
func detectHostPathForApp() string {
	// Try to get our own container ID
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}

	// Try to inspect our own container using Docker
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return ""
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(context.Background(), hostname)
	if err != nil {
		return ""
	}

	// Look for /app mount
	for _, mount := range inspect.Mounts {
		if mount.Destination == "/app" {
			return mount.Source
		}
	}

	return ""
}

// SetupE2EEnvironment creates a fully isolated E2E test environment
// with temp directory, Git repo, holt.yml, and unique instance name
func SetupE2EEnvironment(t *testing.T, holtYML string) *E2EEnvironment {
	ctx := context.Background()

	// Create isolated temporary directory in a location accessible to Docker host
	// When running in Docker-in-Docker (e.g., CI or Claude Code), we need to use
	// a directory that's bind-mounted from the host, not in an overlay filesystem.

	// Check if we're running in Docker (Docker-in-Docker scenario)
	_, inDocker := os.LookupEnv("DOCKER_HOST")
	if !inDocker {
		// Also check for .dockerenv file
		if _, err := os.Stat("/.dockerenv"); err == nil {
			inDocker = true
		}
	}

	var tmpDir string
	var err error

	var tmpDirHost string // Host path for Docker bind mounts

	if inDocker {
		// In DinD, use /app if available (likely mounted from host)
		testWorkspacesDir := filepath.Join("/app", ".test-workspaces")
		if err := os.MkdirAll(testWorkspacesDir, 0755); err == nil {
			// Create temp directory using container path (/app/.test-workspaces)
			tmpDir, err = os.MkdirTemp(testWorkspacesDir, fmt.Sprintf("test-e2e-%s-*", time.Now().Format("20060102-150405")))
			if err == nil && tmpDir != "" {
				// Detect host path for Docker bind mounts
				hostPath := detectHostPathForApp()
				if hostPath != "" {
					// Translate container path to host path: /app/... -> /Users/cam/github/holt/...
					tmpDirHost = filepath.Join(hostPath, tmpDir[len("/app"):])
				} else {
					// If detection fails, use container path and hope for the best
					tmpDirHost = tmpDir
				}
			}
		}
	}

	if tmpDir == "" || err != nil {
		// Fall back to system temp directory
		tmpDir, err = os.MkdirTemp("", fmt.Sprintf("test-e2e-%s-*", time.Now().Format("20060102-150405")))
		tmpDirHost = tmpDir // In non-DinD, paths are the same
	}
	require.NoError(t, err, "Failed to create temp directory")

	// Resolve symlinks to get canonical path (critical for macOS where /var -> /private/var)
	canonicalTmpDir, err := filepath.EvalSymlinks(tmpDir)
	require.NoError(t, err, "Failed to resolve tmpDir symlinks")
	tmpDir = canonicalTmpDir

	// Also resolve host path if different
	if tmpDirHost != tmpDir {
		canonicalTmpDirHost, err := filepath.EvalSymlinks(tmpDirHost)
		if err == nil {
			tmpDirHost = canonicalTmpDirHost
		}
	} else {
		tmpDirHost = tmpDir
	}

	// Register cleanup
	t.Cleanup(func() {
		os.RemoveAll(tmpDir) // Clean up using container path
	})

	// Initialize Git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run(), "Failed to initialize Git repository")

	// Configure Git
	exec.Command("git", "-C", tmpDir, "config", "user.email", "test@holt.local").Run()
	exec.Command("git", "-C", tmpDir, "config", "user.name", "Holt Test").Run()

	// Create initial commit (required for clean workspace check)
	testFile := filepath.Join(tmpDir, "README.md")
	require.NoError(t, os.WriteFile(testFile, []byte("# Test Project\n"), 0644))
	exec.Command("git", "-C", tmpDir, "add", ".").Run()
	exec.Command("git", "-C", tmpDir, "commit", "-m", "Initial commit").Run()

	// Write holt.yml
	holtYMLPath := filepath.Join(tmpDir, "holt.yml")
	require.NoError(t, os.WriteFile(holtYMLPath, []byte(holtYML), 0644), "Failed to write holt.yml")

	// Commit holt.yml so workspace is clean
	exec.Command("git", "-C", tmpDir, "add", "holt.yml").Run()
	exec.Command("git", "-C", tmpDir, "commit", "-m", "Add holt.yml").Run()

	// Create a simple Makefile for test-runner agent tests
	// This allows test-runner to successfully run `make test-failed`
	makefileContent := `# Test Makefile for E2E tests

.PHONY: test-failed
test-failed:
	@echo "Running tests..."
	@echo "All tests passed!"
	@exit 0
`
	makefilePath := filepath.Join(tmpDir, "Makefile")
	require.NoError(t, os.WriteFile(makefilePath, []byte(makefileContent), 0644), "Failed to write Makefile")
	require.NoError(t, exec.Command("git", "-C", tmpDir, "add", "Makefile").Run(), "Failed to git add Makefile")
	require.NoError(t, exec.Command("git", "-C", tmpDir, "commit", "-m", "Add Makefile for tests").Run(), "Failed to git commit Makefile")

	// Fix permissions for Docker container access (critical for CI environments)
	// Containers may run as different users, so we need world-readable/writable files
	// a+rwX means: add read+write for all users, and execute for directories
	chmodCmd := exec.Command("chmod", "-R", "a+rwX", tmpDir)
	if output, err := chmodCmd.CombinedOutput(); err != nil {
		t.Logf("Warning: chmod failed: %v\nOutput: %s", err, string(output))
	}

	// Change to test directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir), "Failed to change to test directory")

	// Generate unique instance name with microseconds for uniqueness
	instanceName := fmt.Sprintf("test-e2e-%s", time.Now().Format("20060102-150405-000000"))

	// Get Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Failed to create Docker client")

	env := &E2EEnvironment{
		T:            t,
		TmpDir:       tmpDir,
		TmpDirHost:   tmpDirHost,
		OriginalDir:  originalDir,
		InstanceName: instanceName,
		DockerClient: cli,
		Ctx:          ctx,
	}

	// Register cleanup
	t.Cleanup(func() {
		if env.BBClient != nil {
			env.BBClient.Close()
		}
		if env.DockerClient != nil {
			env.DockerClient.Close()
		}
		os.Chdir(originalDir)
	})

	return env
}

// CreateTestManifest creates a minimal SystemManifest for E2E tests.
// This allows test artefacts to be properly anchored and pass M4.7 validation.
// Returns the manifest artefact ID (hash) for use as parent in test artefacts.
func (env *E2EEnvironment) CreateTestManifest(ctx context.Context) string {
	manifestID := blackboard.NewID()

	// Create minimal system identity
	identity := &blackboard.SystemIdentity{
		Strategy:     "local",
		ConfigHash:   "test-config-hash",
		GitCommit:    "test-git-hash",
		ComputedAtMs: time.Now().UnixMilli(),
	}

	identityJSON, err := json.Marshal(identity)
	require.NoError(env.T, err, "Failed to marshal test identity")

	manifest := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{}, // Manifests have no parents
		LogicalThreadID: manifestID, // Manifests use their ID as thread
		Version:         1,
		StructuralType:  blackboard.StructuralTypeSystemManifest,
		Type:            "SystemConfig",
		ProducedByRole:  "orchestrator",
		CreatedAtMs:     time.Now().UnixMilli(),
	}, string(identityJSON))

	env.T.Logf("✓ Test SystemManifest created: %s", manifest.ID[:16]+"...")
	return manifest.ID
}

// CreateWorkflowSpine creates a minimal workflow spine (Manifest → Goal) for E2E tests.
// This provides a proper M4.7-compliant starting point for test workflows.
// Returns (manifestID, goalID) for use as anchors in test artefacts.
func (env *E2EEnvironment) CreateWorkflowSpine(ctx context.Context, goalPayload string) (string, string) {
	manifestID := env.CreateTestManifest(ctx)

	goalThreadID := blackboard.NewID()
	goalDefined := env.CreateVerifiableArtefact(ctx, blackboard.ArtefactHeader{
		ParentHashes:    []string{manifestID},
		LogicalThreadID: goalThreadID,
		Version:         1,
		Type:            "GoalDefined",
	}, goalPayload)

	env.T.Logf("✓ Workflow spine created: Manifest → GoalDefined")
	return manifestID, goalDefined.ID
}

// CreateVerifiableArtefact helper creates a V2-compliant artefact (hashed ID) and writes it to the blackboard.
// It handles the hashing, V1 conversion, and error checking to reduce boilerplate in E2E tests.
func (env *E2EEnvironment) CreateVerifiableArtefact(ctx context.Context, header blackboard.ArtefactHeader, payload string) *blackboard.Artefact {
	// Apply sensible defaults for test artefacts
	if header.StructuralType == "" {
		header.StructuralType = blackboard.StructuralTypeStandard
	}
	if header.ProducedByRole == "" {
		header.ProducedByRole = "user"
	}
	if header.CreatedAtMs == 0 {
		header.CreatedAtMs = time.Now().UnixMilli()
	}
	// ClaimID defaults to "" (correct for test artefacts created outside claims)

	// Construct V2 artefact
	artefact := &blackboard.Artefact{
		Header: header,
		Payload: blackboard.ArtefactPayload{
			Content: payload,
		},
	}

	// Compute hash
	hash, err := blackboard.ComputeArtefactHash(artefact)
	require.NoError(env.T, err, "Failed to compute artefact hash")
	artefact.ID = hash

	// Create in Redis
	err = env.BBClient.CreateArtefact(ctx, artefact)
	require.NoError(env.T, err, "Failed to create verifiable artefact")

	return artefact
}

// InitializeBlackboardClient connects to the blackboard for this environment
// and waits for Redis to be ready
func (env *E2EEnvironment) InitializeBlackboardClient() {
	var err error
	env.RedisPort, err = instance.GetInstanceRedisPort(env.Ctx, env.DockerClient, env.InstanceName)
	require.NoError(env.T, err, "Failed to get Redis port")

	// In Docker-in-Docker scenarios, use host.docker.internal instead of localhost
	// because port mappings don't work between sibling containers
	redisHost := "localhost"
	if _, err := os.Stat("/.dockerenv"); err == nil {
		// We're in Docker, use host.docker.internal to reach the host's published ports
		redisHost = "host.docker.internal"
	}

	redisOpts := &redis.Options{
		Addr: fmt.Sprintf("%s:%d", redisHost, env.RedisPort),
	}

	env.BBClient, err = blackboard.NewClient(redisOpts, env.InstanceName)
	require.NoError(env.T, err, "Failed to create blackboard client")

	// Wait for Redis to be ready (up to 10 seconds)
	env.T.Logf("Waiting for Redis to be ready on %s:%d...", redisHost, env.RedisPort)
	for i := 0; i < 10; i++ {
		if err := env.BBClient.Ping(env.Ctx); err == nil {
			env.T.Logf("✓ Redis is ready")
			return
		}
		time.Sleep(1 * time.Second)
	}
	require.Fail(env.T, "Redis did not become ready within 10 seconds")
}

// WaitForContainer waits for a container to be running (up to 30 seconds)
// containerNameSuffix: "orchestrator", "redis", or "agent-{agent-name}"
func (env *E2EEnvironment) WaitForContainer(containerNameSuffix string) {
	// Container naming patterns (M3.7):
	// - orchestrator/redis: holt-{component}-{instance-name}
	// - agents: holt-{instance-name}-{role} (role IS agent key from holt.yml)
	var fullName string
	if containerNameSuffix == "orchestrator" || containerNameSuffix == "redis" {
		fullName = fmt.Sprintf("holt-%s-%s", containerNameSuffix, env.InstanceName)
	} else {
		// Agent pattern: containerNameSuffix is "agent-{agent-name}"
		// Result: holt-{instance-name}-{role} (M3.7: dropped "agent" prefix)
		agentName := containerNameSuffix[6:] // Remove "agent-" prefix to get role
		fullName = fmt.Sprintf("holt-%s-%s", env.InstanceName, agentName)
	}

	var lastState string
	var lastStatus string
	for i := 0; i < 30; i++ {
		containers, err := env.DockerClient.ContainerList(env.Ctx, container.ListOptions{All: true})
		if err == nil {
			for _, c := range containers {
				for _, name := range c.Names {
					if name == "/"+fullName {
						lastState = c.State
						lastStatus = c.Status
						if c.State == "running" {
							env.T.Logf("✓ Container %s is running", fullName)
							return
						}
					}
				}
			}
		}
		time.Sleep(1 * time.Second)
	}

	// Container never became running - show diagnostic info with logs
	if lastState != "" {
		// Try to get container logs for debugging
		logs, logErr := env.DockerClient.ContainerLogs(env.Ctx, fullName, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       "50",
		})
		var logOutput string
		if logErr == nil {
			defer logs.Close()
			logBytes, _ := io.ReadAll(logs)
			logOutput = string(logBytes)
		}

		failMsg := fmt.Sprintf("Container %s did not start within 30 seconds (last state: %s, status: %s)", fullName, lastState, lastStatus)
		if logOutput != "" {
			failMsg += fmt.Sprintf("\n\nContainer logs:\n%s", logOutput)
		}
		require.Fail(env.T, failMsg)
	} else {
		require.Fail(env.T, fmt.Sprintf("Container %s not found", fullName))
	}
}

// WaitForArtefactByType polls blackboard for an artefact of specific type (up to 60 seconds)
func (env *E2EEnvironment) WaitForArtefactByType(artefactType string) *blackboard.Artefact {
	require.NotNil(env.T, env.BBClient, "Blackboard client not initialized - call InitializeBlackboardClient first")

	env.T.Logf("Waiting for artefact of type '%s'...", artefactType)

	var allArtefacts []string // Track all artefacts for debugging

	for i := 0; i < 60; i++ {
		// Scan for artefacts using Redis SCAN
		pattern := fmt.Sprintf("holt:%s:artefact:*", env.InstanceName)
		iter := env.BBClient.RedisClient().Scan(env.Ctx, 0, pattern, 0).Iterator()

		allArtefacts = allArtefacts[:0] // Reset for this iteration

		for iter.Next(env.Ctx) {
			key := iter.Val()

			// Get artefact data
			data, err := env.BBClient.RedisClient().HGetAll(env.Ctx, key).Result()
			if err != nil {
				continue
			}

			// Track this artefact
			if data["type"] != "" {
				allArtefacts = append(allArtefacts, fmt.Sprintf("%s (id=%s, claim_id=%s)", data["type"], data["id"][:8], data["claim_id"]))
			}

			// Check if type matches
			if data["type"] == artefactType {
				// Parse artefact
				artefact := &blackboard.Artefact{
					ID: data["id"],
					Header: blackboard.ArtefactHeader{
						LogicalThreadID: data["logical_id"],
						StructuralType:  blackboard.StructuralType(data["structural_type"]),
						Type:            data["type"],
						ProducedByRole:  data["produced_by_role"],
						ClaimID:         data["claim_id"], // M4.6: Capture ClaimID
						Metadata:        data["metadata"], // M5.1: Capture Metadata
						ParentHashes:    []string{},       // Simplified for now
					},
					Payload: blackboard.ArtefactPayload{
						Content: data["payload"],
					},
				}

				if versionStr, ok := data["version"]; ok {
					if version, err := strconv.Atoi(versionStr); err == nil {
						artefact.Header.Version = version
					}
				}

				// Handle parent hashes if present
				if parentHashesJSON, ok := data["parent_hashes"]; ok && parentHashesJSON != "" {
					var parents []string
					if err := json.Unmarshal([]byte(parentHashesJSON), &parents); err == nil {
						artefact.Header.ParentHashes = parents
					}
				}

				// Parse context_for_roles if present
				if rolesJSON, ok := data["context_for_roles"]; ok && rolesJSON != "" {
					var roles []string
					json.Unmarshal([]byte(rolesJSON), &roles)
					artefact.Header.ContextForRoles = roles
				}

				env.T.Logf("✓ Found artefact: type=%s, id=%s, claim_id=%s, payload=%s", artefact.Header.Type, artefact.ID, artefact.Header.ClaimID, artefact.Payload.Content)
				return artefact
			} else {
				env.T.Logf("✗ other artefact: type=%s, id=%s, claim_id=%s, v=%s payload=%s",
					data["type"],
					data["id"],
					data["claim_id"],
					data["version"],
					data["payload"])
			}
		}

		time.Sleep(1 * time.Second)
	}

	// Timeout - show what artefacts WERE found
	failMsg := fmt.Sprintf("Artefact of type '%s' not found within 60 seconds", artefactType)
	if len(allArtefacts) > 0 {
		failMsg += fmt.Sprintf("\n\nArtefacts found: %s", strings.Join(allArtefacts, ", "))
	} else {
		failMsg += "\n\nNo artefacts found on blackboard"
	}

	// If we found a ToolExecutionFailure, try to extract and display its payload
	pattern := fmt.Sprintf("holt:%s:artefact:*", env.InstanceName)
	iter := env.BBClient.RedisClient().Scan(env.Ctx, 0, pattern, 0).Iterator()
	for iter.Next(env.Ctx) {
		key := iter.Val()
		data, err := env.BBClient.RedisClient().HGetAll(env.Ctx, key).Result()
		if err != nil {
			continue
		}

		if data["type"] == "ToolExecutionFailure" {
			failMsg += fmt.Sprintf("\n\nToolExecutionFailure payload:\n%s", data["payload"])
			break
		}
	}

	// Try to get container logs for debugging
	env.DumpInstanceLogs()

	require.Fail(env.T, failMsg)
	return nil
}

// VerifyGitCommitExists checks that a commit hash exists in the workspace
func (env *E2EEnvironment) VerifyGitCommitExists(commitHash string) {
	cmd := exec.Command("git", "cat-file", "-e", commitHash)
	cmd.Dir = env.TmpDir
	err := cmd.Run()
	require.NoError(env.T, err, "Git commit %s does not exist", commitHash)
	env.T.Logf("✓ Git commit %s exists", commitHash)
}

// VerifyFileExists checks that a file exists in the workspace
func (env *E2EEnvironment) VerifyFileExists(filename string) {
	filePath := filepath.Join(env.TmpDir, filename)
	_, err := os.Stat(filePath)
	require.NoError(env.T, err, "File %s does not exist", filename)
	env.T.Logf("✓ File %s exists", filename)
}

// VerifyFileContent checks file content matches expected
func (env *E2EEnvironment) VerifyFileContent(filename string, expectedContent string) {
	filePath := filepath.Join(env.TmpDir, filename)
	content, err := os.ReadFile(filePath)
	require.NoError(env.T, err, "Failed to read file %s", filename)
	require.Contains(env.T, string(content), expectedContent, "File content mismatch")
	env.T.Logf("✓ File %s contains expected content", filename)
}

// VerifyWorkspaceClean checks that Git workspace has no uncommitted changes
func (env *E2EEnvironment) VerifyWorkspaceClean() {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = env.TmpDir
	output, err := cmd.Output()
	require.NoError(env.T, err, "Failed to run git status")
	require.Empty(env.T, string(output), "Workspace has uncommitted changes")
	env.T.Logf("✓ Workspace is clean")
}

// CreateDirtyWorkspace creates an uncommitted file to make workspace dirty
func (env *E2EEnvironment) CreateDirtyWorkspace() {
	dirtyFile := filepath.Join(env.TmpDir, "uncommitted.txt")
	require.NoError(env.T, os.WriteFile(dirtyFile, []byte("dirty"), 0644))
	env.T.Logf("✓ Created dirty file: uncommitted.txt")
}

// DefaultHoltYML returns a minimal holt.yml with no agents
func DefaultHoltYML() string {
	return `version: "1.0"
agents: []
services:
  redis:
    image: redis:7-alpine
`
}

// GitAgentHoltYML returns a holt.yml with example-git-agent configured
func GitAgentHoltYML() string {
	return `version: "1.0"
agents:
  GitAgent:
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    bid_script: ["/app/bid.sh"]
    workspace:
      mode: rw
services:
  redis:
    image: redis:7-alpine
`
}

// EchoAgentHoltYML returns a holt.yml with example-agent (echo) configured
func EchoAgentHoltYML() string {
	return `version: "1.0"
agents:
  EchoAgent:
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: ro
services:
  redis:
    image: redis:7-alpine
`
}

// ThreePhaseHoltYML returns a holt.yml with review, parallel, and exclusive agents (M3.2)
func ThreePhaseHoltYML() string {
	return `version: "1.0"
agents:
  Reviewer:
    image: "example-reviewer-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "review"
    workspace:
      mode: ro
  ParallelWorker:
    image: "example-parallel-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "claim"
    workspace:
      mode: ro
  Coder:
    image: "example-git-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: rw
services:
  redis:
    image: redis:7-alpine
`
}

// CreateTestAgent creates a custom test agent with provided run.sh script
func (env *E2EEnvironment) CreateTestAgent(agentName, runScript string) {
	agentDir := filepath.Join(env.TmpDir, ".test-agents", agentName)
	require.NoError(env.T, os.MkdirAll(agentDir, 0755))

	// Write run.sh
	runScriptPath := filepath.Join(agentDir, "run.sh")
	require.NoError(env.T, os.WriteFile(runScriptPath, []byte(runScript), 0755))

	env.T.Logf("✓ Created test agent: %s", agentName)
}

// GetProjectRoot returns the project root directory for building Docker images
func GetProjectRoot() string {
	// When running tests, we need to go up from internal/testutil to project root
	// This works because tests compile to a binary in the cmd/holt/commands directory
	root, err := os.Getwd()
	if err != nil {
		return "."
	}

	// Walk up until we find go.mod
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			// Reached filesystem root, default to current dir
			return "."
		}
		root = parent
	}
}

// WaitForArtefactOfType waits for an artefact with the specified type to appear on the blackboard.
//
// WARNING: This function returns the FIRST artefact found with matching type, regardless of version.
// If multiple artefacts with the same type exist (e.g., from rework cycles or duplicate agent runs),
// this may not return the one you expect!
//
// For Knowledge artefacts or situations where multiple versions may exist, use:
//   - WaitForArtefactWithContext() to find an artefact based on its relationship to another artefact
//   - WaitForArtefactVersion() to find a specific version of a logical artefact
//   - FindAllArtefactsOfType() to get all matching artefacts and select the right one
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: Blackboard client
//   - artefactType: The 'type' field to match (e.g., "DesignSpec", "CodeCommit")
//   - timeout: Maximum time to wait
//
// Returns:
//   - The first artefact found with matching type
//   - Error if no artefact found within timeout
func WaitForArtefactOfType(ctx context.Context, client *blackboard.Client, artefactType string, timeout time.Duration) (*blackboard.Artefact, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Scan for artefacts
		pattern := fmt.Sprintf("holt:*:artefact:*")
		iter := client.GetRedisClient().Scan(ctx, 0, pattern, 0).Iterator()

		for iter.Next(ctx) {
			key := iter.Val()
			data, err := client.GetRedisClient().HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}

			if data["type"] == artefactType {
				artefact := &blackboard.Artefact{
					ID: data["id"],
					Header: blackboard.ArtefactHeader{
						LogicalThreadID: data["logical_id"],
						StructuralType:  blackboard.StructuralType(data["structural_type"]),
						Type:            data["type"],
						ProducedByRole:  data["produced_by_role"],
					},
					Payload: blackboard.ArtefactPayload{
						Content: data["payload"],
					},
				}

				if versionStr, ok := data["version"]; ok {
					if version, err := strconv.Atoi(versionStr); err == nil {
						artefact.Header.Version = version
					}
				}

				// Parse context_for_roles if present
				if rolesJSON, ok := data["context_for_roles"]; ok && rolesJSON != "" {
					var roles []string
					json.Unmarshal([]byte(rolesJSON), &roles)
					artefact.Header.ContextForRoles = roles
				}

				return artefact, nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return nil, fmt.Errorf("artefact of type '%s' not found within %v", artefactType, timeout)
}

// WaitForArtefactVersion polls for a specific version of a logical artefact (helper for M4.3 tests)
func WaitForArtefactVersion(ctx context.Context, client *blackboard.Client, logicalID string, targetVersion int, timeout time.Duration) (*blackboard.Artefact, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Scan for artefacts
		pattern := fmt.Sprintf("holt:*:artefact:*")
		iter := client.GetRedisClient().Scan(ctx, 0, pattern, 0).Iterator()

		for iter.Next(ctx) {
			key := iter.Val()
			data, err := client.GetRedisClient().HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}

			if data["logical_id"] == logicalID {
				versionStr := data["version"]
				if version, err := strconv.Atoi(versionStr); err == nil && version == targetVersion {
					artefact := &blackboard.Artefact{
						ID: data["id"],
						Header: blackboard.ArtefactHeader{
							LogicalThreadID: data["logical_id"],
							Version:         version,
							StructuralType:  blackboard.StructuralType(data["structural_type"]),
							Type:            data["type"],
							ProducedByRole:  data["produced_by_role"],
						},
						Payload: blackboard.ArtefactPayload{
							Content: data["payload"],
						},
					}

					return artefact, nil
				}
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return nil, fmt.Errorf("artefact with logical_id='%s' version=%d not found within %v", logicalID, targetVersion, timeout)
}

// FindAllArtefactsOfType returns all artefacts of a specific type that currently exist on the blackboard.
// This is useful when you need to:
//   - Select a specific artefact based on additional criteria (e.g., version, payload content)
//   - Verify the total count of artefacts
//   - Check for duplicates or multiple versions
//
// Unlike WaitForArtefactOfType, this function:
//   - Returns immediately with whatever is currently available (no polling)
//   - Returns ALL matching artefacts, not just the first one
//   - Returns an empty slice if no matches found (no error)
func FindAllArtefactsOfType(ctx context.Context, client *blackboard.Client, artefactType string) ([]*blackboard.Artefact, error) {
	var results []*blackboard.Artefact

	pattern := fmt.Sprintf("holt:*:artefact:*")
	iter := client.GetRedisClient().Scan(ctx, 0, pattern, 0).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		data, err := client.GetRedisClient().HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}

		if data["type"] == artefactType {
			// Debug log
			fmt.Printf("DEBUG: FindAllArtefactsOfType found %s with metadata raw: '%s'\n", data["id"], data["metadata"])

			artefact := &blackboard.Artefact{
				ID: data["id"],
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: data["logical_id"],
					StructuralType:  blackboard.StructuralType(data["structural_type"]),
					Type:            data["type"],
					ProducedByRole:  data["produced_by_role"],
					Metadata:        data["metadata"], // M5.1: Capture Metadata
				},
				Payload: blackboard.ArtefactPayload{
					Content: data["payload"],
				},
			}

			if versionStr, ok := data["version"]; ok {
				if version, err := strconv.Atoi(versionStr); err == nil {
					artefact.Header.Version = version
				}
			}

			// Parse context_for_roles if present
			if rolesJSON, ok := data["context_for_roles"]; ok && rolesJSON != "" {
				var roles []string
				json.Unmarshal([]byte(rolesJSON), &roles)
				artefact.Header.ContextForRoles = roles
			}

			results = append(results, artefact)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("error scanning for artefacts: %w", err)
	}

	return results, nil
}

// WaitForArtefactWithContext waits for an artefact of a specific type that has a relationship
// to another artefact via thread_context attachment. This is essential for M4.3 Knowledge tests
// where multiple artefacts of the same type may exist, and you need to find the one that's
// contextually related to a specific work artefact.
//
// Example: Finding a DesignSpec that has a specific Knowledge checkpoint attached to it.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: Blackboard client
//   - artefactType: The type of artefact to find (e.g., "DesignSpec")
//   - relatedArtefactID: The ID of an artefact that should be in the target's thread_context
//   - instanceName: The Holt instance name (for constructing Redis keys)
//   - timeout: Maximum time to wait
//
// Returns:
//   - The artefact that has relatedArtefactID in its thread_context
//   - Error if no matching artefact found within timeout
func WaitForArtefactWithContext(ctx context.Context, client *blackboard.Client, artefactType string, relatedArtefactID string, instanceName string, timeout time.Duration) (*blackboard.Artefact, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Get all artefacts of the specified type
		artefacts, err := FindAllArtefactsOfType(ctx, client, artefactType)
		if err != nil {
			return nil, err
		}

		// Check each artefact's thread_context to see if it contains the related artefact
		for _, artefact := range artefacts {
			threadContextKey := blackboard.ThreadContextKey(instanceName, artefact.Header.LogicalThreadID)
			isMember, err := client.GetRedisClient().SIsMember(ctx, threadContextKey, relatedArtefactID).Result()
			if err != nil {
				continue
			}
			if isMember {
				return artefact, nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return nil, fmt.Errorf("artefact of type '%s' with relatedArtefactID='%s' in thread_context not found within %v", artefactType, relatedArtefactID, timeout)
}

// DumpInstanceLogs retrieves and logs all container logs for an instance.
// This is useful for debugging test failures - call it before cleanup.
func (env *E2EEnvironment) DumpInstanceLogs() {
	if env.DockerClient == nil {
		return
	}

	env.T.Log("=== Dumping container logs for debugging ===")

	// List all containers for this instance
	containers, err := env.DockerClient.ContainerList(env.Ctx, container.ListOptions{All: true})
	if err != nil {
		env.T.Logf("Failed to list containers: %v", err)
		return
	}

	for _, c := range containers {
		// Check if container belongs to this instance
		for _, name := range c.Names {
			// Instance containers follow pattern: /holt-{component}-{instance} or /holt-{instance}-{agent}
			if strings.Contains(name, env.InstanceName) {
				containerName := strings.TrimPrefix(name, "/")
				env.T.Logf("\n--- Logs for %s (state: %s) ---", containerName, c.State)

				// Get container logs
				logs, logErr := env.DockerClient.ContainerLogs(env.Ctx, containerName, container.LogsOptions{
					ShowStdout: true,
					ShowStderr: true,
					Tail:       "100", // Last 100 lines
				})
				if logErr != nil {
					env.T.Logf("Failed to get logs for %s: %v", containerName, logErr)
					continue
				}

				logBytes, _ := io.ReadAll(logs)
				logs.Close()

				if len(logBytes) > 0 {
					env.T.Logf("%s", string(logBytes))
				} else {
					env.T.Logf("(no logs)")
				}
				env.T.Logf("--- End logs for %s ---\n", containerName)
			}
		}
	}

	env.T.Log("=== End container logs ===")
}

// StringReader wraps a string to implement io.Reader for Docker build contexts
// M5.1: Helper for inline Dockerfiles in E2E tests
type StringReader struct {
	content string
	pos     int
}

// NewStringReader creates a new StringReader
func NewStringReader(s string) *StringReader {
	return &StringReader{content: s, pos: 0}
}

// Read implements io.Reader
func (sr *StringReader) Read(p []byte) (n int, err error) {
	if sr.pos >= len(sr.content) {
		return 0, io.EOF
	}
	n = copy(p, sr.content[sr.pos:])
	sr.pos += n
	return n, nil
}
