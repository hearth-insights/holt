package blackboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Client provides instance-scoped Redis operations for the blackboard.
// All keys and channels are automatically namespaced with the instance name.
// The client is thread-safe and can be used concurrently from multiple goroutines.
type Client struct {
	rdb          *redis.Client
	instanceName string
}

// NewClient creates a new blackboard client for the specified instance.
// The client automatically namespaces all keys and channels with the instance name.
//
// Parameters:
//   - redisOpts: Redis connection options (address, password, DB, etc.)
//   - instanceName: Holt instance identifier (must not be empty)
//
// Returns an error if instanceName is empty.
func NewClient(redisOpts *redis.Options, instanceName string) (*Client, error) {
	if instanceName == "" {
		return nil, fmt.Errorf("instance name cannot be empty")
	}

	return &Client{
		rdb:          redis.NewClient(redisOpts),
		instanceName: instanceName,
	}, nil
}

// Close closes the Redis connection. Implements io.Closer.
// After calling Close(), the client should not be used.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping verifies Redis connectivity. Useful for health checks.
// Returns an error if Redis is not reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// RedisClient returns the underlying Redis client for advanced operations.
// This is primarily for testing purposes. Use the Client methods when possible.
func (c *Client) RedisClient() *redis.Client {
	return c.rdb
}

// CreateArtefact writes an artefact to Redis and publishes an event.
// Validates the artefact before writing. Returns error if validation fails or Redis operation fails.
// Publishes full artefact JSON to holt:{instance}:artefact_events after successful write.
//
// The artefact is stored as a Redis hash at holt:{instance}:artefact:{id}.
// This method is idempotent - writing the same artefact twice is safe.
func (c *Client) CreateArtefact(ctx context.Context, a *Artefact) error {
	// M3.9: Auto-populate CreatedAtMs if not set
	if a.CreatedAtMs == 0 {
		a.CreatedAtMs = time.Now().UnixMilli()
	}

	// Validate artefact
	if err := a.Validate(); err != nil {
		return fmt.Errorf("invalid artefact: %w", err)
	}

	// Convert to Redis hash
	hash, err := ArtefactToHash(a)
	if err != nil {
		return fmt.Errorf("failed to serialize artefact: %w", err)
	}

	// Write to Redis
	key := ArtefactKey(c.instanceName, a.ID)
	if err := c.rdb.HSet(ctx, key, hash).Err(); err != nil {
		return fmt.Errorf("failed to write artefact to Redis: %w", err)
	}

	// Publish event
	artefactJSON, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("failed to marshal artefact for event: %w", err)
	}

	channel := ArtefactEventsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, artefactJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish artefact event: %w", err)
	}

	return nil
}

// GetArtefact retrieves an artefact by ID.
// Returns (nil, redis.Nil) if the artefact doesn't exist.
// Use IsNotFound() to check for not-found errors.
func (c *Client) GetArtefact(ctx context.Context, artefactID string) (*Artefact, error) {
	key := ArtefactKey(c.instanceName, artefactID)

	// Read hash from Redis
	hashData, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read artefact from Redis: %w", err)
	}

	// Check if key exists (HGetAll returns empty map for non-existent keys)
	if len(hashData) == 0 {
		return nil, redis.Nil
	}

	// Convert to Artefact
	artefact, err := HashToArtefact(hashData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize artefact: %w", err)
	}

	return artefact, nil
}

// ArtefactExists checks if an artefact exists without fetching it.
// More efficient than GetArtefact when you only need to check existence.
func (c *Client) ArtefactExists(ctx context.Context, artefactID string) (bool, error) {
	key := ArtefactKey(c.instanceName, artefactID)
	exists, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check artefact existence: %w", err)
	}
	return exists > 0, nil
}

// ScanArtefacts retrieves all artefact IDs that match the given prefix.
// Used for short ID resolution to find full UUIDs from user-provided prefixes.
// Uses Redis SCAN with pattern matching for efficiency.
// Returns array of full UUIDs (sorted) that start with the prefix.
//
// Example: prefix="abc123" might return ["abc12345-6789-...", "abc12399-1234-..."]
func (c *Client) ScanArtefacts(ctx context.Context, prefix string) ([]string, error) {
	// Build scan pattern
	pattern := fmt.Sprintf("holt:%s:artefact:%s*", c.instanceName, prefix)

	// Use SCAN to find matching keys
	var matchingIDs []string
	iter := c.rdb.Scan(ctx, 0, pattern, 0).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		// Extract UUID from key: holt:{instance}:artefact:{uuid}
		artefactPrefix := fmt.Sprintf("holt:%s:artefact:", c.instanceName)
		if len(key) > len(artefactPrefix) {
			uuid := key[len(artefactPrefix):]
			matchingIDs = append(matchingIDs, uuid)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan artefact keys: %w", err)
	}

	// Sort for consistent ordering in error messages
	sort.Strings(matchingIDs)

	return matchingIDs, nil
}

