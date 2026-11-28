package architectureworkspace

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/archconform"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func fixture() projectcontext.Contract {
	return projectcontext.Contract{FormatVersion: 1, Revision: 1, Project: projectcontext.ProjectBinding{CanonicalID: "repository", StorageID: "repository"}, Invariants: []string{"Preserve boundaries"}, ID: "system", Purpose: "Serve requests", Components: []projectcontext.Component{
		{ID: "service", Responsibility: "serve", Paths: []string{"src"}},
		{ID: "api", Parent: "service", Responsibility: "requests", Paths: []string{"src/api"}, Dependencies: []string{"data"}},
		{ID: "data", Parent: "service", Responsibility: "storage", Paths: []string{"src/data"}},
	}, Connections: []projectcontext.Connection{{From: "api", To: "data", Carries: "queries"}}}
}
func TestProjectionAnchorsAndRollups(t *testing.T) {
	c := fixture()
	edge := projectcontext.ContractChanges(c, c).Connections[0].ID()
	file := Finding{Title: "file issue", Evidence: "src/api/main.go:12", Source: "reviewer"}
	observations := []Finding{file, file, {Title: "edge issue", Edge: edge}, {Title: "area issue", Area: "service"}, {Title: "unknown", Evidence: "elsewhere.go"}}
	got, err := Project(c, observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Findings["api"]) != 1 || len(got.Findings[edge]) != 1 || len(got.Findings[""]) != 1 {
		t.Fatalf("anchors: %#v", got.Findings)
	}
	if len(got.Areas["service"]) != 3 {
		t.Fatalf("rollup double counts: %#v", got.Areas)
	}
	if len(got.Layouts) != 2 {
		t.Fatalf("missing hierarchy views: %#v", got.Layouts)
	}
}
func TestConformanceAndReviewer(t *testing.T) {
	findings := Conformance([]archconform.Finding{{Kind: "outside_task", Part: "data", File: "src/data/a.go"}})
	findings = append(findings, Reviewer([]string{"src/api/a.go:api: wrong boundary"})...)
	got, err := Project(fixture(), findings)
	if err != nil {
		t.Fatal(err)
	}
	if got.Findings["data"][0].Source != "conformance" || got.Findings["api"][0].Source != "reviewer" {
		t.Fatal(got)
	}
}

func TestLegacyContractDigestGolden(t *testing.T) {
	c := fixture()
	before, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if before != "sha256:aeb76599b716b9767065cfe738ca7a3742fb270da905644fd5413e139cc18a50" {
		t.Fatalf("legacy digest: %s", before)
	}
	if _, err := Project(c, []Finding{{Title: "issue", Part: "api"}}); err != nil {
		t.Fatal(err)
	}
	after, err := c.CanonicalDigest()
	if err != nil || after != before {
		t.Fatal("projection mutated contract", after, err)
	}
}

func TestReviewerLineEvidenceAndMissingPart(t *testing.T) {
	observations := Reviewer([]string{"src/api/a.go:12:api: boundary mismatch", "src/data/a.go:missing: another mismatch"})
	got, err := Project(fixture(), observations)
	if err != nil {
		t.Fatal(err)
	}
	if got.Findings["api"][0].Evidence != "src/api/a.go:12" {
		t.Fatal(got)
	}
	if len(got.Findings["data"]) != 1 {
		t.Fatal("file ownership was lost", got)
	}
}
