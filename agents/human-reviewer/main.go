package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Input contract from pup (stdin)
type Input struct {
	ClaimType         string     `json:"claim_type"`
	TargetArtefact    Artefact   `json:"target_artefact"`
	ContextChain      []Artefact `json:"context_chain"`
	AdditionalContext []Artefact `json:"additional_context"`
}

// Artefact represents a blackboard artefact
type Artefact struct {
	ID              string   `json:"id"`
	LogicalID       string   `json:"logical_id"`
	Version         int      `json:"version"`
	StructuralType  string   `json:"structural_type"`
	Type            string   `json:"type"`
	Payload         string   `json:"payload"`
	SourceArtefacts []string `json:"source_artefacts"`
	ProducedByRole  string   `json:"produced_by_role"`
}

// Output contract to pup (FD 3)
type Output struct {
	StructuralType  string `json:"structural_type"`
	ArtefactType    string `json:"artefact_type"`
	ArtefactPayload string `json:"artefact_payload"`
	Summary         string `json:"summary"`
}

// ReviewPayload for rejection feedback
type ReviewPayload struct {
	Feedback string `json:"feedback"`
}

func main() {
	// M4.10: Open FD 3 for result JSON output
	fd3 := os.NewFile(uintptr(3), "/dev/fd/3")
	if fd3 == nil {
		log.Fatalf("[HumanReviewer] Failed to open FD 3 for result output")
	}
	defer fd3.Close()

	if err := Run(os.Stdin, fd3, os.Stderr, os.Getenv); err != nil {
		log.Fatalf("[HumanReviewer] Error: %v", err)
	}
}

// Run is the testable entry point
func Run(stdin io.Reader, stdout, stderr io.Writer, getEnv func(string) string) error {
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Get configuration from environment
	autoApprove := getEnv("AUTO_APPROVE") == "true"
	timeoutSeconds := getTimeoutSeconds(getEnv)

	// Read input from stdin
	// Use a buffered reader for both JSON and user input to avoid losing data due to buffering
	reader := bufio.NewReader(stdin)

	var input Input
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("failed to decode stdin: %w", err)
	}

	log.SetOutput(stderr) // Ensure logs go to stderr
	log.Printf("[HumanReviewer] Reviewing %s artefact (version %d)",
		input.TargetArtefact.Type, input.TargetArtefact.Version)

	// Auto-approve mode for testing
	if autoApprove {
		log.Println("[HumanReviewer] AUTO_APPROVE=true, automatically approving")
		return outputApproval(stdout)
	}

	// Display artefact for human review
	displayArtefact(stderr, input.TargetArtefact)

	// Create timeout context
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// Channel for user input
	inputChan := make(chan string, 1)

	// Create a new reader that includes any data buffered by the decoder
	inputReader := io.MultiReader(decoder.Buffered(), reader)
	inputScanner := bufio.NewReader(inputReader)

	// Start goroutine to read user input
	go func() {
		fmt.Fprintf(stderr, "\nReview this %s (v%d). Approve? (y/n/comment): ",
			input.TargetArtefact.Type, input.TargetArtefact.Version)

		response, err := inputScanner.ReadString('\n')
		if err != nil {
			log.Printf("[HumanReviewer] Error reading input: %v", err)
			inputChan <- ""
			return
		}
		inputChan <- strings.TrimSpace(response)
	}()

	// Wait for input, timeout, or signal
	select {
	case <-sigChan:
		// SIGINT/SIGTERM - exit immediately
		log.Println("[HumanReviewer] Received interrupt signal, exiting")
		return fmt.Errorf("received interrupt signal")

	case <-ctx.Done():
		// Timeout
		log.Printf("[HumanReviewer] Review timed out after %d seconds", timeoutSeconds)
		return outputFailure(stdout, "Human review timed out. No response received within timeout period.")

	case response := <-inputChan:
		// Got user input
		return handleUserInput(inputScanner, stdout, stderr, response, input.TargetArtefact)
	}
}

