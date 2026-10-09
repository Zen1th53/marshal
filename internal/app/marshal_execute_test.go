package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/verification"
)

func marshalBrief(t marshal.Task, _ BriefContext) string { return "complete task " + t.PlanTaskID }

// Execute drives an approved plan through dispatch, review, merge and
// verification, and stops where the default acceptance mode needs the user
// to close.
func TestM09ExecuteRunsApprovedPlanToUserClose(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	var seen []marshal.RunState
	run, err := s.Execute(ctx, "run", marshalBrief, func(r marshal.Run) { seen = append(seen, r.State) })
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Verifying {
		t.Fatalf("state = %s, want verifying (awaiting the user's close)", run.State)
	}
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s = %s, want merged", task.PlanTaskID, task.State)
		}
	}
	if len(seen) < 4 {
		t.Fatalf("observer saw %d updates: %v", len(seen), seen)
	}
	before := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	marshalGit(t, repo, "checkout", "--detach")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/marshal/run/pre-close"); got != before {
		t.Fatalf("checkpoint = %s, want the target's pre-close commit %s", got, before)
	}
}

func TestM09CloseRefusesDirtyCheckedOutTargetWithoutChangingRefOrWorktree(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil {
		t.Fatal(err)
	}
	// Dirty tracked content must survive even when the integration does not
	// touch that file. Keep every original refusal/ref/worktree assertion.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("operator edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	file, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx, "run"); err == nil || !strings.Contains(err.Error(), "checked out at "+repo) || !strings.Contains(err.Error(), "switch it away") {
		t.Fatalf("close error = %v", err)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != before {
		t.Fatalf("target moved from %s to %s", before, got)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "README.md")); err != nil || string(got) != string(file) {
		t.Fatalf("worktree README changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("worktree gained a.txt: %v", err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("run state = %s, error = %v", run.State, err)
	}
}

// Under acceptance mode "marshal" the user's standing close authorization,
// granted at approval, lets Execute close the run; the gate still decides,
// and a real checkpoint records where the target was.
func TestM09ExecuteClosesUnderStandingAuthorization(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = marshal.AcceptMarshal
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	before := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "checkout", "--detach")
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Closed {
		t.Fatalf("state = %s, want closed", run.State)
	}
	if marshalGit(t, repo, "rev-parse", "refs/heads/main") != marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration") {
		t.Fatal("target was not fast-forwarded to the integration branch")
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/marshal/run/pre-close"); got != before {
		t.Fatalf("checkpoint = %s, want %s", got, before)
	}
}

