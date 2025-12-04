package orchestrator

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/internal/config"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// setupTestEngine creates an engine with default test configuration for unit tests.
// Returns the engine, the blackboard client, and the configuration object.
func setupTestEngine(t *testing.T) (*Engine, *blackboard.Client, *config.HoltConfig) {
	// Use miniredis for embedded Redis
	mr := miniredis.NewMiniRedis()
	err := mr.Start()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	instanceName := "test-" + uuid.New().String()[:8]
	bbClient, err := blackboard.NewClient(&redis.Options{Addr: mr.Addr()}, instanceName)
	require.NoError(t, err)
	t.Cleanup(func() { bbClient.Close() })

	maxIterations := 3
	cfg := &config.HoltConfig{
		Version: "1.0",
		Orchestrator: &config.OrchestratorConfig{
			MaxReviewIterations: &maxIterations,
		},
		Agents: map[string]config.Agent{
			"Coder": {
				Image:           "test:latest",
				Command:         []string{"test"},
				BiddingStrategy: config.BiddingStrategyConfig{Type: "exclusive"},
			},
			"Reviewer": {
				Image:           "test:latest",
				Command:         []string{"test"},
				BiddingStrategy: config.BiddingStrategyConfig{Type: "review"},
			},
		},
	}

	engine := NewEngine(bbClient, instanceName, cfg, nil)

	return engine, bbClient, cfg
}

func intPtr(i int) *int {
	return &i
}
