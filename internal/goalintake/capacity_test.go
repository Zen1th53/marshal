package goalintake_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
)

func intPtr(v int) *int { return &v }

func governed(provider string, capacity goalintake.Capacity) goalintake.Candidate {
	return goalintake.Candidate{
		Provider: provider, Model: "m", Capacity: capacity,
		Governance: constitution.GovernanceVerified,
	}
}

// The central invariant: a figure nobody measured is never displayed as one.
func TestUnknownCapacityIsNeverPresentedAsANumber(t *testing.T) {
	capacity := goalintake.UnknownCapacity("codex", true)

	if capacity.Known() {
		t.Fatal("a provider that reports nothing claimed to know its capacity")
	}
	if capacity.Remaining != nil || capacity.Limit != nil || capacity.ResetsAt != nil {
		t.Fatal("an unmeasured provider carries invented figures")
	}
	description := capacity.Describe()
	if !strings.Contains(description, "unknown") {
		t.Fatalf("an unmeasured provider was not described as unknown: %q", description)
	}
	// No digits at all, so nothing can be read as a quota figure.
	for _, r := range description {
		if r >= '0' && r <= '9' {
			t.Fatalf("an unmeasured provider produced a numeric claim: %q", description)
		}
	}
}

// Zero remaining and unknown remaining must be distinguishable, or an
// unmeasured provider looks exhausted and an exhausted one looks unmeasured.
func TestZeroRemainingIsNotTheSameAsUnknown(t *testing.T) {
	unknown := goalintake.UnknownCapacity("codex", true)
	exhausted := goalintake.Capacity{
		Provider: "codex", Available: true, Source: goalintake.QuotaFromProvider,
		Remaining: intPtr(0),
	}

	if unknown.Exhausted() {
		t.Fatal("a provider nobody measured was treated as exhausted")
	}
	if !exhausted.Exhausted() {
		t.Fatal("a provider that reported zero was not treated as exhausted")
	}
	if unknown.Describe() == exhausted.Describe() {
		t.Fatal("unknown and exhausted capacity read identically to a user")
	}
}

// A reset time is only shown when the provider gave one. It is the figure a
// user plans around most directly, so estimating it is the worst case.
func TestResetTimeIsNeverEstimated(t *testing.T) {
	noReset := goalintake.Capacity{
		Provider: "codex", Available: true, Source: goalintake.QuotaFromProvider,
		Remaining: intPtr(0),
	}
	description := noReset.Describe()
	if !strings.Contains(description, "did not say when it resets") {
		t.Fatalf("a missing reset time was not stated: %q", description)
	}

	reset := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	withReset := noReset
	withReset.ResetsAt = &reset
	if !strings.Contains(withReset.Describe(), "2026") {
		t.Fatalf("a reported reset time was not shown: %q", withReset.Describe())
	}
}

// Observed usage is a floor, not a total, and says so: MARSHAL sees only its
// own requests, not anything else sharing the account.
func TestObservedCapacityIsDescribedAsAFloor(t *testing.T) {
	observed := goalintake.Capacity{
		Provider: "codex", Available: true, Source: goalintake.QuotaFromObservation,
	}
	description := observed.Describe()
	if !strings.Contains(description, "floor") {
		t.Fatalf("observed usage was not qualified as a floor: %q", description)
	}
}

// Governance outranks capacity: a provider MARSHAL cannot prove it controls is
// not the primary choice however much capacity it reports.
func TestGovernanceOutranksCapacity(t *testing.T) {
	plenty := goalintake.Capacity{
		Provider: "unverified", Available: true, Source: goalintake.QuotaFromProvider,
		Remaining: intPtr(10000), Limit: intPtr(10000),
	}
	selection := goalintake.Select([]goalintake.Candidate{
		{Provider: "unverified", Model: "m", Capacity: plenty, Governance: constitution.GovernanceUnverified},
		governed("verified", goalintake.UnknownCapacity("verified", true)),
	})

	if selection.Provider != "verified" {
		t.Fatalf("selected %q; a governed provider with unknown capacity should outrank an ungoverned one with plenty",
			selection.Provider)
	}
	if selection.Degraded {
		t.Fatal("a verified provider was reported as degraded")
	}
}

// Unknown capacity does not disqualify a provider. Most providers report no
// quota at all, and treating silence as exhaustion would make MARSHAL unusable.
func TestUnknownCapacityDoesNotDisqualify(t *testing.T) {
	selection := goalintake.Select([]goalintake.Candidate{
		governed("codex", goalintake.UnknownCapacity("codex", true)),
	})
	if !selection.Usable() {
		t.Fatalf("a provider with unknown capacity was rejected: %s", selection.Reason)
	}
	if !strings.Contains(selection.Reason, "unknown") {
		t.Fatalf("the reason does not admit that capacity is unknown: %q", selection.Reason)
	}
}