// CreateClaim writes a claim to Redis and publishes an event.
// Validates the claim before writing.
// Publishes full claim JSON to holt:{instance}:claim_events after successful write.
// Also creates an index mapping artefact_id to claim_id for idempotency checks.
func (c *Client) CreateClaim(ctx context.Context, claim *Claim) error {
	// Validate claim
	if err := claim.Validate(); err != nil {
		return fmt.Errorf("invalid claim: %w", err)
	}

	// Convert to Redis hash
	hash, err := ClaimToHash(claim)
	if err != nil {
		return fmt.Errorf("failed to serialize claim: %w", err)
	}

	// Write to Redis
	key := ClaimKey(c.instanceName, claim.ID)
	if err := c.rdb.HSet(ctx, key, hash).Err(); err != nil {
		return fmt.Errorf("failed to write claim to Redis: %w", err)
	}

	// Create artefact -> claim index for idempotency checks
	indexKey := ClaimByArtefactKey(c.instanceName, claim.ArtefactID)
	if err := c.rdb.Set(ctx, indexKey, claim.ID, 0).Err(); err != nil {
		return fmt.Errorf("failed to create claim index: %w", err)
	}

	// Publish event
	claimJSON, err := json.Marshal(claim)
	if err != nil {
		return fmt.Errorf("failed to marshal claim for event: %w", err)
	}

	channel := ClaimEventsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, claimJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish claim event: %w", err)
	}

	return nil
}

// GetClaim retrieves a claim by ID.
// Returns (nil, redis.Nil) if the claim doesn't exist.
func (c *Client) GetClaim(ctx context.Context, claimID string) (*Claim, error) {
	key := ClaimKey(c.instanceName, claimID)

	// Read hash from Redis
	hashData, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read claim from Redis: %w", err)
	}

	// Check if key exists
	if len(hashData) == 0 {
		return nil, redis.Nil
	}

	// Convert to Claim
	claim, err := HashToClaim(hashData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize claim: %w", err)
	}

	return claim, nil
}

// UpdateClaim replaces an existing claim with new data (full HMSET replacement).
// Used by orchestrator to update status and granted agents as claim progresses through phases.
// Validates the claim before writing.
//
// Note: This performs a full replacement of all fields. The claim will be created if it doesn't exist.
func (c *Client) UpdateClaim(ctx context.Context, claim *Claim) error {
	// Validate claim
	if err := claim.Validate(); err != nil {
		return fmt.Errorf("invalid claim: %w", err)
	}

	// Convert to Redis hash
	hash, err := ClaimToHash(claim)
	if err != nil {
		return fmt.Errorf("failed to serialize claim: %w", err)
	}

	// Write to Redis (full replacement)
	key := ClaimKey(c.instanceName, claim.ID)
	if err := c.rdb.HSet(ctx, key, hash).Err(); err != nil {
		return fmt.Errorf("failed to update claim in Redis: %w", err)
	}

	return nil
}

// ClaimExists checks if a claim exists without fetching it.
func (c *Client) ClaimExists(ctx context.Context, claimID string) (bool, error) {
	key := ClaimKey(c.instanceName, claimID)
	exists, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check claim existence: %w", err)
	}
	return exists > 0, nil
}

// GetClaimByArtefactID retrieves a claim by its associated artefact ID.
// Returns (nil, redis.Nil) if no claim exists for the given artefact.
// Used for idempotency checking - ensures only one claim per artefact.
func (c *Client) GetClaimByArtefactID(ctx context.Context, artefactID string) (*Claim, error) {
	// Look up claim ID from index
	indexKey := ClaimByArtefactKey(c.instanceName, artefactID)
	claimID, err := c.rdb.Get(ctx, indexKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, redis.Nil
		}
		return nil, fmt.Errorf("failed to lookup claim by artefact: %w", err)
	}

	// Retrieve the claim
	return c.GetClaim(ctx, claimID)
}

// SetBid records an agent's bid on a claim and publishes a bid_submitted event.
// Uses HSET on holt:{instance}:claim:{claim_id}:bids with key=agentName, value=bidType.
// Validates the bid type before writing.
// Publishes bid_submitted event to workflow_events channel after successful write.
func (c *Client) SetBid(ctx context.Context, claimID string, agentName string, bidType BidType) error {
	// Validate bid type
	if err := bidType.Validate(); err != nil {
		return fmt.Errorf("invalid bid type: %w", err)
	}

	// Write bid to Redis
	key := ClaimBidsKey(c.instanceName, claimID)
	if err := c.rdb.HSet(ctx, key, agentName, string(bidType)).Err(); err != nil {
		return fmt.Errorf("failed to write bid to Redis: %w", err)
	}

	// Publish bid_submitted event
	eventData := map[string]interface{}{
		"claim_id":   claimID,
		"agent_name": agentName,
		"bid_type":   string(bidType),
	}
	if err := c.publishWorkflowEvent(ctx, "bid_submitted", eventData); err != nil {
		return fmt.Errorf("failed to publish bid_submitted event: %w", err)
	}

	return nil
}

// GetAllBids retrieves all bids for a claim as a map of agent name to bid type.
// Returns empty map if no bids exist (not an error).
func (c *Client) GetAllBids(ctx context.Context, claimID string) (map[string]BidType, error) {
	key := ClaimBidsKey(c.instanceName, claimID)

	// Read all bids from Redis
	rawBids, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read bids from Redis: %w", err)
	}

	// Convert string values to BidType
	bids := make(map[string]BidType, len(rawBids))
	for agentName, bidTypeStr := range rawBids {
		bids[agentName] = BidType(bidTypeStr)
	}

	return bids, nil
}

