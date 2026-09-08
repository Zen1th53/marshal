package goalintake

import (
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
)

// This file reports what MARSHAL knows about provider capacity, and — more
// importantly — what it does not.
//
// The invariant that shapes it is that quota, reset times and capacity are
// never invented. A figure a user reads as fact and acts on is worse than no
// figure at all: "you have 12% of your quota left" invites planning around a
// number nobody measured, and when it turns out to be fiction the user has
// already committed. So every value here either came from the provider or is
// absent, and absent is reported as absent.

// QuotaSource records where a capacity figure came from. Provenance is part of
// the value: the same number means different things depending on who said it.
type QuotaSource string

const (
	// QuotaFromProvider means the provider reported it directly.
	QuotaFromProvider QuotaSource = "provider"
	// QuotaFromObservation means MARSHAL inferred it from its own request
	// history. It is a floor, not a total, because MARSHAL sees only its own
	// usage and not anything else sharing the account.
	QuotaFromObservation QuotaSource = "observed"
	// QuotaUnknown means nothing is known. It is the honest default and is
	// never displayed as a number.
	QuotaUnknown QuotaSource = "unknown"
)

// Capacity is what is known about one provider's availability.
//
// Every optional figure is a pointer so that "zero remaining" and "nobody
// knows" are distinguishable. Collapsing them onto a plain int would make an
// unmeasured provider look exhausted, or an exhausted one look unmeasured.
type Capacity struct {
	Provider string `json:"provider"`
	// Available reports whether the provider can be reached at all. This is
	// observable without any quota information.
	Available bool `json:"available"`
	// Source records the provenance of the figures below.
	Source QuotaSource `json:"source"`
	// Remaining is the number of requests left, when the provider said so.
	Remaining *int `json:"remaining,omitempty"`
	// Limit is the ceiling, when the provider said so.
	Limit *int `json:"limit,omitempty"`
	// ResetsAt is when the window rolls over, when the provider said so.
	// MARSHAL never estimates it: a wrong reset time is the figure users plan
	// around most directly.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// ObservedAt is when this was learned, so a reader can judge its age.
	ObservedAt time.Time `json:"observed_at"`
	// Note is a user-safe explanation, used mainly to say why nothing is known.
	Note string `json:"note,omitempty"`
}

// Known reports whether any usable figure is present.
func (c Capacity) Known() bool {
	return c.Source != QuotaUnknown && (c.Remaining != nil || c.Limit != nil)
}

// Exhausted reports whether the provider is known to be out of capacity.
//
// It is deliberately conservative: unknown is not exhausted, so a provider
// nobody measured is not skipped, and a provider that genuinely reported zero
// is. Guessing in either direction would be worse than the honest answer.
func (c Capacity) Exhausted() bool {
	return c.Source != QuotaUnknown && c.Remaining != nil && *c.Remaining <= 0
}

// Describe renders capacity for a user without inventing anything.
func (c Capacity) Describe() string {
	switch {
	case !c.Available:
		return c.Provider + " is unavailable."
	case c.Exhausted():
		if c.ResetsAt != nil {
			return c.Provider + " has no capacity left until " + c.ResetsAt.Format(time.RFC1123) + "."
		}
		// No reset time was reported, so none is offered. Inventing one is
		// exactly the failure this package exists to prevent.
		return c.Provider + " has no capacity left. The provider did not say when it resets."
	case c.Remaining != nil && c.Limit != nil:
		return c.Provider + " reports capacity remaining."
	case c.Source == QuotaObservationOnly():
		return c.Provider + " is available. MARSHAL has only its own usage to go on, so this is a floor rather than a total."
	default:
		return c.Provider + " is available. Remaining capacity is unknown."
	}
}

// QuotaObservationOnly is a helper so callers do not have to import the
// constant to compare against it.
func QuotaObservationOnly() QuotaSource { return QuotaFromObservation }

// UnknownCapacity returns the honest default for a provider nothing is known
// about. It exists so callers cannot accidentally construct a Capacity whose
// zero value reads as "zero remaining".
func UnknownCapacity(provider string, available bool) Capacity {
	return Capacity{
		Provider: provider, Available: available, Source: QuotaUnknown,
		ObservedAt: time.Now().UTC(),
		Note:       "The provider does not report remaining capacity.",
	}
}

