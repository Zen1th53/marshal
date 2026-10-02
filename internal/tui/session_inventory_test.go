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
)

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