// AddVersionToThread adds an artefact to a version thread.
// Uses ZADD with score=version to maintain sorted order.
// Threads are stored as ZSETs at holt:{instance}:thread:{logical_id}.
func (c *Client) AddVersionToThread(ctx context.Context, logicalID string, artefactID string, version int) error {
	key := ThreadKey(c.instanceName, logicalID)
	score := ThreadScore(version)

	z := redis.Z{
		Score:  score,
		Member: artefactID,
	}

	if err := c.rdb.ZAdd(ctx, key, z).Err(); err != nil {
		return fmt.Errorf("failed to add version to thread: %w", err)
	}

	return nil
}

// GetLatestVersion retrieves the artefact ID of the highest version in a thread.
// Returns ("", 0, redis.Nil) if the thread doesn't exist or is empty.
func (c *Client) GetLatestVersion(ctx context.Context, logicalID string) (artefactID string, version int, err error) {
	key := ThreadKey(c.instanceName, logicalID)

	// Get the member with the highest score (ZREVRANGE with limit 1)
	results, err := c.rdb.ZRevRangeWithScores(ctx, key, 0, 0).Result()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get latest version from thread: %w", err)
	}

	// Check if thread is empty
	if len(results) == 0 {
		return "", 0, redis.Nil
	}

	// Extract artefact ID and version
	artefactID = results[0].Member.(string)
	version = VersionFromScore(results[0].Score)

	return artefactID, version, nil
}

// Subscription represents an active Pub/Sub subscription to artefact events.
// Caller must call Close() when done to clean up resources.
// Subscriptions deliver full artefact objects via the Events() channel.
type Subscription struct {
	events <-chan *Artefact
	errors <-chan error
	cancel func()
	once   sync.Once
}

// Events returns the channel of artefact events.
// The channel will be closed when the subscription is closed or the context is cancelled.
func (s *Subscription) Events() <-chan *Artefact {
	return s.events
}

// Errors returns the channel of subscription errors.
// Errors include JSON unmarshaling failures and other non-fatal issues.
// The subscription continues after errors - messages are skipped.
func (s *Subscription) Errors() <-chan error {
	return s.errors
}

// Close stops the subscription and cleans up resources. Implements io.Closer.
// Safe to call multiple times - subsequent calls are no-ops.
func (s *Subscription) Close() error {
	s.once.Do(s.cancel)
	return nil
}

// ClaimSubscription represents an active Pub/Sub subscription to claim events.
// Caller must call Close() when done to clean up resources.
type ClaimSubscription struct {
	events <-chan *Claim
	errors <-chan error
	cancel func()
	once   sync.Once
}

// WorkflowEvent represents a workflow event (bid submission or claim grant).
// These events are published for real-time monitoring via the watch command.
type WorkflowEvent struct {
	Event string                 `json:"event"` // "bid_submitted" or "claim_granted"
	Data  map[string]interface{} `json:"data"`  // Event-specific data
}

// WorkflowSubscription represents an active Pub/Sub subscription to workflow events.
// Caller must call Close() when done to clean up resources.
type WorkflowSubscription struct {
	events <-chan *WorkflowEvent
	errors <-chan error
	cancel func()
	once   sync.Once
}

// Events returns the channel of claim events.
func (s *ClaimSubscription) Events() <-chan *Claim {
	return s.events
}

// Errors returns the channel of subscription errors.
func (s *ClaimSubscription) Errors() <-chan error {
	return s.errors
}

// Close stops the subscription and cleans up resources. Implements io.Closer.
func (s *ClaimSubscription) Close() error {
	s.once.Do(s.cancel)
	return nil
}

// Events returns the channel of workflow events.
func (s *WorkflowSubscription) Events() <-chan *WorkflowEvent {
	return s.events
}

// Errors returns the channel of subscription errors.
func (s *WorkflowSubscription) Errors() <-chan error {
	return s.errors
}

// Close stops the subscription and cleans up resources. Implements io.Closer.
func (s *WorkflowSubscription) Close() error {
	s.once.Do(s.cancel)
	return nil
}

