package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dockerpkg "github.com/dyluth/holt/internal/docker"
	"github.com/dyluth/holt/internal/instance"
	"github.com/dyluth/holt/internal/printer"
	"github.com/dyluth/holt/internal/timespec"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	questionsInstanceName  string
	questionsWatch         bool
	questionsExitOnComplete bool
	questionsSince         string
	questionsOutputFormat  string
)

var questionsCmd = &cobra.Command{
	Use:   "questions [flags]",
	Short: "Display unanswered Question artefacts",
	Long: `Display unanswered Question artefacts, with focus on those requiring human input.

Default behavior (no flags):
  - Display the OLDEST unanswered Question artefact
  - If none exist, wait for a new Question to appear, then display it and exit

Flags:
  --watch              Continuously display Questions as they appear (stream mode)
  --exit-on-complete   Used with --watch. Exit when Terminal artefact is created
  --since <duration>   Display ALL unanswered Questions from time range (e.g., 1h, 30m, 2d)
  --output jsonl       Output Questions as line-delimited JSON for scripting

Examples:
  # Show oldest unanswered question or wait for new one
  holt questions

  # Watch for questions continuously
  holt questions --watch

  # List all unanswered questions from last hour
  holt questions --since 1h

  # Stream questions as JSONL until workflow completes
  holt questions --watch --exit-on-complete --output jsonl`,
	RunE: runQuestions,
}

func init() {
	questionsCmd.Flags().StringVarP(&questionsInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	questionsCmd.Flags().BoolVar(&questionsWatch, "watch", false, "Continuously display Questions as they appear")
	questionsCmd.Flags().BoolVar(&questionsExitOnComplete, "exit-on-complete", false, "Exit with code 0 when Terminal artefact detected")
	questionsCmd.Flags().StringVar(&questionsSince, "since", "", "Show questions from time range (duration or RFC3339)")
	questionsCmd.Flags().StringVarP(&questionsOutputFormat, "output", "o", "default", "Output format (default or jsonl)")

	rootCmd.AddCommand(questionsCmd)
}

func runQuestions(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Validate output format
	if questionsOutputFormat != "default" && questionsOutputFormat != "jsonl" && questionsOutputFormat != "json" {
		return printer.Error(
			"invalid output format",
			fmt.Sprintf("Unknown format: %s", questionsOutputFormat),
			[]string{"Valid formats: default, jsonl, json"},
		)
	}

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := questionsInstanceName
	if targetInstanceName == "" {
		targetInstanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			return handleInstanceDiscoveryError(err)
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

	// Phase 3: Get Redis port and connect
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return printer.ErrorWithContext(
			"Redis port not found",
			fmt.Sprintf("Instance '%s' exists but Redis port label is missing.", targetInstanceName),
			nil,
			[]string{fmt.Sprintf("Restart the instance:\n  holt down --name %s\n  holt up --name %s", targetInstanceName, targetInstanceName)},
		)
	}

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
			},
		)
	}

	// Phase 4: Execute based on flags
	if questionsSince != "" {
		// Historical listing mode
		return runQuestionsHistorical(ctx, bbClient, targetInstanceName)
	} else if questionsWatch {
		// Streaming mode
		return runQuestionsWatch(ctx, bbClient, targetInstanceName)
	} else {
		// Default mode: show oldest or wait for new
		return runQuestionsDefault(ctx, bbClient, targetInstanceName)
	}
}

// runQuestionsDefault shows the oldest unanswered Question, or waits for a new one.
func runQuestionsDefault(ctx context.Context, client *blackboard.Client, instanceName string) error {
	// Get all unanswered questions
	unanswered, err := getUnansweredQuestions(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to get unanswered questions: %w", err)
	}

	if len(unanswered) > 0 {
		// Display oldest and exit
		oldest := unanswered[0]
		displayQuestion(oldest, questionsOutputFormat)
		return nil
	}

	// No questions yet - wait for new one
	if questionsOutputFormat == "default" {
		fmt.Println("No unanswered questions. Waiting for new questions...")
	}

	return waitForNewQuestion(ctx, client, instanceName)
}

