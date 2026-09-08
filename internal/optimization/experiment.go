package optimization

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ExperimentResult is one measured outcome for one task under one
// configuration.
//
// Every attempt is recorded, including the failures and the runs that broke.
// Dropping a failed attempt and reporting the rest is how a candidate is made
// to look better than it is, so quarantine and UNKNOWN are states here rather
// than reasons to omit a row.
type ExperimentResult struct {
	ID        string `json:"result_id"`
	TaskID    string `json:"task_id"`
	TaskClass string `json:"task_class"`
	// Attempt distinguishes repeated runs of the same task. Retries are kept,
	// not collapsed into their best outcome.
	Attempt int `json:"attempt"`
	// Outcome is the verified result, not the agent's self-report.
	Outcome Status `json:"outcome"`
	// Regression marks a result worse than the baseline on this task.
	Regression bool `json:"regression"`
	// Quarantined marks a result excluded from analysis because the run was
	// contaminated. QuarantineReason says why; a quarantine without a reason
	// is not accepted.
	Quarantined      bool     `json:"quarantined"`
	QuarantineReason string   `json:"quarantine_reason,omitempty"`
	Metrics          []Metric `json:"metrics,omitempty"`
	// ClusterID groups results sharing an evidence source.
	ClusterID string `json:"cluster_id"`
	// Holdout marks a result from the held-out split.
	Holdout    bool `json:"holdout"`
	ObservedAt time.Time
}

// QuarantineCause names why a result was excluded.
type QuarantineCause string

const (
	CauseNondeterministic QuarantineCause = "TASK_NONDETERMINISM"
	CauseInfraFailure     QuarantineCause = "INFRA_FAILURE"
	CauseRateLimit        QuarantineCause = "RATE_LIMIT"
	CauseFlakyVerifier    QuarantineCause = "FLAKY_VERIFIER"
	CauseTimeout          QuarantineCause = "TIMEOUT_NOISE"
	CauseHarnessBug       QuarantineCause = "HARNESS_BUG"
)

// Quarantine marks a result contaminated, preserving it rather than deleting
// it. A quarantined result is neither a win nor a loss.
func Quarantine(r ExperimentResult, cause QuarantineCause) ExperimentResult {
	r.Quarantined = true
	r.QuarantineReason = string(cause)
	return r
}

// DetectFlaky finds tasks whose repeated attempts disagree.
//
// Disagreement across attempts of the same task under the same configuration
// means the task, the harness or the verifier is nondeterministic. The result
// is not that the good attempt counts; it is that none of them do until the
// nondeterminism is understood.
func DetectFlaky(results []ExperimentResult) map[string]bool {
	outcomes := map[string]map[Status]bool{}
	for _, r := range results {
		if r.Quarantined {
			continue
		}
		if outcomes[r.TaskID] == nil {
			outcomes[r.TaskID] = map[Status]bool{}
		}
		outcomes[r.TaskID][r.Outcome] = true
	}
	flaky := map[string]bool{}
	for task, seen := range outcomes {
		if len(seen) > 1 {
			flaky[task] = true
		}
	}
	return flaky
}

// QuarantineFlaky marks every attempt of a flaky task as quarantined, so a
// nondeterministic task cannot contribute its lucky run.
func QuarantineFlaky(results []ExperimentResult) []ExperimentResult {
	flaky := DetectFlaky(results)
	out := make([]ExperimentResult, 0, len(results))
	for _, r := range results {
		if flaky[r.TaskID] && !r.Quarantined {
			r = Quarantine(r, CauseNondeterministic)
		}
		out = append(out, r)
	}
	return out
}

// BenchmarkKind names a supported external evaluation.
type BenchmarkKind string

const (
	BenchmarkTerminal BenchmarkKind = "TERMINAL_BENCH"
	BenchmarkSWEBench BenchmarkKind = "SWE_BENCH_VERIFIED"
	BenchmarkInternal BenchmarkKind = "INTERNAL"
)

// AblationMode is one of the six required orchestration configurations.
type AblationMode string

const (
	AblationSingleModel     AblationMode = "SINGLE_MODEL"
	AblationRoutingOnly     AblationMode = "ROUTING_ONLY"
	AblationRoutingCascade  AblationMode = "ROUTING_CASCADE"
	AblationRoutingCritique AblationMode = "ROUTING_CRITIQUE"
	AblationRoutingVerify   AblationMode = "ROUTING_VERIFY"
	AblationFullUltra       AblationMode = "FULL_ULTRA"
)

