//go:build integration
// +build integration

package commands

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestE2E_Logs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== E2E: Holt Logs Command ===")

	projectRoot := testutil.GetProjectRoot()

	// Step 0: Build example-agent Docker image to ensure we have a valid agent
	t.Log("Building example-agent Docker image...")
	buildCmd := exec.Command("docker", "build",
		"--no-cache",
		"-t", "holt/example-agent:test",
		"-f", "agents/example-agent/Dockerfile",
		".")
	buildCmd.Dir = projectRoot
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Logf("Build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build example-agent image")

	// Step 1: Setup environment
	holtYML := `version: "1.0"
orchestrator:
agents:
  ExampleAgent:
    image: "holt/example-agent:test"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    workspace:
      mode: ro
services:
  redis:
    image: redis:7-alpine
`

	env := testutil.SetupE2EEnvironment(t, holtYML)
	defer func() {
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		_ = runDown(downCmd, []string{})
	}()

	// Step 2: Start instance
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err = runUp(upCmd, []string{})
	require.NoError(t, err, "Failed to start instance")

	// Wait for containers
	time.Sleep(5 * time.Second)

	// Step 3: Test logs for orchestrator
	t.Run("LogsOrchestrator", func(t *testing.T) {
		cmd := &cobra.Command{}
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)

		// Set global flag explicitly for test
		logsInstanceName = env.InstanceName
		logsFollow = false
		logsTail = "all"

		// Run logs command
		err := runLogs(cmd, []string{"orchestrator"})
		if err != nil {
			t.Logf("Logs output: %s", buf.String())
		}
		require.NoError(t, err, "holt logs orchestrator failed")
		require.NotEmpty(t, buf.String(), "orchestrator logs should not be empty")
	})

	// Step 4: Test logs for agent
	t.Run("LogsAgent", func(t *testing.T) {
		cmd := &cobra.Command{}
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)

		logsInstanceName = env.InstanceName
		logsFollow = false
		logsTail = "all"

		err := runLogs(cmd, []string{"ExampleAgent"})
		if err != nil {
			t.Logf("Logs output: %s", buf.String())
		}
		require.NoError(t, err, "holt logs ExampleAgent failed")

		// Example agent should print something
		require.NotEmpty(t, buf.String(), "agent logs should not be empty")
	})

	// Step 5: Test logs with no instance name (inference)
	// This requires us to be inside the temporary directory which SetupE2EEnvironment changes to?
	// Actually SetupE2EEnvironment returns a TmpDir but doesn't chdir the test process permanently if parallel.
	// But since we are not parallel we can try setting Cwd or just relying on logsInstanceName=""
	// Note: InferInstanceFromWorkspace check limits.

	t.Run("LogsInference", func(t *testing.T) {
		// Reset global flag to empty to trigger inference
		logsInstanceName = ""

		cmd := &cobra.Command{}
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)

		err := runLogs(cmd, []string{"orchestrator"})
		// This might fail if inference fails, which is what we want to test if that's the bug
		if err != nil {
			t.Logf("Logs output: %s", buf.String())
		}
		require.NoError(t, err, "holt logs orchestrator (inferred) failed")
	})
}
