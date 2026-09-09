package bench

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// AblationOutcome is one task's result under one of the six required
// orchestration modes.
type AblationOutcome struct {
	Mode    optimization.AblationMode
	TaskID  string
	Outcome optimization.Status
	// CostMicros, LatencyMillis and ModelCalls are nil until actually
	// measured; a mode that did not run reports none of them.
	CostMicros    *int64
	LatencyMillis *int64
	ModelCalls    *int64
	// Escalated marks a cascade or ULTRA run that escalated beyond its
	// starting model.
	Escalated bool
	// CriticUsed marks a run where a critique pass actually fired.
	CriticUsed    bool
	FailureReason string
	ObservedAt    time.Time
}

// AblationSuite is one run of all six required modes over the same task set
// and pinned versions.
//
// The pack's requirement is comparative: a suite that ran five of the six
// modes cannot support a claim about what routing, cascade, critique or
// verification contributed, because the missing mode is exactly the
// counterfactual the others are measured against.
type AblationSuite struct {
	ID               string
	DatasetSnapshot  string
	TaskIDs          []string
	MarshalSHA       string
	ConfigDigest     string
	EnvironmentImage string
	// Outcomes holds every recorded task outcome across every mode run so
	// far. A suite is complete only once RequiredAblations is fully covered.
	Outcomes []AblationOutcome
}

// ErrIncompleteAblation marks a suite missing one of the six required modes.
var ErrIncompleteAblation = fmt.Errorf("%w: ablation suite does not cover all six required modes", optimization.ErrInvalid)

// ErrDuplicateOutcome marks multiple outcomes reported for the same mode and task.
var ErrDuplicateOutcome = fmt.Errorf("%w: duplicate ablation outcome", optimization.ErrInvalid)

// ErrMissingOutcome marks a mode or task that was not reported by the runner.
var ErrMissingOutcome = fmt.Errorf("%w: missing ablation outcome", ErrIncompleteAblation)

// ErrUnrecognizedOutcome marks an outcome with an invalid mode, task ID, or status.
var ErrUnrecognizedOutcome = fmt.Errorf("%w: unrecognized ablation outcome", optimization.ErrInvalid)

// ModesCovered returns the set of ablation modes with at least one recorded
// outcome in the suite.
func ModesCovered(s AblationSuite) map[optimization.AblationMode]bool {
	covered := map[optimization.AblationMode]bool{}
	for _, o := range s.Outcomes {
		covered[o.Mode] = true
	}
	return covered
}

// MissingModes returns the required modes the suite has not yet covered, in
// the pack's canonical order, so a caller can report exactly what is missing
// rather than just that something is.
func MissingModes(s AblationSuite) []optimization.AblationMode {
	covered := ModesCovered(s)
	var missing []optimization.AblationMode
	for _, m := range optimization.RequiredAblations() {
		if !covered[m] {
			missing = append(missing, m)
		}
	}
	return missing
}

// ValidateAblationSuite refuses an ablation suite that is missing a required mode, is
// unbound, contains duplicate or missing task outcomes, or names an outcome under a mode
// or task the pack does not recognize.
//
// This is the gate the task explicitly calls out: a suite covering five of
// six modes must be rejected as incomplete, not accepted with a caveat,
// because a caveat is easy to drop three drafts later and a rejected suite is
// not.
func ValidateAblationSuite(s AblationSuite) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("%w: ablation suite id", optimization.ErrInvalid)
	}
	if strings.TrimSpace(s.DatasetSnapshot) == "" || len(s.TaskIDs) == 0 {
		return fmt.Errorf("%w: ablation suite names no dataset snapshot or task set", optimization.ErrInvalid)
	}
	if strings.TrimSpace(s.MarshalSHA) == "" || strings.TrimSpace(s.ConfigDigest) == "" {
		return fmt.Errorf("%w: ablation suite is not bound to an exact MARSHAL state", optimization.ErrInvalid)
	}

	validTasks := make(map[string]bool, len(s.TaskIDs))
	for _, tid := range s.TaskIDs {
		clean := strings.TrimSpace(tid)
		if clean == "" {
			return fmt.Errorf("%w: empty task ID in suite task set", optimization.ErrInvalid)
		}
		if validTasks[clean] {
			return fmt.Errorf("%w: duplicate task ID %q in suite task set", optimization.ErrInvalid, clean)
		}
		validTasks[clean] = true
	}

	type modeTaskKey struct {
		mode   optimization.AblationMode
		taskID string
	}
	seen := make(map[modeTaskKey]bool, len(s.Outcomes))
	for _, o := range s.Outcomes {
		if !optimization.ValidAblation(o.Mode) {
			return fmt.Errorf("%w: outcome names unrecognized ablation mode %q", ErrUnrecognizedOutcome, o.Mode)
		}
		if !optimization.ValidStatus(o.Outcome) {
			return fmt.Errorf("%w: outcome for mode %s has invalid status", ErrUnrecognizedOutcome, o.Mode)
		}
		if !validTasks[o.TaskID] {
			return fmt.Errorf("%w: outcome names unrecognized task %q", ErrUnrecognizedOutcome, o.TaskID)
		}
		k := modeTaskKey{mode: o.Mode, taskID: o.TaskID}
		if seen[k] {
			return fmt.Errorf("%w: duplicate outcome for mode %s task %s", ErrDuplicateOutcome, o.Mode, o.TaskID)
		}
		seen[k] = true
	}

	if missing := MissingModes(s); len(missing) > 0 {
		return fmt.Errorf("%w: missing %v", ErrIncompleteAblation, missing)
	}

	for _, m := range optimization.RequiredAblations() {
		for _, tid := range s.TaskIDs {
			if !seen[modeTaskKey{mode: m, taskID: tid}] {
				return fmt.Errorf("%w: mode %s missing outcome for task %s", ErrMissingOutcome, m, tid)
			}
		}
	}

	return nil
}

