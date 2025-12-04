package debug

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// EvaluateBreakpoints checks if any breakpoint matches the current state
// Returns the first matching breakpoint, or nil if none match
func EvaluateBreakpoints(
	ctx context.Context,
	breakpoints []*Breakpoint,
	artefact *blackboard.Artefact,
	claim *blackboard.Claim,
	eventType EventType,
) *Breakpoint {
	for _, bp := range breakpoints {
		if evaluateBreakpoint(bp, artefact, claim, eventType) {
			return bp
		}
	}
	return nil
}

// evaluateBreakpoint checks if a single breakpoint matches the current state
func evaluateBreakpoint(
	bp *Breakpoint,
	artefact *blackboard.Artefact,
	claim *blackboard.Claim,
	eventType EventType,
) bool {
	switch BreakpointConditionType(bp.ConditionType) {
	case ConditionArtefactType:
		if artefact == nil {
			return false
		}
		return evaluateGlobPattern(bp.Pattern, artefact.Type)

	case ConditionArtefactStructuralType:
		if artefact == nil {
			return false
		}
		return evaluateGlobPattern(bp.Pattern, string(artefact.StructuralType))

	case ConditionClaimStatus:
		if claim == nil {
			return false
		}
		return evaluateGlobPattern(bp.Pattern, string(claim.Status))

	case ConditionAgentRole:
		if artefact == nil {
			return false
		}
		return evaluateGlobPattern(bp.Pattern, artefact.ProducedByRole)

	case ConditionEventType:
		return evaluateGlobPattern(bp.Pattern, string(eventType))

	default:
		// Unknown condition type - don't match
		return false
	}
}

// evaluateGlobPattern performs glob pattern matching using filepath.Match
// Returns true if the pattern matches the value
func evaluateGlobPattern(pattern, value string) bool {
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		// Invalid pattern - don't match
		return false
	}
	return matched
}

// ValidateBreakpointPattern validates a breakpoint pattern
// Returns error if the pattern is invalid for glob matching
func ValidateBreakpointPattern(pattern string) error {
	// Test pattern with dummy value
	_, err := filepath.Match(pattern, "test")
	if err != nil {
		return fmt.Errorf("invalid glob pattern: %w", err)
	}
	return nil
}

// ValidateBreakpointConditionType validates a condition type
func ValidateBreakpointConditionType(conditionType string) error {
	switch BreakpointConditionType(conditionType) {
	case ConditionArtefactType,
		ConditionArtefactStructuralType,
		ConditionClaimStatus,
		ConditionAgentRole,
		ConditionEventType:
		return nil
	default:
		return fmt.Errorf("invalid condition type: %s (must be one of: artefact.type, artefact.structural_type, claim.status, agent.role, event.type)", conditionType)
	}
}
