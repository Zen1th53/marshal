package constitution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Reversibility states how far an action can be undone. It is deliberately
// coarse: the distinction that matters constitutionally is whether MARSHAL can
// restore the prior state on its own.
type Reversibility string

const (
	// ReversibleInternal means MARSHAL can restore the prior state from its own
	// checkpoints and worktree.
	ReversibleInternal Reversibility = "reversible.internal"
	// ReversibleExternal means restoration depends on a system MARSHAL does not
	// own, so success cannot be assumed.
	ReversibleExternal Reversibility = "reversible.external"
	// Irreversible means the effect cannot be undone once issued.
	Irreversible Reversibility = "irreversible"
	// ReversibilityUnknown is a valid answer (Article IV) and is treated as
	// irreversible for gating purposes.
	ReversibilityUnknown Reversibility = "reversible.unknown"
)

// RequiresRestorePlan reports whether the action must carry a checkpoint or an
// explicit disclosure before it may proceed. Unknown reversibility is treated
// as the dangerous case rather than the convenient one.
func (r Reversibility) RequiresRestorePlan() bool {
	return r != ReversibleInternal
}

func (r Reversibility) valid() bool {
	switch r {
	case ReversibleInternal, ReversibleExternal, Irreversible, ReversibilityUnknown:
		return true
	default:
		return false
	}
}

// Surface names the control path a decision arrived on. It is recorded so that
// cross-surface parity can be asserted, never so that a surface can be given
// weaker treatment (Article XI).
type Surface string

const (
	SurfaceTUI  Surface = "tui"
	SurfaceCLI  Surface = "cli"
	SurfaceWeb  Surface = "web"
	SurfaceMCP  Surface = "mcp"
	SurfaceA2A  Surface = "a2a"
	SurfaceCore Surface = "core"
)

func (s Surface) valid() bool {
	switch s {
	case SurfaceTUI, SurfaceCLI, SurfaceWeb, SurfaceMCP, SurfaceA2A, SurfaceCore:
		return true
	default:
		return false
	}
}

// Mode distinguishes Standard from ULTRA operation. Both are bound to the same
// constitution: ULTRA may raise depth, parallelism and autonomy, and may never
// lower a guarantee (invariant CI-018).
type Mode string

const (
	ModeStandard Mode = "standard"
	ModeUltra    Mode = "ultra"
)

// Envelope is the canonical description of one material decision. Every
// surface builds the same envelope for the same action, which is what makes
// cross-surface authority identical rather than merely similar.
//
// The envelope carries only facts MARSHAL itself resolved. Model-supplied
// material never enters it; it travels separately as an Advisory.
type Envelope struct {
	// DecisionID is assigned by the runtime and is stable for one decision.
	DecisionID string `json:"decision_id"`

	// ConstitutionVersion is the version the session is bound to.
	ConstitutionVersion Version `json:"constitution_version"`
	// Process is the lifecycle process (1-8) the decision belongs to.
	Process int `json:"process"`

	ProjectID   string `json:"project_id"`
	SessionID   string `json:"session_id"`
	GoalID      string `json:"goal_id,omitempty"`
	GoalVersion int    `json:"goal_version,omitempty"`

	// Actor is the principal on whose behalf the action is requested.
	Actor string `json:"actor"`
	// ActorRole is the role the actor holds for this decision.
	ActorRole string `json:"actor_role"`
	// Model and Harness record which provider produced any advisory input.
	// They are provenance only and never affect authority.
	Model   string `json:"model,omitempty"`
	Harness string `json:"harness,omitempty"`

	Surface Surface `json:"surface"`
	Mode    Mode    `json:"mode"`

	Domain Domain `json:"domain"`
	// Action is the concrete operation requested.
	Action string `json:"action"`
	// Scope lists the resources the action touches: paths, endpoints, targets.
	Scope []string `json:"scope,omitempty"`
	// BlastRadius counts the resources affected where that is meaningful.
	BlastRadius int `json:"blast_radius"`

	Reversibility Reversibility `json:"reversibility"`
	// ExternalEffects names effects outside MARSHAL's control. Their presence
	// forces disclosure even when the action is otherwise routine.
	ExternalEffects []string `json:"external_effects,omitempty"`

	// RequiredCapabilities are the capability grants the action needs.
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	// EvidenceIDs reference the evidence supporting the request.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Unknowns records what MARSHAL could not determine. A non-empty list is
	// honest input to the gate, not a defect to be hidden (Article IV).
	Unknowns []string `json:"unknowns,omitempty"`

	// ApprovalID references an approval already held for this exact action.
	ApprovalID string `json:"approval_id,omitempty"`
	// CheckpointID references the restore point taken before a mutation.
	CheckpointID string `json:"checkpoint_id,omitempty"`

	// StateDigest binds the decision to the exact state it was evaluated
	// against, so an approval cannot be replayed against changed state.
	StateDigest string `json:"state_digest"`

	RequestedAt time.Time `json:"requested_at"`
}

