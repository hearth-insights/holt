package debug

import (
	"context"
	"testing"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
)

func TestEvaluateBreakpointCondition_ArtefactType(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    string
		expected bool
	}{
		{
			name:     "exact match",
			pattern:  "CodeCommit",
			value:    "CodeCommit",
			expected: true,
		},
		{
			name:     "no match",
			pattern:  "CodeCommit",
			value:    "DesignSpec",
			expected: false,
		},
		{
			name:     "glob wildcard suffix",
			pattern:  "*Spec",
			value:    "DesignSpec",
			expected: true,
		},
		{
			name:     "glob wildcard prefix",
			pattern:  "Code*",
			value:    "CodeCommit",
			expected: true,
		},
		{
			name:     "glob wildcard middle",
			pattern:  "Design*Spec",
			value:    "DesignSpecification",
			expected: false, // * doesn't match across parts
		},
		{
			name:     "full wildcard",
			pattern:  "*",
			value:    "AnyValue",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bp := &Breakpoint{
				ID:            "bp-1",
				ConditionType: string(ConditionArtefactType),
				Pattern:       tt.pattern,
			}

			result := evaluateGlobPattern(bp.Pattern, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateBreakpointCondition_StructuralType(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    blackboard.StructuralType
		expected bool
	}{
		{
			name:     "exact match Standard",
			pattern:  "Standard",
			value:    blackboard.StructuralTypeStandard,
			expected: true,
		},
		{
			name:     "exact match Question",
			pattern:  "Question",
			value:    blackboard.StructuralTypeQuestion,
			expected: true,
		},
		{
			name:     "no match",
			pattern:  "Review",
			value:    blackboard.StructuralTypeStandard,
			expected: false,
		},
		{
			name:     "case sensitive",
			pattern:  "standard",
			value:    blackboard.StructuralTypeStandard,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bp := &Breakpoint{
				ID:            "bp-1",
				ConditionType: string(ConditionArtefactStructuralType),
				Pattern:       tt.pattern,
			}

			result := evaluateGlobPattern(bp.Pattern, string(tt.value))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateBreakpointCondition_ClaimStatus(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    blackboard.ClaimStatus
		expected bool
	}{
		{
			name:     "exact match pending_review",
			pattern:  "pending_review",
			value:    blackboard.ClaimStatusPendingReview,
			expected: true,
		},
		{
			name:     "exact match pending_exclusive",
			pattern:  "pending_exclusive",
			value:    blackboard.ClaimStatusPendingExclusive,
			expected: true,
		},
		{
			name:     "no match",
			pattern:  "pending_review",
			value:    blackboard.ClaimStatusPendingExclusive,
			expected: false,
		},
		{
			name:     "glob wildcard",
			pattern:  "pending_*",
			value:    blackboard.ClaimStatusPendingReview,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bp := &Breakpoint{
				ID:            "bp-1",
				ConditionType: string(ConditionClaimStatus),
				Pattern:       tt.pattern,
			}

			result := evaluateGlobPattern(bp.Pattern, string(tt.value))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateBreakpointCondition_EventType(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    EventType
		expected bool
	}{
		{
			name:     "exact match artefact_received",
			pattern:  "artefact_received",
			value:    EventArtefactReceived,
			expected: true,
		},
		{
			name:     "exact match claim_created",
			pattern:  "claim_created",
			value:    EventClaimCreated,
			expected: true,
		},
		{
			name:     "no match",
			pattern:  "artefact_received",
			value:    EventClaimCreated,
			expected: false,
		},
		{
			name:     "glob wildcard",
			pattern:  "*_created",
			value:    EventClaimCreated,
			expected: true,
		},
		{
			name:     "full wildcard matches all",
			pattern:  "*",
			value:    EventReviewConsensusReached,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bp := &Breakpoint{
				ID:            "bp-1",
				ConditionType: string(ConditionEventType),
				Pattern:       tt.pattern,
			}

			result := evaluateGlobPattern(bp.Pattern, string(tt.value))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateBreakpoints_MultipleBreakpoints(t *testing.T) {
	ctx := context.Background()

	// Create artefact
	artefact := &blackboard.Artefact{
		ID:             "art-123",
		Type:           "CodeCommit",
		StructuralType: blackboard.StructuralTypeStandard,
		ProducedByRole: "coder-agent",
	}

	claim := &blackboard.Claim{
		ID:     "claim-456",
		Status: blackboard.ClaimStatusPendingReview,
	}

	// Multiple breakpoints
	breakpoints := []*Breakpoint{
		{
			ID:            "bp-1",
			ConditionType: string(ConditionArtefactType),
			Pattern:       "DesignSpec", // Won't match
		},
		{
			ID:            "bp-2",
			ConditionType: string(ConditionArtefactType),
			Pattern:       "Code*", // Will match
		},
		{
			ID:            "bp-3",
			ConditionType: string(ConditionClaimStatus),
			Pattern:       "pending_review", // Won't be evaluated (artefact event)
		},
	}

	// Evaluate for artefact_received event
	matched := EvaluateBreakpoints(ctx, breakpoints, artefact, claim, EventArtefactReceived)

	// Should match bp-2 (first matching breakpoint)
	assert.NotNil(t, matched)
	assert.Equal(t, "bp-2", matched.ID)
}

func TestEvaluateBreakpoints_NoMatch(t *testing.T) {
	ctx := context.Background()

	artefact := &blackboard.Artefact{
		ID:             "art-123",
		Type:           "CodeCommit",
		StructuralType: blackboard.StructuralTypeStandard,
	}

	breakpoints := []*Breakpoint{
		{
			ID:            "bp-1",
			ConditionType: string(ConditionArtefactType),
			Pattern:       "DesignSpec", // Won't match
		},
		{
			ID:            "bp-2",
			ConditionType: string(ConditionArtefactStructuralType),
			Pattern:       "Review", // Won't match
		},
	}

	matched := EvaluateBreakpoints(ctx, breakpoints, artefact, nil, EventArtefactReceived)
	assert.Nil(t, matched)
}

func TestEvaluateBreakpoints_ClaimStatusEvent(t *testing.T) {
	ctx := context.Background()

	claim := &blackboard.Claim{
		ID:     "claim-456",
		Status: blackboard.ClaimStatusPendingReview,
	}

	breakpoints := []*Breakpoint{
		{
			ID:            "bp-1",
			ConditionType: string(ConditionClaimStatus),
			Pattern:       "pending_review",
		},
	}

	// Evaluate for claim_created event
	matched := EvaluateBreakpoints(ctx, breakpoints, nil, claim, EventClaimCreated)

	assert.NotNil(t, matched)
	assert.Equal(t, "bp-1", matched.ID)
}

func TestEvaluateBreakpoints_EventTypeMatch(t *testing.T) {
	ctx := context.Background()

	breakpoints := []*Breakpoint{
		{
			ID:            "bp-1",
			ConditionType: string(ConditionEventType),
			Pattern:       "review_consensus_reached",
		},
	}

	// Any artefact/claim, but specific event type
	matched := EvaluateBreakpoints(ctx, breakpoints, nil, nil, EventReviewConsensusReached)

	assert.NotNil(t, matched)
	assert.Equal(t, "bp-1", matched.ID)
}

func TestValidateBreakpointPattern_Valid(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{"simple string", "CodeCommit"},
		{"with wildcard", "Code*"},
		{"with prefix wildcard", "*Spec"},
		{"full wildcard", "*"},
		{"with underscore", "pending_review"},
		{"with hyphen", "coder-agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBreakpointPattern(tt.pattern)
			assert.NoError(t, err)
		})
	}
}

func TestValidateBreakpointPattern_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		isValid bool
	}{
		{"unclosed bracket", "Code[", false},
		{"empty pattern", "", true}, // Empty is valid
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBreakpointPattern(tt.pattern)
			if tt.isValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
