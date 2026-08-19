package episode_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/episode"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT93ProviderAgnosticEpisodeCapture(t *testing.T) {
	capturer := episode.NewCapturer()
	ctx := context.Background()
	now := time.Now().UTC()

	providers := []string{"codex", "claude", "gemini", "opencode"}

	for _, prov := range providers {
		in := episode.EpisodeInput{
			ProjectID:        "PROJ-T93",
			TaskID:           "TASK-930",
			SessionID:        "SESS-1",
			RunID:            "RUN-1",
			Provider:         prov,
			TouchedFiles:     []string{"internal/app/runtime.go", "internal/store/memory.go"},
			CommandsExecuted: []string{"go test ./..."},
			EvidenceIDs:      []string{"EVID-93"},
			OutcomeSummary:   "Fixed memory indexing synchronization issue",
			Success:          true,
			BaseCommit:       "commit-aaa",
			ResultCommit:     "commit-bbb",
			ObservedAt:       now,
			ProviderExtMeta: map[string]any{
				"raw_model_id": prov + "-v1-preview",
			},
		}

		rec, err := capturer.CaptureEpisode(ctx, in)
		if err != nil {
			t.Fatalf("CaptureEpisode (%s): %v", prov, err)
		}

		if rec.Kind != model.MemoryKindEpisodic {
			t.Fatalf("expected episodic kind for provider %s, got: %s", prov, rec.Kind)
		}
		if rec.ProjectID != "PROJ-T93" || rec.ScopeID != "TASK-930" {
			t.Fatalf("scope mismatch for provider %s", prov)
		}
		if rec.Source.Kind != "runtime" || rec.Source.Reference != "TASK-930" {
			t.Fatalf("source reference mismatch for provider %s", prov)
		}
		if rec.ExtMeta["provider"] != prov {
			t.Fatalf("expected provider %s in ext_meta, got: %v", prov, rec.ExtMeta["provider"])
		}
		if rec.ExtMeta["raw_model_id"] != prov+"-v1-preview" {
			t.Fatalf("expected provider extension metadata isolated in ext_meta")
		}
	}
}

func TestT93FailedEpisodeLabeled(t *testing.T) {
	capturer := episode.NewCapturer()
	ctx := context.Background()

	in := episode.EpisodeInput{
		ProjectID:      "PROJ-T93",
		TaskID:         "TASK-FAIL",
		Provider:       "claude",
		Success:        false,
		OutcomeSummary: "Context limit exceeded during compilation",
		ObservedAt:     time.Now().UTC(),
	}

	rec, err := capturer.CaptureEpisode(ctx, in)
	if err != nil {
		t.Fatalf("CaptureEpisode: %v", err)
	}

	if rec.ExtMeta["execution_success"] != false {
		t.Fatal("expected execution_success to be false in ExtMeta")
	}
}
