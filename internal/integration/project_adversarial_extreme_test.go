//go:build !governed

package integration

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/startup"
)

func TestMarshalInitRunsWithoutSandbox(t *testing.T) {
	ctx := context.Background()
	repo := projectRepo(t, "project\n")
	if _, err := app.Bootstrap(ctx, repo); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	assessment := startup.Assess(ctx, startup.NewSystemProber(), startup.Environment{WorkingDir: repo})
	if !assessment.ExecutionPermitted() || !assessment.ControlCenterOpens() {
		for _, check := range assessment.Blocking() {
			if check.ID != "env.sandbox" && check.ID != "env.network" {
				t.Errorf("unexpected blocking check %s: %+v", check.ID, check)
			}
		}
		t.Fatalf("set-up project was not ready without sandbox; blocking checks: %+v", assessment.Blocking())
	}
}
