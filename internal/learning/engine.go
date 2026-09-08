package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// digest produces a stable content digest for a record. The Digest field is
// cleared by callers before hashing so a record never hashes its own digest.
func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// independentClusters counts distinct evidence clusters. Repetitions that share
// a cluster count once, so one source echoed many times cannot look like
// corroboration.
func independentClusters(refs []EvidenceRef) int {
	seen := map[string]bool{}
	for _, r := range refs {
		if r.ClusterID != "" {
			seen[r.ClusterID] = true
		}
	}
	return len(seen)
}

// secretPattern matches material that must never enter memory. Detection is
// deliberately broad: a false positive costs one unpromoted item, a false
// negative leaks a secret into durable, possibly generalized, storage.
var secretPattern = []string{
	"BEGIN RSA PRIVATE KEY", "BEGIN OPENSSH PRIVATE KEY", "BEGIN PRIVATE KEY",
	"BEGIN PGP PRIVATE KEY", "aws_secret_access_key", "AKIA", "ghp_", "gho_",
	"github_pat_", "xoxb-", "xoxp-", "sk-", "api_key=", "apikey=", "password=",
	"passwd=", "secret=", "token=", "authorization: bearer",
}

// CarriesSecret reports whether text contains material that must be kept out of
// durable memory.
func CarriesSecret(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range secretPattern {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// PromotionInput is one candidate considered for durable memory.
type PromotionInput struct {
	Item Item
	// IndependentObservations counts corroborating observations from separate
	// clusters. Repetition of one source does not raise this.
	IndependentObservations int
	// Replayable reports whether the supporting evidence can be replayed.
	Replayable bool
}

// Promote decides whether a candidate may become durable memory, and returns
// the item as it may be stored.
//
// The gates are deliberately conservative. Consensus does not promote, prestige
// does not promote, and repetition does not promote; only evidence does. A
// candidate that fails any gate is returned with ErrNotPromotable so the caller
// records an honest refusal rather than a weakened fact.
func Promote(in PromotionInput, now time.Time) (Item, error) {
	it := in.Item

	if !it.Binding.Valid() {
		return Item{}, fmt.Errorf("%w: entry binding", ErrInvalid)
	}
	if strings.TrimSpace(it.Claim) == "" {
		return Item{}, fmt.Errorf("%w: empty claim", ErrInvalid)
	}
	if !it.State.IsValid() {
		return Item{}, fmt.Errorf("%w: claim state", ErrInvalid)
	}

	// Secrets never become durable memory, at any scope.
	if CarriesSecret(it.Claim) {
		return Item{}, ErrSecretMaterial
	}
	for _, a := range it.Applicability {
		if CarriesSecret(a) {
			return Item{}, ErrSecretMaterial
		}
	}

	// An unsupported or invalidated claim is not knowledge.
	switch it.State {
	case model.ClaimStateUnsupported, model.ClaimStateInvalidated:
		return Item{}, fmt.Errorf("%w: state %s", ErrNotPromotable, it.State)
	}

	// A contested claim stays contested; it is recorded, never promoted to a
	// fact, because an unresolved contradiction is information in itself.
	if it.State == model.ClaimStateContested && len(it.Contradicts) == 0 {
		return Item{}, fmt.Errorf("%w: contested without contradiction refs", ErrInvalid)
	}

	// Stale input cannot enter as current knowledge.
	if !it.Fresh(now) {
		return Item{}, fmt.Errorf("%w: stale candidate", ErrNotPromotable)
	}

	if len(it.Evidence) == 0 {
		return Item{}, fmt.Errorf("%w: no evidence", ErrNotPromotable)
	}

	clusters := independentClusters(it.Evidence)

	switch it.Scope {
	case ScopeProject:
		if it.ProjectID == "" {
			return Item{}, fmt.Errorf("%w: project scope without project", ErrInvalid)
		}
		if it.ProjectID != it.Binding.ProjectID {
			return Item{}, ErrBindingMismatch
		}
	case ScopeGeneral:
		// Generalization is the strong claim: it asserts transferability beyond
		// the project that produced it, so it needs independent corroboration
		// and explicit applicability bounds.
		if clusters < 2 || in.IndependentObservations < 2 {
			return Item{}, fmt.Errorf("%w: general scope needs independent corroboration", ErrNotPromotable)
		}
		if len(it.Applicability) == 0 {
			return Item{}, fmt.Errorf("%w: general scope needs applicability bounds", ErrNotPromotable)
		}
		if it.ProjectID != "" {
			return Item{}, fmt.Errorf("%w: general scope bound to a project", ErrInvalid)
		}
	default:
		return Item{}, fmt.Errorf("%w: scope", ErrInvalid)
	}

	// A critical claim carries more weight, so it must clear a higher bar:
	// verified state, independent clusters, and replayable evidence.
	if it.Critical {
		if it.State != model.ClaimStateVerified {
			return Item{}, fmt.Errorf("%w: critical claim not verified", ErrNotPromotable)
		}
		if clusters < 2 {
			return Item{}, fmt.Errorf("%w: critical claim from one cluster", ErrNotPromotable)
		}
		if !in.Replayable {
			return Item{}, fmt.Errorf("%w: critical claim not replayable", ErrNotPromotable)
		}
	}

	// A VERIFIED item may only come from an outcome that actually verified.
	// "The run finished" is not verification.
	if it.State == model.ClaimStateVerified && it.Binding.Outcome != OutcomeVerifiedComplete {
		return Item{}, fmt.Errorf("%w: verified claim from %s outcome", ErrNotPromotable, it.Binding.Outcome)
	}

	it.RecordedAt = now
	if it.Version <= 0 {
		it.Version = 1
	}
	return it, nil
}

// Revise records new evidence against an existing item and returns the next
// version. The prior version is never mutated: callers keep it, so the history
// stays auditable and nothing is silently overwritten.
//
// Fresh deterministic evidence outranks stored knowledge regardless of how the
// stored item was originally sourced.
func Revise(prev Item, next Item, now time.Time) (Item, error) {
	if prev.ID == "" || prev.ID != next.ID {
		return Item{}, ErrBindingMismatch
	}
	if !next.State.IsValid() {
		return Item{}, fmt.Errorf("%w: claim state", ErrInvalid)
	}
	if CarriesSecret(next.Claim) {
		return Item{}, ErrSecretMaterial
	}
	// A revision that narrows or invalidates needs no new evidence: retracting
	// is always safe. Broadening or strengthening does need evidence.
	strengthening := rank(next.State) > rank(prev.State)
	if strengthening && len(next.Evidence) == 0 {
		return Item{}, fmt.Errorf("%w: strengthening without evidence", ErrNotPromotable)
	}
	if strengthening && next.Scope == ScopeGeneral && independentClusters(next.Evidence) < 2 {
		return Item{}, fmt.Errorf("%w: general strengthening needs independent clusters", ErrNotPromotable)
	}
	next.Version = prev.Version + 1
	next.RecordedAt = now
	return next, nil
}

// rank orders claim states by how much they assert. Only a higher rank counts
// as strengthening and therefore requires evidence.
func rank(s model.ClaimState) int {
	switch s {
	case model.ClaimStateInvalidated:
		return 0
	case model.ClaimStateStale:
		return 1
	case model.ClaimStateUnsupported:
		return 2
	case model.ClaimStateContested:
		return 3
	case model.ClaimStateSupported:
		return 4
	case model.ClaimStateVerified:
		return 5
	}
	return -1
}

// Invalidate marks every item whose dependencies changed as STALE. Only the
// affected items are touched: unrelated knowledge stays current, so a single
// tool upgrade does not wipe the whole store.
func Invalidate(items []Item, changed []Dependency, now time.Time) []Item {
	if len(changed) == 0 {
		return items
	}
	type key struct{ kind, id, version string }
	// A dependency matches on kind and id. A version change is what makes the
	// stored knowledge stale, so an exact match on all three is NOT stale.
	changedKeys := map[key]bool{}
	for _, d := range changed {
		changedKeys[key{d.Kind, d.ID, d.Version}] = true
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		stale := false
		for _, dep := range it.Dependencies {
			for c := range changedKeys {
				if c.kind == dep.Kind && c.id == dep.ID && c.version != dep.Version {
					stale = true
					break
				}
			}
			if stale {
				break
			}
		}
		if stale && it.State != model.ClaimStateInvalidated {
			it.State = model.ClaimStateStale
			it.Version++
			it.RecordedAt = now
		}
		out = append(out, it)
	}
	return out
}

// NewCommit builds a digested MemoryCommit for one exact Process 06 outcome.
func NewCommit(c Commit, now time.Time) (Commit, error) {
	if c.ID == "" {
		return Commit{}, fmt.Errorf("%w: commit id", ErrInvalid)
	}
	if !c.Binding.Valid() {
		return Commit{}, fmt.Errorf("%w: entry binding", ErrInvalid)
	}
	if c.Provenance == "" {
		return Commit{}, fmt.Errorf("%w: provenance", ErrInvalid)
	}
	for _, it := range c.Additions {
		if it.Binding.VerificationID != c.Binding.VerificationID ||
			it.Binding.VerificationVersion != c.Binding.VerificationVersion {
			return Commit{}, ErrBindingMismatch
		}
	}
	// A playbook candidate never arrives pre-activated.
	for _, p := range c.Playbooks {
		if p.Active {
			return Commit{}, fmt.Errorf("%w: playbook self-activation", ErrInvalid)
		}
	}
	c.CreatedAt = now
	if c.Version <= 0 {
		c.Version = 1
	}
	sort.Strings(c.Invalidations)
	c.Digest = ""
	d, err := digest(c)
	if err != nil {
		return Commit{}, err
	}
	c.Digest = d
	return c, nil
}

// Verify reports whether a commit still matches its digest, so tampering with a
// stored commit is detectable.
func (c Commit) Verify() error {
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
