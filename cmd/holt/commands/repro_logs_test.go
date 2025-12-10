package commands

import (
	"bytes"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRepro_Logs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Log("=== Reproduction of holt logs failure ===")

	// Setup minimal environment
	holtYML := `version: "1.0"
orchestrator:
agents:
  TestAgent:
    image: "alpine:latest"
    command: ["/bin/sh", "-c", "echo 'Hello from agent'; sleep 60"]
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

	// Start instance
	upCmd := &cobra.Command{}
	upInstanceName = env.InstanceName
	upForce = false
	err := runUp(upCmd, []string{})
	require.NoError(t, err, "Failed to start instance")

	// Wait for containers
	time.Sleep(2 * time.Second)

	// Test case 1: Test logs for orchestrator
	t.Run("LogsOrchestrator", func(t *testing.T) {
		cmd := &cobra.Command{}
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)

		// Set global flag
		logsInstanceName = env.InstanceName

		// Run logs command
		err := runLogs(cmd, []string{"orchestrator"})
		if err != nil {
			t.Logf("Logs output: %s", buf.String())
		}
		require.NoError(t, err, "holt logs orchestrator failed")
		// We expect some logs, though orchestrator might be quiet if just started.
		// But valid execution is enough.
	})

	// Test case 2: Test logs for agent
	t.Run("LogsAgent", func(t *testing.T) {
		cmd := &cobra.Command{}
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)

		logsInstanceName = env.InstanceName

		err := runLogs(cmd, []string{"TestAgent"})
		if err != nil {
			t.Logf("Logs output: %s", buf.String())
		}
		require.NoError(t, err, "holt logs TestAgent failed")

		// Expect 'Hello from agent'
		require.Contains(t, buf.String(), "Hello from agent")
	})
}
