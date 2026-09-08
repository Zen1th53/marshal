package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// p07Runtime opens a runtime and seeds one canonical Process 06 session with a
// stored completion attestation, which is the state Process 07 binds to.
func p07Runtime(t *testing.T, decision verification.Decision) (*Runtime, verification.Session) {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	now := time.Now().UTC()
	binding := verification.Binding{
		ProjectID: "PROJECT-p07", GoalID: "g", GoalRevision: 1,
		PlanID: "p", PlanVersion: 1, RunID: "r", RunVersion: 1,
		TreeDigest: "tree", EnvironmentDigest: "env",
	}
	// The session is built so that Evaluate genuinely reaches the requested
	// decision. Seeding a state the evidence does not support would test a
	// fiction: the attestation derives its decision from the record, not from
	// what the caller wrote in State.
	session := verification.Session{
		ID: "v-1", Version: 1, Binding: binding, State: decision,
		Criteria: []verification.Criterion{{ID: "c", Mandatory: true, ClaimIDs: []string{"cl-1"}}},
		Claims: []verification.Claim{{
			ID: "cl-1", CriterionID: "c",
			SemanticScope: []string{"internal/example"},
			EvidenceIDs:   []string{"ev-1"},
		}},
		Evidence: []verification.Evidence{{
			ID: "ev-1", ClaimID: "cl-1", Kind: "test", Status: verification.StatusPass,
			ContentDigest: "content", TreeDigest: binding.TreeDigest,
			EnvironmentDigest: binding.EnvironmentDigest,
			Producer:          "runner", Provider: "local", Oracle: "go test", ClusterID: "cluster-1",
			Attempts: 1, Passes: 1, CreatedAt: now,
		}},
		RequiredChecks: map[string]verification.Status{},
		CreatedAt:      now, UpdatedAt: now,
	}
	switch decision {
	case verification.VerifiedComplete:
		// The record above already satisfies every mandatory criterion.
	case verification.PartiallySatisfied:
		// A stated limitation is what makes an otherwise complete outcome
		// partial, so the run is honest about what it did not establish.
		session.Limitations = []string{"performance was not measured"}
	case verification.VerificationFailed:
		// Failing the required check is what turns the outcome into a failure.
		session.RequiredChecks["security"] = verification.StatusFail
	case verification.Blocked:
		session.KnownBlockers = []string{"provider credentials unavailable"}
	default:
		t.Fatalf("unsupported seeded decision %s", decision)
	}
	if got := verification.Evaluate(session, binding, now); got != decision {
		t.Fatalf("seeded session evaluates to %s, want %s", got, decision)
	}
	if err := runtime.store.CreateVerificationSession(ctx, session); err != nil {
		t.Fatalf("CreateVerificationSession: %v", err)
	}
	// The attestation is built by the canonical constructor, so the seeded
	// record is digest-bound exactly the way a real Process 06 run produces it.
	attestation, err := verification.NewCompletionAttestation("att-1", session, "bundle", "process-06", now)
	if err != nil {
		t.Fatalf("NewCompletionAttestation: %v", err)
	}
	if err := runtime.store.AppendCompletionAttestation(ctx, attestation); err != nil {
		t.Fatalf("AppendCompletionAttestation: %v", err)
	}
	return runtime, session
}

func p07Candidate(id, claim string) learning.PromotionInput {
	return learning.PromotionInput{
		Item: learning.Item{
			ID: id, Claim: claim, Scope: learning.ScopeProject, ProjectID: "PROJECT-p07",
			State: model.ClaimStateVerified, Version: 1,
			Evidence: []learning.EvidenceRef{
				{ID: id + "-e1", ClusterID: id + "-c1", Digest: "d1", Kind: "test"},
				{ID: id + "-e2", ClusterID: id + "-c2", Digest: "d2", Kind: "test"},
			},
			Provenance: "process-06 attestation",
		},
		Replayable: true,
	}
}

// The entry binding is read from canonical Process 06 state, never from the
// caller. A caller cannot name the attestation digest, the evidence digest or
// the outcome.
func TestLearningEntryComesFromCanonicalAttestation(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)

	entry, err := runtime.Learning().EntryFor(ctx, session.ID)
	if err != nil {
		t.Fatalf("EntryFor: %v", err)
	}
	if entry.Outcome != learning.OutcomeVerifiedComplete {
		t.Fatalf("outcome = %s, want VERIFIED_COMPLETE", entry.Outcome)
	}
	if entry.AttestationDigest == "" || entry.EvidenceDigest != "bundle" {
		t.Fatalf("entry did not take its digests from the attestation: %+v", entry)
	}
	if !entry.Valid() {
		t.Fatalf("entry is not a complete binding: %+v", entry)
	}
}

