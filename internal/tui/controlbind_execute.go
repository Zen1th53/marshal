package tui

// Control bindings: execution, submitting to the canonical authority.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// --- execution: submitting to the canonical authority ---

func (s *ControlSource) modeBinding(id ActionID, title, mode string) Binding {
	return Binding{
		Action: id, Title: title, Safety: SafetyPlain,
		Prepare: s.prepareULTRA,
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			// A Standard autonomy mode is a session preference rather than an
			// entitlement, but it is still session state and still belongs to
			// the session authority. Writing it to a field in this package
			// would let the screen report a change that happened nowhere.
			if err := s.available(); err != nil {
				return Outcome{}, err
			}
			if err := s.Authority.SetAutonomyMode(ctx, mode); err != nil {
				return refusal("the autonomy mode could not be set", err,
					req.Target, "session state")
			}
			// Prove it by reading the mode back.
			current, err := s.Authority.AutonomyMode(ctx)
			if err != nil {
				return unproven(req.Target, "the session mode", err)
			}
			if !strings.EqualFold(current, mode) {
				reason := fmt.Sprintf(
					"the mode was submitted as %s but the session reads as %s",
					mode, current)
				return Outcome{
					Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
					Proof: Unknown(reason, "session state"),
				}, nil
			}
			return Outcome{
				Verdict: VerdictPass,
				Detail:  fmt.Sprintf("session autonomy set to %s", current),
				Target:  req.Target,
				Proof:   Known(current, "session state"),
			}, nil
		},
	}
}

func (s *ControlSource) executeULTRAMode(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// ULTRA is not a mode the TUI can select. It is the gate's answer to
	// whether a verified lease is held, so this reports that answer rather
	// than setting anything. Turning a local preference into ULTRA is exactly
	// the bypass the governance contract forbids.
	if !s.Authority.ULTRAEntitled() {
		reason := "ULTRA is not active: the Community Cloud gate holds no verified " +
			"lease for this session. Request an entitlement; nothing local can grant it."
		return Outcome{
			Verdict: VerdictBlocked,
			Detail:  reason,
			Target:  req.Target,
			Proof:   Blocked(reason, "Control / Mode & Autonomy / Request ULTRA entitlement", "internal/cloud/gate.go"),
		}, nil
	}
	// Entitlement is necessary but not itself the user's mode choice. Record
	// the choice through the same Workspace authority used by /mode, then read
	// it back. This never changes the gate and therefore cannot mint ULTRA.
	if err := s.Authority.SetAutonomyMode(ctx, "ultra"); err != nil {
		return refusal("ULTRA mode could not be selected", err, req.Target,
			"tui.Workspace command state")
	}
	mode, err := s.Authority.AutonomyMode(ctx)
	if err != nil {
		return unproven(req.Target, "the session mode", err)
	}
	if !strings.EqualFold(mode, "ultra") || !s.Authority.ULTRAEntitled() {
		reason := "the mode choice did not read back as ULTRA under a currently verified lease"
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "tui.Workspace and internal/cloud/gate.go")}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  "ULTRA is active for this session under a verified lease",
		Target:  req.Target,
		Proof:   Known("ULTRA", "internal/cloud/gate.go Gate.Entitled"),
	}, nil
}

// executeULTRAPreference records the preference through the canonical owner.
//
// The frozen spec names tui.Workspace as that owner. An earlier version wrote a
// field in this package instead, which the workspace never read: the screen
// reported a change that had happened in no state anyone consults.
//
// The preference is not an authority. With no entitlement held it changes
// nothing that can run, and the outcome says so rather than implying otherwise.
func (s *ControlSource) executeULTRAPreference(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	current, err := s.Authority.ULTRAExecutionPreference(ctx)
	if err != nil {
		return refusal("the current preference could not be read", err,
			req.Target, "tui.Workspace command state")
	}
	want := !current
	if err := s.Authority.SetULTRAExecutionPreference(ctx, want); err != nil {
		return refusal("the preference could not be set", err,
			req.Target, "tui.Workspace command state")
	}
	// Prove it by reading it back rather than trusting the call.
	after, err := s.Authority.ULTRAExecutionPreference(ctx)
	if err != nil {
		return unproven(req.Target, "the execution preference", err)
	}
	if after != want {
		reason := fmt.Sprintf(
			"the preference was submitted as %t but reads back as %t", want, after)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "tui.Workspace command state"),
		}, nil
	}

	detail := fmt.Sprintf("ULTRA execution preference set to %t", after)
	if !s.Authority.ULTRAEntitled() {
		detail += "; with no entitlement held this preference grants nothing"
	}
	return Outcome{
		Verdict: VerdictPass, Detail: detail, Target: req.Target,
		Proof: Known(fmt.Sprintf("%t", after), "tui.Workspace command state"),
	}, nil
}

