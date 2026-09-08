package optimization

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

func cfNow() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }

func cfGovernance() Governance {
	return Governance{
		GovernableProviders: map[string]bool{"codex": true, "claude": true},
		MinEvidenceClusters: 2,
		MaxCanaryExposure:   0.25,
		RequireRollback:     true,
	}
}

func cfRoute(provider string) Route {
	return Route{
		TaskClass: "go-refactor", Provider: provider, ProviderVersion: "1.0",
		Model: "m", Harness: "h", HarnessVersion: "0.9", VerifierPolicy: "standard",
	}
}

func cfSandbox() SandboxPolicy {
	return SandboxPolicy{
		WritableRoot: "/tmp/replay", MaxWallMillis: 60_000, MaxMemoryBytes: 1 << 30,
	}
}

func cfFactual(outcome learning.Outcome, verifier Status) FactualRun {
	return FactualRun{
		TaskID: "task-1", Route: cfRoute("codex"), Outcome: outcome,
		VerifierResult: verifier, ReplayClass: learning.ReplayExact,
		TreeDigest: "tree", EnvironmentDigest: "env", ObservedAt: cfNow(),
	}
}

func metric(name string, v float64) Metric {
	return Metric{Name: name, Value: &v, Unit: "micros", Source: "provider", Method: "reported"}
}

// A counterfactual is never run against a task that performed a destructive
// external action, because running it would repeat the action.
func TestCounterfactualRefusesDestructiveReplay(t *testing.T) {
	f := cfFactual(learning.OutcomeVerifiedComplete, StatusPass)
	f.SideEffects = []SideEffect{{Kind: "git-push", Destructive: true, Target: "origin/main"}}

	for _, method := range []EvalMethod{MethodReplay, MethodShadow, MethodBenchmark, MethodSynthetic} {
		if err := SafeToEvaluate(f, method); !errors.Is(err, ErrUnsafeCounterfactual) {
			t.Errorf("method %s: err = %v, want ErrUnsafeCounterfactual", method, err)
		}
	}
}

// Process 07's non-replayable judgement is honoured rather than second-guessed,
// but shadow and synthetic evaluation remain available because neither
// re-executes the original task.
func TestNonReplayableBlocksReplayOnly(t *testing.T) {
	f := cfFactual(learning.OutcomeVerifiedComplete, StatusPass)
	f.ReplayClass = learning.ReplayNonReplayable

	if err := SafeToEvaluate(f, MethodReplay); !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("replay of a non-replayable run: err = %v, want ErrUnsafeCounterfactual", err)
	}
	for _, method := range []EvalMethod{MethodShadow, MethodBenchmark, MethodSynthetic} {
		if err := SafeToEvaluate(f, method); err != nil {
			t.Errorf("method %s was refused: %v", method, err)
		}
	}
}

// A replay that asks for production credentials is refused outright: that is a
// second execution against the real world, not a replay.
func TestReplaySandboxRefusesProductionCredentials(t *testing.T) {
	p := cfSandbox()
	p.ProductionCredentials = true
	if err := ValidateSandbox(p); !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("ValidateSandbox err = %v, want ErrUnsafeCounterfactual", err)
	}
}

// A replay without resource bounds or a writable root is not bounded at all.
func TestReplaySandboxNeedsBounds(t *testing.T) {
	if err := ValidateSandbox(SandboxPolicy{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty sandbox accepted: %v", err)
	}
	noRoot := cfSandbox()
	noRoot.WritableRoot = ""
	if err := ValidateSandbox(noRoot); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sandbox without a writable root accepted: %v", err)
	}
	noWall := cfSandbox()
	noWall.MaxWallMillis = 0
	if err := ValidateSandbox(noWall); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sandbox without a wall bound accepted: %v", err)
	}
	if err := ValidateSandbox(cfSandbox()); err != nil {
		t.Fatalf("a bounded sandbox was refused: %v", err)
	}
}

// Evaluating a route MARSHAL cannot govern produces evidence for a change that
// could never be adopted, so it is refused at construction.
func TestCounterfactualRefusesUngovernableAlternate(t *testing.T) {
	c := Counterfactual{
		ID: "cf-1", Method: MethodReplay, Provenance: "test",
		Factual:          cfFactual(learning.OutcomeFailed, StatusFail),
		Alternate:        cfRoute("rogue-provider"),
		AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass,
		Sandbox: cfSandbox(),
	}
	if _, err := NewCounterfactual(c, cfGovernance(), cfNow()); !errors.Is(err, ErrVeto) {
		t.Fatalf("NewCounterfactual err = %v, want ErrVeto", err)
	}
}

