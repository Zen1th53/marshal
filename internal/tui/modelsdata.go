package tui

// Models data: the read bindings behind the Models section.
//
// Models covers providers, harnesses, local models, routing and Process 08
// optimization. It carries the two remaining frozen gaps in the whole pack —
// CTUI-0523 and CTUI-0557 — and it is the section where a UI is most tempted to
// invent: a quota figure, a reset countdown, a "recommended" model.
//
// It invents none of them. Provider capacity has no supported interface on any
// provider, so the section says so rather than estimating from usage; a
// promotion is Process 08's decision, restated here rather than recomputed.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ModelsReader is the canonical Models state the section displays.
type ModelsReader interface {
	// Cycles lists Process 08 optimization cycles, newest first.
	Cycles(ctx context.Context) ([]OptimizationCycle, error)
	// Canaries lists active canaries for a cycle.
	CanariesFor(ctx context.Context, cycleID string) ([]OptimizationCanary, error)
	// LocalModels lists locally available models, as the resource collector
	// observed them.
	LocalModels(ctx context.Context) ([]LocalModel, error)
}

// OptimizationEvidenceReader is the optional richer Process 08 read surface.
// Keeping it separate preserves compatibility with readers that can list
// cycles but cannot enumerate their evidence.
type OptimizationEvidenceReader interface {
	CycleEvidence(ctx context.Context, cycleID string) (OptimizationEvidence, error)
}

type OptimizationEvidence struct {
	Candidates, Counterfactuals, Benchmarks, Results, Promotions int
}

// OptimizationCycle is one Process 08 cycle.
type OptimizationCycle struct {
	ID        string
	Status    string
	StartedAt time.Time
	Candidate string
}

// OptimizationCanary is one canary rollout.
type OptimizationCanary struct {
	ID       string
	Status   string
	Promoted bool
}

// LocalModel is one locally available model.
type LocalModel struct {
	Name          string
	Family        string
	Compatibility string
	Reason        string
}

// ModelsFeed bundles the readers Models binds to.
type ModelsFeed struct {
	Reader    ModelsReader
	ProjectID string
	Now       func() time.Time
}

func (s *ModelsFeed) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

const modelsBinding = "internal/app/optimization_runtime.go, internal/harness, internal/resources"

// ModelsSnapshot is everything Models displays, read at one instant.
type ModelsSnapshot struct {
	// Process 08.
	Cycles       []CycleRow
	CyclesStatus Value
	Canaries     []CanaryRow
	CanaryStatus Value

	// Local models.
	LocalModels []LocalModelRow
	LocalStatus Value

	// The two frozen gaps, rendered as gaps wherever they appear.
	ProviderDetail       Value
	Entitlement          Value
	ProviderStatus       Value
	RoutingStatus        Value
	ContextStatus        Value
	CloudStatus          Value
	EvaluationStatus     Value
	CounterfactualStatus Value
	BenchmarkStatus      Value
	ResultsStatus        Value
	FeedbackStatus       Value

	ObservedAt time.Time
}

// CycleRow is one Process 08 cycle.
type CycleRow struct {
	ID        Value
	Status    Value
	Started   Value
	Candidate Value
}

// CanaryRow is one canary rollout.
type CanaryRow struct {
	ID       Value
	Status   Value
	Promoted Value
}

// LocalModelRow is one locally available model.
type LocalModelRow struct {
	Name          Value
	Family        Value
	Compatibility Value
}

