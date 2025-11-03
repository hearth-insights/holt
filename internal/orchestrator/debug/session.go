package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
)

// SessionManager manages debug session lifecycle
type SessionManager struct {
	client       *blackboard.Client
	instanceName string
}

// NewSessionManager creates a new session manager
func NewSessionManager(client *blackboard.Client, instanceName string) *SessionManager {
	return &SessionManager{
		client:       client,
		instanceName: instanceName,
	}
}

// CreateSession creates a new debug session using Redis SET NX for atomic creation
func (sm *SessionManager) CreateSession(ctx context.Context, sessionID string) error {
	sessionKey := SessionKey(sm.instanceName)

	// Check if session already exists (defensive check before SET NX)
	exists, err := sm.client.RedisClient().Exists(ctx, sessionKey).Result()
	if err != nil {
		return fmt.Errorf("failed to check session existence: %w", err)
	}
	if exists > 0 {
		// Get existing session for error message
		sessionData, _ := sm.client.RedisClient().HGetAll(ctx, sessionKey).Result()
		existingID := sessionData["session_id"]
		connectedAt := sessionData["connected_at_ms"]
		return fmt.Errorf("debug session already active (session_id: %s, started: %s)", existingID, connectedAt)
	}

	// Create session with atomic SET NX
	now := time.Now().UnixMilli()
	session := &Session{
		ID:              sessionID,
		ConnectedAtMs:   now,
		LastHeartbeatMs: now,
		IsPaused:        false,
	}

	// Write session data
	sessionHash := map[string]interface{}{
		"session_id":        session.ID,
		"connected_at_ms":   session.ConnectedAtMs,
		"last_heartbeat_ms": session.LastHeartbeatMs,
		"is_paused":         session.IsPaused,
	}

	// Use HSETNX on a field to ensure atomic creation
	// If the key exists, this will fail
	created, err := sm.client.RedisClient().HSetNX(ctx, sessionKey, "session_id", sessionID).Result()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	if !created {
		return fmt.Errorf("debug session already active (concurrent creation detected)")
	}

	// Set remaining fields
	if err := sm.client.RedisClient().HSet(ctx, sessionKey, sessionHash).Err(); err != nil {
		// Cleanup on failure
		sm.client.RedisClient().Del(ctx, sessionKey)
		return fmt.Errorf("failed to write session data: %w", err)
	}

	// Set TTL
	if err := sm.client.RedisClient().Expire(ctx, sessionKey, SessionTTL).Err(); err != nil {
		// Cleanup on failure
		sm.client.RedisClient().Del(ctx, sessionKey)
		return fmt.Errorf("failed to set session TTL: %w", err)
	}

	return nil
}

