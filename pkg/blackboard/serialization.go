package blackboard

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Serialization helpers for converting between Go structs and Redis hashes
//
// Redis stores data as string-to-string maps (hashes). Complex fields like arrays
// are JSON-encoded into single hash fields. This provides a balance between
// queryability (individual fields) and flexibility (complex structures).

// ArtefactToHash converts an Artefact struct to a Redis hash format.
// Array fields (source_artefacts, context_for_roles) are JSON-encoded.
// The nested Artefact struct is flattened into a Redis hash.
func ArtefactToHash(a *Artefact) (map[string]interface{}, error) {
	// Encode source artefacts array as JSON
	sourceArtefactsJSON, err := json.Marshal(a.Header.ParentHashes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal source artefacts: %w", err)
	}

	// M4.3: Encode context_for_roles array as JSON (only for Knowledge artefacts)
	contextForRolesJSON, err := json.Marshal(a.Header.ContextForRoles)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal context_for_roles: %w", err)
	}

	// M5.1: Ensure metadata is valid JSON (default to empty object)
	metadata := a.Header.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	hash := map[string]interface{}{
		"id":                a.ID,
		"logical_id":        a.Header.LogicalThreadID,
		"version":           a.Header.Version,
		"structural_type":   string(a.Header.StructuralType),
		"type":              a.Header.Type,
		"payload":           a.Payload.Content,
		"source_artefacts":  string(sourceArtefactsJSON),
		"produced_by_role":  a.Header.ProducedByRole,
		"created_at_ms":     a.Header.CreatedAtMs,        // M3.9
		"context_for_roles": string(contextForRolesJSON), // M4.3
		"claim_id":          a.Header.ClaimID,            // M4.6
		"metadata":          metadata,                    // M5.1
	}

	return hash, nil
}

// HashToArtefact converts a Redis hash to an Artefact struct.
// JSON fields are decoded back to Go types.
func HashToArtefact(hash map[string]string) (*Artefact, error) {
	// Parse version number
	version, err := strconv.Atoi(hash["version"])
	if err != nil {
		return nil, fmt.Errorf("invalid version field: %w", err)
	}

	// Decode source artefacts JSON array (maps to ParentHashes)
	var parentHashes []string
	if sourceArtefactsJSON := hash["source_artefacts"]; sourceArtefactsJSON != "" {
		if err := json.Unmarshal([]byte(sourceArtefactsJSON), &parentHashes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal source_artefacts: %w", err)
		}
	}

	// Ensure we have an empty slice instead of nil for consistency
	if parentHashes == nil {
		parentHashes = []string{}
	}

	// M4.3: Decode context_for_roles JSON array (only present for Knowledge artefacts)
	var contextForRoles []string
	if contextForRolesJSON := hash["context_for_roles"]; contextForRolesJSON != "" {
		if err := json.Unmarshal([]byte(contextForRolesJSON), &contextForRoles); err != nil {
			return nil, fmt.Errorf("failed to unmarshal context_for_roles: %w", err)
		}
	}

	if contextForRoles == nil {
		contextForRoles = []string{}
	}

	// M3.9: Parse created_at_ms
	createdAtMs, _ := strconv.ParseInt(hash["created_at_ms"], 10, 64)

	// M5.1: Parse metadata (default to empty object if missing)
	metadata := hash["metadata"]
	if metadata == "" {
		metadata = "{}"
	}

	artefact := &Artefact{
		ID: hash["id"],
		Header: ArtefactHeader{
			LogicalThreadID: hash["logical_id"],
			Version:         version,
			StructuralType:  StructuralType(hash["structural_type"]),
			Type:            hash["type"],
			ParentHashes:    parentHashes,
			ProducedByRole:  hash["produced_by_role"],
			CreatedAtMs:     createdAtMs,
			ContextForRoles: contextForRoles,
			ClaimID:         hash["claim_id"],
			Metadata:        metadata,
		},
		Payload: ArtefactPayload{
			Content: hash["payload"],
		},
	}

	return artefact, nil
}

