package optimization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Governance is the set of hard boundaries optimization operates inside.
// These come from policy, never from learning, and Process 08 cannot edit them.
type Governance struct {
	// GovernableProviders lists providers MARSHAL can actually govern.
	GovernableProviders map[string]bool
	// EvidenceFreshness is how recent supporting evidence must be.
	EvidenceFreshness time.Duration
	// MinEvidenceClusters is the independent-cluster floor for promotion.
	MinEvidenceClusters int
	// MaxCanaryExposure caps how much traffic one canary may take.
	MaxCanaryExposure float64
	// RequireRollback refuses any candidate without a rollback plan.
	RequireRollback bool
}

// Veto applies the hard governance checks to a candidate and returns every
// reason it must not proceed.
//
// The checks read the candidate's declared Effects rather than its prose. A
// persuasive hypothesis is not evidence, and an eloquent justification for
// weakening a sandbox is still a sandbox weakening.
//
// An empty result means governance has no objection. It does not mean the
// candidate is good; that is what the evidence gates are for.
func Veto(c Candidate, g Governance) []string {
	var reasons []string
	e := c.Effects

	if e.WeakensApprovals {
		reasons = append(reasons, "weakens approval requirements")
	}
	if e.WeakensSandbox {
		reasons = append(reasons, "weakens sandbox isolation")
	}
	if e.WeakensNetwork {
		reasons = append(reasons, "weakens network fail-closed policy")
	}
	if e.WeakensEvidence {
		reasons = append(reasons, "weakens evidence requirements")
	}
	if e.RemovesMandatoryVerification {
		reasons = append(reasons, "removes mandatory verification")
	}
	if e.HidesUnknown {
		reasons = append(reasons, "hides UNKNOWN or NOT_RUN state")
	}
	if e.ExpandsPermissions {
		reasons = append(reasons, "expands runtime permissions")
	}
	if e.ExposesSecrets {
		reasons = append(reasons, "exposes secret material")
	}
	if e.RequiresUngovernableProvider {
		reasons = append(reasons, "requires an ungovernable provider")
	}
	if e.UncontrolledExternalEffects {
		reasons = append(reasons, "creates uncontrolled external effects")
	}
	if e.ModifiesGovernance {
		// Process 08 proposes governance changes; it never enacts them. The
		// change must return through Process 03 as a Goal.
		reasons = append(reasons, "modifies hard governance directly, which must loop through Process 03")
	}
	if e.SpendsVerificationReserve {
		reasons = append(reasons, "spends the verification budget reserve")
	}
	if e.ViolatesGoalConstraint {
		reasons = append(reasons, "violates a Goal or constitutional constraint")
	}
	if e.EnablesFleetControl {
		reasons = append(reasons, "enables fleet-wide control, which is unavailable in Community")
	}
	if g.RequireRollback && strings.TrimSpace(c.RollbackPlan) == "" {
		reasons = append(reasons, "has no rollback plan")
	}
	if strings.TrimSpace(c.VerificationPlan) == "" {
		reasons = append(reasons, "has no verification plan")
	}
	if !ValidDimension(c.Dimension) {
		reasons = append(reasons, "names no recognized optimization dimension")
	}
	if len(c.TaskScope) == 0 {
		reasons = append(reasons, "declares no task scope")
	}

	sort.Strings(reasons)
	return reasons
}

// ValidateCandidate rejects a malformed candidate before governance sees it.
func ValidateCandidate(c Candidate) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%w: candidate id", ErrInvalid)
	}
	if strings.TrimSpace(c.Hypothesis) == "" {
		return fmt.Errorf("%w: candidate hypothesis", ErrInvalid)
	}
	if strings.TrimSpace(c.Provenance) == "" {
		return fmt.Errorf("%w: candidate provenance", ErrInvalid)
	}
	if !ValidDimension(c.Dimension) {
		return fmt.Errorf("%w: candidate dimension", ErrInvalid)
	}
	return nil
}

// ClusterKey identifies semantically equivalent candidates.
//
// Two candidates cluster when they change the same dimension for the same task
// scope by the same mechanism. Security impact is part of the key: candidates
// with materially different governance effects never merge, because collapsing
// them would let a safe variant's evidence vouch for a dangerous one.
type ClusterKey struct {
	Dimension Dimension `json:"dimension"`
	Scope     string    `json:"scope"`
	Mechanism string    `json:"mechanism"`
	Effects   string    `json:"effects_digest"`
}

