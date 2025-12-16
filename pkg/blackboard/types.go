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

// Artefact replaces the old v1 struct.
// It uses V2 content-addressable Merkle DAG architecture where
// every artefact's identity is its SHA-256 content hash.
type Artefact struct {
	// ID is the SHA-256 hash (hex-encoded, 64 characters)
	// This is the artefact's immutable, content-derived address.
	ID string `json:"id"`

	Header  ArtefactHeader  `json:"header"`
	Payload ArtefactPayload `json:"payload"`
}

// ArtefactHeader contains metadata and provenance links.
// All fields in this struct are included in the hash computation.
// CRITICAL: Any modification to field names, types, or tags will change hash computation.
type ArtefactHeader struct {
	// ParentHashes replaces v1's SourceArtefacts - now stores SHA-256 hashes, not UUIDs
	// Empty array for root artefacts (e.g., GoalDefined)
	ParentHashes []string `json:"parent_hashes"`

	// LogicalThreadID groups versions of the same conceptual artefact
	// Retained for O(1) "latest version" lookups via Redis ZSET
	// V1 (new thread): Generate new UUID
	// V2+ (versions): Inherit UUID from parent
	LogicalThreadID string `json:"logical_thread_id"` // UUID format

	// Version counter within the logical thread (starts at 1)
	Version int `json:"version"`

	// Timestamp of creation (part of hashed content - CRITICAL for temporal ordering)
	CreatedAtMs int64 `json:"created_at_ms"` // Unix milliseconds

	// Agent that produced this artefact
	ProducedByRole string `json:"produced_by_role"`

	// Orchestration role (hardcoded enum in StructuralType)
	StructuralType StructuralType `json:"structural_type"`

	// User-defined domain type (opaque to orchestrator)
	Type string `json:"type"`

	// M4.3: Context caching - INCLUDED in hash for security/visibility scope
	// Uses omitempty: empty/nil slice excluded from canonical JSON to save space
	ContextForRoles []string `json:"context_for_roles,omitempty"`

	// M4.6 Security Addendum: Grant Linkage & Topology Validation
	// ClaimID cryptographically binds this artefact to the authorization that permitted its creation
	// MUST be present for agent-produced artefacts (unless root artefact with ParentHashes=[])
	// Empty for root artefacts (CLI-generated) and some orchestrator-generated artefacts
	// Uses omitempty: empty string excluded from canonical JSON
	ClaimID string `json:"claim_id,omitempty"`

	// M5.1: Metadata for synchronization
	// Value is JSON-encoded map[string]string (e.g. {"batch_size": "5"})
	// Included in hash to prevent tampering with synchronization parameters
	Metadata string `json:"metadata,omitempty"`
}

// ArtefactPayload is the actual content.
// HARD LIMIT: 1MB (1,048,576 bytes). Larger content must be written to disk/git
// and referenced via hash in the payload.
type ArtefactPayload struct {
	Content string `json:"content"` // Max 1MB
}

// MaxPayloadSize is the hard limit for artefact payload content.
// This prevents Redis memory pressure and ensures fast hash computation.
const MaxPayloadSize = 1 * 1024 * 1024 // 1MB

// HashMismatchError is returned when artefact hash verification fails.
// This indicates potential tampering or data corruption.
type HashMismatchError struct {
	Expected string // Hash computed by verifier (Orchestrator)
	Actual   string // Hash claimed in artefact ID (from Pup)
}

func (e *HashMismatchError) Error() string {
	return "hash mismatch: expected " + e.Expected + ", got " + e.Actual
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
	LastGrantAgent   string `json:"last_grant_agent,omitempty"`  // Last agent granted this claim
	LastGrantTime    int64  `json:"last_grant_time,omitempty"`   // Unix timestamp of last grant
	ArtefactExpected bool   `json:"artefact_expected,omitempty"` // Whether we're waiting for artefact from granted agent

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

	// ClaimStatusDormant indicates the claim received no viable bids and is sleeping
	ClaimStatusDormant ClaimStatus = "dormant"
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
	Current       string             `json:"current"`        // Current phase: "review", "parallel", or "exclusive"
	GrantedAgents []string           `json:"granted_agents"` // Agents granted in this phase
	Received      map[string]string  `json:"received"`       // agentRole → artefactID (received artefacts)
	AllBids       map[string]BidType `json:"all_bids"`       // agentName → bidType (all original bids)
	BidTimestamps map[string]int64   `json:"bid_timestamps"` // agentName → timestampMs (when bids were received)
	StartTimeMs   int64              `json:"start_time_ms"`  // M3.9: Unix timestamp in milliseconds when phase started
}

// GrantQueue represents grant queue metadata for paused claims (M3.5).
// Used when controller-worker agents hit max_concurrent limit.
type GrantQueue struct {
	PausedAtMs int64  `json:"paused_at_ms"` // M3.9: Unix timestamp in milliseconds when claim was paused
	AgentName  string `json:"agent_name"`   // Agent name that would be granted
	Position   int    `json:"position"`     // Reserved for future display/debugging (not populated in M3.5)
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
		ClaimStatusComplete, ClaimStatusTerminated, ClaimStatusDormant:
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
