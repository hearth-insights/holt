package blackboard

import "time"

// SecurityAlert represents a security event (tamper detection, orphan blocks, etc.)
// for M4.6 global lockdown mechanism.
//
// Alerts are:
// 1. LPUSH to holt:{instance}:security:alerts:log (permanent audit trail)
// 2. SET to holt:{instance}:security:lockdown (circuit breaker state)
// 3. PUBLISH to holt:{instance}:security:alerts (real-time notification)
type SecurityAlert struct {
	Type               string `json:"type"`                          // "hash_mismatch", "orphan_block", "timestamp_drift", "security_override"
	TimestampMs        int64  `json:"timestamp_ms"`                  // Unix milliseconds
	OrchestratorAction string `json:"orchestrator_action,omitempty"` // "global_lockdown", "rejected", etc.

	// Hash mismatch fields
	ArtefactIDClaimed string `json:"artefact_id_claimed,omitempty"`
	HashExpected      string `json:"hash_expected,omitempty"`
	HashActual        string `json:"hash_actual,omitempty"`

	// Orphan block fields
	ArtefactID        string `json:"artefact_id,omitempty"`
	MissingParentHash string `json:"missing_parent_hash,omitempty"` // Note: Field name differs from JSON tag for backward compat

	// Timestamp drift fields
	ArtefactTimestampMs     int64 `json:"artefact_timestamp_ms,omitempty"`
	OrchestratorTimestampMs int64 `json:"orchestrator_timestamp_ms,omitempty"`
	DriftMs                 int64 `json:"drift_ms,omitempty"`
	ThresholdMs             int64 `json:"threshold_ms,omitempty"`

	// Security override fields (unlock)
	Action   string `json:"action,omitempty"`   // "lockdown_cleared"
	Reason   string `json:"reason,omitempty"`   // Human-provided justification
	Operator string `json:"operator,omitempty"` // Who cleared the lockdown

	// M4.6 Security Addendum: Topology validation fields
	ViolationType          string   `json:"violation_type,omitempty"`           // "missing_claim_id", "invalid_claim_reference", "parent_linkage_violation", "root_artefact_with_claim"
	ClaimIDProvided        string   `json:"claim_id_provided,omitempty"`        // The ClaimID in the artefact header
	ClaimStatus            string   `json:"claim_status,omitempty"`             // Status of the referenced claim (if invalid)
	ExpectedParentArtefact string   `json:"expected_parent_artefact,omitempty"` // The artefact ID from the claim (parent linkage check)
	ActualParentHashes     []string `json:"actual_parent_hashes,omitempty"`     // The ParentHashes in the artefact (parent linkage check)

	// Common fields
	AgentRole string `json:"agent_role,omitempty"`
	ClaimID   string `json:"claim_id,omitempty"`
}

// Alert type constants for easy use
const (
	AlertTypeHashMismatch         = "hash_mismatch"
	AlertTypeOrphanBlock          = "orphan_block"
	AlertTypeTimestampDrift       = "timestamp_drift"
	AlertTypeSecurityOverride     = "security_override"
	AlertTypeUnauthorizedTopology = "unauthorized_topology" // M4.6 Security Addendum
)

// NewHashMismatchAlert creates a hash mismatch security alert.
func NewHashMismatchAlert(artefactID, expected, actual, agentRole, claimID string) *SecurityAlert {
	return &SecurityAlert{
		Type:               AlertTypeHashMismatch,
		TimestampMs:        time.Now().UnixMilli(),
		OrchestratorAction: "global_lockdown",
		ArtefactIDClaimed:  artefactID,
		HashExpected:       expected,
		HashActual:         actual,
		AgentRole:          agentRole,
		ClaimID:            claimID,
	}
}

// NewOrphanBlockAlert creates an orphan block security alert.
func NewOrphanBlockAlert(artefactID, missingParent, agentRole, claimID string) *SecurityAlert {
	return &SecurityAlert{
		Type:               AlertTypeOrphanBlock,
		TimestampMs:        time.Now().UnixMilli(),
		OrchestratorAction: "global_lockdown",
		ArtefactID:         artefactID,
		MissingParentHash:  missingParent,
		AgentRole:          agentRole,
		ClaimID:            claimID,
	}
}

// NewTimestampDriftAlert creates a timestamp drift security alert.
func NewTimestampDriftAlert(artefactID string, artefactTs, orchTs, drift, threshold int64, agentRole string) *SecurityAlert {
	return &SecurityAlert{
		Type:                    AlertTypeTimestampDrift,
		TimestampMs:             time.Now().UnixMilli(),
		OrchestratorAction:      "rejected",
		ArtefactID:              artefactID,
		ArtefactTimestampMs:     artefactTs,
		OrchestratorTimestampMs: orchTs,
		DriftMs:                 drift,
		ThresholdMs:             threshold,
		AgentRole:               agentRole,
	}
}

// NewSecurityOverrideAlert creates a security override alert (manual unlock).
func NewSecurityOverrideAlert(reason, operator string) *SecurityAlert {
	return &SecurityAlert{
		Type:        AlertTypeSecurityOverride,
		TimestampMs: time.Now().UnixMilli(),
		Action:      "lockdown_cleared",
		Reason:      reason,
		Operator:    operator,
	}
}

// Redis key helpers for security infrastructure

// SecurityAlertsLogKey returns the Redis key for the permanent alert log (LIST).
func SecurityAlertsLogKey(instanceName string) string {
	return "holt:" + instanceName + ":security:alerts:log"
}

// SecurityLockdownKey returns the Redis key for the lockdown circuit breaker (STRING).
func SecurityLockdownKey(instanceName string) string {
	return "holt:" + instanceName + ":security:lockdown"
}

// SecurityAlertsChannel returns the Pub/Sub channel for real-time alerts.
func SecurityAlertsChannel(instanceName string) string {
	return "holt:" + instanceName + ":security:alerts"
}
