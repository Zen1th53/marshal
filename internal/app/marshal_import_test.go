package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

func TestImportedTaskUsesBoundApprovalAndMerge(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	r := &Runtime{store: s.Store, layout: project.Layout{Root: s.Repository, Worktrees: s.Worktrees}}
	base := marshalGit(t, s.Repository, "rev-parse", "HEAD")
	marshalGit(t, s.Repository, "switch", "-c", "finished")
	if err := os.WriteFile(filepath.Join(s.Repository, "a.txt"), []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, s.Repository, "add", "a.txt")
	marshalGit(t, s.Repository, "commit", "-m", "finish")
	head := marshalGit(t, s.Repository, "rev-parse", "HEAD")
	marshalGit(t, s.Repository, "switch", "--detach", base)
	// The operator's current HEAD may differ from the imported result's base.
	if err := os.WriteFile(filepath.Join(s.Repository, "operator.txt"), []byte("separate"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, s.Repository, "add", "operator.txt")
	marshalGit(t, s.Repository, "commit", "-m", "operator branch")
	// The imported task is a durable CLI hand-in, with its runtime session/run.
	if _, err := r.ImportTasks(t.Context(), []model.Task{{ID: "TASK-finished", Title: "write a.txt", Status: model.TaskReview, Risk: model.R1, BaseCommit: &base, HeadCommit: &head}}); err != nil {
		t.Fatal(err)
	}
	agent := model.Agent{ID: "AGENT-worker", ProjectID: s.ProjectID, DisplayName: "worker", Role: model.RoleDeveloper, ModelProvider: "codex", Status: model.AgentRegistered}
	err := s.Store.RegisterAgent(t.Context(), agent)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Store.StartSession(t.Context(), model.SessionStart{ID: "SESSION-finished", AgentID: agent.ID, ProjectID: agent.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	_ = session
	if err := s.Store.StartRun(t.Context(), model.WorkerRun{ID: "RUN-finished", TaskID: "TASK-finished", SessionID: "SESSION-finished", Adapter: "codex", AdapterVersion: "test", StartedAt: time.Now().UTC(), BaseCommit: base, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := s.Store.FinishRun(t.Context(), model.RunFinish{ID: "RUN-finished", Status: "success", EndedAt: time.Now().UTC(), ResultCommit: head, ExitStatus: &zero}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Store.GetMarshalSettings(t.Context(), s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.AcceptanceMode = marshal.AcceptUser
	if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	run, err := r.ImportMarshalTask(t.Context(), s, "imported", "TASK-finished", "test -f a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].ImportedResult.ResultCommit != head {
		t.Fatal("result not pinned")
	}
	if _, err = s.Dispatch(t.Context(), "imported", "TASK-finished", "review"); err == nil {
		t.Fatal("dispatch without approval")
	}
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "", errors.New("no operator approval") }
	if _, err = s.Approve(t.Context(), "imported"); err == nil {
		t.Fatal("plan approved without operator")
	}
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "operator", nil }
	if _, err = s.Approve(t.Context(), "imported"); err != nil {
		t.Fatal(err)
	}
	// A stored source binding cannot be changed after approval.
	approved, rev, err := s.load(t.Context(), "imported")
	if err != nil {
		t.Fatal(err)
	}
	original := *approved.Tasks[0].ImportedResult
	approved.Tasks[0].ImportedResult.Revision++
	if err := s.save(t.Context(), "imported", approved, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := r.marshalGovernedRun(s)(t.Context(), driver.Request{RunID: "imported", Task: approved.Tasks[0]}); err == nil {
		t.Fatal("changed imported binding accepted")
	}
	approved, rev, err = s.load(t.Context(), "imported")
	if err != nil {
		t.Fatal(err)
	}
	approved.Tasks[0].ImportedResult = &original
	if err := s.save(t.Context(), "imported", approved, rev); err != nil {
		t.Fatal(err)
	}
	if err := s.Merge(t.Context(), "imported", "TASK-finished"); err == nil {
		t.Fatal("unaccepted task merged")
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: r.marshalGovernedRun(s)}}
	s.ApprovalActor = onlyApproves("plan")
	if _, err = s.Execute(t.Context(), "imported", func(marshal.Task, BriefContext) string { return "review imported" }, nil); err == nil {
		t.Fatal("accepted without task approval")
	}
	if err := s.Merge(t.Context(), "imported", "TASK-finished"); err == nil {
		t.Fatal("merged without task approval")
	}
	s.ApprovalActor = onlyApproves("TASK-finished")
	if _, err = s.Execute(t.Context(), "imported", func(marshal.Task, BriefContext) string { return "review imported" }, nil); err != nil {
		t.Fatal(err)
	}
	run, err = s.Snapshot(t.Context(), "imported")
	if err != nil || run.Tasks[0].State != marshal.Merged {
		t.Fatalf("not merged: %+v %v", run, err)
	}
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "", errors.New("no close approval") }
	before := marshalGit(t, s.Repository, "rev-parse", "main")
	if err = s.Close(t.Context(), "imported"); err == nil {
		t.Fatal("delivered without close approval")
	}
	if got := marshalGit(t, s.Repository, "rev-parse", "main"); got != before {
		t.Fatal("ref moved without approval")
	}
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "operator", nil }
	if err = s.Close(t.Context(), "imported"); err != nil {
		t.Fatal(err)
	}
	if got := marshalGit(t, s.Repository, "show", "main:a.txt"); got != "done" {
		t.Fatalf("target content %q", got)
	}
}
