package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// /reinjection and /runtime read back the goal binding of this session's runs
// and the constraint package each native turn received.
func TestReinjectionAndRuntimeReadBackRuns(t *testing.T) {
	_, empty, emptyCtx := acceptanceWorkspace(t)
	out, _ := (&CommandHandler{ws: empty}).Handle(emptyCtx, "/reinjection")
	if !strings.Contains(out, "No runs in this session") {
		t.Fatalf("no runs: %s", out)
	}

	// The execution service loads runs once, so seed the durable run store
	// before the runtime is opened.
	ctx := context.Background()
	repo := testgit.New(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	store, err := execution.NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	const session = "SESSION-reinject"
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, execution.ExecutionRun{RunID: "RUN-reinject", SessionID: session, GoalID: "GOAL-r", GoalRevision: 1,
		State: execution.RunRunning, HardConstraints: []string{"no network"}, PolicySnapshot: "policy-v1",
		Tasks: map[string]execution.TaskExecution{
			"T1": {TaskID: "T1", NativeTurn: &execution.NativeTurnBinding{Provider: "codex", TurnID: "turn-1", ConstraintDigest: "sha256:0123456789abcdef0123"}},
			"T2": {TaskID: "T2"},
		}, StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	binding, _ := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	ws := NewWorkspace(runtime.Store(), runtime.ProjectID(), session)
	ws.AttachRuntime(runtime, binding.ID)
	ws.mu.Lock()
	ws.state.Goal = model.GoalContract{ID: "GOAL-r", Revision: 2}
	ws.mu.Unlock()
	h := &CommandHandler{ws: ws}

	out, err = h.handleReinjection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Run RUN-reinject: goal GOAL-r rev 1, STALE (current goal GOAL-r rev 2); 1 hard constraints bound",
		"task T1: codex turn turn-1 received constraint package sha256:0123456789ab"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	out, err = h.handleRuntime(ctx)
	if err != nil || !strings.Contains(out, "Run RUN-reinject: RUNNING (goal GOAL-r rev 1, 2 tasks, policy snapshot policy-v1)") {
		t.Fatalf("/runtime: %q %v", out, err)
	}
}
