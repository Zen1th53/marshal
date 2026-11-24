//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func writeRepoFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func conformanceRuntime(t *testing.T, withDeps bool) (*Runtime, string) {
	t.Helper()
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
	first, _ := requiredAcceptedContextFixture(t, r)
	next := first
	next.Components = []projectcontext.Component{
		{ID: "api", Responsibility: "HTTP", Paths: []string{"internal/api"}},
		{ID: "store", Responsibility: "Data", Paths: []string{"internal/store"}},
		{ID: "web", Responsibility: "Pages", Paths: []string{"web"}},
		{ID: "login", Responsibility: "Sign in", Paths: []string{"web/login"}, Parent: "web"},
	}
	if withDeps {
		next.Components[0].Dependencies = []string{"store"}
	}
	// Splitting the original core into named parts is an explicit removal,
	// so the owner can see the old part disappearing in this revision.
	removed := first.Components[0]
	removed.Removed = true
	next.Components = append(next.Components, removed)
	acceptWatchContract(t, r, next, "conformance")
	dir := r.ProjectRoot()
	writeRepoFile(t, dir, "go.mod", "module example.com/shop\n\ngo 1.26\n")
	writeRepoFile(t, dir, "internal/api/h.go", "package api\nimport \"example.com/shop/internal/store\"\nvar _ = store.X\n")
	writeRepoFile(t, dir, "internal/store/s.go", "package store\nvar X = 1\n")
	gitTest(t, dir, "add", "-A")
	gitTest(t, dir, "commit", "-m", "base")
	return r, dir
}

func TestHandInThatUsesAnUndeclaredPartIsReturned(t *testing.T) {
	r, dir := conformanceRuntime(t, true)
	s := &MarshalService{runtime: r, Repository: dir}
	writeRepoFile(t, dir, "internal/store/s.go", "package store\nimport \"example.com/shop/internal/api\"\nvar X = 1\nvar _ = api.Y\n")
	gitTest(t, dir, "commit", "-am", "store uses api")
	head := gitTest(t, dir, "rev-parse", "HEAD")
	task := marshal.Task{Components: []string{"store"}}
	err := s.architectureHandIn(t.Context(), task, marshal.HandIn{ResultCommit: head, FilesTouched: []string{"internal/store/s.go", "go.mod"}})
	if err == nil || !strings.Contains(err.Error(), "part store uses part api") || !strings.Contains(err.Error(), "owner's Accept") {
		t.Fatalf("err = %v", err)
	}
	// Writing another part's file is outside the task.
	err = s.architectureHandIn(t.Context(), marshal.Task{Components: []string{"login"}}, marshal.HandIn{ResultCommit: head, FilesTouched: []string{"internal/api/h.go"}})
	if err == nil || !strings.Contains(err.Error(), "outside this task's parts (login)") {
		t.Fatalf("err = %v", err)
	}
	// A declared dependency inside the task's own part passes.
	if err := s.architectureHandIn(t.Context(), marshal.Task{Components: []string{"api"}}, marshal.HandIn{ResultCommit: head, FilesTouched: []string{"internal/api/h.go"}}); err != nil {
		t.Fatalf("declared use refused: %v", err)
	}
}

