package learning

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func entry(o Outcome) Entry {
	return Entry{
		ProjectID: "p1", GoalID: "g1", GoalRevision: 1,
		PlanID: "pl1", PlanVersion: 1, RunID: "r1", RunVersion: 1,
		VerificationID: "v1", VerificationVersion: 1,
		AttestationDigest: "att", EvidenceDigest: "bundle",
		TreeDigest: "tree", EnvironmentDigest: "env",
		Outcome: o,
	}
}

func ev(id, cluster string) EvidenceRef {
	return EvidenceRef{ID: id, ClusterID: cluster, Digest: "d" + id, Kind: "test"}
}

func projectItem() Item {
	return Item{
		ID: "i1", Claim: "the build command is make test", Scope: ScopeProject,
		ProjectID: "p1", State: model.ClaimStateSupported,
		Evidence:   []EvidenceRef{ev("e1", "c1")},
		Provenance: "process07", Binding: entry(OutcomeVerifiedComplete),
	}
}

func TestPromotionGatesFailClosed(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)

	tests := []struct {
		name   string
		mutate func(*PromotionInput)
		want   error
	}{
		{"unbound entry", func(in *PromotionInput) { in.Item.Binding.AttestationDigest = "" }, ErrInvalid},
		{"no evidence", func(in *PromotionInput) { in.Item.Evidence = nil }, ErrNotPromotable},
		{"unsupported state", func(in *PromotionInput) { in.Item.State = model.ClaimStateUnsupported }, ErrNotPromotable},
		{"invalidated state", func(in *PromotionInput) { in.Item.State = model.ClaimStateInvalidated }, ErrNotPromotable},
		{"stale candidate", func(in *PromotionInput) { in.Item.ExpiresAt = &past }, ErrNotPromotable},
		{"secret in claim", func(in *PromotionInput) { in.Item.Claim = "use token=ghp_abc123 to push" }, ErrSecretMaterial},
		{"project scope without project", func(in *PromotionInput) { in.Item.ProjectID = "" }, ErrInvalid},
		{"project scope bound elsewhere", func(in *PromotionInput) { in.Item.ProjectID = "other" }, ErrBindingMismatch},
		{"verified claim from failed run", func(in *PromotionInput) {
			in.Item.State = model.ClaimStateVerified
			in.Item.Binding = entry(OutcomeFailed)
		}, ErrNotPromotable},
		{"critical not verified", func(in *PromotionInput) {
			in.Item.Critical = true
			in.Item.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c2")}
			in.Replayable = true
		}, ErrNotPromotable},
		{"critical from one cluster", func(in *PromotionInput) {
			in.Item.Critical = true
			in.Item.State = model.ClaimStateVerified
			in.Item.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c1")}
			in.Replayable = true
		}, ErrNotPromotable},
		{"critical not replayable", func(in *PromotionInput) {
			in.Item.Critical = true
			in.Item.State = model.ClaimStateVerified
			in.Item.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c2")}
			in.Replayable = false
		}, ErrNotPromotable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := PromotionInput{Item: projectItem()}
			tt.mutate(&in)
			if _, err := Promote(in, now); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}

	// The valid project-local candidate promotes.
	if _, err := Promote(PromotionInput{Item: projectItem()}, now); err != nil {
		t.Fatalf("valid candidate: %v", err)
	}
}

// Generalizing a project fact into universal truth is the failure mode this
// guards: it needs independent corroboration and explicit bounds.
func TestGeneralScopeNeedsIndependentCorroboration(t *testing.T) {
	now := time.Now().UTC()

	base := projectItem()
	base.Scope = ScopeGeneral
	base.ProjectID = ""
	base.Applicability = []string{"harness>=2.0"}

	// One cluster, one observation: not generalizable.
	if _, err := Promote(PromotionInput{Item: base, IndependentObservations: 1}, now); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("single-source generalization accepted: %v", err)
	}

	// Two clusters but no applicability bounds: still refused.
	noBounds := base
	noBounds.Applicability = nil
	noBounds.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c2")}
	if _, err := Promote(PromotionInput{Item: noBounds, IndependentObservations: 2}, now); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("unbounded generalization accepted: %v", err)
	}

	// Independent clusters plus explicit bounds: promotable.
	ok := base
	ok.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c2")}
	if _, err := Promote(PromotionInput{Item: ok, IndependentObservations: 2}, now); err != nil {
		t.Fatalf("corroborated generalization refused: %v", err)
	}
}

