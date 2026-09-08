package constitution

import (
	"sort"
	"strings"
	"time"
)

// This file makes ROLLED_BACK a claim about the world rather than about the
// audit log.
//
// The failure mode it exists to prevent is subtle and common: a rollback is
// requested, an event recording the rollback is written, and the system
// thereafter reports the work as undone — while the worktree, the goal or the
// dependent evidence still reflect the change. The record is true (a rollback
// was attempted) and the conclusion is false (the state was restored). The
// only cure is to compare the state against what it was supposed to become and
// to refuse the claim when they differ.

// RollbackStatus is the outcome of a rollback attempt.
type RollbackStatus string

const (
	// RollbackVerified means the intended state was restored and checked.
	// It is the only status that may be reported to a user as success.
	RollbackVerified RollbackStatus = "ROLLED_BACK"
	// RollbackPartial means some targets were restored and others were not.
	// The session is in a mixed state and needs a decision.
	RollbackPartial RollbackStatus = "PARTIALLY_ROLLED_BACK"
	// RollbackFailed means the restore did not take effect.
	RollbackFailed RollbackStatus = "ROLLBACK_FAILED"
	// RollbackUnverifiable means the restore may have happened but cannot be
	// confirmed. It is reported honestly rather than resolved optimistically.
	RollbackUnverifiable RollbackStatus = "ROLLBACK_UNVERIFIED"
)

// Restored reports whether the status may be presented as a successful
// rollback. Only the verified status qualifies.
func (s RollbackStatus) Restored() bool { return s == RollbackVerified }

// RestoreTarget is one thing a rollback was supposed to restore, together with
// what it was supposed to become and what it actually is.
type RestoreTarget struct {
	// Kind names what is being restored: worktree, goal, claim, session scope,
	// checkpoint, evidence.
	Kind string `json:"kind"`
	// Ref identifies the specific target.
	Ref string `json:"ref"`
	// ExpectedDigest is the state the target should hold after the rollback.
	ExpectedDigest string `json:"expected_digest"`
	// ObservedDigest is the state actually read back after the attempt. It
	// must be an independent observation of the target, not a value carried
	// over from the request, or the comparison proves nothing.
	ObservedDigest string `json:"observed_digest"`
	// Observed reports whether the target could be read back at all.
	Observed bool `json:"observed"`
}

// Restored reports whether this target actually reached its intended state.
func (t RestoreTarget) Restored() bool {
	return t.Observed &&
		strings.TrimSpace(t.ExpectedDigest) != "" &&
		t.ObservedDigest == t.ExpectedDigest
}

// RollbackAssessment is the verified account of a rollback attempt.
type RollbackAssessment struct {
	Status RollbackStatus `json:"status"`
	Reason ReasonCode     `json:"reason"`

	// RestoredTargets and UnrestoredTargets partition the targets by outcome.
	RestoredTargets   []string `json:"restored_targets,omitempty"`
	UnrestoredTargets []string `json:"unrestored_targets,omitempty"`
	// UnobservableTargets could not be read back, so nothing is claimed about
	// them either way.
	UnobservableTargets []string `json:"unobservable_targets,omitempty"`

	// ExternalEffects names effects that were issued outside MARSHAL and
	// cannot be undone by restoring local state. They are disclosed rather
	// than quietly excluded from the rollback's scope, because a user reading
	// "rolled back" would otherwise reasonably believe them reversed.
	ExternalEffects []string `json:"external_effects,omitempty"`

	// StaleEvidence lists evidence that described the rolled-back state and
	// must no longer be relied on.
	StaleEvidence []string `json:"stale_evidence,omitempty"`
	// StaleMemory lists memory that rested on the rolled-back state.
	StaleMemory []string `json:"stale_memory,omitempty"`

	// Explanation is user-safe.
	Explanation string    `json:"explanation"`
	AssessedAt  time.Time `json:"assessed_at"`
}

// RollbackRequest is the deterministic evidence base for the assessment.
type RollbackRequest struct {
	Envelope Envelope
	// Targets are the things the rollback was meant to restore, each carrying
	// an independently observed current state.
	Targets []RestoreTarget
	// ExternalEffects are effects already issued outside MARSHAL's control.
	ExternalEffects []string
	// DependentEvidence and DependentMemory rested on the state being undone.
	DependentEvidence []string
	DependentMemory   []string
	Now               time.Time
}

// AssessRollback determines whether a rollback may be reported as done.
//
// A rollback with no targets is unverifiable, never successful: if nothing was
// checked, nothing was proven, and reporting success would be exactly the
// audit-only rollback this function exists to prevent. Any unrestored target
// prevents the verified status; any unobservable target prevents it too,
// because a target that could not be read back has not been shown to be
// restored, and treating silence as success is how false rollbacks are
// reported in the first place.
func AssessRollback(request RollbackRequest) RollbackAssessment {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	assessment := RollbackAssessment{
		AssessedAt:      now,
		ExternalEffects: append([]string(nil), request.ExternalEffects...),
		StaleEvidence:   append([]string(nil), request.DependentEvidence...),
		StaleMemory:     append([]string(nil), request.DependentMemory...),
	}
	sort.Strings(assessment.ExternalEffects)
	sort.Strings(assessment.StaleEvidence)
	sort.Strings(assessment.StaleMemory)

	if len(request.Targets) == 0 {
		assessment.Status = RollbackUnverifiable
		assessment.Reason = ReasonRollbackUnverified
		assessment.Explanation = "Nothing was checked, so the rollback cannot be confirmed."
		return assessment
	}

	for _, target := range request.Targets {
		label := target.Kind + ":" + target.Ref
		switch {
		case !target.Observed:
			assessment.UnobservableTargets = append(assessment.UnobservableTargets, label)
		case target.Restored():
			assessment.RestoredTargets = append(assessment.RestoredTargets, label)
		default:
			assessment.UnrestoredTargets = append(assessment.UnrestoredTargets, label)
		}
	}
	sort.Strings(assessment.RestoredTargets)
	sort.Strings(assessment.UnrestoredTargets)
	sort.Strings(assessment.UnobservableTargets)

	switch {
	case len(assessment.UnrestoredTargets) == 0 && len(assessment.UnobservableTargets) == 0:
		assessment.Status = RollbackVerified
		assessment.Reason = ReasonAllowed
		assessment.Explanation = "The previous state was restored and checked."
	case len(assessment.RestoredTargets) == 0 && len(assessment.UnobservableTargets) == 0:
		assessment.Status = RollbackFailed
		assessment.Reason = ReasonRollbackUnverified
		assessment.Explanation = "The previous state was not restored."
	case len(assessment.UnobservableTargets) > 0 && len(assessment.UnrestoredTargets) == 0:
		assessment.Status = RollbackUnverifiable
		assessment.Reason = ReasonRollbackUnverified
		assessment.Explanation = "Part of the previous state could not be checked, so the rollback is not confirmed."
	default:
		assessment.Status = RollbackPartial
		assessment.Reason = ReasonRollbackUnverified
		assessment.Explanation = "Only part of the previous state was restored."
	}

	// External effects are disclosed on every outcome, including a verified
	// one. A local restore that is complete in every checkable respect still
	// leaves a deployment or an API call standing, and a user reading
	// "rolled back" without that caveat would be misled.
	if len(assessment.ExternalEffects) > 0 {
		assessment.Explanation += " Actions already taken outside this project were not undone."
	}
	return assessment
}
