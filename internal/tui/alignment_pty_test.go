//go:build linux

package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The real binary lists a recorded alignment violation, records a decision
// against it, and says that /blind is not available.
func TestPTYAlignmentViolationsAndDecision(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	t.Chdir(project)
	const session = "SESSION-pty-alignment"
	binding, _ := projectid.LoadBinding(filepath.Join(project, projectid.StateDirName))
	store, err := execution.NewFileRunStore(filepath.Join(project, ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := &execution.AlignmentRecord{Result: alignment.Result{Violations: []alignment.Violation{{
		Type: alignment.CheckScopeLock, Severity: "BLOCKING", Path: "secrets.txt", Message: "file \"secrets.txt\" is outside allowed Goal scope [docs/]"}},
		PredictedRadius: 1, ObservedRadius: 2, EvaluatedAt: now}}
	if err := store.CreateRun(context.Background(), execution.ExecutionRun{RunID: "RUN-al", ProjectID: binding.ID, SessionID: session,
		State: execution.RunDonePendingVerification, Tasks: map[string]execution.TaskExecution{"T1": {TaskID: "T1", Alignment: record}},
		StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", session)
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/alignment scope")
	terminal.mustSee("#0 [BLOCKING] SCOPE_LOCK secrets.txt")
	terminal.sendLine("/alignment resolve run:RUN-al/T1#0 acknowledged reviewed with the owner")
	terminal.mustSee("Decision acknowledged recorded for run:RUN-al/T1#0")
	terminal.sendLine("/alignment violations")
	terminal.mustSee("decision acknowledged by local-uid:")
	terminal.sendLine("/blind")
	terminal.mustSee("not available in this build")
}
