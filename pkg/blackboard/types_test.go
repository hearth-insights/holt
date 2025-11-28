package blackboard

import (
	"testing"
)

// TestArtefactValidate_Valid tests that valid artefacts pass validation
func TestArtefactValidate_Valid(t *testing.T) {
	artefact := &Artefact{
		ID:              NewID(),
		LogicalID:       NewID(),
		Version:         1,
		StructuralType:  StructuralTypeStandard,
		Type:            "CodeCommit",
		ProducedByRole:  "test-agent",
		Payload:         "abc123",
		SourceArtefacts: []string{NewID(), NewID()},
	}

	if err := artefact.Validate(); err != nil {
		t.Errorf("valid artefact failed validation: %v", err)
	}
}

// TestArtefactValidate_EmptySourceArtefacts tests that empty source artefacts array is valid
func TestArtefactValidate_EmptySourceArtefacts(t *testing.T) {
	artefact := &Artefact{
		ID:              NewID(),
		LogicalID:       NewID(),
		Version:         1,
		StructuralType:  StructuralTypeStandard,
		Type:            "GoalDefined",
		ProducedByRole:  "test-agent",
		Payload:         "Create a REST API",
		SourceArtefacts: []string{}, // Empty is valid for root artefacts
	}

	if err := artefact.Validate(); err != nil {
		t.Errorf("artefact with empty source artefacts failed validation: %v", err)
	}
}

// TestArtefactValidate_InvalidVersion tests that version < 1 fails validation
func TestArtefactValidate_InvalidVersion(t *testing.T) {
	testCases := []struct {
		name    string
		version int
	}{
		{"version 0", 0},
		{"negative version", -1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			artefact := &Artefact{
				ID:             NewID(),
				Version:        tc.version,
				StructuralType: StructuralTypeStandard,
				Type:           "CodeCommit",
				ProducedByRole: "test-agent",
				Payload:        "abc123",
			}

			if err := artefact.Validate(); err == nil {
				t.Errorf("expected validation to fail for version %d, but it passed", tc.version)
			}
		})
	}
}

// TestArtefactValidate_InvalidStructuralType tests that invalid structural type fails validation
func TestArtefactValidate_InvalidStructuralType(t *testing.T) {
	artefact := &Artefact{
		ID:             NewID(),
		LogicalID:      NewID(),
		Version:        1,
		StructuralType: "InvalidType",
		Type:           "CodeCommit",
		ProducedByRole: "test-agent",
		Payload:        "abc123",
	}

	if err := artefact.Validate(); err == nil {
		t.Error("expected validation to fail for invalid structural type, but it passed")
	}
}

// TestArtefactValidate_EmptyType tests that empty type fails validation
func TestArtefactValidate_EmptyType(t *testing.T) {
	artefact := &Artefact{
		ID:             NewID(),
		LogicalID:      NewID(),
		Version:        1,
		StructuralType: StructuralTypeStandard,
		Type:           "",
		ProducedByRole: "test-agent",
		Payload:        "abc123",
	}

	if err := artefact.Validate(); err == nil {
		t.Error("expected validation to fail for empty type, but it passed")
	}
}

// TestArtefactValidate_EmptyProducedByRole tests that empty produced_by_role fails validation
func TestArtefactValidate_EmptyProducedByRole(t *testing.T) {
	artefact := &Artefact{
		ID:             NewID(),
		LogicalID:      NewID(),
		Version:        1,
		StructuralType: StructuralTypeStandard,
		Type:           "CodeCommit",
		ProducedByRole: "",
		Payload:        "abc123",
	}

	if err := artefact.Validate(); err == nil {
		t.Error("expected validation to fail for empty produced_by_role, but it passed")
	}
}