// Repeating one source many times must not look like corroboration.
func TestRepetitionOfOneSourceIsNotIndependence(t *testing.T) {
	now := time.Now().UTC()
	it := projectItem()
	it.Scope = ScopeGeneral
	it.ProjectID = ""
	it.Applicability = []string{"any"}
	// Five pieces of evidence, all from the same cluster.
	it.Evidence = []EvidenceRef{ev("e1", "c1"), ev("e2", "c1"), ev("e3", "c1"), ev("e4", "c1"), ev("e5", "c1")}

	if _, err := Promote(PromotionInput{Item: it, IndependentObservations: 5}, now); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("repetition promoted a claim: %v", err)
	}
}

func TestRevisionKeepsHistoryAndDemandsEvidence(t *testing.T) {
	now := time.Now().UTC()
	prev := projectItem()
	prev.Version = 3

	// Strengthening SUPPORTED -> VERIFIED without evidence is refused.
	up := prev
	up.State = model.ClaimStateVerified
	up.Evidence = nil
	if _, err := Revise(prev, up, now); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("evidence-free strengthening accepted: %v", err)
	}

	// Retracting to INVALIDATED needs no new evidence.
	down := prev
	down.State = model.ClaimStateInvalidated
	down.Evidence = nil
	got, err := Revise(prev, down, now)
	if err != nil {
		t.Fatalf("retraction refused: %v", err)
	}
	if got.Version != prev.Version+1 {
		t.Fatalf("version %d, want %d", got.Version, prev.Version+1)
	}
	if prev.State != model.ClaimStateSupported {
		t.Fatal("previous version was mutated")
	}
}

// Only knowledge whose dependency actually changed goes stale.
func TestDependencyInvalidationIsTargeted(t *testing.T) {
	now := time.Now().UTC()
	a := projectItem()
	a.ID = "a"
	a.Dependencies = []Dependency{{Kind: "tool", ID: "go", Version: "1.27"}}
	b := projectItem()
	b.ID = "b"
	b.Dependencies = []Dependency{{Kind: "tool", ID: "python", Version: "3.13"}}

	out := Invalidate([]Item{a, b}, []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}}, now)

	if out[0].State != model.ClaimStateStale {
		t.Fatalf("dependent item not stale: %s", out[0].State)
	}
	if out[1].State != model.ClaimStateSupported {
		t.Fatalf("unrelated item disturbed: %s", out[1].State)
	}
}

func TestCommitDigestDetectsTamperAndRefusesSelfActivation(t *testing.T) {
	now := time.Now().UTC()
	c := Commit{
		ID: "m1", Binding: entry(OutcomeVerifiedComplete), Provenance: "process07",
		Additions: []Item{projectItem()},
	}
	built, err := NewCommit(c, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := built.Verify(); err != nil {
		t.Fatalf("fresh commit: %v", err)
	}

	tampered := built
	tampered.Additions[0].Claim = "forged"
	if err := tampered.Verify(); !errors.Is(err, ErrTampered) {
		t.Fatalf("tamper undetected: %v", err)
	}

	active := c
	active.Playbooks = []PlaybookCandidate{{ID: "pb", Title: "x", Active: true}}
	if _, err := NewCommit(active, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self-activating playbook accepted: %v", err)
	}

	// An addition bound to a different verification is refused.
	crossed := c
	other := projectItem()
	other.Binding.VerificationVersion = 99
	crossed.Additions = []Item{other}
	if _, err := NewCommit(crossed, now); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-bound addition accepted: %v", err)
	}
}
