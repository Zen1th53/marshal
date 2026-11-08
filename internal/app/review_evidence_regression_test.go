package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestReviewCheckReferencesNormalizeEscapes(t *testing.T) {
	command := `test -f a.txt && test "$(cat a.txt)" = done`
	for _, escaped := range []string{command, strings.ReplaceAll(command, "&", `\u0026`), strings.ReplaceAll(command, "&", "&amp;"), strings.ReplaceAll(command, "&", "&#38;"), strings.ReplaceAll(command, "&", `\u0026amp;`)} {
		t.Run(escaped, func(t *testing.T) {
			h := marshal.HandIn{ResultCommit: "head", CheckResults: []marshal.CheckResult{{Command: command, ResultCommit: "head"}}}
			refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"check:" + escaped}}, h)
			if err != nil || !slices.Equal(refs, []string{"check:" + command}) {
				t.Fatalf("escaped check: %q %v", refs, err)
			}
		})
	}
	// Normalize the stored side too, but retain the actual stored reference.
	h := marshal.HandIn{ResultCommit: "head", CheckResults: []marshal.CheckResult{{Command: `true \u0026\u0026 true`, ResultCommit: "head"}}}
	refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"check:true && true"}}, h)
	if err != nil || !slices.Equal(refs, []string{`check:true \u0026\u0026 true`}) {
		t.Fatalf("stored escape: %q %v", refs, err)
	}
}

func TestReviewCheckDropsUnmatchedButRequiresResolvedArtifact(t *testing.T) {
	h := marshal.HandIn{ResultCommit: "head", Diff: "changes", CheckResults: []marshal.CheckResult{{Command: "true", ResultCommit: "old"}}}
	refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"diff", "check:invented", "check:true"}}, h)
	if err != nil || !slices.Equal(refs, []string{"diff"}) {
		t.Fatalf("unmatched checks stopped review: %q %v", refs, err)
	}
	for _, ref := range []string{"check:true", "check:invented", "result:wrong", "invented", "file:"} {
		if _, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{ref}}, h); err == nil {
			t.Errorf("unresolved-only review accepted: %s", ref)
		}
	}
}

func TestDroppedReviewCheckNoteIsRecorded(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.Gate = marshalTestGate(true)
	s.ModelProvider = "model"
	startIntegrityRun(t, s, marshal.Budget{})
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"diff", "check:invented"}}, "other", nil
	}
	verdict, err := s.Review(t.Context(), "run", "a", knownCharge())
	if err != nil || verdict != marshal.VerdictAccept {
		t.Fatalf("review stopped: %s %v", verdict, err)
	}
	stored, err := s.Store.GetMarshalReview(t.Context(), "run", "a", 1)
	if err != nil || stored.Value.Independent == nil || !strings.Contains(strings.Join(stored.Value.Independent.Reasons, " "), "check:invented") {
		t.Fatalf("dropped reference note lost: %+v %v", stored, err)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range decisions {
		if d.Data["cross_review_reference_notes"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("no durable dropped-reference note")
	}
}