// SubscribeArtefactEvents subscribes to artefact creation events for this instance.
// Returns a Subscription that delivers full artefact objects.
// Caller must call subscription.Close() when done.
// Context cancellation also stops the subscription.
//
// Events are delivered on a buffered channel (size 10) to prevent blocking.
// If the subscriber is too slow, events may be dropped by Redis Pub/Sub (at-most-once delivery).
func (c *Client) SubscribeArtefactEvents(ctx context.Context) (*Subscription, error) {
	channel := ArtefactEventsChannel(c.instanceName)
	pubsub := c.rdb.Subscribe(ctx, channel)

	// Create buffered channels for events and errors
	eventsChan := make(chan *Artefact, 10)
	errorsChan := make(chan error, 10)

	// Create cancellation context
	subCtx, cancelFunc := context.WithCancel(ctx)

	// Start goroutine to process messages
	go func() {
		defer close(eventsChan)
		defer close(errorsChan)
		defer pubsub.Close()

		// Receive channel from pubsub
		ch := pubsub.Channel()

		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				// Unmarshal artefact from JSON
				var artefact Artefact
				if err := json.Unmarshal([]byte(msg.Payload), &artefact); err != nil {
					// Send error on error channel, skip message
					select {
					case errorsChan <- fmt.Errorf("failed to unmarshal artefact event: %w", err):
					case <-subCtx.Done():
						return
					}
					continue
				}

				// Send artefact on events channel
				select {
				case eventsChan <- &artefact:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return &Subscription{
		events: eventsChan,
		errors: errorsChan,
		cancel: cancelFunc,
	}, nil
}

// SubscribeClaimEvents subscribes to claim creation events for this instance.
// Returns a ClaimSubscription that delivers full claim objects.
// Caller must call subscription.Close() when done.
func (c *Client) SubscribeClaimEvents(ctx context.Context) (*ClaimSubscription, error) {
	channel := ClaimEventsChannel(c.instanceName)
	pubsub := c.rdb.Subscribe(ctx, channel)

	// Create buffered channels for events and errors
	eventsChan := make(chan *Claim, 10)
	errorsChan := make(chan error, 10)

	// Create cancellation context
	subCtx, cancelFunc := context.WithCancel(ctx)

	// Start goroutine to process messages
	go func() {
		defer close(eventsChan)
		defer close(errorsChan)
		defer pubsub.Close()

		// Receive channel from pubsub
		ch := pubsub.Channel()

		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				// Unmarshal claim from JSON
				var claim Claim
				if err := json.Unmarshal([]byte(msg.Payload), &claim); err != nil {
					// Send error on error channel, skip message
					select {
					case errorsChan <- fmt.Errorf("failed to unmarshal claim event: %w", err):
					case <-subCtx.Done():
						return
					}
					continue
				}

				// Send claim on events channel
				select {
				case eventsChan <- &claim:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return &ClaimSubscription{
		events: eventsChan,
		errors: errorsChan,
		cancel: cancelFunc,
	}, nil
}

// SubscribeWorkflowEvents subscribes to workflow events (bid submissions and grants) for this instance.
// Returns a WorkflowSubscription that delivers workflow event objects.
// Caller must call subscription.Close() when done.
func (c *Client) SubscribeWorkflowEvents(ctx context.Context) (*WorkflowSubscription, error) {
	channel := WorkflowEventsChannel(c.instanceName)
	pubsub := c.rdb.Subscribe(ctx, channel)

	// Create buffered channels for events and errors
	eventsChan := make(chan *WorkflowEvent, 10)
	errorsChan := make(chan error, 10)

	// Create cancellation context
	subCtx, cancelFunc := context.WithCancel(ctx)

	// Start goroutine to process messages
	go func() {
		defer close(eventsChan)
		defer close(errorsChan)
		defer pubsub.Close()

		// Receive channel from pubsub
		ch := pubsub.Channel()

		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				// Unmarshal workflow event from JSON
				var event WorkflowEvent
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					// Send error on error channel, skip message
					select {
					case errorsChan <- fmt.Errorf("failed to unmarshal workflow event: %w", err):
					case <-subCtx.Done():
						return
					}
					continue
				}

				// Send event on events channel
				select {
				case eventsChan <- &event:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return &WorkflowSubscription{
		events: eventsChan,
		errors: errorsChan,
		cancel: cancelFunc,
	}, nil
}

// RawSubscription represents an active Pub/Sub subscription to a raw channel.
// Used for subscribing to custom channels like agent-specific event channels.
// Caller must call Close() when done to clean up resources.
type RawSubscription struct {
	messages <-chan string
	cancel   func()
	once     sync.Once
}

// Messages returns the channel of raw message payloads.
func (s *RawSubscription) Messages() <-chan string {
	return s.messages
}

// Close stops the subscription and cleans up resources. Implements io.Closer.
func (s *RawSubscription) Close() error {
	s.once.Do(s.cancel)
	return nil
}

// SubscribeRawChannel subscribes to a raw Pub/Sub channel for this instance.
// Returns a RawSubscription that delivers message payloads as strings.
// Caller must call subscription.Close() when done.
//
// This is used for subscribing to custom channels like agent-specific event channels
// where the message format is known but not typed (e.g., grant notifications).
func (c *Client) SubscribeRawChannel(ctx context.Context, channel string) (*RawSubscription, error) {
	pubsub := c.rdb.Subscribe(ctx, channel)

	// Create buffered channel for messages
	messagesChan := make(chan string, 10)

	// Create cancellation context
	subCtx, cancelFunc := context.WithCancel(ctx)

	// Start goroutine to process messages
	go func() {
		defer close(messagesChan)
		defer pubsub.Close()

		// Receive channel from pubsub
		ch := pubsub.Channel()

		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				// Send raw payload on messages channel
				select {
				case messagesChan <- msg.Payload:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return &RawSubscription{
		messages: messagesChan,
		cancel:   cancelFunc,
	}, nil
}

// PublishRaw publishes a raw message to a specified Redis Pub/Sub channel.
// This is used for publishing custom messages like grant notifications to agent-specific channels.
// The channel name should be a full channel name (not auto-prefixed with instance).
func (c *Client) PublishRaw(ctx context.Context, channel string, message string) error {
	if err := c.rdb.Publish(ctx, channel, message).Err(); err != nil {
		return fmt.Errorf("failed to publish to channel %s: %w", channel, err)
	}
	return nil
}

// publishWorkflowEvent publishes a workflow event to the workflow_events channel.
// This is an internal helper used by SetBid and orchestrator for real-time monitoring.
// Event types: "bid_submitted", "claim_granted"
func (c *Client) publishWorkflowEvent(ctx context.Context, eventType string, data map[string]interface{}) error {
	event := WorkflowEvent{
		Event: eventType,
		Data:  data,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow event: %w", err)
	}

	channel := WorkflowEventsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, eventJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish workflow event: %w", err)
	}

	return nil
}

// PublishWorkflowEvent publishes a workflow event to the workflow_events channel.
// This is exposed for orchestrator use when publishing claim_granted events.
// Event types: "bid_submitted", "claim_granted"
func (c *Client) PublishWorkflowEvent(ctx context.Context, eventType string, data map[string]interface{}) error {
	return c.publishWorkflowEvent(ctx, eventType, data)
}

// GetClaimsByStatus retrieves all claims with the specified statuses (M3.5).
// Used for orchestrator startup recovery to scan Redis for active claims.
// Returns empty slice if no claims match the specified statuses.
//
// Implementation: Uses Redis SCAN to iterate over claim keys, then filters by status.
// This is efficient for moderate claim counts (<10000) but may need optimization for larger datasets.
func (c *Client) GetClaimsByStatus(ctx context.Context, statuses []string) ([]*Claim, error) {
	if len(statuses) == 0 {
		return []*Claim{}, nil
	}

	// Build status set for O(1) lookup
	statusSet := make(map[ClaimStatus]bool)
	for _, status := range statuses {
		statusSet[ClaimStatus(status)] = true
	}

	// Scan for all claim keys using pattern matching
	pattern := ClaimKey(c.instanceName, "*")
	var claims []*Claim

	// Use SCAN to iterate over keys matching the pattern
	iter := c.rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()

		// Fetch claim from Redis
		hashData, err := c.rdb.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read claim from Redis: %w", err)
		}

		// Skip if key no longer exists (race condition)
		if len(hashData) == 0 {
			continue
		}

		// Deserialize claim
		claim, err := HashToClaim(hashData)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize claim: %w", err)
		}

		// Filter by status
		if statusSet[claim.Status] {
			claims = append(claims, claim)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan claim keys: %w", err)
	}

	return claims, nil
}

// ZAdd adds a member to a sorted set with a score (M3.5 - for grant queue FIFO).
// Used to add claims to the persistent grant queue when max_concurrent limit is reached.
func (c *Client) ZAdd(ctx context.Context, key string, score float64, member string) error {
	z := redis.Z{
		Score:  score,
		Member: member,
	}

	if err := c.rdb.ZAdd(ctx, key, z).Err(); err != nil {
		return fmt.Errorf("failed to add member to sorted set: %w", err)
	}

	return nil
}

// ZRange retrieves members from a sorted set by rank range (M3.5 - for grant queue dequeue).
// Returns members in order from lowest to highest score (FIFO for timestamp-based scores).
// start and stop are inclusive (0-based indexing, -1 for last element).
func (c *Client) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	members, err := c.rdb.ZRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read sorted set range: %w", err)
	}

	return members, nil
}

// ZRangeWithScores retrieves members with scores from a sorted set (M3.5 - for grant queue recovery).
// Used during startup to recover grant queue state with timestamps.
func (c *Client) ZRangeWithScores(ctx context.Context, key string, start, stop int64) ([]redis.Z, error) {
	results, err := c.rdb.ZRangeWithScores(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read sorted set with scores: %w", err)
	}

	return results, nil
}

// ZRem removes members from a sorted set (M3.5 - for grant queue dequeue).
// Used to remove claims from grant queue after they are resumed.
func (c *Client) ZRem(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}

	// Convert string slice to interface slice for variadic function
	memberInterfaces := make([]interface{}, len(members))
	for i, member := range members {
		memberInterfaces[i] = member
	}

	if err := c.rdb.ZRem(ctx, key, memberInterfaces...).Err(); err != nil {
		return fmt.Errorf("failed to remove members from sorted set: %w", err)
	}

	return nil
}

// createOrVersionKnowledgeScript is a Lua script for atomically creating or versioning Knowledge artefacts (M4.3).
// This script ensures that the knowledge_index check-and-set operation is truly atomic.
//
// KEYS[1] = knowledge_index Redis key (HASH mapping knowledge_name → logical_id)
// ARGV[1] = knowledge_name (globally unique name for this knowledge)
// ARGV[2] = new_logical_id (pre-generated UUID from Go, used if creating new thread)
//
// Returns: logical_id (existing or newly created) to use for versioning
var createOrVersionKnowledgeScript = redis.NewScript(`
	local existing = redis.call('HGET', KEYS[1], ARGV[1])
	if existing then
		return existing
	else
		redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
		return ARGV[2]
	end
`)

// CreateOrVersionKnowledge atomically creates a new Knowledge artefact or creates a new version
// of an existing Knowledge thread (M4.3).
//
// This method uses a Lua script to atomically check the knowledge_index and either:
//   - Return the existing logical_id if the knowledge_name already exists
//   - Create a new entry in the index with the provided newLogicalID if it doesn't exist
//
// The method then creates the Knowledge artefact with the appropriate version number and
// adds it to the thread_context SET for the specified threadLogicalID.
//
// Parameters:
//   - knowledgeName: Globally unique name for this knowledge (e.g., "go-sdk-docs")
//   - knowledgePayload: The actual content to cache
//   - contextForRoles: Array of glob patterns for which agent roles should receive this knowledge
//   - threadLogicalID: The logical_id of the work thread this knowledge belongs to
//   - producedByRole: The agent role producing this knowledge
//
// Returns the created Knowledge artefact.
func (c *Client) CreateOrVersionKnowledge(ctx context.Context, knowledgeName, knowledgePayload string, contextForRoles []string, threadLogicalID, producedByRole string) (*Artefact, error) {
	// Generate a new logical_id optimistically (may not be used if knowledge already exists)
	newLogicalID := uuid.New().String()

	// Execute Lua script to atomically get or create the logical_id for this knowledge_name
	indexKey := KnowledgeIndexKey(c.instanceName)
	result, err := createOrVersionKnowledgeScript.Run(ctx, c.rdb, []string{indexKey}, knowledgeName, newLogicalID).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to execute knowledge index script: %w", err)
	}

	logicalID, ok := result.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from Lua script: %T", result)
	}

	// Determine the version number by querying the thread
	threadKey := ThreadKey(c.instanceName, logicalID)
	versions, err := c.rdb.ZRevRangeWithScores(ctx, threadKey, 0, 0).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query thread for version: %w", err)
	}

	var version int
	if len(versions) == 0 {
		// This is v1 of a new knowledge thread
		version = 1
	} else {
		// Increment the latest version
		version = int(versions[0].Score) + 1
	}

	// Default target_roles to ["*"] if empty
	if len(contextForRoles) == 0 {
		contextForRoles = []string{"*"}
	}

	// Create the Knowledge artefact
	artefactID := uuid.New().String()
	knowledge := &Artefact{
		ID:              artefactID,
		LogicalID:       logicalID,
		Version:         version,
		StructuralType:  StructuralTypeKnowledge,
		Type:            knowledgeName, // The knowledge_name becomes the Type field
		Payload:         knowledgePayload,
		SourceArtefacts: []string{}, // Knowledge artefacts have no sources
		ProducedByRole:  producedByRole,
		ContextForRoles: contextForRoles,
		CreatedAtMs:     time.Now().UnixMilli(),
	}

	// Write the artefact to Redis (without publishing an event - Knowledge is passive)
	if err := knowledge.Validate(); err != nil {
		return nil, fmt.Errorf("invalid knowledge artefact: %w", err)
	}

	hash, err := ArtefactToHash(knowledge)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize knowledge artefact: %w", err)
	}

	artefactKey := ArtefactKey(c.instanceName, artefactID)
	if err := c.rdb.HSet(ctx, artefactKey, hash).Err(); err != nil {
		return nil, fmt.Errorf("failed to write knowledge artefact: %w", err)
	}

	// Add to thread tracking
	if err := c.rdb.ZAdd(ctx, threadKey, redis.Z{
		Score:  float64(version),
		Member: artefactID,
	}).Err(); err != nil {
		return nil, fmt.Errorf("failed to add to thread: %w", err)
	}

	// Add to thread_context SET for the work thread
	// M4.3: For manual provisioning, use special "global" logical_id
	targetLogicalID := threadLogicalID
	if targetLogicalID == "" {
		targetLogicalID = "global" // Special marker for manually provisioned knowledge
	}

	threadContextKey := ThreadContextKey(c.instanceName, targetLogicalID)
	if err := c.rdb.SAdd(ctx, threadContextKey, artefactID).Err(); err != nil {
		return nil, fmt.Errorf("failed to add to thread context: %w", err)
	}

	return knowledge, nil
}

