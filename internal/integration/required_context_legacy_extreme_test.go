//go:build !governed

package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cli"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

type legacyIntegrationObserver struct {
	probes, runs, resumes, collections atomic.Int64
	before                             model.Task
}
type legacyIntegrationAdapterSpy struct {
	adapter.Adapter
	observer *legacyIntegrationObserver
}

func observeLegacyIntegrationAdapter(base adapter.Adapter) (adapter.Adapter, *legacyIntegrationObserver) {
	o := &legacyIntegrationObserver{}
	return &legacyIntegrationAdapterSpy{Adapter: base, observer: o}, o
}
func (a *legacyIntegrationAdapterSpy) Probe(ctx context.Context) (adapter.Probe, error) {
	a.observer.probes.Add(1)
	return a.Adapter.Probe(ctx)
}
func (a *legacyIntegrationAdapterSpy) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	a.observer.runs.Add(1)
	return a.Adapter.Run(ctx, request)
}
func (a *legacyIntegrationAdapterSpy) Resume(ctx context.Context, id string, request adapter.Request) (adapter.Result, error) {
	a.observer.resumes.Add(1)
	return a.Adapter.Resume(ctx, id, request)
}
func (a *legacyIntegrationAdapterSpy) CollectEvidence(result adapter.Result) map[string]any {
	a.observer.collections.Add(1)
	return a.Adapter.CollectEvidence(result)
}

func (o *legacyIntegrationObserver) ObserveTask(t *testing.T, r *app.Runtime, id string) {
	t.Helper()
	before, err := r.Task(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	o.before = before
}
func (o *legacyIntegrationObserver) RequireRunRefusal(t *testing.T, r *app.Runtime, id string, result app.RunResult, err error) bool {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) || !reflect.DeepEqual(result, app.RunResult{}) {
		t.Fatal("legacy REST Run bypassed required context or fabricated execution", err, result)
	}
	o.assertNoExecution(t, r, id)
	return true
}
func (o *legacyIntegrationObserver) RequireCLIRefusal(t *testing.T, r *app.Runtime, root, id string, args []string) bool {
	t.Helper()
	var out, errout bytes.Buffer
	code := cli.Execute(t.Context(), root, args, bytes.NewReader(nil), &out, &errout)
	if code == 0 || !strings.Contains(out.String()+errout.String(), projectcontext.ErrReviewPending.Error()) {
		t.Fatal("actual legacy CLI failed to report required-context refusal", code, out.String(), errout.String())
	}
	o.assertNoExecution(t, r, id)
	return true
}
func (o *legacyIntegrationObserver) assertNoExecution(t *testing.T, r *app.Runtime, id string) {
	t.Helper()
	if o.probes.Load() != 0 || o.runs.Load() != 0 || o.resumes.Load() != 0 || o.collections.Load() != 0 {
		t.Fatal("legacy refusal reached provider", o.probes.Load(), o.runs.Load(), o.resumes.Load(), o.collections.Load())
	}
	task, err := r.Task(t.Context(), id)
	if err != nil || o.before.ID != id || o.before.Status != model.TaskReady || !reflect.DeepEqual(task, o.before) || task.Worktree != nil || task.HeadCommit != nil || task.OwnerAgentID != nil {
		t.Fatal("refused legacy Run claimed or changed task", task, err)
	}
	artifacts, err := r.Artifacts(t.Context())
	if err != nil || len(artifacts) != 0 {
		t.Fatal("refused legacy Run created artifacts", artifacts, err)
	}
	grants, err := r.Store().ListCapabilityGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if string(grant.TaskID) == id {
			t.Fatal("refused legacy Run created task capability grant", grant.ID)
		}
	}
	events, err := r.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.HasPrefix(event.Type, "evidence.node.") || strings.HasPrefix(event.Type, "evidence.edge.") {
			t.Fatal("refused legacy Run persisted execution evidence", event.Type)
		}
	}
	layout, err := project.Discover(r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(layout.Worktrees, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused legacy Run materialized worktree", err)
	}
}
