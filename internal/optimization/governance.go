package optimization

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// The boundary Process 08 cannot cross by itself.
//
// Optimization is allowed to notice that a policy is costing something and to
// say so with evidence. It is not allowed to act on that observation by editing
// the policy. The difference matters because a system that can rewrite its own
// constraints in pursuit of a metric will eventually rewrite the constraint
// that was stopping it.

// ChangeClass separates what Process 08 may enact from what it may only
// propose.
type ChangeClass string

const (
	// ChangeTuning adjusts a value inside limits governance already set:
	// which model handles a task class, how many retries, how much context.
	ChangeTuning ChangeClass = "TUNING"
	// ChangeMaterial alters code, policy or a security boundary. It must go
	// back through Process 03 as a Goal and come out through Process 06.
	ChangeMaterial ChangeClass = "MATERIAL"
)

// Classify decides whether a candidate is tuning or a material change.
//
// The classification reads declared effects, not intent. A candidate that
// touches approvals, sandboxing, network policy, evidence requirements,
// permissions or governance itself is material however it is described.
func Classify(c Candidate) ChangeClass {
	e := c.Effects
	if e.ModifiesGovernance || e.WeakensApprovals || e.WeakensSandbox ||
		e.WeakensNetwork || e.WeakensEvidence || e.RemovesMandatoryVerification ||
		e.ExpandsPermissions || e.ExposesSecrets || e.UncontrolledExternalEffects ||
		e.ViolatesGoalConstraint || e.SpendsVerificationReserve {
		return ChangeMaterial
	}
	return ChangeTuning
}

// PolicyProposal is how Process 08 asks for a change it cannot make.
//
// It is a request for the lifecycle to consider something, carrying everything
// a reviewer needs. It confers no authority: an accepted proposal becomes a
// Process 03 Goal, and the change only exists after Process 06 verifies it.
type PolicyProposal struct {
	ID string `json:"proposal_id"`
	// CurrentPolicy states what the rule is today.
	CurrentPolicy string `json:"current_policy"`
	// Limitation states what that rule is costing, in observed terms.
	Limitation string `json:"limitation"`
	// Evidence is what supports the claim that the cost is real.
	Evidence []learning.EvidenceRef `json:"evidence"`
	// DesiredChange states the proposed rule.
	DesiredChange string `json:"desired_change"`
	// SecurityImpact is mandatory. A proposal that cannot describe its own
	// security consequences has not been thought through.
	SecurityImpact string `json:"security_impact"`
	// Alternatives records what else was considered, so the proposal is a
	// choice rather than the only idea anyone had.
	Alternatives     []string `json:"alternatives,omitempty"`
	RollbackPlan     string   `json:"rollback_plan"`
	VerificationPlan string   `json:"verification_plan"`
	CreatedAt        time.Time
}

// ValidateProposal refuses a proposal that is not reviewable.
func ValidateProposal(p PolicyProposal) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: proposal id", ErrInvalid)
	}
	if strings.TrimSpace(p.CurrentPolicy) == "" || strings.TrimSpace(p.DesiredChange) == "" {
		return fmt.Errorf("%w: proposal must state the current and desired policy", ErrInvalid)
	}
	if strings.TrimSpace(p.Limitation) == "" {
		return fmt.Errorf("%w: proposal must state what the current policy costs", ErrInvalid)
	}
	if strings.TrimSpace(p.SecurityImpact) == "" {
		return fmt.Errorf("%w: proposal must state its security impact", ErrInvalid)
	}
	if len(p.Evidence) == 0 {
		return fmt.Errorf("%w: proposal has no supporting evidence", ErrNotPromotable)
	}
	if strings.TrimSpace(p.RollbackPlan) == "" || strings.TrimSpace(p.VerificationPlan) == "" {
		return fmt.Errorf("%w: proposal needs a rollback and verification plan", ErrInvalid)
	}
	return nil
}

// Enact refuses, always.
//
// The function exists so that the refusal is a real code path with a test
// rather than an absence someone could fill in later without noticing what
// they were doing. Process 08 does not enact policy changes; it proposes them,
// and the lifecycle decides.
func Enact(p PolicyProposal) error {
	return fmt.Errorf("%w: Process 08 cannot enact policy change %s; it must become a Process 03 Goal",
		ErrVeto, p.ID)
}

// RequiresLifecycleReturn reports whether a candidate must go back through
// Process 03 rather than being applied here.
func RequiresLifecycleReturn(c Candidate) bool {
	return Classify(c) == ChangeMaterial
}

// Feedback is what Process 08 writes back into Process 07 learning.
//
// Process 08 keeps no private memory. Everything it learns goes back through
// the canonical learning path, where the same promotion gates apply to it as to
// anything else. A parallel store of optimization beliefs would be memory that
// escaped the evidence rules.
type Feedback struct {
	CycleID     string   `json:"optimization_id"`
	CandidateID string   `json:"candidate_id"`
	BaselineID  string   `json:"baseline_id"`
	Decision    Decision `json:"decision"`
	// Outcome carries what actually happened, including the rejections. A
	// feedback record that only reported successes would teach Process 07 that
	// every experiment works.
	Results []ExperimentResult `json:"results,omitempty"`
	// Uncertainty states what the experiment did not establish.
	Uncertainty []string `json:"uncertainty,omitempty"`
	Scope       []string `json:"scope,omitempty"`
	// Versions pins what the result is true of.
	Versions map[string]string `json:"versions"`
	// RolledBack records a promotion that was taken back, so the reversal is
	// learned alongside the promotion.
	RolledBack     bool   `json:"rolled_back"`
	RollbackReason string `json:"rollback_reason,omitempty"`
	// SourceCluster identifies the evidence cluster this feedback belongs to,
	// so feedback derived from an experiment cannot later be counted as
	// independent corroboration of that same experiment.
	SourceCluster string `json:"source_cluster"`
	RecordedAt    time.Time
}

