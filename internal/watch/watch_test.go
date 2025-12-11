package watch

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPollForClaim(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	ctx := context.Background()

	// Create blackboard client
	redisOpts := &redis.Options{
		Addr: mr.Addr(),
	}
	client, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	t.Run("returns claim when found immediately", func(t *testing.T) {
		// Create artefact
		artefactID := uuid.New().String()
		artefact := &blackboard.Artefact{
			ID:              artefactID,
			LogicalID:       uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType",
			ProducedByRole:  "test-agent",
			Payload:         "test",
			SourceArtefacts: []string{},
		}
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Create claim immediately
		claimID := uuid.New().String()
		claim := &blackboard.Claim{
			ID:                    claimID,
			ArtefactID:            artefactID,
			Status:                blackboard.ClaimStatusPendingReview,
			GrantedReviewAgents:   []string{},
			GrantedParallelAgents: []string{},
			GrantedExclusiveAgent: "",
		}
		err = client.CreateClaim(ctx, claim)
		require.NoError(t, err)

		// Poll should find it immediately
		foundClaim, err := PollForClaim(ctx, client, artefactID, 2*time.Second)
		require.NoError(t, err)
		require.NotNil(t, foundClaim)
		require.Equal(t, claimID, foundClaim.ID)
		require.Equal(t, artefactID, foundClaim.ArtefactID)
	})

	t.Run("returns claim when found after delay", func(t *testing.T) {
		// Create artefact
		artefactID := uuid.New().String()
		artefact := &blackboard.Artefact{
			ID:              artefactID,
			LogicalID:       uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType",
			ProducedByRole:  "test-agent",
			Payload:         "test",
			SourceArtefacts: []string{},
		}
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Create claim after a delay
		claimID := uuid.New().String()
		go func() {
			time.Sleep(500 * time.Millisecond)
			claim := &blackboard.Claim{
				ID:                    claimID,
				ArtefactID:            artefactID,
				Status:                blackboard.ClaimStatusPendingReview,
				GrantedReviewAgents:   []string{},
				GrantedParallelAgents: []string{},
				GrantedExclusiveAgent: "",
			}
			client.CreateClaim(context.Background(), claim)
		}()

		// Poll should find it after delay
		start := time.Now()
		foundClaim, err := PollForClaim(ctx, client, artefactID, 2*time.Second)
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, foundClaim)
		require.Equal(t, claimID, foundClaim.ID)
		require.GreaterOrEqual(t, elapsed, 500*time.Millisecond)
		require.Less(t, elapsed, 2*time.Second)
	})

	t.Run("returns error on timeout", func(t *testing.T) {
		// Create artefact but no claim
		artefactID := uuid.New().String()
		artefact := &blackboard.Artefact{
			ID:              artefactID,
			LogicalID:       uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType",
			ProducedByRole:  "test-agent",
			Payload:         "test",
			SourceArtefacts: []string{},
		}
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Poll should timeout
		start := time.Now()
		_, err = PollForClaim(ctx, client, artefactID, 500*time.Millisecond)
		elapsed := time.Since(start)

		require.Error(t, err)
		require.Contains(t, err.Error(), "timeout waiting for claim")
		require.GreaterOrEqual(t, elapsed, 500*time.Millisecond)
		require.Less(t, elapsed, 1*time.Second)
	})

	t.Run("returns error when context cancelled", func(t *testing.T) {
		// Create artefact but no claim
		artefactID := uuid.New().String()
		artefact := &blackboard.Artefact{
			ID:              artefactID,
			LogicalID:       uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType",
			ProducedByRole:  "test-agent",
			Payload:         "test",
			SourceArtefacts: []string{},
		}
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Cancel context after 100ms
		cancelCtx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		// Poll should be cancelled
		_, err = PollForClaim(cancelCtx, client, artefactID, 2*time.Second)
		require.Error(t, err)
		require.Equal(t, context.Canceled, err)
	})

	t.Run("handles multiple polling attempts", func(t *testing.T) {
		// Create artefact
		artefactID := uuid.New().String()
		artefact := &blackboard.Artefact{
			ID:              artefactID,
			LogicalID:       uuid.New().String(),
			Version:         1,
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "TestType",
			ProducedByRole:  "test-agent",
			Payload:         "test",
			SourceArtefacts: []string{},
		}
		err := client.CreateArtefact(ctx, artefact)
		require.NoError(t, err)

		// Create claim after multiple poll intervals (>400ms, so at least 2 polls)
		claimID := uuid.New().String()
		go func() {
			time.Sleep(450 * time.Millisecond)
			claim := &blackboard.Claim{
				ID:                    claimID,
				ArtefactID:            artefactID,
				Status:                blackboard.ClaimStatusPendingReview,
				GrantedReviewAgents:   []string{},
				GrantedParallelAgents: []string{},
				GrantedExclusiveAgent: "",
			}
			client.CreateClaim(context.Background(), claim)
		}()

		// Poll should find it after multiple attempts
		foundClaim, err := PollForClaim(ctx, client, artefactID, 2*time.Second)
		require.NoError(t, err)
		require.NotNil(t, foundClaim)
		require.Equal(t, claimID, foundClaim.ID)
	})
}

