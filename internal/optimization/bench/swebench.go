package bench

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// SWEBenchConfig pins the dataset and evaluator versions a SWE-bench Verified
// run is bound to.
//
// SWE-bench Verified's whole point is that resolution is decided by a fixed,
// versioned test harness the task authors control. A config that does not pin
// EvaluatorVersion cannot be reproduced even if the dataset snapshot matches,
// because the evaluator itself has changed between releases.
type SWEBenchConfig struct {
	DatasetVersion   string
	EvaluatorVersion string
	// EvaluatorPath is where the official evaluator is expected. Empty means
	// "look on PATH".
	EvaluatorPath   string
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

// Resolution is a SWE-bench outcome's provenance, kept as its own type so an
// unofficial run can never be assigned to the same field an official verdict
// occupies. The pack is explicit: only the official evaluator's verdict
// counts as resolution. Making that a distinct type, rather than a bool next
// to the outcome, means a caller cannot accidentally read an unofficial
// result where an official one is required — the type itself has to be
// unwrapped through OfficialOutcome first.
type Resolution struct {
	// resolved is unexported: it may only be set through NewOfficialResolution
	// or NewUnofficialResolution, so its Official flag can never drift from
	// how it was actually produced.
	resolved bool
	official bool
	// Reason records why an unofficial run is unofficial, or why an official
	// one is unresolved.
	reason string
}

// NewOfficialResolution wraps a verdict produced by the official evaluator.
func NewOfficialResolution(resolved bool) Resolution {
	return Resolution{resolved: resolved, official: true}
}

// NewUnofficialResolution wraps a verdict produced by anything else: a local
// test run, a partial harness, a human read of the diff. It can never report
// Official() true, no matter what the caller believes about its accuracy.
func NewUnofficialResolution(resolved bool, reason string) Resolution {
	return Resolution{resolved: resolved, official: false, reason: reason}
}

// Official reports whether this verdict came from the benchmark's own
// evaluator. Only then does Resolved() mean what SWE-bench Verified means by
// "resolved."
func (r Resolution) Official() bool { return r.official }

// Resolved reports the raw verdict. Callers that need an official resolution
// must check Official() first; OfficialOutcome does that for them.
func (r Resolution) Resolved() bool { return r.resolved }

// Reason explains an unofficial resolution.
func (r Resolution) Reason() string { return r.reason }

// OfficialOutcome converts a Resolution into the shared Status vocabulary,
// refusing to report a score for anything that was not judged by the official
// harness.
//
// This is the single choke point every caller must go through to turn a
// SWE-bench verdict into a status. An unofficial run comes back UNKNOWN
// regardless of its own resolved/unresolved reading, because reporting it as
// PASS or FAIL would let it stand in for an official score it never earned.
func OfficialOutcome(r Resolution) optimization.Status {
	if !r.Official() {
		return optimization.StatusUnknown
	}
	if r.Resolved() {
		return optimization.StatusPass
	}
	return optimization.StatusFail
}

// InstanceResult is one SWE-bench Verified instance outcome.
type InstanceResult struct {
	InstanceID string
	Resolution Resolution
	// PatchRef points at the artifact holding the generated patch, so a
	// disputed resolution can be checked against exactly what was applied.
	PatchRef string
	// VerifierLog points at the official evaluator's own output for this
	// instance.
	VerifierLog string
	Tokens      *int64
	CostMicros  *int64
	WallMillis  *int64
	// Retries counts additional attempts on this instance; Escalations counts
	// times the route escalated to a stronger model or a human.
	Retries       int
	Escalations   int
	FailureReason string
	// ReproCommand is the exact command that reproduces this instance's run,
	// required by the pack's reproducibility metadata.
	ReproCommand string
	ObservedAt   time.Time
}

// Runner mirrors the terminal-bench Runner shape for SWE-bench: a probe that
// checks availability without doing work, and an execution step that only
// runs once the probe says the official evaluator is present.
type SWERunner interface {
	Probe(ctx context.Context, cfg SWEBenchConfig) (Probe, error)
	RunInstance(ctx context.Context, cfg SWEBenchConfig, instanceID string) (RawInstanceResult, error)
}

// RawInstanceResult is what the official evaluator reported for one instance,
// before this package wraps it in a type-safe Resolution.
type RawInstanceResult struct {
	// Resolved is nil until the official evaluator actually reports a
	// verdict. It is never defaulted to false: an evaluator that crashed
	// before judging an instance did not resolve it as failing, it simply did
	// not judge it.
	Resolved      *bool
	PatchRef      string
	VerifierLog   string
	Tokens        *int64
	CostMicros    *int64
	WallMillis    *int64
	Retries       int
	Escalations   int
	FailureReason string
	ReproCommand  string
}

// ExecSWERunner shells out to the official SWE-bench Verified evaluator. It
// never synthesizes a verdict; RunInstance is left unimplemented in this
// repository for the same reason ExecRunner.RunTask is: without the actual
// evaluator to test against, any output-parsing code here would be a guess
// this package is built specifically to refuse.
type ExecSWERunner struct {
	LookPath func(string) (string, error)
}

// NewExecSWERunner returns a SWERunner backed by the real OS process and PATH.
func NewExecSWERunner() *ExecSWERunner {
	return &ExecSWERunner{LookPath: exec.LookPath}
}

// Probe checks whether the official SWE-bench Verified evaluator is reachable.
func (r *ExecSWERunner) Probe(_ context.Context, cfg SWEBenchConfig) (Probe, error) {
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	name := cfg.EvaluatorPath
	if strings.TrimSpace(name) == "" {
		name = "swebench"
	}
	path, err := lookPath(name)
	if err != nil || strings.TrimSpace(path) == "" {
		return Probe{
			Available: false,
			Reason:    fmt.Sprintf("official SWE-bench evaluator %q not found on PATH: %v", name, err),
		}, nil
	}
	if strings.TrimSpace(cfg.DatasetSnapshot) == "" {
		return Probe{Available: false, Reason: "no dataset snapshot configured"}, nil
	}
	return Probe{Available: true, Version: cfg.EvaluatorVersion}, nil
}

// RunInstance is intentionally unimplemented here; see ExecSWERunner's doc.
func (r *ExecSWERunner) RunInstance(_ context.Context, _ SWEBenchConfig, instanceID string) (RawInstanceResult, error) {
	return RawInstanceResult{}, fmt.Errorf("swebench: RunInstance not implemented in this environment (instance %s)", instanceID)
}

// ValidSWEConfig refuses a config that could not authorize a comparison.
func ValidSWEConfig(cfg SWEBenchConfig) error {
	if strings.TrimSpace(cfg.DatasetVersion) == "" || strings.TrimSpace(cfg.EvaluatorVersion) == "" {
		return fmt.Errorf("%w: swe-bench dataset and evaluator version must both be pinned", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.DatasetSnapshot) == "" || len(cfg.TaskIDs) == 0 {
		return fmt.Errorf("%w: swe-bench config names no dataset snapshot or instance set", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.MarshalSHA) == "" || strings.TrimSpace(cfg.ConfigDigest) == "" {
		return fmt.Errorf("%w: swe-bench config is not bound to an exact MARSHAL state", optimization.ErrInvalid)
	}
	if strings.TrimSpace(cfg.EnvironmentImage) == "" {
		return fmt.Errorf("%w: swe-bench config names no environment", optimization.ErrInvalid)
	}
	if !cfg.Route.Valid() {
		return fmt.Errorf("%w: swe-bench config route is not exact", optimization.ErrInvalid)
	}
	return nil
}

// RunSWEBench evaluates cfg.TaskIDs (SWE-bench instance IDs) through r.
//
// When the official evaluator is unavailable, every instance comes back
// NOT_RUN. An unofficial local check, if one were run instead, would not
// belong in this function at all: OfficialOutcome refuses to promote its
// verdict to PASS/FAIL, so mixing it in here would only relabel NOT_RUN as
// UNKNOWN without adding anything true.
func RunSWEBench(ctx context.Context, r SWERunner, cfg SWEBenchConfig, now time.Time) (optimization.BenchmarkManifest, []InstanceResult, error) {
	if err := ValidSWEConfig(cfg); err != nil {
		return optimization.BenchmarkManifest{}, nil, err
	}

	manifest, err := optimization.NewManifest(optimization.BenchmarkManifest{
		ID:               cfg.ConfigDigest + ":" + cfg.DatasetSnapshot,
		Kind:             optimization.BenchmarkSWEBench,
		Version:          cfg.DatasetVersion,
		EvaluatorVersion: cfg.EvaluatorVersion,
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
		return manifest, nil, fmt.Errorf("swebench: probe failed: %w", err)
	}
	if !probe.Available {
		results := make([]InstanceResult, 0, len(cfg.TaskIDs))
		for _, id := range cfg.TaskIDs {
			results = append(results, InstanceResult{
				InstanceID:    id,
				Resolution:    NewUnofficialResolution(false, "official evaluator unavailable: "+probe.Reason),
				FailureReason: "official evaluator unavailable: " + probe.Reason,
				ObservedAt:    now,
			})
		}
		manifest.FinishedAt = now
		return manifest, results, nil
	}

	results := make([]InstanceResult, 0, len(cfg.TaskIDs))
	for _, id := range cfg.TaskIDs {
		raw, err := r.RunInstance(ctx, cfg, id)
		if err != nil {
			results = append(results, InstanceResult{
				InstanceID:    id,
				Resolution:    NewUnofficialResolution(false, err.Error()),
				FailureReason: err.Error(),
				ObservedAt:    now,
			})
			continue
		}
		results = append(results, instanceFromRaw(id, raw, now))
	}
	manifest.FinishedAt = now
	return manifest, results, nil
}

func instanceFromRaw(instanceID string, raw RawInstanceResult, now time.Time) InstanceResult {
	var resolution Resolution
	if raw.Resolved == nil {
		// The evaluator ran but did not produce a verdict for this instance.
		// That is not the same as "unresolved" and must not be read as a
		// failure.
		resolution = NewUnofficialResolution(false, "official evaluator produced no verdict for this instance")
	} else {
		resolution = NewOfficialResolution(*raw.Resolved)
	}
	return InstanceResult{
		InstanceID:    instanceID,
		Resolution:    resolution,
		PatchRef:      raw.PatchRef,
		VerifierLog:   raw.VerifierLog,
		Tokens:        raw.Tokens,
		CostMicros:    raw.CostMicros,
		WallMillis:    raw.WallMillis,
		Retries:       raw.Retries,
		Escalations:   raw.Escalations,
		FailureReason: raw.FailureReason,
		ReproCommand:  raw.ReproCommand,
		ObservedAt:    now,
	}
}

// ToExperimentResult projects a SWE-bench InstanceResult into the shared
// experiment shape. The outcome always goes through OfficialOutcome, so an
// unofficial resolution can never leak into promotion evidence as a PASS.
func (ir InstanceResult) ToExperimentResult(resultID string) optimization.ExperimentResult {
	var metrics []optimization.Metric
	addMetric := func(name, unit string, v *int64) {
		if v == nil {
			return
		}
		f := float64(*v)
		metrics = append(metrics, optimization.Metric{
			Name: name, Value: &f, Unit: unit, Source: "swe-bench-verified", Method: "official-evaluator",
		})
	}
	addMetric("tokens", "count", ir.Tokens)
	addMetric("cost_micros", "micros", ir.CostMicros)
	addMetric("wall_millis", "ms", ir.WallMillis)
	retries := float64(ir.Retries)
	escalations := float64(ir.Escalations)
	metrics = append(metrics,
		optimization.Metric{Name: "retries", Value: &retries, Unit: "count", Source: "swe-bench-verified"},
		optimization.Metric{Name: "escalations", Value: &escalations, Unit: "count", Source: "swe-bench-verified"},
	)

	// An unofficial resolution with a failure reason means the run never
	// reached a verdict at all (evaluator missing, runner errored): that is
	// NOT_RUN. An unofficial resolution without one would mean this package
	// itself built a Resolution incorrectly, which OfficialOutcome still
	// refuses to promote to PASS/FAIL — it comes back UNKNOWN instead.
	outcome := OfficialOutcome(ir.Resolution)
	if !ir.Resolution.Official() && ir.FailureReason != "" {
		outcome = optimization.StatusNotRun
	}
	return optimization.ExperimentResult{
		ID:         resultID,
		TaskID:     ir.InstanceID,
		Outcome:    outcome,
		Metrics:    metrics,
		ObservedAt: ir.ObservedAt,
	}
}
