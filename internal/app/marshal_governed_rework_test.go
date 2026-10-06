package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// Exercise the real collection boundary using a pinned imported result, so no
// provider process or sandbox is needed to test prior-head authorization.
func TestMarshalGovernedCollectionAcceptsOnlyRecordedPriorHead(t *testing.T) {
	for _, state := range []string{"base", "new", "recorded", "foreign", "unrecorded", "older", "dirty", "gap"} {
		t.Run(state, func(t *testing.T) {
			s, repo := marshalFixture(t, 1)
			ctx := t.Context()
			base := marshalGit(t, repo, "rev-parse", "HEAD")
			dir := filepath.Join(t.TempDir(), "task")
			marshalGit(t, repo, "worktree", "add", "--detach", dir, base)
			commit := func(text string) string {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				marshalGit(t, dir, "add", "a.txt")
				marshalGit(t, dir, "commit", "-m", text)
				return marshalGit(t, dir, "rev-parse", "HEAD")
			}
			older := commit("older")
			previous := commit("previous")
			head := commit("new")
			foreign := commit("foreign")
			// Bind the source exactly as an approved imported CLI result.
			r := &Runtime{store: s.Store, layout: project.Layout{Root: repo, Worktrees: s.Worktrees}}
			if _, err := r.ImportTasks(ctx, []model.Task{{ID: "TASK-source", Title: "write a.txt", Status: model.TaskReview, Risk: model.R1, BaseCommit: &base, HeadCommit: &head}}); err != nil {
				t.Fatal(err)
			}
			source, err := r.Task(ctx, "TASK-source")
			if err != nil {
				t.Fatal(err)
			}
			draft := s.Model.(marshalFakeModel).draft
			draft.Tasks[0].ImportedResult = &marshal.ImportedResult{TaskID: source.ID, Revision: source.Revision, BaseCommit: base, ResultCommit: head}
			if _, err := s.StartPlanningFromDraft(ctx, "run", "write file", draft, marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			run, rev, err := s.load(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			task := &run.Tasks[0]
			task.ResultCommit = previous
			task.ReturnsByAgent = map[string]int{task.Worker: 2}
			if state == "gap" {
				task.ReturnsByAgent[task.Worker] = 3
			}
			for attempt, result := range []string{older, previous} {
				if state == "unrecorded" && attempt == 1 {
					continue
				}
				if _, err := s.Store.SetMarshalHandIn(ctx, "run", task.PlanTaskID, attempt+1, marshal.HandIn{Worker: task.Worker, Mode: task.Mode, BaseCommit: base, ResultCommit: result}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.save(ctx, "run", run, rev); err != nil {
				t.Fatal(err)
			}
			current := map[string]string{"base": base, "new": head, "recorded": previous, "foreign": foreign, "unrecorded": previous, "older": older, "dirty": previous, "gap": previous}[state]
			marshalGit(t, dir, "reset", "--hard", current)
			if state == "dirty" {
				if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("operator edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = r.marshalGovernedRun(s)(ctx, driver.Request{RunID: "run", Task: *task, Worktree: dir})
			wantSuccess := state == "base" || state == "new" || state == "recorded" || state == "gap"
			if wantSuccess {
				if err != nil {
					t.Fatal(err)
				}
				if got := marshalGit(t, dir, "rev-parse", "HEAD"); got != head {
					t.Fatalf("result = %s, want %s", got, head)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "before import") {
					t.Fatalf("unauthorized state accepted: %v", err)
				}
				if got := marshalGit(t, dir, "rev-parse", "HEAD"); got != current {
					t.Fatalf("ref moved after refusal: %s", got)
				}
				if state == "dirty" {
					if got, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(got) != "operator edit" {
						t.Fatalf("dirty file changed: %q %v", got, err)
					}
				}
			}
		})
	}
}
