package optimization

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Rollout covers the path a promoted candidate takes into real use: shadow
// evaluation that controls nothing, a bounded canary that controls a little,
// and a rollback guard that can take it all back.
//
// The ordering is deliberate. Nothing reaches live traffic without first having
// been evaluated where it could not do damage, and nothing stays in live
// traffic without a trigger that removes it.

// ShadowPolicy bounds an evaluation that runs alongside live work.
//
// Shadow output is scoped evidence and nothing more. A shadow route never
// controls an action, so the question it answers is "what would this have
// decided", not "what should happen next".
type ShadowPolicy struct {
	// MaxCostMicros caps what shadow evaluation may spend. Shadow work is
	// pure overhead on live traffic, so it is bounded rather than best-effort.
	MaxCostMicros int64 `json:"max_cost_micros"`
	// EligibleTaskClasses limits shadow evaluation to task classes where the
	// extra provider exposure is acceptable.
	EligibleTaskClasses []string `json:"eligible_task_classes"`
	// SecretAccess must stay false. A shadow provider is an additional party;
	// sending it secret material widens the blast radius of an experiment.
	SecretAccess bool `json:"secret_access"`
	// AllowExternalEffects must stay false. A shadow run that writes to the
	// world is not a shadow run.
	AllowExternalEffects bool `json:"allow_external_effects"`
}

// ValidateShadow refuses a shadow configuration that could act or leak.
func ValidateShadow(p ShadowPolicy, taskClass string) error {
	if p.SecretAccess {
		return fmt.Errorf("%w: shadow evaluation requested secret access", ErrVeto)
	}
	if p.AllowExternalEffects {
		return fmt.Errorf("%w: shadow evaluation requested external effects", ErrVeto)
	}
	if p.MaxCostMicros <= 0 {
		return fmt.Errorf("%w: shadow evaluation needs a cost bound", ErrInvalid)
	}
	if len(p.EligibleTaskClasses) == 0 {
		return fmt.Errorf("%w: shadow evaluation needs an eligible task scope", ErrInvalid)
	}
	for _, eligible := range p.EligibleTaskClasses {
		if eligible == taskClass {
			return nil
		}
	}
	return fmt.Errorf("%w: task class %s is outside the shadow scope", ErrVeto, taskClass)
}

// CanaryState is where a bounded rollout currently stands.
type CanaryState string

const (
	CanaryPending    CanaryState = "PENDING"
	CanaryRunning    CanaryState = "RUNNING"
	CanaryCompleted  CanaryState = "COMPLETED"
	CanaryRolledBack CanaryState = "ROLLED_BACK"
	// CanaryHalted marks a canary stopped without a completed rollback, which
	// needs operator attention rather than silent completion.
	CanaryHalted CanaryState = "HALTED"
)

// Canary is a bounded rollout of one promoted candidate.
type Canary struct {
	ID          string `json:"canary_id"`
	CandidateID string `json:"candidate_id"`
	BaselineID  string `json:"baseline_id"`
	// Exposure is the fraction of eligible traffic the candidate controls.
	Exposure float64 `json:"exposure"`
	// EligibleTaskClasses bounds which work the canary may touch at all.
	EligibleTaskClasses []string `json:"eligible_task_classes"`
	// MaxTasks and Deadline bound the canary in both work and time, so a
	// forgotten canary expires rather than becoming a silent global rollout.
	MaxTasks int       `json:"max_tasks"`
	Deadline time.Time `json:"deadline"`
	// RollbackTriggers are the conditions that end the canary automatically.
	RollbackTriggers []Trigger `json:"rollback_triggers"`
	// RequiresFullReview marks a security-sensitive or critical change, which
	// cannot ride a canary into production on evidence alone.
	RequiresFullReview bool        `json:"requires_full_review"`
	State              CanaryState `json:"state"`
	StartedAt          time.Time
	// Results accumulate as the canary runs. Every attempt is kept.
	Results []ExperimentResult `json:"results,omitempty"`
	// RollbackReason records why a canary was taken back.
	RollbackReason string `json:"rollback_reason,omitempty"`
}

// Trigger is a condition that ends a canary and restores the baseline.
type Trigger struct {
	Metric string `json:"metric"`
	// Threshold is the value at which the trigger fires.
	Threshold float64 `json:"threshold"`
	// HigherIsWorse orients the comparison for this metric.
	HigherIsWorse bool   `json:"higher_is_worse"`
	Reason        string `json:"reason"`
}