func TestUndeclaredUseIsReportedButNotReturnedWithoutAnyDeclaredDependency(t *testing.T) {
	r, dir := conformanceRuntime(t, false)
	s := &MarshalService{runtime: r, Repository: dir}
	head := gitTest(t, dir, "rev-parse", "HEAD")
	if err := s.architectureHandIn(t.Context(), marshal.Task{Components: []string{"api"}}, marshal.HandIn{ResultCommit: head, FilesTouched: []string{"internal/api/h.go"}}); err != nil {
		t.Fatalf("a contract with no dependencies must not return tasks: %v", err)
	}
	report, err := r.ArchitectureConformance(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if report.DeclaresDependencies || len(report.Findings) != 1 || report.Findings[0].Uses != "store" {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if strings.Join(report.UnimplementedParts, ",") != "login,web" {
		t.Fatalf("unimplemented = %v", report.UnimplementedParts)
	}
	if report.UnownedFiles == 0 || report.CheckedFiles != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestArchitectureNewUnownedSource(t *testing.T) {
	r, dir := conformanceRuntime(t, true)
	s := &MarshalService{runtime: r, Repository: dir}
	base := gitTest(t, dir, "rev-parse", "HEAD")
	files := []string{"new/service.go", "docs/example.go", "new/service_test.go", "tests/helper.py", "go.sum"}
	for _, f := range files {
		writeRepoFile(t, dir, f, "// example\n")
	}
	gitTest(t, dir, "add", "-A")
	gitTest(t, dir, "commit", "-m", "unowned files")
	head := gitTest(t, dir, "rev-parse", "HEAD")
	task := marshal.Task{BaseCommit: base, Components: []string{"api"}}
	for _, f := range files {
		err := s.architectureHandIn(t.Context(), task, marshal.HandIn{ResultCommit: head, FilesTouched: []string{f}})
		if (err != nil) != (f == "new/service.go") {
			t.Errorf("%s: %v", f, err)
		}
	}
	// An already present unowned production file stays advisory.
	task.BaseCommit = head
	writeRepoFile(t, dir, "new/service.go", "package service\nvar Changed = true\n")
	gitTest(t, dir, "commit", "-am", "modify existing unowned source")
	head = gitTest(t, dir, "rev-parse", "HEAD")
	if err := s.architectureHandIn(t.Context(), task, marshal.HandIn{ResultCommit: head, FilesTouched: []string{"new/service.go"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskArchitectureAcceptedSlice(t *testing.T) {
	r, dir := conformanceRuntime(t, true)
	s := &MarshalService{runtime: r, Repository: dir}
	brief, required := s.taskArchitecture(t.Context(), marshal.Task{Files: []string{"internal/api/h.go"}})
	for _, want := range []string{"ARCHITECTURE FOR THIS TASK", "HTTP", "internal/api", "store", "Dependencies"} {
		if !strings.Contains(brief, want) {
			t.Errorf("missing %q: %s", want, brief)
		}
	}
	if !required {
		t.Fatal("accepted architecture not required")
	}
}

func TestRestagedCandidateRequiresArchitectureReview(t *testing.T) {
	r, _ := conformanceRuntime(t, true)
	s, _ := integrationOrderFixture(t, 1, filepath.Join(t.TempDir(), "state.db"), nil, false)
	acceptTask(t, s, "a")
	s.runtime = r
	run := loadRun(t, s)
	run.Tasks[0].Checks = nil
	run.Tasks[0].Files = []string{"internal/api/h.go"}
	architecture, required := s.taskArchitecture(t.Context(), run.Tasks[0])
	if !required {
		t.Fatal("accepted architecture not required")
	}
	for _, tc := range []struct {
		name       string
		verdict    string
		mismatches []string
		wantError  bool
		wantReturn bool
	}{
		{name: "missing verdict", wantError: true},
		{name: "invalid verdict", verdict: "unknown", wantError: true},
		{name: "invalid mismatch", mismatches: []string{"unowned file"}, wantError: true},
		{name: "mismatch", verdict: "conforms", mismatches: []string{"internal/api/h.go:api: undeclared dependency"}, wantReturn: true},
		{name: "conforms", verdict: "conforms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &marshal.MergeIntent{Attempt: 1, ExpectedHead: "advanced-head", Candidate: "candidate", AcceptedEvidenceDigest: "previous-evidence"}
			called := false
			s.CrossReview = func(_ context.Context, task marshal.Task, h marshal.HandIn, control marshal.Control) (marshal.Review, string, error) {
				called = true
				if task.ArchitectureContext != architecture {
					t.Fatalf("architecture context = %q, want %q", task.ArchitectureContext, architecture)
				}
				if h.BaseCommit != in.ExpectedHead || h.ResultCommit != in.Candidate {
					t.Fatalf("review received wrong candidate: %+v", h)
				}
				return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "independent", EvidenceRefs: []string{"candidate-evidence"}, ArchitectureVerdict: tc.verdict, ArchitectureMismatches: tc.mismatches}, "test", nil
			}
			reason, err := s.revalidateMergeCandidate(t.Context(), "run", run, 0, in)
			if !called || (err != nil) != tc.wantError || (reason != "") != tc.wantReturn {
				t.Fatalf("called=%v reason=%q err=%v", called, reason, err)
			}
			if tc.wantError || tc.wantReturn {
				if in.AcceptedEvidenceDigest != "previous-evidence" {
					t.Fatal("rejected review replaced accepted evidence")
				}
			} else if in.AcceptedEvidenceDigest == "previous-evidence" {
				t.Fatal("conforming review did not update evidence")
			}
		})
	}
}