func (s *ControlSource) executeRequestULTRA(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	before := s.Authority.ULTRAEntitled()
	if err := s.Authority.RequestULTRA(ctx); err != nil {
		// A refused or unreachable Cloud is a result, not a crash. The reason
		// distinguishes "you were never entitled" from "the server said 429",
		// which are different problems with different answers.
		reason := fmt.Sprintf("the Community Cloud did not grant an entitlement: %s", err)
		return Outcome{
			Verdict: VerdictBlocked,
			Detail:  reason,
			Target:  req.Target,
			Proof:   Refused(reason, "internal/cloud"),
		}, nil
	}
	// Reread the gate. A request that returned without error but left the gate
	// unentitled has not produced ULTRA, and must not be reported as though it
	// had: the server decides, and its decision may be "pending".
	after := s.Authority.ULTRAEntitled()
	if !after {
		reason := "the request was accepted but no verified lease is held yet; " +
			"an entitlement becomes active only once the server issues and this " +
			"client verifies a signed lease"
		return Outcome{
			Verdict: VerdictNotRun,
			Detail:  reason,
			Target:  req.Target,
			Proof:   NotRun(reason, "internal/cloud/gate.go Gate.Entitled"),
		}, nil
	}
	detail := "a verified lease is now held and ULTRA is active"
	if before {
		detail = "a verified lease was already held; ULTRA remains active"
	}
	return Outcome{
		Verdict: VerdictPass, Detail: detail, Target: req.Target,
		Proof: Known("ULTRA", "internal/cloud/gate.go Gate.Entitled"),
	}, nil
}

func (s *ControlSource) executeStartRun(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	run, err := s.Authority.StartRun(ctx, s.SessionID, req.Target)
	if err != nil {
		return refusal("the run could not be started", err, req.Target, "internal/execution/engine.go")
	}
	// Prove the transition by rereading the run rather than trusting the
	// return value: the call and the durable state are two different facts.
	proof, err := s.Authority.GetRun(ctx, run.RunID)
	if err != nil {
		return unproven(req.Target, run.RunID, err)
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("run %s started", proof.RunID),
		Target:  Target{Kind: "run", ID: proof.RunID, Digest: string(proof.CurrentPhase)},
		Proof:   Known(fmt.Sprintf("run %s, phase %s", proof.RunID, proof.CurrentPhase), "internal/execution"),
	}, nil
}

func (s *ControlSource) executeContinueRun(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	run, err := s.Authority.ExecuteRun(ctx, req.Target.ID, req.Target.Revision)
	if err != nil {
		return refusal("the run could not be continued", err, req.Target, "internal/execution/engine.go")
	}
	proof, err := s.Authority.GetRun(ctx, run.RunID)
	if err != nil {
		return unproven(req.Target, run.RunID, err)
	}
	// "Advanced" is a claim about movement, so something must have moved: the
	// CAS version or the phase. A run identical to the one submitted against
	// did not advance, whatever the call returned.
	if proof.Version == req.Target.Revision && string(proof.CurrentPhase) == req.Target.Digest {
		reason := fmt.Sprintf(
			"the run was accepted but %s is still at version %d in phase %s; "+
				"it may be waiting on an approval, a lease or a provider",
			proof.RunID, proof.Version, proof.CurrentPhase)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "run", ID: proof.RunID, Revision: proof.Version,
				Digest: string(proof.CurrentPhase)},
			Proof: Unknown(reason, "internal/execution"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("run %s advanced to %s", proof.RunID, proof.CurrentPhase),
		Target:  Target{Kind: "run", ID: proof.RunID, Revision: proof.Version, Digest: string(proof.CurrentPhase)},
		Proof:   Known(fmt.Sprintf("phase %s", proof.CurrentPhase), "internal/execution"),
	}, nil
}

