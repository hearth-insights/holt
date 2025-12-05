//go:build integration
// +build integration

package commands

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/hearth-insights/holt/internal/testutil"
	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_M4_10_WorkerRetention verifies keep_containers config and holt down cleanup
func TestE2E_M4_10_WorkerRetention(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()

	// Test: keep_containers=true config is parsed and instance starts
	t.Run("KeepContainersConfig", func(t *testing.T) {
		holtYML := `version: "1.0"
agents:
  TestAgent:
    role: "Test Agent"
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    worker_config:
      image: "example-agent:latest"
      command: ["/app/run.sh"]
      keep_containers: true  # M4.10: Enable retention
services:
  redis:
    image: redis:7-alpine
`
		env := testutil.SetupE2EEnvironment(t, holtYML)
		defer func() {
			downCmd := &cobra.Command{}
			downInstanceName = env.InstanceName
			runDown(downCmd, []string{})
		}()

		// Start instance (should parse keep_containers successfully)
		upCmd := &cobra.Command{}
		upInstanceName = env.InstanceName
		err := runUp(upCmd, []string{})
		require.NoError(t, err, "holt up should succeed with keep_containers=true")

		// Wait for orchestrator
		env.WaitForContainer("orchestrator")

		// Verify instance is running
		cli, err := dockerpkg.NewClient(ctx)
		require.NoError(t, err)
		defer cli.Close()

		containerFilters := filters.NewArgs()
		containerFilters.Add("label", dockerpkg.LabelInstanceName+"="+env.InstanceName)

		containers, err := cli.ContainerList(ctx, container.ListOptions{
			All:     false, // Only running
			Filters: containerFilters,
		})
		require.NoError(t, err)

		// Should have at least orchestrator and redis
		assert.GreaterOrEqual(t, len(containers), 2, "Instance should have running containers")

		// Test holt down cleanup
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		err = runDown(downCmd, []string{})
		require.NoError(t, err, "holt down should clean up all containers")

		// Verify all containers are removed (including any that might have been retained)
		allContainers, err := cli.ContainerList(ctx, container.ListOptions{
			All:     true, // Check stopped containers too
			Filters: containerFilters,
		})
		require.NoError(t, err)

		assert.Equal(t, 0, len(allContainers), "All containers should be removed after holt down")
	})
}

// TestE2E_M4_10_ConfigParsing verifies keep_containers is parsed correctly
func TestE2E_M4_10_ConfigParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Test with keep_containers: false (default)
	t.Run("DefaultKeepContainers", func(t *testing.T) {
		holtYMLDefault := `version: "1.0"
agents:
  TestAgent:
    role: "Test Agent"
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    worker_config:
      image: "example-agent:latest"
      command: ["/app/run.sh"]
      # keep_containers not specified - should default to false
services:
  redis:
    image: redis:7-alpine
`
		env := testutil.SetupE2EEnvironment(t, holtYMLDefault)
		defer func() {
			downCmd := &cobra.Command{}
			downInstanceName = env.InstanceName
			runDown(downCmd, []string{})
		}()

		upCmd := &cobra.Command{}
		upInstanceName = env.InstanceName
		err := runUp(upCmd, []string{})
		require.NoError(t, err, "holt up should succeed with default keep_containers")

		env.WaitForContainer("orchestrator")

		// Clean up
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		err = runDown(downCmd, []string{})
		require.NoError(t, err)
	})

	// Test with keep_containers: true explicitly set
	t.Run("ExplicitKeepContainersTrue", func(t *testing.T) {
		holtYMLTrue := `version: "1.0"
agents:
  TestAgent:
    role: "Test Agent"
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    worker_config:
      image: "example-agent:latest"
      command: ["/app/run.sh"]
      keep_containers: true
services:
  redis:
    image: redis:7-alpine
`
		env := testutil.SetupE2EEnvironment(t, holtYMLTrue)
		defer func() {
			downCmd := &cobra.Command{}
			downInstanceName = env.InstanceName
			runDown(downCmd, []string{})
		}()

		upCmd := &cobra.Command{}
		upInstanceName = env.InstanceName
		err := runUp(upCmd, []string{})
		require.NoError(t, err, "holt up should succeed with keep_containers=true")

		env.WaitForContainer("orchestrator")

		// Clean up
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		err = runDown(downCmd, []string{})
		require.NoError(t, err)
	})

	// Test that config with keep_containers: false works
	t.Run("ExplicitKeepContainersFalse", func(t *testing.T) {
		holtYMLFalse := `version: "1.0"
agents:
  TestAgent:
    role: "Test Agent"
    image: "example-agent:latest"
    command: ["/app/run.sh"]
    bidding_strategy:
      type: "exclusive"
    worker_config:
      image: "example-agent:latest"
      command: ["/app/run.sh"]
      keep_containers: false
services:
  redis:
    image: redis:7-alpine
`
		env := testutil.SetupE2EEnvironment(t, holtYMLFalse)
		defer func() {
			downCmd := &cobra.Command{}
			downInstanceName = env.InstanceName
			runDown(downCmd, []string{})
		}()

		upCmd := &cobra.Command{}
		upInstanceName = env.InstanceName
		err := runUp(upCmd, []string{})
		require.NoError(t, err, "holt up should succeed with keep_containers=false")

		env.WaitForContainer("orchestrator")

		// Clean up
		downCmd := &cobra.Command{}
		downInstanceName = env.InstanceName
		err = runDown(downCmd, []string{})
		require.NoError(t, err)
	})
}
