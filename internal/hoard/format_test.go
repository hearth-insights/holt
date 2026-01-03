package hoard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatPayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		expected string
	}{
		{
			name:     "empty payload",
			payload:  "",
			expected: "-",
		},
		{
			name:     "short single line",
			payload:  "hello.txt",
			expected: "hello.txt",
		},
		{
			name:     "exactly 40 chars",
			payload:  strings.Repeat("a", 40),
			expected: strings.Repeat("a", 40),
		},
		{
			name:     "41 chars - should truncate",
			payload:  strings.Repeat("a", 41),
			expected: strings.Repeat("a", 37) + "...",
		},
		{
			name:     "long payload - should truncate",
			payload:  "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6",
			expected: "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3...",
		},
		{
			name:     "multi-line payload - first line only",
			payload:  "First line\nSecond line\nThird line",
			expected: "First line",
		},
		{
			name:     "multi-line with long first line",
			payload:  strings.Repeat("x", 70) + "\nSecond line",
			expected: strings.Repeat("x", 37) + "...",
		},
		{
			name:     "payload with leading/trailing whitespace",
			payload:  "  \n  hello world  \n  ",
			expected: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPayload(tt.payload)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatProducedBy(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		expected string
	}{
		{
			name:     "empty role",
			role:     "",
			expected: "-",
		},
		{
			name:     "user role",
			role:     "user",
			expected: "user",
		},
		{
			name:     "agent role",
			role:     "git-agent",
			expected: "git-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatProducedBy(tt.role)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatTable(t *testing.T) {
	t.Run("empty artefacts", func(t *testing.T) {
		var buf bytes.Buffer
		FormatTable(&buf, []*blackboard.Artefact{}, nil, "test-instance")

		output := buf.String()
		assert.Contains(t, output, "No artefacts found for instance 'test-instance'")
	})

	t.Run("single artefact", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					Type:           "GoalDefined",
					ProducedByRole: "test-agent",
				},
				Payload: blackboard.ArtefactPayload{
					Content: "hello.txt",
				},
			},
		}

		var buf bytes.Buffer
		FormatTable(&buf, artefacts, nil, "test-instance")

		output := buf.String()
		assert.Contains(t, output, "Artefacts for instance 'test-instance'")
		assert.Contains(t, output, "abc-123")
		assert.Contains(t, output, "Goal") // formatType shortens "GoalDefined" to "Goal"
		assert.Contains(t, output, "test-agent")
		assert.Contains(t, output, "hello.txt")
		assert.Contains(t, output, "1 artefact found")
	})

	t.Run("multiple artefacts", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					Type:           "GoalDefined",
					ProducedByRole: "test-agent",
				},
				Payload: blackboard.ArtefactPayload{
					Content: "hello.txt",
				},
			},
			{
				ID: "def-456",
				Header: blackboard.ArtefactHeader{
					Type:           "CodeCommit",
					ProducedByRole: "test-agent",
				},
				Payload: blackboard.ArtefactPayload{
					Content: "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a",
				},
			},
		}

		var buf bytes.Buffer
		FormatTable(&buf, artefacts, nil, "test-instance")

		output := buf.String()
		assert.Contains(t, output, "abc-123")
		assert.Contains(t, output, "def-456")
		assert.Contains(t, output, "2 artefacts found")
	})

	t.Run("artefact with empty fields", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					Type:           "Unknown",
					ProducedByRole: "test-agent",
				},
				Payload: blackboard.ArtefactPayload{
					Content: "",
				},
			},
		}

		var buf bytes.Buffer
		FormatTable(&buf, artefacts, nil, "test-instance")

		output := buf.String()
		// Should contain "-" for empty fields
		assert.Contains(t, output, "-")
	})

	t.Run("artefact with long payload", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					Type:           "CodeCommit",
					ProducedByRole: "test-agent",
				},
				Payload: blackboard.ArtefactPayload{
					Content: strings.Repeat("x", 100),
				},
			},
		}

		var buf bytes.Buffer
		FormatTable(&buf, artefacts, nil, "test-instance")

		output := buf.String()
		// Payload should be truncated with "..."
		assert.Contains(t, output, "...")
		// Should not contain the full 100 char payload
		assert.NotContains(t, output, strings.Repeat("x", 100))
	})
}

