package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// Old authority fakes retain their governed fixtures while adopting the new
// inventory contract. Native selectors require explicitly seeded identities.
func (a *fakeAuthority) Sessions(ctx context.Context, provider string) (app.SessionInventory, error) {
	var inv app.SessionInventory
	if provider == "" || provider == "codex" {
		runs, err := a.CodexSessions(ctx)
		if err != nil {
			return inv, err
		}
		for _, r := range runs {
			inv.Governed = append(inv.Governed, model.WorkerRun{ID: r.RunID, SessionID: r.SessionID, TaskID: r.TaskID, Adapter: "codex", Status: r.Status, StartedAt: r.StartedAt})
		}
	}
	if provider == "" || provider == "claude" {
		runs, err := a.ClaudeSessions(ctx)
		if err != nil {
			return inv, err
		}
		for _, r := range runs {
			inv.Governed = append(inv.Governed, model.WorkerRun{ID: r.RunID, SessionID: r.SessionID, TaskID: r.TaskID, Adapter: "claude", Status: r.Status, StartedAt: r.StartedAt})
		}
	}
	return inv, nil
}
func (a *fakeAuthority) ResolveNativeConversation(context.Context, string, string) (app.NativeConversation, error) {
	return app.NativeConversation{}, fmt.Errorf("no native conversation fixture")
}
func (a *fakeAuthority) ExecuteNativeCodexConversation(context.Context, string, string, []string) (string, error) {
	return "", fmt.Errorf("no native fixture")
}
