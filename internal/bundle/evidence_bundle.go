package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrProofBundleProhibited = errors.New("naming an artifact 'Proof Bundle' is prohibited: use 'Evidence Bundle' or 'Verification Bundle'")
	ErrInvalidEvidenceBundle = errors.New("invalid evidence bundle")
)

// EvidenceBundle is the exportable, auditable package certifying the verification state of a Goal.
type EvidenceBundle struct {
	BundleType        string              `json:"bundle_type"` // Must be "Evidence Bundle" or "Verification Bundle"
	BundleID          string              `json:"bundle_id"`
	GoalID            string              `json:"goal_id"`
	GoalRevision      int64               `json:"goal_revision"`
	ConstraintsDigest string              `json:"constraints_digest"`
	CommitSHA         string              `json:"commit_sha"`
	Participants      []model.Participant `json:"participants"`
	CriticalClaims    []model.Claim       `json:"critical_claims"`
	EvidenceRefs      []model.EvidenceRef `json:"evidence_refs"`
	UnresolvedItems   []string            `json:"unresolved_items,omitempty"`
	BundleDigest      string              `json:"bundle_digest"`
	ExportedAt        time.Time           `json:"exported_at"`
}

// NewEvidenceBundle creates a bounded Evidence Bundle.
func NewEvidenceBundle(
	bundleID string,
	goal model.GoalContract,
	constraintsDigest string,
	commitSHA string,
	participants []model.Participant,
	claims []model.Claim,
	evidence []model.EvidenceRef,
	unresolved []string,
) (*EvidenceBundle, error) {
	if bundleID == "" {
		bundleID = fmt.Sprintf("bundle-%d", time.Now().UnixNano())
	}

	var critClaims []model.Claim
	for _, c := range claims {
		if c.Criticality.IsCritical() {
			critClaims = append(critClaims, c)
		}
	}

	b := &EvidenceBundle{
		BundleType:        "Evidence Bundle",
		BundleID:          bundleID,
		GoalID:            goal.ID,
		GoalRevision:      goal.Revision,
		ConstraintsDigest: constraintsDigest,
		CommitSHA:         commitSHA,
		Participants:      participants,
		CriticalClaims:    critClaims,
		EvidenceRefs:      evidence,
		UnresolvedItems:   unresolved,
		ExportedAt:        time.Now().UTC(),
	}

	digest, err := b.ComputeDigest()
	if err != nil {
		return nil, err
	}
	b.BundleDigest = digest

	return b, nil
}

// ComputeDigest computes a deterministic SHA256 digest over the bundle contents.
func (b *EvidenceBundle) ComputeDigest() (string, error) {
	clone := *b
	clone.BundleDigest = ""

	data, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("marshal bundle for digest: %w", err)
	}

	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// Validate checks structural integrity and ensures non-negotiable naming discipline.
func (b *EvidenceBundle) Validate() error {
	if b.BundleType != "Evidence Bundle" && b.BundleType != "Verification Bundle" {
		return fmt.Errorf("%w: bundle type %q is invalid", ErrProofBundleProhibited, b.BundleType)
	}
	if b.GoalID == "" {
		return fmt.Errorf("%w: goal ID is required", ErrInvalidEvidenceBundle)
	}
	if b.BundleDigest == "" {
		return fmt.Errorf("%w: bundle digest is required", ErrInvalidEvidenceBundle)
	}
	return nil
}