// Merging a hand-in must not run hooks the repository or a worker set up.
func TestM09MergeDoesNotRunRepositoryHooks(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	hooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	for _, name := range []string{"pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-merge", "post-commit", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte("#!/bin/sh\necho "+name+" >> '"+marker+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marshalGit(t, repo, "config", "core.hooksPath", hooks)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if ran, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("repository hooks ran during the Marshal run: %q", ran)
	}
}

func TestM09ExecuteNeedsBrief(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.Execute(context.Background(), "run", nil, nil); err == nil {
		t.Fatal("Execute without a brief must fail")
	}
}

func TestMarshalBriefContextRecallsProjectMemoryWithProvenanceAndExcludesSharedChannel(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	now := time.Now().UTC()

	// 1. Write a project-scoped memory record.
	projectRec := model.MemoryRecordV2{
		ID:         "MEM-project-1",
		ProjectID:  s.ProjectID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityVerified,
		Title:      "Architecture Decision",
		Body:       "bounded project memory content",
		Scope:      string(model.ScopeProject),
		ScopeID:    s.ProjectID,
		Source:     model.MemorySource{Kind: "git", Reference: "abc1234", AgentID: "codex", SessionID: "sess-1"},
		ObservedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
	}
	if err := s.Store.WriteMemoryV2(ctx, projectRec); err != nil {
		t.Fatalf("WriteMemoryV2 project: %v", err)
	}

	// 2. Write a session-scoped record and a shared_channel record.
	sessionRec := model.MemoryRecordV2{
		ID:         "MEM-session-1",
		ProjectID:  s.ProjectID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityAgent,
		Title:      "Session Discussion",
		Body:       "cross-agent shared channel chat",
		Scope:      string(model.ScopeSession),
		ScopeID:    "sess-peer",
		Source:     model.MemorySource{Kind: "shared_channel", AgentID: "claude", SessionID: "sess-peer"},
		ObservedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
	}
	if err := s.Store.WriteMemoryV2(ctx, sessionRec); err != nil {
		t.Fatalf("WriteMemoryV2 session: %v", err)
	}

	// Approval promotes imported history to project scope, but workers must
	// still receive only task context. Keep the original exact count check.
	imported := projectRec
	imported.ID = "MEM-IMPORT-approved"
	imported.Source = model.MemorySource{Kind: "external", Reference: "earlier-session"}
	imported.Body = "private earlier conversation"
	if err := s.Store.WriteMemoryV2(ctx, imported); err != nil {
		t.Fatal(err)
	}

	run, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}

	bc, err := s.briefContext(ctx, "run", run, run.Tasks[0])
	if err != nil {
		t.Fatalf("briefContext: %v", err)
	}

	if len(bc.Memory) != 1 {
		t.Fatalf("expected 1 project memory record in BriefContext, got %d", len(bc.Memory))
	}
	if bc.Memory[0].ID != "MEM-project-1" {
		t.Fatalf("expected MEM-project-1, got %s", bc.Memory[0].ID)
	}
	if bc.Memory[0].Body != "bounded project memory content" {
		t.Fatalf("expected project body, got %q", bc.Memory[0].Body)
	}
	for _, m := range bc.Memory {
		if m.Scope == string(model.ScopeSession) || m.Source.Kind == "shared_channel" {
			t.Fatalf("BriefContext contains session or shared_channel memory: %+v", m)
		}
	}
}

func TestMarshalCloseAdvancesCleanMainWorktree(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil {
		t.Fatal(err)
	}
	before := marshalGit(t, repo, "rev-parse", "HEAD")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	head := marshalGit(t, repo, "rev-parse", "marshal/run/integration")
	if got := marshalGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
	if got := marshalGit(t, repo, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatal(got)
	}
	if got := marshalGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("dirty after close: %s", got)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "a.txt")); err != nil || string(got) != "done\n" {
		t.Fatalf("delivered file: %q %v", got, err)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/marshal/run/pre-close"); got != before {
		t.Fatalf("checkpoint = %s, want %s", got, before)
	}
}

func TestMarshalReassignmentSelectsModeCompatibleDriver(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			s.Drivers["agy"] = driver.Codex("false")
			s.Drivers["claude"] = driver.Codex("false")
			s.GovernedDrivers = map[string]driver.Driver{"worker": s.Drivers["worker"]}
			if available {
				s.GovernedDrivers["claude"] = s.Drivers["worker"]
			}
			ctx := t.Context()
			if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			run, rev, err := s.load(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			run.Tasks[0].State = marshal.Reassigned
			run.Tasks[0].ReturnsByAgent = map[string]int{"worker": 2}
			if err := s.save(ctx, "run", run, rev); err != nil {
				t.Fatal(err)
			}
			launched, err := s.dispatchReady(ctx, "run", run, marshalBrief)
			if available {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "no other worker supports governed mode") {
				t.Fatalf("missing mode-specific operator message: %v", err)
			}
			for _, d := range launched {
				if _, err := s.CollectHandIn(ctx, "run", d); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Snapshot(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			if available {
				if got.Tasks[0].Worker != "claude" || len(launched) != 1 {
					t.Fatalf("wrong reassignment: %+v launched %d", got.Tasks[0], len(launched))
				}
			} else if got.State != marshal.AwaitingUser || got.Tasks[0].State != marshal.Escalated || len(launched) != 0 || got.Tasks[0].Worker != "worker" {
				t.Fatalf("no compatible driver must stop: %+v", got)
			}
		})
	}
}

