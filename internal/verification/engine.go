package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func validStatus(s Status) bool {
	return s == StatusPass || s == StatusFail || s == StatusBlocked || s == StatusNotRun || s == StatusUnknown
}

func ValidateBinding(b Binding) error {
	if b.ProjectID == "" || b.GoalID == "" || b.PlanID == "" || b.RunID == "" || b.GoalRevision < 1 || b.PlanVersion < 1 || b.RunVersion < 1 || b.TreeDigest == "" || b.EnvironmentDigest == "" {
		return fmt.Errorf("%w: incomplete exact-state binding", ErrInvalid)
	}
	return nil
}

func ValidateSession(s Session) error {
	if s.ID == "" || s.Version < 1 {
		return fmt.Errorf("%w: session identity", ErrInvalid)
	}
	if err := ValidateBinding(s.Binding); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range s.Criteria {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("%w: criterion", ErrInvalid)
		}
		seen[c.ID] = true
	}
	seen = map[string]bool{}
	for _, c := range s.Claims {
		if c.ID == "" || c.CriterionID == "" || seen[c.ID] || len(c.SemanticScope) == 0 {
			return fmt.Errorf("%w: claim", ErrInvalid)
		}
		seen[c.ID] = true
	}
	for name, status := range s.RequiredChecks {
		if strings.TrimSpace(name) == "" || !validStatus(status) {
			return fmt.Errorf("%w: required check", ErrInvalid)
		}
	}
	return nil
}

// Evaluate recomputes the decision from canonical inputs. It never trusts a
// previously persisted State and treats missing/unknown facts as non-success.
func Evaluate(s Session, current Binding, now time.Time) Decision {
	if s.Cancelled {
		return Cancelled
	}
	if ValidateBinding(current) != nil || s.Binding.ProjectID != current.ProjectID || s.Binding.GoalID != current.GoalID || s.Binding.RunID != current.RunID {
		return Blocked
	}
	if s.Binding.GoalRevision != current.GoalRevision {
		return NeedsReplan
	}
	if s.Binding.PlanID != current.PlanID || s.Binding.PlanVersion != current.PlanVersion {
		return NeedsReplan
	}
	if s.Binding.RunVersion != current.RunVersion || s.Binding.TreeDigest != current.TreeDigest || s.Binding.EnvironmentDigest != current.EnvironmentDigest {
		return NeedsReexecution
	}
	if len(s.KnownBlockers) > 0 {
		return Blocked
	}
	for _, c := range s.Contradictions {
		if c.Critical && !c.Resolved {
			return VerificationFailed
		}
	}
	for _, w := range s.Waivers {
		if w.Critical || w.ID == "" || w.Actor == "" || w.Reason == "" || !now.Before(w.ExpiresAt) {
			return Blocked
		}
	}
	for _, st := range s.RequiredChecks {
		if st == StatusFail {
			return VerificationFailed
		}
		if st != StatusPass {
			return Blocked
		}
	}
	evidence := map[string]Evidence{}
	for _, ev := range s.Evidence {
		evidence[ev.ID] = ev
	}
	claims := map[string]Claim{}
	for _, claim := range s.Claims {
		claims[claim.ID] = claim
	}
	partial := false
	for _, criterion := range s.Criteria {
		covered := false
		for _, claimID := range criterion.ClaimIDs {
			claim, ok := claims[claimID]
			if !ok {
				continue
			}
			validClusters := map[string]bool{}
			claimOK := len(claim.EvidenceIDs) > 0
			for _, id := range claim.EvidenceIDs {
				ev, ok := evidence[id]
				if !ok || ev.ClaimID != claim.ID || ev.Status != StatusPass || ev.ContentDigest == "" || ev.TreeDigest != s.Binding.TreeDigest || ev.EnvironmentDigest != s.Binding.EnvironmentDigest || (ev.ExpiresAt != nil && !now.Before(*ev.ExpiresAt)) || ev.Attempts < 1 || ev.Passes < 1 {
					claimOK = false
					break
				}
				cluster := ev.ClusterID
				if cluster == "" {
					cluster = ev.Producer + "\x00" + ev.Provider + "\x00" + ev.Oracle
				}
				validClusters[cluster] = true
			}
			if claim.Critical && len(validClusters) < 2 {
				claimOK = false
			}
			if claimOK {
				covered = true
				break
			}
		}
		if !covered {
			if criterion.Mandatory {
				return VerificationFailed
			}
			partial = true
		}
	}
	if partial || len(s.Limitations) > 0 {
		return PartiallySatisfied
	}
	return VerifiedComplete
}

func digest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func NewCompletionAttestation(id string, s Session, bundleDigest, provenance string, now time.Time) (CompletionAttestation, error) {
	if err := ValidateSession(s); err != nil {
		return CompletionAttestation{}, err
	}
	decision := Evaluate(s, s.Binding, now)
	if bundleDigest == "" || provenance == "" {
		return CompletionAttestation{}, fmt.Errorf("%w: attestation provenance", ErrInvalid)
	}
	criteriaDigest, _ := digest(s.Criteria)
	claimsDigest, _ := digest(s.Claims)
	waivers := make([]string, 0, len(s.Waivers))
	for _, w := range s.Waivers {
		waivers = append(waivers, w.ID)
	}
	sort.Strings(waivers)
	a := CompletionAttestation{ID: id, Binding: s.Binding, VerificationID: s.ID, VerificationVersion: s.Version, CriteriaDigest: criteriaDigest, ClaimsDigest: claimsDigest, EvidenceBundleDigest: bundleDigest, Decision: decision, Limitations: append([]string(nil), s.Limitations...), WaiverIDs: waivers, IssuedAt: now.UTC(), Provenance: provenance}
	if a.ID == "" {
		return CompletionAttestation{}, fmt.Errorf("%w: attestation id", ErrInvalid)
	}
	a.Digest, _ = digest(a)
	return a, nil
}

func (a CompletionAttestation) Verify(current Binding) error {
	stored := a.Digest
	a.Digest = ""
	got, e := digest(a)
	if e != nil {
		return e
	}
	if stored == "" || stored != got {
		return ErrTampered
	}
	if a.Binding != current {
		return ErrBindingMismatch
	}
	return nil
}

func NewIntegrationAttestation(a IntegrationAttestation) (IntegrationAttestation, error) {
	if a.CandidateSHA == "" || a.MainSHA == "" || a.MergeTreeDigest == "" || a.EvidenceBundleDigest == "" || !a.CandidateIsAncestor {
		return IntegrationAttestation{}, fmt.Errorf("%w: integration binding", ErrInvalid)
	}
	for _, s := range a.CriticalGates {
		if s != StatusPass {
			return IntegrationAttestation{}, fmt.Errorf("%w: exact-main gate", ErrInvalid)
		}
	}
	if len(a.Gaps) > 0 {
		return IntegrationAttestation{}, fmt.Errorf("%w: unresolved exact-main gaps", ErrInvalid)
	}
	a.Digest = ""
	a.Digest, _ = digest(a)
	return a, nil
}

// Replay verifies both the recorded content and reproducibility of a check.
func Replay(ev Evidence, observedOutput []byte) error {
	h := sha256.Sum256(observedOutput)
	got := hex.EncodeToString(h[:])
	if ev.OutputDigest == "" || got != ev.OutputDigest {
		return ErrReplayDivergence
	}
	return nil
}
