package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// /fingerprint groups durably recorded failures by signature across runs,
// flags a repeated signature, and never shows a secret from a failure text.
func TestFingerprintHistoryFromDurableRuns(t *testing.T) {
	ctx := context.Background()
	repo := testgit.New(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	store, err := execution.NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i, reason := range []string{
		"build failed at 2026-09-01T12:00:00Z pointer 0xdeadbeef password=hunter2",
		"build failed at 2026-09-02T08:30:00Z pointer 0xcafe password=hunter2",
	} {
		run := execution.ExecutionRun{RunID: "RUN-fp-" + string(rune('a'+i)), SessionID: "S", State: execution.RunFailed,
			Failures:  []execution.RunFailure{{TaskID: "T" + string(rune('1'+i)), Stage: "EXECUTE", Reason: reason, Timestamp: t0.Add(time.Duration(i) * time.Hour)}},
			StartedAt: t0, UpdatedAt: t0}
		if i == 1 {
			run.Failures = append(run.Failures, execution.RunFailure{TaskID: "T9", Stage: "VERIFY", Reason: "different failure", Timestamp: t0})
		}
		if err := store.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	binding, _ := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	ws := NewWorkspace(runtime.Store(), runtime.ProjectID(), "S")
	ws.AttachRuntime(runtime, binding.ID)

	out, err := (&CommandHandler{ws: ws}).handleFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 distinct across 3 recorded failures in 2 runs", "x2  2 tasks, 2 runs, last EXECUTE", "REPEATED", "different failure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("a secret from a failure text was shown:\n%s", out)
	}
}
