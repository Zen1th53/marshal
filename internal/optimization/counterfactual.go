package optimization

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// Counterfactual evaluation answers a bounded question: would another governed
// route plausibly have produced a better verified outcome on this exact task?
//
// Process 07 built the replay index but never evaluated a counterfactual with
// it. The gap was not the substrate; it was that nothing actually re-executed
// an alternate route and compared results. This file closes that.
//
// The safety rule is absolute and comes first: a counterfactual never repeats a
// destructive or irreversible external action. Where a factual run wrote to the
// world, the counterfactual is refused rather than run, and the row says so.

// EvalMethod is how a counterfactual obtained its alternate outcome.
type EvalMethod string

const (
	// MethodReplay re-executes the recorded task against a sandboxed copy of
	// the exact tree, with network disabled and no production credentials.
	MethodReplay EvalMethod = "REPLAY"
	// MethodBenchmark runs the alternate route over pinned benchmark tasks.
	MethodBenchmark EvalMethod = "BENCHMARK"
	// MethodShadow evaluates the alternate route alongside live work without
	// letting it control any action or produce any external effect.
	MethodShadow EvalMethod = "SHADOW"
	// MethodSynthetic uses a fixture that reproduces the task's shape without
	// its side effects.
	MethodSynthetic EvalMethod = "SYNTHETIC"
)

// ValidMethod reports whether m is a recognized evaluation method.
func ValidMethod(m EvalMethod) bool {
	switch m {
	case MethodReplay, MethodBenchmark, MethodShadow, MethodSynthetic:
		return true
	}
	return false
}

// Route is one governed path a task could take.
type Route struct {
	TaskClass       string `json:"task_class"`
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	Model           string `json:"model"`
	Harness         string `json:"harness"`
	HarnessVersion  string `json:"harness_version"`
	// NativeEffort is the harness-native reasoning or effort setting.
	NativeEffort string `json:"native_effort,omitempty"`
	// VerifierPolicy names the verification strategy the route uses.
	VerifierPolicy string `json:"verifier_policy"`
}

// Valid reports whether the route names an exact, governable configuration.
func (r Route) Valid() bool {
	return r.TaskClass != "" && r.Provider != "" && r.ProviderVersion != "" &&
		r.Model != "" && r.VerifierPolicy != ""
}

// SideEffect is an external action a task performed. Counterfactual evaluation
// reads these to decide whether re-running is safe.
type SideEffect struct {
	Kind string `json:"kind"`
	// Destructive marks an effect that cannot be undone or that would cause
	// harm if repeated: a push, a deploy, a delete, a payment, a message sent.
	Destructive bool   `json:"destructive"`
	Target      string `json:"target,omitempty"`
}

// FactualRun is what actually happened: the route taken and the outcome it
// produced, as recorded by Process 05 and verified by Process 06.
type FactualRun struct {
	TaskID  string           `json:"task_id"`
	Route   Route            `json:"route"`
	Outcome learning.Outcome `json:"outcome"`
	// VerifierResult is the Process 06 verdict, not the agent's self-report.
	VerifierResult Status   `json:"verifier_result"`
	Metrics        []Metric `json:"metrics,omitempty"`
	// SideEffects are the external actions the factual run performed.
	SideEffects []SideEffect `json:"side_effects,omitempty"`
	// ReplayClass carries the Process 07 judgement about reproducibility.
	ReplayClass learning.ReplayClass `json:"replay_class"`
	// TreeDigest and EnvironmentDigest bind the run to an exact state.
	TreeDigest        string `json:"tree_digest"`
	EnvironmentDigest string `json:"environment_digest"`
	ObservedAt        time.Time
}

// SandboxPolicy bounds a replay. The defaults are the safe ones: a zero value
// permits nothing.
type SandboxPolicy struct {
	// NetworkEnabled must be explicitly set. Replay runs with network disabled
	// by default, because a replayed task that can reach the internet can
	// still change the world.
	NetworkEnabled bool `json:"network_enabled"`
	// ProductionCredentials must never be true. The field exists so a request
	// for them is refused explicitly rather than silently ignored.
	ProductionCredentials bool `json:"production_credentials"`
	// WritableRoot is the only path the replay may write to.
	WritableRoot string `json:"writable_root"`
	// MaxWallMillis and MaxMemoryBytes bound the resources a replay may use.
	MaxWallMillis  int64 `json:"max_wall_millis"`
	MaxMemoryBytes int64 `json:"max_memory_bytes"`
	// Seeds make a replay deterministic where the task permits it.
	Seeds []string `json:"seeds,omitempty"`
}