func TestFormatJSONL(t *testing.T) {
	t.Run("empty artefacts", func(t *testing.T) {
		var buf bytes.Buffer
		err := FormatJSONL(&buf, []*blackboard.Artefact{})

		require.NoError(t, err)

		// Should be empty (no lines)
		assert.Empty(t, buf.String())
	})

	t.Run("single artefact", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: "logical-1",
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "GoalDefined",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "hello.txt",
				},
			},
		}

		var buf bytes.Buffer
		err := FormatJSONL(&buf, artefacts)

		require.NoError(t, err)

		// Should be one JSON object per line (JSONL format)
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		assert.Len(t, lines, 1)

		// Parse the single line
		var result blackboard.Artefact
		err = json.Unmarshal([]byte(lines[0]), &result)
		require.NoError(t, err)
		assert.Equal(t, "abc-123", result.ID)
		assert.Equal(t, "GoalDefined", result.Header.Type)
		assert.Equal(t, "hello.txt", result.Payload.Content)
	})

	t.Run("multiple artefacts with full data", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: "logical-1",
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "GoalDefined",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "hello.txt",
				},
			},
			{
				ID: "def-456",
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: "logical-2",
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "CodeCommit",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{"abc-123"},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a",
				},
			},
		}

		var buf bytes.Buffer
		err := FormatJSONL(&buf, artefacts)

		require.NoError(t, err)

		// Should be two JSON objects, one per line (JSONL format)
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		assert.Len(t, lines, 2)

		// Parse first line
		var result1 blackboard.Artefact
		err = json.Unmarshal([]byte(lines[0]), &result1)
		require.NoError(t, err)
		assert.Equal(t, "abc-123", result1.ID)
		assert.Equal(t, "logical-1", result1.Header.LogicalThreadID)
		assert.Equal(t, 1, result1.Header.Version)
		assert.Equal(t, blackboard.StructuralTypeStandard, result1.Header.StructuralType)

		// Parse second line
		var result2 blackboard.Artefact
		err = json.Unmarshal([]byte(lines[1]), &result2)
		require.NoError(t, err)
		assert.Equal(t, "def-456", result2.ID)
		assert.Equal(t, []string{"abc-123"}, result2.Header.ParentHashes)
	})

	t.Run("preserves multi-line payloads", func(t *testing.T) {
		artefacts := []*blackboard.Artefact{
			{
				ID: "abc-123",
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: "logical-1",
					Version:         1,
					StructuralType:  blackboard.StructuralTypeStandard,
					Type:            "Config",
					ProducedByRole:  "test-agent",
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{
					Content: "line1\nline2\nline3",
				},
			},
		}

		var buf bytes.Buffer
		err := FormatJSONL(&buf, artefacts)

		require.NoError(t, err)

		// JSONL format: one object per line
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		assert.Len(t, lines, 1)

		var result blackboard.Artefact
		err = json.Unmarshal([]byte(lines[0]), &result)
		require.NoError(t, err)

		// Multi-line payload should be preserved
		assert.Equal(t, "line1\nline2\nline3", result.Payload.Content)
	})
}

func TestFormatSingleJSON(t *testing.T) {
	t.Run("single artefact", func(t *testing.T) {
		artefact := &blackboard.Artefact{
			ID: "abc-123",
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "logical-1",
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "GoalDefined",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "hello.txt",
			},
		}

		var buf bytes.Buffer
		err := FormatSingleJSON(&buf, artefact)

		require.NoError(t, err)

		// Should be valid JSON object
		var result blackboard.Artefact
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, "abc-123", result.ID)
		assert.Equal(t, "GoalDefined", result.Header.Type)
	})

	t.Run("preserves all fields", func(t *testing.T) {
		artefact := &blackboard.Artefact{
			ID: "def-456",
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "logical-2",
				Version:         2,
				StructuralType:  blackboard.StructuralTypeReview,
				Type:            "ReviewFeedback",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{"abc-123", "xyz-789"},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "Some feedback\nwith multiple lines",
			},
		}

		var buf bytes.Buffer
		err := FormatSingleJSON(&buf, artefact)

		require.NoError(t, err)

		var result blackboard.Artefact
		err = json.Unmarshal(buf.Bytes(), &result)
		require.NoError(t, err)

		assert.Equal(t, "def-456", result.ID)
		assert.Equal(t, "logical-2", result.Header.LogicalThreadID)
		assert.Equal(t, 2, result.Header.Version)
		assert.Equal(t, blackboard.StructuralTypeReview, result.Header.StructuralType)
		assert.Equal(t, "ReviewFeedback", result.Header.Type)
		assert.Equal(t, "Some feedback\nwith multiple lines", result.Payload.Content)
		assert.Equal(t, []string{"abc-123", "xyz-789"}, result.Header.ParentHashes)
		assert.Equal(t, "test-agent", result.Header.ProducedByRole)
	})

	t.Run("pretty printed with indentation", func(t *testing.T) {
		artefact := &blackboard.Artefact{
			ID: "abc-123",
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "logical-1",
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "Test",
				ProducedByRole:  "test-agent",
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{
				Content: "test",
			},
		}

		var buf bytes.Buffer
		err := FormatSingleJSON(&buf, artefact)

		require.NoError(t, err)

		output := buf.String()
		// Check for pretty-printed format (should have newlines and indentation)
		assert.Contains(t, output, "\n")
		assert.Contains(t, output, "  ") // indentation
	})
}
