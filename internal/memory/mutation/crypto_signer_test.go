package mutation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/mutation"
)

func TestT150CryptographicallyAuthorizedMutation(t *testing.T) {
	ctx := context.Background()
	secret := []byte("marshal-secure-test-master-key-0123456789")
	signer := mutation.NewSigner(1, secret)
	verifier := mutation.NewVerifier(map[int][]byte{1: secret})

	// 1. Sign initial mutation (v0 -> v1)
	env1, err := signer.SignMutation(ctx, mutation.MutationPayload{
		MemoryID:       "MEM-01",
		PrevRevision:   0,
		NewRevision:    1,
		ActorPrincipal: "operator:zen1th53",
		Action:         "CREATE",
		ContentDigest:  "digest-v1-sha256",
	})
	if err != nil {
		t.Fatalf("SignMutation: %v", err)
	}

	// Verify v1
	if err := verifier.VerifyMutation(ctx, env1, 0); err != nil {
		t.Fatalf("VerifyMutation v1: %v", err)
	}

	// 2. Tampered content digest in envelope fails verification
	tamperedEnv := env1
	tamperedEnv.ContentDigest = "forged-digest"
	if err := verifier.VerifyMutation(ctx, tamperedEnv, 0); !errors.Is(err, mutation.ErrInvalidMutationSignature) {
		t.Fatalf("expected ErrInvalidMutationSignature for tampered digest, got: %v", err)
	}

	// 3. Non-predecessor fork attempt fails
	envFork, err := signer.SignMutation(ctx, mutation.MutationPayload{
		MemoryID:       "MEM-01",
		PrevRevision:   0, // Invalid prev revision when head is 1
		NewRevision:    2,
		ActorPrincipal: "operator:zen1th53",
		Action:         "UPDATE",
		ContentDigest:  "digest-v2-sha256",
	})
	if err != nil {
		t.Fatalf("SignMutation Fork: %v", err)
	}
	if err := verifier.VerifyMutation(ctx, envFork, 1); !errors.Is(err, mutation.ErrInvalidRevisionChain) {
		t.Fatalf("expected ErrInvalidRevisionChain for fork attempt, got: %v", err)
	}

	// 4. Signer rotation (Epoch 1 -> Epoch 2)
	secret2 := []byte("marshal-rotated-key-epoch-2-987654321")
	signer.RotateKey(2, secret2)
	verifier.AddEpochKey(2, secret2)

	env2, err := signer.SignMutation(ctx, mutation.MutationPayload{
		MemoryID:       "MEM-01",
		PrevRevision:   1,
		NewRevision:    2,
		ActorPrincipal: "operator:zen1th53",
		Action:         "UPDATE",
		ContentDigest:  "digest-v2-sha256",
	})
	if err != nil {
		t.Fatalf("SignMutation Epoch 2: %v", err)
	}
	if err := verifier.VerifyMutation(ctx, env2, 1); err != nil {
		t.Fatalf("VerifyMutation with rotated key: %v", err)
	}
}
