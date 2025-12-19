package pup

import (
	"testing"

	"github.com/hearth-insights/holt/pkg/blackboard"
)

// TestFilterContextArtefacts verifies filtering to Standard, Answer, and Review (M3.3)
func TestFilterContextArtefacts(t *testing.T) {
	contextMap := map[string]*blackboard.Artefact{
		"log-1": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-1",
				Type:            "GoalDefined",
				StructuralType:  blackboard.StructuralTypeStandard,
			},
		},
		"log-2": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-2",
				Type:            "DesignSpec",
				StructuralType:  blackboard.StructuralTypeStandard,
			},
		},
		"log-3": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-3",
				Type:            "ToolFailure",
				StructuralType:  blackboard.StructuralTypeFailure,
			},
		},
		"log-4": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-4",
				Type:            "UserAnswer",
				StructuralType:  blackboard.StructuralTypeAnswer,
			},
		},
		"log-5": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-5",
				Type:            "CodeReview",
				StructuralType:  blackboard.StructuralTypeReview,
			},
		},
	}

	filtered := filterContextArtefacts(contextMap)

	// M3.3: Should include Standard, Answer, and Review (4 artefacts)
	if len(filtered) != 4 {
		t.Errorf("Expected 4 filtered artefacts, got %d", len(filtered))
	}

	// Verify only Standard, Answer, and Review types present
	for _, art := range filtered {
		if art.Header.StructuralType != blackboard.StructuralTypeStandard &&
			art.Header.StructuralType != blackboard.StructuralTypeAnswer &&
			art.Header.StructuralType != blackboard.StructuralTypeReview {
			t.Errorf("Filtered artefact has wrong structural_type: %s", art.Header.StructuralType)
		}
	}

	// Verify only Failure was filtered out
	for _, art := range filtered {
		if art.Header.LogicalThreadID == "log-3" {
			t.Errorf("Failure artefact should have been filtered out: %s", art.Header.LogicalThreadID)
		}
	}
}

// TestFilterContextArtefacts_EmptyMap verifies empty map returns empty slice
func TestFilterContextArtefacts_EmptyMap(t *testing.T) {
	contextMap := make(map[string]*blackboard.Artefact)
	filtered := filterContextArtefacts(contextMap)

	if len(filtered) != 0 {
		t.Errorf("Expected empty filtered slice, got %d artefacts", len(filtered))
	}
}

// TestFilterContextArtefacts_AllFiltered verifies all artefacts can be filtered
func TestFilterContextArtefacts_AllFiltered(t *testing.T) {
	contextMap := map[string]*blackboard.Artefact{
		"log-1": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-1",
				StructuralType:  blackboard.StructuralTypeFailure,
			},
		},
		"log-2": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-2",
				StructuralType:  blackboard.StructuralTypeQuestion,
			},
		},
		"log-3": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-3",
				StructuralType:  blackboard.StructuralTypeTerminal,
			},
		},
	}

	filtered := filterContextArtefacts(contextMap)

	if len(filtered) != 0 {
		t.Errorf("Expected all artefacts filtered out, got %d", len(filtered))
	}
}

// TestSortContextChronologically verifies oldest-first ordering
func TestSortContextChronologically(t *testing.T) {
	// Input in BFS order (newest first, discovered from target backwards)
	artefacts := []*blackboard.Artefact{
		{Header: blackboard.ArtefactHeader{LogicalThreadID: "newest", Type: "Third"}},
		{Header: blackboard.ArtefactHeader{LogicalThreadID: "middle", Type: "Second"}},
		{Header: blackboard.ArtefactHeader{LogicalThreadID: "oldest", Type: "First"}},
	}

	sorted := sortContextChronologically(artefacts)

	// Should be reversed (oldest first)
	if len(sorted) != 3 {
		t.Fatalf("Expected 3 sorted artefacts, got %d", len(sorted))
	}

	if sorted[0].Header.LogicalThreadID != "oldest" {
		t.Errorf("Expected oldest artefact first, got %s", sorted[0].Header.LogicalThreadID)
	}

	if sorted[1].Header.LogicalThreadID != "middle" {
		t.Errorf("Expected middle artefact second, got %s", sorted[1].Header.LogicalThreadID)
	}

	if sorted[2].Header.LogicalThreadID != "newest" {
		t.Errorf("Expected newest artefact last, got %s", sorted[2].Header.LogicalThreadID)
	}
}

