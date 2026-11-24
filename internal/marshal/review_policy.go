package marshal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReviewPolicyVersion identifies the current persisted review policy snapshot schema.
const ReviewPolicyVersion = 1

// ErrReviewPolicyLocked is returned when an edition locks review policy against weakening.
var ErrReviewPolicyLocked = errors.New("review policy is locked in this edition; migrate settings or approve a valid policy")

// FinalModelPolicy configures the optional final independent model verification step.
type FinalModelPolicy string

const (
	FinalModelRequired FinalModelPolicy = "required"
	FinalModelOff      FinalModelPolicy = "off"
)

// CrossReviewPolicy configures the optional independent per-hand-in cross-review.
type CrossReviewPolicy string

const (
	CrossReviewRequired CrossReviewPolicy = "required"
	CrossReviewOff      CrossReviewPolicy = "off"
)

// DesignReviewPolicy configures independent pre-approval design review.
type DesignReviewPolicy string

const (
	DesignReviewOnDemand DesignReviewPolicy = "on-demand"
	DesignReviewRequired DesignReviewPolicy = "required"
	DesignReviewOff      DesignReviewPolicy = "off"
)

// ConformancePolicy configures architecture conformance handling on hand-in.
type ConformancePolicy string

const (
	ConformanceEnforce ConformancePolicy = "enforce"
	ConformanceWarn    ConformancePolicy = "warn"
	ConformanceOff     ConformancePolicy = "off"
)

// ReviewPolicy configures optional review controls for upcoming runs.
// Empty fields read as the shipped defaults.
type ReviewPolicy struct {
	FinalModel  FinalModelPolicy   `json:"finalModel,omitempty"`
	CrossReview CrossReviewPolicy  `json:"crossReview,omitempty"`
	Design      DesignReviewPolicy `json:"design,omitempty"`
	Conformance ConformancePolicy  `json:"conformance,omitempty"`
}

// DefaultReviewPolicy returns the shipped defaults used when no policy is stored.
func DefaultReviewPolicy() ReviewPolicy {
	return ReviewPolicy{
		FinalModel:  FinalModelRequired,
		CrossReview: CrossReviewRequired,
		Design:      DesignReviewOnDemand,
		Conformance: ConformanceEnforce,
	}
}

// Effective resolves legacy empty fields to the shipped defaults.
func (p ReviewPolicy) Effective() ReviewPolicy {
	eff := p
	if eff.FinalModel == "" {
		eff.FinalModel = FinalModelRequired
	}
	if eff.CrossReview == "" {
		eff.CrossReview = CrossReviewRequired
	}
	if eff.Design == "" {
		eff.Design = DesignReviewOnDemand
	}
	if eff.Conformance == "" {
		eff.Conformance = ConformanceEnforce
	}
	return eff
}

// IsDefault reports whether p matches the shipped defaults after legacy expansion.
func (p ReviewPolicy) IsDefault() bool {
	return p.Effective() == DefaultReviewPolicy()
}

// Weakened reports whether any field weakens the shipped default review guarantees.
func (p ReviewPolicy) Weakened() bool {
	eff := p.Effective()
	return eff.FinalModel != FinalModelRequired ||
		eff.CrossReview != CrossReviewRequired ||
		eff.Design == DesignReviewOff ||
		eff.Conformance != ConformanceEnforce
}

// Validate checks enum syntax and edition locks.
func (p ReviewPolicy) Validate() error {
	if p.FinalModel != "" && p.FinalModel != FinalModelRequired && p.FinalModel != FinalModelOff {
		return fmt.Errorf("invalid final model policy %q", p.FinalModel)
	}
	if p.CrossReview != "" && p.CrossReview != CrossReviewRequired && p.CrossReview != CrossReviewOff {
		return fmt.Errorf("invalid cross-review policy %q", p.CrossReview)
	}
	if p.Design != "" && p.Design != DesignReviewOnDemand && p.Design != DesignReviewRequired && p.Design != DesignReviewOff {
		return fmt.Errorf("invalid design review policy %q", p.Design)
	}
	if p.Conformance != "" && p.Conformance != ConformanceEnforce && p.Conformance != ConformanceWarn && p.Conformance != ConformanceOff {
		return fmt.Errorf("invalid conformance policy %q", p.Conformance)
	}
	return validateEditionReviewPolicy(p.Effective())
}

