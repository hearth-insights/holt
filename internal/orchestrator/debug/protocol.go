package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
)

// ProtocolHandler handles debug protocol communication (commands and events)
type ProtocolHandler struct {
	client       *blackboard.Client
	instanceName string
	sessionMgr   *SessionManager
}

// NewProtocolHandler creates a new protocol handler
func NewProtocolHandler(client *blackboard.Client, instanceName string) *ProtocolHandler {
	return &ProtocolHandler{
		client:       client,
		instanceName: instanceName,
		sessionMgr:   NewSessionManager(client, instanceName),
	}
}

// SubscribeToCommands subscribes to debug commands from CLI
func (ph *ProtocolHandler) SubscribeToCommands(ctx context.Context) (*redis.PubSub, <-chan *Command, error) {
	channel := CommandChannel(ph.instanceName)

	pubsub := ph.client.RedisClient().Subscribe(ctx, channel)

	// Test subscription
	if _, err := pubsub.Receive(ctx); err != nil {
		pubsub.Close()
		return nil, nil, fmt.Errorf("failed to subscribe to debug commands: %w", err)
	}

	cmdChan := make(chan *Command, 10)

	// Start processing messages
	go func() {
		defer close(cmdChan)
		msgChan := pubsub.Channel()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgChan:
				if !ok {
					return
				}

				var cmd Command
				if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
					log.Printf("[Debug] Failed to unmarshal command: %v", err)
					continue
				}

				cmdChan <- &cmd
			}
		}
	}()

	return pubsub, cmdChan, nil
}

// PublishEvent publishes a debug event to CLI
func (ph *ProtocolHandler) PublishEvent(ctx context.Context, eventType DebugEventType, sessionID string, payload map[string]interface{}) error {
	event := Event{
		EventType: string(eventType),
		SessionID: sessionID,
		Payload:   payload,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	channel := EventChannel(ph.instanceName)
	if err := ph.client.RedisClient().Publish(ctx, channel, eventJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}

// PublishPausedEvent publishes a paused_on_breakpoint event
func (ph *ProtocolHandler) PublishPausedEvent(ctx context.Context, sessionID string, pauseCtx *PauseContext, bp *Breakpoint) error {
	payload := map[string]interface{}{
		"artefact_id":   pauseCtx.ArtefactID,
		"claim_id":      pauseCtx.ClaimID,
		"breakpoint_id": pauseCtx.BreakpointID,
		"event_type":    pauseCtx.EventType,
		"paused_at_ms":  pauseCtx.PausedAtMs,
	}

	if bp != nil {
		payload["breakpoint_condition"] = bp.ConditionType
		payload["breakpoint_pattern"] = bp.Pattern
	}

	return ph.PublishEvent(ctx, EventPausedOnBreakpoint, sessionID, payload)
}

// PublishBreakpointSetEvent publishes a breakpoint_set event
func (ph *ProtocolHandler) PublishBreakpointSetEvent(ctx context.Context, sessionID string, bp *Breakpoint) error {
	payload := map[string]interface{}{
		"breakpoint_id":   bp.ID,
		"condition_type":  bp.ConditionType,
		"pattern":         bp.Pattern,
	}

	return ph.PublishEvent(ctx, EventBreakpointSet, sessionID, payload)
}

// PublishSessionExpiredEvent publishes a session_expired event
func (ph *ProtocolHandler) PublishSessionExpiredEvent(ctx context.Context, sessionID string) error {
	return ph.PublishEvent(ctx, EventSessionExpired, sessionID, map[string]interface{}{})
}

// PublishReviewCompleteEvent publishes a review_complete event
func (ph *ProtocolHandler) PublishReviewCompleteEvent(ctx context.Context, sessionID string, claimID string, decision string) error {
	payload := map[string]interface{}{
		"claim_id": claimID,
		"decision": decision,
	}

	return ph.PublishEvent(ctx, EventReviewComplete, sessionID, payload)
}
