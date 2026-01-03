package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	answerInstanceName  string
	answerThenQuestions bool
)

var answerCmd = &cobra.Command{
	Use:   "answer <question-id> \"<answer-text>\" [flags]",
	Short: "Respond to a Question by creating a new version of the questioned artefact",
	Long: `Respond to a Question by creating a new version of the questioned artefact with clarified content.

The answer is embodied as a new version of the original artefact, with incremented version number
and the Question ID in source_artefacts. This triggers the orchestrator to create a new claim
and continue the workflow.

Arguments:
  <question-id>   ID of the Question artefact (supports prefix matching, minimum 6 characters)
  <answer-text>   The clarified text for the new artefact version (multi-line supported)

Flags:
  --then-questions   After answering, immediately run 'holt questions' to watch for next question

Examples:
  # Basic usage
  holt answer abc-123 "Build REST API with JWT auth (not OAuth)"

  # Multi-line answer
  holt answer def-456 "Requirements:
  1. Support null values
  2. Return 400 for invalid input
  3. Document edge cases"

  # Answer and watch for next question
  holt answer abc-123 "Clarified text here" --then-questions`,
	RunE: runAnswer,
	Args: cobra.ExactArgs(2),
}

func init() {
	answerCmd.Flags().StringVarP(&answerInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	answerCmd.Flags().BoolVar(&answerThenQuestions, "then-questions", false, "After answering, run 'holt questions'")

	rootCmd.AddCommand(answerCmd)
}

func runAnswer(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	questionIDPrefix := args[0]
	answerText := args[1]

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := answerInstanceName
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

	// Phase 4: Find and validate Question ID
	questionID, err := findQuestionByPrefix(ctx, bbClient, questionIDPrefix)
	if err != nil {
		return err
	}

	// Phase 5: Load Question artefact
	questionArtefact, err := bbClient.GetArtefact(ctx, questionID)
	if err != nil {
		if blackboard.IsNotFound(err) {
			return printer.Error(
				"question not found",
				fmt.Sprintf("Question %s not found", questionID),
				[]string{"Run 'holt questions' to see available questions"},
			)
		}
		return fmt.Errorf("failed to load question artefact: %w", err)
	}

	// Phase 6: Parse Question payload to get target artefact ID
	var payload struct {
		QuestionText     string `json:"question_text"`
		TargetArtefactID string `json:"target_artefact_id"`
	}
	if err := json.Unmarshal([]byte(questionArtefact.Payload.Content), &payload); err != nil {
		return printer.Error(
			"invalid question payload",
			fmt.Sprintf("Failed to parse Question payload: %v", err),
			nil,
		)
	}

	// Phase 7: Load target artefact
	targetArtefact, err := bbClient.GetArtefact(ctx, payload.TargetArtefactID)
	if err != nil {
		if blackboard.IsNotFound(err) {
			return printer.Error(
				"target artefact not found",
				fmt.Sprintf("Target artefact %s not found (may have been deleted)", payload.TargetArtefactID),
				[]string{"This Question cannot be answered - consider creating a new goal"},
			)
		}
		return fmt.Errorf("failed to load target artefact: %w", err)
	}

	// Phase 8: Create new artefact version with answer
	// Construct V2-compatible artefact to compute hash
	newArtefact := &blackboard.Artefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{targetArtefact.ID, questionArtefact.ID}, // Link both
			LogicalThreadID: targetArtefact.Header.LogicalThreadID,            // Same logical thread
			Version:         targetArtefact.Header.Version + 1,                // Incremented version
			CreatedAtMs:     now(),
			ProducedByRole:  "user", // Human-produced
			StructuralType:  targetArtefact.Header.StructuralType,
			Type:            targetArtefact.Header.Type,
			ClaimID:         "", // User answer, no claim
			Metadata:        "{}",
		},
		Payload: blackboard.ArtefactPayload{
			Content: answerText, // Clarified text replaces old payload
		},
	}

	hash, err := blackboard.ComputeArtefactHash(newArtefact)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}
	newArtefact.ID = hash

	if err := bbClient.CreateArtefact(ctx, newArtefact); err != nil {
		return fmt.Errorf("failed to create answer artefact: %w", err)
	}

	// Phase 9: Display success message
	fmt.Printf("✓ Answer created: %s v%d\n", shortenID(newArtefact.ID), newArtefact.Header.Version)
	fmt.Printf("  Answered question: %s\n", shortenID(questionID))
	fmt.Printf("  Original artefact: %s v%d\n", shortenID(targetArtefact.ID), targetArtefact.Header.Version)

	// Phase 10: Chain to holt questions if requested
	if answerThenQuestions {
		fmt.Println("\n--- Watching for next question ---")
		return runQuestionsDefault(ctx, bbClient, targetInstanceName)
	}

	return nil
}

// findQuestionByPrefix finds a Question artefact by ID prefix.
// Returns error if prefix is ambiguous or not found.
func findQuestionByPrefix(ctx context.Context, client *blackboard.Client, prefix string) (string, error) {
	// Get all unanswered questions
	unanswered, err := getUnansweredQuestions(ctx, client)
	if err != nil {
		return "", fmt.Errorf("failed to get unanswered questions: %w", err)
	}

	if len(unanswered) == 0 {
		return "", printer.Error(
			"no unanswered questions",
			"No unanswered questions exist",
			[]string{"Run 'holt questions' to monitor for new questions"},
		)
	}

	// Find matching questions by prefix
	var matches []string
	for _, q := range unanswered {
		if strings.HasPrefix(q.ID, prefix) {
			matches = append(matches, q.ID)
		}
	}

	if len(matches) == 0 {
		return "", printer.Error(
			"question not found",
			fmt.Sprintf("No question found matching prefix '%s'", prefix),
			[]string{"Run 'holt questions' to see available questions"},
		)
	}

	if len(matches) > 1 && len(prefix) < 6 {
		return "", printer.Error(
			"ambiguous question ID",
			fmt.Sprintf("Multiple questions exist (%d found). Provide at least 6 characters of the question ID.", len(matches)),
			[]string{"Run 'holt questions --since 1h' to see all questions with full IDs"},
		)
	}

	// Return first match (if prefix >= 6 chars, any match is acceptable)
	return matches[0], nil
}

// now returns current Unix timestamp in milliseconds.
func now() int64 {
	return time.Now().UnixMilli()
}
