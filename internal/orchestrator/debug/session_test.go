package debug

import (
	"context"
	"testing"
	"time"

	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()

	// Use test Redis instance
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use separate test DB
	})

	instanceName := "test-debug-" + time.Now().Format("20060102-150405.000")

	// Clear any existing test data
	ctx := context.Background()

	// Skip test if Redis is not available
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available at localhost:6379: %v", err)
	}
	pattern := "holt:" + instanceName + ":debug:*"
	keys, _ := rdb.Keys(ctx, pattern).Result()
	if len(keys) > 0 {
		rdb.Del(ctx, keys...)
	}

	t.Cleanup(func() {
		rdb.Close()
	})

	return rdb, instanceName
}

func TestSessionManager_CreateSession(t *testing.T) {
	rdb, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// Test successful session creation
	sessionID := "test-session-123"
	err = sm.CreateSession(ctx, sessionID)
	assert.NoError(t, err)

	// Verify session exists in Redis
	sessionKey := SessionKey(instanceName)
	exists, err := rdb.Exists(ctx, sessionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	// Verify session data
	sessionData, err := rdb.HGetAll(ctx, sessionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, sessionID, sessionData["session_id"])
	assert.NotEmpty(t, sessionData["connected_at_ms"])
	assert.NotEmpty(t, sessionData["last_heartbeat_ms"])
	// Redis stores false as "0"
	assert.Equal(t, "0", sessionData["is_paused"])

	// Verify TTL is set
	ttl, err := rdb.TTL(ctx, sessionKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl.Seconds(), float64(25)) // Should be close to 30 seconds
	assert.LessOrEqual(t, ttl.Seconds(), float64(30))
}

func TestSessionManager_CreateSession_AlreadyExists(t *testing.T) {
	rdb, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// Create first session
	err = sm.CreateSession(ctx, "session-1")
	require.NoError(t, err)

	// Try to create second session (should fail)
	err = sm.CreateSession(ctx, "session-2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already active")

	// Verify first session is still intact
	sessionData, err := rdb.HGetAll(ctx, SessionKey(instanceName)).Result()
	require.NoError(t, err)
	assert.Equal(t, "session-1", sessionData["session_id"])
}

func TestSessionManager_GetActiveSession(t *testing.T) {
	_, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// No session exists
	session, err := sm.GetActiveSession(ctx)
	assert.NoError(t, err)
	assert.Nil(t, session)

	// Create session
	sessionID := "test-session-456"
	err = sm.CreateSession(ctx, sessionID)
	require.NoError(t, err)

	// Get active session
	session, err = sm.GetActiveSession(ctx)
	assert.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, sessionID, session.ID)
	assert.False(t, session.IsPaused)
	assert.Greater(t, session.ConnectedAtMs, int64(0))
	assert.Greater(t, session.LastHeartbeatMs, int64(0))
}

func TestSessionManager_RefreshHeartbeat(t *testing.T) {
	_, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// Create session
	err = sm.CreateSession(ctx, "test-session")
	require.NoError(t, err)

	// Get initial heartbeat
	session1, err := sm.GetActiveSession(ctx)
	require.NoError(t, err)
	initialHeartbeat := session1.LastHeartbeatMs

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Refresh heartbeat
	err = sm.RefreshHeartbeat(ctx)
	assert.NoError(t, err)

	// Verify heartbeat was updated
	session2, err := sm.GetActiveSession(ctx)
	require.NoError(t, err)
	assert.Greater(t, session2.LastHeartbeatMs, initialHeartbeat)
}

func TestSessionManager_DeleteSession(t *testing.T) {
	rdb, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// Create session with breakpoints
	err = sm.CreateSession(ctx, "test-session")
	require.NoError(t, err)

	// Add some breakpoints
	bp := &Breakpoint{ID: "bp-1", ConditionType: string(ConditionArtefactType), Pattern: "CodeCommit"}
	err = sm.AddBreakpoint(ctx, bp)
	require.NoError(t, err)

	// Verify breakpoints exist
	bps, err := sm.GetBreakpoints(ctx)
	require.NoError(t, err)
	assert.Len(t, bps, 1)

	// Delete session
	err = sm.DeleteSession(ctx)
	assert.NoError(t, err)

	// Verify session is gone
	exists, err := rdb.Exists(ctx, SessionKey(instanceName)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Verify breakpoints are gone
	bps, err = sm.GetBreakpoints(ctx)
	require.NoError(t, err)
	assert.Len(t, bps, 0)

	// Verify pause context is gone
	exists, err = rdb.Exists(ctx, PauseContextKey(instanceName)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)
}

func TestSessionManager_SessionExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping session expiration test in short mode")
	}

	rdb, instanceName := setupTestRedis(t)
	ctx := context.Background()

	bbClient, err := blackboard.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15}, instanceName)
	require.NoError(t, err)
	defer bbClient.Close()

	sm := NewSessionManager(bbClient, instanceName)

	// Create session with very short TTL for testing
	err = sm.CreateSession(ctx, "test-session")
	require.NoError(t, err)

	// Manually set TTL to 1 second for faster testing
	sessionKey := SessionKey(instanceName)
	err = rdb.Expire(ctx, sessionKey, 1*time.Second).Err()
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(2 * time.Second)

	// Verify session expired
	exists, err := rdb.Exists(ctx, sessionKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// GetActiveSession should return nil
	session, err := sm.GetActiveSession(ctx)
	assert.NoError(t, err)
	assert.Nil(t, session)
}
