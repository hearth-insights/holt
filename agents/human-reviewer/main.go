package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	ClaimType       string      `json:"claim_type"`
	TargetArtefact  Artefact    `json:"target_artefact"`
	ContextChain    []Artefact  `json:"context_chain"`
	AdditionalContext []Artefact `json:"additional_context"`
}

// Artefact represents a blackboard artefact
type Artefact struct {
	ID             string   `json:"id"`
	LogicalID      string   `json:"logical_id"`
	Version        int      `json:"version"`
	StructuralType string   `json:"structural_type"`
	Type           string   `json:"type"`
	Payload        string   `json:"payload"`
	SourceArtefacts []string `json:"source_artefacts"`
	ProducedByRole string   `json:"produced_by_role"`
}

// Output contract to pup (stdout)
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
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Get configuration from environment
	autoApprove := os.Getenv("AUTO_APPROVE") == "true"
	timeoutSeconds := getTimeoutSeconds()

	// Read input from stdin
	var input Input
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&input); err != nil {
		log.Fatalf("[HumanReviewer] Failed to decode stdin: %v", err)
	}

	log.Printf("[HumanReviewer] Reviewing %s artefact (version %d)",
		input.TargetArtefact.Type, input.TargetArtefact.Version)

	// Auto-approve mode for testing
	if autoApprove {
		log.Println("[HumanReviewer] AUTO_APPROVE=true, automatically approving")
		outputApproval()
		return
	}

	// Display artefact for human review
	displayArtefact(input.TargetArtefact)

	// Create timeout context
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// Channel for user input
	inputChan := make(chan string, 1)

	// Start goroutine to read user input
	go func() {
		reader := bufio.NewReader(os.Stdin)
		fmt.Fprintf(os.Stderr, "\nReview this %s (v%d). Approve? (y/n/comment): ",
			input.TargetArtefact.Type, input.TargetArtefact.Version)

		response, err := reader.ReadString('\n')
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
		os.Exit(1)

	case <-ctx.Done():
		// Timeout
		log.Printf("[HumanReviewer] Review timed out after %d seconds", timeoutSeconds)
		outputFailure("Human review timed out. No response received within timeout period.")
		os.Exit(1)

	case response := <-inputChan:
		// Got user input
		handleUserInput(response, input.TargetArtefact)
	}
}

// displayArtefact shows the artefact content to the reviewer
func displayArtefact(art Artefact) {
	fmt.Fprintln(os.Stderr, "\n"+strings.Repeat("=", 80))
	fmt.Fprintf(os.Stderr, "Artefact ID: %s\n", art.ID)
	fmt.Fprintf(os.Stderr, "Type: %s (version %d)\n", art.Type, art.Version)
	fmt.Fprintf(os.Stderr, "Produced by: %s\n", art.ProducedByRole)
	fmt.Fprintln(os.Stderr, strings.Repeat("-", 80))
	fmt.Fprintln(os.Stderr, "Payload:")
	fmt.Fprintln(os.Stderr, art.Payload)
	fmt.Fprintln(os.Stderr, strings.Repeat("=", 80))
}

// handleUserInput processes the user's review decision
func handleUserInput(response string, art Artefact) {
	response = strings.ToLower(response)

	switch {
	case response == "y" || response == "yes":
		// Approval
		log.Println("[HumanReviewer] Approved by human")
		outputApproval()

	case response == "n" || response == "no":
		// Rejection - prompt for feedback
		fmt.Fprint(os.Stderr, "Rejection reason: ")
		reader := bufio.NewReader(os.Stdin)
		feedback, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("[HumanReviewer] Error reading feedback: %v", err)
			outputRejection("Rejected by human (no feedback provided)")
			return
		}
		feedback = strings.TrimSpace(feedback)
		log.Printf("[HumanReviewer] Rejected by human: %s", feedback)
		outputRejection(feedback)

	case strings.HasPrefix(response, "comment "):
		// Direct comment format: "comment <feedback text>"
		feedback := strings.TrimSpace(response[8:])
		if feedback == "" {
			log.Println("[HumanReviewer] Rejected with empty comment")
			outputRejection("Rejected by human (empty comment)")
			return
		}
		log.Printf("[HumanReviewer] Rejected by human: %s", feedback)
		outputRejection(feedback)

	default:
		// Invalid input - treat as rejection
		log.Printf("[HumanReviewer] Invalid input '%s', treating as rejection", response)
		outputRejection(fmt.Sprintf("Invalid review response: %s", response))
	}
}

// outputApproval creates an approval Review artefact
func outputApproval() {
	output := Output{
		StructuralType:  "Review",
		ArtefactType:    "Review",
		ArtefactPayload: "{}",
		Summary:         "Approved by human reviewer",
	}
	printOutput(output)
}

// outputRejection creates a rejection Review artefact with feedback
func outputRejection(feedback string) {
	payload := ReviewPayload{
		Feedback: feedback,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("[HumanReviewer] Failed to marshal rejection payload: %v", err)
	}

	output := Output{
		StructuralType:  "Review",
		ArtefactType:    "Review",
		ArtefactPayload: string(payloadJSON),
		Summary:         "Rejected by human reviewer",
	}
	printOutput(output)
}

// outputFailure creates a Failure artefact (for timeout)
func outputFailure(message string) {
	output := Output{
		StructuralType:  "Failure",
		ArtefactType:    "HumanReviewTimeout",
		ArtefactPayload: message,
		Summary:         "Human review failed: timeout",
	}
	printOutput(output)
}

// printOutput writes the output JSON to stdout
func printOutput(output Output) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		log.Fatalf("[HumanReviewer] Failed to encode output: %v", err)
	}
}

// getTimeoutSeconds reads REVIEW_TIMEOUT env var, defaults to 300 (5 minutes)
func getTimeoutSeconds() int {
	timeoutStr := os.Getenv("REVIEW_TIMEOUT")
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
