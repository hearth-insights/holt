package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/internal/timespec"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	securityInstanceName string
	securityAlerts       bool
	securitySince        string
	securityWatch        bool
	securityUnlock       bool
	securityReason       string
)

var securityCmd = &cobra.Command{
	Use:   "security",
	Short: "Monitor and manage security events (M4.6)",
	Long: `Monitor security alerts and manage global lockdown state.

M4.6 introduces cryptographic verification of the artefact ledger. When tampering
is detected (hash mismatch or orphan block), the orchestrator triggers a global
lockdown that halts all processing for forensic investigation.

This command provides two sub-operations:

1. Monitor Alerts (--alerts):
   View security alerts from the permanent audit log and/or live stream.
   Supports time filtering (--since) and real-time monitoring (--watch).

2. Unlock Lockdown (--unlock --reason):
   Clear global lockdown after investigation. Creates audited override event.
   Requires explicit reason for compliance audit trail.

Alert Types:
  - hash_mismatch:    Artefact ID doesn't match computed hash (tampering)
  - orphan_block:     Artefact references non-existent parent (DAG corruption)
  - timestamp_drift:  Artefact timestamp >5min from orchestrator clock
  - security_override: Lockdown cleared by operator (recovery event)

Examples:
  # Stream live security alerts
  holt security --alerts --watch

  # View alerts from last hour
  holt security --alerts --since=1h

  # View historical alerts then stream new ones
  holt security --alerts --since=24h --watch

  # Clear lockdown after forensic investigation
  holt security --unlock --reason "Investigation complete: memory corruption in agent container, container replaced"

Note: Global lockdown halts the entire orchestrator. Agent containers remain
      running for forensic analysis. Only unlock after thorough investigation.`,
	RunE: runSecurity,
}

func init() {
	securityCmd.Flags().StringVarP(&securityInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	securityCmd.Flags().BoolVar(&securityAlerts, "alerts", false, "Monitor security alerts")
	securityCmd.Flags().StringVar(&securitySince, "since", "", "Show alerts after time (duration or RFC3339)")
	securityCmd.Flags().BoolVar(&securityWatch, "watch", false, "Stream live alerts (use with --alerts)")
	securityCmd.Flags().BoolVar(&securityUnlock, "unlock", false, "Clear global lockdown")
	securityCmd.Flags().StringVar(&securityReason, "reason", "", "Reason for unlock (required with --unlock)")

	rootCmd.AddCommand(securityCmd)
}

func runSecurity(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Validate flags
	if !securityAlerts && !securityUnlock {
		return printer.Error(
			"no operation specified",
			"You must specify either --alerts or --unlock.",
			[]string{
				"Monitor alerts:\n  holt security --alerts [--watch]",
				"Clear lockdown:\n  holt security --unlock --reason \"<text>\"",
				"See help:\n  holt security --help",
			},
		)
	}

	if securityAlerts && securityUnlock {
		return printer.Error(
			"conflicting flags",
			"Cannot use --alerts and --unlock together.",
			[]string{
				"Use separate commands for monitoring and unlocking",
			},
		)
	}

	if securityUnlock && securityReason == "" {
		return printer.Error(
			"missing reason",
			"--unlock requires --reason flag for audit trail.",
			[]string{
				"Provide investigation summary:\n  holt security --unlock --reason \"Investigation complete: ...\"",
			},
		)
	}

	if securityWatch && !securityAlerts {
		return printer.Error(
			"invalid flag combination",
			"--watch can only be used with --alerts.",
			[]string{
				"Correct usage:\n  holt security --alerts --watch",
			},
		)
	}

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := securityInstanceName
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
						"Specify which instance:\n  holt security --name <instance-name> ...",
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

	// Phase 5: Execute operation
	if securityAlerts {
		return runAlertsMonitoring(ctx, bbClient, targetInstanceName)
	}

	if securityUnlock {
		return runUnlockLockdown(ctx, bbClient, targetInstanceName)
	}

	return nil
}

// runAlertsMonitoring monitors security alerts (historical + live stream).
func runAlertsMonitoring(ctx context.Context, client *blackboard.Client, instanceName string) error {
	// Parse --since filter
	var sinceMS int64
	if securitySince != "" {
		parsed, _, err := timespec.ParseRange(securitySince, "")
		if err != nil {
			return printer.Error(
				"invalid time filter",
				err.Error(),
				[]string{"Use duration format like '1h30m' or RFC3339 like '2025-10-29T13:00:00Z'"},
			)
		}
		sinceMS = parsed
	}

	// Phase 1: Historical alerts (if --since specified or not --watch-only)
	var alertCount int
	if securitySince != "" || !securityWatch {
		alerts, err := client.GetSecurityAlerts(ctx, sinceMS)
		if err != nil {
			return fmt.Errorf("failed to fetch historical alerts: %w", err)
		}

		alertCount = len(alerts)

		if len(alerts) == 0 {
			if securitySince != "" {
				fmt.Fprintf(os.Stdout, "No security alerts found since %s\n", securitySince)
			} else {
				fmt.Fprintf(os.Stdout, "No security alerts in audit log\n")
			}
		} else {
			fmt.Fprintf(os.Stdout, "Historical security alerts:\n\n")
			for _, alert := range alerts {
				printAlert(alert)
			}
		}
	}

	// Phase 2: Live streaming (if --watch)
	if securityWatch {
		if securitySince != "" || alertCount > 0 {
			fmt.Fprintf(os.Stdout, "\n--- Streaming live alerts (Ctrl+C to stop) ---\n\n")
		} else {
			fmt.Fprintf(os.Stdout, "Streaming live security alerts (Ctrl+C to stop)...\n\n")
		}

		// Subscribe to security alerts channel
		pubsub := client.SubscribeSecurityAlerts(ctx)
		defer pubsub.Close()

		// Read from channel
		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case msg := <-ch:
				if msg == nil {
					return fmt.Errorf("channel closed unexpectedly")
				}

				// Parse and print alert
				var alert blackboard.SecurityAlert
				if err := json.Unmarshal([]byte(msg.Payload), &alert); err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  Failed to parse alert: %v\n", err)
					continue
				}

				printAlert(alert)
			}
		}
	}

	return nil
}

