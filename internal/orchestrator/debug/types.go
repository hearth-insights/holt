package debug

import "time"

// Session represents an active debug session
type Session struct {
	ID                string    `json:"session_id"`
	ConnectedAtMs     int64     `json:"connected_at_ms"`
	LastHeartbeatMs   int64     `json:"last_heartbeat_ms"`
	IsPaused          bool      `json:"is_paused"`
	PausedArtefactID  string    `json:"paused_artefact_id,omitempty"`
	PausedClaimID     string    `json:"paused_claim_id,omitempty"`
	BreakpointID      string    `json:"breakpoint_id,omitempty"`
	PausedAtMs        int64     `json:"paused_at_ms,omitempty"`
	PausedEventType   string    `json:"paused_event_type,omitempty"` // Which event triggered pause
}

// Breakpoint represents a debug breakpoint condition
type Breakpoint struct {
	ID            string `json:"id"`
	ConditionType string `json:"condition_type"` // artefact.type|artefact.structural_type|claim.status|agent.role|event.type
	Pattern       string `json:"pattern"`        // Glob pattern or exact match
}

// BreakpointConditionType defines supported breakpoint condition types
type BreakpointConditionType string

const (
	ConditionArtefactType          BreakpointConditionType = "artefact.type"
	ConditionArtefactStructuralType BreakpointConditionType = "artefact.structural_type"
	ConditionClaimStatus           BreakpointConditionType = "claim.status"
	ConditionAgentRole             BreakpointConditionType = "agent.role"
	ConditionEventType             BreakpointConditionType = "event.type"
)

// EventType defines supported debug event types that can trigger breakpoints
type EventType string

const (
	EventArtefactReceived       EventType = "artefact_received"
	EventClaimCreated          EventType = "claim_created"
	EventReviewConsensusReached EventType = "review_consensus_reached"
	EventPhaseCompleted        EventType = "phase_completed"
)

// Command represents a debug command from CLI to orchestrator
type Command struct {
	CommandType string                 `json:"command_type"`
	SessionID   string                 `json:"session_id"`
	Payload     map[string]interface{} `json:"payload"`
}

// CommandType defines supported debug commands
type CommandType string

const (
	CommandSetBreakpoints   CommandType = "set_breakpoints"
	CommandClearBreakpoint  CommandType = "clear_breakpoint"
	CommandClearAll         CommandType = "clear_all"
	CommandContinue         CommandType = "continue"
	CommandStepNext         CommandType = "step_next"
	CommandInspectArtefact  CommandType = "inspect_artefact"
	CommandManualReview     CommandType = "manual_review"
	CommandTerminateClaim   CommandType = "terminate_claim"
)

// Event represents a debug event from orchestrator to CLI
type Event struct {
	EventType string                 `json:"event_type"`
	SessionID string                 `json:"session_id"`
	Payload   map[string]interface{} `json:"payload"`
}

// DebugEventType defines debug event types sent to CLI
type DebugEventType string

const (
	EventPausedOnBreakpoint DebugEventType = "paused_on_breakpoint"
	EventStepComplete       DebugEventType = "step_complete"
	EventBreakpointSet      DebugEventType = "breakpoint_set"
	EventSessionActive      DebugEventType = "session_active"
	EventSessionExpired     DebugEventType = "session_expired"
	EventReviewComplete     DebugEventType = "review_complete"
)

// ResumeSignal indicates how to resume from pause
type ResumeSignal int

const (
	ResumeContinue ResumeSignal = iota // Continue normal execution
	ResumeStep                          // Execute one event then pause again
)

// PauseContext contains information about why and where we paused
type PauseContext struct {
	ArtefactID   string
	ClaimID      string
	BreakpointID string
	EventType    string
	PausedAtMs   int64
}

// SessionTTL is the TTL for the debug session key (30 seconds)
const SessionTTL = 30 * time.Second

// HeartbeatInterval is how often the CLI should refresh the session (5 seconds)
const HeartbeatInterval = 5 * time.Second

// MaxBreakpoints is the maximum number of breakpoints allowed per session
const MaxBreakpoints = 100