// TestSortContextChronologically_EmptySlice verifies empty slice handled
func TestSortContextChronologically_EmptySlice(t *testing.T) {
	artefacts := []*blackboard.Artefact{}
	sorted := sortContextChronologically(artefacts)

	if len(sorted) != 0 {
		t.Errorf("Expected empty sorted slice, got %d artefacts", len(sorted))
	}
}

// TestSortContextChronologically_SingleArtefact verifies single artefact
func TestSortContextChronologically_SingleArtefact(t *testing.T) {
	artefacts := []*blackboard.Artefact{
		{Header: blackboard.ArtefactHeader{LogicalThreadID: "only", Type: "OnlyOne"}},
	}

	sorted := sortContextChronologically(artefacts)

	if len(sorted) != 1 {
		t.Fatalf("Expected 1 sorted artefact, got %d", len(sorted))
	}

	if sorted[0].Header.LogicalThreadID != "only" {
		t.Errorf("Expected 'only' artefact, got %s", sorted[0].Header.LogicalThreadID)
	}
}

// M4.3: Knowledge filtering and role matching tests

// TestMatchesRole verifies glob pattern matching for role filtering
func TestMatchesRole(t *testing.T) {
	tests := []struct {
		name            string
		agentRole       string
		contextForRoles []string
		expected        bool
	}{
		{
			name:            "wildcard matches all",
			agentRole:       "any-agent",
			contextForRoles: []string{"*"},
			expected:        true,
		},
		{
			name:            "empty array matches all",
			agentRole:       "any-agent",
			contextForRoles: []string{},
			expected:        true,
		},
		{
			name:            "exact match",
			agentRole:       "coder-agent",
			contextForRoles: []string{"coder-agent"},
			expected:        true,
		},
		{
			name:            "glob prefix match",
			agentRole:       "coder-backend",
			contextForRoles: []string{"coder-*"},
			expected:        true,
		},
		{
			name:            "glob suffix match",
			agentRole:       "backend-coder",
			contextForRoles: []string{"*-coder"},
			expected:        true,
		},
		{
			name:            "no match",
			agentRole:       "reviewer",
			contextForRoles: []string{"coder-*", "tester-*"},
			expected:        false,
		},
		{
			name:            "multiple patterns first matches",
			agentRole:       "coder-go",
			contextForRoles: []string{"coder-*", "reviewer-*"},
			expected:        true,
		},
		{
			name:            "multiple patterns second matches",
			agentRole:       "reviewer-security",
			contextForRoles: []string{"coder-*", "reviewer-*"},
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &Engine{
				config: &Config{AgentName: tt.agentRole},
			}
			result := engine.matchesRole(tt.contextForRoles)
			if result != tt.expected {
				t.Errorf("matchesRole(%v) = %v, expected %v for role %s",
					tt.contextForRoles, result, tt.expected, tt.agentRole)
			}
		})
	}
}