// IsNotFound returns true if the error is a Redis "key not found" error (redis.Nil).
// Use this to check if GetArtefact, GetClaim, or GetLatestVersion returned "not found".
func IsNotFound(err error) bool {
	return errors.Is(err, redis.Nil)
}

// IsHashMismatchError checks if an error is a HashMismatchError and optionally extracts it.
// M4.6 Phase 4: Used by CLI verify command to distinguish hash mismatches from other errors.
// Returns true if err wraps a *HashMismatchError.
// If target is non-nil, the HashMismatchError is extracted into it (like errors.As).
func IsHashMismatchError(err error, target **HashMismatchError) bool {
	if target != nil {
		return errors.As(err, target)
	}
	var mismatchErr *HashMismatchError
	return errors.As(err, &mismatchErr)
}

// GetRedisClient returns the underlying Redis client for advanced operations.
// M4.1: Used by CLI commands to scan for artefacts (e.g., holt questions).
// Warning: This exposes the raw Redis client - use carefully to avoid breaking instance namespacing.
func (c *Client) GetRedisClient() *redis.Client {
	return c.rdb
}

// GetInstanceName returns the instance name this client is scoped to.
// M4.1: Used by CLI commands for key pattern construction.
func (c *Client) GetInstanceName() string {
	return c.instanceName
}

