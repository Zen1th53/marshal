// Package bench adapts external benchmarks and internal ablations into
// Process 08 evidence.
//
// This layer is a harness, not a scoreboard. Its job is to pin versions,
// probe whether an evaluation can actually run here, parse whatever the
// underlying tool produced, and say NOT_RUN when it did not run. A benchmark
// adapter that fills in a plausible number when the harness is absent is not
// an adapter; it is a fabricated result wearing this package's name.
package bench

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// TerminalConfig pins everything a Terminal-Bench run must be bound to before
// it can be compared against anything else.
//
// A benchmark version without a harness version is not reproducible: the same
// tasks judged by two different Harbor releases can disagree, and the
// disagreement would be invisible without both pins recorded.
type TerminalConfig struct {
	BenchmarkVersion string
	HarnessVersion   string
	// HarnessPath is where the Harbor/custom-agent binary is expected. Empty
	// means "look on PATH".
	HarnessPath string
	// DatasetSnapshot identifies the exact task set being run.
	DatasetSnapshot string
	TaskIDs         []string
	MarshalSHA      string
	ConfigDigest    string
	EnvironmentImage string
	Route            optimization.Route
	ModelVersions    map[string]string
	HarnessVersions  map[string]string
	Seeds            []string
	BudgetMicros     *int64
	Mode             optimization.AblationMode
}

// TerminalRun is one Terminal-Bench task outcome, recorded honestly whether or
// not the run actually executed.
type TerminalRun struct {
	Manifest optimization.BenchmarkManifest
	TaskID   string
	// Outcome is the verified result. It is StatusNotRun, never a guessed
	// pass or fail, when the harness could not be invoked.
	Outcome optimization.Status
	// Reward and Success are Terminal-Bench's own reported metrics. Reward is
	// nil until the harness actually reports one.
	Reward  *float64
	Success *bool
	// Tokens, CostMicros, WallMillis and ModelCalls come from the harness or
	// the provider, never estimated.
	Tokens     *int64
	CostMicros *int64
	WallMillis *int64
	ModelCalls *int64
	// UsedRouting, UsedCritic and UsedVerify record which MARSHAL layers were
	// active for this task, so an ablation can be told apart from a raw run.
	UsedRouting bool
	UsedCritic  bool
	UsedVerify  bool
	// FailureReason is required whenever Outcome is not PASS. A failed or
	// unrun task with no stated reason is not a usable record.
	FailureReason string
	// ArtifactPaths point at the raw harness output backing this row, so a
	// disputed number can be checked against the source.
	ArtifactPaths []string
	ObservedAt    time.Time
}

// Runner is the seam between this adapter and an actual Terminal-Bench
// invocation. Tests substitute a fake; production wires in a process runner
// that shells out to Harbor.
type Runner interface {
	// Probe reports whether the harness is available to run at all, and its
	// reported version. It must not attempt to execute a task.
	Probe(ctx context.Context, cfg TerminalConfig) (Probe, error)
	// RunTask executes exactly one task and returns its raw result. It is
	// only called after Probe reports availability.
	RunTask(ctx context.Context, cfg TerminalConfig, taskID string) (RawTaskResult, error)
}

// Probe is what an environment check found, independent of any task outcome.
type Probe struct {
	Available bool
	Version   string
	// Reason explains an unavailable harness: binary missing, dataset
	// missing, version mismatch. It is what NOT_RUN reports back.
	Reason string
}

// RawTaskResult is whatever the underlying harness reported for one task,
// before this package turns it into a TerminalRun. Fields are pointers so an
// unreported value stays unreported.
type RawTaskResult struct {
	Success       *bool
	Reward        *float64
	Tokens        *int64
	CostMicros    *int64
	WallMillis    *int64
	ModelCalls    *int64
	FailureReason string
	ArtifactPaths []string
}

// ExecRunner shells out to a Terminal-Bench-compatible binary on disk. It is
// the production Runner; it never invents a result the process did not print.
type ExecRunner struct {
	// LookPath is overridable in tests; exec.LookPath in production.
	LookPath func(string) (string, error)
}

