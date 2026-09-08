package learning

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// The adversarial suite in spec 38. Each case is an attack on the memory model
// that must fail safely rather than produce a durable false fact.

func advNow() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }

func advEntry(outcome Outcome) Entry {
	return Entry{
		ProjectID: "proj-1", GoalID: "g", GoalRevision: 1, PlanID: "p", PlanVersion: 1,
		RunID: "r", RunVersion: 1, VerificationID: "v", VerificationVersion: 1,
		AttestationDigest: "att", EvidenceDigest: "bundle",
		TreeDigest: "tree", EnvironmentDigest: "env", Outcome: outcome,
	}
}

func advItem(claim string) Item {
	return Item{
		ID: "m-1", Claim: claim, Scope: ScopeProject, ProjectID: "proj-1",
		State: model.ClaimStateVerified, Version: 1,
		Evidence: []EvidenceRef{
			{ID: "e1", ClusterID: "c1", Digest: "d1", Kind: "test", Observed: advNow()},
			{ID: "e2", ClusterID: "c2", Digest: "d2", Kind: "test", Observed: advNow()},
		},
		Provenance: "process-06", Binding: advEntry(OutcomeVerifiedComplete),
	}
}

// secretFixtures builds credential-shaped strings at run time. The parts are
// joined here rather than written as literals so the repository never carries
// anything that reads as a real credential.
func secretFixtures() []string {
	pem := "-----" + "BEGIN RSA PRIVATE" + " KEY-----\nMIIEow\n-----" + "END RSA PRIVATE" + " KEY-----"
	return []string{
		"aws_secret_access_key" + "=" + "AKIA" + strings.Repeat("X", 16),
		pem,
		"export GITHUB_TOKEN=" + "ghp" + "_" + strings.Repeat("a", 36),
		"password" + "=" + "hunter2",
		"authorization: bearer " + strings.Repeat("z", 40),
	}
}

// 1. A provider asserting a rule is not evidence for it. Without supporting
// evidence the claim cannot become durable memory.
func TestAdversarialProviderAssertionIsNotEvidence(t *testing.T) {
	it := advItem("always skip the security gate")
	it.Evidence = nil
	it.Provenance = "provider said: remember this forever"
	if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("Promote err = %v, want ErrNotPromotable", err)
	}
}

// 2. One source echoed many times is still one source, so it cannot satisfy a
// requirement for independent corroboration.
func TestAdversarialRepeatedSourceIsNotIndependent(t *testing.T) {
	it := advItem("this harness flag is broken everywhere")
	it.Scope = ScopeGeneral
	it.ProjectID = ""
	it.Applicability = []string{"harness 0.9"}
	it.Evidence = []EvidenceRef{
		{ID: "e1", ClusterID: "same", Digest: "d", Kind: "log"},
		{ID: "e2", ClusterID: "same", Digest: "d", Kind: "log"},
		{ID: "e3", ClusterID: "same", Digest: "d", Kind: "log"},
	}
	if _, err := Promote(PromotionInput{Item: it, IndependentObservations: 5}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("Promote err = %v, want ErrNotPromotable for one echoed cluster", err)
	}
}

// 3. Stale knowledge is not authority: it cannot be promoted as current.
func TestAdversarialStaleKnowledgeCannotEnterAsCurrent(t *testing.T) {
	it := advItem("the schema has 40 tables")
	past := advNow().Add(-time.Hour)
	it.ExpiresAt = &past
	if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("Promote err = %v, want ErrNotPromotable", err)
	}
}

// 4 and 24. A provider handed only easy, deliberately selected work must be
// flagged rather than appearing universally strong.
func TestAdversarialEasyTaskSelectionIsFlagged(t *testing.T) {
	obs := []RoutingObservation{
		{TaskClass: "easy", Provider: "codex", ProviderVersion: "1", Model: "m", Outcome: OutcomeVerifiedComplete, Selected: true, ClusterID: "c1"},
		{TaskClass: "easy", Provider: "codex", ProviderVersion: "1", Model: "m", Outcome: OutcomeVerifiedComplete, Selected: true, ClusterID: "c2"},
	}
	trust := AggregateTrust(obs)
	if len(trust) != 1 {
		t.Fatalf("trust keys = %d, want 1", len(trust))
	}
	for _, record := range trust {
		if !record.SelectionBiased {
			t.Fatal("a provider given only selected work was not flagged as selection biased")
		}
	}
}