// TriggerGlobalLockdown executes the three-step alert mechanism for M4.6 security events.
//
// Step 1: LPUSH to holt:{instance}:security:alerts:log (permanent audit trail)
// Step 2: SET holt:{instance}:security:lockdown with alert payload (circuit breaker)
// Step 3: PUBLISH to holt:{instance}:security:alerts (real-time notification)
//
// This is called when hash mismatches or orphan blocks are detected.
// The orchestrator will halt ALL operations until manual unlock.
func (c *Client) TriggerGlobalLockdown(ctx context.Context, alert *SecurityAlert) error {
	alertJSON, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal security alert: %w", err)
	}

	// Step 1: LPUSH to permanent audit log (newest first)
	logKey := SecurityAlertsLogKey(c.instanceName)
	if err := c.rdb.LPush(ctx, logKey, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to persist alert to log: %w", err)
	}

	// Step 2: SET lockdown key (circuit breaker state)
	lockdownKey := SecurityLockdownKey(c.instanceName)
	if err := c.rdb.Set(ctx, lockdownKey, alertJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to set lockdown key: %w", err)
	}

	// Step 3: PUBLISH to real-time notification channel
	channel := SecurityAlertsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish alert: %w", err)
	}

	return nil
}

// IsInLockdown checks if the instance is currently in global lockdown.
// Returns (true, alert) if locked down, (false, nil) if operational.
// Used by orchestrator to check lockdown state before processing events.
func (c *Client) IsInLockdown(ctx context.Context) (bool, *SecurityAlert, error) {
	lockdownKey := SecurityLockdownKey(c.instanceName)
	alertJSON, err := c.rdb.Get(ctx, lockdownKey).Result()
	if errors.Is(err, redis.Nil) {
		// No lockdown key = system operational
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("failed to check lockdown status: %w", err)
	}

	// Lockdown key exists - unmarshal alert
	var alert SecurityAlert
	if err := json.Unmarshal([]byte(alertJSON), &alert); err != nil {
		return true, nil, fmt.Errorf("failed to unmarshal lockdown alert: %w", err)
	}

	return true, &alert, nil
}

