package main

import (
	"os"

	"github.com/dyluth/holt/cmd/holt/commands"
	"github.com/dyluth/holt/pkg/version"
)

func main() {
	// Set version information on root command
	commands.SetVersionInfo(version.Version, version.Commit, version.Date)

	// Execute root command
	// Errors are printed directly by the printer package with color formatting
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
