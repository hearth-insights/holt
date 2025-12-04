package pup

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// Config holds the agent pup's runtime configuration loaded from environment variables.
// All fields are required and validated at startup to ensure fail-fast behavior.
// BiddingStrategy defines the agent's bidding behavior (M4.8)
type BiddingStrategy struct {
	Type        blackboard.BidType `json:"type"`
	TargetTypes []string           `json:"target_types,omitempty"`
}

// Config holds the agent pup's runtime configuration loaded from environment variables.
// All fields are required and validated at startup to ensure fail-fast behavior.
type Config struct {
	// InstanceName is the Holt instance identifier (from HOLT_INSTANCE_NAME)
	InstanceName string

	// AgentName is the logical name of this agent (from HOLT_AGENT_NAME)
	// M3.7: This IS the role (agent key from holt.yml)
	AgentName string

	// RedisURL is the Redis connection string (from REDIS_URL)
	RedisURL string

	// Command is the command array to execute for agent tools (from HOLT_AGENT_COMMAND)
	// Expected format: JSON array like ["/app/run.sh"] or ["/usr/bin/python3", "agent.py"]
	Command []string

	// BiddingStrategy is the bid type this agent submits for claims (from HOLT_BIDDING_STRATEGY)
	// M4.8: Now a struct parsed from JSON
	BiddingStrategy BiddingStrategy

	// BidScript is the command array to execute for dynamic bidding (from HOLT_AGENT_BID_SCRIPT)
	BidScript []string

	// MaxContextDepth is the maximum depth for context assembly BFS (from HOLT_MAX_CONTEXT_DEPTH)
	// Defaults to 10 if not set.
	MaxContextDepth int
}

// LoadConfig reads and validates configuration from environment variables.
// Returns an error if any required variable is missing or invalid.
// This function implements fail-fast validation - all errors are detected
// at startup before any resources are allocated.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		InstanceName:    os.Getenv("HOLT_INSTANCE_NAME"),
		AgentName:       os.Getenv("HOLT_AGENT_NAME"), // M3.7: This IS the role
		RedisURL:        os.Getenv("REDIS_URL"),
		MaxContextDepth: 10, // Default value
	}

	// Parse command array from JSON
	commandJSON := os.Getenv("HOLT_AGENT_COMMAND")
	if commandJSON != "" {
		if err := json.Unmarshal([]byte(commandJSON), &cfg.Command); err != nil {
			return nil, fmt.Errorf("failed to parse HOLT_AGENT_COMMAND as JSON array: %w", err)
		}
	}

	// Parse bid script array from JSON
	bidScriptJSON := os.Getenv("HOLT_AGENT_BID_SCRIPT")
	if bidScriptJSON != "" {
		if err := json.Unmarshal([]byte(bidScriptJSON), &cfg.BidScript); err != nil {
			return nil, fmt.Errorf("failed to parse HOLT_AGENT_BID_SCRIPT as JSON array: %w", err)
		}
	}

	// Parse bidding strategy (M4.8)
	biddingStrategyJSON := os.Getenv("HOLT_BIDDING_STRATEGY")
	if biddingStrategyJSON != "" {
		// Try parsing as JSON object (new format)
		if err := json.Unmarshal([]byte(biddingStrategyJSON), &cfg.BiddingStrategy); err != nil {
			// Fallback check: if it's a simple string (legacy env var from old orchestrator?), fail hard
			// We want to enforce the breaking change everywhere.
			return nil, fmt.Errorf("failed to parse HOLT_BIDDING_STRATEGY as JSON object: %w", err)
		}
	}

	// Parse max context depth
	maxContextDepthStr := os.Getenv("HOLT_MAX_CONTEXT_DEPTH")
	if maxContextDepthStr != "" {
		var depth int
		if _, err := fmt.Sscanf(maxContextDepthStr, "%d", &depth); err != nil {
			return nil, fmt.Errorf("invalid HOLT_MAX_CONTEXT_DEPTH: %w", err)
		}
		cfg.MaxContextDepth = depth
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required configuration fields are present and valid.
// Returns the first validation error encountered.
func (c *Config) Validate() error {
	if c.InstanceName == "" {
		return fmt.Errorf("HOLT_INSTANCE_NAME environment variable is required")
	}

	if c.AgentName == "" {
		return fmt.Errorf("HOLT_AGENT_NAME environment variable is required")
	}

	// M3.7: No AgentRole field - AgentName IS the role

	if c.RedisURL == "" {
		return fmt.Errorf("REDIS_URL environment variable is required")
	}

	if len(c.Command) == 0 {
		return fmt.Errorf("HOLT_AGENT_COMMAND environment variable is required (must be a non-empty JSON array)")
	}

	// M3.6: Bidding strategy validation - either bid_script or bidding_strategy required
	hasBidScript := len(c.BidScript) > 0
	hasStaticStrategy := c.BiddingStrategy.Type != ""

	if !hasBidScript && !hasStaticStrategy {
		return fmt.Errorf("either HOLT_BIDDING_STRATEGY or HOLT_AGENT_BID_SCRIPT must be provided")
	}

	// Validate bidding strategy is a valid enum if provided
	if hasStaticStrategy {
		if err := c.BiddingStrategy.Type.Validate(); err != nil {
			return fmt.Errorf("invalid HOLT_BIDDING_STRATEGY type: %w", err)
		}
	} else {
		log.Printf("[WARN] No static bidding_strategy configured for agent %s, relying entirely on bid_script", c.AgentName)
	}

	if c.MaxContextDepth <= 0 {
		return fmt.Errorf("HOLT_MAX_CONTEXT_DEPTH must be a positive integer")
	}

	return nil
}

