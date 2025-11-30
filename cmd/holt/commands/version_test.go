package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetVersionInfo(t *testing.T) {
	// Setup
	v := "1.2.3"
	c := "abcdef"
	d := "2025-01-01"

	// Execute
	SetVersionInfo(v, c, d)

	// Verify
	assert.Equal(t, v, version)
	assert.Equal(t, c, commit)
	assert.Equal(t, d, date)
	assert.Equal(t, "1.2.3 (commit: abcdef, built: 2025-01-01)", rootCmd.Version)
}
