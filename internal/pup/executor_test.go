package pup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/google/uuid"
)

// TestPrepareToolInput verifies the tool input JSON structure
// M2.4: This test verifies empty context_chain for root artefacts (no source_artefacts)
func TestPrepareToolInput(t *testing.T) {
	// Note: This is a simplified unit test. Full context assembly is tested in integration tests.
	// For artefacts with no source_artefacts, context_chain will be empty.

	engine := &Engine{
		config: &Config{
			InstanceName: "test-instance",
			AgentName:    "example-agent",
			// M3.7: AgentRole removed - AgentName IS the role
		},
		bbClient: nil, // Not needed for root artefact (empty source_artefacts)
	}

	claim := &blackboard.Claim{
		ID:                    uuid.New().String(),
		ArtefactID:            "art-123",
		Status:                blackboard.ClaimStatusPendingExclusive,
		GrantedExclusiveAgent: "example-agent",
	}

	targetArtefact := &blackboard.Artefact{
		ID:              "art-123",
		LogicalID:       "log-456",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         "Implement user login",
		SourceArtefacts: []string{}, // No sources = root artefact
	}

	jsonStr, err := engine.prepareToolInput(context.Background(), claim, targetArtefact)
	if err != nil {
		t.Fatalf("prepareToolInput failed: %v", err)
	}

	// Unmarshal to verify structure
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &input); err != nil {
		t.Fatalf("Failed to unmarshal tool input: %v", err)
	}

	// Verify claim_type is hardcoded to "exclusive"
	if input["claim_type"] != "exclusive" {
		t.Errorf("Expected claim_type='exclusive', got %v", input["claim_type"])
	}

	// Verify target_artefact is present
	targetArt, ok := input["target_artefact"].(map[string]interface{})
	if !ok {
		t.Fatalf("target_artefact is not an object")
	}

	if targetArt["id"] != "art-123" {
		t.Errorf("Expected target_artefact.id='art-123', got %v", targetArt["id"])
	}

	if targetArt["type"] != "GoalDefined" {
		t.Errorf("Expected target_artefact.type='GoalDefined', got %v", targetArt["type"])
	}

	// Verify context_chain is empty array
	contextChain, ok := input["context_chain"].([]interface{})
	if !ok {
		t.Fatalf("context_chain is not an array")
	}

	if len(contextChain) != 0 {
		t.Errorf("Expected empty context_chain, got %d items", len(contextChain))
	}
}

// TestParseToolOutput_Valid verifies parsing of valid tool output
func TestParseToolOutput_Valid(t *testing.T) {
	engine := &Engine{}

	stdout := `{
		"artefact_type": "CodeCommit",
		"artefact_payload": "abc123def",
		"summary": "Implemented user login feature"
	}`

	// M4.10: parseToolOutput renamed to parseFD3Output
	output, err := engine.parseFD3Output(stdout)
	if err != nil {
		t.Fatalf("parseFD3Output failed: %v", err)
	}

	if output.ArtefactType != "CodeCommit" {
		t.Errorf("Expected ArtefactType='CodeCommit', got %q", output.ArtefactType)
	}

	if output.ArtefactPayload != "abc123def" {
		t.Errorf("Expected ArtefactPayload='abc123def', got %q", output.ArtefactPayload)
	}

	if output.Summary != "Implemented user login feature" {
		t.Errorf("Expected Summary='Implemented user login feature', got %q", output.Summary)
	}
}

