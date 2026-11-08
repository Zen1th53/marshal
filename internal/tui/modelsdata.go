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

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
)

// CodexGovernanceReader reads live state from the governed Codex control plane.
type CodexGovernanceReader interface {
	CodexHealth(ctx context.Context) (CodexHealthReport, error)
	CodexModels(ctx context.Context) ([]codex.ModelInfo, string, error)
	SelectedCodexModel(ctx context.Context) (string, error)
	CodexSessions(ctx context.Context) ([]CodexSessionSummary, error)
	CodexPlugins(ctx context.Context) ([]codex.PluginInfo, []codex.SkillInfo, error)
}

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
	Reader        ModelsReader
	ProjectID     string
	Now           func() time.Time
	RequestReader EntitlementRequestReader
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

	// Provider detail and entitlement request state.
	ProviderDetail            Value
	ProviderDetails           []ProviderDetailRow
	Entitlement               Value
	EntitlementRequestStatus  Value
	EntitlementLocalAttemptID Value
	EntitlementServerID       Value
	EntitlementAge            Value
	EntitlementError          Value
	ProviderStatus            Value
	RoutingStatus             Value
	ContextStatus             Value
	CloudStatus               Value
	EvaluationStatus          Value
	CounterfactualStatus      Value
	BenchmarkStatus           Value
	ResultsStatus             Value
	FeedbackStatus            Value

	// Governed Codex control plane.
	CodexHealth        CodexHealthReport
	CodexModels        []codex.ModelInfo
	CodexDefaultModel  string
	CodexSelectedModel string
	CodexSessions      []CodexSessionSummary
	CodexPlugins       []codex.PluginInfo
	CodexSkills        []codex.SkillInfo
	CodexStatus        Value

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

// ProviderDetailRow is one provider's live session, usage, and capability state.
type ProviderDetailRow struct {
	Name          Value
	Availability  Value
	Version       Value
	Model         Value
	Auth          Value
	Cooldown      Value
	UsageTokens   Value
	UsageCost     Value
	UsageCalls    Value
	UsageDuration Value
	Quota         Value
	Reset         Value
	Allowance     Value
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
	if editionExtreme {
		snap.CloudStatus = Known("Not needed in Marshal: ULTRA runs locally", modelsBinding)
	}
	snap.EvaluationStatus = Unknown("no Evaluation Lab result is selected", modelsBinding)
	snap.CounterfactualStatus = Unknown("no Process 08 counterfactual record is selected", modelsBinding)
	snap.BenchmarkStatus = Unknown("no Process 08 benchmark manifest is selected", modelsBinding)
	snap.ResultsStatus = Unknown("no Process 08 experiment result is selected", modelsBinding)
	snap.FeedbackStatus = Unknown("no Process 08 feedback record is selected", modelsBinding)
	snap.CodexStatus = Unknown("codex status has not been probed", modelsBinding)

	if s == nil || s.Reader == nil {
		unavailable := Unknown(
			"no optimization service is attached to this workspace", modelsBinding)
		snap.CyclesStatus, snap.CanaryStatus, snap.LocalStatus =
			unavailable, unavailable, unavailable
		s.readEntitlementRequest(ctx, &snap)
		return snap
	}

	s.readCycles(ctx, &snap)
	s.readProviderSummary(ctx, &snap)
	s.readProviderDetails(ctx, &snap)
	s.readEntitlementRequest(ctx, &snap)
	s.readCycleEvidence(ctx, &snap)
	s.readLocalModels(ctx, &snap)
	s.readCodexGovernance(ctx, &snap)
	return snap
}

