package config

import (
	"os"
	"strings"
	"testing"
)

// TestAgentVolumesValidation tests M4.5 volume mount configuration validation
func TestAgentVolumesValidation(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid volume mount with ro mode",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         []string{"~/.config/gcloud:/root/.config/gcloud:ro"},
			},
			wantErr: false,
		},
		{
			name: "valid volume mount with rw mode (security warning logged)",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         []string{"~/.ssh:/root/.ssh:rw"},
			},
			wantErr: false, // Warning only, not an error
		},
		{
			name: "multiple volume mounts",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes: []string{
					"~/.config/gcloud:/root/.config/gcloud:ro",
					"/var/data:/data:ro",
					"~/.kube:/root/.kube",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid format - missing colon",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         []string{"~/foo"},
			},
			wantErr: true,
			errMsg:  "invalid volume mount format",
		},
		{
			name: "invalid format - empty string",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         []string{""},
			},
			wantErr: true,
			errMsg:  "invalid volume mount format",
		},
		{
			name: "no volumes specified (valid)",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         []string{},
			},
			wantErr: false,
		},
		{
			name: "nil volumes (valid)",
			agent: Agent{
				Image:           "test:latest",
				Command:         []string{"/app/run.sh"},
				BiddingStrategy: BiddingStrategyConfig{Type: "exclusive"},
				Volumes:         nil,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.Validate("TestAgent")
			if (err != nil) != tt.wantErr {
				t.Errorf("Agent.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Agent.Validate() error message = %v, want substring %v", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

// TestHoltConfigVolumesYAML tests that volumes field can be loaded from YAML
func TestHoltConfigVolumesYAML(t *testing.T) {
	yamlContent := `version: "1.0"
agents:
  TestAgent:
    image: test:latest
    command: ["/app/run.sh"]
    bidding_strategy: { type: "exclusive" }
    volumes:
      - "~/.config/gcloud:/root/.config/gcloud:ro"
      - "/var/data:/data:ro"
services:
  redis:
    image: redis:7-alpine
`

	// Create temp file
	tmpFile, err := os.CreateTemp("", "test-holt-volumes-*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write YAML content
	if _, err := tmpFile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// Load config
	cfg, err := Load(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to parse YAML: %v", err)
	}

	agent, exists := cfg.Agents["TestAgent"]
	if !exists {
		t.Fatal("TestAgent not found in parsed config")
	}

	if len(agent.Volumes) != 2 {
		t.Errorf("Expected 2 volumes, got %d", len(agent.Volumes))
	}

	expectedVolumes := []string{
		"~/.config/gcloud:/root/.config/gcloud:ro",
		"/var/data:/data:ro",
	}

	for i, expected := range expectedVolumes {
		if i >= len(agent.Volumes) {
			t.Errorf("Missing volume at index %d", i)
			continue
		}
		if agent.Volumes[i] != expected {
			t.Errorf("Volume %d: got %s, want %s", i, agent.Volumes[i], expected)
		}
	}
}