// ValidateFeedback refuses a feedback record that would teach Process 07
// something the experiment did not show.
func ValidateFeedback(f Feedback) error {
	if strings.TrimSpace(f.CycleID) == "" || strings.TrimSpace(f.CandidateID) == "" {
		return fmt.Errorf("%w: feedback identity", ErrInvalid)
	}
	if strings.TrimSpace(f.SourceCluster) == "" {
		return fmt.Errorf("%w: feedback must name its evidence cluster", ErrInvalid)
	}
	if len(f.Versions) == 0 {
		return fmt.Errorf("%w: feedback must pin the versions it is true of", ErrInvalid)
	}
	// A promotion reported with no results is an assertion, not a finding.
	if (f.Decision == DecisionPromote || f.Decision == DecisionPromoteBounded) && len(f.Results) == 0 {
		return fmt.Errorf("%w: a promotion must carry the results that justified it", ErrNotPromotable)
	}
	return nil
}

// SelfReinforcing reports whether feedback would create a loop: optimization
// citing its own earlier feedback as independent evidence for the same claim.
//
// This is the failure mode where a system convinces itself. One experiment
// produces feedback, the feedback becomes memory, the memory is read as support
// for the next experiment, and after a few rounds a single unreplicated result
// looks like a body of evidence.
func SelfReinforcing(f Feedback, priorClusters []string) bool {
	for _, prior := range priorClusters {
		if prior == f.SourceCluster {
			return true
		}
	}
	return false
}

// IndependentClusters counts distinct evidence clusters across feedback
// records, so repeated feedback from one experiment counts once.
func IndependentClusters(records []Feedback) int {
	seen := map[string]bool{}
	for _, f := range records {
		if f.SourceCluster != "" {
			seen[f.SourceCluster] = true
		}
	}
	return len(seen)
}

// PlaybookPromotion is the evaluation of a Process 07 playbook candidate.
//
// Process 07 produced candidates and refused to activate them. Process 08 is
// where they earn activation, and the bar is the same as for any other change:
// replay or benchmark evidence, adversarial verification, and a bounded canary.
// One success does not activate a playbook.
type PlaybookPromotion struct {
	PlaybookID string `json:"playbook_id"`
	// Applicability bounds where the playbook may be used.
	Applicability []string `json:"applicability"`
	// Approvals names what human approval the playbook itself requires when
	// it runs. A playbook cannot launder away an approval requirement.
	Approvals        []string `json:"approvals,omitempty"`
	Constraints      []string `json:"constraints,omitempty"`
	VerificationPlan string   `json:"verification_plan"`
	// Evidence must include a real evaluation, not just the originating run.
	Counterfactuals []string `json:"counterfactual_ids,omitempty"`
	BenchmarkRuns   []string `json:"benchmark_manifest_ids,omitempty"`
	CanaryID        string   `json:"canary_id,omitempty"`
	// AdversarialVerified records that the playbook was tested against the
	// ways it could go wrong, not only the way it should go right.
	AdversarialVerified bool              `json:"adversarial_verified"`
	Versions            map[string]string `json:"versions"`
	RollbackPlan        string            `json:"rollback_plan"`
	DecidedAt           time.Time
}

// EvaluatePlaybook decides whether a playbook candidate may be activated.
func EvaluatePlaybook(p PlaybookPromotion, g Governance, now time.Time) (Decision, []string) {
	var reasons []string

	if len(p.Applicability) == 0 {
		reasons = append(reasons, "playbook declares no applicability bounds")
	}
	if strings.TrimSpace(p.VerificationPlan) == "" {
		reasons = append(reasons, "playbook has no verification plan")
	}
	if strings.TrimSpace(p.RollbackPlan) == "" {
		reasons = append(reasons, "playbook has no rollback plan")
	}
	if len(p.Versions) == 0 {
		reasons = append(reasons, "playbook is not pinned to any versions")
	}
	if !p.AdversarialVerified {
		reasons = append(reasons, "playbook was not adversarially verified")
	}
	if len(p.Counterfactuals) == 0 && len(p.BenchmarkRuns) == 0 {
		reasons = append(reasons, "playbook has neither counterfactual nor benchmark evidence")
	}
	if strings.TrimSpace(p.CanaryID) == "" {
		reasons = append(reasons, "playbook was not exercised through a bounded canary")
	}

	sort.Strings(reasons)
	if len(reasons) > 0 {
		return DecisionNeedsEvidence, reasons
	}
	// Even a fully evidenced playbook activates only where it was shown to
	// work. Applicability is the scope, not a suggestion.
	return DecisionPromoteBounded, nil
}
