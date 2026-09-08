package learning

import (
	"errors"
	"testing"
	"time"
)

func obs(provider string, o Outcome, cluster string, selected bool) RoutingObservation {
	return RoutingObservation{
		TaskClass: "refactor", Provider: provider, ProviderVersion: "2.0",
		Model: "m", Outcome: o, Selected: selected, ClusterID: cluster,
		Observed: time.Now().UTC(),
	}
}

func goodGovernance() Governance {
	return Governance{
		GovernableProviders: map[string]bool{"alpha": true},
		KnownVersions:       map[string]string{"alpha": "2.0"},
		MinClusters:         2,
		MaxRollout:          0.25,
		CapabilityFreshness: 24 * time.Hour,
	}
}

func goodProposal() RoutingProposal {
	trust := AggregateTrust([]RoutingObservation{
		obs("alpha", OutcomeVerifiedComplete, "c1", true),
		obs("alpha", OutcomeFailed, "c2", false),
	})
	return RoutingProposal{
		ID: "rp1", Key: TrustKey{"refactor", "alpha", "2.0", "m"},
		Rationale: "measured", Trust: trust[TrustKey{"refactor", "alpha", "2.0", "m"}],
		RolloutFraction: 0.1, Reversible: true,
	}
}

// Failures and unselected routes must be counted, or every provider looks good
// at whatever it happened to be given.
func TestAggregateIncludesFailuresAndTracksSelectionBias(t *testing.T) {
	trust := AggregateTrust([]RoutingObservation{
		obs("alpha", OutcomeVerifiedComplete, "c1", true),
		obs("alpha", OutcomeFailed, "c2", true),
		obs("alpha", OutcomeBlocked, "c2", true),
	})
	got := trust[TrustKey{"refactor", "alpha", "2.0", "m"}]

	if got.Verified != 1 || got.Failed != 1 || got.Blocked != 1 {
		t.Fatalf("failures dropped: %+v", got)
	}
	if got.Total() != 3 {
		t.Fatalf("total %d want 3", got.Total())
	}
	if got.Clusters != 2 {
		t.Fatalf("clusters %d want 2", got.Clusters)
	}
	// Every observation was a selected route, so the record is selection biased.
	if !got.SelectionBiased {
		t.Fatal("selection bias not detected")
	}

	// One unselected observation is enough to lift the flag.
	mixed := AggregateTrust([]RoutingObservation{
		obs("alpha", OutcomeVerifiedComplete, "c1", true),
		obs("alpha", OutcomeFailed, "c2", false),
	})
	if mixed[TrustKey{"refactor", "alpha", "2.0", "m"}].SelectionBiased {
		t.Fatal("selection bias reported with unselected observations present")
	}
}

// An unmeasured cost must never be reported as zero.
func TestUnmeasuredCostIsNotZero(t *testing.T) {
	trust := AggregateTrust([]RoutingObservation{obs("alpha", OutcomeVerifiedComplete, "c1", true)})
	got := trust[TrustKey{"refactor", "alpha", "2.0", "m"}]
	if got.MeasuredCostMicros != nil || got.MeasuredLatencyMillis != nil {
		t.Fatalf("invented a measurement: %+v", got)
	}

	cost := int64(500)
	measured := obs("alpha", OutcomeVerifiedComplete, "c1", true)
	measured.CostMicros = &cost
	trust = AggregateTrust([]RoutingObservation{measured})
	got = trust[TrustKey{"refactor", "alpha", "2.0", "m"}]
	if got.MeasuredCostMicros == nil || *got.MeasuredCostMicros != 500 {
		t.Fatalf("measured cost lost: %+v", got.MeasuredCostMicros)
	}
}

func TestGovernanceVetoBlocksUnsafeRoutingChanges(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name   string
		mutate func(*RoutingProposal, *Governance)
		reason string
	}{
		{"ungovernable provider", func(p *RoutingProposal, g *Governance) {
			g.GovernableProviders = map[string]bool{}
		}, "provider is not governable"},
		{"version drift", func(p *RoutingProposal, g *Governance) {
			g.KnownVersions["alpha"] = "3.0"
		}, "provider version drift"},
		{"security regression", func(p *RoutingProposal, g *Governance) {
			g.SecurityRegression = true
		}, "open security regression"},
		{"one cluster only", func(p *RoutingProposal, g *Governance) {
			p.Trust.Clusters = 1
		}, "insufficient independent evidence"},
		{"selection biased", func(p *RoutingProposal, g *Governance) {
			p.Trust.SelectionBiased = true
		}, "observations are selection biased"},
		{"unbounded rollout", func(p *RoutingProposal, g *Governance) {
			p.RolloutFraction = 1.0
		}, "rollout not bounded"},
		{"irreversible", func(p *RoutingProposal, g *Governance) {
			p.Reversible = false
		}, "proposal is not reversible"},
		{"task class missing", func(p *RoutingProposal, g *Governance) {
			p.Key.TaskClass = ""
		}, "task class mismatch"},
		{"stale capability data", func(p *RoutingProposal, g *Governance) {
			p.Trust.LastObserved = now.Add(-72 * time.Hour)
		}, "stale capability data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, g := goodProposal(), goodGovernance()
			tt.mutate(&p, &g)

			reasons := Veto(p, g, now)
			found := false
			for _, r := range reasons {
				if r == tt.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("reason %q not raised, got %v", tt.reason, reasons)
			}
			if _, err := ApplyProposal(p, g, now); !errors.Is(err, ErrGovernanceVeto) {
				t.Fatalf("vetoed proposal applied: %v", err)
			}
		})
	}

	// With governance satisfied the proposal proceeds.
	if _, err := ApplyProposal(goodProposal(), goodGovernance(), now); err != nil {
		t.Fatalf("sound proposal refused: %v", err)
	}
}

// A provider praising itself, or one run succeeding, must not move routing.
func TestSelfReportAndSingleRunCannotMoveRouting(t *testing.T) {
	now := time.Now().UTC()

	single := AggregateTrust([]RoutingObservation{obs("alpha", OutcomeVerifiedComplete, "c1", true)})
	p := RoutingProposal{
		ID: "rp", Key: TrustKey{"refactor", "alpha", "2.0", "m"},
		Rationale:       "the provider reported excellent results",
		Trust:           single[TrustKey{"refactor", "alpha", "2.0", "m"}],
		RolloutFraction: 0.1, Reversible: true,
	}
	if _, err := ApplyProposal(p, goodGovernance(), now); !errors.Is(err, ErrGovernanceVeto) {
		t.Fatal("one selected run moved routing")
	}
}
