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
	Type             SecurityAlertType `json:"type"`
	TimestampMs      int64             `json:"timestamp_ms"`
	OrchestratorAction string          `json:"orchestrator_action"` // "global_lockdown", "rejected", etc.

	// Hash mismatch fields
	ArtefactIDClaimed string `json:"artefact_id_claimed,omitempty"`
	HashExpected      string `json:"hash_expected,omitempty"`
	HashActual        string `json:"hash_actual,omitempty"`

	// Orphan block fields
	ArtefactID       string `json:"artefact_id,omitempty"`
	MissingParentHash string `json:"missing_parent_hash,omitempty"`

	// Timestamp drift fields
	ArtefactTimestampMs      int64 `json:"artefact_timestamp_ms,omitempty"`
	OrchestratorTimestampMs int64 `json:"orchestrator_timestamp_ms,omitempty"`
	DriftMs                  int64 `json:"drift_ms,omitempty"`
	ThresholdMs              int64 `json:"threshold_ms,omitempty"`

	// Security override fields (unlock)
	Action   string `json:"action,omitempty"`   // "lockdown_cleared"
	Reason   string `json:"reason,omitempty"`   // Human-provided justification
	Operator string `json:"operator,omitempty"` // Who cleared the lockdown

	// Common fields
	AgentRole string `json:"agent_role,omitempty"`
	ClaimID   string `json:"claim_id,omitempty"`
}

// SecurityAlertType defines the type of security event
type SecurityAlertType string

const (
	// SecurityAlertHashMismatch indicates hash verification failed (tampering detected)
	SecurityAlertHashMismatch SecurityAlertType = "hash_mismatch"

	// SecurityAlertOrphanBlock indicates parent hash not found (DAG integrity violation)
	SecurityAlertOrphanBlock SecurityAlertType = "orphan_block"

	// SecurityAlertTimestampDrift indicates timestamp outside tolerance window
	SecurityAlertTimestampDrift SecurityAlertType = "timestamp_drift"

	// SecurityAlertOverride indicates manual lockdown clearance (audited)
	SecurityAlertOverride SecurityAlertType = "security_override"
)

// NewHashMismatchAlert creates a hash mismatch security alert.
func NewHashMismatchAlert(artefactID, expected, actual, agentRole, claimID string) *SecurityAlert {
	return &SecurityAlert{
		Type:                SecurityAlertHashMismatch,
		TimestampMs:         time.Now().UnixMilli(),
		OrchestratorAction:  "global_lockdown",
		ArtefactIDClaimed:   artefactID,
		HashExpected:        expected,
		HashActual:          actual,
		AgentRole:           agentRole,
		ClaimID:             claimID,
	}
}

// NewOrphanBlockAlert creates an orphan block security alert.
func NewOrphanBlockAlert(artefactID, missingParent, agentRole, claimID string) *SecurityAlert {
	return &SecurityAlert{
		Type:               SecurityAlertOrphanBlock,
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
		Type:                     SecurityAlertTimestampDrift,
		TimestampMs:              time.Now().UnixMilli(),
		OrchestratorAction:       "rejected",
		ArtefactID:               artefactID,
		ArtefactTimestampMs:      artefactTs,
		OrchestratorTimestampMs:  orchTs,
		DriftMs:                  drift,
		ThresholdMs:              threshold,
		AgentRole:                agentRole,
	}
}

// NewSecurityOverrideAlert creates a security override alert (manual unlock).
func NewSecurityOverrideAlert(reason, operator string) *SecurityAlert {
	return &SecurityAlert{
		Type:        SecurityAlertOverride,
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