func (s *ControlSource) executeClaimTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// The expected revision travels with the claim, so a task that moved since
	// the confirmation was shown is refused by the store's own CAS check
	// rather than by anything here.
	if err := s.Authority.ClaimTask(ctx, req.Target.ID, req.Target.ActorID, req.Target.Revision); err != nil {
		return refusal("the task could not be claimed", err, req.Target, "internal/store")
	}
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	// A claim writes, so the revision must have moved past the one submitted.
	if task.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the claim was accepted but task %s is still at revision %d",
			task.ID, task.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "task", ID: task.ID, Revision: task.Revision},
			Proof:  Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("task %s claimed", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(fmt.Sprintf("%s at revision %d", task.Status, task.Revision), "internal/store"),
	}, nil
}

func (s *ControlSource) executeReleaseTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	_, _, reason := s.selection()
	if reason == "" {
		reason = "released from the Control screen"
	}
	if err := s.Authority.ReleaseTask(ctx, req.Target.ID, req.Target.Revision, reason); err != nil {
		return refusal("the lease could not be released", err, req.Target, "internal/store")
	}
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	// A release writes, so the revision must have moved.
	if task.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the release was accepted but task %s is still at revision %d",
			task.ID, task.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "task", ID: task.ID, Revision: task.Revision},
			Proof:  Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("lease on task %s released", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(fmt.Sprintf("%s at revision %d", task.Status, task.Revision), "internal/store"),
	}, nil
}

func (s *ControlSource) executeCancelTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.CancelTask(ctx, req.Target.ID, req.Target.Revision); err != nil {
		return refusal("the task could not be cancelled", err, req.Target, "internal/app/runtime.go")
	}
	// Prove it: a cancellation that left the task running is not a
	// cancellation, whatever the call returned.
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	if task.Status != model.TaskCancelled {
		reason := fmt.Sprintf(
			"the cancellation was accepted but task %s is still %s; "+
				"it may complete before the cancellation takes effect",
			task.ID, task.Status)
		return Outcome{
			Verdict: VerdictUnknown,
			Detail:  reason,
			Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
			Proof:   Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("task %s cancelled", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(string(task.Status), "internal/store"),
	}, nil
}

func (s *ControlSource) executeCancelPlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	state, err := s.Authority.CancelPlan(ctx, req.Target.Revision)
	if err != nil {
		return refusal("the plan could not be cancelled", err, req.Target, "internal/app/plan_runtime.go")
	}
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, state.PlanID, err)
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("plan %s cancelled", proof.PlanID),
		Target:  Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version, Digest: proof.Digest},
		Proof:   Known(proof.Status, "internal/plan"),
	}, nil
}

// executeApproveGoal records Process 03 confirmation.
//
// Runtime.ApproveGoal is the canonical boundary: it checks the expected
// revision itself and writes a new CAS-guarded revision. Composing
// goalintake.ApproveContract with a store write here would put a goal
// lifecycle transition in the TUI, which Process 03 owns.
func (s *ControlSource) executeApproveGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	submitted, err := s.Authority.ApproveGoal(ctx, s.SessionID, req.Target.Revision)
	if err != nil {
		return refusal("the goal could not be confirmed", err, req.Target,
			"internal/app/goal_runtime.go")
	}
	// Reread durable state rather than trusting the mutation's own return.
	// "The call returned a struct saying APPROVED" and "the store holds an
	// approved goal" are different facts, and only the second may be shown.
	approved, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, submitted.ID, err)
	}
	// Prove it: the revision must have advanced and the confirmation settled.
	if approved.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the confirmation was accepted but goal %s is still at revision %d",
			approved.ID, approved.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision},
			Proof:  Unknown(reason, "internal/store/goal.go"),
		}, nil
	}
	if approved.Confirmation != model.ConfirmationApproved {
		reason := fmt.Sprintf(
			"the goal advanced to revision %d but reads as %s rather than APPROVED",
			approved.Revision, approved.Confirmation)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision},
			Proof:  Unknown(reason, "internal/store/goal.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("goal %s confirmed at revision %d",
			approved.ID, approved.Revision),
		Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision,
			Digest: approved.RequestDigest},
		Proof: Known(string(approved.Confirmation), "internal/store/goal.go"),
		Evidence: fmt.Sprintf("goal:%s rev:%d digest:%s",
			approved.ID, approved.Revision, shortDigest(approved.RequestDigest)),
	}, nil
}

