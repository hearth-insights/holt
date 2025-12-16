package blackboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPayloadValidation_ExactLimit verifies 1MB boundary condition.
// 1,048,576 bytes exactly should PASS.
func TestPayloadValidation_ExactLimit(t *testing.T) {
	payload := &ArtefactPayload{
		Content: strings.Repeat("A", MaxPayloadSize), // Exactly 1MB
	}

	err := payload.Validate()
	assert.NoError(t, err, "exactly 1MB payload should be accepted")
}

// TestPayloadValidation_OneByte Over verifies strict 1MB limit enforcement.
// 1,048,577 bytes (1MB + 1) should FAIL.
func TestPayloadValidation_OneByteOver(t *testing.T) {
	payload := &ArtefactPayload{
		Content: strings.Repeat("A", MaxPayloadSize+1), // 1MB + 1 byte
	}

	err := payload.Validate()
	assert.Error(t, err, "1MB + 1 byte payload should be rejected")
	assert.Contains(t, err.Error(), "exceeds 1MB limit", "error message should mention limit")
	assert.Contains(t, err.Error(), "1048577", "error message should show actual size")
}

// TestPayloadValidation_Empty verifies empty payload is valid.
func TestPayloadValidation_Empty(t *testing.T) {
	payload := &ArtefactPayload{
		Content: "",
	}

	err := payload.Validate()
	assert.NoError(t, err, "empty payload should be valid")
}

// TestPayloadValidation_TwoMB verifies large payload rejection.
func TestPayloadValidation_TwoMB(t *testing.T) {
	payload := &ArtefactPayload{
		Content: strings.Repeat("A", 2*MaxPayloadSize), // 2MB
	}

	err := payload.Validate()
	assert.Error(t, err, "2MB payload should be rejected")
	assert.Contains(t, err.Error(), "exceeds 1MB limit")
}

// TestPayloadValidation_NullBytes verifies binary data handling.
func TestPayloadValidation_NullBytes(t *testing.T) {
	payload := &ArtefactPayload{
		Content: "hello\x00world", // Contains null byte
	}

	err := payload.Validate()
	assert.NoError(t, err, "payload with null bytes should be valid (JSON escapes them)")
}

// TestVerifiableArtefactValidation_ValidRoot verifies root artefact validation.
func TestVerifiableArtefactValidation_ValidRoot(t *testing.T) {
	artefact := &Artefact{
		ID: strings.Repeat("a", 64), // Placeholder hash
		Header: ArtefactHeader{
			ParentHashes:    []string{}, // Root artefact - empty parents valid
			LogicalThreadID: "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9",
			Version:         1,
			CreatedAtMs:     1704067200000,
			ProducedByRole:  "user",
			StructuralType:  StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: ArtefactPayload{
			Content: "test goal",
		},
	}

	err := artefact.Validate()
	assert.NoError(t, err, "valid root artefact should pass validation")
}

// TestVerifiableArtefactValidation_EmptyLogicalThreadID verifies required field check.
func TestVerifiableArtefactValidation_EmptyLogicalThreadID(t *testing.T) {
	artefact := &Artefact{
		ID: strings.Repeat("a", 64),
		Header: ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "", // Empty - should fail
			Version:         1,
			CreatedAtMs:     1704067200000,
			ProducedByRole:  "user",
			StructuralType:  StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: ArtefactPayload{
			Content: "test",
		},
	}

	err := artefact.Validate()
	assert.Error(t, err, "empty LogicalThreadID should fail validation")
}

// TestVerifiableArtefactValidation_InvalidHash verifies hash format check.
func TestVerifiableArtefactValidation_InvalidHash(t *testing.T) {
	artefact := &Artefact{
		ID: "not-a-valid-hash", // Too short, wrong format
		Header: ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9",
			Version:         1,
			CreatedAtMs:     1704067200000,
			ProducedByRole:  "user",
			StructuralType:  StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: ArtefactPayload{
			Content: "test",
		},
	}

	err := artefact.Validate()
	assert.Error(t, err, "invalid hash format should fail validation")
	assert.Contains(t, err.Error(), "invalid hash", "error should mention hash validation")
}

// TestVerifiableArtefactValidation_OversizePayload verifies integrated validation.
func TestVerifiableArtefactValidation_OversizePayload(t *testing.T) {
	artefact := &Artefact{
		ID: strings.Repeat("a", 64),
		Header: ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: "0508eb36a3d0dd327c235b6d900f26455a2ee715300f1c4b78c3d3edce8dafe9",
			Version:         1,
			CreatedAtMs:     1704067200000,
			ProducedByRole:  "user",
			StructuralType:  StructuralTypeStandard,
			Type:            "GoalDefined",
		},
		Payload: ArtefactPayload{
			Content: strings.Repeat("A", 2*MaxPayloadSize), // 2MB - too large
		},
	}

	err := artefact.Validate()
	assert.Error(t, err, "oversize payload should fail validation")
	assert.Contains(t, err.Error(), "exceeds 1MB limit")
}
