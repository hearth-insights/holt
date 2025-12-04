// +build integration

package commands

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hearth-insights/holt/internal/orchestrator/debug"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDebugCLI_SessionManagement tests CLI session creation and cleanup
func TestDebugCLI_SessionManagement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup: Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	// Verify Redis connectivity
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	instanceName := fmt.Sprintf("test-debug-cli-%d", time.Now().Unix())

	// Clean up any leftover state
	defer func() {
		sessionKey := fmt.Sprintf("holt:%s:debug:session", instanceName)
		redisClient.Del(ctx, sessionKey)
	}()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379"}, instanceName)
	require.NoError(t, err)

	// Test 1: Create debug session
	t.Run("CreateSession", func(t *testing.T) {
		debugger := NewDebugger(ctx, bbClient, instanceName)

		err := debugger.Initialize()
		require.NoError(t, err, "Should create session successfully")

		// Verify session exists in Redis
		sessionKey := fmt.Sprintf("holt:%s:debug:session", instanceName)
		exists, err := redisClient.Exists(ctx, sessionKey).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), exists, "Session key should exist")

		// Verify session fields
		sessionData, err := redisClient.HGetAll(ctx, sessionKey).Result()
		require.NoError(t, err)
		assert.Equal(t, debugger.sessionID, sessionData["session_id"])
		assert.Equal(t, "false", sessionData["is_paused"])

		// Clean up
		debugger.Cleanup()

		// Verify session deleted
		exists, err = redisClient.Exists(ctx, sessionKey).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists, "Session key should be deleted after cleanup")
	})

	// Test 2: Concurrent session prevention
	t.Run("PreventConcurrentSessions", func(t *testing.T) {
		debugger1 := NewDebugger(ctx, bbClient, instanceName)
		err := debugger1.Initialize()
		require.NoError(t, err, "First session should succeed")

		// Try to create second session
		debugger2 := NewDebugger(ctx, bbClient, instanceName)
		err = debugger2.Initialize()
		assert.Error(t, err, "Second session should fail")
		assert.Contains(t, err.Error(), "already active", "Error should indicate session is active")

		// Clean up first session
		debugger1.Cleanup()

		// Now second session should succeed
		debugger3 := NewDebugger(ctx, bbClient, instanceName)
		err = debugger3.Initialize()
		assert.NoError(t, err, "Session should succeed after first is cleaned up")

		debugger3.Cleanup()
	})

	// Test 3: Heartbeat mechanism
	t.Run("Heartbeat", func(t *testing.T) {
		debugger := NewDebugger(ctx, bbClient, instanceName)
		err := debugger.Initialize()
		require.NoError(t, err)

		sessionKey := fmt.Sprintf("holt:%s:debug:session", instanceName)

		// Get initial heartbeat
		initialHeartbeat, err := redisClient.HGet(ctx, sessionKey, "last_heartbeat_ms").Result()
		require.NoError(t, err)

		// Start heartbeat
		debugger.StartHeartbeat()

		// Wait for heartbeat to refresh
		time.Sleep(6 * time.Second)

		// Get updated heartbeat
		updatedHeartbeat, err := redisClient.HGet(ctx, sessionKey, "last_heartbeat_ms").Result()
		require.NoError(t, err)

		assert.NotEqual(t, initialHeartbeat, updatedHeartbeat, "Heartbeat should be updated")

		// Clean up
		debugger.Cleanup()
	})

	// Test 4: Breakpoint management
	t.Run("BreakpointManagement", func(t *testing.T) {
		debugger := NewDebugger(ctx, bbClient, instanceName)
		err := debugger.Initialize()
		require.NoError(t, err)
		defer debugger.Cleanup()

		// Set breakpoint
		err = debugger.SetBreakpoint("artefact.type=TestArtefact")
		assert.NoError(t, err, "Should set breakpoint successfully")

		// Verify breakpoint stored locally
		debugger.mu.RLock()
		assert.Len(t, debugger.breakpoints, 1, "Should have one breakpoint")
		debugger.mu.RUnlock()

		// Set another breakpoint
		err = debugger.SetBreakpoint("claim.status=pending_review")
		assert.NoError(t, err)

		debugger.mu.RLock()
		assert.Len(t, debugger.breakpoints, 2, "Should have two breakpoints")
		debugger.mu.RUnlock()

		// Invalid breakpoint
		err = debugger.SetBreakpoint("invalid")
		assert.Error(t, err, "Should reject invalid breakpoint format")

		err = debugger.SetBreakpoint("unknown.type=test")
		assert.Error(t, err, "Should reject unknown condition type")
	})

	// Test 5: Event listener
	t.Run("EventListener", func(t *testing.T) {
		debugger := NewDebugger(ctx, bbClient, instanceName)
		err := debugger.Initialize()
		require.NoError(t, err)
		defer debugger.Cleanup()

		// Start event listener
		debugger.StartEventListener()

		// Publish a test event
		event := &debug.Event{
			EventType: "paused_on_breakpoint",
			SessionID: debugger.sessionID,
			Payload: map[string]interface{}{
				"breakpoint_id": "bp-1",
				"event_type":    "artefact_received",
				"pause_context": map[string]interface{}{
					"artefact_id":   "test-artefact-123",
					"claim_id":      "test-claim-456",
					"breakpoint_id": "bp-1",
					"event_type":    "artefact_received",
					"paused_at_ms":  float64(time.Now().UnixMilli()),
				},
			},
		}

		err = debug.PublishEvent(ctx, redisClient, instanceName, event)
		require.NoError(t, err)

		// Wait for event to be processed (give more time for reliability)
		time.Sleep(2 * time.Second)

		// Verify pause state updated
		debugger.mu.RLock()
		isPaused := debugger.isPaused
		pauseCtx := debugger.pauseContext
		debugger.mu.RUnlock()

		assert.True(t, isPaused, "Should be paused after receiving pause event")
		if assert.NotNil(t, pauseCtx, "Should have pause context") {
			assert.Equal(t, "test-artefact-123", pauseCtx.ArtefactID)
		}

		// Publish resumed event
		resumedEvent := &debug.Event{
			EventType: "resumed",
			SessionID: debugger.sessionID,
			Payload:   map[string]interface{}{},
		}

		err = debug.PublishEvent(ctx, redisClient, instanceName, resumedEvent)
		require.NoError(t, err)

		// Wait for event to be processed
		time.Sleep(1 * time.Second)

		// Verify resumed
		debugger.mu.RLock()
		isPausedAfter := debugger.isPaused
		pauseCtxAfter := debugger.pauseContext
		debugger.mu.RUnlock()

		assert.False(t, isPausedAfter, "Should not be paused after resume event")
		assert.Nil(t, pauseCtxAfter, "Pause context should be cleared")
	})
}