// ValidateSandbox refuses a policy that would let a replay touch production.
//
// This is the gate that makes counterfactual evaluation safe to run at all.
// A replay with production credentials is not a replay; it is a second
// execution against the real world.
func ValidateSandbox(p SandboxPolicy) error {
	if p.ProductionCredentials {
		return fmt.Errorf("%w: replay requested production credentials", ErrUnsafeCounterfactual)
	}
	if strings.TrimSpace(p.WritableRoot) == "" {
		return fmt.Errorf("%w: replay needs a bounded writable root", ErrInvalid)
	}
	if p.MaxWallMillis <= 0 || p.MaxMemoryBytes <= 0 {
		return fmt.Errorf("%w: replay needs wall and memory bounds", ErrInvalid)
	}
	return nil
}

// SafeToEvaluate reports whether a counterfactual may be run against this
// factual run, and why not when it may not.
//
// A run that performed a destructive external action is never re-executed:
// re-running it would repeat the action. A run marked non-replayable by
// Process 07 is honoured rather than second-guessed.
func SafeToEvaluate(f FactualRun, method EvalMethod) error {
	for _, e := range f.SideEffects {
		if e.Destructive {
			return fmt.Errorf("%w: factual run performed a destructive %s effect on %s",
				ErrUnsafeCounterfactual, e.Kind, e.Target)
		}
	}
	if method == MethodReplay && f.ReplayClass == learning.ReplayNonReplayable {
		return fmt.Errorf("%w: Process 07 marked this run non-replayable", ErrUnsafeCounterfactual)
	}
	// Shadow and synthetic evaluation never re-execute the factual task, so
	// they remain available even where replay is refused. Benchmark evaluation
	// runs pinned tasks rather than this one.
	return nil
}

// Counterfactual is one evaluated alternate-route comparison.
type Counterfactual struct {
	ID      string     `json:"counterfactual_id"`
	Method  EvalMethod `json:"method"`
	Factual FactualRun `json:"factual"`
	// Alternate is the governed route being asked about.
	Alternate Route `json:"alternate_route"`
	// AlternateOutcome and AlternateVerifier are what the alternate route
	// actually produced under evaluation. They are never predicted.
	AlternateOutcome  learning.Outcome `json:"alternate_outcome"`
	AlternateVerifier Status           `json:"alternate_verifier_result"`
	AlternateMetrics  []Metric         `json:"alternate_metrics,omitempty"`
	// Sandbox records the bounds the evaluation ran under.
	Sandbox SandboxPolicy `json:"sandbox"`
	// Limitations states what this comparison does not establish. A
	// counterfactual is always partial evidence, and saying so is part of the
	// record rather than a caveat left to the reader.
	Limitations []string `json:"limitations,omitempty"`
	// ClusterID groups counterfactuals sharing an evidence source.
	ClusterID   string `json:"cluster_id"`
	Provenance  string `json:"provenance"`
	EvaluatedAt time.Time
	Digest      string `json:"digest"`
}

// Verdict is what a counterfactual comparison concluded.
type Verdict string

const (
	// VerdictAlternateBetter means the alternate route reached a verified
	// outcome the factual route did not.
	VerdictAlternateBetter Verdict = "ALTERNATE_BETTER"
	// VerdictFactualBetter means the factual route did better.
	VerdictFactualBetter Verdict = "FACTUAL_BETTER"
	// VerdictEquivalent means neither route was better on verified outcome.
	VerdictEquivalent Verdict = "EQUIVALENT"
	// VerdictUnknown means the comparison could not be made. It is not a tie.
	VerdictUnknown Verdict = "UNKNOWN"
)

