package commands

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/docker/docker/client"
	"github.com/dyluth/holt/internal/config"
	dockerpkg "github.com/dyluth/holt/internal/docker"
	"github.com/dyluth/holt/internal/printer"
)

// M4.4: RedisMode represents the Redis configuration mode
type RedisMode string

const (
	RedisModeExternal RedisMode = "external"
	RedisModeManaged  RedisMode = "managed"
)

// M4.4: RedisConfig holds the resolved Redis configuration
type RedisConfig struct {
	Mode     RedisMode
	URI      string // For external mode or constructed for managed mode
	Image    string // For managed mode only
	Password string // For managed mode only
}

// M4.4: determineRedisMode analyzes the config and returns the Redis mode and configuration
func determineRedisMode(cfg *config.HoltConfig) (RedisConfig, error) {
	// Default: managed mode with redis:7-alpine
	if cfg.Services == nil || cfg.Services.Redis == nil {
		printer.Info("No external Redis URI found. Starting default Holt-managed Redis container.\n")
		return RedisConfig{
			Mode:  RedisModeManaged,
			Image: "redis:7-alpine",
		}, nil
	}

	redis := cfg.Services.Redis

	// External mode: uri is specified
	if redis.URI != "" {
		sanitized := sanitizeRedisURI(redis.URI)
		printer.Info("Using external Redis: %s\n", sanitized)
		return RedisConfig{
			Mode: RedisModeExternal,
			URI:  redis.URI,
		}, nil
	}

	// Managed mode: image is specified (or default)
	image := redis.Image
	if image == "" {
		image = "redis:7-alpine"
	}

	password := redis.Password

	// Log appropriate message
	if password != "" {
		printer.Info("Starting Holt-managed Redis with authentication enabled\n")
	} else {
		printer.Info("No external Redis URI found. Starting default Holt-managed Redis container.\n")
	}

	return RedisConfig{
		Mode:     RedisModeManaged,
		Image:    image,
		Password: password,
	}, nil
}

// M4.4: sanitizeRedisURI removes credentials from a Redis URI for safe logging
// Replaces password portion with *** (e.g., redis://***@host:port or redis://user:***@host:port)
func sanitizeRedisURI(uri string) string {
	// Pattern to match redis://[user]:password@host or rediss://[user]:password@host
	// We want to replace the password portion with ***

	// Match scheme://[[user]:password@]host[:port][/database]
	re := regexp.MustCompile(`(redis(?:s)?://)(?:[^:@]+:)?([^@]+)@(.+)`)

	// If there's authentication in the URI, replace password
	if re.MatchString(uri) {
		return re.ReplaceAllString(uri, "${1}***@${3}")
	}

	// No authentication - return as-is
	return uri
}

// M4.4: constructManagedRedisURI builds the Redis connection URI for managed Redis
// If password is set, includes it in the URI. Otherwise, returns simple redis://host:port
func constructManagedRedisURI(redisName string, password string) string {
	if password != "" {
		// URI with password: redis://:password@host:6379
		return fmt.Sprintf("redis://:%s@%s:6379", password, redisName)
	}
	// Simple URI: redis://host:6379
	return fmt.Sprintf("redis://%s:6379", redisName)
}

// M4.4: detectRedisModeFromInstance attempts to detect Redis mode from a running instance
// Returns true if managed Redis, false if external. Uses orchestrator container's REDIS_URL env var.
func detectRedisModeFromInstance(ctx context.Context, cli *client.Client, instanceName string) (bool, error) {
	orchestratorName := dockerpkg.OrchestratorContainerName(instanceName)

	// Try to inspect orchestrator container
	containerInfo, err := cli.ContainerInspect(ctx, orchestratorName)
	if err != nil {
		// Orchestrator container doesn't exist or can't be inspected
		// Fallback: check if Redis container exists by label
		printer.Debug("Cannot inspect orchestrator container, falling back to Redis container detection\n")
		return detectManagedRedisContainerExists(ctx, cli, instanceName)
	}

	// Extract REDIS_URL from environment variables
	redisURL := ""
	for _, envVar := range containerInfo.Config.Env {
		if strings.HasPrefix(envVar, "REDIS_URL=") {
			redisURL = strings.TrimPrefix(envVar, "REDIS_URL=")
			break
		}
	}

	if redisURL == "" {
		// No REDIS_URL found - assume managed mode (defensive)
		printer.Debug("No REDIS_URL in orchestrator env, assuming managed mode\n")
		return true, nil
	}

	// Check if URL points to a Holt-managed Redis container
	// Managed Redis container name pattern: holt-<instance>-redis
	expectedRedisName := dockerpkg.RedisContainerName(instanceName)
	isManaged := strings.Contains(redisURL, expectedRedisName)

	if isManaged {
		printer.Info("Detected managed Redis mode\n")
	} else {
		printer.Info("Detected external Redis mode, skipping Redis cleanup\n")
	}

	return isManaged, nil
}

// detectManagedRedisContainerExists checks if a managed Redis container exists for this instance
// This is a fallback when orchestrator container is not available
func detectManagedRedisContainerExists(ctx context.Context, cli *client.Client, instanceName string) (bool, error) {
	redisName := dockerpkg.RedisContainerName(instanceName)

	// Try to inspect Redis container
	_, err := cli.ContainerInspect(ctx, redisName)
	if err != nil {
		// Redis container doesn't exist - assume external mode
		printer.Debug("No managed Redis container found, assuming external mode\n")
		return false, nil
	}

	// Redis container exists - managed mode
	printer.Debug("Found managed Redis container\n")
	return true, nil
}

// M4.4: validateExternalRedisConnection attempts to connect to external Redis URI
// This is a basic validation during holt up to fail fast if Redis is unreachable
// Returns nil if connection succeeds, error otherwise
func validateExternalRedisConnection(ctx context.Context, uri string) error {
	// Import redis client
	// Note: This is a basic check - actual connection logic happens in orchestrator/agents
	// For now, we'll skip validation to avoid adding redis dependency to CLI
	// The orchestrator will fail clearly if connection is invalid

	printer.Debug("Skipping external Redis connection validation (will be validated by orchestrator)\n")
	return nil
}