// Cluster assigns each candidate a cluster id and returns the grouping.
// Provenance is preserved: clustering records that candidates are equivalent,
// it does not discard the ones it groups.
func Cluster(candidates []Candidate) (map[ClusterKey][]Candidate, error) {
	out := map[ClusterKey][]Candidate{}
	for _, c := range candidates {
		effects, err := digest(c.Effects)
		if err != nil {
			return nil, err
		}
		scope := append([]string(nil), c.TaskScope...)
		sort.Strings(scope)
		key := ClusterKey{
			Dimension: c.Dimension,
			Scope:     strings.Join(scope, ","),
			Mechanism: c.Hypothesis,
			Effects:   effects,
		}
		out[key] = append(out[key], c)
	}
	return out, nil
}

// ClusterID returns the stable identifier for a candidate's cluster.
func ClusterID(c Candidate) (string, error) {
	effects, err := digest(c.Effects)
	if err != nil {
		return "", err
	}
	scope := append([]string(nil), c.TaskScope...)
	sort.Strings(scope)
	return digest(ClusterKey{
		Dimension: c.Dimension,
		Scope:     strings.Join(scope, ","),
		Mechanism: c.Hypothesis,
		Effects:   effects,
	})
}

// independentClusters counts how many distinct evidence clusters back a set of
// experiment results. One source repeated is one cluster, however many times
// it is cited.
func independentClusters(results []ExperimentResult) int {
	seen := map[string]bool{}
	for _, r := range results {
		if r.ClusterID != "" {
			seen[r.ClusterID] = true
		}
	}
	return len(seen)
}

// PromotionInput is everything a promotion decision rests on.
type PromotionInput struct {
	Candidate Candidate
	Baseline  Baseline
	// CandidateBaseline is the configuration the candidate ran under. It must
	// be comparable to Baseline for the comparison to mean anything.
	CandidateBaseline Baseline
	// Results are the experiment outcomes supporting the candidate.
	Results []ExperimentResult
	// HoldoutResults are outcomes on tasks held out from development. A
	// regression here blocks promotion regardless of development gains.
	HoldoutResults []ExperimentResult
	// EvidenceAge is how old the supporting evidence is.
	EvidenceAge time.Duration
	// Reproducible reports whether the supporting runs carry enough
	// provenance to be re-run.
	Reproducible bool
}

// Promote decides whether a candidate may be adopted, and how widely.
//
// The gates are ordered so that governance is answered first: a vetoed
// candidate is refused before any score is read, because no benchmark result
// overturns a hard veto. After that the question is whether the evidence
// actually supports the claim, and whether it supports it everywhere or only
// where it was measured.
func Promote(in PromotionInput, g Governance, now time.Time) (PromotionRecord, error) {
	record := PromotionRecord{
		CandidateID: in.Candidate.ID,
		BaselineID:  in.Baseline.ID,
		DecidedAt:   now,
	}

	if err := ValidateCandidate(in.Candidate); err != nil {
		record.Decision = DecisionBlocked
		record.Reasons = []string{err.Error()}
		return finishRecord(record)
	}

	// Governance first. A veto is terminal.
	if reasons := Veto(in.Candidate, g); len(reasons) > 0 {
		record.Decision = DecisionBlocked
		record.Reasons = reasons
		return finishRecord(record)
	}

	// A comparison needs a valid, comparable baseline on both sides.
	if !in.Baseline.Valid() || !in.CandidateBaseline.Valid() {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{"baseline is not pinned"}
		return finishRecord(record)
	}
	if !in.Baseline.Comparable(in.CandidateBaseline) {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{"baseline and candidate configurations are not comparable"}
		return finishRecord(record)
	}

	// Stale evidence cannot authorize a change to a system that has moved on.
	if g.EvidenceFreshness > 0 && in.EvidenceAge > g.EvidenceFreshness {
		record.Decision = DecisionStale
		record.Reasons = []string{"supporting evidence is stale"}
		return finishRecord(record)
	}

	if len(in.Results) == 0 {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{"no experiment results"}
		return finishRecord(record)
	}

	// Every attempt counts. A quarantined result is neither a win nor a loss,
	// and a run whose outcome is UNKNOWN stays UNKNOWN rather than being read
	// as a success.
	var regressions, unknowns, quarantined int
	for _, r := range in.Results {
		switch {
		case r.Quarantined:
			quarantined++
		case r.Outcome == StatusUnknown || r.Outcome == StatusNotRun:
			unknowns++
		case r.Regression:
			regressions++
		}
	}
	if regressions > 0 {
		record.Decision = DecisionReject
		record.Reasons = []string{fmt.Sprintf("%d result(s) regressed against the baseline", regressions)}
		return finishRecord(record)
	}
	usable := len(in.Results) - unknowns - quarantined
	if usable <= 0 {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{"every result was UNKNOWN, NOT_RUN or quarantined"}
		return finishRecord(record)
	}

	// A holdout regression means the gain did not generalize. Development
	// improvement does not override it.
	for _, r := range in.HoldoutResults {
		if r.Regression {
			record.Decision = DecisionReject
			record.Reasons = []string{"holdout set regressed; the improvement did not generalize"}
			return finishRecord(record)
		}
	}

	clusters := independentClusters(in.Results)
	if g.MinEvidenceClusters > 0 && clusters < g.MinEvidenceClusters {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{fmt.Sprintf("evidence spans %d independent cluster(s), need %d", clusters, g.MinEvidenceClusters)}
		return finishRecord(record)
	}

	if !in.Reproducible {
		record.Decision = DecisionNeedsEvidence
		record.Reasons = []string{"supporting runs lack the provenance to be reproduced"}
		return finishRecord(record)
	}

	// The evidence supports the change where it was measured. Promotion is
	// bounded to that scope unless a holdout set showed it generalizes.
	record.Scope = observedScope(in.Results)
	if len(in.HoldoutResults) == 0 {
		record.Decision = DecisionPromoteBounded
		record.Reasons = []string{"no holdout evidence; bounded to the task classes actually measured"}
		return finishRecord(record)
	}
	record.Decision = DecisionPromoteBounded
	record.Reasons = []string{"holdout held; bounded to the measured and held-out task classes"}
	return finishRecord(record)
}