// TestDebugCLI_CommandPublishing tests that CLI can publish commands
func TestDebugCLI_CommandPublishing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup: Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	instanceName := fmt.Sprintf("test-debug-cmd-%d", time.Now().Unix())

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379"}, instanceName)
	require.NoError(t, err)

	debugger := NewDebugger(ctx, bbClient, instanceName)
	err = debugger.Initialize()
	require.NoError(t, err)
	defer debugger.Cleanup()

	// Subscribe to command channel to verify commands are published
	commandChannel := fmt.Sprintf("holt:%s:debug:command", instanceName)
	pubsub := redisClient.Subscribe(ctx, commandChannel)
	defer pubsub.Close()

	// Wait for subscription confirmation
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)

	cmdChan := pubsub.Channel()

	// Test continue command
	t.Run("ContinueCommand", func(t *testing.T) {
		debugger.cmdContinue()

		// Wait for command
		select {
		case msg := <-cmdChan:
			assert.Contains(t, msg.Payload, "continue", "Command should be continue")
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for continue command")
		}
	})

	// Test next command
	t.Run("NextCommand", func(t *testing.T) {
		debugger.cmdNext()

		select {
		case msg := <-cmdChan:
			assert.Contains(t, msg.Payload, "step_next", "Command should be step_next")
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for step command")
		}
	})

	// Test break command
	t.Run("BreakCommand", func(t *testing.T) {
		debugger.cmdBreak("artefact.type=TestType")

		select {
		case msg := <-cmdChan:
			assert.Contains(t, msg.Payload, "set_breakpoints", "Command should be set_breakpoints")
			assert.Contains(t, msg.Payload, "TestType", "Payload should contain pattern")
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for break command")
		}
	})
}
