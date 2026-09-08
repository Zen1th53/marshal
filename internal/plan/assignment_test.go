package plan_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

var probeTime = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// governedCandidate builds a harness whose evidence is current and complete,
// which is the only kind that reaches VERIFIED_GOVERNED.
func governedCandidate(name, provider string) plan.HarnessCandidate {
	return plan.HarnessCandidate{
		Profile: model.HarnessProfile{
			Harness:          name,
			InstalledVersion: "1.0.0",
			BinaryPath:       "/usr/bin/" + name,
			SupportedModels:  []string{name + "-model"},
			DefaultModel:     name + "-model",
			FeatureSupport: map[string]model.FeatureStatus{
				"effort":        model.StatusNative,
				"approval_mode": model.StatusNative,
				"sandbox_mode":  model.StatusNative,
				"output_format": model.StatusNative,
			},
			ReasoningKnobs:  []string{"effort"},
			ProbeEvidenceID: "EVIDENCE-" + name,
			ProbedAt:        probeTime,
			ExpiresAt:       probeTime.Add(24 * time.Hour),
		},
		InstalledVersion: "1.0.0",
		Provider:         provider,
		Capacity:         goalintake.UnknownCapacity(provider, true),
		ContextTokens:    200000,
	}
}

func assignRequest(team plan.Team, candidates ...plan.HarnessCandidate) plan.AssignRequest {
	return plan.AssignRequest{
		Team:       team,
		Tasks:      coveringTasks(),
		Assessment: routineAssessment(),
		Candidates: candidates,
		Now:        planTime,
	}
}

func soloTeam() plan.Team {
	return plan.Team{Members: []plan.Member{
		{Role: plan.RoleDeveloper, Reason: "does the work"},
	}}
}

func workerAndCheckerTeam() plan.Team {
	return plan.Team{Members: []plan.Member{
		{Role: plan.RoleDeveloper, Reason: "does the work"},
		{Role: plan.RoleQA, Reason: "checks the work", Independent: true},
	}}
}

func TestGovernedHarnessIsAssignedWithEvidencedConfiguration(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), governedCandidate("codex", "openai")))

	if !assigned.Complete() {
		t.Fatalf("a governed harness left roles unresolved: %v", assigned.Unresolved)
	}
	developer, ok := assigned.For(plan.RoleDeveloper)
	if !ok {
		t.Fatal("the developer got no assignment")
	}
	if developer.Harness != "codex" || developer.Provider != "openai" {
		t.Fatalf("assigned %s/%s", developer.Harness, developer.Provider)
	}
	// The model comes from the probe, never from a guess.
	if developer.Model != "codex-model" {
		t.Fatalf("model was %q, want the profile's default", developer.Model)
	}
	if developer.Governance != constitution.GovernanceVerified {
		t.Fatalf("governance state was %s", developer.Governance)
	}
	// The probe timestamp travels, so a reader can judge how old the evidence is.
	if !developer.ProbedAt.Equal(probeTime) {
		t.Fatal("the assignment does not record when the harness was probed")
	}
	if strings.TrimSpace(developer.Reason) == "" {
		t.Fatal("the assignment gives no reason")
	}
}

// Governance outranks everything. A faster, higher-capacity, ungovernable
// harness still loses.
func TestUngovernableHarnessIsNeverAssigned(t *testing.T) {
	ungovernable := governedCandidate("rogue", "rogue-provider")
	ungovernable.InstalledVersion = "" // not installed: UNAVAILABLE
	positive := 1000
	ungovernable.Capacity = goalintake.Capacity{
		Provider: "rogue-provider", Available: true,
		Source: goalintake.QuotaObservationOnly(), Remaining: &positive,
	}

	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), ungovernable))

	if assigned.Complete() {
		t.Fatal("an ungovernable harness was assigned")
	}
	for _, assignment := range assigned.Assignments {
		if assignment.Harness == "rogue" {
			t.Fatal("the ungovernable harness reached an assignment")
		}
	}
	if len(assigned.Rejected) == 0 {
		t.Fatal("the rejection was not recorded")
	}
	if assigned.Rejected[0].Tier != plan.EligibleGovernance {
		t.Fatalf("rejected at tier %s, want governance", assigned.Rejected[0].Tier)
	}
}

// An ungovernable harness is not a fallback either: a fallback that escapes
// governance would turn every outage into a bypass.
func TestUngovernableHarnessIsNotEvenAFallback(t *testing.T) {
	ungovernable := governedCandidate("rogue", "rogue-provider")
	ungovernable.InstalledVersion = ""

	assigned := plan.AssignHarnesses(assignRequest(soloTeam(),
		governedCandidate("codex", "openai"), ungovernable))

	developer, ok := assigned.For(plan.RoleDeveloper)
	if !ok {
		t.Fatal("no assignment")
	}
	if developer.Fallback != nil && developer.Fallback.Harness == "rogue" {
		t.Fatal("an ungovernable harness was offered as a fallback")
	}
}

