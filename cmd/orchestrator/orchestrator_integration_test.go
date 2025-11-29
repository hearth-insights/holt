// go:build integration
//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dyluth/holt/internal/orchestrator"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupRedis starts a Redis container for testing.
func setupRedis(t *testing.T) (string, func()) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
		// Disable Ryuk to avoid Docker-in-Docker issues
		SkipReaper: true,
	}

	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	// Get container's internal host and port for Docker network connectivity
	host, err := redisC.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	// Get the mapped port
	mappedPort, err := redisC.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("Failed to get mapped port: %v", err)
	}

	redisURL := "redis://" + host + ":" + mappedPort.Port()

	cleanup := func() {
		if err := redisC.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Redis container: %v", err)
		}
	}

	return redisURL, cleanup
}

// helper to create a valid hashed artefact for V1 client
func createValidArtefact(t *testing.T, logicalID string, version int, structuralType blackboard.StructuralType, typ, payload string, sourceArtefacts []string, role string) *blackboard.Artefact {
	// Create V2 representation to compute hash
	v2 := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    sourceArtefacts,
			LogicalThreadID: logicalID,
			Version:         version,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  role,
			StructuralType:  structuralType,
			Type:            typ,
			ClaimID:         "", // Root/User artefact
		},
		Payload: blackboard.ArtefactPayload{
			Content: payload,
		},
	}

	hash, err := blackboard.ComputeArtefactHash(v2)
	require.NoError(t, err)

	// Return V1 representation with computed hash
	return &blackboard.Artefact{
		ID:              hash,
		LogicalID:       logicalID,
		Version:         version,
		StructuralType:  structuralType,
		Type:            typ,
		Payload:         payload,
		SourceArtefacts: sourceArtefacts,
		ProducedByRole:  role,
		CreatedAtMs:     v2.Header.CreatedAtMs,
	}
}

