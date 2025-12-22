package config

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// OrchestratorConfig specifies orchestrator behavior settings (M3.3, M4.6)
type OrchestratorConfig struct {
	Image                     string `yaml:"image,omitempty"`                        // Docker image to use (default: holt-orchestrator:latest)
	MaxReviewIterations       *int   `yaml:"max_review_iterations,omitempty"`        // How many times an artefact can be rejected and reworked (0 = unlimited, default = 3)
	TimestampDriftToleranceMs *int   `yaml:"timestamp_drift_tolerance_ms,omitempty"` // M4.6: Max allowed timestamp drift in milliseconds (default = 300000 = 5 minutes)
}

// HoltConfig represents the top-level holt.yml configuration
type HoltConfig struct {
	Version      string              `yaml:"version"`
	Orchestrator *OrchestratorConfig `yaml:"orchestrator,omitempty"` // M3.3: Orchestrator holtings
	Agents       map[string]Agent    `yaml:"agents"`
	Services     *ServicesConfig     `yaml:"services,omitempty"`
}

// BiddingStrategyConfig defines the agent's bidding behavior (M4.8)
type BiddingStrategyConfig struct {
	Type        string   `yaml:"type" json:"type"`                                     // Required: review, claim, exclusive, or ignore
	TargetTypes []string `yaml:"target_types,omitempty" json:"target_types,omitempty"` // Optional: list of artefact types to bid on
}

// UnmarshalYAML implements custom unmarshalling to reject legacy string format (M4.8)
func (b *BiddingStrategyConfig) UnmarshalYAML(value *yaml.Node) error {
	// Reject string format (Breaking Change)
	if value.Kind == yaml.ScalarNode {
		return fmt.Errorf("legacy string format for bidding_strategy is no longer supported. Please use object format: { type: \"...\", target_types: [...] }")
	}

	// Unmarshal object format
	type plain BiddingStrategyConfig
	if err := value.Decode((*plain)(b)); err != nil {
		return err
	}

	return nil
}

// Agent represents a single agent configuration
// M3.7: Agent key in holt.yml IS the role - no separate role field
type Agent struct {
	Image           string                `yaml:"image"` // Required: Docker image name for this agent
	Build           *BuildConfig          `yaml:"build,omitempty"`
	Command         []string              `yaml:"command"`
	BidScript       []string              `yaml:"bid_script,omitempty"`
	Workspace       *WorkspaceConfig      `yaml:"workspace,omitempty"`
	Replicas        *int                  `yaml:"replicas,omitempty"`
	Strategy        string                `yaml:"strategy,omitempty"`
	BiddingStrategy BiddingStrategyConfig `yaml:"bidding_strategy"` // Required: review, claim, exclusive, or ignore
	Environment     []string              `yaml:"environment,omitempty"`
	Resources       *ResourcesConfig      `yaml:"resources,omitempty"`
	Prompts         *PromptsConfig        `yaml:"prompts,omitempty"`

	// M3.4: Controller-worker pattern
	Mode   string        `yaml:"mode,omitempty"`   // "controller" or empty (traditional)
	Worker *WorkerConfig `yaml:"worker,omitempty"` // Required if mode="controller"

	// M3.9: Configurable health checks
	HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty"` // Optional: custom health check

	// M4.5: Docker volume mounts
	Volumes []string `yaml:"volumes,omitempty"` // Optional: Docker volume mount specifications (e.g., "~/.config/gcloud:/root/.config/gcloud:ro")

	// M5.1: Synchronization configuration for fan-in pattern
	Synchronize *SynchronizeConfig `yaml:"synchronize,omitempty"` // Optional: Declarative fan-in synchronization
}

// BuildConfig specifies how to build an agent's container image
type BuildConfig struct {
	Context string `yaml:"context"`
}

// WorkspaceConfig specifies workspace mount configuration
type WorkspaceConfig struct {
	Mode string `yaml:"mode"` // "ro" or "rw"
}