// TestStructuralTypeValidate_AllValid tests all valid structural types
func TestStructuralTypeValidate_AllValid(t *testing.T) {
	validTypes := []StructuralType{
		StructuralTypeStandard,
		StructuralTypeReview,
		StructuralTypeQuestion,
		StructuralTypeAnswer,
		StructuralTypeFailure,
		StructuralTypeTerminal,
	}

	for _, st := range validTypes {
		t.Run(string(st), func(t *testing.T) {
			if err := st.Validate(); err != nil {
				t.Errorf("valid structural type %q failed validation: %v", st, err)
			}
		})
	}
}

// TestStructuralTypeValidate_Invalid tests invalid structural type
func TestStructuralTypeValidate_Invalid(t *testing.T) {
	invalidType := StructuralType("InvalidType")
	if err := invalidType.Validate(); err == nil {
		t.Error("expected validation to fail for invalid structural type, but it passed")
	}
}

// TestClaimValidate_Valid tests that valid claims pass validation
func TestClaimValidate_Valid(t *testing.T) {
	claim := &Claim{
		ID:                    NewID(),
		ArtefactID:            NewID(),
		Status:                ClaimStatusPendingReview,
		GrantedReviewAgents:   []string{"agent-1", "agent-2"},
		GrantedParallelAgents: []string{"agent-3"},
		GrantedExclusiveAgent: "",
	}

	if err := claim.Validate(); err != nil {
		t.Errorf("valid claim failed validation: %v", err)
	}
}

// TestClaimValidate_EmptyAgentArrays tests that empty agent arrays are valid
func TestClaimValidate_EmptyAgentArrays(t *testing.T) {
	claim := &Claim{
		ID:                    NewID(),
		ArtefactID:            NewID(),
		Status:                ClaimStatusPendingReview,
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
		GrantedExclusiveAgent: "",
	}

	if err := claim.Validate(); err != nil {
		t.Errorf("claim with empty agent arrays failed validation: %v", err)
	}
}

// TestClaimValidate_InvalidStatus tests that invalid status fails validation
func TestClaimValidate_InvalidStatus(t *testing.T) {
	claim := &Claim{
		ID:         NewID(),
		ArtefactID: NewID(),
		Status:     "invalid-status",
	}

	if err := claim.Validate(); err == nil {
		t.Error("expected validation to fail for invalid status, but it passed")
	}
}

// TestClaimStatusValidate_AllValid tests all valid claim statuses
func TestClaimStatusValidate_AllValid(t *testing.T) {
	validStatuses := []ClaimStatus{
		ClaimStatusPendingReview,
		ClaimStatusPendingParallel,
		ClaimStatusPendingExclusive,
		ClaimStatusComplete,
		ClaimStatusTerminated,
	}

	for _, status := range validStatuses {
		t.Run(string(status), func(t *testing.T) {
			if err := status.Validate(); err != nil {
				t.Errorf("valid claim status %q failed validation: %v", status, err)
			}
		})
	}
}

// TestClaimStatusValidate_Invalid tests invalid claim status
func TestClaimStatusValidate_Invalid(t *testing.T) {
	invalidStatus := ClaimStatus("invalid-status")
	if err := invalidStatus.Validate(); err == nil {
		t.Error("expected validation to fail for invalid claim status, but it passed")
	}
}

// TestBidTypeValidate_AllValid tests all valid bid types
func TestBidTypeValidate_AllValid(t *testing.T) {
	validBidTypes := []BidType{
		BidTypeReview,
		BidTypeParallel,
		BidTypeExclusive,
		BidTypeIgnore,
	}

	for _, bt := range validBidTypes {
		t.Run(string(bt), func(t *testing.T) {
			if err := bt.Validate(); err != nil {
				t.Errorf("valid bid type %q failed validation: %v", bt, err)
			}
		})
	}
}

// TestBidTypeValidate_Invalid tests invalid bid type
func TestBidTypeValidate_Invalid(t *testing.T) {
	invalidBidType := BidType("invalid-bid")
	if err := invalidBidType.Validate(); err == nil {
		t.Error("expected validation to fail for invalid bid type, but it passed")
	}
}
