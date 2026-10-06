package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// ImportMarshalTask creates a review plan for a CLI result. The operator supplies
// a check because imported tasks carry no acceptance criteria or checks.
// Approval, sandboxed checks, review, integration and close remain unchanged.
func (r *Runtime) ImportMarshalTask(ctx context.Context, s *MarshalService, runID, taskID, check string) (marshal.Run, error) {
	task, err := r.Task(ctx, taskID)
	if err != nil {
		return marshal.Run{}, err
	}
	if task.Status != model.TaskReview || task.ControlState != "" || task.BaseCommit == nil || task.HeadCommit == nil || !marshalCommitPattern.MatchString(*task.BaseCommit) || !marshalCommitPattern.MatchString(*task.HeadCommit) || strings.TrimSpace(check) == "" {
		return marshal.Run{}, errors.New("import requires a finished task in review and an executable acceptance check")
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return marshal.Run{}, err
	}
	runs, err := r.store.SessionInventoryRuns(ctx, project.ID, "")
	if err != nil {
		return marshal.Run{}, err
	}
	provider := ""
	for _, run := range runs {
		if run.TaskID == taskID && run.BaseCommit == *task.BaseCommit && run.ResultCommit == *task.HeadCommit && run.Status == "success" && run.ExitStatus != nil && *run.ExitStatus == 0 {
			provider = run.Adapter
			break
		}
	}
	if provider != "codex" && provider != "claude" {
		return marshal.Run{}, errors.New("import requires a successful governed Codex or Claude CLI run")
	}
	names, err := gitMarshal(ctx, s.Repository, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", *task.BaseCommit, *task.HeadCommit)
	if err != nil {
		return marshal.Run{}, err
	}
	if names == "" {
		return marshal.Run{}, errors.New("imported task has no changes")
	}
	criterion := "Acceptance check passes: " + check
	data, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{"id": taskID, "title": task.Title, "worker": provider, "mode": "governed", "criteria": []string{criterion}, "paths": strings.Split(strings.TrimSuffix(names, "\x00"), "\x00"), "checks": []map[string]any{{"command": check, "criteria": []string{criterion}}}, "instructions": "Review the existing result; do not run the worker again."}}})
	var proposal marshalTaskProposal
	if err := json.Unmarshal(data, &proposal); err != nil {
		return marshal.Run{}, err
	}
	draft, err := (&MarshalCLI{ProjectID: string(s.CanonicalPlanProjectID())}).materialize(proposal, "", 1, []string{provider})
	if err != nil {
		return marshal.Run{}, err
	}
	draft.Tasks[0].ImportedResult = &marshal.ImportedResult{TaskID: taskID, Revision: task.Revision, BaseCommit: *task.BaseCommit, ResultCommit: *task.HeadCommit}
	run, err := s.StartPlanningFromDraft(ctx, runID, task.Title, draft, marshal.Budget{})
	if err != nil {
		return run, err
	}
	// Review the result against its own base; the normal close still refuses a
	// target that cannot fast-forward or has changed concurrently.
	run.BaseCommit = *task.BaseCommit
	run.Tasks[0].BaseCommit = *task.BaseCommit
	if err := s.save(ctx, runID, run, 1); err != nil {
		return marshal.Run{}, err
	}
	return run, nil
}
