package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// writePlanPack writes a pack where an interactive Marshal writes it.
func writePlanPack(t *testing.T, repo string, notes map[string]string) {
	t.Helper()
	dir := filepath.Join(repo, MarshalPackRelativePath)
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"REQUIREMENTS.md": "# Requirements\n- keep README.md unchanged\n", "00_INDEX.md": "| task | worker |\n"}
	for id, note := range notes {
		files[filepath.Join("tasks", id+".md")] = note
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// What the person agreed with the Marshal outlives the chat: it is kept with
// the run, bound by the approval and handed to the worker of each task.
func TestPlanPackTravelsFromChatToWorkerBrief(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	writePlanPack(t, repo, map[string]string{"a": "write a first", "b": "b follows a"})
	dir, err := s.TakePlanPack("run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, MarshalPackRelativePath)); !os.IsNotExist(err) {
		t.Fatalf("the pack was left where the next Marshal writes: %v", err)
	}
	pack, err := ReadPlanPack(dir, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Model.Draft(ctx, "write files")
	if err != nil {
		t.Fatal(err)
	}
	d.Pack = &pack
	if _, err := s.StartPlanningFromDraft(ctx, "run", "write files", d, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pack == nil || run.Pack.Digest != pack.Digest {
		t.Fatalf("approved run lost the pack: %+v", run.Pack)
	}
	without := run
	without.Pack = nil
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	if marshalApprovalDigest(p.ApprovalScopeDigest, without) == run.ApprovalScopeDigest {
		t.Fatal("the approval digest does not bind the pack")
	}
	bc, err := s.briefContext(ctx, "run", run, run.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	if bc.Note != "write a first" || !strings.Contains(bc.Requirements, "keep README.md unchanged") || bc.Index == "" {
		t.Fatalf("brief context lacks the pack: %+v", bc)
	}
}

// The person may correct the pack before approving; approval binds the text
// as it stands then, not as the Marshal first wrote it.
func TestApprovalBindsThePackAsThePersonLeftIt(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	writePlanPack(t, repo, map[string]string{"a": "note"})
	dir, err := s.TakePlanPack("run")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := ReadPlanPack(dir, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Model.Draft(ctx, "write files")
	if err != nil {
		t.Fatal(err)
	}
	d.Pack = &pack
	if _, err := s.StartPlanningFromDraft(ctx, "run", "write files", d, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "REQUIREMENTS.md"), []byte("edited by the person\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pack.Requirements != "edited by the person" || run.Pack.Digest == pack.Digest {
		t.Fatalf("approval did not bind the edited pack: %+v", run.Pack)
	}
	if err := os.Remove(filepath.Join(dir, "tasks", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refreshPlanPack("run", run); err == nil {
		t.Fatal("a pack that lost a task's note was accepted")
	}
}

func TestPlanPackRefusesWhatDoesNotMatchTheTasks(t *testing.T) {
	for name, mutate := range map[string]func(dir string) error{
		"missing note": func(dir string) error { return os.Remove(filepath.Join(dir, "tasks", "b.md")) },
		"note for no task": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "tasks", "c.md"), []byte("stray"), 0o600)
		},
		"empty requirements": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "REQUIREMENTS.md"), []byte(" \n"), 0o600)
		},
		"symlinked index": func(dir string) error {
			if err := os.Remove(filepath.Join(dir, "00_INDEX.md")); err != nil {
				return err
			}
			return os.Symlink("/etc/hostname", filepath.Join(dir, "00_INDEX.md"))
		},
		"oversized note": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "tasks", "a.md"), []byte(strings.Repeat("x", marshalPackFileLimit+1)), 0o600)
		},
	} {
		repo := t.TempDir()
		writePlanPack(t, repo, map[string]string{"a": "note a", "b": "note b"})
		dir := filepath.Join(repo, MarshalPackRelativePath)
		if err := mutate(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPlanPack(dir, []string{"a", "b"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTakePlanPackNeedsAPack(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.TakePlanPack("run"); err == nil || !strings.Contains(err.Error(), "wrote no plan pack") {
		t.Fatalf("missing pack: %v", err)
	}
}