// ClearLockdown removes the lockdown circuit breaker and logs the override.
// Creates a security_override alert in the audit log before clearing.
// Used by `holt security --unlock` command.
func (c *Client) ClearLockdown(ctx context.Context, reason, operator string) error {
	// Create override alert for audit trail
	overrideAlert := NewSecurityOverrideAlert(reason, operator)

	alertJSON, err := json.Marshal(overrideAlert)
	if err != nil {
		return fmt.Errorf("failed to marshal override alert: %w", err)
	}

	// LPUSH override to audit log
	logKey := SecurityAlertsLogKey(c.instanceName)
	if err := c.rdb.LPush(ctx, logKey, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to log security override: %w", err)
	}

	// DELETE lockdown key (clear circuit breaker)
	lockdownKey := SecurityLockdownKey(c.instanceName)
	if err := c.rdb.Del(ctx, lockdownKey).Err(); err != nil {
		return fmt.Errorf("failed to clear lockdown key: %w", err)
	}

	// PUBLISH override alert
	channel := SecurityAlertsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish override alert: %w", err)
	}

	return nil
}

// PublishSecurityAlert logs and publishes a security alert WITHOUT triggering lockdown.
// Used for non-critical security events like timestamp drift warnings.
// Step 1: LPUSH to holt:{instance}:security:alerts:log (permanent audit trail)
// Step 2: PUBLISH to holt:{instance}:security:alerts (real-time notification)
// Does NOT set lockdown key (no circuit breaker activation).
func (c *Client) PublishSecurityAlert(ctx context.Context, alert *SecurityAlert) error {
	alertJSON, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal security alert: %w", err)
	}

	// Step 1: LPUSH to permanent audit log (newest first)
	logKey := SecurityAlertsLogKey(c.instanceName)
	if err := c.rdb.LPush(ctx, logKey, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to persist alert to log: %w", err)
	}

	// Step 2: PUBLISH to real-time notification channel
	channel := SecurityAlertsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish alert: %w", err)
	}

	return nil
}

// WriteVerifiableArtefact writes a VerifiableArtefact to Redis.
// M4.6: This is used for V2 hash-based artefacts during testing and eventual migration.
// Similar to CreateArtefact but for the VerifiableArtefact type.
//
// Stores the artefact as a Redis Hash (HSET) to be compatible with existing GetArtefact (HGETALL) calls.
func (c *Client) WriteVerifiableArtefact(ctx context.Context, a *VerifiableArtefact) error {
	// Validate artefact structure
	if err := a.Validate(); err != nil {
		return fmt.Errorf("invalid verifiable artefact: %w", err)
	}

	// Convert V2 VerifiableArtefact to V1 Artefact for backwards compatibility with HGETALL
	// This ensures GetArtefact works for both V1 (UUID) and V2 (Hash) artefacts
	v1Artefact := &Artefact{
		ID:              a.ID,
		LogicalID:       a.Header.LogicalThreadID,
		Version:         a.Header.Version,
		StructuralType:  a.Header.StructuralType,
		Type:            a.Header.Type,
		Payload:         a.Payload.Content,
		SourceArtefacts: a.Header.ParentHashes,
		ProducedByRole:  a.Header.ProducedByRole,
		CreatedAtMs:     a.Header.CreatedAtMs,
		ClaimID:         a.Header.ClaimID,
		ContextForRoles: a.Header.ContextForRoles,
	}

	// Convert to Redis hash
	hash, err := ArtefactToHash(v1Artefact)
	if err != nil {
		return fmt.Errorf("failed to serialize verifiable artefact to hash: %w", err)
	}

	// Write to Redis using hash ID as key (HSET)
	key := ArtefactKey(c.instanceName, a.ID)
	if err := c.rdb.HSet(ctx, key, hash).Err(); err != nil {
		return fmt.Errorf("failed to write verifiable artefact to Redis: %w", err)
	}

	// Note: We don't publish to artefact_events channel here because this is test-only.
	// When V2 is fully implemented, the pup will write and publish.

	return nil
}

