//go:build !governed

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func marshalRetryFixtureWorkspace(t *testing.T) (*Workspace, *app.Runtime, string) {
	w, _, _, _, goal := nativePlanningFixture(t)
	nativePlanningVerifiedCodex(t, w)
	return w, w.runtime, goal.OriginalRequest
}

func marshalRetryCanonicalDraft(t *testing.T, runtime *app.Runtime, w *Workspace, draft app.MarshalDraft) (app.MarshalDraft, error) {
	return runtime.PrepareNativeMarshalDraft(t.Context(), w.sessionID, draft)
}

func marshalRetryProposal() []byte {
	return []byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}],"instructions":"Preserve the accepted policy","expected_output":"one reviewed policy change"}]}`)
}

func marshalRetryConfigureService(service *app.MarshalService) {
	// Match the owned CLI's --version response to the verified fixture profile.
	service.InstalledVersion = func(context.Context, string) string { return "codex-cli 0.159.2" }
}
