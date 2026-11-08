//go:build governed

package cli

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func openProcess05FixtureRuntime(t *testing.T, ctx context.Context, dir string) (*app.Runtime, func(string, func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)), projectid.ID) {
	t.Helper()
	installProcess05FixtureTools(t)
	r, err := app.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	register := func(name string, handler func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)) {
		r.Execution().RegisterHarness(execution.NewMockHarness(name, handler))
	}
	return r, register, projectid.ID(r.ProjectIdentity())
}

func prepareProcess05FixturePlan(req app.CreatePlanRequest) app.CreatePlanRequest { return req }
