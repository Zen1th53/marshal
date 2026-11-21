package codex

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// effortRunner advertises reasoning levels in its catalog, as codex does.
type effortRunner struct{ capturingRunner }

func (r *effortRunner) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	if len(command.Args) == 2 && command.Args[0] == "debug" && command.Args[1] == "models" {
		return adapter.ProcessResult{Stdout: []byte(`{"models":[{"slug":"gpt-5.6-terra","visibility":"list","is_default":true,` +
			`"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"bad value"}]}]}`)}, nil
	}
	return r.capturingRunner.Run(ctx, command)
}

func TestEffortIsValidatedAndPassedToExec(t *testing.T) {
	runner := &effortRunner{}
	client := New("/usr/bin/codex", runner)
	models, _, err := client.Models(context.Background())
	if err != nil || len(models) != 1 || !slices.Equal(models[0].ReasoningEfforts, []string{"low", "high"}) {
		t.Fatalf("catalog efforts: %+v %v", models, err)
	}
	if err := client.ValidateEffort(context.Background(), "gpt-5.6-terra", "xhigh"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unadvertised effort: %v", err)
	}
	request := adapter.Request{TaskID: "TASK-001", Title: "change", Worktree: "/repo/task", Model: "gpt-5.6-terra", Effort: "high"}
	if _, err := client.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.command.Args, " ")
	if !strings.Contains(joined, `-c model_reasoning_effort="high"`) {
		t.Fatalf("exec args lack the effort override: %s", joined)
	}
	request.Effort = "xhigh"
	if _, err := client.Run(context.Background(), request); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("exec with an unadvertised effort: %v", err)
	}
}
