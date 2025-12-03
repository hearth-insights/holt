package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// Test expandTildeInVolume function (M4.5)
func TestExpandTildeInVolume(t *testing.T) {
	// Get actual home directory for testing
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "tilde with path and ro mode",
			input: "~/.config/gcloud:/root/.config/gcloud:ro",
			want:  filepath.Join(homeDir, ".config/gcloud") + ":/root/.config/gcloud:ro",
		},
		{
			name:  "tilde with path and rw mode",
			input: "~/.ssh:/root/.ssh:rw",
			want:  filepath.Join(homeDir, ".ssh") + ":/root/.ssh:rw",
		},
		{
			name:  "tilde alone",
			input: "~:/root:ro",
			want:  homeDir + ":/root:ro",
		},
		{
			name:  "no tilde - absolute path",
			input: "/var/lib/data:/data:ro",
			want:  "/var/lib/data:/data:ro",
		},
		{
			name:  "no mode specified",
			input: "~/.kube:/root/.kube",
			want:  filepath.Join(homeDir, ".kube") + ":/root/.kube",
		},
		{
			name:  "tilde in middle (not expanded)",
			input: "/home/~/data:/data:ro",
			want:  "/home/~/data:/data:ro",
		},
		{
			name:  "multiple colons (Windows path style) - expands first",
			input: "~/C:\\data:/data:ro",
			want:  filepath.Join(homeDir, "C:\\data") + ":/data:ro",
		},
		{
			name:  "minimal format (source:dest)",
			input: "~/foo:/bar",
			want:  filepath.Join(homeDir, "foo") + ":/bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandTildeInVolume(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("expandTildeInVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("expandTildeInVolume() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test edge cases
func TestExpandTildeInVolumeEdgeCases(t *testing.T) {
	t.Run("invalid format (no colon)", func(t *testing.T) {
		// Should pass through unchanged (Docker will error)
		input := "~/foo"
		got, err := expandTildeInVolume(input)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if got != input {
			t.Errorf("Expected passthrough for invalid format, got: %s", got)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		got, err := expandTildeInVolume("")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("Expected empty string, got: %s", got)
		}
	})

	t.Run("just colon", func(t *testing.T) {
		// Should pass through (Docker will error)
		input := ":"
		got, err := expandTildeInVolume(input)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if got != input {
			t.Errorf("Expected passthrough, got: %s", got)
		}
	})
}