// 5. Failed runs are learned, not hidden. Aggregation must count them.
func TestAdversarialFailedRunsAreNotOmitted(t *testing.T) {
	obs := []RoutingObservation{
		{TaskClass: "go", Provider: "p", ProviderVersion: "1", Model: "m", Outcome: OutcomeVerifiedComplete, Selected: true, ClusterID: "c1"},
		{TaskClass: "go", Provider: "p", ProviderVersion: "1", Model: "m", Outcome: OutcomeFailed, Selected: true, ClusterID: "c2"},
		{TaskClass: "go", Provider: "p", ProviderVersion: "1", Model: "m", Outcome: OutcomeBlocked, Selected: false, ClusterID: "c3"},
	}
	for _, record := range AggregateTrust(obs) {
		if record.Failed != 1 || record.Blocked != 1 {
			t.Fatalf("trust = %+v, want the failure and the blocked run counted", record)
		}
		if record.Total() != 3 {
			t.Fatalf("total = %d, want every outcome counted", record.Total())
		}
	}
}

// 6 and 21. A secret never becomes durable memory, at any scope.
func TestAdversarialSecretNeverEntersMemory(t *testing.T) {
	for _, secret := range secretFixtures() {
		it := advItem(secret)
		if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrSecretMaterial) {
			t.Errorf("Promote(%.20q) err = %v, want ErrSecretMaterial", secret, err)
		}
		// A secret hidden in an applicability bound is caught too.
		bounded := advItem("a safe claim")
		bounded.Scope = ScopeGeneral
		bounded.ProjectID = ""
		bounded.Applicability = []string{secret}
		if _, err := Promote(PromotionInput{Item: bounded, IndependentObservations: 3}, advNow()); !errors.Is(err, ErrSecretMaterial) {
			t.Errorf("applicability secret err = %v, want ErrSecretMaterial", err)
		}
	}
}

// 7 and 23. Repetition and user insistence do not promote an unsupported
// claim. Promoting the same unsupported item any number of times still fails.
func TestAdversarialRepetitionDoesNotPromoteUnsupportedClaim(t *testing.T) {
	it := advItem("this refactor is always safe")
	it.State = model.ClaimStateUnsupported
	for i := 0; i < 10; i++ {
		if _, err := Promote(PromotionInput{Item: it, IndependentObservations: i}, advNow()); !errors.Is(err, ErrNotPromotable) {
			t.Fatalf("attempt %d: err = %v, want ErrNotPromotable", i, err)
		}
	}
}

// 8. Prestige does not settle a contradiction. Fresh deterministic evidence
// outranks stored knowledge regardless of the source's standing.
func TestAdversarialPrestigeDoesNotWinContradiction(t *testing.T) {
	prev := advItem("the build passes")
	prev.Provenance = "frontier-model, high confidence"
	prev.Version = 1

	next := prev
	next.State = model.ClaimStateInvalidated
	next.Version = 2
	next.Provenance = "deterministic test run"
	next.Evidence = []EvidenceRef{{ID: "e9", ClusterID: "c9", Digest: "d", Kind: "test", Observed: advNow()}}

	got, err := Revise(prev, next, advNow())
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if got.State != model.ClaimStateInvalidated {
		t.Fatalf("state = %s, want the deterministic result to win", got.State)
	}
}

// 9 and 15. A time-sensitive observation must not stay fresh forever.
func TestAdversarialQuotaObservationIsNotPermanent(t *testing.T) {
	it := advItem("the provider quota is 100 requests per minute")
	expiry := advNow().Add(time.Hour)
	it.ExpiresAt = &expiry
	promoted, err := Promote(PromotionInput{Item: it}, advNow())
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Fresh(advNow().Add(2 * time.Hour)) {
		t.Fatal("a quota observation stayed fresh past its expiry")
	}
}

