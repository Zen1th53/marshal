package tui

// Control actions: the mutation-bearing half of the TUI.
//
// Every action here is a request to a canonical authority. Nothing in this file
// writes to the store, decides policy, or produces a success state of its own.
// That is the whole design: a TUI that can report success without a backend
// having agreed is a TUI that lies at exactly the moment it matters most.
//
// The mutation protocol from 06_GOVERNANCE_AND_SAFETY.md is implemented as a
// type, not as a convention:
//
//  1. Read the exact canonical target and its revision/digest.
//  2. Build a typed request carrying that revision.
//  3. Let the backend evaluate policy, risk, capability and approval.
//  4. Submit once, with idempotency protection.
//  5. Reread durable state and report success only when it proves the change.
//
// Step 5 is why every executor returns the rereading itself rather than a
// boolean: "the call did not error" and "the state actually changed" are
// different claims, and only the second one may be shown to a user.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ActionID identifies a Control action by its frozen spec id.
type ActionID string

// Target is the exact canonical thing an action acts on.
//
// It is captured before the confirmation is shown and re-checked at submit, so
// an approval cannot be rebound to a different object between the two.
type Target struct {
	// Kind names the canonical entity: run, task, plan, goal, checkpoint,
	// approval, session.
	Kind string
	// ID is the canonical identifier.
	ID string
	// ActorID binds a secondary principal when the action assigns ownership.
	// It is part of the reviewed request and must not be looked up again after
	// confirmation, otherwise a changing agent list could rebind the action.
	ActorID string
	// Revision is the expected revision, where the entity has one. A mutation
	// carrying a stale revision must be refused by the backend, not by us.
	Revision int64
	// Digest binds the exact content the user was shown.
	Digest string
	// Scope is the bounded blast radius, in the backend's own words.
	Scope string
	// Summary is one line describing the target for the confirmation screen.
	Summary string
}

// String renders a target for audit and display.
func (t Target) String() string {
	if t.Kind == "" && t.ID == "" {
		return "(no target)"
	}
	s := fmt.Sprintf("%s %s", t.Kind, t.ID)
	if t.Revision > 0 {
		s += fmt.Sprintf(" @rev%d", t.Revision)
	}
	if t.Digest != "" {
		s += fmt.Sprintf(" #%s", shortDigest(t.Digest))
	}
	return s
}

func shortDigest(d string) string {
	if len(d) <= 12 {
		return d
	}
	return d[:12]
}

// Safety classifies how much ceremony an action requires.
type Safety int

const (
	// SafetyPlain is an ordinary action: one confirmation.
	SafetyPlain Safety = iota
	// SafetyGoverned requires a canonical approval to exist and be valid.
	SafetyGoverned
	// SafetySensitive touches credentials or identity.
	SafetySensitive
	// SafetyDestructive discards or overwrites work. Its confirmation
	// defaults to Cancel and requires an explicit acknowledgement.
	SafetyDestructive
)

func (s Safety) String() string {
	switch s {
	case SafetyGoverned:
		return "governed"
	case SafetySensitive:
		return "sensitive"
	case SafetyDestructive:
		return "destructive"
	}
	return "action"
}

// SafetyOf maps a frozen node type to its safety class.
func SafetyOf(t NodeType) Safety {
	switch t {
	case NodeGoverned:
		return SafetyGoverned
	case NodeSensitive:
		return SafetySensitive
	case NodeDestructive:
		return SafetyDestructive
	}
	return SafetyPlain
}

// Outcome is what a canonical authority did.
//
// It is deliberately not a boolean. "Refused" and "failed" are different, and
// both are different from "the call returned but durable state did not change"
// — which is the outcome a naive implementation reports as success.
type Outcome struct {
	// Verdict is the canonical result.
	Verdict Verdict
	// Detail is one user-facing line, bounded and free of secrets.
	Detail string
	// Proof is the reread of durable state that demonstrates the change,
	// or the reason no such proof exists.
	Proof Value
	// Target is what was actually acted on, as re-read after the change.
	Target Target
	// Evidence is a bounded canonical reference, never raw payload.
	Evidence string
	// SecretOnce is transient output displayed only while this completed
	// confirmation remains open. It must never be copied into proof, evidence,
	// status, events, or snapshots.
	SecretOnce string
}

