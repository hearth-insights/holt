package docker

import (
	"fmt"
	"strings"

	"github.com/dyluth/holt/pkg/blackboard"
)

// Label keys used for Holt resources
const (
	LabelProject       = "holt.project"
	LabelInstanceName  = "holt.instance.name"
	LabelInstanceRunID = "holt.instance.run_id"
	LabelWorkspacePath = "holt.workspace.path"
	LabelComponent     = "holt.component"
	LabelRedisPort     = "holt.redis.port"
	LabelAgentName     = "holt.agent.name" // M2.2: Agent name label
	LabelAgentRole     = "holt.agent.role" // M3.6: Agent role label
)

// BuildLabels creates the standard label set for all Holt resources.
// All parameters are required except component (which is resource-specific).
func BuildLabels(instanceName, runID, workspacePath, component string) map[string]string {
	labels := map[string]string{
		LabelProject:       "true",
		LabelInstanceName:  instanceName,
		LabelInstanceRunID: runID,
		LabelWorkspacePath: workspacePath,
	}

	if component != "" {
		labels[LabelComponent] = component
	}

	return labels
}

// GenerateRunID creates a new unique ID for an instance run.
// Each invocation of `holt up` gets a unique run ID.
func GenerateRunID() string {
	return blackboard.NewID()
}

// Resource naming conventions for Holt components

// NetworkName returns the Docker network name for an instance
func NetworkName(instanceName string) string {
	return fmt.Sprintf("holt-network-%s", instanceName)
}

// RedisContainerName returns the Redis container name for an instance
func RedisContainerName(instanceName string) string {
	return fmt.Sprintf("holt-redis-%s", instanceName)
}

// OrchestratorContainerName returns the orchestrator container name for an instance
func OrchestratorContainerName(instanceName string) string {
	return fmt.Sprintf("holt-orchestrator-%s", instanceName)
}

// AgentContainerName returns the agent container name for an instance and agent
// M3.7: Now uses role-based naming (agentRole = agent key from holt.yml)
func AgentContainerName(instanceName, agentRole string) string {
	return fmt.Sprintf("holt-%s-%s", instanceName, agentRole)
}

// WorkerContainerName returns the worker container name for controller-worker pattern
// M3.7: Role-based naming for ephemeral workers
func WorkerContainerName(instanceName, agentRole, claimID string) string {
	// Use first 8 chars of claim ID for readability
	shortClaimID := claimID
	if len(claimID) > 8 {
		shortClaimID = claimID[:8]
	}
	return fmt.Sprintf("holt-%s-%s-worker-%s", instanceName, agentRole, shortClaimID)
}

// M4.4: IsRedisContainer checks if a container name matches the Redis container pattern
func IsRedisContainer(containerName string) bool {
	// Remove leading slash if present (Docker adds this to Names[0])
	name := strings.TrimPrefix(containerName, "/")
	// Check if it matches the holt-redis-* pattern
	return strings.HasPrefix(name, "holt-redis-")
}
