//go:build governed

package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func marshalRetryFixtureWorkspace(t *testing.T) (*Workspace, *app.Runtime, string) {
	w, runtime := realControlWorkspace(t, "SESSION-approve-retry")
	return w, runtime, "fixture"
}

func marshalRetryCanonicalDraft(_ *testing.T, _ *app.Runtime, _ *Workspace, draft app.MarshalDraft) (app.MarshalDraft, error) {
	return draft, nil
}

func marshalRetryProposal() []byte {
	return []byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["fixture checked"],"paths":["README.md"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["fixture checked"]}]}]}`)
}

func marshalRetryConfigureService(*app.MarshalService) {}