// GetActiveSession retrieves the current active session, or nil if none exists
func (sm *SessionManager) GetActiveSession(ctx context.Context) (*Session, error) {
	sessionKey := SessionKey(sm.instanceName)

	sessionData, err := sm.client.RedisClient().HGetAll(ctx, sessionKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Empty map means key doesn't exist
	if len(sessionData) == 0 {
		return nil, nil
	}

	// Parse session data
	session := &Session{}
	session.ID = sessionData["session_id"]

	if connectedAt, err := strconv.ParseInt(sessionData["connected_at_ms"], 10, 64); err == nil {
		session.ConnectedAtMs = connectedAt
	}

	if lastHeartbeat, err := strconv.ParseInt(sessionData["last_heartbeat_ms"], 10, 64); err == nil {
		session.LastHeartbeatMs = lastHeartbeat
	}

	session.IsPaused = sessionData["is_paused"] == "true"
	session.PausedArtefactID = sessionData["paused_artefact_id"]
	session.PausedClaimID = sessionData["paused_claim_id"]
	session.BreakpointID = sessionData["breakpoint_id"]
	session.PausedEventType = sessionData["paused_event_type"]

	if pausedAt, err := strconv.ParseInt(sessionData["paused_at_ms"], 10, 64); err == nil {
		session.PausedAtMs = pausedAt
	}

	return session, nil
}

// RefreshHeartbeat updates the last_heartbeat_ms and refreshes TTL
func (sm *SessionManager) RefreshHeartbeat(ctx context.Context) error {
	sessionKey := SessionKey(sm.instanceName)

	// Update heartbeat timestamp
	now := time.Now().UnixMilli()
	if err := sm.client.RedisClient().HSet(ctx, sessionKey, "last_heartbeat_ms", now).Err(); err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	// Refresh TTL
	if err := sm.client.RedisClient().Expire(ctx, sessionKey, SessionTTL).Err(); err != nil {
		return fmt.Errorf("failed to refresh session TTL: %w", err)
	}

	return nil
}

// DeleteSession removes the session and all associated data (breakpoints, pause context)
func (sm *SessionManager) DeleteSession(ctx context.Context) error {
	sessionKey := SessionKey(sm.instanceName)
	breakpointsKey := BreakpointsKey(sm.instanceName)
	pauseContextKey := PauseContextKey(sm.instanceName)

	// Delete all debug-related keys
	keys := []string{sessionKey, breakpointsKey, pauseContextKey}
	if err := sm.client.RedisClient().Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

// SetPauseContext updates the session with pause information
func (sm *SessionManager) SetPauseContext(ctx context.Context, pauseCtx *PauseContext) error {
	sessionKey := SessionKey(sm.instanceName)
	pauseContextKey := PauseContextKey(sm.instanceName)

	// Update session pause flag
	updates := map[string]interface{}{
		"is_paused":          true,
		"paused_artefact_id": pauseCtx.ArtefactID,
		"paused_claim_id":    pauseCtx.ClaimID,
		"breakpoint_id":      pauseCtx.BreakpointID,
		"paused_event_type":  pauseCtx.EventType,
		"paused_at_ms":       pauseCtx.PausedAtMs,
	}

	if err := sm.client.RedisClient().HSet(ctx, sessionKey, updates).Err(); err != nil {
		return fmt.Errorf("failed to update pause state: %w", err)
	}

	// Store full pause context
	pauseJSON, err := json.Marshal(pauseCtx)
	if err != nil {
		return fmt.Errorf("failed to marshal pause context: %w", err)
	}

	if err := sm.client.RedisClient().Set(ctx, pauseContextKey, pauseJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to store pause context: %w", err)
	}

	return nil
}

// ClearPauseContext removes pause state from session
func (sm *SessionManager) ClearPauseContext(ctx context.Context) error {
	sessionKey := SessionKey(sm.instanceName)
	pauseContextKey := PauseContextKey(sm.instanceName)

	// Clear pause fields in session
	updates := map[string]interface{}{
		"is_paused":          false,
		"paused_artefact_id": "",
		"paused_claim_id":    "",
		"breakpoint_id":      "",
		"paused_event_type":  "",
		"paused_at_ms":       0,
	}

	if err := sm.client.RedisClient().HSet(ctx, sessionKey, updates).Err(); err != nil {
		return fmt.Errorf("failed to clear pause state: %w", err)
	}

	// Delete pause context
	if err := sm.client.RedisClient().Del(ctx, pauseContextKey).Err(); err != nil {
		return fmt.Errorf("failed to delete pause context: %w", err)
	}

	return nil
}

// GetPauseContext retrieves current pause context, or nil if not paused
func (sm *SessionManager) GetPauseContext(ctx context.Context) (*PauseContext, error) {
	pauseContextKey := PauseContextKey(sm.instanceName)

	pauseJSON, err := sm.client.RedisClient().Get(ctx, pauseContextKey).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get pause context: %w", err)
	}

	var pauseCtx PauseContext
	if err := json.Unmarshal([]byte(pauseJSON), &pauseCtx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pause context: %w", err)
	}

	return &pauseCtx, nil
}

// AddBreakpoint adds a new breakpoint to the session
func (sm *SessionManager) AddBreakpoint(ctx context.Context, bp *Breakpoint) error {
	breakpointsKey := BreakpointsKey(sm.instanceName)

	// Check max breakpoints
	count, err := sm.client.RedisClient().LLen(ctx, breakpointsKey).Result()
	if err != nil {
		return fmt.Errorf("failed to check breakpoint count: %w", err)
	}
	if count >= MaxBreakpoints {
		return fmt.Errorf("maximum breakpoints (%d) reached", MaxBreakpoints)
	}

	// Serialize breakpoint
	bpJSON, err := json.Marshal(bp)
	if err != nil {
		return fmt.Errorf("failed to marshal breakpoint: %w", err)
	}

	// Add to list
	if err := sm.client.RedisClient().RPush(ctx, breakpointsKey, bpJSON).Err(); err != nil {
		return fmt.Errorf("failed to add breakpoint: %w", err)
	}

	return nil
}

// GetBreakpoints retrieves all active breakpoints
func (sm *SessionManager) GetBreakpoints(ctx context.Context) ([]*Breakpoint, error) {
	breakpointsKey := BreakpointsKey(sm.instanceName)

	// Get all breakpoint JSON strings
	bpStrings, err := sm.client.RedisClient().LRange(ctx, breakpointsKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get breakpoints: %w", err)
	}

	// Parse breakpoints
	breakpoints := make([]*Breakpoint, 0, len(bpStrings))
	for _, bpStr := range bpStrings {
		var bp Breakpoint
		if err := json.Unmarshal([]byte(bpStr), &bp); err != nil {
			// Log error but continue with other breakpoints
			continue
		}
		breakpoints = append(breakpoints, &bp)
	}

	return breakpoints, nil
}

// RemoveBreakpoint removes a breakpoint by ID
func (sm *SessionManager) RemoveBreakpoint(ctx context.Context, breakpointID string) error {
	breakpointsKey := BreakpointsKey(sm.instanceName)

	// Get all breakpoints
	breakpoints, err := sm.GetBreakpoints(ctx)
	if err != nil {
		return err
	}

	// Find and remove the matching breakpoint
	found := false
	for _, bp := range breakpoints {
		if bp.ID == breakpointID {
			found = true
			bpJSON, _ := json.Marshal(bp)
			// Remove first occurrence (LREM count=1)
			if err := sm.client.RedisClient().LRem(ctx, breakpointsKey, 1, bpJSON).Err(); err != nil {
				return fmt.Errorf("failed to remove breakpoint: %w", err)
			}
			break
		}
	}

	if !found {
		return fmt.Errorf("breakpoint %s not found", breakpointID)
	}

	return nil
}

// ClearAllBreakpoints removes all breakpoints
func (sm *SessionManager) ClearAllBreakpoints(ctx context.Context) error {
	breakpointsKey := BreakpointsKey(sm.instanceName)
	return sm.client.RedisClient().Del(ctx, breakpointsKey).Err()
}

// SessionKey returns the Redis key for the debug session
func SessionKey(instanceName string) string {
	return fmt.Sprintf("holt:%s:debug:session", instanceName)
}

// BreakpointsKey returns the Redis key for breakpoints list
func BreakpointsKey(instanceName string) string {
	return fmt.Sprintf("holt:%s:debug:breakpoints", instanceName)
}

// PauseContextKey returns the Redis key for pause context
func PauseContextKey(instanceName string) string {
	return fmt.Sprintf("holt:%s:debug:pause_context", instanceName)
}

// CommandChannel returns the Pub/Sub channel for debug commands (CLI → Orchestrator)
func CommandChannel(instanceName string) string {
	return fmt.Sprintf("holt:%s:debug:command", instanceName)
}

// EventChannel returns the Pub/Sub channel for debug events (Orchestrator → CLI)
func EventChannel(instanceName string) string {
	return fmt.Sprintf("holt:%s:debug:event", instanceName)
}
