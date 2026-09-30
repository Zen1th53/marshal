package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestComposerGoalMutationGrammarAndOriginalRequest(t *testing.T) {
	_, runtime := realControlWorkspace(t, "SESSION-goal-grammar")
	ws := NewWorkspace(runtime.Store(), runtime.ProjectIdentity(), "SESSION-goal-grammar")
	ws.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	ctx := context.Background()
	request := "Fix  the parser typo.\tDo not change the API.  "
	if _, err := ws.ExecuteCommand(ctx, "/goal create "+request); err != nil {
		t.Fatal(err)
	}
	g, err := runtime.Store().GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil || g.OriginalRequest != request {
		t.Fatalf("original bytes: %q want %q: %v", g.OriginalRequest, request, err)
	}
	for _, line := range []string{"/goal typo overwrite outcome", "/goal free text", "/goal edit", "/goal constraints extra"} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err != nil || out == "" {
			t.Fatalf("%s: %s %v", line, out, err)
		}
		read, err := runtime.Store().GetActiveGoalContract(ctx, ws.sessionID)
		if err != nil || read.Revision != g.Revision || read.DesiredOutcome != g.DesiredOutcome {
			t.Fatalf("%s mutated: %+v %v", line, read, err)
		}
	}
	if _, err := ws.ExecuteCommand(ctx, "/goal add-constraint Stay offline"); err != nil {
		t.Fatal(err)
	}
	g2, err := runtime.Store().GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil || g2.Revision != 2 || len(g2.Constraints) != 2 || g2.Confirmation != model.ConfirmationPending {
		t.Fatalf("add: %+v %v", g2, err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/goal rm-constraint Stay offline"); err != nil {
		t.Fatal(err)
	}
	g3, err := runtime.Store().GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil || g3.Revision != 3 || len(g3.Constraints) != 1 || g3.OriginalRequest != request {
		t.Fatalf("remove: %+v %v", g3, err)
	}
}
