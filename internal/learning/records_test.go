package learning

import (
	"errors"
	"testing"
	"time"
)

func recordsEntry() Entry {
	return Entry{
		ProjectID: "proj-1", GoalID: "g", GoalRevision: 1, PlanID: "p", PlanVersion: 1,
		RunID: "r", RunVersion: 1, VerificationID: "v", VerificationVersion: 1,
		AttestationDigest: "att", EvidenceDigest: "bundle",
		TreeDigest: "tree", EnvironmentDigest: "env",
		Outcome: OutcomeVerifiedComplete,
	}
}

// A record cannot claim exact reproducibility while depending on an external
// system it does not pin.
func TestExactReplayCannotDependOnExternalSystems(t *testing.T) {
	r := ReplayRecord{ID: "rp-1", Binding: recordsEntry(), Class: ReplayExact, ExternalDeps: []string{"api.example.com"}}
	if err := ValidateReplay(r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateReplay err = %v, want ErrInvalid", err)
	}
	r.Class = ReplayBestEffort
	if err := ValidateReplay(r); err != nil {
		t.Fatalf("ValidateReplay: %v", err)
	}
}

// Replay must never re-fire a real-world side effect on its own.
func TestReplayWithSideEffectsIsNotAutomatic(t *testing.T) {
	r := ReplayRecord{
		ID: "rp-1", Binding: recordsEntry(), Class: ReplayExact,
		SideEffects: []string{"posted a pull request"},
	}
	if r.Replayable() {
		t.Fatal("a replay with external side effects was reported automatically replayable")
	}
	safe := ReplayRecord{ID: "rp-2", Binding: recordsEntry(), Class: ReplayExact}
	if !safe.Replayable() {
		t.Fatal("a side-effect-free exact replay was refused")
	}
	nonReplayable := ReplayRecord{ID: "rp-3", Binding: recordsEntry(), Class: ReplayNonReplayable}
	if nonReplayable.Replayable() {
		t.Fatal("a non-replayable record was reported replayable")
	}
}

// An unbound replay record is not an index entry.
func TestReplayNeedsExactBinding(t *testing.T) {
	if err := ValidateReplay(ReplayRecord{ID: "rp-1", Class: ReplayExact}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateReplay err = %v, want ErrInvalid", err)
	}
}

// A seed must never carry secret material into the replay index.
func TestReplaySeedsCannotCarrySecrets(t *testing.T) {
	r := ReplayRecord{
		ID: "rp-1", Binding: recordsEntry(), Class: ReplayExact,
		Seeds: []string{"AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLEKEYDATA"},
	}
	if err := ValidateReplay(r); !errors.Is(err, ErrSecretMaterial) {
		t.Fatalf("ValidateReplay err = %v, want ErrSecretMaterial", err)
	}
}

// A benchmark score must be pinned to a benchmark version and an exact tree,
// so it cannot float free of the code that produced it.
func TestBenchmarkMustBePinned(t *testing.T) {
	base := BenchmarkRecord{
		ID: "b-1", Benchmark: "swe-bench-verified", Version: "2025.1", TaskID: "django-1",
		TreeDigest: "tree", Mode: "full", Resolved: StatusPass, VerifierResult: StatusPass,
	}
	if err := ValidateBenchmark(base); err != nil {
		t.Fatalf("ValidateBenchmark: %v", err)
	}
	unpinned := base
	unpinned.Version = ""
	if err := ValidateBenchmark(unpinned); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unpinned benchmark accepted: %v", err)
	}
	untreed := base
	untreed.TreeDigest = ""
	if err := ValidateBenchmark(untreed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("benchmark without a tree accepted: %v", err)
	}
}

// Unmeasured benchmark cost and latency stay nil rather than reading as zero.
func TestUnmeasuredBenchmarkMetricsStayNil(t *testing.T) {
	b := BenchmarkRecord{
		ID: "b-1", Benchmark: "terminal-bench", Version: "0.9", TaskID: "t-1",
		TreeDigest: "tree", Mode: "baseline", Resolved: StatusNotRun, VerifierResult: StatusNotRun,
	}
	if err := ValidateBenchmark(b); err != nil {
		t.Fatalf("ValidateBenchmark: %v", err)
	}
	if b.CostMicros != nil || b.WallMillis != nil || b.Tokens != nil {
		t.Fatal("unmeasured metrics were materialized as zero")
	}
}

