package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun_Version(t *testing.T) {
	// Capture stdout? For now just ensure it doesn't error
	err := run(context.Background(), []string{"orchestrator", "--version"}, func(key string) string {
		return ""
	})
	assert.NoError(t, err)
}

func TestRun_MissingEnv(t *testing.T) {
	err := run(context.Background(), []string{"orchestrator"}, func(key string) string {
		return ""
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HOLT_INSTANCE_NAME and REDIS_URL must be set")
}

func TestRun_InvalidRedisURL(t *testing.T) {
	err := run(context.Background(), []string{"orchestrator"}, func(key string) string {
		if key == "HOLT_INSTANCE_NAME" {
			return "test-instance"
		}
		if key == "REDIS_URL" {
			return "invalid-url"
		}
		return ""
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid REDIS_URL")
}

func TestRun_RedisConnectionFailure(t *testing.T) {
	// Point to a closed port
	err := run(context.Background(), []string{"orchestrator"}, func(key string) string {
		if key == "HOLT_INSTANCE_NAME" {
			return "test-instance"
		}
		if key == "REDIS_URL" {
			return "redis://localhost:12345"
		}
		return ""
	})
	assert.Error(t, err)
	// Error message depends on whether client creation or ping fails first,
	// but it should be one of them.
	// NewClient doesn't connect immediately, so it likely fails at Ping.
	assert.Contains(t, err.Error(), "Redis not accessible")
}