// Validate rejects an envelope that is missing the context a constitutional
// decision depends on. Validation failure is a refusal, never a downgrade to a
// permissive default: an envelope MARSHAL cannot understand is one it must not
// authorize.
func (e Envelope) Validate() error {
	if strings.TrimSpace(e.DecisionID) == "" {
		return fmt.Errorf("%w: decision ID is required", ErrInvalidConstitution)
	}
	if e.ConstitutionVersion.IsZero() {
		return fmt.Errorf("%w: decision %s is not bound to a constitution version", ErrInvalidConstitution, e.DecisionID)
	}
	if e.Process < 1 || e.Process > 8 {
		return fmt.Errorf("%w: decision %s names process %d, expected 1-8", ErrInvalidConstitution, e.DecisionID, e.Process)
	}
	if strings.TrimSpace(e.ProjectID) == "" {
		return fmt.Errorf("%w: decision %s has no project ID", ErrInvalidConstitution, e.DecisionID)
	}
	if strings.TrimSpace(e.SessionID) == "" {
		return fmt.Errorf("%w: decision %s has no session ID", ErrInvalidConstitution, e.DecisionID)
	}
	if strings.TrimSpace(e.Actor) == "" {
		return fmt.Errorf("%w: decision %s has no actor", ErrInvalidConstitution, e.DecisionID)
	}
	if !e.Domain.Valid() {
		return fmt.Errorf("%w: decision %s has unclassified domain %q", ErrInvalidConstitution, e.DecisionID, e.Domain)
	}
	if strings.TrimSpace(e.Action) == "" {
		return fmt.Errorf("%w: decision %s has no action", ErrInvalidConstitution, e.DecisionID)
	}
	if !e.Surface.valid() {
		return fmt.Errorf("%w: decision %s arrived on unknown surface %q", ErrInvalidConstitution, e.DecisionID, e.Surface)
	}
	if e.Mode != ModeStandard && e.Mode != ModeUltra {
		return fmt.Errorf("%w: decision %s has unknown mode %q", ErrInvalidConstitution, e.DecisionID, e.Mode)
	}
	if !e.Reversibility.valid() {
		return fmt.Errorf("%w: decision %s has unknown reversibility %q", ErrInvalidConstitution, e.DecisionID, e.Reversibility)
	}
	if e.BlastRadius < 0 {
		return fmt.Errorf("%w: decision %s has a negative blast radius", ErrInvalidConstitution, e.DecisionID)
	}
	if e.RequestedAt.IsZero() {
		return fmt.Errorf("%w: decision %s has no request time", ErrInvalidConstitution, e.DecisionID)
	}
	if strings.TrimSpace(e.StateDigest) == "" {
		return fmt.Errorf("%w: decision %s is not bound to a state digest", ErrInvalidConstitution, e.DecisionID)
	}
	return nil
}

// BindingDigest is the identity an approval is bound to. It covers exactly the
// properties that make an approval meaningful: who asked, for what action, in
// what domain, over what scope, at what blast radius and reversibility, in
// which project and session, against which state.
//
// Anything that changes those properties changes the digest, which stales the
// approval (invariant CI-002). The digest deliberately excludes the decision
// ID, timestamps and the surface, so that re-issuing the identical action does
// not require a new approval and no surface can obtain a distinct binding for
// the same material action.
func (e Envelope) BindingDigest() string {
	scope := append([]string(nil), e.Scope...)
	sort.Strings(scope)
	effects := append([]string(nil), e.ExternalEffects...)
	sort.Strings(effects)
	capabilities := append([]string(nil), e.RequiredCapabilities...)
	sort.Strings(capabilities)

	h := sha256.New()
	for _, field := range []string{
		e.ConstitutionVersion.String(),
		e.ProjectID,
		e.SessionID,
		e.GoalID,
		fmt.Sprint(e.GoalVersion),
		e.Actor,
		e.ActorRole,
		string(e.Domain),
		e.Action,
		strings.Join(scope, "\x1f"),
		fmt.Sprint(e.BlastRadius),
		string(e.Reversibility),
		strings.Join(effects, "\x1f"),
		strings.Join(capabilities, "\x1f"),
		e.StateDigest,
	} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
