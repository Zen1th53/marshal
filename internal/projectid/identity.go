// Package projectid establishes which project MARSHAL is looking at.
//
// The rule that shapes everything here is that a path is not an identity. A
// project that moves is still the same project; a different repository that
// later occupies the old path is not. Both halves matter: the first because
// reorganizing a workspace is ordinary and should not destroy a project's
// history, the second because inheriting another project's memory would be a
// cross-project leak with none of the usual warning signs.
//
// Identity is therefore derived from durable repository evidence and recorded
// in a binding, rather than inferred from where the directory happens to sit.
//
// Process 02 is subordinate to Process 00. Nothing here grants authority: an
// identity is evidence about which project this is, and the constitutional
// gate still decides what may be done with it.
package projectid

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrInvalidIdentity reports a malformed or unusable project identity.
var ErrInvalidIdentity = errors.New("project identity is invalid")

// ErrScopeEscape reports a requested working scope that resolves outside the
// project root. It is refused rather than clamped, because a caller asking for
// a path outside the project has a different idea of the boundary than MARSHAL
// does, and silently narrowing their request would hide that disagreement.
var ErrScopeEscape = errors.New("requested scope lies outside the project")

// ID is a stable project identifier. It is independent of the filesystem path
// and survives the project being moved, renamed or restored elsewhere.
type ID string

// Valid reports whether the ID is well formed.
func (i ID) Valid() bool {
	return strings.HasPrefix(string(i), "PROJECT-") && len(i) == len("PROJECT-")+32
}

func (i ID) String() string { return string(i) }

// Evidence is what an identity is derived from.
//
// The fields are deliberately all things that belong to the repository rather
// than to its location. A project keeps them when it moves; a different
// repository at the same path has different ones.
type Evidence struct {
	// RootCommit is the first commit reachable from the initial history. It is
	// the strongest available evidence of repository identity: two clones of
	// the same project share it, and two unrelated repositories effectively
	// never do.
	RootCommit string `json:"root_commit,omitempty"`
	// RemoteURL is the canonical remote, normalized. It is corroborating
	// evidence rather than proof, because remotes are renamed, forks share
	// them briefly, and a repository may have none at all.
	RemoteURL string `json:"remote_url,omitempty"`
	// CreatedNonce is a random value minted when MARSHAL first adopts a
	// project. It is what gives a repository with no commits and no remote a
	// durable identity, and what keeps two independently created projects
	// distinct even if they later look identical.
	CreatedNonce string `json:"created_nonce,omitempty"`
}

// HasDurableEvidence reports whether the evidence can identify a project
// across a move. A nonce alone qualifies, because it is recorded in the
// project's own binding file and travels with the directory.
func (e Evidence) HasDurableEvidence() bool {
	return strings.TrimSpace(e.RootCommit) != "" || strings.TrimSpace(e.CreatedNonce) != ""
}

// Derive computes a project ID from evidence.
//
// The path is deliberately not an input. Two directories holding the same
// repository derive the same ID; the same directory holding a different
// repository derives a different one.
//
// The remote URL is excluded from the derivation even though it is recorded:
// including it would give a fork a different identity from its upstream the
// moment someone changed a remote, and would give two unrelated repositories
// the same identity if they briefly shared one. It is kept as corroborating
// evidence for rebinding decisions instead.
func Derive(evidence Evidence) (ID, error) {
	if !evidence.HasDurableEvidence() {
		return "", fmt.Errorf("%w: no durable evidence to derive an identity from", ErrInvalidIdentity)
	}
	h := sha256.New()
	// A domain separator keeps these hashes from colliding with any other
	// digest in the system.
	h.Write([]byte("marshal.project.identity.v1\x00"))
	h.Write([]byte(strings.TrimSpace(evidence.RootCommit)))
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(evidence.CreatedNonce)))
	sum := h.Sum(nil)
	return ID("PROJECT-" + hex.EncodeToString(sum[:16])), nil
}

// Binding is the project-local record of which project a directory holds.
//
// It lives inside the project, so it moves with it. That is what allows a
// moved project to be recognised: the binding arrives at the new path along
// with everything else.
type Binding struct {
	// ID is the canonical project identity.
	ID ID `json:"project_id"`
	// Evidence is what the ID was derived from, kept so a later check can tell
	// whether this is still the same repository.
	Evidence Evidence `json:"evidence"`
	// RecordedRoot is where the project was when the binding was written. It
	// is informational: a difference means the project moved, not that the
	// binding is wrong.
	RecordedRoot string `json:"recorded_root"`
	// SchemaVersion guards the binding format itself.
	SchemaVersion int `json:"schema_version"`
}

// BindingSchemaVersion is the current binding format.
const BindingSchemaVersion = 1

// Validate rejects a binding that cannot be trusted to identify a project.
func (b Binding) Validate() error {
	if !b.ID.Valid() {
		return fmt.Errorf("%w: binding has a malformed project ID", ErrInvalidIdentity)
	}
	if b.SchemaVersion != BindingSchemaVersion {
		return fmt.Errorf("%w: binding uses schema %d, expected %d",
			ErrInvalidIdentity, b.SchemaVersion, BindingSchemaVersion)
	}
	if !b.Evidence.HasDurableEvidence() {
		return fmt.Errorf("%w: binding records no durable evidence", ErrInvalidIdentity)
	}
	// A binding must be self-consistent: the recorded ID has to be the one its
	// own evidence produces. This is what makes a hand-edited or copied
	// binding detectable rather than simply believed.
	derived, err := Derive(b.Evidence)
	if err != nil {
		return err
	}
	if derived != b.ID {
		return fmt.Errorf("%w: binding ID does not match its own evidence", ErrInvalidIdentity)
	}
	return nil
}

