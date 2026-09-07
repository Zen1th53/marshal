package constitution_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func TestVersionCompatibility(t *testing.T) {
	cases := []struct {
		name       string
		session    constitution.Version
		runtime    constitution.Version
		compatible bool
	}{
		{"identical", constitution.Version{1, 0, 0}, constitution.Version{1, 0, 0}, true},
		{"runtime ahead by patch", constitution.Version{1, 0, 0}, constitution.Version{1, 0, 3}, true},
		{"runtime ahead by minor", constitution.Version{1, 0, 0}, constitution.Version{1, 2, 0}, true},
		{"session ahead of runtime", constitution.Version{1, 3, 0}, constitution.Version{1, 2, 0}, false},
		{"major mismatch upward", constitution.Version{1, 0, 0}, constitution.Version{2, 0, 0}, false},
		{"major mismatch downward", constitution.Version{2, 0, 0}, constitution.Version{1, 0, 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.session.CompatibleWith(tc.runtime); got != tc.compatible {
				t.Fatalf("session %s under runtime %s: compatible=%v, want %v",
					tc.session, tc.runtime, got, tc.compatible)
			}
		})
	}
}

func TestParseVersionRejectsMalformed(t *testing.T) {
	for _, input := range []string{"", "1", "1.0", "1.0.0.0", "1.0.x", "1..0", "-1.0.0", "a.b.c"} {
		if _, err := constitution.ParseVersion(input); err == nil {
			t.Fatalf("ParseVersion(%q) accepted a malformed version", input)
		}
	}
	parsed, err := constitution.ParseVersion(" 2.11.4 ")
	if err != nil {
		t.Fatalf("ParseVersion returned an unexpected error: %v", err)
	}
	if parsed.Compare(constitution.Version{Major: 2, Minor: 11, Patch: 4}) != 0 {
		t.Fatalf("ParseVersion produced %s, want 2.11.4", parsed)
	}
}

func TestDefaultRegistryIsWellFormed(t *testing.T) {
	registry := constitution.Default()
	if registry.Version().Compare(constitution.Current) != 0 {
		t.Fatalf("default registry reports version %s, want %s", registry.Version(), constitution.Current)
	}
	if registry.Len() == 0 {
		t.Fatal("default registry is empty")
	}
	for _, inv := range registry.All() {
		info := constitution.Describe(inv.Reason)
		if info.Recovery == "" {
			t.Fatalf("invariant %s maps to reason %s with no described recovery", inv.ID, inv.Reason)
		}
		if info.Code != inv.Reason {
			t.Fatalf("invariant %s reason %s is not in the reason catalog", inv.ID, inv.Reason)
		}
	}
}

// A hard invariant must never be described as retryable: retrying an identical
// request is exactly the bypass the invariant exists to refuse.
func TestHardInvariantsAreNotRetryable(t *testing.T) {
	for _, inv := range constitution.Default().All() {
		if inv.Severity != constitution.SeverityHard {
			continue
		}
		if constitution.Describe(inv.Reason).Retryable {
			t.Fatalf("hard invariant %s maps to retryable reason %s", inv.ID, inv.Reason)
		}
	}
}

func TestRegistryRejectsDuplicateAndMalformedInvariants(t *testing.T) {
	valid := constitution.Invariant{
		ID: "X-1", Article: "I", Severity: constitution.SeverityHard,
		Reason: constitution.ReasonFalseSuccess, Explanation: "explained",
	}
	if _, err := constitution.NewRegistry(constitution.Current, []constitution.Invariant{valid, valid}); err == nil {
		t.Fatal("registry accepted a duplicate invariant ID")
	}
	noExplanation := valid
	noExplanation.Explanation = ""
	if _, err := constitution.NewRegistry(constitution.Current, []constitution.Invariant{noExplanation}); err == nil {
		t.Fatal("registry accepted an invariant with no user explanation")
	}
	badSeverity := valid
	badSeverity.Severity = "ADVISORY"
	if _, err := constitution.NewRegistry(constitution.Current, []constitution.Invariant{badSeverity}); err == nil {
		t.Fatal("registry accepted an invariant with an invalid severity")
	}
	badDomain := valid
	badDomain.Domains = []constitution.Domain{"not-a-domain"}
	if _, err := constitution.NewRegistry(constitution.Current, []constitution.Invariant{badDomain}); err == nil {
		t.Fatal("registry accepted an invariant referencing an unknown domain")
	}
	if _, err := constitution.NewRegistry(constitution.Version{}, []constitution.Invariant{valid}); err == nil {
		t.Fatal("registry accepted a zero constitution version")
	}
	if _, err := constitution.NewRegistry(constitution.Current, nil); err == nil {
		t.Fatal("registry accepted an empty invariant set")
	}
}