// runUnlockLockdown clears the global lockdown after investigation.
func runUnlockLockdown(ctx context.Context, client *blackboard.Client, instanceName string) error {
	// Check if lockdown is active
	lockdownAlert, err := client.GetLockdownState(ctx)
	if err != nil {
		if blackboard.IsNotFound(err) {
			fmt.Fprintf(os.Stdout, "✓ No active lockdown\n\nThe orchestrator is not in lockdown state.\n")
			return nil
		}
		return fmt.Errorf("failed to check lockdown state: %w", err)
	}

	// Display current lockdown state
	fmt.Fprintf(os.Stdout, "Current lockdown state:\n\n")
	printAlert(lockdownAlert)
	fmt.Fprintf(os.Stdout, "\n--- Clearing lockdown ---\n\n")

	// Create security override event
	overrideAlert := blackboard.SecurityAlert{
		Type:        "security_override",
		TimestampMs: time.Now().UnixMilli(),
		Action:      "lockdown_cleared",
		Reason:      securityReason,
		Operator:    "admin", // TODO: Get from environment or user context
	}

	// Execute unlock (3-step: LPUSH log + DEL lockdown + PUBLISH)
	if err := client.UnlockGlobalLockdown(ctx, overrideAlert); err != nil {
		return fmt.Errorf("failed to unlock lockdown: %w", err)
	}

	// Success
	fmt.Fprintf(os.Stdout, "✓ Lockdown cleared\n\n")
	fmt.Fprintf(os.Stdout, "  Reason:   %s\n", securityReason)
	fmt.Fprintf(os.Stdout, "  Operator: admin\n")
	fmt.Fprintf(os.Stdout, "  Override event logged to audit trail\n")
	fmt.Fprintf(os.Stdout, "  Orchestrator will resume processing on next event loop iteration\n")

	return nil
}

