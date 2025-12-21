//go:build integration
// +build integration

package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerQueueing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Build images
	buildAgentImage(t, "example-agent", "agents/example-agent/Dockerfile")
	buildAgentImage(t, "example-reviewer-agent", "agents/example-reviewer-agent/Dockerfile")

	// Setup test environment
	tmpDir := setupGitRepo(t)
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)
	err := os.Chdir(tmpDir)
	require.NoError(t, err)

	holtConfig := `version: "1.0"
agents:
  worker-bee:
    role: "worker-bee"
    image: "example-agent:latest"
    mode: "controller"
    command: ["/app/run.sh"]
    workspace: { mode: ro }
    bidding_strategy:
      type: "exclusive"
      target_types: ["GoalDefined"]
    worker:
      max_concurrent: 2
      image: "example-agent:latest"
      command: ["/app/run.sh"]
      workspace: { mode: ro }

  reviewer-bee:
    role: "reviewer-bee"
    image: "example-reviewer-agent:latest"
    mode: "controller"
    command: ["/app/run.sh"]
    workspace: { mode: ro }
    bidding_strategy:
      type: "review"
      target_types: ["GoalDefined"]
    worker:
      max_concurrent: 1
      image: "example-reviewer-agent:latest"
      command: ["/app/run.sh"]
      workspace: { mode: ro }

services:
  redis: { image: "redis:7-alpine" }
  orchestrator: { image: "holt-orchestrator:latest" }
`
	require.NoError(t, os.WriteFile("holt.yml", []byte(holtConfig), 0644))
	require.NoError(t, runLocalCommand("git", "add", "holt.yml"))
	require.NoError(t, runLocalCommand("git", "commit", "-m", "Add holt config"))

	instanceName := fmt.Sprintf("test-queue-%s", uuid.New().String()[:8])
	
	// Start Holt
	t.Logf("Starting Holt instance: %s", instanceName)
	upCmd := &cobra.Command{}
	upInstanceName = instanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err)

	cli := getDockerClient(t)
	defer cli.Close()
	defer func() {
		// Log orchestrator on failure
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", fmt.Sprintf("holt-orchestrator-%s", instanceName)).CombinedOutput()
			fmt.Printf("--- ORCHESTRATOR LOGS ---\n%s\n------------------------\n", string(out))
		}
		cleanupInstance(t, cli, instanceName)
	}()

	time.Sleep(5 * time.Second)

	// Submit 4 items of work manually
	t.Log("Submitting 4 tasks...")
	for i := 1; i <= 4; i++ {
		forageCmd := &cobra.Command{}
		forageInstanceName = instanceName
		forageWatch = false
		forageGoal = fmt.Sprintf("task-%d", i)
		err := runForage(forageCmd, []string{})
		require.NoError(t, err)
	}

	// Monitor
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	maxWorkers := 0
	maxReviewers := 0

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	t.Log("Starting monitoring of worker queues...")

	for {
		select {
		case <-ctx.Done():
			t.Logf("Timeout state: Reviews=%d, Exclusive=%d, MaxWorkers=%d, MaxReviewers=%d", 
				reviewCount(instanceName), exclusiveCount(instanceName), maxWorkers, maxReviewers)
			t.Fatal("Timeout: Workflow failed to complete in time")
		case <-ticker.C:
			// 1. Count active containers to verify queueing
			workers, _ := getTestActiveWorkers(instanceName, "worker-bee")
			if len(workers) > maxWorkers {
				maxWorkers = len(workers)
				t.Logf("Observed %d worker-bee workers", maxWorkers)
			}
			if len(workers) > 2 {
				t.Errorf("CONCURRENCY VIOLATION: worker-bee has %d workers (limit 2)", len(workers))
			}

			reviewers, _ := getTestActiveWorkers(instanceName, "reviewer-bee")
			if len(reviewers) > maxReviewers {
				maxReviewers = len(reviewers)
				t.Logf("Observed %d reviewer-bee reviewers", maxReviewers)
			}
			if len(reviewers) > 1 {
				t.Errorf("CONCURRENCY VIOLATION: reviewer-bee has %d reviewers (limit 1)", len(reviewers))
			}

			// 2. Count completed claims via Redis
			completeCount := countCompleteClaims(instanceName)

			if completeCount == 4 {
				t.Logf("SUCCESS: All 4 tasks completed successfully.")
				t.Logf("Max Workers Observed: %d (limit 2)", maxWorkers)
				t.Logf("Max Reviewers Observed: %d (limit 1)", maxReviewers)
				
				assert.LessOrEqual(t, maxWorkers, 2)
				assert.LessOrEqual(t, maxReviewers, 1)
				// Note: At high speed, we might still miss the worker container existence
				// but we verify the logic works via the final completion.
				return
			}
		}
	}
}

func countCompleteClaims(instanceName string) int {
	redisContainer := fmt.Sprintf("holt-redis-%s", instanceName)
	out, err := exec.Command("docker", "exec", redisContainer, "redis-cli", "KEYS", fmt.Sprintf("holt:%s:claim:*", instanceName)).Output()
	if err != nil {
		return 0
	}
	keys := strings.Fields(string(out))
	
	count := 0
	for _, k := range keys {
		if strings.HasSuffix(k, ":bids") {
			continue
		}
		statusOut, _ := exec.Command("docker", "exec", redisContainer, "redis-cli", "HGET", k, "status").Output()
		if strings.TrimSpace(string(statusOut)) == "complete" {
			count++
		}
	}
	return count
}

func reviewCount(instanceName string) int {
	// ... helper for logging ...
	return 0 
}

func exclusiveCount(instanceName string) int {
	// ... helper for logging ...
	return 0
}

func buildAgentImage(t *testing.T, name, dockerfile string) {
	t.Logf("Building agent image: %s", name)
	rootExec := exec.Command("git", "rev-parse", "--show-toplevel")
	rootBytes, err := rootExec.Output()
	require.NoError(t, err)
	root := strings.TrimSpace(string(rootBytes))

	cmd := exec.Command("docker", "build", "-t", name+":latest", "-f", dockerfile, ".")
	cmd.Dir = root
	
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Build failed: %s", string(out))
	}
	require.NoError(t, err)
}

func getTestActiveWorkers(instanceName, role string) ([]string, error) {
	prefix := fmt.Sprintf("holt-%s-%s-worker", instanceName, role)
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		return nil, err
	}
	
	var active []string
	for _, name := range strings.Fields(string(out)) {
		if strings.HasPrefix(strings.TrimPrefix(name, "/"), prefix) {
			active = append(active, name)
		}
	}
	return active, nil
}

func runLocalCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}