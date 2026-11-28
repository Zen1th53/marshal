//go:build !governed

package app

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/architectureworkspace"
)

func TestArchitectureWorkspaceRetainsAdvisoryAndReviewerFindings(t *testing.T) {
	r, _ := conformanceRuntime(t, false)
	snapshot, err := r.ArchitectureWorkspace(t.Context(), architectureworkspace.Reviewer([]string{"internal/store/s.go:store: boundary mismatch"})...)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Layouts) == 0 {
		t.Fatal("accepted diagram missing")
	}
	api := snapshot.Findings["api"]
	if len(api) != 1 || api[0].Source != "conformance" {
		t.Fatalf("advisory conformance missing: %#v", api)
	}
	store := snapshot.Findings["store"]
	if len(store) != 1 || store[0].Source != "reviewer" {
		t.Fatalf("reviewer finding missing: %#v", store)
	}
	var child *architectureworkspace.Finding
	for _, finding := range snapshot.Findings["login"] {
		if finding.Source == "conformance" && finding.Title == "No tracked implementation files for this component" {
			child = &finding
			break
		}
	}
	if child == nil || !slices.Contains(snapshot.Members["web"], "login") || !slices.Contains(snapshot.Areas["web"], *child) {
		t.Fatalf("unimplemented child not rolled up: %#v", snapshot.Areas)
	}
}

func TestArchitectureWorkspaceIgnoresMarshalState(t *testing.T) {
	r, dir := conformanceRuntime(t, false)
	before, err := r.ArchitectureWorkspace(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reportBefore, err := r.ArchitectureConformance(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, dir, ".marshal/runtime.go", "package state\n")
	gitTest(t, dir, "add", "-f", ".marshal/runtime.go")
	gitTest(t, dir, "commit", "-m", "tracked runtime state")
	after, err := r.ArchitectureWorkspace(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reportAfter, err := r.ArchitectureConformance(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	reportAfter.Commit = reportBefore.Commit
	if !reflect.DeepEqual(reportBefore, reportAfter) || !reflect.DeepEqual(before, after) {
		t.Fatalf("runtime state changed conformance or workspace: before=%+v after=%+v", reportBefore, reportAfter)
	}
}