// TestParseToolOutput_Invalid verifies error handling for invalid output
func TestParseToolOutput_Invalid(t *testing.T) {
	engine := &Engine{}

	tests := []struct {
		name   string
		stdout string
	}{
		{
			name:   "empty stdout",
			stdout: "",
		},
		{
			name:   "invalid JSON",
			stdout: "not json at all",
		},
		{
			name:   "partial JSON",
			stdout: `{"artefact_type": "Code`,
		},
		{
			name:   "missing required fields",
			stdout: `{"artefact_type": "Code"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// M4.10: parseToolOutput renamed to parseFD3Output
			_, err := engine.parseFD3Output(tt.stdout)
			if err == nil {
				t.Errorf("Expected parseFD3Output to fail for %s, but got nil error", tt.name)
			}
		})
	}
}

// TestCreateResultArtefact_Provenance verifies derivative provenance model
func TestCreateResultArtefact_Provenance(t *testing.T) {
	// This is a conceptual test - we can't run it without Redis
	// But we can verify the logic by examining the code

	engine := &Engine{
		config: &Config{
			// M3.7: AgentRole removed - AgentName IS the role
		},
	}

	claim := &blackboard.Claim{
		ID:         uuid.New().String(),
		ArtefactID: "source-art-123",
	}

	output := &ToolOutput{
		ArtefactType:    "EchoSuccess",
		ArtefactPayload: "echo-456",
		Summary:         "Echo successful",
	}

	// We can't actually create the artefact without Redis, but we can verify
	// that the function would create the correct structure by checking the logic
	// in executor.go:createResultArtefact

	// Key points to verify:
	// 1. New artefact ID is generated (not from claim)
	// 2. logical_id equals the new artefact ID (derivative relationship)
	// 3. version = 1 (first version of new thread)
	// 4. source_artefacts = [claim.ArtefactID]

	// This test serves as documentation of the expected behavior
	if claim.ArtefactID != "source-art-123" {
		t.Errorf("Test setup error")
	}

	if output.ArtefactType != "EchoSuccess" {
		t.Errorf("Test setup error")
	}

	// Actual validation would happen in integration tests with real Redis
	_ = engine
}

// TestTruncate verifies the truncate helper function
func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string not truncated",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length not truncated",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string truncated",
			input:    "hello world this is a long string",
			maxLen:   10,
			expected: "hello worl...",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestLimitedWriter verifies the limitedWriter enforces size limits
func TestLimitedWriter(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		writes    []string
		expected  string
		expectLen int
	}{
		{
			name:      "single write under limit",
			limit:     100,
			writes:    []string{"hello"},
			expected:  "hello",
			expectLen: 5,
		},
		{
			name:      "multiple writes under limit",
			limit:     100,
			writes:    []string{"hello", " ", "world"},
			expected:  "hello world",
			expectLen: 11,
		},
		{
			name:      "single write at limit",
			limit:     5,
			writes:    []string{"hello"},
			expected:  "hello",
			expectLen: 5,
		},
		{
			name:      "single write over limit",
			limit:     5,
			writes:    []string{"hello world"},
			expected:  "hello",
			expectLen: 5,
		},
		{
			name:      "multiple writes exceeding limit",
			limit:     10,
			writes:    []string{"hello", " ", "world", " ", "extra"},
			expected:  "hello worl",
			expectLen: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 0, tt.limit+10)
			writer := &sliceWriter{buf: &buf}
			lw := &limitedWriter{w: writer, limit: tt.limit}

			for _, write := range tt.writes {
				lw.Write([]byte(write))
			}

			result := string(buf)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if len(buf) != tt.expectLen {
				t.Errorf("Expected length %d, got %d", tt.expectLen, len(buf))
			}
		})
	}
}

// sliceWriter is a helper for testing that writes to a byte slice
type sliceWriter struct {
	buf *[]byte
}

func (sw *sliceWriter) Write(p []byte) (n int, err error) {
	*sw.buf = append(*sw.buf, p...)
	return len(p), nil
}

// M4.10: Unit tests for FD 3 Return Model

// TestParseFD3Output_Validation verifies FD 3 result validation (M4.10)
func TestParseFD3Output_Validation(t *testing.T) {
	engine := &Engine{}

	tests := []struct {
		name       string
		fd3Result  string
		shouldFail bool
		errSubstr  string
	}{
		{
			name:       "valid JSON",
			fd3Result:  `{"artefact_type":"Test","artefact_payload":"payload","summary":"ok"}`,
			shouldFail: false,
		},
		{
			name:       "valid JSON with whitespace",
			fd3Result:  "  \n  {\"artefact_type\":\"Test\",\"artefact_payload\":\"\",\"summary\":\"ok\"}  \n  ",
			shouldFail: false,
		},
		{
			name:       "empty FD 3",
			fd3Result:  "",
			shouldFail: true,
			errSubstr:  "no output on FD 3",
		},
		{
			name:       "non-JSON on FD 3",
			fd3Result:  "This is not JSON",
			shouldFail: true,
			errSubstr:  "does not start with JSON",
		},
		{
			name:       "invalid JSON syntax",
			fd3Result:  `{"artefact_type":"Test","artefact_payload":"missing quote}`,
			shouldFail: true,
			errSubstr:  "invalid JSON on FD 3",
		},
		{
			name:       "missing required field",
			fd3Result:  `{"artefact_type":"Test"}`,
			shouldFail: true,
			errSubstr:  "validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := engine.parseFD3Output(tt.fd3Result)

			if tt.shouldFail {
				if err == nil {
					t.Fatalf("Expected error containing %q, got nil", tt.errSubstr)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("Expected error containing %q, got: %v", tt.errSubstr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if output == nil {
					t.Fatal("Expected non-nil output")
				}
			}
		})
	}
}

// TestLimitedReader_M4_10 verifies limitedReader enforces size limit
func TestLimitedReader_M4_10(t *testing.T) {
	t.Run("reads within limit", func(t *testing.T) {
		data := []byte("Hello, World!")
		reader := &limitedReader{
			r:     bytes.NewReader(data),
			limit: 100,
		}

		buf := make([]byte, len(data))
		n, err := reader.Read(buf)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if n != len(data) {
			t.Fatalf("Expected to read %d bytes, got %d", len(data), n)
		}
		if string(buf[:n]) != string(data) {
			t.Fatalf("Expected %q, got %q", string(data), string(buf[:n]))
		}
	})

	t.Run("enforces limit", func(t *testing.T) {
		data := []byte("This is a long string that exceeds the limit")
		reader := &limitedReader{
			r:     bytes.NewReader(data),
			limit: 10,
		}

		// Read first 10 bytes (within limit)
		buf := make([]byte, 10)
		n, err := reader.Read(buf)
		if err != nil {
			t.Fatalf("Unexpected error on first read: %v", err)
		}
		if n != 10 {
			t.Fatalf("Expected to read 10 bytes, got %d", n)
		}

		// Try to read more (should fail with limit error)
		buf2 := make([]byte, 5)
		_, err = reader.Read(buf2)
		if err == nil {
			t.Fatal("Expected error when exceeding limit, got nil")
		}
		if !strings.Contains(err.Error(), "read limit exceeded") {
			t.Fatalf("Expected 'read limit exceeded' error, got: %v", err)
		}
	})
}

// M5.1: Unit Tests for Multi-Artefact Output (Producer Side)

// TestParseToolOutputs_SingleOutput verifies parsing single JSON object (batch_size=1)
func TestParseToolOutputs_SingleOutput(t *testing.T) {
	engine := &Engine{}

	fd3Result := `{"artefact_type":"TestResult","artefact_payload":"test-123","summary":"Single test passed"}`

	outputs, err := engine.parseToolOutputs(fd3Result)
	if err != nil {
		t.Fatalf("parseToolOutputs failed: %v", err)
	}

	if len(outputs) != 1 {
		t.Fatalf("Expected 1 output, got %d", len(outputs))
	}

	if outputs[0].ArtefactType != "TestResult" {
		t.Errorf("Expected ArtefactType='TestResult', got %q", outputs[0].ArtefactType)
	}

	if outputs[0].ArtefactPayload != "test-123" {
		t.Errorf("Expected ArtefactPayload='test-123', got %q", outputs[0].ArtefactPayload)
	}

	if outputs[0].Summary != "Single test passed" {
		t.Errorf("Expected Summary='Single test passed', got %q", outputs[0].Summary)
	}
}

// TestParseToolOutputs_MultipleOutputs verifies parsing multiple JSON objects (batch_size=5)
func TestParseToolOutputs_MultipleOutputs(t *testing.T) {
	engine := &Engine{}

	// Multiple JSON objects separated by newlines
	fd3Result := `{"artefact_type":"TestResult","artefact_payload":"test-1","summary":"Test 1"}
{"artefact_type":"TestResult","artefact_payload":"test-2","summary":"Test 2"}
{"artefact_type":"TestResult","artefact_payload":"test-3","summary":"Test 3"}
{"artefact_type":"TestResult","artefact_payload":"test-4","summary":"Test 4"}
{"artefact_type":"TestResult","artefact_payload":"test-5","summary":"Test 5"}`

	outputs, err := engine.parseToolOutputs(fd3Result)
	if err != nil {
		t.Fatalf("parseToolOutputs failed: %v", err)
	}

	if len(outputs) != 5 {
		t.Fatalf("Expected 5 outputs, got %d", len(outputs))
	}

	// Verify all outputs parsed correctly
	for i, output := range outputs {
		expectedPayload := fmt.Sprintf("test-%d", i+1)
		if output.ArtefactPayload != expectedPayload {
			t.Errorf("Output %d: expected payload %q, got %q", i, expectedPayload, output.ArtefactPayload)
		}

		if output.ArtefactType != "TestResult" {
			t.Errorf("Output %d: expected type 'TestResult', got %q", i, output.ArtefactType)
		}
	}
}

// TestParseToolOutputs_MultipleOutputs_NoNewlines verifies parsing without newline separators
func TestParseToolOutputs_MultipleOutputs_NoNewlines(t *testing.T) {
	engine := &Engine{}

	// JSON objects concatenated without newlines (still valid with json.Decoder)
	fd3Result := `{"artefact_type":"A","artefact_payload":"1","summary":"ok"}{"artefact_type":"B","artefact_payload":"2","summary":"ok"}{"artefact_type":"C","artefact_payload":"3","summary":"ok"}`

	outputs, err := engine.parseToolOutputs(fd3Result)
	if err != nil {
		t.Fatalf("parseToolOutputs failed: %v", err)
	}

	if len(outputs) != 3 {
		t.Fatalf("Expected 3 outputs, got %d", len(outputs))
	}

	expectedTypes := []string{"A", "B", "C"}
	for i, output := range outputs {
		if output.ArtefactType != expectedTypes[i] {
			t.Errorf("Output %d: expected type %q, got %q", i, expectedTypes[i], output.ArtefactType)
		}
	}
}

// TestParseToolOutputs_EmptyOutput verifies error handling for empty FD 3
func TestParseToolOutputs_EmptyOutput(t *testing.T) {
	engine := &Engine{}

	fd3Result := ""

	_, err := engine.parseToolOutputs(fd3Result)
	if err == nil {
		t.Fatal("Expected error for empty FD 3 output, got nil")
	}

	if !strings.Contains(err.Error(), "no output on FD 3") {
		t.Errorf("Expected error message about 'no output on FD 3', got: %v", err)
	}
}

// TestParseToolOutputs_MalformedJSON verifies error handling for invalid JSON
func TestParseToolOutputs_MalformedJSON(t *testing.T) {
	engine := &Engine{}

	tests := []struct {
		name      string
		fd3Result string
		errSubstr string
	}{
		{
			name:      "non-JSON text",
			fd3Result: "This is not JSON at all",
			errSubstr: "does not start with JSON",
		},
		{
			name:      "incomplete JSON",
			fd3Result: `{"artefact_type":"Test","artefact_payload":"incomplete`,
			errSubstr: "invalid JSON on FD 3",
		},
		{
			name:      "first object valid, second invalid",
			fd3Result: `{"artefact_type":"Test","artefact_payload":"ok","summary":"ok"}{"artefact_type":"Bad"`,
			errSubstr: "invalid JSON on FD 3",
		},
		{
			name:      "missing required field",
			fd3Result: `{"artefact_type":"Test"}`,
			errSubstr: "validation failed",
		},
		{
			name:      "empty artefact_type",
			fd3Result: `{"artefact_type":"","artefact_payload":"test","summary":"ok"}`,
			errSubstr: "validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.parseToolOutputs(tt.fd3Result)
			if err == nil {
				t.Fatalf("Expected error for %s, got nil", tt.name)
			}

			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("Expected error containing %q, got: %v", tt.errSubstr, err)
			}
		})
	}
}