func TestMarshalCloseRefusesOtherUnsafeCheckouts(t *testing.T) {
	for _, kind := range []string{"staged", "untracked", "linked", "diverged", "verification", "ignored"} {
		t.Run(kind, func(t *testing.T) {
			s, repo := marshalFixture(t, 1)
			ctx := t.Context()
			if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil {
				t.Fatal(err)
			}
			checkout := repo
			switch kind {
			case "staged", "untracked", "diverged":
				if err := os.WriteFile(filepath.Join(repo, "operator.txt"), []byte("keep me"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind != "untracked" {
					marshalGit(t, repo, "add", "operator.txt")
				}
				if kind == "diverged" {
					marshalGit(t, repo, "commit", "-m", "operator advance")
				}
			case "ignored":
				if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("a.txt\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("keep ignored content"), 0600); err != nil {
					t.Fatal(err)
				}
			case "linked":
				marshalGit(t, repo, "switch", "--detach")
				checkout = filepath.Join(t.TempDir(), "linked")
				marshalGit(t, repo, "worktree", "add", checkout, "main")
			case "verification":
				s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
					return verification.Session{}, verification.Binding{}, fmt.Errorf("verification refused")
				}
			}
			before := marshalGit(t, checkout, "rev-parse", "HEAD")
			status := marshalGit(t, checkout, "status", "--porcelain")
			if err := s.Close(ctx, "run"); err == nil {
				t.Fatal("unsafe close succeeded")
			} else if kind == "linked" || kind == "staged" || kind == "untracked" {
				if !strings.Contains(err.Error(), "checked out at "+checkout) || !strings.Contains(err.Error(), "switch it away") {
					t.Fatal(err)
				}
			}
			if got := marshalGit(t, checkout, "rev-parse", "HEAD"); got != before {
				t.Fatalf("HEAD changed: %s", got)
			}
			if got := marshalGit(t, checkout, "status", "--porcelain"); got != status {
				t.Fatalf("status changed: %s", got)
			}
			if got := marshalGit(t, repo, "rev-parse", "main"); got != before {
				t.Fatalf("target changed: %s", got)
			}
			if kind == "staged" || kind == "untracked" || kind == "diverged" {
				if got, err := os.ReadFile(filepath.Join(repo, "operator.txt")); err != nil || string(got) != "keep me" {
					t.Fatalf("operator content changed: %q %v", got, err)
				}
			}
			if kind == "ignored" {
				if got, err := os.ReadFile(filepath.Join(repo, "a.txt")); err != nil || string(got) != "keep ignored content" {
					t.Fatalf("ignored content changed: %q %v", got, err)
				}
			} else if _, err := os.Stat(filepath.Join(checkout, "a.txt")); !os.IsNotExist(err) {
				t.Fatalf("worktree gained result: %v", err)
			}
			run, err := s.Snapshot(ctx, "run")
			if err != nil || run.State != marshal.Verifying {
				t.Fatalf("run changed: %s %v", run.State, err)
			}
		})
	}
}

func TestMarshalReassignmentCannotUseNativeDriverForGovernedTask(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Reassigned
	if err := s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.Drivers["agy"] = driver.Codex("false")
	if err := s.Reassign(ctx, "run", "a", "agy"); err == nil || !strings.Contains(err.Error(), "does not support task mode governed") {
		t.Fatalf("wrong-mode reassignment: %v", err)
	}
	got, err := s.Snapshot(ctx, "run")
	if err != nil || got.Tasks[0].Worker != "worker" {
		t.Fatalf("worker changed: %+v %v", got.Tasks, err)
	}
}

func TestMarshalReassignmentSkipsAliasesAndKeepsNativeMode(t *testing.T) {
	s := &MarshalService{Drivers: map[string]driver.Driver{"agy": driver.Agy(""), "codex": driver.Codex("")}, GovernedDrivers: map[string]driver.Driver{"claude": driver.Governed{}, "claude-code": driver.Governed{}, "codex": driver.Governed{}}}
	if got := s.otherWorker("claude", marshal.Governed); got != "codex" {
		t.Fatalf("reassigned to same provider alias: %s", got)
	}
	if got := s.otherWorker("codex", marshal.Native); got != "agy" {
		t.Fatalf("native reassignment = %s", got)
	}
}

func TestMarshalReassignmentReportsUnsupportedModeToOperator(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Reassigned
	run.Tasks[0].ReturnsByAgent = map[string]int{"worker": 2}
	if err := s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.Drivers["agy"] = driver.Agy("")
	s.GovernedDrivers = map[string]driver.Driver{"worker": s.Drivers["worker"]}
	got, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err == nil || !strings.Contains(err.Error(), "no other worker supports governed mode") || !strings.Contains(err.Error(), "operator intervention required") {
		t.Fatalf("missing operator explanation: %v", err)
	}
	if got.State != marshal.AwaitingUser || got.Tasks[0].State != marshal.Escalated || got.Tasks[0].Worker != "worker" {
		t.Fatalf("unsafe mode/state: %+v", got)
	}
	decisions, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	last := decisions[len(decisions)-1]
	if !strings.Contains(fmt.Sprint(last.Data["reason"]), "no other worker supports governed mode") {
		t.Fatalf("missing durable reason: %+v", last)
	}
}

type ultraGate struct {
	enabled bool
}

func (g ultraGate) Capability(name string) bool { return name == marshal.CapabilityMarshal }
func (g ultraGate) ExecutionEnabled() bool      { return g.enabled }

func TestUltraCollectAndReviewAsSoonAsReady(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 2)
	s.Gate = ultraGate{enabled: true}
	s.ModelProvider = "test"
	s.CrossReview = func(_ context.Context, t marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		ref := "check:test -f " + t.PlanTaskID + ".txt"
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{ref}}, "test", nil
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "test", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}

	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.UltraConcurrency = 2
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	draft.Tasks[1].DependsOn = nil
	draft.Plan.Tasks[1].DependsOn = nil
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	if _, err := s.StartPlanning(ctx, "run-ultra", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-ultra"); err != nil {
		t.Fatal(err)
	}

	runRec, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run-ultra")
	if err != nil {
		t.Fatal(err)
	}
	runRec.Value.Process05Bound = false
	if _, err := s.Store.SetMarshalRun(ctx, s.ProjectID, "run-ultra", runRec.Value, runRec.Revision); err != nil {
		t.Fatal(err)
	}

	releaseA := make(chan struct{})
	bHandedIn := make(chan struct{})

	// Task a waits for releaseA
	// Task b completes immediately and signals bHandedIn
	s.Drivers["worker"] = driver.Governed{
		Provider: "worker",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			if req.Task.PlanTaskID == "a" {
				<-releaseA
				_ = os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("a\n"), 0600)
			} else {
				_ = os.WriteFile(filepath.Join(req.Worktree, "b.txt"), []byte("b\n"), 0600)
				close(bHandedIn)
			}
			return nil, nil
		},
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := s.Execute(ctx, "run-ultra", marshalBrief, nil)
		done <- execErr
	}()

	// Wait for b to finish
	select {
	case <-bHandedIn:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task b to finish")
	}

	// Wait for task b to be collected and reviewed while task a is still blocked
	var run marshal.Run
	for range 50 {
		time.Sleep(50 * time.Millisecond)
		r, _, err := s.load(ctx, "run-ultra")
		if err == nil {
			run = r
			if len(r.Tasks) == 2 && r.Tasks[1].State == marshal.Accepted {
				break
			}
		}
	}
	if len(run.Tasks) < 2 {
		t.Fatalf("expected 2 tasks in run, got: %+v", run)
	}
	if run.Tasks[1].State != marshal.Accepted {
		t.Fatalf("task b should be collected and reviewed as soon as ready; got state: %s, task 0 state: %s, run state: %s", run.Tasks[1].State, run.Tasks[0].State, run.State)
	}
	// Verify task a is still dispatched
	if run.Tasks[0].State != marshal.Dispatched {
		t.Fatalf("task a should still be dispatched, got: %s", run.Tasks[0].State)
	}

	// Release task a so it can complete
	close(releaseA)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("execute timed out after releasing task a")
	}

	// Both tasks should now be merged in plan order
	finalRun, _, err := s.load(ctx, "run-ultra")
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Tasks[0].State != marshal.Merged || finalRun.Tasks[1].State != marshal.Merged {
		t.Fatalf("both tasks should be merged: %+v", finalRun.Tasks)
	}
}