// Succeeded reports whether durable state proved the transition.
func (o Outcome) Succeeded() bool { return o.Verdict == VerdictPass && o.Proof.Status.IsSuccess() }

// ActionRequest is a typed, revision-bound mutation request.
//
// It is built once, shown to the user in a confirmation, and submitted
// unchanged. The confirmation displays these exact fields, so what was approved
// and what is submitted cannot diverge.
type ActionRequest struct {
	Action ActionID
	// Title is the action's frozen title.
	Title  string
	Safety Safety
	Target Target
	// SessionID and ProjectID correlate the request, as the protocol requires.
	SessionID string
	ProjectID string
	// ApprovalID names the canonical approval this action consumes, when the
	// action is governed. An empty value on a governed action is a refusal,
	// not a permission.
	ApprovalID string
	// Inputs are the typed, operator-supplied fields collected before review.
	// They are immutable once confirmation begins and are included in the
	// idempotency digest, so a changed form value is a different intent.
	Inputs map[string]string
	// Rationale is the operator's stated reason, recorded by the backend.
	Rationale string
	// IdempotencyKey makes a repeated submission a no-op rather than a second
	// mutation. Enter arriving twice must not run anything twice.
	IdempotencyKey string
	// PreparedAt is when the target was read, so staleness is measurable.
	PreparedAt time.Time
}

// Executor performs one action against a canonical authority.
//
// The signature returns an Outcome rather than an error alone because a refusal
// is a result: "policy declined this" is information the user needs, not a
// failure to report as a crash.
type Executor func(ctx context.Context, req ActionRequest) (Outcome, error)

// Binding is what the TUI knows about one Control action.
type Binding struct {
	Action ActionID
	Title  string
	Safety Safety
	// Inputs describes the closed form required by this action. A binding may
	// never use an unrestricted command field; values are passed only to the
	// named canonical method.
	Inputs []InputSpec
	// Prepare reads the canonical target and its current revision.
	Prepare func(ctx context.Context) (Target, error)
	// PrepareRequest is used when the exact target depends on typed form input.
	// It receives the immutable request that will later be submitted.
	PrepareRequest func(ctx context.Context, req ActionRequest) (Target, error)
	// Execute submits the request to the canonical authority.
	Execute Executor
	// RequiresApproval says this particular backend operation consumes a
	// separately issued canonical approval. SafetyGoverned alone is not enough:
	// a plan approval, a Cloud entitlement request, and a Process 05 tool action
	// are all governed, but only the last may consume a RuntimeApproval. Treating
	// the visual safety class as an approval requirement lets an unrelated
	// approval be attached to the wrong action.
	RequiresApproval bool
	// Gap, when set, means no canonical binding exists. The action renders
	// disabled with this reason and cannot be executed.
	Gap string
	// Requires names a precondition the caller must satisfy, shown when the
	// action is unavailable for a reason other than a gap.
	Requires string
}

// Bound reports whether this action can actually run.
func (b Binding) Bound() bool { return b.Gap == "" && b.Execute != nil }

// ErrNoBinding is returned when an action has no canonical authority behind it.
var ErrNoBinding = errors.New("tui: no canonical binding exists for this action")

// ErrApprovalRequired is returned when a governed action lacks a valid
// approval. It is a refusal, not a failure.
var ErrApprovalRequired = errors.New("tui: this action requires a canonical approval")

// ErrStaleTarget is returned when the target changed between preparation and
// submission.
var ErrStaleTarget = errors.New("tui: the target changed since it was read")

// --- the confirmation state machine ---