// GetVerifiableArtefact retrieves a V2 VerifiableArtefact by its hash ID.
// Returns (nil, redis.Nil) if the artefact doesn't exist.
// Use IsNotFound() to check for not-found errors.
// M4.6 Phase 4: Used by CLI verify command for independent hash verification.
func (c *Client) GetVerifiableArtefact(ctx context.Context, hashID string) (*VerifiableArtefact, error) {
	key := ArtefactKey(c.instanceName, hashID)

	// Read hash from Redis (HGETALL)
	hashData, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read verifiable artefact from Redis: %w", err)
	}

	// Check if key exists
	if len(hashData) == 0 {
		return nil, redis.Nil
	}

	// Convert Hash to V1 Artefact first (reusing existing deserialization logic)
	v1Artefact, err := HashToArtefact(hashData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize verifiable artefact from hash: %w", err)
	}

	// Convert V1 Artefact to V2 VerifiableArtefact structure
	v2Artefact := &VerifiableArtefact{
		ID: v1Artefact.ID,
		Header: ArtefactHeader{
			ParentHashes:    v1Artefact.SourceArtefacts,
			LogicalThreadID: v1Artefact.LogicalID,
			Version:         v1Artefact.Version,
			CreatedAtMs:     v1Artefact.CreatedAtMs,
			ProducedByRole:  v1Artefact.ProducedByRole,
			StructuralType:  v1Artefact.StructuralType,
			Type:            v1Artefact.Type,
			ContextForRoles: v1Artefact.ContextForRoles,
			ClaimID:         v1Artefact.ClaimID,
		},
		Payload: ArtefactPayload{
			Content: v1Artefact.Payload,
		},
	}

	return v2Artefact, nil
}

// ScanKeys scans for Redis keys matching the given pattern.
// Returns an array of matching key strings (sorted for consistency).
// Uses Redis SCAN for efficient iteration without blocking.
// M4.6 Phase 4: Used for short hash resolution in verify command.
func (c *Client) ScanKeys(ctx context.Context, pattern string) ([]string, error) {
	var matches []string
	iter := c.rdb.Scan(ctx, 0, pattern, 0).Iterator()

	for iter.Next(ctx) {
		matches = append(matches, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan keys: %w", err)
	}

	// Sort for consistent ordering
	sort.Strings(matches)

	return matches, nil
}

// GetSecurityAlerts retrieves historical security alerts from the permanent audit log.
// Returns alerts filtered by timestamp (sinceMs=0 returns all alerts).
// Alerts are returned in reverse chronological order (newest first).
// M4.6 Phase 4: Used by CLI security --alerts command.
func (c *Client) GetSecurityAlerts(ctx context.Context, sinceMs int64) ([]SecurityAlert, error) {
	key := SecurityAlertsLogKey(c.instanceName)

	// LRANGE 0 -1 gets all elements (newest first due to LPUSH)
	alertsJSON, err := c.rdb.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to read security alerts log: %w", err)
	}

	var alerts []SecurityAlert
	for _, alertJSON := range alertsJSON {
		var alert SecurityAlert
		if err := json.Unmarshal([]byte(alertJSON), &alert); err != nil {
			// Log parse error but continue (don't fail entire query for one bad entry)
			continue
		}

		// Apply timestamp filter
		if sinceMs > 0 && alert.TimestampMs < sinceMs {
			continue
		}

		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// GetLockdownState checks if the orchestrator is in global lockdown.
// Returns the lockdown alert if active, or (nil, redis.Nil) if not in lockdown.
// M4.6 Phase 4: Used by CLI security --unlock command.
func (c *Client) GetLockdownState(ctx context.Context) (SecurityAlert, error) {
	key := SecurityLockdownKey(c.instanceName)

	data, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return SecurityAlert{}, err
	}

	var alert SecurityAlert
	if err := json.Unmarshal([]byte(data), &alert); err != nil {
		return SecurityAlert{}, fmt.Errorf("failed to parse lockdown alert: %w", err)
	}

	return alert, nil
}

// SubscribeSecurityAlerts subscribes to the security alerts Pub/Sub channel.
// Returns a PubSub object for receiving live alerts.
// Caller must call Close() on the returned PubSub when done.
// M4.6 Phase 4: Used by CLI security --alerts --watch command.
func (c *Client) SubscribeSecurityAlerts(ctx context.Context) *redis.PubSub {
	channel := SecurityAlertsChannel(c.instanceName)
	return c.rdb.Subscribe(ctx, channel)
}

// UnlockGlobalLockdown clears the global lockdown state and logs the override.
// Performs three operations: (1) LPUSH override to audit log, (2) DEL lockdown key, (3) PUBLISH override alert.
// M4.6 Phase 4: Used by CLI security --unlock command.
func (c *Client) UnlockGlobalLockdown(ctx context.Context, overrideAlert SecurityAlert) error {
	// Serialize alert
	alertJSON, err := json.Marshal(overrideAlert)
	if err != nil {
		return fmt.Errorf("failed to marshal override alert: %w", err)
	}

	// Step 1: LPUSH to audit log
	logKey := SecurityAlertsLogKey(c.instanceName)
	if err := c.rdb.LPush(ctx, logKey, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to log override alert: %w", err)
	}

	// Step 2: DEL lockdown key (clear circuit breaker)
	lockdownKey := SecurityLockdownKey(c.instanceName)
	if err := c.rdb.Del(ctx, lockdownKey).Err(); err != nil {
		return fmt.Errorf("failed to clear lockdown key: %w", err)
	}

	// Step 3: PUBLISH override alert (real-time notification)
	channel := SecurityAlertsChannel(c.instanceName)
	if err := c.rdb.Publish(ctx, channel, alertJSON).Err(); err != nil {
		return fmt.Errorf("failed to publish override alert: %w", err)
	}

	return nil
}