func (s *ModelsFeed) readEntitlementRequest(ctx context.Context, snap *ModelsSnapshot) {
	var reqReader EntitlementRequestReader
	if s != nil && s.RequestReader != nil {
		reqReader = s.RequestReader
	} else if s != nil && s.Reader != nil {
		if r, ok := s.Reader.(EntitlementRequestReader); ok {
			reqReader = r
		}
	}

	if reqReader == nil {
		unattached := Unknown("no entitlement request reader is attached to this workspace", modelsBinding)
		snap.Entitlement = unattached
		snap.EntitlementRequestStatus = unattached
		snap.EntitlementLocalAttemptID = unattached
		snap.EntitlementServerID = unattached
		snap.EntitlementAge = unattached
		snap.EntitlementError = Empty(modelsBinding)
		return
	}

	obs, err := reqReader.EntitlementRequest(ctx)
	if err != nil {
		errVal := Errored(fmt.Sprintf("entitlement request state could not be read: %v", err), modelsBinding)
		snap.Entitlement = errVal
		snap.EntitlementRequestStatus = errVal
		snap.EntitlementLocalAttemptID = Unknown("request state error", modelsBinding)
		snap.EntitlementServerID = Unknown("request state error", modelsBinding)
		snap.EntitlementAge = Unknown("request state error", modelsBinding)
		snap.EntitlementError = Errored(RedactContent(err.Error(), nil), modelsBinding)
		return
	}

	if !obs.Recorded && obs.LocalAttemptID == "" {
		snap.Entitlement = NotRun("no entitlement request has been recorded for this session", modelsBinding)
		snap.EntitlementRequestStatus = NotRun("no entitlement request has been recorded for this session", modelsBinding)
		snap.EntitlementLocalAttemptID = Unknown("no local request attempt recorded", modelsBinding)
		snap.EntitlementServerID = Unknown("no server request ID recorded", modelsBinding)
		snap.EntitlementAge = Unknown("no request recorded", modelsBinding)
		snap.EntitlementError = Empty(modelsBinding)
		return
	}

	state := strings.ToUpper(strings.TrimSpace(obs.State))
	if state == "" {
		state = "RECORDED"
	}
	snap.Entitlement = Known(fmt.Sprintf("request %s: state=%s", obs.LocalAttemptID, state), modelsBinding)
	snap.EntitlementRequestStatus = Known(state, modelsBinding)
	if obs.LocalAttemptID != "" {
		snap.EntitlementLocalAttemptID = Known(obs.LocalAttemptID, modelsBinding)
	} else {
		snap.EntitlementLocalAttemptID = Unknown("no local attempt ID recorded", modelsBinding)
	}
	if obs.HasServerID && obs.ServerRequestID != "" {
		snap.EntitlementServerID = Known(obs.ServerRequestID, modelsBinding)
	} else {
		// Old status-only server response gets server-ID UNKNOWN, not a made-up ID.
		snap.EntitlementServerID = Unknown("server response did not return a server request ID", modelsBinding)
	}
	if !obs.RequestedAt.IsZero() {
		snap.EntitlementAge = Known(relativeTime(s.now(), obs.RequestedAt), modelsBinding)
	} else {
		snap.EntitlementAge = Unknown("request timestamp was not recorded", modelsBinding)
	}
	if obs.Error != "" {
		snap.EntitlementError = Known(RedactContent(obs.Error, nil), modelsBinding)
	} else {
		snap.EntitlementError = Empty(modelsBinding)
	}
}

