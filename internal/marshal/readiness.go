package marshal

import "fmt"

// ProviderReadiness contains observed facts, not assumptions from installation.
// Unknown authentication never qualifies a provider as runnable.
type ProviderReadiness struct {
	Installed   bool
	SignedIn    bool
	AuthUnknown bool
	Unavailable string
}

func (p ProviderReadiness) Runnable() bool {
	return p.Installed && p.SignedIn && !p.AuthUnknown && p.Unavailable == ""
}

// Readiness is the shared execution verdict. Blocker always names the first
// prerequisite to fix, with the planner taking priority over workers and closure.
type Readiness struct {
	PlannerRunnable    bool
	Workers            []string
	FinalCheckEligible bool
	ChecksOnly         bool
	ReviewPolicy       ReviewPolicy
	Blocker            string
	Guarantee          string
	NeedsConfirmation  bool
	// SetupPending marks a planner blocker that is a setup step not taken
	// yet (no planner chosen, or its sign-in not checked yet) rather than an
	// observed failure.
	SetupPending bool
}

func SignInCommand(provider string) string {
	switch provider {
	case "codex":
		return "codex login"
	case "opencode":
		return "opencode auth login"
	default:
		return provider
	}
}

// ComputeReadiness resolves the effective policy supplied by the caller. For
// an existing run this must be its pinned EffectiveReviewPolicy, not settings.
func ComputeReadiness(planner string, providers map[string]ProviderReadiness, policy ReviewPolicy) Readiness {
	r := Readiness{ReviewPolicy: policy.Effective()}
	p := providers[planner]
	r.PlannerRunnable = planner != "" && p.Runnable()
	r.SetupPending = planner == "" || (p.Installed && p.AuthUnknown)
	switch {
	case planner == "":
		r.Blocker = "Choose your planner: run `/provider use <name>`."
	case !p.Installed:
		r.Blocker = fmt.Sprintf("Your planner %s can't run: install `%s` on PATH.", planner, planner)
	case p.AuthUnknown:
		r.Blocker = fmt.Sprintf("Your planner %s has not been checked: run `%s` and check sign-in, then press v on the Board.", planner, SignInCommand(planner))
	case !p.SignedIn:
		r.Blocker = fmt.Sprintf("Your planner %s can't run: run `%s` and sign in.", planner, SignInCommand(planner))
	case p.Unavailable != "":
		r.Blocker = fmt.Sprintf("Your planner %s can't run: %s. Choose another planner with `/provider use <name>`.", planner, p.Unavailable)
	}
	for _, name := range []string{"claude", "codex", "agy", "opencode"} {
		if providers[name].Runnable() {
			r.Workers = append(r.Workers, name)
		}
	}
	r.ChecksOnly = r.ReviewPolicy.FinalModel == FinalModelOff
	r.FinalCheckEligible = r.ChecksOnly
	for _, name := range r.Workers {
		if r.ReviewPolicy.FinalProvider == "any" || r.ReviewPolicy.FinalProvider == name {
			r.FinalCheckEligible = true
		}
	}
	if r.Blocker == "" && len(r.Workers) == 0 {
		r.Blocker = "No worker can run: run `claude` and sign in."
	}
	if r.Blocker == "" && !r.FinalCheckEligible {
		r.Blocker = "Final check needs a qualified provider allowed by final-provider: sign in, or approve a checks-only policy with `/marshal settings final-model off`."
	}
	r.Guarantee = "Independent hand-in review is required."
	if r.ReviewPolicy.CrossReview == CrossReviewOff {
		r.Guarantee = "Review is Off: hand-ins will not get independent review."
	}
	if r.ReviewPolicy.Design == DesignReviewOff {
		r.Guarantee += " Design review is Off."
	}
	switch {
	case r.ChecksOnly:
		r.Guarantee += " Checks-only delivery: executable checks run; no independent final model check or verified-delivery receipt."
	case !r.FinalCheckEligible:
		r.Guarantee += " Final check is unavailable; closure will wait for a qualified provider. Sign in, or approve a checks-only policy before dispatch."
	default:
		r.Guarantee += " A qualified provider independently checks the integrated result in a fresh session; native model identity and commit binding are required."
	}
	r.NeedsConfirmation = !r.FinalCheckEligible || r.ChecksOnly || r.ReviewPolicy.CrossReview == CrossReviewOff || r.ReviewPolicy.Design == DesignReviewOff
	return r
}
