//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
)

type sessionRunControlAuthority interface {
	CommandRunControl(context.Context, app.CommandEnvelope, string) (execution.RunControlResult, error)
}

func (a *runtimeControlAuthority) CommandRunControl(ctx context.Context, envelope app.CommandEnvelope, operation string) (execution.RunControlResult, error) {
	if a.runtime == nil || a.localControl == nil {
		return execution.RunControlResult{}, errNoRuntime
	}
	return a.runtime.CommandRunControl(a.localControl.Context(ctx), envelope, operation)
}

// This recovery action resumes the session's exact paused canonical execution
// run. It never recreates a provider-native conversation or replays spent task
// approval receipts. The engine revalidates its existing goal binding.
func configureSessionResumeBinding(source *ControlSource, bindings map[ActionID]Binding) {
	if _, ok := source.Authority.(sessionRunControlAuthority); !ok {
		return
	}
	binding := bindings["CTUI-0073"]
	// Adopting an interrupted workspace's durable recovery context is reached
	// through the same action. A typed run ID selects the paused-run
	// continuation, which stays canonical for runs; a recovery ID selects the
	// workspace context adoption.
	adoption := binding
	binding.Gap = ""
	binding.Title = "Resume interrupted session"
	binding.Safety = SafetyPlain
	binding.Inputs = []InputSpec{{Key: "run_id", Label: "Exact durable workspace recovery ID, or exact paused canonical run ID of this session (native conversations use /resume)", Required: true, Validate: canonicalIDValidator("recovery or run ID")}}
	if adoption.PrepareRequest == nil || adoption.Execute == nil {
		adoption = Binding{}
	}
	binding.PrepareRequest = func(ctx context.Context, req ActionRequest) (Target, error) {
		target, err := source.prepareSessionResume(ctx, req)
		if err == nil || adoption.PrepareRequest == nil {
			return target, err
		}
		// Not a run of this session: treat the same exact ID as a durable
		// workspace recovery context.
		recovery := req
		recovery.Inputs = map[string]string{"recovery_id": strings.TrimSpace(req.Inputs["run_id"])}
		if adopted, adoptErr := adoption.PrepareRequest(ctx, recovery); adoptErr == nil {
			return adopted, nil
		}
		return Target{}, err
	}
	binding.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
		if adoption.Execute != nil && req.Target.Kind == "workspace-recovery" {
			recovery := req
			recovery.Inputs = map[string]string{"recovery_id": strings.TrimSpace(req.Inputs["run_id"])}
			return adoption.Execute(ctx, recovery)
		}
		return source.executeSessionResume(ctx, req)
	}
	bindings[binding.Action] = binding
}

func (s *ControlSource) prepareSessionResume(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	id := strings.TrimSpace(req.Inputs["run_id"])
	if err := validateCanonicalID("recovery or run ID", id); err != nil {
		return Target{}, err
	}
	run, err := s.Authority.GetRun(ctx, id)
	if err != nil {
		return Target{}, err
	}
	if run.RunID != id || string(run.ProjectID) != s.ProjectID || run.SessionID != s.SessionID {
		return Target{}, ErrStaleTarget
	}
	if run.State != execution.RunPaused {
		return Target{}, fmt.Errorf("only the exact paused execution run can resume here; recorded state is %s", run.State)
	}
	goal, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return Target{}, err
	}
	if goal.ID != run.GoalID || goal.Revision != run.GoalRevision || !goal.Confirmation.Settled() || goal.ProjectID != s.ProjectID || goal.SessionID != s.SessionID {
		return Target{}, fmt.Errorf("the run's exact approved goal binding is no longer active; a fresh approved plan is required")
	}
	return Target{Kind: "run", ID: id, Revision: run.Version, Digest: fmt.Sprintf("%s/%d/%s/%d/%s", run.GoalID, run.GoalRevision, run.PlanID, run.PlanVersion, run.State), Scope: fmt.Sprintf("paused execution run in session %s; native conversation is not recreated", s.SessionID), Summary: fmt.Sprintf("resume execution run %s at version %d, goal %s revision %d", id, run.Version, run.GoalID, run.GoalRevision)}, nil
}

func (s *ControlSource) executeSessionResume(ctx context.Context, req ActionRequest) (Outcome, error) {
	authority, ok := s.Authority.(sessionRunControlAuthority)
	if !ok {
		return Outcome{}, ErrNoBinding
	}
	if req.Target.Kind != "run" || req.Target.ID != strings.TrimSpace(req.Inputs["run_id"]) {
		return Outcome{}, ErrStaleTarget
	}
	result, err := authority.CommandRunControl(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: "run:" + req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}, "resume")
	if err != nil {
		return refusal("canonical session run continuation refused", err, req.Target, "internal/app/run_control.go")
	}
	proof, err := s.Authority.GetRun(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, result.Run.RunID, err)
	}
	if result.Run.RunID != req.Target.ID || proof.RunID != req.Target.ID || proof.SessionID != s.SessionID || string(proof.ProjectID) != s.ProjectID || proof.GoalID != result.Run.GoalID || proof.GoalRevision != result.Run.GoalRevision || proof.PlanID != result.Run.PlanID || proof.PlanVersion != result.Run.PlanVersion || proof.Version < result.Run.Version || result.Run.Version <= req.Target.Revision || proof.State == execution.RunPaused {
		return Outcome{Verdict: VerdictUnknown, Detail: "run continuation returned without coherent durable read-back", Target: req.Target, Proof: Unknown("refresh the exact run", "internal/app/run_control.go")}, nil
	}
	// Execution may advance between the recorded command and this read. Report
	// its observed state, not an assumed running process or successful delivery.
	detail := fmt.Sprintf("execution run %s continuation recorded; observed state %s at version %d; native conversation is not recreated and task delivery is not confirmed", proof.RunID, proof.State, proof.Version)
	return Outcome{Verdict: VerdictPass, Detail: detail, Target: Target{Kind: "run", ID: proof.RunID, Revision: proof.Version}, Proof: Known(detail, "internal/app/run_control.go")}, nil
}
