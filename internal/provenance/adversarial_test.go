package provenance

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestT07A04AdversarialImmutabilityAndBoundaries(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	// 1. Unknown change ID
	if err := eng.AttachToolCall(ctx, "nonexistent", "tool-1"); !errors.Is(err, ErrChangeNotFound) {
		t.Fatalf("expected ErrChangeNotFound, got %v", err)
	}

	// 2. Begin record
	_, err := eng.Begin(ctx, "chg-adv", "task-adv", "agent-adv", "gemini", "ctx-adv", "patch-adv")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// 3. Patch mismatch
	if err := eng.AttachPatch(ctx, "chg-adv", "patch-mismatch"); !errors.Is(err, ErrPatchMismatch) {
		t.Fatalf("expected ErrPatchMismatch, got %v", err)
	}

	// 4. Invalid commit SHA
	for _, badSHA := range import_bad_shas() {
		if _, err := eng.Seal(ctx, "chg-adv", badSHA); !errors.Is(err, ErrInvalidCommit) {
			t.Fatalf("sha %q expected ErrInvalidCommit, got %v", badSHA, err)
		}
	}

	// 5. Valid seal
	sha := "abcdef0123456789abcdef0123456789abcdef01"
	if _, err := eng.Seal(ctx, "chg-adv", sha); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// 6. Post-seal mutations must fail closed
	if err := eng.AttachToolCall(ctx, "chg-adv", "t2"); !errors.Is(err, ErrAlreadySealed) {
		t.Fatalf("post-seal tool call error: %v", err)
	}
	if err := eng.AttachEvidence(ctx, "chg-adv", "ev2"); !errors.Is(err, ErrAlreadySealed) {
		t.Fatalf("post-seal evidence error: %v", err)
	}
	if err := eng.AttachApproval(ctx, "chg-adv", "app2"); !errors.Is(err, ErrAlreadySealed) {
		t.Fatalf("post-seal approval error: %v", err)
	}
}

func TestT07A04ConcurrentSealing(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	_, _ = eng.Begin(ctx, "chg-conc", "task-c", "agent-c", "claude", "ctx", "patch")

	var wg sync.WaitGroup
	sha := "9999999999abcdef9999999999abcdef99999999"
	sealedCount := 0
	var mu sync.Mutex

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := eng.Seal(ctx, "chg-conc", sha)
			if err == nil {
				mu.Lock()
				sealedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if sealedCount != 1 {
		t.Fatalf("expected exactly 1 successful seal, got %d", sealedCount)
	}
}

func import_bad_shas() []string {
	return []string{
		"invalid",
		"1234",
		"1234567890abcdef1234567890abcdef1234567G",
		"../../etc/passwd",
	}
}