// M2.6 formatter tests
func TestFormatters(t *testing.T) {
	t.Run("defaultFormatter formats artefact events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &defaultFormatter{writer: writer}

		artefact := &blackboard.Artefact{
			ID:             "abc-123",
			Type:           "GoalDefined",
			ProducedByRole: "test-agent",
		}

		err := formatter.FormatArtefact(artefact)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, "✨ Artefact created")
		require.Contains(t, output, "type=GoalDefined")
		require.Contains(t, output, "id=abc-123")
	})

	t.Run("defaultFormatter formats Terminal artefact with completion message", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &defaultFormatter{writer: writer}

		artefact := &blackboard.Artefact{
			ID:             "terminal-12345678-1234-1234-1234-123456789012",
			Type:           "PackagedModule",
			StructuralType: blackboard.StructuralTypeTerminal,
			ProducedByRole: "ModulePackager",
		}

		err := formatter.FormatArtefact(artefact)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, "✨ Artefact created")
		require.Contains(t, output, "type=PackagedModule")
		require.Contains(t, output, "id=terminal") // Short ID (first 8 chars)
		require.Contains(t, output, "🎉 Workflow completed")
		require.Contains(t, output, "Terminal artefact created")
	})

	t.Run("defaultFormatter formats claim events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &defaultFormatter{writer: writer}

		claim := &blackboard.Claim{
			ID:         "claim-12345678-1234-1234-1234-123456789012",
			ArtefactID: "artefact-12345678-1234-1234-1234-123456789012",
			Status:     blackboard.ClaimStatusPendingReview,
		}

		err := formatter.FormatClaim(claim, 0)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, "⏳ Claim created")
		require.Contains(t, output, "claim=claim-12")    // Short ID (first 8 chars)
		require.Contains(t, output, "artefact=artefact") // Short ID (first 8 chars)
		require.Contains(t, output, "status=pending_review")
	})

	t.Run("defaultFormatter formats bid_submitted events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &defaultFormatter{writer: writer}

		event := &blackboard.WorkflowEvent{
			Event: "bid_submitted",
			Data: map[string]interface{}{
				"claim_id":   "claim-12345678-1234-1234-1234-123456789012",
				"agent_name": "test-agent",
				"bid_type":   "exclusive",
			},
		}

		err := formatter.FormatWorkflow(event, 0)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, "🙋 Bid submitted")
		require.Contains(t, output, "agent=test-agent")
		require.Contains(t, output, "claim=claim-12") // Short ID (first 8 chars)
		require.Contains(t, output, "type=exclusive")
	})

	t.Run("defaultFormatter formats claim_granted events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &defaultFormatter{writer: writer}

		event := &blackboard.WorkflowEvent{
			Event: "claim_granted",
			Data: map[string]interface{}{
				"claim_id":   "claim-12345678-1234-1234-1234-123456789012",
				"agent_name": "test-agent",
				"grant_type": "exclusive",
			},
		}

		err := formatter.FormatWorkflow(event, 0)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, "🏆 Claim granted")
		require.Contains(t, output, "agent=test-agent")
		require.Contains(t, output, "claim=claim-12") // Short ID (first 8 chars)
		require.Contains(t, output, "type=exclusive")
	})

	t.Run("jsonlFormatter formats artefact events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &jsonlFormatter{writer: writer}

		artefact := &blackboard.Artefact{
			ID:             "abc-123",
			Type:           "GoalDefined",
			ProducedByRole: "test-agent",
		}

		err := formatter.FormatArtefact(artefact)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, `"event":"artefact_created"`)
		require.Contains(t, output, `"id":"abc-123"`)
		require.Contains(t, output, `"type":"GoalDefined"`)
	})

	t.Run("jsonlFormatter formats claim events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &jsonlFormatter{writer: writer}

		claim := &blackboard.Claim{
			ID:         "claim-123",
			ArtefactID: "artefact-456",
			Status:     blackboard.ClaimStatusPendingReview,
		}

		err := formatter.FormatClaim(claim, 0)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, `"event":"claim_created"`)
		require.Contains(t, output, `"id":"claim-123"`)
		require.Contains(t, output, `"artefact_id":"artefact-456"`)
	})

	t.Run("jsonlFormatter formats workflow events", func(t *testing.T) {
		var buf []byte
		writer := &testWriter{buf: &buf}
		formatter := &jsonlFormatter{writer: writer}

		event := &blackboard.WorkflowEvent{
			Event: "bid_submitted",
			Data: map[string]interface{}{
				"claim_id":   "claim-123",
				"agent_name": "test-agent",
			},
		}

		err := formatter.FormatWorkflow(event, 0)
		require.NoError(t, err)

		output := string(buf)
		require.Contains(t, output, `"event":"bid_submitted"`)
		require.Contains(t, output, `"claim_id":"claim-123"`)
		require.Contains(t, output, `"agent_name":"test-agent"`)
	})
}