// TestParseToolOutputs_WhitespaceHandling verifies trimming and whitespace handling
func TestParseToolOutputs_WhitespaceHandling(t *testing.T) {
	engine := &Engine{}

	// Leading/trailing whitespace should be handled
	fd3Result := `

	{"artefact_type":"Test","artefact_payload":"data","summary":"ok"}

	`

	outputs, err := engine.parseToolOutputs(fd3Result)
	if err != nil {
		t.Fatalf("parseToolOutputs failed with whitespace: %v", err)
	}

	if len(outputs) != 1 {
		t.Fatalf("Expected 1 output, got %d", len(outputs))
	}
}

// TestParseToolOutputs_MixedStructuralTypes verifies handling different structural types
func TestParseToolOutputs_MixedStructuralTypes(t *testing.T) {
	engine := &Engine{}

	// Question artefact has structural_type set
	fd3Result := `{"structural_type":"Question","artefact_type":"QuestionAsked","artefact_payload":"q1","summary":"ok"}
{"artefact_type":"StandardResult","artefact_payload":"r1","summary":"ok"}`

	outputs, err := engine.parseToolOutputs(fd3Result)
	if err != nil {
		t.Fatalf("parseToolOutputs failed: %v", err)
	}

	if len(outputs) != 2 {
		t.Fatalf("Expected 2 outputs, got %d", len(outputs))
	}

	// First output should have explicit structural type
	if outputs[0].StructuralType != string(blackboard.StructuralTypeQuestion) {
		t.Errorf("Expected StructuralType='Question', got %q", outputs[0].StructuralType)
	}

	// Second output should default to Standard
	if outputs[1].GetStructuralType() != blackboard.StructuralTypeStandard {
		t.Errorf("Expected default StructuralType='Standard', got %q", outputs[1].GetStructuralType())
	}
}