// ReadModels gathers Models state at one instant.
func (s *ModelsFeed) ReadModels(ctx context.Context) ModelsSnapshot {
	snap := ModelsSnapshot{ObservedAt: s.now()}

	// The two frozen gaps are stated the same way wherever they surface, so a
	// reader meets one explanation rather than several paraphrases.
	snap.ProviderDetail = NotRun(
		"IMPLEMENTATION GAP (CTUI-0523): no provider exposes a machine-readable "+
			"detail surface through a supported interface, so MARSHAL has no "+
			"evidence to show here", modelsBinding)
	snap.Entitlement = NotRun(
		"IMPLEMENTATION GAP (CTUI-0557): entitlement request state has no "+
			"canonical store; the Cloud gate reports whether a lease is held, "+
			"not what was requested", modelsBinding)
	snap.ProviderStatus = Unknown("provider discovery and qualification evidence is not exposed by the attached Models reader", modelsBinding)
	snap.RoutingStatus = Unknown("no selected canonical plan/run route is exposed by the attached Models reader", modelsBinding)
	snap.ContextStatus = Unknown("no compiled context package is selected", modelsBinding)
	snap.CloudStatus = Unknown("Community Cloud detail is owned by the live Cloud status reader, not this optimization reader", modelsBinding)
	snap.EvaluationStatus = Unknown("no Evaluation Lab result is selected", modelsBinding)
	snap.CounterfactualStatus = Unknown("no Process 08 counterfactual record is selected", modelsBinding)
	snap.BenchmarkStatus = Unknown("no Process 08 benchmark manifest is selected", modelsBinding)
	snap.ResultsStatus = Unknown("no Process 08 experiment result is selected", modelsBinding)
	snap.FeedbackStatus = Unknown("no Process 08 feedback record is selected", modelsBinding)

	if s == nil || s.Reader == nil {
		unavailable := Unknown(
			"no optimization service is attached to this workspace", modelsBinding)
		snap.CyclesStatus, snap.CanaryStatus, snap.LocalStatus =
			unavailable, unavailable, unavailable
		return snap
	}

	s.readCycles(ctx, &snap)
	s.readProviderSummary(ctx, &snap)
	s.readCycleEvidence(ctx, &snap)
	s.readLocalModels(ctx, &snap)
	return snap
}

func (s *ModelsFeed) readProviderSummary(ctx context.Context, snap *ModelsSnapshot) {
	reader, ok := s.Reader.(ProviderReader)
	if !ok {
		return
	}
	probes, err := reader.Providers(ctx)
	if err != nil {
		snap.ProviderStatus = Errored(fmt.Sprintf("provider discovery failed: %s", err), modelsBinding)
		return
	}
	installed := 0
	for _, probe := range probes {
		if probe.Probed && probe.Found {
			installed++
		}
	}
	snap.ProviderStatus = Known(fmt.Sprintf("%d installed of %d discovered providers", installed, len(probes)), modelsBinding)
}

func (s *ModelsFeed) readCycleEvidence(ctx context.Context, snap *ModelsSnapshot) {
	reader, ok := s.Reader.(OptimizationEvidenceReader)
	if !ok || len(snap.Cycles) == 0 || snap.Cycles[0].ID.Status != TruthKnown {
		return
	}
	evidence, err := reader.CycleEvidence(ctx, snap.Cycles[0].ID.Text)
	if err != nil {
		v := Errored(fmt.Sprintf("Process 08 evidence could not be read: %s", err), modelsBinding)
		snap.EvaluationStatus, snap.CounterfactualStatus, snap.BenchmarkStatus = v, v, v
		snap.ResultsStatus, snap.FeedbackStatus = v, v
		return
	}
	snap.EvaluationStatus = Known(fmt.Sprintf("%d candidates evaluated", evidence.Candidates), modelsBinding)
	snap.CounterfactualStatus = Known(fmt.Sprintf("%d counterfactual evaluations", evidence.Counterfactuals), modelsBinding)
	snap.BenchmarkStatus = Known(fmt.Sprintf("%d benchmark manifests", evidence.Benchmarks), modelsBinding)
	snap.ResultsStatus = Known(fmt.Sprintf("%d experiment results", evidence.Results), modelsBinding)
	snap.FeedbackStatus = Known(fmt.Sprintf("%d promotion/feedback records", evidence.Promotions), modelsBinding)
}

