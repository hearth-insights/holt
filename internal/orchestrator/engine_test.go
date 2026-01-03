package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessArtefact(t *testing.T) {
	// Setup miniredis
	s := miniredis.RunT(t)

	// Setup blackboard client
	client, err := blackboard.NewClient(&redis.Options{Addr: s.Addr()}, "test-instance")
	require.NoError(t, err)
	defer client.Close()

	engine := &Engine{
		client:        client,
		instanceName:  "test-instance",
		agentRegistry: map[string]string{}, // No agents for this test to avoid consensus wait
	}

	ctx := context.Background()

	// Scenario 1: Skip non-claimable artefacts
	t.Run("SkipNonClaimable", func(t *testing.T) {
		nonClaimableTypes := []blackboard.StructuralType{
			blackboard.StructuralTypeTerminal,
			blackboard.StructuralTypeFailure,
			blackboard.StructuralTypeReview,
			blackboard.StructuralTypeKnowledge,
		}

		for _, st := range nonClaimableTypes {
			artefact := &blackboard.Artefact{
				Header: blackboard.ArtefactHeader{
					LogicalThreadID: "logical-" + string(st),
					Version:         1,
					StructuralType:  st,
					Type:            "TestType",
					ProducedByRole:  "user",
					CreatedAtMs:     time.Now().UnixMilli(),
					ParentHashes:    []string{},
				},
				Payload: blackboard.ArtefactPayload{Content: "test payload"},
			}

			// Compute valid hash
			hash, err := blackboard.ComputeArtefactHash(artefact)
			require.NoError(t, err)
			artefact.ID = hash

			err = engine.processArtefact(ctx, artefact)
			assert.NoError(t, err)

			// Verify no claim was created
			exists, err := client.ClaimExists(ctx, "claim-for-"+artefact.ID)
			assert.NoError(t, err)
			assert.False(t, exists)

			// Verify no claim index
			claim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
			assert.Error(t, err)
			assert.True(t, blackboard.IsNotFound(err))
			assert.Nil(t, claim)
		}
	})

	// Scenario 2: Successful claim creation for Standard artefact
	t.Run("CreateClaim", func(t *testing.T) {
		artefact := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "logical-standard",
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "Code",
				ProducedByRole:  "user",
				CreatedAtMs:     time.Now().UnixMilli(),
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{Content: "some code content"},
		}

		// Compute valid hash

		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash

		err = engine.processArtefact(ctx, artefact)
		assert.NoError(t, err)

		// Verify claim created
		claim, err := client.GetClaimByArtefactID(ctx, artefact.ID)
		assert.NoError(t, err)
		assert.NotNil(t, claim)
		assert.Equal(t, artefact.ID, claim.ArtefactID)
		assert.Equal(t, blackboard.ClaimStatusPendingReview, claim.Status)
	})

	// Scenario 3: Idempotency (Duplicate artefact)
	t.Run("Idempotency", func(t *testing.T) {
		artefact := &blackboard.Artefact{
			Header: blackboard.ArtefactHeader{
				LogicalThreadID: "logical-duplicate",
				Version:         1,
				StructuralType:  blackboard.StructuralTypeStandard,
				Type:            "Code",
				ProducedByRole:  "user",
				CreatedAtMs:     time.Now().UnixMilli(),
				ParentHashes:    []string{},
			},
			Payload: blackboard.ArtefactPayload{Content: "duplicate code content"},
		}

		// Compute valid hash

		hash, err := blackboard.ComputeArtefactHash(artefact)
		require.NoError(t, err)
		artefact.ID = hash

		// First processing
		err = engine.processArtefact(ctx, artefact)
		assert.NoError(t, err)

		// Get the created claim ID
		claim1, err := client.GetClaimByArtefactID(ctx, artefact.ID)
		assert.NoError(t, err)

		// Second processing
		err = engine.processArtefact(ctx, artefact)
		assert.NoError(t, err)

		// Verify claim ID hasn't changed (no new claim created)
		claim2, err := client.GetClaimByArtefactID(ctx, artefact.ID)
		assert.NoError(t, err)
		assert.Equal(t, claim1.ID, claim2.ID)
	})
}
