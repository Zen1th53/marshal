//go:build linux

package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

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
func (a *sweepAgentsEmptyAuthority) Sessions(context.Context, string) (app.SessionInventory, error) {
	if a.fail {
		return app.SessionInventory{}, fmt.Errorf("session storage unavailable")
	}
	return app.SessionInventory{}, nil
}

type seededNativeAuthority struct {
	*fakeAuthority
	root     string
	sourceID string
}

func (a *seededNativeAuthority) ResolveNativeConversation(ctx context.Context, provider, target string) (app.NativeConversation, error) {
	if target != "--last" && target != a.sourceID {
		return app.NativeConversation{}, fmt.Errorf("unknown native conversation")
	}
	return app.NativeConversation{ID: app.NativeConversationID(provider, a.root, a.sourceID), SourceID: a.sourceID, Provider: provider, UpdatedAt: time.Now()}, nil
}
func (a *seededNativeAuthority) ExecuteNativeCodexConversation(ctx context.Context, operation, target string, tail []string) (string, error) {
	if target != app.NativeConversationID("codex", a.root, a.sourceID) {
		return "", fmt.Errorf("unknown native fixture")
	}
	cmd := exec.CommandContext(ctx, "codex", append([]string{"exec", operation, a.sourceID}, tail...)...)
	cmd.Dir = a.root
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func TestSessionInventoryThinProjection(t *testing.T) {
	_, ws, _ := newControlWorkspace(t)
	source, _ := testControl(t)
	ws.AttachControlSource(source)
	out := sweepAgentExecute(t, ws, "/sessions")
	for _, want := range []string{"NATIVE conversations", "GOVERNED runs", "source=worker run", "session-codex-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
}
