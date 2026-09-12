package tui

// Control screens: what the mutation-bearing section renders.
//
// Two things here matter more than the rest. First, an approval screen shows
// the exact action, target, scope, digest and revision the decision binds to —
// never a generic "approve the current thing", because an approval that does
// not name what it approves can be silently rebound to something else. Second,
// a confirmation shows the same typed request that will be submitted, so what
// the user agreed to and what runs cannot diverge.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// ControlSnapshot is the Control state rendered at one instant.
type ControlSnapshot struct {
	// Approvals is the pending queue, newest first.
	Approvals               []*execution.RuntimeApproval
	ResolvedApprovals       []*execution.RuntimeApproval
	ResolvedApprovalsStatus Value
	// ApprovalsStatus explains an unreadable or empty queue.
	ApprovalsStatus Value
	// Checkpoints are durable checkpoints, newest first.
	Checkpoints []execution.CheckpointRecord
	// CheckpointsStatus explains an unreadable or empty list.
	CheckpointsStatus Value
	// Run is the current run's position.
	Run Value
	// Plan is the current plan's position.
	Plan Value
	// Goal is the active goal's position.
	Goal Value
	// Mode is the session's operating mode, from the canonical gate.
	Mode Value
	// Autonomy and ULTRAPreference are Workspace-owned preferences. They never
	// imply entitlement; Mode remains the Cloud gate's answer.
	Autonomy          Value
	ULTRAPreference   Value
	Tasks             Value
	Canaries          Value
	Budget            *model.ConsumedBudget
	BudgetStatus      Value
	Termination       *model.GoalTermination
	TerminationStatus Value
	// Selected names which approval and checkpoint the actions act on.
	SelectedApproval   string
	SelectedCheckpoint string
	// ObservedAt is when this was read.
	ObservedAt time.Time
}

const controlSource = "internal/execution, internal/app"

// ReadControl gathers the Control state at one instant.
func (s *ControlSource) ReadControl(ctx context.Context) ControlSnapshot {
	snap := ControlSnapshot{ObservedAt: s.now()}
	if err := s.available(); err != nil {
		unavailable := Unknown(err.Error(), controlSource)
		snap.ApprovalsStatus, snap.CheckpointsStatus = unavailable, unavailable
		snap.ResolvedApprovalsStatus = unavailable
		snap.Run, snap.Plan, snap.Goal, snap.Mode = unavailable, unavailable, unavailable, unavailable
		snap.Autonomy, snap.ULTRAPreference, snap.Tasks, snap.Canaries = unavailable, unavailable, unavailable, unavailable
		snap.BudgetStatus, snap.TerminationStatus = unavailable, unavailable
		return snap
	}

	snap.SelectedApproval, snap.SelectedCheckpoint, _ = s.selection()

	if approvals, err := s.Authority.PendingApprovals(ctx); err != nil {
		snap.ApprovalsStatus = Errored(
			fmt.Sprintf("the approval queue could not be read: %s", err), controlSource)
	} else if len(approvals) == 0 {
		snap.ApprovalsStatus = Empty(controlSource)
	} else {
		snap.Approvals = approvals
		snap.ApprovalsStatus = Known(
			fmt.Sprintf("%d awaiting decision", len(approvals)), controlSource)
	}
	if approvals, err := s.Authority.ResolvedApprovals(ctx); err != nil {
		snap.ResolvedApprovalsStatus = Errored(fmt.Sprintf("approval history could not be read: %s", err), controlSource)
	} else if len(approvals) == 0 {
		snap.ResolvedApprovalsStatus = Empty(controlSource)
	} else {
		snap.ResolvedApprovals = approvals
		snap.ResolvedApprovalsStatus = Known(fmt.Sprintf("%d decision(s)", len(approvals)), controlSource)
	}

	if records, err := s.Authority.Checkpoints(ctx); err != nil {
		snap.CheckpointsStatus = Errored(
			fmt.Sprintf("checkpoints could not be listed: %s", err), controlSource)
	} else if len(records) == 0 {
		snap.CheckpointsStatus = Empty(controlSource)
	} else {
		snap.Checkpoints = records
		snap.CheckpointsStatus = Known(fmt.Sprintf("%d durable", len(records)), controlSource)
	}

	snap.Run = targetValue(s.prepareRun(ctx))
	snap.Plan = targetValue(s.preparePlan(ctx))
	snap.Goal = targetValue(s.prepareGoal(ctx))
	if mode, err := s.Authority.AutonomyMode(ctx); err != nil {
		snap.Autonomy = Unknown(err.Error(), "tui.Workspace")
	} else {
		snap.Autonomy = Known(mode, "tui.Workspace")
	}
	if preference, err := s.Authority.ULTRAExecutionPreference(ctx); err != nil {
		snap.ULTRAPreference = Unknown(err.Error(), "tui.Workspace")
	} else {
		snap.ULTRAPreference = Known(fmt.Sprintf("%t", preference), "tui.Workspace")
	}
	if tasks, err := s.Authority.Tasks(ctx); err != nil {
		snap.Tasks = Unknown(err.Error(), "internal/app.Runtime.Tasks")
	} else {
		snap.Tasks = Known(fmt.Sprintf("%d canonical task(s)", len(tasks)), "internal/app.Runtime.Tasks")
	}
	if canaries, err := s.Authority.ActiveCanaries(ctx); err != nil {
		snap.Canaries = Unknown(err.Error(), "internal/app.OptimizationService")
	} else {
		snap.Canaries = Known(fmt.Sprintf("%d active canary rollout(s)", len(canaries)), "internal/app.OptimizationService")
	}
	if goal, err := s.Authority.CurrentGoal(ctx); err == nil && goal.ID != "" {
		if consumed, budgetErr := s.Authority.BudgetConsumed(ctx, goal.ID, goal.Revision); budgetErr != nil {
			snap.BudgetStatus = NotRun(budgetErr.Error(), "internal/store.GetBudgetTracker")
		} else {
			snap.Budget = &consumed
			snap.BudgetStatus = Known("recorded", "internal/store.GetBudgetTracker")
		}
		if term, termErr := s.Authority.GoalTermination(ctx, goal.ID, goal.Revision); termErr != nil {
			snap.TerminationStatus = NotRun(termErr.Error(), "internal/store.GetGoalTermination")
		} else {
			snap.Termination = &term
			snap.TerminationStatus = Known(string(term.State), "internal/store.GetGoalTermination")
		}
	}

	if s.Authority.ULTRAEntitled() {
		snap.Mode = Known("ULTRA", "internal/cloud/gate.go Gate.Entitled")
	} else {
		snap.Mode = Known("Standard", "internal/cloud/gate.go Gate.Entitled")
	}
	return snap
}

