package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun(t *testing.T) {
	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Test with --help to ensure it runs without error
	os.Args = []string{"holt", "--help"}
	
	err := run()
	assert.NoError(t, err)
}