// TestOrchestrator_CreatesClaimForGoalDefined tests the happy path.
func TestOrchestrator_CreatesClaimForGoalDefined(t *testing.T) {
	redisURL, cleanup := setupRedis(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create blackboard client
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client, err := blackboard.NewClient(opts, "test-instance")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start orchestrator
	engine := orchestrator.NewEngine(client, "test-instance", nil, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Give orchestrator time to subscribe
	time.Sleep(500 * time.Millisecond)

	// Create a GoalDefined artefact
	artefact := createValidArtefact(t,
		blackboard.NewID(),
		1,
		blackboard.StructuralTypeStandard,
		"GoalDefined",
		"hello world",
		[]string{},
		"user", // M3.7: GoalDefined created by user
	)

	if err := client.CreateArtefact(ctx, artefact); err != nil {
		t.Fatalf("Failed to create artefact: %v", err)
	}

	// Wait for claim to be created (with timeout)
	var claim *blackboard.Claim
	for i := 0; i < 20; i++ {
		claim, err = client.GetClaimByArtefactID(ctx, artefact.ID)
		if err == nil {
			break
		}
		if !blackboard.IsNotFound(err) {
			t.Fatalf("Unexpected error: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if claim == nil {
		t.Fatal("Claim was not created within timeout")
	}

	// Verify claim properties
	if claim.ArtefactID != artefact.ID {
		t.Errorf("Expected claim for artefact %s, got %s", artefact.ID, claim.ArtefactID)
	}

	if claim.Status != blackboard.ClaimStatusPendingReview {
		t.Errorf("Expected status pending_review, got %s", claim.Status)
	}

	if len(claim.GrantedReviewAgents) != 0 {
		t.Errorf("Expected no granted agents in Phase 1, got %v", claim.GrantedReviewAgents)
	}

	// Stop orchestrator
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Orchestrator returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Orchestrator did not shut down within timeout")
	}
}

// TestOrchestrator_SkipsTerminalArtefacts verifies Terminal artefacts are skipped.
func TestOrchestrator_SkipsTerminalArtefacts(t *testing.T) {
	redisURL, cleanup := setupRedis(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client, err := blackboard.NewClient(opts, "test-instance")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start orchestrator
	engine := orchestrator.NewEngine(client, "test-instance", nil, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Give orchestrator time to subscribe
	time.Sleep(500 * time.Millisecond)

	// Create a Terminal artefact
	artefact := createValidArtefact(t,
		blackboard.NewID(),
		1,
		blackboard.StructuralTypeTerminal,
		"FinalReport",
		"workflow complete",
		[]string{},
		"orchestrator", // Terminal usually by orchestrator
	)

	if err := client.CreateArtefact(ctx, artefact); err != nil {
		t.Fatalf("Failed to create artefact: %v", err)
	}

	// Wait a bit to ensure no claim is created
	time.Sleep(1 * time.Second)

	// Verify no claim was created
	claim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
	if err != nil && !blackboard.IsNotFound(err) {
		t.Fatalf("Unexpected error: %v", err)
	}

	if claim != nil {
		t.Error("Expected no claim for Terminal artefact, but claim was created")
	}

	// Stop orchestrator
	cancel()
	<-errCh
}

// TestOrchestrator_IdempotentClaimCreation verifies duplicate events produce single claim.
func TestOrchestrator_IdempotentClaimCreation(t *testing.T) {
	redisURL, cleanup := setupRedis(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client, err := blackboard.NewClient(opts, "test-instance")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start orchestrator
	engine := orchestrator.NewEngine(client, "test-instance", nil, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Give orchestrator time to subscribe
	time.Sleep(500 * time.Millisecond)

	// Create artefact twice (simulating duplicate event)
	artefact := createValidArtefact(t,
		blackboard.NewID(),
		1,
		blackboard.StructuralTypeStandard,
		"GoalDefined",
		"test goal",
		[]string{},
		"user", // M3.7: GoalDefined created by user
	)

	if err := client.CreateArtefact(ctx, artefact); err != nil {
		t.Fatalf("Failed to create artefact (first): %v", err)
	}

	// Wait for first claim
	time.Sleep(500 * time.Millisecond)

	// Get first claim ID
	firstClaim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
	if err != nil {
		t.Fatalf("Failed to get first claim: %v", err)
	}

	// Create artefact again (idempotent operation - should not create new claim)
	if err := client.CreateArtefact(ctx, artefact); err != nil {
		t.Fatalf("Failed to create artefact (second): %v", err)
	}

	// Wait a bit
	time.Sleep(500 * time.Millisecond)

	// Verify only one claim exists
	secondClaim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
	if err != nil {
		t.Fatalf("Failed to get second claim: %v", err)
	}

	if firstClaim.ID != secondClaim.ID {
		t.Errorf("Expected same claim ID, got %s and %s", firstClaim.ID, secondClaim.ID)
	}

	// Stop orchestrator
	cancel()
	<-errCh
}

// TestOrchestrator_HealthCheckEndpoint verifies /healthz endpoint works.
func TestOrchestrator_HealthCheckEndpoint(t *testing.T) {
	redisURL, cleanup := setupRedis(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client, err := blackboard.NewClient(opts, "test-instance")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start orchestrator
	engine := orchestrator.NewEngine(client, "test-instance", nil, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Give orchestrator time to start health server
	time.Sleep(500 * time.Millisecond)

	// Call health check endpoint
	resp, err := http.Get("http://localhost:8080/healthz")
	if err != nil {
		t.Fatalf("Failed to call health check: %v", err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Stop orchestrator
	cancel()
	<-errCh
}

// TestOrchestrator_GracefulShutdown verifies SIGTERM handling.
func TestOrchestrator_GracefulShutdown(t *testing.T) {
	redisURL, cleanup := setupRedis(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Failed to parse Redis URL: %v", err)
	}

	client, err := blackboard.NewClient(opts, "test-instance")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start orchestrator
	engine := orchestrator.NewEngine(client, "test-instance", nil, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.Run(ctx)
	}()

	// Give orchestrator time to start
	time.Sleep(500 * time.Millisecond)

	// Cancel context (simulates SIGTERM)
	cancel()

	// Verify orchestrator exits within timeout
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Orchestrator returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Orchestrator did not shut down within timeout")
	}
}
