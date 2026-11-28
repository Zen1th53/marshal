//go:build !governed

package app

import (
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
	if len(snapshot.Areas["web"]) != 1 {
		t.Fatalf("unimplemented child not rolled up: %#v", snapshot.Areas)
	}
}
