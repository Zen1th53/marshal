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