// Domain-scoped invariants must be selected for their domains and only theirs,
// while unscoped invariants apply everywhere.
func TestRegistryDomainScoping(t *testing.T) {
	registry := constitution.Default()
	memoryInvariants := registry.ForDomain(constitution.DomainMemoryPromotion)
	found := false
	for _, inv := range memoryInvariants {
		if inv.ID == constitution.InvMemoryPromotionGoverned {
			found = true
		}
	}
	if !found {
		t.Fatal("memory promotion domain does not select the memory promotion invariant")
	}
	for _, inv := range registry.ForDomain(constitution.DomainRead) {
		if inv.ID == constitution.InvMemoryPromotionGoverned {
			t.Fatal("memory promotion invariant leaked into the read domain")
		}
	}
	// Project isolation is unscoped and must govern every domain, reads included.
	for _, domain := range constitution.Domains() {
		isolated := false
		for _, inv := range registry.ForDomain(domain) {
			if inv.ID == constitution.InvProjectIsolation {
				isolated = true
			}
		}
		if !isolated {
			t.Fatalf("project isolation does not apply to domain %s", domain)
		}
	}
}

func TestAuthorityHierarchyPrecedence(t *testing.T) {
	// A hard policy denial beats a goal contract that would permit.
	resolution := constitution.Resolve([]constitution.Claim{
		{Level: constitution.AuthorityGoalContract, Source: "goal-7", Directive: "proceed", Permits: true},
		{Level: constitution.AuthorityHardPolicy, Source: "policy-net", Directive: "deny egress", Permits: false},
	})
	if resolution.Permitted {
		t.Fatal("a goal contract overrode a hard policy denial")
	}
	if resolution.Governing != constitution.AuthorityHardPolicy {
		t.Fatalf("governing authority was %s, want hard-policy", resolution.Governing)
	}
	if len(resolution.Conflicts) != 1 {
		t.Fatalf("expected the overridden claim to be recorded, got %d conflicts", len(resolution.Conflicts))
	}
}

// The central Process 00 rule: an AI claim can never turn a denial into a
// permission, and the attempt is reported as overreach.
func TestAIClaimCannotOverrideRuleBearingAuthority(t *testing.T) {
	for _, aiLevel := range []constitution.AuthorityLevel{
		constitution.AuthorityControlIntelligence,
		constitution.AuthoritySpecializedAgent,
	} {
		for _, ruleLevel := range []constitution.AuthorityLevel{
			constitution.AuthorityConstitution,
			constitution.AuthorityHardPolicy,
			constitution.AuthorityUserConstraint,
			constitution.AuthorityGoalContract,
			constitution.AuthorityProjectPolicy,
			constitution.AuthorityApprovedPlan,
			constitution.AuthorityProcessState,
		} {
			resolution := constitution.Resolve([]constitution.Claim{
				{Level: aiLevel, Source: "model", Directive: "allow it", Permits: true},
				{Level: ruleLevel, Source: "rule", Directive: "deny", Permits: false},
			})
			if resolution.Permitted {
				t.Fatalf("%s overrode %s", aiLevel, ruleLevel)
			}
			if !resolution.AIOverreach() {
				t.Fatalf("%s overriding %s was not reported as AI overreach", aiLevel, ruleLevel)
			}
		}
	}
}

// A provider default is the weakest source and cannot outrank MARSHAL.
func TestProviderDefaultCannotOutrankMarshal(t *testing.T) {
	resolution := constitution.Resolve([]constitution.Claim{
		{Level: constitution.AuthorityProviderDefault, Source: "harness-config", Directive: "skip approval", Permits: true},
		{Level: constitution.AuthorityConstitution, Source: "CI-002", Directive: "require approval", Permits: false},
	})
	if resolution.Permitted {
		t.Fatal("a provider default overrode the constitution")
	}
	if resolution.Governing != constitution.AuthorityConstitution {
		t.Fatalf("governing authority was %s, want constitution", resolution.Governing)
	}
}

// Absence of any authorising claim is a denial, not a default permission.
func TestResolveFailsClosedWithNoClaims(t *testing.T) {
	if constitution.Resolve(nil).Permitted {
		t.Fatal("an empty claim set was treated as permission")
	}
	// Unknown levels are discarded rather than ranked, so a forged level
	// cannot smuggle in a permission.
	forged := constitution.Resolve([]constitution.Claim{{Level: AuthorityLevelForged, Source: "forged", Permits: true}})
	if forged.Permitted {
		t.Fatal("a claim at an unrecognised authority level was honoured")
	}
}

const AuthorityLevelForged constitution.AuthorityLevel = 99

func TestMutatingDomainClassification(t *testing.T) {
	for _, domain := range []constitution.Domain{
		constitution.DomainFileMutation, constitution.DomainShell, constitution.DomainNetwork,
		constitution.DomainRollback, constitution.DomainMemoryPromotion, constitution.DomainExternalEffect,
	} {
		if !domain.Mutating() {
			t.Fatalf("domain %s should be classified as mutating", domain)
		}
	}
	for _, domain := range []constitution.Domain{constitution.DomainRead, constitution.DomainRouting} {
		if domain.Mutating() {
			t.Fatalf("domain %s should not be classified as mutating", domain)
		}
	}
	if constitution.Domain("invented").Valid() {
		t.Fatal("an unrecognised domain was accepted as valid")
	}
}

func TestDescribeUnknownReasonFailsClosed(t *testing.T) {
	info := constitution.Describe(constitution.ReasonCode("CONST_NOT_A_REAL_CODE"))
	if info.Retryable {
		t.Fatal("an unrecognised reason code was reported as retryable")
	}
}