// NewExecRunner returns a Runner backed by the real OS process and PATH.
func NewExecRunner() *ExecRunner {
	return &ExecRunner{LookPath: exec.LookPath}
}

// Probe checks whether the Harbor/custom-agent binary is reachable. It never
// runs a task: a probe that executes work to check availability is not a
// cheap, side-effect-free check any more.
func (r *ExecRunner) Probe(_ context.Context, cfg TerminalConfig) (Probe, error) {
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	name := cfg.HarnessPath
	if strings.TrimSpace(name) == "" {
		name = "harbor"
	}
	path, err := lookPath(name)
	if err != nil || strings.TrimSpace(path) == "" {
		return Probe{
			Available: false,
			Reason:    fmt.Sprintf("terminal-bench harness %q not found on PATH: %v", name, err),
		}, nil
	}
	if strings.TrimSpace(cfg.DatasetSnapshot) == "" {
		return Probe{Available: false, Reason: "no dataset snapshot configured"}, nil
	}
	return Probe{Available: true, Version: cfg.HarnessVersion}, nil
}

// RunTask is not implemented by ExecRunner in this repository: invoking the
// real Harbor CLI, parsing its output format and mapping exit codes belongs
// to an integration that has an actual binary to test against. Calling it
// here would force a choice between not building this adapter at all and
// guessing at an output schema this repo cannot verify, and guessing is the
// one thing this package refuses to do.
func (r *ExecRunner) RunTask(_ context.Context, _ TerminalConfig, taskID string) (RawTaskResult, error) {
	return RawTaskResult{}, fmt.Errorf("terminal-bench: RunTask not implemented in this environment (task %s)", taskID)
}

