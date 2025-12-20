package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	claimsInstanceName string
	claimsIncomplete   bool
	claimsStatuses     []string
	claimsSummaryOnly  bool
	claimsOutputJSON   bool
)

var claimsCmd = &cobra.Command{
	Use:   "claims",
	Short: "Query claims by status",
	Long: `Query and display claims by their status.

Useful for diagnosing stuck workflows or finding incomplete work.

Examples:
  # Show all incomplete claims (pending_*)
  holt claims --incomplete

  # Show specific statuses
  holt claims --status pending_review,pending_exclusive

  # For specific instance
  holt claims --name my-instance --incomplete`,
	RunE: runClaims,
}

func init() {
	claimsCmd.Flags().StringVarP(&claimsInstanceName, "name", "n", "", "Instance name (auto-inferred if omitted)")
	claimsCmd.Flags().BoolVar(&claimsIncomplete, "incomplete", false, "Show all incomplete claims (pending_*)")
	claimsCmd.Flags().StringSliceVar(&claimsStatuses, "status", []string{}, "Specific statuses to query (comma-separated)")
	claimsCmd.Flags().BoolVar(&claimsSummaryOnly, "summary", false, "Show only counts, don't fetch artefact details")
	claimsCmd.Flags().BoolVar(&claimsOutputJSON, "json", false, "Output as one-line JSON per claim")

	rootCmd.AddCommand(claimsCmd)
}

