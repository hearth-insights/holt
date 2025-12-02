package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun_AutoApprove(t *testing.T) {
	input := Input{
		TargetArtefact: Artefact{
			Type:    "TestArtefact",
			Version: 1,
			Payload: "test payload",
		},
	}
	inputJSON, _ := json.Marshal(input)
	stdin := bytes.NewReader(inputJSON)
	var stdout, stderr bytes.Buffer

	err := Run(stdin, &stdout, &stderr, func(key string) string {
		if key == "AUTO_APPROVE" {
			return "true"
		}
		return ""
	})

	assert.NoError(t, err)

	var output Output
	err = json.Unmarshal(stdout.Bytes(), &output)
	assert.NoError(t, err)
	assert.Equal(t, "Review", output.StructuralType)
	assert.Equal(t, "Approved by human reviewer", output.Summary)
}

func TestRun_Approve(t *testing.T) {
	input := Input{
		TargetArtefact: Artefact{
			Type:    "TestArtefact",
			Version: 1,
			Payload: "test payload",
		},
	}
	inputJSON, _ := json.Marshal(input)
	
	// Simulate user typing "y"
	stdin := strings.NewReader(string(inputJSON) + "y\n")
	var stdout, stderr bytes.Buffer

	err := Run(stdin, &stdout, &stderr, func(key string) string {
		return ""
	})

	assert.NoError(t, err)

	var output Output
	err = json.Unmarshal(stdout.Bytes(), &output)
	assert.NoError(t, err)
	assert.Equal(t, "Review", output.StructuralType)
	assert.Equal(t, "Approved by human reviewer", output.Summary)
}

func TestRun_RejectWithFeedback(t *testing.T) {
	input := Input{
		TargetArtefact: Artefact{
			Type:    "TestArtefact",
			Version: 1,
			Payload: "test payload",
		},
	}
	inputJSON, _ := json.Marshal(input)
	
	// Simulate user typing "n" then "bad code"
	stdin := strings.NewReader(string(inputJSON) + "n\nbad code\n")
	var stdout, stderr bytes.Buffer

	err := Run(stdin, &stdout, &stderr, func(key string) string {
		return ""
	})

	assert.NoError(t, err)

	var output Output
	err = json.Unmarshal(stdout.Bytes(), &output)
	assert.NoError(t, err)
	assert.Equal(t, "Review", output.StructuralType)
	assert.Equal(t, "Rejected by human reviewer", output.Summary)
	
	var payload ReviewPayload
	err = json.Unmarshal([]byte(output.ArtefactPayload), &payload)
	assert.NoError(t, err)
	assert.Equal(t, "bad code", payload.Feedback)
}

func TestRun_Timeout(t *testing.T) {
	input := Input{
		TargetArtefact: Artefact{
			Type:    "TestArtefact",
			Version: 1,
			Payload: "test payload",
		},
	}
	inputJSON, _ := json.Marshal(input)
	
	// Use a pipe to simulate blocking input
	r, w := io.Pipe()
	defer w.Close()
	
	// Write input JSON then keep pipe open but silent to simulate waiting for user input
	go func() {
		w.Write(inputJSON)
		// Do NOT close w here, or write newline. Just let it hang.
	}()

	var stdout, stderr bytes.Buffer

	err := Run(r, &stdout, &stderr, func(key string) string {
		if key == "REVIEW_TIMEOUT" {
			return "1" // 1 second timeout
		}
		return ""
	})

	assert.NoError(t, err)

	var output Output
	err = json.Unmarshal(stdout.Bytes(), &output)
	assert.NoError(t, err)
	assert.Equal(t, "Failure", output.StructuralType)
	assert.Equal(t, "HumanReviewTimeout", output.ArtefactType)
}