// executeReviseGoal creates a fresh pending Process 03 revision. The runtime
// owns intake reconstruction, hard-constraint preservation, and the CAS
// write; Control supplies only the operator's typed interpretation and reason.
func (s *ControlSource) executeReviseGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	revised, err := s.Authority.ReviseGoal(ctx, app.CommandEnvelope{ProjectID: s.ProjectID, SessionID: s.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}, req.Inputs["interpretation"], req.Inputs["reason"])

	if err != nil {
		return refusal("the goal could not be revised", err, req.Target, "internal/app/goal_runtime.go")
	}
	proof, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, revised.ID, err)
	}
	if proof.ID != req.Target.ID || proof.Revision <= req.Target.Revision ||
		proof.Confirmation != model.ConfirmationPending ||
		proof.DesiredOutcome != strings.TrimSpace(req.Inputs["interpretation"]) {
		reason := fmt.Sprintf("goal revision did not durably match the reviewed typed intent (id=%s revision=%d confirmation=%s)", proof.ID, proof.Revision, proof.Confirmation)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/goal.go")}, nil
	}
	return Outcome{Verdict: VerdictPass,
		Detail: fmt.Sprintf("goal %s revised to version %d; it requires fresh confirmation", proof.ID, proof.Revision),
		Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest, Scope: req.Target.Scope, Summary: proof.RevisionReason},
		Proof:  Known(fmt.Sprintf("pending goal revision %d", proof.Revision), "internal/app/goal_runtime.go")}, nil
}

// executeRemember submits typed semantic knowledge only through MemoryService
// and proves success by rereading the exact durable record. It does not turn a
// UI card into a memory row itself.
func (s *ControlSource) executeRemember(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	record, err := s.Authority.Remember(ctx, req.Inputs["title"], req.Inputs["body"])
	if err != nil {
		return refusal("the memory could not be recorded", err, req.Target, "internal/app/memory_runtime.go")
	}
	proof, err := s.Authority.MemoryRecord(ctx, record.ID)
	if err != nil {
		return unproven(req.Target, record.ID, err)
	}
	if proof.ID != record.ID || proof.Title != strings.TrimSpace(req.Inputs["title"]) || proof.Body != strings.TrimSpace(req.Inputs["body"]) {
		reason := fmt.Sprintf("memory %s was accepted but its durable reread does not match the typed record", record.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory_v2.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("memory %s recorded", proof.ID),
		Target: Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: proof.ScopeID, Summary: proof.Title},
		Proof:  Known(proof.ID, "internal/app/memory_runtime.go")}, nil
}

func (s *ControlSource) executeCaptureOutcome(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	record, err := s.Authority.CaptureOutcome(ctx, req.Target.ID,
		strings.ToLower(strings.TrimSpace(req.Inputs["status"])), req.Inputs["failure_reason"])
	if err != nil {
		return refusal("the task outcome could not be captured", err, req.Target, "internal/app/memory_runtime.go")
	}
	proof, err := s.Authority.MemoryRecord(ctx, record.ID)
	if err != nil {
		return unproven(req.Target, record.ID, err)
	}
	if proof.ID != record.ID || proof.RunID == "" || proof.ScopeID != req.Target.ID {
		reason := fmt.Sprintf("captured outcome %s did not reread with the exact task/run binding", record.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("outcome for task %s captured as %s", req.Target.ID, req.Inputs["status"]),
		Target: Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: proof.ScopeID, Summary: proof.Title},
		Proof:  Known(proof.ID, "internal/app/memory_runtime.go")}, nil
}

