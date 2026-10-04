package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
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

func TestM09CloseRefusesCheckedOutTargetWithoutChangingRefOrWorktree(t *testing.T) {
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
	marshalGit(t, repo, "-c", "core.hooksPath=/dev/null", "checkout", "--detach")
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