// Verdict is the outcome of checking a binding against observed evidence.
type Verdict string

const (
	// VerdictSame means the observed repository is the project the binding
	// names, at the path it was last seen.
	VerdictSame Verdict = "SAME"
	// VerdictMoved means the same project at a different path. This is a
	// normal condition and is safe to rebind.
	VerdictMoved Verdict = "MOVED"
	// VerdictDifferent means a different repository is present. Its most
	// important consequence is that the existing state must not be adopted:
	// this is the reused-path case that would otherwise leak one project's
	// memory into another.
	VerdictDifferent Verdict = "DIFFERENT"
	// VerdictUnverifiable means the evidence is insufficient to decide. It is
	// treated as unsafe rather than as agreement.
	VerdictUnverifiable Verdict = "UNVERIFIABLE"
)

// SafeToAdopt reports whether existing project state may be used. Only an
// identity that was positively confirmed qualifies; unverifiable does not,
// because adopting state on the strength of not having disproved it is how
// cross-project contamination happens.
func (v Verdict) SafeToAdopt() bool { return v == VerdictSame || v == VerdictMoved }

// Comparison is a verdict together with why it was reached.
type Comparison struct {
	Verdict Verdict `json:"verdict"`
	// Reason is a user-safe explanation.
	Reason string `json:"reason"`
	// ObservedID is the identity the observed evidence produces, when it can
	// be derived.
	ObservedID ID `json:"observed_id,omitempty"`
	// PathChanged reports that the project is not where the binding recorded
	// it, independently of whether it is the same project.
	PathChanged bool `json:"path_changed"`
}

// Compare checks a stored binding against freshly observed evidence.
//
// The comparison is evidence-first: a matching root commit settles the
// question regardless of where the directory sits or what its remote says. A
// path difference alone never changes the verdict, only the PathChanged flag.
func Compare(binding Binding, observed Evidence, observedRoot string) Comparison {
	comparison := Comparison{PathChanged: binding.RecordedRoot != "" && binding.RecordedRoot != observedRoot}

	if err := binding.Validate(); err != nil {
		comparison.Verdict = VerdictUnverifiable
		comparison.Reason = "The recorded project identity could not be verified."
		return comparison
	}

	// A root commit is the strongest evidence available, so when both sides
	// have one it decides the question outright.
	boundCommit := strings.TrimSpace(binding.Evidence.RootCommit)
	seenCommit := strings.TrimSpace(observed.RootCommit)
	if boundCommit != "" && seenCommit != "" {
		if boundCommit != seenCommit {
			comparison.Verdict = VerdictDifferent
			comparison.Reason = "A different repository is in this directory."
			if derived, err := Derive(observed); err == nil {
				comparison.ObservedID = derived
			}
			return comparison
		}
		comparison.ObservedID = binding.ID
		if comparison.PathChanged {
			comparison.Verdict = VerdictMoved
			comparison.Reason = "This project has moved to a new location."
			return comparison
		}
		comparison.Verdict = VerdictSame
		comparison.Reason = "This is the same project."
		return comparison
	}

	// Without a shared root commit the nonce carries identity. It lives in the
	// binding, so it can only confirm that the binding travelled with the
	// directory — which is exactly the moved-project case.
	if boundCommit == "" && strings.TrimSpace(binding.Evidence.CreatedNonce) != "" {
		comparison.ObservedID = binding.ID
		if comparison.PathChanged {
			comparison.Verdict = VerdictMoved
			comparison.Reason = "This project has moved to a new location."
			return comparison
		}
		comparison.Verdict = VerdictSame
		comparison.Reason = "This is the same project."
		return comparison
	}

	// One side has a root commit and the other does not. That is a real
	// change — a repository gained or lost its history — and it is not safe to
	// assume it is the same project.
	comparison.Verdict = VerdictUnverifiable
	comparison.Reason = "This project's identity could not be confirmed."
	return comparison
}

// NormalizeRemote canonicalizes a remote URL so that the same repository
// reached over SSH and HTTPS, with or without a .git suffix or trailing slash,
// compares equal.
//
// Normalization is presentational only. Because the remote does not feed the
// ID derivation, an imperfect normalization cannot merge two projects'
// identities; at worst it weakens a corroborating signal.
func NormalizeRemote(remote string) string {
	value := strings.TrimSpace(remote)
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")

	// Strip the scheme first, so that a userinfo prefix is handled the same
	// way whether or not a scheme was present. Doing this after the userinfo
	// step would leave "ssh://git@host/..." carrying its user.
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		value = strings.TrimPrefix(value, scheme)
	}
	// git@host:owner/repo → host/owner/repo
	if at := strings.Index(value, "@"); at >= 0 && !strings.Contains(value[:at], "/") {
		value = value[at+1:]
		value = strings.Replace(value, ":", "/", 1)
	}
	return strings.ToLower(value)
}

// RelatedTo reports whether two identities share upstream provenance through
// their remotes.
//
// Sharing a remote makes two checkouts *related*, not the same: a fork and its
// upstream are distinct project contexts even while they point at the same
// URL, and each keeps its own memory and evidence unless a user deliberately
// links them. This function exists so that relationship can be shown, never so
// it can be used to merge state.
func RelatedTo(a, b Evidence) bool {
	left, right := NormalizeRemote(a.RemoteURL), NormalizeRemote(b.RemoteURL)
	return left != "" && left == right
}

// SortIDs orders identities for stable presentation.
func SortIDs(ids []ID) {
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
}
