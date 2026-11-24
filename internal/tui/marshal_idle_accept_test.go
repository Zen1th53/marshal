//go:build linux

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
)

type idleReviewModel struct{ reviews int }

func (m *idleReviewModel) Draft(context.Context, string) (app.MarshalDraft, error) {
	return app.MarshalDraft{}, errors.New("unused")
}
func (m *idleReviewModel) Review(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, error) {
	m.reviews++
	return marshal.Review{}, errors.New("stub review")
}
func (m *idleReviewModel) Amend(context.Context, marshal.Run, string) (app.MarshalDraft, error) {
	return app.MarshalDraft{}, errors.New("unused")
}

func idleAcceptFixture(t *testing.T, name string) (*Workspace, *app.MarshalService, string) {
	t.Helper()
	w, rt := realControlWorkspace(t, name, false)
	service := rt.Marshal()
	runID := "RUN-idle"
	m := w.marshalSession()
	m.mu.Lock()
	m.service, m.runID = service, runID
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-idle", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn, ResultCommit: "result"}}}
	if _, err := service.Store.SetMarshalRun(t.Context(), rt.ProjectID(), runID, run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalTask(t.Context(), runID, run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalHandIn(t.Context(), runID, "T.1", 1, marshal.HandIn{ResultCommit: "result"}); err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel(runID, "codex", run, "review"))
	return w, service, runID
}

func TestIdleAcceptWithModelUsesRunCommandPath(t *testing.T) {
	w, service, runID := idleAcceptFixture(t, "SESSION-idle-accept-model")
	stub := &idleReviewModel{}
	service.Model = stub
	_, _ = w.ExecuteCommand(t.Context(), "/marshal accept T.1")
	if stub.reviews == 0 {
		t.Fatal("idle accept skipped the review gate although a model is available")
	}
	m := w.marshalSession()
	m.mu.Lock()
	leaked := len(m.approvals)
	m.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("idle accept left %d in-memory approvals", leaked)
	}
	record, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
	if err != nil || len(record.Value.Commands) == 0 {
		t.Fatalf("no durable run command recorded: %v", err)
	}
}

func TestIdleAcceptProposalChecksBinding(t *testing.T) {
	for _, binding := range []string{"", "stale-digest"} {
		w, _, runID := idleAcceptFixture(t, "SESSION-idle-accept-binding")
		tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"accept","id":"T.1"}`}}}
		w.observeMarshalProposals(tr)
		batch := w.permissions.queue.Take(false)
		if len(batch) != 1 {
			t.Fatalf("popup=%v", batch)
		}
		req := batch[0]
		req.ApprovalBinding = binding
		err := w.applyMarshalProposal(t.Context(), req)
		if err == nil || strings.Contains(err.Error(), "Approved") {
			t.Fatalf("binding %q accepted: %v", binding, err)
		}
		m := w.marshalSession()
		m.mu.Lock()
		granted := len(m.approvals)
		m.mu.Unlock()
		if granted != 0 {
			t.Fatalf("binding %q granted approval for %s", binding, runID)
		}
	}
}