// AblationRunner is the injectable seam for executing ablation modes across a
// task set.
//
// Implementations execute one required ablation mode across the suite's task
// set and return the observed outcomes. Tests inject a fake runner; production
// or benchmark environments wire an adapter driving real models or harnesses.
type AblationRunner interface {
	RunAblation(ctx context.Context, suite AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error)
}

// AblationRunnerFunc adapts an ordinary function into an AblationRunner.
type AblationRunnerFunc func(ctx context.Context, suite AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error)

// RunAblation calls f(ctx, suite, mode).
func (f AblationRunnerFunc) RunAblation(ctx context.Context, suite AblationSuite, mode optimization.AblationMode) ([]AblationOutcome, error) {
	return f(ctx, suite, mode)
}

// RunAblationSuite executes every optimization.RequiredAblations() mode across
// the supplied deterministic task set using the provided injectable runner.
//
// It captures exactly one outcome per (mode, task), preserves NOT_RUN and
// UNKNOWN truthfully, rejects missing, duplicate, or unrecognized outcomes,
// and validates the completed suite.
func RunAblationSuite(ctx context.Context, r AblationRunner, suite AblationSuite, now ...time.Time) (AblationSuite, error) {
	if r == nil {
		return suite, fmt.Errorf("%w: ablation runner is required", optimization.ErrInvalid)
	}
	if strings.TrimSpace(suite.ID) == "" {
		return suite, fmt.Errorf("%w: ablation suite id", optimization.ErrInvalid)
	}
	if strings.TrimSpace(suite.DatasetSnapshot) == "" || len(suite.TaskIDs) == 0 {
		return suite, fmt.Errorf("%w: ablation suite names no dataset snapshot or task set", optimization.ErrInvalid)
	}
	if strings.TrimSpace(suite.MarshalSHA) == "" || strings.TrimSpace(suite.ConfigDigest) == "" {
		return suite, fmt.Errorf("%w: ablation suite is not bound to an exact MARSHAL state", optimization.ErrInvalid)
	}
	if len(suite.Outcomes) > 0 {
		return suite, fmt.Errorf("%w: initial ablation suite must not contain outcomes before execution", optimization.ErrInvalid)
	}

	validTasks := make(map[string]bool, len(suite.TaskIDs))
	for _, tid := range suite.TaskIDs {
		clean := strings.TrimSpace(tid)
		if clean == "" {
			return suite, fmt.Errorf("%w: empty task ID in suite task set", optimization.ErrInvalid)
		}
		if validTasks[clean] {
			return suite, fmt.Errorf("%w: duplicate task ID %q in suite task set", optimization.ErrInvalid, clean)
		}
		validTasks[clean] = true
	}

	observedAt := time.Now()
	if len(now) > 0 && !now[0].IsZero() {
		observedAt = now[0]
	}

	type modeTaskKey struct {
		mode   optimization.AblationMode
		taskID string
	}
	seen := make(map[modeTaskKey]bool)
	var allOutcomes []AblationOutcome

	for _, mode := range optimization.RequiredAblations() {
		if err := ctx.Err(); err != nil {
			return suite, err
		}

		outcomes, err := r.RunAblation(ctx, suite, mode)
		if err != nil {
			return suite, fmt.Errorf("ablation run failed for mode %s: %w", mode, err)
		}

		seenForMode := make(map[string]bool, len(suite.TaskIDs))
		for _, o := range outcomes {
			if o.Mode != mode {
				return suite, fmt.Errorf("%w: outcome mode %q does not match running mode %q", ErrUnrecognizedOutcome, o.Mode, mode)
			}
			if !optimization.ValidAblation(o.Mode) {
				return suite, fmt.Errorf("%w: unrecognized ablation mode %q", ErrUnrecognizedOutcome, o.Mode)
			}
			if !validTasks[o.TaskID] {
				return suite, fmt.Errorf("%w: unrecognized task ID %q for mode %s", ErrUnrecognizedOutcome, o.TaskID, mode)
			}
			k := modeTaskKey{mode: mode, taskID: o.TaskID}
			if seen[k] || seenForMode[o.TaskID] {
				return suite, fmt.Errorf("%w: duplicate outcome for mode %s task %s", ErrDuplicateOutcome, mode, o.TaskID)
			}
			if !optimization.ValidStatus(o.Outcome) {
				return suite, fmt.Errorf("%w: invalid status %q for mode %s task %s", ErrUnrecognizedOutcome, o.Outcome, mode, o.TaskID)
			}

			// Truthfully preserve NOT_RUN, UNKNOWN, PASS, FAIL, BLOCKED as reported.
			if o.ObservedAt.IsZero() {
				o.ObservedAt = observedAt
			}

			seen[k] = true
			seenForMode[o.TaskID] = true
			allOutcomes = append(allOutcomes, o)
		}

		for _, tid := range suite.TaskIDs {
			if !seenForMode[tid] {
				return suite, fmt.Errorf("%w: mode %s omitted outcome for task %s", ErrMissingOutcome, mode, tid)
			}
		}
	}

	completed := suite
	completed.Outcomes = allOutcomes

	if err := ValidateAblationSuite(completed); err != nil {
		return completed, fmt.Errorf("completed ablation suite validation failed: %w", err)
	}

	return completed, nil
}