// targetValue renders a prepared target, or why it could not be prepared.
func targetValue(target Target, err error) Value {
	if err != nil {
		// A missing run or plan is a legitimate state, not a failure: it means
		// there is nothing to act on yet.
		return NotRun(err.Error(), controlSource)
	}
	return Known(target.Summary, controlSource)
}

// ApprovalFields renders the exact binding a decision would carry.
//
// Every field the governance contract names is shown: action, target, scope,
// digest, revision, requester, expiry and consumption state. This is what makes
// the decision specific — a screen that showed only "approve?" would let the
// same keystroke apply to whatever happened to be first in the queue.
func ApprovalFields(a *execution.RuntimeApproval, now time.Time) []Field {
	if a == nil {
		return []Field{{Label: "Approval", Value: Empty(controlSource)}}
	}
	fields := []Field{
		{Label: "Approval", Value: Known(a.ApprovalID, controlSource)},
		{Label: "Action", Value: Known(a.OperationType, controlSource)},
		{Label: "Target", Value: Known(a.TargetResource, controlSource)},
		{Label: "Risk", Value: Known(string(a.RiskLevel), controlSource)},
	}

	if a.Scope != "" {
		fields = append(fields, Field{Label: "Scope", Value: Known(a.Scope, controlSource)})
	} else {
		// An unstated scope is not an unlimited one, and it must not read as
		// though the blast radius were known to be small.
		fields = append(fields, Field{Label: "Scope", Value: Unknown(
			"this approval records no scope, so the blast radius is not stated",
			controlSource)})
	}

	fields = append(fields,
		Field{Label: "Action digest", Value: Known(a.ActionDigest, controlSource)},
		Field{Label: "State digest", Value: Known(a.StateDigest, controlSource)},
		Field{Label: "Plan", Value: Known(
			fmt.Sprintf("%s version %d", a.PlanID, a.PlanVersion), controlSource)},
		Field{Label: "Run / task", Value: Known(
			fmt.Sprintf("%s / %s", a.RunID, a.TaskID), controlSource)},
		Field{Label: "Requested", Value: Known(
			a.CreatedAt.UTC().Format(time.RFC3339), controlSource)},
	)

	// Expiry and consumption are the two states that make an approval unusable,
	// and both must be visible before a decision rather than discovered after.
	fields = append(fields, Field{Label: "Expiry", Value: approvalExpiry(a, now)})
	fields = append(fields, Field{Label: "State", Value: approvalState(a)})

	if a.DiffPreview != "" {
		fields = append(fields, Field{Label: "Diff", Value: Known(
			firstLines(a.DiffPreview, 6), controlSource)})
	} else {
		fields = append(fields, Field{Label: "Diff", Value: Empty(controlSource)})
	}
	return fields
}

