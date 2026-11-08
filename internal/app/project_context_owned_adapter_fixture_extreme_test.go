//go:build !governed

package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// Only the adapter transport is simulated. Canonical Plan acceptance, actual
// Runtime-owned provider callback, mandatory context admission and watcher run
// normally; this fixture supplies no Source/coverage/completion authority.
type requiredOwnedFixtureAdapter struct {
	netpolMockAdapter
	provider, required string
	probes, runs       atomic.Int64
	handle             func(context.Context, adapter.Request) (adapter.Result, error)
}

func (a *requiredOwnedFixtureAdapter) Probe(context.Context) (adapter.Probe, error) {
	a.probes.Add(1)
	return adapter.Probe{Name: a.provider, Available: true, Version: "owned-host-adapter-fixture"}, nil
}
func (a *requiredOwnedFixtureAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	a.runs.Add(1)
	if !strings.Contains(req.TrustedContext, a.required) {
		return adapter.Result{}, fmt.Errorf("owned callback omitted full mandatory policy")
	}
	// A launched backend acknowledges launch before any canonical read.
	execution.AcknowledgeWorkerLaunch(ctx)
	if a.handle != nil {
		return a.handle(ctx, req)
	}
	if err := ctx.Err(); err != nil {
		return adapter.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(req.Worktree, "README.md"), []byte("fixed by actual owned host callback\n"), 0600); err != nil {
		return adapter.Result{}, err
	}
	now := time.Now().UTC()
	return adapter.Result{Adapter: a.provider, AdapterVersion: "owned-host-adapter-fixture", Model: req.Model, SessionID: "owned-host-adapter-session", Status: adapter.StatusSuccess, Stdout: []byte("owned host completed"), StartedAt: now, EndedAt: now}, nil
}

func requiredOwnedContextFixture(t *testing.T, client *requiredOwnedFixtureAdapter) (*Runtime, projectcontext.Binding, model.MemoryRecordV2, requiredProjectWorkspaceState) {
	t.Helper()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := OpenWithOptions(t.Context(), repo.Path(), Options{Adapters: map[string]adapter.Adapter{client.provider: client}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	r, b, rec, state := requiredContextFixtureForRuntime(t, r)
	client.required = rec.Body
	return r, b, rec, state
}
