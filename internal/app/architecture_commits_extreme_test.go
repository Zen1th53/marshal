//go:build !governed

package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestArchitectureIsBoundToCommitsExactly(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := OpenWithOptions(ctx, repo.Path(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	dir := r.ProjectRoot()

	before := gitTest(t, dir, "rev-parse", "HEAD")
	if at, err := r.ArchitectureAtCommit(ctx, "HEAD", ""); err != nil || at.Bound {
		t.Fatalf("nothing recorded yet must say so, got %+v %v", at, err)
	}
	contract, _ := requiredAcceptedContextFixture(t, r)
	r.recordArchitectureCommit(ctx, dir, "", "accepted", "")
	r.recordArchitectureCommit(ctx, dir, "", "accepted", "") // recording twice changes nothing

	gitTest(t, dir, "commit", "--allow-empty", "-m", "one")
	gitTest(t, dir, "commit", "--allow-empty", "-m", "two")
	at, err := r.ArchitectureAtCommit(ctx, "HEAD", "")
	if err != nil || !at.Bound || at.Exact || at.Distance != 2 || at.BoundCommit != before || at.Contract.ID != contract.ID {
		t.Fatalf("HEAD two commits after acceptance: %+v %v", at, err)
	}
	if len(at.Bindings) != 1 || at.Bindings[0].Event != "accepted" {
		t.Fatalf("bindings = %+v", at.Bindings)
	}

	r.recordArchitectureCommit(ctx, dir, "", "merged", "fix")
	at, err = r.ArchitectureAtCommit(ctx, "HEAD", "core")
	if err != nil || !at.Exact || at.Bindings[0].Task != "fix" || len(at.Contract.Components) != 1 {
		t.Fatalf("merged HEAD: %+v %v", at, err)
	}
	if _, err := r.ArchitectureAtCommit(ctx, "HEAD", "nope"); err == nil {
		t.Fatal("an unknown component must be refused")
	}
	note := gitTest(t, dir, "notes", "--ref=marshal-architecture", "show", "HEAD")
	if !strings.Contains(note, `"event":"merged"`) || !strings.Contains(note, contract.ID) {
		t.Fatalf("git note = %s", note)
	}

	history, err := r.ArchitectureHistory(ctx, "core", 10)
	if err != nil || len(history) != 2 || history[0].Event != "merged" || history[1].Event != "accepted" || history[0].Component == nil {
		t.Fatalf("history = %+v %v", history, err)
	}
	for _, bad := range []string{"--output=x", "a..b", "HEAD;rm"} {
		if _, err := r.ArchitectureAtCommit(ctx, bad, ""); err == nil {
			t.Errorf("accepted commit argument %q", bad)
		}
	}
}
