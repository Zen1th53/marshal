package antigravity_test

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
	"time"
)

var _ adapter.Adapter = (*antigravity.Client)(nil)

func TestAntigravityAdapterEvidenceIsReportedMetadataOnly(t *testing.T) {
	called := false
	client := antigravity.New("agy", &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		called = true
		return adapter.ProcessResult{}, nil
	}})
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	r := adapter.Result{Adapter: "agy", AdapterVersion: "1.2.14", SessionID: "native-session", ExitCode: 7, StartedAt: start, EndedAt: end, TimedOut: true, Cancelled: true, OutputTruncated: true, Stdout: []byte("private-stdout"), Stderr: []byte("private-stderr"), FinalText: "private-body"}
	got := client.CollectEvidence(r)
	if called || got["adapter"] != "agy" || got["session_id"] != "native-session" || got["exit_code"] != 7 || got["started_at"] != start || got["ended_at"] != end || got["cancelled"] != true || got["output_truncated"] != true {
		t.Fatal("metadata identity lost or native call made")
	}
	for _, key := range []string{"stdout", "stderr", "final_text", "events", "model"} {
		if _, exists := got[key]; exists {
			t.Fatal("unreported/raw evidence manufactured")
		}
	}
	if err := client.Shutdown(t.Context(), "native-session"); !errors.Is(err, model.ErrUnavailable) || called {
		t.Fatal("adapter claimed ownership of Runtime-managed native process")
	}
}
