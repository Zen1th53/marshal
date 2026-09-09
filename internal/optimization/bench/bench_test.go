package bench

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/optimization"
)

func benchRoute() optimization.Route {
	return optimization.Route{TaskClass: "code", Provider: "codex", ProviderVersion: "1", Model: "m", Harness: "h", HarnessVersion: "1", VerifierPolicy: "verify"}
}

func terminalConfig() TerminalConfig {
	return TerminalConfig{BenchmarkVersion: "v1", HarnessVersion: "harbor-v1", DatasetSnapshot: "snap", TaskIDs: []string{"t1", "t2"}, MarshalSHA: "sha", ConfigDigest: "cfg", EnvironmentImage: "img", Route: benchRoute()}
}

type terminalFake struct {
	probe Probe
	raw   RawTaskResult
	calls int
}

func (f *terminalFake) Probe(context.Context, TerminalConfig) (Probe, error) { return f.probe, nil }
func (f *terminalFake) RunTask(_ context.Context, _ TerminalConfig, _ string) (RawTaskResult, error) {
	f.calls++
	return f.raw, nil
}

func TestTerminalBenchUnavailableIsNotRunForEveryPinnedTask(t *testing.T) {
	fake := &terminalFake{probe: Probe{Reason: "not installed"}}
	_, runs, err := RunTerminalBench(context.Background(), fake, terminalConfig(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || len(runs) != 2 {
		t.Fatalf("calls=%d runs=%d", fake.calls, len(runs))
	}
	for _, run := range runs {
		if run.Outcome != optimization.StatusNotRun || run.FailureReason == "" {
			t.Fatalf("unavailable result=%+v", run)
		}
	}
}

func TestTerminalBenchRecordsRealHarnessResult(t *testing.T) {
	ok := true
	fake := &terminalFake{probe: Probe{Available: true, Version: "harbor-v1"}, raw: RawTaskResult{Success: &ok, ArtifactPaths: []string{"artifact.json"}}}
	manifest, runs, err := RunTerminalBench(context.Background(), fake, terminalConfig(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 || !manifest.Official || len(runs) != 2 {
		t.Fatalf("manifest=%+v calls=%d runs=%d", manifest, fake.calls, len(runs))
	}
	if runs[0].Outcome != optimization.StatusPass || len(runs[0].ArtifactPaths) != 1 {
		t.Fatalf("run=%+v", runs[0])
	}
}

func sweConfig() SWEBenchConfig {
	return SWEBenchConfig{DatasetVersion: "verified-v1", EvaluatorVersion: "official-v1", DatasetSnapshot: "snap", TaskIDs: []string{"i1"}, MarshalSHA: "sha", ConfigDigest: "cfg", EnvironmentImage: "img", Route: benchRoute()}
}

type sweFake struct {
	probe Probe
	raw   RawInstanceResult
	calls int
}

func (f *sweFake) Probe(context.Context, SWEBenchConfig) (Probe, error) { return f.probe, nil }
func (f *sweFake) RunInstance(_ context.Context, _ SWEBenchConfig, _ string) (RawInstanceResult, error) {
	f.calls++
	return f.raw, nil
}

func TestSWEBenchDoesNotTurnMissingOfficialEvaluatorIntoFailure(t *testing.T) {
	fake := &sweFake{probe: Probe{Reason: "missing"}}
	_, results, err := RunSWEBench(context.Background(), fake, sweConfig(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || len(results) != 1 {
		t.Fatalf("calls=%d results=%d", fake.calls, len(results))
	}
	if got := results[0].ToExperimentResult("r").Outcome; got != optimization.StatusNotRun {
		t.Fatalf("outcome=%s, want NOT_RUN", got)
	}
}

func TestSWEBenchAcceptsOnlyOfficialEvaluatorVerdict(t *testing.T) {
	ok := true
	fake := &sweFake{probe: Probe{Available: true}, raw: RawInstanceResult{Resolved: &ok, PatchRef: "patch", VerifierLog: "official-log", ReproCommand: "official run"}}
	_, results, err := RunSWEBench(context.Background(), fake, sweConfig(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || !results[0].Resolution.Official() {
		t.Fatalf("result=%+v calls=%d", results[0], fake.calls)
	}
	if got := results[0].ToExperimentResult("r").Outcome; got != optimization.StatusPass {
		t.Fatalf("outcome=%s", got)
	}
	if got := OfficialOutcome(NewUnofficialResolution(true, "local")); got != optimization.StatusUnknown {
		t.Fatalf("unofficial=%s", got)
	}
}

func TestAblationSuiteRequiresAllSixModes(t *testing.T) {
	suite := AblationSuite{ID: "a", DatasetSnapshot: "d", TaskIDs: []string{"t"}, MarshalSHA: "sha", ConfigDigest: "cfg", EnvironmentImage: "img"}
	for _, mode := range optimization.RequiredAblations()[:5] {
		suite.Outcomes = append(suite.Outcomes, AblationOutcome{Mode: mode, TaskID: "t", Outcome: optimization.StatusPass})
	}
	if err := ValidateAblationSuite(suite); !errors.Is(err, ErrIncompleteAblation) {
		t.Fatalf("err=%v", err)
	}
	suite.Outcomes = append(suite.Outcomes, AblationOutcome{Mode: optimization.AblationFullUltra, TaskID: "t", Outcome: optimization.StatusPass})
	if err := ValidateAblationSuite(suite); err != nil {
		t.Fatalf("complete suite rejected: %v", err)
	}
}

func TestRunAblationSuiteExecutesEveryModeAndPreservesOutcomes(t *testing.T) {
	suite := AblationSuite{
		ID:               "run-all-six",
		DatasetSnapshot:  "deterministic-fixture-v1",
		TaskIDs:          []string{"task-a", "task-b"},
		MarshalSHA:       "exact-main",
		ConfigDigest:     "config-digest",
		EnvironmentImage: "fixture",
	}
	called := map[optimization.AblationMode]int{}
	runner := AblationRunnerFunc(func(_ context.Context, _ AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error) {
		called[mode]++
		return []AblationOutcome{
			{Mode: mode, TaskID: "task-a", Outcome: optimization.StatusPass},
			{Mode: mode, TaskID: "task-b", Outcome: optimization.StatusNotRun, FailureReason: "fixture harness deliberately unavailable"},
		}, nil
	})
	now := time.Date(2026, time.September, 9, 0, 0, 0, 0, time.UTC)
	completed, err := RunAblationSuite(context.Background(), runner, suite, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAblationSuite(completed); err != nil {
		t.Fatalf("completed suite invalid: %v", err)
	}
	if got, want := len(completed.Outcomes), len(optimization.RequiredAblations())*len(suite.TaskIDs); got != want {
		t.Fatalf("outcome count=%d, want=%d", got, want)
	}
	for _, mode := range optimization.RequiredAblations() {
		if called[mode] != 1 {
			t.Errorf("mode %s calls=%d, want 1", mode, called[mode])
		}
	}
	for _, outcome := range completed.Outcomes {
		if outcome.ObservedAt != now {
			t.Errorf("outcome %s/%s observed_at=%s, want %s", outcome.Mode, outcome.TaskID, outcome.ObservedAt, now)
		}
	}
}

func TestRunAblationSuiteRejectsOmittedOrDuplicateOutcome(t *testing.T) {
	suite := AblationSuite{ID: "reject-incomplete", DatasetSnapshot: "fixture", TaskIDs: []string{"task-a", "task-b"}, MarshalSHA: "sha", ConfigDigest: "cfg", EnvironmentImage: "fixture"}
	omit := AblationRunnerFunc(func(_ context.Context, _ AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error) {
		return []AblationOutcome{{Mode: mode, TaskID: "task-a", Outcome: optimization.StatusPass}}, nil
	})
	if _, err := RunAblationSuite(context.Background(), omit, suite); !errors.Is(err, ErrMissingOutcome) {
		t.Fatalf("omitted outcome error=%v, want ErrMissingOutcome", err)
	}

	duplicate := AblationRunnerFunc(func(_ context.Context, _ AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error) {
		return []AblationOutcome{
			{Mode: mode, TaskID: "task-a", Outcome: optimization.StatusPass},
			{Mode: mode, TaskID: "task-a", Outcome: optimization.StatusPass},
			{Mode: mode, TaskID: "task-b", Outcome: optimization.StatusPass},
		}, nil
	})
	if _, err := RunAblationSuite(context.Background(), duplicate, suite); !errors.Is(err, ErrDuplicateOutcome) {
		t.Fatalf("duplicate outcome error=%v, want ErrDuplicateOutcome", err)
	}
}

func TestBaselineSuiteKeepsUnavailableHarnessHonest(t *testing.T) {
	suite := BaselineSuite{ID: "b", DatasetSnapshot: "d", TaskIDs: []string{"t"}, EnvironmentImage: "img", MarshalSHA: "sha", Configs: []BaselineConfig{
		{Harness: HarnessCodex, HarnessVersion: "1", Model: "m", ModelVersion: "1", ToolAccess: []string{"shell"}, Budget: TaskBudget{BudgetMicros: 1, WallMillis: 1}},
		{Harness: HarnessAider, HarnessVersion: "1", Model: "m", ModelVersion: "1", ToolAccess: []string{"shell"}, Budget: TaskBudget{BudgetMicros: 1, WallMillis: 1}},
	}}
	if err := ValidateBaselineSuite(suite); err != nil {
		t.Fatal(err)
	}
	if got := RunBaseline(suite.Configs[1], "t", time.Now(), nil); got.Outcome != optimization.StatusNotRun || got.FailureReason == "" {
		t.Fatalf("baseline=%+v", got)
	}
}
