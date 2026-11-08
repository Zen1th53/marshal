package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

type taskCommandAuthority interface {
	CommandTask(context.Context, app.CommandEnvelope, app.TaskCommand) (model.Task, error)
}

func (a *runtimeControlAuthority) CommandTask(ctx context.Context, e app.CommandEnvelope, input app.TaskCommand) (model.Task, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return model.Task{}, fmt.Errorf("authenticated local task authority is unavailable")
	}
	if e.ProjectID != a.runtime.ProjectIdentity() || e.SessionID != a.sessionID {
		return model.Task{}, fmt.Errorf("%w: task command belongs to another workspace", authz.ErrDenied)
	}
	return a.runtime.CommandTask(a.localControl.Context(ctx), e, input)
}

// executionActionBindings replaces the five task/cell GAP entries at integration.
// Explicit IDs in the form prevent selection from silently falling back to a
// different eligible task when the reviewed task changes state.
func executionActionBindings(s *ControlSource) map[ActionID]Binding {
	bindings := make(map[ActionID]Binding)
	for id, op := range map[ActionID]string{"CTUI-0043": "pause", "CTUI-0044": "resume", "CTUI-0045": "retry"} {
		bindings[id] = taskCommandBinding(s, id, op)
	}
	for id, binding := range cellCommandBindings(s) {
		bindings[id] = binding
	}
	return bindings
}

func taskCommandBinding(s *ControlSource, id ActionID, operation string) Binding {
	return Binding{Action: id, Title: operation + " task", Safety: SafetyGoverned,
		Inputs: []InputSpec{{Key: "task_id", Label: "Exact task ID", Required: true}},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			if s == nil {
				return Target{}, ErrNoBinding
			}
			if _, ok := s.Authority.(taskCommandAuthority); !ok {
				return Target{}, ErrNoBinding
			}
			id := strings.TrimSpace(req.Inputs["task_id"])
			if id == "" {
				return Target{}, fmt.Errorf("select an exact task ID")
			}
			task, err := s.Authority.Task(ctx, id)
			if err != nil {
				return Target{}, err
			}
			if task.ID != id {
				return Target{}, ErrStaleTarget
			}
			return taskTarget(task, "", operation+" task "+id), nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			const source = "internal/app/task_commands.go CommandTask"
			if s == nil {
				return refusal("task authority unavailable", ErrNoBinding, req.Target, source)
			}
			a, ok := s.Authority.(taskCommandAuthority)
			if !ok {
				return refusal("task authority unavailable", ErrNoBinding, req.Target, source)
			}
			if req.Target.Kind != "task" || req.Target.ID == "" || req.ProjectID != s.ProjectID || req.SessionID != s.SessionID {
				return refusal("task target mismatch", ErrStaleTarget, req.Target, source)
			}
			if selected := req.Inputs["task_id"]; selected != "" && selected != req.Target.ID {
				return refusal("task selection changed", ErrStaleTarget, req.Target, source)
			}
			result, err := a.CommandTask(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}, app.TaskCommand{Operation: operation})
			if err != nil {
				return refusal("task command refused", err, req.Target, source)
			}
			proof, err := s.Authority.Task(ctx, req.Target.ID)
			if err != nil {
				return unproven(req.Target, req.Target.ID, err)
			}
			if result.ID != req.Target.ID || result.Revision != req.Target.Revision+1 || result.ID != proof.ID || proof.Revision < result.Revision || proof.Status == model.TaskCancelled || proof.Status == model.TaskMerged || proof.Status == model.TaskSuperseded {
				return unproven(req.Target, req.Target.ID, fmt.Errorf("durable task revision disagrees with command"))
			}
			state := proof.ControlState
			valid := operation == "pause" && (state == "paused" || state == "pause-requested") || operation == "resume" && state == "" || operation == "retry" && state == "" && proof.Attempt >= result.Attempt
			if !valid {
				return unproven(req.Target, req.Target.ID, fmt.Errorf("durable task no longer proves %s", operation))
			}
			detail := fmt.Sprintf("task %s: %s committed at revision %d (attempt %d)", proof.ID, operation, proof.Revision, proof.Attempt)
			if state == "pause-requested" {
				detail = "pause requested; task is settling; provider stop is not yet proved"
			}
			return Outcome{Verdict: VerdictPass, Detail: detail, Target: taskTarget(proof, "", detail), Proof: Known(fmt.Sprintf("control=%s status=%s revision=%d attempt=%d", state, proof.Status, proof.Revision, proof.Attempt), source)}, nil
		},
	}
}