// A baseline recorded against another tree cannot judge this one.
func TestBaselineFromAnotherTreeIsUnbound(t *testing.T) {
	b := Baseline{ID: "bl-1", Binding: recordsEntry(), Metric: "tests", Value: "412"}
	if got := CompareBaseline(b, "400", "other-tree", "env"); got != BaselineUnbound {
		t.Fatalf("outcome = %s, want UNBOUND", got)
	}
	if got := CompareBaseline(b, "412", "tree", "env"); got != BaselineMatch {
		t.Fatalf("outcome = %s, want MATCH", got)
	}
}

// Divergence prompts revalidation; only an invariant makes it a failure.
func TestBaselineDivergenceRevalidatesUnlessInvariant(t *testing.T) {
	b := Baseline{ID: "bl-1", Binding: recordsEntry(), Metric: "runtime_ms", Value: "100"}
	if got := CompareBaseline(b, "140", "tree", "env"); got != BaselineRevalidate {
		t.Fatalf("outcome = %s, want REVALIDATE", got)
	}
	b.Invariant = true
	if got := CompareBaseline(b, "140", "tree", "env"); got != BaselineViolation {
		t.Fatalf("outcome = %s, want VIOLATION", got)
	}
}

// A verifier that misses mutations or is flaky may not stand alone for a
// critical claim.
func TestWeakOracleCannotStandAlone(t *testing.T) {
	strong := OracleRecord{Verifier: "go test", Version: "1.24", MutationsCaught: []string{"off-by-one"}}
	if !SoleOracleAllowed(strong) {
		t.Fatal("a clean verifier was refused as sole oracle")
	}
	misses := strong
	misses.MutationsMissed = []string{"boundary"}
	if SoleOracleAllowed(misses) {
		t.Fatal("a verifier that misses mutations was allowed as sole oracle")
	}
	flaky := strong
	flaky.Flaky = true
	if SoleOracleAllowed(flaky) {
		t.Fatal("a flaky verifier was allowed as sole oracle")
	}
	blind := OracleRecord{Verifier: "noop", Version: "1"}
	if SoleOracleAllowed(blind) {
		t.Fatal("a verifier that caught nothing was allowed as sole oracle")
	}
}

// An observed capability needs an exact harness version and real evidence:
// documentation alone is not an observation.
func TestCapabilityNeedsVersionAndEvidence(t *testing.T) {
	c := Capability{
		Harness: "codex", Version: "0.9.1", Feature: "--headless",
		Observed: StatusFail, Documented: StatusPass, Scope: ScopeGeneral,
		Evidence: []EvidenceRef{{ID: "e1", ClusterID: "c1", Digest: "d", Kind: "run", Observed: time.Now()}},
	}
	if err := ValidateCapability(c); err != nil {
		t.Fatalf("ValidateCapability: %v", err)
	}

	noVersion := c
	noVersion.Version = ""
	if err := ValidateCapability(noVersion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("capability without a version accepted: %v", err)
	}

	noEvidence := c
	noEvidence.Evidence = nil
	if err := ValidateCapability(noEvidence); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("observed capability without evidence accepted: %v", err)
	}

	// NOT_RUN is an honest absence and needs no evidence.
	notRun := c
	notRun.Observed = StatusNotRun
	notRun.Evidence = nil
	if err := ValidateCapability(notRun); err != nil {
		t.Fatalf("NOT_RUN capability rejected: %v", err)
	}
}

// A general capability cannot secretly be bound to one project.
func TestGeneralCapabilityCannotBindAProject(t *testing.T) {
	c := Capability{
		Harness: "codex", Version: "0.9.1", Feature: "--headless",
		Observed: StatusNotRun, Documented: StatusPass,
		Scope: ScopeGeneral, ProjectID: "proj-1",
	}
	if err := ValidateCapability(c); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateCapability err = %v, want ErrInvalid", err)
	}
}