func approvalExpiry(a *execution.RuntimeApproval, now time.Time) Value {
	if a.ExpiresAt == nil {
		return Unknown("this approval records no expiry", controlSource)
	}
	remaining := a.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return Value{
			Text:   a.ExpiresAt.UTC().Format(time.RFC3339),
			Status: TruthStale,
			Reason: "this approval has expired and can no longer be decided",
			Source: controlSource, Observed: now,
		}
	}
	return Known(fmt.Sprintf("%s (in %s)",
		a.ExpiresAt.UTC().Format(time.RFC3339), roundDuration(remaining)), controlSource)
}

func approvalState(a *execution.RuntimeApproval) Value {
	switch a.Status {
	case execution.ApprovalRequested:
		return Known("awaiting decision", controlSource)
	case execution.ApprovalApproved, execution.ApprovalDenied:
		who := a.ApprovedBy
		if who == "" {
			who = "an unrecorded decider"
		}
		return Value{
			Text:   fmt.Sprintf("%s by %s", a.Status, who),
			Status: TruthStale,
			Reason: "this approval has already been decided and cannot be decided again",
			Source: controlSource,
		}
	}
	return Value{
		Text: string(a.Status), Status: TruthStale,
		Reason: fmt.Sprintf("this approval is %s and cannot be decided", a.Status),
		Source: controlSource,
	}
}

func firstLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") +
		fmt.Sprintf("\n… %d more line(s) not shown", len(lines)-n)
}

// CheckpointFields renders one checkpoint's durable record.
func CheckpointFields(record execution.CheckpointRecord) []Field {
	fields := []Field{
		{Label: "Checkpoint", Value: Known(record.CheckpointID, controlSource)},
		{Label: "Run / task", Value: Known(
			fmt.Sprintf("%s / %s", record.RunID, record.TaskID), controlSource)},
		{Label: "Taken", Value: Known(
			record.CreatedAt.UTC().Format(time.RFC3339), controlSource)},
		{Label: "Reason", Value: Known(record.Reason, controlSource)},
		{Label: "State digest", Value: Known(record.StateDigest, controlSource)},
	}
	if record.SnapshotDigest != "" {
		fields = append(fields, Field{Label: "Snapshot digest",
			Value: Known(record.SnapshotDigest, controlSource)})
	} else {
		fields = append(fields, Field{Label: "Snapshot digest", Value: Blocked(
			"this checkpoint predates content digests and cannot be restored safely",
			"create a new checkpoint", controlSource)})
	}
	if record.GitCommit != "" {
		fields = append(fields, Field{Label: "Commit",
			Value: Known(strings.TrimSpace(record.GitCommit), controlSource)})
	} else {
		fields = append(fields, Field{Label: "Commit", Value: Empty(controlSource)})
	}
	if record.WorktreePath != "" {
		fields = append(fields, Field{Label: "Snapshot", Value: Known("present", controlSource)})
	} else {
		fields = append(fields, Field{Label: "Snapshot", Value: Unknown(
			"this record names no snapshot path, so there is nothing to restore",
			controlSource)})
	}
	if record.RestoredAt != nil {
		fields = append(fields, Field{Label: "Restored",
			Value: Known(record.RestoredAt.UTC().Format(time.RFC3339), controlSource)})
	}
	fields = append(fields, Field{Label: "Integrity", Value: Value{
		Text:   "verified again by the canonical checkpoint engine before restore",
		Status: TruthKnown,
		Reason: "the state digest binds checkpoint metadata and the snapshot digest " +
			"binds relative paths, modes, sizes, and file bytes",
		Source: "internal/execution/checkpoint.go",
	}})
	return fields
}