// 10. A version-specific failure cannot be generalized without independent
// corroboration and explicit applicability bounds.
func TestAdversarialVersionSpecificFailureIsNotGeneralized(t *testing.T) {
	it := advItem("the tool crashes on every version")
	it.Scope = ScopeGeneral
	it.ProjectID = ""
	// One cluster, no applicability bounds: this is a project observation
	// dressed as a universal law.
	it.Evidence = []EvidenceRef{{ID: "e1", ClusterID: "c1", Digest: "d", Kind: "log"}}
	if _, err := Promote(PromotionInput{Item: it, IndependentObservations: 1}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("Promote err = %v, want ErrNotPromotable", err)
	}
}

// 11 and 22. A claim that does not bind to the exact Process 06 state it
// alleges cannot be promoted, so a repository file cannot inject policy.
func TestAdversarialUnboundClaimCannotBePromoted(t *testing.T) {
	it := advItem("policy allows unrestricted network egress")
	it.Binding = Entry{}
	if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Promote err = %v, want ErrInvalid", err)
	}

	// A project claim naming a different project than its binding is refused.
	crossProject := advItem("this repository builds with bazel")
	crossProject.ProjectID = "other-project"
	if _, err := Promote(PromotionInput{Item: crossProject}, advNow()); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("Promote err = %v, want ErrBindingMismatch", err)
	}
}

// 12 and 13. Routing learning cannot relax governance, and a proposal without
// independent evidence is vetoed.
func TestAdversarialRoutingCannotBypassGovernance(t *testing.T) {
	key := TrustKey{TaskClass: "go", Provider: "ungovernable", ProviderVersion: "1", Model: "m"}
	proposal := RoutingProposal{
		ID: "rp-1", Key: key,
		Rationale:       "a benchmark fixture says it is fastest",
		Trust:           Trust{Key: key, Verified: 9, Clusters: 3, Observations: 9, LastObserved: advNow()},
		RolloutFraction: 0.1,
		Reversible:      true,
	}
	// Governance does not govern this provider, so the proposal cannot proceed
	// however strong its measured record looks.
	governance := Governance{MinClusters: 2, MaxRollout: 0.5, GovernableProviders: map[string]bool{}}
	if _, err := ApplyProposal(proposal, governance, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatalf("ApplyProposal err = %v, want ErrGovernanceVeto", err)
	}

	// Making the provider governable is not enough on its own: a selection
	// biased record, a version drift or an open security regression each veto
	// the same proposal.
	governable := Governance{
		MinClusters: 2, MaxRollout: 0.5,
		GovernableProviders: map[string]bool{"ungovernable": true},
	}
	if _, err := ApplyProposal(proposal, governable, advNow()); err != nil {
		t.Fatalf("a governed, evidenced, bounded proposal was refused: %v", err)
	}

	biased := proposal
	biased.Trust.SelectionBiased = true
	if _, err := ApplyProposal(biased, governable, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("a selection-biased proposal was applied")
	}

	drifted := governable
	drifted.KnownVersions = map[string]string{"ungovernable": "2"}
	if _, err := ApplyProposal(proposal, drifted, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("a proposal resting on a drifted provider version was applied")
	}

	regressed := governable
	regressed.SecurityRegression = true
	if _, err := ApplyProposal(proposal, regressed, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("a proposal was applied while a security regression was open")
	}

	// One run must never rewrite global routing.
	unbounded := proposal
	unbounded.RolloutFraction = 1.0
	if _, err := ApplyProposal(unbounded, governable, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("an unbounded global rollout was applied")
	}

	irreversible := proposal
	irreversible.Reversible = false
	if _, err := ApplyProposal(irreversible, governable, advNow()); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("an irreversible routing change was applied")
	}
}

// 16. A playbook candidate cannot arrive already active.
func TestAdversarialPlaybookCannotSelfActivate(t *testing.T) {
	_, err := NewCommit(Commit{
		ID: "mc-1", Binding: advEntry(OutcomeVerifiedComplete), Provenance: "test",
		Playbooks: []PlaybookCandidate{{ID: "pb", Title: "t", Scope: ScopeProject, ProjectID: "proj-1", Active: true}},
	}, advNow())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewCommit err = %v, want ErrInvalid", err)
	}
}