// displayArtefact shows the artefact content to the reviewer
func displayArtefact(w io.Writer, art Artefact) {
	fmt.Fprintln(w, "\n"+strings.Repeat("=", 80))
	fmt.Fprintf(w, "Artefact ID: %s\n", art.ID)
	fmt.Fprintf(w, "Type: %s (version %d)\n", art.Type, art.Version)
	fmt.Fprintf(w, "Produced by: %s\n", art.ProducedByRole)
	fmt.Fprintln(w, strings.Repeat("-", 80))
	fmt.Fprintln(w, "Payload:")
	fmt.Fprintln(w, art.Payload)
	fmt.Fprintln(w, strings.Repeat("=", 80))
}

// handleUserInput processes the user's review decision
func handleUserInput(reader *bufio.Reader, stdout, stderr io.Writer, response string, art Artefact) error {
	response = strings.ToLower(response)

	switch {
	case response == "y" || response == "yes":
		// Approval
		log.Println("[HumanReviewer] Approved by human")
		return outputApproval(stdout)

	case response == "n" || response == "no":
		// Rejection - prompt for feedback
		fmt.Fprint(stderr, "Rejection reason: ")
		feedback, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("[HumanReviewer] Error reading feedback: %v", err)
			return outputRejection(stdout, "Rejected by human (no feedback provided)")
		}
		feedback = strings.TrimSpace(feedback)
		log.Printf("[HumanReviewer] Rejected by human: %s", feedback)
		return outputRejection(stdout, feedback)

	case strings.HasPrefix(response, "comment "):
		// Direct comment format: "comment <feedback text>"
		feedback := strings.TrimSpace(response[8:])
		if feedback == "" {
			log.Println("[HumanReviewer] Rejected with empty comment")
			return outputRejection(stdout, "Rejected by human (empty comment)")
		}
		log.Printf("[HumanReviewer] Rejected by human: %s", feedback)
		return outputRejection(stdout, feedback)

	default:
		// Invalid input - treat as rejection
		log.Printf("[HumanReviewer] Invalid input '%s', treating as rejection", response)
		return outputRejection(stdout, fmt.Sprintf("Invalid review response: %s", response))
	}
}

// outputApproval creates an approval Review artefact
func outputApproval(w io.Writer) error {
	output := Output{
		StructuralType:  "Review",
		ArtefactType:    "Review",
		ArtefactPayload: "{}",
		Summary:         "Approved by human reviewer",
	}
	return printOutput(w, output)
}

// outputRejection creates a rejection Review artefact with feedback
func outputRejection(w io.Writer, feedback string) error {
	payload := ReviewPayload{
		Feedback: feedback,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal rejection payload: %w", err)
	}

	output := Output{
		StructuralType:  "Review",
		ArtefactType:    "Review",
		ArtefactPayload: string(payloadJSON),
		Summary:         "Rejected by human reviewer",
	}
	return printOutput(w, output)
}

// outputFailure creates a Failure artefact (for timeout)
func outputFailure(w io.Writer, message string) error {
	output := Output{
		StructuralType:  "Failure",
		ArtefactType:    "HumanReviewTimeout",
		ArtefactPayload: message,
		Summary:         "Human review failed: timeout",
	}
	return printOutput(w, output)
}

// printOutput writes the output JSON to stdout
func printOutput(w io.Writer, output Output) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return fmt.Errorf("failed to encode output: %w", err)
	}
	return nil
}

// getTimeoutSeconds reads REVIEW_TIMEOUT env var, defaults to 300 (5 minutes)
func getTimeoutSeconds(getEnv func(string) string) int {
	timeoutStr := getEnv("REVIEW_TIMEOUT")
	if timeoutStr == "" {
		return 300 // Default: 5 minutes
	}

	timeout, err := strconv.Atoi(timeoutStr)
	if err != nil {
		log.Printf("[HumanReviewer] Invalid REVIEW_TIMEOUT '%s', using default 300", timeoutStr)
		return 300
	}

	if timeout <= 0 {
		log.Printf("[HumanReviewer] Invalid REVIEW_TIMEOUT %d, using default 300", timeout)
		return 300
	}

	return timeout
}