// ConfirmPhase is where a pending mutation stands.
type ConfirmPhase int

const (
	// PhaseIdle means nothing is pending.
	PhaseIdle ConfirmPhase = iota
	// PhasePreparing means the canonical target is being read.
	PhasePreparing
	// PhaseConfirming means the user is looking at the exact request.
	PhaseConfirming
	// PhaseSubmitting means the request is in flight. No further submission
	// is accepted in this phase, which is what makes repeated Enter safe.
	PhaseSubmitting
	// PhaseDone means an outcome is being shown.
	PhaseDone
)

func (p ConfirmPhase) String() string {
	switch p {
	case PhasePreparing:
		return "preparing"
	case PhaseConfirming:
		return "confirming"
	case PhaseSubmitting:
		return "submitting"
	case PhaseDone:
		return "done"
	}
	return "idle"
}

// Confirmation is one pending mutation, from preparation to outcome.
//
// It exists so that the sequence "read target, show it, submit that same
// target, prove the result" is a single object with one owner, rather than
// state scattered across a render loop where a stray key can advance it.
type Confirmation struct {
	mu sync.Mutex

	phase   ConfirmPhase
	binding Binding
	request ActionRequest

	// acknowledged records the explicit extra step a destructive action
	// requires. A destructive confirmation defaults to Cancel, so Enter alone
	// must not proceed.
	acknowledged bool

	// selection is which button has focus: 0 Cancel, 1 Proceed. Every
	// confirmation starts on Cancel so key repeat can never turn the Enter that
	// opened a dialog into the Enter that submits it.
	selection int

	outcome  Outcome
	failure  error
	prepared Target

	// submitted guards against a second submission of the same request, which
	// is what a repeated Enter or a doubled key event would otherwise cause.
	submitted bool
}

// Phase reports where the confirmation stands.
func (c *Confirmation) Phase() ConfirmPhase {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.phase
}

// Request returns the exact request under confirmation.
func (c *Confirmation) Request() ActionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request
}

// Binding returns the binding being confirmed.
func (c *Confirmation) Binding() Binding {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.binding
}

// Outcome returns the result, once there is one.
func (c *Confirmation) Outcome() Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.outcome
}

// Failure returns why the action could not proceed, if it could not.
func (c *Confirmation) Failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure
}

// Selection reports the focused button: 0 Cancel, 1 Proceed.
func (c *Confirmation) Selection() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selection
}

// Acknowledged reports whether a destructive action's extra step was taken.
func (c *Confirmation) Acknowledged() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acknowledged
}

// Begin prepares an action for confirmation.
//
// Preparation reads the canonical target, so the confirmation shows the state
// as it actually is rather than as the list last rendered it. An unbound action
// never reaches confirmation at all.
func (c *Confirmation) Begin(ctx context.Context, b Binding, req ActionRequest) error {
	req.Inputs = cloneInputs(req.Inputs)
	c.mu.Lock()
	if c.phase == PhaseSubmitting {
		c.mu.Unlock()
		return fmt.Errorf("tui: a mutation is already in flight")
	}
	c.mu.Unlock()

	if !b.Bound() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reset()
		c.binding, c.request = b, req
		c.phase = PhaseDone
		reason := b.Gap
		if reason == "" {
			reason = ErrNoBinding.Error()
		}
		c.failure = errors.New(reason)
		c.outcome = Outcome{
			Verdict: VerdictBlocked,
			Detail:  reason,
			Proof:   Blocked(reason, b.Title, string(b.Action)),
		}
		return ErrNoBinding
	}

	c.mu.Lock()
	c.reset()
	c.binding, c.request = b, req
	c.phase = PhasePreparing
	c.mu.Unlock()

	var target Target
	var err error
	if b.PrepareRequest != nil {
		target, err = b.PrepareRequest(ctx, req)
	} else if b.Prepare != nil {
		target, err = b.Prepare(ctx)
	} else {
		err = ErrNoBinding
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.phase = PhaseDone
		c.failure = err
		c.outcome = Outcome{
			Verdict: VerdictBlocked,
			Detail:  err.Error(),
			Proof:   Blocked(err.Error(), b.Title, string(b.Action)),
		}
		return err
	}

	c.prepared = target
	c.request.Target = target
	c.request.PreparedAt = time.Now().UTC()
	// The idempotency key binds the action to the exact target revision and
	// digest, so a resubmission after the state moved is a different key and
	// the backend can tell the two apart.
	c.request.IdempotencyKey = idempotencyKey(c.request)
	c.phase = PhaseConfirming
	// Every confirmation starts on Cancel. Submission therefore always needs a
	// distinct Right/Tab gesture, even for a plain action.
	c.selection = 0
	return nil
}

