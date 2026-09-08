package learning

import (
	"fmt"
	"sort"
	"time"
)

// TrustKey scopes trust narrowly. Trust earned by one provider version on one
// task class says nothing about a different version or a different task, so the
// key carries all of it.
type TrustKey struct {
	TaskClass       string `json:"task_class"`
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	Model           string `json:"model"`
}

// Trust is an evidence-backed record of measured outcomes. There is no score
// invented here: the counts are what was observed, and callers read them
// alongside SelectionBiased before acting.
type Trust struct {
	Key TrustKey

	Verified int `json:"verified"`
	Partial  int `json:"partial"`
	Failed   int `json:"failed"`
	Blocked  int `json:"blocked"`

	Reworks          int `json:"reworks"`
	VerifierDisputes int `json:"verifier_disputes"`

	// Clusters counts independent evidence clusters behind these observations.
	Clusters int `json:"clusters"`

	// SelectionBiased is true when every observation came from a route that was
	// deliberately selected. A provider only ever chosen for easy work looks
	// strong for reasons that have nothing to do with capability, so this flag
	// travels with the record instead of being silently forgotten.
	SelectionBiased bool `json:"selection_biased"`

	// MeasuredCostMicros and MeasuredLatencyMillis are averages over the
	// observations that actually carried a measurement. When nothing was
	// measured they stay nil: an unmeasured cost is never reported as zero.
	MeasuredCostMicros    *int64 `json:"measured_cost_micros,omitempty"`
	MeasuredLatencyMillis *int64 `json:"measured_latency_millis,omitempty"`

	Observations int `json:"observations"`
	LastObserved time.Time
}

// Total returns how many outcomes back this record.
func (t Trust) Total() int { return t.Verified + t.Partial + t.Failed + t.Blocked }

// AggregateTrust folds observations into per-key trust records.
//
// Failed, blocked and unselected observations are folded in alongside the
// successes. Learning only from what succeeded would make every provider look
// good at whatever it was given, which is the survivorship trap this guards
// against.
func AggregateTrust(obs []RoutingObservation) map[TrustKey]Trust {
	out := map[TrustKey]Trust{}
	clusters := map[TrustKey]map[string]bool{}
	costs := map[TrustKey][]int64{}
	lats := map[TrustKey][]int64{}
	anyUnselected := map[TrustKey]bool{}

	for _, o := range obs {
		k := TrustKey{o.TaskClass, o.Provider, o.ProviderVersion, o.Model}
		t := out[k]
		t.Key = k
		switch o.Outcome {
		case OutcomeVerifiedComplete:
			t.Verified++
		case OutcomePartial:
			t.Partial++
		case OutcomeFailed:
			t.Failed++
		case OutcomeBlocked:
			t.Blocked++
		}
		t.Reworks += o.Reworks
		t.VerifierDisputes += o.VerifierDisputes
		t.Observations++
		if o.Observed.After(t.LastObserved) {
			t.LastObserved = o.Observed
		}
		if clusters[k] == nil {
			clusters[k] = map[string]bool{}
		}
		if o.ClusterID != "" {
			clusters[k][o.ClusterID] = true
		}
		if !o.Selected {
			anyUnselected[k] = true
		}
		if o.CostMicros != nil {
			costs[k] = append(costs[k], *o.CostMicros)
		}
		if o.LatencyMillis != nil {
			lats[k] = append(lats[k], *o.LatencyMillis)
		}
		out[k] = t
	}

	for k, t := range out {
		t.Clusters = len(clusters[k])
		t.SelectionBiased = !anyUnselected[k]
		if v := mean(costs[k]); v != nil {
			t.MeasuredCostMicros = v
		}
		if v := mean(lats[k]); v != nil {
			t.MeasuredLatencyMillis = v
		}
		out[k] = t
	}
	return out
}

// mean averages measured values, returning nil when nothing was measured so an
// absent measurement is never rendered as zero.
func mean(v []int64) *int64 {
	if len(v) == 0 {
		return nil
	}
	var sum int64
	for _, x := range v {
		sum += x
	}
	avg := sum / int64(len(v))
	return &avg
}

// RoutingProposal is a candidate change to how work is routed. It is a
// proposal: it takes effect only after governance declines to veto it and a
// bounded rollout confirms it.
type RoutingProposal struct {
	ID        string   `json:"proposal_id"`
	Key       TrustKey `json:"key"`
	Rationale string   `json:"rationale"`
	Trust     Trust    `json:"trust"`
	// RolloutFraction bounds the change. A proposal that wants everything at
	// once is refused: one run must not rewrite global routing.
	RolloutFraction float64 `json:"rollout_fraction"`
	Reversible      bool    `json:"reversible"`
}

// Governance describes the hard constraints a proposal must respect. These come
// from policy, not from learning, and learning may never relax them.
type Governance struct {
	// GovernableProviders lists providers MARSHAL can actually govern.
	GovernableProviders map[string]bool
	// CapabilityFreshness is how recent capability data must be.
	CapabilityFreshness time.Duration
	// KnownVersions maps a provider to the version governance currently knows.
	// A drifted version invalidates trust earned on the old one.
	KnownVersions map[string]string
	// SecurityRegression blocks any proposal when a security regression is open.
	SecurityRegression bool
	// MinClusters is the independent-cluster floor for acting on trust.
	MinClusters int
	// MaxRollout caps how much traffic one proposal may move.
	MaxRollout float64
}

// Veto applies the governance checks to a proposal and returns the reasons it
// must not proceed. An empty result means no veto; it does not mean the
// proposal is proven good, only that governance has no objection.
func Veto(p RoutingProposal, g Governance, now time.Time) []string {
	var reasons []string

	if g.SecurityRegression {
		reasons = append(reasons, "open security regression")
	}
	if !g.GovernableProviders[p.Key.Provider] {
		reasons = append(reasons, "provider is not governable")
	}
	if known, ok := g.KnownVersions[p.Key.Provider]; ok && known != p.Key.ProviderVersion {
		reasons = append(reasons, "provider version drift")
	}
	if p.Trust.Clusters < g.MinClusters {
		reasons = append(reasons, "insufficient independent evidence")
	}
	if p.Trust.Observations == 0 {
		reasons = append(reasons, "no observations")
	}
	if p.Trust.SelectionBiased {
		reasons = append(reasons, "observations are selection biased")
	}
	if g.CapabilityFreshness > 0 && !p.Trust.LastObserved.IsZero() &&
		now.Sub(p.Trust.LastObserved) > g.CapabilityFreshness {
		reasons = append(reasons, "stale capability data")
	}
	if p.Key.TaskClass == "" {
		reasons = append(reasons, "task class mismatch")
	}
	if p.RolloutFraction <= 0 || p.RolloutFraction > g.MaxRollout {
		reasons = append(reasons, "rollout not bounded")
	}
	if !p.Reversible {
		reasons = append(reasons, "proposal is not reversible")
	}

	sort.Strings(reasons)
	return reasons
}

// ApplyProposal returns the proposal only when governance raises no objection.
// Learning proposes; governance disposes. A vetoed proposal is an outcome to
// record, not an obstacle to route around.
func ApplyProposal(p RoutingProposal, g Governance, now time.Time) (RoutingProposal, error) {
	if reasons := Veto(p, g, now); len(reasons) > 0 {
		return RoutingProposal{}, fmt.Errorf("%w: %v", ErrGovernanceVeto, reasons)
	}
	return p, nil
}
