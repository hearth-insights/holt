package commands

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute_UnknownCommand(t *testing.T) {
	// Redirect stderr to capture output
	buf := new(bytes.Buffer)
	rootCmd.SetErr(buf)
	rootCmd.SetOut(buf)

	// Configure root command to reject arguments (simulating app behavior with subcommands)
	originalArgs := rootCmd.Args
	rootCmd.Args = cobra.NoArgs
	defer func() { rootCmd.Args = originalArgs }()

	// Set args to an unknown command
	rootCmd.SetArgs([]string{"banana"})

	// Execute should return an error
	err := Execute()
	require.Error(t, err) // Use require to stop if nil
	assert.Contains(t, err.Error(), "unknown command \"banana\"")

	// Output should contain the error message and usage
	output := buf.String()
	assert.Contains(t, output, "Error: unknown command \"banana\"")
	assert.Contains(t, output, "Usage:")
}

func TestExecute_UnknownFlag(t *testing.T) {
	// Redirect stderr to capture output
	buf := new(bytes.Buffer)
	rootCmd.SetErr(buf)
	rootCmd.SetOut(buf)

	// Set args to an unknown flag
	rootCmd.SetArgs([]string{"hoard", "--banana"})

	// Execute should return an error
	err := Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --banana")

	// Output should contain the error message and usage
	output := buf.String()
	assert.Contains(t, output, "Error: unknown flag: --banana")
	assert.Contains(t, output, "Usage:")
}
