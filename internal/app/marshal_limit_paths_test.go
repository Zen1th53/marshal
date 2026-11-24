package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

func limitGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// A text diff over the hand-in cap is handed in by its verified base and
// result commits, with every changed path enumerated: scope is still checked
// against the whole commit, and the review cannot cite a diff it never saw.
func TestOversizedDiffKeepsFullScopeValidation(t *testing.T) {
	wt := t.TempDir()
	limitGit(t, wt, "init", "-q")
	limitGit(t, wt, "config", "user.name", "Test Author")
	limitGit(t, wt, "config", "user.email", "author@example.invalid")
	if err := os.WriteFile(filepath.Join(wt, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	limitGit(t, wt, "add", "README")
	limitGit(t, wt, "commit", "-q", "-m", "base")
	task := marshal.Task{PlanTaskID: "T1", Worker: "worker-1", BaseCommit: limitGit(t, wt, "rev-parse", "HEAD"), Files: []string{"big.txt"}}
	g := driver.Governed{Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "big.txt"), []byte(strings.Repeat("a line of text\n", (17<<20)/15)), 0o644); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("out of scope\n"), 0o644)
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	h, err := g.Launch(ctx, driver.Request{Task: task, Worktree: wt, Brief: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	handIn, err := g.Wait(ctx, h)
	if err != nil {
		t.Fatalf("an oversized diff stopped the hand-in: %v", err)
	}
	if handIn.Diff != "" || len(handIn.Limits) != 1 {
		t.Fatalf("diff %d bytes, limits %+v", len(handIn.Diff), handIn.Limits)
	}
	limit := handIn.Limits[0]
	if limit.Kind != "git_diff" || !limit.Limited || limit.ActualBytes <= limit.CapBytes || limit.EvidenceRef != "result:"+handIn.ResultCommit ||
		!strings.Contains(limit.Action, task.BaseCommit+" "+handIn.ResultCommit+" -- <path>") || strings.Contains(limit.Action, wt) {
		t.Fatalf("limit = %+v", limit)
	}
	// Scope covers the full commit: the out-of-scope file is still found.
	if err := marshal.ValidateHandIn(task, handIn); err == nil || !strings.Contains(err.Error(), "out of scope file: outside.txt") {
		t.Fatalf("scope check on an oversized hand-in: %v", err)
	}
	task.Files = append(task.Files, "outside.txt")
	if err := marshal.ValidateHandIn(task, handIn); err != nil {
		t.Fatalf("in-scope oversized hand-in refused: %v", err)
	}
	// The review rests on the commit and its files, never on a partial diff.
	if _, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"diff"}}, handIn); err == nil {
		t.Fatal("review cited a diff that was not handed in")
	}
	if refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"result:" + handIn.ResultCommit, "file:big.txt"}}, handIn); err != nil || len(refs) != 2 {
		t.Fatalf("commit and file evidence: %v %v", refs, err)
	}
}

// A final check whose required input cannot fit pauses for a split of the
// plan, not a plain retry, and the run notice carries the same limit.
func TestIrreducibleVerifierContextSuggestsSplit(t *testing.T) {
	s := approvedNoticeRun(t)
	rec, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	run := rec.Value
	run.State = marshal.Verifying
	if err := s.save(t.Context(), "run", run, rec.Revision); err != nil {
		t.Fatal(err)
	}
	cause := irreducibleVerifierInput(t)
	limit, ok := model.LimitOf(cause)
	if !ok || limit.Kind == "" || limit.ActualBytes <= limit.CapBytes || !strings.Contains(limit.Action, "Split") {
		t.Fatalf("verifier input refusal carries no split: %v %+v", cause, limit)
	}
	if err := s.pauseVerificationFailure(t.Context(), "run", fmt.Errorf("integrated result is not verified: %w", cause)); err == nil {
		t.Fatal("pause returned no error")
	}
	stored, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	p := stored.Pause
	if stored.State != marshal.AwaitingUser || p == nil || p.Code != marshal.CodeLimitExceeded || p.Action != marshal.PauseUsePlan || p.Limit == nil || *p.Limit != limit {
		t.Fatalf("pause: %s %+v", stored.State, p)
	}
	if len(p.Resolutions) != 1 || p.Resolutions[0] != limit.Action || strings.Contains(p.Resolutions[0], marshal.VerificationRetry) {
		t.Fatalf("next step: %q", p.Resolutions)
	}
	if !strings.Contains(p.Reason, limit.Subject) || !strings.Contains(p.Reason, fmt.Sprint(limit.CapBytes)) {
		t.Fatalf("reason does not name the offending input: %q", p.Reason)
	}
	notices := runNotices(t, s)
	if len(notices) != 1 || notices[0].EventID != p.NoticeID || notices[0].Status != marshal.CodeLimitExceeded {
		t.Fatalf("notices %+v", notices)
	}
	var payload RunNotice
	if err := json.Unmarshal([]byte(notices[0].PayloadJSON), &payload); err != nil || payload.Limit == nil || *payload.Limit != limit || payload.Next != limit.Action {
		t.Fatalf("notice payload %+v %v", payload, err)
	}
}

// A pack file at its cap is read; one byte over, the pack is refused whole
// with the file named, both sizes and a decomposition.
func TestPackFileLimitAtCap(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int{marshalPackFileLimit, marshalPackFileLimit + 1} {
		if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		text, err := readPackFile(dir, "note.md")
		if size == marshalPackFileLimit {
			if err != nil || len(text) != size {
				t.Fatalf("file at cap: %d bytes, %v", len(text), err)
			}
			continue
		}
		limit, ok := model.LimitOf(err)
		if !ok || limit.Kind != "pack_file" || !strings.Contains(limit.Subject, "note.md") || limit.ActualBytes != int64(size) || limit.CapBytes != marshalPackFileLimit || !strings.Contains(limit.Action, "split") {
			t.Fatalf("file over cap: %v %+v", err, limit)
		}
	}
}