// runQuestionsWatch streams Questions as they appear.
func runQuestionsWatch(ctx context.Context, client *blackboard.Client, instanceName string) error {
	// Subscribe to artefact events
	subscription, err := client.SubscribeArtefactEvents(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to artefact events: %w", err)
	}
	defer subscription.Close()

	for {
		select {
		case <-ctx.Done():
			return nil

		case artefact, ok := <-subscription.Events():
			if !ok {
				return nil
			}

			// Check if it's a Question
			if artefact.StructuralType != blackboard.StructuralTypeQuestion {
				// If exit-on-complete is enabled, check for Terminal
				if questionsExitOnComplete && artefact.StructuralType == blackboard.StructuralTypeTerminal {
					return nil
				}
				continue
			}

			// Display the question
			displayQuestion(artefact, questionsOutputFormat)

		case err, ok := <-subscription.Errors():
			if !ok {
				return nil
			}
			return fmt.Errorf("subscription error: %w", err)
		}
	}
}

// runQuestionsHistorical displays all unanswered Questions from a time range.
func runQuestionsHistorical(ctx context.Context, client *blackboard.Client, instanceName string) error {
	// Parse time filter
	sinceMS, _, err := timespec.ParseRange(questionsSince, "")
	if err != nil {
		return printer.Error(
			"invalid time filter",
			err.Error(),
			[]string{"Use duration format like '1h30m' or RFC3339 like '2025-10-29T13:00:00Z'"},
		)
	}

	// Get all unanswered questions
	unanswered, err := getUnansweredQuestions(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to get unanswered questions: %w", err)
	}

	// Filter by time
	var filtered []*blackboard.Artefact
	for _, q := range unanswered {
		if q.CreatedAtMs >= sinceMS {
			filtered = append(filtered, q)
		}
	}

	if len(filtered) == 0 {
		if questionsOutputFormat == "default" {
			duration := questionsSince
			fmt.Printf("No unanswered questions found in the last %s.\n", duration)
		}
		return nil
	}

	// Display all filtered questions
	if questionsOutputFormat == "default" {
		fmt.Printf("Unanswered Questions (last %s):\n\n", questionsSince)
		for i, q := range filtered {
			fmt.Printf("%d. ", i+1)
			displayQuestion(q, questionsOutputFormat)
			fmt.Println()
		}
		fmt.Printf("Answer with: holt answer <id> \"your clarified text\"\n")
	} else {
		for _, q := range filtered {
			displayQuestion(q, questionsOutputFormat)
		}
	}

	return nil
}

// waitForNewQuestion waits for a new Question artefact to appear.
func waitForNewQuestion(ctx context.Context, client *blackboard.Client, instanceName string) error {
	subscription, err := client.SubscribeArtefactEvents(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to artefact events: %w", err)
	}
	defer subscription.Close()

	for {
		select {
		case <-ctx.Done():
			return nil

		case artefact, ok := <-subscription.Events():
			if !ok {
				return nil
			}

			if artefact.StructuralType == blackboard.StructuralTypeQuestion {
				displayQuestion(artefact, questionsOutputFormat)
				return nil
			}

		case err, ok := <-subscription.Errors():
			if !ok {
				return nil
			}
			return fmt.Errorf("subscription error: %w", err)
		}
	}
}

// getUnansweredQuestions retrieves all unanswered Question artefacts.
// A Question Q about target T is unanswered if NO artefact exists with:
// - logical_id == T.logical_id
// - version > T.version
// - Q.id in source_artefacts
func getUnansweredQuestions(ctx context.Context, client *blackboard.Client) ([]*blackboard.Artefact, error) {
	// Get all artefacts (we need to scan for Questions)
	// Note: In production, this could be optimized with a dedicated Question index
	allArtefacts, err := getAllArtefacts(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get all artefacts: %w", err)
	}

	// Find all Question artefacts
	var questions []*blackboard.Artefact
	for _, art := range allArtefacts {
		if art.StructuralType == blackboard.StructuralTypeQuestion {
			questions = append(questions, art)
		}
	}

	// For each Question, check if it's been answered
	var unanswered []*blackboard.Artefact
	for _, q := range questions {
		if isQuestionUnanswered(ctx, client, q, allArtefacts) {
			unanswered = append(unanswered, q)
		}
	}

	// Sort by creation time (oldest first)
	sort.Slice(unanswered, func(i, j int) bool {
		return unanswered[i].CreatedAtMs < unanswered[j].CreatedAtMs
	})

	return unanswered, nil
}