// testWriter is a simple writer for testing formatters
type testWriter struct {
	buf *[]byte
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

func TestDisplayHistoricalArtefacts_DeterministicTimestamps(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	ctx := context.Background()

	// Create blackboard client
	redisOpts := &redis.Options{
		Addr: mr.Addr(),
	}
	client, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	// Create artefact
	artefactID := "test-artefact-id"
	artefact := &blackboard.Artefact{
		ID:              artefactID,
		LogicalID:       "logical-id",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "TestType",
		ProducedByRole:  "test-agent",
		Payload:         "test",
		SourceArtefacts: []string{},
		CreatedAtMs:     1000000, // Fixed timestamp
	}
	err = client.CreateArtefact(ctx, artefact)
	require.NoError(t, err)

	// Create claim with multiple bids
	claimID := "test-claim-id"
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            artefactID,
		Status:                blackboard.ClaimStatusPendingReview,
		GrantedReviewAgents:   []string{},
		GrantedParallelAgents: []string{},
		GrantedExclusiveAgent: "",
		PhaseState: &blackboard.PhaseState{
			AllBids: map[string]blackboard.BidType{
				"agent-a": blackboard.BidTypeExclusive,
				"agent-b": blackboard.BidTypeIgnore,
				"agent-c": blackboard.BidTypeReview,
				"agent-d": blackboard.BidTypeParallel,
				"agent-e": blackboard.BidTypeExclusive,
			},
			BidTimestamps: map[string]int64{
				"agent-a": 1000001,
				"agent-b": 1000002,
				"agent-c": 1000003,
				"agent-d": 1000004,
				"agent-e": 1000005,
			},
		},
	}
	err = client.CreateClaim(ctx, claim)
	require.NoError(t, err)

	// Run displayHistoricalArtefacts multiple times and verify output is identical
	var firstOutput string

	for i := 0; i < 10; i++ {
		var buf bytes.Buffer
		formatter := &defaultFormatter{
			writer: &buf,
			client: client,
		}
		filters := &FilterCriteria{} // No filters

		err := displayHistoricalArtefacts(ctx, client, "test-instance", filters, formatter)
		require.NoError(t, err)

		currentOutput := buf.String()
		if i == 0 {
			firstOutput = currentOutput
		} else {
			if currentOutput != firstOutput {
				// Find the difference for better error reporting
				require.Equal(t, firstOutput, currentOutput, "Output mismatch on iteration %d", i)
			}
		}
	}
}