// Without a completion attestation there is nothing to bind to, so learning is
// blocked rather than guessed.
func TestLearningIsBlockedWithoutAnAttestation(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	now := time.Now().UTC()
	session := verification.Session{
		ID: "v-noattest", Version: 1,
		Binding: verification.Binding{
			ProjectID: "PROJECT-p07", GoalID: "g", GoalRevision: 1,
			PlanID: "p", PlanVersion: 1, RunID: "r", RunVersion: 1,
			TreeDigest: "tree", EnvironmentDigest: "env",
		},
		State: verification.Blocked, Criteria: []verification.Criterion{{ID: "c"}},
		RequiredChecks: map[string]verification.Status{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := runtime.store.CreateVerificationSession(ctx, session); err != nil {
		t.Fatalf("CreateVerificationSession: %v", err)
	}
	if _, err := runtime.Learning().EntryFor(ctx, session.ID); !errors.Is(err, learning.ErrInvalid) {
		t.Fatalf("EntryFor err = %v, want ErrInvalid for a missing attestation", err)
	}
}

// A candidate that fails a promotion gate is refused and recorded, not dropped
// and not weakened. The honest refusal is what becomes durable.
func TestLearningRecordsRefusalsInsteadOfWeakeningClaims(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)

	good := p07Candidate("m-good", "the build command is go build")
	noEvidence := p07Candidate("m-unsupported", "this refactor is always safe")
	noEvidence.Item.Evidence = nil
	secret := p07Candidate("m-secret", "password"+"="+"hunter2")

	got, err := runtime.Learning().Commit(ctx, CommitInput{
		ID: "mc-1", Verification: session.ID, Provenance: "test",
		Candidates: []learning.PromotionInput{good, noEvidence, secret},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(got.Additions) != 1 || got.Additions[0].ID != "m-good" {
		t.Fatalf("additions = %+v, want only the evidenced claim", got.Additions)
	}
	if len(got.BlockedLearning) != 2 {
		t.Fatalf("blocked = %+v, want both refusals recorded", got.BlockedLearning)
	}
	joined := strings.Join(got.BlockedLearning, " ")
	if !strings.Contains(joined, "m-unsupported") || !strings.Contains(joined, "m-secret") {
		t.Fatalf("refusals do not name what was refused: %+v", got.BlockedLearning)
	}
	// The secret must not appear in the stored refusal reason either.
	if strings.Contains(joined, "hunter2") {
		t.Fatal("a refusal reason leaked the secret it refused")
	}
}

// A run that only reached PARTIAL cannot produce verified memory.
func TestLearningCannotPromoteVerifiedFromPartialOutcome(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.PartiallySatisfied)

	got, err := runtime.Learning().Commit(ctx, CommitInput{
		ID: "mc-1", Verification: session.ID, Provenance: "test",
		Candidates: []learning.PromotionInput{p07Candidate("m-1", "the feature works")},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(got.Additions) != 0 {
		t.Fatalf("a verified claim was promoted from a PARTIAL outcome: %+v", got.Additions)
	}
	if len(got.BlockedLearning) != 1 {
		t.Fatalf("blocked = %+v, want the refusal recorded", got.BlockedLearning)
	}
	if got.Binding.Outcome != learning.OutcomePartial {
		t.Fatalf("outcome = %s, want PARTIAL", got.Binding.Outcome)
	}
}

// A failed run is still learned from: the outcome is ingested honestly.
func TestLearningIngestsFailedOutcomes(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerificationFailed)

	failure := p07Candidate("m-fail", "the migration times out over 10k rows")
	failure.Item.State = model.ClaimStateSupported

	got, err := runtime.Learning().Commit(ctx, CommitInput{
		ID: "mc-1", Verification: session.ID, Provenance: "postmortem",
		Candidates: []learning.PromotionInput{failure},
		Observations: []learning.RoutingObservation{{
			TaskClass: "migration", Provider: "codex", ProviderVersion: "0.9", Model: "m",
			Outcome: learning.OutcomeFailed, Selected: true, EvidenceID: "e", ClusterID: "c",
			Observed: time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got.Binding.Outcome != learning.OutcomeFailed {
		t.Fatalf("outcome = %s, want FAILED", got.Binding.Outcome)
	}
	if len(got.Additions) != 1 {
		t.Fatalf("a supported failure claim was not learned: %+v", got.BlockedLearning)
	}

	trust, err := runtime.Learning().Trust(ctx, "migration")
	if err != nil {
		t.Fatalf("Trust: %v", err)
	}
	for _, record := range trust {
		if record.Failed != 1 {
			t.Fatalf("trust = %+v, want the failure counted", record)
		}
	}
}

// The full lifecycle: a verified outcome becomes memory, a dependency change
// stales exactly the knowledge resting on it, and a later retrieval sees the
// stale item as unusable while unrelated memory stays current.
func TestLearningLifecycleFromVerifiedOutcomeToStaleMemory(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)
	service := runtime.Learning()

	onGo := p07Candidate("m-go", "the toolchain builds with go 1.28")
	onGo.Item.Dependencies = []learning.Dependency{{Kind: "tool", ID: "go", Version: "1.28"}}
	onDocs := p07Candidate("m-docs", "the docs live under docs/")

	first, err := service.Commit(ctx, CommitInput{
		ID: "mc-1", Verification: session.ID, Provenance: "process-06",
		Candidates: []learning.PromotionInput{onGo, onDocs},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(first.Additions) != 2 {
		t.Fatalf("additions = %d, want 2: %+v", len(first.Additions), first.BlockedLearning)
	}

	// Both claims are usable before anything changes.
	results, err := service.Search(ctx, learning.Query{ProjectID: "PROJECT-p07"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if !r.Usable {
			t.Fatalf("%s is not usable before any change", r.Item.ID)
		}
	}

	// The toolchain moves. Only the claim resting on it becomes stale.
	invalidated, err := service.Invalidate(ctx, "mc-2", session.ID, "toolchain upgrade",
		[]learning.Dependency{{Kind: "tool", ID: "go", Version: "1.29"}})
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if len(invalidated.Revisions) != 1 || invalidated.Revisions[0].ID != "m-go" {
		t.Fatalf("revisions = %+v, want only the go claim staled", invalidated.Revisions)
	}

	after, err := service.Search(ctx, learning.Query{ProjectID: "PROJECT-p07", IncludeStale: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	byID := map[string]learning.Result{}
	for _, r := range after {
		byID[r.Item.ID] = r
	}
	if byID["m-go"].Usable {
		t.Fatal("stale memory is still usable after its dependency changed")
	}
	if !byID["m-docs"].Usable {
		t.Fatal("unrelated memory was staled by an unrelated dependency change")
	}

	// History keeps both versions, so what was believed before stays visible.
	history, err := service.History(ctx, "m-go")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d versions, want 2", len(history))
	}
	if history[0].State != model.ClaimStateVerified || history[1].State != model.ClaimStateStale {
		t.Fatalf("history = %s,%s, want VERIFIED then STALE", history[0].State, history[1].State)
	}

	// A later Goal re-injects memory: constraints stay binding, the fresh
	// claim is advisory, and the stale claim is not carried forward as fact.
	context07, err := service.Context(ctx, []string{"never touch the security package"},
		learning.Query{ProjectID: "PROJECT-p07"})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	var constraints, advisory int
	for _, in := range context07 {
		switch in.Kind {
		case learning.InjectionConstraint:
			constraints++
			if !in.Binding {
				t.Fatal("a canonical constraint was re-injected as advisory")
			}
		case learning.InjectionAdvisory:
			advisory++
			if in.Binding {
				t.Fatal("learned memory was re-injected as binding")
			}
			if in.ItemID == "m-go" {
				t.Fatal("stale memory was re-injected as fact")
			}
		}
	}
	if constraints != 1 || advisory != 1 {
		t.Fatalf("context = %d constraints and %d advisory, want 1 and 1", constraints, advisory)
	}
}

// Export is secret-safe and integrity-checked, and a restore never overwrites
// newer canonical memory.
func TestLearningExportAndRestoreAreSafe(t *testing.T) {
	ctx := context.Background()
	runtime, session := p07Runtime(t, verification.VerifiedComplete)
	service := runtime.Learning()

	if _, err := service.Commit(ctx, CommitInput{
		ID: "mc-1", Verification: session.ID, Provenance: "process-06",
		Candidates: []learning.PromotionInput{p07Candidate("m-1", "the build command is go build")},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	bundle, err := service.Export(ctx, "PROJECT-p07", true)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := learning.VerifyExport(bundle); err != nil {
		t.Fatalf("VerifyExport: %v", err)
	}

	// Restoring the same bundle over unchanged memory changes nothing.
	plans, err := service.PlanRestore(ctx, bundle, "PROJECT-p07", true)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	for _, p := range plans {
		if p.Apply {
			t.Fatalf("restore would overwrite equal-version memory: %+v", p)
		}
	}

	// A tampered bundle is refused outright.
	bundle.Items[0].Claim = "the build command is rm -rf /"
	if _, err := service.PlanRestore(ctx, bundle, "PROJECT-p07", true); !errors.Is(err, learning.ErrTampered) {
		t.Fatalf("PlanRestore err = %v, want ErrTampered", err)
	}
}

// Routing learning proposes; governance disposes. The service cannot apply a
// proposal governance vetoes.
func TestLearningRoutingProposalRespectsGovernanceVeto(t *testing.T) {
	ctx := context.Background()
	runtime, _ := p07Runtime(t, verification.VerifiedComplete)

	key := learning.TrustKey{TaskClass: "go", Provider: "p", ProviderVersion: "1", Model: "m"}
	proposal := learning.RoutingProposal{
		ID: "rp-1", Key: key, Rationale: "measured",
		Trust:           learning.Trust{Key: key, Verified: 5, Clusters: 3, Observations: 5, LastObserved: time.Now().UTC()},
		RolloutFraction: 0.1, Reversible: true,
	}
	if _, err := runtime.Learning().ProposeRouting(ctx, proposal, learning.Governance{
		MinClusters: 2, MaxRollout: 0.5, GovernableProviders: map[string]bool{},
	}); !errors.Is(err, learning.ErrGovernanceVeto) {
		t.Fatalf("ProposeRouting err = %v, want ErrGovernanceVeto", err)
	}
}
