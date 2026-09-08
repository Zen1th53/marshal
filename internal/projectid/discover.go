package projectid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This file gathers identity evidence from a real repository and persists the
// binding.
//
// Every operation here is read-only with one deliberate exception: writing the
// binding file when MARSHAL first adopts a project. That write is what gives a
// project a durable identity, and it happens only when a caller explicitly
// adopts — never as a side effect of looking.

// BindingFileName is where a project records its identity, inside the
// project's own MARSHAL directory so that it travels with the project.
const BindingFileName = "project.json"

// Collector reads repository evidence.
type Collector interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type gitCollector struct{}

// NewGitCollector returns a collector backed by the real git binary.
func NewGitCollector() Collector { return gitCollector{} }

func (gitCollector) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// Evidence collection is bounded. A repository with a pathological history
	// should degrade one piece of evidence, not stall project opening.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	return cmd.Output()
}

// CollectEvidence gathers what can be observed about a repository.
//
// Missing evidence is not an error. A repository with no commits has no root
// commit, and one with no remote has no URL; both are ordinary states, and the
// caller decides whether what was found is enough.
func CollectEvidence(ctx context.Context, collector Collector, root string) Evidence {
	if collector == nil {
		collector = NewGitCollector()
	}
	var evidence Evidence

	// The root commit is the first commit in the repository's history. Taking
	// the last line of the reverse-ordered log gives the earliest reachable
	// commit; --max-parents=0 restricts it to true roots.
	if out, err := collector.Run(ctx, root, "rev-list", "--max-parents=0", "HEAD"); err == nil {
		lines := strings.Fields(strings.TrimSpace(string(out)))
		if len(lines) > 0 {
			// A repository can have several root commits after a graft or an
			// unrelated-history merge. The earliest listed is stable across
			// clones, and sorting keeps the choice deterministic.
			roots := append([]string(nil), lines...)
			earliest := roots[0]
			for _, candidate := range roots[1:] {
				if candidate < earliest {
					earliest = candidate
				}
			}
			evidence.RootCommit = earliest
		}
	}

	if out, err := collector.Run(ctx, root, "config", "--get", "remote.origin.url"); err == nil {
		evidence.RemoteURL = strings.TrimSpace(string(out))
	}
	return evidence
}

// NewNonce mints the random component used to identify a project that has no
// durable repository evidence of its own.
func NewNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("%w: cannot generate a project nonce", ErrInvalidIdentity)
	}
	return hex.EncodeToString(buffer), nil
}

// LoadBinding reads a project's identity binding.
//
// A binding that is absent, unreadable or invalid returns found=false rather
// than an error the caller has to interpret: in every one of those cases the
// answer is the same, which is that this directory has no identity MARSHAL can
// rely on yet.
func LoadBinding(marshalDir string) (Binding, bool) {
	data, err := os.ReadFile(filepath.Join(marshalDir, BindingFileName))
	if err != nil {
		return Binding{}, false
	}
	var binding Binding
	if err := json.Unmarshal(data, &binding); err != nil {
		return Binding{}, false
	}
	if err := binding.Validate(); err != nil {
		return Binding{}, false
	}
	return binding, true
}

// SaveBinding writes a project's identity binding.
//
// It refuses to write an invalid binding, so a malformed identity cannot be
// persisted and then later read back as authoritative. The write is atomic:
// a crash partway through leaves the previous binding intact rather than a
// truncated file that would read as "no identity".
func SaveBinding(marshalDir string, binding Binding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(marshalDir, 0o700); err != nil {
		return fmt.Errorf("%w: cannot create the project state directory", ErrInvalidIdentity)
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: cannot encode the project binding", ErrInvalidIdentity)
	}
	data = append(data, '\n')

	final := filepath.Join(marshalDir, BindingFileName)
	temporary := final + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("%w: cannot write the project binding", ErrInvalidIdentity)
	}
	if err := os.Rename(temporary, final); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("%w: cannot save the project binding", ErrInvalidIdentity)
	}
	return nil
}

// Resolution is the outcome of resolving which project a directory holds.
type Resolution struct {
	// ID is the project identity, when one could be established.
	ID ID `json:"project_id,omitempty"`
	// Comparison explains how the identity relates to what was recorded.
	Comparison Comparison `json:"comparison"`
	// Evidence is what was observed.
	Evidence Evidence `json:"evidence"`
	// Bound reports whether a binding already existed.
	Bound bool `json:"bound"`
	// NeedsRebind reports that the project moved and its binding should be
	// updated once the user or policy confirms. Rebinding is a write, so it is
	// never done as part of resolving.
	NeedsRebind bool `json:"needs_rebind"`
	// AdoptExisting reports whether existing project state may be used.
	AdoptExisting bool `json:"adopt_existing"`
	// Reason is a user-safe explanation.
	Reason string `json:"reason"`
}

