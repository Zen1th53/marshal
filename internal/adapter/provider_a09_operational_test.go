package adapter_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/gemini"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/evidence"
)

// TestProviderEvidenceContractIsConcurrencyNeutral exercises the same
// evidence boundary concurrently for every first-class provider. Provider
// identity remains metadata only: it cannot add trust-bearing fields or alter
// the sanitizer and digest contract.
func TestProviderEvidenceContractIsConcurrencyNeutral(t *testing.T) {
	providers := []struct {
		name  string
		build func() adapter.Adapter
	}{
		{"codex", func() adapter.Adapter { return codex.New("codex", providerRunner{}) }},
		{"claude", func() adapter.Adapter { return claude.New("claude", providerRunner{}) }},
		{"gemini", func() adapter.Adapter { return gemini.New("gemini", providerRunner{}) }},
		{"opencode", func() adapter.Adapter { return opencode.New("opencode", providerRunner{}) }},
	}

	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	for _, provider := range providers {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			const workers = 16
			var wg sync.WaitGroup
			errs := make(chan error, workers)
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					result, err := provider.build().Run(context.Background(), adapter.Request{
						TaskID: "TASK-A09-" + provider.name, Title: "operational security", Worktree: t.TempDir(),
					})
					if err != nil {
						errs <- err
						return
					}
					metadata := provider.build().CollectEvidence(result)
					for _, forbidden := range []string{"subject_id", "task_id", "change_id", "policy_digest", "authorized", "approved"} {
						if _, ok := metadata[forbidden]; ok {
							errs <- errors.New("provider emitted trust-bearing metadata: " + forbidden)
							return
						}
					}
					stringMetadata := map[string]string{
						"adapter": provider.name, "session_id": result.SessionID,
						"exit_code": "0", "timed_out": "false", "output_truncated": "false",
					}
					digest, err := evidence.CanonicalDigest(evidence.NodeTypeOutput, stringMetadata)
					if err != nil {
						errs <- err
						return
					}
					_, err = sanitizer.SanitizeNode(context.Background(), evidence.Node{
						ID: evidence.NodeID("NODE-A09-" + provider.name), Type: evidence.NodeTypeOutput,
						Digest: digest, Metadata: stringMetadata,
					})
					if err != nil {
						errs <- err
					}
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}
}

type cancellingRunner struct {
	entered chan struct{}
}

func (r *cancellingRunner) Run(ctx context.Context, _ adapter.Command) (adapter.ProcessResult, error) {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return adapter.ProcessResult{}, ctx.Err()
}

// TestProviderCancellationStopsEveryAdapter verifies cancellation reaches the
// provider process boundary for all providers. No background provider work may
// outlive the caller's context.
func TestProviderCancellationStopsEveryAdapter(t *testing.T) {
	providers := []struct {
		name  string
		build func(adapter.ProcessRunner) adapter.Adapter
	}{
		{"codex", func(r adapter.ProcessRunner) adapter.Adapter { return codex.New("codex", r) }},
		{"claude", func(r adapter.ProcessRunner) adapter.Adapter { return claude.New("claude", r) }},
		{"gemini", func(r adapter.ProcessRunner) adapter.Adapter { return gemini.New("gemini", r) }},
		{"opencode", func(r adapter.ProcessRunner) adapter.Adapter { return opencode.New("opencode", r) }},
	}
	for _, provider := range providers {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			runner := &cancellingRunner{entered: make(chan struct{}, 1)}
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				_, err := provider.build(runner).Run(ctx, adapter.Request{
					TaskID: "TASK-A09-CANCEL", Title: "cancel", Worktree: t.TempDir(),
				})
				result <- err
			}()
			select {
			case <-runner.entered:
			case <-time.After(time.Second):
				t.Fatal("provider runner was not entered")
			}
			cancel()
			select {
			case err := <-result:
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("provider run outlived cancelled context")
			}
		})
	}
}
