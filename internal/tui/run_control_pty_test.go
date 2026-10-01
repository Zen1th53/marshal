//go:build linux

package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The real binary pauses and cancels a run of its session, asks which run
// when more than one qualifies, and records the result durably.
func TestPTYRunControl(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	const session = "SESSION-pty-runs"
	binding, found := projectid.LoadBinding(filepath.Join(project, projectid.StateDirName))
	if !found {
		t.Fatal("no binding")
	}
	store, err := execution.NewFileRunStore(filepath.Join(project, ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"RUN-a", "RUN-b"} {
		if err := store.CreateRun(context.Background(), execution.ExecutionRun{RunID: id, ProjectID: binding.ID, SessionID: session,
			State: execution.RunRunning, StartedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", session)
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/pause")
	terminal.mustSee("more than one run qualifies")
	terminal.sendLine("/pause run:RUN-a")
	terminal.mustSee("Run RUN-a PAUSED; no task is executing.")
	terminal.sendLine("/cancel run:RUN-b")
	terminal.mustSee("Run RUN-b CANCELLED")
	terminal.sendLine("/cancel run:RUN-zz")
	terminal.mustSee("is not a run of this session")

	runtime, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	a, _ := runtime.Execution().Engine().GetRun(context.Background(), "RUN-a")
	b, _ := runtime.Execution().Engine().GetRun(context.Background(), "RUN-b")
	if a.State != execution.RunPaused || b.State != execution.RunCancelled {
		t.Fatalf("durable states: %s %s", a.State, b.State)
	}
}