// ValidateCanary refuses a rollout that is not actually bounded.
//
// Each requirement here exists because its absence turns a canary into an
// unmonitored global change: no scope means everything, no cap means forever,
// no trigger means nothing stops it.
func ValidateCanary(c Canary, g Governance) error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.CandidateID) == "" {
		return fmt.Errorf("%w: canary identity", ErrInvalid)
	}
	if strings.TrimSpace(c.BaselineID) == "" {
		return fmt.Errorf("%w: canary needs a baseline to compare against", ErrInvalid)
	}
	if c.Exposure <= 0 {
		return fmt.Errorf("%w: canary needs a positive exposure", ErrInvalid)
	}
	if g.MaxCanaryExposure > 0 && c.Exposure > g.MaxCanaryExposure {
		return fmt.Errorf("%w: exposure %.2f exceeds the governed maximum %.2f",
			ErrVeto, c.Exposure, g.MaxCanaryExposure)
	}
	if c.Exposure >= 1.0 {
		return fmt.Errorf("%w: a canary controlling all traffic is a global rollout", ErrVeto)
	}
	if len(c.EligibleTaskClasses) == 0 {
		return fmt.Errorf("%w: canary needs an eligible task scope", ErrInvalid)
	}
	if c.MaxTasks <= 0 {
		return fmt.Errorf("%w: canary needs a task bound", ErrInvalid)
	}
	if c.Deadline.IsZero() {
		return fmt.Errorf("%w: canary needs a deadline", ErrInvalid)
	}
	if len(c.RollbackTriggers) == 0 {
		return fmt.Errorf("%w: canary needs at least one rollback trigger", ErrInvalid)
	}
	if c.RequiresFullReview {
		// A security-sensitive change does not get to prove itself in
		// production. It goes back through the lifecycle.
		return fmt.Errorf("%w: security-sensitive change requires the full Process 03 to 06 flow, not a canary", ErrVeto)
	}
	return nil
}

// GuardResult is the outcome of evaluating a canary's rollback guard.
type GuardResult struct {
	// Rollback reports whether the canary must be taken back now.
	Rollback bool `json:"rollback"`
	// Reasons names every trigger that fired.
	Reasons []string `json:"reasons,omitempty"`
	// Expired reports that the canary hit its task or time bound.
	Expired bool `json:"expired"`
}

// EvaluateGuard decides whether a running canary must be rolled back.
//
// A security failure or a verified-outcome regression rolls back immediately;
// those are not traded against a cost improvement. Expiry ends the canary
// without rolling it back, because reaching the bound is success, not failure.
func EvaluateGuard(c Canary, observed map[string]float64, now time.Time) GuardResult {
	var result GuardResult

	for _, t := range c.RollbackTriggers {
		value, ok := observed[t.Metric]
		if !ok {
			// A trigger whose metric was never measured cannot clear the
			// canary. It is reported so the gap is visible rather than
			// treated as a pass.
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("trigger metric %s was not measured", t.Metric))
			result.Rollback = true
			continue
		}
		fired := value > t.Threshold
		if !t.HigherIsWorse {
			fired = value < t.Threshold
		}
		if fired {
			result.Rollback = true
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("%s: %s (%.4f against threshold %.4f)", t.Metric, t.Reason, value, t.Threshold))
		}
	}

	// A regression in the accumulated results ends the canary regardless of
	// what the aggregate metrics say.
	for _, r := range c.Results {
		if r.Regression && !r.Quarantined {
			result.Rollback = true
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("task %s regressed against the baseline", r.TaskID))
		}
	}

	if len(c.Results) >= c.MaxTasks || !now.Before(c.Deadline) {
		result.Expired = true
	}

	sort.Strings(result.Reasons)
	return result
}

// Rollback records the reversal of a canary and returns the updated record.
//
// A rollback preserves the evidence rather than discarding it: the results that
// triggered the reversal are what Process 07 learns from, so throwing them away
// would mean relearning the same failure later.
func Rollback(c Canary, reason string, now time.Time) (Canary, error) {
	if strings.TrimSpace(reason) == "" {
		return Canary{}, fmt.Errorf("%w: rollback needs a reason", ErrInvalid)
	}
	if c.State == CanaryRolledBack {
		return c, nil
	}
	c.State = CanaryRolledBack
	c.RollbackReason = reason
	return c, nil
}

// DriftKind names something whose change can invalidate a promoted
// optimization.
type DriftKind string

const (
	DriftProvider  DriftKind = "PROVIDER"
	DriftModel     DriftKind = "MODEL"
	DriftHarness   DriftKind = "HARNESS"
	DriftBenchmark DriftKind = "BENCHMARK"
	DriftTool      DriftKind = "TOOL"
	DriftPricing   DriftKind = "PRICING"
	DriftQuota     DriftKind = "QUOTA"
	DriftPolicy    DriftKind = "POLICY"
	DriftWorkload  DriftKind = "WORKLOAD"
	DriftHardware  DriftKind = "HARDWARE"
)

// DriftSignal is one observed change in the environment an optimization rests on.
type DriftSignal struct {
	Kind DriftKind `json:"kind"`
	// Subject names what changed: which provider, which tool.
	Subject string `json:"subject"`
	// From and To record the versions or values on each side of the change.
	From string `json:"from"`
	To   string `json:"to"`
	// Observed is when the change was detected, not when it happened.
	Observed time.Time `json:"observed"`
}