// ValidConfig refuses a config missing the pins a reproducible run needs.
func ValidConfig(cfg TerminalConfig) error {
	if strings.TrimSpace(cfg.BenchmarkVersion) == "" || strings.TrimSpace(cfg.HarnessVersion) == "" {
		return fmt.Errorf("%w: terminal-bench and harness version must both be pinned", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.DatasetSnapshot) == "" || len(cfg.TaskIDs) == 0 {
		return fmt.Errorf("%w: terminal-bench config names no dataset snapshot or task set", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.MarshalSHA) == "" || strings.TrimSpace(cfg.ConfigDigest) == "" {
		return fmt.Errorf("%w: terminal-bench config is not bound to an exact MARSHAL state", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.EnvironmentImage) == "" {
		return fmt.Errorf("%w: terminal-bench config names no environment", optimization.ErrInvalid)
	}
	if !cfg.Route.Valid() {
		return fmt.Errorf("%w: terminal-bench config route is not exact", optimization.ErrInvalid)
	}
	return nil
}

// notRunResult builds the single row that stands in for a task that could not
// be executed. It carries the reason forward rather than an empty record.
func notRunResult(taskID, reason string, now time.Time) TerminalRun {
	return TerminalRun{
		TaskID:        taskID,
		Outcome:       optimization.StatusNotRun,
		FailureReason: reason,
		ObservedAt:    now,
	}
}

// RunTerminalBench evaluates cfg.TaskIDs through r and returns one TerminalRun
// per task plus the manifest binding them together.
//
// When the environment cannot run the benchmark at all, every task comes back
// NOT_RUN with the probe's reason. This is the honest outcome the pack
// requires: a missing harness is not a benchmark failure and it is certainly
// not a benchmark pass, so it gets its own status rather than being folded
// into either.
func RunTerminalBench(ctx context.Context, r Runner, cfg TerminalConfig, now time.Time) (optimization.BenchmarkManifest, []TerminalRun, error) {
	if err := ValidConfig(cfg); err != nil {
		return optimization.BenchmarkManifest{}, nil, err
	}

	manifest, err := optimization.NewManifest(optimization.BenchmarkManifest{
		ID:               cfg.ConfigDigest + ":" + cfg.DatasetSnapshot,
		Kind:             optimization.BenchmarkTerminal,
		Version:          cfg.BenchmarkVersion,
		EvaluatorVersion: cfg.HarnessVersion,
		// Terminal-Bench has no separate "official" evaluator distinct from
		// its own harness the way SWE-bench does; running its harness at all
		// is the official path.
		Official:         true,
		DatasetSnapshot:  cfg.DatasetSnapshot,
		TaskIDs:          cfg.TaskIDs,
		MarshalSHA:       cfg.MarshalSHA,
		ConfigDigest:     cfg.ConfigDigest,
		ModelVersions:    cfg.ModelVersions,
		HarnessVersions:  cfg.HarnessVersions,
		EnvironmentImage: cfg.EnvironmentImage,
		Seeds:            cfg.Seeds,
		BudgetMicros:     cfg.BudgetMicros,
		Mode:             cfg.Mode,
		StartedAt:        now,
	})
	if err != nil {
		return optimization.BenchmarkManifest{}, nil, err
	}

	probe, err := r.Probe(ctx, cfg)
	if err != nil {
		return manifest, nil, fmt.Errorf("terminal-bench: probe failed: %w", err)
	}
	if !probe.Available {
		runs := make([]TerminalRun, 0, len(cfg.TaskIDs))
		for _, id := range cfg.TaskIDs {
			runs = append(runs, notRunResult(id, "terminal-bench harness unavailable: "+probe.Reason, now))
		}
		manifest.FinishedAt = now
		return manifest, runs, nil
	}

	runs := make([]TerminalRun, 0, len(cfg.TaskIDs))
	for _, id := range cfg.TaskIDs {
		raw, err := r.RunTask(ctx, cfg, id)
		if err != nil {
			runs = append(runs, notRunResult(id, err.Error(), now))
			continue
		}
		runs = append(runs, terminalRunFromRaw(id, cfg, raw, now))
	}
	manifest.FinishedAt = now
	return manifest, runs, nil
}

func terminalRunFromRaw(taskID string, cfg TerminalConfig, raw RawTaskResult, now time.Time) TerminalRun {
	outcome := optimization.StatusUnknown
	switch {
	case raw.Success == nil:
		outcome = optimization.StatusUnknown
	case *raw.Success:
		outcome = optimization.StatusPass
	default:
		outcome = optimization.StatusFail
	}
	return TerminalRun{
		TaskID:        taskID,
		Outcome:       outcome,
		Reward:        raw.Reward,
		Success:       raw.Success,
		Tokens:        raw.Tokens,
		CostMicros:    raw.CostMicros,
		WallMillis:    raw.WallMillis,
		ModelCalls:    raw.ModelCalls,
		UsedRouting:   cfg.Mode != "" && cfg.Mode != optimization.AblationSingleModel,
		UsedCritic:    cfg.Mode == optimization.AblationRoutingCritique || cfg.Mode == optimization.AblationFullUltra,
		UsedVerify:    cfg.Mode == optimization.AblationRoutingVerify || cfg.Mode == optimization.AblationFullUltra,
		FailureReason: raw.FailureReason,
		ArtifactPaths: raw.ArtifactPaths,
		ObservedAt:    now,
	}
}

// ToExperimentResult projects a TerminalRun into the shared experiment shape
// so Terminal-Bench evidence can flow through the same comparison and
// promotion logic as any other Process 08 result.
func ToExperimentResult(run TerminalRun, resultID string) optimization.ExperimentResult {
	var metrics []optimization.Metric
	addMetric := func(name, unit string, v *int64) {
		if v == nil {
			return
		}
		f := float64(*v)
		metrics = append(metrics, optimization.Metric{
			Name: name, Value: &f, Unit: unit, Source: "terminal-bench", Method: "harness-reported",
		})
	}
	addMetric("tokens", "count", run.Tokens)
	addMetric("cost_micros", "micros", run.CostMicros)
	addMetric("wall_millis", "ms", run.WallMillis)
	addMetric("model_calls", "count", run.ModelCalls)
	if run.Reward != nil {
		v := *run.Reward
		metrics = append(metrics, optimization.Metric{
			Name: "reward", Value: &v, Source: "terminal-bench", Method: "harness-reported",
		})
	}
	return optimization.ExperimentResult{
		ID:         resultID,
		TaskID:     run.TaskID,
		Outcome:    run.Outcome,
		Metrics:    metrics,
		ObservedAt: run.ObservedAt,
	}
}