// A provider that genuinely reported zero is skipped, and the next is used.
func TestExhaustedProviderFallsBack(t *testing.T) {
	exhausted := goalintake.Capacity{
		Provider: "codex", Available: true, Source: goalintake.QuotaFromProvider,
		Remaining: intPtr(0),
	}
	selection := goalintake.Select([]goalintake.Candidate{
		governed("codex", exhausted),
		governed("claude", goalintake.UnknownCapacity("claude", true)),
	})
	if selection.Provider != "claude" {
		t.Fatalf("selected %q rather than falling back past an exhausted provider", selection.Provider)
	}
}

// A provider MARSHAL cannot govern is not used at all, not even as a fallback:
// that is an uncontrolled capability rather than a reduced one.
func TestUngovernableProviderIsNotUsed(t *testing.T) {
	selection := goalintake.Select([]goalintake.Candidate{
		{Provider: "rogue", Model: "m", Governance: constitution.GovernanceUnavailable,
			Capacity: goalintake.UnknownCapacity("rogue", true)},
	})
	if selection.Usable() {
		t.Fatalf("an ungovernable provider was selected: %+v", selection)
	}
	if !strings.Contains(selection.Reason, "cannot be governed") {
		t.Fatalf("the reason does not name the cause: %q", selection.Reason)
	}
	for _, fallback := range selection.Fallbacks {
		if fallback == "rogue" {
			t.Fatal("an ungovernable provider was offered as a fallback")
		}
	}
}

// A degraded provider is usable and is reported as degraded, so a caller can
// say so rather than implying the selection was fully informed.
func TestDegradedProviderIsUsableAndSaysSo(t *testing.T) {
	selection := goalintake.Select([]goalintake.Candidate{
		{Provider: "codex", Model: "m", Governance: constitution.GovernanceDegraded,
			Capacity: goalintake.UnknownCapacity("codex", true)},
	})
	if !selection.Usable() {
		t.Fatal("a degraded provider was rejected outright")
	}
	if !selection.Degraded {
		t.Fatal("a degraded provider was not reported as degraded")
	}
	if !strings.Contains(selection.Reason, "reduced mode") {
		t.Fatalf("the reason does not explain the degradation: %q", selection.Reason)
	}
}

// With nothing usable, the reason names the actual causes rather than failing
// generically.
func TestNoUsableProviderExplainsWhy(t *testing.T) {
	offline := goalintake.UnknownCapacity("offline", false)
	exhausted := goalintake.Capacity{
		Provider: "spent", Available: true, Source: goalintake.QuotaFromProvider, Remaining: intPtr(0),
	}
	selection := goalintake.Select([]goalintake.Candidate{
		governed("offline", offline),
		governed("spent", exhausted),
	})
	if selection.Usable() {
		t.Fatal("a provider was selected when none was usable")
	}
	for _, expected := range []string{"unavailable", "out of capacity", "offline", "spent"} {
		if !strings.Contains(selection.Reason, expected) {
			t.Fatalf("the reason omits %q: %s", expected, selection.Reason)
		}
	}

	// No candidates at all is its own message.
	if goalintake.Select(nil).Usable() {
		t.Fatal("a provider was selected from an empty list")
	}
}

// Fallbacks are ordered and exclude the chosen provider, so a caller can walk
// them without re-trying what already failed.
func TestFallbacksAreOrderedAndExcludeTheChoice(t *testing.T) {
	selection := goalintake.Select([]goalintake.Candidate{
		{Provider: "degraded", Model: "m", Governance: constitution.GovernanceDegraded,
			Capacity: goalintake.UnknownCapacity("degraded", true)},
		governed("verified", goalintake.UnknownCapacity("verified", true)),
		{Provider: "unverified", Model: "m", Governance: constitution.GovernanceUnverified,
			Capacity: goalintake.UnknownCapacity("unverified", true)},
	})

	if selection.Provider != "verified" {
		t.Fatalf("selected %q, want the verified provider", selection.Provider)
	}
	if len(selection.Fallbacks) != 2 {
		t.Fatalf("fallbacks were %v", selection.Fallbacks)
	}
	if selection.Fallbacks[0] != "degraded" {
		t.Fatalf("fallbacks are not ordered by governance: %v", selection.Fallbacks)
	}
	for _, fallback := range selection.Fallbacks {
		if fallback == selection.Provider {
			t.Fatal("the chosen provider appears in its own fallback list")
		}
	}
}

// Selection is deterministic, so two surfaces cannot pick different providers
// for the same situation.
func TestSelectionIsDeterministic(t *testing.T) {
	candidates := []goalintake.Candidate{
		governed("a", goalintake.UnknownCapacity("a", true)),
		governed("b", goalintake.UnknownCapacity("b", true)),
		{Provider: "c", Model: "m", Governance: constitution.GovernanceDegraded,
			Capacity: goalintake.UnknownCapacity("c", true)},
	}
	first := goalintake.Select(candidates)
	for i := 0; i < 10; i++ {
		next := goalintake.Select(candidates)
		if next.Provider != first.Provider || len(next.Fallbacks) != len(first.Fallbacks) {
			t.Fatal("repeated selection over identical candidates differed")
		}
	}
}