// observedScope returns the task classes the results actually covered, sorted
// so the scope is deterministic.
func observedScope(results []ExperimentResult) []string {
	seen := map[string]bool{}
	for _, r := range results {
		if r.TaskClass != "" {
			seen[r.TaskClass] = true
		}
	}
	out := make([]string, 0, len(seen))
	for class := range seen {
		out = append(out, class)
	}
	sort.Strings(out)
	return out
}

func finishRecord(r PromotionRecord) (PromotionRecord, error) {
	r.Digest = ""
	d, err := digest(r)
	if err != nil {
		return PromotionRecord{}, err
	}
	r.Digest = d
	if r.Decision == DecisionBlocked {
		return r, fmt.Errorf("%w: %v", ErrVeto, r.Reasons)
	}
	return r, nil
}

// VerifyPromotion reports whether a promotion record still matches its digest,
// so a decision altered after the fact is detectable.
func (r PromotionRecord) Verify() error {
	want := r.Digest
	if want == "" {
		return fmt.Errorf("%w: missing digest", ErrInvalid)
	}
	r.Digest = ""
	got, err := digest(r)
	if err != nil {
		return err
	}
	if got != want {
		return ErrTampered
	}
	return nil
}

// NewCycle builds a digested optimization cycle bound to one exact Process 07
// state.
func NewCycle(c Cycle, now time.Time) (Cycle, error) {
	if strings.TrimSpace(c.ID) == "" {
		return Cycle{}, fmt.Errorf("%w: cycle id", ErrInvalid)
	}
	if !c.Binding.Valid() {
		return Cycle{}, fmt.Errorf("%w: Process 07 entry binding", ErrInvalid)
	}
	if strings.TrimSpace(c.Provenance) == "" {
		return Cycle{}, fmt.Errorf("%w: cycle provenance", ErrInvalid)
	}
	if len(c.Objectives) == 0 {
		return Cycle{}, fmt.Errorf("%w: cycle declares no objectives", ErrInvalid)
	}
	for _, cand := range c.Candidates {
		if err := ValidateCandidate(cand); err != nil {
			return Cycle{}, err
		}
	}
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Version <= 0 {
		c.Version = 1
	}
	sort.Strings(c.ExperimentRefs)
	sort.Strings(c.BlockedOptimization)
	c.Digest = ""
	d, err := digest(c)
	if err != nil {
		return Cycle{}, err
	}
	c.Digest = d
	return c, nil
}

// Verify reports whether a stored cycle still matches its digest.
func (c Cycle) Verify() error {
	want := c.Digest
	if want == "" {
		return fmt.Errorf("%w: missing digest", ErrInvalid)
	}
	c.Digest = ""
	got, err := digest(c)
	if err != nil {
		return err
	}
	if got != want {
		return ErrTampered
	}
	return nil
}