// PerModeSummary aggregates outcomes for one ablation mode across the shared
// task set.
type PerModeSummary struct {
	Mode        optimization.AblationMode
	Tasks       int
	Passed      int
	Failed      int
	NotRun      int
	Unknown     int
	Escalations int
	CriticUses  int
	// TotalCostMicros and TotalLatencyMillis are nil unless every contributing
	// task actually reported the metric; a partial sum would misrepresent the
	// mode's real cost.
	TotalCostMicros    *int64
	TotalLatencyMillis *int64
}

// Summarize aggregates a completed suite's outcomes per mode, in the pack's
// canonical mode order.
//
// It does not require ValidateSuite to have passed: a caller auditing a
// broken suite can still see what each present mode looks like, and
// MissingModes tells them what is absent.
func Summarize(s AblationSuite) []PerModeSummary {
	byMode := map[optimization.AblationMode][]AblationOutcome{}
	for _, o := range s.Outcomes {
		byMode[o.Mode] = append(byMode[o.Mode], o)
	}

	var out []PerModeSummary
	for _, mode := range optimization.RequiredAblations() {
		outcomes, ok := byMode[mode]
		if !ok {
			continue
		}
		sum := PerModeSummary{Mode: mode, Tasks: len(outcomes)}
		var costTotal, latencyTotal int64
		costComplete, latencyComplete := true, true
		for _, o := range outcomes {
			switch o.Outcome {
			case optimization.StatusPass:
				sum.Passed++
			case optimization.StatusFail:
				sum.Failed++
			case optimization.StatusNotRun:
				sum.NotRun++
			default:
				sum.Unknown++
			}
			if o.Escalated {
				sum.Escalations++
			}
			if o.CriticUsed {
				sum.CriticUses++
			}
			if o.CostMicros != nil {
				costTotal += *o.CostMicros
			} else {
				costComplete = false
			}
			if o.LatencyMillis != nil {
				latencyTotal += *o.LatencyMillis
			} else {
				latencyComplete = false
			}
		}
		if costComplete && len(outcomes) > 0 {
			sum.TotalCostMicros = &costTotal
		}
		if latencyComplete && len(outcomes) > 0 {
			sum.TotalLatencyMillis = &latencyTotal
		}
		out = append(out, sum)
	}
	return out
}

// ToExperimentResults projects every outcome in the suite into the shared
// experiment shape, tagging each result's TaskClass with its ablation mode so
// downstream comparison logic can group by mode without a separate index.
func ToExperimentResults(s AblationSuite) []optimization.ExperimentResult {
	results := make([]optimization.ExperimentResult, 0, len(s.Outcomes))
	sorted := append([]AblationOutcome(nil), s.Outcomes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Mode != sorted[j].Mode {
			return sorted[i].Mode < sorted[j].Mode
		}
		return sorted[i].TaskID < sorted[j].TaskID
	})
	for i, o := range sorted {
		var metrics []optimization.Metric
		addMetric := func(name, unit string, v *int64) {
			if v == nil {
				return
			}
			f := float64(*v)
			metrics = append(metrics, optimization.Metric{
				Name: name, Value: &f, Unit: unit, Source: "ablation-suite", Method: "harness-reported",
			})
		}
		addMetric("cost_micros", "micros", o.CostMicros)
		addMetric("latency_millis", "ms", o.LatencyMillis)
		addMetric("model_calls", "count", o.ModelCalls)
		results = append(results, optimization.ExperimentResult{
			ID:         fmt.Sprintf("%s:%s:%d", s.ID, o.Mode, i),
			TaskID:     o.TaskID,
			TaskClass:  string(o.Mode),
			Outcome:    o.Outcome,
			Metrics:    metrics,
			ObservedAt: o.ObservedAt,
		})
	}
	return results
}