// idempotencyKey derives a stable key from what is actually being done.
func idempotencyKey(req ActionRequest) string {
	keys := make([]string, 0, len(req.Inputs))
	for key := range req.Inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(h, "%s\x00%s\x00", key, req.Inputs[key])
	}
	return fmt.Sprintf("%s|%s|%s|%s|%d|%s|%s|%x",
		req.Action, req.Target.Kind, req.Target.ID,
		req.Target.ActorID, req.Target.Revision, req.Target.Digest, req.SessionID,
		h.Sum(nil))
}

func cloneInputs(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (c *Confirmation) reset() {
	c.phase = PhaseIdle
	c.binding = Binding{}
	c.request = ActionRequest{}
	c.acknowledged = false
	c.selection = 0
	c.outcome = Outcome{}
	c.failure = nil
	c.prepared = Target{}
	c.submitted = false
}

// Cancel dismisses a pending confirmation without mutating anything.
func (c *Confirmation) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A submission already in flight cannot be recalled from here. Saying so
	// is better than clearing the screen and leaving the user believing a
	// mutation was stopped when it was not.
	if c.phase == PhaseSubmitting {
		return
	}
	c.reset()
}

// Dismiss clears a finished confirmation.
func (c *Confirmation) Dismiss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseDone {
		c.reset()
	}
}

// MoveSelection moves between Cancel and Proceed.
func (c *Confirmation) MoveSelection(delta int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase != PhaseConfirming {
		return
	}
	c.selection += delta
	if c.selection < 0 {
		c.selection = 0
	}
	if c.selection > 1 {
		c.selection = 1
	}
}

// Acknowledge records the extra step a destructive action requires.
func (c *Confirmation) Acknowledge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseConfirming {
		c.acknowledged = true
	}
}

