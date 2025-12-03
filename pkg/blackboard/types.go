// Package blackboard provides type-safe Go definitions and Redis schema patterns
// for the Holt blackboard architecture. The blackboard is the central shared state
// system where all Holt components (orchestrator, pups, CLI) interact via well-defined
// data structures stored in Redis.
//
// All Redis keys and channels are namespaced by instance name to enable multiple
// Holt instances to safely coexist on a single Redis server.
package blackboard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Artefact represents an immutable work product on the blackboard.
// Artefacts are the fundamental unit of state in Holt - every piece of work,
// decision, and result is represented as an artefact with complete provenance.
type Artefact struct {
	ID              string         `json:"id"`               // SHA-256 Hash (64 hex chars)
	LogicalID       string         `json:"logical_id"`       // UUID or Hash - groups versions
	Version         int            `json:"version"`          // Incrementing version number (starts at 1)
	StructuralType  StructuralType `json:"structural_type"`  // Role in orchestration flow
	Type            string         `json:"type"`             // User-defined domain type (e.g., "CodeCommit", "DesignSpec")
	Payload         string         `json:"payload"`          // Main content (git hash, JSON, text)
	SourceArtefacts []string       `json:"source_artefacts"` // Array of parent artefact IDs
	ProducedByRole  string         `json:"produced_by_role"` // Agent's role from holt.yml or "user"
	CreatedAtMs     int64          `json:"created_at_ms"`    // M3.9: Unix timestamp in milliseconds when artefact was created

	// M4.3: Context caching - glob patterns for which agent roles should receive this Knowledge
	ContextForRoles []string `json:"context_for_roles,omitempty"` // Only used for Knowledge artefacts

	// M4.6 Security Addendum: Grant Linkage & Topology Validation
	// ClaimID cryptographically binds this artefact to the authorization that permitted its creation
	ClaimID string `json:"claim_id,omitempty"`
}

// StructuralType defines the role an artefact plays in the orchestration flow.
// These types determine how the orchestrator handles claims and agent coordination.
type StructuralType string

const (
	// StructuralTypeStandard represents normal work artefacts that trigger standard claim processing
	StructuralTypeStandard StructuralType = "Standard"

	// StructuralTypeReview represents review feedback artefacts from review-phase agents
	StructuralTypeReview StructuralType = "Review"

	// StructuralTypeQuestion represents questions escalated to humans for answers
	StructuralTypeQuestion StructuralType = "Question"

	// StructuralTypeAnswer represents human answers to questions, unblocking workflows
	StructuralTypeAnswer StructuralType = "Answer"

	// StructuralTypeFailure represents agent failures that terminate claim processing
	StructuralTypeFailure StructuralType = "Failure"

	// StructuralTypeTerminal represents workflow completion, stopping all processing
	StructuralTypeTerminal StructuralType = "Terminal"

	// StructuralTypeKnowledge represents cached context data ignored by orchestrator (M4.3)
	StructuralTypeKnowledge StructuralType = "Knowledge"

	// StructuralTypeSystemManifest represents system-generated manifests (e.g., M4.7 Integrity Manifests)
	StructuralTypeSystemManifest StructuralType = "SystemManifest"
)

// Claim represents the orchestrator's decision about an artefact.
// Claims track which agents have been granted access to work on an artefact,
// and coordinate the phased execution model (review → parallel → exclusive).
type Claim struct {
	ID                    string      `json:"id"`                      // Unique identifier for this claim
	ArtefactID            string      `json:"artefact_id"`             // The artefact this claim is for
	Status                ClaimStatus `json:"status"`                  // Current lifecycle state
	GrantedReviewAgents   []string    `json:"granted_review_agents"`   // Agent names granted review access
	GrantedParallelAgents []string    `json:"granted_parallel_agents"` // Agent names granted parallel access
	GrantedExclusiveAgent string      `json:"granted_exclusive_agent"` // Single agent name granted exclusive access

	// M3.3: Feedback loop support
	AdditionalContextIDs []string `json:"additional_context_ids,omitempty"` // Review artefact IDs for feedback claims
	TerminationReason    string   `json:"termination_reason,omitempty"`     // Explicit reason when status=terminated

	// M3.5: Phase state persistence (for orchestrator restart resilience)
	PhaseState *PhaseState `json:"phase_state,omitempty"` // Current phase execution state

	// M3.5: Grant queue persistence (for controller-worker max_concurrent pausing)
	GrantQueue *GrantQueue `json:"grant_queue,omitempty"` // Queue metadata when paused at max_concurrent

	// M3.5: Grant tracking (for re-triggering on restart)
	LastGrantAgent    string `json:"last_grant_agent,omitempty"`    // Last agent granted this claim
	LastGrantTime     int64  `json:"last_grant_time,omitempty"`     // Unix timestamp of last grant
	ArtefactExpected  bool   `json:"artefact_expected,omitempty"`   // Whether we're waiting for artefact from granted agent

	// M3.9: Agent version auditing
	GrantedAgentImageID string `json:"granted_agent_image_id,omitempty"` // Docker image ID of agent that was granted this claim
}

// ClaimStatus defines the lifecycle state of a claim.
// Claims progress through phases: review → parallel → exclusive → complete/terminated.
type ClaimStatus string