// DetectDrift compares a promoted optimization's pinned baseline against the
// current environment and reports what moved.
//
// A promoted optimization is only valid for the world it was measured in. When
// that world changes, the right answer is to re-evaluate, not to keep trusting
// a number gathered against a different provider version.
func DetectDrift(pinned Baseline, current Baseline, now time.Time) []DriftSignal {
	var signals []DriftSignal

	for name, version := range pinned.ModelVersions {
		if got, ok := current.ModelVersions[name]; ok && got != version {
			signals = append(signals, DriftSignal{
				Kind: DriftModel, Subject: name, From: version, To: got, Observed: now,
			})
		}
	}
	for name, version := range pinned.HarnessVersions {
		if got, ok := current.HarnessVersions[name]; ok && got != version {
			signals = append(signals, DriftSignal{
				Kind: DriftHarness, Subject: name, From: version, To: got, Observed: now,
			})
		}
	}
	if pinned.BenchmarkVersion != "" && pinned.BenchmarkVersion != current.BenchmarkVersion {
		signals = append(signals, DriftSignal{
			Kind: DriftBenchmark, Subject: "benchmark",
			From: pinned.BenchmarkVersion, To: current.BenchmarkVersion, Observed: now,
		})
	}
	if pinned.Toolchain != current.Toolchain {
		signals = append(signals, DriftSignal{
			Kind: DriftTool, Subject: "toolchain",
			From: pinned.Toolchain, To: current.Toolchain, Observed: now,
		})
	}
	if pinned.VerifierPolicy != current.VerifierPolicy {
		signals = append(signals, DriftSignal{
			Kind: DriftPolicy, Subject: "verifier_policy",
			From: pinned.VerifierPolicy, To: current.VerifierPolicy, Observed: now,
		})
	}
	if pinned.EnvironmentHash != current.EnvironmentHash {
		signals = append(signals, DriftSignal{
			Kind: DriftHardware, Subject: "environment",
			From: pinned.EnvironmentHash, To: current.EnvironmentHash, Observed: now,
		})
	}

	sort.Slice(signals, func(i, j int) bool {
		if signals[i].Kind != signals[j].Kind {
			return signals[i].Kind < signals[j].Kind
		}
		return signals[i].Subject < signals[j].Subject
	})
	return signals
}

// StaleAfterDrift reports whether drift invalidates a promotion.
//
// Any drift in what the optimization was measured against makes the promotion
// stale. This is deliberately strict: the cost of re-evaluating is bounded,
// while the cost of routing real work on evidence gathered against a provider
// version that no longer exists is not.
func StaleAfterDrift(signals []DriftSignal) bool { return len(signals) > 0 }

// ExplorationPolicy bounds trying something not yet known to be good.
type ExplorationPolicy struct {
	// SafeTaskClasses are the only classes exploration may touch. Critical
	// work uses known-good routes.
	SafeTaskClasses []string `json:"safe_task_classes"`
	MaxCostMicros   int64    `json:"max_cost_micros"`
	// AllowSecretExposure must stay false.
	AllowSecretExposure bool `json:"allow_secret_exposure"`
	// AllowDestructiveEffects must stay false: exploration never duplicates a
	// real-world action to find out what would have happened.
	AllowDestructiveEffects bool `json:"allow_destructive_effects"`
	// StopAfterFailures ends exploration rather than letting it run on.
	StopAfterFailures int `json:"stop_after_failures"`
}

// AllowExploration reports whether exploring an alternate route is permitted
// for this task, and why not when it is not.
func AllowExploration(p ExplorationPolicy, taskClass string, highRisk bool, failures int) error {
	if p.AllowSecretExposure {
		return fmt.Errorf("%w: exploration requested secret exposure", ErrVeto)
	}
	if p.AllowDestructiveEffects {
		return fmt.Errorf("%w: exploration requested destructive effects", ErrVeto)
	}
	if highRisk {
		// High-risk work is where a known-good route matters most, so it is
		// exactly where exploration is not justified by curiosity.
		return fmt.Errorf("%w: high-risk work uses known-good routes", ErrVeto)
	}
	if p.MaxCostMicros <= 0 {
		return fmt.Errorf("%w: exploration needs a cost bound", ErrInvalid)
	}
	if p.StopAfterFailures > 0 && failures >= p.StopAfterFailures {
		return fmt.Errorf("%w: exploration stopped after %d failures", ErrVeto, failures)
	}
	for _, safe := range p.SafeTaskClasses {
		if safe == taskClass {
			return nil
		}
	}
	return fmt.Errorf("%w: task class %s is outside the exploration scope", ErrVeto, taskClass)
}