func (s *ModelsFeed) readProviderDetails(ctx context.Context, snap *ModelsSnapshot) {
	at := s.now()
	if s == nil || s.Reader == nil {
		return
	}

	if statusReader, ok := s.Reader.(ProviderStatusReader); ok {
		statuses, err := statusReader.ProviderStatuses(ctx)
		if err != nil {
			snap.ProviderDetail = Errored(fmt.Sprintf("provider detail could not be read: %s", err), modelsBinding)
			return
		}
		if len(statuses) == 0 {
			snap.ProviderDetail = Empty(modelsBinding)
			return
		}
		for _, p := range statuses {
			row := ProviderDetailRow{
				Name: Known(p.Name, modelsBinding),
			}
			if !p.Found {
				reason := "the provider's command was not found on PATH"
				row.Availability = Known("not installed", modelsBinding)
				row.Version = NotRun(reason, modelsBinding)
				row.Model = NotRun(reason, modelsBinding)
				row.Quota = NotRun(reason, modelsBinding)
				row.Reset = NotRun(reason, modelsBinding)
				row.Allowance = NotRun(reason, modelsBinding)
				row.Auth = NotRun(reason, modelsBinding)
				row.Cooldown = NotRun(reason, modelsBinding)
				row.UsageTokens = NotRun(reason, modelsBinding)
				row.UsageCost = NotRun(reason, modelsBinding)
				row.UsageCalls = NotRun(reason, modelsBinding)
				row.UsageDuration = NotRun(reason, modelsBinding)
			} else {
				row.Availability = Known("installed", modelsBinding)
				if p.Version != "" {
					row.Version = Known(p.Version, modelsBinding)
				} else {
					row.Version = Unknown("the provider did not report a version", modelsBinding)
				}
				if p.Model != "" {
					row.Model = Known(p.Model, modelsBinding)
				} else {
					row.Model = Unknown("no model has been selected for this provider", modelsBinding)
				}
				quotaReason := p.QuotaReason
				if quotaReason == "" {
					quotaReason = "no provider exposes a machine-readable quota or capacity through a supported interface"
				}
				resetReason := p.ResetReason
				if resetReason == "" {
					resetReason = "no provider exposes a machine-readable quota reset time through a supported interface"
				}
				allowanceReason := p.AllowanceReason
				if allowanceReason == "" {
					allowanceReason = "no provider exposes a machine-readable temporary or promotional allowance through a supported interface"
				}
				row.Quota = Unknown(quotaReason, modelsBinding)
				row.Reset = Unknown(resetReason, modelsBinding)
				row.Allowance = Unknown(allowanceReason, modelsBinding)
				row.Auth = formatAuthValue(p.Auth, p.Found, at)
				row.Cooldown = formatCooldownValue(p.Cooldown, p.Found, at)
				row.UsageTokens = formatTokensValue(p.Usage.TotalTokens, p.Usage.Reported, p.Usage.Source)
				row.UsageCost = formatCostValue(p.Usage.CostUSD, p.Usage.Reported, p.Usage.Source)
				row.UsageCalls = formatCallsValue(p.Usage.ModelCalls, p.Usage.Reported, p.Usage.Source)
				row.UsageDuration = formatDurationValue(p.Usage.Duration, p.Usage.Reported, p.Usage.Source)
			}
			snap.ProviderDetails = append(snap.ProviderDetails, row)
		}
		snap.ProviderDetail = Known(fmt.Sprintf("%d providers detailed", len(snap.ProviderDetails)), modelsBinding)
		return
	}

	pReader, ok := s.Reader.(ProviderReader)
	if !ok {
		return
	}
	probes, err := pReader.Providers(ctx)
	if err != nil {
		snap.ProviderDetail = Errored(fmt.Sprintf("provider detail could not be read: %s", err), modelsBinding)
		return
	}
	if len(probes) == 0 {
		snap.ProviderDetail = Empty(modelsBinding)
		return
	}
	for _, p := range probes {
		row := ProviderDetailRow{
			Name: Known(p.Name, modelsBinding),
		}
		switch {
		case !p.Probed:
			note := p.ProbeNote
			if note == "" {
				note = "this provider was not probed in this session"
			}
			row.Availability = NotRun(note, modelsBinding)
			row.Version = NotRun(note, modelsBinding)
			row.Model = NotRun(note, modelsBinding)
			row.Quota = NotRun(note, modelsBinding)
			row.Reset = NotRun(note, modelsBinding)
			row.Allowance = NotRun(note, modelsBinding)
			row.Auth = NotRun(note, modelsBinding)
			row.Cooldown = NotRun(note, modelsBinding)
			row.UsageTokens = NotRun(note, modelsBinding)
			row.UsageCost = NotRun(note, modelsBinding)
			row.UsageCalls = NotRun(note, modelsBinding)
			row.UsageDuration = NotRun(note, modelsBinding)
		case p.Found:
			row.Availability = Known("installed", modelsBinding)
			if p.Version != "" {
				row.Version = Known(p.Version, modelsBinding)
			} else {
				row.Version = Unknown("the provider did not report a version", modelsBinding)
			}
			if p.Model != "" {
				row.Model = Known(p.Model, modelsBinding)
			} else {
				row.Model = Unknown("no model has been selected for this provider", modelsBinding)
			}
			quotaReason := p.QuotaReason
			if quotaReason == "" {
				quotaReason = "no provider exposes a machine-readable quota or capacity through a supported interface"
			}
			resetReason := p.ResetReason
			if resetReason == "" {
				resetReason = "no provider exposes a machine-readable quota reset time through a supported interface"
			}
			allowanceReason := p.AllowanceReason
			if allowanceReason == "" {
				allowanceReason = "no provider exposes a machine-readable temporary or promotional allowance through a supported interface"
			}
			row.Quota = Unknown(quotaReason, modelsBinding)
			row.Reset = Unknown(resetReason, modelsBinding)
			row.Allowance = Unknown(allowanceReason, modelsBinding)

			if p.AuthStatus != "" {
				row.Auth = formatAuthValue(app.ProviderAuthObservation{
					Provider:   p.Name,
					Status:     app.ProviderAuthStatus(p.AuthStatus),
					Reason:     p.AuthReason,
					Source:     p.AuthSource,
					ObservedAt: p.AuthTime,
				}, p.Found, at)
			} else if obs, ok := GetAuthPreflightObservation(p.Name, authObservationMaxAge); ok {
				row.Auth = formatAuthValue(app.ProviderAuthObservation{
					Provider:   p.Name,
					Status:     app.ProviderAuthStatus(obs.Status),
					Reason:     obs.Reason,
					Source:     obs.Source,
					ObservedAt: obs.ObservedAt,
				}, p.Found, at)
			} else {
				row.Auth = Unknown("no provider exposes an inspectable authentication or session state through a supported interface", modelsBinding)
			}

			if p.CooldownActive {
				row.Cooldown = formatCooldownValue(app.ProviderCooldownObservation{
					Active:     true,
					RetryAfter: p.CooldownRemaining,
					Reason:     p.CooldownReason,
				}, p.Found, at)
			} else {
				row.Cooldown = Known("no active cooldown", modelsBinding)
			}

			row.UsageTokens = formatTokensValue(p.TotalTokens, p.UsageReported, "")
			row.UsageCost = formatCostValue(p.CostUSD, p.UsageReported, "")
			row.UsageCalls = formatCallsValue(p.ModelCalls, p.UsageReported, "")
			row.UsageDuration = formatDurationValue(p.UsageDuration, p.UsageReported, "")
		default:
			reason := "the provider's command was not found on PATH"
			row.Availability = Known("not installed", modelsBinding)
			row.Version = NotRun(reason, modelsBinding)
			row.Model = NotRun(reason, modelsBinding)
			row.Quota = NotRun(reason, modelsBinding)
			row.Reset = NotRun(reason, modelsBinding)
			row.Allowance = NotRun(reason, modelsBinding)
			row.Auth = NotRun(reason, modelsBinding)
			row.Cooldown = NotRun(reason, modelsBinding)
			row.UsageTokens = NotRun(reason, modelsBinding)
			row.UsageCost = NotRun(reason, modelsBinding)
			row.UsageCalls = NotRun(reason, modelsBinding)
			row.UsageDuration = NotRun(reason, modelsBinding)
		}
		snap.ProviderDetails = append(snap.ProviderDetails, row)
	}
	snap.ProviderDetail = Known(fmt.Sprintf("%d providers detailed", len(snap.ProviderDetails)), modelsBinding)
}

