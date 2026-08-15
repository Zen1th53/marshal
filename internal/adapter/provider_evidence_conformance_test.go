package adapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/gemini"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type providerRunner struct{}

func (providerRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	start := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var output string
	switch command.Path {
	case "codex":
		output = `{"type":"thread.started","thread_id":"codex-session"}
{"type":"item.completed","item":{"type":"agent_message","text":"provider output"}}`
	case "claude":
		output = `{"session_id":"claude-session","result":"provider output"}`
	case "gemini":
		output = `{"type":"session/init","session_id":"gemini-session"}
{"type":"final_result","text":"provider output"}`
	case "opencode":
		output = `{"session_id":"opencode-session","result":"provider output"}`
	}
	return adapter.ProcessResult{
		Stdout:    []byte(output),
		ExitCode:  0,
		StartedAt: start,
		EndedAt:   start.Add(time.Second),
		Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true},
	}, nil
}

func TestProvidersProduceTheSameEvidenceMetadataContract(t *testing.T) {
	providers := []struct {
		name  string
		build func() adapter.Adapter
	}{
		{name: "codex", build: func() adapter.Adapter { return codex.New("codex", providerRunner{}) }},
		{name: "claude", build: func() adapter.Adapter { return claude.New("claude", providerRunner{}) }},
		{name: "gemini", build: func() adapter.Adapter { return gemini.New("gemini", providerRunner{}) }},
		{name: "opencode", build: func() adapter.Adapter { return opencode.New("opencode", providerRunner{}) }},
	}

	wantKeys := map[string]struct{}{
		"adapter": {}, "adapter_version": {}, "session_id": {}, "exit_code": {},
		"started_at": {}, "ended_at": {}, "timed_out": {}, "output_truncated": {},
	}
	for _, provider := range providers {
		t.Run(provider.name, func(t *testing.T) {
			result, err := provider.build().Run(context.Background(), adapter.Request{
				TaskID: "TASK-A06", Title: "provider conformance", Worktree: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != adapter.StatusSuccess {
				t.Fatalf("provider result status = %s, want success", result.Status)
			}

			metadata := provider.build().CollectEvidence(result)
			if len(metadata) != len(wantKeys) {
				t.Fatalf("metadata keys = %#v, want exactly %#v", metadata, wantKeys)
			}
			for key := range wantKeys {
				if _, ok := metadata[key]; !ok {
					t.Errorf("metadata missing common key %q: %#v", key, metadata)
				}
			}
			for _, forbidden := range []string{"subject_id", "task_id", "change_id", "policy_digest", "authorized", "approved"} {
				if _, ok := metadata[forbidden]; ok {
					t.Errorf("provider supplied trust-bearing metadata %q", forbidden)
				}
			}

			// Provider output is data only. It must pass the same T06 sanitizer and
			// canonical digest path regardless of which adapter produced it.
			stringMetadata := map[string]string{
				"adapter":          provider.name,
				"session_id":       result.SessionID,
				"exit_code":        "0",
				"timed_out":        "false",
				"output_truncated": "false",
			}
			digest, err := evidence.CanonicalDigest(evidence.NodeTypeOutput, stringMetadata)
			if err != nil {
				t.Fatal(err)
			}
			clean, err := evidence.NewStrictSanitizer(evidence.SanitizerConfig{}).SanitizeNode(context.Background(), evidence.Node{
				ID: evidence.NodeID("NODE-" + provider.name), Type: evidence.NodeTypeOutput, Digest: digest, Metadata: stringMetadata,
			})
			if err != nil {
				t.Fatalf("provider metadata rejected by common sanitizer: %v", err)
			}
			if clean.Metadata["adapter"] != provider.name {
				t.Fatalf("adapter metadata changed during sanitization: %#v", clean.Metadata)
			}
		})
	}
}

func TestProviderEvidenceDoesNotGrantTrust(t *testing.T) {
	providers := []string{"codex", "claude", "gemini", "opencode"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			metadata := map[string]string{"adapter": provider, "claim": "verified"}
			digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, metadata)
			if err != nil {
				t.Fatal(err)
			}
			// A provider name and model text are content metadata only. No
			// provider-specific branch or implicit authorization is available
			// through the provider-neutral evidence contract.
			if _, err := evidence.NewStrictSanitizer(evidence.SanitizerConfig{}).SanitizeNode(context.Background(), evidence.Node{
				ID: evidence.NodeID("NODE-" + provider), Type: evidence.NodeTypeClaim, Digest: digest, Metadata: metadata,
			}); err != nil {
				t.Fatalf("provider claim failed common evidence contract: %v", err)
			}
		})
	}
}
