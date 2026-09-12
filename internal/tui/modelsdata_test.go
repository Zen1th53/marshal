package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Models carries the two remaining frozen gaps and is the section most tempted
// to invent: a quota, a countdown, a recommendation. These tests defend against
// each of those.

type fakeModelsReader struct {
	cycles    []OptimizationCycle
	cyclesErr error
	canaries  []OptimizationCanary
	canaryErr error
	local     []LocalModel
	localErr  error
}

type richModelsReader struct {
	fakeModelsReader
	evidence OptimizationEvidence
	probes   []ProviderProbe
}

func (r richModelsReader) CycleEvidence(context.Context, string) (OptimizationEvidence, error) {
	return r.evidence, nil
}
func (r richModelsReader) Providers(context.Context) ([]ProviderProbe, error) {
	return r.probes, nil
}

func (f fakeModelsReader) Cycles(context.Context) ([]OptimizationCycle, error) {
	return f.cycles, f.cyclesErr
}
func (f fakeModelsReader) CanariesFor(context.Context, string) ([]OptimizationCanary, error) {
	return f.canaries, f.canaryErr
}
func (f fakeModelsReader) LocalModels(context.Context) ([]LocalModel, error) {
	return f.local, f.localErr
}

func testModels(t *testing.T, reader ModelsReader) *ModelsFeed {
	t.Helper()
	return &ModelsFeed{Reader: reader, ProjectID: "proj-1", Now: fixedClock()}
}

// The two frozen gaps render as gaps everywhere, including with no reader.
func TestModelsGapsAreAlwaysStated(t *testing.T) {
	for _, snap := range []ModelsSnapshot{
		(&ModelsFeed{Now: fixedClock()}).ReadModels(context.Background()),
		testModels(t, fakeModelsReader{}).ReadModels(context.Background()),
	} {
		for name, v := range map[string]Value{
			"provider detail": snap.ProviderDetail,
			"entitlement":     snap.Entitlement,
		} {
			if v.Status.IsSuccess() {
				t.Fatalf("%s reports a known value but is a frozen gap", name)
			}
			if !strings.Contains(v.Display(), "IMPLEMENTATION GAP") {
				t.Fatalf("%s does not name its gap: %q", name, v.Display())
			}
			if !strings.Contains(v.Display(), "CTUI-") {
				t.Fatalf("%s does not identify which gap: %q", name, v.Display())
			}
		}
	}
}

// A gap node renders as a gap through the node renderer too, so reaching it by
// cross-link from another section shows the same thing.
func TestModelsGapNodesRenderAsGaps(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	for _, id := range []string{"CTUI-0523", "CTUI-0557"} {
		node, ok := ia.Node(id)
		if !ok {
			t.Fatalf("%s is not in the IA", id)
		}
		content, handled := RenderModelsNode(node, ModelsSnapshot{})
		if !handled {
			t.Fatalf("%s is not rendered", id)
		}
		if !content.HasNotice {
			t.Fatalf("%s renders without a notice", id)
		}
		if !strings.Contains(content.Notice.Display(), "IMPLEMENTATION GAP") {
			t.Fatalf("%s does not name its gap: %q", id, content.Notice.Display())
		}
	}
}

// A canary that was not promoted is NOT_RUN, never a silent success. Promotion
// is a decision Process 08 records, not one inferred from a healthy canary.
func TestUnpromotedCanaryIsNotRun(t *testing.T) {
	snap := testModels(t, fakeModelsReader{
		cycles: []OptimizationCycle{{ID: "cycle-1", Status: "RUNNING",
			StartedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)}},
		canaries: []OptimizationCanary{{ID: "canary-1", Status: "ACTIVE"}},
	}).ReadModels(context.Background())

	if len(snap.Canaries) != 1 {
		t.Fatalf("got %d canaries", len(snap.Canaries))
	}
	promoted := snap.Canaries[0].Promoted
	if promoted.Status.IsSuccess() {
		t.Fatalf("an unpromoted canary reports %q", promoted.Display())
	}
	if promoted.Status != TruthNotRun {
		t.Fatalf("an unpromoted canary reports %s", promoted.Status.Label())
	}
}

// A running cycle has produced no result, and must not read as one.
func TestRunningCycleIsNotAResult(t *testing.T) {
	snap := testModels(t, fakeModelsReader{
		cycles: []OptimizationCycle{{ID: "cycle-1", Status: "RUNNING",
			StartedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)}},
	}).ReadModels(context.Background())

	status := snap.Cycles[0].Status
	if status.Status.IsSuccess() {
		t.Fatalf("a running cycle reports %q", status.Display())
	}
	if status.Status != TruthNotRun {
		t.Fatalf("a running cycle reports %s", status.Status.Label())
	}
}

