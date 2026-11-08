package tui

// Control bindings: Codex doctor, review, skill, model and dispatch actions.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *ControlSource) prepareCodexDoctor(ctx context.Context) (Target, error) {
	if s.Authority == nil {
		return Target{}, ErrNoBinding
	}
	health, err := s.Authority.CodexHealth(ctx)
	if err != nil {
		return Target{}, err
	}
	if !health.Available {
		return Target{}, fmt.Errorf("%w: Codex CLI is unavailable", model.ErrUnavailable)
	}
	return Target{Kind: "codex_doctor", ID: "local", Digest: health.Version, Summary: "bounded native Codex doctor"}, nil
}

func (s *ControlSource) executeCodexDoctor(ctx context.Context, req ActionRequest) (Outcome, error) {
	report, err := s.Authority.CodexDoctor(ctx)
	if err != nil {
		return refusal("Codex doctor did not complete", err, req.Target, "internal/adapter/codex/operations.go")
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("Codex doctor completed: %s (%d checks)", report.OverallStatus, report.CheckCount),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("native doctor status=%s; checks=%d", report.OverallStatus, report.CheckCount), "internal/adapter/codex/operations.go"),
	}, nil
}

func (s *ControlSource) prepareCodexReview(ctx context.Context) (Target, error) {
	if s.Authority == nil {
		return Target{}, ErrNoBinding
	}
	health, err := s.Authority.CodexHealth(ctx)
	if err != nil {
		return Target{}, err
	}
	if !health.Available {
		return Target{}, fmt.Errorf("%w: Codex CLI is unavailable", model.ErrUnavailable)
	}
	return Target{Kind: "codex_review", ID: "current-head", Digest: health.Version, Summary: "exact current commit only"}, nil
}

func (s *ControlSource) executeCodexReview(ctx context.Context, req ActionRequest) (Outcome, error) {
	result, err := s.Authority.RunCodexReview(ctx)
	if err != nil {
		return refusal("Codex commit review failed", err, req.Target, "internal/app/runtime.go")
	}
	return Outcome{Verdict: VerdictPass, Detail: "Codex reviewed current commit " + result.Commit,
		Target: req.Target, Proof: Known("review output digest "+result.OutputDigest, "internal/app/runtime.go")}, nil
}

func (s *ControlSource) prepareCodexSkillInstall(_ context.Context, req ActionRequest) (Target, error) {
	if s.Authority == nil {
		return Target{}, ErrNoBinding
	}
	name := strings.TrimSpace(req.Inputs["skill"])
	digest, err := s.Authority.PreviewCodexSkill(name)
	if err != nil {
		return Target{}, fmt.Errorf("preview project Codex skill: %w", err)
	}
	return Target{Kind: "codex_skill", ID: name, Digest: digest, Summary: "project-local Codex skill " + name}, nil
}

func (s *ControlSource) executeCodexSkillInstall(ctx context.Context, req ActionRequest) (Outcome, error) {
	digest, err := s.Authority.InstallCodexSkill(ctx, req.Target.ID, req.Target.Digest)
	if err != nil {
		return refusal("Codex skill installation refused", err, req.Target, "internal/app/runtime.go")
	}
	return Outcome{Verdict: VerdictPass, Detail: "Codex skill installed: " + req.Target.ID, Target: req.Target, Proof: Known("digest = "+digest, "internal/app/runtime.go")}, nil
}

func (s *ControlSource) prepareCodexModelSelect(ctx context.Context) (Target, error) {
	if s.Authority == nil {
		return Target{}, ErrNoBinding
	}
	current, _ := s.Authority.SelectedCodexModel(ctx)
	revision := int64(0)
	if preference, err := s.Authority.CodexModelPreference(ctx); err == nil {
		current = preference.Model
		revision = preference.Revision
	} else if !errors.Is(err, model.ErrNotFound) {
		return Target{}, fmt.Errorf("read codex model preference: %w", err)
	}
	if current == "" {
		current = "default"
	}
	return Target{
		Kind:     "codex_model",
		ID:       current,
		Summary:  fmt.Sprintf("Current effective model: %s", current),
		Revision: revision,
	}, nil
}

func (s *ControlSource) executeCodexModelSelect(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	modelSlug := strings.TrimSpace(req.Inputs["model"])
	if modelSlug == "" {
		return Outcome{}, errors.New("model slug is required")
	}
	selected, err := s.Authority.CodexSelectModel(ctx, modelSlug, req.Target.Revision)
	if err != nil {
		return refusal("model selection refused", err, req.Target, "internal/adapter/codex")
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("Effective Codex model set to %s", modelSlug),
		Target:  Target{Kind: "codex_model", ID: modelSlug, Summary: modelSlug, Revision: selected.Revision},
		Proof:   Known(fmt.Sprintf("model = %s revision = %d", modelSlug, selected.Revision), "internal/adapter/codex"),
	}, nil
}

func (s *ControlSource) prepareCodexDispatch(ctx context.Context, req ActionRequest) (Target, error) {
	if s.Authority == nil {
		return Target{}, ErrNoBinding
	}
	taskID := strings.TrimSpace(req.Inputs["task_id"])
	if taskID == "" {
		return Target{}, errors.New("task_id is required")
	}
	task, err := s.Authority.Task(ctx, taskID)
	if err != nil {
		return Target{}, fmt.Errorf("task %s: %w", taskID, err)
	}
	agentID := strings.TrimSpace(req.Inputs["agent_id"])
	if agentID == "" {
		if resolver, ok := s.Authority.(interface {
			DefaultCodexDispatchAgent(context.Context) (string, error)
		}); ok {
			agentID, err = resolver.DefaultCodexDispatchAgent(ctx)
			if err != nil {
				return Target{}, err
			}
		} else {
			return Target{}, errors.New("agent_id is required because this authority cannot resolve a unique local Codex agent")
		}
	}
	modelName := strings.TrimSpace(req.Inputs["model"])
	if modelName == "" {
		modelName, _ = s.Authority.SelectedCodexModel(ctx)
	}
	return Target{
		Kind:     "task",
		ID:       task.ID,
		ActorID:  agentID,
		Summary:  fmt.Sprintf("%s (agent=%s model=%s)", task.Title, agentID, modelName),
		Revision: task.Revision,
	}, nil
}

func (s *ControlSource) executeCodexDispatch(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	taskID := strings.TrimSpace(req.Inputs["task_id"])
	agentID := strings.TrimSpace(req.Inputs["agent_id"])
	if agentID == "" {
		agentID = req.Target.ActorID
	}
	modelName := strings.TrimSpace(req.Inputs["model"])
	res, err := s.Authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{
		TaskID:           taskID,
		AgentID:          agentID,
		Model:            modelName,
		ExpectedRevision: req.Target.Revision,
	})
	if err != nil {
		return refusal("codex task dispatch failed", err, req.Target, "internal/adapter/codex")
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("Run %s completed with status %s (session %s, model %s)", res.RunID, res.Status, res.SessionID, res.Model),
		Target:  Target{Kind: "run", ID: res.RunID, Revision: req.Target.Revision},
		Proof:   Known(fmt.Sprintf("run_id = %s, commit = %s", res.RunID, res.ResultCommit), "internal/adapter/codex"),
	}, nil
}

func validateTokenCapabilities(value string) error {
	_, err := auth.ValidateCapabilities(strings.Split(value, ","))
	return err
}

func validateOutcomeStatus(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "failed":
		return nil
	default:
		return errors.New("outcome status must be success or failed")
	}
}
