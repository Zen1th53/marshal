package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommandSetModelThroughTheOperatorBoundary(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": newFakeCodexAdapter()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "model:codex", IdempotencyKey: "m1"}

	if _, err := r.CommandSetModel(ctx, e, "codex", "gpt-6-astra"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous: %v", err)
	}
	set, err := r.CommandSetModel(owner, e, "codex", "gpt-6-astra")
	if err != nil || set.Model != "gpt-6-astra" || set.Revision != 1 {
		t.Fatalf("select: %+v %v", set, err)
	}
	if replay, err := r.CommandSetModel(owner, e, "codex", "gpt-6-astra"); err != nil || replay.Revision != 1 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err := r.CommandSetModel(owner, e, "codex", "gpt-5.6-terra"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reuse for another model: %v", err)
	}
	stale := CommandEnvelope{ProjectID: e.ProjectID, SessionID: "s", TargetID: "model:codex", IdempotencyKey: "m2"}
	if _, err := r.CommandSetModel(owner, stale, "codex", "gpt-5.6-terra"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	unknown := CommandEnvelope{ProjectID: e.ProjectID, SessionID: "s", TargetID: "model:codex", ExpectedVersion: 1, IdempotencyKey: "m3"}
	if _, err := r.CommandSetModel(owner, unknown, "codex", "gpt-fake-nonexistent"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("model outside the catalog: %v", err)
	}
	other := CommandEnvelope{ProjectID: e.ProjectID, SessionID: "s", TargetID: "model:opencode", IdempotencyKey: "m4"}
	if _, err := r.CommandSetModel(owner, other, "opencode", "anything"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("adapter that reads no preference: %v", err)
	}
	if rev, current, err := r.ModelPreferenceRevision(ctx, "codex"); err != nil || rev != 1 || current != "gpt-6-astra" {
		t.Fatalf("read-back: %d %q %v", rev, current, err)
	}
}