// WorkerConfig specifies worker configuration for controller-worker pattern (M3.4)
type WorkerConfig struct {
	Image          string           `yaml:"image"`                    // Worker image (can differ from controller)
	MaxConcurrent  int              `yaml:"max_concurrent,omitempty"` // Default: 1
	Command        []string         `yaml:"command"`
	Workspace      *WorkspaceConfig `yaml:"workspace,omitempty"`
	Environment    []string         `yaml:"environment,omitempty"`     // M4.10: Custom environment variables for worker
	KeepContainers bool             `yaml:"keep_containers,omitempty"` // M4.10: Retain worker containers for debugging (default: false)
}

// ResourcesConfig specifies resource limits and reservations
type ResourcesConfig struct {
	Limits       *ResourceLimits `yaml:"limits,omitempty"`
	Reservations *ResourceLimits `yaml:"reservations,omitempty"`
}

// ResourceLimits specifies CPU and memory limits
type ResourceLimits struct {
	CPUs   string `yaml:"cpus,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

// PromptsConfig specifies custom prompts for agent operations
type PromptsConfig struct {
	Claim     string `yaml:"claim,omitempty"`
	Execution string `yaml:"execution,omitempty"`
}

// HealthCheckConfig specifies custom health check configuration (M3.9)
type HealthCheckConfig struct {
	Command  []string `yaml:"command"`            // Command to execute for health check
	Interval string   `yaml:"interval,omitempty"` // Check interval (default: 30s)
	Timeout  string   `yaml:"timeout,omitempty"`  // Command timeout (default: 5s)
}

// SynchronizeConfig defines fan-in synchronization configuration (M5.1)
// Enables declarative waiting for multiple prerequisite artefacts from parallel workflow branches.
type SynchronizeConfig struct {
	// AncestorType is the artefact type to find as the common ancestor (e.g., "CodeCommit")
	AncestorType string `yaml:"ancestor_type" json:"ancestor_type"` // Required

	// WaitFor specifies the prerequisite artefacts to wait for
	WaitFor []WaitCondition `yaml:"wait_for" json:"wait_for"` // Required: at least one condition

	// MaxDepth limits descendant traversal depth (0 = unlimited)
	MaxDepth int `yaml:"max_depth,omitempty" json:"max_depth,omitempty"` // Optional: default 0 (unlimited)
}

// WaitCondition specifies a single prerequisite artefact to wait for (M5.1)
// Supports two patterns:
//  1. Named pattern: Wait for exactly one artefact of this type
//  2. Producer-Declared pattern: Wait for N artefacts where N is from metadata
type WaitCondition struct {
	// Type is the artefact type to wait for (e.g., "TestResult")
	Type string `yaml:"type" json:"type"` // Required

	// CountFromMetadata is the metadata key containing the expected count (Producer-Declared pattern)
	// If empty, waits for exactly 1 artefact (Named pattern)
	CountFromMetadata string `yaml:"count_from_metadata,omitempty" json:"count_from_metadata,omitempty"` // Optional
}

// ServicesConfig specifies service-level overrides
type ServicesConfig struct {
	Orchestrator *ServiceOverride `yaml:"orchestrator,omitempty"`
	Redis        *ServiceOverride `yaml:"redis,omitempty"`
}

// ServiceOverride allows overriding default service images
// M4.4: Added URI and Password fields for Redis configuration
type ServiceOverride struct {
	Image     string           `yaml:"image,omitempty"`
	URI       string           `yaml:"uri,omitempty"`      // M4.4: External Redis URI (mutually exclusive with Image)
	Password  string           `yaml:"password,omitempty"` // M4.4: Password for managed Redis (only valid with Image)
	Resources *ResourcesConfig `yaml:"resources,omitempty"`
}

// Validate performs strict validation on the configuration
func (c *HoltConfig) Validate() error {
	// Required: version
	if c.Version != "1.0" {
		return fmt.Errorf("unsupported version: %s (expected: 1.0)", c.Version)
	}

	// Required: at least one agent
	if len(c.Agents) == 0 {
		return fmt.Errorf("no agents defined")
	}

	// M3.7: Validate agent keys (which are now roles)
	for agentRole, agent := range c.Agents {
		if err := validateRoleName(agentRole); err != nil {
			return fmt.Errorf("invalid agent role '%s': %w", agentRole, err)
		}
		if err := agent.Validate(agentRole); err != nil {
			return err
		}
	}

	// M3.7: Role uniqueness is guaranteed by map keys (no duplicate check needed)

	// M3.3: Apply default orchestrator config if missing
	if c.Orchestrator == nil {
		defaultIterations := 3
		defaultDriftMs := 300000 // 5 minutes
		c.Orchestrator = &OrchestratorConfig{
			MaxReviewIterations:       &defaultIterations,
			TimestampDriftToleranceMs: &defaultDriftMs,
		}
	} else {
		// Orchestrator section exists but fields may be missing - apply defaults
		if c.Orchestrator.MaxReviewIterations == nil {
			defaultIterations := 3
			c.Orchestrator.MaxReviewIterations = &defaultIterations
		}
		if c.Orchestrator.TimestampDriftToleranceMs == nil {
			defaultDriftMs := 300000 // 5 minutes
			c.Orchestrator.TimestampDriftToleranceMs = &defaultDriftMs
		}
	}

	// M3.3: Validate orchestrator config
	if *c.Orchestrator.MaxReviewIterations < 0 {
		return fmt.Errorf("orchestrator.max_review_iterations must be >= 0 (0 = unlimited), got %d", *c.Orchestrator.MaxReviewIterations)
	}

	// M4.6: Validate timestamp drift tolerance
	if *c.Orchestrator.TimestampDriftToleranceMs < 0 {
		return fmt.Errorf("orchestrator.timestamp_drift_tolerance_ms must be >= 0, got %d", *c.Orchestrator.TimestampDriftToleranceMs)
	}

	// M4.4: Validate Redis configuration
	if c.Services != nil && c.Services.Redis != nil {
		if err := validateRedisConfig(c.Services.Redis); err != nil {
			return err
		}
	}

	return nil
}

// validateRoleName ensures role names follow conventions (M3.7)
// Rules: PascalCase recommended, alphanumeric + hyphens, 1-64 chars
func validateRoleName(role string) error {
	if role == "" {
		return fmt.Errorf("role cannot be empty")
	}
	if len(role) > 64 {
		return fmt.Errorf("role name too long (max 64 chars)")
	}

	// Check alphanumeric (allowing hyphens for compound roles like "Code-Reviewer")
	for _, ch := range role {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || ch == '-') {
			return fmt.Errorf("role must be alphanumeric with optional hyphens")
		}
	}

	// Warn if not PascalCase (not an error - allow flexibility)
	if role[0] < 'A' || role[0] > 'Z' {
		log.Printf("[Config] Warning: Role '%s' should start with uppercase letter (PascalCase convention)", role)
	}

	return nil
}

// Validate performs validation on a single agent configuration
func (a *Agent) Validate(name string) error {
	// M3.7: No role field validation - agent key IS the role

	// Required: image
	if a.Image == "" {
		return fmt.Errorf("agent '%s': image is required", name)
	}

	// Required: command
	if len(a.Command) == 0 {
		return fmt.Errorf("agent '%s': command is required", name)
	}

	// M3.6: Bidding strategy validation - either bid_script or bidding_strategy or synchronize required
	hasBidScript := len(a.BidScript) > 0
	hasStaticStrategy := a.BiddingStrategy.Type != ""
	hasSynchronize := a.Synchronize != nil // M5.1

	if !hasBidScript && !hasStaticStrategy && !hasSynchronize {
		return fmt.Errorf("agent '%s': either bidding_strategy, bid_script, or synchronize must be provided", name)
	}

	// M5.1: Validate synchronize is mutually exclusive with bidding_strategy and bid_script
	if hasSynchronize {
		if hasStaticStrategy {
			return fmt.Errorf("agent '%s': synchronize and bidding_strategy are mutually exclusive", name)
		}
		if hasBidScript {
			return fmt.Errorf("agent '%s': synchronize and bid_script are mutually exclusive", name)
		}

		// Validate synchronize configuration
		if a.Synchronize.AncestorType == "" {
			return fmt.Errorf("agent '%s': synchronize block missing ancestor_type", name)
		}

		if len(a.Synchronize.WaitFor) == 0 {
			return fmt.Errorf("agent '%s': synchronize block has empty wait_for list", name)
		}

		// Validate each wait condition
		for i, condition := range a.Synchronize.WaitFor {
			if condition.Type == "" {
				return fmt.Errorf("agent '%s': synchronize wait_for[%d] missing type", name, i)
			}
		}

		// M5.1.1: Validate count_from_metadata exclusivity
		// If ANY wait_for has count_from_metadata, there must be ONLY ONE wait_for condition
		hasCountFromMetadata := false
		for _, condition := range a.Synchronize.WaitFor {
			if condition.CountFromMetadata != "" {
				hasCountFromMetadata = true
				break
			}
		}

		if hasCountFromMetadata && len(a.Synchronize.WaitFor) > 1 {
			return fmt.Errorf("agent '%s': count_from_metadata pattern requires exactly ONE wait_for condition (found %d)", name, len(a.Synchronize.WaitFor))
		}

		// Validate max_depth (must be non-negative)
		if a.Synchronize.MaxDepth < 0 {
			return fmt.Errorf("agent '%s': synchronize max_depth must be >= 0", name)
		}
	}

	// Validate bidding_strategy enum if provided
	if hasStaticStrategy {
		if a.BiddingStrategy.Type != "review" && a.BiddingStrategy.Type != "claim" && a.BiddingStrategy.Type != "exclusive" && a.BiddingStrategy.Type != "ignore" {
			return fmt.Errorf("agent '%s': invalid bidding_strategy type: %s (must be 'review', 'claim', 'exclusive', or 'ignore')", name, a.BiddingStrategy.Type)
		}
	}

	// If build.context specified AND no pre-built image is listed, verify path exists
	// If a.Image is set, the build context is optional (pre-built image is used)
	if a.Image == "" && a.Build != nil && a.Build.Context != "" {
		if _, err := os.Stat(a.Build.Context); os.IsNotExist(err) {
			return fmt.Errorf("agent '%s': build context does not exist: %s", name, a.Build.Context)
		}
	}

	// Validate workspace mode if specified
	if a.Workspace != nil {
		if a.Workspace.Mode != "" && a.Workspace.Mode != "ro" && a.Workspace.Mode != "rw" {
			return fmt.Errorf("agent '%s': invalid workspace mode: %s (must be 'ro' or 'rw')", name, a.Workspace.Mode)
		}
	}

	// Validate strategy if specified
	if a.Strategy != "" && a.Strategy != "reuse" && a.Strategy != "fresh_per_call" {
		return fmt.Errorf("agent '%s': invalid strategy: %s (must be 'reuse' or 'fresh_per_call')", name, a.Strategy)
	}

	// M3.4: Validate controller-worker configuration
	if a.Mode == "controller" {
		// Validate worker config exists
		if a.Worker == nil {
			return fmt.Errorf("agent '%s' has mode='controller' but no worker configuration", name)
		}

		// Validate worker image
		if a.Worker.Image == "" {
			return fmt.Errorf("agent '%s' worker configuration missing image", name)
		}

		// Validate worker command
		if len(a.Worker.Command) == 0 {
			return fmt.Errorf("agent '%s' worker configuration missing command", name)
		}

		// Set default max_concurrent if not specified
		if a.Worker.MaxConcurrent == 0 {
			a.Worker.MaxConcurrent = 1
		}

		// Validate max_concurrent is positive
		if a.Worker.MaxConcurrent < 1 {
			return fmt.Errorf("agent '%s' worker.max_concurrent must be >= 1", name)
		}

		// Validate worker workspace mode if specified
		if a.Worker.Workspace != nil && a.Worker.Workspace.Mode != "" {
			if a.Worker.Workspace.Mode != "ro" && a.Worker.Workspace.Mode != "rw" {
				return fmt.Errorf("agent '%s' worker: invalid workspace mode: %s (must be 'ro' or 'rw')", name, a.Worker.Workspace.Mode)
			}
		}
	} else if a.Mode != "" {
		// Unknown mode
		return fmt.Errorf("agent '%s' has unknown mode '%s' (valid: 'controller' or omit)", name, a.Mode)
	}

	// M4.5: Validate volume mounts (optional security warnings)
	for _, vol := range a.Volumes {
		// Basic format check: should have at least one colon
		if !strings.Contains(vol, ":") {
			return fmt.Errorf("agent '%s': invalid volume mount format '%s' (expected 'source:destination' or 'source:destination:mode')", name, vol)
		}

		// Security warning for rw mode
		if strings.HasSuffix(vol, ":rw") {
			log.Printf("[Config] Security Warning: Agent '%s' has read-write volume mount '%s'. Consider using ':ro' mode for credential directories.", name, vol)
		}
	}

	return nil
}

// Load reads and validates holt.yml from the specified path
// M4.4: Enhanced to expand environment variables before parsing YAML
func Load(path string) (*HoltConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// M4.4: Expand environment variables before parsing
	expandedData, err := expandEnvVars(data)
	if err != nil {
		return nil, fmt.Errorf("failed to expand environment variables: %w", err)
	}

	var config HoltConfig
	if err := yaml.Unmarshal(expandedData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// ExtractEnvVarNames extracts all environment variable names referenced in YAML content (M4.4)
// Returns a slice of unique variable names found in ${VAR_NAME} patterns.
// This is used to pass environment variables to containers that need to load the config.
func ExtractEnvVarNames(data []byte) []string {
	content := string(data)

	// Regex to find ${VAR_NAME} patterns
	// Matches ${...} where ... is alphanumeric plus underscore
	re := regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

	// Find all matches
	matches := re.FindAllStringSubmatch(content, -1)

	// Extract unique variable names
	varNames := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			varNames[match[1]] = true
		}
	}

	// Convert to slice
	result := make([]string, 0, len(varNames))
	for varName := range varNames {
		result = append(result, varName)
	}

	return result
}

// expandEnvVars expands environment variable references in YAML content (M4.4)
// Supports ${VAR_NAME} syntax. Returns error if referenced variable is not set.
func expandEnvVars(data []byte) ([]byte, error) {
	content := string(data)

	// Regex to find ${VAR_NAME} patterns
	// Matches ${...} where ... is alphanumeric plus underscore
	re := regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

	// Track missing variables for error reporting
	var missingVars []string

	// Replace all matches
	result := re.ReplaceAllStringFunc(content, func(match string) string {
		// Extract variable name (remove ${ and })
		varName := match[2 : len(match)-1]

		// Get value from environment
		value, exists := os.LookupEnv(varName)
		if !exists {
			missingVars = append(missingVars, varName)
			return match // Keep original if not found (will be caught below)
		}

		return value
	})

	// Error if any variables were missing
	if len(missingVars) > 0 {
		return nil, fmt.Errorf("environment variable '%s' referenced in holt.yml is not set", missingVars[0])
	}

	return []byte(result), nil
}

// validateRedisConfig validates Redis configuration (M4.4)
func validateRedisConfig(redis *ServiceOverride) error {
	hasURI := redis.URI != ""
	hasImage := redis.Image != ""
	hasPassword := redis.Password != ""

	// M4.4: URI and Image are mutually exclusive
	if hasURI && hasImage {
		return fmt.Errorf("invalid configuration: services.redis.uri and services.redis.image are mutually exclusive. Use 'uri' for external Redis or 'image' for managed Redis")
	}

	// M4.4: Password is only valid with managed Redis (Image mode)
	if hasPassword && hasURI {
		log.Printf("[Config] Warning: services.redis.password is ignored when using external Redis (uri mode)")
	}

	// M4.4: Validate URI length (practical Docker env var limit)
	if hasURI && len(redis.URI) > 4096 {
		return fmt.Errorf("services.redis.uri exceeds maximum length of 4096 characters")
	}

	// M4.4: Basic URI format validation (must start with redis:// or rediss://)
	if hasURI {
		uri := strings.ToLower(strings.TrimSpace(redis.URI))
		if !strings.HasPrefix(uri, "redis://") && !strings.HasPrefix(uri, "rediss://") {
			return fmt.Errorf("services.redis.uri must start with 'redis://' or 'rediss://' scheme")
		}
	}

	return nil
}