// RequiredAblations lists the six configurations the pack requires.
func RequiredAblations() []AblationMode {
	return []AblationMode{
		AblationSingleModel, AblationRoutingOnly, AblationRoutingCascade,
		AblationRoutingCritique, AblationRoutingVerify, AblationFullUltra,
	}
}

// ValidAblation reports whether m is one of the six required modes.
func ValidAblation(m AblationMode) bool {
	for _, known := range RequiredAblations() {
		if m == known {
			return true
		}
	}
	return false
}

// BenchmarkManifest is the machine-readable provenance for one evaluation run.
//
// Without this, a score is a number with no way to reproduce it, and the pack
// is explicit that insufficient provenance cannot authorize promotion.
type BenchmarkManifest struct {
	ID   string        `json:"manifest_id"`
	Kind BenchmarkKind `json:"benchmark"`
	// Version pins the benchmark itself.
	Version string `json:"benchmark_version"`
	// EvaluatorVersion pins the harness that judged the results. For
	// SWE-bench Verified only the official evaluator's verdict counts.
	EvaluatorVersion string `json:"evaluator_version"`
	// Official marks a run performed with the benchmark's own harness. An
	// unofficial run is never reported as an official score.
	Official bool `json:"official"`
	// DatasetSnapshot identifies the exact task set.
	DatasetSnapshot  string            `json:"dataset_snapshot"`
	TaskIDs          []string          `json:"task_ids"`
	MarshalSHA       string            `json:"marshal_sha"`
	ConfigDigest     string            `json:"config_digest"`
	ModelVersions    map[string]string `json:"model_versions"`
	HarnessVersions  map[string]string `json:"harness_versions"`
	ToolVersions     map[string]string `json:"tool_versions,omitempty"`
	EnvironmentImage string            `json:"environment_image"`
	Seeds            []string          `json:"seeds,omitempty"`
	BudgetMicros     *int64            `json:"budget_micros,omitempty"`
	Mode             AblationMode      `json:"mode,omitempty"`
	StartedAt        time.Time
	FinishedAt       time.Time
	// Exclusions records tasks left out and why. An undisclosed exclusion is
	// how a hard subset quietly disappears from a score.
	Exclusions []Exclusion `json:"exclusions,omitempty"`
	Digest     string      `json:"digest"`
}

// Exclusion records one task omitted from a run, with its reason.
type Exclusion struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// ValidateManifest refuses a manifest that cannot authorize a comparison.
func ValidateManifest(m BenchmarkManifest) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("%w: manifest id", ErrInvalid)
	}
	if strings.TrimSpace(m.Version) == "" || strings.TrimSpace(m.EvaluatorVersion) == "" {
		return fmt.Errorf("%w: benchmark and evaluator must both be pinned", ErrInvalid)
	}
	if strings.TrimSpace(m.DatasetSnapshot) == "" || len(m.TaskIDs) == 0 {
		return fmt.Errorf("%w: manifest names no dataset snapshot or task set", ErrInvalid)
	}
	if strings.TrimSpace(m.MarshalSHA) == "" || strings.TrimSpace(m.ConfigDigest) == "" {
		return fmt.Errorf("%w: manifest is not bound to an exact MARSHAL state", ErrInvalid)
	}
	if strings.TrimSpace(m.EnvironmentImage) == "" {
		return fmt.Errorf("%w: manifest names no environment", ErrInvalid)
	}
	if m.Mode != "" && !ValidAblation(m.Mode) {
		return fmt.Errorf("%w: ablation mode", ErrInvalid)
	}
	for _, e := range m.Exclusions {
		if strings.TrimSpace(e.Reason) == "" {
			return fmt.Errorf("%w: task %s excluded without a reason", ErrInvalid, e.TaskID)
		}
	}
	return nil
}

// NewManifest validates and digests a benchmark manifest.
func NewManifest(m BenchmarkManifest) (BenchmarkManifest, error) {
	if err := ValidateManifest(m); err != nil {
		return BenchmarkManifest{}, err
	}
	sort.Strings(m.TaskIDs)
	sort.Slice(m.Exclusions, func(i, j int) bool { return m.Exclusions[i].TaskID < m.Exclusions[j].TaskID })
	m.Digest = ""
	d, err := digest(m)
	if err != nil {
		return BenchmarkManifest{}, err
	}
	m.Digest = d
	return m, nil
}