func runClaims(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := claimsInstanceName
	if targetInstanceName == "" {
		targetInstanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			if err.Error() == "no Holt instances found for this workspace" {
				return printer.Error(
					"no Holt instances found",
					"No running instances found for this workspace.",
					[]string{"Start an instance first:\n  holt up"},
				)
			}
			if err.Error() == "multiple instances found for this workspace, use --name to specify which one" {
				return printer.Error(
					"multiple instances found",
					"Found multiple running instances for this workspace.",
					[]string{
						"Specify which instance to query:\n  holt claims --name <instance-name>",
						"List instances:\n  holt list",
					},
				)
			}
			return fmt.Errorf("failed to infer instance: %w", err)
		}
	}

	// Phase 2: Verify instance is running
	if err := instance.VerifyInstanceRunning(ctx, cli, targetInstanceName); err != nil {
		return printer.Error(
			fmt.Sprintf("instance '%s' is not running", targetInstanceName),
			fmt.Sprintf("Error: %v", err),
			[]string{fmt.Sprintf("Start the instance:\n  holt up --name %s", targetInstanceName)},
		)
	}

	// Phase 3: Get Redis connection
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to get Redis port: %w", err)
	}

	// Use GetRedisURL and ParseURL like watch command does
	redisURL := instance.GetRedisURL(redisPort)
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	// Verify Redis connectivity
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return printer.Error(
			"cannot connect to Redis",
			fmt.Sprintf("Failed to ping Redis at %s: %v", redisURL, err),
			[]string{"Verify instance is healthy:\n  holt list"},
		)
	}

	bbClient, err := blackboard.NewClient(redisOpts, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to create blackboard client: %w", err)
	}

	// Phase 4: Determine which statuses to query
	statusesToQuery := claimsStatuses
	if claimsIncomplete {
		if len(statusesToQuery) > 0 {
			return printer.Error(
				"conflicting flags",
				"Cannot use both --incomplete and --status together",
				[]string{"Use one or the other:\n  holt claims --incomplete\n  holt claims --status pending_review"},
			)
		}
		statusesToQuery = []string{
			string(blackboard.ClaimStatusPendingReview),
			string(blackboard.ClaimStatusPendingParallel),
			string(blackboard.ClaimStatusPendingExclusive),
			string(blackboard.ClaimStatusPendingAssignment),
		}
	}

	if len(statusesToQuery) == 0 {
		return printer.Error(
			"no statuses specified",
			"Must specify either --incomplete or --status",
			[]string{
				"Show all incomplete claims:\n  holt claims --incomplete",
				"Show specific statuses:\n  holt claims --status pending_review,pending_exclusive",
			},
		)
	}

	// Phase 5: Query claims
	claims, err := bbClient.GetClaimsByStatus(ctx, statusesToQuery)
	if err != nil {
		return fmt.Errorf("failed to query claims: %w", err)
	}

	// Phase 6: Display results
	if len(claims) == 0 {
		if claimsOutputJSON {
			// No output for JSON mode when empty
			return nil
		}
		printer.Info("No claims found matching statuses: %s\n", strings.Join(statusesToQuery, ", "))
		return nil
	}

	// JSON output mode
	if claimsOutputJSON {
		for _, claim := range claims {
			// Fetch artefact for type information
			artefact, err := bbClient.GetArtefact(ctx, claim.ArtefactID)

			output := map[string]interface{}{
				"claim_id":     claim.ID,
				"artefact_id":  claim.ArtefactID,
				"status":       claim.Status,
			}

			if err == nil {
				output["artefact_type"] = artefact.Header.Type
				output["artefact_version"] = artefact.Header.Version
			}

			if len(claim.GrantedReviewAgents) > 0 {
				output["review_agents"] = claim.GrantedReviewAgents
			}
			if len(claim.GrantedParallelAgents) > 0 {
				output["parallel_agents"] = claim.GrantedParallelAgents
			}
			if claim.GrantedExclusiveAgent != "" {
				output["exclusive_agent"] = claim.GrantedExclusiveAgent
			}
			if len(claim.AdditionalContextIDs) > 0 {
				output["additional_context_count"] = len(claim.AdditionalContextIDs)
			}

			jsonBytes, err := json.Marshal(output)
			if err != nil {
				continue
			}
			fmt.Println(string(jsonBytes))
		}
		return nil
	}

	// Group claims by status for organized display
	claimsByStatus := make(map[blackboard.ClaimStatus][]*blackboard.Claim)
	for _, claim := range claims {
		claimsByStatus[claim.Status] = append(claimsByStatus[claim.Status], claim)
	}

	// Display summary first
	fmt.Printf("\nFound %d claim(s) in instance '%s'\n\n", len(claims), targetInstanceName)

	// Display each status group
	for _, status := range statusesToQuery {
		statusClaims := claimsByStatus[blackboard.ClaimStatus(status)]
		if len(statusClaims) == 0 {
			continue
		}

		fmt.Printf("━━━ %s (%d) ━━━\n\n", status, len(statusClaims))

		for i, claim := range statusClaims {
			// Fetch artefact to show type/version
			artefact, err := bbClient.GetArtefact(ctx, claim.ArtefactID)
			if err != nil {
				fmt.Printf("  %d. Claim: %s\n", i+1, shortClaimID(claim.ID))
				fmt.Printf("     Artefact: %s (failed to load: %v)\n", shortClaimID(claim.ArtefactID), err)
				fmt.Printf("     Status: %s\n\n", claim.Status)
				continue
			}

			fmt.Printf("  %d. Claim: %s\n", i+1, shortClaimID(claim.ID))
			fmt.Printf("     Artefact: %s (type=%s, v%d)\n", shortClaimID(claim.ArtefactID), artefact.Header.Type, artefact.Header.Version)
			fmt.Printf("     Status: %s\n", claim.Status)

			// Show granted agents based on status
			if len(claim.GrantedReviewAgents) > 0 {
				fmt.Printf("     Review agents: %v\n", claim.GrantedReviewAgents)
			}
			if len(claim.GrantedParallelAgents) > 0 {
				fmt.Printf("     Parallel agents: %v\n", claim.GrantedParallelAgents)
			}
			if claim.GrantedExclusiveAgent != "" {
				fmt.Printf("     Exclusive agent: %s\n", claim.GrantedExclusiveAgent)
			}

			// Show additional context if present (feedback loop)
			if len(claim.AdditionalContextIDs) > 0 {
				fmt.Printf("     Additional context: %d artefact(s)\n", len(claim.AdditionalContextIDs))
			}

			fmt.Println()
		}
	}

	// Display summary footer
	fmt.Printf("Total: %d incomplete claim(s)\n\n", len(claims))

	return nil
}

// shortClaimID returns the first 8 characters of an ID for readability
func shortClaimID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
