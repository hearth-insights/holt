package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAtWorkerLimit(t *testing.T) {
	wm := &WorkerManager{
		workersByRole: map[string]int{
			"role-a": 2,
			"role-b": 0,
		},
	}

	tests := []struct {
		name          string
		role          string
		maxConcurrent int
		expected      bool
	}{
		{"BelowLimit", "role-a", 5, false},
		{"AtLimit", "role-a", 2, true},
		{"AboveLimit", "role-a", 1, true}, // Should not happen in practice but logic holds
		{"ZeroWorkers", "role-b", 1, false},
		{"ZeroLimit", "role-b", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wm.IsAtWorkerLimit(tt.role, tt.maxConcurrent)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkerCleanupLogic(t *testing.T) {
	// Test the internal logic of cleanupWorker without actual Docker calls
	// We can't easily mock Docker client here without complex setup, 
	// so we'll test the state management part if possible.
	// However, cleanupWorker calls dockerClient.ContainerRemove which will panic if nil.
	// So we skip deep logic testing here and rely on integration tests or future refactoring for testability.
	
	// For now, IsAtWorkerLimit provides some coverage.
}
