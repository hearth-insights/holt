package config

import (
	"strings"
	"testing"
)

// M5.1: Unit Tests for Synchronize Configuration Validation

// TestAgent_Validate_Synchronize tests synchronize block validation
func TestAgent_Validate_Synchronize(t *testing.T) {
	tests := []struct {
		name          string
		agent         Agent
		expectError   bool
		errorContains string
	}{
		// Valid synchronize configurations
		{
			name: "valid synchronize with Named pattern",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
						{Type: "LintResult"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid synchronize with Producer-Declared pattern",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "DataBatch",
					WaitFor: []WaitCondition{
						{Type: "ProcessedRecord", CountFromMetadata: "batch_size"},
					},
				},
			},
			expectError: false,
		},
		{
			name: "INVALID: mixed patterns (count_from_metadata with multiple wait_for)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "Workflow",
					WaitFor: []WaitCondition{
						{Type: "StepComplete"}, // Named
						{Type: "BatchResult", CountFromMetadata: "expected_count"}, // Producer-Declared
					},
				},
			},
			expectError:   true,
			errorContains: "count_from_metadata pattern requires exactly ONE wait_for condition",
		},
		{
			name: "INVALID: single wait_for without count_from_metadata (not a fan-in)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"}, // Only one type, no count_from_metadata
					},
				},
			},
			expectError:   true,
			errorContains: "single wait_for without count_from_metadata is not a valid merge",
		},
		{
			name: "INVALID: duplicate types in TYPES mode",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
						{Type: "LintResult"},
						{Type: "TestResult"}, // DUPLICATE!
					},
				},
			},
			expectError:   true,
			errorContains: "duplicate type 'TestResult' in wait_for",
		},
		{
			name: "valid synchronize with max_depth=0 (unlimited)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
						{Type: "LintResult"}, // TYPES mode requires 2+ types
					},
					MaxDepth: 0, // Unlimited depth
				},
			},
			expectError: false,
		},
		{
			name: "valid synchronize with max_depth=5",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
						{Type: "LintResult"}, // TYPES mode requires 2+ types
					},
					MaxDepth: 5,
				},
			},
			expectError: false,
		},

		// Mutual exclusivity errors
		{
			name: "synchronize with bidding_strategy (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
					},
				},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"}, // Conflict!
			},
			expectError:   true,
			errorContains: "synchronize and bidding_strategy are mutually exclusive",
		},
		{
			name: "synchronize with bid_script (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
					},
				},
				BidScript: []string{"/app/bid.sh"}, // Conflict!
			},
			expectError:   true,
			errorContains: "synchronize and bid_script are mutually exclusive",
		},

		// Missing required fields
		{
			name: "missing ancestor_type (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					// AncestorType missing!
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
					},
				},
			},
			expectError:   true,
			errorContains: "missing ancestor_type",
		},
		{
			name: "empty wait_for list (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor:      []WaitCondition{}, // Empty!
				},
			},
			expectError:   true,
			errorContains: "empty wait_for list",
		},
		{
			name: "wait_for condition missing type (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult"},
						{Type: ""}, // Missing type!
					},
				},
			},
			expectError:   true,
			errorContains: "wait_for[1] missing type",
		},
		{
			name: "negative max_depth (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: "TestResult", CountFromMetadata: "count"}, // M5.1.1 REFACTOR: Use COUNT mode to pass single wait_for check
					},
					MaxDepth: -5, // Invalid!
				},
			},
			expectError:   true,
			errorContains: "max_depth must be >= 0",
		},

		// Missing all bidding strategies (error)
		{
			name: "no bidding_strategy, bid_script, or synchronize (error)",
			agent: Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				// No Synchronize, no BiddingStrategy, no BidScript
			},
			expectError:   true,
			errorContains: "either bidding_strategy, bid_script, or synchronize must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.Validate("test-agent")

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectError && err != nil && tt.errorContains != "" {
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			}
		})
	}
}