// Selection is a chosen provider together with why it was chosen.
type Selection struct {
	// Provider is the chosen provider, empty when none can be used.
	Provider string `json:"provider,omitempty"`
	// Model is the chosen model.
	Model string `json:"model,omitempty"`
	// Reason explains the choice in the user's terms.
	Reason string `json:"reason"`
	// Fallbacks are the providers that would be tried next, in order.
	Fallbacks []string `json:"fallbacks,omitempty"`
	// Governance is the harness governance state of the chosen provider.
	Governance constitution.GovernanceState `json:"governance"`
	// Degraded reports that the choice was made with less information or less
	// capability than ideal, so a caller can say so rather than implying the
	// selection was fully informed.
	Degraded bool `json:"degraded"`
}

// Usable reports whether a provider was chosen.
func (s Selection) Usable() bool { return s.Provider != "" }

// Candidate is a provider available for selection.
type Candidate struct {
	Provider string
	Model    string
	Capacity Capacity
	// Governance is the evidence-derived governance state from Process 00.
	// A provider MARSHAL cannot govern is not a candidate for real work.
	Governance constitution.GovernanceState
}

// Select chooses a provider for interpretation work.
//
// The ordering rule is that governance comes before capacity, which comes
// before preference. A provider MARSHAL cannot prove it governs is not made
// the primary choice however much capacity it reports, because capacity is
// about whether work can run and governance is about whether it can be
// controlled — and the second is the one that matters when something goes
// wrong.
//
// Unknown capacity does not disqualify a provider. Most providers do not
// report quota at all, and treating silence as exhaustion would make MARSHAL
// unusable with them.
func Select(candidates []Candidate) Selection {
	if len(candidates) == 0 {
		return Selection{Reason: "No provider is configured."}
	}

	ordered := append([]Candidate(nil), candidates...)
	sort.SliceStable(ordered, func(a, b int) bool {
		return candidateRank(ordered[a]) < candidateRank(ordered[b])
	})

	var chosen *Candidate
	var fallbacks []string
	for i := range ordered {
		candidate := ordered[i]
		if !candidate.Capacity.Available || candidate.Capacity.Exhausted() {
			continue
		}
		if candidate.Governance == constitution.GovernanceUnavailable {
			// A provider MARSHAL cannot govern at all is not used, even as a
			// fallback: it is not a reduced capability, it is an uncontrolled
			// one.
			continue
		}
		if chosen == nil {
			chosen = &ordered[i]
			continue
		}
		fallbacks = append(fallbacks, candidate.Provider)
	}

	if chosen == nil {
		return Selection{
			Reason:    unusableReason(candidates),
			Fallbacks: nil,
		}
	}

	selection := Selection{
		Provider:   chosen.Provider,
		Model:      chosen.Model,
		Fallbacks:  fallbacks,
		Governance: chosen.Governance,
		Degraded:   !chosen.Governance.Governed(),
	}
	switch {
	case selection.Degraded:
		selection.Reason = chosen.Provider + " was selected. MARSHAL cannot fully confirm control of it, so it is operating in a reduced mode."
	case chosen.Capacity.Known():
		selection.Reason = chosen.Provider + " was selected, with capacity reported by the provider."
	default:
		// The absence of a figure is stated rather than papered over.
		selection.Reason = chosen.Provider + " was selected. Its remaining capacity is unknown."
	}
	return selection
}

// candidateRank orders candidates. Lower sorts first.
func candidateRank(candidate Candidate) int {
	switch candidate.Governance {
	case constitution.GovernanceVerified:
		return 0
	case constitution.GovernanceDegraded:
		return 1
	case constitution.GovernanceUnverified:
		return 2
	default:
		return 3
	}
}

// unusableReason explains why nothing could be selected, naming the actual
// cause rather than a generic failure.
func unusableReason(candidates []Candidate) string {
	var unavailable, exhausted, ungoverned []string
	for _, candidate := range candidates {
		switch {
		case !candidate.Capacity.Available:
			unavailable = append(unavailable, candidate.Provider)
		case candidate.Capacity.Exhausted():
			exhausted = append(exhausted, candidate.Provider)
		case candidate.Governance == constitution.GovernanceUnavailable:
			ungoverned = append(ungoverned, candidate.Provider)
		}
	}
	sort.Strings(unavailable)
	sort.Strings(exhausted)
	sort.Strings(ungoverned)

	var parts []string
	if len(unavailable) > 0 {
		parts = append(parts, "unavailable: "+strings.Join(unavailable, ", "))
	}
	if len(exhausted) > 0 {
		parts = append(parts, "out of capacity: "+strings.Join(exhausted, ", "))
	}
	if len(ungoverned) > 0 {
		parts = append(parts, "cannot be governed: "+strings.Join(ungoverned, ", "))
	}
	if len(parts) == 0 {
		return "No provider could be selected."
	}
	return "No provider could be selected (" + strings.Join(parts, "; ") + ")."
}
