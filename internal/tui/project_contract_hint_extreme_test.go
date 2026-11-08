//go:build !governed

package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestExplainProjectContractSubmissionNamesMissingFields(t *testing.T) {
	// The shape a Marshal guessed in the e2e run: no action, version or digest.
	sub := store.ProjectContractSubmission{Contract: projectcontext.Contract{ID: "CONTRACT-calc-1", Purpose: "calc", Components: []projectcontext.Component{{ID: "calc", Responsibility: "adds"}}, Invariants: []string{"tests unchanged"}}}
	err := explainProjectContractSubmission(sub, projectcontext.ErrInvalid)
	if !errors.Is(err, projectcontext.ErrInvalid) {
		t.Fatalf("explanation lost the refusal class: %v", err)
	}
	for _, want := range []string{`action must be "project-contract"`, "format_version must be 1", "revision must be expected_version+1", "paths", "reviewed_inventory_digest", `"action":"project-contract"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("explanation lacks %q: %v", want, err)
		}
	}
	if other := explainProjectContractSubmission(sub, model.ErrConflict); other != model.ErrConflict {
		t.Fatalf("non-shape refusal rewritten: %v", other)
	}
}