// A fallback names what does not transfer. Approvals in particular are bound to
// what was approved, and changing provider changes that.
func TestFallbackNamesWhatMustBeRevalidated(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(soloTeam(),
		governedCandidate("codex", "openai"), governedCandidate("claude", "anthropic")))

	developer, _ := assigned.For(plan.RoleDeveloper)
	if developer.Fallback == nil {
		t.Fatal("a second governed harness produced no fallback")
	}
	if developer.Fallback.Harness == developer.Harness {
		t.Fatal("the fallback is the same harness as the primary")
	}
	joined := strings.Join(developer.Fallback.Revalidate, " ")
	if !strings.Contains(joined, "approval") {
		t.Fatalf("the fallback does not say approvals must be re-sought: %v", developer.Fallback.Revalidate)
	}
}

// Known-zero quota disqualifies. UNKNOWN does not, because treating unknown as
// empty would strand work on a guess.
func TestKnownZeroQuotaDisqualifiesButUnknownDoesNot(t *testing.T) {
	zero := governedCandidate("exhausted", "provider-a")
	none := 0
	zero.Capacity = goalintake.Capacity{
		Provider: "provider-a", Available: true,
		Source: goalintake.QuotaObservationOnly(), Remaining: &none,
	}
	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), zero))
	if assigned.Complete() {
		t.Fatal("a provider known to have no quota was assigned")
	}

	// Unknown capacity is usable.
	unknown := governedCandidate("unmeasured", "provider-b")
	unknown.Capacity = goalintake.UnknownCapacity("provider-b", true)
	usable := plan.AssignHarnesses(assignRequest(soloTeam(), unknown))
	if !usable.Complete() {
		t.Fatalf("a provider with unknown quota was skipped: %v", usable.Unresolved)
	}
	developer, _ := usable.For(plan.RoleDeveloper)
	// Nothing invents a figure for what was never measured.
	if developer.Capacity.Remaining != nil || developer.Capacity.ResetsAt != nil {
		t.Fatal("a quota figure was invented for an unmeasured provider")
	}
}

// A profile describing a different build is evidence about that build.
func TestVersionDriftDisqualifiesTheHarness(t *testing.T) {
	drifted := governedCandidate("codex", "openai")
	drifted.InstalledVersion = "2.0.0" // the profile describes 1.0.0

	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), drifted))
	if assigned.Complete() {
		t.Fatal("a harness whose probe describes a different build was assigned")
	}
	if len(assigned.Rejected) == 0 {
		t.Fatal("the drift rejection was not recorded")
	}
}

// Unsupported settings are omitted and recorded, never passed anyway.
func TestUnsupportedSettingsAreOmittedRatherThanInvented(t *testing.T) {
	sparse := governedCandidate("minimal", "provider")
	// This harness supports nothing beyond running a model.
	sparse.Profile.FeatureSupport = map[string]model.FeatureStatus{}
	sparse.Profile.ReasoningKnobs = nil

	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), sparse))
	developer, ok := assigned.For(plan.RoleDeveloper)
	if !ok {
		t.Fatalf("a harness with no knobs was rejected entirely: %v", assigned.Unresolved)
	}
	if developer.Native.Effort != "" {
		t.Fatalf("an unsupported effort setting was configured anyway: %q", developer.Native.Effort)
	}
	if len(developer.Native.Omitted) == 0 {
		t.Fatal("the omitted settings were not recorded, so they would vanish silently")
	}
}

// MARSHAL never asks a harness to skip its own confirmations.
func TestApprovalsAreNeverConfiguredAway(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), governedCandidate("codex", "openai")))
	developer, _ := assigned.For(plan.RoleDeveloper)

	lowered := strings.ToLower(developer.Native.ApprovalMode)
	for _, forbidden := range []string{"bypass", "dangerous", "no-confirm", "skip", "never"} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("approval mode %q weakens confirmations", developer.Native.ApprovalMode)
		}
	}
	if developer.Native.ToolPolicy != "least-privilege" {
		t.Fatalf("tool policy was %q", developer.Native.ToolPolicy)
	}
}

// An independent checker prefers a harness the workers are not on, since a
// verifier sharing the worker's harness shares its blind spots.
func TestIndependentCheckerPrefersASeparateHarness(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(workerAndCheckerTeam(),
		governedCandidate("codex", "openai"), governedCandidate("claude", "anthropic")))

	developer, ok := assigned.For(plan.RoleDeveloper)
	if !ok {
		t.Fatal("no developer assignment")
	}
	qa, ok := assigned.For(plan.RoleQA)
	if !ok {
		t.Fatal("no QA assignment")
	}
	if qa.Harness == developer.Harness {
		t.Fatalf("the checker runs on the same harness as the work (%s) despite an alternative", qa.Harness)
	}
	if qa.SharesWorkerHarness {
		t.Fatal("a separately-assigned checker was flagged as sharing")
	}
}

