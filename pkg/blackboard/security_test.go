package blackboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalLockdown(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	client, err := NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// 1. Verify system starts unlocked
	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked)

	// 2. Trigger lockdown
	alert := NewHashMismatchAlert("art-123", "hash-exp", "hash-act", "agent-1", "claim-1")
	err = client.TriggerGlobalLockdown(ctx, alert)
	require.NoError(t, err)

	// 3. Verify system is locked
	locked, lockedAlert, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.Equal(t, alert.Type, lockedAlert.Type)
	assert.Equal(t, alert.ArtefactIDClaimed, lockedAlert.ArtefactIDClaimed)

	// 4. Verify GetLockdownState
	stateAlert, err := client.GetLockdownState(ctx)
	require.NoError(t, err)
	assert.Equal(t, alert.Type, stateAlert.Type)

	// 5. Verify alert was logged
	alerts, err := client.GetSecurityAlerts(ctx, 0)
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, alert.Type, alerts[0].Type)

	// 6. Clear lockdown
	err = client.ClearLockdown(ctx, "False positive", "admin")
	require.NoError(t, err)

	// 7. Verify system is unlocked
	locked, _, err = client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked)

	// 8. Verify override was logged
	alerts, err = client.GetSecurityAlerts(ctx, 0)
	require.NoError(t, err)
	require.Len(t, alerts, 2) // Original alert + override alert
	assert.Equal(t, AlertTypeSecurityOverride, alerts[0].Type) // Newest first
}

func TestSecurityAlerts(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	client, err := NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Subscribe to alerts
	pubsub := client.SubscribeSecurityAlerts(ctx)
	defer pubsub.Close()

	// Wait for subscription confirmation
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)

	// Publish non-locking alert
	alert := NewTimestampDriftAlert("art-123", 1000, 2000, 1000, 500, "agent-1")
	err = client.PublishSecurityAlert(ctx, alert)
	require.NoError(t, err)

	// Verify alert received
	msg, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	
	var receivedAlert SecurityAlert
	err = json.Unmarshal([]byte(msg.Payload), &receivedAlert)
	require.NoError(t, err)
	assert.Equal(t, alert.Type, receivedAlert.Type)
	assert.Equal(t, alert.DriftMs, receivedAlert.DriftMs)

	// Verify alert logged but NO lockdown
	alerts, err := client.GetSecurityAlerts(ctx, 0)
	require.NoError(t, err)
	require.Len(t, alerts, 1)

	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked)
}

func TestVerifiableArtefacts(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	client, err := NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Create V2 artefact
	v2Artefact := &VerifiableArtefact{
		ID: "a3f5b8c91d2e4f7a9b1c3d5e6f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a", // Valid 64-char hex
		Header: ArtefactHeader{
			Type:            "GoalDefined",
			StructuralType:  StructuralTypeStandard,
			Version:         1,
			LogicalThreadID: "thread-1",
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			ParentHashes:    []string{"b4c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6"}, // Valid 64-char hex
		},
		Payload: ArtefactPayload{
			Content: "payload content",
		},
	}

	// Write V2 artefact
	err = client.WriteVerifiableArtefact(ctx, v2Artefact)
	require.NoError(t, err)

	// Read back as V2
	readV2, err := client.GetVerifiableArtefact(ctx, v2Artefact.ID)
	require.NoError(t, err)
	assert.Equal(t, v2Artefact.ID, readV2.ID)
	assert.Equal(t, v2Artefact.Payload.Content, readV2.Payload.Content)
	assert.Equal(t, v2Artefact.Header.ParentHashes, readV2.Header.ParentHashes)

	// Read back as V1 (compatibility check)
	readV1, err := client.GetArtefact(ctx, v2Artefact.ID)
	require.NoError(t, err)
	assert.Equal(t, v2Artefact.ID, readV1.ID)
	assert.Equal(t, v2Artefact.Payload.Content, readV1.Payload)
	assert.Equal(t, v2Artefact.Header.ParentHashes, readV1.SourceArtefacts)
}

func TestSecurityAlertTypes(t *testing.T) {
	// Test constructors
	mismatch := NewHashMismatchAlert("art-1", "exp", "act", "agent", "claim")
	assert.Equal(t, AlertTypeHashMismatch, mismatch.Type)
	assert.Equal(t, "global_lockdown", mismatch.OrchestratorAction)

	orphan := NewOrphanBlockAlert("art-1", "missing", "agent", "claim")
	assert.Equal(t, AlertTypeOrphanBlock, orphan.Type)
	assert.Equal(t, "global_lockdown", orphan.OrchestratorAction)

	drift := NewTimestampDriftAlert("art-1", 100, 200, 100, 50, "agent")
	assert.Equal(t, AlertTypeTimestampDrift, drift.Type)
	assert.Equal(t, "rejected", drift.OrchestratorAction)

	override := NewSecurityOverrideAlert("reason", "operator")
	assert.Equal(t, AlertTypeSecurityOverride, override.Type)
	assert.Equal(t, "lockdown_cleared", override.Action)
}

func TestUnlockGlobalLockdown(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	defer s.Close()

	client, err := NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Trigger lockdown
	alert := NewHashMismatchAlert("art-123", "hash-exp", "hash-act", "agent-1", "claim-1")
	err = client.TriggerGlobalLockdown(ctx, alert)
	require.NoError(t, err)

	// Unlock using UnlockGlobalLockdown (alternative to ClearLockdown)
	override := *NewSecurityOverrideAlert("Manual unlock", "admin")
	err = client.UnlockGlobalLockdown(ctx, override)
	require.NoError(t, err)

	// Verify unlocked
	locked, _, err := client.IsInLockdown(ctx)
	require.NoError(t, err)
	assert.False(t, locked)

	// Verify logs
	alerts, err := client.GetSecurityAlerts(ctx, 0)
	require.NoError(t, err)
	require.Len(t, alerts, 2)
	assert.Equal(t, AlertTypeSecurityOverride, alerts[0].Type)
}
