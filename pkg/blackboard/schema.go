package blackboard

import "fmt"

// Redis key pattern helpers
//
// All Redis keys and Pub/Sub channels are namespaced by instance name to enable
// multiple Holt instances to safely coexist on a single Redis server.
//
// Key pattern: holt:{instance_name}:{entity}:{uuid}
// Channel pattern: holt:{instance_name}:{event_type}_events

// ArtefactKey returns the Redis key for an artefact.
// Pattern: holt:{instance_name}:artefact:{artefact_id}
func ArtefactKey(instanceName, artefactID string) string {
	return fmt.Sprintf("holt:%s:artefact:%s", instanceName, artefactID)
}

// ClaimKey returns the Redis key for a claim.
// Pattern: holt:{instance_name}:claim:{claim_id}
func ClaimKey(instanceName, claimID string) string {
	return fmt.Sprintf("holt:%s:claim:%s", instanceName, claimID)
}

// ClaimBidsKey returns the Redis key for a claim's bids hash.
// Pattern: holt:{instance_name}:claim:{claim_id}:bids
func ClaimBidsKey(instanceName, claimID string) string {
	return fmt.Sprintf("holt:%s:claim:%s:bids", instanceName, claimID)
}

// ClaimByArtefactKey returns the Redis key for the artefact->claim index.
// This enables idempotency checking by looking up claims by artefact ID.
// Pattern: holt:{instance_name}:claim_by_artefact:{artefact_id}
func ClaimByArtefactKey(instanceName, artefactID string) string {
	return fmt.Sprintf("holt:%s:claim_by_artefact:%s", instanceName, artefactID)
}

// ThreadKey returns the Redis key for a thread tracking ZSET.
// Pattern: holt:{instance_name}:thread:{logical_id}
func ThreadKey(instanceName, logicalID string) string {
	return fmt.Sprintf("holt:%s:thread:%s", instanceName, logicalID)
}

// ArtefactEventsChannel returns the Pub/Sub channel name for artefact events.
// Pattern: holt:{instance_name}:artefact_events
func ArtefactEventsChannel(instanceName string) string {
	return fmt.Sprintf("holt:%s:artefact_events", instanceName)
}

// ClaimEventsChannel returns the Pub/Sub channel name for claim events.
// Pattern: holt:{instance_name}:claim_events
func ClaimEventsChannel(instanceName string) string {
	return fmt.Sprintf("holt:%s:claim_events", instanceName)
}

// AgentEventsChannel returns the agent-specific event channel name.
// Used by orchestrator to publish grant notifications to individual agents.
// Pattern: holt:{instance_name}:agent:{agent_name}:events
func AgentEventsChannel(instanceName, agentName string) string {
	return fmt.Sprintf("holt:%s:agent:%s:events", instanceName, agentName)
}

// WorkflowEventsChannel returns the Pub/Sub channel name for workflow events.
// This channel carries bid submissions and claim grants for real-time monitoring.
// Pattern: holt:{instance_name}:workflow_events
func WorkflowEventsChannel(instanceName string) string {
	return fmt.Sprintf("holt:%s:workflow_events", instanceName)
}

// AgentImagesKey returns the Redis key for the agent images hash (M3.9).
// This hash stores agent role → Docker image ID mappings for audit trails.
// Pattern: holt:{instance_name}:agent_images
func AgentImagesKey(instanceName string) string {
	return fmt.Sprintf("holt:%s:agent_images", instanceName)
}

// KnowledgeIndexKey returns the Redis key for the global knowledge index hash (M4.3).
// This hash maps knowledge_name → logical_id for globally unique knowledge threads.
// Pattern: holt:{instance_name}:knowledge_index
func KnowledgeIndexKey(instanceName string) string {
	return fmt.Sprintf("holt:%s:knowledge_index", instanceName)
}

// ThreadContextKey returns the Redis key for a thread's context set (M4.3).
// This SET contains Knowledge artefact IDs attached to a specific work thread.
// Pattern: holt:{instance_name}:thread_context:{logical_id}
func ThreadContextKey(instanceName, logicalID string) string {
	return fmt.Sprintf("holt:%s:thread_context:%s", instanceName, logicalID)
}

// ChildrenIndexKey returns the Redis key for the reverse index (M5.1).
// This SET contains child artefact IDs for a given parent artefact.
// Pattern: holt:{instance_name}:index:children:{parent_id}
func ChildrenIndexKey(instanceName, parentID string) string {
	return fmt.Sprintf("holt:%s:index:children:%s", instanceName, parentID)
}

// SyncDedupLockKey returns the Redis key for synchronization deduplication lock (M5.1).
// DEPRECATED: M5.1.1 removes client-side locking in favor of Orchestrator-driven accumulation.
// This STRING key acts as a distributed lock to prevent duplicate bids.
// Pattern: holt:{instance_name}:sync_dedup:{ancestor_id}:{agent_role_hash}
func SyncDedupLockKey(instanceName, ancestorID, agentRoleHash string) string {
	return fmt.Sprintf("holt:%s:sync_dedup:%s:%s", instanceName, ancestorID, agentRoleHash)
}

// ClaimAccumulatorKey returns the Redis key for a claim accumulator hash (M5.1.1).
// The accumulator IS the Fan-In Claim - it tracks accumulated claims and lifecycle state.
// Pattern: holt:{instance_name}:claim-accumulator:{ancestor_id}:{role}
func ClaimAccumulatorKey(instanceName, ancestorID, role string) string {
	return fmt.Sprintf("holt:%s:claim-accumulator:%s:%s", instanceName, ancestorID, role)
}

// ClaimAccumulatorClaimsKey returns the Redis key for accumulated claims SET (M5.1.1).
// This SET contains claim IDs that have been accumulated into a batch.
// Pattern: holt:{instance_name}:claim-accumulator:{ancestor_id}:{role}:claims
func ClaimAccumulatorClaimsKey(instanceName, ancestorID, role string) string {
	return fmt.Sprintf("holt:%s:claim-accumulator:%s:%s:claims", instanceName, ancestorID, role)
}
