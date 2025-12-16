package testutil

import (
	"os"
	"path/filepath"
	"runtime"
)

// GetProjectRoot returns the project root directory for building Docker images
// It uses runtime.Caller to find the location of this file and walks up to go.mod
func GetProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		// Fallback to Getwd if runtime.Caller fails (unlikely)
		wd, _ := os.Getwd()
		return wd
	}

	dir := filepath.Dir(filename)

	// Walk up until we find go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return "."
		}
		dir = parent
	}
}