// A rolled-back cycle is a real, recorded negative outcome.
func TestRolledBackCycleReadsAsARealOutcome(t *testing.T) {
	snap := testModels(t, fakeModelsReader{
		cycles: []OptimizationCycle{{ID: "cycle-1", Status: "ROLLED_BACK",
			StartedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)}},
	}).ReadModels(context.Background())

	status := snap.Cycles[0].Status
	if status.Display() != "ROLLED_BACK" {
		t.Fatalf("a rolled-back cycle rendered as %q", status.Display())
	}
	if !strings.Contains(status.Reason, "recorded negative outcome") {
		t.Fatalf("the outcome is not explained: %q", status.Reason)
	}
}

// An unrecognised Process 08 status becomes UNKNOWN, never a success.
func TestUnrecognisedOptimizationStatusIsUnknown(t *testing.T) {
	for _, status := range []string{"NEW_STATE", "ok", "green"} {
		v := optimizationStatus(status)
		if v.Status.IsSuccess() {
			t.Fatalf("status %q was treated as success: %q", status, v.Display())
		}
		if v.Status != TruthUnknown {
			t.Fatalf("status %q reported %s", status, v.Status.Label())
		}
	}
}

// A "may fit" model carries its reason, so it is never read as a recommendation.
func TestModelCompatibilityCarriesItsReason(t *testing.T) {
	snap := testModels(t, fakeModelsReader{local: []LocalModel{{
		Name: "llama-70b", Family: "llama",
		Compatibility: "MAY_FIT",
		Reason:        "needs 40 GiB and this machine has 32 GiB available",
	}}}).ReadModels(context.Background())

	compatibility := snap.LocalModels[0].Compatibility
	if compatibility.Reason == "" {
		t.Fatal("a fit assessment carries no reason")
	}
	if !strings.Contains(compatibility.Reason, "32 GiB") {
		t.Fatalf("the reason was lost: %q", compatibility.Reason)
	}
}

// A model the collector could not assess is UNKNOWN with a reason.
func TestUnassessedModelIsUnknown(t *testing.T) {
	snap := testModels(t, fakeModelsReader{local: []LocalModel{{
		Name: "mystery", Compatibility: "",
	}}}).ReadModels(context.Background())

	compatibility := snap.LocalModels[0].Compatibility
	if compatibility.Status.IsSuccess() {
		t.Fatalf("an unassessed model reports %q", compatibility.Display())
	}
	if compatibility.Reason == "" {
		t.Fatal("an unassessed model gives no reason")
	}
}

// An unreadable cycle list is distinct from an empty one.
func TestUnreadableCyclesAreDistinctFromNoCycles(t *testing.T) {
	empty := testModels(t, fakeModelsReader{}).ReadModels(context.Background())
	if empty.CyclesStatus.Status != TruthEmpty {
		t.Fatalf("no cycles reported %s", empty.CyclesStatus.Status.Label())
	}

	broken := testModels(t, fakeModelsReader{
		cyclesErr: errors.New("the optimization store is unreachable"),
	}).ReadModels(context.Background())
	if broken.CyclesStatus.Status != TruthError {
		t.Fatalf("unreadable cycles reported %s", broken.CyclesStatus.Status.Label())
	}
}

func TestRichModelsReaderUsesCanonicalProcess08AndProviderEvidence(t *testing.T) {
	snap := testModels(t, richModelsReader{
		fakeModelsReader: fakeModelsReader{cycles: []OptimizationCycle{{
			ID: "cycle-1", Status: "RUNNING", StartedAt: time.Now().UTC(),
		}}},
		evidence: OptimizationEvidence{Candidates: 2, Counterfactuals: 1, Benchmarks: 3, Results: 4, Promotions: 1},
		probes:   []ProviderProbe{{Name: "codex", Probed: true, Found: true}, {Name: "claude", Probed: true}},
	}).ReadModels(context.Background())

	for name, value := range map[string]Value{
		"providers": snap.ProviderStatus, "evaluation": snap.EvaluationStatus,
		"counterfactuals": snap.CounterfactualStatus, "benchmarks": snap.BenchmarkStatus,
		"results": snap.ResultsStatus, "feedback": snap.FeedbackStatus,
	} {
		if value.Status != TruthKnown {
			t.Fatalf("%s = %s (%s)", name, value.Status.Label(), value.Display())
		}
	}
	if !strings.Contains(snap.ProviderStatus.Text, "1 installed of 2") {
		t.Fatalf("provider evidence = %q", snap.ProviderStatus.Text)
	}
}

// Process 08 actions that decide promotions are unavailable: a promotion from
// invented evidence would change how MARSHAL routes work on the strength of
// nothing.
func TestOptimizationDecisionsAreUnavailable(t *testing.T) {
	source, _ := testControl(t)
	for _, id := range []ActionID{
		"CTUI-0573", "CTUI-0587", "CTUI-0617", "CTUI-0621", "CTUI-0623", "CTUI-0626",
	} {
		binding, ok := source.Bindings()[id]
		if !ok {
			t.Fatalf("%s has no binding", id)
		}
		if binding.Bound() {
			t.Fatalf("%s decides a Process 08 outcome from content this screen "+
				"does not collect", id)
		}
		if !strings.Contains(binding.Requires, "Process 08") {
			t.Fatalf("%s does not name the owning process: %q", id, binding.Requires)
		}
	}
}