// printAlert formats and prints a security alert to stdout.
func printAlert(alert blackboard.SecurityAlert) {
	timestamp := time.UnixMilli(alert.TimestampMs).Format("2006-01-02 15:04:05 MST")

	switch alert.Type {
	case "hash_mismatch":
		fmt.Fprintf(os.Stdout, "[%s] 🚨 TAMPER DETECTED - SYSTEM LOCKED DOWN\n", timestamp)
		fmt.Fprintf(os.Stdout, "  Type:          hash_mismatch\n")
		fmt.Fprintf(os.Stdout, "  Agent:         %s\n", alert.AgentRole)
		fmt.Fprintf(os.Stdout, "  Claimed hash:  %s\n", alert.ArtefactIDClaimed)
		fmt.Fprintf(os.Stdout, "  Expected hash: %s\n", alert.HashExpected)
		fmt.Fprintf(os.Stdout, "  Action:        %s\n", alert.OrchestratorAction)
		if alert.ClaimID != "" {
			fmt.Fprintf(os.Stdout, "  Claim ID:      %s\n", alert.ClaimID)
		}
		fmt.Fprintf(os.Stdout, "  Recovery:      holt security --unlock --reason \"<investigation summary>\"\n")

	case "orphan_block":
		fmt.Fprintf(os.Stdout, "[%s] 🚨 ORPHAN BLOCK DETECTED - SYSTEM LOCKED DOWN\n", timestamp)
		fmt.Fprintf(os.Stdout, "  Type:          orphan_block\n")
		fmt.Fprintf(os.Stdout, "  Agent:         %s\n", alert.AgentRole)
		fmt.Fprintf(os.Stdout, "  Artefact ID:   %s\n", alert.ArtefactID)
		fmt.Fprintf(os.Stdout, "  Missing parent: %s\n", alert.MissingParentHash)
		fmt.Fprintf(os.Stdout, "  Action:        %s\n", alert.OrchestratorAction)
		if alert.ClaimID != "" {
			fmt.Fprintf(os.Stdout, "  Claim ID:      %s\n", alert.ClaimID)
		}
		fmt.Fprintf(os.Stdout, "  Recovery:      holt security --unlock --reason \"<investigation summary>\"\n")

	case "timestamp_drift":
		fmt.Fprintf(os.Stdout, "[%s] ⚠️  TIMESTAMP DRIFT WARNING\n", timestamp)
		fmt.Fprintf(os.Stdout, "  Type:          timestamp_drift\n")
		fmt.Fprintf(os.Stdout, "  Agent:         %s\n", alert.AgentRole)
		fmt.Fprintf(os.Stdout, "  Artefact ID:   %s\n", alert.ArtefactID)
		fmt.Fprintf(os.Stdout, "  Artefact time: %d (Unix ms)\n", alert.ArtefactTimestampMs)
		fmt.Fprintf(os.Stdout, "  Orchestrator:  %d (Unix ms)\n", alert.OrchestratorTimestampMs)
		fmt.Fprintf(os.Stdout, "  Drift:         %d ms (%s threshold)\n", alert.DriftMs, formatDriftThreshold(alert.ThresholdMs))
		fmt.Fprintf(os.Stdout, "  Action:        %s\n", alert.OrchestratorAction)
		fmt.Fprintf(os.Stdout, "  Note:          Check NTP synchronization on agent containers\n")

	case "security_override":
		fmt.Fprintf(os.Stdout, "[%s] ✓ LOCKDOWN CLEARED\n", timestamp)
		fmt.Fprintf(os.Stdout, "  Type:          security_override\n")
		fmt.Fprintf(os.Stdout, "  Action:        %s\n", alert.Action)
		fmt.Fprintf(os.Stdout, "  Reason:        %s\n", alert.Reason)
		fmt.Fprintf(os.Stdout, "  Operator:      %s\n", alert.Operator)

	default:
		// Unknown alert type - print raw JSON
		fmt.Fprintf(os.Stdout, "[%s] UNKNOWN ALERT TYPE: %s\n", timestamp, alert.Type)
		alertJSON, _ := json.MarshalIndent(alert, "  ", "  ")
		fmt.Fprintf(os.Stdout, "  %s\n", string(alertJSON))
	}

	fmt.Fprintf(os.Stdout, "\n")
}

// formatDriftThreshold formats the drift threshold in human-readable format.
func formatDriftThreshold(thresholdMs int64) string {
	seconds := thresholdMs / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	return fmt.Sprintf("%dm", minutes)
}