// Verify reports whether a stored manifest still matches its digest.
func (m BenchmarkManifest) Verify() error {
	want := m.Digest
	if want == "" {
		return fmt.Errorf("%w: missing digest", ErrInvalid)
	}
	m.Digest = ""
	got, err := digest(m)
	if err != nil {
		return err
	}
	if got != want {
		return ErrTampered
	}
	return nil
}

// OfficialResolution reports whether a benchmark result may be described as an
// official score. Only a run through the benchmark's own evaluator qualifies.
func (m BenchmarkManifest) OfficialResolution() bool {
	return m.Official && m.Kind != BenchmarkInternal && strings.TrimSpace(m.EvaluatorVersion) != ""
}

// Comparison summarizes a paired comparison between a baseline and a candidate
// over the same task set.
type Comparison struct {
	Tasks int `json:"tasks"`
	// Wins, Losses and Ties count paired task outcomes.
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Ties   int `json:"ties"`
	// Unknown counts pairs where one side could not be judged.
	Unknown int `json:"unknown"`
	// Excluded counts pairs dropped because a result was quarantined.
	Excluded int `json:"excluded"`
	// EffectSize is the win-rate difference over judged pairs. It is nil when
	// too few pairs were judged to compute one honestly.
	EffectSize *float64 `json:"effect_size,omitempty"`
	// Interval is a bootstrap-free normal-approximation interval, present only
	// when the sample supports it.
	IntervalLow  *float64 `json:"interval_low,omitempty"`
	IntervalHigh *float64 `json:"interval_high,omitempty"`
	// WeakEvidence marks a comparison whose sample is too small to generalize.
	// It travels with the result rather than being left for a reader to infer.
	WeakEvidence bool `json:"weak_evidence"`
}

// minPairsForInterval is the smallest judged sample for which this code will
// report an interval. Below it, precision would be theatre.
const minPairsForInterval = 20

// ComparePaired runs a paired comparison over matched task results.
//
// Pairs where either side is quarantined or UNKNOWN are excluded from the win
// count and reported separately, so a comparison cannot be strengthened by
// discarding the runs that went badly.
func ComparePaired(baseline, candidate []ExperimentResult) Comparison {
	byTask := map[string]ExperimentResult{}
	for _, r := range baseline {
		byTask[r.TaskID] = r
	}

	var c Comparison
	for _, cand := range candidate {
		base, ok := byTask[cand.TaskID]
		if !ok {
			continue
		}
		c.Tasks++
		if base.Quarantined || cand.Quarantined {
			c.Excluded++
			continue
		}
		if base.Outcome == StatusUnknown || cand.Outcome == StatusUnknown ||
			base.Outcome == StatusNotRun || cand.Outcome == StatusNotRun {
			c.Unknown++
			continue
		}
		basePass := base.Outcome == StatusPass
		candPass := cand.Outcome == StatusPass
		switch {
		case candPass && !basePass:
			c.Wins++
		case basePass && !candPass:
			c.Losses++
		default:
			c.Ties++
		}
	}

	judged := c.Wins + c.Losses + c.Ties
	if judged == 0 {
		c.WeakEvidence = true
		return c
	}
	effect := float64(c.Wins-c.Losses) / float64(judged)
	c.EffectSize = &effect

	if judged < minPairsForInterval {
		// The sample is too small for an interval that would mean anything.
		// Saying so is more useful than printing a wide one.
		c.WeakEvidence = true
		return c
	}
	// Normal approximation on the paired win-rate difference.
	p := float64(c.Wins) / float64(judged)
	q := float64(c.Losses) / float64(judged)
	variance := (p + q - (p-q)*(p-q)) / float64(judged)
	if variance < 0 {
		variance = 0
	}
	margin := 1.96 * math.Sqrt(variance)
	low, high := effect-margin, effect+margin
	c.IntervalLow, c.IntervalHigh = &low, &high
	return c
}

// Improved reports whether the comparison shows a real improvement.
//
// A positive effect size is not enough on a weak sample: one task win is not
// global superiority, and an interval spanning zero has not distinguished the
// candidate from the baseline.
func (c Comparison) Improved() bool {
	if c.EffectSize == nil || *c.EffectSize <= 0 {
		return false
	}
	if c.WeakEvidence {
		return false
	}
	if c.IntervalLow != nil && *c.IntervalLow <= 0 {
		return false
	}
	return true
}