// executeCreatePlan builds a Process 04 plan from the confirmed goal.
func (s *ControlSource) executeCreatePlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	created, err := s.Authority.CreatePlan(ctx, s.SessionID)
	if err != nil {
		return refusal("the plan could not be created", err, req.Target,
			"internal/app/plan_runtime.go")
	}
	// Prove it by rereading the current plan rather than trusting the return.
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, created.PlanID, err)
	}
	if proof.PlanID == "" || proof.PlanID != created.PlanID {
		reason := fmt.Sprintf(
			"plan %s was created but the current plan reads as %q",
			created.PlanID, proof.PlanID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan"),
		}, nil
	}
	if strings.EqualFold(proof.Status, "BLOCKED") {
		reason := fmt.Sprintf("plan %s was created with status BLOCKED: decomposition incomplete or blocked", proof.PlanID)
		return Outcome{
			Verdict: VerdictFail,
			Detail:  reason,
			Target: Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version,
				Digest: proof.Digest},
			Proof: Blocked(reason, "goal", "internal/plan"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("plan %s created at version %d", proof.PlanID, proof.Version),
		Target: Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version,
			Digest: proof.Digest},
		Proof: Known(proof.Status, "internal/plan"),
	}, nil
}

// prepareVerification reads the current Process 06 session as the target.
func (s *ControlSource) prepareVerification(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil || runID == "" {
		return Target{}, errors.New("no run is active, so no verification applies")
	}
	session, err := s.Authority.CurrentVerification(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the verification session could not be read: %w", err)
	}
	if session.ID == "" {
		return Target{}, errors.New("no verification session has been started for this run")
	}
	return Target{
		Kind:     "verification",
		ID:       session.ID,
		Revision: session.Version,
		Digest:   string(session.State),
		Scope:    fmt.Sprintf("run %s", session.Binding.RunID),
		Summary: fmt.Sprintf("verification %s v%d (%s) for run %s",
			session.ID, session.Version, session.State, session.Binding.RunID),
	}, nil
}

// executeStartVerification opens a Process 06 session for the current run.
func (s *ControlSource) executeStartVerification(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	started, err := s.Authority.StartVerification(ctx, req.Target.ID)
	if err != nil {
		return refusal("the verification session could not be started", err,
			req.Target, "internal/app/verification_runtime.go")
	}
	// Prove it by rereading the session rather than trusting the return.
	proof, err := s.Authority.Verification(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, started.ID, err)
	}
	if proof.ID == "" || proof.ID != started.ID {
		reason := fmt.Sprintf(
			"verification %s was started but reads back as %q", started.ID, proof.ID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, verifySource),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("verification %s started", proof.ID),
		Target: Target{Kind: "verification", ID: proof.ID, Revision: proof.Version,
			Digest: string(proof.State)},
		Proof: Known(string(proof.State), verifySource),
	}, nil
}

// executeEvaluateVerification asks Process 06 for its decision.
//
// The verdict is the service's. This reports it whatever it is — including a
// failure — and never upgrades a non-result, which is the single rule that
// makes the whole section worth reading.
func (s *ControlSource) executeEvaluateVerification(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	evaluated, err := s.Authority.EvaluateVerification(ctx, req.Target.ID)
	if err != nil {
		return refusal("the verification could not be evaluated", err,
			req.Target, "internal/app/verification_runtime.go")
	}
	proof, err := s.Authority.Verification(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, evaluated.ID, err)
	}
	// The evaluation must have moved the session, or nothing was decided.
	if proof.Version <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the evaluation was accepted but verification %s is still at version %d",
			proof.ID, proof.Version)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, verifySource),
		}, nil
	}
	// A decision of FAIL is a successful evaluation reporting a failure. The
	// action succeeded; the verification did not, and both are said plainly.
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("verification %s evaluated: Process 06 decided %s",
			proof.ID, proof.State),
		Target: Target{Kind: "verification", ID: proof.ID, Revision: proof.Version,
			Digest: string(proof.State)},
		Proof:    verificationStatus(string(proof.State)),
		Evidence: fmt.Sprintf("verification:%s v%d", proof.ID, proof.Version),
	}, nil
}