// TestAgent_Validate_Synchronize_EdgeCases tests edge cases for synchronize validation
func TestAgent_Validate_Synchronize_EdgeCases(t *testing.T) {
	t.Run("single wait_for condition", func(t *testing.T) {
		// M5.1.1 REFACTOR: Single wait_for requires count_from_metadata (COUNT mode)
		agent := Agent{
			Image:   "test:latest",
			Command: []string{"/app/run.sh"},
			Synchronize: &SynchronizeConfig{
				AncestorType: "CodeCommit",
				WaitFor: []WaitCondition{
					{Type: "TestResult", CountFromMetadata: "batch_size"}, // COUNT mode
				},
			},
		}

		err := agent.Validate("test-agent")
		if err != nil {
			t.Errorf("Single wait_for with count_from_metadata should be valid, got error: %v", err)
		}
	})

	t.Run("many wait_for conditions", func(t *testing.T) {
		agent := Agent{
			Image:   "test:latest",
			Command: []string{"/app/run.sh"},
			Synchronize: &SynchronizeConfig{
				AncestorType: "CodeCommit",
				WaitFor: []WaitCondition{
					{Type: "Type1"},
					{Type: "Type2"},
					{Type: "Type3"},
					{Type: "Type4"},
					{Type: "Type5"},
					{Type: "Type6"},
					{Type: "Type7"},
					{Type: "Type8"},
					{Type: "Type9"},
					{Type: "Type10"},
				},
			},
		}

		err := agent.Validate("test-agent")
		if err != nil {
			t.Errorf("Many wait_for conditions should be valid, got error: %v", err)
		}
	})

	t.Run("count_from_metadata can be empty for Named pattern", func(t *testing.T) {
		// M5.1.1 REFACTOR: TYPES mode (Named pattern) requires 2+ types
		agent := Agent{
			Image:   "test:latest",
			Command: []string{"/app/run.sh"},
			Synchronize: &SynchronizeConfig{
				AncestorType: "CodeCommit",
				WaitFor: []WaitCondition{
					{Type: "TestResult", CountFromMetadata: ""},  // Empty is valid for TYPES mode
					{Type: "LintResult", CountFromMetadata: ""}, // Need 2+ types for valid TYPES mode
				},
			},
		}

		err := agent.Validate("test-agent")
		if err != nil {
			t.Errorf("Empty CountFromMetadata should be valid (Named pattern), got error: %v", err)
		}
	})

	t.Run("ancestor_type can be any non-empty string", func(t *testing.T) {
		testCases := []string{
			"CustomType",
			"My-Special-Type",
			"Type_With_Underscores",
			"123NumericStart",
		}

		for _, ancestorType := range testCases {
			// M5.1.1 REFACTOR: Use COUNT mode (single wait_for with count_from_metadata)
			agent := Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: ancestorType,
					WaitFor: []WaitCondition{
						{Type: "SomeType", CountFromMetadata: "count"},
					},
				},
			}

			err := agent.Validate("test-agent")
			if err != nil {
				t.Errorf("AncestorType %q should be valid, got error: %v", ancestorType, err)
			}
		}
	})

	t.Run("wait_for type can be any non-empty string", func(t *testing.T) {
		testCases := []string{
			"CustomType",
			"My-Special-Type",
			"Type_With_Underscores",
			"123NumericStart",
		}

		for _, waitType := range testCases {
			// M5.1.1 REFACTOR: Use COUNT mode (single wait_for with count_from_metadata)
			agent := Agent{
				Image:   "test:latest",
				Command: []string{"/app/run.sh"},
				Synchronize: &SynchronizeConfig{
					AncestorType: "CodeCommit",
					WaitFor: []WaitCondition{
						{Type: waitType, CountFromMetadata: "count"},
					},
				},
			}

			err := agent.Validate("test-agent")
			if err != nil {
				t.Errorf("WaitFor type %q should be valid, got error: %v", waitType, err)
			}
		}
	})
}

// TestAgent_Validate_Synchronize_Multiple_Errors tests error index in wait_for
func TestAgent_Validate_Synchronize_Multiple_Errors(t *testing.T) {
	t.Run("error reports correct wait_for index", func(t *testing.T) {
		agent := Agent{
			Image:   "test:latest",
			Command: []string{"/app/run.sh"},
			Synchronize: &SynchronizeConfig{
				AncestorType: "CodeCommit",
				WaitFor: []WaitCondition{
					{Type: "TestResult"},   // Valid
					{Type: "LintResult"},   // Valid
					{Type: ""},             // Invalid at index 2
					{Type: "SecurityScan"}, // Valid
				},
			},
		}

		err := agent.Validate("test-agent")
		if err == nil {
			t.Fatal("Expected error for empty type at index 2")
		}

		if !strings.Contains(err.Error(), "wait_for[2]") {
			t.Errorf("Expected error to mention index [2], got: %v", err)
		}
	})

	t.Run("first empty type is reported", func(t *testing.T) {
		agent := Agent{
			Image:   "test:latest",
			Command: []string{"/app/run.sh"},
			Synchronize: &SynchronizeConfig{
				AncestorType: "CodeCommit",
				WaitFor: []WaitCondition{
					{Type: ""}, // Invalid at index 0
					{Type: ""}, // Also invalid but should report first
				},
			},
		}

		err := agent.Validate("test-agent")
		if err == nil {
			t.Fatal("Expected error for empty type")
		}

		// Should report first error (index 0)
		if !strings.Contains(err.Error(), "wait_for[0]") {
			t.Errorf("Expected error to mention index [0], got: %v", err)
		}
	})
}
