//go:build !governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRequiredContextLegacyRuntimeRunCannotBypassCanonicalDispatch(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-contract", true: "accepted-contract-is-not-execution-authority"}[accepted], func(t *testing.T) {
			var r *Runtime
			if accepted {
				r, _, _, _ = requiredContextFixture(t, true)
			} else {
				r = openNetpolRuntime(t)
			}
			mock := newFakeCodexAdapter()
			r.adapters = map[string]adapter.Adapter{"codex": mock}
			agent, err := r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "Legacy worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			id := "TASK-required-legacy"
			if _, err = r.ImportTasks(t.Context(), []model.Task{{ID: id, Title: "Write an output", Status: model.TaskReady, Risk: model.R1}}); err != nil {
				t.Fatal(err)
			}
			before, err := r.Task(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.Run(t.Context(), RunRequest{TaskID: id, AgentID: agent.ID, Adapter: "codex"})
			if !errors.Is(err, projectcontext.ErrReviewPending) || mock.probes != 0 || mock.runs != 0 || result.RunID != "" {
				t.Fatal("legacy entry point reached provider without qualified context", err, mock.probes, mock.runs)
			}
			after, err := r.Task(t.Context(), id)
			if err != nil || after.Status != before.Status || after.Revision != before.Revision || after.Worktree != nil || after.OwnerAgentID != nil {
				t.Fatal("refused legacy entry point acquired execution authority", err)
			}
		})
	}
}