// A recorded counterfactual always states what it does not establish.
func TestCounterfactualRecordsItsOwnLimitations(t *testing.T) {
	c := Counterfactual{
		ID: "cf-1", Method: MethodReplay, Provenance: "test", ClusterID: "c1",
		Factual:          cfFactual(learning.OutcomeFailed, StatusFail),
		Alternate:        cfRoute("claude"),
		AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass,
		Sandbox: cfSandbox(),
	}
	got, err := NewCounterfactual(c, cfGovernance(), cfNow())
	if err != nil {
		t.Fatalf("NewCounterfactual: %v", err)
	}
	if len(got.Limitations) == 0 {
		t.Fatal("a counterfactual was recorded with no stated limitations")
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// A counterfactual altered after the fact is detectable.
func TestCounterfactualTamperIsDetected(t *testing.T) {
	c := Counterfactual{
		ID: "cf-1", Method: MethodReplay, Provenance: "test", ClusterID: "c1",
		Factual:          cfFactual(learning.OutcomeFailed, StatusFail),
		Alternate:        cfRoute("claude"),
		AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass,
		Sandbox: cfSandbox(),
	}
	got, err := NewCounterfactual(c, cfGovernance(), cfNow())
	if err != nil {
		t.Fatalf("NewCounterfactual: %v", err)
	}
	got.AlternateOutcome = learning.OutcomeFailed
	if err := got.Verify(); !errors.Is(err, ErrTampered) {
		t.Fatalf("Verify err = %v, want ErrTampered", err)
	}
}

// Verified outcome dominates. A cheaper, faster route that did not verify did
// not do better.
func TestCompareRanksVerifiedOutcomeAboveCost(t *testing.T) {
	c := Counterfactual{
		Factual: FactualRun{
			Outcome: learning.OutcomeVerifiedComplete, VerifierResult: StatusPass,
			Metrics: []Metric{metric("cost_micros", 5000)},
		},
		AlternateOutcome: learning.OutcomeFailed, AlternateVerifier: StatusFail,
		AlternateMetrics: []Metric{metric("cost_micros", 10)},
	}
	if got := Compare(c); got != VerdictFactualBetter {
		t.Fatalf("verdict = %s, want FACTUAL_BETTER despite the cheaper alternate", got)
	}
}

// Cost only separates routes that both reached a verified outcome.
func TestCompareUsesCostOnlyAmongVerifiedRoutes(t *testing.T) {
	c := Counterfactual{
		Factual: FactualRun{
			Outcome: learning.OutcomeVerifiedComplete, VerifierResult: StatusPass,
			Metrics: []Metric{metric("cost_micros", 5000)},
		},
		AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass,
		AlternateMetrics: []Metric{metric("cost_micros", 1000)},
	}
	if got := Compare(c); got != VerdictAlternateBetter {
		t.Fatalf("verdict = %s, want ALTERNATE_BETTER on measured cost", got)
	}
}

// An unmeasured cost cannot separate two routes: it is not a cost of zero.
func TestCompareIgnoresUnmeasuredCost(t *testing.T) {
	c := Counterfactual{
		Factual: FactualRun{
			Outcome: learning.OutcomeVerifiedComplete, VerifierResult: StatusPass,
			Metrics: []Metric{{Name: "cost_micros", Source: "provider"}},
		},
		AlternateOutcome: learning.OutcomeVerifiedComplete, AlternateVerifier: StatusPass,
		AlternateMetrics: []Metric{{Name: "cost_micros", Source: "provider"}},
	}
	if got := Compare(c); got != VerdictEquivalent {
		t.Fatalf("verdict = %s, want EQUIVALENT when neither cost was measured", got)
	}
}

// UNKNOWN is not a tie. A comparison that could not be made says so.
func TestCompareKeepsUnknownDistinctFromEquivalent(t *testing.T) {
	for _, status := range []Status{StatusUnknown, StatusNotRun} {
		c := Counterfactual{
			Factual:           FactualRun{Outcome: learning.OutcomeVerifiedComplete, VerifierResult: status},
			AlternateOutcome:  learning.OutcomeVerifiedComplete,
			AlternateVerifier: StatusPass,
		}
		if got := Compare(c); got != VerdictUnknown {
			t.Errorf("factual verifier %s: verdict = %s, want UNKNOWN", status, got)
		}
	}
}

func aggFixture(t *testing.T, id, cluster string, method EvalMethod, alternateWins bool) Counterfactual {
	t.Helper()
	factualOutcome, factualVerifier := learning.OutcomeFailed, StatusFail
	altOutcome, altVerifier := learning.OutcomeVerifiedComplete, StatusPass
	if !alternateWins {
		factualOutcome, factualVerifier = learning.OutcomeVerifiedComplete, StatusPass
		altOutcome, altVerifier = learning.OutcomeFailed, StatusFail
	}
	f := cfFactual(factualOutcome, factualVerifier)
	f.TaskID = id
	c, err := NewCounterfactual(Counterfactual{
		ID: id, Method: method, Provenance: "test", ClusterID: cluster,
		Factual: f, Alternate: cfRoute("claude"),
		AlternateOutcome: altOutcome, AlternateVerifier: altVerifier,
		Sandbox: cfSandbox(),
	}, cfGovernance(), cfNow())
	if err != nil {
		t.Fatalf("NewCounterfactual(%s): %v", id, err)
	}
	return c
}

// Route evidence counts independent clusters, so one comparison repeated does
// not accumulate into apparent support.
func TestRouteEvidenceCountsClustersNotObservations(t *testing.T) {
	cfs := []Counterfactual{
		aggFixture(t, "a", "same", MethodReplay, true),
		aggFixture(t, "b", "same", MethodReplay, true),
		aggFixture(t, "c", "same", MethodReplay, true),
	}
	evidence := AggregateRouteEvidence(cfs)
	if len(evidence) != 1 {
		t.Fatalf("routes = %d, want 1", len(evidence))
	}
	for _, e := range evidence {
		if e.Better != 3 {
			t.Fatalf("wins = %d, want 3", e.Better)
		}
		if e.Clusters != 1 {
			t.Fatalf("clusters = %d, want 1 for one repeated source", e.Clusters)
		}
		if ok, why := e.SupportsRouting(cfGovernance()); ok {
			t.Fatalf("one echoed cluster justified a routing change: %s", why)
		}
	}
}

// Evidence built entirely from synthetic fixtures proves the mechanism, not
// that the route is better on real work.
func TestSyntheticOnlyEvidenceCannotJustifyRouting(t *testing.T) {
	cfs := []Counterfactual{
		aggFixture(t, "a", "c1", MethodSynthetic, true),
		aggFixture(t, "b", "c2", MethodSynthetic, true),
		aggFixture(t, "c", "c3", MethodSynthetic, true),
	}
	for _, e := range AggregateRouteEvidence(cfs) {
		ok, why := e.SupportsRouting(cfGovernance())
		if ok {
			t.Fatal("synthetic-only evidence justified a routing change")
		}
		if why == "" {
			t.Fatal("refusal gave no reason")
		}
	}
}

// Real multi-cluster evidence with more wins than losses does support a change.
func TestReplayEvidenceAcrossClustersSupportsRouting(t *testing.T) {
	cfs := []Counterfactual{
		aggFixture(t, "a", "c1", MethodReplay, true),
		aggFixture(t, "b", "c2", MethodBenchmark, true),
		aggFixture(t, "c", "c3", MethodShadow, true),
	}
	for _, e := range AggregateRouteEvidence(cfs) {
		if ok, why := e.SupportsRouting(cfGovernance()); !ok {
			t.Fatalf("real multi-cluster evidence was refused: %s", why)
		}
	}
}

// A route that loses as often as it wins has not shown an improvement.
func TestEvenRouteEvidenceDoesNotSupportChange(t *testing.T) {
	cfs := []Counterfactual{
		aggFixture(t, "a", "c1", MethodReplay, true),
		aggFixture(t, "b", "c2", MethodReplay, false),
	}
	for _, e := range AggregateRouteEvidence(cfs) {
		if ok, _ := e.SupportsRouting(cfGovernance()); ok {
			t.Fatalf("a route with %d wins and %d losses was supported", e.Better, e.Worse)
		}
	}
}