const (
	// ClaimStatusPendingReview indicates the claim is in the review phase
	ClaimStatusPendingReview ClaimStatus = "pending_review"

	// ClaimStatusPendingParallel indicates the claim is in the parallel execution phase
	ClaimStatusPendingParallel ClaimStatus = "pending_parallel"

	// ClaimStatusPendingExclusive indicates the claim is in the exclusive execution phase
	ClaimStatusPendingExclusive ClaimStatus = "pending_exclusive"

	// ClaimStatusPendingAssignment indicates a feedback claim with pre-assigned agent (M3.3)
	ClaimStatusPendingAssignment ClaimStatus = "pending_assignment"

	// ClaimStatusComplete indicates the claim has been successfully processed
	ClaimStatusComplete ClaimStatus = "complete"

	// ClaimStatusTerminated indicates the claim was terminated (failure or review feedback)
	ClaimStatusTerminated ClaimStatus = "terminated"
)

// BidType represents an agent's interest level in a claim.
// Agents submit bids to indicate how they want to interact with an artefact.
type BidType string

const (
	// BidTypeReview indicates the agent wants to review the artefact (read-only, parallel)
	BidTypeReview BidType = "review"

	// BidTypeParallel indicates the agent wants to work in parallel with other agents
	BidTypeParallel BidType = "claim"

	// BidTypeExclusive indicates the agent wants exclusive access to the artefact
	BidTypeExclusive BidType = "exclusive"

	// BidTypeIgnore indicates the agent has no interest in the artefact
	BidTypeIgnore BidType = "ignore"
)

// Bid represents a single agent's bid on a claim.
// Note: In Redis, bids are stored as a hash where key=agent_name, value=bid_type.
// This struct is for in-memory representation.
type Bid struct {
	AgentName   string  `json:"agent_name"`   // Logical name of the agent
	BidType     BidType `json:"bid_type"`     // Type of bid submitted
	TimestampMs int64   `json:"timestamp_ms"` // Unix timestamp in milliseconds when bid was submitted
}

// PhaseState represents persisted phase execution state for restart resilience (M3.5).
// Stored as JSON-encoded fields in the Claim Redis hash.
type PhaseState struct {
	Current       string             `json:"current"`         // Current phase: "review", "parallel", or "exclusive"
	GrantedAgents []string           `json:"granted_agents"`  // Agents granted in this phase
	Received      map[string]string  `json:"received"`        // agentRole → artefactID (received artefacts)
	AllBids       map[string]BidType `json:"all_bids"`        // agentName → bidType (all original bids)
	BidTimestamps map[string]int64   `json:"bid_timestamps"`  // agentName → timestampMs (when bids were received)
	StartTimeMs   int64              `json:"start_time_ms"`   // M3.9: Unix timestamp in milliseconds when phase started
}

// GrantQueue represents grant queue metadata for paused claims (M3.5).
// Used when controller-worker agents hit max_concurrent limit.
type GrantQueue struct {
	PausedAtMs int64  `json:"paused_at_ms"` // M3.9: Unix timestamp in milliseconds when claim was paused
	AgentName  string `json:"agent_name"`   // Agent name that would be granted
	Position   int    `json:"position"`     // Reserved for future display/debugging (not populated in M3.5)
}

// Validate checks if the Artefact has valid field values.
// Returns an error if any validation fails.
func (a *Artefact) Validate() error {
	if a.ID == "" {
		return fmt.Errorf("invalid artefact ID: empty")
	}

	if a.LogicalID == "" {
		return fmt.Errorf("invalid logical ID: empty")
	}

	if a.Version < 1 {
		return fmt.Errorf("invalid version: must be >= 1, got %d", a.Version)
	}

	if err := a.StructuralType.Validate(); err != nil {
		return fmt.Errorf("invalid structural type: %w", err)
	}

	if a.Type == "" {
		return fmt.Errorf("artefact type cannot be empty")
	}

	if a.ProducedByRole == "" {
		return fmt.Errorf("produced_by_role cannot be empty")
	}

	// Validate all source artefact IDs
	for i, sourceID := range a.SourceArtefacts {
		if sourceID == "" {
			return fmt.Errorf("invalid source artefact at index %d: empty", i)
		}
	}

	return nil
}

// Validate checks if the StructuralType is a valid enum value.
func (st StructuralType) Validate() error {
	switch st {
	case StructuralTypeStandard, StructuralTypeReview, StructuralTypeQuestion,
		StructuralTypeAnswer, StructuralTypeFailure, StructuralTypeTerminal,
		StructuralTypeKnowledge, StructuralTypeSystemManifest: // Added SystemManifest
		return nil
	default:
		return fmt.Errorf("unknown structural type: %q", st)
	}
}

// Validate checks if the Claim has valid field values.
func (c *Claim) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("invalid claim ID: empty")
	}

	if c.ArtefactID == "" {
		return fmt.Errorf("invalid artefact ID: empty")
	}

	if err := c.Status.Validate(); err != nil {
		return fmt.Errorf("invalid status: %w", err)
	}

	return nil
}

// Validate checks if the ClaimStatus is a valid enum value.
func (cs ClaimStatus) Validate() error {
	switch cs {
	case ClaimStatusPendingReview, ClaimStatusPendingParallel,
		ClaimStatusPendingExclusive, ClaimStatusPendingAssignment,
		ClaimStatusComplete, ClaimStatusTerminated:
		return nil
	default:
		return fmt.Errorf("unknown claim status: %q", cs)
	}
}

// Validate checks if the BidType is a valid enum value.
func (bt BidType) Validate() error {
	switch bt {
	case BidTypeReview, BidTypeParallel, BidTypeExclusive, BidTypeIgnore:
		return nil
	default:
		return fmt.Errorf("unknown bid type: %q", bt)
	}
}

// NewID generates a random 32-byte hex string (64 characters).
// Replaces UUIDs for V2 clean break.
func NewID() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		// Should not happen in normal operation, but safe fallback
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