func (s *ModelsFeed) readCodexGovernance(ctx context.Context, snap *ModelsSnapshot) {
	reader, ok := s.Reader.(CodexGovernanceReader)
	if !ok {
		snap.CodexStatus = Unknown("no codex governance reader attached", modelsBinding)
		return
	}
	health, err := reader.CodexHealth(ctx)
	if err != nil {
		snap.CodexStatus = Errored(fmt.Sprintf("codex health check failed: %v", err), modelsBinding)
	} else {
		snap.CodexHealth = health
		if health.Available {
			snap.CodexStatus = Known(fmt.Sprintf("codex available (%s)", health.Version), modelsBinding)
		} else {
			snap.CodexStatus = Value{Text: "UNAVAILABLE", Status: TruthKnown, Reason: health.Verdict, Source: modelsBinding}
		}
	}
	if models, def, mErr := reader.CodexModels(ctx); mErr == nil {
		snap.CodexModels = models
		snap.CodexDefaultModel = def
	}
	if sel, sErr := reader.SelectedCodexModel(ctx); sErr == nil {
		snap.CodexSelectedModel = sel
	}
	if sessions, sessErr := reader.CodexSessions(ctx); sessErr == nil {
		snap.CodexSessions = sessions
	}
	if plugins, skills, pErr := reader.CodexPlugins(ctx); pErr == nil {
		snap.CodexPlugins = plugins
		snap.CodexSkills = skills
	}
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
