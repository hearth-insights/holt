package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	provisionInstanceName string
	provisionName         string
	provisionFromFile     string
	provisionFromLiteral  string
	provisionFromURL      string
	provisionFromCommand  string
	provisionTargetRoles  string
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Manually create or update cached knowledge for agents",
	Long: `Manually provision knowledge artefacts that agents can use across workflows.

The provision command creates or updates Knowledge artefacts in the knowledge_index,
making them available to agents based on role-matching patterns. Provisioned knowledge
persists across workflow runs and can be versioned.

Source Flags (exactly one required):
  --from-file <path>        Load knowledge from a file
  --from-literal "text"     Use literal string as knowledge
  --from-url <url>          Fetch knowledge from a URL
  --from-command "cmd"      Execute command and use stdout as knowledge

Examples:
  # Provision from file
  holt provision --name sdk-docs --from-file ./docs/api.md

  # Provision literal text
  holt provision --name config --from-literal "API_KEY=abc123"

  # Provision from URL
  holt provision --name readme --from-url https://example.com/README.md

  # Provision from command output
  holt provision --name env-info --from-command "env | grep AWS"

  # Target specific agent roles (glob patterns)
  holt provision --name backend-docs --from-file ./backend.md --target-roles "backend-*,api-*"`,
	RunE: runProvision,
}

func init() {
	provisionCmd.Flags().StringVarP(&provisionInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	provisionCmd.Flags().StringVar(&provisionName, "knowledge-name", "", "Globally unique name for this knowledge (required)")
	provisionCmd.Flags().StringVar(&provisionFromFile, "from-file", "", "Load knowledge from file")
	provisionCmd.Flags().StringVar(&provisionFromLiteral, "from-literal", "", "Use literal string as knowledge")
	provisionCmd.Flags().StringVar(&provisionFromURL, "from-url", "", "Fetch knowledge from URL")
	provisionCmd.Flags().StringVar(&provisionFromCommand, "from-command", "", "Execute command and use stdout")
	provisionCmd.Flags().StringVar(&provisionTargetRoles, "target-roles", "*", "Comma-separated role globs (default: all)")
	provisionCmd.MarkFlagRequired("knowledge-name")
	rootCmd.AddCommand(provisionCmd)
}

func runProvision(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Validate exactly one source flag is provided
	sources := 0
	if provisionFromFile != "" {
		sources++
	}
	if provisionFromLiteral != "" {
		sources++
	}
	if provisionFromURL != "" {
		sources++
	}
	if provisionFromCommand != "" {
		sources++
	}

	if sources == 0 {
		return printer.Error(
			"no knowledge source provided",
			"Exactly one source flag is required.",
			[]string{
				"Use --from-file <path>",
				"Use --from-literal \"text\"",
				"Use --from-url <url>",
				"Use --from-command \"cmd\"",
			},
		)
	}

	if sources > 1 {
		return printer.Error(
			"multiple knowledge sources provided",
			"Only one source flag can be used at a time.",
			[]string{"Choose one: --from-file, --from-literal, --from-url, or --from-command"},
		)
	}

	// Load knowledge content from the specified source
	content, err := loadKnowledgeContent()
	if err != nil {
		return printer.Error(
			"failed to load knowledge content",
			err.Error(),
			nil,
		)
	}

	// Parse target roles (comma-separated)
	targetRoles := parseTargetRoles(provisionTargetRoles)

	// Create Docker client
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Infer instance if not specified
	instanceName := provisionInstanceName
	if instanceName == "" {
		instanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			return printer.Error(
				"failed to infer instance",
				err.Error(),
				[]string{
					"Specify instance explicitly:\n  holt provision --name <instance> ...",
					"List instances:\n  holt list",
				},
			)
		}
		fmt.Printf("Inferred instance: %s\n", instanceName)
	}

	// Verify instance is running
	if err := instance.VerifyInstanceRunning(ctx, cli, instanceName); err != nil {
		return printer.Error(
			fmt.Sprintf("instance '%s' is not running", instanceName),
			fmt.Sprintf("Error: %v", err),
			[]string{fmt.Sprintf("Start the instance:\n  holt up --name %s", instanceName)},
		)
	}

	// Get Redis port
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, instanceName)
	if err != nil {
		return printer.Error(
			"Redis port not found",
			err.Error(),
			[]string{fmt.Sprintf("Restart instance:\n  holt down --name %s\n  holt up --name %s", instanceName, instanceName)},
		)
	}

	redisOpts := &redis.Options{
		Addr: fmt.Sprintf("localhost:%d", redisPort),
	}

	bbClient, err := blackboard.NewClient(redisOpts, instanceName)
	if err != nil {
		return printer.Error(
			"failed to create blackboard client",
			err.Error(),
			nil,
		)
	}
	defer bbClient.Close()

	// Test connection
	if err := bbClient.Ping(ctx); err != nil {
		return printer.Error(
			"failed to connect to Redis",
			err.Error(),
			[]string{"Ensure Redis is running for instance:\n  holt list"},
		)
	}

	// Create or version knowledge
	// Use empty string for threadLogicalID - will be mapped to "global"
	knowledge, err := bbClient.CreateOrVersionKnowledge(
		ctx,
		provisionName,
		content,
		targetRoles,
		"", // Empty threadLogicalID = global knowledge
		"cli",
	)
	if err != nil {
		return printer.Error(
			"failed to provision knowledge",
			err.Error(),
			nil,
		)
	}

	// Print success message
	var message string
	if knowledge.Header.Version > 1 {
		message = fmt.Sprintf("Updated %s from version %d -> %d", provisionName, knowledge.Header.Version-1, knowledge.Header.Version)
	} else {
		message = fmt.Sprintf("Initialized %s (version %d)", provisionName, knowledge.Header.Version)
	}

	fmt.Printf("✅ %s\n", message)
	fmt.Printf("  ID: %s\n", knowledge.ID)
	fmt.Printf("  Version: %d\n", knowledge.Header.Version)
	fmt.Printf("  Target roles: %v\n", targetRoles)
	fmt.Printf("  Content size: %d bytes\n", len(content))

	return nil
}

// loadKnowledgeContent loads content from the specified source
func loadKnowledgeContent() (string, error) {
	if provisionFromFile != "" {
		return loadFromFile(provisionFromFile)
	}

	if provisionFromLiteral != "" {
		return provisionFromLiteral, nil
	}

	if provisionFromURL != "" {
		return loadFromURL(provisionFromURL)
	}

	if provisionFromCommand != "" {
		return loadFromCommand(provisionFromCommand)
	}

	return "", fmt.Errorf("no source specified")
}

// loadFromFile reads content from a file
func loadFromFile(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(bytes), nil
}

// loadFromURL fetches content from a URL
func loadFromURL(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error: %s", resp.Status)
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(bytes), nil
}

// loadFromCommand executes a command and captures stdout
func loadFromCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nOutput: %s", err, string(output))
	}
	return string(output), nil
}

// parseTargetRoles splits comma-separated roles and trims whitespace
func parseTargetRoles(rolesStr string) []string {
	if rolesStr == "" || rolesStr == "*" {
		return []string{"*"}
	}

	parts := strings.Split(rolesStr, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			roles = append(roles, trimmed)
		}
	}

	if len(roles) == 0 {
		return []string{"*"}
	}

	return roles
}