// Compare reports what the counterfactual established.
//
// Verified outcome dominates. A route that was cheaper and faster but did not
// reach a verified outcome did not do better, because Process 08 optimizes
// outcomes rather than the numbers that are easiest to move. Cost and latency
// only separate routes that reached the same verified outcome, and only when
// both were actually measured.
func Compare(c Counterfactual) Verdict {
	if c.Factual.VerifierResult == StatusUnknown || c.AlternateVerifier == StatusUnknown {
		return VerdictUnknown
	}
	if c.Factual.VerifierResult == StatusNotRun || c.AlternateVerifier == StatusNotRun {
		return VerdictUnknown
	}

	factualVerified := c.Factual.Outcome == learning.OutcomeVerifiedComplete &&
		c.Factual.VerifierResult == StatusPass
	alternateVerified := c.AlternateOutcome == learning.OutcomeVerifiedComplete &&
		c.AlternateVerifier == StatusPass

	switch {
	case alternateVerified && !factualVerified:
		return VerdictAlternateBetter
	case factualVerified && !alternateVerified:
		return VerdictFactualBetter
	case !factualVerified && !alternateVerified:
		return VerdictEquivalent
	}

	// Both reached a verified outcome. Only measured metrics may separate them.
	factualCost, okF := metricValue(c.Factual.Metrics, "cost_micros")
	alternateCost, okA := metricValue(c.AlternateMetrics, "cost_micros")
	if okF && okA {
		if alternateCost < factualCost {
			return VerdictAlternateBetter
		}
		if factualCost < alternateCost {
			return VerdictFactualBetter
		}
	}
	return VerdictEquivalent
}

func metricValue(metrics []Metric, name string) (float64, bool) {
	for _, m := range metrics {
		if m.Name == name && m.Measured() {
			return *m.Value, true
		}
	}
	return 0, false
}

// NewCounterfactual validates and digests one counterfactual evaluation.
//
// It refuses to record a comparison that should never have been run: an unsafe
// evaluation, an unbound factual run, an ungoverned alternate route, or a
// sandbox that would have reached production.
func NewCounterfactual(c Counterfactual, g Governance, now time.Time) (Counterfactual, error) {
	if strings.TrimSpace(c.ID) == "" {
		return Counterfactual{}, fmt.Errorf("%w: counterfactual id", ErrInvalid)
	}
	if !ValidMethod(c.Method) {
		return Counterfactual{}, fmt.Errorf("%w: evaluation method", ErrInvalid)
	}
	if strings.TrimSpace(c.Provenance) == "" {
		return Counterfactual{}, fmt.Errorf("%w: counterfactual provenance", ErrInvalid)
	}
	if c.Factual.TaskID == "" || !c.Factual.Route.Valid() {
		return Counterfactual{}, fmt.Errorf("%w: factual run is not bound to an exact route", ErrInvalid)
	}
	if c.Factual.TreeDigest == "" || c.Factual.EnvironmentDigest == "" {
		return Counterfactual{}, fmt.Errorf("%w: factual run is not bound to an exact state", ErrInvalid)
	}
	if !c.Alternate.Valid() {
		return Counterfactual{}, fmt.Errorf("%w: alternate route is not exact", ErrInvalid)
	}
	// The alternate route must be one MARSHAL can govern. Evaluating a route
	// that could never be adopted produces evidence for a change that could
	// never be made.
	if g.GovernableProviders != nil && !g.GovernableProviders[c.Alternate.Provider] {
		return Counterfactual{}, fmt.Errorf("%w: alternate provider %s is not governable",
			ErrVeto, c.Alternate.Provider)
	}
	if err := SafeToEvaluate(c.Factual, c.Method); err != nil {
		return Counterfactual{}, err
	}
	// Replay and shadow both execute something, so both need real bounds.
	// Benchmark and synthetic run pinned or fabricated tasks instead.
	if c.Method == MethodReplay || c.Method == MethodShadow {
		if err := ValidateSandbox(c.Sandbox); err != nil {
			return Counterfactual{}, err
		}
	}
	if !ValidStatus(c.AlternateVerifier) {
		return Counterfactual{}, fmt.Errorf("%w: alternate verifier result", ErrInvalid)
	}

	// A single counterfactual is bounded evidence about one task. Recording
	// that limitation is mandatory rather than optional.
	c.Limitations = append([]string(nil), c.Limitations...)
	c.Limitations = append(c.Limitations,
		fmt.Sprintf("evaluated by %s on task %s only", c.Method, c.Factual.TaskID))
	sort.Strings(c.Limitations)

	c.EvaluatedAt = now
	c.Digest = ""
	d, err := digest(c)
	if err != nil {
		return Counterfactual{}, err
	}
	c.Digest = d
	return c, nil
}