// ClaimToHash converts a Claim struct to a Redis hash format.
// Array fields (granted_review_agents, granted_parallel_agents) are JSON-encoded.
func ClaimToHash(c *Claim) (map[string]interface{}, error) {
	// Encode agent arrays as JSON
	reviewAgentsJSON, err := json.Marshal(c.GrantedReviewAgents)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal granted_review_agents: %w", err)
	}

	parallelAgentsJSON, err := json.Marshal(c.GrantedParallelAgents)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal granted_parallel_agents: %w", err)
	}

	// M3.3: Encode additional_context_ids as JSON
	additionalContextJSON, err := json.Marshal(c.AdditionalContextIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal additional_context_ids: %w", err)
	}

	hash := map[string]interface{}{
		"id":                      c.ID,
		"artefact_id":             c.ArtefactID,
		"status":                  string(c.Status),
		"granted_review_agents":   string(reviewAgentsJSON),
		"granted_parallel_agents": string(parallelAgentsJSON),
		"granted_exclusive_agent": c.GrantedExclusiveAgent,
		"additional_context_ids":  string(additionalContextJSON), // M3.3
		"termination_reason":      c.TerminationReason,           // M3.3
	}

	// M3.5: Encode phase state as JSON if present
	if c.PhaseState != nil {
		phaseStateJSON, err := json.Marshal(c.PhaseState)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal phase_state: %w", err)
		}
		hash["phase_state"] = string(phaseStateJSON)
	} else {
		hash["phase_state"] = ""
	}

	// M3.5: Encode grant queue as JSON if present
	if c.GrantQueue != nil {
		grantQueueJSON, err := json.Marshal(c.GrantQueue)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal grant_queue: %w", err)
		}
		hash["grant_queue"] = string(grantQueueJSON)
	} else {
		hash["grant_queue"] = ""
	}

	// M3.5: Grant tracking fields
	hash["last_grant_agent"] = c.LastGrantAgent
	hash["last_grant_time"] = c.LastGrantTime
	hash["artefact_expected"] = c.ArtefactExpected

	// M3.9: Agent version auditing
	hash["granted_agent_image_id"] = c.GrantedAgentImageID

	return hash, nil
}

// HashToClaim converts a Redis hash to a Claim struct.
// JSON fields are decoded back to Go types.
func HashToClaim(hash map[string]string) (*Claim, error) {
	// Decode granted review agents JSON array
	var reviewAgents []string
	if reviewAgentsJSON := hash["granted_review_agents"]; reviewAgentsJSON != "" {
		if err := json.Unmarshal([]byte(reviewAgentsJSON), &reviewAgents); err != nil {
			return nil, fmt.Errorf("failed to unmarshal granted_review_agents: %w", err)
		}
	}

	// Decode granted parallel agents JSON array
	var parallelAgents []string
	if parallelAgentsJSON := hash["granted_parallel_agents"]; parallelAgentsJSON != "" {
		if err := json.Unmarshal([]byte(parallelAgentsJSON), &parallelAgents); err != nil {
			return nil, fmt.Errorf("failed to unmarshal granted_parallel_agents: %w", err)
		}
	}

	// M3.3: Decode additional_context_ids JSON array
	var additionalContextIDs []string
	if additionalContextJSON := hash["additional_context_ids"]; additionalContextJSON != "" {
		if err := json.Unmarshal([]byte(additionalContextJSON), &additionalContextIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal additional_context_ids: %w", err)
		}
	}

	// Ensure we have empty slices instead of nil for consistency
	if reviewAgents == nil {
		reviewAgents = []string{}
	}
	if parallelAgents == nil {
		parallelAgents = []string{}
	}
	if additionalContextIDs == nil {
		additionalContextIDs = []string{}
	}

	// M3.5: Decode phase state JSON if present
	var phaseState *PhaseState
	if phaseStateJSON := hash["phase_state"]; phaseStateJSON != "" {
		phaseState = &PhaseState{}
		if err := json.Unmarshal([]byte(phaseStateJSON), phaseState); err != nil {
			return nil, fmt.Errorf("failed to unmarshal phase_state: %w", err)
		}
	}

	// M3.5: Decode grant queue JSON if present
	var grantQueue *GrantQueue
	if grantQueueJSON := hash["grant_queue"]; grantQueueJSON != "" {
		grantQueue = &GrantQueue{}
		if err := json.Unmarshal([]byte(grantQueueJSON), grantQueue); err != nil {
			return nil, fmt.Errorf("failed to unmarshal grant_queue: %w", err)
		}
	}

	// M3.5: Parse grant tracking fields
	lastGrantTime, _ := strconv.ParseInt(hash["last_grant_time"], 10, 64)
	artefactExpected, _ := strconv.ParseBool(hash["artefact_expected"])

	claim := &Claim{
		ID:                    hash["id"],
		ArtefactID:            hash["artefact_id"],
		Status:                ClaimStatus(hash["status"]),
		GrantedReviewAgents:   reviewAgents,
		GrantedParallelAgents: parallelAgents,
		GrantedExclusiveAgent: hash["granted_exclusive_agent"],
		AdditionalContextIDs:  additionalContextIDs,           // M3.3
		TerminationReason:     hash["termination_reason"],     // M3.3
		PhaseState:            phaseState,                     // M3.5
		GrantQueue:            grantQueue,                     // M3.5
		LastGrantAgent:        hash["last_grant_agent"],       // M3.5
		LastGrantTime:         lastGrantTime,                  // M3.5
		ArtefactExpected:      artefactExpected,               // M3.5
		GrantedAgentImageID:   hash["granted_agent_image_id"], // M3.9
	}

	return claim, nil
}
