//go:build governed

package integration

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/startup"
)

// Watchlist: setting a project up is not the same as it being ready.
func TestAdversarialInitDoesNotImplyReady(t *testing.T) {
	ctx := context.Background()
	repo := projectRepo(t, "project\n")
	if _, err := app.Bootstrap(ctx, repo); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// The project is set up, but readiness still depends on the environment
	// rather than on setup having succeeded.
	assessment := startup.Assess(ctx, startup.NewSystemProber(), startup.Environment{
		WorkingDir: repo, SandboxEnforced: false, NetworkEnforced: false,
	})
	if assessment.ExecutionPermitted() {
		t.Fatal("a set-up project was executable with no sandbox")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("the control center closed for a set-up project")
	}
}