// Resolve determines the identity of the project rooted at root.
//
// It never writes. A project with no binding is reported as unbound rather
// than silently adopted, because creating an identity is a decision about
// taking the project on, and that belongs to the caller.
func Resolve(ctx context.Context, collector Collector, root, marshalDir string) Resolution {
	observed := CollectEvidence(ctx, collector, root)
	resolution := Resolution{Evidence: observed}

	stored, found := LoadBinding(marshalDir)
	if !found {
		// No binding. If a MARSHAL state directory exists anyway, this is
		// state whose provenance cannot be established — quite possibly copied
		// from another project — so it is reported rather than adopted.
		if info, err := os.Stat(marshalDir); err == nil && info.IsDir() {
			resolution.Comparison = Comparison{
				Verdict: VerdictUnverifiable,
				Reason:  "Project state was found here but it does not say which project it belongs to.",
			}
			resolution.Reason = resolution.Comparison.Reason
			return resolution
		}
		resolution.Comparison = Comparison{Verdict: VerdictUnverifiable, Reason: "This project has not been set up yet."}
		resolution.Reason = resolution.Comparison.Reason
		return resolution
	}

	resolution.Bound = true
	resolution.Comparison = Compare(stored, observed, root)

	// A binding that says the project lives somewhere else could mean one of
	// two things: the project moved here, or this binding was copied here
	// while the original is still in place. The comparison cannot tell those
	// apart from its inputs alone, because a copied binding is internally
	// consistent and shares the lineage it came from.
	//
	// The filesystem can: if the recorded location still holds this same
	// binding, the project did not move — a copy was made. Treating that as a
	// move would let the second directory adopt the first project's memory.
	if resolution.Comparison.Verdict == VerdictMoved && stillBoundElsewhere(stored) {
		resolution.Comparison.Verdict = VerdictDifferent
		resolution.Comparison.Reason = "This project state was copied from a project that is still in its original location."
		resolution.Reason = resolution.Comparison.Reason
		return resolution
	}

	resolution.Reason = resolution.Comparison.Reason
	resolution.AdoptExisting = resolution.Comparison.Verdict.SafeToAdopt()
	if resolution.AdoptExisting {
		resolution.ID = stored.ID
		resolution.NeedsRebind = resolution.Comparison.Verdict == VerdictMoved
	}
	return resolution
}

// stillBoundElsewhere reports whether the location a binding records still
// holds that same binding.
//
// When it does, the project did not move: it is still there, and what is in
// front of us is a copy of its state. When the recorded location is gone, or
// now holds a different project, the move is genuine.
// It reconstructs the other project's state directory from its recorded root
// using StateDirName, which must match the layout used by project.Discover. A
// mismatch would make this check silently find nothing and report every copy
// as a move, so the name is a named constant rather than a literal.
func stillBoundElsewhere(binding Binding) bool {
	if strings.TrimSpace(binding.RecordedRoot) == "" {
		return false
	}
	elsewhere, found := LoadBinding(filepath.Join(binding.RecordedRoot, StateDirName))
	return found && elsewhere.ID == binding.ID
}

// StateDirName is the project-local MARSHAL state directory. It mirrors the
// layout in internal/project.
const StateDirName = ".marshal"

// Adopt establishes an identity for a project and records it.
//
// This is the only writing operation in the package, and it is deliberate:
// adopting a project is the moment MARSHAL takes responsibility for it. It
// refuses to overwrite an existing binding that belongs to a different
// project, which is what stops a reused directory from quietly acquiring the
// previous occupant's identity.
func Adopt(ctx context.Context, collector Collector, root, marshalDir string) (Binding, error) {
	observed := CollectEvidence(ctx, collector, root)

	if existing, found := LoadBinding(marshalDir); found {
		comparison := Compare(existing, observed, root)
		switch comparison.Verdict {
		case VerdictSame:
			return existing, nil
		case VerdictMoved:
			// The project moved. Update where it was last seen; its identity
			// and evidence are unchanged.
			rebound := existing
			rebound.RecordedRoot = root
			if err := SaveBinding(marshalDir, rebound); err != nil {
				return Binding{}, err
			}
			return rebound, nil
		default:
			return Binding{}, fmt.Errorf(
				"%w: this directory holds project state belonging to a different project",
				ErrInvalidIdentity)
		}
	}

	// Every adoption mints a nonce, not only those with no repository history.
	//
	// A root commit identifies a *lineage*, and two independently created
	// projects can genuinely share one: scaffolding two repositories from the
	// same template in the same second produces byte-identical initial commits
	// and therefore the same hash. Deriving identity from the root commit alone
	// would make those two projects one, and each would see the other's memory.
	//
	// The nonce makes each adoption distinct while the root commit is still
	// recorded, so clone and fork relationships remain visible through
	// RelatedTo and through the evidence itself.
	nonce, err := NewNonce()
	if err != nil {
		return Binding{}, err
	}
	observed.CreatedNonce = nonce

	id, err := Derive(observed)
	if err != nil {
		return Binding{}, err
	}
	binding := Binding{
		ID: id, Evidence: observed, RecordedRoot: root,
		SchemaVersion: BindingSchemaVersion,
	}
	if err := SaveBinding(marshalDir, binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// ErrNotBound reports that a directory holds no usable project identity.
var ErrNotBound = errors.New("this directory has no project identity")