func TestDisplayHistoricalArtefacts_FeedbackClaim_Comprehensive(t *testing.T) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	ctx := context.Background()

	// Create blackboard client
	redisOpts := &redis.Options{
		Addr: mr.Addr(),
	}
	client, err := blackboard.NewClient(redisOpts, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	// 1. Create Original Artefact (t=1000)
	artefactID := "original-artefact-id"
	artefact := &blackboard.Artefact{
		ID:             artefactID,
		LogicalID:      "logical-id",
		Version:        1,
		StructuralType: blackboard.StructuralTypeStandard,
		Type:           "Code",
		ProducedByRole: "coder",
		Payload:        "bad code",
		CreatedAtMs:    1000,
	}
	require.NoError(t, client.CreateArtefact(ctx, artefact))

	// 2. Create Original Claim (Terminated)
	claimID := "original-claim-id"
	claim := &blackboard.Claim{
		ID:                    claimID,
		ArtefactID:            artefactID,
		Status:                blackboard.ClaimStatusTerminated, // Terminated due to feedback
		GrantedReviewAgents:   []string{"reviewer"},
		GrantedParallelAgents: []string{},
		GrantedExclusiveAgent: "",
		PhaseState: &blackboard.PhaseState{
			AllBids: map[string]blackboard.BidType{
				"reviewer": blackboard.BidTypeReview,
			},
			BidTimestamps: map[string]int64{
				"reviewer": 1001,
			},
		},
	}
	require.NoError(t, client.CreateClaim(ctx, claim))

	// 3. Create Review Artefact (Rejected) (t=2000)
	reviewID := "review-artefact-id"
	reviewArtefact := &blackboard.Artefact{
		ID:              reviewID,
		LogicalID:       "review-logical-id",
		Version:         1,
		StructuralType:  blackboard.StructuralTypeReview,
		Type:            "Review",
		ProducedByRole:  "reviewer",
		Payload:         `{"status":"rejected"}`, // Not empty {} so it is a rejection
		SourceArtefacts: []string{artefactID},
		CreatedAtMs:     2000,
	}
	require.NoError(t, client.CreateArtefact(ctx, reviewArtefact))

	// 4. Create Feedback Claim (PendingAssignment)
	// This mimics what orchestrator creates for rework.
	// Crucial: It has GrantedExclusiveAgent SET, which triggered the bug.
	feedbackClaimID := "feedback-claim-id"
	feedbackClaim := &blackboard.Claim{
		ID:                    feedbackClaimID,
		ArtefactID:            artefactID,
		Status:                blackboard.ClaimStatusPendingAssignment,
		GrantedExclusiveAgent: "coder",            // Assigned back to producer
		AdditionalContextIDs:  []string{reviewID}, // Context proves it IS a feedback claim
	}
	require.NoError(t, client.CreateClaim(ctx, feedbackClaim))

	// Run displayHistoricalArtefacts
	var buf bytes.Buffer
	formatter := &defaultFormatter{
		writer: &buf,
		client: client,
	}
	// Filter just enough to include our things
	filters := &testFilterCriteria{}

	err = displayHistoricalArtefacts(ctx, client, "test-instance", (*FilterCriteria)(filters), formatter)
	require.NoError(t, err)

	output := buf.String()

	// ASSERTIONS

	// 1. Check Original Artefact Creation
	require.Contains(t, output, "[01:00:01.000] ✨ Artefact created: by=coder, type=Code")

	// 2. Check Original Claim Creation
	require.Contains(t, output, "[01:00:01.000] ⏳ Claim created: claim=original, artefact=original, status=terminated")

	// 3. Check Bid Submitted (reconstructed from PhaseState)
	require.Contains(t, output, "[01:00:01.001] 🙋 Bid submitted: agent=reviewer, claim=original, type=review")

	// 4. Check Claim Granted (Review) - synthetic event from Grants list
	// Note: We populated GrantedReviewAgents, so this should appear. Timestamp is +100ms offset in logic.
	require.Contains(t, output, "[01:00:01.100] 🏆 Claim granted: agent=reviewer, claim=original, type=review")

	// 5. Check Review Rejection (from Review Artefact)
	require.Contains(t, output, "[01:00:02.000] ❌ Review Rejected: by=reviewer for artefact original (review: review-a)")

	// 6. Check Rework Assignment (synthetic event from feedback claim existence)
	// Logic: timestamp = latest_review_ts (2000) + 1 = 2001ms = 2.001s
	require.Contains(t, output, "[01:00:02.001] 🔄 Rework Assigned: to=coder for claim feedback (iteration 1)")

	// 7. CRITICAL: Check absence of misleading "Exclusive Grant"
	// Before fix, this would appear at t=1000 + some offset, claiming "exclusive" grant to "coder".
	require.NotContains(t, output, "type=exclusive", "Should NOT contain any exclusive grants")
	require.NotContains(t, output, "🏆 Claim granted: agent=coder", "Should NOT contain grant to coder")
}

// Helper to workaround FilterCriteria type alias if needed, though simple cast should work if accessible
type testFilterCriteria FilterCriteria