// TestFilterAndMergeKnowledge verifies role filtering and latest-version-wins strategy
func TestFilterAndMergeKnowledge(t *testing.T) {
	t.Run("filters by role", func(t *testing.T) {
		engine := &Engine{
			config: &Config{AgentName: "coder-backend"},
		}

		allKnowledge := []*blackboard.Artefact{
			{
				Header: blackboard.ArtefactHeader{
					Type:            "go-sdk-docs",
					Version:         1,
					ContextForRoles: []string{"coder-*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Go SDK documentation"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "review-guidelines",
					Version:         1,
					ContextForRoles: []string{"reviewer-*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Review guidelines"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "global-config",
					Version:         1,
					ContextForRoles: []string{"*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Global config"},
			},
		}

		filtered, err := engine.filterAndMergeKnowledge(allKnowledge)
		if err != nil {
			t.Fatalf("filterAndMergeKnowledge failed: %v", err)
		}

		// Should match: go-sdk-docs (coder-*) and global-config (*)
		// Should NOT match: review-guidelines (reviewer-*)
		if len(filtered) != 2 {
			t.Errorf("Expected 2 filtered knowledge, got %d", len(filtered))
		}

		found := make(map[string]bool)
		for _, k := range filtered {
			found[k.Header.Type] = true
		}

		if !found["go-sdk-docs"] {
			t.Error("Expected go-sdk-docs to be included")
		}
		if !found["global-config"] {
			t.Error("Expected global-config to be included")
		}
		if found["review-guidelines"] {
			t.Error("review-guidelines should have been filtered out")
		}
	})

	t.Run("latest version wins", func(t *testing.T) {
		engine := &Engine{
			config: &Config{AgentName: "coder"},
		}

		allKnowledge := []*blackboard.Artefact{
			{
				Header: blackboard.ArtefactHeader{
					Type:            "api-docs",
					Version:         1,
					ContextForRoles: []string{"*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Version 1"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "api-docs",
					Version:         3,
					ContextForRoles: []string{"*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Version 3"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "api-docs",
					Version:         2,
					ContextForRoles: []string{"*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Version 2"},
			},
		}

		filtered, err := engine.filterAndMergeKnowledge(allKnowledge)
		if err != nil {
			t.Fatalf("filterAndMergeKnowledge failed: %v", err)
		}

		// Should only have one result (latest version)
		if len(filtered) != 1 {
			t.Errorf("Expected 1 merged knowledge, got %d", len(filtered))
		}

		if filtered[0].Header.Version != 3 {
			t.Errorf("Expected version 3 (latest), got version %d", filtered[0].Header.Version)
		}

		if filtered[0].Payload.Content != "Version 3" {
			t.Errorf("Expected 'Version 3' payload, got %s", filtered[0].Payload.Content)
		}
	})

	t.Run("multiple knowledge with different names", func(t *testing.T) {
		engine := &Engine{
			config: &Config{AgentName: "backend-coder"},
		}

		allKnowledge := []*blackboard.Artefact{
			{
				Header: blackboard.ArtefactHeader{
					Type:            "api-docs",
					Version:         2,
					ContextForRoles: []string{"*-coder"},
				},
				Payload: blackboard.ArtefactPayload{Content: "API v2"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "api-docs",
					Version:         1,
					ContextForRoles: []string{"*-coder"},
				},
				Payload: blackboard.ArtefactPayload{Content: "API v1"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "db-schema",
					Version:         3,
					ContextForRoles: []string{"backend-*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Schema v3"},
			},
			{
				Header: blackboard.ArtefactHeader{
					Type:            "db-schema",
					Version:         1,
					ContextForRoles: []string{"backend-*"},
				},
				Payload: blackboard.ArtefactPayload{Content: "Schema v1"},
			},
		}

		filtered, err := engine.filterAndMergeKnowledge(allKnowledge)
		if err != nil {
			t.Fatalf("filterAndMergeKnowledge failed: %v", err)
		}

		// Should have 2 results (latest of each name)
		if len(filtered) != 2 {
			t.Errorf("Expected 2 merged knowledge, got %d", len(filtered))
		}

		byName := make(map[string]*blackboard.Artefact)
		for _, k := range filtered {
			byName[k.Header.Type] = k
		}

		if byName["api-docs"].Header.Version != 2 {
			t.Errorf("Expected api-docs version 2, got %d", byName["api-docs"].Header.Version)
		}

		if byName["db-schema"].Header.Version != 3 {
			t.Errorf("Expected db-schema version 3, got %d", byName["db-schema"].Header.Version)
		}
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		engine := &Engine{
			config: &Config{AgentName: "tester"},
		}

		allKnowledge := []*blackboard.Artefact{
			{
				Header: blackboard.ArtefactHeader{
					Type:            "coder-only",
					Version:         1,
					ContextForRoles: []string{"coder-*"},
				},
			},
		}

		filtered, err := engine.filterAndMergeKnowledge(allKnowledge)
		if err != nil {
			t.Fatalf("filterAndMergeKnowledge failed: %v", err)
		}

		if len(filtered) != 0 {
			t.Errorf("Expected 0 filtered knowledge, got %d", len(filtered))
		}
	})
}

// TestFilterContextArtefacts_FiltersOutKnowledge verifies Knowledge artefacts are excluded from context chain
func TestFilterContextArtefacts_FiltersOutKnowledge(t *testing.T) {
	contextMap := map[string]*blackboard.Artefact{
		"log-1": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-1",
				Type:            "GoalDefined",
				StructuralType:  blackboard.StructuralTypeStandard,
			},
		},
		"log-2": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-2",
				Type:            "api-docs",
				StructuralType:  blackboard.StructuralTypeKnowledge,
			},
		},
		"log-3": {
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "log-3",
				Type:            "CodeCommit",
				StructuralType:  blackboard.StructuralTypeStandard,
			},
		},
	}

	filtered := filterContextArtefacts(contextMap)

	// Should include only Standard artefacts (2), not Knowledge
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered artefacts (excluding Knowledge), got %d", len(filtered))
	}

	// Verify Knowledge was filtered out
	for _, art := range filtered {
		if art.Header.StructuralType == blackboard.StructuralTypeKnowledge {
			t.Errorf("Knowledge artefact should have been filtered out")
		}
	}
}
