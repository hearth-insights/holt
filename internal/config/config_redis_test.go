package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M4.4: Tests for environment variable expansion
func TestExpandEnvVars_ValidVariable(t *testing.T) {
	// Set test environment variable
	os.Setenv("TEST_REDIS_PASSWORD", "secret123")
	defer os.Unsetenv("TEST_REDIS_PASSWORD")

	input := []byte(`password: "${TEST_REDIS_PASSWORD}"`)
	result, err := expandEnvVars(input)

	require.NoError(t, err)
	assert.Equal(t, `password: "secret123"`, string(result))
}

func TestExpandEnvVars_MultipleVariables(t *testing.T) {
	os.Setenv("TEST_HOST", "redis.example.com")
	os.Setenv("TEST_PORT", "6379")
	defer func() {
		os.Unsetenv("TEST_HOST")
		os.Unsetenv("TEST_PORT")
	}()

	input := []byte(`uri: "redis://${TEST_HOST}:${TEST_PORT}"`)
	result, err := expandEnvVars(input)

	require.NoError(t, err)
	assert.Equal(t, `uri: "redis://redis.example.com:6379"`, string(result))
}

func TestExpandEnvVars_MissingVariable(t *testing.T) {
	input := []byte(`password: "${MISSING_VAR}"`)
	result, err := expandEnvVars(input)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "environment variable 'MISSING_VAR' referenced in holt.yml is not set")
}

func TestExpandEnvVars_NoVariables(t *testing.T) {
	input := []byte(`password: "plain-text"`)
	result, err := expandEnvVars(input)

	require.NoError(t, err)
	assert.Equal(t, input, result)
}

func TestExpandEnvVars_EmptyValue(t *testing.T) {
	os.Setenv("EMPTY_VAR", "")
	defer os.Unsetenv("EMPTY_VAR")

	input := []byte(`password: "${EMPTY_VAR}"`)
	result, err := expandEnvVars(input)

	require.NoError(t, err)
	assert.Equal(t, `password: ""`, string(result))
}

func TestExpandEnvVars_SpecialCharacters(t *testing.T) {
	os.Setenv("SPECIAL_PASS", "p@ss w0rd!$#")
	defer os.Unsetenv("SPECIAL_PASS")

	input := []byte(`password: "${SPECIAL_PASS}"`)
	result, err := expandEnvVars(input)

	require.NoError(t, err)
	assert.Equal(t, `password: "p@ss w0rd!$#"`, string(result))
}

// M4.4: Tests for Redis configuration validation
func TestValidateRedisConfig_MutuallyExclusive(t *testing.T) {
	redis := &ServiceOverride{
		Image: "redis:7-alpine",
		URI:   "redis://external:6379",
	}

	err := validateRedisConfig(redis)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidateRedisConfig_ExternalMode_Valid(t *testing.T) {
	redis := &ServiceOverride{
		URI: "redis://host:6379",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
}

func TestValidateRedisConfig_ExternalMode_TLS(t *testing.T) {
	redis := &ServiceOverride{
		URI: "rediss://secure-host:6380",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
}

func TestValidateRedisConfig_ExternalMode_InvalidScheme(t *testing.T) {
	redis := &ServiceOverride{
		URI: "http://wrong:6379",
	}

	err := validateRedisConfig(redis)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start with 'redis://' or 'rediss://'")
}

func TestValidateRedisConfig_ExternalMode_TooLong(t *testing.T) {
	// Create a URI longer than 4KB
	longURI := "redis://host:6379/" + string(make([]byte, 5000))
	redis := &ServiceOverride{
		URI: longURI,
	}

	err := validateRedisConfig(redis)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestValidateRedisConfig_ManagedMode_WithPassword(t *testing.T) {
	redis := &ServiceOverride{
		Image:    "redis:7-alpine",
		Password: "secret123",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
}

func TestValidateRedisConfig_ManagedMode_NoPassword(t *testing.T) {
	redis := &ServiceOverride{
		Image: "redis:7-alpine",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
}

func TestValidateRedisConfig_PasswordIgnoredInExternalMode(t *testing.T) {
	// This should log a warning but not error
	redis := &ServiceOverride{
		URI:      "redis://external:6379",
		Password: "ignored",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
	// Note: Warning is logged but we can't easily test log output
}

// M4.4: Integration test - Load config with external Redis
func TestLoad_ExternalRedis(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
services:
  redis:
    uri: "redis://external-redis:6379"
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.Services)
	assert.NotNil(t, cfg.Services.Redis)
	assert.Equal(t, "redis://external-redis:6379", cfg.Services.Redis.URI)
	assert.Equal(t, "", cfg.Services.Redis.Image)
}

// M4.4: Integration test - Load config with managed Redis + password
func TestLoad_ManagedRedisWithPassword(t *testing.T) {
	os.Setenv("TEST_REDIS_PASS", "test-password-123")
	defer os.Unsetenv("TEST_REDIS_PASS")

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
services:
  redis:
    image: "redis:7-alpine"
    password: "${TEST_REDIS_PASS}"
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.Services)
	assert.NotNil(t, cfg.Services.Redis)
	assert.Equal(t, "redis:7-alpine", cfg.Services.Redis.Image)
	assert.Equal(t, "test-password-123", cfg.Services.Redis.Password)
}

// M4.4: Integration test - Load config with both uri and image (should fail)
func TestLoad_MutualExclusivityError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
services:
  redis:
    uri: "redis://external:6379"
    image: "redis:7-alpine"
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

// M4.4: Integration test - Load config with missing env var (should fail)
func TestLoad_MissingEnvVarError(t *testing.T) {
	// Ensure variable is NOT set
	os.Unsetenv("MISSING_REDIS_URI")

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
services:
  redis:
    uri: "${MISSING_REDIS_URI}"
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "environment variable 'MISSING_REDIS_URI' referenced in holt.yml is not set")
}

// M4.4: Backward compatibility test - Legacy config with only image field
func TestLoad_LegacyManagedRedis(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
services:
  redis:
    image: "redis:7-alpine"
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.Services)
	assert.NotNil(t, cfg.Services.Redis)
	assert.Equal(t, "redis:7-alpine", cfg.Services.Redis.Image)
	assert.Equal(t, "", cfg.Services.Redis.URI)
	assert.Equal(t, "", cfg.Services.Redis.Password)
}

// M4.4: Backward compatibility test - No services.redis section
func TestLoad_NoRedisConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "holt.yml")

	config := `version: "1.0"
agents:
  test-agent:
    image: "test:latest"
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
`
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	// Services section may be nil or have nil Redis - both are valid
}

// M4.4: Test URI with database number
func TestValidateRedisConfig_URIWithDatabase(t *testing.T) {
	redis := &ServiceOverride{
		URI: "redis://host:6379/2",
	}

	err := validateRedisConfig(redis)
	assert.NoError(t, err)
}

// M4.4: Test URI with authentication
func TestValidateRedisConfig_URIWithAuth(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{"password only", "redis://:password@host:6379"},
		{"username and password", "redis://user:password@host:6379"},
		{"TLS with auth", "rediss://user:password@host:6380"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redis := &ServiceOverride{
				URI: tt.uri,
			}
			err := validateRedisConfig(redis)
			assert.NoError(t, err)
		})
	}
}