// isQuestionUnanswered checks if a Question has been answered.
func isQuestionUnanswered(ctx context.Context, client *blackboard.Client, question *blackboard.Artefact, allArtefacts []*blackboard.Artefact) bool {
	// Parse Question payload to get target artefact ID
	var payload struct {
		TargetArtefactID string `json:"target_artefact_id"`
	}
	if err := json.Unmarshal([]byte(question.Payload), &payload); err != nil {
		return true // Can't parse - treat as unanswered
	}

	// Load target artefact
	targetArtefact, err := client.GetArtefact(ctx, payload.TargetArtefactID)
	if err != nil {
		return true // Target not found - treat as unanswered
	}

	// Check if any artefact exists with:
	// - Same logical_id as target
	// - Higher version than target
	// - Question ID in source_artefacts
	for _, art := range allArtefacts {
		if art.LogicalID == targetArtefact.LogicalID &&
			art.Version > targetArtefact.Version &&
			containsString(art.SourceArtefacts, question.ID) {
			return false // Found an answer
		}
	}

	return true // No answer found
}

// displayQuestion formats and displays a Question artefact.
func displayQuestion(q *blackboard.Artefact, format string) {
	if format == "jsonl" || format == "json" {
		// JSONL output
		data, _ := json.Marshal(q)
		fmt.Println(string(data))
		return
	}

	// Default human-readable output
	var payload struct {
		QuestionText     string `json:"question_text"`
		TargetArtefactID string `json:"target_artefact_id"`
	}
	_ = json.Unmarshal([]byte(q.Payload), &payload)

	// Shorten IDs for display
	questionIDShort := shortenID(q.ID)
	targetIDShort := shortenID(payload.TargetArtefactID)

	fmt.Printf("Question %s (about artefact %s)\n", questionIDShort, targetIDShort)
	fmt.Printf("  Asked by: %s\n", q.ProducedByRole)
	if payload.QuestionText != "" {
		fmt.Printf("  Question: \"%s\"\n", payload.QuestionText)
	}
	fmt.Printf("\n  Answer with: holt answer %s \"your clarified requirements\"\n", questionIDShort)
}

// getAllArtefacts retrieves all artefacts from Redis.
// Note: This is inefficient for large datasets and should be optimized in production.
func getAllArtefacts(ctx context.Context, client *blackboard.Client) ([]*blackboard.Artefact, error) {
	// Use the internal Redis client to scan for artefact keys
	// This is a workaround since blackboard.Client doesn't expose a ListAllArtefacts method
	// In production, we'd add a proper query method to the blackboard package

	rdb := client.GetRedisClient()
	pattern := fmt.Sprintf("holt:%s:artefact:*", client.GetInstanceName())

	var artefacts []*blackboard.Artefact
	iter := rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract artefact ID from key (format: holt:instance:artefact:id)
		parts := strings.Split(key, ":")
		if len(parts) < 4 {
			continue
		}
		artefactID := parts[3]

		// Load artefact
		art, err := client.GetArtefact(ctx, artefactID)
		if err != nil {
			continue
		}
		artefacts = append(artefacts, art)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan artefacts: %w", err)
	}

	return artefacts, nil
}

// shortenID returns the first 8 characters of a UUID for display.
func shortenID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// containsString checks if a slice contains a string.
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// handleInstanceDiscoveryError formats instance discovery errors.
func handleInstanceDiscoveryError(err error) error {
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
				"Specify which instance to use:\n  holt questions --name <instance-name>",
				"List instances:\n  holt list",
			},
		)
	}
	return fmt.Errorf("failed to infer instance: %w", err)
}
