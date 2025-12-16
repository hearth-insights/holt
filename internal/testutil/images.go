//go:build integration
// +build integration

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var buildBaseOnce sync.Once

// EnsureBaseImage builds the holt-test-base-agent image once per test run.
// This image contains the pre-compiled pup binary and common dependencies.
func EnsureBaseImage(t *testing.T) {
	// We use a singleton to ensure we only build the base image once
	// regardless of how many tests call this function.
	buildBaseOnce.Do(func() {
		t.Log("Building 'holt-test-base-agent' image (this happens only once)...")
		start := time.Now()

		projectRoot := GetProjectRoot()
		// Use relative path for Dockerfile to avoid issues with absolute paths on some systems
		dockerfile := "testing/docker/Dockerfile.base"

		cmd := exec.Command("docker", "build",
			"-t", "holt-test-base-agent:latest",
			"-f", dockerfile,
			".")
		cmd.Dir = projectRoot

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Docker build output:\n%s", string(output))
		}
		require.NoError(t, err, "Failed to build holt-test-base-agent")

		t.Logf("✓ Base image built in %v", time.Since(start))
	})
}

// BuildFastTestImage builds a test agent image using the pre-built base image.
// It generates a temporary Dockerfile that inherits from holt-test-base-agent
// and copies the provided scripts.
//
// imageName: Name of the resulting image (e.g., "example-agent:latest")
// scriptPaths: Map of source path -> destination path in container (e.g., {"agents/example-agent/run.sh": "/app/run.sh"})
func BuildFastTestImage(t *testing.T, imageName string, scriptPaths map[string]string) {
	EnsureBaseImage(t)

	projectRoot := GetProjectRoot()
	tmpDir := t.TempDir()
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")

	// Generate Dockerfile
	// We switch to USER agent at the end to match standard agent behavior
	content := "FROM holt-test-base-agent:latest\n"
	content += "USER root\n" // Switch to root to copy files and chmod

	for src, dest := range scriptPaths {
		// Use filepath.Base for the COPY source because we'll pass the context properly
		// Actually, simpler to copy files to tmpDir and COPY from there, OR
		// just inline the COPY instructions assuming build context is projectRoot.
		// Let's assume build context is projectRoot.
		content += fmt.Sprintf("COPY %s %s\n", src, dest)
		content += fmt.Sprintf("RUN chmod +x %s\n", dest)
	}

	content += "USER agent\n" // Drop privileges
	content += "ENTRYPOINT [\"/app/pup\"]\n"

	err := os.WriteFile(dockerfilePath, []byte(content), 0644)
	require.NoError(t, err, "Failed to create temporary Dockerfile")

	// Build the image using the generated Dockerfile
	cmd := exec.Command("docker", "build",
		"-t", imageName,
		"-f", dockerfilePath,
		".") // Context is project root
	cmd.Dir = projectRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Docker build output:\n%s", string(output))
	}
	require.NoError(t, err, "Failed to build fast test image: %s", imageName)
}

var buildTestAgentOnce sync.Once

// EnsureTestAgentImage builds the consolidated 'holt-test-agent' image once per test run.
// This image contains scripts for both example-agent and example-git-agent.
func EnsureTestAgentImage(t *testing.T) {
	EnsureBaseImage(t)

	buildTestAgentOnce.Do(func() {
		t.Log("Building 'holt-test-agent' image (consolidated agent)...")
		start := time.Now()

		projectRoot := GetProjectRoot()

		// Use relative path for Dockerfile
		dockerfile := "testing/docker/Dockerfile.test-agent"

		cmd := exec.Command("docker", "build",
			"-t", "holt-test-agent:latest",
			"-f", dockerfile,
			".")
		cmd.Dir = projectRoot

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Docker build output:\n%s", string(output))
		}
		require.NoError(t, err, "Failed to build holt-test-agent")

		t.Logf("✓ Consolidated test agent built in %v", time.Since(start))
	})
}