func (s *ModelsFeed) readCycles(ctx context.Context, snap *ModelsSnapshot) {
	cycles, err := s.Reader.Cycles(ctx)
	switch {
	case err != nil:
		snap.CyclesStatus = Errored(
			fmt.Sprintf("optimization cycles could not be read: %s", err), modelsBinding)
		snap.CanaryStatus = snap.CyclesStatus
		return
	case len(cycles) == 0:
		// No cycle is an ordinary state: Process 08 runs on demand.
		snap.CyclesStatus = Empty(modelsBinding)
		snap.CanaryStatus = Empty(modelsBinding)
		return
	}

	sorted := make([]OptimizationCycle, len(cycles))
	copy(sorted, cycles)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].StartedAt.Equal(sorted[j].StartedAt) {
			return sorted[i].StartedAt.After(sorted[j].StartedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})

	at := s.now()
	for _, cycle := range sorted {
		snap.Cycles = append(snap.Cycles, CycleRow{
			ID:     Known(cycle.ID, modelsBinding),
			Status: optimizationStatus(cycle.Status),
			Started: knownOrUnknown(relativeTime(at, cycle.StartedAt),
				"the cycle records no start time", modelsBinding),
			Candidate: knownOrEmpty(cycle.Candidate, modelsBinding),
		})
	}
	snap.CyclesStatus = Known(fmt.Sprintf("%d cycles", len(sorted)), modelsBinding)

	// Canaries belong to the newest cycle: showing every cycle's canaries at
	// once would make it unclear which rollout a row describes.
	canaries, err := s.Reader.CanariesFor(ctx, sorted[0].ID)
	switch {
	case err != nil:
		snap.CanaryStatus = Errored(
			fmt.Sprintf("canaries could not be read: %s", err), modelsBinding)
	case len(canaries) == 0:
		snap.CanaryStatus = Empty(modelsBinding)
	default:
		for _, canary := range canaries {
			row := CanaryRow{
				ID:     Known(canary.ID, modelsBinding),
				Status: optimizationStatus(canary.Status),
			}
			// Promotion is a decision Process 08 records, not one inferred
			// from a canary looking healthy.
			if canary.Promoted {
				row.Promoted = Known("promoted", modelsBinding)
			} else {
				row.Promoted = NotRun(
					"this canary has not been promoted", modelsBinding)
			}
			snap.Canaries = append(snap.Canaries, row)
		}
		snap.CanaryStatus = Known(
			fmt.Sprintf("%d canaries in cycle %s", len(canaries), sorted[0].ID), modelsBinding)
	}
}

func (s *ModelsFeed) readLocalModels(ctx context.Context, snap *ModelsSnapshot) {
	models, err := s.Reader.LocalModels(ctx)
	switch {
	case err != nil:
		snap.LocalStatus = Errored(
			fmt.Sprintf("local models could not be listed: %s", err), modelsBinding)
		return
	case len(models) == 0:
		snap.LocalStatus = Empty(modelsBinding)
		return
	}

	sorted := make([]LocalModel, len(models))
	copy(sorted, models)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, model := range sorted {
		snap.LocalModels = append(snap.LocalModels, LocalModelRow{
			Name:   Known(model.Name, modelsBinding),
			Family: knownOrEmpty(model.Family, modelsBinding),
			// Compatibility is the collector's assessment against this
			// machine's memory and accelerators, carried with its reason so a
			// "may fit" is not read as a recommendation.
			Compatibility: modelCompatibility(model),
		})
	}
	snap.LocalStatus = Known(fmt.Sprintf("%d local models", len(sorted)), modelsBinding)
}

// optimizationStatus restates a Process 08 status without judging it.
func optimizationStatus(status string) Value {
	normalised := strings.ToUpper(strings.TrimSpace(status))
	switch normalised {
	case "":
		return Unknown("no status was recorded", modelsBinding)
	case "COMPLETED", "PROMOTED", "SUCCEEDED":
		return Known(normalised, modelsBinding)
	case "FAILED", "ROLLED_BACK", "REJECTED", "ABORTED":
		return Value{
			Text: normalised, Status: TruthKnown,
			Reason: "this is a recorded negative outcome, not a missing one",
			Source: modelsBinding,
		}
	case "RUNNING", "PENDING", "STARTED", "IN_PROGRESS":
		return NotRun(fmt.Sprintf(
			"recorded as %s: this has not produced a result yet", normalised), modelsBinding)
	}
	return Unknown(fmt.Sprintf(
		"Process 08 reported %q, which this build does not recognise", status), modelsBinding)
}

// modelCompatibility renders the collector's fit assessment with its reason.
//
// "MAY_FIT" without its reason reads as an endorsement; with the reason it
// reads as what it is — an estimate against this machine's memory.
func modelCompatibility(model LocalModel) Value {
	normalised := strings.ToUpper(strings.TrimSpace(model.Compatibility))
	if normalised == "" || normalised == "UNKNOWN" {
		reason := model.Reason
		if reason == "" {
			reason = "the collector could not assess this model against this machine"
		}
		return Unknown(reason, modelsBinding)
	}
	return Value{
		Text:   normalised,
		Status: TruthKnown,
		Reason: model.Reason,
		Source: modelsBinding,
	}
}