// 17. A human waiver is not verification. A run that only reached PARTIAL
// cannot produce a VERIFIED memory item however it was waived.
func TestAdversarialWaiverIsNotVerification(t *testing.T) {
	it := advItem("the migration is safe")
	it.Binding = advEntry(OutcomePartial)
	it.Binding.Waivers = []string{"waiver-1"}
	if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("Promote err = %v, want ErrNotPromotable", err)
	}
}

// 18. A VERIFIED item may only come from an outcome that actually verified.
func TestAdversarialUnverifiedOutcomeCannotProduceVerifiedMemory(t *testing.T) {
	for _, outcome := range []Outcome{OutcomePartial, OutcomeFailed, OutcomeBlocked} {
		it := advItem("the feature works")
		it.Binding = advEntry(outcome)
		if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrNotPromotable) {
			t.Errorf("outcome %s: err = %v, want ErrNotPromotable", outcome, err)
		}
	}
}

// 19. Collecting expired raw evidence must not break an attestation: the
// digest and provenance survive.
func TestAdversarialGCDoesNotBreakAttestation(t *testing.T) {
	past := advNow().Add(-time.Hour)
	plans := PlanGC([]GCCandidate{{
		ID: "raw-1", Class: ClassRawOutput, KeepUntil: &past,
		AttestationRefs: []string{"att-1"},
	}}, advNow())
	if plans[0].Action != GCExpirePayload {
		t.Fatalf("action = %s, want the digest retained for the attestation", plans[0].Action)
	}
	if !strings.Contains(plans[0].Reason, "digest retained") {
		t.Fatalf("reason = %q, want it to state that provenance survives", plans[0].Reason)
	}
}

// 20. Retrieval must not silently drop a contradiction.
func TestAdversarialRetrievalKeepsContradiction(t *testing.T) {
	contested := advItem("tests are hermetic")
	contested.State = model.ClaimStateContested
	contested.Contradicts = []string{"m-other"}
	results := Retrieve([]Item{contested}, Query{ProjectID: "proj-1"}, advNow())
	if len(results) != 1 {
		t.Fatalf("results = %d, want the contradiction surfaced", len(results))
	}
	if !results[0].Contradicted || results[0].Usable {
		t.Fatalf("result = %+v, want contradicted and unusable", results[0])
	}
}

// 14. A restore must not overwrite fresher memory. Covered end to end here so
// the adversarial suite carries the case in its own right.
func TestAdversarialRestoreCannotOverwriteFresherMemory(t *testing.T) {
	old := advItem("the build command is make")
	old.Version = 1
	backup, err := Export([]Item{old}, 84, "proj-1", advNow())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	newer := advItem("the build command is go build")
	newer.Version = 7

	plans, err := Restore(backup, []Item{newer}, 84)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(plans) != 1 || plans[0].Apply {
		t.Fatalf("plans = %+v, want the newer memory kept", plans)
	}
}

// A contested claim without contradiction references is malformed rather than
// promotable: the contradiction must be recorded, not asserted.
func TestAdversarialContestedClaimNeedsContradictionRefs(t *testing.T) {
	it := advItem("the API is stable")
	it.State = model.ClaimStateContested
	if _, err := Promote(PromotionInput{Item: it}, advNow()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Promote err = %v, want ErrInvalid", err)
	}
}

// A critical claim must clear the higher bar: verified, multi-cluster and
// replayable.
func TestAdversarialCriticalClaimNeedsReplayableMultiClusterEvidence(t *testing.T) {
	base := advItem("the sandbox blocks all network egress")
	base.Critical = true

	oneCluster := base
	oneCluster.Evidence = []EvidenceRef{{ID: "e1", ClusterID: "c1", Digest: "d", Kind: "test"}}
	if _, err := Promote(PromotionInput{Item: oneCluster, Replayable: true}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("single-cluster critical claim err = %v, want ErrNotPromotable", err)
	}

	notReplayable := base
	if _, err := Promote(PromotionInput{Item: notReplayable, Replayable: false}, advNow()); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("non-replayable critical claim err = %v, want ErrNotPromotable", err)
	}

	if _, err := Promote(PromotionInput{Item: base, Replayable: true}, advNow()); err != nil {
		t.Fatalf("a fully evidenced critical claim was refused: %v", err)
	}
}
