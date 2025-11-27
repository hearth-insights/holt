package commands

import (
	"context"
	"fmt"
	"os"

	dockerpkg "github.com/dyluth/holt/internal/docker"
	"github.com/dyluth/holt/internal/instance"
	"github.com/dyluth/holt/internal/printer"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var verifyInstanceName string

var verifyCmd = &cobra.Command{
	Use:   "verify <artefact-id>",
	Short: "Independently verify an artefact's cryptographic hash",
	Long: `Verify a V2 artefact's cryptographic integrity by recomputing its hash.

This command fetches the artefact from Redis, recomputes its SHA-256 hash using
RFC 8785 JSON canonicalization, and verifies it matches the artefact ID.

This provides independent verification for compliance audits - the same
cryptographic verification logic used by the orchestrator is available
to external auditors and forensic investigators.

Hash Verification Process:
  1. Fetch artefact from blackboard (Redis)
  2. Canonicalize Header + Payload using RFC 8785
  3. Compute SHA-256 hash of canonical bytes
  4. Compare computed hash with stored artefact ID

Success indicates:
  ✓ Artefact content has not been tampered with
  ✓ Hash matches what the agent Pup computed at creation time
  ✓ All parent relationships are cryptographically sound

Failure indicates potential tampering or data corruption.

Examples:
  # Verify a V2 artefact by full hash ID
  holt verify a3f2b9c4e8d6f1a7b5c3e9d2f4a8b6c1e7d3f9a2b8c4e6d1f7a3b9c5e2d8f4a1

  # Verify using short hash (first 8+ characters)
  holt verify a3f2b9c4

  # Verify artefact in specific instance
  holt verify --name prod-instance a3f2b9c4

Note: This command only works with V2 (hash-based) artefacts.
      V1 (UUID-based) artefacts do not support cryptographic verification.`,
	Args: cobra.ExactArgs(1),
	RunE: runVerify,
}

func init() {
	verifyCmd.Flags().StringVarP(&verifyInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	artefactID := args[0]

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := verifyInstanceName
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
						"Specify which instance to verify:\n  holt verify --name <instance-name> " + artefactID,
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

	// Phase 3: Get Redis port
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return printer.ErrorWithContext(
			"Redis port not found",
			fmt.Sprintf("Instance '%s' exists but Redis port label is missing.", targetInstanceName),
			nil,
			[]string{fmt.Sprintf("Restart the instance:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName)},
		)
	}

	// Phase 4: Connect to blackboard
	redisURL := instance.GetRedisURL(redisPort)
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	bbClient, err := blackboard.NewClient(redisOpts, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to create blackboard client: %w", err)
	}
	defer bbClient.Close()

	// Verify Redis connectivity
	if err := bbClient.Ping(ctx); err != nil {
		return printer.ErrorWithContext(
			"Redis connection failed",
			fmt.Sprintf("Could not connect to Redis at %s", redisURL),
			nil,
			[]string{
				fmt.Sprintf("Check Redis container status:\n  docker logs holt-redis-%s", targetInstanceName),
				fmt.Sprintf("Restart if needed:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName),
			},
		)
	}

	// Phase 5: Resolve short ID if necessary (V2 artefacts use 64-char hex hashes)
	fullID := artefactID
	if len(artefactID) < 64 {
		// Short hash provided - need to resolve to full hash
		// For V2, we'll scan artefact keys matching the prefix
		resolvedID, err := resolveV2ShortHash(ctx, bbClient, targetInstanceName, artefactID)
		if err != nil {
			return err
		}
		fullID = resolvedID
		fmt.Fprintf(os.Stdout, "Resolved short hash '%s' to full ID:\n  %s\n\n", artefactID, fullID)
	}

	// Phase 6: Fetch V2 artefact
	artefact, err := bbClient.GetVerifiableArtefact(ctx, fullID)
	if err != nil {
		if blackboard.IsNotFound(err) {
			return printer.Error(
				fmt.Sprintf("artefact '%s' not found", fullID),
				"The artefact does not exist or is not a V2 (hash-based) artefact.",
				[]string{
					"List V2 artefacts:\n  holt hoard --output=jsonl | jq 'select(.id | length == 64)'",
					"Note: V1 (UUID) artefacts cannot be cryptographically verified.",
				},
			)
		}
		return fmt.Errorf("failed to fetch artefact: %w", err)
	}

	// Phase 7: Verify hash
	fmt.Fprintf(os.Stdout, "Verifying artefact %s...\n\n", fullID[:16]+"...")

	if err := blackboard.ValidateArtefactHash(artefact); err != nil {
		// Hash mismatch detected
		var mismatchErr *blackboard.HashMismatchError
		if blackboard.IsHashMismatchError(err, &mismatchErr) {
			fmt.Fprintf(os.Stderr, "✗ Hash verification FAILED\n\n")
			fmt.Fprintf(os.Stderr, "  Stored ID:    %s\n", artefact.ID)
			fmt.Fprintf(os.Stderr, "  Computed:     %s\n\n", mismatchErr.Expected)
			fmt.Fprintf(os.Stderr, "CRITICAL: This artefact has been tampered with or corrupted!\n\n")
			fmt.Fprintf(os.Stderr, "Artefact details:\n")
			fmt.Fprintf(os.Stderr, "  Type:         %s\n", artefact.Header.Type)
			fmt.Fprintf(os.Stderr, "  Producer:     %s\n", artefact.Header.ProducedByRole)
			fmt.Fprintf(os.Stderr, "  Created:      %d (Unix ms)\n", artefact.Header.CreatedAtMs)
			fmt.Fprintf(os.Stderr, "  Parents:      %v\n", artefact.Header.ParentHashes)
			fmt.Fprintf(os.Stderr, "  Payload size: %d bytes\n\n", len(artefact.Payload.Content))
			fmt.Fprintf(os.Stderr, "Immediate actions:\n")
			fmt.Fprintf(os.Stderr, "  1. Check security alerts: holt security --alerts\n")
			fmt.Fprintf(os.Stderr, "  2. Inspect orchestrator logs: holt logs orchestrator\n")
			fmt.Fprintf(os.Stderr, "  3. Contact security team immediately\n")
			return fmt.Errorf("hash verification failed")
		}
		return fmt.Errorf("verification failed: %w", err)
	}

	// Phase 8: Success - display verification details
	fmt.Fprintf(os.Stdout, "✓ Hash verification PASSED\n\n")
	fmt.Fprintf(os.Stdout, "  Stored ID:    %s\n", artefact.ID)
	fmt.Fprintf(os.Stdout, "  Computed:     %s\n\n", artefact.ID)
	fmt.Fprintf(os.Stdout, "Artefact details:\n")
	fmt.Fprintf(os.Stdout, "  Type:         %s\n", artefact.Header.Type)
	fmt.Fprintf(os.Stdout, "  Producer:     %s\n", artefact.Header.ProducedByRole)
	fmt.Fprintf(os.Stdout, "  Created:      %d (Unix ms)\n", artefact.Header.CreatedAtMs)
	fmt.Fprintf(os.Stdout, "  Version:      %d\n", artefact.Header.Version)
	fmt.Fprintf(os.Stdout, "  Thread ID:    %s\n", artefact.Header.LogicalThreadID)
	fmt.Fprintf(os.Stdout, "  Parents:      %v\n", artefact.Header.ParentHashes)
	fmt.Fprintf(os.Stdout, "  Payload size: %d bytes\n", len(artefact.Payload.Content))

	return nil
}

// resolveV2ShortHash resolves a short hash prefix to a full V2 artefact hash.
// Returns an error if no match or multiple matches found.
func resolveV2ShortHash(ctx context.Context, client *blackboard.Client, instanceName, shortHash string) (string, error) {
	// Scan for V2 artefact keys matching the prefix
	// V2 artefacts are stored at: holt:{instance}:artefact:{hash}
	pattern := fmt.Sprintf("holt:%s:artefact:%s*", instanceName, shortHash)

	matches, err := client.ScanKeys(ctx, pattern)
	if err != nil {
		return "", fmt.Errorf("failed to scan for matching artefacts: %w", err)
	}

	if len(matches) == 0 {
		return "", printer.Error(
			fmt.Sprintf("no V2 artefacts found matching '%s'", shortHash),
			"No hash-based artefacts match this prefix.",
			[]string{
				"Verify the hash prefix is correct",
				"List all artefacts: holt hoard",
			},
		)
	}

	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "Ambiguous short hash '%s' matches multiple artefacts:\n\n", shortHash)
		for _, key := range matches {
			// Extract hash from key: holt:{instance}:artefact:{hash}
			parts := len("holt:" + instanceName + ":artefact:")
			if len(key) > parts {
				hash := key[parts:]
				fmt.Fprintf(os.Stderr, "  %s\n", hash)
			}
		}
		fmt.Fprintf(os.Stderr, "\nProvide more characters to uniquely identify the artefact.\n")
		return "", fmt.Errorf("ambiguous short hash")
	}

	// Extract full hash from the single match
	key := matches[0]
	prefix := "holt:" + instanceName + ":artefact:"
	if len(key) <= len(prefix) {
		return "", fmt.Errorf("invalid artefact key format: %s", key)
	}

	return key[len(prefix):], nil
}