// Submit performs the mutation, once.
//
// Every guard here is one the contract names: a destructive action needs its
// acknowledgement, an approval-consuming action needs an approval id, and no
// request may be submitted twice. The re-read of the target before submitting is the TOCTOU
// check: if the canonical state moved while the confirmation was on screen, the
// approval the user gave was for something that no longer exists.
func (c *Confirmation) Submit(ctx context.Context) (Outcome, error) {
	c.mu.Lock()
	if c.phase != PhaseConfirming {
		phase := c.phase
		c.mu.Unlock()
		return Outcome{}, fmt.Errorf("tui: nothing to submit while %s", phase)
	}
	if c.submitted {
		c.mu.Unlock()
		return Outcome{}, fmt.Errorf("tui: this request was already submitted")
	}
	if c.selection != 1 {
		c.mu.Unlock()
		return Outcome{}, fmt.Errorf("tui: the confirmation is on Cancel")
	}
	if c.binding.Safety == SafetyDestructive && !c.acknowledged {
		c.mu.Unlock()
		return Outcome{}, fmt.Errorf(
			"tui: a destructive action requires an explicit acknowledgement first")
	}
	if c.binding.RequiresApproval && strings.TrimSpace(c.request.ApprovalID) == "" {
		c.mu.Unlock()
		return Outcome{}, ErrApprovalRequired
	}

	binding, request, prepared := c.binding, c.request, c.prepared
	c.submitted = true
	c.phase = PhaseSubmitting
	c.mu.Unlock()

	// Re-read the target immediately before submitting. An approval binds an
	// exact revision and digest; if either moved while the confirmation was on
	// screen, the thing the user agreed to is not the thing that would run.
	if binding.Prepare != nil || binding.PrepareRequest != nil {
		var current Target
		var err error
		if binding.PrepareRequest != nil {
			current, err = binding.PrepareRequest(ctx, request)
		} else {
			current, err = binding.Prepare(ctx)
		}
		if err != nil {
			return c.finish(Outcome{
				Verdict: VerdictBlocked,
				Detail:  fmt.Sprintf("the target could not be re-read before submitting: %s", err),
				Proof:   Blocked(err.Error(), binding.Title, string(binding.Action)),
			}, err)
		}
		if changed, why := targetMoved(prepared, current); changed {
			// The acknowledgement was given for state that has now moved, so
			// it is withdrawn: a destructive action must be acknowledged
			// against the state it will actually run on.
			c.mu.Lock()
			c.acknowledged = false
			c.mu.Unlock()
			err := fmt.Errorf("%w: %s", ErrStaleTarget, why)
			return c.finish(Outcome{
				Verdict: VerdictBlocked,
				Detail:  err.Error(),
				Target:  current,
				Proof: Blocked(why,
					"re-open the action to approve the current state", string(binding.Action)),
			}, err)
		}
	}

	outcome, err := binding.Execute(ctx, request)
	return c.finish(outcome, err)
}

// finish records an outcome and leaves the confirmation showing it.
func (c *Confirmation) finish(outcome Outcome, err error) (Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.phase = PhaseDone
	c.failure = err
	if err != nil && outcome.Verdict == "" {
		outcome.Verdict = VerdictFail
		if outcome.Detail == "" {
			outcome.Detail = err.Error()
		}
		if outcome.Proof.Status == TruthUnset {
			outcome.Proof = Errored(err.Error(), string(c.binding.Action))
		}
	}
	// An outcome that carries no proof of the transition is not a success,
	// whatever the call returned. This is the step the contract requires and
	// the one an implementation is most tempted to skip.
	if outcome.Verdict == VerdictPass && !outcome.Proof.Status.IsSuccess() {
		outcome.Verdict = VerdictUnknown
		outcome.Detail = "the authority accepted the request but durable state " +
			"did not prove the change; re-read the target before assuming it applied"
	}
	c.outcome = outcome
	return outcome, err
}

// targetMoved reports whether the canonical target changed between preparation
// and submission.
func targetMoved(prepared, current Target) (bool, string) {
	if prepared.ID != current.ID {
		return true, fmt.Sprintf("the target changed from %s to %s",
			prepared.String(), current.String())
	}
	if prepared.Revision != current.Revision {
		return true, fmt.Sprintf(
			"revision moved from %d to %d while the confirmation was open",
			prepared.Revision, current.Revision)
	}
	if prepared.Digest != current.Digest {
		return true, fmt.Sprintf(
			"the content digest changed from %s to %s while the confirmation was open",
			shortDigest(prepared.Digest), shortDigest(current.Digest))
	}
	// Scope is the blast radius the operator agreed to. A scope widened from
	// one file to the whole repository is a different decision even when the
	// id, revision and digest are unchanged, so it is compared too.
	if prepared.Scope != current.Scope {
		return true, fmt.Sprintf(
			"the scope changed from %q to %q while the confirmation was open",
			prepared.Scope, current.Scope)
	}
	// The summary carries the fields the confirmation actually displayed —
	// status, phase, operation, resource. Comparing it catches a change in
	// anything the operator read but the three identifiers above do not cover.
	if prepared.Summary != current.Summary {
		return true, fmt.Sprintf(
			"the target's state changed from %q to %q while the confirmation was open",
			prepared.Summary, current.Summary)
	}
	return false, ""
}