// ConfirmationFields renders the exact request awaiting a decision.
//
// The user sees precisely the typed request that will be submitted: the same
// action, target, revision and digest. Rendering anything else here — a summary,
// a friendlier paraphrase — would break the binding between what was approved
// and what runs.
func ConfirmationFields(c *Confirmation) []Field {
	req := c.Request()
	binding := c.Binding()

	fields := []Field{
		{Label: "Action", Value: Known(
			fmt.Sprintf("%s [%s]", binding.Title, binding.Safety), string(binding.Action))},
		{Label: "Target", Value: Known(req.Target.String(), controlSource)},
	}
	if req.Target.Summary != "" {
		fields = append(fields, Field{Label: "", Value: Known(req.Target.Summary, controlSource)})
	}
	if req.Target.Scope != "" {
		fields = append(fields, Field{Label: "Scope", Value: Known(req.Target.Scope, controlSource)})
	} else {
		fields = append(fields, Field{Label: "Scope", Value: Unknown(
			"no scope was recorded for this target", controlSource)})
	}
	if req.Target.Revision > 0 {
		fields = append(fields, Field{Label: "Revision",
			Value: Known(fmt.Sprintf("%d", req.Target.Revision), controlSource)})
	}
	if req.Target.Digest != "" {
		fields = append(fields, Field{Label: "Digest",
			Value: Known(req.Target.Digest, controlSource)})
	}
	if binding.RequiresApproval {
		if req.ApprovalID != "" {
			fields = append(fields, Field{Label: "Approval",
				Value: Known(req.ApprovalID, controlSource)})
		} else {
			fields = append(fields, Field{Label: "Approval", Value: Blocked(
				"this governed action carries no approval and cannot be submitted",
				"Control / Approvals", controlSource)})
		}
	}
	for _, input := range binding.Inputs {
		value := req.Inputs[input.Key]
		if input.Sensitive {
			fields = append(fields, Field{Label: input.Label,
				Value: Value{Text: "provided", Status: TruthKnown, Redacted: true, Source: string(binding.Action)}})
			continue
		}
		fields = append(fields, Field{Label: input.Label,
			Value: knownOrEmpty(RedactContent(value, nil), string(binding.Action))})
	}
	fields = append(fields, Field{Label: "Idempotency",
		Value: Known(shortDigest(req.IdempotencyKey), controlSource)})
	return fields
}

// ConfirmationWarning is the sentence a destructive confirmation must carry.
func ConfirmationWarning(c *Confirmation) (Value, bool) {
	binding := c.Binding()
	switch binding.Safety {
	case SafetyDestructive:
		return Blocked(
			"This discards or overwrites work and cannot be undone from here. "+
				"Press 'y' to acknowledge, then choose Proceed.",
			binding.Title, string(binding.Action)), true
	case SafetyGoverned:
		return Value{
			Text:   "This action is governed by canonical MARSHAL authority.",
			Status: TruthKnown,
			Reason: "the backend evaluates policy, capability and any required approval at submission; " +
				"nothing here grants authority",
			Source: string(binding.Action),
		}, true
	}
	return Value{}, false
}

// OutcomeFields renders what a canonical authority actually did.
func OutcomeFields(o Outcome) []Field {
	fields := []Field{
		{Label: "Result", Value: verdictValue(o)},
	}
	if o.Detail != "" {
		fields = append(fields, Field{Label: "Detail", Value: Known(o.Detail, controlSource)})
	}
	// The proof is the reread of durable state. Showing it separately from the
	// verdict is the point: a verdict without proof is a claim, not a result.
	fields = append(fields, Field{Label: "Proof", Value: o.Proof})
	if o.Target.ID != "" {
		fields = append(fields, Field{Label: "Target now",
			Value: Known(o.Target.String(), controlSource)})
	}
	if o.Evidence != "" {
		fields = append(fields, Field{Label: "Evidence",
			Value: Known(o.Evidence, controlSource)})
	}
	if o.SecretOnce != "" {
		fields = append(fields, Field{Label: "Plaintext token (shown once)",
			Value: Known(o.SecretOnce, "transient result; not persisted")})
	}
	return fields
}

func verdictValue(o Outcome) Value {
	switch o.Verdict {
	case VerdictPass:
		if !o.Proof.Status.IsSuccess() {
			// Belt and braces: a pass without proof must never render as one.
			return Unknown("the authority accepted the request but durable state "+
				"did not prove the change", controlSource)
		}
		return Known("applied", controlSource)
	case VerdictFail:
		return Errored(o.Detail, controlSource)
	case VerdictBlocked:
		return Blocked(o.Detail, "the screen that owns this", controlSource)
	case VerdictNotRun:
		return NotRun(o.Detail, controlSource)
	}
	return Unknown(o.Detail, controlSource)
}