// With only one governed harness the check still happens, but the weaker claim
// is recorded rather than hidden.
func TestSharedHarnessVerificationIsRecordedNotHidden(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(workerAndCheckerTeam(),
		governedCandidate("codex", "openai")))

	qa, ok := assigned.For(plan.RoleQA)
	if !ok {
		t.Fatalf("the checker was dropped rather than sharing: %v", assigned.Unresolved)
	}
	if !qa.SharesWorkerHarness {
		t.Fatal("a check sharing the worker's harness was not flagged, so it reads as fully independent")
	}
	if !strings.Contains(strings.ToLower(qa.Reason), "same harness") {
		t.Fatalf("the reason does not disclose the shared harness: %q", qa.Reason)
	}
}

// A role with nothing eligible blocks the plan rather than being dropped.
func TestRoleWithNoEligibleHarnessIsUnresolved(t *testing.T) {
	assigned := plan.AssignHarnesses(assignRequest(soloTeam()))
	if assigned.Complete() {
		t.Fatal("a team with no candidates produced a complete assignment")
	}
	if len(assigned.Unresolved) == 0 {
		t.Fatal("the unassignable role was silently dropped")
	}
	if !strings.Contains(assigned.Unresolved[0], string(plan.RoleDeveloper)) {
		t.Fatalf("the unresolved entry does not name the role: %v", assigned.Unresolved)
	}
}

// A harness whose probe names no model is unusable, rather than being given a
// plausible-sounding name.
//
// This is the invariant most likely to be violated by a helpful-looking
// default: a synthesised name like "<harness>-default" reads fine in a plan
// and fails at execution time, when it is expensive to diagnose. Nothing here
// may fill the gap.
func TestNoModelIsInventedWhenTheProbeNamesNone(t *testing.T) {
	silent := governedCandidate("silent", "provider")
	silent.Profile.DefaultModel = ""
	silent.Profile.SupportedModels = nil

	assigned := plan.AssignHarnesses(assignRequest(soloTeam(), silent))

	if assigned.Complete() {
		t.Fatal("a harness whose probe names no model was assigned one anyway")
	}
	for _, assignment := range assigned.Assignments {
		if assignment.Model != "" {
			t.Fatalf("model %q was invented; the probe named none", assignment.Model)
		}
	}
	if len(assigned.Rejected) == 0 || assigned.Rejected[0].Tier != plan.EligibleFit {
		t.Fatalf("rejections were %+v, want a task-fit rejection", assigned.Rejected)
	}

	// A harness that lists models but names no default uses a listed one
	// rather than inventing: reading evidence is not the same as guessing.
	listed := governedCandidate("listed", "provider")
	listed.Profile.DefaultModel = ""
	listed.Profile.SupportedModels = []string{"listed-a", "listed-b"}
	usable := plan.AssignHarnesses(assignRequest(soloTeam(), listed))
	developer, ok := usable.For(plan.RoleDeveloper)
	if !ok {
		t.Fatalf("a harness with listed models was rejected: %v", usable.Unresolved)
	}
	if developer.Model != "listed-a" {
		t.Fatalf("model was %q, want one the profile actually lists", developer.Model)
	}
}

// A required capability the harness lacks disqualifies it, and probe_required
// is not treated as support.
func TestMissingCapabilityDisqualifies(t *testing.T) {
	request := assignRequest(soloTeam(), governedCandidate("codex", "openai"))
	request.RequiredCapabilities = []string{"web_search"}

	assigned := plan.AssignHarnesses(request)
	if assigned.Complete() {
		t.Fatal("a harness missing a required capability was assigned")
	}
	if assigned.Rejected[0].Tier != plan.EligibleCapability {
		t.Fatalf("rejected at tier %s, want capabilities", assigned.Rejected[0].Tier)
	}
}

// Work larger than a known context window disqualifies; an unknown window does
// not, because refusing on an unmeasured number would be inventing it.
func TestContextFitOnlyAppliesWhenBothSidesAreKnown(t *testing.T) {
	small := governedCandidate("small", "provider")
	small.ContextTokens = 1000
	request := assignRequest(soloTeam(), small)
	request.EstimatedContextTokens = 500000

	if plan.AssignHarnesses(request).Complete() {
		t.Fatal("work larger than the context window was assigned to it")
	}

	unknownWindow := governedCandidate("unknown", "provider")
	unknownWindow.ContextTokens = 0
	unknownRequest := assignRequest(soloTeam(), unknownWindow)
	unknownRequest.EstimatedContextTokens = 500000
	if !plan.AssignHarnesses(unknownRequest).Complete() {
		t.Fatal("an unmeasured context window was treated as too small")
	}
}

// Assignment is deterministic, so the same plan shown twice reads the same.
func TestAssignmentIsDeterministic(t *testing.T) {
	request := assignRequest(workerAndCheckerTeam(),
		governedCandidate("codex", "openai"), governedCandidate("claude", "anthropic"))

	first := plan.AssignHarnesses(request)
	for i := 0; i < 10; i++ {
		next := plan.AssignHarnesses(request)
		if len(next.Assignments) != len(first.Assignments) {
			t.Fatal("repeated assignment produced different counts")
		}
		for j := range next.Assignments {
			if next.Assignments[j].Role != first.Assignments[j].Role ||
				next.Assignments[j].Harness != first.Assignments[j].Harness {
				t.Fatal("repeated assignment produced different results")
			}
		}
	}
}