// ReviewPolicySnapshot captures the review policy bound to a run before approval.
type ReviewPolicySnapshot struct {
	PolicyVersion int                `json:"policyVersion"`
	PlanDigest    string             `json:"planDigest,omitempty"`
	FinalModel    FinalModelPolicy   `json:"finalModel"`
	CrossReview   CrossReviewPolicy  `json:"crossReview"`
	Design        DesignReviewPolicy `json:"design"`
	Conformance   ConformancePolicy  `json:"conformance"`
	Actor         string             `json:"actor,omitempty"`
	ConfirmedAt   time.Time          `json:"confirmedAt,omitempty"`
}

// NewReviewPolicySnapshot freezes the effective review policy for a plan.
func NewReviewPolicySnapshot(policy ReviewPolicy, planDigest, actor string, confirmedAt time.Time) *ReviewPolicySnapshot {
	eff := policy.Effective()
	return &ReviewPolicySnapshot{
		PolicyVersion: ReviewPolicyVersion,
		PlanDigest:    planDigest,
		FinalModel:    eff.FinalModel,
		CrossReview:   eff.CrossReview,
		Design:        eff.Design,
		Conformance:   eff.Conformance,
		Actor:         actor,
		ConfirmedAt:   confirmedAt,
	}
}

// Policy converts a snapshot into its ReviewPolicy fields.
func (s ReviewPolicySnapshot) Policy() ReviewPolicy {
	return ReviewPolicy{
		FinalModel:  s.FinalModel,
		CrossReview: s.CrossReview,
		Design:      s.Design,
		Conformance: s.Conformance,
	}.Effective()
}

// Validate checks snapshot version, field values, and edition policy locks.
func (s ReviewPolicySnapshot) Validate() error {
	if s.PolicyVersion != ReviewPolicyVersion {
		return fmt.Errorf("unsupported review policy snapshot version %d", s.PolicyVersion)
	}
	if s.FinalModel == "" || s.CrossReview == "" || s.Design == "" || s.Conformance == "" {
		return errors.New("incomplete review policy snapshot")
	}
	return ReviewPolicy{
		FinalModel:  s.FinalModel,
		CrossReview: s.CrossReview,
		Design:      s.Design,
		Conformance: s.Conformance,
	}.Validate()
}

// DesignReviewReceipt binds an independent fresh-session design critique to
// the exact draft digest that was reviewed.
type DesignReviewReceipt struct {
	Provider     string    `json:"provider"`
	SessionID    string    `json:"sessionId"`
	FreshSession bool      `json:"freshSession"`
	DraftDigest  string    `json:"draftDigest"`
	Findings     string    `json:"findings"`
	ReviewedAt   time.Time `json:"reviewedAt,omitempty"`
}

// DesignDraftDigest returns the canonical digest of a design draft.
func DesignDraftDigest(draft string) string {
	sum := sha256.Sum256([]byte(draft))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ValidateForDraft verifies that the receipt came from a fresh session and
// matches the exact expected draft digest.
func (r DesignReviewReceipt) ValidateForDraft(expectedDigest string) error {
	if strings.TrimSpace(r.Provider) == "" {
		return errors.New("design review receipt missing provider")
	}
	if !r.FreshSession || strings.TrimSpace(r.SessionID) == "" {
		return errors.New("design review receipt requires a fresh independent session")
	}
	if strings.TrimSpace(r.Findings) == "" {
		return errors.New("design review receipt missing findings")
	}
	if expectedDigest == "" || r.DraftDigest == "" || r.DraftDigest != expectedDigest {
		return errors.New("design review receipt does not match current draft")
	}
	return nil
}

// EffectiveReviewPolicy returns the review policy bound to r. Legacy runs with
// no snapshot read as the shipped defaults (or Settings.ReviewPolicy if set),
// bounded by the edition's policy lock.
func (r Run) EffectiveReviewPolicy() ReviewPolicy {
	var p ReviewPolicy
	if r.ReviewPolicy != nil {
		p = r.ReviewPolicy.Policy()
	} else {
		p = r.Settings.ReviewPolicy.Effective()
	}
	return effectiveEditionReviewPolicy(p)
}

// ValidateReviewPolicy verifies both the run's settings and snapshot against
// schema and edition locks. Legacy absence of a snapshot is valid.
func (r Run) ValidateReviewPolicy() error {
	if err := r.Settings.ReviewPolicy.Validate(); err != nil {
		return err
	}
	if r.ReviewPolicy != nil {
		if err := r.ReviewPolicy.Validate(); err != nil {
			return err
		}
	}
	return nil
}
