package testutil

import (
	"os"
	"path/filepath"
)

// GetProjectRoot returns the project root directory for building Docker images
// It walks up the directory tree until it finds go.mod
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