// Verify reports whether a stored counterfactual still matches its digest.
func (c Counterfactual) Verify() error {
	want := c.Digest
	if want == "" {
		return fmt.Errorf("%w: missing digest", ErrInvalid)
	}
	c.Digest = ""
	got, err := digest(c)
	if err != nil {
		return err
	}
	if got != want {
		return ErrTampered
	}
	return nil
}

// RouteEvidence aggregates counterfactual verdicts for one alternate route.
//
// It counts by independent cluster rather than by observation, so the same
// comparison repeated does not look like accumulating support.
type RouteEvidence struct {
	Route Route `json:"route"`
	// Better, Worse and Equivalent count verdicts.
	Better     int `json:"better"`
	Worse      int `json:"worse"`
	Equivalent int `json:"equivalent"`
	// Unknown counts comparisons that could not be made. They are reported
	// rather than dropped, because a route whose evaluations mostly failed is
	// not a route with a clean record.
	Unknown int `json:"unknown"`
	// Clusters is the number of independent evidence clusters.
	Clusters int `json:"clusters"`
	// Methods records which evaluation methods contributed, so evidence built
	// entirely from synthetic fixtures is distinguishable from replay.
	Methods []EvalMethod `json:"methods"`
}

// AggregateRouteEvidence folds counterfactuals into per-route evidence.
func AggregateRouteEvidence(cfs []Counterfactual) map[Route]RouteEvidence {
	clusters := map[Route]map[string]bool{}
	methods := map[Route]map[EvalMethod]bool{}
	out := map[Route]RouteEvidence{}

	for _, c := range cfs {
		e := out[c.Alternate]
		e.Route = c.Alternate
		switch Compare(c) {
		case VerdictAlternateBetter:
			e.Better++
		case VerdictFactualBetter:
			e.Worse++
		case VerdictEquivalent:
			e.Equivalent++
		default:
			e.Unknown++
		}
		if clusters[c.Alternate] == nil {
			clusters[c.Alternate] = map[string]bool{}
		}
		if c.ClusterID != "" {
			clusters[c.Alternate][c.ClusterID] = true
		}
		if methods[c.Alternate] == nil {
			methods[c.Alternate] = map[EvalMethod]bool{}
		}
		methods[c.Alternate][c.Method] = true
		out[c.Alternate] = e
	}

	for route, e := range out {
		e.Clusters = len(clusters[route])
		for m := range methods[route] {
			e.Methods = append(e.Methods, m)
		}
		sort.Slice(e.Methods, func(i, j int) bool { return e.Methods[i] < e.Methods[j] })
		out[route] = e
	}
	return out
}

// SupportsRouting reports whether route evidence is strong enough to justify a
// routing candidate.
//
// Support requires more wins than losses across at least the governance
// cluster floor, and it requires that the evidence is not built solely from
// synthetic fixtures: a fixture proves the mechanism works, not that the route
// is better on real work.
func (e RouteEvidence) SupportsRouting(g Governance) (bool, string) {
	if e.Clusters < g.MinEvidenceClusters {
		return false, fmt.Sprintf("evidence spans %d independent cluster(s), need %d",
			e.Clusters, g.MinEvidenceClusters)
	}
	if e.Better <= e.Worse {
		return false, fmt.Sprintf("alternate route won %d and lost %d comparisons", e.Better, e.Worse)
	}
	if e.Unknown > e.Better {
		return false, fmt.Sprintf("%d comparison(s) were UNKNOWN against %d wins", e.Unknown, e.Better)
	}
	syntheticOnly := len(e.Methods) > 0
	for _, m := range e.Methods {
		if m != MethodSynthetic {
			syntheticOnly = false
			break
		}
	}
	if syntheticOnly {
		return false, "evidence is entirely synthetic; no replay, benchmark or shadow result"
	}
	return true, ""
}
